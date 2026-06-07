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
	Schedule            EngineSchedule
	WebhookURL          string
	WebhookURLByChannel map[string]string
	DecisionTokenSecret string
	DashboardBaseURL    string
	MaxPerTick          int
	SendSpacing         time.Duration
	SourceDelivery      []SourceDeliveryPolicy
	Sources             []SourceConfig
	Fetch               FetchFunc
	Parse               ParseFunc
}

type EngineSchedule struct {
	IngestionInterval time.Duration
	ProducerInterval  time.Duration
	DeliveryInterval  time.Duration
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
	producer, err := ProduceOutbox(ctx, e.store, e.producerConfig())
	if err != nil {
		return summary, err
	}
	summary.Producer = producer
	delivery, err := DrainDueOutbox(ctx, e.store, e.deliveryDeps())
	if err != nil {
		return summary, err
	}
	summary.Delivery = delivery
	return summary, nil
}

func (e *Engine) RunIngestionOnce(ctx context.Context) (RunSummary, error) {
	summary, acquired, err := e.acquirePhaseLease(ctx, "activity:ingestion")
	if err != nil || !acquired {
		return summary, err
	}
	defer func() { _ = e.store.ReleaseActivityLease(context.Background(), "activity:ingestion", e.cfg.OwnerID) }()
	if len(e.cfg.Sources) == 0 {
		return summary, nil
	}
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
	return summary, nil
}

func (e *Engine) RunProducerOnce(ctx context.Context) (RunSummary, error) {
	summary, acquired, err := e.acquirePhaseLease(ctx, "activity:producer")
	if err != nil || !acquired {
		return summary, err
	}
	defer func() { _ = e.store.ReleaseActivityLease(context.Background(), "activity:producer", e.cfg.OwnerID) }()
	producer, err := ProduceOutbox(ctx, e.store, e.producerConfig())
	if err != nil {
		return summary, err
	}
	summary.Producer = producer
	return summary, nil
}

func (e *Engine) RunDeliveryOnce(ctx context.Context) (RunSummary, error) {
	summary, acquired, err := e.acquirePhaseLease(ctx, "activity:delivery")
	if err != nil || !acquired {
		return summary, err
	}
	defer func() { _ = e.store.ReleaseActivityLease(context.Background(), "activity:delivery", e.cfg.OwnerID) }()
	delivery, err := DrainDueOutbox(ctx, e.store, e.deliveryDeps())
	if err != nil {
		return summary, err
	}
	summary.Delivery = delivery
	return summary, nil
}

func (e *Engine) Run(ctx context.Context, schedule EngineSchedule, logf func(string, ...any)) error {
	schedule = normalizeEngineSchedule(schedule)
	go e.runPhaseLoop(ctx, "ingestion", schedule.IngestionInterval, e.RunIngestionOnce, logf)
	go e.runPhaseLoop(ctx, "producer", schedule.ProducerInterval, e.RunProducerOnce, logf)
	go e.runPhaseLoop(ctx, "delivery", schedule.DeliveryInterval, e.RunDeliveryOnce, logf)
	<-ctx.Done()
	return ctx.Err()
}

func (e *Engine) runPhaseLoop(ctx context.Context, phase string, interval time.Duration, run func(context.Context) (RunSummary, error), logf func(string, ...any)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		summary, err := run(ctx)
		if logf != nil {
			if err != nil {
				logf("activity %s run failed: %v", phase, err)
			} else {
				logf("activity %s run summary: lease=%v ingestion=%+v producer=%+v delivery=%+v", phase, summary.LeaseAcquired, summary.Ingestion, summary.Producer, summary.Delivery)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Engine) acquirePhaseLease(ctx context.Context, leaseName string) (RunSummary, bool, error) {
	if e == nil || e.store == nil {
		return RunSummary{}, false, errors.New("activity engine: store is nil")
	}
	if !e.cfg.Enabled {
		return RunSummary{}, false, errors.New("activity engine: disabled")
	}
	acquired, err := e.store.AcquireActivityLease(ctx, leaseName, e.cfg.OwnerID, e.cfg.WorkerLeaseTTL)
	if err != nil {
		return RunSummary{}, false, err
	}
	summary := RunSummary{LeaseAcquired: acquired}
	return summary, acquired, nil
}

func (e *Engine) producerConfig() ProducerConfig {
	return ProducerConfig{
		WebhookURL:          e.cfg.WebhookURL,
		WebhookURLByChannel: e.cfg.WebhookURLByChannel,
		DecisionTokenSecret: e.cfg.DecisionTokenSecret,
		DashboardBaseURL:    e.cfg.DashboardBaseURL,
		MaxPerTick:          e.cfg.MaxPerTick,
		SourcePolicies:      e.cfg.SourceDelivery,
		Now:                 e.now,
	}
}

func (e *Engine) deliveryDeps() DeliveryDeps {
	return DeliveryDeps{
		WebhookURL:          e.cfg.WebhookURL,
		WebhookURLByChannel: e.cfg.WebhookURLByChannel,
		Client:              e.client,
		Now:                 e.now,
		BatchSize:           e.cfg.MaxPerTick,
		SendSpacing:         e.cfg.SendSpacing,
	}
}

func normalizeEngineSchedule(schedule EngineSchedule) EngineSchedule {
	if schedule.IngestionInterval <= 0 {
		schedule.IngestionInterval = 5 * time.Minute
	}
	if schedule.ProducerInterval <= 0 {
		schedule.ProducerInterval = time.Minute
	}
	if schedule.DeliveryInterval <= 0 {
		schedule.DeliveryInterval = 30 * time.Second
	}
	return schedule
}
