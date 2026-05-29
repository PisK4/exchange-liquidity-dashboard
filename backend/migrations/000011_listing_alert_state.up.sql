-- Liquidity Alert (#10 liquidity_lag / #11 worst_depth) state table.
-- See architecture/方案设计/EdgeX运营/Listing/
-- 2026-05-29-Listing-Agent-Dashboard-Liquidity-Alerts-#10-#11.md §3.2
-- and docs/feat/listing-agent-liquidity-alert.md §5 for the authoritative
-- state-machine contract.
--
-- The table is generic by design: alert_kind is a free-form discriminator
-- so future #12 / #13 alert kinds can be added without another migration.

CREATE TABLE IF NOT EXISTS t_listing_alert_state (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  alert_kind VARCHAR(64) NOT NULL,
  canonical_symbol VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  severity_seq INT NOT NULL DEFAULT 1,
  reissue_count INT NOT NULL DEFAULT 0,
  clear_streak INT NOT NULL DEFAULT 0,
  first_triggered_at TIMESTAMP NOT NULL,
  last_pushed_at TIMESTAMP NULL,
  last_evaluated_at TIMESTAMP NOT NULL,
  last_severity_json JSON NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_listing_alert_state (alert_kind, canonical_symbol),
  KEY idx_listing_alert_state_status (alert_kind, status, last_evaluated_at)
);
