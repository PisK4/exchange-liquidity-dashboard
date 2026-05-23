package domain

import (
	"strings"
	"testing"
)

func TestEnforceTierMonotonicityBidViolation(t *testing.T) {
	row := &PlatformSnapshot{
		Platform: "bitget",
		DepthByTier: map[string]DepthMetrics{
			"0.05%": {BidUSD: 100, AskUSD: 100, TotalUSD: 200, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"0.10%": {BidUSD: 200, AskUSD: 150, TotalUSD: 350, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"1.00%": {BidUSD: 50, AskUSD: 600, TotalUSD: 650, DepthStatus: StatusAggregatedOrderbook, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyAggregatedStrict},
			"2.00%": {BidUSD: 250, AskUSD: 1000, TotalUSD: 1250, DepthStatus: StatusAggregatedOrderbook, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyAggregatedStrict},
		},
	}
	EnforceTierMonotonicity(row)

	got := row.DepthByTier["1.00%"]
	if got.BidUSD != 200 {
		t.Fatalf("1.00%% bid_usd should be clamped to 200, got %v", got.BidUSD)
	}
	if got.AskUSD != 600 {
		t.Fatalf("1.00%% ask_usd should stay 600, got %v", got.AskUSD)
	}
	if got.TotalUSD != 800 {
		t.Fatalf("1.00%% total_usd should be recomputed to 800, got %v", got.TotalUSD)
	}
	if got.DepthStatus != StatusPartial {
		t.Fatalf("1.00%% depth_status should be partial, got %v", got.DepthStatus)
	}
	if got.PolicyAcceptance != PolicyLooseLowerBound {
		t.Fatalf("1.00%% policy should be loose_lower_bound, got %v", got.PolicyAcceptance)
	}
	if got.StrictComplete {
		t.Fatalf("1.00%% strict_complete should be false")
	}
	if !strings.Contains(got.PartialReason, ReasonMonotonicityLowerBound) {
		t.Fatalf("1.00%% partial_reason should contain monotonicity_lower_bound, got %v", got.PartialReason)
	}

	// 2.00% bid_usd was 250 and is now lifted to at least the corrected 1.00% bid (200), but
	// since 250 >= 200, no correction is applied; ask_usd 1000 >= 600 unchanged.
	deep := row.DepthByTier["2.00%"]
	if deep.BidUSD != 250 || deep.AskUSD != 1000 {
		t.Fatalf("2.00%% values should remain (250, 1000), got (%v, %v)", deep.BidUSD, deep.AskUSD)
	}
	if deep.PartialReason != "" {
		t.Fatalf("2.00%% partial_reason should remain empty, got %v", deep.PartialReason)
	}
}

func TestEnforceTierMonotonicityCascades(t *testing.T) {
	row := &PlatformSnapshot{
		Platform: "bitget",
		DepthByTier: map[string]DepthMetrics{
			"0.10%": {BidUSD: 1_000_000, AskUSD: 500_000, TotalUSD: 1_500_000, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"1.00%": {BidUSD: 100_000, AskUSD: 400_000, TotalUSD: 500_000, DepthStatus: StatusAggregatedOrderbook, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyAggregatedStrict},
			"2.00%": {BidUSD: 800_000, AskUSD: 300_000, TotalUSD: 1_100_000, DepthStatus: StatusAggregatedOrderbook, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyAggregatedStrict},
		},
	}
	EnforceTierMonotonicity(row)

	one := row.DepthByTier["1.00%"]
	if one.BidUSD != 1_000_000 || one.AskUSD != 500_000 {
		t.Fatalf("1.00%% should clamp both sides, got (bid=%v, ask=%v)", one.BidUSD, one.AskUSD)
	}
	two := row.DepthByTier["2.00%"]
	if two.BidUSD != 1_000_000 || two.AskUSD != 500_000 {
		t.Fatalf("2.00%% should inherit clamped 1.00%% values, got (bid=%v, ask=%v)", two.BidUSD, two.AskUSD)
	}
}

func TestEnforceTierMonotonicitySkipsUnavailableTier(t *testing.T) {
	row := &PlatformSnapshot{
		Platform: "gate",
		DepthByTier: map[string]DepthMetrics{
			"0.10%": {BidUSD: 200, AskUSD: 200, TotalUSD: 400, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"1.00%": {BidUSD: 0, AskUSD: 0, TotalUSD: 0, DepthStatus: StatusPartial, DisplayAvailable: false, PolicyAcceptance: PolicyLooseLowerBound, PhysicalLimit: true, PartialReason: ReasonFeedTruncation},
			"2.00%": {BidUSD: 50, AskUSD: 50, TotalUSD: 100, DepthStatus: StatusAggregatedOrderbook, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyAggregatedStrict},
		},
	}
	EnforceTierMonotonicity(row)

	mid := row.DepthByTier["1.00%"]
	if mid.BidUSD != 0 || mid.PartialReason != ReasonFeedTruncation {
		t.Fatalf("1.00%% should be untouched when not display_available, got %+v", mid)
	}
	deep := row.DepthByTier["2.00%"]
	if deep.BidUSD != 200 || deep.AskUSD != 200 {
		t.Fatalf("2.00%% should still inherit from 0.10%% across the gap, got (bid=%v, ask=%v)", deep.BidUSD, deep.AskUSD)
	}
}

func TestEnforceTierMonotonicityNoOpWhenMonotonic(t *testing.T) {
	row := &PlatformSnapshot{
		Platform: "binance",
		DepthByTier: map[string]DepthMetrics{
			"0.05%": {BidUSD: 100, AskUSD: 100, TotalUSD: 200, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"0.10%": {BidUSD: 200, AskUSD: 250, TotalUSD: 450, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"1.00%": {BidUSD: 1000, AskUSD: 1100, TotalUSD: 2100, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
			"2.00%": {BidUSD: 2000, AskUSD: 2200, TotalUSD: 4200, DepthStatus: StatusComplete, DisplayAvailable: true, StrictComplete: true, PolicyAcceptance: PolicyRawStrict},
		},
	}
	EnforceTierMonotonicity(row)
	for _, tier := range []string{"0.05%", "0.10%", "1.00%", "2.00%"} {
		d := row.DepthByTier[tier]
		if d.PartialReason != "" || !d.StrictComplete || d.DepthStatus != StatusComplete {
			t.Fatalf("%s should remain untouched, got %+v", tier, d)
		}
	}
}

func TestAppendPartialReasonDeduplicates(t *testing.T) {
	cases := []struct {
		existing string
		add      string
		want     string
	}{
		{"", "monotonicity_lower_bound", "monotonicity_lower_bound"},
		{"feed_truncation", "monotonicity_lower_bound", "feed_truncation,monotonicity_lower_bound"},
		{"feed_truncation,monotonicity_lower_bound", "monotonicity_lower_bound", "feed_truncation,monotonicity_lower_bound"},
	}
	for _, c := range cases {
		got := appendPartialReason(c.existing, c.add)
		if got != c.want {
			t.Fatalf("appendPartialReason(%q, %q) = %q, want %q", c.existing, c.add, got, c.want)
		}
	}
}
