package instrument

import (
	"encoding/json"
	"testing"
)

// lighterSamplePayload mixes one perp (BTC, market_type="perp") and
// one spot (BTC, market_type="spot") so we can prove the surface
// filter does not let one overwrite the other.
const lighterSamplePayload = `{
  "code": "OK",
  "order_book_details": [
    {"symbol": "BTC", "market_id": 1, "market_type": "perp", "status": "active"},
    {"symbol": "ETH", "market_id": 2, "market_type": "perp", "status": "active"},
    {"symbol": "BTC", "market_id": 100, "market_type": "spot", "status": "active"},
    {"symbol": "SOLD", "market_id": 999, "market_type": "perp", "status": "inactive"}
  ]
}`

func TestNormalizeLighterOrderBookDetailPerpSurface(t *testing.T) {
	row := []byte(`{"symbol":"BTC","market_id":1,"market_type":"perp","status":"active"}`)
	got, err := NormalizeLighterOrderBookDetail(row, "perp")
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.Platform != "lighter" || got.MarketType != "perp" || got.MarketSurface != "perp" {
		t.Fatalf("platform/market_type/surface = %q/%q/%q", got.Platform, got.MarketType, got.MarketSurface)
	}
	if got.CanonicalSymbol != "BTC" || got.BaseAsset != "BTC" || got.QuoteAsset != "USDC" {
		t.Fatalf("canonical/base/quote = %+v", got)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status = %q, want active", got.StatusNormalized)
	}
	// Spec F6: market_id must be preserved into raw_json (Top30 backfill
	// looks it up to query candles).
	var checkRaw map[string]any
	if err := json.Unmarshal(got.RawJSON, &checkRaw); err != nil {
		t.Fatalf("raw_json must parse: %v", err)
	}
	if v, ok := checkRaw["market_id"]; !ok || v == nil {
		t.Fatalf("market_id missing from raw_json: %+v", checkRaw)
	}
}

func TestNormalizeLighterOrderBookDetailSpotSurfaceUsesSpotMarketType(t *testing.T) {
	row := []byte(`{"symbol":"BTC","market_id":100,"market_type":"spot","status":"active"}`)
	got, err := NormalizeLighterOrderBookDetail(row, "spot")
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.MarketType != "spot" || got.MarketSurface != "spot" {
		t.Fatalf("market_type/surface = %q/%q, want spot/spot", got.MarketType, got.MarketSurface)
	}
}

func TestNormalizeLighterOrderBookDetailDelisted(t *testing.T) {
	row := []byte(`{"symbol":"OLD","market_id":99,"market_type":"perp","status":"inactive"}`)
	got, err := NormalizeLighterOrderBookDetail(row, "perp")
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.StatusNormalized != "delisted" && got.StatusNormalized != "paused" {
		t.Fatalf("inactive must not be active, got %q", got.StatusNormalized)
	}
}

func TestFilterLighterPayloadBySurfacePreservesBothSides(t *testing.T) {
	// Spec §A.4: BTC perp + BTC spot must each produce one row.
	// Snapshot PK is (platform, market_type, api_symbol) so the two
	// rows do not interfere.
	perps, err := FilterLighterPayloadBySurface([]byte(lighterSamplePayload), "perp")
	if err != nil {
		t.Fatalf("FilterLighterPayloadBySurface perp err = %v", err)
	}
	spots, err := FilterLighterPayloadBySurface([]byte(lighterSamplePayload), "spot")
	if err != nil {
		t.Fatalf("FilterLighterPayloadBySurface spot err = %v", err)
	}
	if len(perps) != 3 {
		// BTC + ETH + SOLD (inactive but still snapshotted)
		t.Fatalf("perp surface should produce 3 normalized rows, got %d", len(perps))
	}
	if len(spots) != 1 {
		t.Fatalf("spot surface should produce 1 normalized row, got %d", len(spots))
	}
	// Both BTC rows must be present and have different market_types.
	var perpBTC, spotBTC *NormalizedInstrument
	for i := range perps {
		if perps[i].CanonicalSymbol == "BTC" {
			perpBTC = &perps[i]
		}
	}
	for i := range spots {
		if spots[i].CanonicalSymbol == "BTC" {
			spotBTC = &spots[i]
		}
	}
	if perpBTC == nil || spotBTC == nil {
		t.Fatalf("BTC must exist on both surfaces; perp=%v spot=%v", perpBTC, spotBTC)
	}
	if perpBTC.MarketType == spotBTC.MarketType {
		t.Fatalf("BTC perp and BTC spot must have distinct market_type; both %q", perpBTC.MarketType)
	}
}

func TestNormalizeLighterOrderBookDetailMissingSymbolFails(t *testing.T) {
	row := []byte(`{"market_id":1,"market_type":"perp"}`)
	_, err := NormalizeLighterOrderBookDetail(row, "perp")
	if err == nil {
		t.Fatalf("expected error on missing symbol")
	}
}
