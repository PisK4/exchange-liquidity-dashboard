package activity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeDeliveryStore struct {
	rows     []DeliveryOutbox
	disabled []int64
	updates  []DeliveryOutbox
	attempts int
}

func (f *fakeDeliveryStore) LoadDueActivityOutbox(ctx context.Context, now time.Time, limit int) ([]DeliveryOutbox, error) {
	return f.rows, nil
}

func (f *fakeDeliveryStore) MarkActivityOutboxDisabledNoWebhook(ctx context.Context, id int64, now time.Time) error {
	f.disabled = append(f.disabled, id)
	return nil
}

func (f *fakeDeliveryStore) UpdateActivityOutboxAfterSend(ctx context.Context, id int64, status string, attempt int, nextAttempt time.Time, lastErr string, now time.Time, sent bool) error {
	f.updates = append(f.updates, DeliveryOutbox{ID: id, Status: status, AttemptCount: attempt, LastError: lastErr})
	return nil
}

func (f *fakeDeliveryStore) RecordActivityDeliveryAttempt(ctx context.Context, outboxID int64, attempt int, status string, httpStatus *int, errMsg, responseBody string, attemptedAt time.Time) error {
	f.attempts++
	return nil
}

func TestDrainDueOutboxMarksDisabledWithoutWebhook(t *testing.T) {
	store := &fakeDeliveryStore{rows: []DeliveryOutbox{{ID: 7, PayloadJSON: []byte(`{"msg_type":"interactive"}`), Status: DeliveryStatusPending}}}
	res, err := DrainDueOutbox(context.Background(), store, DeliveryDeps{})
	if err != nil {
		t.Fatalf("DrainDueOutbox err=%v", err)
	}
	if res.Disabled != 1 || len(store.disabled) != 1 || store.disabled[0] != 7 {
		t.Fatalf("res=%+v disabled=%+v", res, store.disabled)
	}
}

func TestDrainDueOutboxMarksSentOn2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()
	store := &fakeDeliveryStore{rows: []DeliveryOutbox{{ID: 8, PayloadJSON: []byte(`{"msg_type":"interactive"}`), Status: DeliveryStatusPending, MaxAttempts: 3}}}
	res, err := DrainDueOutbox(context.Background(), store, DeliveryDeps{WebhookURL: srv.URL + "/bot", Client: srv.Client()})
	if err != nil {
		t.Fatalf("DrainDueOutbox err=%v", err)
	}
	if res.Sent != 1 || store.attempts != 1 || len(store.updates) != 1 || store.updates[0].Status != DeliveryStatusSent {
		t.Fatalf("res=%+v updates=%+v attempts=%d", res, store.updates, store.attempts)
	}
}

func TestDrainDueOutboxRoutesByTargetChannel(t *testing.T) {
	var defaultHits, gateHits int
	defaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultHits++
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer defaultSrv.Close()
	gateSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateHits++
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer gateSrv.Close()

	store := &fakeDeliveryStore{rows: []DeliveryOutbox{
		{ID: 10, TargetChannel: DeliveryChannelLarkActivity, PayloadJSON: []byte(`{"msg_type":"interactive"}`), Status: DeliveryStatusPending, MaxAttempts: 3},
		{ID: 11, TargetChannel: "lark_activity_gate", PayloadJSON: []byte(`{"msg_type":"interactive"}`), Status: DeliveryStatusPending, MaxAttempts: 3},
	}}
	res, err := DrainDueOutbox(context.Background(), store, DeliveryDeps{
		WebhookURL: defaultSrv.URL,
		WebhookURLByChannel: map[string]string{
			"lark_activity_gate": gateSrv.URL,
		},
		Client: defaultSrv.Client(),
	})
	if err != nil {
		t.Fatalf("DrainDueOutbox err=%v", err)
	}
	if res.Sent != 2 || defaultHits != 1 || gateHits != 1 {
		t.Fatalf("res=%+v defaultHits=%d gateHits=%d", res, defaultHits, gateHits)
	}
}

func TestDrainDueOutboxDisablesUnknownChannelWithoutWebhook(t *testing.T) {
	store := &fakeDeliveryStore{rows: []DeliveryOutbox{{
		ID: 12, TargetChannel: "lark_activity_gate", PayloadJSON: []byte(`{"msg_type":"interactive"}`), Status: DeliveryStatusPending,
	}}}
	res, err := DrainDueOutbox(context.Background(), store, DeliveryDeps{WebhookURL: "https://example.invalid/default"})
	if err != nil {
		t.Fatalf("DrainDueOutbox err=%v", err)
	}
	if res.Disabled != 1 || len(store.disabled) != 1 || store.disabled[0] != 12 {
		t.Fatalf("res=%+v disabled=%+v", res, store.disabled)
	}
}

func TestDrainDueOutboxRetriesAndFailsAtMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad card", http.StatusBadRequest)
	}))
	defer srv.Close()
	store := &fakeDeliveryStore{rows: []DeliveryOutbox{{ID: 9, PayloadJSON: []byte(`{"bad":true}`), Status: DeliveryStatusPending, AttemptCount: 2, MaxAttempts: 3}}}
	res, err := DrainDueOutbox(context.Background(), store, DeliveryDeps{WebhookURL: srv.URL, Client: srv.Client()})
	if err != nil {
		t.Fatalf("DrainDueOutbox err=%v", err)
	}
	if res.Failed != 1 || len(store.updates) != 1 || store.updates[0].Status != DeliveryStatusFailed {
		t.Fatalf("res=%+v updates=%+v", res, store.updates)
	}
}
