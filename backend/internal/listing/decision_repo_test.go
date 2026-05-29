package listing

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestRepositoryInsertDecisionReturnsIDAndInsertedFlag covers the
// fresh-insert path: a new row lands and the helper returns
// inserted=true alongside the auto-increment id.
func TestRepositoryInsertDecisionReturnsIDAndInsertedFlag(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 1, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	dec := DecisionRecord{
		CandidateID:         7,
		CardID:              "card-001",
		MessageID:           "msg-001",
		OperatorOpenID:      "ou_pis",
		Action:              DecisionActionPrepareListing,
		SignatureVerified:   true,
		CallbackPayloadJSON: json.RawMessage(`{"action":"prepare_listing"}`),
		CallbackTS:          now,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_decision")).
		WillReturnResult(sqlmock.NewResult(501, 1))

	id, inserted, err := repo.InsertDecision(context.Background(), dec)
	if err != nil {
		t.Fatalf("InsertDecision err = %v", err)
	}
	if !inserted || id != 501 {
		t.Errorf("inserted=%v id=%d, want inserted=true id=501", inserted, id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryInsertDecisionIsIdempotentOnUniqueKey covers the
// double-click safety: a second insert with the same
// (candidate_id, operator_open_id, action, callback_ts) returns
// inserted=false plus the original id, which the API layer maps to
// a 200 OK with status=already_recorded.
func TestRepositoryInsertDecisionIsIdempotentOnUniqueKey(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 1, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	dec := DecisionRecord{
		CandidateID:         7,
		OperatorOpenID:      "ou_pis",
		Action:              DecisionActionPrepareListing,
		SignatureVerified:   true,
		CallbackPayloadJSON: json.RawMessage(`{}`),
		CallbackTS:          now,
	}
	// First insert: hit (1 row affected).
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_decision")).
		WillReturnResult(sqlmock.NewResult(501, 1))
	if _, inserted, err := repo.InsertDecision(context.Background(), dec); err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}
	// Second insert: 0 rows affected → must SELECT existing id.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_decision")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM t_listing_decision WHERE candidate_id")).
		WithArgs(int64(7), "ou_pis", DecisionActionPrepareListing, now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(501)))

	id, inserted, err := repo.InsertDecision(context.Background(), dec)
	if err != nil {
		t.Fatalf("second InsertDecision err = %v", err)
	}
	if inserted || id != 501 {
		t.Errorf("inserted=%v id=%d, want inserted=false id=501", inserted, id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
