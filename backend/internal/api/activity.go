package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
)

type ActivityStore interface {
	ListActivityEvents(ctx context.Context, filter activity.EventFilter) ([]activity.ActivityEvent, string, error)
	GetActivityEvent(ctx context.Context, id int64) (activity.ActivityEvent, []activity.ActivityEventSymbol, []activity.RawEvidence, error)
	ListActivitySourceHealth(ctx context.Context, platform, status string, enabled *bool) ([]activity.SourceState, error)
	ListActivityDeliveries(ctx context.Context, filter activity.DeliveryFilter) ([]activity.DeliveryOutbox, string, error)
	RecordReview(ctx context.Context, rec activity.ReviewRecord) error
	RecordDecision(ctx context.Context, rec activity.DecisionRecord) error
	RedriveDelivery(ctx context.Context, id int64, reason string) (bool, error)
}

func WithActivityStore(store ActivityStore) Option {
	return func(s *Server) { s.activity = store }
}

func WithActivityDecisionTokenSecret(secret string) Option {
	return func(s *Server) { s.activityDecisionSecret = secret }
}

func WithActivityNow(now func() time.Time) Option {
	return func(s *Server) { s.activityNow = now }
}

func (s *Server) registerActivityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/activity/events", s.activityEvents)
	mux.HandleFunc("/api/activity/events/", s.activityEventDetail)
	mux.HandleFunc("/api/activity/source-health", s.activitySourceHealth)
	mux.HandleFunc("/api/activity/deliveries", s.activityDeliveries)
	mux.HandleFunc("/api/activity/review/", s.activityReview)
	mux.HandleFunc("/api/activity/decision/", s.activityDecision)
	mux.HandleFunc("/api/activity/deliveries/", s.activityDeliveryDetail)
}

func (s *Server) activityEvents(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	q := r.URL.Query()
	filter := activity.EventFilter{
		Platform:     q.Get("platform"),
		ActivityType: q.Get("activity_type"),
		Status:       q.Get("status"),
		ReviewStatus: q.Get("review_status"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	items, next, err := s.activity.ListActivityEvents(r.Context(), filter)
	if err != nil {
		writeActivityError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, activityEventSummaryToWire(item))
	}
	writeJSON(w, map[string]any{"items": out, "next_cursor": next})
}

func (s *Server) activityEventDetail(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	id, ok := parseTrailingID(w, r, "/api/activity/events/")
	if !ok {
		return
	}
	ev, symbols, raw, err := s.activity.GetActivityEvent(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		writeActivityError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"event":             activityEventSummaryToWire(ev),
		"symbols":           symbols,
		"raw_evidence_refs": activityRawEvidenceToWire(raw),
	})
}

func (s *Server) activitySourceHealth(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	q := r.URL.Query()
	var enabled *bool
	if raw := q.Get("enabled"); raw != "" {
		v := raw == "true" || raw == "1"
		enabled = &v
	}
	rows, err := s.activity.ListActivitySourceHealth(r.Context(), q.Get("platform"), q.Get("source_status"), enabled)
	if err != nil {
		writeActivityError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, activitySourceStateToWire(row))
	}
	writeJSON(w, map[string]any{"items": out})
}

func (s *Server) activityDeliveries(w http.ResponseWriter, r *http.Request) {
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	q := r.URL.Query()
	filter := activity.DeliveryFilter{Status: q.Get("status"), EventType: q.Get("event_type")}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	rows, next, err := s.activity.ListActivityDeliveries(r.Context(), filter)
	if err != nil {
		writeActivityError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, activityDeliveryToWire(row))
	}
	writeJSON(w, map[string]any{"items": out, "next_cursor": next})
}

func (s *Server) activityReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	id, ok := parseTrailingID(w, r, "/api/activity/review/")
	if !ok {
		return
	}
	var body struct {
		Action   string `json:"action"`
		Reviewer string `json:"reviewer"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeActivityCodeError(w, http.StatusBadRequest, "invalid_json", "")
		return
	}
	if body.Action != "approve" && body.Action != "reject" {
		writeActivityCodeError(w, http.StatusBadRequest, "invalid_review_action", "")
		return
	}
	if strings.TrimSpace(body.Reviewer) == "" {
		body.Reviewer = "manual_unknown"
	}
	if err := s.activity.RecordReview(r.Context(), activity.ReviewRecord{EventID: id, Action: body.Action, Reviewer: body.Reviewer, Reason: body.Reason}); err != nil {
		writeActivityError(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "event_id": id, "action": body.Action})
}

func (s *Server) activityDecision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	id, ok := parseTrailingID(w, r, "/api/activity/decision/")
	if !ok {
		return
	}
	var body struct {
		Action   string `json:"action"`
		Version  int    `json:"version"`
		Token    string `json:"token"`
		Reviewer string `json:"reviewer"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeActivityCodeError(w, http.StatusBadRequest, "invalid_json", "")
		return
	}
	claims, err := activity.VerifyDecisionToken(body.Token, s.activityDecisionSecret, s.nowActivity())
	if err != nil {
		writeActivityCodeError(w, http.StatusUnauthorized, "invalid_decision_token", "")
		return
	}
	if claims.EventID != id || claims.EventVersion != body.Version || claims.Action != body.Action {
		writeActivityCodeError(w, http.StatusConflict, "event_version_changed", strconv.Itoa(claims.EventVersion))
		return
	}
	if strings.TrimSpace(body.Reviewer) == "" {
		body.Reviewer = "manual_unknown"
	}
	if err := s.activity.RecordDecision(r.Context(), activity.DecisionRecord{
		EventID:      id,
		EventVersion: claims.EventVersion,
		ContentHash:  claims.ContentHash,
		Action:       claims.Action,
		Reviewer:     body.Reviewer,
		Reason:       body.Reason,
	}); err != nil {
		writeActivityError(w, err)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "event_id": id, "action": claims.Action})
}

func (s *Server) activityDeliveryDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/redrive") {
		http.NotFound(w, r)
		return
	}
	if s.activity == nil {
		writeActivityUnavailable(w, "activity store not configured")
		return
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/activity/deliveries/"), "/redrive")
	id, err := strconv.ParseInt(strings.Trim(rest, "/"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ok, err := s.activity.RedriveDelivery(r.Context(), id, body.Reason)
	if err != nil {
		writeActivityError(w, err)
		return
	}
	if !ok {
		writeActivityCodeError(w, http.StatusConflict, "redrive_not_allowed", "")
		return
	}
	writeJSON(w, map[string]any{"status": "redrive_pending", "id": id})
}

func activityEventSummaryToWire(ev activity.ActivityEvent) map[string]any {
	return map[string]any{
		"id":                  ev.ID,
		"raw_evidence_id":     ev.RawEvidenceID,
		"platform":            ev.Platform,
		"source_group":        ev.SourceGroup,
		"source_url":          ev.SourceURL,
		"title":               ev.Title,
		"activity_type":       ev.ActivityType,
		"content_text":        ev.ContentText,
		"reward_pool_text":    ev.RewardPoolText,
		"start_time":          ev.StartTime,
		"end_time":            ev.EndTime,
		"raw_time_text":       ev.RawTimeText,
		"review_status":       ev.ReviewStatus,
		"ops_decision_action": ev.OpsDecisionAction,
		"event_status":        ev.EventStatus,
		"event_version":       ev.EventVersion,
		"content_hash":        ev.ContentHash,
		"dedupe_key":          ev.DedupeKey,
		"publish_time":        ev.PublishTime,
		"needs_human_review":  ev.NeedsHumanReview,
		"auto_push_allowed":   ev.AutoPushAllowed,
		"parser_warnings":     rawJSONOrDefault(ev.ParserWarningsJSON, []byte(`[]`)),
		"rich_fields_summary": rawJSONOrDefault(ev.RichFieldsSummaryJSON, []byte(`{}`)),
	}
}

func activityRawEvidenceToWire(rows []activity.RawEvidence) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":                 row.ID,
			"source_key":         row.SourceKey,
			"platform":           row.Platform,
			"source_group":       row.SourceGroup,
			"source_url":         row.SourceURL,
			"fetch_mode":         row.FetchMode,
			"payload_hash":       row.PayloadHash,
			"payload_preview":    row.PayloadPreview,
			"payload_size_bytes": row.PayloadSizeBytes,
			"payload_truncated":  row.PayloadTruncated,
			"schema_hash":        row.SchemaHash,
			"content_hash":       row.ContentHash,
			"fetched_at":         row.FetchedAt,
		})
	}
	return out
}

func rawJSONOrDefault(raw json.RawMessage, fallback []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(fallback)
	}
	return raw
}

func activitySourceStateToWire(s activity.SourceState) map[string]any {
	return map[string]any{
		"id":                       s.ID,
		"platform":                 s.Platform,
		"source_group":             s.SourceGroup,
		"source_type":              s.SourceType,
		"source_url":               s.SourceURL,
		"source_key":               s.SourceKey,
		"fetch_mode":               s.FetchMode,
		"evidence_quality":         s.EvidenceQuality,
		"enabled":                  s.Enabled,
		"auto_push_enabled":        s.AutoPushEnabled,
		"requires_proxy":           s.RequiresProxy,
		"requires_browser_context": s.RequiresBrowserContext,
		"requires_login":           s.RequiresLogin,
		"personalized":             s.Personalized,
		"source_status":            s.SourceStatus,
		"last_http_status":         s.LastHTTPStatus,
		"last_error_kind":          s.LastErrorKind,
		"disabled_until":           s.DisabledUntil,
		"updated_at":               s.UpdatedAt,
	}
}

func activityDeliveryToWire(d activity.DeliveryOutbox) map[string]any {
	return map[string]any{
		"id":              d.ID,
		"event_type":      d.EventType,
		"dedupe_key":      d.DedupeKey,
		"target_channel":  d.TargetChannel,
		"status":          d.Status,
		"attempt_count":   d.AttemptCount,
		"max_attempts":    d.MaxAttempts,
		"next_attempt_at": d.NextAttemptAt,
		"last_error":      d.LastError,
		"sent_at":         d.SentAt,
		"created_at":      d.CreatedAt,
		"updated_at":      d.UpdatedAt,
	}
}

func parseTrailingID(w http.ResponseWriter, r *http.Request, prefix string) (int64, bool) {
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (s *Server) nowActivity() time.Time {
	if s.activityNow != nil {
		return s.activityNow()
	}
	return time.Now().UTC()
}

func writeActivityUnavailable(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "unavailable", "feature": "activity_agent", "reason": reason})
}

func writeActivityError(w http.ResponseWriter, err error) {
	writeActivityCodeError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func writeActivityCodeError(w http.ResponseWriter, code int, errCode, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	payload := map[string]any{"error": map[string]any{"code": errCode}}
	if detail != "" {
		payload["error"].(map[string]any)["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(payload)
}
