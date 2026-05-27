package listing

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"edgex-dashboard/backend/internal/config"
)

// Engine orchestrates the Listing Agent worker loop. It exposes both
// a Run (long-running) and a RunOnce (single tick) entry point so the
// dashboard CLI can drive the same code path from `--run-once` smoke
// tests and from the long-lived role=listing process.
type Engine struct {
	cfg  config.Config
	repo *Repository
	deps EngineDeps
}

// EngineDeps wires the moving parts an Engine needs that aren't part
// of config (clocks, universe loader, HTTP client). Production
// callers leave the optional fields nil and let the engine fill in
// sensible defaults.
type EngineDeps struct {
	Now          func() time.Time
	LoadUniverse func() (*config.ListedUniverse, error)
	HTTPClient   *http.Client
	OwnerID      string
	// Logger is used when non-nil; defaults to the standard logger.
	Logger *log.Logger
}

// RunSummary aggregates per-stage results from a single RunOnce tick.
type RunSummary struct {
	Fusion    FusionResult
	Top30Push Top30PushResult
	Delivery  DeliveryResult
	Started   time.Time
	Finished  time.Time
}

// NewEngine wires an Engine with concrete dependencies. The
// dashboard CLI passes the live *Repository plus a configured loader
// for listed_universe.yaml; tests pass an in-memory loader.
func NewEngine(cfg config.Config, repo *Repository, deps EngineDeps) *Engine {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = http.DefaultClient
	}
	if deps.OwnerID == "" {
		deps.OwnerID, _ = os.Hostname()
		if deps.OwnerID == "" {
			deps.OwnerID = "listing-engine"
		}
	}
	if deps.Logger == nil {
		deps.Logger = log.Default()
	}
	return &Engine{cfg: cfg, repo: repo, deps: deps}
}

// RunOnce executes one full tick of the listing pipeline. Each stage
// reports its own fail-closed status; one stage's fail does not
// short-circuit later stages because the operator dashboard relies
// on consistent counts even when one source is unavailable.
func (e *Engine) RunOnce(ctx context.Context) (RunSummary, error) {
	start := e.deps.Now()
	summary := RunSummary{Started: start}

	// Step 1: fuse any unfused signals into candidates.
	fusion, fusionErr := FuseSignals(ctx, e.repo, FusionDeps{
		LoadUniverse: e.deps.LoadUniverse,
		Now:          e.deps.Now,
	})
	summary.Fusion = fusion
	if fusionErr != nil && !errors.Is(fusionErr, ErrFusionFailClosed) {
		e.deps.Logger.Printf("listing engine: fusion error: %v", fusionErr)
	}

	// Step 2: produce Top30 push outbox rows.
	webhook := resolveWebhookURL(e.cfg.Runtime.ListingAgent.Delivery)
	top30, top30Err := ProduceTop30Push(ctx, e.repo, Top30Deps{
		LoadUniverse:  e.deps.LoadUniverse,
		Now:           e.deps.Now,
		DashboardBase: e.cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL,
		WebhookURL:    webhook,
		MaxAttempts:   e.cfg.Runtime.ListingAgent.Worker.MaxAttempts,
		StaleAfter:    e.cfg.Runtime.ListingAgent.Top30Push.StaleAfter,
	})
	summary.Top30Push = top30
	if top30Err != nil {
		e.deps.Logger.Printf("listing engine: top30 push error: %v", top30Err)
	}

	// Step 3: drain due outbox. Empty webhook URL marks rows as disabled
	// without producing a network call; this is intentional so smoke
	// tests can run without a webhook configured.
	delivery, deliveryErr := DrainDueOutbox(ctx, e.repo, DeliveryDeps{
		WebhookURL:    webhook,
		WebhookSecret: e.cfg.Runtime.ListingAgent.Delivery.Top30WebhookSecret,
		Client:        e.deps.HTTPClient,
		Now:           e.deps.Now,
	})
	summary.Delivery = delivery
	if deliveryErr != nil {
		e.deps.Logger.Printf("listing engine: delivery error: %v", deliveryErr)
	}

	summary.Finished = e.deps.Now()
	return summary, nil
}

// Run loops until ctx is cancelled, ticking RunOnce on the configured
// interval. The Top30 push interval doubles as the engine cadence
// because it bounds the freshness of the user-facing card; fusion is
// idempotent and cheap enough to run at the same cadence.
func (e *Engine) Run(ctx context.Context) error {
	interval := e.cfg.Runtime.ListingAgent.Top30Push.PollInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		summary, err := e.RunOnce(ctx)
		if err != nil {
			e.deps.Logger.Printf("listing engine: tick error: %v", err)
		}
		e.deps.Logger.Printf("listing engine tick: fusion=%+v top30=%+v delivery=%+v", summary.Fusion, summary.Top30Push, summary.Delivery)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func resolveWebhookURL(cfg config.ListingDeliveryConfig) string {
	if cfg.Top30WebhookURL != "" {
		return cfg.Top30WebhookURL
	}
	if cfg.Top30WebhookURLEnv != "" {
		return os.Getenv(cfg.Top30WebhookURLEnv)
	}
	return ""
}
