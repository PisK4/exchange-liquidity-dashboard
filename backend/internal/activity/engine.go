package activity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

type EngineStore interface {
	IngestionStore
	ProducerStore
	DeliveryStore
	AcquireActivityLease(ctx context.Context, leaseName, ownerID string, ttl time.Duration) (bool, error)
	ReleaseActivityLease(ctx context.Context, leaseName, ownerID string) error
}

type EngineConfig struct {
	Enabled             bool
	OwnerID             string
	WorkerLeaseTTL      time.Duration
	WebhookURL          string
	DecisionTokenSecret string
	DashboardBaseURL    string
	MaxPerTick          int
	SendSpacing         time.Duration
	Sources             []SourceConfig
	Fetch               FetchFunc
	Parse               ParseFunc
}

type Engine struct {
	store  EngineStore
	cfg    EngineConfig
	client *http.Client
	now    func() time.Time
}

type EngineOption func(*Engine)

func WithEngineHTTPClient(client *http.Client) EngineOption {
	return func(e *Engine) { e.client = client }
}

func WithEngineNow(now func() time.Time) EngineOption {
	return func(e *Engine) { e.now = now }
}

type RunSummary struct {
	LeaseAcquired bool
	Ingestion     IngestionResult
	Producer      ProducerResult
	Delivery      DeliveryResult
}

func NewEngine(store EngineStore, cfg EngineConfig, opts ...EngineOption) *Engine {
	if cfg.OwnerID == "" {
		host, _ := os.Hostname()
		cfg.OwnerID = fmt.Sprintf("%s:%d:activity:all", host, os.Getpid())
	}
	if cfg.WorkerLeaseTTL <= 0 {
		cfg.WorkerLeaseTTL = 2 * time.Minute
	}
	if cfg.MaxPerTick <= 0 {
		cfg.MaxPerTick = 10
	}
	e := &Engine{
		store: store,
		cfg:   cfg,
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *Engine) RunOnce(ctx context.Context) (RunSummary, error) {
	if e == nil || e.store == nil {
		return RunSummary{}, errors.New("activity engine: store is nil")
	}
	if !e.cfg.Enabled {
		return RunSummary{}, errors.New("activity engine: disabled")
	}
	acquired, err := e.store.AcquireActivityLease(ctx, "activity:run_once", e.cfg.OwnerID, e.cfg.WorkerLeaseTTL)
	if err != nil {
		return RunSummary{}, err
	}
	summary := RunSummary{LeaseAcquired: acquired}
	if !acquired {
		return summary, nil
	}
	defer func() { _ = e.store.ReleaseActivityLease(context.Background(), "activity:run_once", e.cfg.OwnerID) }()

	if len(e.cfg.Sources) > 0 {
		ingestion, err := IngestSources(ctx, e.store, IngestionDeps{
			Sources: e.cfg.Sources,
			Fetch:   e.cfg.Fetch,
			Parse:   e.cfg.Parse,
			Now:     e.now,
		})
		if err != nil {
			return summary, err
		}
		summary.Ingestion = ingestion
	}
	producer, err := ProduceOutbox(ctx, e.store, ProducerConfig{
		WebhookURL:          e.cfg.WebhookURL,
		DecisionTokenSecret: e.cfg.DecisionTokenSecret,
		DashboardBaseURL:    e.cfg.DashboardBaseURL,
		MaxPerTick:          e.cfg.MaxPerTick,
		Now:                 e.now,
	})
	if err != nil {
		return summary, err
	}
	summary.Producer = producer
	delivery, err := DrainDueOutbox(ctx, e.store, DeliveryDeps{
		WebhookURL:  e.cfg.WebhookURL,
		Client:      e.client,
		Now:         e.now,
		BatchSize:   e.cfg.MaxPerTick,
		SendSpacing: e.cfg.SendSpacing,
	})
	if err != nil {
		return summary, err
	}
	summary.Delivery = delivery
	return summary, nil
}

func (e *Engine) Run(ctx context.Context, interval time.Duration, logf func(string, ...any)) error {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		summary, err := e.RunOnce(ctx)
		if logf != nil {
			if err != nil {
				logf("activity run failed: %v", err)
			} else {
				logf("activity run summary: lease=%v producer=%+v delivery=%+v", summary.LeaseAcquired, summary.Producer, summary.Delivery)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
