package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
	"edgex-ops-intelligence/backend/internal/config"
)

type fakeActivityStore struct {
	events       []activity.ActivityEvent
	sourceHealth []activity.SourceState
	deliveries   []activity.DeliveryOutbox
	event        activity.ActivityEvent
	symbols      []activity.ActivityEventSymbol
	raw          []activity.RawEvidence
	reviews      []activity.ReviewRecord
	decisions    []activity.DecisionRecord
	redriveOK    bool
}

func (f *fakeActivityStore) ListActivityEvents(ctx context.Context, filter activity.EventFilter) ([]activity.ActivityEvent, string, error) {
	return f.events, "", nil
}
func (f *fakeActivityStore) GetActivityEvent(ctx context.Context, id int64) (activity.ActivityEvent, []activity.ActivityEventSymbol, []activity.RawEvidence, error) {
	return f.event, f.symbols, f.raw, nil
}
func (f *fakeActivityStore) ListActivitySourceHealth(ctx context.Context, platform, status string, enabled *bool) ([]activity.SourceState, error) {
	return f.sourceHealth, nil
}
func (f *fakeActivityStore) ListActivityDeliveries(ctx context.Context, filter activity.DeliveryFilter) ([]activity.DeliveryOutbox, string, error) {
	return f.deliveries, "", nil
}
func (f *fakeActivityStore) RecordReview(ctx context.Context, rec activity.ReviewRecord) error {
	f.reviews = append(f.reviews, rec)
	return nil
}
func (f *fakeActivityStore) RecordDecision(ctx context.Context, rec activity.DecisionRecord) error {
	f.decisions = append(f.decisions, rec)
	return nil
}
func (f *fakeActivityStore) RedriveDelivery(ctx context.Context, id int64, reason string) (bool, error) {
	return f.redriveOK, nil
}

func TestActivityEventsReturns503WhenReaderMissing(t *testing.T) {
	server := NewServer(config.Config{}, fakeStoreReader{})
	req := httptest.NewRequest(http.MethodGet, "/api/activity/events", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["feature"] != "activity_agent" {
		t.Fatalf("body=%+v", body)
	}
}

func TestActivityEventsReturnsListWithoutTop30Context(t *testing.T) {
	now := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)
	store := &fakeActivityStore{events: []activity.ActivityEvent{{
		ID: 42, Platform: "binance", SourceGroup: "cms_article_detail", Title: "Launchpool ABC",
		ActivityType: "launchpool", ReviewStatus: activity.ReviewPending, EventVersion: 2,
		ContentHash: "hash", DedupeKey: "binance|cms_article_detail|abc", PublishTime: &now,
	}}}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithActivityStore(store), WithActivityDecisionTokenSecret("secret"))
	req := httptest.NewRequest(http.MethodGet, "/api/activity/events?platform=binance&limit=10", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items := body["items"].([]any)
	first := items[0].(map[string]any)
	if first["title"] != "Launchpool ABC" || first["platform"] != "binance" {
		t.Fatalf("event=%+v", first)
	}
	if _, has := first["top30_context"]; has {
		t.Fatalf("activity event response must not include top30_context: %+v", first)
	}
}

func TestActivityEventDetailReturnsReadableContentAndRawEvidencePreview(t *testing.T) {
	now := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)
	store := &fakeActivityStore{
		event: activity.ActivityEvent{
			ID: 42, RawEvidenceID: 9, Platform: "gate", SourceGroup: "launchpool_project_list", SourceURL: "https://gate.example/launchpool/abc",
			Title: "Gate Launchpool ABC", ActivityType: "launchpool", ContentText: "Launchpool project list entry",
			RewardPoolText: "100,000 USDT", ReviewStatus: activity.ReviewPending, EventStatus: activity.EventStatusActive,
			EventVersion: 1, ContentHash: "hash", DedupeKey: "gate|launchpool|abc", PublishTime: &now,
			ParserWarningsJSON:    json.RawMessage(`["raw_time_unknown"]`),
			RichFieldsSummaryJSON: json.RawMessage(`{"reward":"100,000 USDT"}`),
		},
		raw: []activity.RawEvidence{{
			ID: 9, SourceKey: "gate|launchpool_project_list|utls_proxy_json", Platform: "gate", SourceGroup: "launchpool_project_list",
			SourceURL: "https://gate.example/api/list", FetchMode: "utls_proxy_json", PayloadHash: "payload-hash",
			PayloadPreview: `{"title":"Gate Launchpool ABC"}`, PayloadSizeBytes: 1024, FetchedAt: now,
		}},
	}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithActivityStore(store))
	req := httptest.NewRequest(http.MethodGet, "/api/activity/events/42", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	event := body["event"].(map[string]any)
	if event["content_text"] != "Launchpool project list entry" || event["reward_pool_text"] != "100,000 USDT" {
		t.Fatalf("event detail missing readable fields: %+v", event)
	}
	refs := body["raw_evidence_refs"].([]any)
	first := refs[0].(map[string]any)
	if first["payload_preview"] == "" || first["fetch_mode"] != "utls_proxy_json" {
		t.Fatalf("raw evidence preview missing: %+v", first)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("PayloadPreview")) {
		t.Fatalf("activity detail must use snake_case wire shape: %s", w.Body.String())
	}
}

func TestActivityReviewDefaultsReviewer(t *testing.T) {
	store := &fakeActivityStore{}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithActivityStore(store))
	req := httptest.NewRequest(http.MethodPost, "/api/activity/review/42", bytes.NewReader([]byte(`{"action":"approve"}`)))
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(store.reviews) != 1 || store.reviews[0].Reviewer != "manual_unknown" || store.reviews[0].Action != "approve" {
		t.Fatalf("reviews=%+v", store.reviews)
	}
}

func TestActivityDecisionValidatesSignedVersionedToken(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	token, err := activity.GenerateDecisionToken(activity.DecisionTokenClaims{
		EventID: 42, EventVersion: 3, ContentHash: "hash-v3", Action: activity.DecisionDifferentiate, ExpiresAt: now.Add(time.Hour),
	}, "secret")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	store := &fakeActivityStore{}
	server := NewServer(config.Config{}, fakeStoreReader{},
		WithActivityStore(store),
		WithActivityDecisionTokenSecret("secret"),
		WithActivityNow(func() time.Time { return now }),
	)
	body := []byte(`{"action":"differentiate","version":3,"token":"` + token + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/activity/decision/42", bytes.NewReader(body))
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(store.decisions) != 1 || store.decisions[0].ContentHash != "hash-v3" || store.decisions[0].Action != activity.DecisionDifferentiate {
		t.Fatalf("decisions=%+v", store.decisions)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/activity/decision/42", bytes.NewReader([]byte(`{"action":"differentiate","version":4,"token":"`+token+`"}`)))
	w = httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("version mismatch status=%d want 409 body=%s", w.Code, w.Body.String())
	}
}

func TestActivityRedriveRejectsDisallowedStatus(t *testing.T) {
	store := &fakeActivityStore{redriveOK: false}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithActivityStore(store))
	req := httptest.NewRequest(http.MethodPost, "/api/activity/deliveries/9/redrive", bytes.NewReader([]byte(`{"reason":"retry"}`)))
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", w.Code, w.Body.String())
	}
}

func TestActivitySourceHealthAndDeliveriesUseSnakeCaseWireShape(t *testing.T) {
	status := 200
	store := &fakeActivityStore{
		sourceHealth: []activity.SourceState{{
			ID: 1, Platform: "gate", SourceGroup: "launchpool_project_list", FetchMode: "utls_proxy_json",
			Enabled: true, SourceStatus: activity.SourceStatusOK, LastHTTPStatus: &status,
		}},
		deliveries: []activity.DeliveryOutbox{{
			ID: 9, EventType: activity.DeliveryEventEventAlert, DedupeKey: "activity_event|9|1",
			TargetChannel: activity.DeliveryChannelLarkActivity, Status: activity.DeliveryStatusPending,
			AttemptCount: 1, MaxAttempts: 5,
		}},
	}
	server := NewServer(config.Config{}, fakeStoreReader{}, WithActivityStore(store))
	for _, path := range []string{"/api/activity/source-health", "/api/activity/deliveries"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		server.Routes().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("SourceGroup")) || bytes.Contains(w.Body.Bytes(), []byte("EventType")) {
			t.Fatalf("%s must use snake_case wire shape: %s", path, w.Body.String())
		}
	}
}
