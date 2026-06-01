package instrument

import (
	"encoding/json"
	"strings"
)

// Gate spot endpoint /api/v4/spot/currency_pairs returns rows like:
//
//	{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable"}
type gateSpotRaw struct {
	ID          string `json:"id"`
	Base        string `json:"base"`
	Quote       string `json:"quote"`
	TradeStatus string `json:"trade_status"`
}

// Gate USDT futures endpoint /api/v4/futures/usdt/contracts returns
// rows whose name field always uses the underscore convention
// (BASE_USDT). quanto_multiplier is a stringified float — Top30
// backfill needs it for USD value conversion.
type gateFuturesRaw struct {
	Name             string `json:"name"`
	QuantoMultiplier string `json:"quanto_multiplier"`
	InDelisting      bool   `json:"in_delisting"`
}

// NormalizeGateSpotPair turns one /spot/currency_pairs row into a
// NormalizedInstrument. trade_status semantics from the Gate docs:
//
//	tradable             → active
//	untradable / suspend → paused
//	delisted             → delisted
func NormalizeGateSpotPair(raw json.RawMessage) (NormalizedInstrument, error) {
	var p gateSpotRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "gate", Message: "decode spot pair: " + err.Error()}
	}
	if strings.TrimSpace(p.ID) == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "gate", Message: "missing id"}
	}
	base := strings.ToUpper(strings.TrimSpace(p.Base))
	quote := strings.ToUpper(strings.TrimSpace(p.Quote))
	status := "unknown"
	switch strings.ToLower(strings.TrimSpace(p.TradeStatus)) {
	case "tradable":
		status = "active"
	case "untradable", "suspend", "suspended":
		status = "paused"
	case "delisted":
		status = "delisted"
	}
	return NormalizedInstrument{
		Platform:         "gate",
		MarketType:       "spot",
		APISymbol:        p.ID,
		CanonicalSymbol:  canonicalSymbol(base, p.ID),
		BaseAsset:        base,
		QuoteAsset:       quote,
		SettleAsset:      quote,
		MarketSurface:    "spot",
		InstrumentKind:   "canonical",
		StatusRaw:        p.TradeStatus,
		StatusNormalized: status,
		StatusFieldName:  "trade_status",
		DelistFlag:       status == "delisted",
		RawJSON:          append(json.RawMessage(nil), raw...),
		RawJSONHash:      computeHash(raw),
	}, nil
}

// NormalizeGateFuturesContract turns one
// /api/v4/futures/usdt/contracts row into a NormalizedInstrument.
// quanto_multiplier is intentionally NOT promoted to a struct field —
// it stays inside RawJSON so the CatalogResolver DB-first path reads
// it directly from t_listing_instrument_snapshot.raw_json without
// schema changes.
func NormalizeGateFuturesContract(raw json.RawMessage) (NormalizedInstrument, error) {
	var p gateFuturesRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "gate", Message: "decode futures contract: " + err.Error()}
	}
	if strings.TrimSpace(p.Name) == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "gate", Message: "missing name"}
	}
	upperName := strings.ToUpper(p.Name)
	if !strings.HasSuffix(upperName, "_USDT") {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "gate", Message: "expected _USDT suffix on usdt_futures contract: " + p.Name}
	}
	base := strings.TrimSuffix(upperName, "_USDT")
	status := "active"
	if p.InDelisting {
		status = "delisted"
	}
	return NormalizedInstrument{
		Platform:         "gate",
		MarketType:       "usdt_futures",
		APISymbol:        p.Name,
		CanonicalSymbol:  base,
		BaseAsset:        base,
		QuoteAsset:       "USDT",
		SettleAsset:      "USDT",
		MarketSurface:    "perp",
		InstrumentKind:   "canonical",
		StatusRaw:        gateFuturesStatusRaw(p.InDelisting),
		StatusNormalized: status,
		StatusFieldName:  "in_delisting",
		DelistFlag:       p.InDelisting,
		RawJSON:          append(json.RawMessage(nil), raw...),
		RawJSONHash:      computeHash(raw),
	}, nil
}

func gateFuturesStatusRaw(inDelisting bool) string {
	if inDelisting {
		return "in_delisting"
	}
	return "active"
}
