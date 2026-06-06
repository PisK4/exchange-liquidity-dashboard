package collector

import (
	"context"
	"sync"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/adapter"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

type concurrencyProbeAdapter struct {
	platform string
	delay    time.Duration

	mu        sync.Mutex
	active    int
	maxActive int
}

func (a *concurrencyProbeAdapter) Name() string { return a.platform }

func (a *concurrencyProbeAdapter) FetchInstruments(ctx context.Context) (adapter.CatalogResult, error) {
	return adapter.CatalogResult{}, nil
}

func (a *concurrencyProbeAdapter) FetchOrderBook(ctx context.Context, sub domain.SymbolSub) (domain.OrderBookSnapshot, error) {
	a.enter()
	defer a.leave()
	if err := probeSleep(ctx, a.delay); err != nil {
		return domain.OrderBookSnapshot{Platform: sub.Platform, DisplaySymbol: sub.DisplaySymbol, SnapshotTS: time.Now().UTC(), DepthStatus: domain.StatusError, Error: err.Error()}, err
	}
	return domain.OrderBookSnapshot{
		Platform:       sub.Platform,
		DisplaySymbol:  sub.DisplaySymbol,
		SourceEndpoint: sub.SourceEndpoint,
		SnapshotTS:     time.Now().UTC(),
		DepthStatus:    domain.StatusComplete,
		Bids:           []domain.Level{{Price: 99, Size: 1}},
		Asks:           []domain.Level{{Price: 101, Size: 1}},
	}, nil
}

func (a *concurrencyProbeAdapter) FetchTicker(ctx context.Context, sub domain.SymbolSub) (domain.VolumeSnapshot, error) {
	a.enter()
	defer a.leave()
	if err := probeSleep(ctx, a.delay); err != nil {
		return domain.VolumeSnapshot{Platform: sub.Platform, DisplaySymbol: sub.DisplaySymbol, SnapshotTS: time.Now().UTC(), Status: domain.StatusError, Error: err.Error()}, err
	}
	return domain.VolumeSnapshot{
		Platform:      sub.Platform,
		DisplaySymbol: sub.DisplaySymbol,
		SnapshotTS:    time.Now().UTC(),
		Volume24HUSD:  100,
		Status:        domain.StatusComplete,
	}, nil
}

func (a *concurrencyProbeAdapter) FetchTop30(ctx context.Context, surface domain.SymbolSub) ([]domain.Top30Row, error) {
	return nil, nil
}

func (a *concurrencyProbeAdapter) enter() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
}

func (a *concurrencyProbeAdapter) leave() {
	a.mu.Lock()
	a.active--
	a.mu.Unlock()
}

func (a *concurrencyProbeAdapter) max() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maxActive
}

func probeSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func TestCollectOnceLimitsPerPlatformConcurrency(t *testing.T) {
	cfg := config.Default()
	cfg.Platforms = []string{"binance"}
	cfg.Runtime.Collection.PerPlatformConcurrency = 1
	cfg.Runtime.Collection.PerPlatformRatePerSec = 0
	cfg.Runtime.HTTPTimeout = 200 * time.Millisecond
	cfg.Symbols = []domain.SymbolSub{
		{Platform: "binance", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC", APISymbol: "BTCUSDT"},
		{Platform: "binance", DisplaySymbol: "ETH-USDT (perp)", Canonical: "ETH", APISymbol: "ETHUSDT"},
		{Platform: "binance", DisplaySymbol: "SOL-USDT (perp)", Canonical: "SOL", APISymbol: "SOLUSDT"},
	}
	store := NewStore(cfg)
	probe := &concurrencyProbeAdapter{platform: "binance", delay: 15 * time.Millisecond}
	c := &Collector{
		cfg:         cfg,
		store:       store,
		adapters:    map[string]adapter.ExchangeAdapter{"binance": probe},
		cooldown:    map[string]time.Time{},
		consecFails: map[string]int{},
		cooldownNow: time.Now,
	}

	if err := c.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	if probe.max() > 1 {
		t.Fatalf("max in-flight binance requests = %d, want <= 1", probe.max())
	}
}
