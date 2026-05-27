package instrument

import (
	"encoding/json"
	"strconv"
	"strings"
)

type okxSwapRaw struct {
	InstID    string `json:"instId"`
	State     string `json:"state"`
	BaseCcy   string `json:"baseCcy"`
	QuoteCcy  string `json:"quoteCcy"`
	SettleCcy string `json:"settleCcy"`
	CtType    string `json:"ctType"`
	ListTime  string `json:"listTime"`
}

func NormalizeOKXSwap(raw json.RawMessage) (NormalizedInstrument, error) {
	var p okxSwapRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "okx", Message: "decode swap: " + err.Error()}
	}
	if p.InstID == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "okx", Message: "missing instId"}
	}
	status := "unknown"
	switch p.State {
	case "live":
		status = "active"
	case "preopen":
		status = "pre_listing"
	case "suspend":
		status = "paused"
	case "expired", "settlement":
		status = "delisted"
	}
	base := p.BaseCcy
	quote := p.QuoteCcy
	if base == "" || quote == "" {
		if parts := strings.Split(p.InstID, "-"); len(parts) >= 2 {
			if base == "" {
				base = parts[0]
			}
			if quote == "" {
				quote = parts[1]
			}
		}
	}
	kind := "non_canonical"
	if strings.EqualFold(p.CtType, "linear") && strings.HasSuffix(strings.ToUpper(p.InstID), "-SWAP") {
		kind = "canonical"
	}
	var listMillis int64
	if p.ListTime != "" {
		if v, err := strconv.ParseInt(p.ListTime, 10, 64); err == nil {
			listMillis = v
		}
	}
	return NormalizedInstrument{
		Platform:             "okx",
		MarketType:           "swap",
		APISymbol:            p.InstID,
		CanonicalSymbol:      canonicalSymbol(base, p.InstID),
		BaseAsset:            base,
		QuoteAsset:           quote,
		SettleAsset:          firstNonEmpty(p.SettleCcy, quote),
		MarketSurface:        "perp",
		InstrumentKind:       kind,
		ContractType:         p.CtType,
		StatusRaw:            p.State,
		StatusNormalized:     status,
		StatusFieldName:      "state",
		ListingTimeTS:        nowFromUnixMillis(listMillis),
		ListingTimeFieldName: "listTime",
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
		RawJSONHash:          computeHash(raw),
	}, nil
}
