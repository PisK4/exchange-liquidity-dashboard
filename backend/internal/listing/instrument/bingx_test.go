package instrument

import (
	"testing"
)

func TestNormalizeBingXSpotSymbolActive(t *testing.T) {
	raw := []byte(`{"symbol":"BTC-USDT","status":1,"baseAsset":"BTC","quoteAsset":"USDT"}`)
	got, err := NormalizeBingXSpotSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.Platform != "bingx" || got.MarketType != "spot" {
		t.Fatalf("platform/market_type = %q/%q", got.Platform, got.MarketType)
	}
	if got.CanonicalSymbol != "BTC" || got.BaseAsset != "BTC" || got.QuoteAsset != "USDT" {
		t.Fatalf("canonical/base/quote = %+v", got)
	}
	if got.MarketSurface != "spot" || got.InstrumentKind != "canonical" {
		t.Fatalf("surface/kind = %q/%q", got.MarketSurface, got.InstrumentKind)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status = %q, want active", got.StatusNormalized)
	}
}

func TestNormalizeBingXSpotSymbolHaltedStatusZero(t *testing.T) {
	raw := []byte(`{"symbol":"NEW-USDT","status":0,"baseAsset":"NEW","quoteAsset":"USDT"}`)
	got, err := NormalizeBingXSpotSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.StatusNormalized != "paused" && got.StatusNormalized != "pre_listing" {
		t.Fatalf("status=0 spot must NOT map to active; got %q", got.StatusNormalized)
	}
}

func TestNormalizeBingXSwapSymbolActive(t *testing.T) {
	raw := []byte(`{"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":1893456000000}`)
	got, err := NormalizeBingXSwapSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.Platform != "bingx" || got.MarketType != "swap" || got.MarketSurface != "perp" {
		t.Fatalf("platform/market_type/surface = %q/%q/%q", got.Platform, got.MarketType, got.MarketSurface)
	}
	if got.CanonicalSymbol != "BTC" || got.BaseAsset != "BTC" {
		t.Fatalf("canonical/base = %q/%q", got.CanonicalSymbol, got.BaseAsset)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status = %q, want active", got.StatusNormalized)
	}
	if got.InstrumentKind != "canonical" {
		t.Fatalf("real crypto must be canonical, got %q", got.InstrumentKind)
	}
	if got.ListingTimeTS == nil {
		t.Fatalf("launchTime should populate listing_time_ts")
	}
}

func TestNormalizeBingXSwapSymbolSyntheticNCSK(t *testing.T) {
	// Spec F7: BingX NCSK* / NCCO* synthetic tokens (US-equity / commodity
	// derivatives) MUST be tagged instrument_kind=synthetic so refresh
	// SQL can filter them out of listed_universe.
	raw := []byte(`{"symbol":"NCSKTSLA-USDT","status":1,"asset":"NCSKTSLA","quoteAsset":"USDT"}`)
	got, err := NormalizeBingXSwapSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.InstrumentKind != "synthetic" {
		t.Fatalf("NCSK* must be tagged synthetic, got instrument_kind=%q", got.InstrumentKind)
	}
}

func TestNormalizeBingXSwapSymbolSyntheticNCCO(t *testing.T) {
	raw := []byte(`{"symbol":"NCCOOIL-USDT","status":1,"asset":"NCCOOIL","quoteAsset":"USDT"}`)
	got, err := NormalizeBingXSwapSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.InstrumentKind != "synthetic" {
		t.Fatalf("NCCO* must be tagged synthetic, got instrument_kind=%q", got.InstrumentKind)
	}
}

func TestNormalizeBingXSpotSymbolMissingSymbolFails(t *testing.T) {
	raw := []byte(`{"baseAsset":"BTC","quoteAsset":"USDT"}`)
	_, err := NormalizeBingXSpotSymbol(raw)
	if err == nil {
		t.Fatalf("expected SchemaDriftError on missing symbol")
	}
}

func TestNormalizeBingXSwapSymbolSyntheticFiltersBaseAssetPrefix(t *testing.T) {
	// Make sure the synthetic detection is on baseAsset, not on the
	// symbol field (defensive — some venues prefix the symbol).
	raw := []byte(`{"symbol":"BTC-NCSKUSDT","status":1,"asset":"BTC","quoteAsset":"NCSKUSDT"}`)
	got, err := NormalizeBingXSwapSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.InstrumentKind == "synthetic" {
		t.Fatalf("synthetic detection must scope to base asset, not raw symbol")
	}
}

func TestNormalizeBingXSwapStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "active", raw: `{"symbol":"TEST-USDT","asset":"TEST","quoteAsset":"USDT","status":1}`, want: "active"},
		{name: "pre listing", raw: `{"symbol":"TEST-USDT","asset":"TEST","quoteAsset":"USDT","status":5}`, want: "pre_listing"},
		{name: "delisted", raw: `{"symbol":"TEST-USDT","asset":"TEST","quoteAsset":"USDT","status":25}`, want: "delisted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeBingXSwapSymbol([]byte(tc.raw))
			if err != nil {
				t.Fatalf("normalize err = %v", err)
			}
			if got.StatusNormalized != tc.want {
				t.Fatalf("normalized to %q, want %q", got.StatusNormalized, tc.want)
			}
		})
	}
}

func TestNormalizeBingXSpotStatusTenIsUnknown(t *testing.T) {
	raw := []byte(`{"symbol":"TEST-USDT","baseAsset":"TEST","quoteAsset":"USDT","status":10}`)
	got, err := NormalizeBingXSpotSymbol(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.StatusNormalized != "unknown" {
		t.Fatalf("spot status=10 normalized to %q, want unknown", got.StatusNormalized)
	}
}
