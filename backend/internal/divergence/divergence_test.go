package divergence

import (
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
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

// stubResolver is a minimal in-memory CanonicalResolver used to drive
// the alias-aware aggregation tests below. The map key is
// "<platformLower>|<baseUpper>" so the helper matches the
// case-normalisation contract documented on CanonicalResolver.
type stubResolver struct {
	m map[string]string
}

func (s stubResolver) ResolveCanonical(platform, base string) string {
	if s.m == nil {
		return strings.ToUpper(base)
	}
	key := strings.ToLower(platform) + "|" + strings.ToUpper(base)
	if v, ok := s.m[key]; ok {
		return v
	}
	return strings.ToUpper(base)
}

func TestCompute_AliasResolverMergesGoldBucket(t *testing.T) {
	// Five platforms publishing the same canonical (GOLD) under four
	// different aliases — XAU, XAUT, PAXG, and the literal "GOLD".
	// Without an alias resolver these would form four buckets and
	// edgeX-listed-via-PAXG would not annotate the GOLD divergence row.
	resolver := stubResolver{m: map[string]string{
		"binance|XAU":      "GOLD",
		"bitget|XAU":       "GOLD",
		"bitget|XAUT":      "GOLD",
		"bitget|PAXG":      "GOLD",
		"hyperliquid|GOLD": "GOLD",
		"edgex|XAUT":       "GOLD",
		"edgex|PAXG":       "GOLD",
	}}
	cfg := Config{
		CEXPlatforms:         []string{"binance", "bitget"},
		DEXPlatforms:         []string{"hyperliquid", "edgeX"},
		SignificantRankDelta: 5,
		Resolver:             resolver,
	}
	rows := []InputRow{
		mkRow("binance", 1, "XAU-USDT (perp)", 1000, boolPtr(false)),
		mkRow("bitget", 1, "XAU-USDT (perp)", 500, boolPtr(false)),
		mkRow("bitget", 2, "XAUT-USDT (perp)", 300, boolPtr(false)),
		mkRow("bitget", 3, "PAXG-USDT (perp)", 200, boolPtr(false)),
		mkRow("hyperliquid", 1, "XYZ:GOLD-USD (perp)", 800, nil),
		mkRow("edgeX", 1, "XAUT-USD (perp)", 100, boolPtr(true)),
		mkRow("edgeX", 2, "PAXG-USD (perp)", 80, boolPtr(true)),
	}
	snap := Compute(rows, cfg)
	if len(snap.CEXTop30) != 1 || snap.CEXTop30[0].Symbol != "GOLD" {
		t.Fatalf("CEX aggregate must collapse to GOLD, got %+v", snap.CEXTop30)
	}
	if got := snap.CEXTop30[0].AdjustedVolume24HUSD; got != 2000 {
		t.Fatalf("GOLD CEX volume = %v, want 2000 (1000+500+300+200)", got)
	}
	if got := snap.CEXTop30[0].PlatformCount; got != 2 {
		t.Fatalf("GOLD CEX platform_count = %d, want 2 (binance+bitget)", got)
	}
	if len(snap.DEXTop30) != 1 || snap.DEXTop30[0].Symbol != "GOLD" {
		t.Fatalf("DEX aggregate must collapse to GOLD, got %+v", snap.DEXTop30)
	}
	if snap.DEXTop30[0].PlatformCount != 2 {
		t.Fatalf("GOLD DEX platform_count = %d, want 2 (hyperliquid+edgeX)", snap.DEXTop30[0].PlatformCount)
	}
	var goldRow *domain.Top30DivergenceRow
	for i := range snap.Divergence {
		if snap.Divergence[i].Symbol == "GOLD" {
			goldRow = &snap.Divergence[i]
			break
		}
	}
	if goldRow == nil {
		t.Fatalf("GOLD divergence row missing; got %+v", snap.Divergence)
	}
	if !goldRow.EdgexListed {
		t.Fatalf("GOLD divergence row must inherit EdgexListed=true via the edgeX PAXG/XAUT alias rows")
	}
}

func TestCompute_AliasResolverMergesXyzIndexPerp(t *testing.T) {
	// Hyperliquid's XYZ:CL collapses to CL (handled by
	// CanonicaliseSymbol). The resolver then keeps it as CL because
	// neither hyperliquid nor edgeX has an explicit "CL" alias entry
	// in this stub, so the cross-class bucket joins naturally on
	// "CL" with edgeX's plain CL-USD (perp) row.
	resolver := stubResolver{m: map[string]string{}}
	cfg := Config{
		CEXPlatforms:         []string{"binance"},
		DEXPlatforms:         []string{"hyperliquid", "edgeX"},
		SignificantRankDelta: 5,
		Resolver:             resolver,
	}
	rows := []InputRow{
		mkRow("binance", 1, "CL-USDT (perp)", 1000, boolPtr(true)),
		mkRow("hyperliquid", 1, "XYZ:CL-USD (perp)", 800, nil),
		mkRow("edgeX", 1, "CL-USD (perp)", 100, boolPtr(true)),
	}
	snap := Compute(rows, cfg)
	if len(snap.DEXTop30) != 1 || snap.DEXTop30[0].Symbol != "CL" {
		t.Fatalf("DEX aggregate must collapse XYZ:CL + CL onto CL, got %+v", snap.DEXTop30)
	}
	if snap.DEXTop30[0].PlatformCount != 2 {
		t.Fatalf("CL DEX platform_count = %d, want 2 (hyperliquid+edgeX)", snap.DEXTop30[0].PlatformCount)
	}
}

func TestCompute_NilResolverIsBackwardCompatible(t *testing.T) {
	rows := []InputRow{
		mkRow("binance", 1, "BTC-USDT (perp)", 1000, nil),
		mkRow("hyperliquid", 1, "BTC-USDC (perp)", 800, nil),
	}
	cfg := defaultConfig()
	cfg.Resolver = nil
	snap := Compute(rows, cfg)
	if len(snap.CEXTop30) != 1 || snap.CEXTop30[0].Symbol != "BTC" {
		t.Fatalf("nil resolver must not break the existing identity path: %+v", snap.CEXTop30)
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

		// Hyperliquid index-perp namespace prefix (HIP-2 "XYZ:" tickers).
		{"XYZ:CL-USD (perp)", "CL"},
		{"XYZ:NVDA-USD (perp)", "NVDA"},
		{"XYZ:BRENTOIL-USD (perp)", "BRENTOIL"},
		{"XYZ:GOLD-USD (perp)", "GOLD"},
		{"XYZ:SP500-USD (perp)", "SP500"},

		// 1000-scaled perp variants ("1000PEPE" on binance/bybit
		// reports the same underlying liquidity as plain "PEPE" on
		// gate/mexc/okx — they must aggregate into a single canonical).
		{"1000PEPE-USDT (perp)", "PEPE"},
		{"1000SHIB-USDT (perp)", "SHIB"},
		{"1000BONK-USDT (perp)", "BONK"},
		{"1000FLOKI-USDT (perp)", "FLOKI"},
		// 10000-scaled (Asian memecoins on some venues) collapses too.
		{"10000COQ-USDT (perp)", "COQ"},

		// BASE(ALIAS) parenthetical (bingx publishes "GOLD(XAU)" /
		// "SILVER(XAG)" — we prefer the outer BASE since it already
		// matches a V1 canonical).
		{"GOLD(XAU)-USDT (perp)", "GOLD"},
		{"SILVER(XAG)-USDT (perp)", "SILVER"},

		// Combinations:
		{"XYZ:1000PEPE-USD (perp)", "PEPE"},
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
