CREATE TABLE IF NOT EXISTS t_activity_event_symbol (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  event_id BIGINT UNSIGNED NOT NULL,
  canonical_symbol VARCHAR(64) NOT NULL,
  display_symbol VARCHAR(128) NULL,
  market_surface VARCHAR(32) NOT NULL,
  role VARCHAR(32) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_activity_event_symbol (event_id, canonical_symbol, market_surface, role),
  KEY idx_activity_event_symbol_lookup (canonical_symbol, market_surface)
);

CREATE TABLE IF NOT EXISTS t_activity_digest (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  digest_kind VARCHAR(32) NOT NULL,
  digest_key VARCHAR(64) NOT NULL,
  digest_date DATE NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  summary_json JSON NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_activity_digest_key (digest_kind, digest_key)
);

CREATE TABLE IF NOT EXISTS t_activity_digest_item (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  digest_id BIGINT UNSIGNED NOT NULL,
  event_id BIGINT UNSIGNED NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  severity VARCHAR(32) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_activity_digest_item (digest_id, event_id),
  KEY idx_activity_digest_item_event (event_id)
);
