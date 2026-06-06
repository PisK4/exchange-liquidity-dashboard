package collector

import (
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
)

// qualityRow builds a minimal PlatformSnapshot fixture for the
// enrichQualityVsMedianRows tests.
func qualityRow(platform string, status string, spreadBP float64, slippage map[string]float64) domain.PlatformSnapshot {
	return domain.PlatformSnapshot{
		Platform:        platform,
		DisplaySymbol:   "BTC-USDT (perp)",
		SnapshotTS:      time.Now().UTC(),
		DepthStatus:     status,
		SpreadBP:        spreadBP,
		WorstSlippageBP: slippage,
	}
}

func TestEnrichQualityVsMedianRowsPublishesSignedSpreadDiff(t *testing.T) {
	// Competitor spreads: 2, 3, 4 → median = 3.
	// edgeX spread = 1 → diff = 1 - 3 = -2 (better than median).
	rows := []domain.PlatformSnapshot{
		qualityRow("edgeX", domain.StatusComplete, 1.0, nil),
		qualityRow("binance", domain.StatusComplete, 2.0, nil),
		qualityRow("okx", domain.StatusComplete, 3.0, nil),
		qualityRow("bybit", domain.StatusComplete, 4.0, nil),
	}
	rows = enrichQualityVsMedianRows(rows)
	wantSpread := map[string]float64{
		"edgeX":   -2.0,
		"binance": -1.0,
		"okx":     0.0,
		"bybit":   1.0,
	}
	for _, row := range rows {
		want, ok := wantSpread[row.Platform]
		if !ok {
			continue
		}
		if row.VsMedianSpreadBP == nil {
			t.Fatalf("%s VsMedianSpreadBP nil, want %v", row.Platform, want)
		}
		if *row.VsMedianSpreadBP != want {
			t.Fatalf("%s VsMedianSpreadBP = %v, want %v", row.Platform, *row.VsMedianSpreadBP, want)
		}
	}
}

func TestEnrichQualityVsMedianRowsRequiresThreeCompetitorSamples(t *testing.T) {
	// Only 2 competitors with data → median undefined → all vs_median nil.
	rows := []domain.PlatformSnapshot{
		qualityRow("edgeX", domain.StatusComplete, 1.0, nil),
		qualityRow("binance", domain.StatusComplete, 2.0, nil),
		qualityRow("okx", domain.StatusComplete, 3.0, nil),
		// bybit stale → not in cohort
		qualityRow("bybit", domain.StatusStale, 4.0, nil),
	}
	rows = enrichQualityVsMedianRows(rows)
	for _, row := range rows {
		if row.VsMedianSpreadBP != nil {
			t.Fatalf("%s VsMedianSpreadBP = %v, want nil (only 2 competitor samples)", row.Platform, *row.VsMedianSpreadBP)
		}
	}
}

func TestEnrichQualityVsMedianRowsExcludesEdgeXFromCohort(t *testing.T) {
	// If edgeX were in the cohort, its outlier value would skew the
	// median. Competitors: 2, 3, 4 → median = 3.
	// If edgeX were included: samples = [0.1, 2, 3, 4] → median = 2.5.
	// We expect the competitor-only median = 3, so edgeX diff = -2.9.
	rows := []domain.PlatformSnapshot{
		qualityRow("edgeX", domain.StatusComplete, 0.1, nil),
		qualityRow("binance", domain.StatusComplete, 2.0, nil),
		qualityRow("okx", domain.StatusComplete, 3.0, nil),
		qualityRow("bybit", domain.StatusComplete, 4.0, nil),
	}
	rows = enrichQualityVsMedianRows(rows)
	for _, row := range rows {
		if row.Platform != "edgeX" {
			continue
		}
		if row.VsMedianSpreadBP == nil {
			t.Fatalf("edgeX VsMedianSpreadBP nil")
		}
		// -2.9 (edgeX excluded from cohort) vs -2.4 (edgeX included).
		got := *row.VsMedianSpreadBP
		want := -2.9
		if got != want {
			t.Fatalf("edgeX VsMedianSpreadBP = %v, want %v (edgeX must NOT be in median cohort)", got, want)
		}
	}
}

func TestEnrichQualityVsMedianRowsPerBucketSlippage(t *testing.T) {
	// 50K bucket: competitors [1, 2, 3] → median = 2. edgeX 0.5 → diff = -1.5.
	// 100K bucket: competitors [3, 4, 5] → median = 4. edgeX 2 → diff = -2.
	// 500K bucket: competitors [10, 12, 14] → median = 12. edgeX 8 → diff = -4.
	rows := []domain.PlatformSnapshot{
		qualityRow("edgeX", domain.StatusComplete, 1.0, map[string]float64{"50000": 0.5, "100000": 2.0, "500000": 8.0}),
		qualityRow("binance", domain.StatusComplete, 2.0, map[string]float64{"50000": 1, "100000": 3, "500000": 10}),
		qualityRow("okx", domain.StatusComplete, 3.0, map[string]float64{"50000": 2, "100000": 4, "500000": 12}),
		qualityRow("bybit", domain.StatusComplete, 4.0, map[string]float64{"50000": 3, "100000": 5, "500000": 14}),
	}
	rows = enrichQualityVsMedianRows(rows)
	for _, row := range rows {
		if row.Platform != "edgeX" {
			continue
		}
		want := map[string]float64{"50000": -1.5, "100000": -2.0, "500000": -4.0}
		for bucket, w := range want {
			got, ok := row.VsMedianSlippageBP[bucket]
			if !ok {
				t.Fatalf("edgeX VsMedianSlippageBP[%s] missing, want %v", bucket, w)
			}
			if got != w {
				t.Fatalf("edgeX VsMedianSlippageBP[%s] = %v, want %v", bucket, got, w)
			}
		}
	}
}

func TestEnrichQualityVsMedianRowsHandlesEmptyInput(t *testing.T) {
	rows := []domain.PlatformSnapshot{}
	got := enrichQualityVsMedianRows(rows)
	if len(got) != 0 {
		t.Fatalf("empty input should produce empty output, got %d rows", len(got))
	}
}
