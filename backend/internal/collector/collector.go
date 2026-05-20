package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"edgex-dashboard/backend/internal/adapter"
	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/indicators"
)

type Collector struct {
	cfg      config.Config
	store    *Store
	adapters map[string]adapter.ExchangeAdapter
}

func NewCollector(cfg config.Config, store *Store) *Collector {
	return NewCollectorWithLighter(cfg, store, nil)
}

func NewCollectorWithLighter(cfg config.Config, store *Store, lighter adapter.LighterBookProvider) *Collector {
	adapters := map[string]adapter.ExchangeAdapter{}
	for _, p := range cfg.Platforms {
		adapters[p] = adapter.NewWithLighter(p, cfg.Runtime.HTTPTimeout, lighter)
	}
	return &Collector{cfg: cfg, store: store, adapters: adapters}
}

func (c *Collector) CollectOnce(ctx context.Context) error {
	started := time.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Runtime.HTTPTimeout*6+10*time.Second)
	defer cancel()
	statuses := []domain.CollectionStatus{}
	success, failed := 0, 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, sub := range c.cfg.Symbols {
		sub := sub
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter := c.adapters[sub.Platform]
			begin := time.Now()
			book, err := adapter.FetchOrderBook(ctx, sub)
			status := domain.StatusComplete
			if err != nil {
				status = book.DepthStatus
				mu.Lock()
				failed++
				mu.Unlock()
			} else {
				mu.Lock()
				success++
				mu.Unlock()
			}
			mu.Lock()
			statuses = append(statuses, domain.CollectionStatus{Platform: sub.Platform, DisplaySymbol: sub.DisplaySymbol, Collector: "rest_orderbook", SourceEndpoint: sub.SourceEndpoint, Status: status, Error: book.Error, SnapshotTS: book.SnapshotTS, LatencyMS: time.Since(begin).Milliseconds()})
			mu.Unlock()
			c.store.SavePlatformSnapshot(platformFromBook(book, c.cfg.Runtime))

			begin = time.Now()
			vol, verr := adapter.FetchTicker(ctx, sub)
			vstatus := vol.Status
			if verr != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			} else {
				mu.Lock()
				success++
				mu.Unlock()
			}
			mu.Lock()
			statuses = append(statuses, domain.CollectionStatus{Platform: sub.Platform, DisplaySymbol: sub.DisplaySymbol, Collector: "rest_ticker", SourceEndpoint: sub.SourceEndpoint, Status: vstatus, Error: vol.Error, SnapshotTS: vol.SnapshotTS, LatencyMS: time.Since(begin).Milliseconds()})
			mu.Unlock()
			c.store.SaveVolume(vol)
		}()
	}
	wg.Wait()
	c.store.SaveStatus(statuses, RunSummary{RunID: fmt.Sprintf("run-%d", started.Unix()), StartedAt: started, CompletedAt: time.Now().UTC(), Success: success, Failed: failed})
	if failed > 0 {
		return fmt.Errorf("%d collection attempts failed or unsupported", failed)
	}
	return nil
}

func platformFromBook(book domain.OrderBookSnapshot, runtime config.Runtime) domain.PlatformSnapshot {
	row := domain.PlatformSnapshot{Platform: book.Platform, DisplaySymbol: book.DisplaySymbol, SnapshotTS: book.SnapshotTS, SourceEndpoint: book.SourceEndpoint, DepthStatus: book.DepthStatus, PartialReason: book.PartialReason, Error: book.Error, DepthByTier: map[string]domain.DepthMetrics{}, BuySlippageBP: map[string]float64{}, SellSlippageBP: map[string]float64{}}
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return row
	}
	row.MidPrice = indicators.MidPrice(book)
	row.SpreadBP = indicators.SpreadBP(book.Bids[0].Price, book.Asks[0].Price)
	for _, tier := range runtime.DepthTiers {
		label := fmt.Sprintf("%.2f%%", tier*100)
		depth := indicators.DepthAtTier(book, tier)
		row.DepthByTier[label] = depth
		if label == "0.10%" {
			row.Imbalance = indicators.Imbalance(depth.BidUSD, depth.AskUSD)
		}
	}
	for _, bucket := range runtime.SlippageBucketsUSD {
		label := fmt.Sprintf("%.0f", bucket)
		row.BuySlippageBP[label] = indicators.BuySlippageBP(book, bucket)
		row.SellSlippageBP[label] = indicators.SellSlippageBP(book, bucket)
	}
	return row
}
