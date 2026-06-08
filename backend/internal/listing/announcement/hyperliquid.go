package announcement

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const hyperliquidSourceModule = "hyperliquid_entries"

type hyperliquidAnnouncementRaw struct {
	ID           string `json:"id"`
	UUID         string `json:"uuid"`
	Hash         string `json:"hash"`
	Title        string `json:"title"`
	Preview      string `json:"preview"`
	Category     string `json:"category"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	SourceURL    string `json:"sourceUrl"`
	SourceModule string `json:"sourceModule"`
}

type quotedSymbol struct {
	base  string
	quote string
}

var hyperliquidQuotedSymbolRE = regexp.MustCompile(`\b([0-9A-Z]{2,20})[\s\-/]*(USDC|USD)\b`)

var hyperliquidTokenStopwords = map[string]struct{}{
	"ADDED": {}, "AND": {}, "DELIST": {}, "ENABLED": {}, "FOR": {}, "HYPERP": {},
	"HYPERPS": {}, "LISTING": {}, "NEW": {}, "PERP": {}, "PERPS": {}, "REGULAR": {},
	"SPOT": {}, "THE": {}, "TO": {}, "USD": {}, "USDC": {}, "USDT": {}, "VOTE": {},
}

// ParseHyperliquidAnnouncement parses Hyperliquid's entries feed rows.
// The feed is more changelog-like than CEX CMS APIs, so only explicit
// high-confidence listing titles emit symbols. Delistings and generic
// updates are retained as parent audit rows with no child signals.
func ParseHyperliquidAnnouncement(raw json.RawMessage) (ParsedAnnouncement, error) {
	var p hyperliquidAnnouncementRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "hyperliquid", Message: "decode announcement: " + err.Error()}
	}
	id := firstNonEmpty(p.ID, p.UUID, p.Hash)
	if id == "" || strings.TrimSpace(p.Title) == "" {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "hyperliquid", Message: "missing id/hash/uuid or title"}
	}
	sourceModule := strings.TrimSpace(p.SourceModule)
	if sourceModule == "" {
		sourceModule = hyperliquidSourceModule
	}
	out := ParsedAnnouncement{
		Platform:        "hyperliquid",
		AnnouncementID:  id,
		Title:           p.Title,
		URL:             p.SourceURL,
		Category:        p.Category,
		Description:     p.Preview,
		ParseConfidence: ConfidenceRejected,
		SignalSubtype:   SubtypeIrrelevant,
		RawPayloadJSON:  append(json.RawMessage(nil), raw...),
		RawPayloadHash:  computeHash(raw),
	}
	if t := parseRFC3339(p.CreatedAt); t != nil {
		out.PublishedAt = t
	}
	if t := parseRFC3339(p.UpdatedAt); t != nil {
		out.UpdatedAt = t
	}

	subtype, confidence, symbols := parseHyperliquidTitle(p.Title, p.Category)
	out.SignalSubtype = subtype
	out.ParseConfidence = confidence
	for _, sym := range symbols {
		out.Symbols = append(out.Symbols, sym.toParsed(out.PublishedAt, subtype, sourceModule))
	}
	return out, nil
}

func parseHyperliquidTitle(title, category string) (string, string, []quotedSymbol) {
	lowerTitle := strings.ToLower(title)
	lowerCategory := strings.ToLower(category)
	if strings.Contains(lowerTitle, "delist") || strings.Contains(lowerCategory, "delist") {
		return SubtypeIrrelevant, ConfidenceAuditOnly, nil
	}
	if strings.Contains(lowerTitle, "spot") {
		spots := extractHyperliquidSpotSymbols(title)
		if len(spots) == 0 {
			return SubtypeSpotListing, ConfidenceAuditOnly, nil
		}
		return SubtypeSpotListing, ConfidenceMedium, spots
	}
	if strings.Contains(lowerTitle, "new listing:") && (strings.Contains(lowerTitle, "perps") || strings.Contains(lowerTitle, "hyperps")) {
		perps := extractHyperliquidQuotedSymbols(title)
		if len(perps) == 0 {
			return SubtypePerpListing, ConfidenceHigh, nil
		}
		return SubtypePerpListing, ConfidenceHigh, perps
	}
	return SubtypeIrrelevant, ConfidenceRejected, nil
}

func extractHyperliquidQuotedSymbols(title string) []quotedSymbol {
	matches := hyperliquidQuotedSymbolRE.FindAllStringSubmatch(strings.ToUpper(title), -1)
	out := make([]quotedSymbol, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		base := strings.TrimSpace(m[1])
		quote := strings.TrimSpace(m[2])
		if base == "" || quote == "" || isHyperliquidStopword(base) {
			continue
		}
		key := base + "|" + quote
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, quotedSymbol{base: base, quote: quote})
	}
	return out
}

func extractHyperliquidSpotSymbols(title string) []quotedSymbol {
	upper := strings.ToUpper(title)
	idx := strings.Index(upper, "SPOT")
	if idx >= 0 {
		upper = upper[idx+len("SPOT"):]
	}
	fields := regexp.MustCompile(`[A-Z0-9]+`).FindAllString(upper, -1)
	out := make([]quotedSymbol, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, token := range fields {
		if len(token) < 2 || isHyperliquidStopword(token) {
			continue
		}
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, quotedSymbol{base: token, quote: ""})
	}
	return out
}

func (s quotedSymbol) toParsed(listingTime *time.Time, subtype, sourceModule string) ParsedAnnouncementSymbol {
	surface := "perp"
	display := s.base + "-" + s.quote + " (perp)"
	if subtype == SubtypeSpotListing {
		surface = "spot"
		display = s.base + " (spot)"
	}
	return ParsedAnnouncementSymbol{
		CanonicalSymbol: s.base,
		DisplaySymbol:   display,
		MarketSurface:   surface,
		InstrumentKind:  "canonical",
		SignalSubtype:   subtype,
		ListingTimeTS:   listingTime,
		SourceModule:    sourceModule,
	}
}

func isHyperliquidStopword(token string) bool {
	_, ok := hyperliquidTokenStopwords[token]
	return ok
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseRFC3339(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}
