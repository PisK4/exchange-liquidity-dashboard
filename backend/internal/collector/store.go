package collector

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"sync"
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
}

type RunSummary struct {
	RunID       string    `json:"run_id"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Success     int       `json:"success"`
	Failed      int       `json:"failed"`
}

func NewStore(cfg config.Config) *Store {
	return &Store{cfg: cfg, platforms: map[string]domain.PlatformSnapshot{}, platformHistory: map[string][]domain.PlatformSnapshot{}, volumes: map[string]domain.VolumeSnapshot{}}
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
	for _, sub := range s.cfg.Symbols {
		if !seen[sub.DisplaySymbol] {
			seen[sub.DisplaySymbol] = true
			out = append(out, sub.DisplaySymbol)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) SymbolMappings() []domain.SymbolSub {
	return append([]domain.SymbolSub(nil), s.cfg.Symbols...)
}

func (s *Store) Coverage() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []map[string]any{}
	for _, sub := range s.cfg.Symbols {
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
	return map[string]any{
		"tabs":                 []string{"monitor", "quality", "share", "top30"},
		"platforms":            s.cfg.Platforms,
		"symbols":              s.Symbols(),
		"windows":              []string{"24h", "7d", "30d"},
		"depth_tiers":          s.cfg.Runtime.DepthTiers,
		"slippage_buckets_usd": s.cfg.Runtime.SlippageBucketsUSD,
		"refresh_interval_sec": int(s.cfg.Runtime.CollectionInterval.Seconds()),
		"volume_discounts":     s.cfg.Runtime.VolumeDiscounts,
	}
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
	return map[string]any{"symbol": symbol, "snapshot_ts": latestTS(rows), "rows": rows, "competitor_median_by_tier": medians, "kpis": liquidityKPIs(rows, medians, s.volumes, symbol)}
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
	if window != "24h" {
		return map[string]any{"window": window, "status": domain.StatusUnsupported, "reason": "historical platform share is not implemented yet", "rows": []any{}, "snapshot_ts": time.Now().UTC(), "trend": map[string]any{"status": domain.StatusUnsupported}}
	}
	rawByPlatform := map[string]float64{}
	byPlatform := map[string]float64{}
	statusByPlatform := map[string]string{}
	for _, v := range s.volumes {
		statusByPlatform[v.Platform] = mergeVolumeStatus(statusByPlatform[v.Platform], v.Status)
		if v.Status == domain.StatusComplete {
			rawByPlatform[v.Platform] += v.Volume24HUSD
			byPlatform[v.Platform] += indicators.AdjustedVolume(v.Platform, v.Volume24HUSD)
		}
	}
	var denom float64
	for _, v := range byPlatform {
		denom += v
	}
	rows := []map[string]any{}
	for _, p := range s.cfg.Platforms {
		raw := rawByPlatform[p]
		adjusted := byPlatform[p]
		status := statusByPlatform[p]
		if status == "" {
			status = domain.StatusStale
		}
		share := 0.0
		if denom > 0 {
			share = adjusted / denom * 100
		}
		row := map[string]any{"platform": p, "discount": discount(p), "status": status}
		if status == domain.StatusComplete {
			row["raw_volume_usd"] = raw
			row["adjusted_volume_usd"] = adjusted
			row["adjusted_volume_24h_usd"] = adjusted
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
	kpis := map[string]any{
		"edgex_share_pct":        shareForAdjusted(byPlatform["edgeX"], denom),
		"edgex_total_volume_usd": rawByPlatform["edgeX"],
		"denominator_usd":        denom,
	}
	return map[string]any{"window": "24h", "snapshot_ts": time.Now().UTC(), "denominator_usd": denom, "rows": rows, "kpis": kpis, "history": map[string]string{"7d": domain.StatusUnsupported, "30d": domain.StatusUnsupported}, "trend": map[string]any{"status": domain.StatusUnsupported}}
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
	now := time.Now().UTC()
	rows := []domain.Top30Row{}
	return map[string]any{"surface": surface, "platform": platform, "snapshot_ts": now, "status": domain.StatusUnsupported, "rows": rows}
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

func liquidityKPIs(rows []domain.PlatformSnapshot, medians map[string]float64, volumes map[string]domain.VolumeSnapshot, symbol string) map[string]any {
	var edge domain.PlatformSnapshot
	for _, r := range rows {
		if r.Platform == "edgeX" {
			edge = r
		}
	}
	share := 0.0
	denom := 0.0
	for _, v := range volumes {
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
	return map[string]any{
		"edgex_depth_by_tier":       edge.DepthByTier,
		"edgex_vs_median_by_tier":   edge.VsMedianByTier,
		"competitor_median_by_tier": medians,
		"edgex_spread_bp":           edge.SpreadBP,
		"edgex_spread_10m_status":   domain.StatusUnsupported,
		"edgex_24h_share_pct":       share,
		"symbol_share_7d_status":    domain.StatusUnsupported,
		"symbol_share_wow_status":   domain.StatusUnsupported,
	}
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
