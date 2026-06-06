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
