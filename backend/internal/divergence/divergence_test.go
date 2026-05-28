package divergence

import (
	"testing"
	"time"

	"edgex-dashboard/backend/internal/domain"
)

// boolPtr is a tiny helper so the table-driven tests below keep their
// three-state EdgexListed expectations readable.
func boolPtr(b bool) *bool { return &b }

func defaultConfig() Config {
	return Config{
		CEXPlatforms:         []string{"binance", "okx", "mexc"},
		DEXPlatforms:         []string{"hyperliquid", "edgeX"},
		SignificantRankDelta: 5,
	}
}

func mkRow(platform string, rank int, symbol string, vol float64, listed *bool) InputRow {
	return InputRow{
		Platform:     platform,
		Symbol:       symbol,
		Rank:         rank,
		Volume24HUSD: vol,
		Status:       domain.StatusComplete,
		SnapshotTS:   time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		EdgexListed:  listed,
	}
}

func TestCompute_EmptyInputUnsupported(t *testing.T) {
	snap := Compute(nil, defaultConfig())
	if snap.Status != domain.StatusUnsupported {
		t.Fatalf("status = %q, want unsupported", snap.Status)
	}
	if len(snap.CEXTop30) != 0 || len(snap.DEXTop30) != 0 || len(snap.Divergence) != 0 {
		t.Fatalf("expected empty aggregates / divergence, got %+v", snap)
	}
	if snap.SignificantRankDelta != 5 {
		t.Fatalf("threshold = %d, want 5 (config round-trip)", snap.SignificantRankDelta)
	}
}

func TestCompute_AggregatesAcrossCEXClass(t *testing.T) {
	rows := []InputRow{
		mkRow("binance", 1, "BTC", 100, nil),
		mkRow("binance", 2, "ETH", 50, nil),
		mkRow("okx", 1, "ETH", 80, nil),
		mkRow("okx", 2, "BTC", 60, nil),
		mkRow("mexc", 1, "BTC", 200, nil),
	}
	snap := Compute(rows, defaultConfig())
	if len(snap.CEXTop30) != 2 {
		t.Fatalf("CEX rows = %d, want 2", len(snap.CEXTop30))
	}
	if snap.CEXTop30[0].Symbol != "BTC" || snap.CEXTop30[0].AdjustedVolume24HUSD != 360 {
		t.Fatalf("CEX[0] = %+v, want BTC=360", snap.CEXTop30[0])
	}
	if snap.CEXTop30[0].PlatformCount != 3 {
		t.Fatalf("BTC platform_count = %d, want 3", snap.CEXTop30[0].PlatformCount)
	}
}

func TestCompute_PartialStatusWhenOneClassEmpty(t *testing.T) {
	rows := []InputRow{
		mkRow("binance", 1, "BTC", 100, nil),
	}
	snap := Compute(rows, defaultConfig())
	if snap.Status != domain.StatusPartial {
		t.Fatalf("status = %q, want partial", snap.Status)
	}
}

func TestCompute_EdgexGapCountPreservesLegacyCollectorBehavior(t *testing.T) {
	// Phase 1 spec: KPI must preserve the legacy collector behavior
	// for the /api/snapshot/top30/divergence response. Three-state
	// strict filtering lives in the Phase 2 listing producer, not in
	// KPI count.
	//   BTC hot on both classes; *listed=true   → not a gap
	//   FOO hot on both classes; *listed=false  → counts as gap
	//   BAR hot on both classes; listed=nil     → also counts (legacy
	//                                              collector source
	//                                              had bool, so nil
	//                                              ≡ false for the KPI)
	rows := []InputRow{
		mkRow("binance", 1, "BTC", 1000, boolPtr(true)),
		mkRow("binance", 2, "FOO", 500, boolPtr(false)),
		mkRow("binance", 3, "BAR", 400, nil),
		mkRow("hyperliquid", 1, "BTC", 1000, boolPtr(true)),
		mkRow("hyperliquid", 2, "FOO", 500, boolPtr(false)),
		mkRow("hyperliquid", 3, "BAR", 400, nil),
	}
	snap := Compute(rows, defaultConfig())
	if snap.KPI.EdgexGapCount != 2 {
		t.Fatalf("edgex_gap_count = %d, want 2 (FOO + BAR, both not known-listed)", snap.KPI.EdgexGapCount)
	}
}

func TestCompute_HeavyCategoryClassification(t *testing.T) {
	rows := []InputRow{mkRow("binance", 1, "XYZ", 1000, nil)}
	for i := 2; i <= 9; i++ {
		rows = append(rows, mkRow("binance", i, padSym("CX", i), 900-float64(i), nil))
	}
	rows = append(rows, mkRow("binance", 10, "FILLER", 50, nil))
	for i := 1; i <= 9; i++ {
		rows = append(rows, mkRow("hyperliquid", i, padSym("DX", i), 900-float64(i), nil))
	}
	rows = append(rows, mkRow("hyperliquid", 10, "XYZ", 100, nil))

	snap := Compute(rows, defaultConfig())
	var xyz *domain.Top30DivergenceRow
	for i := range snap.Divergence {
		if snap.Divergence[i].Symbol == "XYZ" {
			xyz = &snap.Divergence[i]
			break
		}
	}
	if xyz == nil {
		t.Fatalf("XYZ row missing from divergence: %+v", snap.Divergence)
	}
	if xyz.Category != domain.Top30DivergenceCEXHeavy {
		t.Fatalf("XYZ category = %q, want cex_heavy", xyz.Category)
	}
	if xyz.RankDelta == nil || *xyz.RankDelta != 9 {
		t.Fatalf("XYZ rank_delta = %+v, want 9", xyz.RankDelta)
	}
}

func TestCompute_MergesAcrossQuoteVariants(t *testing.T) {
	rows := []InputRow{
		mkRow("binance", 1, "BTC-USDT (perp)", 1000, nil),
		mkRow("okx", 1, "BTC-USDT (perp)", 500, nil),
		mkRow("hyperliquid", 1, "BTC-USDC (perp)", 800, nil),
		mkRow("edgeX", 1, "BTC-USD (perp)", 200, boolPtr(true)),
	}
	snap := Compute(rows, defaultConfig())
	if len(snap.CEXTop30) != 1 || snap.CEXTop30[0].Symbol != "BTC" {
		t.Fatalf("CEX aggregate did not collapse to BTC: %+v", snap.CEXTop30)
	}
	if len(snap.DEXTop30) != 1 || snap.DEXTop30[0].Symbol != "BTC" {
		t.Fatalf("DEX aggregate did not collapse to BTC: %+v", snap.DEXTop30)
	}
	if !snap.Divergence[0].EdgexListed {
		t.Fatalf("BTC should carry edgex_listed=true via the BTC-USD (perp) edgeX row")
	}
}

func TestCanonicaliseSymbol(t *testing.T) {
	cases := []struct{ in, want string }{
		{"BTC", "BTC"},
		{"BTC-USDT (perp)", "BTC"},
		{"BTC-USDC (perp)", "BTC"},
		{"BTC-USD (perp)", "BTC"},
		{"ETH-BUSD (perp)", "ETH"},
		{"ETH-FDUSD (perp)", "ETH"},
		{"kPEPE-USDT (perp)", "KPEPE"},
		{"  HYPE-USDC (perp)  ", "HYPE"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := CanonicaliseSymbol(tc.in); got != tc.want {
			t.Errorf("CanonicaliseSymbol(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func padSym(prefix string, n int) string {
	if n < 10 {
		return prefix + "0" + itoa(n)
	}
	return prefix + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
