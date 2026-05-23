package adapter

import (
	"testing"
	"time"
)

// retryBackoff baseline = attempt^2 * 300ms; with up-to-25% jitter it
// must stay in [base, 1.25*base].
func TestRetryBackoffStaysWithinJitterBudget(t *testing.T) {
	for attempt := 1; attempt <= 4; attempt++ {
		base := time.Duration(attempt*attempt) * 300 * time.Millisecond
		ceiling := base + base*25/100
		// Sample many calls — wall-clock entropy means each invocation
		// independently picks a slice of the jitter budget.
		for i := 0; i < 200; i++ {
			got := retryBackoff(attempt)
			if got < base {
				t.Fatalf("attempt=%d retryBackoff=%v < base=%v (jitter must be >= 0)", attempt, got, base)
			}
			if got > ceiling {
				t.Fatalf("attempt=%d retryBackoff=%v > ceiling=%v (jitter must be <= 25%%)", attempt, got, ceiling)
			}
		}
	}
}

func TestRetryBackoffJitterIsNonZero(t *testing.T) {
	// Stub the entropy source so we can prove jitter actually moves the
	// total above the deterministic base.
	orig := retryJitterFracBP
	defer func() { retryJitterFracBP = orig }()
	retryJitterFracBP = func(int) int { return 2500 }
	got := retryBackoff(1)
	want := 300*time.Millisecond + 75*time.Millisecond // 25% of 300ms
	if got != want {
		t.Errorf("retryBackoff(1) with full jitter = %v, want %v", got, want)
	}
}

func TestRetryBackoffJitterZeroFracIsBaseOnly(t *testing.T) {
	orig := retryJitterFracBP
	defer func() { retryJitterFracBP = orig }()
	retryJitterFracBP = func(int) int { return 0 }
	got := retryBackoff(2)
	want := 4 * 300 * time.Millisecond
	if got != want {
		t.Errorf("retryBackoff(2) with zero jitter = %v, want %v", got, want)
	}
}
