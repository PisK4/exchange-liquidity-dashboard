package collector

import (
	"math"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

// TestSymbolShare24hAllNativeStatusComplete pins the happy path: when
// every configured platform has a complete native ticker reading, the
// share KPI must report status=complete and no CoinGecko fallback is
// consulted.
func TestSymbolShare24hAllNativeStatusComplete(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 200, Status: domain.StatusComplete})
	store.SaveVolume(domain.VolumeSnapshot{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 800, Status: domain.StatusComplete})

	share, status := store.symbolShare24hLocked("BTC-USDT (perp)")
	if status != domain.StatusComplete {
		t.Fatalf("status = %q, want complete", status)
	}
	want := 200.0 / 1000.0 * 100
	if math.Abs(share-want) > 0.001 {
		t.Fatalf("share = %v, want %v", share, want)
	}
}

// TestSymbolShare24hEdgeXBlockedFallsBackToCoinGecko reproduces the
// pro.edgex.exchange Cloudflare 403 symptom: native ticker errored out
// for edgeX while the 9 competitors still report complete. With the
// fallback, the KPI must recover edgeX's daily CoinGecko reading for
// the current UTC day and report status=partial.
func TestSymbolShare24hEdgeXBlockedFallsBackToCoinGecko(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	today := startOfUTCDay(now)

	// Native edgeX ticker fails (StatusError, vol=0). Native binance is ok.
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 0, Status: domain.StatusError, Error: "http 403: cloudflare"})
	store.SaveVolume(domain.VolumeSnapshot{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 800, Status: domain.StatusComplete})

	// CoinGecko already has today's per-symbol daily aggregate for edgeX.
	store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
		{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Day: today, Volume24HUSD: 200, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko, SnapshotTS: now},
	})

	share, status := store.symbolShare24hLocked("BTC-USDT (perp)")
	if status != domain.StatusPartial {
		t.Fatalf("status = %q, want partial", status)
	}
	want := 200.0 / 1000.0 * 100
	if math.Abs(share-want) > 0.001 {
		t.Fatalf("share = %v, want %v", share, want)
	}
}

// TestSymbolShare24hEdgeXMissingBothChannelsIsStale ensures that when
// edgeX cannot be resolved by either the native ticker or CoinGecko
// today-row, the KPI returns share=0 with status=stale rather than
// silently emitting a zero share that the UI cannot distinguish from a
// legitimate "edgeX has no volume" reading.
func TestSymbolShare24hEdgeXMissingBothChannelsIsStale(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 0, Status: domain.StatusError, Error: "http 403"})
	store.SaveVolume(domain.VolumeSnapshot{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 800, Status: domain.StatusComplete})

	share, status := store.symbolShare24hLocked("BTC-USDT (perp)")
	if status != domain.StatusStale {
		t.Fatalf("status = %q, want stale", status)
	}
	if share != 0 {
		t.Fatalf("share = %v, want 0", share)
	}
}

// TestSymbolShare24hFallbackCanonicalDailyKeyCollapsesQuote covers the
// quote-currency variance: edgeX writes per-symbol daily aggregates
// canonicalised to BASE-USDT (perp) regardless of whether the upstream
// uses USD / USDC / USDT. The fallback must hit the canonical key.
func TestSymbolShare24hFallbackCanonicalDailyKeyCollapsesQuote(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	today := startOfUTCDay(now)
	store.SaveVolume(domain.VolumeSnapshot{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 800, Status: domain.StatusComplete})

	// SaveDailyVolumeAggregates canonicalises BTC-USD → BTC-USDT before
	// landing in dailySymbolVolumes, so the fallback resolves on the
	// canonical key whatever quote variant the upstream supplied.
	store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
		{Platform: "edgeX", DisplaySymbol: "BTC-USD (perp)", Day: today, Volume24HUSD: 200, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko, SnapshotTS: now},
	})

	share, status := store.symbolShare24hLocked("BTC-USDT (perp)")
	if status != domain.StatusPartial {
		t.Fatalf("status = %q, want partial", status)
	}
	want := 200.0 / 1000.0 * 100
	if math.Abs(share-want) > 0.001 {
		t.Fatalf("share = %v, want %v", share, want)
	}
}

// TestSymbolShare24hLiquidityKPIExposesStatus is the contract test: the
// Liquidity API surface must include edgex_24h_share_status alongside
// edgex_24h_share_pct so the frontend can render "—" instead of a
// misleading 0.00% when the reading is stale.
func TestSymbolShare24hLiquidityKPIExposesStatus(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	now := time.Now().UTC()
	store.SaveVolume(domain.VolumeSnapshot{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 200, Status: domain.StatusComplete})
	store.SaveVolume(domain.VolumeSnapshot{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", SnapshotTS: now, Volume24HUSD: 800, Status: domain.StatusComplete})

	out := store.Liquidity("BTC-USDT (perp)")
	kpis, ok := out["kpis"].(map[string]any)
	if !ok {
		t.Fatalf("kpis missing or wrong type: %T", out["kpis"])
	}
	if got := kpis["edgex_24h_share_status"]; got != domain.StatusComplete {
		t.Fatalf("edgex_24h_share_status = %v, want complete", got)
	}
	if _, ok := kpis["edgex_24h_share_pct"]; !ok {
		t.Fatalf("edgex_24h_share_pct missing from kpis")
	}
}
