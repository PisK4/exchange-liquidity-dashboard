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

func TestRepositoryUpsertActivitySourceState(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()
	httpStatus := 200

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_activity_source_state")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.UpsertActivitySourceState(context.Background(), SourceState{
		Platform:               "gate",
		SourceGroup:            "launchpool_project_list",
		SourceType:             "announcement_api",
		SourceURL:              "https://gate.example/launchpool",
		SourceKey:              "gate|launchpool_project_list|utls_proxy_json",
		FetchMode:              "utls_proxy_json",
		EvidenceQuality:        "api_json",
		Enabled:                true,
		PollIntervalSeconds:    600,
		AutoPushEnabled:        true,
		RequiresProxy:          true,
		RequiresBrowserContext: false,
		SourceStatus:           SourceStatusOK,
		LastHTTPStatus:         &httpStatus,
		LastContentHash:        "content-hash",
		SampleCount:            1,
		EventCount:             2,
		UpdatedAt:              now,
	}); err != nil {
		t.Fatalf("UpsertActivitySourceState err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryUpsertActivityEventPersistsSymbols(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_activity_event")).
		WillReturnResult(sqlmock.NewResult(77, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_activity_event_symbol WHERE event_id = ?")).
		WithArgs(int64(77)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_activity_event_symbol")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	id, inserted, err := repo.UpsertActivityEvent(context.Background(), ActivityEvent{
		RawEvidenceID:     11,
		Platform:          "binance",
		SourceGroup:       "cms_article_list",
		SourceExternalID:  "abc",
		SourceURL:         "https://binance.example/abc",
		Title:             "Binance Launchpool ABC",
		ActivityType:      "launchpool",
		ContentText:       "Stake to earn ABC",
		ContentHash:       "content-hash",
		DedupeKey:         "binance|cms_article_list|abc",
		ConfidenceScore:   0.9,
		AutoPushAllowed:   true,
		ReviewStatus:      ReviewPending,
		EventVersion:      1,
		ParserVersion:     "activity-parser-v1",
		SourceContextJSON: json.RawMessage(`{"fetch_mode":"http_direct"}`),
		TargetSymbols: []ActivityEventSymbol{{
			CanonicalSymbol: "ABC", DisplaySymbol: "ABC-USDT", MarketSurface: "perp", Role: "target",
		}},
	})
	if err != nil {
		t.Fatalf("UpsertActivityEvent err=%v", err)
	}
	if id != 77 || !inserted {
		t.Fatalf("id=%d inserted=%v", id, inserted)
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
