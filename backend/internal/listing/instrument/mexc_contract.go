package instrument

import (
	"encoding/json"
	"strings"
)

type mexcContractRaw struct {
	Symbol      string  `json:"symbol"`
	BaseCoin    string  `json:"baseCoin"`
	QuoteCoin   string  `json:"quoteCoin"`
	State       *int    `json:"state"`
	OpeningTime int64   `json:"openingTime"`
	ContractSize float64 `json:"contractSize"`
}

func NormalizeMEXCContract(raw json.RawMessage) (NormalizedInstrument, error) {
	var p mexcContractRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "mexc", Message: "decode contract: " + err.Error()}
	}
	if p.Symbol == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "mexc", Message: "missing symbol"}
	}
	status := "unknown"
	if p.State != nil {
		switch *p.State {
		case 0:
			status = "active"
		case 1:
			status = "pre_listing"
		case 2:
			status = "paused"
		case 3, 4:
			status = "delisted"
		}
	}
	base := p.BaseCoin
	quote := p.QuoteCoin
	if base == "" || quote == "" {
		if parts := strings.Split(p.Symbol, "_"); len(parts) >= 2 {
			if base == "" {
				base = parts[0]
			}
			if quote == "" {
				quote = parts[1]
			}
		}
	}
	stateRaw := ""
	if p.State != nil {
		stateRaw = strings.ToLower(stateNames[*p.State])
		if stateRaw == "" {
			stateRaw = "unknown"
		}
	}
	return NormalizedInstrument{
		Platform:             "mexc",
		MarketType:           "contract",
		APISymbol:            p.Symbol,
		CanonicalSymbol:      canonicalSymbol(base, p.Symbol),
		BaseAsset:            base,
		QuoteAsset:           quote,
		SettleAsset:          quote,
		MarketSurface:        "perp",
		InstrumentKind:       "canonical",
		StatusRaw:            stateRaw,
		StatusNormalized:     status,
		StatusFieldName:      "state",
		ListingTimeTS:        nowFromUnixMillis(p.OpeningTime),
		ListingTimeFieldName: "openingTime",
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
		RawJSONHash:          computeHash(raw),
	}, nil
}

var stateNames = map[int]string{
	0: "active",
	1: "pre_listing",
	2: "paused",
	3: "delisted",
	4: "settled",
}
