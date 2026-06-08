package instrument

import (
	"encoding/json"
	"strconv"
	"strings"
)

type bitgetUSDTFuturesRaw struct {
	Symbol       string          `json:"symbol"`
	BaseCoin     string          `json:"baseCoin"`
	QuoteCoin    string          `json:"quoteCoin"`
	SymbolStatus string          `json:"symbolStatus"`
	OpenTime     string          `json:"openTime"`
	LaunchTime   string          `json:"launchTime"`
	IsRwa        json.RawMessage `json:"isRwa"`
}

func NormalizeBitgetUSDTFutures(raw json.RawMessage) (NormalizedInstrument, error) {
	var p bitgetUSDTFuturesRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bitget", Message: "decode usdt-futures: " + err.Error()}
	}
	if p.Symbol == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bitget", Message: "missing symbol"}
	}
	isRWA, err := parseBitgetIsRWA(p.IsRwa)
	if err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "bitget", Message: "decode isRwa: " + err.Error()}
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
	if isRWA {
		kind = "rwa"
	}
	var openMillis int64
	listingTimeField := "openTime"
	if p.OpenTime != "" {
		if v, err := strconv.ParseInt(p.OpenTime, 10, 64); err == nil {
			openMillis = v
		}
	} else if p.LaunchTime != "" {
		if v, err := strconv.ParseInt(p.LaunchTime, 10, 64); err == nil {
			openMillis = v
			listingTimeField = "launchTime"
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
		ListingTimeFieldName: listingTimeField,
		DelistFlag:           status == "delisted",
		RawJSON:              append(json.RawMessage(nil), raw...),
	}
	n.StableHash = n.ComputeStableHash()
	return n, nil
}

func parseBitgetIsRWA(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "no", "n", "false", "0":
		return false, nil
	case "yes", "y", "true", "1":
		return true, nil
	default:
		return false, &SchemaDriftError{Platform: "bitget", Message: "unsupported isRwa value " + s}
	}
}
