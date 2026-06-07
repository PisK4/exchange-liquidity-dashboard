package collector

import (
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

type StoreSnapshot struct {
	Config                   config.Config
	Symbols                  []domain.SymbolSub
	Platforms                map[string]domain.PlatformSnapshot
	PlatformHistory          map[string][]domain.PlatformSnapshot
	Volumes                  map[string]domain.VolumeSnapshot
	Status                   []domain.CollectionStatus
	Run                      RunSummary
	CoinGeckoPlatformVolumes map[string]domain.PlatformVolumeAggregate
	DailyPlatformVolumes     map[string][]domain.DailyVolumeAggregate
	DailySymbolVolumes       map[string][]domain.DailyVolumeAggregate
	Top30ByPlatform          map[string][]domain.Top30Row
	Top30BackfillSkipCounts  map[string]map[string]int
	CoinGeckoLastPullTS      time.Time
	CoinGeckoGovernance      map[string]any
}

func (s *Store) Snapshot() StoreSnapshot {
	if snap := s.snapshot.Load(); snap != nil {
		return snap.clone()
	}
	s.publishSnapshot()
	if snap := s.snapshot.Load(); snap != nil {
		return snap.clone()
	}
	return StoreSnapshot{}
}

func (s *Store) publishSnapshot() {
	s.mu.RLock()
	snap := s.buildSnapshotLocked()
	s.mu.RUnlock()
	s.snapshot.Store(&snap)
}

func (s *Store) publishSnapshotLocked() {
	snap := s.buildSnapshotLocked()
	s.snapshot.Store(&snap)
}

func (s *Store) buildSnapshotLocked() StoreSnapshot {
	return StoreSnapshot{
		Config:                   cloneConfig(s.cfg),
		Symbols:                  cloneSlice(s.symbolSnapshot()),
		Platforms:                clonePlatformMap(s.platforms),
		PlatformHistory:          clonePlatformHistory(s.platformHistory),
		Volumes:                  cloneMap(s.volumes),
		Status:                   cloneSlice(s.status),
		Run:                      s.run,
		CoinGeckoPlatformVolumes: cloneMap(s.cgPlatformVolumes),
		DailyPlatformVolumes:     cloneMapOfSlices(s.dailyPlatformVolumes),
		DailySymbolVolumes:       cloneMapOfSlices(s.dailySymbolVolumes),
		Top30ByPlatform:          cloneMapOfSlices(s.top30ByPlatform),
		Top30BackfillSkipCounts:  cloneNestedIntMap(s.top30BackfillSkipCounts),
		CoinGeckoLastPullTS:      s.cgLastPullTS,
		CoinGeckoGovernance:      cloneMap(s.cgGovernance),
	}
}

func (snap StoreSnapshot) clone() StoreSnapshot {
	return StoreSnapshot{
		Config:                   cloneConfig(snap.Config),
		Symbols:                  cloneSlice(snap.Symbols),
		Platforms:                clonePlatformMap(snap.Platforms),
		PlatformHistory:          clonePlatformHistory(snap.PlatformHistory),
		Volumes:                  cloneMap(snap.Volumes),
		Status:                   cloneSlice(snap.Status),
		Run:                      snap.Run,
		CoinGeckoPlatformVolumes: cloneMap(snap.CoinGeckoPlatformVolumes),
		DailyPlatformVolumes:     cloneMapOfSlices(snap.DailyPlatformVolumes),
		DailySymbolVolumes:       cloneMapOfSlices(snap.DailySymbolVolumes),
		Top30ByPlatform:          cloneMapOfSlices(snap.Top30ByPlatform),
		Top30BackfillSkipCounts:  cloneNestedIntMap(snap.Top30BackfillSkipCounts),
		CoinGeckoLastPullTS:      snap.CoinGeckoLastPullTS,
		CoinGeckoGovernance:      cloneMap(snap.CoinGeckoGovernance),
	}
}

func cloneConfig(in config.Config) config.Config {
	out := in
	out.Symbols = cloneSlice(in.Symbols)
	out.Platforms = cloneSlice(in.Platforms)
	out.Runtime.DepthTiers = cloneSlice(in.Runtime.DepthTiers)
	out.Runtime.SlippageBucketsUSD = cloneSlice(in.Runtime.SlippageBucketsUSD)
	out.Runtime.VolumeDiscounts = cloneMap(in.Runtime.VolumeDiscounts)
	out.Runtime.WSProviders = cloneMap(in.Runtime.WSProviders)
	out.Runtime.StalenessByCategory = cloneMap(in.Runtime.StalenessByCategory)
	out.Runtime.CoinGecko.ExchangeID = cloneMap(in.Runtime.CoinGecko.ExchangeID)
	out.Runtime.CoinGecko.MarketName = cloneMap(in.Runtime.CoinGecko.MarketName)
	return out
}

func clonePlatformMap(in map[string]domain.PlatformSnapshot) map[string]domain.PlatformSnapshot {
	if in == nil {
		return nil
	}
	out := make(map[string]domain.PlatformSnapshot, len(in))
	for k, v := range in {
		out[k] = clonePlatformSnapshot(v)
	}
	return out
}

func clonePlatformHistory(in map[string][]domain.PlatformSnapshot) map[string][]domain.PlatformSnapshot {
	if in == nil {
		return nil
	}
	out := make(map[string][]domain.PlatformSnapshot, len(in))
	for k, rows := range in {
		dup := make([]domain.PlatformSnapshot, len(rows))
		for i, row := range rows {
			dup[i] = clonePlatformSnapshot(row)
		}
		out[k] = dup
	}
	return out
}

func clonePlatformSnapshot(row domain.PlatformSnapshot) domain.PlatformSnapshot {
	row.DepthByTier = cloneDepthMetricsMap(row.DepthByTier)
	row.VsMedianByTier = cloneMap(row.VsMedianByTier)
	row.BuySlippageBP = cloneMap(row.BuySlippageBP)
	row.SellSlippageBP = cloneMap(row.SellSlippageBP)
	row.WorstSlippageBP = cloneMap(row.WorstSlippageBP)
	if row.LastCollectionTS != nil {
		ts := *row.LastCollectionTS
		row.LastCollectionTS = &ts
	}
	return row
}

func cloneDepthMetricsMap(in map[string]domain.DepthMetrics) map[string]domain.DepthMetrics {
	if in == nil {
		return nil
	}
	out := make(map[string]domain.DepthMetrics, len(in))
	for k, v := range in {
		v.AggregationParams = cloneMap(v.AggregationParams)
		out[k] = v
	}
	return out
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

func cloneMap[T any](in map[string]T) map[string]T {
	if in == nil {
		return nil
	}
	out := make(map[string]T, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneMapOfSlices[T any](in map[string][]T) map[string][]T {
	if in == nil {
		return nil
	}
	out := make(map[string][]T, len(in))
	for k, v := range in {
		out[k] = cloneSlice(v)
	}
	return out
}

func cloneNestedIntMap(in map[string]map[string]int) map[string]map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]int, len(in))
	for k, v := range in {
		out[k] = cloneMap(v)
	}
	return out
}
