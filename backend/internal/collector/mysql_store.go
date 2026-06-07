package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-ops-intelligence/backend/internal/domain"
)

const initSchemaSQL = `
CREATE TABLE IF NOT EXISTS t_symbol_mapping (id BIGINT AUTO_INCREMENT PRIMARY KEY, display_symbol VARCHAR(96) NOT NULL, canonical VARCHAR(32) NOT NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, platform VARCHAR(32) NOT NULL, api_symbol VARCHAR(96) NOT NULL, source_endpoint VARCHAR(255) NOT NULL);
CREATE TABLE IF NOT EXISTS t_orderbook_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, tier VARCHAR(16) NOT NULL DEFAULT '', bid_usd DECIMAL(28,8), ask_usd DECIMAL(28,8), total_usd DECIMAL(28,8), depth_status VARCHAR(32) NOT NULL, partial_reason VARCHAR(128), depth_source VARCHAR(32), source_id VARCHAR(64), levels_returned INT, bid_levels_returned INT, ask_levels_returned INT, api_level_cap INT, farthest_bid_pct DECIMAL(18,8), farthest_ask_pct DECIMAL(18,8), farthest_distance_pct DECIMAL(18,8), source_endpoint VARCHAR(255), aggregation_params_json JSON, strict_complete TINYINT(1) NOT NULL DEFAULT 0, display_available TINYINT(1) NOT NULL DEFAULT 0, policy_acceptance VARCHAR(32), physical_limit TINYINT(1) NOT NULL DEFAULT 0, unofficial_ui_endpoint TINYINT(1) NOT NULL DEFAULT 0, error_message TEXT, depth_json JSON, buy_slippage_json JSON, sell_slippage_json JSON);
CREATE TABLE IF NOT EXISTS t_book_quality_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, spread_bp DECIMAL(18,8), imbalance_pct DECIMAL(18,8));
CREATE TABLE IF NOT EXISTS t_symbol_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL, source_endpoint VARCHAR(255), error_message TEXT);
CREATE TABLE IF NOT EXISTS t_top30_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, symbol VARCHAR(96) NOT NULL, rank_no INT NOT NULL, volume_24h_usd DECIMAL(28,8), volume_7d_usd DECIMAL(28,8) NULL, delta_7d_pct DECIMAL(10,4) NULL, coverage_count INT NULL, edgex_listed TINYINT(1) NULL, suggested_action VARCHAR(64) NULL, data_source VARCHAR(32) NOT NULL DEFAULT 'coingecko', source_endpoint VARCHAR(255) NULL, status VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, UNIQUE KEY uk_top30_platform_symbol_ts (platform, symbol, snapshot_ts));
CREATE TABLE IF NOT EXISTS t_daily_volume_aggregate (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96), day DATE NOT NULL, volume_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL, data_source VARCHAR(32) NOT NULL DEFAULT 'native', source_endpoint VARCHAR(255) NULL, snapshot_ts TIMESTAMP NULL, UNIQUE KEY uk_day_platform_symbol_source (day, platform, display_symbol, data_source));
CREATE TABLE IF NOT EXISTS t_coingecko_platform_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,2), open_interest_usd DECIMAL(28,2), data_source VARCHAR(32) NOT NULL DEFAULT 'coingecko', source_endpoint VARCHAR(255) NOT NULL, status VARCHAR(32) NOT NULL, INDEX idx_cg_platform_ts (platform, snapshot_ts));
CREATE TABLE IF NOT EXISTS t_collection_run (id BIGINT AUTO_INCREMENT PRIMARY KEY, run_id VARCHAR(64) NOT NULL, started_at TIMESTAMP NOT NULL, completed_at TIMESTAMP, success_count INT, failed_count INT);
CREATE TABLE IF NOT EXISTS t_collection_status (id BIGINT AUTO_INCREMENT PRIMARY KEY, run_id VARCHAR(64) NOT NULL, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96), collector VARCHAR(32) NOT NULL, source_endpoint VARCHAR(255), status VARCHAR(32) NOT NULL, error_message TEXT, snapshot_ts TIMESTAMP NOT NULL, latency_ms BIGINT NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS t_listing_instrument_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, market_type VARCHAR(32) NOT NULL, api_symbol VARCHAR(96) NOT NULL, api_market_id VARCHAR(96) NULL, display_symbol VARCHAR(128) NULL, canonical_symbol VARCHAR(64) NULL, base_asset VARCHAR(64) NULL, quote_asset VARCHAR(64) NULL, settle_asset VARCHAR(64) NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, contract_type VARCHAR(64) NULL, status_raw VARCHAR(64) NULL, status_normalized VARCHAR(32) NOT NULL, status_field_name VARCHAR(64) NULL, listing_time_ts TIMESTAMP NULL, listing_time_field_name VARCHAR(64) NULL, delist_flag TINYINT(1) NOT NULL DEFAULT 0, first_seen_at TIMESTAMP NOT NULL, previous_seen_at TIMESTAMP NULL, last_seen_at TIMESTAMP NOT NULL, raw_json JSON NOT NULL, raw_json_hash VARCHAR(64) NOT NULL, normalizer_version VARCHAR(32) NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_instrument (platform, market_type, api_symbol), KEY idx_listing_instrument_symbol (canonical_symbol, market_surface, instrument_kind), KEY idx_listing_instrument_status (platform, market_type, status_normalized, last_seen_at), KEY idx_listing_instrument_listing_time (listing_time_ts));
CREATE TABLE IF NOT EXISTS t_listing_announcement (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, announcement_id VARCHAR(191) NOT NULL, announcement_url VARCHAR(512) NULL, title TEXT NOT NULL, description TEXT NULL, category VARCHAR(128) NULL, tags_json JSON NULL, language VARCHAR(32) NULL, published_at TIMESTAMP NULL, source_updated_at TIMESTAMP NULL, parsed_market_type VARCHAR(32) NULL, effective_listing_time TIMESTAMP NULL, parse_confidence VARCHAR(16) NOT NULL, raw_payload_json JSON NOT NULL, raw_payload_hash VARCHAR(64) NOT NULL, parser_version VARCHAR(32) NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_announcement (platform, announcement_id), KEY idx_listing_announcement_published (platform, published_at), KEY idx_listing_announcement_hash (raw_payload_hash));
CREATE TABLE IF NOT EXISTS t_listing_announcement_symbol (id BIGINT AUTO_INCREMENT PRIMARY KEY, announcement_id BIGINT NOT NULL, canonical_symbol VARCHAR(64) NOT NULL, display_symbol VARCHAR(128) NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, signal_subtype VARCHAR(64) NOT NULL, listing_time_ts TIMESTAMP NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_announcement_symbol (announcement_id, canonical_symbol, market_surface, instrument_kind), KEY idx_listing_announcement_symbol_symbol (canonical_symbol, market_surface, instrument_kind));
CREATE TABLE IF NOT EXISTS t_listing_signal_observation (id BIGINT AUTO_INCREMENT PRIMARY KEY, signal_type VARCHAR(32) NOT NULL, signal_subtype VARCHAR(64) NULL, source_platform VARCHAR(32) NOT NULL, market_type VARCHAR(32) NULL, api_symbol VARCHAR(96) NULL, api_market_id VARCHAR(96) NULL, canonical_symbol VARCHAR(64) NOT NULL, display_symbol VARCHAR(128) NULL, base_asset VARCHAR(64) NULL, quote_asset VARCHAR(64) NULL, settle_asset VARCHAR(64) NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, status_raw VARCHAR(64) NULL, status_normalized VARCHAR(32) NULL, confidence VARCHAR(16) NULL, observed_at TIMESTAMP NOT NULL, source_snapshot_ts TIMESTAMP NULL, published_at TIMESTAMP NULL, listing_time_ts TIMESTAMP NULL, source_endpoint VARCHAR(255) NULL, source_url VARCHAR(512) NULL, fingerprint VARCHAR(160) NOT NULL, payload_json JSON NOT NULL, raw_payload_json JSON NULL, raw_payload_hash VARCHAR(64) NULL, fused_at TIMESTAMP NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_signal_fingerprint (fingerprint), KEY idx_listing_signal_type_time (signal_type, observed_at), KEY idx_listing_signal_identity (canonical_symbol, market_surface, instrument_kind, observed_at), KEY idx_listing_signal_unfused (fused_at, observed_at));
CREATE TABLE IF NOT EXISTS t_listing_candidate (id BIGINT AUTO_INCREMENT PRIMARY KEY, canonical_symbol VARCHAR(64) NOT NULL, display_symbol VARCHAR(128) NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, lifecycle_status VARCHAR(64) NOT NULL, lifecycle_status_label VARCHAR(128) NULL, evidence_kind VARCHAR(64) NOT NULL, confidence_level VARCHAR(32) NOT NULL, business_score DECIMAL(10,4) NULL, business_score_version VARCHAR(32) NULL, recommendation VARCHAR(64) NULL, recommendation_label VARCHAR(128) NULL, source_platforms_json JSON NOT NULL, top30_enrichment_json JSON NULL, first_observed_at TIMESTAMP NOT NULL, last_observed_at TIMESTAMP NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_candidate_identity (canonical_symbol, market_surface, instrument_kind), KEY idx_listing_candidate_status (lifecycle_status, last_observed_at), KEY idx_listing_candidate_score (business_score, last_observed_at));
CREATE TABLE IF NOT EXISTS t_listing_candidate_signal (id BIGINT AUTO_INCREMENT PRIMARY KEY, candidate_id BIGINT NOT NULL, signal_id BIGINT NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_candidate_signal (candidate_id, signal_id), KEY idx_listing_candidate_signal_signal (signal_id));
CREATE TABLE IF NOT EXISTS t_listing_source_state (id BIGINT AUTO_INCREMENT PRIMARY KEY, source_key VARCHAR(96) NOT NULL, source_type VARCHAR(32) NOT NULL, platform VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL, last_success_at TIMESTAMP NULL, last_error_at TIMESTAMP NULL, consecutive_error_count INT NOT NULL DEFAULT 0, schema_drift_count INT NOT NULL DEFAULT 0, disabled_until TIMESTAMP NULL, last_error TEXT NULL, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_source_state (source_key), KEY idx_listing_source_state_status (status, disabled_until));
CREATE TABLE IF NOT EXISTS t_listing_worker_lease (lease_name VARCHAR(96) NOT NULL, owner_id VARCHAR(96) NOT NULL, expires_at TIMESTAMP(6) NOT NULL, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, PRIMARY KEY (lease_name));
CREATE TABLE IF NOT EXISTS t_listing_risk_plan (id BIGINT AUTO_INCREMENT PRIMARY KEY, candidate_id BIGINT NOT NULL, risk_plan_version VARCHAR(32) NOT NULL, template_name VARCHAR(64) NOT NULL, max_leverage DECIMAL(18,8) NULL, max_position_usd DECIMAL(28,8) NULL, leverage_tiers_json JSON NOT NULL, funding_initial_mode VARCHAR(64) NULL, mm_quote_required TINYINT(1) NOT NULL DEFAULT 0, risk_notes_json JSON NULL, source_evidence_json JSON NOT NULL, generated_at TIMESTAMP NOT NULL, approved_at TIMESTAMP NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, KEY idx_listing_risk_plan_candidate (candidate_id, generated_at));
CREATE TABLE IF NOT EXISTS t_listing_decision (id BIGINT AUTO_INCREMENT PRIMARY KEY, candidate_id BIGINT NOT NULL, card_id VARCHAR(128) NULL, message_id VARCHAR(128) NULL, operator_open_id VARCHAR(128) NOT NULL, action VARCHAR(64) NOT NULL, reason TEXT NULL, signature_verified TINYINT(1) NOT NULL DEFAULT 0, callback_payload_json JSON NOT NULL, callback_ts TIMESTAMP NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_decision_idempotency (candidate_id, operator_open_id, action, callback_ts), KEY idx_listing_decision_candidate (candidate_id, created_at));
CREATE TABLE IF NOT EXISTS t_listing_watchlist (id BIGINT AUTO_INCREMENT PRIMARY KEY, candidate_id BIGINT NOT NULL, canonical_symbol VARCHAR(64) NOT NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, watch_status VARCHAR(32) NOT NULL, watch_reason TEXT NULL, source_decision_id BIGINT NULL, watch_started_at TIMESTAMP NOT NULL, edgex_listed_at TIMESTAMP NULL, transferred_to_dashboard_at TIMESTAMP NULL, payload_json JSON NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_watchlist_candidate (candidate_id), KEY idx_listing_watchlist_status (watch_status, watch_started_at));
CREATE TABLE IF NOT EXISTS t_listing_action_dispatch (id BIGINT AUTO_INCREMENT PRIMARY KEY, candidate_id BIGINT NOT NULL, decision_id BIGINT NOT NULL, dispatch_type VARCHAR(64) NOT NULL, target_channel VARCHAR(64) NOT NULL, status VARCHAR(32) NOT NULL, outbox_id BIGINT NULL, payload_json JSON NOT NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, KEY idx_listing_action_dispatch_status (status, created_at), KEY idx_listing_action_dispatch_candidate (candidate_id, created_at));
CREATE TABLE IF NOT EXISTS t_listing_delivery_outbox (id BIGINT AUTO_INCREMENT PRIMARY KEY, event_type VARCHAR(64) NOT NULL, dedupe_key VARCHAR(191) NOT NULL, target_channel VARCHAR(64) NOT NULL, status VARCHAR(32) NOT NULL, attempt_count INT NOT NULL DEFAULT 0, max_attempts INT NOT NULL DEFAULT 5, next_attempt_at TIMESTAMP NULL, payload_json JSON NOT NULL, last_error TEXT NULL, sent_at TIMESTAMP NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_delivery_dedupe (dedupe_key), KEY idx_listing_delivery_due (status, next_attempt_at), KEY idx_listing_delivery_event (event_type, created_at));
CREATE TABLE IF NOT EXISTS t_listing_delivery_attempt (id BIGINT AUTO_INCREMENT PRIMARY KEY, outbox_id BIGINT NOT NULL, attempt_no INT NOT NULL, status VARCHAR(32) NOT NULL, http_status INT NULL, error_message TEXT NULL, attempted_at TIMESTAMP NOT NULL, response_body TEXT NULL, latency_ms BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_delivery_attempt (outbox_id, attempt_no), KEY idx_listing_delivery_attempt_outbox (outbox_id, attempted_at));
CREATE TABLE IF NOT EXISTS t_listing_alert_state (id BIGINT AUTO_INCREMENT PRIMARY KEY, alert_kind VARCHAR(64) NOT NULL, canonical_symbol VARCHAR(64) NOT NULL, status VARCHAR(32) NOT NULL, severity_seq INT NOT NULL DEFAULT 1, reissue_count INT NOT NULL DEFAULT 0, clear_streak INT NOT NULL DEFAULT 0, first_triggered_at TIMESTAMP NOT NULL, last_pushed_at TIMESTAMP NULL, last_evaluated_at TIMESTAMP NOT NULL, last_severity_json JSON NULL, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, UNIQUE KEY uk_listing_alert_state (alert_kind, canonical_symbol), KEY idx_listing_alert_state_status (alert_kind, status, last_evaluated_at));
CREATE TABLE IF NOT EXISTS t_activity_source_state (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, source_group VARCHAR(96) NOT NULL, source_type VARCHAR(32) NOT NULL, source_url VARCHAR(512) NULL, source_key VARCHAR(191) NOT NULL UNIQUE, fetch_mode VARCHAR(32) NOT NULL, evidence_quality VARCHAR(32) NOT NULL DEFAULT 'unknown', enabled TINYINT(1) NOT NULL DEFAULT 1, poll_interval_seconds INT NOT NULL DEFAULT 3600, auto_push_enabled TINYINT(1) NOT NULL DEFAULT 1, requires_proxy TINYINT(1) NOT NULL DEFAULT 0, requires_browser_context TINYINT(1) NOT NULL DEFAULT 0, requires_login TINYINT(1) NOT NULL DEFAULT 0, region_sensitive TINYINT(1) NOT NULL DEFAULT 0, personalized TINYINT(1) NOT NULL DEFAULT 0, source_context_json JSON NULL, last_http_status INT NULL, last_error_kind VARCHAR(64) NULL, last_schema_hash CHAR(64) NULL, last_content_hash CHAR(64) NULL, sample_count INT NOT NULL DEFAULT 0, event_count INT NOT NULL DEFAULT 0, source_status VARCHAR(32) NOT NULL DEFAULT 'ok', disabled_until DATETIME(3) NULL, last_checked_at DATETIME(3) NULL, last_success_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, KEY idx_activity_source_platform (platform), KEY idx_activity_source_group (source_group), KEY idx_activity_source_type (source_type), KEY idx_activity_source_fetch_mode (fetch_mode), KEY idx_activity_source_status (source_status, disabled_until));
CREATE TABLE IF NOT EXISTS t_activity_raw_evidence (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, source_key VARCHAR(191) NOT NULL, platform VARCHAR(32) NOT NULL, source_group VARCHAR(96) NOT NULL, source_url VARCHAR(512) NULL, fetch_mode VARCHAR(32) NOT NULL, payload_text LONGTEXT NULL, payload_hash CHAR(64) NOT NULL, schema_hash CHAR(64) NULL, content_hash CHAR(64) NULL, payload_size_bytes BIGINT UNSIGNED NOT NULL, payload_truncated TINYINT(1) NOT NULL DEFAULT 0, payload_preview MEDIUMTEXT NULL, response_meta_json JSON NULL, fixture_ref VARCHAR(255) NULL, fetched_at DATETIME(3) NOT NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, KEY idx_activity_raw_source_fetched (source_key, fetched_at), KEY idx_activity_raw_payload_hash (payload_hash));
CREATE TABLE IF NOT EXISTS t_activity_event (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, raw_evidence_id BIGINT UNSIGNED NULL, platform VARCHAR(32) NOT NULL, source_group VARCHAR(96) NOT NULL, source_external_id VARCHAR(191) NULL, source_url VARCHAR(512) NULL, title TEXT NOT NULL, activity_type VARCHAR(64) NOT NULL, target_symbols_json JSON NULL, reward_pool_text TEXT NULL, reward_pool_usd_estimate DECIMAL(28,8) NULL, reward_pool_primary_token VARCHAR(32) NULL, reward_pool_parse_confidence VARCHAR(32) NULL, has_reward_pool TINYINT(1) NOT NULL DEFAULT 0, start_time DATETIME(3) NULL, end_time DATETIME(3) NULL, publish_time DATETIME(3) NULL, raw_time_text TEXT NULL, raw_timezone_hint VARCHAR(64) NULL, time_parse_confidence VARCHAR(32) NULL, content_text MEDIUMTEXT NULL, content_hash CHAR(64) NOT NULL, dedupe_key VARCHAR(191) NOT NULL UNIQUE, confidence_score DECIMAL(8,4) NOT NULL DEFAULT 0, needs_human_review TINYINT(1) NOT NULL DEFAULT 0, auto_push_allowed TINYINT(1) NOT NULL DEFAULT 0, event_status VARCHAR(32) NOT NULL DEFAULT 'active', review_status VARCHAR(32) NOT NULL DEFAULT 'pending', ops_decision_action VARCHAR(32) NULL, ops_decision_stale TINYINT(1) NOT NULL DEFAULT 0, reviewer VARCHAR(128) NULL, review_reason TEXT NULL, reviewed_at DATETIME(3) NULL, event_version INT NOT NULL DEFAULT 1, parser_version VARCHAR(64) NOT NULL, source_context_json JSON NULL, parser_warnings_json JSON NULL, reward_pools_json JSON NULL, task_conditions_json JSON NULL, eligibility_rules_json JSON NULL, rich_fields_summary_json JSON NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, KEY idx_activity_event_platform_publish (platform, publish_time), KEY idx_activity_event_review_auto (review_status, auto_push_allowed), KEY idx_activity_event_status_updated (event_status, updated_at));
CREATE TABLE IF NOT EXISTS t_activity_event_symbol (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, event_id BIGINT UNSIGNED NOT NULL, canonical_symbol VARCHAR(64) NOT NULL, display_symbol VARCHAR(128) NULL, market_surface VARCHAR(32) NOT NULL, role VARCHAR(32) NOT NULL, sort_order INT NOT NULL DEFAULT 0, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, UNIQUE KEY uk_activity_event_symbol (event_id, canonical_symbol, market_surface, role), KEY idx_activity_event_symbol_lookup (canonical_symbol, market_surface));
CREATE TABLE IF NOT EXISTS t_activity_digest (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, digest_kind VARCHAR(32) NOT NULL, digest_key VARCHAR(64) NOT NULL, digest_date DATE NOT NULL, status VARCHAR(32) NOT NULL DEFAULT 'draft', summary_json JSON NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, UNIQUE KEY uk_activity_digest_key (digest_kind, digest_key));
CREATE TABLE IF NOT EXISTS t_activity_digest_item (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, digest_id BIGINT UNSIGNED NOT NULL, event_id BIGINT UNSIGNED NOT NULL, sort_order INT NOT NULL DEFAULT 0, severity VARCHAR(32) NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, UNIQUE KEY uk_activity_digest_item (digest_id, event_id), KEY idx_activity_digest_item_event (event_id));
CREATE TABLE IF NOT EXISTS t_activity_review_item (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, event_id BIGINT UNSIGNED NOT NULL, event_version INT NOT NULL, content_hash CHAR(64) NOT NULL, status VARCHAR(32) NOT NULL DEFAULT 'pending', reason TEXT NULL, reviewer VARCHAR(128) NULL, reviewed_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, KEY idx_activity_review_status (status, created_at), KEY idx_activity_review_event (event_id, event_version));
CREATE TABLE IF NOT EXISTS t_activity_delivery_outbox (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, event_type VARCHAR(64) NOT NULL, event_id BIGINT UNSIGNED NULL, event_version INT NULL, dedupe_key VARCHAR(191) NOT NULL UNIQUE, target_channel VARCHAR(64) NOT NULL, status VARCHAR(32) NOT NULL, attempt_count INT NOT NULL DEFAULT 0, max_attempts INT NOT NULL DEFAULT 5, next_attempt_at DATETIME(3) NULL, payload_json JSON NOT NULL, last_error TEXT NULL, sent_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, CHECK (status IN ('pending','retry','sent','failed','disabled_no_webhook','disabled_missing_secret','muted','redrive_pending')), KEY idx_activity_delivery_due (status, next_attempt_at), KEY idx_activity_delivery_event (event_type, created_at));
CREATE TABLE IF NOT EXISTS t_activity_delivery_attempt (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, outbox_id BIGINT UNSIGNED NOT NULL, attempt_no INT NOT NULL, status VARCHAR(32) NOT NULL, http_status INT NULL, error_message TEXT NULL, attempted_at DATETIME(3) NOT NULL, response_body TEXT NULL, latency_ms BIGINT NOT NULL DEFAULT 0, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL, UNIQUE KEY uk_activity_delivery_attempt (outbox_id, attempt_no), KEY idx_activity_delivery_attempt_outbox (outbox_id, attempted_at));
CREATE TABLE IF NOT EXISTS t_activity_worker_lease (lease_name VARCHAR(191) NOT NULL PRIMARY KEY, owner VARCHAR(191) NOT NULL, expires_at DATETIME(3) NOT NULL, heartbeat_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL);
`

func OpenMySQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ApplyMigrations(db *sql.DB) error {
	for _, stmt := range strings.Split(initSchemaSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed at %q: %w", firstLine(stmt), err)
		}
	}
	return applySchemaPostInit(db)
}

// applySchemaPostInit runs in-place ALTER TABLE migrations that
// `CREATE TABLE IF NOT EXISTS` can't pick up on existing prod databases.
// Each step checks INFORMATION_SCHEMA first so the function stays
// effectively no-op on every subsequent boot.
func applySchemaPostInit(db *sql.DB) error {
	if err := applyListingSchemaPostInit(db); err != nil {
		return err
	}
	return applyActivitySchemaPostInit(db)
}

// applyListingSchemaPostInit widens t_listing_signal_observation
// fingerprint from VARCHAR(96) to VARCHAR(160) as a defence-in-depth
// guard against future producer fingerprint drift.
func applyListingSchemaPostInit(db *sql.DB) error {
	const (
		listingSignalTable      = "t_listing_signal_observation"
		fingerprintColumn       = "fingerprint"
		fingerprintTargetLength = 160
	)
	var currentLen sql.NullInt64
	err := db.QueryRow(`SELECT CHARACTER_MAXIMUM_LENGTH
		   FROM INFORMATION_SCHEMA.COLUMNS
		  WHERE TABLE_SCHEMA = DATABASE()
		    AND TABLE_NAME   = ?
		    AND COLUMN_NAME  = ?`, listingSignalTable, fingerprintColumn).Scan(&currentLen)
	if err != nil {
		if err == sql.ErrNoRows {
			// Table not present yet (e.g. fresh DB before its CREATE
			// TABLE in this same ApplyMigrations call had a chance to
			// run, or running against a DB without the listing schema
			// at all). Nothing to widen.
			return nil
		}
		return fmt.Errorf("inspect %s.%s width: %w", listingSignalTable, fingerprintColumn, err)
	}
	if currentLen.Valid && currentLen.Int64 >= fingerprintTargetLength {
		return nil
	}
	if _, err := db.Exec(fmt.Sprintf(
		`ALTER TABLE %s MODIFY %s VARCHAR(%d) NOT NULL`,
		listingSignalTable, fingerprintColumn, fingerprintTargetLength)); err != nil {
		return fmt.Errorf("widen %s.%s to VARCHAR(%d): %w",
			listingSignalTable, fingerprintColumn, fingerprintTargetLength, err)
	}
	return nil
}

func applyActivitySchemaPostInit(db *sql.DB) error {
	columns := []struct {
		name       string
		after      string
		definition string
	}{
		{name: "last_checked_at", after: "disabled_until", definition: "DATETIME(3) NULL"},
		{name: "last_success_at", after: "last_checked_at", definition: "DATETIME(3) NULL"},
	}
	for _, col := range columns {
		if err := ensureColumnExists(db, "t_activity_source_state", col.name, col.definition, col.after); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumnExists(db *sql.DB, tableName, columnName, definition, afterColumn string) error {
	var exists int
	err := db.QueryRow(`SELECT COUNT(*)
		   FROM INFORMATION_SCHEMA.COLUMNS
		  WHERE TABLE_SCHEMA = DATABASE()
		    AND TABLE_NAME = ?
		    AND COLUMN_NAME = ?`, tableName, columnName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("inspect %s.%s existence: %w", tableName, columnName, err)
	}
	if exists > 0 {
		return nil
	}
	stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, tableName, columnName, definition)
	if afterColumn != "" {
		stmt += fmt.Sprintf(` AFTER %s`, afterColumn)
	}
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("add %s.%s: %w", tableName, columnName, err)
	}
	return nil
}

func (s *Store) AttachDB(db *sql.DB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
}

// MySQLBacked reports whether the store has a live MySQL connection
// attached. The /api/health and /api/readiness endpoints use this to
// decide whether to surface MySQL-specific status fields.
func (s *Store) MySQLBacked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db != nil
}

// PingDB verifies the MySQL connection is live. Used by the readiness
// gate. Returns nil when not MySQL-backed (in-memory mode is its own
// state -- readiness still passes for it).
func (s *Store) PingDB(ctx context.Context) error {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil
	}
	return db.PingContext(ctx)
}

// snapshotRowCountTables enumerates the per-snapshot tables whose rough
// row counts the operator wants surfaced via /api/health. We use
// information_schema.TABLE_ROWS (an estimate) rather than COUNT(*) so
// the call stays O(1) regardless of table size.
var snapshotRowCountTables = []string{
	"t_orderbook_snapshot",
	"t_book_quality_snapshot",
	"t_symbol_volume_snapshot",
	"t_collection_status",
	"t_coingecko_platform_volume_snapshot",
	"t_top30_snapshot",
	"t_daily_volume_aggregate",
}

// SnapshotRowCounts returns INFORMATION_SCHEMA-derived row count
// estimates for the snapshot tables. Each value is the value the MySQL
// optimiser uses for cardinality estimates -- it can drift from the
// exact COUNT(*) by up to ANALYZE TABLE granularity, which is fine for
// the health-check use case where the goal is "is data flowing?", not
// "how many rows exactly?". Returns (nil, nil) when not MySQL-backed.
func (s *Store) SnapshotRowCounts(ctx context.Context) (map[string]int64, error) {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return nil, nil
	}
	placeholders, args := buildInClauseArgs(snapshotRowCountTables)
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME, TABLE_ROWS FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var n sql.NullInt64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		if n.Valid {
			out[name] = n.Int64
		} else {
			out[name] = 0
		}
	}
	return out, rows.Err()
}

func (s *Store) persistSymbolMappingsLocked(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM t_symbol_mapping`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO t_symbol_mapping (display_symbol, canonical, market_surface, instrument_kind, platform, api_symbol, source_endpoint) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, sub := range s.cfg.Symbols {
		if _, err := stmt.ExecContext(ctx, sub.DisplaySymbol, sub.Canonical, sub.MarketSurface, sub.InstrumentKind, sub.Platform, sub.APISymbol, sub.SourceEndpoint); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) persistPlatformSnapshot(ctx context.Context, row domain.PlatformSnapshot) error {
	db := s.db
	if db == nil {
		return nil
	}
	rows := platformSnapshotOrderbookRows(row)
	for _, tier := range sortedOrderbookTiers(rows) {
		dbRow := rows[tier]
		_, err := db.ExecContext(ctx, `INSERT INTO t_orderbook_snapshot (platform, display_symbol, snapshot_ts, tier, bid_usd, ask_usd, total_usd, depth_status, partial_reason, depth_source, source_id, levels_returned, bid_levels_returned, ask_levels_returned, api_level_cap, farthest_bid_pct, farthest_ask_pct, farthest_distance_pct, source_endpoint, aggregation_params_json, strict_complete, display_available, policy_acceptance, physical_limit, unofficial_ui_endpoint, error_message, depth_json, buy_slippage_json, sell_slippage_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, dbRow.Platform, dbRow.DisplaySymbol, dbRow.SnapshotTS, dbRow.Tier, dbRow.BidUSD, dbRow.AskUSD, dbRow.TotalUSD, dbRow.DepthStatus, nullString(dbRow.PartialReason), nullString(dbRow.DepthSource), nullString(dbRow.SourceID), nullInt(dbRow.LevelsReturned), nullInt(dbRow.BidLevelsReturned), nullInt(dbRow.AskLevelsReturned), nullInt(dbRow.APILevelCap), nullFloat(dbRow.FarthestBidPct), nullFloat(dbRow.FarthestAskPct), nullFloat(dbRow.FarthestDistancePct), dbRow.SourceEndpoint, dbRow.AggregationParamsJSON, boolToInt(dbRow.StrictComplete), boolToInt(dbRow.DisplayAvailable), nullString(dbRow.PolicyAcceptance), boolToInt(dbRow.PhysicalLimit), boolToInt(dbRow.UnofficialUIEndpoint), nullString(dbRow.Error), dbRow.DepthJSON, dbRow.BuySlippageJSON, dbRow.SellSlippageJSON)
		if err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO t_book_quality_snapshot (platform, display_symbol, snapshot_ts, spread_bp, imbalance_pct) VALUES (?, ?, ?, ?, ?)`, row.Platform, row.DisplaySymbol, row.SnapshotTS, row.SpreadBP, row.Imbalance)
	return err
}

func (s *Store) persistVolume(ctx context.Context, row domain.VolumeSnapshot) error {
	db := s.db
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `INSERT INTO t_symbol_volume_snapshot (platform, display_symbol, snapshot_ts, volume_24h_usd, status, source_endpoint, error_message) VALUES (?, ?, ?, ?, ?, ?, ?)`, row.Platform, row.DisplaySymbol, row.SnapshotTS, row.Volume24HUSD, row.Status, row.SourceEndpoint, nullString(row.Error))
	return err
}

func (s *Store) persistCoinGeckoPlatformVolume(ctx context.Context, row domain.PlatformVolumeAggregate) error {
	db := s.db
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO t_coingecko_platform_volume_snapshot
		   (platform, snapshot_ts, volume_24h_usd, open_interest_usd, data_source, source_endpoint, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.Platform, row.SnapshotTS, row.Volume24HUSD, row.OpenInterestUSD,
		defaultString(row.DataSource, domain.DataSourceCoinGecko),
		row.SourceEndpoint, row.Status,
	)
	return err
}

// persistDailyVolumeAggregate UPSERTs by (day, platform, display_symbol,
// data_source). It also enforces priority semantics across data_source so a
// coingecko_backfill row never displaces a coingecko or native row for the
// same (platform, day, display_symbol), and conversely a fresh coingecko or
// native row evicts any earlier backfill row for that same (platform, day,
// display_symbol). This mirrors the in-memory mergeDailyAggregate dedup so
// reload-from-MySQL surfaces the same shape the running collector observes.
func (s *Store) persistDailyVolumeAggregate(ctx context.Context, row domain.DailyVolumeAggregate) error {
	db := s.db
	if db == nil {
		return nil
	}
	source := defaultString(row.DataSource, domain.DataSourceNative)

	// Backfill rows yield to any higher-priority row already covering the
	// same slot; live rows evict every lower-priority backfill row.
	switch source {
	case domain.DataSourceCoinGeckoBackfill:
		has, err := s.hasHigherPriorityDailyRow(ctx, db, row, []string{
			domain.DataSourceNative, domain.DataSourceCoinGecko, domain.DataSourceNativeBackfill,
		})
		if err != nil {
			return err
		}
		if has {
			return nil
		}
	case domain.DataSourceNativeBackfill:
		has, err := s.hasHigherPriorityDailyRow(ctx, db, row, []string{
			domain.DataSourceNative, domain.DataSourceCoinGecko,
		})
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		if err := s.deleteDailyRowsBySource(ctx, db, row, []string{
			domain.DataSourceCoinGeckoBackfill,
		}); err != nil {
			return err
		}
	default:
		if err := s.deleteDailyRowsBySource(ctx, db, row, []string{
			domain.DataSourceCoinGeckoBackfill, domain.DataSourceNativeBackfill,
		}); err != nil {
			return err
		}
	}

	if row.DisplaySymbol == "" {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM t_daily_volume_aggregate
			   WHERE day = ? AND platform = ? AND display_symbol IS NULL AND data_source = ?`,
			row.Day, row.Platform, source,
		); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO t_daily_volume_aggregate
			   (platform, display_symbol, day, volume_usd, status, data_source, source_endpoint, snapshot_ts)
			 VALUES (?, NULL, ?, ?, ?, ?, ?, ?)`,
			row.Platform, row.Day, row.Volume24HUSD, row.Status, source, nullString(row.SourceEndpoint), nullTimePtr(row.SnapshotTS),
		)
		return err
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO t_daily_volume_aggregate
		   (platform, display_symbol, day, volume_usd, status, data_source, source_endpoint, snapshot_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   volume_usd = VALUES(volume_usd),
		   status = VALUES(status),
		   source_endpoint = VALUES(source_endpoint),
		   snapshot_ts = VALUES(snapshot_ts)`,
		row.Platform, row.DisplaySymbol, row.Day, row.Volume24HUSD, row.Status, source, nullString(row.SourceEndpoint), nullTimePtr(row.SnapshotTS),
	)
	return err
}

// hasHigherPriorityDailyRow reports whether a row from any of the given
// data sources already covers the same (day, platform, display_symbol) slot
// as `row`. Used by lower-priority writers (backfill paths) to short-circuit
// inserts that would be shadowed by a more trusted source anyway.
func (s *Store) hasHigherPriorityDailyRow(ctx context.Context, db *sql.DB, row domain.DailyVolumeAggregate, sources []string) (bool, error) {
	if len(sources) == 0 {
		return false, nil
	}
	placeholders, args := buildInClauseArgs(sources)
	var n int
	if row.DisplaySymbol == "" {
		query := `SELECT COUNT(*) FROM t_daily_volume_aggregate
			   WHERE day = ? AND platform = ? AND display_symbol IS NULL
			     AND data_source IN (` + placeholders + `)`
		err := db.QueryRowContext(ctx, query, append([]any{row.Day, row.Platform}, args...)...).Scan(&n)
		return n > 0, err
	}
	query := `SELECT COUNT(*) FROM t_daily_volume_aggregate
		   WHERE day = ? AND platform = ? AND display_symbol = ?
		     AND data_source IN (` + placeholders + `)`
	err := db.QueryRowContext(ctx, query, append([]any{row.Day, row.Platform, row.DisplaySymbol}, args...)...).Scan(&n)
	return n > 0, err
}

// deleteDailyRowsBySource removes any row whose data_source falls into the
// given list for the same (day, platform, display_symbol) slot as `row`.
// Invoked when a higher-priority writer lands so the lower-priority rows
// don't shadow the in-memory dedup on reload-from-MySQL.
func (s *Store) deleteDailyRowsBySource(ctx context.Context, db *sql.DB, row domain.DailyVolumeAggregate, sources []string) error {
	if len(sources) == 0 {
		return nil
	}
	placeholders, args := buildInClauseArgs(sources)
	if row.DisplaySymbol == "" {
		query := `DELETE FROM t_daily_volume_aggregate
			   WHERE day = ? AND platform = ? AND display_symbol IS NULL
			     AND data_source IN (` + placeholders + `)`
		_, err := db.ExecContext(ctx, query, append([]any{row.Day, row.Platform}, args...)...)
		return err
	}
	query := `DELETE FROM t_daily_volume_aggregate
		   WHERE day = ? AND platform = ? AND display_symbol = ?
		     AND data_source IN (` + placeholders + `)`
	_, err := db.ExecContext(ctx, query, append([]any{row.Day, row.Platform, row.DisplaySymbol}, args...)...)
	return err
}

func buildInClauseArgs(values []string) (string, []any) {
	placeholders := strings.Repeat("?,", len(values))
	placeholders = strings.TrimRight(placeholders, ",")
	args := make([]any, 0, len(values))
	for _, v := range values {
		args = append(args, v)
	}
	return placeholders, args
}

func (s *Store) persistTop30(ctx context.Context, platform string, rows []domain.Top30Row) error {
	db := s.db
	if db == nil {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM t_top30_snapshot WHERE platform = ? AND snapshot_ts < DATE_SUB(NOW(), INTERVAL 30 DAY)`,
		platform,
	); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO t_top30_snapshot
		   (platform, symbol, rank_no, volume_24h_usd, volume_7d_usd, delta_7d_pct, coverage_count, edgex_listed, suggested_action, data_source, source_endpoint, status, snapshot_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   rank_no = VALUES(rank_no),
		   volume_24h_usd = VALUES(volume_24h_usd),
		   volume_7d_usd = VALUES(volume_7d_usd),
		   delta_7d_pct = VALUES(delta_7d_pct),
		   coverage_count = VALUES(coverage_count),
		   edgex_listed = VALUES(edgex_listed),
		   suggested_action = VALUES(suggested_action),
		   data_source = VALUES(data_source),
		   source_endpoint = VALUES(source_endpoint),
		   status = VALUES(status)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range rows {
		var v7d, d7d sql.NullFloat64
		if row.Volume7DUSD != nil {
			v7d = sql.NullFloat64{Float64: *row.Volume7DUSD, Valid: true}
		}
		if row.Delta7DPct != nil {
			d7d = sql.NullFloat64{Float64: *row.Delta7DPct, Valid: true}
		}
		if _, err := stmt.ExecContext(ctx,
			platform, row.Symbol, row.Rank, row.Volume24HUSD,
			v7d, d7d,
			nullInt(row.CoverageCount),
			edgexListedTinyInt(row.EdgexListed, row.ListedStatus),
			nullString(row.Action),
			defaultString(row.DataSource, domain.DataSourceCoinGecko),
			nullString(row.SourceEndpoint),
			row.Status,
			row.SnapshotTS,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolToTinyInt(b bool) sql.NullInt64 {
	if !b {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: 1, Valid: true}
}

// edgexListedTinyInt writes the t_top30_snapshot.edgex_listed column so
// downstream consumers (notably the Listing Agent Top30 hot-gap push)
// can distinguish "known not listed" from "unknown / universe missing".
// listedStatus == domain.StatusComplete means the listed_universe lookup
// ran successfully; any other status (empty, insufficient_history, ...)
// means we never resolved a real listing flag and must persist NULL so
// BuildTop30PushEvents can fail-close on that row.
func edgexListedTinyInt(listed bool, listedStatus string) sql.NullInt64 {
	if listedStatus != domain.StatusComplete {
		return sql.NullInt64{}
	}
	if listed {
		return sql.NullInt64{Int64: 1, Valid: true}
	}
	return sql.NullInt64{Int64: 0, Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nullTimePtr(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func (s *Store) persistStatus(ctx context.Context, rows []domain.CollectionStatus, run RunSummary) error {
	db := s.db
	if db == nil {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.persistSymbolMappingsLocked(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO t_collection_run (run_id, started_at, completed_at, success_count, failed_count) VALUES (?, ?, ?, ?, ?)`, run.RunID, run.StartedAt, run.CompletedAt, run.Success, run.Failed); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO t_collection_status (run_id, platform, display_symbol, collector, source_endpoint, status, error_message, snapshot_ts, latency_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, run.RunID, row.Platform, row.DisplaySymbol, row.Collector, row.SourceEndpoint, row.Status, nullString(row.Error), row.SnapshotTS, row.LatencyMS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadMaxDayPerSymbol returns MAX(day) across every data_source for the
// given (platform, displaySymbol). Top30Backfiller uses this for gap
// detection so a process restart on an already-populated MySQL doesn't
// re-pull the full cold-start window. A zero time.Time is returned when no
// row exists (i.e. a fresh symbol) or when the DB is not attached.
func (s *Store) LoadMaxDayPerSymbol(ctx context.Context, platform, displaySymbol string) (time.Time, error) {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return time.Time{}, nil
	}
	var day sql.NullTime
	err := db.QueryRowContext(ctx,
		`SELECT MAX(day) FROM t_daily_volume_aggregate
		 WHERE platform = ? AND display_symbol = ?`,
		platform, canonicalDailyKey(displaySymbol),
	).Scan(&day)
	if err != nil {
		return time.Time{}, err
	}
	if !day.Valid {
		return time.Time{}, nil
	}
	return day.Time.UTC(), nil
}

func (s *Store) LoadLatestFromDB(ctx context.Context) error {
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT s.platform, s.display_symbol, s.snapshot_ts, s.tier, COALESCE(s.bid_usd,0), COALESCE(s.ask_usd,0), COALESCE(s.total_usd,0), s.depth_status, COALESCE(s.partial_reason,''), COALESCE(s.depth_source,''), COALESCE(s.source_id,''), COALESCE(s.levels_returned,0), COALESCE(s.bid_levels_returned,0), COALESCE(s.ask_levels_returned,0), COALESCE(s.api_level_cap,0), COALESCE(s.farthest_bid_pct,0), COALESCE(s.farthest_ask_pct,0), COALESCE(s.farthest_distance_pct,0), COALESCE(s.source_endpoint,''), COALESCE(s.aggregation_params_json,'{}'), COALESCE(s.strict_complete,0), COALESCE(s.display_available,0), COALESCE(s.policy_acceptance,''), COALESCE(s.physical_limit,0), COALESCE(s.unofficial_ui_endpoint,0), COALESCE(s.error_message,''), COALESCE(s.depth_json,'{}'), COALESCE(s.buy_slippage_json,'{}'), COALESCE(s.sell_slippage_json,'{}') FROM t_orderbook_snapshot s JOIN (SELECT platform, display_symbol, MAX(snapshot_ts) AS snapshot_ts FROM t_orderbook_snapshot GROUP BY platform, display_symbol) latest ON latest.platform = s.platform AND latest.display_symbol = s.display_symbol AND latest.snapshot_ts = s.snapshot_ts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := map[string]domain.PlatformSnapshot{}
	for rows.Next() {
		var (
			platform, displaySymbol, tier, status, reason, source, sourceID, sourceEndpoint, aggJSON, errMsg, depthJSON, buyJSON, sellJSON string
			snapshotTS                                                                                                                     time.Time
			depth                                                                                                                          domain.DepthMetrics
			strictComplete, displayAvailable, physicalLimit, unofficialUIEndpoint                                                          int
		)
		if err := rows.Scan(&platform, &displaySymbol, &snapshotTS, &tier, &depth.BidUSD, &depth.AskUSD, &depth.TotalUSD, &status, &reason, &source, &sourceID, &depth.LevelsReturned, &depth.BidLevelsReturned, &depth.AskLevelsReturned, &depth.APILevelCap, &depth.FarthestBidPct, &depth.FarthestAskPct, &depth.FarthestDistancePct, &sourceEndpoint, &aggJSON, &strictComplete, &displayAvailable, &depth.PolicyAcceptance, &physicalLimit, &unofficialUIEndpoint, &errMsg, &depthJSON, &buyJSON, &sellJSON); err != nil {
			return err
		}
		k := key(platform, displaySymbol)
		row := loaded[k]
		if row.DepthByTier == nil {
			row.Platform = platform
			row.DisplaySymbol = displaySymbol
			row.SnapshotTS = snapshotTS
			row.SourceEndpoint = sourceEndpoint
			row.DepthStatus = status
			row.PartialReason = reason
			row.Error = errMsg
			row.DepthByTier = map[string]domain.DepthMetrics{}
			_ = json.Unmarshal([]byte(buyJSON), &row.BuySlippageBP)
			_ = json.Unmarshal([]byte(sellJSON), &row.SellSlippageBP)
		}
		if tier != "" {
			depth.DepthStatus = status
			depth.PartialReason = reason
			depth.DepthSource = source
			depth.SourceID = sourceID
			depth.SourceEndpoint = sourceEndpoint
			depth.StrictComplete = strictComplete != 0
			depth.DisplayAvailable = displayAvailable != 0
			depth.PhysicalLimit = physicalLimit != 0
			depth.UnofficialUIEndpoint = unofficialUIEndpoint != 0
			_ = json.Unmarshal([]byte(aggJSON), &depth.AggregationParams)
			domain.DeriveDepthMetricsDefaults(row.DepthStatus, &depth)
			row.DepthByTier[tier] = depth
		} else {
			_ = json.Unmarshal([]byte(depthJSON), &row.DepthByTier)
		}
		domain.NormalizePlatformSnapshot(&row)
		loaded[k] = row
	}
	for _, row := range loaded {
		row.DepthStatus, row.PartialReason = summarizeDepthStatus(row.DepthByTier, row.DepthStatus, row.PartialReason)
		s.hydratePlatformSnapshot(row)
	}
	volRows, err := db.QueryContext(ctx, `SELECT platform, display_symbol, snapshot_ts, COALESCE(source_endpoint,''), volume_24h_usd, status, COALESCE(error_message,'') FROM t_symbol_volume_snapshot s WHERE id IN (SELECT MAX(id) FROM t_symbol_volume_snapshot GROUP BY platform, display_symbol)`)
	if err != nil {
		return err
	}
	defer volRows.Close()
	for volRows.Next() {
		var row domain.VolumeSnapshot
		if err := volRows.Scan(&row.Platform, &row.DisplaySymbol, &row.SnapshotTS, &row.SourceEndpoint, &row.Volume24HUSD, &row.Status, &row.Error); err != nil {
			return err
		}
		s.hydrateVolume(row)
	}
	if err := s.loadCoinGeckoPlatformVolumes(ctx, db); err != nil {
		return err
	}
	if err := s.loadDailyVolumeAggregates(ctx, db); err != nil {
		return err
	}
	if err := s.loadTop30(ctx, db); err != nil {
		return err
	}
	return rows.Err()
}

func (s *Store) loadCoinGeckoPlatformVolumes(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, snapshot_ts, COALESCE(volume_24h_usd,0), COALESCE(open_interest_usd,0),
		        COALESCE(data_source,''), COALESCE(source_endpoint,''), COALESCE(status,'')
		 FROM t_coingecko_platform_volume_snapshot s
		 WHERE id IN (SELECT MAX(id) FROM t_coingecko_platform_volume_snapshot GROUP BY platform)`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	batch := []domain.PlatformVolumeAggregate{}
	for rows.Next() {
		var row domain.PlatformVolumeAggregate
		if err := rows.Scan(&row.Platform, &row.SnapshotTS, &row.Volume24HUSD, &row.OpenInterestUSD, &row.DataSource, &row.SourceEndpoint, &row.Status); err != nil {
			return err
		}
		batch = append(batch, row)
	}
	if len(batch) > 0 {
		s.hydrateCoinGeckoPlatformVolumes(batch)
	}
	return rows.Err()
}

func (s *Store) loadDailyVolumeAggregates(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, COALESCE(display_symbol,'') AS display_symbol, day,
		        COALESCE(volume_usd,0), COALESCE(status,''),
		        COALESCE(data_source,'native'), COALESCE(source_endpoint,'')
		 FROM t_daily_volume_aggregate
		 WHERE day >= DATE_SUB(UTC_DATE(), INTERVAL 60 DAY)`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	batch := []domain.DailyVolumeAggregate{}
	for rows.Next() {
		var row domain.DailyVolumeAggregate
		if err := rows.Scan(&row.Platform, &row.DisplaySymbol, &row.Day, &row.Volume24HUSD, &row.Status, &row.DataSource, &row.SourceEndpoint); err != nil {
			return err
		}
		batch = append(batch, row)
	}
	if len(batch) > 0 {
		s.hydrateDailyVolumeAggregates(batch)
	}
	return rows.Err()
}

func (s *Store) loadTop30(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, symbol, rank_no, COALESCE(volume_24h_usd,0), volume_7d_usd, delta_7d_pct,
		        COALESCE(coverage_count,0), COALESCE(edgex_listed,0), COALESCE(suggested_action,''),
		        COALESCE(data_source,''), COALESCE(source_endpoint,''), COALESCE(status,''),
		        snapshot_ts
		 FROM t_top30_snapshot s
		 WHERE (platform, snapshot_ts) IN (SELECT platform, MAX(snapshot_ts) FROM t_top30_snapshot GROUP BY platform)
		 ORDER BY platform, rank_no`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	byPlatform := map[string][]domain.Top30Row{}
	for rows.Next() {
		var (
			row    domain.Top30Row
			v7d    sql.NullFloat64
			d7d    sql.NullFloat64
			listed int
		)
		if err := rows.Scan(&row.Platform, &row.Symbol, &row.Rank, &row.Volume24HUSD, &v7d, &d7d, &row.CoverageCount, &listed, &row.Action, &row.DataSource, &row.SourceEndpoint, &row.Status, &row.SnapshotTS); err != nil {
			return err
		}
		if v7d.Valid {
			val := v7d.Float64
			row.Volume7DUSD = &val
			row.Volume7DStatus = domain.StatusComplete
		} else {
			row.Volume7DStatus = domain.StatusInsufficientHistory
		}
		if d7d.Valid {
			val := d7d.Float64
			row.Delta7DPct = &val
			row.Delta7DStatus = domain.StatusComplete
		} else {
			row.Delta7DStatus = domain.StatusInsufficientHistory
		}
		row.EdgexListed = listed != 0
		byPlatform[row.Platform] = append(byPlatform[row.Platform], row)
	}
	for platform, list := range byPlatform {
		s.hydrateTop30(platform, list)
	}
	return rows.Err()
}

type orderbookDBRow struct {
	Platform              string
	DisplaySymbol         string
	SnapshotTS            time.Time
	Tier                  string
	BidUSD                float64
	AskUSD                float64
	TotalUSD              float64
	DepthStatus           string
	PartialReason         string
	DepthSource           string
	SourceID              string
	LevelsReturned        int
	BidLevelsReturned     int
	AskLevelsReturned     int
	APILevelCap           int
	FarthestBidPct        float64
	FarthestAskPct        float64
	FarthestDistancePct   float64
	SourceEndpoint        string
	AggregationParamsJSON string
	StrictComplete        bool
	DisplayAvailable      bool
	PolicyAcceptance      string
	PhysicalLimit         bool
	UnofficialUIEndpoint  bool
	Error                 string
	DepthJSON             string
	BuySlippageJSON       string
	SellSlippageJSON      string
}

func platformSnapshotOrderbookRows(row domain.PlatformSnapshot) map[string]orderbookDBRow {
	depthJSON, _ := json.Marshal(row.DepthByTier)
	buyJSON, _ := json.Marshal(row.BuySlippageBP)
	sellJSON, _ := json.Marshal(row.SellSlippageBP)
	out := map[string]orderbookDBRow{}
	if len(row.DepthByTier) == 0 {
		depth := domain.DepthMetrics{DepthStatus: row.DepthStatus, PartialReason: row.PartialReason}
		domain.DeriveDepthMetricsDefaults(row.DepthStatus, &depth)
		out[""] = orderbookDBRow{
			Platform:              row.Platform,
			DisplaySymbol:         row.DisplaySymbol,
			SnapshotTS:            row.SnapshotTS,
			DepthStatus:           row.DepthStatus,
			PartialReason:         row.PartialReason,
			SourceEndpoint:        row.SourceEndpoint,
			StrictComplete:        depth.StrictComplete,
			DisplayAvailable:      depth.DisplayAvailable,
			PolicyAcceptance:      depth.PolicyAcceptance,
			PhysicalLimit:         depth.PhysicalLimit,
			UnofficialUIEndpoint:  depth.UnofficialUIEndpoint,
			Error:                 row.Error,
			AggregationParamsJSON: "{}",
			DepthJSON:             string(depthJSON),
			BuySlippageJSON:       string(buyJSON),
			SellSlippageJSON:      string(sellJSON),
		}
		return out
	}
	for tier, depth := range row.DepthByTier {
		domain.DeriveDepthMetricsDefaults(row.DepthStatus, &depth)
		paramsJSON, _ := json.Marshal(depth.AggregationParams)
		if string(paramsJSON) == "null" {
			paramsJSON = []byte("{}")
		}
		status := depth.DepthStatus
		if status == "" {
			status = row.DepthStatus
		}
		sourceEndpoint := depth.SourceEndpoint
		if sourceEndpoint == "" {
			sourceEndpoint = row.SourceEndpoint
		}
		out[tier] = orderbookDBRow{
			Platform:              row.Platform,
			DisplaySymbol:         row.DisplaySymbol,
			SnapshotTS:            row.SnapshotTS,
			Tier:                  tier,
			BidUSD:                depth.BidUSD,
			AskUSD:                depth.AskUSD,
			TotalUSD:              depth.TotalUSD,
			DepthStatus:           status,
			PartialReason:         depth.PartialReason,
			DepthSource:           depth.DepthSource,
			SourceID:              depth.SourceID,
			LevelsReturned:        depth.LevelsReturned,
			BidLevelsReturned:     depth.BidLevelsReturned,
			AskLevelsReturned:     depth.AskLevelsReturned,
			APILevelCap:           depth.APILevelCap,
			FarthestBidPct:        depth.FarthestBidPct,
			FarthestAskPct:        depth.FarthestAskPct,
			FarthestDistancePct:   depth.FarthestDistancePct,
			SourceEndpoint:        sourceEndpoint,
			AggregationParamsJSON: string(paramsJSON),
			StrictComplete:        depth.StrictComplete,
			DisplayAvailable:      depth.DisplayAvailable,
			PolicyAcceptance:      depth.PolicyAcceptance,
			PhysicalLimit:         depth.PhysicalLimit,
			UnofficialUIEndpoint:  depth.UnofficialUIEndpoint,
			Error:                 row.Error,
			DepthJSON:             string(depthJSON),
			BuySlippageJSON:       string(buyJSON),
			SellSlippageJSON:      string(sellJSON),
		}
	}
	return out
}

func sortedOrderbookTiers(rows map[string]orderbookDBRow) []string {
	tiers := make([]string, 0, len(rows))
	for tier := range rows {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	return tiers
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func nullFloat(v float64) sql.NullFloat64 {
	if v == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
