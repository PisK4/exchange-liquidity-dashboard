package announcement

import (
	"encoding/json"
)

type bitgetAnnouncementRaw struct {
	AnnounceID    string      `json:"announceId"`
	AnnounceTitle string      `json:"announceTitle"`
	AnnounceURL   string      `json:"announceUrl"`
	Category      string      `json:"category"`
	Language      string      `json:"language"`
	PublishTime   json.Number `json:"publishTime"`
	UpdateTime    json.Number `json:"updateTime"`
}

func ParseBitgetAnnouncement(raw json.RawMessage) (ParsedAnnouncement, error) {
	var p bitgetAnnouncementRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "bitget", Message: "decode announcement: " + err.Error()}
	}
	if p.AnnounceID == "" || p.AnnounceTitle == "" {
		return ParsedAnnouncement{}, &SchemaDriftError{Platform: "bitget", Message: "missing id or title"}
	}
	subtype, confidence, emit := classifyTitle(p.AnnounceTitle)
	out := ParsedAnnouncement{
		Platform:        "bitget",
		AnnouncementID:  p.AnnounceID,
		Title:           p.AnnounceTitle,
		URL:             p.AnnounceURL,
		Category:        p.Category,
		Language:        p.Language,
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
		for _, s := range extractCanonicalSymbols(p.AnnounceTitle) {
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
