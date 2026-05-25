package collector

import (
	"math"
	"sort"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/marketdata/coingecko"
)

func newFundingTestStore() *Store {
	cfg := config.Config{
		Platforms: []string{"binance", "edgeX"},
		Symbols: []domain.SymbolSub{
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC"},
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC"},
		},
	}
	return NewStore(cfg)
}

func optionalNumber(valid bool, v float64) coingecko.OptionalFlexibleNumber {
	return coingecko.OptionalFlexibleNumber{Valid: valid, Value: v}
}

func mustFloat(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatal("expected non-nil pointer, got nil")
	}
	return *p
}

func TestBuildFundingRowsHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	configured := map[string]struct{}{
		"BTC-USDT (perp)": {},
		"ETH-USDT (perp)": {},
	}
	byPlatformSymbol := map[string]map[string]coingecko.Ticker{
		"binance": {
			"BTC-USDT (perp)": {Market: "Binance (Futures)", Symbol: "BTCUSDT", FundingRate: optionalNumber(true, 0.003164), Volume24H: 12345},
		},
		"edgeX": {
			"BTC-USD (perp)": {Market: "edgeX", Symbol: "BTCUSD", FundingRate: optionalNumber(true, 0.005), Volume24H: 99},
		},
		"hyperliquid": {
			"BTC-USDC (perp)": {Market: "Hyperliquid", Symbol: "BTCUSDC", FundingRate: optionalNumber(true, 0.00125), Volume24H: 50000},
		},
		"lighter": {
			"BTC-USDC (perp)": {Market: "Lighter", Symbol: "BTCUSDC", FundingRate: optionalNumber(false, 0), Volume24H: 1000},
		},
	}

	rows := buildFundingRows(byPlatformSymbol, configured, "https://api.coingecko.com/api/v3/derivatives", now)

	if got := len(rows); got != 4 {
		t.Fatalf("len(rows) = %d, want 4 (one per platform)", got)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Platform < rows[j].Platform })
	binance, edgex, hyperliquid, lighter := rows[0], rows[1], rows[2], rows[3]
	if binance.Platform != "binance" || edgex.Platform != "edgeX" || hyperliquid.Platform != "hyperliquid" || lighter.Platform != "lighter" {
		t.Fatalf("unexpected platform order: %+v", rows)
	}

	// binance: 8h native = 8h normalized, identity
	if binance.Status != domain.StatusComplete {
		t.Fatalf("binance status = %q, want complete", binance.Status)
	}
	if got := mustFloat(t, binance.Rate8h); math.Abs(got-0.003164) > 1e-9 {
		t.Fatalf("binance Rate8h = %v, want 0.003164", got)
	}
	if binance.PeriodHours != 8 {
		t.Fatalf("binance PeriodHours = %d, want 8", binance.PeriodHours)
	}
	if binance.DisplaySymbol != "BTC-USDT (perp)" {
		t.Fatalf("binance DisplaySymbol = %q, want BTC-USDT (perp)", binance.DisplaySymbol)
	}

	// edgeX: 4h native × 2 = 8h; canonical maps USD→USDT
	if edgex.PeriodHours != 4 {
		t.Fatalf("edgeX PeriodHours = %d, want 4", edgex.PeriodHours)
	}
	if got := mustFloat(t, edgex.Rate8h); math.Abs(got-0.010) > 1e-9 {
		t.Fatalf("edgeX Rate8h = %v, want 0.010", got)
	}
	if edgex.DisplaySymbol != "BTC-USDT (perp)" {
		t.Fatalf("edgeX canonical display = %q, want BTC-USDT (perp)", edgex.DisplaySymbol)
	}

	// hyperliquid: 1h native × 8 = 8h
	if hyperliquid.PeriodHours != 1 {
		t.Fatalf("hyperliquid PeriodHours = %d, want 1", hyperliquid.PeriodHours)
	}
	if got := mustFloat(t, hyperliquid.Rate8h); math.Abs(got-0.010) > 1e-9 {
		t.Fatalf("hyperliquid Rate8h = %v, want 0.010", got)
	}

	// lighter: funding was null → status=stale, no Rate8h, no RateNative,
	// but PeriodHours still populated so the UI can render the period
	// label in the tooltip.
	if lighter.Status != domain.StatusStale {
		t.Fatalf("lighter status = %q, want stale", lighter.Status)
	}
	if lighter.PeriodHours != 1 {
		t.Fatalf("lighter PeriodHours = %d, want 1", lighter.PeriodHours)
	}
	if lighter.Rate8h != nil || lighter.RateNative != nil {
		t.Fatalf("lighter should not carry numeric rates: %+v", lighter)
	}

	// Source provenance: every row carries the endpoint and source tag so
	// the operator's debug pane can show "last seen at X via Y".
	for _, r := range rows {
		if r.Source != domain.DataSourceCoinGecko {
			t.Fatalf("Source = %q, want coingecko", r.Source)
		}
		if r.SourceEndpoint == "" {
			t.Fatalf("SourceEndpoint empty for %s", r.Platform)
		}
		if r.SnapshotTS == nil || !r.SnapshotTS.Equal(now) {
			t.Fatalf("SnapshotTS = %v, want %v", r.SnapshotTS, now)
		}
	}
}

func TestBuildFundingRowsSanityRejectsAbsurdRate(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	configured := map[string]struct{}{"BTC-USDT (perp)": {}}

	// 0.1 per 1h on hyperliquid → 0.8 per 8h → sanity reject (|rate| > 0.5)
	bps := map[string]map[string]coingecko.Ticker{
		"hyperliquid": {
			"BTC-USDC (perp)": {Symbol: "BTCUSDC", FundingRate: optionalNumber(true, 0.1), Volume24H: 1},
		},
	}
	rows := buildFundingRows(bps, configured, "endpoint", now)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Status != domain.StatusStale {
		t.Fatalf("status = %q, want stale (sanity reject)", r.Status)
	}
	if r.Rate8h != nil {
		t.Fatalf("Rate8h should be nil when sanity rejected, got %v", *r.Rate8h)
	}
	if r.RateNative == nil || *r.RateNative != 0.1 {
		t.Fatalf("RateNative should retain raw upstream value, got %+v", r.RateNative)
	}
}

func TestBuildFundingRowsPicksHighestVolumeForCanonicalKey(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	configured := map[string]struct{}{"BTC-USDT (perp)": {}}

	// Two CoinGecko entries collapse onto BTC-USDT (perp); deeper book wins.
	bps := map[string]map[string]coingecko.Ticker{
		"bingx": {
			"BTC-USDT (perp)": {Symbol: "BTCUSDT", FundingRate: optionalNumber(true, 0.001), Volume24H: 100},
			"BTC-USDC (perp)": {Symbol: "BTCUSDC", FundingRate: optionalNumber(true, 0.002), Volume24H: 999},
		},
	}
	rows := buildFundingRows(bps, configured, "endpoint", now)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (canonical dedupe)", len(rows))
	}
	if got := mustFloat(t, rows[0].RateNative); math.Abs(got-0.002) > 1e-9 {
		t.Fatalf("RateNative = %v, want 0.002 (highest-volume ticker)", got)
	}
}

func TestBuildFundingRowsRejectsUnknownPlatform(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	configured := map[string]struct{}{"BTC-USDT (perp)": {}}

	bps := map[string]map[string]coingecko.Ticker{
		"drift_exchange": {
			"BTC-USDT (perp)": {Symbol: "BTCUSDT", FundingRate: optionalNumber(true, 0.001), Volume24H: 1},
		},
	}
	rows := buildFundingRows(bps, configured, "endpoint", now)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Status != domain.StatusUnsupported {
		t.Fatalf("status = %q, want unsupported", rows[0].Status)
	}
	if rows[0].PeriodHours != 0 {
		t.Fatalf("PeriodHours = %d, want 0 (unknown period)", rows[0].PeriodHours)
	}
}

func TestBuildFundingRowsIgnoresUnconfiguredSymbols(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	configured := map[string]struct{}{"BTC-USDT (perp)": {}}

	bps := map[string]map[string]coingecko.Ticker{
		"binance": {
			"BTC-USDT (perp)":  {Symbol: "BTCUSDT", FundingRate: optionalNumber(true, 0.001), Volume24H: 1},
			"DOGE-USDT (perp)": {Symbol: "DOGEUSDT", FundingRate: optionalNumber(true, 0.005), Volume24H: 1},
		},
	}
	rows := buildFundingRows(bps, configured, "endpoint", now)
	if len(rows) != 1 || rows[0].DisplaySymbol != "BTC-USDT (perp)" {
		t.Fatalf("expected only BTC row, got %+v", rows)
	}
}

func TestStoreSaveFundingRatesReplaceSemantics(t *testing.T) {
	store := newFundingTestStore()
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	v1 := 0.001
	store.SaveFundingRates([]domain.PlatformFundingRate{{
		Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", PeriodHours: 8, Rate8h: &v1, Status: domain.StatusComplete, SnapshotTS: &now,
	}})
	store.mu.RLock()
	got := store.fundingForLocked("binance", "BTC-USDT (perp)")
	store.mu.RUnlock()
	if got == nil || mustFloat(t, got.Rate8h) != 0.001 {
		t.Fatalf("after first save, got %+v", got)
	}

	v2 := 0.002
	later := now.Add(5 * time.Minute)
	store.SaveFundingRates([]domain.PlatformFundingRate{{
		Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", PeriodHours: 8, Rate8h: &v2, Status: domain.StatusComplete, SnapshotTS: &later,
	}})
	store.mu.RLock()
	got = store.fundingForLocked("binance", "BTC-USDT (perp)")
	store.mu.RUnlock()
	if got == nil || mustFloat(t, got.Rate8h) != 0.002 {
		t.Fatalf("after second save, got %+v", got)
	}
	if got.SnapshotTS == nil || !got.SnapshotTS.Equal(later) {
		t.Fatalf("SnapshotTS = %v, want %v", got.SnapshotTS, later)
	}
}

func TestStoreSaveFundingRatesSkipsEmptyKeys(t *testing.T) {
	store := newFundingTestStore()
	store.SaveFundingRates([]domain.PlatformFundingRate{
		{Platform: "", DisplaySymbol: "BTC-USDT (perp)", Status: domain.StatusComplete},
		{Platform: "binance", DisplaySymbol: "", Status: domain.StatusComplete},
	})
	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.funding) != 0 {
		t.Fatalf("expected no entries written, got %+v", store.funding)
	}
}

func TestStoreFundingForLockedReturnsCopy(t *testing.T) {
	store := newFundingTestStore()
	v := 0.003
	store.SaveFundingRates([]domain.PlatformFundingRate{{
		Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Rate8h: &v, Status: domain.StatusComplete,
	}})
	store.mu.RLock()
	got := store.fundingForLocked("binance", "BTC-USDT (perp)")
	store.mu.RUnlock()
	if got == nil {
		t.Fatal("expected non-nil")
	}
	// Mutating the returned struct must not propagate back into the store.
	got.Status = domain.StatusStale
	store.mu.RLock()
	again := store.fundingForLocked("binance", "BTC-USDT (perp)")
	store.mu.RUnlock()
	if again.Status != domain.StatusComplete {
		t.Fatalf("mutation leaked into store: status = %q", again.Status)
	}
}
