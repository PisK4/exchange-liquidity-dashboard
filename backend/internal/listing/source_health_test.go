package listing

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/announcement"
	"edgex-ops-intelligence/backend/internal/listing/instrument"

	"github.com/DATA-DOG/go-sqlmock"
)

// sourceStateCols mirrors the column order LoadSourceState reads. It
// stays alongside the test so a future migration change forces both
// the loader and these fixtures to update in lock-step.
var sourceStateCols = []string{
	"source_key", "source_type", "platform", "status",
	"last_success_at", "last_error_at", "consecutive_error_count", "schema_drift_count",
	"disabled_until", "last_error", "updated_at",
}

func defaultHealthDeps(now time.Time) PollHealthDeps {
	return PollHealthDeps{
		Now:            func() time.Time { return now },
		ErrorThreshold: 5,
		ErrorBackoff:   15 * time.Minute,
		DriftBackoff:   30 * time.Minute,
	}
}

// TestPollWithSourceHealthFirstRunSuccessUpsertsOK locks the bootstrap
// path: a brand-new source_key MUST result in a single OK row, with
// last_success_at = now and consecutive_error_count = 0. Without this
// guard, operators would not see the source in t_listing_source_state
// until the first error happened.
func TestPollWithSourceHealthFirstRunSuccessUpsertsOK(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows(sourceStateCols))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/instrument/binance/usdm_futures",
		SourceType: SourceTypeInstrument,
		Platform:   "binance",
	}, defaultHealthDeps(now), func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatalf("PollWithSourceHealth err = %v", err)
	}
	if out.Status != SourceStatusOK || out.ConsecutiveErrors != 0 || out.Skipped {
		t.Errorf("out = %+v, want Status=ok ConsecutiveErrors=0 Skipped=false", out)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPollWithSourceHealthSuccessResetsConsecutiveErrors makes sure
// a recovery clears the running error counter so the wrapper does
// not stay stuck in error mode after a single bad call.
func TestPollWithSourceHealthSuccessResetsConsecutiveErrors(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	prevErrAt := now.Add(-5 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows(sourceStateCols).AddRow(
			"listing/instrument/binance/usdm_futures", SourceTypeInstrument, "binance", SourceStatusError,
			nil, prevErrAt, 3, 0, nil, "timeout", prevErrAt,
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(0, 2))

	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/instrument/binance/usdm_futures",
		SourceType: SourceTypeInstrument,
		Platform:   "binance",
	}, defaultHealthDeps(now), func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatalf("PollWithSourceHealth err = %v", err)
	}
	if out.Status != SourceStatusOK || out.ConsecutiveErrors != 0 {
		t.Errorf("out = %+v, want Status=ok ConsecutiveErrors=0", out)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPollWithSourceHealthErrorIncrementsConsecutiveErrors verifies
// the running counter increments and the error message is captured
// so operators can read it back through ListSourceHealth.
func TestPollWithSourceHealthErrorIncrementsConsecutiveErrors(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows(sourceStateCols).AddRow(
			"listing/instrument/binance/usdm_futures", SourceTypeInstrument, "binance", SourceStatusError,
			nil, now.Add(-5*time.Minute), 2, 0, nil, "timeout", now.Add(-5*time.Minute),
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(0, 2))

	pollErr := errors.New("dial tcp: i/o timeout")
	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/instrument/binance/usdm_futures",
		SourceType: SourceTypeInstrument,
		Platform:   "binance",
	}, defaultHealthDeps(now), func(ctx context.Context) error { return pollErr })
	if !errors.Is(err, pollErr) {
		t.Fatalf("err = %v, want wraps pollErr", err)
	}
	if out.Status != SourceStatusError || out.ConsecutiveErrors != 3 {
		t.Errorf("out = %+v, want Status=error ConsecutiveErrors=3", out)
	}
	if out.DisabledUntil != nil {
		t.Errorf("DisabledUntil = %v, want nil (below threshold)", out.DisabledUntil)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPollWithSourceHealthEscalatesToDisabledUntil verifies the
// fail-closed escalation: once consecutive_error_count reaches the
// threshold, the wrapper writes disabled_until=now+ErrorBackoff so
// the next tick skips the source until it heals.
func TestPollWithSourceHealthEscalatesToDisabledUntil(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows(sourceStateCols).AddRow(
			"listing/instrument/binance/usdm_futures", SourceTypeInstrument, "binance", SourceStatusError,
			nil, now.Add(-5*time.Minute), 4, 0, nil, "timeout", now.Add(-5*time.Minute),
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(0, 2))

	pollErr := errors.New("dial tcp: i/o timeout")
	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/instrument/binance/usdm_futures",
		SourceType: SourceTypeInstrument,
		Platform:   "binance",
	}, defaultHealthDeps(now), func(ctx context.Context) error { return pollErr })
	if !errors.Is(err, pollErr) {
		t.Fatalf("err = %v, want wraps pollErr", err)
	}
	if out.Status != SourceStatusDisabledUntil || out.ConsecutiveErrors != 5 {
		t.Errorf("out = %+v, want Status=disabled_until ConsecutiveErrors=5", out)
	}
	if out.DisabledUntil == nil || !out.DisabledUntil.Equal(now.Add(15*time.Minute)) {
		t.Errorf("DisabledUntil = %v, want now+15m", out.DisabledUntil)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPollWithSourceHealthInstrumentSchemaDriftFlagsCounter ensures
// an *instrument.SchemaDriftError is treated as the schema_drift
// status (not the generic error escalation) and the lifetime counter
// is incremented separately from consecutive_error_count.
func TestPollWithSourceHealthInstrumentSchemaDriftFlagsCounter(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows(sourceStateCols))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	driftErr := &instrument.SchemaDriftError{Platform: "binance", Message: "unexpected field"}
	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/instrument/binance/usdm_futures",
		SourceType: SourceTypeInstrument,
		Platform:   "binance",
	}, defaultHealthDeps(now), func(ctx context.Context) error { return driftErr })
	if !errors.Is(err, driftErr) {
		t.Fatalf("err = %v, want wraps driftErr", err)
	}
	if out.Status != SourceStatusSchemaDrift || !out.SchemaDrift {
		t.Errorf("out = %+v, want Status=schema_drift SchemaDrift=true", out)
	}
	if out.DisabledUntil == nil || !out.DisabledUntil.Equal(now.Add(30*time.Minute)) {
		t.Errorf("DisabledUntil = %v, want now+30m on schema drift", out.DisabledUntil)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPollWithSourceHealthAnnouncementSchemaDriftFlagsCounter covers
// the symmetric case for announcement parsers (different package,
// same contract). Without both branches the wrapper would silently
// classify announcement parser failures as generic errors and the
// schema_drift_count operator dashboard would never light up.
func TestPollWithSourceHealthAnnouncementSchemaDriftFlagsCounter(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/announcement/bybit").
		WillReturnRows(sqlmock.NewRows(sourceStateCols))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	driftErr := &announcement.SchemaDriftError{Platform: "bybit", Message: "missing list[]"}
	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/announcement/bybit",
		SourceType: SourceTypeAnnouncement,
		Platform:   "bybit",
	}, defaultHealthDeps(now), func(ctx context.Context) error { return driftErr })
	if !errors.Is(err, driftErr) {
		t.Fatalf("err = %v, want wraps driftErr", err)
	}
	if out.Status != SourceStatusSchemaDrift || !out.SchemaDrift {
		t.Errorf("out = %+v, want Status=schema_drift SchemaDrift=true", out)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestPollWithSourceHealthSkipsWhenDisabledUntilInFuture asserts the
// fail-closed contract from the other direction: when the wrapper
// finds disabled_until still in the future, the poll callback MUST
// NOT run and no further DB writes happen.
func TestPollWithSourceHealthSkipsWhenDisabledUntilInFuture(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows(sourceStateCols).AddRow(
			"listing/instrument/binance/usdm_futures", SourceTypeInstrument, "binance", SourceStatusDisabledUntil,
			nil, now.Add(-2*time.Minute), 5, 0, future, "throttled", now.Add(-2*time.Minute),
		))

	pollCalled := 0
	out, err := PollWithSourceHealth(context.Background(), repo, SourceHealthKey{
		SourceKey:  "listing/instrument/binance/usdm_futures",
		SourceType: SourceTypeInstrument,
		Platform:   "binance",
	}, defaultHealthDeps(now), func(ctx context.Context) error {
		pollCalled++
		return nil
	})
	if err != nil {
		t.Fatalf("PollWithSourceHealth err = %v, want nil (skip is not an error)", err)
	}
	if !out.Skipped || out.Status != SourceStatusDisabledUntil {
		t.Errorf("out = %+v, want Skipped=true Status=disabled_until", out)
	}
	if pollCalled != 0 {
		t.Errorf("poll callback ran %d times, want 0 when disabled_until in future", pollCalled)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
