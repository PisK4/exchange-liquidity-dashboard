package collector

import (
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/divergence"
	"edgex-ops-intelligence/backend/internal/domain"
)

func newDivergenceStore(t *testing.T) *Store {
	t.Helper()
	cfg := config.Default()
	cfg.Runtime.Top30Divergence = config.Top30DivergenceConfig{
		CEXPlatforms:         []string{"binance", "okx", "mexc"},
		DEXPlatforms:         []string{"hyperliquid", "edgeX"},
		SignificantRankDelta: 5,
	}
	return NewStore(cfg)
}

func mkTop30Row(rank int, symbol string, vol float64) domain.Top30Row {
	return domain.Top30Row{
		Rank:         rank,
		Symbol:       symbol,
		Volume24HUSD: vol,
		Status:       domain.StatusComplete,
		SnapshotTS:   time.Now().UTC(),
	}
}

func TestTop30Divergence_EmptyStoreUnsupported(t *testing.T) {
	store := newDivergenceStore(t)
	snap := store.Top30Divergence()
	if snap.Status != domain.StatusUnsupported {
		t.Fatalf("expected status=unsupported on empty store, got %q", snap.Status)
	}
	if len(snap.CEXTop30) != 0 || len(snap.DEXTop30) != 0 {
		t.Fatalf("expected empty aggregates on empty store, got cex=%d dex=%d", len(snap.CEXTop30), len(snap.DEXTop30))
	}
	if len(snap.Divergence) != 0 {
		t.Fatalf("expected empty divergence on empty store, got %d rows", len(snap.Divergence))
	}
	if snap.SignificantRankDelta != 5 {
		t.Fatalf("threshold must round-trip from config, got %d", snap.SignificantRankDelta)
	}
}

func TestTop30Divergence_AggregatesAcrossClass(t *testing.T) {
	store := newDivergenceStore(t)
	// binance Top30: BTC #1 ($100), ETH #2 ($50)
	store.SaveTop30("binance", []domain.Top30Row{
		mkTop30Row(1, "BTC", 100),
		mkTop30Row(2, "ETH", 50),
	})
	// okx Top30: ETH #1 ($80), BTC #2 ($60)
	store.SaveTop30("okx", []domain.Top30Row{
		mkTop30Row(1, "ETH", 80),
		mkTop30Row(2, "BTC", 60),
	})
	// mexc Top30: BTC #1 ($200). With the platform discount removed,
	// MEXC contributes its full raw 200 to the BTC bucket.
	store.SaveTop30("mexc", []domain.Top30Row{
		mkTop30Row(1, "BTC", 200),
	})

	snap := store.Top30Divergence()
	if len(snap.CEXTop30) != 2 {
		t.Fatalf("expected 2 aggregate rows in CEX, got %d", len(snap.CEXTop30))
	}
	// BTC volume = 100 + 60 + 200 = 360
	// ETH volume = 50 + 80        = 130
	// BTC > ETH so BTC is rank #1
	if snap.CEXTop30[0].Symbol != "BTC" {
		t.Fatalf("expected CEX #1 = BTC, got %q", snap.CEXTop30[0].Symbol)
	}
	if got := snap.CEXTop30[0].AdjustedVolume24HUSD; got != 360 {
		t.Fatalf("expected BTC volume=360 (raw, no discount), got %v", got)
	}
	if got := snap.CEXTop30[0].RawVolume24HUSD; got != 360 {
		t.Fatalf("expected BTC raw=360, got %v", got)
	}
	if snap.CEXTop30[0].AdjustedVolume24HUSD != snap.CEXTop30[0].RawVolume24HUSD {
		t.Fatalf("expected adjusted == raw after discount removal, got adj=%v raw=%v",
			snap.CEXTop30[0].AdjustedVolume24HUSD, snap.CEXTop30[0].RawVolume24HUSD)
	}
	if snap.CEXTop30[0].PlatformCount != 3 {
		t.Fatalf("expected BTC platform_count=3, got %d", snap.CEXTop30[0].PlatformCount)
	}
	if snap.CEXTop30[1].Symbol != "ETH" || snap.CEXTop30[1].Rank != 2 {
		t.Fatalf("expected CEX #2 = ETH, got %+v", snap.CEXTop30[1])
	}
}

func TestTop30Divergence_CategoriesAndKPI(t *testing.T) {
	store := newDivergenceStore(t)
	// CEX universe: only BTC and FOO
	store.SaveTop30("binance", []domain.Top30Row{
		mkTop30Row(1, "BTC", 1000),
		mkTop30Row(2, "FOO", 500),
	})
	// DEX universe: only BTC and BAR (FOO is CEX-only, BAR is DEX-only)
	store.SaveTop30("hyperliquid", []domain.Top30Row{
		mkTop30Row(1, "BTC", 800),
		mkTop30Row(2, "BAR", 300),
	})

	snap := store.Top30Divergence()
	if snap.Status != domain.StatusComplete {
		t.Fatalf("expected status=complete, got %q", snap.Status)
	}
	byCategory := map[string]int{}
	for _, row := range snap.Divergence {
		byCategory[row.Category]++
	}
	if byCategory[domain.Top30DivergenceCEXOnly] != 1 {
		t.Fatalf("expected exactly 1 cex_only row, got %d", byCategory[domain.Top30DivergenceCEXOnly])
	}
	if byCategory[domain.Top30DivergenceDEXOnly] != 1 {
		t.Fatalf("expected exactly 1 dex_only row, got %d", byCategory[domain.Top30DivergenceDEXOnly])
	}
	if byCategory[domain.Top30DivergenceAligned] != 1 {
		t.Fatalf("expected exactly 1 aligned row (BTC #1/#1), got %d", byCategory[domain.Top30DivergenceAligned])
	}
	if snap.KPI.CEXOnlyCount != 1 || snap.KPI.DEXOnlyCount != 1 || snap.KPI.AlignedCount != 1 {
		t.Fatalf("KPI counts wrong: %+v", snap.KPI)
	}
}

func TestTop30Divergence_HeavyClassification(t *testing.T) {
	store := newDivergenceStore(t)
	// CEX places XYZ at rank 1; DEX places XYZ at rank 10. Threshold=5
	// (configured in newDivergenceStore) so |Δ|=9 triggers cex_heavy.
	cexRows := []domain.Top30Row{mkTop30Row(1, "XYZ", 1000)}
	for i := 2; i <= 9; i++ {
		cexRows = append(cexRows, mkTop30Row(i, padSymbol("CX", i), 900-float64(i)))
	}
	cexRows = append(cexRows, mkTop30Row(10, "FILLER", 50))
	store.SaveTop30("binance", cexRows)

	dexRows := []domain.Top30Row{}
	for i := 1; i <= 9; i++ {
		dexRows = append(dexRows, mkTop30Row(i, padSymbol("DX", i), 900-float64(i)))
	}
	dexRows = append(dexRows, mkTop30Row(10, "XYZ", 100))
	store.SaveTop30("hyperliquid", dexRows)

	snap := store.Top30Divergence()
	var xyz *domain.Top30DivergenceRow
	for i := range snap.Divergence {
		if snap.Divergence[i].Symbol == "XYZ" {
			xyz = &snap.Divergence[i]
			break
		}
	}
	if xyz == nil {
		t.Fatalf("XYZ row missing from divergence output")
	}
	if xyz.Category != domain.Top30DivergenceCEXHeavy {
		t.Fatalf("expected XYZ = cex_heavy, got %q", xyz.Category)
	}
	if xyz.RankDelta == nil || *xyz.RankDelta != 9 {
		t.Fatalf("expected |Δ|=9 for XYZ, got %+v", xyz.RankDelta)
	}
}

func TestTop30Divergence_EdgexGapCount(t *testing.T) {
	store := newDivergenceStore(t)
	// BTC is hot on both classes and listed on edgeX → not a gap.
	// FOO is hot on both classes but NOT listed → counts as gap.
	store.SaveTop30("binance", []domain.Top30Row{
		mkTop30Row(1, "BTC", 1000),
		mkTop30Row(2, "FOO", 500),
	})
	store.SaveTop30("hyperliquid", []domain.Top30Row{
		mkTop30Row(1, "BTC", 1000),
		mkTop30Row(2, "FOO", 500),
	})
	store.SaveTop30("edgeX", []domain.Top30Row{
		{Rank: 1, Symbol: "BTC", Volume24HUSD: 200, Status: domain.StatusComplete, EdgexListed: true, SnapshotTS: time.Now().UTC()},
	})

	snap := store.Top30Divergence()
	if snap.KPI.EdgexGapCount != 1 {
		t.Fatalf("expected edgex_gap_count=1 (FOO hot on both, not listed), got %d (kpi=%+v)", snap.KPI.EdgexGapCount, snap.KPI)
	}
}

func TestTop30Divergence_PartialStatusOnSingleClass(t *testing.T) {
	store := newDivergenceStore(t)
	store.SaveTop30("binance", []domain.Top30Row{mkTop30Row(1, "BTC", 100)})
	snap := store.Top30Divergence()
	if snap.Status != domain.StatusPartial {
		t.Fatalf("expected status=partial when only one class has data, got %q", snap.Status)
	}
	if len(snap.DEXTop30) != 0 {
		t.Fatalf("expected empty DEX aggregate, got %d rows", len(snap.DEXTop30))
	}
	if len(snap.CEXTop30) != 1 {
		t.Fatalf("expected 1 CEX aggregate row, got %d", len(snap.CEXTop30))
	}
}

// TestTop30Divergence_MergesAcrossQuoteVariants pins the bug-fix where
// BTC-USDT (CEX-side perp), BTC-USDC (Hyperliquid perp) and BTC-USD
// (edgeX perp) used to surface as three separate rows. They denote
// the same BTC perpetual product and MUST collapse onto a single
// canonical "BTC" row so the CEX-vs-DEX comparison aligns the two
// camps. Without this normalisation BTC trends as cex_only (USDT
// branch) while BTC-USDC / BTC-USD show up as dex_only — the exact
// symptom the operator reported.
func TestTop30Divergence_MergesAcrossQuoteVariants(t *testing.T) {
	store := newDivergenceStore(t)
	// CEX camp prices BTC in USDT (the post-NormaliseSymbol form for
	// every centralised venue we collect).
	store.SaveTop30("binance", []domain.Top30Row{
		mkTop30Row(1, "BTC-USDT (perp)", 1000),
	})
	store.SaveTop30("okx", []domain.Top30Row{
		mkTop30Row(1, "BTC-USDT (perp)", 500),
	})
	// DEX camp: Hyperliquid uses USDC settlement, edgeX uses USD.
	// Both must merge with the CEX BTC bucket.
	store.SaveTop30("hyperliquid", []domain.Top30Row{
		mkTop30Row(1, "BTC-USDC (perp)", 800),
	})
	store.SaveTop30("edgeX", []domain.Top30Row{
		{Rank: 1, Symbol: "BTC-USD (perp)", Volume24HUSD: 200, Status: domain.StatusComplete, EdgexListed: true, SnapshotTS: time.Now().UTC()},
	})

	snap := store.Top30Divergence()
	if len(snap.CEXTop30) != 1 || snap.CEXTop30[0].Symbol != "BTC" {
		t.Fatalf("expected CEX aggregate to collapse to a single BTC row, got %+v", snap.CEXTop30)
	}
	if snap.CEXTop30[0].PlatformCount != 2 {
		t.Fatalf("expected BTC CEX platform_count=2 (binance+okx), got %d", snap.CEXTop30[0].PlatformCount)
	}
	if got := snap.CEXTop30[0].RawVolume24HUSD; got != 1500 {
		t.Fatalf("expected BTC CEX raw vol=1500 (1000+500), got %v", got)
	}
	if len(snap.DEXTop30) != 1 || snap.DEXTop30[0].Symbol != "BTC" {
		t.Fatalf("expected DEX aggregate to collapse BTC-USDC + BTC-USD into one BTC row, got %+v", snap.DEXTop30)
	}
	if snap.DEXTop30[0].PlatformCount != 2 {
		t.Fatalf("expected BTC DEX platform_count=2 (hyperliquid+edgeX), got %d", snap.DEXTop30[0].PlatformCount)
	}
	if got := snap.DEXTop30[0].RawVolume24HUSD; got != 1000 {
		t.Fatalf("expected BTC DEX raw vol=1000 (800+200), got %v", got)
	}
	if len(snap.Divergence) != 1 || snap.Divergence[0].Symbol != "BTC" {
		t.Fatalf("expected exactly one divergence row labelled BTC, got %+v", snap.Divergence)
	}
	if snap.Divergence[0].Category != domain.Top30DivergenceAligned {
		t.Fatalf("expected BTC category=aligned (both camps rank it #1), got %q", snap.Divergence[0].Category)
	}
	if !snap.Divergence[0].EdgexListed {
		t.Fatalf("expected BTC to be marked edgex_listed=true (edgeX BTC-USD row carried the flag)")
	}
	if snap.KPI.EdgexGapCount != 0 {
		t.Fatalf("expected edgex_gap_count=0 (BTC listed on edgeX, no gap), got %d", snap.KPI.EdgexGapCount)
	}
}

func TestCanonicaliseDivergenceSymbol(t *testing.T) {
	// The canonicalisation rule now lives in internal/divergence;
	// keep this collector-level test as a thin guard that the shim
	// still forwards the same behaviour, plus the legacy case set the
	// store-side wired into.
	cases := []struct {
		in, want string
	}{
		{"BTC", "BTC"},
		{"BTC-USDT (perp)", "BTC"},
		{"BTC-USDC (perp)", "BTC"},
		{"BTC-USD (perp)", "BTC"},
		{"ETH-BUSD (perp)", "ETH"},
		{"ETH-FDUSD (perp)", "ETH"},
		{"kPEPE-USDT (perp)", "KPEPE"},
		// Scale-prefix perp variants ("1000PEPE", "10000COQ") used to
		// round-trip as their raw form; the canonicaliser now strips
		// them so cross-platform Top30 buckets merge with the
		// underlying ticker (PEPE / COQ).
		{"1000PEPE-USDT (perp)", "PEPE"},
		// Hyperliquid HIP-2 namespace prefix.
		{"XYZ:CL-USD (perp)", "CL"},
		// bingx-style BASE(ALIAS) parenthetical.
		{"GOLD(XAU)-USDT (perp)", "GOLD"},
		{"  HYPE-USDC (perp)  ", "HYPE"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := divergence.CanonicaliseSymbol(tc.in); got != tc.want {
			t.Errorf("divergence.CanonicaliseSymbol(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestClassifyDivergence(t *testing.T) {
	// Classification likewise lives in internal/divergence; the
	// thin-shim Top30Divergence forwards verbatim. This test keeps the
	// old collector-level assertions running so the renamed export
	// surface is still wired correctly.
	one, two, eleven := 1, 2, 11
	cases := []struct {
		name     string
		cex, dex *int
		thresh   int
		want     string
	}{
		{"both nil → aligned (degenerate)", nil, nil, 10, domain.Top30DivergenceAligned},
		{"cex only", &one, nil, 10, domain.Top30DivergenceCEXOnly},
		{"dex only", nil, &one, 10, domain.Top30DivergenceDEXOnly},
		{"aligned within threshold", &one, &two, 10, domain.Top30DivergenceAligned},
		{"cex_heavy when CEX ranks lower number", &one, &eleven, 5, domain.Top30DivergenceCEXHeavy},
		{"dex_heavy when DEX ranks lower number", &eleven, &one, 5, domain.Top30DivergenceDEXHeavy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := divergence.ClassifyDivergence(tc.cex, tc.dex, tc.thresh); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func padSymbol(prefix string, n int) string {
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
