package liquidity

import (
	"math"
	"testing"
	"time"
)

// fakeUniverse / fakeResolver let tests avoid importing config; they
// satisfy the small interfaces defined in types.go.

type fakeUniverse struct {
	listed map[string]struct{}
}

func newFakeUniverse(symbols ...string) *fakeUniverse {
	set := make(map[string]struct{}, len(symbols))
	for _, s := range symbols {
		set[s] = struct{}{}
	}
	return &fakeUniverse{listed: set}
}

func (f *fakeUniverse) IsListed(platform, base string) bool {
	_, ok := f.listed[base]
	return ok
}

type fakeResolver struct {
	exclusive map[string]bool
}

func (f *fakeResolver) ResolveCanonical(platform, base string) string { return base }
func (f *fakeResolver) IsPlatformExclusive(canonical string) bool {
	return f.exclusive[canonical]
}

func mkRow(plat string, depth float64) PlatformDepthRow {
	return PlatformDepthRow{
		Platform:   plat,
		DepthUSD:   depth,
		SnapshotTS: time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
	}
}

func mkMatrix(rows map[string]map[string]float64) map[string]map[string]PlatformDepthRow {
	out := make(map[string]map[string]PlatformDepthRow, len(rows))
	for canonical, perPlat := range rows {
		out[canonical] = make(map[string]PlatformDepthRow, len(perPlat))
		for plat, depth := range perPlat {
			out[canonical][plat] = mkRow(plat, depth)
		}
	}
	return out
}

func defaultCfg() Config {
	return Config{
		DepthTierPct:     0.001,
		LagThreshold:     0.5,
		MinComparators:   3,
		ReissueInterval:  6 * time.Hour,
		ClearConsecutive: 3,
	}
}

func TestComputeLiquidityLagBasic(t *testing.T) {
	// 4 competitors with median = (6.0+6.0)/2 = 6.0M. edgeX = 2.4M
	// (ratio 0.4 → lag fires). edgeX is NOT last because mexc 1.0M
	// sits below it, so worst_depth does NOT fire and the test
	// isolates the lag path.
	matrix := mkMatrix(map[string]map[string]float64{
		"BTC": {
			"edgeX":   2_400_000,
			"binance": 8_500_000,
			"okx":     7_100_000,
			"bybit":   6_000_000,
			"bitget":  6_000_000,
			"mexc":    1_000_000,
		},
	})
	got := Compute(matrix, newFakeUniverse("BTC"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d: %+v", len(got), got)
	}
	c := got[0]
	if c.Kind != KindLiquidityLag {
		t.Errorf("kind = %q, want liquidity_lag", c.Kind)
	}
	if c.Canonical != "BTC" {
		t.Errorf("canonical = %q, want BTC", c.Canonical)
	}
	if math.Abs(c.MedianDepth-6_000_000) > 1 {
		t.Errorf("median = %v, want 6M", c.MedianDepth)
	}
	if math.Abs(c.Ratio-0.4) > 0.001 {
		t.Errorf("ratio = %v, want 0.4", c.Ratio)
	}
	if c.TotalPlatforms != 6 {
		t.Errorf("total_platforms = %d, want 6", c.TotalPlatforms)
	}
	// edgeX 2.4M sits between mexc 1.0M (rank 6) and bitget/bybit 6.0M
	// (rank 4 or 5). It should land at rank 5.
	if c.EdgexRank != 5 {
		t.Errorf("edgeX rank = %d, want 5", c.EdgexRank)
	}
}

func TestComputeWorstDepthOnly(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"ETH": {
			"edgeX":   1_400_000, // last place, but ratio > 0.5
			"binance": 4_000_000,
			"okx":     3_500_000,
			"bybit":   3_000_000,
			"bitget":  2_500_000, // median = (3.0+3.5)/2 = 3.25M ; edgeX/median = 0.43
		},
	})
	// ratio 0.43 < 0.5 → lag also fires. Adjust so only worst fires:
	matrix = mkMatrix(map[string]map[string]float64{
		"ETH": {
			"edgeX":   2_300_000, // ratio 2.3/3.0 = 0.77 → no lag; still last
			"binance": 4_000_000,
			"okx":     3_500_000,
			"bybit":   3_000_000,
			"bitget":  2_500_000, // median 3.0
		},
	})
	got := Compute(matrix, newFakeUniverse("ETH"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d: %+v", len(got), got)
	}
	if got[0].Kind != KindWorstDepth {
		t.Errorf("kind = %q, want worst_depth", got[0].Kind)
	}
}

// TestComputeWorstDepthSemanticsLockedToBottom freezes Phase-0 step
// §4.2 of 2026-05-29-listing-agent.md: #11 worst_depth fires iff
// edgeX is the LAST entry (edgexRank == TotalPlatforms, i.e. depth
// bottom). When edgeX sits at rank N-1 (second-to-last) the spec
// explicitly requires worst_depth NOT to fire — that's the weaker
// "second-from-bottom" signal which we intentionally do NOT alert on
// during this experiment. Lag may still fire independently; this
// test isolates by keeping the ratio above the lag threshold.
func TestComputeWorstDepthSemanticsLockedToBottom(t *testing.T) {
	t.Run("rank N (last) fires worst_depth", func(t *testing.T) {
		matrix := mkMatrix(map[string]map[string]float64{
			"BTC": {
				"binance": 5_000_000,
				"okx":     4_500_000,
				"bybit":   4_000_000,
				"bitget":  3_500_000, // median = (4.0+4.5)/2 = 4.25M
				"edgeX":   3_000_000, // ratio 3.0/4.25 ≈ 0.71 → no lag; last place → worst fires
			},
		})
		got := Compute(matrix, newFakeUniverse("BTC"), &fakeResolver{}, defaultCfg(), time.Now())
		if len(got) != 1 || got[0].Kind != KindWorstDepth {
			t.Fatalf("rank N must produce exactly one worst_depth candidate, got %+v", got)
		}
		if got[0].EdgexRank != got[0].TotalPlatforms {
			t.Fatalf("edgexRank = %d, TotalPlatforms = %d; want equal", got[0].EdgexRank, got[0].TotalPlatforms)
		}
	})

	t.Run("rank N-1 (second-to-last) does NOT fire worst_depth", func(t *testing.T) {
		matrix := mkMatrix(map[string]map[string]float64{
			"BTC": {
				"binance": 5_000_000,
				"okx":     4_500_000,
				"bybit":   4_000_000,
				"bitget":  3_500_000, // median = (4.0+4.5)/2 = 4.25M
				"edgeX":   3_000_000, // ratio 3.0/4.25 ≈ 0.71 → no lag; second-to-last
				"mexc":    2_500_000, // pushes edgeX up to rank 5 (N-1); mexc at rank 6 (N)
			},
		})
		got := Compute(matrix, newFakeUniverse("BTC"), &fakeResolver{}, defaultCfg(), time.Now())
		for _, c := range got {
			if c.Kind == KindWorstDepth {
				t.Fatalf("edgeX at rank N-1 must NOT fire worst_depth, got %+v", c)
			}
		}
	})
}

func TestComputeBothKindsAtOnce(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"SOL": {
			"edgeX":   500_000, // tiny + last
			"binance": 3_000_000,
			"okx":     2_500_000,
			"bybit":   2_000_000,
			"bitget":  1_500_000, // median 2.25
		},
	})
	got := Compute(matrix, newFakeUniverse("SOL"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(got))
	}
	if got[0].Kind != KindLiquidityLag || got[1].Kind != KindWorstDepth {
		t.Errorf("ordering wrong: %v / %v", got[0].Kind, got[1].Kind)
	}
}

func TestComputeMinComparatorsGuard(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"BTC": {
			"edgeX":   500_000,
			"binance": 5_000_000,
			"okx":     5_000_000, // only 2 competitors → < MinComparators(3)
		},
	})
	got := Compute(matrix, newFakeUniverse("BTC"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 0 {
		t.Fatalf("want 0 candidates (insufficient comparators), got %d", len(got))
	}
}

func TestComputeSkipsExclusive(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"HYPE_INDEX": {
			"edgeX":       100,
			"hyperliquid": 50_000,
			"okx":         40_000,
			"bybit":       30_000,
		},
	})
	resolver := &fakeResolver{exclusive: map[string]bool{"HYPE_INDEX": true}}
	got := Compute(matrix, newFakeUniverse("HYPE_INDEX"), resolver, defaultCfg(), time.Now())
	if len(got) != 0 {
		t.Fatalf("exclusive canonical must be skipped, got %+v", got)
	}
}

func TestComputeSkipsNonListed(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"BTC": {
			"edgeX":   1_000_000,
			"binance": 5_000_000,
			"okx":     4_000_000,
			"bybit":   3_000_000,
		},
	})
	// BTC is NOT in the listed universe → skip even though the
	// depth matrix has an edgeX row.
	got := Compute(matrix, newFakeUniverse( /* empty */ ), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 0 {
		t.Fatalf("non-listed canonical must be skipped, got %+v", got)
	}
}

func TestComputeSkipsMissingEdgex(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"BTC": {
			"binance": 5_000_000,
			"okx":     4_000_000,
			"bybit":   3_000_000,
			"bitget":  2_000_000,
		},
	})
	got := Compute(matrix, newFakeUniverse("BTC"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 0 {
		t.Fatalf("missing edgeX row must yield no candidates, got %+v", got)
	}
}

func TestComputeSkipsZeroDepthEdgex(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"BTC": {
			"edgeX":   0,
			"binance": 5_000_000,
			"okx":     4_000_000,
			"bybit":   3_000_000,
			"bitget":  2_000_000,
		},
	})
	got := Compute(matrix, newFakeUniverse("BTC"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 0 {
		t.Fatalf("edgeX depth=0 must yield no candidates, got %+v", got)
	}
}

func TestMedianDepthOddVsEven(t *testing.T) {
	odd := []PlatformDepthRow{
		mkRow("a", 100), mkRow("b", 200), mkRow("c", 300),
	}
	if got := medianDepth(odd); got != 200 {
		t.Errorf("odd median = %v, want 200", got)
	}
	even := []PlatformDepthRow{
		mkRow("a", 100), mkRow("b", 200), mkRow("c", 300), mkRow("d", 400),
	}
	if got := medianDepth(even); got != 250 {
		t.Errorf("even median = %v, want 250", got)
	}
}

func TestFormatTierLabel(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.001, "0.1%"},
		{0.0005, "0.05%"},
		{0.01, "1%"},
		{0.02, "2%"},
		{0, ""},
	}
	for _, tc := range cases {
		got := formatTierLabel(tc.in)
		if got != tc.want {
			t.Errorf("formatTierLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestComputeOutputDeterministicOrder(t *testing.T) {
	matrix := mkMatrix(map[string]map[string]float64{
		"BTC": {"edgeX": 100, "binance": 1000, "okx": 900, "bybit": 800, "bitget": 700},
		"ETH": {"edgeX": 100, "binance": 1000, "okx": 900, "bybit": 800, "bitget": 700},
		"SOL": {"edgeX": 100, "binance": 1000, "okx": 900, "bybit": 800, "bitget": 700},
	})
	got := Compute(matrix, newFakeUniverse("BTC", "ETH", "SOL"), &fakeResolver{}, defaultCfg(), time.Now())
	if len(got) != 6 {
		t.Fatalf("want 6 candidates (3 canonicals × 2 kinds), got %d", len(got))
	}
	// canonical-sorted, lag before worst per canonical
	wantOrder := []string{
		"BTC|liquidity_lag", "BTC|worst_depth",
		"ETH|liquidity_lag", "ETH|worst_depth",
		"SOL|liquidity_lag", "SOL|worst_depth",
	}
	for i, w := range wantOrder {
		got_i := got[i].Canonical + "|" + string(got[i].Kind)
		if got_i != w {
			t.Errorf("idx %d = %q, want %q", i, got_i, w)
		}
	}
}
