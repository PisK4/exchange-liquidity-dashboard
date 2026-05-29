package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/listing"
)

type fakeDecisionWriter struct {
	calls []listing.DecisionRecord
	id    int64
	hit   bool
	err   error
}

func (f *fakeDecisionWriter) InsertDecision(ctx context.Context, d listing.DecisionRecord) (int64, bool, error) {
	f.calls = append(f.calls, d)
	if f.err != nil {
		return 0, false, f.err
	}
	return f.id, f.hit, nil
}

func signCallback(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte(secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func newCallbackServer(t *testing.T, writer *fakeDecisionWriter, now time.Time, opts ...Option) http.Handler {
	t.Helper()
	cfg := config.Config{}
	base := []Option{WithListingDecisionWriter(writer), WithListingCallback(ListingCallbackConfig{
		Secret:        "test-secret",
		MaxClockSkew:  300 * time.Second,
		OperatorAllow: []string{"ou_pis", "ou_alice"},
		Now:           func() time.Time { return now },
	})}
	server := NewServer(cfg, fakeStoreReader{}, append(base, opts...)...)
	return server.Routes()
}

func postCallback(t *testing.T, handler http.Handler, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/listing/callback", bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// TestListingCallbackRejectsInvalidSignature is the first security
// rail: an attacker without the shared secret cannot forge a valid
// HMAC and the handler must respond 401 BEFORE doing any database
// work.
func TestListingCallbackRejectsInvalidSignature(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	writer := &fakeDecisionWriter{id: 1, hit: true}
	handler := newCallbackServer(t, writer, now)
	body := []byte(`{"action":{"value":{"candidate_id":7,"action":"prepare_listing"}},"operator":{"open_id":"ou_pis"}}`)
	w := postCallback(t, handler, body, map[string]string{
		"X-Lark-Request-Timestamp": strconv.FormatInt(now.Unix(), 10),
		"X-Lark-Signature":         "deadbeef",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writer received %d calls, want 0 on invalid signature", len(writer.calls))
	}
}

// TestListingCallbackRejectsStaleTimestamp ensures the ±300s window
// from §5 is enforced. A signature that was valid 10 minutes ago
// must NOT be accepted today — this is the replay defence.
func TestListingCallbackRejectsStaleTimestamp(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	stale := now.Add(-10 * time.Minute)
	writer := &fakeDecisionWriter{id: 1, hit: true}
	handler := newCallbackServer(t, writer, now)
	body := []byte(`{"action":{"value":{"candidate_id":7,"action":"prepare_listing"}},"operator":{"open_id":"ou_pis"}}`)
	ts := strconv.FormatInt(stale.Unix(), 10)
	w := postCallback(t, handler, body, map[string]string{
		"X-Lark-Request-Timestamp": ts,
		"X-Lark-Signature":         signCallback("test-secret", ts, body),
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for stale timestamp", w.Code)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writer received %d calls, want 0 on stale timestamp", len(writer.calls))
	}
}

// TestListingCallbackRejectsNonWhitelistedOperator covers the
// configurable open_id whitelist. A valid signature + fresh
// timestamp from an unknown operator must be denied with 403.
func TestListingCallbackRejectsNonWhitelistedOperator(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	writer := &fakeDecisionWriter{id: 1, hit: true}
	handler := newCallbackServer(t, writer, now)
	body := []byte(`{"action":{"value":{"candidate_id":7,"action":"prepare_listing"}},"operator":{"open_id":"ou_attacker"}}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	w := postCallback(t, handler, body, map[string]string{
		"X-Lark-Request-Timestamp": ts,
		"X-Lark-Signature":         signCallback("test-secret", ts, body),
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-whitelisted operator", w.Code)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writer received %d calls, want 0 on non-whitelisted operator", len(writer.calls))
	}
}

// TestListingCallbackAcceptsValidClickAndTruncatesCallbackTS is the
// happy path: a valid signature + fresh timestamp + whitelisted
// operator yields a decision insert with callback_ts truncated to
// second precision (per §5 'avoid fast double-click 击穿
// uk_listing_decision_idempotency').
func TestListingCallbackAcceptsValidClickAndTruncatesCallbackTS(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 1, 800000000, time.UTC) // 10:00:01.800 — must be truncated
	writer := &fakeDecisionWriter{id: 501, hit: true}
	handler := newCallbackServer(t, writer, now)
	body := []byte(`{"action":{"value":{"candidate_id":7,"action":"prepare_listing","risk_plan_id":101,"dedupe_key":"listing_decision|7|2026-05-30"}},"operator":{"open_id":"ou_pis"},"open_message_id":"om_1","open_card_id":"card_1"}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	w := postCallback(t, handler, body, map[string]string{
		"X-Lark-Request-Timestamp": ts,
		"X-Lark-Signature":         signCallback("test-secret", ts, body),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if len(writer.calls) != 1 {
		t.Fatalf("writer.calls = %d, want 1", len(writer.calls))
	}
	rec := writer.calls[0]
	if rec.CandidateID != 7 || rec.Action != listing.DecisionActionPrepareListing || rec.OperatorOpenID != "ou_pis" {
		t.Errorf("record = %+v", rec)
	}
	if !rec.SignatureVerified {
		t.Errorf("SignatureVerified = false, want true on valid signature")
	}
	if rec.CallbackTS.Nanosecond() != 0 {
		t.Errorf("CallbackTS = %v, want truncated to seconds", rec.CallbackTS)
	}
	if rec.CallbackTS.Unix() != now.Unix() {
		t.Errorf("CallbackTS unix = %d, want %d", rec.CallbackTS.Unix(), now.Unix())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp["status"] != "recorded" {
		t.Errorf("response status = %v, want recorded", resp["status"])
	}
	if fmt.Sprintf("%v", resp["decision_id"]) != "501" {
		t.Errorf("response decision_id = %v, want 501", resp["decision_id"])
	}
}

// TestListingCallbackIdempotentOnDoubleClick maps the repo helper's
// inserted=false branch onto a stable HTTP shape (200 OK, status=
// already_recorded). Operators clicking twice should not see an
// error — the system already has their intent on file.
func TestListingCallbackIdempotentOnDoubleClick(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 1, 0, time.UTC)
	writer := &fakeDecisionWriter{id: 501, hit: false}
	handler := newCallbackServer(t, writer, now)
	body := []byte(`{"action":{"value":{"candidate_id":7,"action":"prepare_listing"}},"operator":{"open_id":"ou_pis"}}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	w := postCallback(t, handler, body, map[string]string{
		"X-Lark-Request-Timestamp": ts,
		"X-Lark-Signature":         signCallback("test-secret", ts, body),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 on idempotent click", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "already_recorded" {
		t.Errorf("status = %v, want already_recorded", resp["status"])
	}
}
