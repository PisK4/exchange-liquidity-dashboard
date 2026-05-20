package collector

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"edgex-dashboard/backend/internal/adapter"
	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

// SymbolBackfiller drives the per-(platform, display_symbol) historical
// daily-volume back-fill. It probes each configured adapter for the optional
// DailyVolumeHistoryFetcher capability, requests `days` of UTC-day rows, and
// persists them via Store.SaveDailyVolumeAggregates so the 7d / WoW share KPIs
// can be answered without waiting for the rolling /derivatives writer to
// accumulate seven UTC days of history. Live rows (DataSourceCoinGecko /
// DataSourceNative) always evict backfill rows for the same slot via the
// existing dedup, so this routine is idempotent.
type SymbolBackfiller struct {
	cfg      config.Config
	store    *Store
	adapters map[string]adapter.ExchangeAdapter
}

func NewSymbolBackfiller(cfg config.Config, store *Store, lighter adapter.LighterBookProvider) *SymbolBackfiller {
	adapters := map[string]adapter.ExchangeAdapter{}
	for _, p := range cfg.Platforms {
		adapters[p] = adapter.NewWithLighterAndProxy(p, cfg.Runtime.HTTPTimeout, lighter, cfg.Runtime.ExchangeProxy)
	}
	return &SymbolBackfiller{cfg: cfg, store: store, adapters: adapters}
}

// Run launches a goroutine that performs an initial back-fill (after a short
// boot delay so the main collector can claim its first batch of public-API
// budget) and then re-runs once per UTC day. Cancellation is observed
// through ctx; transient per-platform errors are logged and the round
// continues with the next platform.
func (b *SymbolBackfiller) Run(ctx context.Context, days int) {
	if days <= 0 {
		days = 14
	}
	go func() {
		initial := time.NewTimer(2 * time.Minute)
		defer initial.Stop()
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
		}
		if err := b.RunOnce(ctx, days); err != nil {
			log.Printf("symbol-volume backfill: initial run failed: %v", err)
		}
		next := time.NewTimer(nextSymbolBackfillDelay(time.Now().UTC()))
		defer next.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-next.C:
				if err := b.RunOnce(ctx, days); err != nil {
					log.Printf("symbol-volume backfill: daily run failed: %v", err)
				}
				next.Reset(nextSymbolBackfillDelay(time.Now().UTC()))
			}
		}
	}()
}

// RunOnce performs one pass over every (platform, V1 display_symbol) pair
// and persists whatever the adapter can return. The first error per call is
// returned but no per-platform error short-circuits subsequent platforms.
func (b *SymbolBackfiller) RunOnce(ctx context.Context, days int) error {
	if days <= 0 {
		days = 14
	}
	if len(b.cfg.Symbols) == 0 {
		return errors.New("symbol-volume backfill: no symbols configured")
	}
	var firstErr error
	var totalRows int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, sub := range b.cfg.Symbols {
		sub := sub
		ad, ok := b.adapters[sub.Platform]
		if !ok {
			continue
		}
		fetcher, ok := ad.(adapter.DailyVolumeHistoryFetcher)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := fetcher.FetchDailyVolumeHistory(ctx, sub, days)
			if err != nil {
				log.Printf("symbol-volume backfill: %s %s: %v", sub.Platform, sub.DisplaySymbol, err)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if len(rows) == 0 {
				return
			}
			b.store.SaveDailyVolumeAggregates(filterRecentBackfillRows(rows, days))
			mu.Lock()
			totalRows += len(rows)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if totalRows > 0 {
		log.Printf("symbol-volume backfill: persisted %d rows across %d symbols × %d platforms",
			totalRows, len(uniqueSymbols(b.cfg.Symbols)), len(b.cfg.Platforms))
	}
	return firstErr
}

// filterRecentBackfillRows drops any row whose Day falls outside the
// requested rolling window. Some exchange klines return one extra leading
// or trailing row outside the requested range (e.g. Binance includes the
// current partial day); we want every persisted row to map to a complete
// UTC day inside [today-days+1, today].
func filterRecentBackfillRows(rows []domain.DailyVolumeAggregate, days int) []domain.DailyVolumeAggregate {
	today := startOfUTCDay(time.Now().UTC())
	cutoff := today.AddDate(0, 0, -(days - 1))
	out := make([]domain.DailyVolumeAggregate, 0, len(rows))
	for _, r := range rows {
		if r.Day.Before(cutoff) {
			continue
		}
		if r.Day.After(today) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func uniqueSymbols(subs []domain.SymbolSub) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range subs {
		if s.DisplaySymbol != "" {
			out[s.DisplaySymbol] = struct{}{}
		}
	}
	return out
}

// nextSymbolBackfillDelay returns the delay from `now` to the next UTC
// 02:00 mark; the chosen hour deliberately lags coingecko's 01:00 backfill
// so the higher-priority CG row for "today" is in place before the
// per-symbol native backfill runs.
func nextSymbolBackfillDelay(now time.Time) time.Duration {
	utc := now.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day(), 2, 0, 0, 0, time.UTC)
	if !next.After(utc) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(utc)
}
