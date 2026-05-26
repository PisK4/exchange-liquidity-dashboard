package collector

import (
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/indicators"
)

// top30AggregateLimit caps the per-class aggregate ranking at the same N
// as the per-platform Top30. Going wider would surface long-tail symbols
// the user has not opted into and dilute the divergence narrative.
const top30AggregateLimit = 30

// Top30Divergence builds the CEX-vs-DEX aggregate comparison from the
// already-cached per-platform Top30 snapshots. The method is read-only:
// no MySQL access, no network calls, no new tables — it composes solely
// from s.top30ByPlatform and s.cfg.Runtime.Top30Divergence.
//
// The aggregation pipeline is:
//
//  1. For each venue class (CEX / DEX), iterate every member platform's
//     latest Top30 rows. For each (symbol) appearing in any member's
//     Top30, sum AdjustedVolume(platform, vol_24h) (which folds MEXC×0.4
//     and Gate×0.5 in) to get the class's adjusted volume for that
//     symbol; the raw volume is summed in parallel for display.
//  2. Sort each class by adjusted volume descending and take top 30.
//  3. Outer-join the two aggregate Top30s on `symbol` to produce a
//     divergence row per union member. Categorise each row as
//     cex_only / dex_only / cex_heavy / dex_heavy / aligned using the
//     SignificantRankDelta threshold.
//  4. Sort the divergence rows so the highest |Δrank| / strongest
//     single-side concentration surfaces first.
//  5. Emit a KPI strip counting each category + the edgeX gap (a symbol
//     that ranks in BOTH class aggregates but is NOT listed on edgeX).
//
// Edge cases:
//   - Empty store (collector hasn't produced any Top30 yet) returns
//     Status=unsupported with empty slices so the API contract is
//     unambiguous.
//   - One class has no data → Status=partial, the empty class's
//     aggregate is an empty slice and every joined row falls into the
//     other class's *_only bucket.
//   - The CEX/DEX configuration is empty → Status=unsupported with the
//     reason captured in the response platform lists (empty arrays make
//     the misconfiguration visible to the UI without an extra field).
func (s *Store) Top30Divergence() domain.Top30DivergenceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := s.cfg.Runtime.Top30Divergence
	cexPlatforms := append([]string(nil), cfg.CEXPlatforms...)
	dexPlatforms := append([]string(nil), cfg.DEXPlatforms...)
	threshold := cfg.SignificantRankDelta
	if threshold <= 0 {
		threshold = 10
	}

	cexAgg, cexLatest := s.aggregateClassLocked(cexPlatforms)
	dexAgg, dexLatest := s.aggregateClassLocked(dexPlatforms)

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
	listed := s.edgexListedSetLocked()
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

// aggregateClassLocked sums AdjustedVolume(platform, vol_24h) over every
// platform in `platforms` for each symbol that appears in any member's
// Top30, then takes the top `top30AggregateLimit` by adjusted volume.
// Returns the ranked rows plus the latest snapshot_ts seen across the
// member rows so the caller can surface a per-class freshness signal.
//
// Caller MUST hold s.mu (RLock or Lock).
func (s *Store) aggregateClassLocked(platforms []string) ([]domain.Top30AggregateRow, time.Time) {
	if len(platforms) == 0 {
		return nil, time.Time{}
	}
	type bucket struct {
		adjusted   float64
		raw        float64
		contribute map[string]struct{}
	}
	byCanonical := map[string]*bucket{}
	var latest time.Time
	for _, platform := range platforms {
		rows, ok := s.top30ByPlatform[platform]
		if !ok {
			continue
		}
		for _, row := range rows {
			if row.Status != domain.StatusComplete && row.Status != "" {
				continue
			}
			if row.Volume24HUSD <= 0 {
				continue
			}
			canonical := canonicaliseDivergenceSymbol(row.Symbol)
			if canonical == "" {
				continue
			}
			b, ok := byCanonical[canonical]
			if !ok {
				b = &bucket{contribute: map[string]struct{}{}}
				byCanonical[canonical] = b
			}
			b.adjusted += indicators.AdjustedVolume(platform, row.Volume24HUSD)
			b.raw += row.Volume24HUSD
			b.contribute[platform] = struct{}{}
			if row.SnapshotTS.After(latest) {
				latest = row.SnapshotTS
			}
		}
	}
	if len(byCanonical) == 0 {
		return nil, latest
	}
	// Emit the canonical (base asset) as Symbol so the aggregate row's
	// label honestly represents the post-merge identity. Using the
	// first-seen raw display here would mislead the reader: a "BTC"
	// row that summed BTC-USDT + BTC-USDC + BTC-USD volumes would
	// otherwise be labelled "BTC-USDT (perp)" and look like a single-
	// quote aggregate.
	out := make([]domain.Top30AggregateRow, 0, len(byCanonical))
	for canonical, b := range byCanonical {
		contributors := make([]string, 0, len(b.contribute))
		for p := range b.contribute {
			contributors = append(contributors, p)
		}
		sort.Strings(contributors)
		out = append(out, domain.Top30AggregateRow{
			Symbol:                canonical,
			AdjustedVolume24HUSD:  b.adjusted,
			RawVolume24HUSD:       b.raw,
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
	if len(out) > top30AggregateLimit {
		out = out[:top30AggregateLimit]
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, latest
}

// edgexListedSetLocked returns the set of canonical symbols currently
// listed on edgeX, derived from the cached edgeX Top30 row. We piggyback
// on Top30Row.EdgexListed here because the divergence response should
// reflect whatever the existing Top30 endpoint reports — keeping the
// answer to "is this listed?" in a single place avoids drift between
// the two views.
//
// Caller MUST hold s.mu (RLock or Lock).
func (s *Store) edgexListedSetLocked() map[string]struct{} {
	out := map[string]struct{}{}
	for _, rows := range s.top30ByPlatform {
		for _, row := range rows {
			if row.EdgexListed {
				out[canonicaliseDivergenceSymbol(row.Symbol)] = struct{}{}
			}
		}
	}
	return out
}

// buildDivergenceRows performs the outer join described in the package
// docstring. The output is sorted by:
//  1. category priority: cex_only / dex_only first (strongest signal),
//     then cex_heavy / dex_heavy, then aligned;
//  2. within the same category, by |Δrank| descending;
//  3. ties broken by best rank (min of the two ranks) ascending so a
//     symbol that's #1 on one side ranks above a #15 on one side.
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
		row.Category = classifyDivergence(row.CEXRank, row.DEXRank, threshold)
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

// classifyDivergence implements the category truth table:
//
//	(cex, dex)             -> aligned
//	(cex,   _)             -> cex_only
//	(  _, dex)             -> dex_only
//	(cex, dex) Δ>=N, cex<dex -> cex_heavy (more popular on CEX)
//	(cex, dex) Δ>=N, dex<cex -> dex_heavy
//
// "cex<dex" reads "lower CEX rank number", i.e. CEX places it higher.
func classifyDivergence(cexRank, dexRank *int, threshold int) string {
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

// categoryPriority defines the sort order used in buildDivergenceRows.
// cex_only / dex_only carry the strongest operational signal (no overlap
// at all between venue classes) so they surface first; heavy categories
// next; aligned rows last since they're least actionable.
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
	// cex_only / dex_only rows have no Δ; treat them as max-Δ for
	// sorting so they top their category bucket. top30AggregateLimit is
	// the rank ceiling, so anything beyond that is "off the chart".
	return top30AggregateLimit + 1
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
	return top30AggregateLimit + 1
}

func indexAggregateBySymbol(rows []domain.Top30AggregateRow) map[string]domain.Top30AggregateRow {
	out := make(map[string]domain.Top30AggregateRow, len(rows))
	for _, row := range rows {
		out[canonicaliseDivergenceSymbol(row.Symbol)] = row
	}
	return out
}

// divergenceQuoteSuffixes lists the perp settlement quotes we collapse
// onto the base asset. Order is significant only insofar as longer
// suffixes must come first when two share a prefix (e.g. "-USDT" vs the
// generic "-USD"); we do that explicitly here so the first match wins.
//
// The list intentionally mirrors the quotes NormaliseSymbol emits (USDT,
// USDC, USD) plus the legacy CEX-issued stablecoins (BUSD, FDUSD, TUSD)
// in case CoinGecko ever serves a back-cataloged pair on those quotes.
// Adding a new quote here is a one-line change with a regression test
// added to Top30Divergence_MergesAcrossQuoteVariants.
var divergenceQuoteSuffixes = []string{
	"-USDT", "-USDC", "-USD", "-BUSD", "-FDUSD", "-TUSD",
}

// canonicaliseDivergenceSymbol normalises a Top30 Symbol to the base
// asset used as the cross-camp join key. CoinGecko collector emits
// venue-specific forms via NormaliseSymbol — e.g. "BTC-USDT (perp)" on
// every CEX, "BTC-USDC (perp)" on Hyperliquid, "BTC-USD (perp)" on
// edgeX — that all denote the same BTC perpetual product. For the
// divergence view the quote is noise: CEX BTC-USDT, DEX BTC-USDC and
// edgeX BTC-USD must collapse into a single "BTC" row so the
// CEX-vs-DEX comparison shows BTC as aligned (which it is, both camps
// list it heavily) instead of three different cex_only/dex_only rows.
//
// Aligns with the symbol-resolution semantics elsewhere in the
// dashboard (Store.ResolveSymbol / SymbolSub.Canonical), but we don't
// need to consult the symbol catalog because:
//   - the catalog only covers the ~74 curated canonicals; Top30 long
//     tails (PUMP, MEW, MOODENG, …) wouldn't be in it
//   - NormaliseSymbol already collapses all venue tickers to the same
//     "BASE-QUOTE (perp)" shape, so a stable quote-suffix strip is
//     equivalent to a full catalog lookup for symbols that ARE in the
//     catalog, and deterministic for those that aren't.
//
// Empty / whitespace-only input returns empty.
func canonicaliseDivergenceSymbol(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, " (PERP)")
	for _, suffix := range divergenceQuoteSuffixes {
		if strings.HasSuffix(s, suffix) {
			return strings.TrimSuffix(s, suffix)
		}
	}
	return s
}
