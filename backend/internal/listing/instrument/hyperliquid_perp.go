package instrument

import (
	"encoding/json"
	"strings"
)

type hyperliquidPerpRaw struct {
	Name        string      `json:"name"`
	MaxLeverage json.Number `json:"maxLeverage"`
	IsDelisted  bool        `json:"isDelisted"`
}

func NormalizeHyperliquidPerp(raw json.RawMessage) (NormalizedInstrument, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var p hyperliquidPerpRaw
	if err := dec.Decode(&p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "hyperliquid", Message: "decode perp: " + err.Error()}
	}
	if p.Name == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "hyperliquid", Message: "missing name"}
	}
	status := "active"
	if p.IsDelisted {
		status = "delisted"
	}
	return NormalizedInstrument{
		Platform:         "hyperliquid",
		MarketType:       "perp",
		APISymbol:        p.Name,
		CanonicalSymbol:  strings.ToUpper(p.Name),
		BaseAsset:        strings.ToUpper(p.Name),
		QuoteAsset:       "USD",
		SettleAsset:      "USDC",
		MarketSurface:    "perp",
		InstrumentKind:   "canonical",
		StatusRaw:        statusOrDelisted(p.IsDelisted),
		StatusNormalized: status,
		StatusFieldName:  "isDelisted",
		DelistFlag:       p.IsDelisted,
		RawJSON:          append(json.RawMessage(nil), raw...),
		RawJSONHash:      computeHash(raw),
	}, nil
}

func statusOrDelisted(isDelisted bool) string {
	if isDelisted {
		return "delisted"
	}
	return "active"
}
