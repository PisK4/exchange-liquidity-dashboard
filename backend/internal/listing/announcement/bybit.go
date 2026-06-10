package announcement

import (
	"encoding/json"
	"time"
)

type bybitAnnouncementRaw struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	URL         string      `json:"url"`
	Category    string      `json:"category"`
	Language    string      `json:"language"`
	PublishTime json.Number `json:"publishTime"`
	UpdateTime  json.Number `json:"updateTime"`
	Description string      `json:"description"`
}

func ParseBybitAnnouncement(raw json.RawMessage) (ParsedAnnouncement, error) {
	var p bybitAnnouncementRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "bybit", Message: "decode announcement: " + err.Error()}
	}
	if p.ID == "" || p.Title == "" {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "bybit", Message: "missing id or title"}
	}
	subtype, confidence, emit := classifyTitle(p.Title)
	out := ParsedAnnouncement{
		Platform:        "bybit",
		AnnouncementID:  p.ID,
		Title:           p.Title,
		URL:             p.URL,
		Category:        p.Category,
		Language:        p.Language,
		Description:     p.Description,
		ParseConfidence: confidence,
		SignalSubtype:   subtype,
		RawPayloadJSON:  append(json.RawMessage(nil), raw...),
		RawPayloadHash:  computeHash(raw),
	}
	if t := parseEpochMillis(p.PublishTime); t != nil {
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

func parseEpochMillis(n json.Number) *time.Time {
	if n == "" {
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		return nil
	}
	if v <= 0 {
		return nil
	}
	t := time.UnixMilli(v).UTC()
	return &t
}
