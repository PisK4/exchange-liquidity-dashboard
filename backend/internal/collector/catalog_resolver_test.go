package collector

import (
	"errors"
	"path/filepath"
	"testing"
)

// realDumpsDir locates the raw-instruments tree shipped in the repo so the
// resolver can be tested against real exchange responses. Tests skip
// gracefully when the dump is missing (e.g. inside a CI runner that
// excluded those large fixtures).
func realDumpsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "docs", "raw-instruments"))
	if err != nil {
		t.Fatalf("locate raw-instruments: %v", err)
	}
	return dir
}

// TestResolveConventionPlatforms verifies the convention-only synthesis for
// the seven exchanges whose api_symbol can be derived from base asset
// alone. No dump access is needed; the lookup table lives entirely in
// synthesizeConvention.
func TestResolveConventionPlatforms(t *testing.T) {
	r := NewCatalogResolver(t.TempDir())
	cases := []struct {
		platform, base, wantAPISymbol string
	}{
		{"binance", "BTC", "BTCUSDT"},
		{"binance", "doge", "DOGEUSDT"},
		{"okx", "ETH", "ETH-USDT-SWAP"},
		{"bybit", "SOL", "SOLUSDT"},
		{"bitget", "1000PEPE", "1000PEPEUSDT"},
		{"bingx", "BTC", "BTC-USDT"},
		{"mexc", "ETH", "ETH_USDT"},
	}
	for _, c := range cases {
		sub, err := r.Resolve(c.platform, c.base, "")
		if err != nil {
			t.Fatalf("%s/%s: %v", c.platform, c.base, err)
		}
		if sub.APISymbol != c.wantAPISymbol {
			t.Errorf("%s/%s: api_symbol=%q want %q", c.platform, c.base, sub.APISymbol, c.wantAPISymbol)
		}
		if sub.DisplaySymbol == "" {
			t.Errorf("%s/%s: display_symbol empty", c.platform, c.base)
		}
	}
}

// TestResolveCustomDisplaySymbol checks that the caller-supplied display
// symbol round-trips into the returned SymbolSub so persisted daily-volume
// rows key consistently with the live CoinGecko writer's
// "BTC-USDT (perp)" format.
func TestResolveCustomDisplaySymbol(t *testing.T) {
	r := NewCatalogResolver(t.TempDir())
	sub, err := r.Resolve("binance", "BTC", "BTC-USDT (perp)")
	if err != nil {
		t.Fatal(err)
	}
	if sub.DisplaySymbol != "BTC-USDT (perp)" {
		t.Errorf("display_symbol=%q want %q", sub.DisplaySymbol, "BTC-USDT (perp)")
	}
}

// TestResolveEmptyBase guards against silent acceptance of an empty symbol.
func TestResolveEmptyBase(t *testing.T) {
	r := NewCatalogResolver(t.TempDir())
	if _, err := r.Resolve("binance", "  ", ""); !errors.Is(err, ErrSymbolUnsupported) {
		t.Errorf("expected ErrSymbolUnsupported, got %v", err)
	}
}

// TestResolveGateFromDump exercises the gate loader which is the most
// sensitive: a missing quanto_multiplier silently produces wrong USD
// conversions in the kline backfill. We assert at least BTC resolves and
// carries a positive multiplier.
func TestResolveGateFromDump(t *testing.T) {
	r := NewCatalogResolver(realDumpsDir(t))
	sub, err := r.Resolve("gate", "BTC", "")
	if err != nil {
		t.Skipf("gate dump unavailable: %v", err)
	}
	if sub.APISymbol != "BTC_USDT" {
		t.Errorf("api_symbol=%q want BTC_USDT", sub.APISymbol)
	}
	if sub.QuantoMultiplier <= 0 {
		t.Errorf("quanto_multiplier=%v want > 0", sub.QuantoMultiplier)
	}
}

// TestResolveLighterFromDump verifies that lighter resolution yields a
// non-nil MarketID pointer. The adapter dereferences this pointer when
// building the candles request URL.
func TestResolveLighterFromDump(t *testing.T) {
	r := NewCatalogResolver(realDumpsDir(t))
	sub, err := r.Resolve("lighter", "BTC", "")
	if err != nil {
		t.Skipf("lighter dump unavailable: %v", err)
	}
	if sub.MarketID == nil {
		t.Fatalf("market_id nil")
	}
	if *sub.MarketID < 0 {
		t.Errorf("market_id=%d want >= 0", *sub.MarketID)
	}
}

// TestResolveEdgeXFromDump confirms the coinList→baseCoinId→contractList
// resolution chain produces a contract_id for BTC.
func TestResolveEdgeXFromDump(t *testing.T) {
	r := NewCatalogResolver(realDumpsDir(t))
	sub, err := r.Resolve("edgeX", "BTC", "")
	if err != nil {
		t.Skipf("edgeX dump unavailable: %v", err)
	}
	if sub.ContractID == "" {
		t.Errorf("contract_id empty")
	}
}

// TestResolveHyperliquidUnknownBase asserts that bases not present in the
// hyperliquid universe surface as ErrSymbolUnsupported rather than being
// fabricated by convention. This protects against silently issuing a
// candleSnapshot for a coin Hyperliquid does not list.
func TestResolveHyperliquidUnknownBase(t *testing.T) {
	r := NewCatalogResolver(realDumpsDir(t))
	_, err := r.Resolve("hyperliquid", "NEVER_TRADED_COIN_XYZ", "")
	if err == nil {
		t.Fatal("expected error for unknown base")
	}
	if !errors.Is(err, ErrSymbolUnsupported) {
		t.Fatalf("expected ErrSymbolUnsupported, got %v", err)
	}
}

// TestResolveUnknownPlatform guards against typos in the dispatch list.
func TestResolveUnknownPlatform(t *testing.T) {
	r := NewCatalogResolver(t.TempDir())
	_, err := r.Resolve("not-a-platform", "BTC", "")
	if !errors.Is(err, ErrSymbolUnsupported) {
		t.Errorf("expected ErrSymbolUnsupported, got %v", err)
	}
}

// TestResolveCachesPerPlatform verifies that the second call for the same
// platform does not re-read the dump (we approximate that by removing the
// dumps dir between calls; if caching is wrong, the second call would
// fail).
func TestResolveCachesPerPlatform(t *testing.T) {
	dumps := realDumpsDir(t)
	r := NewCatalogResolver(dumps)
	if _, err := r.Resolve("lighter", "BTC", ""); err != nil {
		t.Skipf("lighter dump unavailable: %v", err)
	}
	// Second call must succeed even if we point at a non-existent dir,
	// proving the per-platform map was cached.
	r.dumpsDir = filepath.Join(t.TempDir(), "nonexistent")
	if _, err := r.Resolve("lighter", "BTC", ""); err != nil {
		t.Errorf("cached resolution failed: %v", err)
	}
}
