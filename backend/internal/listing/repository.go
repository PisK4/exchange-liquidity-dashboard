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

// LoadSourceState reads the current row for source_key, or nil if no
// row exists yet (i.e. the source has not produced a health entry).
// The wrapper Phase 1.3 adds uses the nil return as the
// "first observation" branch when bootstrapping a new source.
func (r *Repository) LoadSourceState(ctx context.Context, sourceKey string) (*SourceState, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	const query = `SELECT source_key, source_type, platform, status,
	  last_success_at, last_error_at, consecutive_error_count, schema_drift_count,
	  disabled_until, last_error, updated_at
	  FROM t_listing_source_state
	  WHERE source_key = ?
	  LIMIT 1`
	rows, err := r.db.QueryContext(ctx, query, sourceKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
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
	return &s, rows.Err()
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

// UpsertInstrumentSnapshot writes the latest normalized view of one
// exchange instrument to t_listing_instrument_snapshot. The unique
// key (platform, market_type, api_symbol) carries first_seen_at
// forward across updates; previous_seen_at is rolled from the prior
// last_seen_at so the audit trail keeps the two most recent ticks.
//
// The caller (instrument poller) is responsible for loading the prior
// snapshot first, computing the diff, and only then upserting; this
// helper does NOT emit signals.
func (r *Repository) UpsertInstrumentSnapshot(ctx context.Context, s InstrumentSnapshot) error {
	if r.db == nil {
		return errors.New("listing repository: no db attached")
	}
	if s.LastSeenAt.IsZero() {
		s.LastSeenAt = r.now()
	}
	if s.FirstSeenAt.IsZero() {
		s.FirstSeenAt = s.LastSeenAt
	}
	if len(s.RawJSON) == 0 {
		s.RawJSON = json.RawMessage(`{}`)
	}
	const query = `INSERT INTO t_listing_instrument_snapshot
	  (platform, market_type, api_symbol, api_market_id, display_symbol, canonical_symbol,
	   base_asset, quote_asset, settle_asset, market_surface, instrument_kind, contract_type,
	   status_raw, status_normalized, status_field_name, listing_time_ts, listing_time_field_name,
	   delist_flag, first_seen_at, last_seen_at, raw_json, raw_json_hash, normalizer_version)
	  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	  ON DUPLICATE KEY UPDATE
	    api_market_id = VALUES(api_market_id),
	    display_symbol = VALUES(display_symbol),
	    canonical_symbol = VALUES(canonical_symbol),
	    base_asset = VALUES(base_asset),
	    quote_asset = VALUES(quote_asset),
	    settle_asset = VALUES(settle_asset),
	    market_surface = VALUES(market_surface),
	    instrument_kind = VALUES(instrument_kind),
	    contract_type = VALUES(contract_type),
	    status_raw = VALUES(status_raw),
	    status_normalized = VALUES(status_normalized),
	    status_field_name = VALUES(status_field_name),
	    listing_time_ts = VALUES(listing_time_ts),
	    listing_time_field_name = VALUES(listing_time_field_name),
	    delist_flag = VALUES(delist_flag),
	    previous_seen_at = last_seen_at,
	    last_seen_at = VALUES(last_seen_at),
	    raw_json = VALUES(raw_json),
	    raw_json_hash = VALUES(raw_json_hash),
	    normalizer_version = VALUES(normalizer_version)`
	_, err := r.db.ExecContext(ctx, query,
		s.Platform, s.MarketType, s.APISymbol, nullString(s.APIMarketID), nullString(s.DisplaySymbol), nullString(s.CanonicalSymbol),
		nullString(s.BaseAsset), nullString(s.QuoteAsset), nullString(s.SettleAsset),
		s.MarketSurface, s.InstrumentKind, nullString(s.ContractType),
		nullString(s.StatusRaw), s.StatusNormalized, nullString(s.StatusFieldName),
		nullTimePtr(s.ListingTimeTS), nullString(s.ListingTimeFieldName),
		s.DelistFlag, s.FirstSeenAt, s.LastSeenAt, []byte(s.RawJSON), s.RawJSONHash, s.NormalizerVersion,
	)
	return err
}

// LatestInstrumentSnapshotByKey returns the current row keyed by
// (platform, market_type, api_symbol) or nil when none exists. The
// poller uses the nil return as the "no prev to diff against" signal.
func (r *Repository) LatestInstrumentSnapshotByKey(ctx context.Context, platform, marketType, apiSymbol string) (*InstrumentSnapshot, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	const query = `SELECT id, platform, market_type, api_symbol, api_market_id, display_symbol,
	  canonical_symbol, base_asset, quote_asset, settle_asset, market_surface,
	  instrument_kind, contract_type, status_raw, status_normalized,
	  status_field_name, listing_time_ts, listing_time_field_name, delist_flag,
	  first_seen_at, previous_seen_at, last_seen_at, raw_json, raw_json_hash,
	  normalizer_version
	  FROM t_listing_instrument_snapshot
	  WHERE platform = ? AND market_type = ? AND api_symbol = ?
	  LIMIT 1`
	rows, err := r.db.QueryContext(ctx, query, platform, marketType, apiSymbol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var (
		out                InstrumentSnapshot
		apiMarketID        sql.NullString
		displaySymbol      sql.NullString
		canonicalSymbol    sql.NullString
		baseAsset          sql.NullString
		quoteAsset         sql.NullString
		settleAsset        sql.NullString
		contractType       sql.NullString
		statusRaw          sql.NullString
		statusFieldName    sql.NullString
		listingTimeTS      sql.NullTime
		listingTimeFieldNm sql.NullString
		previousSeenAt     sql.NullTime
		rawJSON            []byte
	)
	if err := rows.Scan(
		&out.ID, &out.Platform, &out.MarketType, &out.APISymbol, &apiMarketID, &displaySymbol,
		&canonicalSymbol, &baseAsset, &quoteAsset, &settleAsset, &out.MarketSurface,
		&out.InstrumentKind, &contractType, &statusRaw, &out.StatusNormalized,
		&statusFieldName, &listingTimeTS, &listingTimeFieldNm, &out.DelistFlag,
		&out.FirstSeenAt, &previousSeenAt, &out.LastSeenAt, &rawJSON, &out.RawJSONHash,
		&out.NormalizerVersion,
	); err != nil {
		return nil, err
	}
	if apiMarketID.Valid {
		out.APIMarketID = apiMarketID.String
	}
	if displaySymbol.Valid {
		out.DisplaySymbol = displaySymbol.String
	}
	if canonicalSymbol.Valid {
		out.CanonicalSymbol = canonicalSymbol.String
	}
	if baseAsset.Valid {
		out.BaseAsset = baseAsset.String
	}
	if quoteAsset.Valid {
		out.QuoteAsset = quoteAsset.String
	}
	if settleAsset.Valid {
		out.SettleAsset = settleAsset.String
	}
	if contractType.Valid {
		out.ContractType = contractType.String
	}
	if statusRaw.Valid {
		out.StatusRaw = statusRaw.String
	}
	if statusFieldName.Valid {
		out.StatusFieldName = statusFieldName.String
	}
	if listingTimeTS.Valid {
		t := listingTimeTS.Time
		out.ListingTimeTS = &t
	}
	if listingTimeFieldNm.Valid {
		out.ListingTimeFieldName = listingTimeFieldNm.String
	}
	if previousSeenAt.Valid {
		t := previousSeenAt.Time
		out.PreviousSeenAt = &t
	}
	out.RawJSON = json.RawMessage(append([]byte(nil), rawJSON...))
	return &out, rows.Err()
}

// HasInstrumentBaseline returns true when t_listing_instrument_snapshot
// already contains at least one row for the (platform, market_type)
// pair. The instrument poller uses this to gate cold-start: a brand-
// new platform must NOT emit new_symbol signals on its first poll,
// otherwise every existing exchange instrument would be misreported
// as a fresh listing.
func (r *Repository) HasInstrumentBaseline(ctx context.Context, platform, marketType string) (bool, error) {
	if r.db == nil {
		return false, errors.New("listing repository: no db attached")
	}
	const query = `SELECT 1 FROM t_listing_instrument_snapshot
	  WHERE platform = ? AND market_type = ? LIMIT 1`
	var present int
	err := r.db.QueryRowContext(ctx, query, platform, marketType).Scan(&present)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return present == 1, nil
}

// HasAnnouncementBaseline is the announcement counterpart of
// HasInstrumentBaseline: true iff t_listing_announcement already
// contains at least one row for the given platform. The announcement
// poller uses this to decide whether the current pass should write
// announcement_listing signals (warm path) or only persist the
// parent row (cold-start baseline-only). Without it a fresh deploy
// would post every historical announcement as a new perp candidate.
func (r *Repository) HasAnnouncementBaseline(ctx context.Context, platform string) (bool, error) {
	if r.db == nil {
		return false, errors.New("listing repository: no db attached")
	}
	const query = `SELECT 1 FROM t_listing_announcement
	  WHERE platform = ? LIMIT 1`
	var present int
	err := r.db.QueryRowContext(ctx, query, platform).Scan(&present)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return present == 1, nil
}

// HasAnnouncementForExternalID returns true when the (platform,
// announcement_id) pair already exists, which the poller uses to
// decide between baseline write (cold start) and signal emission
// (warm path, only for newly observed announcements). Without this
// guard a re-fetch of the same CMS page would emit duplicate signals
// alongside the idempotent INSERT — visible to operators as noise.
func (r *Repository) HasAnnouncementForExternalID(ctx context.Context, platform, announcementID string) (bool, error) {
	if r.db == nil {
		return false, errors.New("listing repository: no db attached")
	}
	const query = `SELECT 1 FROM t_listing_announcement
	  WHERE platform = ? AND announcement_id = ? LIMIT 1`
	var present int
	err := r.db.QueryRowContext(ctx, query, platform, announcementID).Scan(&present)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return present == 1, nil
}

// UpsertRiskPlan writes a new t_listing_risk_plan audit row. The
// table is intentionally INSERT-only (every generation writes a new
// row) so historical decision cards keep the exact plan that was
// rendered, even after the production table is retuned.
//
// The returned id is the auto-increment primary key so the producer
// can attach it to the decision card payload.
func (r *Repository) UpsertRiskPlan(ctx context.Context, plan RiskPlan) (int64, error) {
	if r.db == nil {
		return 0, errors.New("listing repository: no db attached")
	}
	const query = `INSERT INTO t_listing_risk_plan (
	  candidate_id, risk_plan_version, template_name, max_leverage, max_position_usd,
	  leverage_tiers_json, funding_initial_mode, mm_quote_required, risk_notes_json,
	  source_evidence_json, generated_at, approved_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	mmQuote := 0
	if plan.MMQuoteRequired {
		mmQuote = 1
	}
	res, err := r.db.ExecContext(ctx, query,
		plan.CandidateID,
		plan.RiskPlanVersion,
		plan.TemplateName,
		nullFloat(plan.MaxLeverage),
		nullFloat(plan.MaxPositionUSD),
		[]byte(plan.LeverageTiersJSON),
		nullString(plan.FundingInitialMode),
		mmQuote,
		nullRawJSON(plan.RiskNotesJSON),
		[]byte(plan.SourceEvidenceJSON),
		plan.GeneratedAt,
		nullTimePtr(plan.ApprovedAt),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LatestRiskPlanByCandidate returns the most recently generated risk
// plan row for the candidate, or nil if no row exists yet. The
// decision card producer uses this to avoid recomputing the plan
// when the candidate is re-presented in subsequent ticks within the
// same UTC day.
func (r *Repository) LatestRiskPlanByCandidate(ctx context.Context, candidateID int64) (*RiskPlan, error) {
	if r.db == nil {
		return nil, errors.New("listing repository: no db attached")
	}
	const query = `SELECT id, candidate_id, risk_plan_version, template_name, max_leverage, max_position_usd,
	         leverage_tiers_json, funding_initial_mode, mm_quote_required, risk_notes_json,
	         source_evidence_json, generated_at, approved_at, created_at
	    FROM t_listing_risk_plan
	   WHERE candidate_id = ?
	   ORDER BY generated_at DESC, id DESC
	   LIMIT 1`
	rows, err := r.db.QueryContext(ctx, query, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var p RiskPlan
	var maxLev, maxPos sql.NullFloat64
	var leverageTiers, riskNotes, sourceEvidence []byte
	var fundingMode sql.NullString
	var mmQuote int
	var approvedAt sql.NullTime
	if err := rows.Scan(&p.ID, &p.CandidateID, &p.RiskPlanVersion, &p.TemplateName,
		&maxLev, &maxPos, &leverageTiers, &fundingMode, &mmQuote, &riskNotes,
		&sourceEvidence, &p.GeneratedAt, &approvedAt, &p.CreatedAt); err != nil {
		return nil, err
	}
	if maxLev.Valid {
		v := maxLev.Float64
		p.MaxLeverage = &v
	}
	if maxPos.Valid {
		v := maxPos.Float64
		p.MaxPositionUSD = &v
	}
	p.FundingInitialMode = fundingMode.String
	p.MMQuoteRequired = mmQuote == 1
	if len(leverageTiers) > 0 {
		p.LeverageTiersJSON = append(json.RawMessage(nil), leverageTiers...)
	}
	if len(riskNotes) > 0 {
		p.RiskNotesJSON = append(json.RawMessage(nil), riskNotes...)
	}
	if len(sourceEvidence) > 0 {
		p.SourceEvidenceJSON = append(json.RawMessage(nil), sourceEvidence...)
	}
	if approvedAt.Valid {
		t := approvedAt.Time
		p.ApprovedAt = &t
	}
	return &p, rows.Err()
}

// InsertActionDispatch writes one audit row to t_listing_action_dispatch.
// The row is INSERT-only; the producer can later mark it
// completed via a separate UPDATE when the downstream side
// (listing-ops group, MM channel) acknowledges receipt.
func (r *Repository) InsertActionDispatch(ctx context.Context, row ActionDispatchRecord) (int64, error) {
	if r.db == nil {
		return 0, errors.New("listing repository: no db attached")
	}
	var outboxID any
	if row.OutboxID != nil {
		outboxID = *row.OutboxID
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO t_listing_action_dispatch
		   (candidate_id, decision_id, dispatch_type, target_channel, status, outbox_id, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.CandidateID, row.DecisionID, row.DispatchType, row.TargetChannel, row.Status,
		outboxID, []byte(row.PayloadJSON),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertWatchlist writes a watchlist entry keyed on candidate_id (the
// unique key uk_listing_watchlist_candidate). Re-clicking 进入观察 on
// the same candidate refreshes the row in place rather than erroring
// on duplicate key.
func (r *Repository) UpsertWatchlist(ctx context.Context, w WatchlistEntry) (int64, error) {
	if r.db == nil {
		return 0, errors.New("listing repository: no db attached")
	}
	const query = `INSERT INTO t_listing_watchlist (
	  candidate_id, canonical_symbol, market_surface, instrument_kind,
	  watch_status, watch_reason, source_decision_id, watch_started_at, payload_json)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON DUPLICATE KEY UPDATE
	  canonical_symbol = VALUES(canonical_symbol),
	  market_surface   = VALUES(market_surface),
	  instrument_kind  = VALUES(instrument_kind),
	  watch_status     = VALUES(watch_status),
	  watch_reason     = VALUES(watch_reason),
	  source_decision_id = VALUES(source_decision_id),
	  watch_started_at = VALUES(watch_started_at),
	  payload_json     = VALUES(payload_json)`
	res, err := r.db.ExecContext(ctx, query,
		w.CandidateID, w.CanonicalSymbol, w.MarketSurface, w.InstrumentKind,
		w.WatchStatus, nullString(w.WatchReason), w.SourceDecisionID, w.WatchStartedAt,
		[]byte(w.PayloadJSON),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertDecision writes one t_listing_decision row. The unique key
// on (candidate_id, operator_open_id, action, callback_ts) makes
// the operation idempotent: a second click with the same callback_ts
// (truncated to seconds upstream) returns inserted=false plus the
// existing id so the API layer can surface a stable 200 OK shape.
func (r *Repository) InsertDecision(ctx context.Context, d DecisionRecord) (int64, bool, error) {
	if r.db == nil {
		return 0, false, errors.New("listing repository: no db attached")
	}
	verified := 0
	if d.SignatureVerified {
		verified = 1
	}
	const query = `INSERT IGNORE INTO t_listing_decision (
	  candidate_id, card_id, message_id, operator_open_id, action, reason,
	  signature_verified, callback_payload_json, callback_ts)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := r.db.ExecContext(ctx, query,
		d.CandidateID,
		nullString(d.CardID),
		nullString(d.MessageID),
		d.OperatorOpenID,
		d.Action,
		nullString(d.Reason),
		verified,
		[]byte(d.CallbackPayloadJSON),
		d.CallbackTS,
	)
	if err != nil {
		return 0, false, err
	}
	affected, _ := res.RowsAffected()
	if affected > 0 {
		id, _ := res.LastInsertId()
		return id, true, nil
	}
	var existing int64
	err = r.db.QueryRowContext(ctx,
		`SELECT id FROM t_listing_decision
		  WHERE candidate_id = ? AND operator_open_id = ? AND action = ? AND callback_ts = ?
		  LIMIT 1`,
		d.CandidateID, d.OperatorOpenID, d.Action, d.CallbackTS,
	).Scan(&existing)
	if err != nil {
		return 0, false, err
	}
	return existing, false, nil
}

// LatestDecisionForCandidate returns the action + callback_ts of the
// most recent t_listing_decision row for the candidate. The bool is
// false when no decision exists yet (first time the candidate is
// considered). Used by the decision card producer to honour the
// configurable ignore_cooldown without re-deriving the timestamp on
// every tick.
func (r *Repository) LatestDecisionForCandidate(ctx context.Context, candidateID int64) (string, time.Time, bool, error) {
	if r.db == nil {
		return "", time.Time{}, false, errors.New("listing repository: no db attached")
	}
	const query = `SELECT action, callback_ts FROM t_listing_decision
	  WHERE candidate_id = ?
	  ORDER BY callback_ts DESC, id DESC
	  LIMIT 1`
	var action string
	var ts time.Time
	err := r.db.QueryRowContext(ctx, query, candidateID).Scan(&action, &ts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, err
	}
	return action, ts, true, nil
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

func nullRawJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}
