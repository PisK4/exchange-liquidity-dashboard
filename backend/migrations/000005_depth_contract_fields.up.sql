ALTER TABLE t_orderbook_snapshot
  ADD COLUMN strict_complete TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN display_available TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN policy_acceptance VARCHAR(32) NULL,
  ADD COLUMN physical_limit TINYINT(1) NOT NULL DEFAULT 0,
  ADD COLUMN unofficial_ui_endpoint TINYINT(1) NOT NULL DEFAULT 0;

UPDATE t_orderbook_snapshot SET
  strict_complete = CASE WHEN depth_status IN ('complete', 'aggregated_orderbook', 'ws_limited_depth') THEN 1 ELSE 0 END,
  display_available = CASE WHEN depth_status IN ('complete', 'aggregated_orderbook', 'ws_limited_depth', 'partial') AND physical_limit = 0 THEN 1 ELSE 0 END,
  policy_acceptance = CASE
    WHEN policy_acceptance IS NOT NULL AND policy_acceptance <> '' THEN policy_acceptance
    WHEN depth_status = 'complete' THEN 'raw_strict'
    WHEN depth_status IN ('aggregated_orderbook', 'ws_limited_depth') THEN 'aggregated_strict'
    WHEN depth_status = 'partial' THEN 'loose_lower_bound'
    ELSE policy_acceptance
  END
WHERE policy_acceptance IS NULL OR policy_acceptance = '';
