package collector

import (
	"edgex-dashboard/backend/internal/divergence"
	"edgex-dashboard/backend/internal/domain"
)

// Top30Divergence builds the CEX-vs-DEX aggregate comparison from the
// already-cached per-platform Top30 snapshots. The method is read-only:
// no MySQL access, no network calls, no new tables.
//
// As of the divergence-package refactor the heavy lifting (per-class
// aggregation, outer join, classification, KPI strip) lives in the
// internal/divergence shared package. This method only adapts the
// store's in-memory top30ByPlatform map into []divergence.InputRow
// values and forwards them; the API contract on
// /api/snapshot/top30/divergence is preserved verbatim.
//
// The adapter intentionally collapses the bool-typed Top30Row.EdgexListed
// onto an *bool. This is the boundary where the collector's bool flag
// loses its three-state distinction: a Top30Row with EdgexListed=false
// becomes *false here (not nil) so the divergence KPI keeps counting
// it as an edgeX gap, matching legacy behaviour for the API view. The
// listing-side producer (Phase 2) reads its t_top30_snapshot rows
// directly into divergence.InputRow values, where the adapter can
// preserve the true three-state semantics from MySQL's NULL/0/1.
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
	rows := s.divergenceInputRowsLocked()
	return divergence.Compute(rows, divergence.Config{
		CEXPlatforms:         cfg.CEXPlatforms,
		DEXPlatforms:         cfg.DEXPlatforms,
		SignificantRankDelta: cfg.SignificantRankDelta,
		Resolver:             s.cfg.CanonicalIndex,
	})
}

// divergenceInputRowsLocked materialises the InputRow stream the
// divergence package consumes from the store's per-platform Top30
// cache. Filters down to the CEX ∪ DEX platform sets configured on the
// runtime so an experimental platform sitting in top30ByPlatform never
// pollutes the comparison.
//
// Caller MUST hold s.mu (RLock or Lock).
func (s *Store) divergenceInputRowsLocked() []divergence.InputRow {
	cfg := s.cfg.Runtime.Top30Divergence
	member := make(map[string]struct{}, len(cfg.CEXPlatforms)+len(cfg.DEXPlatforms))
	for _, p := range cfg.CEXPlatforms {
		member[p] = struct{}{}
	}
	for _, p := range cfg.DEXPlatforms {
		member[p] = struct{}{}
	}
	var out []divergence.InputRow
	for platform, rows := range s.top30ByPlatform {
		if _, ok := member[platform]; !ok {
			continue
		}
		for _, row := range rows {
			listed := row.EdgexListed
			out = append(out, divergence.InputRow{
				Platform:     platform,
				Symbol:       row.Symbol,
				Rank:         row.Rank,
				Volume24HUSD: row.Volume24HUSD,
				Status:       row.Status,
				SnapshotTS:   row.SnapshotTS,
				// Collector source has a bool flag with no concept of
				// "unknown"; promote that to *bool so the package's
				// three-state semantics still hold on this boundary.
				EdgexListed: &listed,
			})
		}
	}
	return out
}
