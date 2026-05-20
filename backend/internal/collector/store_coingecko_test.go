package collector

import (
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

func newCoinGeckoStore(t *testing.T) *Store {
	t.Helper()
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "lighter"}
	cfg.Runtime.CoinGecko.Enabled = true
	return NewStore(cfg)
}

// TestShareDoesNotDoubleCountWhenBothSourcesPresent is the M4 regression
// guard from the v2 plan: writing a native volume for Lighter AND a
// CoinGecko aggregate for Lighter must NOT add up. Share(24h) for the 9
// competitors strictly reads from the CoinGecko-only map.
func TestShareDoesNotDoubleCountWhenBothSourcesPresent(t *testing.T) {
	store := newCoinGeckoStore(t)
	now := time.Now().UTC()

	store.SaveVolume(domain.VolumeSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now,
		Volume24HUSD:  100,
		Status:        domain.StatusComplete,
	})
	// Native Lighter ticker writes here, but Share(24h) for Lighter must
	// IGNORE this row when CoinGecko data is present (R3).
	store.SaveVolume(domain.VolumeSnapshot{
		Platform:      "lighter",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now,
		Volume24HUSD:  77, // would double-count if leak occurs
		Status:        domain.StatusComplete,
	})
	store.SaveCoinGeckoPlatformVolumes([]domain.PlatformVolumeAggregate{
		{Platform: "binance", SnapshotTS: now, Volume24HUSD: 1000, Status: domain.StatusComplete},
		{Platform: "lighter", SnapshotTS: now, Volume24HUSD: 200, Status: domain.StatusComplete},
	})

	got := store.Share("24h")
	rows := got["rows"].([]map[string]any)

	rowByPlatform := map[string]map[string]any{}
	for _, row := range rows {
		rowByPlatform[row["platform"].(string)] = row
	}
	lighter := rowByPlatform["lighter"]
	if lighter["raw_volume_usd"].(float64) != 200 {
		t.Fatalf("expected Lighter raw=200 (CoinGecko only), got %+v", lighter)
	}
	if lighter["adjusted_volume_usd"].(float64) != 200 {
		t.Fatalf("expected Lighter adjusted=200 (no discount), got %+v", lighter)
	}
	if lighter["data_source"] != domain.DataSourceCoinGecko {
		t.Fatalf("expected Lighter data_source=coingecko, got %+v", lighter)
	}

	// Denominator should be 100 (edgeX) + 1000 (binance) + 200 (lighter) = 1300.
	// If Lighter native leaked in we would see 1300 + 77 = 1377.
	if got["denominator_usd"].(float64) != 1300 {
		t.Fatalf("expected denominator 1300 with strict source isolation, got %+v", got)
	}

	edge := rowByPlatform["edgeX"]
	if edge["raw_volume_usd"].(float64) != 100 || edge["data_source"] != domain.DataSourceNative {
		t.Fatalf("expected edgeX raw=100, data_source=native; got %+v", edge)
	}
}

func TestShareUsesCoinGeckoFor9CompetitorsExclusively(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "okx"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 200, Status: domain.StatusComplete})
	// Binance / OKX intentionally NOT written via SaveVolume - only via
	// CoinGecko. The legacy native fallback must not bleed in.
	store.SaveCoinGeckoPlatformVolumes([]domain.PlatformVolumeAggregate{
		{Platform: "binance", SnapshotTS: now, Volume24HUSD: 5000, Status: domain.StatusComplete},
		{Platform: "okx", SnapshotTS: now, Volume24HUSD: 4000, Status: domain.StatusComplete},
	})
	got := store.Share("24h")
	denom := got["denominator_usd"].(float64)
	if denom != 200+5000+4000 {
		t.Fatalf("denominator expected 9200, got %v", denom)
	}
}

func TestShare24hMarksMissingCoinGeckoAsStale(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 500, Status: domain.StatusComplete})
	got := store.Share("24h")
	rows := got["rows"].([]map[string]any)
	var binance map[string]any
	for _, row := range rows {
		if row["platform"] == "binance" {
			binance = row
		}
	}
	if binance["status"] != domain.StatusStale {
		t.Fatalf("expected binance stale when CoinGecko missing, got %+v", binance)
	}
	if _, ok := binance["raw_volume_usd"]; ok {
		t.Fatalf("stale row should not emit a raw_volume_usd, got %+v", binance)
	}
}

func TestShare7DReturnsInsufficientHistoryWhenNoDailyDataAtAll(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	got := store.Share("7d")
	if got["status"] != domain.StatusInsufficientHistory {
		t.Fatalf("expected insufficient_history when no daily rows exist, got %+v", got)
	}
	rows := got["rows"].([]map[string]any)
	for _, r := range rows {
		if r["status"] != domain.StatusInsufficientHistory {
			t.Fatalf("expected all per-platform rows to be insufficient_history, got %+v", r)
		}
	}
}

func TestShare7DReturnsPartialWhenSomeDaysSeen(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	// Only one day of data: each platform is partial (we have something
	// but less than the 7d window); the overall window status is partial.
	store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
		{Platform: "edgeX", Day: today, Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		{Platform: "binance", Day: today, Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
	})
	got := store.Share("7d")
	if got["status"] != domain.StatusPartial {
		t.Fatalf("expected partial when 1d < 7d window, got %+v", got)
	}
	rows := got["rows"].([]map[string]any)
	for _, r := range rows {
		if r["status"] != domain.StatusPartial {
			t.Fatalf("expected platform row partial, got %+v", r)
		}
	}
}

func TestShare7DReturnsPartialWhenGaps(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	// edgeX has all 7 days; binance only has 3 -> binance is partial,
	// overall window status is partial as well.
	for i := 0; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "edgeX", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	for i := 0; i < 3; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", Day: today.AddDate(0, 0, -i), Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		})
	}
	got := store.Share("7d")
	if got["status"] != domain.StatusPartial {
		t.Fatalf("expected partial when some platforms lack full window, got %+v", got)
	}
}

func TestShare7DReturnsCompleteWhenContiguous(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "edgeX", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
			{Platform: "binance", Day: today.AddDate(0, 0, -i), Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		})
	}
	got := store.Share("7d")
	if got["status"] != domain.StatusComplete {
		t.Fatalf("expected complete when both platforms have full 7d window, got %+v", got)
	}
	denom := got["denominator_usd"].(float64)
	if denom != 7*100+7*1000 {
		t.Fatalf("expected denominator 7700, got %v", denom)
	}
	trend := got["trend"].(map[string]any)
	if trend["status"] != domain.StatusComplete {
		t.Fatalf("trend status expected complete, got %+v", trend)
	}
}

func TestTop30ReturnsCoinGeckoRowsWhenStored(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)
	now := time.Now().UTC()
	rows := []domain.Top30Row{
		{Rank: 1, Platform: "binance", Symbol: "BTC-USDT (perp)", Volume24HUSD: 99999, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko, SnapshotTS: now, Volume7DStatus: domain.StatusInsufficientHistory, Delta7DStatus: domain.StatusInsufficientHistory},
		{Rank: 2, Platform: "binance", Symbol: "ETH-USDT (perp)", Volume24HUSD: 50000, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko, SnapshotTS: now, Volume7DStatus: domain.StatusInsufficientHistory, Delta7DStatus: domain.StatusInsufficientHistory},
	}
	store.SaveTop30("binance", rows)
	got := store.Top30("perp", "binance")
	if got["status"] != domain.StatusComplete {
		t.Fatalf("expected status complete, got %+v", got)
	}
	gotRows := got["rows"].([]domain.Top30Row)
	if len(gotRows) != 2 {
		t.Fatalf("expected 2 top30 rows, got %d", len(gotRows))
	}
	if gotRows[0].DataSource != domain.DataSourceCoinGecko {
		t.Fatalf("expected coingecko data source, got %+v", gotRows[0])
	}
}

func TestTop30ReturnsUnsupportedForUnknownPlatform(t *testing.T) {
	store := NewStore(config.Default())
	got := store.Top30("perp", "edgeX")
	if got["status"] != domain.StatusUnsupported {
		t.Fatalf("expected unsupported for edgeX (no CoinGecko data), got %+v", got)
	}
}

func TestDashboardMetaIncludesCoinGeckoLineageWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.CoinGecko.Enabled = true
	store := NewStore(cfg)
	meta := store.DashboardMeta()
	ds, ok := meta["data_sources"].(map[string]any)
	if !ok {
		t.Fatalf("expected data_sources block, got %+v", meta)
	}
	cg, ok := ds["coingecko"].(map[string]any)
	if !ok {
		t.Fatalf("expected coingecko block under data_sources, got %+v", ds)
	}
	if cg["enabled"] != true {
		t.Fatalf("expected enabled=true, got %+v", cg)
	}
	if ids, ok := cg["exchange_ids"].([]string); !ok || len(ids) != 9 {
		t.Fatalf("expected 9 exchange_ids, got %+v", cg["exchange_ids"])
	}
	if names, ok := cg["market_names"].([]string); !ok || len(names) != 9 {
		t.Fatalf("expected 9 market_names, got %+v", cg["market_names"])
	}
}

func TestSaveVolumeMirrorsEdgexIntoDailyAggregate(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: today.Add(2 * time.Hour), Volume24HUSD: 12345, Status: domain.StatusComplete})
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: today.Add(3 * time.Hour), Volume24HUSD: 67890, Status: domain.StatusComplete})

	store.mu.RLock()
	defer store.mu.RUnlock()
	rows := store.dailyPlatformVolumes["edgeX"]
	// edgeX is reported as platform-level (DisplaySymbol filled but stored
	// under symbol map keyed by display symbol). We assert at least one
	// entry landed in the symbol-keyed map for edgeX/BTC-USDT.
	k := key("edgeX", "BTC-USDT (perp)")
	symRows := store.dailySymbolVolumes[k]
	if len(symRows) == 0 {
		t.Fatalf("expected daily symbol aggregate to be created for edgeX")
	}
	if symRows[len(symRows)-1].Volume24HUSD != 67890 {
		t.Fatalf("expected last writer wins (67890), got %+v", symRows[len(symRows)-1])
	}
	// Platform-level map intentionally empty for edgeX because SaveVolume
	// only emits a symbol-level row; CoinGecko populates platform-level
	// daily rollups for the 9 competitors.
	_ = rows
}
