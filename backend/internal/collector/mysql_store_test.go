package collector

import (
	"math"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

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
		"aggregation_params_json JSON",
		"CREATE TABLE IF NOT EXISTS t_symbol_volume_snapshot",
		"CREATE TABLE IF NOT EXISTS t_collection_status",
		"CREATE TABLE IF NOT EXISTS t_collection_run",
	}
	for _, snippet := range required {
		if !contains(initSchemaSQL, snippet) {
			t.Fatalf("init schema missing %q", snippet)
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
	if kpis["symbol_share_7d_status"] != domain.StatusUnsupported {
		t.Fatalf("expected unsupported 7d symbol share, got %+v", kpis)
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
	if edge.DepthStatusLabel != "深度落后" {
		t.Fatalf("expected depth status label, got %+v", edge)
	}
	if rows[3].Rank01 != 0 {
		t.Fatalf("unsupported platform must not be ranked, got %+v", rows[3])
	}
}

func TestLiquidityMedianAndRankUseTierLevelDepthStatus(t *testing.T) {
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
	if medians["2.00%"] != 2500 {
		t.Fatalf("expected partial 2.00%% lower bounds to be excluded from median, got %+v", medians)
	}
	rows := got["rows"].([]domain.PlatformSnapshot)
	if rows[0].Rank01 != 4 {
		t.Fatalf("expected edgeX to be ranked by complete 0.10%% tier even when row is partial, got %+v", rows[0])
	}
	if _, ok := rows[0].VsMedianByTier["2.00%"]; ok {
		t.Fatalf("partial edgeX 2.00%% lower bound must not get vs median ratio, got %+v", rows[0].VsMedianByTier)
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

func TestShareReturnsFormalFieldsAndUnsupportedHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "mexc", "gate", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	for _, vol := range []domain.VolumeSnapshot{
		{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete},
		{Platform: "mexc", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete},
		{Platform: "gate", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete},
		{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 100, Status: domain.StatusComplete},
	} {
		store.SaveVolume(vol)
	}

	got := store.Share("24h")
	kpis := got["kpis"].(map[string]any)
	if kpis["edgex_total_volume_usd"].(float64) != 100 {
		t.Fatalf("expected raw edgeX total volume, got %+v", kpis)
	}
	if got["denominator_usd"].(float64) != 290 {
		t.Fatalf("expected adjusted denominator, got %+v", got)
	}
	rows := got["rows"].([]map[string]any)
	var mexc map[string]any
	for _, row := range rows {
		if row["platform"] == "mexc" {
			mexc = row
		}
	}
	if mexc["raw_volume_usd"].(float64) != 100 || mexc["adjusted_volume_usd"].(float64) != 40 {
		t.Fatalf("expected raw and adjusted MEXC volumes, got %+v", mexc)
	}

	historical := store.Share("7d")
	if historical["status"] != domain.StatusUnsupported {
		t.Fatalf("expected unsupported historical share, got %+v", historical)
	}
}
