package listing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
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
	Fusion          FusionResult
	Top30Push       Top30PushResult
	DivergencePush  DivergencePushResult
	Delivery        DeliveryResult
	Started         time.Time
	Finished        time.Time
}

// NewEngine wires an Engine with concrete dependencies. The
// dashboard CLI passes the live *Repository plus a configured loader
// for listed_universe.yaml; tests pass an in-memory loader.
func NewEngine(cfg config.Config, repo *Repository, deps EngineDeps) *Engine {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.HTTPClient == nil {
		proxy := strings.TrimSpace(cfg.Runtime.ListingAgent.Delivery.Proxy)
		client, err := buildDeliveryHTTPClient(proxy)
		if err != nil {
			log.Printf("listing engine: delivery proxy %q ignored: %v", proxy, err)
			client = http.DefaultClient
		} else if proxy != "" {
			log.Printf("listing engine: delivery http client routed through proxy %q", proxy)
		}
		deps.HTTPClient = client
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

// buildDeliveryHTTPClient returns an *http.Client whose Transport
// routes every outbound request through proxyURL. Returns the package
// default client when proxyURL is blank so production deployments
// without a configured proxy keep their previous behaviour. The
// resulting client is intentionally only used by the Listing Agent
// delivery worker; the 9 exchange adapters and the CoinGecko
// collector continue to honour their own per-client proxy knobs so
// latency measurements are not polluted by a shared transport.
func buildDeliveryHTTPClient(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return http.DefaultClient, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("proxy url missing scheme or host: %q", proxyURL)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{Transport: transport}, nil
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
	webhook := resolveWebhookURL(e.cfg)
	top30, top30Err := ProduceTop30Push(ctx, e.repo, Top30Deps{
		LoadUniverse:             e.deps.LoadUniverse,
		Now:                      e.deps.Now,
		DashboardBase:            e.cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL,
		WebhookURL:               webhook,
		MaxAttempts:              e.cfg.Runtime.ListingAgent.Worker.MaxAttempts,
		StaleAfter:               e.cfg.Runtime.ListingAgent.Top30Push.StaleAfter,
		AutoQuietAfterStreakDays: e.cfg.Runtime.ListingAgent.Top30Push.AutoQuietAfterStreakDays,
		SendSpacing:              e.cfg.Runtime.ListingAgent.Top30Push.SendSpacing,
	})
	summary.Top30Push = top30
	if top30Err != nil {
		e.deps.Logger.Printf("listing engine: top30 push error: %v", top30Err)
	}

	// Step 2b: produce divergence push outbox rows (#2-#5). Shares the
	// same webhook + max-attempts knobs as the hot-gap path because
	// both target the same Lark channel.
	divergence, divErr := ProduceDivergencePush(ctx, e.repo, DivergenceDeps{
		Now:           e.deps.Now,
		DashboardBase: e.cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL,
		WebhookURL:    webhook,
		MaxAttempts:   e.cfg.Runtime.ListingAgent.Worker.MaxAttempts,
		DivergenceCfg: e.cfg.Runtime.Top30Divergence,
		PushCfg:       e.cfg.Runtime.ListingAgent.Top30DivergencePush,
	})
	summary.DivergencePush = divergence
	if divErr != nil {
		e.deps.Logger.Printf("listing engine: divergence push error: %v", divErr)
	}

	// Step 3: drain due outbox. Empty webhook URL marks rows as disabled
	// without producing a network call; this is intentional so smoke
	// tests can run without a webhook configured.
	delivery, deliveryErr := DrainDueOutbox(ctx, e.repo, DeliveryDeps{
		WebhookURL:    webhook,
		WebhookSecret: e.cfg.Runtime.ListingAgent.Delivery.Top30WebhookSecret,
		Client:        e.deps.HTTPClient,
		Now:           e.deps.Now,
		BatchSize:     e.cfg.Runtime.ListingAgent.Top30Push.MaxPerTick,
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
		e.deps.Logger.Printf("listing engine tick: fusion=%+v top30=%+v divergence=%+v delivery=%+v", summary.Fusion, summary.Top30Push, summary.DivergencePush, summary.Delivery)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func resolveWebhookURL(cfg config.Config) string {
	if cfg.Alert.Enabled && strings.TrimSpace(cfg.Alert.WebHookP3) != "" {
		return cfg.Alert.WebHookP3
	}
	delivery := cfg.Runtime.ListingAgent.Delivery
	if delivery.Top30WebhookURL != "" {
		return delivery.Top30WebhookURL
	}
	if delivery.Top30WebhookURLEnv != "" {
		return os.Getenv(delivery.Top30WebhookURLEnv)
	}
	return ""
}
