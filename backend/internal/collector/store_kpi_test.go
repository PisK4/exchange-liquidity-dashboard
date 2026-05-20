package collector

import (
	"math"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

// TestSymbolShare7dInsufficientHistory pins the V1 fallback: when no
// per-symbol daily aggregates exist the KPI must remain insufficient_history,
// the numeric must be suppressed from the kpis map, and the WoW status must
// stay insufficient as well so the front-end keeps the badge in degraded
// state.
func TestSymbolShare7dInsufficientHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	share, status7d, statusWoW := store.symbolShare7dLocked("BTC-USDT (perp)")
	if share != 0 || status7d != domain.StatusInsufficientHistory || statusWoW != domain.StatusInsufficientHistory {
		t.Fatalf("expected zero share + insufficient statuses, got share=%v 7d=%s wow=%s", share, status7d, statusWoW)
	}
	rows := store.Liquidity("BTC-USDT (perp)")["kpis"].(map[string]any)
	if _, ok := rows["symbol_share_7d_pct"]; ok {
		t.Fatalf("symbol_share_7d_pct must be omitted when insufficient_history, got %+v", rows)
	}
}

// TestSymbolShare7dPartialWindow seeds 3 of 7 days on each platform and
// expects partial + a positive share number (best-effort).
func TestSymbolShare7dPartialWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 3; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		})
	}
	share, status7d, _ := store.symbolShare7dLocked("BTC-USDT (perp)")
	if status7d != domain.StatusPartial {
		t.Fatalf("expected partial, got %s", status7d)
	}
	want := 100 * 3 / float64(100*3+1000*3) * 100 // 9.0909%
	if math.Abs(share-want) > 0.01 {
		t.Fatalf("expected share≈%v, got %v", want, share)
	}
}

// TestSymbolShare7dCompleteWindow seeds 7 days on every platform and
// expects status=complete with the precise edgeX adj/denom share. MEXC
// discount is verified by spiking a MEXC entry with a 0.4 multiplier.
func TestSymbolShare7dCompleteWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "mexc"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
			{Platform: "mexc", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 500, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		})
	}
	share, status7d, _ := store.symbolShare7dLocked("BTC-USDT (perp)")
	if status7d != domain.StatusComplete {
		t.Fatalf("expected complete, got %s", status7d)
	}
	// Adjusted denominator = 7*100 (edgeX) + 7*1000 (binance) + 7*500*0.4 (mexc).
	denom := 7*100 + 7*1000 + 7*500*0.4
	want := 7 * 100 / denom * 100
	if math.Abs(share-want) > 0.001 {
		t.Fatalf("expected share≈%v, got %v", want, share)
	}
}

// TestEdgeXSpread10mAveragesRecentSamples checks the rolling 10-min mean
// with: (a) no samples → insufficient_history, (b) one valid sample → partial,
// (c) two+ valid samples within the window → complete and the arithmetic mean.
// Samples older than 10 minutes must be excluded.
func TestEdgeXSpread10mAveragesRecentSamples(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)

	// (a) empty history
	avg, status := store.edgexSpread10mLocked("BTC-USDT (perp)")
	if status != domain.StatusInsufficientHistory || avg != 0 {
		t.Fatalf("expected insufficient_history initially, got status=%s avg=%v", status, avg)
	}

	now := time.Now().UTC()
	// Stale sample > 10 min ago must be ignored.
	stale := domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now.Add(-12 * time.Minute),
		DepthStatus:   domain.StatusComplete,
		SpreadBP:      99,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 10}},
	}
	store.SavePlatformSnapshot(stale)
	if _, status := store.edgexSpread10mLocked("BTC-USDT (perp)"); status != domain.StatusInsufficientHistory {
		t.Fatalf("stale sample must not count, got %s", status)
	}

	// (b) one fresh valid sample → partial.
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now.Add(-2 * time.Minute),
		DepthStatus:   domain.StatusComplete,
		SpreadBP:      2.0,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 10}},
	})
	avg, status = store.edgexSpread10mLocked("BTC-USDT (perp)")
	if status != domain.StatusPartial {
		t.Fatalf("expected partial with 1 sample, got %s", status)
	}
	if math.Abs(avg-2.0) > 1e-9 {
		t.Fatalf("expected mean 2.0, got %v", avg)
	}

	// (c) add a second valid sample → complete + arithmetic mean.
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now.Add(-1 * time.Minute),
		DepthStatus:   domain.StatusComplete,
		SpreadBP:      4.0,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 10}},
	})
	avg, status = store.edgexSpread10mLocked("BTC-USDT (perp)")
	if status != domain.StatusComplete {
		t.Fatalf("expected complete with 2+ samples, got %s", status)
	}
	if math.Abs(avg-3.0) > 1e-9 {
		t.Fatalf("expected mean 3.0, got %v", avg)
	}

	// A SpreadBP=0 sample must not be counted.
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now.Add(-30 * time.Second),
		DepthStatus:   domain.StatusComplete,
		SpreadBP:      0,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 10}},
	})
	avg, _ = store.edgexSpread10mLocked("BTC-USDT (perp)")
	if math.Abs(avg-3.0) > 1e-9 {
		t.Fatalf("zero-spread sample must not bias mean, got %v", avg)
	}
}

// TestLiquidityKPIsExposeNumericsWhenAvailable exercises the public
// Liquidity() output to ensure both numeric KPI fields surface in the kpis
// map exactly when the underlying status indicates usable data.
func TestLiquidityKPIsExposeNumericsWhenAvailable(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 900, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		})
	}
	now := time.Now().UTC()
	for _, ts := range []time.Time{now.Add(-3 * time.Minute), now.Add(-1 * time.Minute)} {
		store.SavePlatformSnapshot(domain.PlatformSnapshot{
			Platform:      "edgeX",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    ts,
			DepthStatus:   domain.StatusComplete,
			SpreadBP:      1.5,
			DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 10}},
		})
	}
	out := store.Liquidity("BTC-USDT (perp)")
	kpis := out["kpis"].(map[string]any)
	if kpis["symbol_share_7d_status"] != domain.StatusComplete {
		t.Fatalf("expected complete 7d status, got %+v", kpis["symbol_share_7d_status"])
	}
	share, ok := kpis["symbol_share_7d_pct"].(float64)
	if !ok || math.Abs(share-10.0) > 0.001 {
		t.Fatalf("expected 7d share ≈ 10%%, got %+v", kpis["symbol_share_7d_pct"])
	}
	if kpis["edgex_spread_10m_status"] != domain.StatusComplete {
		t.Fatalf("expected complete spread status, got %+v", kpis["edgex_spread_10m_status"])
	}
	avg, ok := kpis["edgex_spread_10m_bp"].(float64)
	if !ok || math.Abs(avg-1.5) > 1e-9 {
		t.Fatalf("expected spread 10m bp = 1.5, got %+v", kpis["edgex_spread_10m_bp"])
	}
}
