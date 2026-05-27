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

func TestEnrichFundingRankRowsAssignsAscending(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("binance", ptr(0.0090), domain.StatusComplete),
		makeRow("okx", ptr(0.0120), domain.StatusComplete),
		makeRow("bybit", ptr(0.0060), domain.StatusComplete),
		makeRow("edgeX", ptr(0.0050), domain.StatusComplete),
	}
	rows = enrichFundingRankRows(rows)
	want := map[string]int{
		"edgeX":   1,
		"bybit":   2,
		"binance": 3,
		"okx":     4,
	}
	for _, row := range rows {
		if row.Funding == nil {
			t.Fatalf("%s funding should not be nil", row.Platform)
		}
		if got := row.Funding.Rank; got != want[row.Platform] {
			t.Fatalf("%s rank = %d, want %d", row.Platform, got, want[row.Platform])
		}
	}
}

func TestEnrichFundingRankRowsSkipsIncompleteAndMissing(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("binance", ptr(0.0090), domain.StatusComplete),
		makeRow("bingx", nil, domain.StatusUnsupported),
		makeRow("mexc", ptr(0.0010), domain.StatusStale),
		{Platform: "kraken", DisplaySymbol: "BTC-USDT (perp)"}, // funding nil
		makeRow("edgeX", ptr(0.0050), domain.StatusComplete),
	}
	rows = enrichFundingRankRows(rows)
	// edgeX 0.0050 → rank 1; binance 0.0090 → rank 2.
	// bingx (unsupported), mexc (stale), kraken (no funding) stay at rank 0.
	for _, row := range rows {
		switch row.Platform {
		case "edgeX":
			if row.Funding.Rank != 1 {
				t.Fatalf("edgeX rank = %d, want 1", row.Funding.Rank)
			}
		case "binance":
			if row.Funding.Rank != 2 {
				t.Fatalf("binance rank = %d, want 2", row.Funding.Rank)
			}
		case "bingx", "mexc":
			if row.Funding.Rank != 0 {
				t.Fatalf("%s rank = %d, want 0 (incomplete should not be ranked)", row.Platform, row.Funding.Rank)
			}
		case "kraken":
			if row.Funding != nil {
				t.Fatalf("kraken funding should remain nil, got %+v", row.Funding)
			}
		}
	}
}

func TestEnrichFundingRankRowsStableOnTies(t *testing.T) {
	rows := []domain.PlatformSnapshot{
		makeRow("binance", ptr(0.0050), domain.StatusComplete),
		makeRow("okx", ptr(0.0050), domain.StatusComplete),
		makeRow("bybit", ptr(0.0050), domain.StatusComplete),
	}
	rows = enrichFundingRankRows(rows)
	// SliceStable preserves input order on equal keys.
	want := map[string]int{
		"binance": 1,
		"okx":     2,
		"bybit":   3,
	}
	for _, row := range rows {
		if got := row.Funding.Rank; got != want[row.Platform] {
			t.Fatalf("%s rank = %d, want %d (stable order on ties)", row.Platform, got, want[row.Platform])
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
