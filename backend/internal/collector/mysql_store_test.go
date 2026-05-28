package collector

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

func TestListingSchemaIncludedInInitSchemaSQL(t *testing.T) {
	for _, table := range []string{
		"t_listing_instrument_snapshot",
		"t_listing_announcement",
		"t_listing_announcement_symbol",
		"t_listing_signal_observation",
		"t_listing_candidate",
		"t_listing_candidate_signal",
		"t_listing_source_state",
		"t_listing_worker_lease",
		"t_listing_risk_plan",
		"t_listing_decision",
		"t_listing_watchlist",
		"t_listing_action_dispatch",
		"t_listing_delivery_outbox",
		"t_listing_delivery_attempt",
	} {
		if !contains(initSchemaSQL, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("initSchemaSQL missing CREATE TABLE for %s", table)
		}
	}
	if contains(initSchemaSQL, "webhook_url") {
		t.Fatalf("initSchemaSQL must not persist full webhook URL")
	}
}

func TestListingMigrationUpAndDownExist(t *testing.T) {
	upPath := filepath.Join("..", "..", "migrations", "000010_listing_agent_p1.up.sql")
	downPath := filepath.Join("..", "..", "migrations", "000010_listing_agent_p1.down.sql")
	up, err := os.ReadFile(upPath)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(downPath)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	upStr := string(up)
	downStr := string(down)
	for _, table := range []string{
		"t_listing_instrument_snapshot",
		"t_listing_announcement",
		"t_listing_announcement_symbol",
		"t_listing_signal_observation",
		"t_listing_candidate",
		"t_listing_candidate_signal",
		"t_listing_source_state",
		"t_listing_worker_lease",
		"t_listing_risk_plan",
		"t_listing_decision",
		"t_listing_watchlist",
		"t_listing_action_dispatch",
		"t_listing_delivery_outbox",
		"t_listing_delivery_attempt",
	} {
		if !contains(upStr, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("up migration missing %s", table)
		}
		if !contains(downStr, "DROP TABLE IF EXISTS "+table) {
			t.Fatalf("down migration missing %s", table)
		}
	}
	if contains(upStr, "webhook_url") {
		t.Fatalf("up migration must not persist full webhook URL")
	}
	if !contains(upStr, "uk_listing_delivery_dedupe") {
		t.Fatalf("up migration must declare delivery outbox dedupe unique key")
	}
	if !contains(upStr, "uk_listing_candidate_identity") {
		t.Fatalf("up migration must declare candidate identity unique key")
	}
}

func TestInitSchemaIncludesPersistenceTables(t *testing.T) {
	required := []string{
		"CREATE TABLE IF NOT EXISTS t_symbol_mapping",
		"CREATE TABLE IF NOT EXISTS t_orderbook_snapshot",
		"tier VARCHAR(16)",
		"bid_usd DECIMAL(28,8)",
		"bid_levels_returned INT",
		"ask_levels_returned INT",
		"api_level_cap INT",
		"farthest_bid_pct DECIMAL(18,8)",
		"farthest_ask_pct DECIMAL(18,8)",
		"depth_source VARCHAR(32)",
		"source_id VARCHAR(64)",
		"strict_complete TINYINT(1)",
		"display_available TINYINT(1)",
		"policy_acceptance VARCHAR(32)",
		"physical_limit TINYINT(1)",
		"unofficial_ui_endpoint TINYINT(1)",
		"aggregation_params_json JSON",
		"CREATE TABLE IF NOT EXISTS t_symbol_volume_snapshot",
		"CREATE TABLE IF NOT EXISTS t_collection_status",
		"CREATE TABLE IF NOT EXISTS t_collection_run",
		"UNIQUE KEY uk_top30_platform_symbol_ts",
	}
	for _, snippet := range required {
		if !contains(initSchemaSQL, snippet) {
			t.Fatalf("init schema missing %q", snippet)
		}
	}
	for _, forbidden := range []string{
		"t_exchange_instrument_catalog",
		"t_platform_volume_snapshot",
		"t_runtime_config",
	} {
		if contains(initSchemaSQL, forbidden) {
			t.Fatalf("init schema must not create unused empty table %q", forbidden)
		}
	}
}

func TestTop30UniqueKeyMigrationDeduplicatesBeforeAddingKey(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000009_top30_unique_key.up.sql"))
	if err != nil {
		t.Fatalf("read top30 unique key migration: %v", err)
	}
	sql := string(body)
	for _, required := range []string{
		"DELETE t1 FROM t_top30_snapshot t1",
		"t1.platform = t2.platform",
		"t1.symbol = t2.symbol",
		"t1.snapshot_ts = t2.snapshot_ts",
		"ADD UNIQUE KEY uk_top30_platform_symbol_ts",
	} {
		if !contains(sql, required) {
			t.Fatalf("migration missing %q:\n%s", required, sql)
		}
	}
}

func TestPlatformSnapshotOrderbookRowsArePerTierWithLineage(t *testing.T) {
	row := domain.PlatformSnapshot{
		Platform:       "gate",
		DisplaySymbol:  "BTC-USDT (perp)",
		SnapshotTS:     time.Now().UTC(),
		SourceEndpoint: "https://example.test/raw",
		DepthStatus:    domain.StatusPartial,
		PartialReason:  domain.ReasonAPILevelCap,
		DepthByTier: map[string]domain.DepthMetrics{
			"0.10%": {
				BidUSD:              10,
				AskUSD:              11,
				TotalUSD:            21,
				DepthStatus:         domain.StatusComplete,
				DepthSource:         domain.SourceRawOrderbook,
				SourceID:            "gate_raw",
				SourceEndpoint:      "https://example.test/raw",
				LevelsReturned:      400,
				BidLevelsReturned:   200,
				AskLevelsReturned:   200,
				APILevelCap:         400,
				FarthestBidPct:      0.2,
				FarthestAskPct:      0.3,
				FarthestDistancePct: 0.3,
			},
			"2.00%": {
				BidUSD:              100,
				AskUSD:              110,
				TotalUSD:            210,
				DepthStatus:         domain.StatusAggregatedOrderbook,
				DepthSource:         domain.SourceAggregatedOrderbook,
				SourceID:            "gate_agg_10",
				SourceEndpoint:      "https://example.test/agg?interval=10",
				LevelsReturned:      400,
				BidLevelsReturned:   200,
				AskLevelsReturned:   200,
				APILevelCap:         400,
				FarthestBidPct:      2.1,
				FarthestAskPct:      2.2,
				FarthestDistancePct: 2.2,
				AggregationParams:   map[string]string{"interval": "10"},
			},
		},
		BuySlippageBP:  map[string]float64{"50000": 1.2},
		SellSlippageBP: map[string]float64{"50000": 1.4},
	}

	rows := platformSnapshotOrderbookRows(row)
	if len(rows) != 2 {
		t.Fatalf("expected one DB row per tier, got %d", len(rows))
	}
	deep := rows["2.00%"]
	if deep.DepthStatus != domain.StatusAggregatedOrderbook || deep.DepthSource != domain.SourceAggregatedOrderbook || deep.SourceID != "gate_agg_10" {
		t.Fatalf("expected deep tier lineage to be preserved, got %+v", deep)
	}
	if deep.BidLevelsReturned != 200 || deep.AskLevelsReturned != 200 || deep.FarthestDistancePct != 2.2 {
		t.Fatalf("expected level/farthest metrics to be preserved, got %+v", deep)
	}
	if !deep.StrictComplete || !deep.DisplayAvailable || deep.PolicyAcceptance != domain.PolicyAggregatedStrict {
		t.Fatalf("expected strict display contract fields to be derived, got %+v", deep)
	}
	if deep.AggregationParamsJSON == "" || !contains(deep.AggregationParamsJSON, "interval") {
		t.Fatalf("expected aggregation params json, got %+v", deep)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTop30DoesNotPromoteTickerSnapshotsToLiveRanking(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)
	store.SaveVolume(domain.VolumeSnapshot{
		Platform:      "binance",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    time.Now().UTC(),
		Volume24HUSD:  123,
		Status:        domain.StatusComplete,
	})

	got := store.Top30("perp", "binance")
	rows := got["rows"].([]domain.Top30Row)
	if got["status"] != domain.StatusUnsupported {
		t.Fatalf("ticker-only data must not be represented as live Top30, got %+v", got)
	}
	if len(rows) != 0 {
		t.Fatalf("unsupported Top30 should not return synthetic ranking rows, got %+v", rows)
	}
}

func TestLiquidityFallsBackToRecentDisplayableSnapshotWhenLatestCollectionFails(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX"}
	cfg.Runtime.DisplayFallbackWindow = 30 * time.Minute
	store := NewStore(cfg)
	now := time.Now().UTC()
	oldGood := domain.PlatformSnapshot{
		Platform:       "edgeX",
		DisplaySymbol:  "BTC-USDT (perp)",
		SnapshotTS:     now.Add(-10 * time.Minute),
		SourceEndpoint: "ok-source",
		DepthStatus:    domain.StatusComplete,
		DepthByTier: map[string]domain.DepthMetrics{
			"0.10%": {TotalUSD: 123},
		},
		BuySlippageBP:  map[string]float64{},
		SellSlippageBP: map[string]float64{},
	}
	latestError := domain.PlatformSnapshot{
		Platform:       "edgeX",
		DisplaySymbol:  "BTC-USDT (perp)",
		SnapshotTS:     now,
		SourceEndpoint: "failed-source",
		DepthStatus:    domain.StatusError,
		Error:          "context deadline exceeded",
		DepthByTier:    map[string]domain.DepthMetrics{},
		BuySlippageBP:  map[string]float64{},
		SellSlippageBP: map[string]float64{},
	}
	store.SavePlatformSnapshot(oldGood)
	store.SavePlatformSnapshot(latestError)

	got := store.Liquidity("BTC-USDT (perp)")
	rows := got["rows"].([]domain.PlatformSnapshot)
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	row := rows[0]
	if row.DepthStatus != domain.StatusComplete {
		t.Fatalf("expected fallback display status complete, got %+v", row)
	}
	if row.DataFreshness != domain.FreshnessDelayed {
		t.Fatalf("expected delayed freshness, got %+v", row)
	}
	if row.LastCollectionStatus != domain.StatusError || row.LastCollectionError != "context deadline exceeded" {
		t.Fatalf("expected latest collection error metadata, got %+v", row)
	}
	if row.DepthByTier["0.10%"].TotalUSD != 123 {
		t.Fatalf("expected previous depth to be displayed, got %+v", row.DepthByTier)
	}
}

func TestLiquidityDoesNotFallbackPastDisplayWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX"}
	cfg.Runtime.DisplayFallbackWindow = 30 * time.Minute
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:       "edgeX",
		DisplaySymbol:  "BTC-USDT (perp)",
		SnapshotTS:     now.Add(-31 * time.Minute),
		DepthStatus:    domain.StatusComplete,
		DepthByTier:    map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 123}},
		BuySlippageBP:  map[string]float64{},
		SellSlippageBP: map[string]float64{},
	})
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:       "edgeX",
		DisplaySymbol:  "BTC-USDT (perp)",
		SnapshotTS:     now,
		DepthStatus:    domain.StatusError,
		Error:          "context deadline exceeded",
		DepthByTier:    map[string]domain.DepthMetrics{},
		BuySlippageBP:  map[string]float64{},
		SellSlippageBP: map[string]float64{},
	})

	got := store.Liquidity("BTC-USDT (perp)")
	row := got["rows"].([]domain.PlatformSnapshot)[0]
	if row.DepthStatus != domain.StatusError {
		t.Fatalf("expected latest error after fallback window expires, got %+v", row)
	}
}

func TestSavePlatformSnapshotPrunesHistoryOutsideFallbackWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX"}
	cfg.Runtime.DisplayFallbackWindow = 30 * time.Minute
	store := NewStore(cfg)
	now := time.Now().UTC()

	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now.Add(-45 * time.Minute),
		DepthStatus:   domain.StatusComplete,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 1}},
	})
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now.Add(-20 * time.Minute),
		DepthStatus:   domain.StatusComplete,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 2}},
	})
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now,
		DepthStatus:   domain.StatusError,
		DepthByTier:   map[string]domain.DepthMetrics{},
	})

	history := store.platformHistory[key("edgeX", "BTC-USDT (perp)")]
	if len(history) != 2 {
		t.Fatalf("expected history to be pruned to 2 rows, got %d: %+v", len(history), history)
	}
	if !history[0].SnapshotTS.Equal(now.Add(-20 * time.Minute)) {
		t.Fatalf("expected oldest retained row at -20m, got %+v", history[0].SnapshotTS)
	}
}

func TestHydrateHelpersPopulateMemoryWithoutPersistencePath(t *testing.T) {
	cfg := config.Default()
	store := NewStore(cfg)
	now := time.Now().UTC()

	store.hydratePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now,
		DepthStatus:   domain.StatusComplete,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 10}},
	})
	store.hydrateVolume(domain.VolumeSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now,
		Volume24HUSD:  100,
		Status:        domain.StatusComplete,
	})
	store.hydrateCoinGeckoPlatformVolumes([]domain.PlatformVolumeAggregate{
		{Platform: "binance", SnapshotTS: now, Volume24HUSD: 200, Status: domain.StatusComplete},
	})
	store.hydrateDailyVolumeAggregates([]domain.DailyVolumeAggregate{
		{Platform: "binance", DisplaySymbol: "BTC-USD (perp)", Day: now, Volume24HUSD: 300, Status: domain.StatusComplete},
	})
	store.hydrateTop30("binance", []domain.Top30Row{
		{Platform: "binance", Symbol: "BTC-USDT (perp)", Rank: 1, Volume24HUSD: 400, Status: domain.StatusComplete, SnapshotTS: now},
	})

	if _, ok := store.platforms[key("edgeX", "BTC-USDT (perp)")]; !ok {
		t.Fatal("expected platform snapshot in memory")
	}
	if got := store.volumes[key("edgeX", "BTC-USDT (perp)")].Volume24HUSD; got != 100 {
		t.Fatalf("expected hydrated volume 100, got %v", got)
	}
	if got := store.cgPlatformVolumes["binance"].Volume24HUSD; got != 200 {
		t.Fatalf("expected hydrated CoinGecko volume 200, got %v", got)
	}
	if _, ok := store.dailySymbolVolumes[key("binance", "BTC-USDT (perp)")]; !ok {
		t.Fatal("expected daily symbol volume to be canonicalized and hydrated")
	}
	if got := store.top30ByPlatform["binance"]; len(got) != 1 || got[0].Symbol != "BTC-USDT (perp)" {
		t.Fatalf("expected hydrated top30 row, got %+v", got)
	}
}

func TestLiquidityComputesCompetitorMedianRankAndUnsupportedHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "okx", "mexc"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	for _, row := range []domain.PlatformSnapshot{
		{
			Platform:      "edgeX",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusComplete,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {BidUSD: 10, AskUSD: 10, TotalUSD: 20},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
		{
			Platform:      "binance",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusComplete,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {BidUSD: 50, AskUSD: 50, TotalUSD: 100},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
		{
			Platform:      "okx",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusComplete,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {BidUSD: 100, AskUSD: 100, TotalUSD: 200},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
		{
			Platform:       "mexc",
			DisplaySymbol:  "BTC-USDT (perp)",
			SnapshotTS:     now,
			DepthStatus:    domain.StatusUnsupported,
			Error:          "not available",
			DepthByTier:    map[string]domain.DepthMetrics{},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
	} {
		store.SavePlatformSnapshot(row)
	}

	got := store.Liquidity("BTC-USDT (perp)")
	kpis := got["kpis"].(map[string]any)
	if kpis["symbol_share_7d_status"] != domain.StatusInsufficientHistory {
		t.Fatalf("expected insufficient_history 7d symbol share, got %+v", kpis)
	}
	if kpis["symbol_share_wow_status"] != domain.StatusInsufficientHistory {
		t.Fatalf("expected insufficient_history WoW symbol share, got %+v", kpis)
	}
	medians := got["competitor_median_by_tier"].(map[string]float64)
	if medians["0.10%"] != 150 {
		t.Fatalf("expected competitor median to exclude edgeX and unsupported rows, got %+v", medians)
	}
	rows := got["rows"].([]domain.PlatformSnapshot)
	edge := rows[0]
	if math.Abs(edge.VsMedianByTier["0.10%"]-0.1333333333) > 0.0001 {
		t.Fatalf("unexpected edgeX vs median: %+v", edge.VsMedianByTier)
	}
	if edge.Rank01 != 3 {
		t.Fatalf("expected edgeX rank 3 among valid platforms, got %+v", edge)
	}
	if edge.DepthStatusLabel != "落后" {
		t.Fatalf("expected depth status label, got %+v", edge)
	}
	if rows[3].Rank01 != 0 {
		t.Fatalf("unsupported platform must not be ranked, got %+v", rows[3])
	}
}

func TestLiquidityMedianAndRankUseTierLevelDisplayAvailability(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "okx", "gate"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	for _, row := range []domain.PlatformSnapshot{
		{
			Platform:      "edgeX",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusPartial,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {TotalUSD: 20, DepthStatus: domain.StatusComplete},
				"2.00%": {TotalUSD: 200, DepthStatus: domain.StatusPartial, PartialReason: domain.ReasonAPILevelCap},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
		{
			Platform:      "binance",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusPartial,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {TotalUSD: 100, DepthStatus: domain.StatusComplete},
				"2.00%": {TotalUSD: 1000, DepthStatus: domain.StatusPartial, PartialReason: domain.ReasonAPILevelCap},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
		{
			Platform:      "okx",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusComplete,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {TotalUSD: 200, DepthStatus: domain.StatusComplete},
				"2.00%": {TotalUSD: 2000, DepthStatus: domain.StatusComplete},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
		{
			Platform:      "gate",
			DisplaySymbol: "BTC-USDT (perp)",
			SnapshotTS:    now,
			DepthStatus:   domain.StatusComplete,
			DepthByTier: map[string]domain.DepthMetrics{
				"0.10%": {TotalUSD: 300, DepthStatus: domain.StatusComplete},
				"2.00%": {TotalUSD: 3000, DepthStatus: domain.StatusAggregatedOrderbook, DepthSource: domain.SourceAggregatedOrderbook},
			},
			BuySlippageBP:  map[string]float64{},
			SellSlippageBP: map[string]float64{},
		},
	} {
		store.SavePlatformSnapshot(row)
	}

	got := store.Liquidity("BTC-USDT (perp)")
	medians := got["competitor_median_by_tier"].(map[string]float64)
	if medians["0.10%"] != 200 {
		t.Fatalf("expected complete 0.10%% tiers to participate in median, got %+v", medians)
	}
	if medians["2.00%"] != 2000 {
		t.Fatalf("expected displayable partial 2.00%% lower bounds to participate in median, got %+v", medians)
	}
	rows := got["rows"].([]domain.PlatformSnapshot)
	if rows[0].Rank01 != 4 {
		t.Fatalf("expected edgeX to be ranked by complete 0.10%% tier even when row is partial, got %+v", rows[0])
	}
	if math.Abs(rows[0].VsMedianByTier["2.00%"]-0.1) > 0.0001 {
		t.Fatalf("displayable partial edgeX 2.00%% lower bound should get vs median ratio, got %+v", rows[0].VsMedianByTier)
	}
}

func TestPlatformFromBookCreatesPerTierStatuses(t *testing.T) {
	runtime := config.Default().Runtime
	runtime.DepthTiers = []float64{0.001, 0.02}
	book := domain.OrderBookSnapshot{
		Platform:       "edgeX",
		DisplaySymbol:  "BTC-USDT (perp)",
		SourceEndpoint: "https://example.test/depth",
		SnapshotTS:     time.Now().UTC(),
		DepthStatus:    domain.StatusPartial,
		PartialReason:  domain.ReasonAPILevelCap,
		DepthSource:    domain.SourceRawOrderbook,
		SourceID:       "raw",
		APILevelCap:    4,
		Bids: []domain.Level{
			{Price: 99.95, Size: 1},
			{Price: 99.0, Size: 1},
		},
		Asks: []domain.Level{
			{Price: 100.05, Size: 1},
			{Price: 101.0, Size: 1},
		},
		SourceBooks: map[string]domain.BookView{},
	}
	book.SourceBooks["raw"] = domain.BookView{
		SourceID:       "raw",
		Source:         domain.SourceRawOrderbook,
		SourceEndpoint: book.SourceEndpoint,
		Bids:           book.Bids,
		Asks:           book.Asks,
		SnapshotTS:     book.SnapshotTS,
		APILevelCap:    book.APILevelCap,
	}

	row := platformFromBook(book, runtime)
	if row.DepthByTier["0.10%"].DepthStatus != domain.StatusComplete {
		t.Fatalf("expected near tier complete, got %+v", row.DepthByTier["0.10%"])
	}
	if row.DepthByTier["2.00%"].DepthStatus != domain.StatusPartial || row.DepthByTier["2.00%"].PartialReason != domain.ReasonAPILevelCap {
		t.Fatalf("expected deep tier partial/api_level_cap, got %+v", row.DepthByTier["2.00%"])
	}
	if row.DepthStatus != domain.StatusPartial {
		t.Fatalf("expected row status partial when only near tier is complete, got %+v", row)
	}
}

func TestLiquidityWithoutCompetitorMedianDoesNotReportHealthy(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    now,
		DepthStatus:   domain.StatusComplete,
		DepthByTier: map[string]domain.DepthMetrics{
			"0.10%": {BidUSD: 10, AskUSD: 10, TotalUSD: 20},
		},
		BuySlippageBP:  map[string]float64{},
		SellSlippageBP: map[string]float64{},
	})
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:       "binance",
		DisplaySymbol:  "BTC-USDT (perp)",
		SnapshotTS:     now,
		DepthStatus:    domain.StatusUnsupported,
		Error:          "unsupported",
		DepthByTier:    map[string]domain.DepthMetrics{},
		BuySlippageBP:  map[string]float64{},
		SellSlippageBP: map[string]float64{},
	})

	got := store.Liquidity("BTC-USDT (perp)")
	row := got["rows"].([]domain.PlatformSnapshot)[0]
	if row.DepthStatusLabel != domain.StatusUnsupported {
		t.Fatalf("missing competitor median must not be marked healthy, got %+v", row)
	}
}

func TestShareReturnsFormalFieldsAndInsufficientHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "mexc", "gate", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	// edgeX is now also sourced from CoinGecko (same as the 9
	// competitors); native ticker writes still happen for the Liquidity
	// tab but they must not feed Share(24h) anymore.
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 999, Status: domain.StatusComplete, SourceEndpoint: "edgex"})
	store.SaveCoinGeckoPlatformVolumes([]domain.PlatformVolumeAggregate{
		{Platform: "edgeX", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		{Platform: "mexc", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		{Platform: "gate", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
		{Platform: "binance", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko},
	})

	got := store.Share("24h")
	kpis := got["kpis"].(map[string]any)
	if kpis["edgex_total_volume_usd"].(float64) != 100 {
		t.Fatalf("expected raw edgeX total volume, got %+v", kpis)
	}
	// edgeX(100) + mexc(40) + gate(50) + binance(100) = 290 adjusted
	if got["denominator_usd"].(float64) != 290 {
		t.Fatalf("expected adjusted denominator 290, got %+v", got)
	}
	rows := got["rows"].([]map[string]any)
	var mexc, edge map[string]any
	for _, row := range rows {
		switch row["platform"] {
		case "mexc":
			mexc = row
		case "edgeX":
			edge = row
		}
	}
	if mexc["raw_volume_usd"].(float64) != 100 || mexc["adjusted_volume_usd"].(float64) != 40 {
		t.Fatalf("expected raw and adjusted MEXC volumes, got %+v", mexc)
	}
	if mexc["data_source"] != domain.DataSourceCoinGecko {
		t.Fatalf("expected MEXC data_source=coingecko, got %+v", mexc)
	}
	if edge["data_source"] != domain.DataSourceCoinGecko {
		t.Fatalf("expected edgeX data_source=coingecko, got %+v", edge)
	}

	historical := store.Share("7d")
	// Only one UTC day of data has been written, so a 7-day window must be
	// reported as insufficient_history (R6) rather than unsupported.
	if historical["status"] != domain.StatusInsufficientHistory {
		t.Fatalf("expected insufficient_history historical share, got %+v", historical)
	}
}

func TestEdgexListedTinyIntDistinguishesKnownFalseFromUnknown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		listed    bool
		status    string
		wantValid bool
		wantValue int64
	}{
		{"known_not_listed", false, domain.StatusComplete, true, 0},
		{"known_listed", true, domain.StatusComplete, true, 1},
		{"unknown_status_empty", false, "", false, 0},
		{"unknown_status_insufficient", false, domain.StatusInsufficientHistory, false, 0},
		{"listed_true_but_status_missing_writes_null", true, "", false, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := edgexListedTinyInt(tc.listed, tc.status)
			if got.Valid != tc.wantValid {
				t.Fatalf("Valid mismatch: got %v, want %v", got.Valid, tc.wantValid)
			}
			if tc.wantValid && got.Int64 != tc.wantValue {
				t.Fatalf("Int64 mismatch: got %d, want %d", got.Int64, tc.wantValue)
			}
		})
	}
}
