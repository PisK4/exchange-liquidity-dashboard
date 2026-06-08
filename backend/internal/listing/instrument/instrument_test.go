package instrument

import (
	"testing"
)

func TestBinanceUSDMNormalizerActiveBTC(t *testing.T) {
	raw := []byte(`{"symbol":"ABCUSDT","status":"TRADING","baseAsset":"ABC","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000}`)
	got, err := NormalizeBinanceUSDM(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.CanonicalSymbol != "ABC" || got.BaseAsset != "ABC" || got.QuoteAsset != "USDT" {
		t.Fatalf("canonical/base/quote = %+v", got)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status_normalized = %q, want active", got.StatusNormalized)
	}
	if got.MarketSurface != "perp" || got.InstrumentKind != "canonical" {
		t.Fatalf("market_surface/instrument_kind = %q/%q", got.MarketSurface, got.InstrumentKind)
	}
	if got.ListingTimeTS == nil {
		t.Fatalf("listing_time_ts should be set from onboardDate")
	}
	if got.StableHash == "" {
		t.Fatalf("raw_json_hash must be populated")
	}
}

func TestBinanceUSDMTreatsNonPerpetualAsNonCanonical(t *testing.T) {
	raw := []byte(`{"symbol":"BTCUSD_240329","status":"TRADING","baseAsset":"BTC","quoteAsset":"USD","contractType":"CURRENT_QUARTER","onboardDate":0}`)
	got, err := NormalizeBinanceUSDM(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.InstrumentKind == "canonical" {
		t.Fatalf("non-perpetual binance USD-M contract must not be canonical, got %+v", got)
	}
}

func TestBybitLinearNormalizerPreLaunchIsPreListing(t *testing.T) {
	raw := []byte(`{"symbol":"ABCUSDT","status":"PreLaunch","baseCoin":"ABC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"}`)
	got, err := NormalizeBybitLinear(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "pre_listing" {
		t.Fatalf("status_normalized = %q, want pre_listing", got.StatusNormalized)
	}
	if got.CanonicalSymbol != "ABC" {
		t.Fatalf("canonical = %q", got.CanonicalSymbol)
	}
}

func TestOKXSwapNormalizerLive(t *testing.T) {
	raw := []byte(`{"instId":"ABC-USDT-SWAP","state":"live","baseCcy":"ABC","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000"}`)
	got, err := NormalizeOKXSwap(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "active" || got.CanonicalSymbol != "ABC" || got.QuoteAsset != "USDT" {
		t.Fatalf("unexpected normalized: %+v", got)
	}
}

func TestBitgetUSDTFuturesNormalizerActive(t *testing.T) {
	raw := []byte(`{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1893456000000","isRwa":false}`)
	got, err := NormalizeBitgetUSDTFutures(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "active" || got.InstrumentKind != "canonical" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestBitgetUSDTFuturesNormalizerAcceptsStringIsRWA(t *testing.T) {
	raw := []byte(`{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"","launchTime":"1893456000000","isRwa":"NO"}`)
	got, err := NormalizeBitgetUSDTFutures(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.InstrumentKind != "canonical" {
		t.Fatalf("isRwa=NO must stay canonical, got %+v", got)
	}
	if got.ListingTimeTS == nil || got.ListingTimeFieldName != "launchTime" {
		t.Fatalf("launchTime fallback not applied: %+v", got)
	}
}

func TestBitgetUSDTFuturesRWAIsNotCanonical(t *testing.T) {
	raw := []byte(`{"symbol":"TSLAUSDT","baseCoin":"TSLA","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1893456000000","isRwa":"YES"}`)
	got, err := NormalizeBitgetUSDTFutures(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.InstrumentKind == "canonical" {
		t.Fatalf("RWA must not be canonical, got %+v", got)
	}
}

func TestMEXCUnknownStateDoesNotBecomeActive(t *testing.T) {
	raw := []byte(`{"symbol":"ABC_USDT","baseCoin":"ABC","quoteCoin":"USDT","state":99}`)
	got, err := NormalizeMEXCContract(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "unknown" {
		t.Fatalf("status_normalized = %q, want unknown", got.StatusNormalized)
	}
}

func TestMEXCActiveStateMappedToActive(t *testing.T) {
	raw := []byte(`{"symbol":"ABC_USDT","baseCoin":"ABC","quoteCoin":"USDT","state":0,"openingTime":1893456000000}`)
	got, err := NormalizeMEXCContract(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("MEXC state=0 must map to active, got %q", got.StatusNormalized)
	}
}

func TestHyperliquidPerpDelistedFlag(t *testing.T) {
	raw := []byte(`{"name":"ABC","maxLeverage":50,"isDelisted":true}`)
	got, err := NormalizeHyperliquidPerp(raw)
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "delisted" || !got.DelistFlag {
		t.Fatalf("delisted hyperliquid: %+v", got)
	}
}

func TestDiffEmitsNoCandidateOnBaselineRun(t *testing.T) {
	curr := NormalizedInstrument{Platform: "binance", APISymbol: "ABCUSDT", CanonicalSymbol: "ABC", StatusNormalized: "active"}
	got := Diff(nil, curr, false)
	if len(got) != 0 {
		t.Fatalf("baseline run must not emit events, got %+v", got)
	}
}

func TestDiffEmitsNewSymbolWhenBaselineReady(t *testing.T) {
	curr := NormalizedInstrument{Platform: "binance", APISymbol: "ABCUSDT", CanonicalSymbol: "ABC", StatusNormalized: "active"}
	got := Diff(nil, curr, true)
	if len(got) != 1 || got[0].Subtype != "new_symbol" {
		t.Fatalf("want new_symbol, got %+v", got)
	}
}

func TestDiffEmitsStatusChangedOnTransitionToActive(t *testing.T) {
	prev := NormalizedInstrument{StatusNormalized: "pre_listing", StableHash: "h1"}
	curr := NormalizedInstrument{Platform: "binance", APISymbol: "ABCUSDT", CanonicalSymbol: "ABC", StatusNormalized: "active", StableHash: "h2"}
	got := Diff(&prev, curr, true)
	want := false
	for _, e := range got {
		if e.Subtype == "status_changed" {
			want = true
		}
	}
	if !want {
		t.Fatalf("want status_changed event, got %+v", got)
	}
}

func TestDiffEmitsRelistedFromDelistedToActive(t *testing.T) {
	prev := NormalizedInstrument{StatusNormalized: "delisted", StableHash: "h1"}
	curr := NormalizedInstrument{Platform: "binance", APISymbol: "ABCUSDT", CanonicalSymbol: "ABC", StatusNormalized: "active", StableHash: "h2"}
	got := Diff(&prev, curr, true)
	want := false
	for _, e := range got {
		if e.Subtype == "relisted" {
			want = true
		}
	}
	if !want {
		t.Fatalf("want relisted, got %+v", got)
	}
}

func TestDiffEmitsMetadataChangedWhenOnlyHashDiffers(t *testing.T) {
	prev := NormalizedInstrument{StatusNormalized: "active", StableHash: "h1"}
	curr := NormalizedInstrument{Platform: "binance", APISymbol: "ABCUSDT", CanonicalSymbol: "ABC", StatusNormalized: "active", StableHash: "h2"}
	got := Diff(&prev, curr, true)
	if len(got) != 1 || got[0].Subtype != "metadata_changed" {
		t.Fatalf("want only metadata_changed, got %+v", got)
	}
}
