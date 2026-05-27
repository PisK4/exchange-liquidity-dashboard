package listing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/listing/announcement"
)

// Thin aliases so call sites in this file stay short while the
// announcement package remains the canonical home for the parsed
// shapes.
type (
	annParsed = announcementParsedAdapter
	annSymbol = announcement.ParsedAnnouncementSymbol
)

// announcementParsedAdapter projects announcement.ParsedAnnouncement
// onto the fields the repository writes. The adapter keeps optional
// future fields (e.g. effective listing time once added to the parser
// output) explicit rather than relying on the parser struct shape.
type announcementParsedAdapter struct {
	Platform             string
	AnnouncementID       string
	URL                  string
	Title                string
	Description          string
	Category             string
	Language             string
	PublishedAt          *time.Time
	UpdatedAt            *time.Time
	EffectiveListingTime *time.Time
	ParseConfidence      string
	RawPayloadJSON       json.RawMessage
	RawPayloadHash       string
	ParserVersion        string
}

func newAnnouncementParsed(a announcement.ParsedAnnouncement) annParsed {
	pv := a.RawPayloadHash // unused; kept to keep parser-side hash visible
	_ = pv
	return annParsed{
		Platform:        a.Platform,
		AnnouncementID:  a.AnnouncementID,
		URL:             a.URL,
		Title:           a.Title,
		Description:     a.Description,
		Category:        a.Category,
		Language:        a.Language,
		PublishedAt:     a.PublishedAt,
		UpdatedAt:       a.UpdatedAt,
		ParseConfidence: a.ParseConfidence,
		RawPayloadJSON:  a.RawPayloadJSON,
		RawPayloadHash:  a.RawPayloadHash,
		ParserVersion:   announcement.ParserVersion,
	}
}

// Repository is the MySQL-backed read/write gateway for every Listing
// Agent table. It is intentionally tiny: there is one method per
// concrete table action; higher-level fusion / scoring / delivery code
// composes those primitives in their own files.
//
// All methods take an explicit context.Context so callers can layer
// run-once timeouts without cooperating with the repository.
type Repository struct {
	db  *sql.DB
	now func() time.Time
}

// NewRepository wires a Repository over an existing *sql.DB. The
// database lifecycle (open, ping, close) stays with the caller so the
// dashboard's existing collector.OpenMySQL plumbing keeps owning it.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// DB returns the underlying *sql.DB. Tests outside the package use it
// to clean state between fixtures.
func (r *Repository) DB() *sql.DB { return r.db }

// UpsertCandidate inserts or updates a candidate row keyed by
// (canonical_symbol, market_surface, instrument_kind). Returns the
// resulting candidate id. New candidates anchor first_observed_at to
// observed_at; existing candidates only bump last_observed_at and the
// derived fields.
func (r *Repository) UpsertCandidate(ctx context.Context, c CandidateUpsert) (int64, error) {
	if r.db == nil {
		return 0, errors.New("listing repository: no db attached")
	}
	platforms := c.SourcePlatforms
	if platforms == nil {
		platforms = []string{}
	}
	platformsJSON, err := json.Marshal(platforms)
	if err != nil {
		return 0, fmt.Errorf("marshal source_platforms: %w", err)
	}
	var top30 any
	if len(c.Top30Enrichment) > 0 {
		top30 = []byte(c.Top30Enrichment)
	}
	observedAt := c.ObservedAt
	if observedAt.IsZero() {
		observedAt = r.now()
	}
	const query = `INSERT INTO t_listing_candidate
  (canonical_symbol, display_symbol, market_surface, instrument_kind,
   lifecycle_status, lifecycle_status_label, evidence_kind, confidence_level,
   business_score, business_score_version, recommendation, recommendation_label,
   source_platforms_json, top30_enrichment_json, first_observed_at, last_observed_at)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON DUPLICATE KEY UPDATE
   display_symbol = VALUES(display_symbol),
   lifecycle_status = VALUES(lifecycle_status),
   lifecycle_status_label = VALUES(lifecycle_status_label),
   evidence_kind = VALUES(evidence_kind),
   confidence_level = VALUES(confidence_level),
   business_score = VALUES(business_score),
   business_score_version = VALUES(business_score_version),
   recommendation = VALUES(recommendation),
   recommendation_label = VALUES(recommendation_label),
   source_platforms_json = VALUES(source_platforms_json),
   top30_enrichment_json = VALUES(top30_enrichment_json),
   last_observed_at = VALUES(last_observed_at)`
	res, err := r.db.ExecContext(ctx, query,
		c.CanonicalSymbol, nullString(c.DisplaySymbol), c.MarketSurface, c.InstrumentKind,
		c.LifecycleStatus, nullString(c.LifecycleStatusLabel), c.EvidenceKind, c.ConfidenceLevel,
		nullFloat(c.BusinessScore), nullString(c.BusinessScoreVersion),
		nullString(c.Recommendation), nullString(c.RecommendationLabel),
		platformsJSON, top30, observedAt, observedAt,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		// MySQL returns 0 from LastInsertId() when the UPDATE branch ran
		// without modifying an auto-increment column; resolve the row.
		row := r.db.QueryRowContext(ctx,
			`SELECT id FROM t_listing_candidate WHERE canonical_symbol = ? AND market_surface = ? AND instrument_kind = ?`,
			c.CanonicalSymbol, c.MarketSurface, c.InstrumentKind,
		)
		if err := row.Scan(&id); err != nil {
			return 0, fmt.Errorf("resolve candidate id: %w", err)
		}
	}
	return id, nil
}

// InsertSignal writes a SignalObservation with INSERT IGNORE keyed by
// fingerprint. Returns (id, inserted, err). When inserted=false the
// fingerprint already existed and the returned id resolves to the
// previously-stored row.
func (r *Repository) InsertSignal(ctx context.Context, s SignalObservation) (int64, bool, error) {
	if r.db == nil {
		return 0, false, errors.New("listing repository: no db attached")
	}
	payload := s.PayloadJSON
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var rawPayload any
	if len(s.RawPayloadJSON) > 0 {
		rawPayload = []byte(s.RawPayloadJSON)
	}
	observedAt := s.ObservedAt
	if observedAt.IsZero() {
		observedAt = r.now()
	}
	const query = `INSERT IGNORE INTO t_listing_signal_observation
  (signal_type, signal_subtype, source_platform, market_type, api_symbol, api_market_id,
   canonical_symbol, display_symbol, base_asset, quote_asset, settle_asset,
   market_surface, instrument_kind, status_raw, status_normalized, confidence,
   observed_at, source_snapshot_ts, published_at, listing_time_ts,
   source_endpoint, source_url, fingerprint, payload_json, raw_payload_json, raw_payload_hash)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := r.db.ExecContext(ctx, query,
		s.SignalType, nullString(s.SignalSubtype), s.SourcePlatform, nullString(s.MarketType),
		nullString(s.APISymbol), nullString(s.APIMarketID),
		s.CanonicalSymbol, nullString(s.DisplaySymbol), nullString(s.BaseAsset),
		nullString(s.QuoteAsset), nullString(s.SettleAsset),
		s.MarketSurface, s.InstrumentKind,
		nullString(s.StatusRaw), nullString(s.StatusNormalized), nullString(s.Confidence),
		observedAt, nullTimePtr(s.SourceSnapshotTS), nullTimePtr(s.PublishedAt), nullTimePtr(s.ListingTimeTS),
		nullString(s.SourceEndpoint), nullString(s.SourceURL), s.Fingerprint,
		[]byte(payload), rawPayload, nullString(s.RawPayloadHash),
	)
	if err != nil {
		return 0, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if affected == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		return id, true, nil
	}
	var existing int64
	row := r.db.QueryRowContext(ctx, `SELECT id FROM t_listing_signal_observation WHERE fingerprint = ?`, s.Fingerprint)
	if err := row.Scan(&existing); err != nil {
		return 0, false, fmt.Errorf("resolve signal id for fingerprint: %w", err)
	}
	return existing, false, nil
}

// LinkCandidateSignal records a candidate→signal relationship in
// t_listing_candidate_signal. The unique key makes the operation
// naturally idempotent.
func (r *Repository) LinkCandidateSignal(ctx context.Context, candidateID, signalID int64) error {
	if r.db == nil {
		return errors.New("listing repository: no db attached")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT IGNORE INTO t_listing_candidate_signal (candidate_id, signal_id) VALUES (?, ?)`,
		candidateID, signalID,
	)
	return err
}

// UpsertAnnouncement inserts (or updates) one t_listing_announcement
// row keyed by (platform, announcement_id). Returns the row id.
func (r *Repository) UpsertAnnouncement(ctx context.Context, parsed announcement.ParsedAnnouncement) (int64, error) {
	a := newAnnouncementParsed(parsed)
	if r.db == nil {
		return 0, errors.New("listing repository: no db attached")
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO t_listing_announcement
		   (platform, announcement_id, announcement_url, title, description, category, tags_json,
		    language, published_at, source_updated_at, parsed_market_type, effective_listing_time,
		    parse_confidence, raw_payload_json, raw_payload_hash, parser_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   announcement_url = VALUES(announcement_url),
		   title = VALUES(title),
		   description = VALUES(description),
		   category = VALUES(category),
		   tags_json = VALUES(tags_json),
		   language = VALUES(language),
		   published_at = VALUES(published_at),
		   source_updated_at = VALUES(source_updated_at),
		   parse_confidence = VALUES(parse_confidence),
		   raw_payload_json = VALUES(raw_payload_json),
		   raw_payload_hash = VALUES(raw_payload_hash),
		   parser_version = VALUES(parser_version)`,
		a.Platform, a.AnnouncementID, nullString(a.URL), a.Title, nullString(a.Description),
		nullString(a.Category), nil, nullString(a.Language),
		nullTimePtr(a.PublishedAt), nullTimePtr(a.UpdatedAt),
		nil, nullTimePtr(a.EffectiveListingTime),
		a.ParseConfidence, []byte(a.RawPayloadJSON), a.RawPayloadHash, a.ParserVersion,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if id == 0 {
		row := r.db.QueryRowContext(ctx,
			`SELECT id FROM t_listing_announcement WHERE platform = ? AND announcement_id = ?`,
			a.Platform, a.AnnouncementID)
		if err := row.Scan(&id); err != nil {
			return 0, fmt.Errorf("resolve announcement id: %w", err)
		}
	}
	return id, nil
}

// InsertAnnouncementSymbolAndSignal materialises one child symbol row
// in t_listing_announcement_symbol and emits the matching
// announcement_listing signal observation. The two writes are linked
// by the deterministic signal fingerprint so subsequent runs are
// idempotent without needing a transaction.
func (r *Repository) InsertAnnouncementSymbolAndSignal(
	ctx context.Context,
	announcementID int64,
	platform string,
	announcementExternalID string,
	sym annSymbol,
	rawPayload []byte,
	observedAt time.Time,
) (int64, bool, error) {
	if r.db == nil {
		return 0, false, errors.New("listing repository: no db attached")
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT IGNORE INTO t_listing_announcement_symbol
		   (announcement_id, canonical_symbol, display_symbol, market_surface, instrument_kind,
		    signal_subtype, listing_time_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		announcementID, sym.CanonicalSymbol, nullString(sym.DisplaySymbol),
		sym.MarketSurface, sym.InstrumentKind, sym.SignalSubtype, nullTimePtr(sym.ListingTimeTS)); err != nil {
		return 0, false, err
	}
	fingerprint := fmt.Sprintf("announcement_listing|%s|%s|%s|%s|%s",
		platform, announcementExternalID, sym.CanonicalSymbol, sym.MarketSurface, sym.InstrumentKind)
	signal := SignalObservation{
		SignalType:      SignalAnnouncementListing,
		SignalSubtype:   sym.SignalSubtype,
		SourcePlatform:  platform,
		CanonicalSymbol: sym.CanonicalSymbol,
		DisplaySymbol:   sym.DisplaySymbol,
		MarketSurface:   sym.MarketSurface,
		InstrumentKind:  sym.InstrumentKind,
		ObservedAt:      observedAt,
		ListingTimeTS:   sym.ListingTimeTS,
		Fingerprint:     fingerprint,
		PayloadJSON:     buildAnnouncementPayload(announcementID, announcementExternalID, sym),
		RawPayloadJSON:  rawPayload,
	}
	return r.InsertSignal(ctx, signal)
}

func buildAnnouncementPayload(announcementID int64, externalID string, sym annSymbol) json.RawMessage {
	payload := map[string]any{
		"announcement_id":          announcementID,
		"announcement_external_id": externalID,
		"canonical_symbol":         sym.CanonicalSymbol,
		"market_surface":           sym.MarketSurface,
		"instrument_kind":          sym.InstrumentKind,
		"signal_subtype":           sym.SignalSubtype,
	}
	if sym.ListingTimeTS != nil {
		payload["listing_time_ts"] = sym.ListingTimeTS.UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(payload)
	return b
}

// MarkSignalFused stamps fused_at on the given signal id. Used by the
// fusion worker so subsequent fusion runs do not re-process the same
// signal.
func (r *Repository) MarkSignalFused(ctx context.Context, signalID int64, fusedAt time.Time) error {
	if r.db == nil {
		return errors.New("listing repository: no db attached")
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE t_listing_signal_observation SET fused_at = ? WHERE id = ?`,
		fusedAt, signalID,
	)
	return err
}

// ListUnfusedSignals returns up to `limit` signals whose fused_at is
// NULL, ordered by observed_at ascending. Used by the fusion worker.
func (r *Repository) ListUnfusedSignals(ctx context.Context, limit int) ([]SignalObservation, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, signal_type, signal_subtype, source_platform, market_type, api_symbol, api_market_id,
		        canonical_symbol, display_symbol, base_asset, quote_asset, settle_asset,
		        market_surface, instrument_kind, status_raw, status_normalized, confidence,
		        observed_at, source_snapshot_ts, published_at, listing_time_ts,
		        source_endpoint, source_url, fingerprint, payload_json, raw_payload_json, raw_payload_hash
		   FROM t_listing_signal_observation
		  WHERE fused_at IS NULL
		  ORDER BY observed_at ASC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSignalRows(rows)
}

// ListCandidates returns candidates that match the given filter. The
// filter applies LIKE matching for Symbol and JSON_CONTAINS for
// Platform; an empty filter returns the most recent candidates.
func (r *Repository) ListCandidates(ctx context.Context, f CandidateFilter) ([]Candidate, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	var where []string
	var args []any
	where = append(where, "1 = 1")
	if f.Status != "" {
		where = append(where, "lifecycle_status = ?")
		args = append(args, f.Status)
	}
	if f.EvidenceKind != "" {
		where = append(where, "evidence_kind = ?")
		args = append(args, f.EvidenceKind)
	}
	if f.Platform != "" {
		where = append(where, "JSON_CONTAINS(source_platforms_json, JSON_QUOTE(?))")
		args = append(args, f.Platform)
	}
	if f.Symbol != "" {
		where = append(where, "canonical_symbol LIKE ?")
		args = append(args, "%"+strings.ToUpper(f.Symbol)+"%")
	}
	query := `SELECT id, canonical_symbol, display_symbol, market_surface, instrument_kind,
	         lifecycle_status, lifecycle_status_label, evidence_kind, confidence_level,
	         business_score, business_score_version, recommendation, recommendation_label,
	         source_platforms_json, top30_enrichment_json, first_observed_at, last_observed_at
	    FROM t_listing_candidate
	   WHERE ` + strings.Join(where, " AND ") + `
	   ORDER BY last_observed_at DESC
	   LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCandidateRows(rows)
}

// GetCandidate fetches a single candidate by id.
func (r *Repository) GetCandidate(ctx context.Context, id int64) (Candidate, error) {
	if r.db == nil {
		return Candidate{}, errors.New("listing repository: no db attached")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, canonical_symbol, display_symbol, market_surface, instrument_kind,
		         lifecycle_status, lifecycle_status_label, evidence_kind, confidence_level,
		         business_score, business_score_version, recommendation, recommendation_label,
		         source_platforms_json, top30_enrichment_json, first_observed_at, last_observed_at
		    FROM t_listing_candidate
		   WHERE id = ?`, id)
	c, err := scanCandidateRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, sql.ErrNoRows
	}
	return c, err
}

// ListCandidateSignals returns the SignalObservation rows linked to
// the candidate. includeRaw controls whether the raw payload column
// is materialised; the read-only API returns the raw payload only on
// the detail endpoints.
func (r *Repository) ListCandidateSignals(ctx context.Context, candidateID int64, includeRaw bool) ([]SignalObservation, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	rawCol := "NULL AS raw_payload_json"
	if includeRaw {
		rawCol = "s.raw_payload_json"
	}
	query := `SELECT s.id, s.signal_type, s.signal_subtype, s.source_platform, s.market_type,
	         s.api_symbol, s.api_market_id, s.canonical_symbol, s.display_symbol,
	         s.base_asset, s.quote_asset, s.settle_asset,
	         s.market_surface, s.instrument_kind, s.status_raw, s.status_normalized, s.confidence,
	         s.observed_at, s.source_snapshot_ts, s.published_at, s.listing_time_ts,
	         s.source_endpoint, s.source_url, s.fingerprint, s.payload_json, ` + rawCol + `, s.raw_payload_hash
	    FROM t_listing_signal_observation s
	    JOIN t_listing_candidate_signal cs ON cs.signal_id = s.id
	   WHERE cs.candidate_id = ?
	   ORDER BY s.observed_at DESC`
	rows, err := r.db.QueryContext(ctx, query, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSignalRows(rows)
}

// ListSourceHealth returns every row from t_listing_source_state. The
// API exposes this verbatim so operators can quickly see which sources
// are stale / drift / disabled.
func (r *Repository) ListSourceHealth(ctx context.Context) ([]SourceState, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT source_key, source_type, platform, status,
		         last_success_at, last_error_at, consecutive_error_count, schema_drift_count,
		         disabled_until, last_error, updated_at
		    FROM t_listing_source_state
		   ORDER BY source_key ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceState
	for rows.Next() {
		var s SourceState
		var lastSuccess, lastError, disabledUntil sql.NullTime
		var lastErr sql.NullString
		if err := rows.Scan(&s.SourceKey, &s.SourceType, &s.Platform, &s.Status,
			&lastSuccess, &lastError, &s.ConsecutiveErrorCount, &s.SchemaDriftCount,
			&disabledUntil, &lastErr, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if lastSuccess.Valid {
			t := lastSuccess.Time
			s.LastSuccessAt = &t
		}
		if lastError.Valid {
			t := lastError.Time
			s.LastErrorAt = &t
		}
		if disabledUntil.Valid {
			t := disabledUntil.Time
			s.DisabledUntil = &t
		}
		if lastErr.Valid {
			s.LastError = lastErr.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpsertSourceState writes the latest health for a single source key.
func (r *Repository) UpsertSourceState(ctx context.Context, s SourceState) error {
	if r.db == nil {
		return errors.New("listing repository: no db attached")
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = r.now()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_listing_source_state
		   (source_key, source_type, platform, status,
		    last_success_at, last_error_at, consecutive_error_count, schema_drift_count,
		    disabled_until, last_error, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   status = VALUES(status),
		   last_success_at = COALESCE(VALUES(last_success_at), last_success_at),
		   last_error_at = COALESCE(VALUES(last_error_at), last_error_at),
		   consecutive_error_count = VALUES(consecutive_error_count),
		   schema_drift_count = VALUES(schema_drift_count),
		   disabled_until = VALUES(disabled_until),
		   last_error = VALUES(last_error),
		   updated_at = VALUES(updated_at)`,
		s.SourceKey, s.SourceType, s.Platform, s.Status,
		nullTimePtr(s.LastSuccessAt), nullTimePtr(s.LastErrorAt),
		s.ConsecutiveErrorCount, s.SchemaDriftCount,
		nullTimePtr(s.DisabledUntil), nullString(s.LastError), s.UpdatedAt,
	)
	return err
}

// ListDeliveries returns outbox rows that match the given filter. A
// recent attempt summary is attached when available; callers should
// surface this on the read-only /api/listing/deliveries endpoint.
func (r *Repository) ListDeliveries(ctx context.Context, f DeliveryFilter) ([]DeliveryOutbox, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	var where []string
	var args []any
	where = append(where, "1 = 1")
	if f.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, f.EventType)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, event_type, dedupe_key, target_channel, status, attempt_count, max_attempts,
		         next_attempt_at, payload_json, last_error, sent_at, created_at, updated_at
		    FROM t_listing_delivery_outbox
		   WHERE `+strings.Join(where, " AND ")+`
		   ORDER BY created_at DESC
		   LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryOutbox
	for rows.Next() {
		o, err := scanOutboxRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// scanCandidateRow is a generic row reader used by both
// QueryRowContext and QueryContext loops.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanCandidateRow(s rowScanner) (Candidate, error) {
	var c Candidate
	var display, lifecycleLabel, scoreVersion, recommendation, recommendationLabel sql.NullString
	var businessScore sql.NullFloat64
	var platforms []byte
	var top30 []byte
	if err := s.Scan(&c.ID, &c.CanonicalSymbol, &display, &c.MarketSurface, &c.InstrumentKind,
		&c.LifecycleStatus, &lifecycleLabel, &c.EvidenceKind, &c.ConfidenceLevel,
		&businessScore, &scoreVersion, &recommendation, &recommendationLabel,
		&platforms, &top30, &c.FirstObservedAt, &c.LastObservedAt); err != nil {
		return Candidate{}, err
	}
	c.DisplaySymbol = display.String
	c.LifecycleStatusLabel = lifecycleLabel.String
	c.BusinessScoreVersion = scoreVersion.String
	c.Recommendation = recommendation.String
	c.RecommendationLabel = recommendationLabel.String
	if businessScore.Valid {
		v := businessScore.Float64
		c.BusinessScore = &v
	}
	if len(platforms) > 0 {
		_ = json.Unmarshal(platforms, &c.SourcePlatforms)
	}
	if len(top30) > 0 {
		c.Top30Enrichment = append(json.RawMessage(nil), top30...)
	}
	return c, nil
}

func scanCandidateRows(rows *sql.Rows) ([]Candidate, error) {
	var out []Candidate
	for rows.Next() {
		c, err := scanCandidateRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanSignalRows(rows *sql.Rows) ([]SignalObservation, error) {
	var out []SignalObservation
	for rows.Next() {
		var s SignalObservation
		var subtype, marketType, apiSymbol, apiMarketID, displaySymbol, baseAsset, quoteAsset, settleAsset sql.NullString
		var statusRaw, statusNormalized, confidence, sourceEndpoint, sourceURL, rawHash sql.NullString
		var sourceSnapshotTS, publishedAt, listingTimeTS sql.NullTime
		var payload, rawPayload []byte
		if err := rows.Scan(&s.ID, &s.SignalType, &subtype, &s.SourcePlatform, &marketType,
			&apiSymbol, &apiMarketID, &s.CanonicalSymbol, &displaySymbol,
			&baseAsset, &quoteAsset, &settleAsset,
			&s.MarketSurface, &s.InstrumentKind, &statusRaw, &statusNormalized, &confidence,
			&s.ObservedAt, &sourceSnapshotTS, &publishedAt, &listingTimeTS,
			&sourceEndpoint, &sourceURL, &s.Fingerprint, &payload, &rawPayload, &rawHash); err != nil {
			return nil, err
		}
		s.SignalSubtype = subtype.String
		s.MarketType = marketType.String
		s.APISymbol = apiSymbol.String
		s.APIMarketID = apiMarketID.String
		s.DisplaySymbol = displaySymbol.String
		s.BaseAsset = baseAsset.String
		s.QuoteAsset = quoteAsset.String
		s.SettleAsset = settleAsset.String
		s.StatusRaw = statusRaw.String
		s.StatusNormalized = statusNormalized.String
		s.Confidence = confidence.String
		s.SourceEndpoint = sourceEndpoint.String
		s.SourceURL = sourceURL.String
		s.RawPayloadHash = rawHash.String
		if sourceSnapshotTS.Valid {
			t := sourceSnapshotTS.Time
			s.SourceSnapshotTS = &t
		}
		if publishedAt.Valid {
			t := publishedAt.Time
			s.PublishedAt = &t
		}
		if listingTimeTS.Valid {
			t := listingTimeTS.Time
			s.ListingTimeTS = &t
		}
		if len(payload) > 0 {
			s.PayloadJSON = append(json.RawMessage(nil), payload...)
		}
		if len(rawPayload) > 0 {
			s.RawPayloadJSON = append(json.RawMessage(nil), rawPayload...)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanOutboxRow(rows *sql.Rows) (DeliveryOutbox, error) {
	var o DeliveryOutbox
	var nextAttempt, sentAt sql.NullTime
	var lastErr sql.NullString
	var payload []byte
	if err := rows.Scan(&o.ID, &o.EventType, &o.DedupeKey, &o.TargetChannel, &o.Status,
		&o.AttemptCount, &o.MaxAttempts, &nextAttempt, &payload, &lastErr, &sentAt,
		&o.CreatedAt, &o.UpdatedAt); err != nil {
		return DeliveryOutbox{}, err
	}
	if nextAttempt.Valid {
		t := nextAttempt.Time
		o.NextAttemptAt = &t
	}
	if sentAt.Valid {
		t := sentAt.Time
		o.SentAt = &t
	}
	o.LastError = lastErr.String
	if len(payload) > 0 {
		o.PayloadJSON = append(json.RawMessage(nil), payload...)
	}
	return o, nil
}

// --- helpers ---

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullTimePtr(p *time.Time) any {
	if p == nil || p.IsZero() {
		return nil
	}
	return *p
}
