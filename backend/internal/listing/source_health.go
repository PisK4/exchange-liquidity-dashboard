package listing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/announcement"
	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// SourceHealthKey identifies one source in t_listing_source_state.
// (SourceKey, SourceType, Platform) together let the operator
// dashboard group sources by family and platform while keeping the
// unique row keyed by SourceKey.
type SourceHealthKey struct {
	SourceKey  string
	SourceType string
	Platform   string
}

// PollHealthDeps wires the clock + escalation thresholds the wrapper
// needs. ErrorThreshold is the inclusive trigger: once
// consecutive_error_count >= ErrorThreshold the wrapper writes
// disabled_until = now + ErrorBackoff. Schema-drift errors bypass
// the counter and go straight to disabled_until = now + DriftBackoff.
type PollHealthDeps struct {
	Now            func() time.Time
	ErrorThreshold int
	ErrorBackoff   time.Duration
	DriftBackoff   time.Duration
}

// PollHealthOutcome describes what the wrapper decided this tick.
type PollHealthOutcome struct {
	Skipped           bool
	Status            string
	SchemaDrift       bool
	ConsecutiveErrors int
	DisabledUntil     *time.Time
}

// PollWithSourceHealth wraps one poll attempt with the
// t_listing_source_state book-keeping required by Phase 1 of
// 2026-05-29-listing-agent.md.
//
// Contract:
//   - Load existing source state row. nil → first run.
//   - If disabled_until is in the future → skip the poll, return
//     Skipped=true with no error so the engine can move on without
//     paying a fetch round-trip.
//   - Else run the poll callback. Classify the outcome:
//   - nil error → status=ok, consecutive_error_count=0.
//   - *SchemaDriftError (either subpackage) → status=schema_drift,
//     schema_drift_count++, disabled_until=now+DriftBackoff.
//   - generic error → status=error, consecutive_error_count++,
//     escalating to disabled_until=now+ErrorBackoff once the count
//     reaches ErrorThreshold.
//   - Persist the new state via UpsertSourceState.
//   - Return the original poll error verbatim so callers can wrap it
//     in their own logging without losing %w chains.
//
// The wrapper deliberately does NOT touch t_listing_instrument_snapshot
// or t_listing_signal_observation; that work belongs to the poll
// callbacks (RunInstrumentPoll, RunAnnouncementPoll). Separation of
// concerns keeps both layers narrow and individually unit-testable.
func PollWithSourceHealth(
	ctx context.Context,
	repo *Repository,
	key SourceHealthKey,
	deps PollHealthDeps,
	poll func(ctx context.Context) error,
) (PollHealthOutcome, error) {
	if repo == nil {
		return PollHealthOutcome{}, errors.New("source health: repo is nil")
	}
	if key.SourceKey == "" || key.SourceType == "" || key.Platform == "" {
		return PollHealthOutcome{}, errors.New("source health: SourceKey/SourceType/Platform required")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.ErrorThreshold <= 0 {
		deps.ErrorThreshold = 5
	}
	if deps.ErrorBackoff <= 0 {
		deps.ErrorBackoff = 15 * time.Minute
	}
	if deps.DriftBackoff <= 0 {
		deps.DriftBackoff = 30 * time.Minute
	}

	prev, err := repo.LoadSourceState(ctx, key.SourceKey)
	if err != nil {
		return PollHealthOutcome{}, fmt.Errorf("load source state %s: %w", key.SourceKey, err)
	}
	now := deps.Now()

	if prev != nil && prev.DisabledUntil != nil && prev.DisabledUntil.After(now) {
		until := *prev.DisabledUntil
		return PollHealthOutcome{
			Skipped:           true,
			Status:            SourceStatusDisabledUntil,
			ConsecutiveErrors: prev.ConsecutiveErrorCount,
			DisabledUntil:     &until,
		}, nil
	}

	pollErr := poll(ctx)
	next := SourceState{
		SourceKey:  key.SourceKey,
		SourceType: key.SourceType,
		Platform:   key.Platform,
		UpdatedAt:  now,
	}
	if prev != nil {
		next.SchemaDriftCount = prev.SchemaDriftCount
		next.LastSuccessAt = prev.LastSuccessAt
	}
	out := PollHealthOutcome{}

	switch {
	case pollErr == nil:
		next.Status = SourceStatusOK
		next.ConsecutiveErrorCount = 0
		next.LastSuccessAt = &now
		out.Status = SourceStatusOK
		out.ConsecutiveErrors = 0
	case isSchemaDriftError(pollErr):
		next.Status = SourceStatusSchemaDrift
		next.SchemaDriftCount++
		next.LastErrorAt = &now
		next.LastError = pollErr.Error()
		// schema drift skips the consecutive_error_count path because
		// drift is a different operator signal (parser is broken,
		// not the upstream API) — collapsing them would mask one.
		if prev != nil {
			next.ConsecutiveErrorCount = prev.ConsecutiveErrorCount
		}
		drift := now.Add(deps.DriftBackoff)
		next.DisabledUntil = &drift
		out.Status = SourceStatusSchemaDrift
		out.SchemaDrift = true
		out.ConsecutiveErrors = next.ConsecutiveErrorCount
		out.DisabledUntil = &drift
	default:
		next.LastErrorAt = &now
		next.LastError = pollErr.Error()
		if prev != nil {
			next.ConsecutiveErrorCount = prev.ConsecutiveErrorCount + 1
		} else {
			next.ConsecutiveErrorCount = 1
		}
		if next.ConsecutiveErrorCount >= deps.ErrorThreshold {
			until := now.Add(deps.ErrorBackoff)
			next.DisabledUntil = &until
			next.Status = SourceStatusDisabledUntil
			out.Status = SourceStatusDisabledUntil
			out.DisabledUntil = &until
		} else {
			next.Status = SourceStatusError
			out.Status = SourceStatusError
		}
		out.ConsecutiveErrors = next.ConsecutiveErrorCount
	}

	if upErr := repo.UpsertSourceState(ctx, next); upErr != nil {
		if pollErr != nil {
			return out, fmt.Errorf("upsert source state after poll error %v: %w", pollErr, upErr)
		}
		return out, fmt.Errorf("upsert source state: %w", upErr)
	}
	return out, pollErr
}

func isSchemaDriftError(err error) bool {
	if err == nil {
		return false
	}
	var instDrift *instrument.SchemaDriftError
	if errors.As(err, &instDrift) {
		return true
	}
	var annDrift *announcement.SchemaDriftError
	return errors.As(err, &annDrift)
}
