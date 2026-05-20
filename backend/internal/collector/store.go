package collector

import (
	"context"
	"database/sql"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/indicators"
)

type Store struct {
	mu              sync.RWMutex
	cfg             config.Config
	platforms       map[string]domain.PlatformSnapshot
	platformHistory map[string][]domain.PlatformSnapshot
	volumes         map[string]domain.VolumeSnapshot
	status          []domain.CollectionStatus
	run             RunSummary
	db              *sql.DB

	// liveSymbols holds the symbol slice consumed by the API surface
	// (Symbols / SymbolMappings / Coverage). It starts as a copy of
	// cfg.Symbols and is hot-swapped by ReloadCatalogFrontendMeta when
	// instrument_catalog.yaml changes on disk. Only display-side metadata
	// (FrontendURL / URLVerified / CatalogStatus) is hot-reloaded; the
	// collector keeps reading the immutable cfg.Symbols snapshot it
	// captured at construction so adapter-critical fields (api_symbol,
	// contract_id, market_id, contract_size, quanto_multiplier,
	// api_level_cap) cannot change mid-cycle.
	liveSymbols atomic.Pointer[[]domain.SymbolSub]

	// CoinGecko-only path (R3: strictly segregated from native volumes map).
	// cgPlatformVolumes holds the latest /derivatives 24h aggregate per
	// competitor platform; Share(24h) reads this map for the 9 competitors
	// and never merges it with `volumes`.
	cgPlatformVolumes map[string]domain.PlatformVolumeAggregate

	// Daily rollups for 7d/30d windows. Each map value is sorted ascending
	// by Day. dailyPlatformVolumes keys on platform; dailySymbolVolumes
	// keys on "platform|display_symbol" for symbol-level KPIs.
	dailyPlatformVolumes map[string][]domain.DailyVolumeAggregate
	dailySymbolVolumes   map[string][]domain.DailyVolumeAggregate

	// Top30 ranking per platform, refreshed on every CoinGecko collector
	// run. Top30 returns the slice as-is once filled; empty slice keeps the
	// existing "unsupported" fallback.
	top30ByPlatform map[string][]domain.Top30Row

	cgLastPullTS time.Time
}

type RunSummary struct {
	RunID       string    `json:"run_id"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Success     int       `json:"success"`
	Failed      int       `json:"failed"`
}

func NewStore(cfg config.Config) *Store {
	s := &Store{
		cfg:                  cfg,
		platforms:            map[string]domain.PlatformSnapshot{},
		platformHistory:      map[string][]domain.PlatformSnapshot{},
		volumes:              map[string]domain.VolumeSnapshot{},
		cgPlatformVolumes:    map[string]domain.PlatformVolumeAggregate{},
		dailyPlatformVolumes: map[string][]domain.DailyVolumeAggregate{},
		dailySymbolVolumes:   map[string][]domain.DailyVolumeAggregate{},
		top30ByPlatform:      map[string][]domain.Top30Row{},
	}
	initial := append([]domain.SymbolSub(nil), cfg.Symbols...)
	s.liveSymbols.Store(&initial)
	return s
}

// symbolSnapshot returns the slice currently published to the API surface.
// Callers must treat the returned slice as read-only; it is shared with all
// future readers until the next ReloadCatalogFrontendMeta swap.
func (s *Store) symbolSnapshot() []domain.SymbolSub {
	if p := s.liveSymbols.Load(); p != nil {
		return *p
	}
	return s.cfg.Symbols
}

// ReloadCatalogFrontendMeta rebuilds the live symbol slice from the
// immutable cfg.Symbols base and overwrites only the display-side metadata
// fields (FrontendURL / URLVerified / CatalogStatus) from cat. Other catalog
// fields are intentionally ignored: changing api_symbol / contract_id /
// market_id / contract_size / quanto_multiplier / api_level_cap mid-process
// would silently desync the running collector and its persisted rows. Such
// fields still require a full restart. Returns the number of symbols
// touched (entries that had at least one of the three metadata fields
// changed compared to the current live slice).
func (s *Store) ReloadCatalogFrontendMeta(cat config.Catalog) int {
	base := append([]domain.SymbolSub(nil), s.cfg.Symbols...)
	if len(cat.Platforms) > 0 {
		for i := range base {
			platform := cat.Platforms[base[i].Platform]
			if platform == nil {
				continue
			}
			entry, ok := platform[base[i].Canonical]
			if !ok {
				continue
			}
			base[i].FrontendURL = entry.FrontendURL
			base[i].URLVerified = entry.URLVerified
			base[i].CatalogStatus = entry.CatalogStatus
		}
	}
	previous := s.symbolSnapshot()
	changed := 0
	if len(previous) == len(base) {
		for i := range base {
			if base[i].FrontendURL != previous[i].FrontendURL ||
				base[i].URLVerified != previous[i].URLVerified ||
				base[i].CatalogStatus != previous[i].CatalogStatus {
				changed++
			}
		}
	} else {
		changed = len(base)
	}
	s.liveSymbols.Store(&base)
	return changed
}

// WatchCatalog polls the catalog yaml mtime every `interval` and applies a
// hot-reload of frontend metadata whenever the file changes. The first
// observed mtime is recorded but not treated as a change so a freshly
// booted process does not log a spurious reload. The watcher exits when
// ctx is done; transient stat / parse errors are logged and the loop
// continues so a half-saved editor write does not kill the watcher.
func (s *Store) WatchCatalog(ctx context.Context, path string, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	var lastMtime int64
	if fi, err := os.Stat(path); err == nil {
		lastMtime = fi.ModTime().UnixNano()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fi, err := os.Stat(path)
			if err != nil {
				if !os.IsNotExist(err) {
					log.Printf("instrument_catalog.yaml stat: %v", err)
				}
				continue
			}
			mt := fi.ModTime().UnixNano()
			if mt == lastMtime {
				continue
			}
			lastMtime = mt
			cat, err := config.LoadCatalog(path)
			if err != nil {
				log.Printf("instrument_catalog.yaml reload failed: %v", err)
				continue
			}
			changed := s.ReloadCatalogFrontendMeta(cat)
			log.Printf("instrument_catalog.yaml reloaded (%d symbol metadata updates)", changed)
		}
	}
}

func key(platform, symbol string) string { return platform + "|" + symbol }

func (s *Store) SavePlatformSnapshot(row domain.PlatformSnapshot) {
	s.mu.Lock()
	s.savePlatformSnapshotLocked(row)
	s.mu.Unlock()
	if err := s.persistPlatformSnapshot(context.Background(), row); err != nil {
		log.Printf("persist platform snapshot: %v", err)
	}
}

func (s *Store) savePlatformSnapshotLocked(row domain.PlatformSnapshot) {
	k := key(row.Platform, row.DisplaySymbol)
	s.platforms[k] = row
	s.platformHistory[k] = append(s.platformHistory[k], row)
}

func (s *Store) SaveVolume(row domain.VolumeSnapshot) {
	s.mu.Lock()
	s.volumes[key(row.Platform, row.DisplaySymbol)] = row
	s.mu.Unlock()
	if err := s.persistVolume(context.Background(), row); err != nil {
		log.Printf("persist volume snapshot: %v", err)
	}
	// edgeX platform-level Share KPIs are sourced exclusively from
	// CoinGecko now (see share24hLocked / shareHistoricalLocked); we no
	// longer mirror per-symbol native volume into dailyPlatformVolumes.
	// The native ticker is still persisted to s.volumes above for the
	// per-symbol Liquidity tab, where CoinGecko's platform-level
	// aggregate cannot be decomposed.
}

// SaveCoinGeckoPlatformVolumes records the latest CoinGecko-sourced 24h
// volume per competitor platform. The store keeps this map strictly separate
// from `volumes` so Share(24h) for the 9 competitors never reads native
// per-symbol numbers (R3: prevents Lighter / Hyperliquid double-counting).
func (s *Store) SaveCoinGeckoPlatformVolumes(rows []domain.PlatformVolumeAggregate) {
	if len(rows) == 0 {
		return
	}
	s.mu.Lock()
	for _, row := range rows {
		if row.Platform == "" {
			continue
		}
		if row.DataSource == "" {
			row.DataSource = domain.DataSourceCoinGecko
		}
		s.cgPlatformVolumes[row.Platform] = row
		if row.SnapshotTS.After(s.cgLastPullTS) {
			s.cgLastPullTS = row.SnapshotTS
		}
	}
	s.mu.Unlock()
	for _, row := range rows {
		if row.Platform == "" {
			continue
		}
		if err := s.persistCoinGeckoPlatformVolume(context.Background(), row); err != nil {
			log.Printf("persist coingecko platform volume: %v", err)
		}
	}
}

// SaveDailyVolumeAggregates inserts or replaces per-day rollups. Volume24HUSD
// is always raw USD; AdjustedVolume() is applied only at query time so
// MEXC×0.4 / Gate×0.5 discounts cannot accidentally leak into stored values.
func (s *Store) SaveDailyVolumeAggregates(rows []domain.DailyVolumeAggregate) {
	if len(rows) == 0 {
		return
	}
	s.mu.Lock()
	for _, row := range rows {
		if row.Platform == "" {
			continue
		}
		if row.DataSource == "" {
			row.DataSource = domain.DataSourceNative
		}
		row.Day = startOfUTCDay(row.Day)
		if row.DisplaySymbol == "" {
			s.dailyPlatformVolumes[row.Platform] = mergeDailyAggregate(s.dailyPlatformVolumes[row.Platform], row)
		} else {
			k := key(row.Platform, row.DisplaySymbol)
			s.dailySymbolVolumes[k] = mergeDailyAggregate(s.dailySymbolVolumes[k], row)
		}
	}
	s.mu.Unlock()
	for _, row := range rows {
		if row.Platform == "" {
			continue
		}
		row.Day = startOfUTCDay(row.Day)
		if err := s.persistDailyVolumeAggregate(context.Background(), row); err != nil {
			log.Printf("persist daily volume aggregate: %v", err)
		}
	}
}

// SaveTop30 replaces the cached Top30 ranking for the given (platform) key.
// CoinGecko delivers all rows in a single response, so callers pass the full
// slice for one platform at a time.
func (s *Store) SaveTop30(platform string, rows []domain.Top30Row) {
	if platform == "" {
		return
	}
	dup := make([]domain.Top30Row, len(rows))
	copy(dup, rows)
	s.mu.Lock()
	s.top30ByPlatform[platform] = dup
	s.mu.Unlock()
	if err := s.persistTop30(context.Background(), platform, dup); err != nil {
		log.Printf("persist top30: %v", err)
	}
}

func (s *Store) SaveStatus(rows []domain.CollectionStatus, run RunSummary) {
	s.mu.Lock()
	s.status = rows
	s.run = run
	s.mu.Unlock()
	if err := s.persistStatus(context.Background(), rows, run); err != nil {
		log.Printf("persist collection status: %v", err)
	}
}

func (s *Store) Symbols() []string {
	seen := map[string]bool{}
	var out []string
	for _, sub := range s.symbolSnapshot() {
		if !seen[sub.DisplaySymbol] {
			seen[sub.DisplaySymbol] = true
			out = append(out, sub.DisplaySymbol)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) SymbolMappings() []domain.SymbolSub {
	return append([]domain.SymbolSub(nil), s.symbolSnapshot()...)
}

func (s *Store) Coverage() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []map[string]any{}
	for _, sub := range s.symbolSnapshot() {
		p, ok := s.platforms[key(sub.Platform, sub.DisplaySymbol)]
		status := domain.StatusStale
		if ok {
			status = p.DepthStatus
		}
		rows = append(rows, map[string]any{"platform": sub.Platform, "display_symbol": sub.DisplaySymbol, "depth_status": status, "source_endpoint": sub.SourceEndpoint})
	}
	return map[string]any{"rows": rows, "snapshot_ts": time.Now().UTC()}
}

func (s *Store) DashboardMeta() map[string]any {
	out := map[string]any{
		"tabs":                 []string{"monitor", "quality", "share", "top30"},
		"platforms":            s.cfg.Platforms,
		"symbols":              s.Symbols(),
		"windows":              []string{"24h", "7d", "30d"},
		"depth_tiers":          s.cfg.Runtime.DepthTiers,
		"slippage_buckets_usd": s.cfg.Runtime.SlippageBucketsUSD,
		"refresh_interval_sec": int(s.cfg.Runtime.CollectionInterval.Seconds()),
		"volume_discounts":     s.cfg.Runtime.VolumeDiscounts,
	}
	cg := s.cfg.Runtime.CoinGecko
	if cg.Enabled {
		s.mu.RLock()
		lastPull := s.cgLastPullTS
		s.mu.RUnlock()
		ids := make([]string, 0, len(cg.ExchangeID))
		for _, id := range cg.ExchangeID {
			if id != "" {
				ids = append(ids, id)
			}
		}
		names := make([]string, 0, len(cg.MarketName))
		for _, n := range cg.MarketName {
			if n != "" {
				names = append(names, n)
			}
		}
		sort.Strings(ids)
		sort.Strings(names)
		meta := map[string]any{
			"enabled":       true,
			"exchange_ids":  ids,
			"market_names":  names,
			"pull_interval": cg.PullInterval.String(),
		}
		if !lastPull.IsZero() {
			meta["last_pull_ts"] = lastPull
		}
		out["data_sources"] = map[string]any{"coingecko": meta}
	}
	return out
}

func (s *Store) Liquidity(symbol string) map[string]any {
	if symbol == "" {
		symbol = "BTC-USDT (perp)"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []domain.PlatformSnapshot{}
	for _, p := range s.cfg.Platforms {
		if row, ok := s.displayPlatformSnapshotLocked(p, symbol); ok {
			rows = append(rows, row)
		} else {
			rows = append(rows, missingPlatform(p, symbol))
		}
	}
	medians := competitorMedianByTier(rows)
	rows = enrichLiquidityRows(rows, medians)
	return map[string]any{
		"symbol":                    symbol,
		"snapshot_ts":               latestTS(rows),
		"rows":                      rows,
		"competitor_median_by_tier": medians,
		"kpis":                      s.liquidityKPIsLocked(rows, medians, symbol),
	}
}

func (s *Store) Quality(symbol string) map[string]any {
	if symbol == "" {
		symbol = "BTC-USDT (perp)"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []domain.PlatformSnapshot{}
	for _, p := range s.cfg.Platforms {
		if row, ok := s.displayPlatformSnapshotLocked(p, symbol); ok {
			rows = append(rows, row)
		} else {
			rows = append(rows, missingPlatform(p, symbol))
		}
	}
	rows = enrichQualityRows(rows)
	return map[string]any{"symbol": symbol, "snapshot_ts": latestTS(rows), "slippage_buckets_usd": s.cfg.Runtime.SlippageBucketsUSD, "rows": rows}
}

func (s *Store) Share(window string) map[string]any {
	if window == "" {
		window = "24h"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch window {
	case "24h":
		return s.share24hLocked()
	case "7d":
		return s.shareHistoricalLocked("7d", 7)
	case "30d":
		return s.shareHistoricalLocked("30d", 30)
	default:
		return map[string]any{
			"window":      window,
			"status":      domain.StatusUnsupported,
			"reason":      "unsupported window",
			"rows":        []any{},
			"snapshot_ts": time.Now().UTC(),
			"trend":       map[string]any{"status": domain.StatusUnsupported},
		}
	}
}

// share24hLocked implements R3: every platform — edgeX and the 9
// competitors alike — sources its 24h volume from CoinGecko
// (s.cgPlatformVolumes). The native per-symbol map (s.volumes) is never
// merged in, so Lighter / Hyperliquid / edgeX native data cannot
// double-count against CoinGecko aggregates.
func (s *Store) share24hLocked() map[string]any {
	now := time.Now().UTC()
	rawByPlatform := map[string]float64{}
	adjustedByPlatform := map[string]float64{}
	statusByPlatform := map[string]string{}
	sourceByPlatform := map[string]string{}
	snapshotTSByPlatform := map[string]time.Time{}

	for _, p := range s.cfg.Platforms {
		agg, ok := s.cgPlatformVolumes[p]
		if !ok {
			statusByPlatform[p] = domain.StatusStale
			continue
		}
		statusByPlatform[p] = agg.Status
		sourceByPlatform[p] = domain.DataSourceCoinGecko
		if agg.SnapshotTS.After(snapshotTSByPlatform[p]) {
			snapshotTSByPlatform[p] = agg.SnapshotTS
		}
		if agg.Status == domain.StatusComplete && agg.Volume24HUSD > 0 {
			rawByPlatform[p] = agg.Volume24HUSD
			adjustedByPlatform[p] = indicators.AdjustedVolume(p, agg.Volume24HUSD)
		}
	}

	var denom float64
	for _, v := range adjustedByPlatform {
		denom += v
	}

	rows := []map[string]any{}
	for _, p := range s.cfg.Platforms {
		status := statusByPlatform[p]
		if status == "" {
			status = domain.StatusStale
		}
		row := map[string]any{
			"platform":    p,
			"discount":    discount(p),
			"status":      status,
			"data_source": sourceByPlatform[p],
		}
		if status == domain.StatusComplete {
			share := 0.0
			if denom > 0 {
				share = adjustedByPlatform[p] / denom * 100
			}
			row["raw_volume_usd"] = rawByPlatform[p]
			row["adjusted_volume_usd"] = adjustedByPlatform[p]
			row["adjusted_volume_24h_usd"] = adjustedByPlatform[p]
			row["share_pct"] = share
			row["denominator_pct"] = share
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return numeric(rows[i]["adjusted_volume_usd"]) > numeric(rows[j]["adjusted_volume_usd"])
	})
	for i := range rows {
		rows[i]["rank"] = i + 1
	}

	historyStatus := s.historicalShareStatusLocked()
	kpis := map[string]any{
		"edgex_share_pct":        shareForAdjusted(adjustedByPlatform["edgeX"], denom),
		"edgex_total_volume_usd": rawByPlatform["edgeX"],
		"denominator_usd":        denom,
	}
	trend := s.shareTrendLocked(30)
	return map[string]any{
		"window":          "24h",
		"snapshot_ts":     now,
		"denominator_usd": denom,
		"rows":            rows,
		"kpis":            kpis,
		"history":         historyStatus,
		"trend":           trend,
	}
}

// shareHistoricalLocked covers 7d / 30d. It reads from
// dailyPlatformVolumes, applies R6 status semantics (insufficient_history,
// partial, complete), and folds in MEXC×0.4 / Gate×0.5 discounts only at
// query time per R5.
func (s *Store) shareHistoricalLocked(window string, days int) map[string]any {
	now := time.Now().UTC()
	cutoff := startOfUTCDay(now).AddDate(0, 0, -(days - 1))

	rawByPlatform := map[string]float64{}
	adjustedByPlatform := map[string]float64{}
	statusByPlatform := map[string]string{}
	daysCovered := map[string]int{}
	sourceByPlatform := map[string]string{}

	for _, p := range s.cfg.Platforms {
		rows := s.dailyPlatformVolumes[p]
		if len(rows) == 0 {
			statusByPlatform[p] = domain.StatusInsufficientHistory
			continue
		}
		seen := 0
		for _, r := range rows {
			if r.Day.Before(cutoff) {
				continue
			}
			if r.Status != domain.StatusComplete || r.Volume24HUSD <= 0 {
				continue
			}
			rawByPlatform[p] += r.Volume24HUSD
			adjustedByPlatform[p] += indicators.AdjustedVolume(p, r.Volume24HUSD)
			seen++
			if r.DataSource != "" {
				sourceByPlatform[p] = r.DataSource
			}
		}
		daysCovered[p] = seen
		switch {
		case seen == 0:
			statusByPlatform[p] = domain.StatusInsufficientHistory
		case seen < days:
			statusByPlatform[p] = domain.StatusPartial
		default:
			statusByPlatform[p] = domain.StatusComplete
		}
	}

	var denom float64
	completeCount := 0
	for p, status := range statusByPlatform {
		if status == domain.StatusComplete {
			denom += adjustedByPlatform[p]
			completeCount++
		}
	}

	rows := []map[string]any{}
	for _, p := range s.cfg.Platforms {
		status := statusByPlatform[p]
		row := map[string]any{
			"platform":    p,
			"discount":    discount(p),
			"status":      status,
			"data_source": sourceByPlatform[p],
			"days_seen":   daysCovered[p],
			"days_window": days,
		}
		if status == domain.StatusComplete {
			share := 0.0
			if denom > 0 {
				share = adjustedByPlatform[p] / denom * 100
			}
			row["raw_volume_usd"] = rawByPlatform[p]
			row["adjusted_volume_usd"] = adjustedByPlatform[p]
			row["adjusted_volume_total_usd"] = adjustedByPlatform[p]
			row["share_pct"] = share
			row["denominator_pct"] = share
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return numeric(rows[i]["adjusted_volume_usd"]) > numeric(rows[j]["adjusted_volume_usd"])
	})
	for i := range rows {
		rows[i]["rank"] = i + 1
	}

	insufficientCount := 0
	for _, status := range statusByPlatform {
		if status == domain.StatusInsufficientHistory {
			insufficientCount++
		}
	}
	overall := domain.StatusPartial
	switch {
	case insufficientCount == len(s.cfg.Platforms):
		overall = domain.StatusInsufficientHistory
	case completeCount == len(s.cfg.Platforms):
		overall = domain.StatusComplete
	}

	kpis := map[string]any{
		"edgex_share_pct":        shareForAdjusted(adjustedByPlatform["edgeX"], denom),
		"edgex_total_volume_usd": rawByPlatform["edgeX"],
		"denominator_usd":        denom,
	}
	trend := s.shareTrendLocked(days)
	return map[string]any{
		"window":          window,
		"status":          overall,
		"snapshot_ts":     now,
		"denominator_usd": denom,
		"rows":            rows,
		"kpis":            kpis,
		"trend":           trend,
	}
}

// historicalShareStatusLocked returns one status code per historical window
// so the 24h response can hint at whether 7d/30d data is ready.
func (s *Store) historicalShareStatusLocked() map[string]string {
	out := map[string]string{}
	for _, w := range []struct {
		key  string
		days int
	}{{"7d", 7}, {"30d", 30}} {
		complete := 0
		insufficient := 0
		for _, p := range s.cfg.Platforms {
			seen := s.daysSeenLocked(p, w.days)
			switch {
			case seen >= w.days:
				complete++
			case seen == 0:
				insufficient++
			}
		}
		switch {
		case complete == len(s.cfg.Platforms):
			out[w.key] = domain.StatusComplete
		case insufficient == len(s.cfg.Platforms):
			out[w.key] = domain.StatusInsufficientHistory
		default:
			out[w.key] = domain.StatusPartial
		}
	}
	return out
}

func (s *Store) daysSeenLocked(platform string, days int) int {
	cutoff := startOfUTCDay(time.Now().UTC()).AddDate(0, 0, -(days - 1))
	seen := 0
	for _, r := range s.dailyPlatformVolumes[platform] {
		if r.Day.Before(cutoff) {
			continue
		}
		if r.Status == domain.StatusComplete && r.Volume24HUSD > 0 {
			seen++
		}
	}
	return seen
}

// shareTrendLocked emits up to `days` data points for the edgeX share over
// time, each point carrying three rolling-window shares — the day itself
// (24h), the trailing 7 days, and the trailing 30 days — so the Share tab
// can draw the design's "三口径滚动叠加" comparison without spawning
// extra API calls. Returns status=insufficient_history when no daily
// rollups exist for edgeX at all, status=partial when some days are
// missing inside the window, status=complete when all `days` are
// populated.
func (s *Store) shareTrendLocked(days int) map[string]any {
	now := time.Now().UTC()
	if days <= 0 {
		days = 30
	}
	// We need 29 days of look-back to support a 30d trailing window for
	// the very first plotted point, so we fetch a wider raw range and
	// only emit the last `days` points.
	const trailingDays = 30
	lookback := days + trailingDays - 1
	rawStart := startOfUTCDay(now).AddDate(0, 0, -lookback)
	plotStart := startOfUTCDay(now).AddDate(0, 0, -(days - 1))
	today := startOfUTCDay(now)

	type bucket struct {
		denomAdjusted float64
		edgexAdjusted float64
		denomRaw      float64
		edgexRaw      float64
		platformsSeen map[string]struct{}
	}
	byDay := map[time.Time]*bucket{}
	for _, p := range s.cfg.Platforms {
		for _, r := range s.dailyPlatformVolumes[p] {
			if r.Day.Before(rawStart) {
				continue
			}
			if r.Status != domain.StatusComplete || r.Volume24HUSD <= 0 {
				continue
			}
			b, ok := byDay[r.Day]
			if !ok {
				b = &bucket{platformsSeen: map[string]struct{}{}}
				byDay[r.Day] = b
			}
			adj := indicators.AdjustedVolume(p, r.Volume24HUSD)
			b.denomAdjusted += adj
			b.denomRaw += r.Volume24HUSD
			if p == "edgeX" {
				b.edgexAdjusted += adj
				b.edgexRaw += r.Volume24HUSD
			}
			b.platformsSeen[p] = struct{}{}
		}
	}
	if len(byDay) == 0 {
		return map[string]any{"status": domain.StatusInsufficientHistory, "points": []any{}}
	}

	// rolling sums a window of [end-window+1, end] inclusive and returns
	// (edgeX_adjusted, denom_adjusted, days_seen). Days with no bucket
	// are silently skipped — share% naturally degrades when coverage is
	// partial.
	rolling := func(end time.Time, window int) (float64, float64, int) {
		var edgexAdj, denomAdj float64
		days := 0
		for i := 0; i < window; i++ {
			d := end.AddDate(0, 0, -i)
			b, ok := byDay[d]
			if !ok {
				continue
			}
			edgexAdj += b.edgexAdjusted
			denomAdj += b.denomAdjusted
			days++
		}
		return edgexAdj, denomAdj, days
	}

	points := []map[string]any{}
	plottedDays := 0
	for d := plotStart; !d.After(today); d = d.AddDate(0, 0, 1) {
		b, ok := byDay[d]
		if !ok {
			continue
		}
		plottedDays++
		share24h := 0.0
		if b.denomAdjusted > 0 {
			share24h = b.edgexAdjusted / b.denomAdjusted * 100
		}
		share7d, days7d := 0.0, 0
		if e7, n7, d7 := rolling(d, 7); n7 > 0 {
			share7d = e7 / n7 * 100
			days7d = d7
		}
		share30d, days30d := 0.0, 0
		if e30, n30, d30 := rolling(d, 30); n30 > 0 {
			share30d = e30 / n30 * 100
			days30d = d30
		}
		point := map[string]any{
			"day":               d.Format("2006-01-02"),
			"edgex_share_pct":   share24h,
			"share_24h_pct":     share24h,
			"share_7d_pct":      share7d,
			"share_30d_pct":     share30d,
			"days_7d":           days7d,
			"days_30d":          days30d,
			"denominator_usd":   b.denomAdjusted,
			"edgex_volume_usd":  b.edgexRaw,
			"platforms_covered": len(b.platformsSeen),
		}
		points = append(points, point)
	}
	status := domain.StatusComplete
	if plottedDays < days {
		status = domain.StatusPartial
	}
	return map[string]any{"status": status, "points": points}
}

func (s *Store) Top30(surface, platform string) map[string]any {
	if surface == "" {
		surface = "perp"
	}
	if platform == "" {
		platform = "binance"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, ok := s.top30ByPlatform[platform]
	now := time.Now().UTC()
	if !ok || len(rows) == 0 {
		return map[string]any{
			"surface":     surface,
			"platform":    platform,
			"snapshot_ts": now,
			"status":      domain.StatusUnsupported,
			"rows":        []domain.Top30Row{},
		}
	}
	out := make([]domain.Top30Row, len(rows))
	copy(out, rows)
	return map[string]any{
		"surface":     surface,
		"platform":    platform,
		"snapshot_ts": latestTop30TS(out),
		"status":      domain.StatusComplete,
		"rows":        out,
	}
}

func (s *Store) displayPlatformSnapshotLocked(platform, symbol string) (domain.PlatformSnapshot, bool) {
	k := key(platform, symbol)
	latest, ok := s.platforms[k]
	if !ok {
		return domain.PlatformSnapshot{}, false
	}
	if isDisplayableSnapshot(latest) {
		latest.DataFreshness = domain.FreshnessLive
		return latest, true
	}
	window := s.cfg.Runtime.DisplayFallbackWindow
	if window <= 0 {
		return latest, true
	}
	cutoff := latest.SnapshotTS.Add(-window)
	history := s.platformHistory[k]
	for i := len(history) - 1; i >= 0; i-- {
		candidate := history[i]
		if candidate.SnapshotTS.Before(cutoff) {
			break
		}
		if !isDisplayableSnapshot(candidate) {
			continue
		}
		candidate.DataFreshness = domain.FreshnessDelayed
		candidate.LastCollectionStatus = latest.DepthStatus
		candidate.LastCollectionError = latest.Error
		lastCollectionTS := latest.SnapshotTS
		candidate.LastCollectionTS = &lastCollectionTS
		return candidate, true
	}
	return latest, true
}

func isDisplayableSnapshot(row domain.PlatformSnapshot) bool {
	switch row.DepthStatus {
	case domain.StatusComplete, domain.StatusPartial, domain.StatusAggregatedOrderbook, domain.StatusWSLimitedDepth:
		return len(row.DepthByTier) > 0
	default:
		return false
	}
}

func (s *Store) CollectionStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{"last_run": s.run, "rows": s.status}
}

func (s *Store) RuntimeConfig() config.Runtime { return s.cfg.Runtime }

func missingPlatform(platform, symbol string) domain.PlatformSnapshot {
	return domain.PlatformSnapshot{Platform: platform, DisplaySymbol: symbol, SnapshotTS: time.Now().UTC(), DepthStatus: domain.StatusStale, Error: "no collection result yet", DepthByTier: map[string]domain.DepthMetrics{}, BuySlippageBP: map[string]float64{}, SellSlippageBP: map[string]float64{}}
}

func latestTS(rows []domain.PlatformSnapshot) time.Time {
	var t time.Time
	for _, r := range rows {
		if r.SnapshotTS.After(t) {
			t = r.SnapshotTS
		}
	}
	return t
}

// liquidityKPIsLocked builds the per-symbol KPIs for the Liquidity tab. The
// caller already holds s.mu (RLock).
func (s *Store) liquidityKPIsLocked(rows []domain.PlatformSnapshot, medians map[string]float64, symbol string) map[string]any {
	var edge domain.PlatformSnapshot
	for _, r := range rows {
		if r.Platform == "edgeX" {
			edge = r
		}
	}
	// 24h symbol-level share continues to use native volumes per symbol
	// because CoinGecko's /derivatives ticker is platform-level, not
	// per-symbol; mixing the two would conflate denominators.
	share := 0.0
	denom := 0.0
	for _, v := range s.volumes {
		if v.DisplaySymbol == symbol && v.Status == domain.StatusComplete {
			adj := indicators.AdjustedVolume(v.Platform, v.Volume24HUSD)
			denom += adj
			if v.Platform == "edgeX" {
				share = adj
			}
		}
	}
	if denom > 0 {
		share = share / denom * 100
	}
	share7d, sharePctWoW := s.symbolShareKPIsLocked(symbol)
	return map[string]any{
		"edgex_depth_by_tier":       edge.DepthByTier,
		"edgex_vs_median_by_tier":   edge.VsMedianByTier,
		"competitor_median_by_tier": medians,
		"edgex_spread_bp":           edge.SpreadBP,
		"edgex_spread_10m_status":   domain.StatusUnsupported,
		"edgex_24h_share_pct":       share,
		"symbol_share_7d_status":    share7d,
		"symbol_share_wow_status":   sharePctWoW,
	}
}

// symbolShareKPIsLocked computes the R6 status string for the 7d and
// week-over-week symbol-level share KPIs. Returns the status only; the
// numeric value is still TODO once symbol-level daily aggregates are
// populated by the CoinGecko collector.
func (s *Store) symbolShareKPIsLocked(symbol string) (string, string) {
	if symbol == "" {
		return domain.StatusInsufficientHistory, domain.StatusInsufficientHistory
	}
	// 7d window
	cutoff7 := startOfUTCDay(time.Now().UTC()).AddDate(0, 0, -6)
	seen7 := 0
	for _, p := range s.cfg.Platforms {
		k := key(p, symbol)
		for _, r := range s.dailySymbolVolumes[k] {
			if r.Day.Before(cutoff7) {
				continue
			}
			if r.Status == domain.StatusComplete && r.Volume24HUSD > 0 {
				seen7++
				break
			}
		}
	}
	status7d := domain.StatusInsufficientHistory
	if seen7 >= len(s.cfg.Platforms) {
		status7d = domain.StatusComplete
	} else if seen7 > 0 {
		status7d = domain.StatusPartial
	}
	// 14d window for WoW
	cutoff14 := startOfUTCDay(time.Now().UTC()).AddDate(0, 0, -13)
	seen14 := 0
	for _, p := range s.cfg.Platforms {
		k := key(p, symbol)
		for _, r := range s.dailySymbolVolumes[k] {
			if r.Day.Before(cutoff14) {
				continue
			}
			if r.Status == domain.StatusComplete && r.Volume24HUSD > 0 {
				seen14++
				break
			}
		}
	}
	statusWoW := domain.StatusInsufficientHistory
	if seen14 >= len(s.cfg.Platforms) {
		statusWoW = domain.StatusComplete
	} else if seen14 > 0 {
		statusWoW = domain.StatusPartial
	}
	return status7d, statusWoW
}

func competitorMedianByTier(rows []domain.PlatformSnapshot) map[string]float64 {
	byTier := map[string][]float64{}
	for _, row := range rows {
		if row.Platform == "edgeX" {
			continue
		}
		for tier, depth := range row.DepthByTier {
			if depth.TotalUSD > 0 && isComparableDepthTier(row, depth) {
				byTier[tier] = append(byTier[tier], depth.TotalUSD)
			}
		}
	}
	out := map[string]float64{}
	for tier, values := range byTier {
		out[tier] = median(values)
	}
	return out
}

func enrichLiquidityRows(rows []domain.PlatformSnapshot, medians map[string]float64) []domain.PlatformSnapshot {
	ranked := append([]domain.PlatformSnapshot(nil), rows...)
	sort.Slice(ranked, func(i, j int) bool {
		return comparableTierTotal(ranked[i], "0.10%") > comparableTierTotal(ranked[j], "0.10%")
	})
	ranks := map[string]int{}
	rank := 1
	for _, row := range ranked {
		if comparableTierTotal(row, "0.10%") <= 0 {
			continue
		}
		ranks[row.Platform] = rank
		rank++
	}
	out := append([]domain.PlatformSnapshot(nil), rows...)
	for i := range out {
		out[i].VsMedianByTier = map[string]float64{}
		for tier, depth := range out[i].DepthByTier {
			if medians[tier] > 0 && depth.TotalUSD > 0 && isComparableDepthTier(out[i], depth) {
				out[i].VsMedianByTier[tier] = depth.TotalUSD / medians[tier]
			}
		}
		out[i].Rank01 = ranks[out[i].Platform]
		out[i].DepthStatusLabel = depthStatusLabel(out[i])
	}
	return out
}

func enrichQualityRows(rows []domain.PlatformSnapshot) []domain.PlatformSnapshot {
	out := append([]domain.PlatformSnapshot(nil), rows...)
	for i := range out {
		out[i].WorstSlippageBP = map[string]float64{}
		for bucket, buy := range out[i].BuySlippageBP {
			sell := out[i].SellSlippageBP[bucket]
			if buy >= sell {
				out[i].WorstSlippageBP[bucket] = buy
			} else {
				out[i].WorstSlippageBP[bucket] = sell
			}
		}
		out[i].Verdict = qualityVerdict(out[i])
	}
	return out
}

func isComparableDepth(row domain.PlatformSnapshot) bool {
	switch row.DepthStatus {
	case domain.StatusComplete, domain.StatusPartial, domain.StatusAggregatedOrderbook, domain.StatusWSLimitedDepth:
		return true
	default:
		return false
	}
}

func isComparableDepthTier(row domain.PlatformSnapshot, depth domain.DepthMetrics) bool {
	switch depth.DepthStatus {
	case domain.StatusComplete, domain.StatusAggregatedOrderbook:
		return true
	case "":
		return isComparableDepth(row)
	default:
		return false
	}
}

func comparableTierTotal(row domain.PlatformSnapshot, tier string) float64 {
	depth := row.DepthByTier[tier]
	if depth.TotalUSD <= 0 || !isComparableDepthTier(row, depth) {
		return 0
	}
	return depth.TotalUSD
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func depthStatusLabel(row domain.PlatformSnapshot) string {
	if !isComparableDepth(row) {
		return domain.StatusUnsupported
	}
	ratio, ok := row.VsMedianByTier["0.10%"]
	if !ok || ratio <= 0 {
		return domain.StatusUnsupported
	}
	if ratio < 0.5 {
		return "深度落后"
	}
	if ratio < 0.8 {
		return "偏弱"
	}
	return "达标"
}

func qualityVerdict(row domain.PlatformSnapshot) string {
	if !isComparableDepth(row) || row.SpreadBP <= 0 {
		return domain.StatusUnsupported
	}
	absImbalance := row.Imbalance
	if absImbalance < 0 {
		absImbalance = -absImbalance
	}
	if row.SpreadBP < 1 && absImbalance < 15 {
		return "健康"
	}
	if row.SpreadBP < 1.5 || absImbalance < 25 {
		return "关注"
	}
	return "较差"
}

func shareForAdjusted(adjusted, denom float64) float64 {
	if denom <= 0 {
		return 0
	}
	return adjusted / denom * 100
}

func mergeVolumeStatus(current, next string) string {
	if next == domain.StatusComplete || current == domain.StatusComplete {
		return domain.StatusComplete
	}
	if next == domain.StatusError || current == domain.StatusError {
		return domain.StatusError
	}
	if next == domain.StatusUnsupported || current == domain.StatusUnsupported {
		return domain.StatusUnsupported
	}
	if next != "" {
		return next
	}
	return current
}

func numeric(value any) float64 {
	if v, ok := value.(float64); ok {
		return v
	}
	return 0
}
func discount(platform string) float64 {
	if platform == "mexc" {
		return 0.4
	}
	if platform == "gate" {
		return 0.5
	}
	return 1
}

// startOfUTCDay snaps a timestamp down to 00:00:00 UTC of the same calendar
// day. Used to key daily rollups by date rather than instant.
func startOfUTCDay(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// mergeDailyAggregate inserts or replaces a row in a daily-rollup slice. The
// slice stays sorted ascending by Day and is deduped by (Day, DisplaySymbol)
// — at most one row per (day, symbol) is retained, selected by data-source
// priority: native > coingecko > coingecko_backfill. This ensures the share
// aggregation can simply sum every entry without double-counting when both
// a backfill row and a live row land on the same day.
func mergeDailyAggregate(rows []domain.DailyVolumeAggregate, row domain.DailyVolumeAggregate) []domain.DailyVolumeAggregate {
	for i, existing := range rows {
		if existing.Day.Equal(row.Day) && existing.DisplaySymbol == row.DisplaySymbol {
			if dataSourcePriority(row.DataSource) >= dataSourcePriority(existing.DataSource) {
				rows[i] = row
			}
			return rows
		}
	}
	rows = append(rows, row)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Day.Before(rows[j].Day) })
	return rows
}

// dataSourcePriority ranks the daily-aggregate data sources from most to
// least trustworthy. A live coingecko or native row always wins over a
// backfill row landing on the same day.
func dataSourcePriority(src string) int {
	switch src {
	case domain.DataSourceNative:
		return 3
	case domain.DataSourceCoinGecko:
		return 2
	case domain.DataSourceCoinGeckoBackfill:
		return 1
	}
	return 0
}

func latestTop30TS(rows []domain.Top30Row) time.Time {
	var t time.Time
	for _, r := range rows {
		if r.SnapshotTS.After(t) {
			t = r.SnapshotTS
		}
	}
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}
