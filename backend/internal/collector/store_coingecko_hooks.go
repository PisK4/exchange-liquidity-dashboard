package collector

import (
	"log"
	"time"
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
