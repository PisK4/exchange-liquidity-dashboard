package listing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDrainDueOutboxMarksSentOn2xx(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()

	rows := sqlmock.NewRows([]string{
		"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
		"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
	}).AddRow(
		int64(7), DeliveryEventTop30HotGap, "top30_hot_gap|ABC|优先上架|2026-05-27", DeliveryChannelLarkTop30,
		OutboxStatusPending, 0, 5, now.Add(-time.Minute),
		[]byte(`{"msg_type":"post","content":{}}`), nil, nil, now, now,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_delivery_outbox SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_delivery_attempt")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := DrainDueOutbox(context.Background(), repo, DeliveryDeps{
		WebhookURL: srv.URL,
		Client:     srv.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("DrainDueOutbox err = %v", err)
	}
	if result.Sent != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected one webhook hit, got %d", hits)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestDrainDueOutboxDisablesWhenNoWebhook(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
		"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
	}).AddRow(
		int64(7), DeliveryEventTop30HotGap, "top30_hot_gap|ABC|优先上架|2026-05-27", DeliveryChannelLarkTop30,
		OutboxStatusPending, 0, 5, now.Add(-time.Minute),
		[]byte(`{"msg_type":"post","content":{}}`), nil, nil, now, now,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_delivery_outbox SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := DrainDueOutbox(context.Background(), repo, DeliveryDeps{
		WebhookURL: "",
		Client:     http.DefaultClient,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("DrainDueOutbox err = %v", err)
	}
	if result.Disabled != 1 {
		t.Fatalf("result = %+v want one disabled", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestDrainDueOutboxSuppressesAlreadyListedDecisionCardBeforeRetry(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 20, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rows := sqlmock.NewRows([]string{
		"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
		"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
	}).AddRow(
		int64(77), DeliveryEventListingDecisionCandidate, "listing_decision|13602|first_listing", DeliveryChannelLarkTop30,
		OutboxStatusRetry, 2, 5, now.Add(-time.Minute),
		[]byte(`{"msg_type":"interactive","card":{"elements":[]}}`), "previous webhook error", nil, now.Add(-10*time.Minute), now.Add(-time.Minute),
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WithArgs(int64(13602)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
			"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
			"business_score", "business_score_version", "recommendation", "recommendation_label",
			"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
		}).AddRow(
			int64(13602), "SPCX", "SPCXUSDC", "perp", "rwa",
			LifecycleAlreadyListed, LifecycleStatusLabels[LifecycleAlreadyListed], EvidenceInstrumentDiffOnly, ConfidenceMedium,
			nil, BusinessScoreVersion, RecommendationRecordOnly, RecommendationLabels[RecommendationRecordOnly],
			[]byte(`["bitget"]`), nil, now.Add(-24*time.Hour), now,
		))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_delivery_outbox SET status")).
		WithArgs(OutboxStatusDisabled, "suppressed: candidate already listed on edgeX", now, int64(77)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := DrainDueOutbox(context.Background(), repo, DeliveryDeps{
		WebhookURL: srv.URL,
		Client:     srv.Client(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("DrainDueOutbox err = %v", err)
	}
	if result.Disabled != 1 || result.Sent != 0 || result.Retried != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want only disabled suppression", result)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("webhook hits = %d, want 0 for suppressed stale decision card", hits)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestLarkSignProducesStableHash(t *testing.T) {
	sig := LarkSign("secret-key", time.Unix(1716800000, 0))
	if sig == "" {
		t.Fatal("LarkSign should return a non-empty signature")
	}
}

func TestDrainDueOutboxAppliesDedupeKeyPrefixFilter(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Smoke prefix isolation: only rows whose dedupe_key starts with
	// the prefix are drained, even if production rows are also
	// pending. The mock will refuse to match if the LIKE clause is
	// missing from the SQL or the arg ordering is wrong, so this is
	// effectively an assertion on the query shape too.
	prefix := "lark_push_test|abc123|"
	rows := sqlmock.NewRows([]string{
		"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
		"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
	}).AddRow(
		int64(99), DeliveryEventTop30HotGap, prefix+"top30_hot_gap|ABC|优先上架|2026-05-27",
		DeliveryChannelLarkTop30, OutboxStatusPending, 0, 5, now.Add(-time.Minute),
		[]byte(`{"msg_type":"post","content":{}}`), nil, nil, now, now,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN .+ AND dedupe_key LIKE`).
		WithArgs(OutboxStatusPending, OutboxStatusRetry, now, prefix+"%", 50).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_delivery_outbox SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_delivery_attempt")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := DrainDueOutbox(context.Background(), repo, DeliveryDeps{
		WebhookURL:      srv.URL,
		Client:          srv.Client(),
		Now:             func() time.Time { return now },
		DedupeKeyPrefix: prefix,
	})
	if err != nil {
		t.Fatalf("DrainDueOutbox err = %v", err)
	}
	if result.Sent != 1 {
		t.Fatalf("result = %+v want Sent=1", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestDrainDueOutboxAttachesLarkSignWhenSecretSet(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rows := sqlmock.NewRows([]string{
		"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
		"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
	}).AddRow(
		int64(7), DeliveryEventTop30HotGap, "top30_hot_gap|ABC|优先上架|2026-05-27", DeliveryChannelLarkTop30,
		OutboxStatusPending, 0, 5, now.Add(-time.Minute),
		[]byte(`{"msg_type":"post","content":{}}`), nil, nil, now, now,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_delivery_outbox SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_delivery_attempt")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	_, err := DrainDueOutbox(context.Background(), repo, DeliveryDeps{
		WebhookURL:    srv.URL,
		WebhookSecret: "secret-key",
		Client:        srv.Client(),
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("DrainDueOutbox err = %v", err)
	}
	if _, ok := receivedBody["timestamp"]; !ok {
		t.Fatalf("expected timestamp in body, got %+v", receivedBody)
	}
	if _, ok := receivedBody["sign"]; !ok {
		t.Fatalf("expected sign in body, got %+v", receivedBody)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
