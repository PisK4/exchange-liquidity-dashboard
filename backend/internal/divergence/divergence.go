// Package divergence implements the venue-class Top30 aggregation
// and CEX-vs-DEX outer join used by both the collector API
// (/api/snapshot/top30/divergence) and the Listing Agent divergence
// push producer (#2-#5 alert cards).
//
// The package is intentionally pure: it consumes a slice of InputRow
// values and returns a domain.Top30DivergenceSnapshot without touching
// MySQL, the network, or the collector's in-memory store. Both call
// sites adapt their own datasource into []InputRow.
//
// Input three-state semantics:
//   - EdgexListed == nil       — "unknown" (collector status incomplete
//     or catalog missing). edgex_gap_count never counts this row.
//   - *EdgexListed == false    — "known unlisted". edgex_gap_count counts
//     this row when both CEXRank and DEXRank are non-nil. Listing
//     Agent #2-#5 alert cards filter strictly on this state.
//   - *EdgexListed == true     — "known listed". Row carries
//     EdgexListed=true in the output divergence row; no gap counted.
package divergence

import (
	"sort"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
)

// aggregateLimit caps the per-class aggregate ranking at the same N
// as the per-platform Top30. Going wider would surface long-tail
// symbols the user has not opted into and dilute the divergence
// narrative.
const aggregateLimit = 30

// InputRow is the union of fields the divergence calculation needs.
// EdgexListed uses *bool so callers can preserve the three-state
// distinction between "unknown" and "known unlisted".
type InputRow struct {
	Platform     string
	Symbol       string
	Rank         int
	Volume24HUSD float64
	Status       string
	SnapshotTS   time.Time
	EdgexListed  *bool
}

// Config carries the venue-class assignments and the |Δrank|
// threshold. Mirrors config.Top30DivergenceConfig but is duplicated
// here so the package has no import edge into config.
//
// Resolver is an optional alias-aware canonicaliser. When non-nil it
// is consulted after the per-symbol CanonicaliseSymbol pass so a
// (platform, base) tuple like (edgeX, PAXG) or (binance, XAU) gets
// folded onto the shared V1 canonical (GOLD). When nil, divergence
// keeps the legacy identity behaviour where each platform's
// post-normalisation symbol becomes its own bucket — this is what
// existing tests and the V1 collector path still rely on.
type Config struct {
	CEXPlatforms         []string
	DEXPlatforms         []string
	SignificantRankDelta int
	Resolver             CanonicalResolver
}

// CanonicalResolver maps a (platform, raw base asset) tuple to the
// V1-canonical key used for cross-platform aggregation. Implementations
// should be safe for concurrent reads, return the uppercased base
// unchanged on a miss, and treat empty inputs as no-op.
type CanonicalResolver interface {
	ResolveCanonical(platform, base string) string
}

// resolveCanonical is the internal helper that combines
// CanonicaliseSymbol (raw-form normalisation) with an optional
// CanonicalResolver (alias-aware fold). Centralised so aggregateClass
// and edgexListedSet stay in lock-step.
func resolveCanonical(resolver CanonicalResolver, platform, symbol string) string {
	canonical := CanonicaliseSymbol(symbol)
	if canonical == "" {
		return ""
	}
	if resolver == nil {
		return canonical
	}
	resolved := resolver.ResolveCanonical(platform, canonical)
	if resolved == "" {
		return canonical
	}
	return resolved
}

// Compute aggregates the per-class Top30 from rows, outer-joins the
// two aggregates, classifies each row, and emits the KPI strip. Pure
// function: rows and cfg are not mutated.
func Compute(rows []InputRow, cfg Config) domain.Top30DivergenceSnapshot {
	cexPlatforms := append([]string(nil), cfg.CEXPlatforms...)
	dexPlatforms := append([]string(nil), cfg.DEXPlatforms...)
	threshold := cfg.SignificantRankDelta
	if threshold <= 0 {
		threshold = 10
	}

	cexAgg, cexLatest := aggregateClass(rows, cexPlatforms, cfg.Resolver)
	dexAgg, dexLatest := aggregateClass(rows, dexPlatforms, cfg.Resolver)

	snapshotTS := cexLatest
	if dexLatest.After(snapshotTS) {
		snapshotTS = dexLatest
	}
	if snapshotTS.IsZero() {
		snapshotTS = time.Now().UTC()
	}

	status := domain.StatusComplete
	switch {
	case len(cexAgg) == 0 && len(dexAgg) == 0:
		status = domain.StatusUnsupported
	case len(cexAgg) == 0 || len(dexAgg) == 0:
		status = domain.StatusPartial
	}

	divergence := buildDivergenceRows(cexAgg, dexAgg, threshold)
	listed := edgexListedSet(rows, cfg.Resolver)
	for i := range divergence {
		if _, ok := listed[divergence[i].Symbol]; ok {
			divergence[i].EdgexListed = true
		}
		divergence[i].EdgexListedStatus = domain.StatusComplete
	}

	kpi := domain.Top30DivergenceKPI{}
	for _, row := range divergence {
		switch row.Category {
		case domain.Top30DivergenceCEXOnly:
			kpi.CEXOnlyCount++
		case domain.Top30DivergenceDEXOnly:
			kpi.DEXOnlyCount++
		case domain.Top30DivergenceCEXHeavy, domain.Top30DivergenceDEXHeavy:
			kpi.HeavyCount++
		case domain.Top30DivergenceAligned:
			kpi.AlignedCount++
		}
		if row.CEXRank != nil && row.DEXRank != nil && !row.EdgexListed {
			kpi.EdgexGapCount++
		}
	}

	return domain.Top30DivergenceSnapshot{
		SnapshotTS:           snapshotTS,
		Status:               status,
		CEXPlatforms:         cexPlatforms,
		DEXPlatforms:         dexPlatforms,
		SignificantRankDelta: threshold,
		CEXTop30:             cexAgg,
		DEXTop30:             dexAgg,
		Divergence:           divergence,
		KPI:                  kpi,
	}
}

// aggregateClass sums Volume24HUSD across the configured platforms,
// folds quote-variants onto the base asset, optionally consults the
// alias resolver to merge platform-specific aliases (PAXG/XAUT →
// GOLD), and returns the top-N ranked aggregate plus the latest
// snapshot_ts seen across the rows. Pure function.
func aggregateClass(rows []InputRow, platforms []string, resolver CanonicalResolver) ([]domain.Top30AggregateRow, time.Time) {
	if len(platforms) == 0 {
		return nil, time.Time{}
	}
	member := make(map[string]struct{}, len(platforms))
	for _, p := range platforms {
		member[p] = struct{}{}
	}
	type bucket struct {
		volume     float64
		contribute map[string]struct{}
	}
	byCanonical := map[string]*bucket{}
	var latest time.Time
	for _, row := range rows {
		if _, ok := member[row.Platform]; !ok {
			continue
		}
		if row.Status != domain.StatusComplete && row.Status != "" {
			continue
		}
		if row.Volume24HUSD <= 0 {
			continue
		}
		canonical := resolveCanonical(resolver, row.Platform, row.Symbol)
		if canonical == "" {
			continue
		}
		b, ok := byCanonical[canonical]
		if !ok {
			b = &bucket{contribute: map[string]struct{}{}}
			byCanonical[canonical] = b
		}
		b.volume += row.Volume24HUSD
		b.contribute[row.Platform] = struct{}{}
		if row.SnapshotTS.After(latest) {
			latest = row.SnapshotTS
		}
	}
	if len(byCanonical) == 0 {
		return nil, latest
	}
	out := make([]domain.Top30AggregateRow, 0, len(byCanonical))
	for canonical, b := range byCanonical {
		contributors := make([]string, 0, len(b.contribute))
		for p := range b.contribute {
			contributors = append(contributors, p)
		}
		sort.Strings(contributors)
		out = append(out, domain.Top30AggregateRow{
			Symbol:                canonical,
			AdjustedVolume24HUSD:  b.volume,
			RawVolume24HUSD:       b.volume,
			PlatformCount:         len(b.contribute),
			ContributingPlatforms: contributors,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AdjustedVolume24HUSD != out[j].AdjustedVolume24HUSD {
			return out[i].AdjustedVolume24HUSD > out[j].AdjustedVolume24HUSD
		}
		return out[i].Symbol < out[j].Symbol
	})
	if len(out) > aggregateLimit {
		out = out[:aggregateLimit]
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, latest
}

// edgexListedSet builds the set of canonical symbols whose EdgexListed
// flag is *known true* across the inputs. Three-state semantics: a
// nil flag is never treated as listed; only *true counts. This mirrors
// collector.edgexListedSetLocked but works on the union of all rows
// (caller passes both class memberships in already).
func edgexListedSet(rows []InputRow, resolver CanonicalResolver) map[string]struct{} {
	out := map[string]struct{}{}
	for _, row := range rows {
		if row.EdgexListed == nil || !*row.EdgexListed {
			continue
		}
		canonical := resolveCanonical(resolver, row.Platform, row.Symbol)
		if canonical == "" {
			continue
		}
		out[canonical] = struct{}{}
	}
	return out
}

func buildDivergenceRows(cex, dex []domain.Top30AggregateRow, threshold int) []domain.Top30DivergenceRow {
	cexBySymbol := indexAggregateBySymbol(cex)
	dexBySymbol := indexAggregateBySymbol(dex)

	symbols := map[string]struct{}{}
	for s := range cexBySymbol {
		symbols[s] = struct{}{}
	}
	for s := range dexBySymbol {
		symbols[s] = struct{}{}
	}
	out := make([]domain.Top30DivergenceRow, 0, len(symbols))
	for symbol := range symbols {
		cexRow, hasCEX := cexBySymbol[symbol]
		dexRow, hasDEX := dexBySymbol[symbol]
		row := domain.Top30DivergenceRow{Symbol: symbol}
		if hasCEX {
			rank := cexRow.Rank
			row.CEXRank = &rank
			adj := cexRow.AdjustedVolume24HUSD
			raw := cexRow.RawVolume24HUSD
			row.CEXAdjustedVolUSD = &adj
			row.CEXRawVolUSD = &raw
			row.CEXPlatformCount = cexRow.PlatformCount
		}
		if hasDEX {
			rank := dexRow.Rank
			row.DEXRank = &rank
			adj := dexRow.AdjustedVolume24HUSD
			raw := dexRow.RawVolume24HUSD
			row.DEXAdjustedVolUSD = &adj
			row.DEXRawVolUSD = &raw
			row.DEXPlatformCount = dexRow.PlatformCount
		}
		row.Category = ClassifyDivergence(row.CEXRank, row.DEXRank, threshold)
		if row.CEXRank != nil && row.DEXRank != nil {
			delta := *row.CEXRank - *row.DEXRank
			if delta < 0 {
				delta = -delta
			}
			row.RankDelta = &delta
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi := categoryPriority(out[i].Category)
		pj := categoryPriority(out[j].Category)
		if pi != pj {
			return pi < pj
		}
		di := rankDeltaValue(out[i])
		dj := rankDeltaValue(out[j])
		if di != dj {
			return di > dj
		}
		bi := bestRank(out[i])
		bj := bestRank(out[j])
		if bi != bj {
			return bi < bj
		}
		return out[i].Symbol < out[j].Symbol
	})
	return out
}

// ClassifyDivergence implements the category truth table. Exported so
// downstream tooling (e.g. preview CLI) can re-classify ad-hoc rows
// without re-running Compute.
func ClassifyDivergence(cexRank, dexRank *int, threshold int) string {
	switch {
	case cexRank != nil && dexRank == nil:
		return domain.Top30DivergenceCEXOnly
	case cexRank == nil && dexRank != nil:
		return domain.Top30DivergenceDEXOnly
	case cexRank == nil && dexRank == nil:
		return domain.Top30DivergenceAligned
	}
	delta := *cexRank - *dexRank
	abs := delta
	if abs < 0 {
		abs = -abs
	}
	if abs < threshold {
		return domain.Top30DivergenceAligned
	}
	if delta < 0 {
		return domain.Top30DivergenceCEXHeavy
	}
	return domain.Top30DivergenceDEXHeavy
}

func categoryPriority(category string) int {
	switch category {
	case domain.Top30DivergenceCEXOnly, domain.Top30DivergenceDEXOnly:
		return 0
	case domain.Top30DivergenceCEXHeavy, domain.Top30DivergenceDEXHeavy:
		return 1
	case domain.Top30DivergenceAligned:
		return 2
	}
	return 3
}

func rankDeltaValue(row domain.Top30DivergenceRow) int {
	if row.RankDelta != nil {
		return *row.RankDelta
	}
	return aggregateLimit + 1
}

func bestRank(row domain.Top30DivergenceRow) int {
	if row.CEXRank != nil && row.DEXRank != nil {
		if *row.CEXRank < *row.DEXRank {
			return *row.CEXRank
		}
		return *row.DEXRank
	}
	if row.CEXRank != nil {
		return *row.CEXRank
	}
	if row.DEXRank != nil {
		return *row.DEXRank
	}
	return aggregateLimit + 1
}

func indexAggregateBySymbol(rows []domain.Top30AggregateRow) map[string]domain.Top30AggregateRow {
	out := make(map[string]domain.Top30AggregateRow, len(rows))
	for _, row := range rows {
		out[CanonicaliseSymbol(row.Symbol)] = row
	}
	return out
}

// quoteSuffixes mirrors collector.divergenceQuoteSuffixes verbatim; the
// list must stay in sync to preserve canonicalisation across the
// listing / collector paths.
var quoteSuffixes = []string{
	"-USDT", "-USDC", "-USD", "-BUSD", "-FDUSD", "-TUSD",
}

// namespacePrefixes lists per-platform symbol-namespace prefixes the
// canonicaliser strips before alias resolution. Currently scoped to
// Hyperliquid's HIP-2 "XYZ:" index perps (XYZ:CL, XYZ:NVDA, ...) which
// are tokenized equities/commodities whose underlying ticker is the
// part after the colon.
var namespacePrefixes = []string{"XYZ:"}

// scalePrefixes captures perp-product scale variants ("1000PEPE",
// "10000COQ") used by binance / bybit / okx for assets whose unit
// price is tiny. The underlying asset is identical to the
// un-prefixed canonical, so for cross-platform Top30 aggregation we
// strip these prefixes so the buckets merge.
var scalePrefixes = []string{"10000", "1000"}

// CanonicaliseSymbol normalises a Top30 Symbol to the base asset used
// as the cross-camp join key. Exported so listing-side producers can
// filter / compare canonical forms without re-implementing the rule.
//
// The rule order is intentional:
//  1. trim + upper
//  2. drop " (PERP)" / "-USDT" et al. quote suffixes
//  3. drop platform namespace prefix ("XYZ:")
//  4. unwrap "BASE(ALIAS)" parenthetical to BASE (the outer name is
//     already the V1 canonical in every observed case — GOLD(XAU)
//     → GOLD; SILVER(XAG) → SILVER)
//  5. strip "1000" / "10000" perp-scale prefix so 1000PEPE collapses
//     onto PEPE
//
// The output is still platform-agnostic — alias resolution
// (PAXG/XAUT on edgeX → GOLD canonical) is a separate concern that
// requires (platform, base) context and lives in the per-row
// resolver pipeline.
func CanonicaliseSymbol(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, " (PERP)")
	for _, suffix := range quoteSuffixes {
		if strings.HasSuffix(s, suffix) {
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}
	for _, prefix := range namespacePrefixes {
		if strings.HasPrefix(s, prefix) {
			rest := strings.TrimPrefix(s, prefix)
			if rest != "" {
				s = rest
				break
			}
		}
	}
	if open := strings.IndexByte(s, '('); open > 0 && strings.HasSuffix(s, ")") {
		s = s[:open]
	}
	for _, prefix := range scalePrefixes {
		if strings.HasPrefix(s, prefix) && len(s) > len(prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	return s
}
