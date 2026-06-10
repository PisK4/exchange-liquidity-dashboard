package announcement

import (
	"encoding/json"
	"strings"
)

type binanceCMSAnnouncementRaw struct {
	// Binance's bapi CMS surface has historically returned `id`
	// either as a JSON string ("123") or as a bare number (123)
	// depending on which catalog (and which generation of the
	// dashboard frontend) the article belongs to. Accept both by
	// declaring the field as json.RawMessage and normalising the
	// string form below.
	ID           json.RawMessage `json:"id"`
	Code         string          `json:"code"`
	Title        string          `json:"title"`
	Body         string          `json:"body"`
	URL          string          `json:"url"`
	CategoryName string          `json:"catalogName"`
	ReleaseDate  json.Number     `json:"releaseDate"`
	UpdateTime   json.Number     `json:"updateTime"`
	Language     string          `json:"language"`
}

func ParseBinanceCMSAnnouncement(raw json.RawMessage) (ParsedAnnouncement, error) {
	var p binanceCMSAnnouncementRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "binance", Message: "decode cms announcement: " + err.Error()}
	}
	id := strings.Trim(strings.TrimSpace(string(p.ID)), `"`)
	if id == "" {
		id = strings.TrimSpace(p.Code)
	}
	if id == "" || strings.TrimSpace(p.Title) == "" {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "binance", Message: "missing id/code or title"}
	}
	subtype, confidence, emit := classifyTitle(p.Title)
	out := ParsedAnnouncement{
		Platform:        "binance",
		AnnouncementID:  id,
		Title:           p.Title,
		URL:             p.URL,
		Category:        p.CategoryName,
		Description:     p.Body,
		Language:        p.Language,
		ParseConfidence: confidence,
		SignalSubtype:   subtype,
		RawPayloadJSON:  append(json.RawMessage(nil), raw...),
		RawPayloadHash:  computeHash(raw),
	}
	if t := parseEpochMillis(p.ReleaseDate); t != nil {
		out.PublishedAt = t
	}
	if t := parseEpochMillis(p.UpdateTime); t != nil {
		out.UpdatedAt = t
	}
	if emit {
		for _, s := range extractCanonicalSymbols(p.Title) {
			out.Symbols = append(out.Symbols, ParsedAnnouncementSymbol{
				CanonicalSymbol: s,
				MarketSurface:   "perp",
				InstrumentKind:  "canonical",
				SignalSubtype:   subtype,
			})
		}
	}
	return out, nil
}
