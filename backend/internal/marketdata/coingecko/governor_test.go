package coingecko

import (
	"context"
	"testing"
	"time"
)

func TestBudgetGovernorDisabledAllowsRequests(t *testing.T) {
	g := NewBudgetGovernor(GovernorConfig{Enabled: false, RequestsPerMinute: 1, Burst: 1})
	if err := g.BeforeRequest(context.Background(), "/derivatives", PriorityPrimary); err != nil {
		t.Fatalf("BeforeRequest disabled = %v, want nil", err)
	}
	if status := g.Status(); status.State != "disabled" {
		t.Fatalf("status = %q, want disabled", status.State)
	}
}

func TestBudgetGovernorThrottlesSharedMainBudget(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	g := NewBudgetGovernor(GovernorConfig{Enabled: true, RequestsPerMinute: 60, Burst: 1})
	g.now = func() time.Time { return now }
	g.sleep = func(ctx context.Context, d time.Duration) error {
		now = now.Add(d)
		return nil
	}
	g.tokens = 1
	g.lastRefill = now

	if err := g.BeforeRequest(context.Background(), "/derivatives", PriorityPrimary); err != nil {
		t.Fatalf("first request: %v", err)
	}
	beforeWait := now
	if err := g.BeforeRequest(context.Background(), "/derivatives", PriorityPrimary); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if !now.After(beforeWait) {
		t.Fatalf("second request should wait for a refilled token; before=%s after=%s", beforeWait, now)
	}
}

func TestBudgetGovernorBackfillUsesSeparateBudget(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	g := NewBudgetGovernor(GovernorConfig{Enabled: true, RequestsPerMinute: 600, Burst: 10, BackfillRequestsPerMinute: 60})
	g.now = func() time.Time { return now }
	g.sleep = func(ctx context.Context, d time.Duration) error {
		now = now.Add(d)
		return nil
	}
	g.tokens = 10
	g.lastRefill = now
	g.backfillTokens = 1
	g.backfillLastRefill = now

	if err := g.BeforeRequest(context.Background(), "/exchanges/binance/volume_chart", PriorityBackfill); err != nil {
		t.Fatalf("first backfill request: %v", err)
	}
	beforeWait := now
	if err := g.BeforeRequest(context.Background(), "/exchanges/binance/volume_chart", PriorityBackfill); err != nil {
		t.Fatalf("second backfill request: %v", err)
	}
	if !now.After(beforeWait) {
		t.Fatalf("second backfill request should wait on backfill budget; before=%s after=%s", beforeWait, now)
	}
}

func TestBudgetGovernorRateLimitStartsCooldown(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	g := NewBudgetGovernor(GovernorConfig{Enabled: true, RequestsPerMinute: 60, Burst: 1, DefaultCooldown: time.Minute})
	g.now = func() time.Time { return now }
	g.lastRefill = now

	g.AfterResponse("/derivatives", PriorityPrimary, &RateLimitedError{Endpoint: "/derivatives", RetryAfter: 42 * time.Second})
	status := g.Status()
	if status.State != "cooling_down" {
		t.Fatalf("state = %q, want cooling_down", status.State)
	}
	if got, want := status.CooldownUntil, now.Add(42*time.Second); !got.Equal(want) {
		t.Fatalf("cooldown_until = %s, want %s", got, want)
	}
	err := g.BeforeRequest(context.Background(), "/derivatives", PriorityPrimary)
	if !IsCoolingDown(err) {
		t.Fatalf("BeforeRequest err = %v, want cooldown", err)
	}
}

func TestBudgetGovernorCooldownUsesDefaultAndMax(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	g := NewBudgetGovernor(GovernorConfig{Enabled: true, RequestsPerMinute: 60, Burst: 1, DefaultCooldown: 15 * time.Minute, MaxCooldown: time.Minute})
	g.now = func() time.Time { return now }
	g.lastRefill = now

	g.AfterResponse("/derivatives", PriorityPrimary, &RateLimitedError{Endpoint: "/derivatives"})
	if got, want := g.Status().CooldownUntil, now.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("default cooldown should be capped: got %s want %s", got, want)
	}
}
