package collector

import (
	"math"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
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

// TestTop30Window7dFromDailyHistory exercises the read-path window
// aggregation: when 7 contiguous UTC days of daily aggregates exist for a
// Top30 row's (platform, symbol), Top30() must promote Volume7DStatus to
// complete and produce a sum matching the seeded values. Previous-week
// data is also seeded so the 7d Δ surfaces a non-nil percentage. MEXC×0.4
// discount is NOT applied here because the Top30 24h column is also
// reported raw; the column must stay self-consistent.
func TestTop30Window7dFromDailyHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	for i := 7; i < 14; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 800, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	store.SaveTop30("binance", []domain.Top30Row{
		{Rank: 1, Platform: "binance", Symbol: "BTC-USDT (perp)", Volume24HUSD: 1234, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko, SnapshotTS: time.Now().UTC(), Volume7DStatus: domain.StatusInsufficientHistory, Delta7DStatus: domain.StatusInsufficientHistory},
	})
	out := store.Top30("perp", "binance")
	rows := out["rows"].([]domain.Top30Row)
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0].Volume7DStatus != domain.StatusComplete {
		t.Fatalf("Volume7DStatus=%s want complete", rows[0].Volume7DStatus)
	}
	if rows[0].Volume7DUSD == nil || math.Abs(*rows[0].Volume7DUSD-7000) > 1e-6 {
		t.Fatalf("Volume7DUSD=%+v want 7000", rows[0].Volume7DUSD)
	}
	if rows[0].Delta7DStatus != domain.StatusComplete {
		t.Fatalf("Delta7DStatus=%s want complete", rows[0].Delta7DStatus)
	}
	if rows[0].Delta7DPct == nil {
		t.Fatalf("Delta7DPct nil")
	}
	wantDelta := (7000.0 - 5600.0) / 5600.0 * 100
	if math.Abs(*rows[0].Delta7DPct-wantDelta) > 1e-6 {
		t.Fatalf("Delta7DPct=%v want %v", *rows[0].Delta7DPct, wantDelta)
	}
}

// TestTop30Window7dInsufficient ensures that with only 4 of 7 days seeded,
// the row stays in insufficient_history and the numeric pointers are
// cleared (not stale from a previous SaveTop30 call).
func TestTop30Window7dInsufficient(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 4; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 1000, Status: domain.StatusComplete, DataSource: domain.DataSourceNative},
		})
	}
	store.SaveTop30("binance", []domain.Top30Row{
		{Rank: 1, Platform: "binance", Symbol: "BTC-USDT (perp)", Volume24HUSD: 1234, Status: domain.StatusComplete, DataSource: domain.DataSourceCoinGecko, SnapshotTS: time.Now().UTC(), Volume7DStatus: domain.StatusInsufficientHistory, Delta7DStatus: domain.StatusInsufficientHistory},
	})
	out := store.Top30("perp", "binance")
	rows := out["rows"].([]domain.Top30Row)
	if rows[0].Volume7DStatus != domain.StatusInsufficientHistory {
		t.Fatalf("Volume7DStatus=%s want insufficient_history", rows[0].Volume7DStatus)
	}
	if rows[0].Volume7DUSD != nil {
		t.Fatalf("Volume7DUSD=%v want nil", rows[0].Volume7DUSD)
	}
	if rows[0].Delta7DPct != nil {
		t.Fatalf("Delta7DPct=%v want nil", rows[0].Delta7DPct)
	}
}

// TestTop30RosterUnionReturnsBases sanity-checks the helper that feeds the
// Top30Backfiller: it must collapse "BTC-USDT (perp)" / "ETH-USDT (perp)"
// to BTC / ETH and dedup across rounds within one snapshot.
func TestTop30RosterUnionReturnsBases(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"binance", "okx"}
	store := NewStore(cfg)
	store.SaveTop30("binance", []domain.Top30Row{
		{Rank: 1, Platform: "binance", Symbol: "BTC-USDT (perp)", Volume24HUSD: 1, Status: domain.StatusComplete, SnapshotTS: time.Now().UTC()},
		{Rank: 2, Platform: "binance", Symbol: "ETH-USDT (perp)", Volume24HUSD: 1, Status: domain.StatusComplete, SnapshotTS: time.Now().UTC()},
	})
	store.SaveTop30("okx", []domain.Top30Row{
		{Rank: 1, Platform: "okx", Symbol: "BTC-USDT (perp)", Volume24HUSD: 1, Status: domain.StatusComplete, SnapshotTS: time.Now().UTC()},
		{Rank: 2, Platform: "okx", Symbol: "1000PEPE-USDT (perp)", Volume24HUSD: 1, Status: domain.StatusComplete, SnapshotTS: time.Now().UTC()},
	})
	roster := store.Top30RosterUnion()
	if got := roster["binance"]; len(got) != 2 || got[0].BaseAsset != "BTC" || got[1].BaseAsset != "ETH" || got[0].DisplaySymbol != "BTC-USDT (perp)" || got[1].DisplaySymbol != "ETH-USDT (perp)" {
		t.Fatalf("binance roster=%+v want [BTC,BTC-USDT (perp); ETH,ETH-USDT (perp)]", got)
	}
	if got := roster["okx"]; len(got) != 2 || got[0].BaseAsset != "1000PEPE" || got[1].BaseAsset != "BTC" {
		t.Fatalf("okx roster=%+v want [1000PEPE BTC]", got)
	}
}

// TestEdgeXBTCUSDFoldsIntoCanonicalKey pins the regression that produced
// 'partial' status on the Liquidity tab: edgeX's native ticker is
// BTC-USD (perp), but cfg.Symbols / V1 KPIs query by canonical
// BTC-USDT (perp). Both display variants must collapse onto the same
// in-memory bucket so a 7-contiguous-day edgeX history satisfies the
// share7d completeness gate. The TOP30 row's user-facing Symbol stays
// the platform-native name; only the daily-aggregate key is canonical.
func TestEdgeXBTCUSDFoldsIntoCanonicalKey(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance"}
	store := NewStore(cfg)
	today := startOfUTCDay(time.Now().UTC())
	for i := 0; i < 7; i++ {
		store.SaveDailyVolumeAggregates([]domain.DailyVolumeAggregate{
			// edgeX writes its native '-USD' suffix...
			{Platform: "edgeX", DisplaySymbol: "BTC-USD (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 100, Status: domain.StatusComplete, DataSource: domain.DataSourceNativeBackfill},
			// ...others write the canonical '-USDT' suffix.
			{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Day: today.AddDate(0, 0, -i), Volume24HUSD: 900, Status: domain.StatusComplete, DataSource: domain.DataSourceNativeBackfill},
		})
	}
	_, status7d, _ := store.symbolShare7dLocked("BTC-USDT (perp)")
	if status7d != domain.StatusComplete {
		t.Fatalf("expected complete (canonical fold succeeded), got %s", status7d)
	}
	// The TOP30 enrichment lookup must also succeed via the canonical
	// fold even when row.Symbol carries the platform-native '-USD' name.
	store.SaveTop30("edgeX", []domain.Top30Row{
		{Rank: 1, Platform: "edgeX", Symbol: "BTC-USD (perp)", Volume24HUSD: 100, Status: domain.StatusComplete, SnapshotTS: time.Now().UTC(), Volume7DStatus: domain.StatusInsufficientHistory, Delta7DStatus: domain.StatusInsufficientHistory},
	})
	out := store.Top30("perp", "edgeX")
	rows := out["rows"].([]domain.Top30Row)
	if rows[0].Volume7DStatus != domain.StatusComplete {
		t.Fatalf("expected complete 7d on edgeX BTC-USD via canonical fold, got %s", rows[0].Volume7DStatus)
	}
	if rows[0].Volume7DUSD == nil || *rows[0].Volume7DUSD != 700 {
		t.Fatalf("expected 7d sum = 700 (7*100), got %v", rows[0].Volume7DUSD)
	}
}

// TestCanonicalDailyKey covers the symbol-suffix collapse helper directly.
func TestCanonicalDailyKey(t *testing.T) {
	cases := map[string]string{
		"BTC-USDT (perp)":      "BTC-USDT (perp)",
		"BTC-USD (perp)":       "BTC-USDT (perp)",
		"BTC-USDC (perp)":      "BTC-USDT (perp)",
		"1000PEPE-USDT (perp)": "1000PEPE-USDT (perp)",
		"":                     "",
		"NOT-A-PERP":           "NOT-A-PERP",
		"BTC-EUR (perp)":       "BTC-EUR (perp)", // unknown quote: passthrough
	}
	for in, want := range cases {
		if got := canonicalDailyKey(in); got != want {
			t.Errorf("canonicalDailyKey(%q) = %q, want %q", in, got, want)
		}
	}
}
