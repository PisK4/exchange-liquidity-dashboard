package liquidity

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EdgexPlatform is the canonical lower-case platform key for edgeX
// throughout the dashboard. Mirroring it here lets Compute stay
// independent of internal/collector and internal/config.
const EdgexPlatform = "edgex"

// Compute scans the per-canonical depth matrix and emits one
// AlertCandidate per (canonical, kind) pair that satisfies the
// trigger conditions in spec §1. The function is pure: matrix and
// cfg are not mutated, and the output order is deterministic
// (canonical ASC, then kind: liquidity_lag before worst_depth).
//
// Filtering precedence (mirrors spec §1 "共同红线"):
//
//  1. universe.IsListed("edgeX", canonical) == false → skip
//  2. resolver.IsPlatformExclusive(canonical) == true → skip
//  3. fewer than cfg.MinComparators non-edgeX rows → skip
//  4. edgeX row missing or depth ≤ 0 → skip (cannot rank)
//
// Then emit at most two candidates:
//   - lag: edgex < median * cfg.LagThreshold
//   - worst: edgexRank == TotalPlatforms (i.e. the LAST place when
//     1-indexed; spec calls this "倒数第一" semantically — the PRD
//     says "倒数第二" but the only safe interpretation when edgeX
//     is at the bottom of the ladder is "last", because if edgeX
//     is N-1 there must be something below it which we already
//     ranked. The spec doc clarifies: rank == N-1 in 0-indexed,
//     which equals "last 0-indexed", i.e. TotalPlatforms-1 in
//     1-indexed terms. We unify both rules to "edgeX is at the
//     bottom of the ranking" because PRD-wise that is the strongest
//     possible signal anyway).
//
// IMPORTANT: we intentionally use 1-indexed Rank in the output
// candidate (1 == strongest) because that's how the card renders
// it. Internally we still sort the depths descending.
func Compute(
	matrix map[string]map[string]PlatformDepthRow,
	universe ListedLookup,
	resolver CanonicalResolver,
	cfg Config,
	now time.Time,
) []AlertCandidate {
	if cfg.MinComparators <= 0 {
		cfg.MinComparators = 3
	}
	if cfg.LagThreshold <= 0 || cfg.LagThreshold >= 1 {
		cfg.LagThreshold = 0.5
	}
	tierLabel := formatTierLabel(cfg.DepthTierPct)

	canonicals := make([]string, 0, len(matrix))
	for canonical := range matrix {
		canonicals = append(canonicals, canonical)
	}
	sort.Strings(canonicals)

	out := make([]AlertCandidate, 0, len(canonicals))
	for _, canonical := range canonicals {
		perPlatform := matrix[canonical]
		if len(perPlatform) == 0 {
			continue
		}
		if universe != nil && !universe.IsListed("edgeX", canonical) {
			continue
		}
		if resolver != nil && resolver.IsPlatformExclusive(canonical) {
			continue
		}
		edgex, edgexOK := lookupEdgex(perPlatform)
		if !edgexOK || edgex.DepthUSD <= 0 || math.IsNaN(edgex.DepthUSD) {
			continue
		}

		competitors := collectCompetitorRows(perPlatform)
		if len(competitors) < cfg.MinComparators {
			continue
		}

		median := medianDepth(competitors)
		if median <= 0 {
			continue
		}

		platforms := buildPlatformRows(perPlatform, median)
		totalPlatforms := len(platforms)
		edgexRank := edgexRankFromPlatforms(platforms)
		if edgexRank == 0 {
			continue
		}
		ratio := edgex.DepthUSD / median

		baseCandidate := AlertCandidate{
			Canonical:      canonical,
			DisplaySymbol:  edgex.DisplaySymbol,
			Tier:           tierLabel,
			EdgexDepth:     edgex.DepthUSD,
			MedianDepth:    median,
			Ratio:          ratio,
			Comparators:    len(competitors),
			TotalPlatforms: totalPlatforms,
			EdgexRank:      edgexRank,
			Platforms:      platforms,
			EvaluatedAt:    now,
		}

		if ratio < cfg.LagThreshold {
			lag := baseCandidate
			lag.Kind = KindLiquidityLag
			out = append(out, lag)
		}
		if edgexRank == totalPlatforms {
			worst := baseCandidate
			worst.Kind = KindWorstDepth
			out = append(out, worst)
		}
	}
	return out
}

func lookupEdgex(perPlatform map[string]PlatformDepthRow) (PlatformDepthRow, bool) {
	for plat, row := range perPlatform {
		if strings.EqualFold(plat, EdgexPlatform) {
			return row, true
		}
	}
	return PlatformDepthRow{}, false
}

func collectCompetitorRows(perPlatform map[string]PlatformDepthRow) []PlatformDepthRow {
	out := make([]PlatformDepthRow, 0, len(perPlatform))
	for plat, row := range perPlatform {
		if strings.EqualFold(plat, EdgexPlatform) {
			continue
		}
		if row.DepthUSD <= 0 || math.IsNaN(row.DepthUSD) {
			continue
		}
		out = append(out, row)
	}
	return out
}

// medianDepth computes the classic statistical median over the input
// rows' DepthUSD column. Even N → average of the two middle values.
// Returns 0 when rows is empty.
func medianDepth(rows []PlatformDepthRow) float64 {
	depths := make([]float64, 0, len(rows))
	for _, r := range rows {
		depths = append(depths, r.DepthUSD)
	}
	if len(depths) == 0 {
		return 0
	}
	sort.Float64s(depths)
	n := len(depths)
	if n%2 == 1 {
		return depths[n/2]
	}
	return (depths[n/2-1] + depths[n/2]) / 2.0
}

// buildPlatformRows constructs the sorted-by-depth platform list
// that ends up on the Lark card. Rank is 1-indexed (1 == strongest).
// IsMedian marks rows whose depth equals the competitor median value
// (post-floor for even counts the marker may apply to two rows).
func buildPlatformRows(perPlatform map[string]PlatformDepthRow, median float64) []AlertPlatformRow {
	rows := make([]AlertPlatformRow, 0, len(perPlatform))
	for plat, row := range perPlatform {
		if row.DepthUSD <= 0 || math.IsNaN(row.DepthUSD) {
			continue
		}
		rows = append(rows, AlertPlatformRow{
			Platform:   plat,
			DepthUSD:   row.DepthUSD,
			IsEdgex:    strings.EqualFold(plat, EdgexPlatform),
			SnapshotTS: row.SnapshotTS,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].DepthUSD == rows[j].DepthUSD {
			return rows[i].Platform < rows[j].Platform
		}
		return rows[i].DepthUSD > rows[j].DepthUSD
	})
	for i := range rows {
		rows[i].Rank = i + 1
		if median > 0 && math.Abs(rows[i].DepthUSD-median) < 1e-9 {
			rows[i].IsMedian = true
		}
	}
	return rows
}

func edgexRankFromPlatforms(rows []AlertPlatformRow) int {
	for _, r := range rows {
		if r.IsEdgex {
			return r.Rank
		}
	}
	return 0
}

// formatTierLabel converts 0.001 → "0.1%". Two decimal places are
// emitted only when needed (0.0005 → "0.05%", 0.02 → "2%").
func formatTierLabel(tier float64) string {
	if tier <= 0 {
		return ""
	}
	pct := tier * 100
	if pct == math.Trunc(pct) {
		return strconv.FormatInt(int64(pct), 10) + "%"
	}
	formatted := strconv.FormatFloat(pct, 'f', 4, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted + "%"
}

// formatPercent renders a 0-to-1 ratio as a "%" string with one
// fractional digit, e.g. 0.4138 → "41.4%". Used by render.go.
func formatPercent(ratio float64) string {
	return fmt.Sprintf("%.1f%%", ratio*100)
}
