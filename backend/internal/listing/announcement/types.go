// Package announcement owns the platform-specific announcement
// parsers and the SchemaDriftError contract used by the announcement
// poller. Parsers are intentionally JSON-only: callers (poller) hand
// over raw payload bytes, parsers produce ParsedAnnouncement and the
// caller persists.
package announcement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const ParserVersion = "v1"

// ParsedAnnouncement is the platform-neutral announcement projection.
type ParsedAnnouncement struct {
	Platform        string
	AnnouncementID  string
	URL             string
	Title           string
	Description     string
	Category        string
	Language        string
	PublishedAt     *time.Time
	UpdatedAt       *time.Time
	ParseConfidence string
	SignalSubtype   string
	RawPayloadJSON  json.RawMessage
	RawPayloadHash  string
	Symbols         []ParsedAnnouncementSymbol
}

// ParsedAnnouncementSymbol is one child row materialized from
// ParsedAnnouncement; multi-symbol announcements produce multiple
// independent rows.
type ParsedAnnouncementSymbol struct {
	CanonicalSymbol string
	DisplaySymbol   string
	MarketSurface   string
	InstrumentKind  string
	SignalSubtype   string
	ListingTimeTS   *time.Time
	SourceModule    string
}

// Parse-confidence labels.
const (
	ConfidenceHigh      = "high"
	ConfidenceMedium    = "medium"
	ConfidenceAuditOnly = "audit_only"
	ConfidenceRejected  = "rejected"
)

// Signal-subtype constants. Mirror domain.go so the two layers stay
// aligned.
const (
	SubtypePerpListing = "perp_listing_announcement"
	SubtypePreMarket   = "pre_market_announcement"
	SubtypeStockPerp   = "stock_perp_announcement"
	SubtypeSpotListing = "spot_listing_announcement"
	SubtypeIrrelevant  = "irrelevant_announcement"
	SubtypeParseFailed = "parse_failed"
)

// Parser skip reason labels surfaced by AnnouncementPollResult. They
// distinguish "source is quiet" from "parser rejected every row" without
// changing the candidate-generation gates.
const (
	SkipReasonAuditOnlySpot      = "audit_only_spot"
	SkipReasonPreMarket          = "pre_market"
	SkipReasonRejectedIrrelevant = "rejected_irrelevant"
	SkipReasonPerpNoSymbol       = "perp_no_symbol"
	SkipReasonAuditOnly          = "audit_only"
)

// SchemaDriftError is returned by parsers when the upstream schema no
// longer matches expectations. The poller increments
// t_listing_source_state.schema_drift_count and may set
// disabled_until once a threshold is reached.
type SchemaDriftError struct {
	Platform string
	Message  string
}

func (e *SchemaDriftError) Error() string {
	if e == nil {
		return ""
	}
	if e.Platform == "" {
		return e.Message
	}
	return e.Platform + ": " + e.Message
}

func computeHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// canonicalTokenRE matches a base symbol that is immediately followed
// by a USDT / USDC / USD quote currency in the title. The base and the
// quote may be glued ("SLXUSDT"), space-separated ("SLX USDT"), dash-
// separated ("SLX-USDT", common in Bitget) or slash-separated
// ("SLX/USDT"). Capture group 1 is the base.
//
// Requiring the quote suffix is what makes this regex robust against
// noise words. Earlier iterations used a bare `\b[A-Z]{2,20}\b`
// pattern guarded by a stopword map, which mis-fired on real-world
// titles such as "New Listing: SLXUSDT Perpetual Contract with up to
// 20x Leverage" — extracting "NEW" (not in stopwords) alongside the
// real symbol "SLXUSDT" (unstripped suffix). Anchoring on the quote
// currency eliminates that entire class of false positives without
// needing an ever-growing stopword list.
var canonicalTokenRE = regexp.MustCompile(`\b([0-9]{0,4}[A-Z]{2,20})[\s\-/]*(?:USDT|USDC|USD)\b`)

// symbolStopwords is now reduced to the quote currencies themselves —
// they still need to be rejected for cases like "USDCUSDT" where the
// regex's capture group could otherwise match a quote currency masque-
// rading as a base.
var symbolStopwords = map[string]struct{}{
	"USDT": {}, "USDC": {}, "USD": {},
}

func extractCanonicalSymbols(title string) []string {
	matches := canonicalTokenRE.FindAllStringSubmatch(strings.ToUpper(title), -1)
	var out []string
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		base := m[1]
		if _, ok := symbolStopwords[base]; ok {
			continue
		}
		if len(base) < 2 {
			continue
		}
		if _, dup := seen[base]; dup {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out
}

// classifyTitle returns (signalSubtype, parseConfidence, emitSymbols).
// Non-canonical announcements (spot, pre-market, activity, etc.) emit
// no child rows so the only audit trail is the parent row.
func classifyTitle(title string) (string, string, bool) {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "spot"):
		return SubtypeSpotListing, ConfidenceAuditOnly, false
	case strings.Contains(t, "pre-market") || strings.Contains(t, "pre market"):
		return SubtypePreMarket, ConfidenceAuditOnly, false
	case looksPerpListingTitle(t):
		return SubtypePerpListing, ConfidenceHigh, true
	}
	return SubtypeIrrelevant, ConfidenceRejected, false
}

func looksPerpListingTitle(lowerTitle string) bool {
	if strings.Contains(lowerTitle, "perpetual contract") || strings.Contains(lowerTitle, "perp contract") || strings.Contains(lowerTitle, "perpetual") {
		return true
	}
	listingIntent := strings.Contains(lowerTitle, "listing") || strings.Contains(lowerTitle, "listed") || strings.Contains(lowerTitle, "will list") || strings.Contains(lowerTitle, "launch")
	if !listingIntent {
		return false
	}
	return strings.Contains(lowerTitle, "futures") || strings.Contains(lowerTitle, "usdt-m") || strings.Contains(lowerTitle, "usdt-margined") || strings.Contains(lowerTitle, "contract trading") || strings.Contains(lowerTitle, "linear contract")
}

// SkipReason returns a stable parser-observability label for parsed
// announcements that emitted no child symbols.
func (p ParsedAnnouncement) SkipReason() string {
	switch p.SignalSubtype {
	case SubtypeSpotListing:
		return SkipReasonAuditOnlySpot
	case SubtypePreMarket:
		return SkipReasonPreMarket
	case SubtypeIrrelevant:
		return SkipReasonRejectedIrrelevant
	}
	if p.ParseConfidence == ConfidenceHigh || p.ParseConfidence == ConfidenceMedium {
		return SkipReasonPerpNoSymbol
	}
	return SkipReasonAuditOnly
}
