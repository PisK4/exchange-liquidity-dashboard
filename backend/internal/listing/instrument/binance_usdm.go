package instrument

import (
	"encoding/json"
	"strings"
)

type binanceUSDMRaw struct {
	Symbol       string `json:"symbol"`
	Status       string `json:"status"`
	BaseAsset    string `json:"baseAsset"`
	QuoteAsset   string `json:"quoteAsset"`
	MarginAsset  string `json:"marginAsset"`
	ContractType string `json:"contractType"`
	OnboardDate  int64  `json:"onboardDate"`
}

func NormalizeBinanceUSDM(raw json.RawMessage) (NormalizedInstrument, error) {
	var p binanceUSDMRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "binance", Message: "decode usdm contract: " + err.Error()}
	}
	if p.Symbol == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "binance", Message: "missing symbol"}
	}
	status := "unknown"
	switch p.Status {
	case "TRADING":
		status = "active"
	case "PRE_LAUNCH", "PRE_TRADING":
		status = "pre_listing"
	case "PAUSED", "BREAK", "HALT", "POST_TRADING":
		status = "paused"
	case "SETTLING", "PENDING_TRADING":
		status = "paused"
	case "DELIVERING", "CLOSED", "DELISTED":
		status = "delisted"
	}
	kind := "non_canonical"
	if strings.EqualFold(p.ContractType, "PERPETUAL") {
		kind = "canonical"
	}
	n := NormalizedInstrument{
		Platform:             "binance",
		MarketType:           "usdm_futures",
		APISymbol:            p.Symbol,
		CanonicalSymbol:      canonicalSymbol(p.BaseAsset, p.Symbol),
		BaseAsset:            p.BaseAsset,
		QuoteAsset:           p.QuoteAsset,
		SettleAsset:          firstNonEmpty(p.MarginAsset, p.QuoteAsset),
		MarketSurface:        "perp",
		InstrumentKind:       kind,
		ContractType:         p.ContractType,
		StatusRaw:            p.Status,
		StatusNormalized:     status,
		StatusFieldName:      "status",
		ListingTimeTS:        nowFromUnixMillis(p.OnboardDate),
		ListingTimeFieldName: "onboardDate",
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func canonicalSymbol(base, fallback string) string {
	if base != "" {
		return strings.ToUpper(base)
	}
	return strings.ToUpper(fallback)
}
