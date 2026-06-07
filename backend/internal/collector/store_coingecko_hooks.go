package collector

import (
	"log"
	"time"

	"edgex-ops-intelligence/backend/internal/marketdata/coingecko"
)

// RecordCoinGeckoPullSuccess advances the last-success timestamp surfaced
// under OpsIntelligenceMeta.data_sources.coingecko.last_pull_ts.
//
// Kept in this file (rather than store.go) so the CoinGecko collector can
// be merged independently of unrelated Store WIP touching the same
// hot-spot.
func (s *Store) RecordCoinGeckoPullSuccess(at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.mu.Lock()
	if at.After(s.cgLastPullTS) {
		s.cgLastPullTS = at
	}
	s.mu.Unlock()
}

// RecordCoinGeckoPullFailure leaves a log trail when a /derivatives pull
// fails so a missed cycle is observable even when no
// SaveCoinGeckoPlatformVolumes call follows.
func (s *Store) RecordCoinGeckoPullFailure(at time.Time, err error) {
	if err == nil {
		return
	}
	log.Printf("coingecko: pull at %s failed: %v", at.UTC().Format(time.RFC3339), err)
}

// RecordCoinGeckoGovernance exposes the process-local CoinGecko budget state
// through CollectionStatus and OpsIntelligenceMeta so operators can distinguish
// live pulls, stale-cache service, cooldown, and skipped backfill runs.
func (s *Store) RecordCoinGeckoGovernance(status coingecko.GovernorStatus, cacheState string) {
	if s == nil {
		return
	}
	row := map[string]any{
		"enabled":                      status.Enabled,
		"state":                        status.State,
		"cache_state":                  cacheState,
		"requests_per_minute":          status.RequestsPerMin,
		"backfill_requests_per_minute": status.BackfillPerMin,
		"default_cooldown":             status.DefaultCooldown.String(),
	}
	if !status.CooldownUntil.IsZero() {
		row["cooldown_until"] = status.CooldownUntil
	}
	if status.LastEndpoint != "" {
		row["last_endpoint"] = status.LastEndpoint
	}
	if status.LastError != "" {
		row["last_error"] = status.LastError
	}
	if status.LastPriority != "" {
		row["last_priority"] = string(status.LastPriority)
	}

	s.mu.Lock()
	s.cgGovernance = row
	s.publishSnapshotLocked()
	s.mu.Unlock()
}
