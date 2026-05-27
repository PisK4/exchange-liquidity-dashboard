-- Listing Agent P1 backend detection + Top30 hot-gap push schema.
-- See architecture/方案设计/EdgeX运营/Listing/
-- 2026-05-27-Listing-Agent-P1-主链路方案设计.md §15 and §23 for the
-- authoritative table contracts. Webhook URLs are NEVER stored; only
-- a coarse target_channel string per delivery row.

CREATE TABLE IF NOT EXISTS t_listing_instrument_snapshot (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  platform VARCHAR(32) NOT NULL,
  market_type VARCHAR(32) NOT NULL,
  api_symbol VARCHAR(96) NOT NULL,
  api_market_id VARCHAR(96) NULL,
  display_symbol VARCHAR(128) NULL,
  canonical_symbol VARCHAR(64) NULL,
  base_asset VARCHAR(64) NULL,
  quote_asset VARCHAR(64) NULL,
  settle_asset VARCHAR(64) NULL,
  market_surface VARCHAR(32) NOT NULL,
  instrument_kind VARCHAR(32) NOT NULL,
  contract_type VARCHAR(64) NULL,
  status_raw VARCHAR(64) NULL,
  status_normalized VARCHAR(32) NOT NULL,
  status_field_name VARCHAR(64) NULL,
  listing_time_ts TIMESTAMP NULL,
  listing_time_field_name VARCHAR(64) NULL,
  delist_flag TINYINT(1) NOT NULL DEFAULT 0,
  first_seen_at TIMESTAMP NOT NULL,
  previous_seen_at TIMESTAMP NULL,
  last_seen_at TIMESTAMP NOT NULL,
  raw_json JSON NOT NULL,
  raw_json_hash VARCHAR(64) NOT NULL,
  normalizer_version VARCHAR(32) NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_instrument (platform, market_type, api_symbol),
  KEY idx_listing_instrument_symbol (canonical_symbol, market_surface, instrument_kind),
  KEY idx_listing_instrument_status (platform, market_type, status_normalized, last_seen_at),
  KEY idx_listing_instrument_listing_time (listing_time_ts)
);

CREATE TABLE IF NOT EXISTS t_listing_announcement (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  platform VARCHAR(32) NOT NULL,
  announcement_id VARCHAR(191) NOT NULL,
  announcement_url VARCHAR(512) NULL,
  title TEXT NOT NULL,
  description TEXT NULL,
  category VARCHAR(128) NULL,
  tags_json JSON NULL,
  language VARCHAR(32) NULL,
  published_at TIMESTAMP NULL,
  source_updated_at TIMESTAMP NULL,
  parsed_market_type VARCHAR(32) NULL,
  effective_listing_time TIMESTAMP NULL,
  parse_confidence VARCHAR(16) NOT NULL,
  raw_payload_json JSON NOT NULL,
  raw_payload_hash VARCHAR(64) NOT NULL,
  parser_version VARCHAR(32) NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_announcement (platform, announcement_id),
  KEY idx_listing_announcement_published (platform, published_at),
  KEY idx_listing_announcement_hash (raw_payload_hash)
);

CREATE TABLE IF NOT EXISTS t_listing_announcement_symbol (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  announcement_id BIGINT NOT NULL,
  canonical_symbol VARCHAR(64) NOT NULL,
  display_symbol VARCHAR(128) NULL,
  market_surface VARCHAR(32) NOT NULL,
  instrument_kind VARCHAR(32) NOT NULL,
  signal_subtype VARCHAR(64) NOT NULL,
  listing_time_ts TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_announcement_symbol (announcement_id, canonical_symbol, market_surface, instrument_kind),
  KEY idx_listing_announcement_symbol_symbol (canonical_symbol, market_surface, instrument_kind)
);

CREATE TABLE IF NOT EXISTS t_listing_signal_observation (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  signal_type VARCHAR(32) NOT NULL,
  signal_subtype VARCHAR(64) NULL,
  source_platform VARCHAR(32) NOT NULL,
  market_type VARCHAR(32) NULL,
  api_symbol VARCHAR(96) NULL,
  api_market_id VARCHAR(96) NULL,
  canonical_symbol VARCHAR(64) NOT NULL,
  display_symbol VARCHAR(128) NULL,
  base_asset VARCHAR(64) NULL,
  quote_asset VARCHAR(64) NULL,
  settle_asset VARCHAR(64) NULL,
  market_surface VARCHAR(32) NOT NULL,
  instrument_kind VARCHAR(32) NOT NULL,
  status_raw VARCHAR(64) NULL,
  status_normalized VARCHAR(32) NULL,
  confidence VARCHAR(16) NULL,
  observed_at TIMESTAMP NOT NULL,
  source_snapshot_ts TIMESTAMP NULL,
  published_at TIMESTAMP NULL,
  listing_time_ts TIMESTAMP NULL,
  source_endpoint VARCHAR(255) NULL,
  source_url VARCHAR(512) NULL,
  fingerprint VARCHAR(96) NOT NULL,
  payload_json JSON NOT NULL,
  raw_payload_json JSON NULL,
  raw_payload_hash VARCHAR(64) NULL,
  fused_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_signal_fingerprint (fingerprint),
  KEY idx_listing_signal_type_time (signal_type, observed_at),
  KEY idx_listing_signal_identity (canonical_symbol, market_surface, instrument_kind, observed_at),
  KEY idx_listing_signal_unfused (fused_at, observed_at)
);

CREATE TABLE IF NOT EXISTS t_listing_candidate (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  canonical_symbol VARCHAR(64) NOT NULL,
  display_symbol VARCHAR(128) NULL,
  market_surface VARCHAR(32) NOT NULL,
  instrument_kind VARCHAR(32) NOT NULL,
  lifecycle_status VARCHAR(64) NOT NULL,
  lifecycle_status_label VARCHAR(128) NULL,
  evidence_kind VARCHAR(64) NOT NULL,
  confidence_level VARCHAR(32) NOT NULL,
  business_score DECIMAL(10,4) NULL,
  business_score_version VARCHAR(32) NULL,
  recommendation VARCHAR(64) NULL,
  recommendation_label VARCHAR(128) NULL,
  source_platforms_json JSON NOT NULL,
  top30_enrichment_json JSON NULL,
  first_observed_at TIMESTAMP NOT NULL,
  last_observed_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_candidate_identity (canonical_symbol, market_surface, instrument_kind),
  KEY idx_listing_candidate_status (lifecycle_status, last_observed_at),
  KEY idx_listing_candidate_score (business_score, last_observed_at)
);

CREATE TABLE IF NOT EXISTS t_listing_candidate_signal (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  candidate_id BIGINT NOT NULL,
  signal_id BIGINT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_candidate_signal (candidate_id, signal_id),
  KEY idx_listing_candidate_signal_signal (signal_id)
);

CREATE TABLE IF NOT EXISTS t_listing_source_state (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  source_key VARCHAR(96) NOT NULL,
  source_type VARCHAR(32) NOT NULL,
  platform VARCHAR(32) NOT NULL,
  status VARCHAR(32) NOT NULL,
  last_success_at TIMESTAMP NULL,
  last_error_at TIMESTAMP NULL,
  consecutive_error_count INT NOT NULL DEFAULT 0,
  schema_drift_count INT NOT NULL DEFAULT 0,
  disabled_until TIMESTAMP NULL,
  last_error TEXT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_source_state (source_key),
  KEY idx_listing_source_state_status (status, disabled_until)
);

CREATE TABLE IF NOT EXISTS t_listing_worker_lease (
  lease_name VARCHAR(96) NOT NULL,
  owner_id VARCHAR(96) NOT NULL,
  expires_at TIMESTAMP(6) NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (lease_name)
);

-- P1b prebuilt (not driven by P1a code yet, kept here so we do not have
-- to ship another migration before the decision callback / risk plan
-- workers land).
CREATE TABLE IF NOT EXISTS t_listing_risk_plan (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  candidate_id BIGINT NOT NULL,
  risk_plan_version VARCHAR(32) NOT NULL,
  template_name VARCHAR(64) NOT NULL,
  max_leverage DECIMAL(18,8) NULL,
  max_position_usd DECIMAL(28,8) NULL,
  leverage_tiers_json JSON NOT NULL,
  funding_initial_mode VARCHAR(64) NULL,
  mm_quote_required TINYINT(1) NOT NULL DEFAULT 0,
  risk_notes_json JSON NULL,
  source_evidence_json JSON NOT NULL,
  generated_at TIMESTAMP NOT NULL,
  approved_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_listing_risk_plan_candidate (candidate_id, generated_at)
);

CREATE TABLE IF NOT EXISTS t_listing_decision (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  candidate_id BIGINT NOT NULL,
  card_id VARCHAR(128) NULL,
  message_id VARCHAR(128) NULL,
  operator_open_id VARCHAR(128) NOT NULL,
  action VARCHAR(64) NOT NULL,
  reason TEXT NULL,
  signature_verified TINYINT(1) NOT NULL DEFAULT 0,
  callback_payload_json JSON NOT NULL,
  callback_ts TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_decision_idempotency (candidate_id, operator_open_id, action, callback_ts),
  KEY idx_listing_decision_candidate (candidate_id, created_at)
);

CREATE TABLE IF NOT EXISTS t_listing_watchlist (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  candidate_id BIGINT NOT NULL,
  canonical_symbol VARCHAR(64) NOT NULL,
  market_surface VARCHAR(32) NOT NULL,
  instrument_kind VARCHAR(32) NOT NULL,
  watch_status VARCHAR(32) NOT NULL,
  watch_reason TEXT NULL,
  source_decision_id BIGINT NULL,
  watch_started_at TIMESTAMP NOT NULL,
  edgex_listed_at TIMESTAMP NULL,
  transferred_to_dashboard_at TIMESTAMP NULL,
  payload_json JSON NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_watchlist_candidate (candidate_id),
  KEY idx_listing_watchlist_status (watch_status, watch_started_at)
);

CREATE TABLE IF NOT EXISTS t_listing_action_dispatch (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  candidate_id BIGINT NOT NULL,
  decision_id BIGINT NOT NULL,
  dispatch_type VARCHAR(64) NOT NULL,
  target_channel VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  outbox_id BIGINT NULL,
  payload_json JSON NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_listing_action_dispatch_status (status, created_at),
  KEY idx_listing_action_dispatch_candidate (candidate_id, created_at)
);

CREATE TABLE IF NOT EXISTS t_listing_delivery_outbox (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  event_type VARCHAR(64) NOT NULL,
  dedupe_key VARCHAR(191) NOT NULL,
  target_channel VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempt_count INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 5,
  next_attempt_at TIMESTAMP NULL,
  payload_json JSON NOT NULL,
  last_error TEXT NULL,
  sent_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_delivery_dedupe (dedupe_key),
  KEY idx_listing_delivery_due (status, next_attempt_at),
  KEY idx_listing_delivery_event (event_type, created_at)
);

CREATE TABLE IF NOT EXISTS t_listing_delivery_attempt (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  outbox_id BIGINT NOT NULL,
  attempt_no INT NOT NULL,
  status VARCHAR(32) NOT NULL,
  http_status INT NULL,
  error_message TEXT NULL,
  attempted_at TIMESTAMP NOT NULL,
  response_body TEXT NULL,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_delivery_attempt (outbox_id, attempt_no),
  KEY idx_listing_delivery_attempt_outbox (outbox_id, attempted_at)
);
