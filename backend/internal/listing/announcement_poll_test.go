package listing

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/listing/announcement"

	"github.com/DATA-DOG/go-sqlmock"
)

func newBybitPerpAnnouncement() announcement.ParsedAnnouncement {
	published := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	return announcement.ParsedAnnouncement{
		Platform:        "bybit",
		AnnouncementID:  "ann-001",
		Title:           "ABC Perpetual Contract Listing Notice",
		URL:             "https://announcements.bybit.com/article/ann-001",
		Category:        "perpetual",
		Language:        "en",
		ParseConfidence: announcement.ConfidenceHigh,
		RawPayloadJSON:  json.RawMessage(`{"id":"ann-001","title":"ABC Perpetual Contract Listing Notice"}`),
		RawPayloadHash:  "hash-ann-001",
		PublishedAt:     &published,
		Symbols: []announcement.ParsedAnnouncementSymbol{
			{
				CanonicalSymbol: "ABC",
				MarketSurface:   "perp",
				InstrumentKind:  "canonical",
				SignalSubtype:   announcement.SubtypePerpListing,
				ListingTimeTS:   &published,
			},
		},
	}
}

func newBybitSpotIrrelevant() announcement.ParsedAnnouncement {
	published := time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)
	return announcement.ParsedAnnouncement{
		Platform:        "bybit",
		AnnouncementID:  "ann-spot-XYZ",
		Title:           "XYZ Spot Trading Pair Launch",
		ParseConfidence: announcement.ConfidenceAuditOnly,
		RawPayloadJSON:  json.RawMessage(`{"id":"ann-spot-XYZ"}`),
		RawPayloadHash:  "hash-spot",
		PublishedAt:     &published,
		// No Symbols on irrelevant announcements — classifyTitle filtered.
	}
}

// TestRunAnnouncementPollColdStartWritesParentsNoSignals asserts the
// cold-start contract for Phase 1 §AnnouncementPoll: when no rows
// exist for the platform, parent announcements are persisted (so
// the next tick recognises them as already-seen) but NO signals fire.
// Without this guard the first poll after a fresh deploy would
// re-emit every historical announcement as a new perp candidate.
func TestRunAnnouncementPollColdStartWritesParentsNoSignals(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 30, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_announcement")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	src := AnnouncementSource{
		Platform:  "bybit",
		SourceURL: "https://announcements.bybit.com/api/list",
		SourceKey: "listing/announcement/bybit",
		Fetch: func(ctx context.Context) ([]json.RawMessage, error) {
			return []json.RawMessage{json.RawMessage(`{}`)}, nil
		},
		Parse: func(raw json.RawMessage) (announcement.ParsedAnnouncement, error) {
			return newBybitPerpAnnouncement(), nil
		},
	}
	res, err := RunAnnouncementPoll(context.Background(), repo, src, AnnouncementPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunAnnouncementPoll err = %v", err)
	}
	if !res.Baseline {
		t.Errorf("Baseline = false, want true on cold start")
	}
	if res.SignalsEmitted != 0 {
		t.Errorf("SignalsEmitted = %d, want 0 on cold start", res.SignalsEmitted)
	}
	if res.Announcements != 1 {
		t.Errorf("Announcements = %d, want 1", res.Announcements)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunAnnouncementPollWarmEmitsSignalForUnseenPerpAnnouncement
// drives the steady-state path: a perp-listing announcement that
// the database has not yet seen produces one parent row, one symbol
// child row, and one announcement_listing signal.
func TestRunAnnouncementPollWarmEmitsSignalForUnseenPerpAnnouncement(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 30, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	// HasAnnouncementForExternalID: not yet present → emit signal path.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit", "ann-001").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_announcement")).
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_announcement_symbol")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(101, 1))

	src := AnnouncementSource{
		Platform:  "bybit",
		SourceURL: "https://announcements.bybit.com/api/list",
		SourceKey: "listing/announcement/bybit",
		Fetch: func(ctx context.Context) ([]json.RawMessage, error) {
			return []json.RawMessage{json.RawMessage(`{}`)}, nil
		},
		Parse: func(raw json.RawMessage) (announcement.ParsedAnnouncement, error) {
			return newBybitPerpAnnouncement(), nil
		},
	}
	res, err := RunAnnouncementPoll(context.Background(), repo, src, AnnouncementPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunAnnouncementPoll err = %v", err)
	}
	if res.Baseline {
		t.Errorf("Baseline = true, want false in warm path")
	}
	if res.SignalsEmitted != 1 {
		t.Errorf("SignalsEmitted = %d, want 1", res.SignalsEmitted)
	}
	if res.Announcements != 1 {
		t.Errorf("Announcements = %d, want 1", res.Announcements)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunAnnouncementPollWarmSkipsAlreadySeenAnnouncements ensures a
// re-fetch of the same announcement_id does NOT re-emit the signal:
// the parent row is still upserted (idempotent), but the symbol/signal
// fan-out is gated on HasAnnouncementForExternalID=false. This is
// the back-stop for the case where the CMS API returns the last N
// announcements on every poll regardless of pagination cursor.
func TestRunAnnouncementPollWarmSkipsAlreadySeenAnnouncements(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 30, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit", "ann-001").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_announcement")).
		WillReturnResult(sqlmock.NewResult(7, 2)) // ON DUPLICATE KEY UPDATE -> 2 rows affected

	src := AnnouncementSource{
		Platform: "bybit",
		Fetch: func(ctx context.Context) ([]json.RawMessage, error) {
			return []json.RawMessage{json.RawMessage(`{}`)}, nil
		},
		Parse: func(raw json.RawMessage) (announcement.ParsedAnnouncement, error) {
			return newBybitPerpAnnouncement(), nil
		},
	}
	res, err := RunAnnouncementPoll(context.Background(), repo, src, AnnouncementPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunAnnouncementPoll err = %v", err)
	}
	if res.SignalsEmitted != 0 {
		t.Errorf("SignalsEmitted = %d, want 0 when announcement already known", res.SignalsEmitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunAnnouncementPollSkipsIrrelevantAnnouncements: a spot or
// pre-market announcement (classifyTitle yields no symbols) only
// produces the parent row for audit; no signals, no child rows.
func TestRunAnnouncementPollSkipsIrrelevantAnnouncements(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 30, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit", "ann-spot-XYZ").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_announcement")).
		WillReturnResult(sqlmock.NewResult(8, 1))

	src := AnnouncementSource{
		Platform: "bybit",
		Fetch: func(ctx context.Context) ([]json.RawMessage, error) {
			return []json.RawMessage{json.RawMessage(`{}`)}, nil
		},
		Parse: func(raw json.RawMessage) (announcement.ParsedAnnouncement, error) {
			return newBybitSpotIrrelevant(), nil
		},
	}
	res, err := RunAnnouncementPoll(context.Background(), repo, src, AnnouncementPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunAnnouncementPoll err = %v", err)
	}
	if res.SignalsEmitted != 0 {
		t.Errorf("SignalsEmitted = %d, want 0 for spot/pre-market announcement", res.SignalsEmitted)
	}
	if res.Announcements != 1 {
		t.Errorf("Announcements = %d, want 1 (parent still persisted for audit)", res.Announcements)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunAnnouncementPollPropagatesParseError verifies that a
// SchemaDriftError surfaces up the call stack so the source-health
// wrapper can classify it correctly. The driver writes nothing in
// this case because we have no parsed view to persist.
func TestRunAnnouncementPollPropagatesParseError(t *testing.T) {
	now := time.Date(2026, 5, 29, 18, 30, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	driftErr := &announcement.SchemaDriftError{Platform: "bybit", Message: "missing id"}
	src := AnnouncementSource{
		Platform: "bybit",
		Fetch: func(ctx context.Context) ([]json.RawMessage, error) {
			return []json.RawMessage{json.RawMessage(`{}`)}, nil
		},
		Parse: func(raw json.RawMessage) (announcement.ParsedAnnouncement, error) {
			return announcement.ParsedAnnouncement{}, driftErr
		},
	}
	_, err := RunAnnouncementPoll(context.Background(), repo, src, AnnouncementPollDeps{
		Now: func() time.Time { return now },
	})
	if !errors.Is(err, driftErr) {
		t.Fatalf("err = %v, want wraps driftErr", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
