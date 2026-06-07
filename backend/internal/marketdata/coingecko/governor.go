package coingecko

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RequestPriority lets the shared CoinGecko budget distinguish dashboard
// collection from lower-priority backfill and listing enrichment calls.
type RequestPriority string

const (
	PriorityPrimary  RequestPriority = "primary"
	PriorityBackfill RequestPriority = "backfill"
	PriorityListing  RequestPriority = "listing"
)

// GovernorConfig is the CoinGecko package-local copy of the runtime
// governance knobs. Keeping it here avoids coupling the HTTP client package to
// backend/internal/config.
type GovernorConfig struct {
	Enabled                   bool
	RequestsPerMinute         int
	Burst                     int
	DefaultCooldown           time.Duration
	MaxCooldown               time.Duration
	BackfillRequestsPerMinute int
}

// GovernorStatus is a lightweight snapshot exposed to logs and status APIs.
type GovernorStatus struct {
	Enabled         bool            `json:"enabled"`
	State           string          `json:"state"`
	CooldownUntil   time.Time       `json:"cooldown_until,omitempty"`
	LastEndpoint    string          `json:"last_endpoint,omitempty"`
	LastError       string          `json:"last_error,omitempty"`
	LastPriority    RequestPriority `json:"last_priority,omitempty"`
	RequestsPerMin  int             `json:"requests_per_minute"`
	BackfillPerMin  int             `json:"backfill_requests_per_minute"`
	DefaultCooldown time.Duration   `json:"default_cooldown"`
}

// BudgetGovernor is a process-local rate limiter and cooldown switch for all
// CoinGecko endpoints used by the local backend process.
type BudgetGovernor struct {
	mu sync.Mutex

	cfg GovernorConfig

	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	tokens     float64
	lastRefill time.Time

	backfillTokens     float64
	backfillLastRefill time.Time

	cooldownUntil time.Time
	lastEndpoint  string
	lastError     string
	lastPriority  RequestPriority
}

// NewBudgetGovernor constructs a process-local governor. A nil governor is
// also valid for callers that want historical ungoverned behaviour.
func NewBudgetGovernor(cfg GovernorConfig) *BudgetGovernor {
	if cfg.Burst <= 0 {
		cfg.Burst = 1
	}
	if cfg.DefaultCooldown <= 0 {
		cfg.DefaultCooldown = 15 * time.Minute
	}
	if cfg.MaxCooldown <= 0 {
		cfg.MaxCooldown = time.Hour
	}
	now := time.Now().UTC()
	return &BudgetGovernor{
		cfg:                cfg,
		now:                func() time.Time { return time.Now().UTC() },
		sleep:              defaultSleep,
		tokens:             float64(cfg.Burst),
		lastRefill:         now,
		backfillTokens:     1,
		backfillLastRefill: now,
	}
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// BeforeRequest blocks until the shared budget admits a request or returns a
// CooldownError when CoinGecko is cooling down after a previous 429.
func (g *BudgetGovernor) BeforeRequest(ctx context.Context, endpoint string, priority RequestPriority) error {
	if g == nil || !g.cfg.Enabled {
		return nil
	}
	for {
		wait, err := g.reserve(endpoint, priority)
		if err != nil || wait <= 0 {
			return err
		}
		if err := g.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (g *BudgetGovernor) reserve(endpoint string, priority RequestPriority) (time.Duration, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if g.cooldownUntil.After(now) {
		return 0, &CooldownError{Endpoint: endpoint, Priority: priority, CooldownUntil: g.cooldownUntil}
	}
	g.refillLocked(now)
	if g.tokens < 1 {
		return g.waitForMainTokenLocked(), nil
	}
	if priority == PriorityBackfill && g.cfg.BackfillRequestsPerMinute > 0 {
		if g.backfillTokens < 1 {
			return g.waitForBackfillTokenLocked(), nil
		}
		g.backfillTokens -= 1
	}
	g.tokens -= 1
	g.lastEndpoint = endpoint
	g.lastPriority = priority
	return 0, nil
}

func (g *BudgetGovernor) refillLocked(now time.Time) {
	if g.lastRefill.IsZero() {
		g.lastRefill = now
	}
	if g.cfg.RequestsPerMinute > 0 {
		elapsed := now.Sub(g.lastRefill).Seconds()
		g.tokens += elapsed * float64(g.cfg.RequestsPerMinute) / 60
		if max := float64(g.cfg.Burst); g.tokens > max {
			g.tokens = max
		}
	}
	g.lastRefill = now

	if g.backfillLastRefill.IsZero() {
		g.backfillLastRefill = now
	}
	if g.cfg.BackfillRequestsPerMinute > 0 {
		elapsed := now.Sub(g.backfillLastRefill).Seconds()
		g.backfillTokens += elapsed * float64(g.cfg.BackfillRequestsPerMinute) / 60
		if g.backfillTokens > 1 {
			g.backfillTokens = 1
		}
	}
	g.backfillLastRefill = now
}

func (g *BudgetGovernor) waitForMainTokenLocked() time.Duration {
	if g.cfg.RequestsPerMinute <= 0 {
		return 0
	}
	return time.Duration((1-g.tokens)*60/float64(g.cfg.RequestsPerMinute)*float64(time.Second)) + time.Millisecond
}

func (g *BudgetGovernor) waitForBackfillTokenLocked() time.Duration {
	if g.cfg.BackfillRequestsPerMinute <= 0 {
		return 0
	}
	return time.Duration((1-g.backfillTokens)*60/float64(g.cfg.BackfillRequestsPerMinute)*float64(time.Second)) + time.Millisecond
}

// AfterResponse updates cooldown state from a completed request.
func (g *BudgetGovernor) AfterResponse(endpoint string, priority RequestPriority, err error) {
	if g == nil || !g.cfg.Enabled {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastEndpoint = endpoint
	g.lastPriority = priority
	if err == nil {
		g.lastError = ""
		return
	}
	g.lastError = err.Error()
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		return
	}
	d := rl.RetryAfter
	if d <= 0 {
		d = g.cfg.DefaultCooldown
	}
	if g.cfg.MaxCooldown > 0 && d > g.cfg.MaxCooldown {
		d = g.cfg.MaxCooldown
	}
	g.cooldownUntil = g.now().Add(d)
}

// Status returns a stable snapshot for observability surfaces.
func (g *BudgetGovernor) Status() GovernorStatus {
	if g == nil {
		return GovernorStatus{Enabled: false, State: "disabled"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	state := "healthy"
	if !g.cfg.Enabled {
		state = "disabled"
	} else if g.cooldownUntil.After(now) {
		state = "cooling_down"
	}
	return GovernorStatus{
		Enabled:         g.cfg.Enabled,
		State:           state,
		CooldownUntil:   g.cooldownUntil,
		LastEndpoint:    g.lastEndpoint,
		LastError:       g.lastError,
		LastPriority:    g.lastPriority,
		RequestsPerMin:  g.cfg.RequestsPerMinute,
		BackfillPerMin:  g.cfg.BackfillRequestsPerMinute,
		DefaultCooldown: g.cfg.DefaultCooldown,
	}
}

// CooldownError is returned when callers should not send a CoinGecko request.
type CooldownError struct {
	Endpoint      string
	Priority      RequestPriority
	CooldownUntil time.Time
}

func (e *CooldownError) Error() string {
	return fmt.Sprintf("coingecko: cooldown active until %s for %s (%s)", e.CooldownUntil.UTC().Format(time.RFC3339), e.Endpoint, e.Priority)
}

// IsCoolingDown reports whether err is a local governor cooldown skip.
func IsCoolingDown(err error) bool {
	if err == nil {
		return false
	}
	var cooldown *CooldownError
	return errors.As(err, &cooldown)
}
