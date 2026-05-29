package liquidity

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func TestDecideActionFirstTrigger(t *testing.T) {
	cfg := defaultCfg()
	now := mustParse(t, "2026-05-29T03:00:00Z")
	got := DecideAction(AlertState{}, true, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionFirstTrigger {
		t.Fatalf("action = %v, want first_trigger", got.Action)
	}
	if got.Phase != PhaseFirst {
		t.Errorf("phase = %q, want %q", got.Phase, PhaseFirst)
	}
	if got.DedupeKey != "liquidity_lag|BTC|seq1|first" {
		t.Errorf("dedupe = %q", got.DedupeKey)
	}
	if got.NewState.Status != StatusActive {
		t.Errorf("status = %q", got.NewState.Status)
	}
	if got.NewState.SeveritySeq != 1 {
		t.Errorf("seq = %d", got.NewState.SeveritySeq)
	}
	if !got.NewState.FirstTriggeredAt.Equal(now) || !got.NewState.LastPushedAt.Equal(now) {
		t.Errorf("timestamps wrong: first=%v pushed=%v", got.NewState.FirstTriggeredAt, got.NewState.LastPushedAt)
	}
}

func TestDecideActionReissueCooldown(t *testing.T) {
	cfg := defaultCfg()
	cfg.ReissueInterval = 6 * time.Hour
	prev := AlertState{
		Kind:             KindLiquidityLag,
		Canonical:        "BTC",
		Status:           StatusActive,
		SeveritySeq:      1,
		FirstTriggeredAt: mustParse(t, "2026-05-28T09:00:00Z"),
		LastPushedAt:     mustParse(t, "2026-05-29T00:00:00Z"),
	}
	now := mustParse(t, "2026-05-29T03:00:00Z") // only 3h since last push
	got := DecideAction(prev, true, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionSilent {
		t.Fatalf("want silent within cooldown, got %v", got.Action)
	}
	if got.DedupeKey != "" {
		t.Errorf("silent must not emit dedupe key, got %q", got.DedupeKey)
	}
	if got.NewState.ClearStreak != 0 {
		t.Errorf("clear_streak must reset to 0 when triggered, got %d", got.NewState.ClearStreak)
	}
}

func TestDecideActionReissueAfter6h(t *testing.T) {
	cfg := defaultCfg()
	prev := AlertState{
		Kind:             KindLiquidityLag,
		Canonical:        "BTC",
		Status:           StatusActive,
		SeveritySeq:      2,
		ReissueCount:     1,
		FirstTriggeredAt: mustParse(t, "2026-05-28T09:00:00Z"),
		LastPushedAt:     mustParse(t, "2026-05-28T21:00:00Z"),
	}
	now := mustParse(t, "2026-05-29T04:00:00Z") // 7h since last push
	got := DecideAction(prev, true, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionReissue {
		t.Fatalf("want reissue, got %v", got.Action)
	}
	if got.Phase != PhaseReissue {
		t.Errorf("phase = %q", got.Phase)
	}
	if got.NewState.ReissueCount != 2 {
		t.Errorf("reissue_count = %d, want 2", got.NewState.ReissueCount)
	}
	if got.DedupeKey != "liquidity_lag|BTC|seq2|reissue2" {
		t.Errorf("dedupe = %q", got.DedupeKey)
	}
	if !got.NewState.LastPushedAt.Equal(now) {
		t.Errorf("last_pushed must update to now, got %v", got.NewState.LastPushedAt)
	}
}

func TestDecideActionClearAfterStreak(t *testing.T) {
	cfg := defaultCfg()
	cfg.ClearConsecutive = 3
	prev := AlertState{
		Kind:             KindLiquidityLag,
		Canonical:        "BTC",
		Status:           StatusActive,
		SeveritySeq:      1,
		ClearStreak:      2,
		FirstTriggeredAt: mustParse(t, "2026-05-28T09:00:00Z"),
		LastPushedAt:     mustParse(t, "2026-05-29T03:00:00Z"),
	}
	now := mustParse(t, "2026-05-29T09:00:00Z")
	got := DecideAction(prev, false, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionClear {
		t.Fatalf("want clear, got %v", got.Action)
	}
	if got.NewState.Status != StatusCleared {
		t.Errorf("status = %q", got.NewState.Status)
	}
	if got.NewState.ClearStreak != 3 {
		t.Errorf("clear_streak should be 3 (filled the streak), got %d", got.NewState.ClearStreak)
	}
	if !strings.HasSuffix(got.DedupeKey, "|seq1|clear") {
		t.Errorf("dedupe = %q", got.DedupeKey)
	}
}

func TestDecideActionClearStreakAccumulatesBeforeFiring(t *testing.T) {
	cfg := defaultCfg()
	cfg.ClearConsecutive = 3
	prev := AlertState{
		Kind:        KindLiquidityLag,
		Canonical:   "BTC",
		Status:      StatusActive,
		SeveritySeq: 1,
		ClearStreak: 1,
	}
	now := mustParse(t, "2026-05-29T03:00:00Z")
	got := DecideAction(prev, false, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionSilent {
		t.Fatalf("want silent until streak reaches threshold, got %v", got.Action)
	}
	if got.NewState.ClearStreak != 2 {
		t.Errorf("clear_streak should increment to 2, got %d", got.NewState.ClearStreak)
	}
	if got.NewState.Status != StatusActive {
		t.Errorf("status should stay active, got %q", got.NewState.Status)
	}
}

func TestDecideActionReentryIncrementsSeq(t *testing.T) {
	cfg := defaultCfg()
	prev := AlertState{
		Kind:        KindLiquidityLag,
		Canonical:   "BTC",
		Status:      StatusCleared,
		SeveritySeq: 1,
		ClearStreak: 3,
	}
	now := mustParse(t, "2026-05-29T03:00:00Z")
	got := DecideAction(prev, true, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionFirstTrigger {
		t.Fatalf("re-entry must be first_trigger, got %v", got.Action)
	}
	if got.NewState.SeveritySeq != 2 {
		t.Errorf("seq = %d, want 2", got.NewState.SeveritySeq)
	}
	if got.DedupeKey != "liquidity_lag|BTC|seq2|first" {
		t.Errorf("dedupe = %q", got.DedupeKey)
	}
	if got.NewState.ClearStreak != 0 {
		t.Errorf("clear_streak must reset, got %d", got.NewState.ClearStreak)
	}
}

func TestDecideActionClearedAndStillNotTriggered(t *testing.T) {
	cfg := defaultCfg()
	prev := AlertState{
		Kind:        KindLiquidityLag,
		Canonical:   "BTC",
		Status:      StatusCleared,
		SeveritySeq: 1,
		ClearStreak: 5,
	}
	now := mustParse(t, "2026-05-29T03:00:00Z")
	got := DecideAction(prev, false, KindLiquidityLag, "BTC", cfg, now)
	if got.Action != ActionSilent {
		t.Errorf("want silent on cleared+!triggered, got %v", got.Action)
	}
	if got.NewState.ClearStreak != 0 {
		t.Errorf("clear_streak must reset when cleared+not triggered, got %d", got.NewState.ClearStreak)
	}
}
