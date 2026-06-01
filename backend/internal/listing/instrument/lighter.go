package instrument

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Lighter /api/v1/orderBookDetails?filter=all returns:
//
//	{"order_book_details": [
//	    {"symbol":"BTC","market_id":1,"market_type":"perp","status":"active"},
//	    {"symbol":"BTC","market_id":100,"market_type":"spot","status":"active"},
//	    ...
//	]}
//
// Spec F6: the listing fetcher splits this into two SOURCES
// (lighter/perp and lighter/spot) which share a single HTTP request
// via the request-level cache. The normalizer here takes a single
// row plus the target surface so it can stamp the appropriate
// market_type / market_surface combo.
type lighterDetailRaw struct {
	Symbol     string `json:"symbol"`
	MarketID   int    `json:"market_id"`
	MarketType string `json:"market_type"`
	Status     string `json:"status"`
}

type lighterEnvelope struct {
	OrderBookDetails []json.RawMessage `json:"order_book_details"`
}

// NormalizeLighterOrderBookDetail turns one orderBookDetails entry
// into a NormalizedInstrument. The caller already filtered by
// surface so the function does NOT silently skip rows whose
// market_type disagrees — it returns an error so wiring bugs in the
// fetcher do not silently leak cross-surface rows into snapshots.
func NormalizeLighterOrderBookDetail(raw json.RawMessage, surface string) (NormalizedInstrument, error) {
	if surface != "perp" && surface != "spot" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "lighter", Message: "unknown surface " + surface}
	}
	var p lighterDetailRaw
	if err := json.Unmarshal(raw, &p); err != nil {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "lighter", Message: "decode detail: " + err.Error()}
	}
	if strings.TrimSpace(p.Symbol) == "" {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "lighter", Message: "missing symbol"}
	}
	if p.MarketType != "" && p.MarketType != surface {
		return NormalizedInstrument{}, &SchemaDriftError{Platform: "lighter", Message: fmt.Sprintf("surface mismatch: want %s, got %s", surface, p.MarketType)}
	}
	base := strings.ToUpper(strings.TrimSpace(p.Symbol))
	status := mapLighterStatus(p.Status)
	return NormalizedInstrument{
		Platform:         "lighter",
		MarketType:       surface,
		APISymbol:        base,
		APIMarketID:      fmt.Sprintf("%d", p.MarketID),
		CanonicalSymbol:  base,
		BaseAsset:        base,
		QuoteAsset:       "USDC",
		SettleAsset:      "USDC",
		MarketSurface:    surface,
		InstrumentKind:   "canonical",
		StatusRaw:        p.Status,
		StatusNormalized: status,
		StatusFieldName:  "status",
		DelistFlag:       status == "delisted",
		RawJSON:          append(json.RawMessage(nil), raw...),
		RawJSONHash:      computeHash(raw),
	}, nil
}

// FilterLighterPayloadBySurface decodes the full orderBookDetails
// payload and emits one NormalizedInstrument per entry whose
// market_type matches surface. Rows with empty market_type are
// treated as belonging to surface (some early responses had the
// field omitted on perps).
func FilterLighterPayloadBySurface(payload []byte, surface string) ([]NormalizedInstrument, error) {
	if surface != "perp" && surface != "spot" {
		return nil, &SchemaDriftError{Platform: "lighter", Message: "unknown surface " + surface}
	}
	var env lighterEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, &SchemaDriftError{Platform: "lighter", Message: "decode envelope: " + err.Error()}
	}
	if len(env.OrderBookDetails) == 0 {
		return nil, nil
	}
	out := make([]NormalizedInstrument, 0, len(env.OrderBookDetails))
	for _, row := range env.OrderBookDetails {
		var hint lighterDetailRaw
		if err := json.Unmarshal(row, &hint); err != nil {
			continue
		}
		if hint.MarketType != "" && hint.MarketType != surface {
			continue
		}
		if hint.MarketType == "" && surface != "perp" {
			// Be conservative: when market_type is absent we default to
			// perp (Lighter's spot is a recent addition).
			continue
		}
		n, err := NormalizeLighterOrderBookDetail(row, surface)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func mapLighterStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "trading", "live":
		return "active"
	case "pre_listing", "prelaunch":
		return "pre_listing"
	case "paused", "halt":
		return "paused"
	case "inactive", "delisted", "suspended":
		return "delisted"
	case "":
		return "active"
	default:
		return "unknown"
	}
}
