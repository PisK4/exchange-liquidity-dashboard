package collector

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"edgex-dashboard/backend/internal/adapter"
	"edgex-dashboard/backend/internal/config"
)

// Top30Backfiller pulls per-(platform, base_asset) daily kline rows for the
// union of bases currently ranked in every platform's Top30 view. Unlike
// SymbolBackfiller which restricts itself to the V1 cfg.Symbols set
// (BTC/ETH/SOL), this backfiller iterates over the live roster reported by
// CoinGecko, resolves each base to a SymbolSub via CatalogResolver, and
// requests the missing UTC days from the platform's native kline endpoint.
//
// Persistence is delegated to Store.SaveDailyVolumeAggregates which writes
// rows as data_source=native_backfill; the existing UPSERT priority means
// any later live (coingecko / native) row wins over the backfill value for
// the same (platform, day, display_symbol).
//
// Concurrency model:
//   - One goroutine per platform; up to PerPlatformConcurrency in-flight
//     kline requests per platform via a semaphore. Cross-platform fan-out
//     runs in parallel.
//   - Each platform has its own simple rate limiter (PerPlatformRatePerSec)
//     so a noisy free-tier (mexc / gate) does not starve a cheaper one
//     (binance / okx).
//   - 429 / 5xx errors are surfaced as transient and retried by the
//     adapter layer; permanent errors (e.g. CatalogResolver returns
//     ErrSymbolUnsupported) cause the (platform, base) to be skipped for
//     the round.
type Top30Backfiller struct {
	cfg       config.Config
	store     *Store
	adapters  map[string]adapter.ExchangeAdapter
	resolver  *CatalogResolver
	limiters  map[string]*rateLimiter
	perPlatN  int
	ratePerS  int
	coldDays  int
	repairDay int
	schedHour int
	schedMin  int
}

// NewTop30Backfiller wires the per-platform REST adapters using the same
// proxy / lighter WS provider the live collector uses, so the back-fill
// stream is indistinguishable from the live one at the network layer.
// resolver is built from the same raw-instruments directory used by
// `make catalog`; nil resolver disables the backfiller (logged once).
func NewTop30Backfiller(cfg config.Config, store *Store, lighter adapter.LighterBookProvider, resolver *CatalogResolver) *Top30Backfiller {
	bf := cfg.Runtime.Backfill
	if bf.PerPlatformConcurrency <= 0 {
		bf.PerPlatformConcurrency = 3
	}
	if bf.PerPlatformRatePerSec <= 0 {
		bf.PerPlatformRatePerSec = 4
	}
	if bf.ColdStartDays <= 0 {
		bf.ColdStartDays = 14
	}
	if bf.DailyRepairDays <= 0 {
		bf.DailyRepairDays = 3
	}
	if bf.ScheduleUTCHour < 0 || bf.ScheduleUTCHour > 23 {
		bf.ScheduleUTCHour = 2
	}
	if bf.ScheduleUTCMinute < 0 || bf.ScheduleUTCMinute > 59 {
		bf.ScheduleUTCMinute = 30
	}
	adapters := map[string]adapter.ExchangeAdapter{}
	limiters := map[string]*rateLimiter{}
	for _, p := range cfg.Platforms {
		adapters[p] = adapter.NewWithLighterAndProxy(p, cfg.Runtime.HTTPTimeout, lighter, cfg.Runtime.ExchangeProxy)
		limiters[p] = newRateLimiter(bf.PerPlatformRatePerSec)
	}
	return &Top30Backfiller{
		cfg:       cfg,
		store:     store,
		adapters:  adapters,
		resolver:  resolver,
		limiters:  limiters,
		perPlatN:  bf.PerPlatformConcurrency,
		ratePerS:  bf.PerPlatformRatePerSec,
		coldDays:  bf.ColdStartDays,
		repairDay: bf.DailyRepairDays,
		schedHour: bf.ScheduleUTCHour,
		schedMin:  bf.ScheduleUTCMinute,
	}
}

// Run launches the boot-time round (after a short delay so the live
// CoinGecko collector can populate the Top30 roster first) and then
// schedules one daily repair pass at the configured UTC mark.
func (b *Top30Backfiller) Run(ctx context.Context) {
	go func() {
		// Wait for the first /derivatives poll to land so RosterUnion()
		// is non-empty. The CoinGecko collector first-poll runs at boot
		// then every PullInterval; 90s is generous enough that even a
		// cold cache + retry lands by then.
		initial := time.NewTimer(90 * time.Second)
		defer initial.Stop()
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
		}
		if err := b.RunOnce(ctx); err != nil {
			log.Printf("top30 backfill: initial run failed: %v", err)
		}
		next := time.NewTimer(nextTop30BackfillDelay(time.Now().UTC(), b.schedHour, b.schedMin))
		defer next.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-next.C:
				if err := b.RunOnce(ctx); err != nil {
					log.Printf("top30 backfill: daily run failed: %v", err)
				}
				next.Reset(nextTop30BackfillDelay(time.Now().UTC(), b.schedHour, b.schedMin))
			}
		}
	}()
}

// RunOnce performs one full pass over the current Top30 roster. The first
// error per platform is preserved for diagnostics but does not short-
// circuit subsequent platforms — operationally we prefer "9 of 10
// platforms back-filled" over "stopped on first 429".
func (b *Top30Backfiller) RunOnce(ctx context.Context) error {
	if b.resolver == nil {
		return errors.New("top30 backfill: catalog resolver not configured")
	}
	roster := b.store.Top30RosterUnion()
	if len(roster) == 0 {
		return errors.New("top30 backfill: roster empty (no Top30 data yet)")
	}

	type platformResult struct {
		platform string
		rows     int
		errCount int
	}
	results := make(chan platformResult, len(roster))
	var wg sync.WaitGroup
	for platform, entries := range roster {
		platform, entries := platform, entries
		ad, ok := b.adapters[platform]
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
			rows, errs := b.runPlatform(ctx, platform, entries, fetcher)
			results <- platformResult{platform: platform, rows: rows, errCount: errs}
		}()
	}
	wg.Wait()
	close(results)
	totalRows, totalErr := 0, 0
	var firstErr error
	for r := range results {
		totalRows += r.rows
		totalErr += r.errCount
		log.Printf("top30 backfill: %s persisted %d rows (%d skipped/failed)", r.platform, r.rows, r.errCount)
	}
	if totalRows == 0 && totalErr > 0 {
		firstErr = errors.New("top30 backfill: all platforms failed (see logs)")
	}
	log.Printf("top30 backfill: complete, %d platforms, %d rows persisted, %d skipped/failed",
		len(roster), totalRows, totalErr)
	return firstErr
}

// runPlatform processes one platform's roster with bounded concurrency.
func (b *Top30Backfiller) runPlatform(ctx context.Context, platform string, entries []RosterEntry, fetcher adapter.DailyVolumeHistoryFetcher) (rows int, errCount int) {
	sem := make(chan struct{}, b.perPlatN)
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		limit = b.limiters[platform]
	)
	for _, entry := range entries {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if limit != nil {
				if err := limit.Wait(ctx); err != nil {
					return
				}
			}
			n, err := b.runOne(ctx, platform, entry.BaseAsset, entry.DisplaySymbol, fetcher)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errCount++
				return
			}
			rows += n
		}()
	}
	wg.Wait()
	return rows, errCount
}

// runOne resolves a single (platform, base), decides how many days to
// fetch via gap detection, calls the kline endpoint, filters the result
// to the requested window, and persists. displaySymbol is taken from the
// roster entry so it matches whatever convention the platform uses
// ("BTC-USD (perp)" on edgeX, "BTC-USDT (perp)" elsewhere).
func (b *Top30Backfiller) runOne(ctx context.Context, platform, base, displaySymbol string, fetcher adapter.DailyVolumeHistoryFetcher) (int, error) {
	sub, err := b.resolver.Resolve(platform, base, displaySymbol)
	if err != nil {
		// Unsupported on this platform — silently skip. ErrSymbolUnsupported
		// is the only expected branch here.
		if errors.Is(err, ErrSymbolUnsupported) {
			return 0, nil
		}
		log.Printf("top30 backfill: %s %s resolve: %v", platform, base, err)
		return 0, err
	}
	days := b.decideFetchDays(ctx, platform, displaySymbol)
	if days <= 0 {
		return 0, nil
	}
	klines, err := fetcher.FetchDailyVolumeHistory(ctx, sub, days)
	if err != nil {
		// adapter.ErrInstrumentNotFound is a permanent skip — exchanges
		// like BingX have CG-reported tickers (GOLD/NASDAQ100/...) that
		// don't have a USDT-base alias on the REST API, so retrying is
		// pointless and the warning would just spam every backfill round.
		if errors.Is(err, adapter.ErrInstrumentNotFound) {
			return 0, nil
		}
		log.Printf("top30 backfill: %s %s fetch %dd: %v", platform, base, days, err)
		return 0, err
	}
	rows := filterRecentBackfillRows(klines, days)
	if len(rows) == 0 {
		return 0, nil
	}
	b.store.SaveDailyVolumeAggregates(rows)
	return len(rows), nil
}

// decideFetchDays returns the smallest backfill window that closes the
// gap between today and the most recent persisted day for this
// (platform, displaySymbol). The decision must satisfy three goals:
//   - cold-start: a brand-new symbol pulls the full cold-start window so
//     7d Vol / Δ light up after the first round;
//   - shallow-history: a symbol with today's CG row but no prior days
//     still pulls the full cold-start window, otherwise gap=0 short-
//     circuits to repairDay=3 and 7d stays insufficient_history forever;
//   - steady-state: a symbol with many days of history just patches
//     today + a small repair window for late writers.
func (b *Top30Backfiller) decideFetchDays(ctx context.Context, platform, displaySymbol string) int {
	today := startOfUTCDay(time.Now().UTC())
	last := b.store.DailySymbolHistoryLatest(platform, displaySymbol)
	if last.IsZero() {
		if dbLast, err := b.store.LoadMaxDayPerSymbol(ctx, platform, displaySymbol); err == nil && !dbLast.IsZero() {
			last = startOfUTCDay(dbLast)
		}
	} else {
		last = startOfUTCDay(last)
	}
	if last.IsZero() {
		return b.coldDays
	}
	// Shallow-history guard: even if we have today's row, force cold
	// start when the symbol owns fewer than 7 distinct days. A floor of
	// 7 ensures the 7d window always closes after one backfill round.
	if dayCount := b.store.DailySymbolDayCount(platform, displaySymbol); dayCount < 7 {
		return b.coldDays
	}
	gapDays := int(today.Sub(last).Hours() / 24)
	if gapDays <= 0 {
		return b.repairDay
	}
	want := gapDays + 1
	if want > b.coldDays {
		want = b.coldDays
	}
	return want
}

// nextTop30BackfillDelay returns the duration from `now` to the next
// scheduled UTC mark (HH:MM). The slot is deliberately AFTER the
// CoinGecko 01:00 backfill so the higher-priority CG row for "today" is
// in place before the per-symbol native pass refines it.
func nextTop30BackfillDelay(now time.Time, hour, minute int) time.Duration {
	utc := now.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day(), hour, minute, 0, 0, time.UTC)
	if !next.After(utc) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(utc)
}

// rateLimiter is a minimal token-bucket sized at one token per
// (1/ratePerS) seconds, just enough to avoid hammering free-tier
// endpoints. We deliberately avoid a heavyweight package dependency: the
// backfill window is bounded (≤14 days × ~30 symbols) so a simple sleep-
// between-requests primitive is adequate.
type rateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newRateLimiter(perSec int) *rateLimiter {
	if perSec <= 0 {
		return nil
	}
	return &rateLimiter{interval: time.Second / time.Duration(perSec)}
}

func (r *rateLimiter) Wait(ctx context.Context) error {
	r.mu.Lock()
	wait := r.interval - time.Since(r.last)
	if wait < 0 {
		wait = 0
	}
	// Reserve the slot before sleeping so concurrent callers space out.
	r.last = time.Now().Add(wait)
	r.mu.Unlock()
	if wait == 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
