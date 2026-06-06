package collector

import (
	"context"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/adapter"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

// TestNextTop30BackfillDelay pins the schedule arithmetic: the next slot
// is always strictly in the future and lands on the configured HH:MM mark.
func TestNextTop30BackfillDelay(t *testing.T) {
	now := time.Date(2026, 5, 22, 1, 30, 0, 0, time.UTC)
	d := nextTop30BackfillDelay(now, 2, 30)
	want := time.Hour
	if d != want {
		t.Fatalf("delay=%s want %s", d, want)
	}
	now2 := time.Date(2026, 5, 22, 5, 0, 0, 0, time.UTC)
	d2 := nextTop30BackfillDelay(now2, 2, 30)
	if d2 < 21*time.Hour || d2 > 22*time.Hour {
		t.Fatalf("delay=%s want roughly 21h30m", d2)
	}
}

// TestRateLimiterSpacing verifies the limiter spaces concurrent callers.
func TestRateLimiterSpacing(t *testing.T) {
	r := newRateLimiter(10) // 100ms interval
	if r == nil {
		t.Fatal("nil limiter")
	}
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := r.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 200*time.Millisecond {
		t.Fatalf("3 calls completed in %s, want at least 200ms", elapsed)
	}
}

// TestDecideFetchDays exercises the gap-detection heuristic. A fresh
// symbol gets the cold-start window; a same-day repair returns the
// daily-repair window; older gaps return gap+1 capped at cold-start.
func TestDecideFetchDays(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)
	bf := &Top30Backfiller{store: store, coldDays: 14, repairDay: 3}

	// Fresh symbol: no in-memory rows, no DB → cold start window.
	if got := bf.decideFetchDays(context.Background(), "binance", "DOGE-USDT (perp)"); got != 14 {
		t.Fatalf("fresh symbol days=%d want 14", got)
	}
	// Today already covered but only one day of history → cold-start
	// window (shallow-history guard prevents gap=0 from short-circuiting).
	today := startOfUTCDay(time.Now().UTC())
	store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
		{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today, Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
	})
	if got := bf.decideFetchDays(context.Background(), "binance", "BTC-USDT (perp)"); got != 14 {
		t.Fatalf("shallow-history days=%d want 14", got)
	}
	// Once 7 days exist, same-day → repair window.
	for i := 1; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	if got := bf.decideFetchDays(context.Background(), "binance", "BTC-USDT (perp)"); got != 3 {
		t.Fatalf("steady-state days=%d want 3", got)
	}
	// 5-day gap with deep history → 6 (gap+1).
	for i := 5; i < 12; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", DisplaySymbol: "ETH-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	if got := bf.decideFetchDays(context.Background(), "binance", "ETH-USDT (perp)"); got != 6 {
		t.Fatalf("5-day gap days=%d want 6", got)
	}
	// Huge gap → capped at cold-start.
	for i := 90; i < 100; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", DisplaySymbol: "SOL-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	if got := bf.decideFetchDays(context.Background(), "binance", "SOL-USDT (perp)"); got != 14 {
		t.Fatalf("90-day gap days=%d want 14 (cap)", got)
	}
}

// fakeFetcher implements adapter.DailyVolumeHistoryFetcher and records the
// (platform, sub.APISymbol, days) tuples it was asked for.
type fakeFetcher struct {
	calls    []fetchCall
	rowsFn   func(sub domain.SymbolSub, days int) []domain.DailyVolumeAggregate
	errOnSym map[string]error
}

type fetchCall struct {
	platform  string
	apiSymbol string
	days      int
}

func (f *fakeFetcher) FetchDailyVolumeHistory(ctx context.Context, sub domain.SymbolSub, days int) ([]domain.DailyVolumeAggregate, error) {
	f.calls = append(f.calls, fetchCall{platform: sub.Platform, apiSymbol: sub.APISymbol, days: days})
	if err := f.errOnSym[sub.APISymbol]; err != nil {
		return nil, err
	}
	if f.rowsFn != nil {
		return f.rowsFn(sub, days), nil
	}
	return nil, nil
}

// adapter.ExchangeAdapter satisfaction is needed for NewTop30Backfiller's
// constructor path; we only exercise FetchDailyVolumeHistory here so we
// inject the fake directly into a hand-built backfiller.
var _ adapter.DailyVolumeHistoryFetcher = (*fakeFetcher)(nil)

// TestRunOnceSkipsUnsupportedAndPersistsRest verifies the orchestration:
// when CatalogResolver returns ErrSymbolUnsupported for a base, the
// backfiller silently skips it; supported bases persist their rows; the
// roster is taken from Store.Top30RosterUnion().
func TestRunOnceSkipsUnsupportedAndPersistsRest(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"binance"}
	store := NewStore(cfg)
	store.SaveTop30("binance", []domain.Top30Row{
		{Rank: 1, Platform: "binance", Symbol: "BTC-USDT (perp)", Volume24HUSD: 1, Status: domain.StatusComplete, SnapshotTS: time.Now().UTC()},
	})
	resolver := NewCatalogResolver(t.TempDir())
	today := startOfUTCDay(time.Now().UTC())
	fetcher := &fakeFetcher{
		rowsFn: func(sub domain.SymbolSub, days int) []domain.DailyVolumeAggregate {
			out := make([]domain.DailyVolumeAggregate, 0, days)
			for i := 0; i < days; i++ {
				out = append(out, domain.DailyVolumeAggregate{
					Platform:      sub.Platform,
					DisplaySymbol: sub.DisplaySymbol,
					Day:           today.AddDate(0, 0, -i),
					Volume24HUSD:  100 * float64(days-i),
					Status:        domain.StatusComplete,
					DataSource:    domain.DataSourceNativeBackfill,
				})
			}
			return out
		},
	}
	bf := &Top30Backfiller{
		cfg:       cfg,
		store:     store,
		resolver:  resolver,
		adapters:  map[string]adapter.ExchangeAdapter{"binance": &adapterStub{fetcher: fetcher}},
		limiters:  map[string]*rateLimiter{"binance": nil},
		perPlatN:  3,
		coldDays:  14,
		repairDay: 3,
	}
	if err := bf.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(fetcher.calls) != 1 {
		t.Fatalf("calls=%d want 1", len(fetcher.calls))
	}
	if fetcher.calls[0].apiSymbol != "BTCUSDT" {
		t.Errorf("apiSymbol=%q want BTCUSDT", fetcher.calls[0].apiSymbol)
	}
	last := store.DailySymbolHistoryLatest("binance", "BTC-USDT (perp)")
	if last.IsZero() {
		t.Errorf("no rows persisted")
	}
}

func TestRunRosterOnceUsesExplicitEntries(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"binance"}
	store := NewStore(cfg)
	resolver := NewCatalogResolver(t.TempDir())
	today := startOfUTCDay(time.Now().UTC())
	fetcher := &fakeFetcher{
		rowsFn: func(sub domain.SymbolSub, days int) []domain.DailyVolumeAggregate {
			return []domain.DailyVolumeAggregate{{
				Platform:      sub.Platform,
				DisplaySymbol: sub.DisplaySymbol,
				Day:           today,
				Volume24HUSD:  100,
				Status:        domain.StatusComplete,
				DataSource:    domain.DataSourceNativeBackfill,
			}}
		},
	}
	bf := &Top30Backfiller{
		cfg:       cfg,
		store:     store,
		resolver:  resolver,
		adapters:  map[string]adapter.ExchangeAdapter{"binance": &adapterStub{fetcher: fetcher}},
		limiters:  map[string]*rateLimiter{"binance": nil},
		perPlatN:  1,
		coldDays:  14,
		repairDay: 3,
	}
	err := bf.RunRosterOnce(context.Background(), map[string][]RosterEntry{
		"binance": []RosterEntry{
			{BaseAsset: "ETH", DisplaySymbol: "ETH-USDT (perp)"},
		},
	})
	if err != nil {
		t.Fatalf("RunRosterOnce: %v", err)
	}
	if len(fetcher.calls) != 1 {
		t.Fatalf("calls=%d want 1", len(fetcher.calls))
	}
	if fetcher.calls[0].apiSymbol != "ETHUSDT" {
		t.Fatalf("apiSymbol=%q want ETHUSDT", fetcher.calls[0].apiSymbol)
	}
	if last := store.DailySymbolHistoryLatest("binance", "ETH-USDT (perp)"); last.IsZero() {
		t.Fatalf("explicit roster row was not persisted")
	}
}

func TestRunOneRecordsBackfillSkipReasons(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)
	resolver := NewCatalogResolver(t.TempDir())
	fetcher := &fakeFetcher{
		errOnSym: map[string]error{"BTCUSDT": adapter.ErrInstrumentNotFound},
	}
	bf := &Top30Backfiller{store: store, resolver: resolver, coldDays: 14, repairDay: 3}

	rows, err := bf.runOne(context.Background(), "binance", "BTC", "BTC-USDT (perp)", fetcher)
	if err != nil {
		t.Fatalf("instrument not found should be a permanent skip, got %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows=%d want 0", rows)
	}
	counts := store.Top30BackfillSkipCounts()
	if counts["binance"]["instrument_not_found"] != 1 {
		t.Fatalf("instrument_not_found skip count missing: %+v", counts)
	}

	rows, err = bf.runOne(context.Background(), "unknown", "BTC", "BTC-USDT (perp)", fetcher)
	if err != nil {
		t.Fatalf("unsupported symbol should be a permanent skip, got %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows=%d want 0", rows)
	}
	counts = store.Top30BackfillSkipCounts()
	if counts["unknown"]["symbol_unsupported"] != 1 {
		t.Fatalf("symbol_unsupported skip count missing: %+v", counts)
	}

	partialFetcher := &fakeFetcher{
		rowsFn: func(sub domain.SymbolSub, days int) []domain.DailyVolumeAggregate {
			return []domain.DailyVolumeAggregate{{
				Platform:      sub.Platform,
				DisplaySymbol: sub.DisplaySymbol,
				Day:           startOfUTCDay(time.Now().UTC()),
				Volume24HUSD:  100,
				Status:        domain.StatusComplete,
				DataSource:    domain.DataSourceNativeBackfill,
			}}
		},
	}
	rows, err = bf.runOne(context.Background(), "binance", "ETH", "ETH-USDT (perp)", partialFetcher)
	if err != nil {
		t.Fatalf("partial days should persist available rows without failing, got %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows=%d want 1", rows)
	}
	counts = store.Top30BackfillSkipCounts()
	if counts["binance"]["partial_days"] != 1 {
		t.Fatalf("partial_days skip count missing: %+v", counts)
	}
}

// TestRunOnceRosterEmpty surfaces the early-return path when SaveTop30
// has not yet been called.
func TestRunOnceRosterEmpty(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)
	bf := &Top30Backfiller{store: store, resolver: NewCatalogResolver(t.TempDir())}
	err := bf.RunOnce(context.Background())
	if err == nil || !containsString(err.Error(), "roster empty") {
		t.Fatalf("expected roster empty error, got %v", err)
	}
}

// TestRunOnceNoResolver guards against a nil resolver (mis-wiring).
func TestRunOnceNoResolver(t *testing.T) {
	bf := &Top30Backfiller{}
	if err := bf.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error for nil resolver")
	}
}

// adapterStub adapts a fakeFetcher into an adapter.ExchangeAdapter for the
// purposes of NewTop30Backfiller; only the DailyVolumeHistoryFetcher
// interface is exercised by tests so the remaining methods are stubbed
// minimally.
type adapterStub struct {
	fetcher *fakeFetcher
}

func (a *adapterStub) Name() string { return "stub" }
func (a *adapterStub) FetchInstruments(ctx context.Context) (adapter.CatalogResult, error) {
	return adapter.CatalogResult{}, nil
}
func (a *adapterStub) FetchOrderBook(ctx context.Context, sub domain.SymbolSub) (domain.OrderBookSnapshot, error) {
	return domain.OrderBookSnapshot{}, nil
}
func (a *adapterStub) FetchTicker(ctx context.Context, sub domain.SymbolSub) (domain.VolumeSnapshot, error) {
	return domain.VolumeSnapshot{}, nil
}
func (a *adapterStub) FetchTop30(ctx context.Context, sub domain.SymbolSub) ([]domain.Top30Row, error) {
	return nil, nil
}
func (a *adapterStub) FetchDailyVolumeHistory(ctx context.Context, sub domain.SymbolSub, days int) ([]domain.DailyVolumeAggregate, error) {
	return a.fetcher.FetchDailyVolumeHistory(ctx, sub, days)
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
