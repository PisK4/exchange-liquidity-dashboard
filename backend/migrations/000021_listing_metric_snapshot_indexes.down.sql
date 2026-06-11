ALTER TABLE t_symbol_volume_snapshot
  DROP INDEX idx_symbol_volume_canonical_surface_latest;

ALTER TABLE t_orderbook_snapshot
  DROP INDEX idx_orderbook_canonical_surface_tier_latest;
