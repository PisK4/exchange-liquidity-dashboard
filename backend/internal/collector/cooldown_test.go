package collector

import (
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

func newCooldownCollector(t *testing.T, threshold int, duration time.Duration, now func() time.Time) *Collector {
	t.Helper()
	cfg := config.Config{Runtime: config.Runtime{
		CooldownFailureThreshold: threshold,
		CooldownDuration:         duration,
	}}
	c := &Collector{
		cfg:         cfg,
		cooldown:    map[string]time.Time{},
		consecFails: map[string]int{},
		cooldownNow: now,
	}
	return c
}

func TestCooldownTrigersAfterThresholdConsecutiveFailures(t *testing.T) {
	fixed := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	c := newCooldownCollector(t, 3, 5*time.Minute, func() time.Time { return fixed })
	sub := domain.SymbolSub{Platform: "binance", Canonical: "BTC"}

	if c.shouldSkipForCooldown(sub) {
		t.Fatalf("fresh pair must not be in cooldown")
	}
	c.recordCollectionResult(sub, false)
	c.recordCollectionResult(sub, false)
	if c.shouldSkipForCooldown(sub) {
		t.Fatalf("two failures should not trigger cooldown when threshold=3")
	}
	c.recordCollectionResult(sub, false)
	if !c.shouldSkipForCooldown(sub) {
		t.Fatalf("third consecutive failure must trigger cooldown")
	}
}

func TestCooldownClearsOnSuccess(t *testing.T) {
	fixed := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	c := newCooldownCollector(t, 2, 5*time.Minute, func() time.Time { return fixed })
	sub := domain.SymbolSub{Platform: "okx", Canonical: "ETH"}

	c.recordCollectionResult(sub, false)
	c.recordCollectionResult(sub, false)
	if !c.shouldSkipForCooldown(sub) {
		t.Fatalf("expected cooldown after threshold failures")
	}
	c.recordCollectionResult(sub, true)
	if c.shouldSkipForCooldown(sub) {
		t.Fatalf("a single success must clear cooldown")
	}
}

func TestCooldownExpiresAfterDuration(t *testing.T) {
	now := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := newCooldownCollector(t, 1, 5*time.Minute, clock)
	sub := domain.SymbolSub{Platform: "gate", Canonical: "URNM"}

	c.recordCollectionResult(sub, false)
	if !c.shouldSkipForCooldown(sub) {
		t.Fatalf("expected immediate cooldown with threshold=1")
	}

	now = now.Add(4 * time.Minute)
	if !c.shouldSkipForCooldown(sub) {
		t.Fatalf("expected cooldown still active before expiry")
	}

	now = now.Add(2 * time.Minute) // 6m total > 5m duration
	if c.shouldSkipForCooldown(sub) {
		t.Fatalf("expected cooldown to expire after duration")
	}
}

func TestCooldownDisabledByConfig(t *testing.T) {
	c := newCooldownCollector(t, 0, 5*time.Minute, time.Now)
	sub := domain.SymbolSub{Platform: "lighter", Canonical: "BTC"}
	c.recordCollectionResult(sub, false)
	c.recordCollectionResult(sub, false)
	c.recordCollectionResult(sub, false)
	if c.shouldSkipForCooldown(sub) {
		t.Fatalf("threshold=0 must disable cooldown entirely")
	}
}

func TestCooldownIsolatesAcrossPairs(t *testing.T) {
	fixed := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	c := newCooldownCollector(t, 2, 5*time.Minute, func() time.Time { return fixed })
	a := domain.SymbolSub{Platform: "binance", Canonical: "BTC"}
	b := domain.SymbolSub{Platform: "binance", Canonical: "ETH"}

	c.recordCollectionResult(a, false)
	c.recordCollectionResult(a, false)
	if !c.shouldSkipForCooldown(a) {
		t.Fatalf("a should be in cooldown")
	}
	if c.shouldSkipForCooldown(b) {
		t.Fatalf("b must remain available even when a is parked")
	}
}
