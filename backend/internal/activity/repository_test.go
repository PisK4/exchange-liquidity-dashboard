package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newActivityRepoWithMock(t *testing.T, now time.Time) (*Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock new: %v", err)
	}
	repo := NewRepository(db)
	repo.now = func() time.Time { return now }
	return repo, mock, func() { _ = db.Close() }
}

func TestRepositoryUpsertRawEvidenceTruncatesAndHashesPayload(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_activity_raw_evidence")).
		WillReturnResult(sqlmock.NewResult(11, 1))
	id, err := repo.UpsertRawEvidence(context.Background(), RawEvidence{
		SourceKey:    "binance|cms_article_list|http_direct",
		Platform:     "binance",
		SourceGroup:  "cms_article_list",
		SourceURL:    "https://example.test",
		FetchMode:    "http_direct",
		PayloadText:  `{"title":"abc"}`,
		FetchedAt:    now,
		ResponseMeta: json.RawMessage(`{"status":200}`),
	})
	if err != nil {
		t.Fatalf("UpsertRawEvidence err=%v", err)
	}
	if id != 11 {
		t.Fatalf("id=%d want 11", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryReviewAndDecisionTransitions(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_activity_event")).
		WithArgs(ReviewApproved, DecisionDifferentiate, false, "ops_alice", sql.NullString{String: "confirmed", Valid: true}, int64(42), 3, "hash-v3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.RecordDecision(context.Background(), DecisionRecord{
		EventID:      42,
		EventVersion: 3,
		ContentHash:  "hash-v3",
		Action:       DecisionDifferentiate,
		Reviewer:     "ops_alice",
		Reason:       "confirmed",
	}); err != nil {
		t.Fatalf("RecordDecision err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryRedriveOnlyAllowsDisabledOrFailed(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_activity_delivery_outbox")).
		WithArgs(DeliveryStatusRedrivePending, "manual redrive", now, int64(9), DeliveryStatusDisabledNoWebhook, DeliveryStatusDisabledMissingSecret, DeliveryStatusFailed).
		WillReturnResult(sqlmock.NewResult(0, 1))
	ok, err := repo.RedriveDelivery(context.Background(), 9, "manual redrive")
	if err != nil {
		t.Fatalf("RedriveDelivery err=%v", err)
	}
	if !ok {
		t.Fatalf("RedriveDelivery ok=false want true")
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_activity_delivery_outbox")).
		WithArgs(DeliveryStatusRedrivePending, "not allowed", now, int64(10), DeliveryStatusDisabledNoWebhook, DeliveryStatusDisabledMissingSecret, DeliveryStatusFailed).
		WillReturnResult(sqlmock.NewResult(0, 0))
	ok, err = repo.RedriveDelivery(context.Background(), 10, "not allowed")
	if err != nil {
		t.Fatalf("RedriveDelivery second err=%v", err)
	}
	if ok {
		t.Fatalf("RedriveDelivery ok=true for disallowed status")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
