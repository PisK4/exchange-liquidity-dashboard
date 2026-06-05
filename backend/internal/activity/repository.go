package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Repository struct {
	db  *sql.DB
	now func() time.Time
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (r *Repository) UpsertRawEvidence(ctx context.Context, e RawEvidence) (int64, error) {
	if r.db == nil {
		return 0, errors.New("activity repository: no db attached")
	}
	if e.FetchedAt.IsZero() {
		e.FetchedAt = r.now()
	}
	prepared := PrepareRawEvidencePayload(e.PayloadText, MaxRawPayloadBytes)
	responseMeta := e.ResponseMeta
	if len(responseMeta) == 0 {
		responseMeta = json.RawMessage(`{}`)
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO t_activity_raw_evidence
		   (source_key, platform, source_group, source_url, fetch_mode,
		    payload_text, payload_hash, schema_hash, content_hash,
		    payload_size_bytes, payload_truncated, payload_preview,
		    response_meta_json, fixture_ref, fetched_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
		 ON DUPLICATE KEY UPDATE
		   content_hash = VALUES(content_hash),
		   response_meta_json = VALUES(response_meta_json),
		   fetched_at = VALUES(fetched_at),
		   updated_at = UTC_TIMESTAMP(3)`,
		e.SourceKey, e.Platform, e.SourceGroup, e.SourceURL, e.FetchMode,
		nullString(prepared.PayloadText), prepared.Hash, nullString(e.SchemaHash), nullString(e.ContentHash),
		prepared.SizeBytes, prepared.Truncated, nullString(prepared.Preview),
		responseMeta, nullString(e.FixtureRef), e.FetchedAt,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (r *Repository) RecordDecision(ctx context.Context, rec DecisionRecord) error {
	if r.db == nil {
		return errors.New("activity repository: no db attached")
	}
	reviewStatus, ok := ReviewStatusForDecision(rec.Action)
	if !ok {
		return ErrInvalidDecisionAction
	}
	reviewer := rec.Reviewer
	if reviewer == "" {
		reviewer = "manual_unknown"
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_activity_event
		    SET review_status = ?,
		        ops_decision_action = ?,
		        ops_decision_stale = ?,
		        reviewer = ?,
		        review_reason = ?,
		        reviewed_at = UTC_TIMESTAMP(3),
		        updated_at = UTC_TIMESTAMP(3)
		  WHERE id = ? AND event_version = ? AND content_hash = ?`,
		reviewStatus, rec.Action, false, reviewer, nullString(rec.Reason), rec.EventID, rec.EventVersion, rec.ContentHash,
	)
	return err
}

func (r *Repository) RecordReview(ctx context.Context, rec ReviewRecord) error {
	if r.db == nil {
		return errors.New("activity repository: no db attached")
	}
	status := ReviewApproved
	if rec.Action == "reject" {
		status = ReviewRejected
	}
	reviewer := rec.Reviewer
	if reviewer == "" {
		reviewer = "manual_unknown"
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_activity_event
		    SET review_status = ?,
		        reviewer = ?,
		        review_reason = ?,
		        reviewed_at = UTC_TIMESTAMP(3),
		        updated_at = UTC_TIMESTAMP(3)
		  WHERE id = ?`,
		status, reviewer, nullString(rec.Reason), rec.EventID,
	)
	return err
}

func (r *Repository) ListActivityEvents(ctx context.Context, filter EventFilter) ([]ActivityEvent, string, error) {
	if r.db == nil {
		return nil, "", errors.New("activity repository: no db attached")
	}
	limit := clampLimit(filter.Limit, 50, 200)
	query := `SELECT id, platform, source_group, COALESCE(source_external_id,''), COALESCE(source_url,''), title, activity_type,
	                 content_hash, dedupe_key, needs_human_review, auto_push_allowed, event_status,
	                 review_status, COALESCE(ops_decision_action,''), ops_decision_stale,
	                 event_version, parser_version, publish_time, created_at, updated_at
	            FROM t_activity_event`
	where := []string{}
	args := []any{}
	if filter.Platform != "" {
		where = append(where, "platform = ?")
		args = append(args, filter.Platform)
	}
	if filter.ActivityType != "" {
		where = append(where, "activity_type = ?")
		args = append(args, filter.ActivityType)
	}
	if filter.Status != "" {
		where = append(where, "event_status = ?")
		args = append(args, filter.Status)
	}
	if filter.ReviewStatus != "" {
		where = append(where, "review_status = ?")
		args = append(args, filter.ReviewStatus)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY COALESCE(publish_time, updated_at) DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []ActivityEvent{}
	for rows.Next() {
		ev, err := scanEventSummary(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, ev)
	}
	return out, "", rows.Err()
}

func (r *Repository) GetActivityEvent(ctx context.Context, id int64) (ActivityEvent, []ActivityEventSymbol, []RawEvidence, error) {
	if r.db == nil {
		return ActivityEvent{}, nil, nil, errors.New("activity repository: no db attached")
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, platform, source_group, COALESCE(source_external_id,''), COALESCE(source_url,''), title, activity_type,
	                 content_hash, dedupe_key, needs_human_review, auto_push_allowed, event_status,
	                 review_status, COALESCE(ops_decision_action,''), ops_decision_stale,
	                 event_version, parser_version, publish_time, created_at, updated_at
	            FROM t_activity_event WHERE id = ?`, id)
	ev, err := scanEventSummary(row)
	if err != nil {
		return ActivityEvent{}, nil, nil, err
	}
	symbols, err := r.loadEventSymbols(ctx, id)
	if err != nil {
		return ActivityEvent{}, nil, nil, err
	}
	return ev, symbols, nil, nil
}

func (r *Repository) ListActivitySourceHealth(ctx context.Context, platform, status string, enabled *bool) ([]SourceState, error) {
	if r.db == nil {
		return nil, errors.New("activity repository: no db attached")
	}
	query := `SELECT id, platform, source_group, source_type, COALESCE(source_url,''), source_key, fetch_mode,
	                 evidence_quality, enabled, auto_push_enabled, requires_proxy, requires_browser_context,
	                 requires_login, personalized, source_status, last_http_status, COALESCE(last_error_kind,''),
	                 disabled_until, updated_at
	            FROM t_activity_source_state`
	where := []string{}
	args := []any{}
	if platform != "" {
		where = append(where, "platform = ?")
		args = append(args, platform)
	}
	if status != "" {
		where = append(where, "source_status = ?")
		args = append(args, status)
	}
	if enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, *enabled)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY platform, source_group"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceState{}
	for rows.Next() {
		var s SourceState
		var enabledInt, autoPush, requiresProxy, requiresBrowser, requiresLogin, personalized int
		var httpStatus sql.NullInt64
		var disabled sql.NullTime
		if err := rows.Scan(&s.ID, &s.Platform, &s.SourceGroup, &s.SourceType, &s.SourceURL, &s.SourceKey, &s.FetchMode,
			&s.EvidenceQuality, &enabledInt, &autoPush, &requiresProxy, &requiresBrowser, &requiresLogin, &personalized,
			&s.SourceStatus, &httpStatus, &s.LastErrorKind, &disabled, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Enabled = enabledInt != 0
		s.AutoPushEnabled = autoPush != 0
		s.RequiresProxy = requiresProxy != 0
		s.RequiresBrowserContext = requiresBrowser != 0
		s.RequiresLogin = requiresLogin != 0
		s.Personalized = personalized != 0
		if httpStatus.Valid {
			v := int(httpStatus.Int64)
			s.LastHTTPStatus = &v
		}
		if disabled.Valid {
			t := disabled.Time.UTC()
			s.DisabledUntil = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) ListActivityDeliveries(ctx context.Context, filter DeliveryFilter) ([]DeliveryOutbox, string, error) {
	if r.db == nil {
		return nil, "", errors.New("activity repository: no db attached")
	}
	limit := clampLimit(filter.Limit, 50, 200)
	query := `SELECT id, event_type, dedupe_key, target_channel, status, attempt_count, max_attempts,
	                 next_attempt_at, payload_json, COALESCE(last_error,''), sent_at, created_at, updated_at
	            FROM t_activity_delivery_outbox`
	where := []string{}
	args := []any{}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, filter.EventType)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []DeliveryOutbox{}
	for rows.Next() {
		var d DeliveryOutbox
		var next, sent sql.NullTime
		if err := rows.Scan(&d.ID, &d.EventType, &d.DedupeKey, &d.TargetChannel, &d.Status, &d.AttemptCount, &d.MaxAttempts,
			&next, &d.PayloadJSON, &d.LastError, &sent, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, "", err
		}
		if next.Valid {
			t := next.Time.UTC()
			d.NextAttemptAt = &t
		}
		if sent.Valid {
			t := sent.Time.UTC()
			d.SentAt = &t
		}
		out = append(out, d)
	}
	return out, "", rows.Err()
}

func (r *Repository) RedriveDelivery(ctx context.Context, id int64, reason string) (bool, error) {
	if r.db == nil {
		return false, errors.New("activity repository: no db attached")
	}
	now := r.now()
	res, err := r.db.ExecContext(ctx,
		`UPDATE t_activity_delivery_outbox
		    SET status = ?,
		        last_error = ?,
		        next_attempt_at = ?,
		        updated_at = UTC_TIMESTAMP(3)
		  WHERE id = ?
		    AND status IN (?, ?, ?)`,
		DeliveryStatusRedrivePending, reason, now, id,
		DeliveryStatusDisabledNoWebhook, DeliveryStatusDisabledMissingSecret, DeliveryStatusFailed,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

type eventScanner interface {
	Scan(dest ...any) error
}

func scanEventSummary(row eventScanner) (ActivityEvent, error) {
	var ev ActivityEvent
	var sourceExternalID, sourceURL, ops string
	var needsReview, autoPush, stale int
	var publish sql.NullTime
	if err := row.Scan(&ev.ID, &ev.Platform, &ev.SourceGroup, &sourceExternalID, &sourceURL, &ev.Title, &ev.ActivityType,
		&ev.ContentHash, &ev.DedupeKey, &needsReview, &autoPush, new(string),
		&ev.ReviewStatus, &ops, &stale, &ev.EventVersion, &ev.ParserVersion, &publish, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
		return ActivityEvent{}, err
	}
	ev.SourceExternalID = sourceExternalID
	ev.SourceURL = sourceURL
	ev.NeedsHumanReview = needsReview != 0
	ev.AutoPushAllowed = autoPush != 0
	ev.OpsDecisionAction = ops
	ev.OpsDecisionStale = stale != 0
	if publish.Valid {
		t := publish.Time.UTC()
		ev.PublishTime = &t
	}
	return ev, nil
}

func (r *Repository) loadEventSymbols(ctx context.Context, eventID int64) ([]ActivityEventSymbol, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT event_id, canonical_symbol, COALESCE(display_symbol,''), market_surface, role, sort_order
		FROM t_activity_event_symbol WHERE event_id = ? ORDER BY sort_order, canonical_symbol`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityEventSymbol{}
	for rows.Next() {
		var s ActivityEventSymbol
		if err := rows.Scan(&s.EventID, &s.CanonicalSymbol, &s.DisplaySymbol, &s.MarketSurface, &s.Role, &s.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func clampLimit(value, def, max int) int {
	if value <= 0 {
		value = def
	}
	if value > max {
		return max
	}
	return value
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
