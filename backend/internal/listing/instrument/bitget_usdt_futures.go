package instrument

import (
	"encoding/json"
	"strconv"
	"strings"
)

type bitgetUSDTFuturesRaw struct {
	Symbol       string `json:"symbol"`
	BaseCoin     string `json:"baseCoin"`
	QuoteCoin    string `json:"quoteCoin"`
	SymbolStatus string `json:"symbolStatus"`
	OpenTime     string `json:"openTime"`
	IsRwa        bool   `json:"isRwa"`
}

func NormalizeBitgetUSDTFutures(raw json.RawMessage) (NormalizedInstrument, error) {
	var p bitgetUSDTFuturesRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bitget", Message: "decode usdt-futures: " + err.Error()}
	}
	if p.Symbol == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bitget", Message: "missing symbol"}
	}
	status := "unknown"
	switch strings.ToLower(p.SymbolStatus) {
	case "normal":
		status = "active"
	case "listed":
		status = "pre_listing"
	case "halt", "suspend", "limit_open":
		status = "paused"
	case "off", "offline", "delisted":
		status = "delisted"
	}
	kind := "canonical"
	if p.IsRwa {
		kind = "rwa"
	}
	var openMillis int64
	if p.OpenTime != "" {
		if v, err := strconv.ParseInt(p.OpenTime, 10, 64); err == nil {
			openMillis = v
		}
	}
	n := NormalizedInstrument{
		Platform:             "bitget",
		MarketType:           "usdt_futures",
		APISymbol:            p.Symbol,
		CanonicalSymbol:      canonicalSymbol(p.BaseCoin, p.Symbol),
		BaseAsset:            p.BaseCoin,
		QuoteAsset:           p.QuoteCoin,
		SettleAsset:          firstNonEmpty(p.QuoteCoin, "USDT"),
		MarketSurface:        "perp",
		InstrumentKind:       kind,
		StatusRaw:            p.SymbolStatus,
		StatusNormalized:     status,
		StatusFieldName:      "symbolStatus",
		ListingTimeTS:        nowFromUnixMillis(openMillis),
		ListingTimeFieldName: "openTime",
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}
