package main

import "testing"

// TestParseIncludeFlagMatrix locks down Phase 0.5 of
// 2026-05-29-listing-agent.md: the smoke harness must recognise the
// liquidity family in addition to hot_gap and divergence. The flag is
// a 4-tuple (hotGap, divergence, liquidity, err) so the orchestrator
// can wire family-specific build/insert paths without re-parsing.
func TestParseIncludeFlagMatrix(t *testing.T) {
	cases := []struct {
		raw                                string
		wantHot, wantDiv, wantLiq, wantErr bool
	}{
		{raw: "", wantHot: true, wantDiv: true, wantLiq: true},
		{raw: "all", wantHot: true, wantDiv: true, wantLiq: true},
		{raw: "hot_gap", wantHot: true},
		{raw: "hotgap", wantHot: true},
		{raw: "top30", wantHot: true},
		{raw: "#1", wantHot: true},
		{raw: "divergence", wantDiv: true},
		{raw: "div", wantDiv: true},
		{raw: "#2-#5", wantDiv: true},
		{raw: "liquidity", wantLiq: true},
		{raw: "liq", wantLiq: true},
		{raw: "#10-#11", wantLiq: true},
		{raw: "garbage", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			hot, div, liq, err := parseIncludeFlag(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if hot != tc.wantHot || div != tc.wantDiv || liq != tc.wantLiq {
				t.Fatalf("parseIncludeFlag(%q) = (%v,%v,%v), want (%v,%v,%v)",
					tc.raw, hot, div, liq, tc.wantHot, tc.wantDiv, tc.wantLiq)
			}
		})
	}
}

// TestBuildLiquiditySmokeFixturesCoversThreePhases asserts the
// liquidity smoke fixtures cover first / reissue / clear payloads, so
// one --include=liquidity run exercises every phase the production
// state machine produces. Without this guard a refactor could quietly
// drop one of the phases and the operator would not notice until a
// real alert had to fire that family.
func TestBuildLiquiditySmokeFixturesCoversThreePhases(t *testing.T) {
	cards := buildLiquiditySmokeFixtures("https://dashboard.example/liquidity", 0.5)
	if len(cards) < 3 {
		t.Fatalf("want >=3 fixture cards (first/reissue/clear), got %d", len(cards))
	}
	seen := map[string]bool{}
	for _, c := range cards {
		seen[c.Phase] = true
	}
	for _, phase := range []string{"first", "reissue", "clear"} {
		if !seen[phase] {
			t.Errorf("liquidity smoke fixtures missing phase=%q (got %v)", phase, seen)
		}
	}
}
