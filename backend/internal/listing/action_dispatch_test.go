package listing

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestRepositoryInsertActionDispatchInsertsAuditRow verifies the
// dispatch audit row is written verbatim — the producer signals
// which downstream channel (listing-ops / mm) the action should be
// fanned out to via target_channel.
func TestRepositoryInsertActionDispatchInsertsAuditRow(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	row := ActionDispatchRecord{
		CandidateID:   7,
		DecisionID:    501,
		DispatchType:  DispatchTypeListingOps,
		TargetChannel: DispatchChannelLarkListingOps,
		Status:        DispatchStatusPending,
		PayloadJSON:   json.RawMessage(`{"action":"prepare_listing"}`),
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_action_dispatch")).
		WillReturnResult(sqlmock.NewResult(701, 1))

	id, err := repo.InsertActionDispatch(context.Background(), row)
	if err != nil {
		t.Fatalf("InsertActionDispatch err = %v", err)
	}
	if id != 701 {
		t.Errorf("id = %d, want 701", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryUpsertWatchlistMakesEntryIdempotentByCandidate
// ensures the watchlist write is keyed on candidate_id (unique key
// uk_listing_watchlist_candidate), so re-clicking 进入观察 on the
// same card refreshes the row instead of error-ing out.
func TestRepositoryUpsertWatchlistMakesEntryIdempotentByCandidate(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	row := WatchlistEntry{
		CandidateID:      7,
		CanonicalSymbol:  "ABC",
		MarketSurface:    "perp",
		InstrumentKind:   "canonical",
		WatchStatus:      WatchStatusObserving,
		SourceDecisionID: 501,
		WatchStartedAt:   now,
		PayloadJSON:      json.RawMessage(`{"recommendation":"watch"}`),
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_watchlist")).
		WillReturnResult(sqlmock.NewResult(801, 1))

	id, err := repo.UpsertWatchlist(context.Background(), row)
	if err != nil {
		t.Fatalf("UpsertWatchlist err = %v", err)
	}
	if id != 801 {
		t.Errorf("id = %d, want 801", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestDispatchDecisionActionPrepareListingWritesAuditAndNotifies
// covers the §Phase 2 happy path: 准备上线 button click produces
// (1) a t_listing_action_dispatch audit row, (2) a delivery outbox
// row notifying the listing-ops group, but does NOT create a
// watchlist entry (prepare ≠ watch).
func TestDispatchDecisionActionPrepareListingWritesAuditAndNotifies(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_action_dispatch")).
		WillReturnResult(sqlmock.NewResult(701, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(901, 1))

	dec := DecisionRecord{ID: 501, CandidateID: 7, Action: DecisionActionPrepareListing, OperatorOpenID: "ou_pis", CallbackTS: now}
	cand := Candidate{ID: 7, CanonicalSymbol: "ABC", DisplaySymbol: "ABC-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical"}
	res, err := DispatchDecisionAction(context.Background(), repo, dec, cand, DispatchDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("DispatchDecisionAction err = %v", err)
	}
	if res.DispatchID != 701 {
		t.Errorf("DispatchID = %d", res.DispatchID)
	}
	if res.OutboxRows != 1 {
		t.Errorf("OutboxRows = %d, want 1", res.OutboxRows)
	}
	if res.WatchlistID != 0 {
		t.Errorf("WatchlistID = %d, want 0 (prepare must not write watchlist)", res.WatchlistID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestDispatchDecisionActionEnterWatchlistWritesWatchlistRow covers
// the 进入观察 path: dispatch audit row + watchlist row, no
// downstream notification (watch is a self-service silent action).
func TestDispatchDecisionActionEnterWatchlistWritesWatchlistRow(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_action_dispatch")).
		WillReturnResult(sqlmock.NewResult(702, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_watchlist")).
		WillReturnResult(sqlmock.NewResult(801, 1))

	dec := DecisionRecord{ID: 502, CandidateID: 8, Action: DecisionActionEnterWatchlist, OperatorOpenID: "ou_pis", CallbackTS: now}
	cand := Candidate{ID: 8, CanonicalSymbol: "DEF", DisplaySymbol: "DEF-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical", Recommendation: RecommendationWatch}
	res, err := DispatchDecisionAction(context.Background(), repo, dec, cand, DispatchDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("DispatchDecisionAction err = %v", err)
	}
	if res.WatchlistID != 801 {
		t.Errorf("WatchlistID = %d, want 801", res.WatchlistID)
	}
	if res.OutboxRows != 0 {
		t.Errorf("OutboxRows = %d, want 0 (watch is silent)", res.OutboxRows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestDispatchDecisionActionIgnoreOnlyWritesAudit covers the 忽略
// path: dispatch audit row carrying the reason but NO outbox / NO
// watchlist write. The cooldown gate in the producer is the only
// thing that needs the row, so this stays minimal.
func TestDispatchDecisionActionIgnoreOnlyWritesAudit(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_action_dispatch")).
		WillReturnResult(sqlmock.NewResult(703, 1))

	dec := DecisionRecord{ID: 503, CandidateID: 9, Action: DecisionActionIgnore, OperatorOpenID: "ou_pis", Reason: "low liquidity", CallbackTS: now}
	cand := Candidate{ID: 9, CanonicalSymbol: "GHI", DisplaySymbol: "GHI-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical"}
	res, err := DispatchDecisionAction(context.Background(), repo, dec, cand, DispatchDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("DispatchDecisionAction err = %v", err)
	}
	if res.DispatchID != 703 {
		t.Errorf("DispatchID = %d", res.DispatchID)
	}
	if res.OutboxRows != 0 || res.WatchlistID != 0 {
		t.Errorf("res = %+v, want no outbox / watchlist for ignore", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestDispatchDecisionActionContactMMNotifiesMMChannel covers the
// 联系MM path: audit row + outbox notification on the MM channel,
// no watchlist.
func TestDispatchDecisionActionContactMMNotifiesMMChannel(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_action_dispatch")).
		WillReturnResult(sqlmock.NewResult(704, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(902, 1))

	dec := DecisionRecord{ID: 504, CandidateID: 10, Action: DecisionActionContactMM, OperatorOpenID: "ou_alice", CallbackTS: now}
	cand := Candidate{ID: 10, CanonicalSymbol: "JKL", DisplaySymbol: "JKL-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical"}
	res, err := DispatchDecisionAction(context.Background(), repo, dec, cand, DispatchDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("DispatchDecisionAction err = %v", err)
	}
	if res.OutboxRows != 1 {
		t.Errorf("OutboxRows = %d", res.OutboxRows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
