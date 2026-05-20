package collector

import (
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

func TestInitSchemaIncludesPersistenceTables(t *testing.T) {
	required := []string{
		"CREATE TABLE IF NOT EXISTS t_symbol_mapping",
		"CREATE TABLE IF NOT EXISTS t_orderbook_snapshot",
		"depth_json JSON",
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
	if rows[0].Status != domain.StatusUnsupported {
		t.Fatalf("ticker-only data must not be represented as live Top30, got %+v", rows[0])
	}
	if rows[0].Volume24HUSD != 0 {
		t.Fatalf("ticker-only Top30 row should not carry synthetic ranking volume, got %+v", rows[0])
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
