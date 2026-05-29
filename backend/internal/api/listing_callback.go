package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/listing"
)

// DecisionWriter is the narrow write interface the callback handler
// uses to persist a button click. *listing.Repository satisfies it;
// tests pass fakes so the API tests stay independent of MySQL.
type DecisionWriter interface {
	InsertDecision(ctx context.Context, d listing.DecisionRecord) (int64, bool, error)
}

// ListingCallbackConfig holds the runtime knobs the callback handler
// reads: shared HMAC secret, ±MaxClockSkew window, operator open_id
// whitelist, plus an injectable clock for tests. None of these are
// persisted; the secret in particular only lives in memory and never
// reaches MySQL or the structured log lines.
type ListingCallbackConfig struct {
	Secret        string
	MaxClockSkew  time.Duration
	OperatorAllow []string
	Now           func() time.Time
}

// WithListingDecisionWriter attaches the decision writer used by the
// callback route. Leaving it unset disables the route (returns 503).
func WithListingDecisionWriter(w DecisionWriter) Option {
	return func(s *Server) { s.decisions = w }
}

// WithListingCallback configures the HMAC verification + operator
// gate for the Lark callback. The handler is gated on a non-empty
// Secret: when blank the route returns 503 so callbacks aren't
// silently accepted in environments that have not yet configured
// the shared secret.
func WithListingCallback(cfg ListingCallbackConfig) Option {
	return func(s *Server) { s.callback = cfg }
}

func (s *Server) registerListingCallback(mux *http.ServeMux) {
	mux.HandleFunc("/api/listing/callback", s.listingCallback)
}

func (s *Server) listingCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.decisions == nil || strings.TrimSpace(s.callback.Secret) == "" {
		writeListingUnavailable(w, "listing callback not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	timestamp := r.Header.Get("X-Lark-Request-Timestamp")
	signature := r.Header.Get("X-Lark-Signature")
	if timestamp == "" || signature == "" {
		writeCallbackError(w, http.StatusUnauthorized, "missing signature headers")
		return
	}
	expected := computeCallbackSignature(s.callback.Secret, timestamp)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		writeCallbackError(w, http.StatusUnauthorized, "signature mismatch")
		return
	}
	tsUnix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		writeCallbackError(w, http.StatusBadRequest, "timestamp not int")
		return
	}
	now := s.callbackNow()
	skew := s.callback.MaxClockSkew
	if skew <= 0 {
		skew = 300 * time.Second
	}
	if abs(now.Unix()-tsUnix) > int64(skew/time.Second) {
		writeCallbackError(w, http.StatusForbidden, "timestamp outside window")
		return
	}
	parsed, err := decodeCallbackBody(body)
	if err != nil {
		writeCallbackError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !operatorAllowed(s.callback.OperatorAllow, parsed.OperatorOpenID) {
		writeCallbackError(w, http.StatusForbidden, "operator not in whitelist")
		return
	}
	rec := listing.DecisionRecord{
		CandidateID:         parsed.CandidateID,
		CardID:              parsed.CardID,
		MessageID:           parsed.MessageID,
		OperatorOpenID:      parsed.OperatorOpenID,
		Action:              parsed.Action,
		Reason:              parsed.Reason,
		SignatureVerified:   true,
		CallbackPayloadJSON: body,
		CallbackTS:          now.Truncate(time.Second),
	}
	id, inserted, err := s.decisions.InsertDecision(r.Context(), rec)
	if err != nil {
		writeListingError(w, err)
		return
	}
	status := "recorded"
	if !inserted {
		status = "already_recorded"
	}
	writeJSON(w, map[string]any{
		"status":      status,
		"decision_id": id,
		"action":      rec.Action,
		"candidate":   rec.CandidateID,
	})
}

func (s *Server) callbackNow() time.Time {
	if s.callback.Now != nil {
		return s.callback.Now()
	}
	return time.Now().UTC()
}

// computeCallbackSignature follows spec §5: HMAC-SHA256 of
// (timestamp + secret) keyed on the shared secret, base64 encoded.
// This matches the reference implementation in
// backend/internal/listing/delivery.go LarkSign for outbound payloads
// so operators only need to remember one signing convention.
func computeCallbackSignature(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte(secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func operatorAllowed(allow []string, openID string) bool {
	if openID == "" {
		return false
	}
	for _, a := range allow {
		if strings.EqualFold(strings.TrimSpace(a), openID) {
			return true
		}
	}
	return false
}

// callbackPayload is the minimal projection of the Lark interactive
// callback envelope. The structure tolerates Lark's two common
// payload shapes (action.value object vs string) by accepting either.
type callbackPayload struct {
	CandidateID    int64
	Action         string
	Reason         string
	OperatorOpenID string
	CardID         string
	MessageID      string
}

func decodeCallbackBody(body []byte) (callbackPayload, error) {
	var envelope struct {
		Action struct {
			Value json.RawMessage `json:"value"`
		} `json:"action"`
		Operator struct {
			OpenID string `json:"open_id"`
		} `json:"operator"`
		OpenMessageID string `json:"open_message_id"`
		OpenCardID    string `json:"open_card_id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return callbackPayload{}, errors.New("invalid json envelope")
	}
	var value struct {
		CandidateID int64  `json:"candidate_id"`
		Action      string `json:"action"`
		Reason      string `json:"reason"`
	}
	if len(envelope.Action.Value) > 0 {
		if err := json.Unmarshal(envelope.Action.Value, &value); err != nil {
			return callbackPayload{}, errors.New("invalid action.value")
		}
	}
	if value.CandidateID == 0 || value.Action == "" {
		return callbackPayload{}, errors.New("action.value missing candidate_id or action")
	}
	return callbackPayload{
		CandidateID:    value.CandidateID,
		Action:         value.Action,
		Reason:         value.Reason,
		OperatorOpenID: envelope.Operator.OpenID,
		CardID:         envelope.OpenCardID,
		MessageID:      envelope.OpenMessageID,
	}, nil
}

func writeCallbackError(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "rejected", "reason": reason})
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
