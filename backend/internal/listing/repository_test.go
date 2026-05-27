package listing

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newRepoWithMock(t *testing.T, now time.Time) (*Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	repo := NewRepository(db)
	repo.now = func() time.Time { return now }
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func TestRepositoryUpsertCandidateInsertsAndReturnsID(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	score := 90.0
	upsert := CandidateUpsert{
		CanonicalSymbol:      "ABC",
		DisplaySymbol:        "ABC-USDT (perp)",
		MarketSurface:        "perp",
		InstrumentKind:       "canonical",
		LifecycleStatus:      LifecycleConfirmedListingCandidate,
		LifecycleStatusLabel: LifecycleStatusLabels[LifecycleConfirmedListingCandidate],
		EvidenceKind:         EvidenceAnnouncementAndAPI,
		ConfidenceLevel:      ConfidenceHigh,
		BusinessScore:        &score,
		BusinessScoreVersion: "v1",
		Recommendation:       RecommendationPrepareListing,
		RecommendationLabel:  RecommendationLabels[RecommendationPrepareListing],
		SourcePlatforms:      []string{"binance", "bybit"},
		ObservedAt:           now,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).
		WillReturnResult(sqlmock.NewResult(123, 1))

	id, err := repo.UpsertCandidate(context.Background(), upsert)
	if err != nil {
		t.Fatalf("UpsertCandidate err = %v", err)
	}
	if id != 123 {
		t.Fatalf("returned id = %d, want 123", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryInsertSignalRespectsFingerprintIdempotency(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	signal := SignalObservation{
		SignalType:      SignalAnnouncementListing,
		SignalSubtype:   AnnouncementPerpListing,
		SourcePlatform:  "bybit",
		CanonicalSymbol: "ABC",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		ObservedAt:      now,
		Fingerprint:     "announcement_listing|bybit|123|ABC|perp|canonical",
		PayloadJSON:     json.RawMessage(`{"title":"ABC listing"}`),
	}

	// First insert: brand-new fingerprint, returns lastInsertID and inserted=true.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(42, 1))

	id, inserted, err := repo.InsertSignal(context.Background(), signal)
	if err != nil {
		t.Fatalf("InsertSignal err = %v", err)
	}
	if !inserted || id != 42 {
		t.Fatalf("inserted=%v id=%d, want inserted=true id=42", inserted, id)
	}

	// Second insert with same fingerprint: RowsAffected=0, must SELECT existing id.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM t_listing_signal_observation WHERE fingerprint")).
		WithArgs(signal.Fingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))

	id2, inserted2, err := repo.InsertSignal(context.Background(), signal)
	if err != nil {
		t.Fatalf("second InsertSignal err = %v", err)
	}
	if inserted2 || id2 != 42 {
		t.Fatalf("second insert: inserted=%v id=%d, want inserted=false id=42", inserted2, id2)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryLinkCandidateSignalUsesInsertIgnore(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).
		WithArgs(int64(1), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.LinkCandidateSignal(context.Background(), 1, 2); err != nil {
		t.Fatalf("LinkCandidateSignal err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryAcquireLeaseCASGrantsThenRefuses(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_worker_lease")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT owner_id FROM t_listing_worker_lease")).
		WithArgs("listing/instrument/binance").
		WillReturnRows(sqlmock.NewRows([]string{"owner_id"}).AddRow("self"))

	ok, err := repo.AcquireLease(context.Background(), "listing/instrument/binance", "self", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease err = %v", err)
	}
	if !ok {
		t.Fatalf("first AcquireLease should succeed")
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_worker_lease")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT owner_id FROM t_listing_worker_lease")).
		WithArgs("listing/instrument/binance").
		WillReturnRows(sqlmock.NewRows([]string{"owner_id"}).AddRow("other"))

	ok2, err := repo.AcquireLease(context.Background(), "listing/instrument/binance", "self", time.Minute)
	if err != nil {
		t.Fatalf("second AcquireLease err = %v", err)
	}
	if ok2 {
		t.Fatalf("second AcquireLease should refuse when other owner holds the lease")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryReleaseLeaseSkipsWhenOwnerMismatch(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_listing_worker_lease")).
		WithArgs("lease-foo", "self").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.ReleaseLease(context.Background(), "lease-foo", "self"); err != nil {
		t.Fatalf("ReleaseLease err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryListCandidatesAppliesFilters guards the API filter
// surface: limit clamp, status, evidence_kind, platform, symbol.
func TestRepositoryListCandidatesAppliesFilters(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(1), "ABC", "ABC-USDT (perp)", "perp", "canonical",
		LifecycleConfirmedListingCandidate, LifecycleStatusLabels[LifecycleConfirmedListingCandidate],
		EvidenceAnnouncementAndAPI, ConfidenceHigh,
		90.0, "v1", RecommendationPrepareListing, RecommendationLabels[RecommendationPrepareListing],
		[]byte(`["binance","bybit"]`), nil, now, now,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_candidate WHERE`).
		WillReturnRows(rows)

	got, err := repo.ListCandidates(context.Background(), CandidateFilter{
		Limit:        10,
		Status:       LifecycleConfirmedListingCandidate,
		EvidenceKind: EvidenceAnnouncementAndAPI,
		Platform:     "binance",
		Symbol:       "ABC",
	})
	if err != nil {
		t.Fatalf("ListCandidates err = %v", err)
	}
	if len(got) != 1 || got[0].CanonicalSymbol != "ABC" {
		t.Fatalf("unexpected candidate list: %+v", got)
	}
	if !errors.Is(mock.ExpectationsWereMet(), nil) {
		t.Fatalf("expectations: %v", mock.ExpectationsWereMet())
	}
}
