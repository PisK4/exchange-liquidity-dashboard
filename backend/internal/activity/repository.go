package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
