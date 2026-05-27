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

// extractCanonicalSymbols pulls perp listing symbols from a title.
// The pattern allows 1000PEPE / 100ZBC style prefixes and rejects
// suffixes like USDT / USDC; pre-market, spot, activity, maintenance
// and other non-canonical announcements are filtered upstream.
var canonicalTokenRE = regexp.MustCompile(`\b[0-9]{0,4}[A-Z]{2,20}\b`)

// symbolStopwords are tokens that pass the regex but cannot be
// canonical symbols. The list is conservative: every entry is a real
// false-positive observed when iterating on the parser.
var symbolStopwords = map[string]struct{}{
	"AND": {}, "OR": {}, "THE": {}, "WILL": {}, "BE": {}, "USDT": {}, "USDC": {}, "USD": {},
	"PERPETUAL": {}, "PERP": {}, "CONTRACT": {}, "CONTRACTS": {}, "FUTURES": {}, "FUTURE": {},
	"LISTED": {}, "LISTING": {}, "LAUNCH": {}, "NOTICE": {}, "TRADING": {},
	"PRE": {}, "MARKET": {}, "SPOT": {}, "ACTIVITY": {}, "AIRDROP": {},
}

func extractCanonicalSymbols(title string) []string {
	matches := canonicalTokenRE.FindAllString(strings.ToUpper(title), -1)
	var out []string
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		if _, ok := symbolStopwords[m]; ok {
			continue
		}
		if len(m) < 2 {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
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
	case strings.Contains(t, "perpetual contract") || strings.Contains(t, "perp contract") || strings.Contains(t, "perpetual"):
		return SubtypePerpListing, ConfidenceHigh, true
	}
	return SubtypeIrrelevant, ConfidenceRejected, false
}
