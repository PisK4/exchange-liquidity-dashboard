ALTER TABLE t_orderbook_snapshot
  DROP COLUMN unofficial_ui_endpoint,
  DROP COLUMN physical_limit,
  DROP COLUMN policy_acceptance,
  DROP COLUMN display_available,
  DROP COLUMN strict_complete;
