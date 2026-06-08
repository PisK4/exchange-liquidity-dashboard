package collector

import (
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

func TestStoreSnapshotPublishesConsistentReadModel(t *testing.T) {
	store := NewStore(config.Config{
		Symbols:   []domain.SymbolSub{{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC"}},
		Platforms: []string{"edgeX"},
	})
	ts := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    ts,
		DepthStatus:   domain.StatusComplete,
		DepthByTier: map[string]domain.DepthMetrics{
			"0.10%": {TotalUSD: 100, AggregationParams: map[string]string{"mode": "strict"}},
		},
		BuySlippageBP:   map[string]float64{"100000": 1},
		SellSlippageBP:  map[string]float64{"100000": 2},
		WorstSlippageBP: map[string]float64{"100000": 2},
	})
	store.SaveStatus([]domain.CollectionStatus{{Platform: "edgeX", Status: domain.StatusComplete}}, RunSummary{
		RunID:       "run-1",
		StartedAt:   ts.Add(-time.Second),
		CompletedAt: ts,
		Success:     1,
	})

	snap := store.Snapshot()
	if got := len(snap.Platforms); got != 1 {
		t.Fatalf("snapshot platforms = %d, want 1", got)
	}
	if snap.Run.RunID != "run-1" || len(snap.Status) != 1 {
		t.Fatalf("snapshot status/run not published: %#v %#v", snap.Run, snap.Status)
	}

	row := snap.Platforms["edgeX|BTC-USDT (perp)"]
	row.DepthByTier["0.10%"] = domain.DepthMetrics{TotalUSD: 1}
	row.BuySlippageBP["100000"] = 99
	snap.Platforms["edgeX|BTC-USDT (perp)"] = domain.PlatformSnapshot{Platform: "mutated"}
	snap2 := store.Snapshot()
	if snap2.Platforms["edgeX|BTC-USDT (perp)"].Platform != "edgeX" {
		t.Fatalf("Snapshot must return an isolated read model, got %#v", snap2.Platforms["edgeX|BTC-USDT (perp)"])
	}
	if snap2.Platforms["edgeX|BTC-USDT (perp)"].DepthByTier["0.10%"].TotalUSD != 100 {
		t.Fatalf("Snapshot must isolate nested depth maps, got %#v", snap2.Platforms["edgeX|BTC-USDT (perp)"].DepthByTier)
	}
	if snap2.Platforms["edgeX|BTC-USDT (perp)"].BuySlippageBP["100000"] != 1 {
		t.Fatalf("Snapshot must isolate nested slippage maps, got %#v", snap2.Platforms["edgeX|BTC-USDT (perp)"].BuySlippageBP)
	}
}

func TestStoreSnapshotKeepsEdgeXSurfacesSeparate(t *testing.T) {
	store := NewStore(config.Config{
		Symbols: []domain.SymbolSub{
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC", MarketSurface: "perp_v1", InstrumentKind: "perp", Lineage: "edgeX-perp-v1", APISymbol: "BTCUSD", ContractID: "10000001"},
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC", MarketSurface: "perp_v2", InstrumentKind: "perp", Lineage: "edgeX-perp-v2", APISymbol: "BTCUSDC", ContractID: "30000001"},
		},
		Platforms: []string{"edgeX"},
	})
	ts := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:        "edgeX",
		DisplayPlatform: "edgeX V1",
		DisplaySymbol:   "BTC-USDT (perp)",
		VenueSymbol:     "BTCUSD",
		MarketSurface:   "perp_v1",
		InstrumentKind:  "perp",
		Lineage:         "edgeX-perp-v1",
		ContractID:      "10000001",
		SnapshotTS:      ts,
		DepthStatus:     domain.StatusComplete,
		DepthByTier:     map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 100}},
		BuySlippageBP:   map[string]float64{},
		SellSlippageBP:  map[string]float64{},
		WorstSlippageBP: map[string]float64{},
		CanonicalSymbol: "BTC",
		PlatformGroup:   "edgeX",
		BaseAsset:       "BTC",
		QuoteAsset:      "USDT",
		SourceEndpoint:  "v1-source",
		DataFreshness:   domain.FreshnessLive,
	})
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:        "edgeX",
		DisplayPlatform: "edgeX V2",
		DisplaySymbol:   "BTC-USDT (perp)",
		VenueSymbol:     "BTCUSDC",
		MarketSurface:   "perp_v2",
		InstrumentKind:  "perp",
		Lineage:         "edgeX-perp-v2",
		ContractID:      "30000001",
		SnapshotTS:      ts,
		DepthStatus:     domain.StatusComplete,
		DepthByTier:     map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 200}},
		BuySlippageBP:   map[string]float64{},
		SellSlippageBP:  map[string]float64{},
		WorstSlippageBP: map[string]float64{},
		CanonicalSymbol: "BTC",
		PlatformGroup:   "edgeX",
		BaseAsset:       "BTC",
		QuoteAsset:      "USDC",
		SourceEndpoint:  "v2-source",
	})

	rows := store.Liquidity("BTC-USDT (perp)")["rows"].([]domain.PlatformSnapshot)
	if len(rows) != 2 {
		t.Fatalf("expected two EdgeX surface rows, got %d: %+v", len(rows), rows)
	}
	bySurface := map[string]domain.PlatformSnapshot{}
	for _, row := range rows {
		bySurface[row.MarketSurface] = row
	}
	if bySurface["perp_v1"].ContractID != "10000001" || bySurface["perp_v1"].DepthByTier["0.10%"].TotalUSD != 100 {
		t.Fatalf("unexpected V1 row: %+v", bySurface["perp_v1"])
	}
	if bySurface["perp_v2"].ContractID != "30000001" || bySurface["perp_v2"].DepthByTier["0.10%"].TotalUSD != 200 {
		t.Fatalf("unexpected V2 row: %+v", bySurface["perp_v2"])
	}
}

func TestTop30ListedSurfacesExposeEdgeXPerpVersions(t *testing.T) {
	store := NewStore(config.Config{
		Symbols: []domain.SymbolSub{
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC", MarketSurface: "perp_v1", InstrumentKind: "perp", Lineage: "edgeX-perp-v1", APISymbol: "BTCUSD", ContractID: "10000001", BaseAsset: "BTC", QuoteAsset: "USDT", SourceEndpoint: "v1-source"},
			{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC", MarketSurface: "perp_v2", InstrumentKind: "perp", Lineage: "edgeX-perp-v2", APISymbol: "BTCUSDC", ContractID: "30000001", BaseAsset: "BTC", QuoteAsset: "USDC", SourceEndpoint: "v2-source"},
		},
		Platforms: []string{"edgeX", "binance"},
	})
	ts := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	store.SaveTop30("binance", []domain.Top30Row{{
		Rank:           1,
		Platform:       "binance",
		Symbol:         "BTC-USDT (perp)",
		Volume24HUSD:   1000,
		Status:         domain.StatusComplete,
		SnapshotTS:     ts,
		EdgexListed:    true,
		ListedStatus:   domain.StatusComplete,
		CoverageStatus: domain.StatusComplete,
	}})

	rows := store.Top30("perp", "binance")["rows"].([]domain.Top30Row)
	if len(rows) != 1 {
		t.Fatalf("expected one Top30 row, got %d", len(rows))
	}
	if len(rows[0].EdgexListedSurfaces) != 2 {
		t.Fatalf("expected V1 and V2 listed surfaces, got %+v", rows[0].EdgexListedSurfaces)
	}
	byLineage := map[string]domain.ListedSurfaceDetail{}
	for _, surface := range rows[0].EdgexListedSurfaces {
		byLineage[surface.Lineage] = surface
	}
	if byLineage["edgeX-perp-v1"].DisplayPlatform != "edgeX V1" || byLineage["edgeX-perp-v1"].ContractID != "10000001" {
		t.Fatalf("unexpected V1 surface detail: %+v", byLineage["edgeX-perp-v1"])
	}
	if byLineage["edgeX-perp-v2"].DisplayPlatform != "edgeX V2" || byLineage["edgeX-perp-v2"].ContractID != "30000001" {
		t.Fatalf("unexpected V2 surface detail: %+v", byLineage["edgeX-perp-v2"])
	}
}

func TestWarmCacheSummaryDetectsUsableSnapshotData(t *testing.T) {
	store := NewStore(config.Config{
		Symbols:   []domain.SymbolSub{{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC"}},
		Platforms: []string{"edgeX"},
	})
	if got := store.WarmCacheSummary(); got.HasUsableData {
		t.Fatalf("empty store must not be warm: %+v", got)
	}
	store.SavePlatformSnapshot(domain.PlatformSnapshot{
		Platform:      "edgeX",
		DisplaySymbol: "BTC-USDT (perp)",
		SnapshotTS:    time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
		DepthStatus:   domain.StatusComplete,
		DepthByTier:   map[string]domain.DepthMetrics{"0.10%": {TotalUSD: 100}},
	})
	got := store.WarmCacheSummary()
	if !got.HasUsableData || got.PlatformSnapshots != 1 {
		t.Fatalf("warm cache summary = %+v", got)
	}
}
