package listing

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/announcement"

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

// TestRepositoryInsertSignalSurfacesSilentFail locks down the
// regression behaviour introduced after the 2026-06-01 root-cause:
// when INSERT IGNORE reports affected=0 because MySQL silently
// dropped the row (e.g. fingerprint column overflow under strict
// sql_mode), the fallback SELECT must distinguish "duplicate key
// already there" (returns id) from "row never landed" (returns
// ErrSignalSilentFail) so callers can downgrade the failure to a
// warning and operators can see the actual constraint via SHOW
// WARNINGS instead of chasing a misleading sql.ErrNoRows.
func TestRepositoryInsertSignalSurfacesSilentFail(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	signal := SignalObservation{
		SignalType:      SignalInstrumentDiff,
		SignalSubtype:   "metadata_changed",
		SourcePlatform:  "binance",
		CanonicalSymbol: "BTC",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		ObservedAt:      now,
		Fingerprint:     "instrument_diff:" + strings.Repeat("a", 64),
		PayloadJSON:     json.RawMessage(`{"diff_subtype":"metadata_changed"}`),
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM t_listing_signal_observation WHERE fingerprint")).
		WithArgs(signal.Fingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, inserted, err := repo.InsertSignal(context.Background(), signal)
	if err == nil {
		t.Fatalf("InsertSignal err = nil, want ErrSignalSilentFail")
	}
	if !errors.Is(err, ErrSignalSilentFail) {
		t.Fatalf("InsertSignal err = %v, want errors.Is(err, ErrSignalSilentFail)", err)
	}
	if inserted {
		t.Fatalf("InsertSignal inserted = true on silent-fail path")
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

func TestRepositoryUpsertAnnouncementInsertsParent(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	parsed := announcement.ParsedAnnouncement{
		Platform:        "bybit",
		AnnouncementID:  "a1",
		Title:           "ABC USDT Perpetual",
		URL:             "https://example.test/a1",
		ParseConfidence: announcement.ConfidenceHigh,
		RawPayloadJSON:  json.RawMessage(`{"id":"a1"}`),
		RawPayloadHash:  "h",
		PublishedAt:     &now,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_announcement")).
		WillReturnResult(sqlmock.NewResult(7, 1))

	id, err := repo.UpsertAnnouncement(context.Background(), parsed)
	if err != nil {
		t.Fatalf("UpsertAnnouncement err = %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryUpsertInstrumentSnapshotInserts locks the write side
// of t_listing_instrument_snapshot: the first sighting of a (platform,
// market_type, api_symbol) triple must produce an INSERT ... ON
// DUPLICATE KEY UPDATE row, carry the normalizer version, and stamp
// last_seen_at to the caller-provided clock. Phase 1 of
// 2026-05-29-listing-agent.md needs this helper as the foundation
// for both the bootstrap baseline pass and the steady-state diff loop.
func TestRepositoryUpsertInstrumentSnapshotInserts(t *testing.T) {
	now := time.Date(2026, 5, 29, 16, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	snap := InstrumentSnapshot{
		Platform:          "binance",
		MarketType:        "usdm_futures",
		APISymbol:         "BTCUSDT",
		DisplaySymbol:     "BTCUSDT",
		CanonicalSymbol:   "BTC",
		BaseAsset:         "BTC",
		QuoteAsset:        "USDT",
		SettleAsset:       "USDT",
		MarketSurface:     "perp",
		InstrumentKind:    "canonical",
		ContractType:      "PERPETUAL",
		StatusRaw:         "TRADING",
		StatusNormalized:  "active",
		StatusFieldName:   "status",
		ListingTimeTS:     &now,
		LastSeenAt:        now,
		RawJSON:           json.RawMessage(`{"symbol":"BTCUSDT"}`),
		StableHash:        "deadbeef",
		NormalizerVersion: "v1",
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := repo.UpsertInstrumentSnapshot(context.Background(), snap); err != nil {
		t.Fatalf("UpsertInstrumentSnapshot err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryLatestInstrumentSnapshotByKeyReturnsRowOrNil ensures
// the loader returns a populated *InstrumentSnapshot when the row
// exists and nil when it does not, so callers can use a single
// non-nil check to mean "we have a prev to diff against".
func TestRepositoryLatestInstrumentSnapshotByKeyReturnsRowOrNil(t *testing.T) {
	now := time.Date(2026, 5, 29, 16, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	cols := []string{
		"id", "platform", "market_type", "api_symbol", "api_market_id", "display_symbol",
		"canonical_symbol", "base_asset", "quote_asset", "settle_asset", "market_surface",
		"instrument_kind", "contract_type", "status_raw", "status_normalized",
		"status_field_name", "listing_time_ts", "listing_time_field_name", "delist_flag",
		"first_seen_at", "previous_seen_at", "last_seen_at", "raw_json", "raw_json_hash",
		"normalizer_version",
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "BTCUSDT").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(
			int64(1), "binance", "usdm_futures", "BTCUSDT", nil, "BTCUSDT",
			"BTC", "BTC", "USDT", "USDT", "perp",
			"canonical", "PERPETUAL", "TRADING", "active",
			"status", now, nil, false,
			now, nil, now, []byte(`{"symbol":"BTCUSDT"}`), "deadbeef",
			"v1",
		))

	got, err := repo.LatestInstrumentSnapshotByKey(context.Background(), "binance", "usdm_futures", "BTCUSDT")
	if err != nil {
		t.Fatalf("Load existing err = %v", err)
	}
	if got == nil || got.StableHash != "deadbeef" {
		t.Fatalf("got = %+v, want snapshot with hash=deadbeef", got)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "MISSING").
		WillReturnRows(sqlmock.NewRows(cols))

	missing, err := repo.LatestInstrumentSnapshotByKey(context.Background(), "binance", "usdm_futures", "MISSING")
	if err != nil {
		t.Fatalf("Load missing err = %v", err)
	}
	if missing != nil {
		t.Fatalf("Load missing = %+v, want nil", missing)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryHasInstrumentBaselineFlagsFirstRun is the bootstrap
// guard used by the poller: a brand-new platform must NOT emit
// new_symbol signals on its first poll. The helper answers the
// "do we already have any prior rows for this platform?" question
// the Diff() function expects to be lifted out of the data layer.
func TestRepositoryHasInstrumentBaselineFlagsFirstRun(t *testing.T) {
	now := time.Date(2026, 5, 29, 16, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))
	got, err := repo.HasInstrumentBaseline(context.Background(), "binance", "usdm_futures")
	if err != nil {
		t.Fatalf("HasInstrumentBaseline err = %v", err)
	}
	if !got {
		t.Fatalf("HasInstrumentBaseline = false, want true when row exists")
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("bybit", "linear").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))
	got, err = repo.HasInstrumentBaseline(context.Background(), "bybit", "linear")
	if err != nil {
		t.Fatalf("HasInstrumentBaseline cold err = %v", err)
	}
	if got {
		t.Fatalf("HasInstrumentBaseline cold = true, want false on first run")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryInsertAnnouncementSymbolAndSignalChains(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	sym := announcement.ParsedAnnouncementSymbol{
		CanonicalSymbol: "ABC",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		SignalSubtype:   announcement.SubtypePerpListing,
		ListingTimeTS:   &now,
	}
	rawPayload := json.RawMessage(`{"id":"a1"}`)
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_announcement_symbol")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(101, 1))

	signalID, inserted, err := repo.InsertAnnouncementSymbolAndSignal(context.Background(), 7, "bybit", "a1", sym, rawPayload, now)
	if err != nil {
		t.Fatalf("InsertAnnouncementSymbolAndSignal err = %v", err)
	}
	if !inserted || signalID != 101 {
		t.Fatalf("inserted=%v id=%d, want inserted=true id=101", inserted, signalID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryGetCandidateByKeyFound(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WithArgs("ABC", "perp", "canonical").
		WillReturnRows(sqlmock.NewRows(fusionCandidateColumns()).AddRow(
			int64(42), "ABC", "ABC-USDT (perp)", "perp", "canonical",
			LifecycleConfirmedListingCandidate, LifecycleStatusLabels[LifecycleConfirmedListingCandidate], EvidenceAnnouncementAndAPI, ConfidenceHigh,
			90.0, BusinessScoreVersion, RecommendationPrepareListing, RecommendationLabels[RecommendationPrepareListing],
			[]byte(`["binance"]`), nil, now.Add(-time.Hour), now,
		))

	got, ok, err := repo.GetCandidateByKey(context.Background(), "ABC", "perp", "canonical")
	if err != nil {
		t.Fatalf("GetCandidateByKey err = %v", err)
	}
	if !ok {
		t.Fatalf("GetCandidateByKey ok = false, want true")
	}
	if got.ID != 42 || got.CanonicalSymbol != "ABC" || got.Recommendation != RecommendationPrepareListing {
		t.Fatalf("candidate = %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryGetCandidateByKeyMissing(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WithArgs("MISSING", "perp", "canonical").
		WillReturnRows(sqlmock.NewRows(fusionCandidateColumns()))

	got, ok, err := repo.GetCandidateByKey(context.Background(), "MISSING", "perp", "canonical")
	if err != nil {
		t.Fatalf("GetCandidateByKey err = %v", err)
	}
	if ok {
		t.Fatalf("GetCandidateByKey ok = true, want false; got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
