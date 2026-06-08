package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
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

func activitySourceStateRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "platform", "source_group", "source_type", "source_url", "source_key", "fetch_mode",
		"evidence_quality", "enabled", "poll_interval_seconds", "auto_push_enabled", "requires_proxy", "requires_browser_context",
		"requires_login", "personalized", "source_status", "last_http_status", "last_error_kind",
		"last_schema_hash", "last_content_hash", "sample_count", "event_count", "source_context_json",
		"disabled_until", "last_checked_at", "last_success_at", "updated_at",
	})
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

func TestRepositoryLoadActivitySourceState(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	checkedAt := now.Add(-10 * time.Minute)
	successAt := now.Add(-20 * time.Minute)
	disabledUntil := now.Add(30 * time.Minute)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_source_state")).
		WithArgs("gate|launchpool_project_list|utls_proxy_json").
		WillReturnRows(activitySourceStateRows().AddRow(
			int64(9), "gate", "launchpool_project_list", "announcement_api", "https://gate.example/launchpool",
			"gate|launchpool_project_list|utls_proxy_json", "utls_proxy_json", "api_json",
			1, 600, 1, 1, 0, 0, 0, SourceStatusDegraded, 429, "http_429",
			"schema-hash", "content-hash", 3, 4, []byte(`{"last_error_message":"EOF","attempt_count":3,"proxy_used":true}`), disabledUntil, checkedAt, successAt, now,
		))
	state, ok, err := repo.LoadActivitySourceState(context.Background(), "gate|launchpool_project_list|utls_proxy_json")
	if err != nil {
		t.Fatalf("LoadActivitySourceState err=%v", err)
	}
	if !ok {
		t.Fatalf("LoadActivitySourceState ok=false")
	}
	if state.SourceKey != "gate|launchpool_project_list|utls_proxy_json" || state.PollIntervalSeconds != 600 || state.LastHTTPStatus == nil || *state.LastHTTPStatus != 429 {
		t.Fatalf("state=%+v", state)
	}
	if state.DisabledUntil == nil || !state.DisabledUntil.Equal(disabledUntil) {
		t.Fatalf("DisabledUntil=%v want %s", state.DisabledUntil, disabledUntil)
	}
	if state.LastCheckedAt == nil || !state.LastCheckedAt.Equal(checkedAt) {
		t.Fatalf("LastCheckedAt=%v want %s", state.LastCheckedAt, checkedAt)
	}
	if state.LastSuccessAt == nil || !state.LastSuccessAt.Equal(successAt) {
		t.Fatalf("LastSuccessAt=%v want %s", state.LastSuccessAt, successAt)
	}
	var sourceContext map[string]any
	if err := json.Unmarshal(state.SourceContextJSON, &sourceContext); err != nil {
		t.Fatalf("SourceContextJSON err=%v json=%s", err, state.SourceContextJSON)
	}
	if sourceContext["last_error_message"] != "EOF" || sourceContext["attempt_count"] != float64(3) || sourceContext["proxy_used"] != true {
		t.Fatalf("sourceContext=%+v", sourceContext)
	}

	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_source_state")).
		WithArgs("missing|source|http_direct").
		WillReturnRows(activitySourceStateRows())
	_, ok, err = repo.LoadActivitySourceState(context.Background(), "missing|source|http_direct")
	if err != nil {
		t.Fatalf("LoadActivitySourceState missing err=%v", err)
	}
	if ok {
		t.Fatalf("LoadActivitySourceState missing ok=true")
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

func TestRepositoryListOutboxCandidateEventsSkipsAlreadyProducedAndHydratesContent(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "raw_evidence_id", "platform", "source_group", "source_external_id", "source_url", "title", "activity_type",
		"content_text", "reward_pool_text", "start_time", "end_time", "publish_time", "raw_time_text",
		"content_hash", "dedupe_key", "needs_human_review", "auto_push_allowed", "event_status",
		"review_status", "ops_decision_action", "ops_decision_stale", "event_version", "parser_version",
		"parser_warnings_json", "rich_fields_summary_json", "created_at", "updated_at",
	}).AddRow(
		int64(42), int64(9), "binance", "cms_article_detail", "abc", "https://binance.example/abc", "Binance Launchpool ABC", "launchpool",
		"Stake BNB to earn ABC", "300,000 USDT", nil, nil, now, "",
		"hash", "binance|cms_article_detail|abc", 0, 1, EventStatusActive,
		ReviewPending, "", 0, 1, "activity-parser-v1",
		[]byte(`["raw_time_unknown"]`), []byte(`{"reward":"300,000 USDT"}`), now, now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_event e")).
		WithArgs(EventStatusActive, ReviewRejected, ReviewApproved, 10).
		WillReturnRows(rows)

	events, err := repo.ListOutboxCandidateEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListOutboxCandidateEvents err=%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len=%d want 1", len(events))
	}
	ev := events[0]
	if ev.ContentText != "Stake BNB to earn ABC" || ev.RewardPoolText != "300,000 USDT" || ev.RawEvidenceID != 9 {
		t.Fatalf("event did not hydrate detail fields: %+v", ev)
	}
	if !strings.Contains(string(ev.ParserWarningsJSON), "raw_time_unknown") {
		t.Fatalf("parser warnings not hydrated: %s", ev.ParserWarningsJSON)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryListOutboxCandidateEventsBySourceFiltersPlatformAndGroup(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "raw_evidence_id", "platform", "source_group", "source_external_id", "source_url", "title", "activity_type",
		"content_text", "reward_pool_text", "start_time", "end_time", "publish_time", "raw_time_text",
		"content_hash", "dedupe_key", "needs_human_review", "auto_push_allowed", "event_status",
		"review_status", "ops_decision_action", "ops_decision_stale", "event_version", "parser_version",
		"parser_warnings_json", "rich_fields_summary_json", "created_at", "updated_at",
	}).AddRow(
		int64(43), int64(10), "gate", "launchpool_project_list", "gate-abc", "https://gate.example/abc", "Gate Launchpool ABC", "launchpool",
		"Stake to earn ABC", "100,000 USDT", nil, nil, now, "",
		"hash-gate", "gate|launchpool_project_list|abc", 0, 1, EventStatusActive,
		ReviewPending, "", 0, 1, "activity-parser-v1",
		[]byte(`[]`), []byte(`{"reward":"100,000 USDT"}`), now, now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_event e")).
		WithArgs(EventStatusActive, ReviewRejected, ReviewApproved, "gate", "launchpool_project_list", 2).
		WillReturnRows(rows)

	events, err := repo.ListOutboxCandidateEventsBySource(context.Background(), "gate", "launchpool_project_list", 2)
	if err != nil {
		t.Fatalf("ListOutboxCandidateEventsBySource err=%v", err)
	}
	if len(events) != 1 || events[0].Platform != "gate" || events[0].SourceGroup != "launchpool_project_list" {
		t.Fatalf("events=%+v", events)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryGetActivityEventReturnsRawEvidencePreview(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newActivityRepoWithMock(t, now)
	defer cleanup()

	eventRows := sqlmock.NewRows([]string{
		"id", "raw_evidence_id", "platform", "source_group", "source_external_id", "source_url", "title", "activity_type",
		"content_text", "reward_pool_text", "start_time", "end_time", "publish_time", "raw_time_text",
		"content_hash", "dedupe_key", "needs_human_review", "auto_push_allowed", "event_status",
		"review_status", "ops_decision_action", "ops_decision_stale", "event_version", "parser_version",
		"parser_warnings_json", "rich_fields_summary_json", "created_at", "updated_at",
	}).AddRow(
		int64(42), int64(9), "gate", "launchpool_project_list", "gate-abc", "https://gate.example/abc", "Gate Launchpool ABC", "launchpool",
		"Launchpool project list entry", "", nil, nil, nil, "",
		"hash", "gate|launchpool|abc", 0, 1, EventStatusActive,
		ReviewPending, "", 0, 1, "activity-parser-v1",
		[]byte(`[]`), []byte(`{}`), now, now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_event WHERE id = ?")).
		WithArgs(int64(42)).
		WillReturnRows(eventRows)
	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_event_symbol WHERE event_id = ?")).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"event_id", "canonical_symbol", "display_symbol", "market_surface", "role", "sort_order"}))
	rawRows := sqlmock.NewRows([]string{
		"id", "source_key", "platform", "source_group", "source_url", "fetch_mode", "payload_hash", "payload_preview",
		"payload_size_bytes", "payload_truncated", "schema_hash", "content_hash", "fetched_at",
	}).AddRow(int64(9), "gate|launchpool_project_list|utls_proxy_json", "gate", "launchpool_project_list", "https://gate.example/api/list", "utls_proxy_json", "payload-hash", `{"title":"Gate Launchpool ABC"}`, int64(1024), 0, "schema", "content", now)
	mock.ExpectQuery(regexp.QuoteMeta("FROM t_activity_raw_evidence")).
		WithArgs(int64(9)).
		WillReturnRows(rawRows)

	ev, _, raw, err := repo.GetActivityEvent(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetActivityEvent err=%v", err)
	}
	if ev.ContentText != "Launchpool project list entry" {
		t.Fatalf("event content=%q", ev.ContentText)
	}
	if len(raw) != 1 || raw[0].PayloadPreview == "" || raw[0].PayloadSizeBytes != 1024 {
		t.Fatalf("raw evidence=%+v", raw)
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
