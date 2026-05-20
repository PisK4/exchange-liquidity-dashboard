package collector

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"strings"
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
	return map[string]any{"symbol": symbol, "snapshot_ts": latestTS(rows), "rows": rows, "kpis": liquidityKPIs(rows, s.volumes, symbol)}
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
	return map[string]any{"symbol": symbol, "snapshot_ts": latestTS(rows), "slippage_buckets_usd": s.cfg.Runtime.SlippageBucketsUSD, "rows": rows}
}

func (s *Store) Share(window string) map[string]any {
	if window == "" {
		window = "24h"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if window != "24h" {
		return map[string]any{"window": window, "status": domain.HistoryInsufficient, "rows": []any{}, "snapshot_ts": time.Now().UTC()}
	}
	byPlatform := map[string]float64{}
	for _, v := range s.volumes {
		if v.Status == domain.StatusComplete {
			byPlatform[v.Platform] += indicators.AdjustedVolume(v.Platform, v.Volume24HUSD)
		}
	}
	var denom float64
	for _, v := range byPlatform {
		denom += v
	}
	rows := []map[string]any{}
	for _, p := range s.cfg.Platforms {
		adjusted := byPlatform[p]
		share := 0.0
		if denom > 0 {
			share = adjusted / denom * 100
		}
		rows = append(rows, map[string]any{"platform": p, "adjusted_volume_24h_usd": adjusted, "share_pct": share, "discount": discount(p), "status": statusForVolume(s.volumes, p)})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i]["adjusted_volume_24h_usd"].(float64) > rows[j]["adjusted_volume_24h_usd"].(float64)
	})
	return map[string]any{"window": "24h", "snapshot_ts": time.Now().UTC(), "denominator_usd": denom, "rows": rows, "history": map[string]string{"7d": domain.HistoryInsufficient, "30d": domain.HistoryInsufficient}}
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
	for i, sym := range top30Symbols() {
		rows = append(rows, domain.Top30Row{Rank: i + 1, Platform: platform, Symbol: sym, Volume7DStatus: domain.HistoryInsufficient, Delta7DStatus: domain.HistoryInsufficient, Action: "TODO(P1)", SnapshotTS: now, SourceEndpoint: "top30 adapter pending", Status: domain.StatusUnsupported, Error: "live exchange Top30 ranking is not implemented yet"})
	}
	return map[string]any{"surface": surface, "platform": platform, "snapshot_ts": now, "rows": rows}
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

func liquidityKPIs(rows []domain.PlatformSnapshot, volumes map[string]domain.VolumeSnapshot, symbol string) map[string]any {
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
	return map[string]any{"edgex_depth_by_tier": edge.DepthByTier, "edgex_spread_bp": edge.SpreadBP, "edgex_24h_share_pct": share}
}

func statusForVolume(volumes map[string]domain.VolumeSnapshot, platform string) string {
	for _, v := range volumes {
		if v.Platform == platform {
			return v.Status
		}
	}
	return domain.StatusStale
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
func symbolBase(display string) string { return strings.TrimSuffix(display, " (perp)") }

func top30Symbols() []string {
	return []string{
		"BTC-USDT", "ETH-USDT", "SOL-USDT", "XRP-USDT", "BNB-USDT",
		"DOGE-USDT", "HYPE-USDT", "SUI-USDT", "LINK-USDT", "AVAX-USDT",
		"TON-USDT", "ADA-USDT", "TRX-USDT", "PEPE-USDT", "WIF-USDT",
		"BONK-USDT", "ENA-USDT", "JUP-USDT", "FARTCOIN-USDT", "LTC-USDT",
		"BCH-USDT", "DOT-USDT", "NEAR-USDT", "APT-USDT", "ARB-USDT",
		"OP-USDT", "INJ-USDT", "TIA-USDT", "SEI-USDT", "WLD-USDT",
	}
}
