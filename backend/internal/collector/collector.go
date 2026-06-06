package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"edgex-ops-intelligence/backend/internal/adapter"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
	"edgex-ops-intelligence/backend/internal/indicators"
	"golang.org/x/sync/errgroup"
)

type Collector struct {
	cfg      config.Config
	store    *Store
	adapters map[string]adapter.ExchangeAdapter

	// cooldownMu guards cooldown / consecFails. Both maps are keyed by
	// "{platform}|{canonical}" and hold per-pair retry state.
	cooldownMu  sync.Mutex
	cooldown    map[string]time.Time
	consecFails map[string]int
	cooldownNow func() time.Time
}

func NewCollector(cfg config.Config, store *Store) *Collector {
	return NewCollectorWithLighter(cfg, store, nil)
}

func NewCollectorWithLighter(cfg config.Config, store *Store, lighter adapter.LighterBookProvider) *Collector {
	adapters := map[string]adapter.ExchangeAdapter{}
	for _, p := range cfg.Platforms {
		adapters[p] = adapter.NewWithLighterProxyAndRateLimit(p, cfg.Runtime.HTTPTimeout, lighter, cfg.Runtime.ExchangeProxy, cfg.Runtime.Collection.PerPlatformRatePerSec)
	}
	return &Collector{
		cfg:         cfg,
		store:       store,
		adapters:    adapters,
		cooldown:    map[string]time.Time{},
		consecFails: map[string]int{},
		cooldownNow: time.Now,
	}
}

// cooldownKey is the (platform, canonical) tuple identifier used for
// per-pair retry state. Falls back to display_symbol when the canonical
// is empty (legacy V1 BTC/ETH/SOL pre-schema-v2 surfaces).
func cooldownKey(sub domain.SymbolSub) string {
	canon := sub.Canonical
	if canon == "" {
		canon = sub.DisplaySymbol
	}
	return sub.Platform + "|" + canon
}

// shouldSkipForCooldown reports whether a (platform, canonical) tuple is
// currently inside its cooldown window. Cooldown is opted-in via the
// runtime CooldownFailureThreshold/CooldownDuration knobs; if either is
// non-positive cooldown is disabled and this always returns false.
func (c *Collector) shouldSkipForCooldown(sub domain.SymbolSub) bool {
	if c.cfg.Runtime.CooldownFailureThreshold <= 0 || c.cfg.Runtime.CooldownDuration <= 0 {
		return false
	}
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	until, ok := c.cooldown[cooldownKey(sub)]
	if !ok {
		return false
	}
	if c.cooldownNow().Before(until) {
		return true
	}
	delete(c.cooldown, cooldownKey(sub))
	return false
}

// recordCollectionResult updates per-pair consecutive-failure state.
// Tally is bumped on hard error / unsupported; reset on a usable
// snapshot. Once the configured threshold is reached the pair is parked
// in the cooldown map until now+CooldownDuration.
func (c *Collector) recordCollectionResult(sub domain.SymbolSub, ok bool) {
	if c.cfg.Runtime.CooldownFailureThreshold <= 0 || c.cfg.Runtime.CooldownDuration <= 0 {
		return
	}
	key := cooldownKey(sub)
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	if ok {
		delete(c.consecFails, key)
		delete(c.cooldown, key)
		return
	}
	c.consecFails[key]++
	if c.consecFails[key] >= c.cfg.Runtime.CooldownFailureThreshold {
		c.cooldown[key] = c.cooldownNow().Add(c.cfg.Runtime.CooldownDuration)
	}
}

func (c *Collector) CollectOnce(ctx context.Context) error {
	started := time.Now().UTC()
	statuses := []domain.CollectionStatus{}
	success, failed := 0, 0
	var mu sync.Mutex
	semaphores := c.collectionSemaphores()
	g, groupCtx := errgroup.WithContext(ctx)
	for _, sub := range c.cfg.Symbols {
		sub := sub
		g.Go(func() error {
			sem := semaphores[sub.Platform]
			if err := acquirePlatformSlot(groupCtx, sem); err != nil {
				return err
			}
			defer releasePlatformSlot(sem)
			taskCtx, cancel := context.WithTimeout(groupCtx, collectionTaskTimeout(c.cfg.Runtime.HTTPTimeout))
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					failed++
					statuses = append(statuses, domain.CollectionStatus{
						Platform:       sub.Platform,
						DisplaySymbol:  sub.DisplaySymbol,
						Collector:      "rest_orderbook",
						SourceEndpoint: sub.SourceEndpoint,
						Status:         domain.StatusError,
						Error:          fmt.Sprintf("collector panic: %v", r),
						SnapshotTS:     time.Now().UTC(),
					})
					mu.Unlock()
				}
			}()
			if c.shouldSkipForCooldown(sub) {
				now := time.Now().UTC()
				mu.Lock()
				statuses = append(statuses, domain.CollectionStatus{
					Platform:       sub.Platform,
					DisplaySymbol:  sub.DisplaySymbol,
					Collector:      "rest_orderbook",
					SourceEndpoint: sub.SourceEndpoint,
					Status:         domain.StatusUnsupported,
					Error:          "skipped: pair in cooldown after consecutive failures",
					SnapshotTS:     now,
				})
				statuses = append(statuses, domain.CollectionStatus{
					Platform:       sub.Platform,
					DisplaySymbol:  sub.DisplaySymbol,
					Collector:      "rest_ticker",
					SourceEndpoint: sub.SourceEndpoint,
					Status:         domain.StatusUnsupported,
					Error:          "skipped: pair in cooldown after consecutive failures",
					SnapshotTS:     now,
				})
				mu.Unlock()
				return nil
			}
			adapter := c.adapters[sub.Platform]
			begin := time.Now()
			book, err := adapter.FetchOrderBook(taskCtx, sub)
			orderbookOK := false
			status := domain.StatusComplete
			if err != nil {
				status = book.DepthStatus
				mu.Lock()
				failed++
				mu.Unlock()
			} else if book.DepthStatus == domain.StatusUnsupported {
				status = domain.StatusUnsupported
			} else {
				orderbookOK = true
				mu.Lock()
				success++
				mu.Unlock()
			}
			mu.Lock()
			statuses = append(statuses, domain.CollectionStatus{Platform: sub.Platform, DisplaySymbol: sub.DisplaySymbol, Collector: "rest_orderbook", SourceEndpoint: sub.SourceEndpoint, Status: status, Error: book.Error, SnapshotTS: book.SnapshotTS, LatencyMS: time.Since(begin).Milliseconds()})
			mu.Unlock()
			c.store.SavePlatformSnapshot(platformFromBook(book, c.cfg.Runtime))

			begin = time.Now()
			vol, verr := adapter.FetchTicker(taskCtx, sub)
			vstatus := vol.Status
			tickerOK := false
			if verr != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			} else if vstatus == domain.StatusUnsupported {
				// expected for (platform, canonical) pairs with no catalog entry
			} else {
				tickerOK = true
				mu.Lock()
				success++
				mu.Unlock()
			}
			mu.Lock()
			statuses = append(statuses, domain.CollectionStatus{Platform: sub.Platform, DisplaySymbol: sub.DisplaySymbol, Collector: "rest_ticker", SourceEndpoint: sub.SourceEndpoint, Status: vstatus, Error: vol.Error, SnapshotTS: vol.SnapshotTS, LatencyMS: time.Since(begin).Milliseconds()})
			mu.Unlock()
			c.store.SaveVolume(vol)

			c.recordCollectionResult(sub, orderbookOK || tickerOK)
			return nil
		})
	}
	waitErr := g.Wait()
	c.store.SaveStatus(statuses, RunSummary{RunID: fmt.Sprintf("run-%d", started.Unix()), StartedAt: started, CompletedAt: time.Now().UTC(), Success: success, Failed: failed})
	if waitErr != nil {
		return waitErr
	}
	if failed > 0 {
		return fmt.Errorf("%d collection attempts failed or unsupported", failed)
	}
	return nil
}

func (c *Collector) collectionSemaphores() map[string]chan struct{} {
	limit := c.cfg.Runtime.Collection.PerPlatformConcurrency
	if limit <= 0 {
		limit = 1
	}
	out := map[string]chan struct{}{}
	for _, sub := range c.cfg.Symbols {
		if _, ok := out[sub.Platform]; !ok {
			out[sub.Platform] = make(chan struct{}, limit)
		}
	}
	return out
}

func acquirePlatformSlot(ctx context.Context, sem chan struct{}) error {
	if sem == nil {
		return nil
	}
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releasePlatformSlot(sem chan struct{}) {
	if sem != nil {
		<-sem
	}
}

func collectionTaskTimeout(httpTimeout time.Duration) time.Duration {
	if httpTimeout <= 0 {
		httpTimeout = 5 * time.Second
	}
	return httpTimeout*6 + 10*time.Second
}

func platformFromBook(book domain.OrderBookSnapshot, runtime config.Runtime) domain.PlatformSnapshot {
	row := domain.PlatformSnapshot{Platform: book.Platform, DisplaySymbol: book.DisplaySymbol, SnapshotTS: book.SnapshotTS, SourceEndpoint: book.SourceEndpoint, DepthStatus: book.DepthStatus, PartialReason: book.PartialReason, Error: book.Error, DepthByTier: map[string]domain.DepthMetrics{}, BuySlippageBP: map[string]float64{}, SellSlippageBP: map[string]float64{}}
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return row
	}
	row.MidPrice = indicators.MidPrice(book)
	spread, ok := indicators.ValidSpreadBP(book.Bids[0].Price, book.Asks[0].Price)
	if !ok {
		row.DepthStatus = domain.StatusError
		row.Error = "invalid orderbook: best bid must be lower than best ask"
		return row
	}
	row.SpreadBP = spread
	for _, tier := range runtime.DepthTiers {
		label := fmt.Sprintf("%.2f%%", tier*100)
		depth := adapter.TierDepthMetrics(book, tier)
		domain.DeriveDepthMetricsDefaults(book.DepthStatus, &depth)
		row.DepthByTier[label] = depth
		if label == "0.10%" {
			row.Imbalance = indicators.Imbalance(depth.BidUSD, depth.AskUSD)
		}
	}
	row.DepthStatus, row.PartialReason = summarizeDepthStatus(row.DepthByTier, book.DepthStatus, book.PartialReason)
	for _, bucket := range runtime.SlippageBucketsUSD {
		label := fmt.Sprintf("%.0f", bucket)
		row.BuySlippageBP[label] = indicators.BuySlippageBP(book, bucket)
		row.SellSlippageBP[label] = indicators.SellSlippageBP(book, bucket)
	}
	return row
}

func summarizeDepthStatus(depthByTier map[string]domain.DepthMetrics, fallbackStatus, fallbackReason string) (string, string) {
	if len(depthByTier) == 0 {
		return fallbackStatus, fallbackReason
	}
	hasDisplayable := false
	for _, depth := range depthByTier {
		switch depth.DepthStatus {
		case domain.StatusPartial:
			if depth.PartialReason != "" {
				return domain.StatusPartial, depth.PartialReason
			}
			return domain.StatusPartial, domain.ReasonUnknown
		case domain.StatusComplete, domain.StatusAggregatedOrderbook, domain.StatusWSLimitedDepth:
			hasDisplayable = true
		}
	}
	if hasDisplayable {
		return domain.StatusComplete, ""
	}
	return fallbackStatus, fallbackReason
}
