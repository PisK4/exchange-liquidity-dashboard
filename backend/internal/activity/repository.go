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

func (r *Repository) UpsertActivitySourceState(ctx context.Context, s SourceState) error {
	if r.db == nil {
		return errors.New("activity repository: no db attached")
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = r.now()
	}
	if s.SourceKey == "" {
		s.SourceKey = BuildSourceKey(s.Platform, s.SourceGroup, s.FetchMode)
	}
	if s.SourceType == "" {
		s.SourceType = "activity_source"
	}
	if s.EvidenceQuality == "" {
		s.EvidenceQuality = "unknown"
	}
	if s.SourceStatus == "" {
		s.SourceStatus = SourceStatusOK
	}
	if s.PollIntervalSeconds <= 0 {
		s.PollIntervalSeconds = 1800
	}
	sourceContext := s.SourceContextJSON
	if len(sourceContext) == 0 {
		sourceContext = json.RawMessage(`{}`)
	}
	var httpStatus any
	if s.LastHTTPStatus != nil {
		httpStatus = *s.LastHTTPStatus
	}
	var lastCheckedAt, lastSuccessAt any
	if s.LastCheckedAt != nil {
		lastCheckedAt = *s.LastCheckedAt
	}
	if s.LastSuccessAt != nil {
		lastSuccessAt = *s.LastSuccessAt
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_activity_source_state
		   (platform, source_group, source_type, source_url, source_key, fetch_mode,
		    evidence_quality, enabled, poll_interval_seconds, auto_push_enabled,
		    requires_proxy, requires_browser_context, requires_login, region_sensitive,
		    personalized, source_context_json, last_http_status, last_error_kind,
		    last_schema_hash, last_content_hash, sample_count, event_count,
		    source_status, disabled_until, last_checked_at, last_success_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   source_type = VALUES(source_type),
		   source_url = VALUES(source_url),
		   fetch_mode = VALUES(fetch_mode),
		   evidence_quality = VALUES(evidence_quality),
		   enabled = VALUES(enabled),
		   poll_interval_seconds = VALUES(poll_interval_seconds),
		   auto_push_enabled = VALUES(auto_push_enabled),
		   requires_proxy = VALUES(requires_proxy),
		   requires_browser_context = VALUES(requires_browser_context),
		   requires_login = VALUES(requires_login),
		   personalized = VALUES(personalized),
		   source_context_json = VALUES(source_context_json),
		   last_http_status = VALUES(last_http_status),
		   last_error_kind = VALUES(last_error_kind),
		   last_schema_hash = VALUES(last_schema_hash),
		   last_content_hash = VALUES(last_content_hash),
		   sample_count = sample_count + VALUES(sample_count),
		   event_count = event_count + VALUES(event_count),
		   source_status = VALUES(source_status),
		   disabled_until = VALUES(disabled_until),
		   last_checked_at = VALUES(last_checked_at),
		   last_success_at = COALESCE(VALUES(last_success_at), last_success_at),
		   updated_at = VALUES(updated_at)`,
		s.Platform, s.SourceGroup, s.SourceType, nullString(s.SourceURL), s.SourceKey, s.FetchMode,
		s.EvidenceQuality, boolInt(s.Enabled), s.PollIntervalSeconds, boolInt(s.AutoPushEnabled),
		boolInt(s.RequiresProxy), boolInt(s.RequiresBrowserContext), boolInt(s.RequiresLogin), 0,
		boolInt(s.Personalized), sourceContext, httpStatus, nullString(s.LastErrorKind),
		nullString(s.LastSchemaHash), nullString(s.LastContentHash), s.SampleCount, s.EventCount,
		s.SourceStatus, s.DisabledUntil, lastCheckedAt, lastSuccessAt, s.UpdatedAt, s.UpdatedAt,
	)
	return err
}

func (r *Repository) LoadActivitySourceState(ctx context.Context, sourceKey string) (SourceState, bool, error) {
	if r.db == nil {
		return SourceState{}, false, errors.New("activity repository: no db attached")
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, platform, source_group, source_type, COALESCE(source_url,''), source_key, fetch_mode,
	                 evidence_quality, enabled, poll_interval_seconds, auto_push_enabled, requires_proxy, requires_browser_context,
	                 requires_login, personalized, source_status, last_http_status, COALESCE(last_error_kind,''),
	                 COALESCE(last_schema_hash,''), COALESCE(last_content_hash,''), sample_count, event_count,
	                 COALESCE(source_context_json,'{}'), disabled_until, last_checked_at, last_success_at, updated_at
	            FROM t_activity_source_state
	           WHERE source_key = ?`, sourceKey)
	state, err := scanActivitySourceState(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceState{}, false, nil
		}
		return SourceState{}, false, err
	}
	return state, true, nil
}

func (r *Repository) UpsertActivityEvent(ctx context.Context, ev ActivityEvent) (int64, bool, error) {
	if r.db == nil {
		return 0, false, errors.New("activity repository: no db attached")
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = r.now()
	}
	if ev.UpdatedAt.IsZero() {
		ev.UpdatedAt = ev.CreatedAt
	}
	if ev.EventVersion <= 0 {
		ev.EventVersion = 1
	}
	if ev.EventStatus == "" {
		ev.EventStatus = EventStatusActive
	}
	if ev.ReviewStatus == "" {
		ev.ReviewStatus = ReviewPending
	}
	if ev.ParserVersion == "" {
		ev.ParserVersion = "activity-parser-v1"
	}
	if len(ev.SourceContextJSON) == 0 {
		ev.SourceContextJSON = json.RawMessage(`{}`)
	}
	if len(ev.ParserWarningsJSON) == 0 {
		ev.ParserWarningsJSON = json.RawMessage(`[]`)
	}
	if len(ev.RewardPoolsJSON) == 0 {
		ev.RewardPoolsJSON = json.RawMessage(`[]`)
	}
	if len(ev.TaskConditionsJSON) == 0 {
		ev.TaskConditionsJSON = json.RawMessage(`[]`)
	}
	if len(ev.EligibilityRulesJSON) == 0 {
		ev.EligibilityRulesJSON = json.RawMessage(`[]`)
	}
	if len(ev.RichFieldsSummaryJSON) == 0 {
		ev.RichFieldsSummaryJSON = json.RawMessage(`{}`)
	}
	targetSymbols, err := json.Marshal(ev.TargetSymbols)
	if err != nil {
		return 0, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO t_activity_event
		   (raw_evidence_id, platform, source_group, source_external_id, source_url, title, activity_type,
		    target_symbols_json, reward_pool_text, reward_pool_usd_estimate, reward_pool_primary_token,
		    reward_pool_parse_confidence, has_reward_pool, start_time, end_time, publish_time,
		    raw_time_text, raw_timezone_hint, time_parse_confidence, content_text, content_hash, dedupe_key,
		    confidence_score, needs_human_review, auto_push_allowed, event_status, review_status,
		    ops_decision_stale, event_version, parser_version, source_context_json, parser_warnings_json,
		    reward_pools_json, task_conditions_json, eligibility_rules_json, rich_fields_summary_json,
		    created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   id = LAST_INSERT_ID(id),
		   raw_evidence_id = VALUES(raw_evidence_id),
		   source_url = VALUES(source_url),
		   title = VALUES(title),
		   activity_type = VALUES(activity_type),
		   target_symbols_json = VALUES(target_symbols_json),
		   reward_pool_text = VALUES(reward_pool_text),
		   reward_pool_usd_estimate = VALUES(reward_pool_usd_estimate),
		   reward_pool_primary_token = VALUES(reward_pool_primary_token),
		   reward_pool_parse_confidence = VALUES(reward_pool_parse_confidence),
		   has_reward_pool = VALUES(has_reward_pool),
		   start_time = VALUES(start_time),
		   end_time = VALUES(end_time),
		   publish_time = VALUES(publish_time),
		   raw_time_text = VALUES(raw_time_text),
		   raw_timezone_hint = VALUES(raw_timezone_hint),
		   time_parse_confidence = VALUES(time_parse_confidence),
		   content_text = VALUES(content_text),
		   ops_decision_stale = IF(content_hash <> VALUES(content_hash), 1, ops_decision_stale),
		   event_version = IF(content_hash <> VALUES(content_hash), event_version + 1, event_version),
		   content_hash = VALUES(content_hash),
		   confidence_score = VALUES(confidence_score),
		   needs_human_review = VALUES(needs_human_review),
		   auto_push_allowed = VALUES(auto_push_allowed),
		   event_status = VALUES(event_status),
		   parser_version = VALUES(parser_version),
		   source_context_json = VALUES(source_context_json),
		   parser_warnings_json = VALUES(parser_warnings_json),
		   reward_pools_json = VALUES(reward_pools_json),
		   task_conditions_json = VALUES(task_conditions_json),
		   eligibility_rules_json = VALUES(eligibility_rules_json),
		   rich_fields_summary_json = VALUES(rich_fields_summary_json),
		   updated_at = VALUES(updated_at)`,
		nullInt64(ev.RawEvidenceID), ev.Platform, ev.SourceGroup, nullString(ev.SourceExternalID), nullString(ev.SourceURL), ev.Title, ev.ActivityType,
		targetSymbols, nullString(ev.RewardPoolText), ev.RewardPoolUSDEstimate, nullString(ev.RewardPoolPrimaryToken),
		nullString(ev.RewardPoolParseConfidence), boolInt(ev.HasRewardPool), ev.StartTime, ev.EndTime, ev.PublishTime,
		nullString(ev.RawTimeText), nullString(ev.RawTimezoneHint), nullString(ev.TimeParseConfidence), nullString(ev.ContentText), ev.ContentHash, ev.DedupeKey,
		ev.ConfidenceScore, boolInt(ev.NeedsHumanReview), boolInt(ev.AutoPushAllowed), ev.EventStatus, ev.ReviewStatus,
		boolInt(ev.OpsDecisionStale), ev.EventVersion, ev.ParserVersion, ev.SourceContextJSON, ev.ParserWarningsJSON,
		ev.RewardPoolsJSON, ev.TaskConditionsJSON, ev.EligibilityRulesJSON, ev.RichFieldsSummaryJSON,
		ev.CreatedAt, ev.UpdatedAt,
	)
	if err != nil {
		return 0, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM t_activity_event_symbol WHERE event_id = ?`, id); err != nil {
		return 0, false, err
	}
	for _, sym := range ev.TargetSymbols {
		if strings.TrimSpace(sym.CanonicalSymbol) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO t_activity_event_symbol
			   (event_id, canonical_symbol, display_symbol, market_surface, role, sort_order, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON DUPLICATE KEY UPDATE
			   display_symbol = VALUES(display_symbol),
			   sort_order = VALUES(sort_order),
			   updated_at = VALUES(updated_at)`,
			id, sym.CanonicalSymbol, nullString(sym.DisplaySymbol), sym.MarketSurface, sym.Role, sym.SortOrder, ev.UpdatedAt, ev.UpdatedAt,
		); err != nil {
			return 0, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, true, nil
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
	query := `SELECT id, COALESCE(raw_evidence_id,0), platform, source_group, COALESCE(source_external_id,''), COALESCE(source_url,''), title, activity_type,
	                 COALESCE(content_text,''), COALESCE(reward_pool_text,''), start_time, end_time, publish_time, COALESCE(raw_time_text,''),
	                 content_hash, dedupe_key, needs_human_review, auto_push_allowed, event_status,
	                 review_status, COALESCE(ops_decision_action,''), ops_decision_stale,
	                 event_version, parser_version, COALESCE(parser_warnings_json,'[]'), COALESCE(rich_fields_summary_json,'{}'), created_at, updated_at
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
	row := r.db.QueryRowContext(ctx, `SELECT id, COALESCE(raw_evidence_id,0), platform, source_group, COALESCE(source_external_id,''), COALESCE(source_url,''), title, activity_type,
	                 COALESCE(content_text,''), COALESCE(reward_pool_text,''), start_time, end_time, publish_time, COALESCE(raw_time_text,''),
	                 content_hash, dedupe_key, needs_human_review, auto_push_allowed, event_status,
	                 review_status, COALESCE(ops_decision_action,''), ops_decision_stale,
	                 event_version, parser_version, COALESCE(parser_warnings_json,'[]'), COALESCE(rich_fields_summary_json,'{}'), created_at, updated_at
	            FROM t_activity_event WHERE id = ?`, id)
	ev, err := scanEventSummary(row)
	if err != nil {
		return ActivityEvent{}, nil, nil, err
	}
	symbols, err := r.loadEventSymbols(ctx, id)
	if err != nil {
		return ActivityEvent{}, nil, nil, err
	}
	raw, err := r.loadRawEvidenceRefs(ctx, ev)
	if err != nil {
		return ActivityEvent{}, nil, nil, err
	}
	return ev, symbols, raw, nil
}

func (r *Repository) ListActivitySourceHealth(ctx context.Context, platform, status string, enabled *bool) ([]SourceState, error) {
	if r.db == nil {
		return nil, errors.New("activity repository: no db attached")
	}
	query := `SELECT id, platform, source_group, source_type, COALESCE(source_url,''), source_key, fetch_mode,
	                 evidence_quality, enabled, poll_interval_seconds, auto_push_enabled, requires_proxy, requires_browser_context,
	                 requires_login, personalized, source_status, last_http_status, COALESCE(last_error_kind,''),
	                 COALESCE(last_schema_hash,''), COALESCE(last_content_hash,''), sample_count, event_count,
	                 COALESCE(source_context_json,'{}'), disabled_until, last_checked_at, last_success_at, updated_at
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
		s, err := scanActivitySourceState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type activitySourceStateScanner interface {
	Scan(dest ...any) error
}

func scanActivitySourceState(scanner activitySourceStateScanner) (SourceState, error) {
	var s SourceState
	var enabledInt, autoPush, requiresProxy, requiresBrowser, requiresLogin, personalized int
	var httpStatus sql.NullInt64
	var disabled, lastChecked, lastSuccess sql.NullTime
	if err := scanner.Scan(&s.ID, &s.Platform, &s.SourceGroup, &s.SourceType, &s.SourceURL, &s.SourceKey, &s.FetchMode,
		&s.EvidenceQuality, &enabledInt, &s.PollIntervalSeconds, &autoPush, &requiresProxy, &requiresBrowser, &requiresLogin, &personalized,
		&s.SourceStatus, &httpStatus, &s.LastErrorKind, &s.LastSchemaHash, &s.LastContentHash, &s.SampleCount, &s.EventCount,
		&s.SourceContextJSON, &disabled, &lastChecked, &lastSuccess, &s.UpdatedAt); err != nil {
		return SourceState{}, err
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
	if lastChecked.Valid {
		t := lastChecked.Time.UTC()
		s.LastCheckedAt = &t
	}
	if lastSuccess.Valid {
		t := lastSuccess.Time.UTC()
		s.LastSuccessAt = &t
	}
	return s, nil
}

func (r *Repository) ListActivityDeliveries(ctx context.Context, filter DeliveryFilter) ([]DeliveryOutbox, string, error) {
	if r.db == nil {
		return nil, "", errors.New("activity repository: no db attached")
	}
	limit := clampLimit(filter.Limit, 50, 200)
	query := `SELECT id, event_type, event_id, event_version, dedupe_key, target_channel, status, attempt_count, max_attempts,
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
		d, err := scanDeliveryOutboxRow(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, d)
	}
	return out, "", rows.Err()
}

func (r *Repository) ListOutboxCandidateEvents(ctx context.Context, limit int) ([]ActivityEvent, error) {
	return r.listOutboxCandidateEvents(ctx, "", "", limit)
}

func (r *Repository) ListOutboxCandidateEventsBySource(ctx context.Context, platform, sourceGroup string, limit int) ([]ActivityEvent, error) {
	return r.listOutboxCandidateEvents(ctx, platform, sourceGroup, limit)
}

func (r *Repository) listOutboxCandidateEvents(ctx context.Context, platform, sourceGroup string, limit int) ([]ActivityEvent, error) {
	if r.db == nil {
		return nil, errors.New("activity repository: no db attached")
	}
	limit = clampLimit(limit, 10, 200)
	query := `SELECT e.id, COALESCE(e.raw_evidence_id,0), e.platform, e.source_group, COALESCE(e.source_external_id,''), COALESCE(e.source_url,''), e.title, e.activity_type,
	                 COALESCE(e.content_text,''), COALESCE(e.reward_pool_text,''), e.start_time, e.end_time, e.publish_time, COALESCE(e.raw_time_text,''),
	                 e.content_hash, e.dedupe_key, e.needs_human_review, e.auto_push_allowed, e.event_status,
	                 e.review_status, COALESCE(e.ops_decision_action,''), e.ops_decision_stale,
	                 e.event_version, e.parser_version, COALESCE(e.parser_warnings_json,'[]'), COALESCE(e.rich_fields_summary_json,'{}'), e.created_at, e.updated_at
	            FROM t_activity_event e
	           WHERE e.event_status = ?
	             AND e.review_status <> ?
	             AND (e.auto_push_allowed = 1 OR e.review_status = ? OR e.needs_human_review = 1)`
	args := []any{EventStatusActive, ReviewRejected, ReviewApproved}
	if strings.TrimSpace(platform) != "" {
		query += `
	             AND e.platform = ?`
		args = append(args, strings.ToLower(strings.TrimSpace(platform)))
	}
	if strings.TrimSpace(sourceGroup) != "" {
		query += `
	             AND e.source_group = ?`
		args = append(args, strings.TrimSpace(sourceGroup))
	}
	query += `
	             AND NOT EXISTS (
	               SELECT 1
	                 FROM t_activity_delivery_outbox o
	                WHERE o.dedupe_key = CASE
	                  WHEN e.needs_human_review = 1 AND e.review_status <> 'approved'
	                    THEN CONCAT('activity_review_required|', e.id, '|v', e.event_version)
	                  ELSE CONCAT('activity_event_alert|', e.id, '|v', e.event_version)
	                END
	             )
	           ORDER BY e.updated_at DESC, e.id DESC
	           LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityEvent{}
	for rows.Next() {
		ev, err := scanEventSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (r *Repository) InsertActivityOutbox(ctx context.Context, row DeliveryOutbox) error {
	if r.db == nil {
		return errors.New("activity repository: no db attached")
	}
	if row.TargetChannel == "" {
		row.TargetChannel = DeliveryChannelLarkActivity
	}
	if row.MaxAttempts <= 0 {
		row.MaxAttempts = 5
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = r.now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_activity_delivery_outbox
		   (event_type, event_id, event_version, dedupe_key, target_channel, status, attempt_count, max_attempts, next_attempt_at, payload_json, last_error, sent_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   event_id = COALESCE(VALUES(event_id), event_id),
		   event_version = COALESCE(VALUES(event_version), event_version),
		   payload_json = IF(status = 'sent', payload_json, VALUES(payload_json)),
		   status = IF(status IN ('sent','pending','retry'), status, VALUES(status)),
		   next_attempt_at = IF(status = 'sent', next_attempt_at, VALUES(next_attempt_at)),
		   updated_at = IF(status = 'sent', updated_at, VALUES(updated_at))`,
		row.EventType, nullInt64(row.EventID), nullInt(row.EventVersion), row.DedupeKey, row.TargetChannel, row.Status, row.AttemptCount, row.MaxAttempts,
		row.NextAttemptAt, row.PayloadJSON, nullString(row.LastError), row.SentAt, row.CreatedAt, row.UpdatedAt,
	)
	return err
}

func (r *Repository) LoadDueActivityOutbox(ctx context.Context, now time.Time, limit int) ([]DeliveryOutbox, error) {
	if r.db == nil {
		return nil, errors.New("activity repository: no db attached")
	}
	limit = clampLimit(limit, 50, 200)
	rows, err := r.db.QueryContext(ctx, `SELECT id, event_type, event_id, event_version, dedupe_key, target_channel, status, attempt_count, max_attempts,
	                 next_attempt_at, payload_json, COALESCE(last_error,''), sent_at, created_at, updated_at
	            FROM t_activity_delivery_outbox
	           WHERE status IN (?, ?, ?)
	             AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
	           ORDER BY next_attempt_at ASC, id ASC
	           LIMIT ?`,
		DeliveryStatusPending, DeliveryStatusRetry, DeliveryStatusRedrivePending, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryOutbox{}
	for rows.Next() {
		row, err := scanDeliveryOutboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *Repository) MarkActivityOutboxDisabledNoWebhook(ctx context.Context, id int64, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_activity_delivery_outbox
		    SET status = ?, last_error = ?, updated_at = ?
		  WHERE id = ?`,
		DeliveryStatusDisabledNoWebhook, "webhook url not configured", now, id,
	)
	return err
}

func (r *Repository) UpdateActivityOutboxAfterSend(ctx context.Context, id int64, status string, attempt int, nextAttempt time.Time, lastErr string, now time.Time, sent bool) error {
	var sentAt sql.NullTime
	if sent {
		sentAt = sql.NullTime{Time: now, Valid: true}
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_activity_delivery_outbox
		    SET status = ?,
		        attempt_count = ?,
		        next_attempt_at = ?,
		        last_error = ?,
		        sent_at = ?,
		        updated_at = ?
		  WHERE id = ?`,
		status, attempt, nextAttempt, nullString(lastErr), sentAt, now, id,
	)
	return err
}

func (r *Repository) RecordActivityDeliveryAttempt(ctx context.Context, outboxID int64, attempt int, status string, httpStatus *int, errMsg, responseBody string, attemptedAt time.Time) error {
	var statusValue any
	if httpStatus != nil {
		statusValue = *httpStatus
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_activity_delivery_attempt
		   (outbox_id, attempt_no, status, http_status, error_message, response_body, latency_ms, attempted_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
		 ON DUPLICATE KEY UPDATE
		   status = VALUES(status),
		   http_status = VALUES(http_status),
		   error_message = VALUES(error_message),
		   response_body = VALUES(response_body),
		   attempted_at = VALUES(attempted_at),
		   updated_at = UTC_TIMESTAMP(3)`,
		outboxID, attempt, status, statusValue, nullString(errMsg), nullString(responseBody), 0, attemptedAt,
	)
	return err
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
	var eventStatus string
	var needsReview, autoPush, stale int
	var start, end, publish sql.NullTime
	var parserWarnings, richFields []byte
	if err := row.Scan(&ev.ID, &ev.RawEvidenceID, &ev.Platform, &ev.SourceGroup, &sourceExternalID, &sourceURL, &ev.Title, &ev.ActivityType,
		&ev.ContentText, &ev.RewardPoolText, &start, &end, &publish, &ev.RawTimeText,
		&ev.ContentHash, &ev.DedupeKey, &needsReview, &autoPush, &eventStatus,
		&ev.ReviewStatus, &ops, &stale, &ev.EventVersion, &ev.ParserVersion, &parserWarnings, &richFields, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
		return ActivityEvent{}, err
	}
	ev.SourceExternalID = sourceExternalID
	ev.SourceURL = sourceURL
	ev.NeedsHumanReview = needsReview != 0
	ev.AutoPushAllowed = autoPush != 0
	ev.EventStatus = eventStatus
	ev.OpsDecisionAction = ops
	ev.OpsDecisionStale = stale != 0
	if start.Valid {
		t := start.Time.UTC()
		ev.StartTime = &t
	}
	if end.Valid {
		t := end.Time.UTC()
		ev.EndTime = &t
	}
	if publish.Valid {
		t := publish.Time.UTC()
		ev.PublishTime = &t
	}
	if len(parserWarnings) > 0 {
		ev.ParserWarningsJSON = json.RawMessage(parserWarnings)
	}
	if len(richFields) > 0 {
		ev.RichFieldsSummaryJSON = json.RawMessage(richFields)
	}
	return ev, nil
}

func (r *Repository) loadRawEvidenceRefs(ctx context.Context, ev ActivityEvent) ([]RawEvidence, error) {
	where := ""
	args := []any{}
	if ev.RawEvidenceID > 0 {
		where = "id = ?"
		args = append(args, ev.RawEvidenceID)
	} else {
		where = "platform = ? AND source_group = ?"
		args = append(args, ev.Platform, ev.SourceGroup)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, source_key, platform, source_group, COALESCE(source_url,''), fetch_mode,
	             COALESCE(payload_hash,''), LEFT(COALESCE(payload_preview,''), 8000), payload_size_bytes, payload_truncated,
	             COALESCE(schema_hash,''), COALESCE(content_hash,''), fetched_at
	        FROM t_activity_raw_evidence
	       WHERE `+where+`
	       ORDER BY fetched_at DESC, id DESC
	       LIMIT 5`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RawEvidence{}
	for rows.Next() {
		var raw RawEvidence
		var truncated int
		if err := rows.Scan(&raw.ID, &raw.SourceKey, &raw.Platform, &raw.SourceGroup, &raw.SourceURL, &raw.FetchMode,
			&raw.PayloadHash, &raw.PayloadPreview, &raw.PayloadSizeBytes, &truncated,
			&raw.SchemaHash, &raw.ContentHash, &raw.FetchedAt); err != nil {
			return nil, err
		}
		raw.PayloadTruncated = truncated != 0
		out = append(out, raw)
	}
	return out, rows.Err()
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

func scanDeliveryOutboxRow(rows *sql.Rows) (DeliveryOutbox, error) {
	var d DeliveryOutbox
	var next, sent sql.NullTime
	var eventID, eventVersion sql.NullInt64
	if err := rows.Scan(&d.ID, &d.EventType, &eventID, &eventVersion, &d.DedupeKey, &d.TargetChannel, &d.Status, &d.AttemptCount, &d.MaxAttempts,
		&next, &d.PayloadJSON, &d.LastError, &sent, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return DeliveryOutbox{}, err
	}
	if eventID.Valid {
		d.EventID = eventID.Int64
	}
	if eventVersion.Valid {
		d.EventVersion = int(eventVersion.Int64)
	}
	if next.Valid {
		t := next.Time.UTC()
		d.NextAttemptAt = &t
	}
	if sent.Valid {
		t := sent.Time.UTC()
		d.SentAt = &t
	}
	return d, nil
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

func nullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullInt(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
