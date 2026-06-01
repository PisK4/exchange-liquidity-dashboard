package instrument

import (
	"encoding/json"
	"strconv"
	"strings"
)

type bybitLinearRaw struct {
	Symbol       string `json:"symbol"`
	Status       string `json:"status"`
	BaseCoin     string `json:"baseCoin"`
	QuoteCoin    string `json:"quoteCoin"`
	SettleCoin   string `json:"settleCoin"`
	ContractType string `json:"contractType"`
	LaunchTime   string `json:"launchTime"`
}

func NormalizeBybitLinear(raw json.RawMessage) (NormalizedInstrument, error) {
	var p bybitLinearRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bybit", Message: "decode linear: " + err.Error()}
	}
	if p.Symbol == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bybit", Message: "missing symbol"}
	}
	status := "unknown"
	switch p.Status {
	case "Trading":
		status = "active"
	case "PreLaunch", "PreLaunching":
		status = "pre_listing"
	case "Settling", "Closed", "Delivering":
		status = "paused"
	case "Delisted":
		status = "delisted"
	}
	kind := "non_canonical"
	if strings.EqualFold(p.ContractType, "LinearPerpetual") {
		kind = "canonical"
	}
	var launchMillis int64
	if p.LaunchTime != "" {
		if v, err := strconv.ParseInt(p.LaunchTime, 10, 64); err == nil {
			launchMillis = v
		}
	}
	n := NormalizedInstrument{
		Platform:             "bybit",
		MarketType:           "linear_futures",
		APISymbol:            p.Symbol,
		CanonicalSymbol:      canonicalSymbol(p.BaseCoin, p.Symbol),
		BaseAsset:            p.BaseCoin,
		QuoteAsset:           p.QuoteCoin,
		SettleAsset:          firstNonEmpty(p.SettleCoin, p.QuoteCoin),
		MarketSurface:        "perp",
		InstrumentKind:       kind,
		ContractType:         p.ContractType,
		StatusRaw:            p.Status,
		StatusNormalized:     status,
		StatusFieldName:      "status",
		ListingTimeTS:        nowFromUnixMillis(launchMillis),
		ListingTimeFieldName: "launchTime",
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}
