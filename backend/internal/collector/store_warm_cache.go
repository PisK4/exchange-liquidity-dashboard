package collector

import "edgex-ops-intelligence/backend/internal/startup"

// WarmCacheSummary reports whether LoadLatestFromDB (or an earlier in-process
// collection) has published enough dashboard data for the API to serve while
// live collectors continue warming in the background.
func (s *Store) WarmCacheSummary() startup.WarmCacheSummary {
	snap := s.Snapshot()
	top30Rows := 0
	for _, rows := range snap.Top30ByPlatform {
		top30Rows += len(rows)
	}
	summary := startup.WarmCacheSummary{
		PlatformSnapshots:    len(snap.Platforms),
		VolumeSnapshots:      len(snap.Volumes),
		Top30Rows:            top30Rows,
		CollectionStatusRows: len(snap.Status),
	}
	summary.HasUsableData = summary.PlatformSnapshots > 0 || summary.VolumeSnapshots > 0 || summary.Top30Rows > 0
	return summary
}
