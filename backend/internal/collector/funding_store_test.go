package collector

import (
	"math"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/domain"
)

func ptr(v float64) *float64 { return &v }

func makeRow(platform string, rate8h *float64, status string) domain.PlatformSnapshot {
	row := domain.PlatformSnapshot{
		Platform:      platform,
		DisplaySymbol: "BTC-USDT (perp)",
		DepthByTier:   map[string]domain.DepthMetrics{},
	}
	row.Funding = &domain.PlatformFundingRate{
		Platform:      platform,
		DisplaySymbol: "BTC-USDT (perp)",
		Status:        status,
		PeriodHours:   8,
	}
	if rate8h != nil {
		row.Funding.Rate8h = ptr(*rate8h)
		row.Funding.RateNative = ptr(*rate8h)
	}
	return row
}

func TestCompetitorFundingMedian8hHappyPath(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("edgeX", ptr(0.01), domain.StatusComplete), // excluded
		makeRow("binance", ptr(0.003), domain.StatusComplete),
		makeRow("okx", ptr(0.001), domain.StatusComplete),
		makeRow("bybit", ptr(0.005), domain.StatusComplete),
	}
	med, samples, status := competitorFundingMedian8h(rows)
	if status != domain.StatusComplete {
		t.Fatalf("status = %q, want complete", status)
	}
	if samples != 3 {
		t.Fatalf("samples = %d, want 3 (edgeX excluded)", samples)
	}
	if math.Abs(med-0.003) > 1e-9 {
		t.Fatalf("median = %v, want 0.003", med)
	}
}

func TestCompetitorFundingMedian8hExcludesEdgeXEvenWhenIncluded(t *testing.T) {
	// All competitors share 0.001 funding; edgeX has 100x value; if
	// median accidentally included edgeX, the value would jump.
	rows := []domain.PlatformSnapshot{
		makeRow("edgeX", ptr(0.1), domain.StatusComplete),
		makeRow("binance", ptr(0.001), domain.StatusComplete),
		makeRow("okx", ptr(0.001), domain.StatusComplete),
		makeRow("bybit", ptr(0.001), domain.StatusComplete),
	}
	med, _, _ := competitorFundingMedian8h(rows)
	if math.Abs(med-0.001) > 1e-9 {
		t.Fatalf("median = %v, want 0.001 (edgeX must be excluded)", med)
	}
}

func TestCompetitorFundingMedian8hReturnsStaleWhenSamplesBelow3(t *testing.T) {
	cases := []struct {
		name        string
		rows        []domain.PlatformSnapshot
		wantSamples int
	}{
		{"zero samples", []domain.PlatformSnapshot{}, 0},
		{"one sample", []domain.PlatformSnapshot{makeRow("binance", ptr(0.001), domain.StatusComplete)}, 1},
		{"two samples", []domain.PlatformSnapshot{
			makeRow("binance", ptr(0.001), domain.StatusComplete),
			makeRow("okx", ptr(0.002), domain.StatusComplete),
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			med, samples, status := competitorFundingMedian8h(tc.rows)
			if status != domain.StatusStale {
				t.Fatalf("status = %q, want stale", status)
			}
			if samples != tc.wantSamples {
				t.Fatalf("samples = %d, want %d", samples, tc.wantSamples)
			}
			if med != 0 {
				t.Fatalf("median = %v, want 0 (stale)", med)
			}
		})
	}
}

func TestCompetitorFundingMedian8hIgnoresStaleSamples(t *testing.T) {
	// Even with 4 competitor rows, if 2 are stale we have only 2 complete
	// samples → median should fall back to stale per decision F.
	rows := []domain.PlatformSnapshot{
		makeRow("edgeX", ptr(0.01), domain.StatusComplete),
		makeRow("binance", ptr(0.001), domain.StatusComplete),
		makeRow("okx", ptr(0.002), domain.StatusComplete),
		makeRow("bybit", nil, domain.StatusStale),
		makeRow("bitget", nil, domain.StatusStale),
	}
	_, samples, status := competitorFundingMedian8h(rows)
	if status != domain.StatusStale {
		t.Fatalf("status = %q, want stale (only 2 complete competitor samples)", status)
	}
	if samples != 2 {
		t.Fatalf("samples = %d, want 2", samples)
	}
}

func TestEnrichFundingVsMedianRowsSetsDelta(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("edgeX", ptr(0.01), domain.StatusComplete),
		makeRow("binance", ptr(0.003), domain.StatusComplete),
		makeRow("okx", ptr(0.001), domain.StatusComplete),
		makeRow("bybit", ptr(0.005), domain.StatusComplete),
	}
	med, _, status := competitorFundingMedian8h(rows)
	rows = enrichFundingVsMedianRows(rows, med, status)
	if rows[0].Funding.VsMedian8h == nil {
		t.Fatalf("edgeX VsMedian8h should be set")
	}
	if got := *rows[0].Funding.VsMedian8h; math.Abs(got-(0.01-0.003)) > 1e-9 {
		t.Fatalf("edgeX VsMedian8h = %v, want %v", got, 0.01-0.003)
	}
	for _, row := range rows[1:] {
		if row.Funding.VsMedian8h == nil {
			t.Fatalf("%s VsMedian8h should be set", row.Platform)
		}
	}
}

func TestEnrichFundingVsMedianRowsSkipsWhenMedianStale(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("binance", ptr(0.003), domain.StatusComplete),
		makeRow("okx", ptr(0.001), domain.StatusComplete),
	}
	_, _, status := competitorFundingMedian8h(rows)
	if status != domain.StatusStale {
		t.Fatalf("precondition: status = %q, want stale", status)
	}
	rows = enrichFundingVsMedianRows(rows, 0, status)
	for _, row := range rows {
		if row.Funding.VsMedian8h != nil {
			t.Fatalf("%s VsMedian8h should be nil when median is stale", row.Platform)
		}
	}
}

func TestEnrichFundingVsMedianRowsPreservesRowsWithoutFunding(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)"},
		makeRow("okx", ptr(0.001), domain.StatusComplete),
		makeRow("bybit", ptr(0.002), domain.StatusComplete),
		makeRow("bitget", ptr(0.003), domain.StatusComplete),
	}
	med, _, status := competitorFundingMedian8h(rows)
	rows = enrichFundingVsMedianRows(rows, med, status)
	if rows[0].Funding != nil {
		t.Fatalf("row without funding should remain nil, got %+v", rows[0].Funding)
	}
}

func TestStoreAttachFundingLockedFillsFromMap(t *testing.T) {
	store := newFundingTestStore()
	now := time.Now().UTC()
	store.SaveFundingRates([]domain.PlatformFundingRate{{
		Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", PeriodHours: 8, Rate8h: ptr(0.003), Status: domain.StatusComplete, SnapshotTS: &now,
	}})
	rows := []domain.PlatformSnapshot{
		{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)"},
		{Platform: "okx", DisplaySymbol: "BTC-USDT (perp)"}, // no funding stored
	}
	store.mu.RLock()
	out := store.attachFundingLocked(rows)
	store.mu.RUnlock()
	if out[0].Funding == nil || out[0].Funding.Status != domain.StatusComplete {
		t.Fatalf("binance row should have funding, got %+v", out[0].Funding)
	}
	if out[1].Funding != nil {
		t.Fatalf("okx row without funding should remain nil, got %+v", out[1].Funding)
	}
}

func intPtr(v int) *int { return &v }

func TestEnrichFundingRankBySignSplitsCohorts(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("binance", ptr(0.0090), domain.StatusComplete),
		makeRow("okx", ptr(0.0120), domain.StatusComplete),
		makeRow("bybit", ptr(-0.0060), domain.StatusComplete),
		makeRow("hyperliquid", ptr(-0.0030), domain.StatusComplete),
		makeRow("edgeX", ptr(0.0050), domain.StatusComplete),
	}
	rows = enrichFundingRankBySignRows(rows)

	// Positive cohort sorted desc by rate: okx 0.012 → 1, binance 0.009 → 2, edgeX 0.005 → 3.
	// Negative cohort sorted asc by rate: bybit -0.006 → 1, hyperliquid -0.003 → 2.
	wantPos := map[string]*int{
		"okx":     intPtr(1),
		"binance": intPtr(2),
		"edgeX":   intPtr(3),
	}
	wantNeg := map[string]*int{
		"bybit":       intPtr(1),
		"hyperliquid": intPtr(2),
	}
	for _, row := range rows {
		f := row.Funding
		if f == nil {
			t.Fatalf("%s funding nil", row.Platform)
		}
		if want, ok := wantPos[row.Platform]; ok {
			if f.RankPositive == nil || *f.RankPositive != *want {
				t.Fatalf("%s RankPositive = %v, want %d", row.Platform, f.RankPositive, *want)
			}
			if f.RankNegative != nil {
				t.Fatalf("%s in positive cohort but RankNegative = %v (want nil)", row.Platform, *f.RankNegative)
			}
		}
		if want, ok := wantNeg[row.Platform]; ok {
			if f.RankNegative == nil || *f.RankNegative != *want {
				t.Fatalf("%s RankNegative = %v, want %d", row.Platform, f.RankNegative, *want)
			}
			if f.RankPositive != nil {
				t.Fatalf("%s in negative cohort but RankPositive = %v (want nil)", row.Platform, *f.RankPositive)
			}
		}
	}
}

func TestEnrichFundingRankBySignSkipsIncompleteMissingAndZero(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("binance", ptr(0.0090), domain.StatusComplete),
		makeRow("bingx", nil, domain.StatusUnsupported),
		makeRow("mexc", ptr(0.0010), domain.StatusStale),
		makeRow("lighter", ptr(0.0), domain.StatusComplete),    // exactly zero → neither cohort
		{Platform: "kraken", DisplaySymbol: "BTC-USDT (perp)"}, // funding nil
		makeRow("edgeX", ptr(0.0050), domain.StatusComplete),
	}
	rows = enrichFundingRankBySignRows(rows)
	for _, row := range rows {
		f := row.Funding
		switch row.Platform {
		case "edgeX":
			// edgeX 0.0050 is the smaller positive → rank 2 (binance 0.009 ranks 1)
			if f.RankPositive == nil || *f.RankPositive != 2 {
				t.Fatalf("edgeX RankPositive = %v, want 2", f.RankPositive)
			}
			if f.RankNegative != nil {
				t.Fatalf("edgeX RankNegative should be nil, got %v", *f.RankNegative)
			}
		case "binance":
			if f.RankPositive == nil || *f.RankPositive != 1 {
				t.Fatalf("binance RankPositive = %v, want 1", f.RankPositive)
			}
		case "bingx", "mexc":
			if f.RankPositive != nil || f.RankNegative != nil {
				t.Fatalf("%s should have no rank (status != complete), got pos=%v neg=%v", row.Platform, f.RankPositive, f.RankNegative)
			}
		case "lighter":
			if f.RankPositive != nil || f.RankNegative != nil {
				t.Fatalf("lighter rate exactly 0 should have no rank, got pos=%v neg=%v", f.RankPositive, f.RankNegative)
			}
		case "kraken":
			if f != nil {
				t.Fatalf("kraken funding should remain nil, got %+v", f)
			}
		}
	}
}

func TestEnrichFundingRankBySignUses1224Ties(t *testing.T) {
	// Four venues tied at 0.01, two cleanly separated values below.
	// 1224 standard: tied venues share rank 1, next non-tied jumps to 5.
	rows := []domain.PlatformSnapshot{
		makeRow("edgeX", ptr(0.0100), domain.StatusComplete),
		makeRow("okx", ptr(0.0100), domain.StatusComplete),
		makeRow("bingx", ptr(0.0100), domain.StatusComplete),
		makeRow("gate", ptr(0.0100), domain.StatusComplete),
		makeRow("mexc", ptr(0.0071), domain.StatusComplete),
		makeRow("binance", ptr(0.0070), domain.StatusComplete),
	}
	rows = enrichFundingRankBySignRows(rows)
	want := map[string]int{
		"edgeX":   1,
		"okx":     1,
		"bingx":   1,
		"gate":    1,
		"mexc":    5, // skipped 2/3/4 occupied by tie cohort
		"binance": 6,
	}
	for _, row := range rows {
		f := row.Funding
		got := 0
		if f != nil && f.RankPositive != nil {
			got = *f.RankPositive
		}
		if got != want[row.Platform] {
			t.Fatalf("%s RankPositive = %d, want %d (1224 tie rule)", row.Platform, got, want[row.Platform])
		}
	}
}

func TestFindPlatformRow(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		{Platform: "binance"},
		{Platform: "edgeX"},
		{Platform: "okx"},
	}
	if got := findPlatformRow(rows, "edgeX"); got == nil || got.Platform != "edgeX" {
		t.Fatalf("findPlatformRow(edgeX) = %+v, want edgeX row", got)
	}
	if got := findPlatformRow(rows, "missing"); got != nil {
		t.Fatalf("findPlatformRow(missing) = %+v, want nil", got)
	}
}
