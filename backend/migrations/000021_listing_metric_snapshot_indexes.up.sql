ALTER TABLE t_orderbook_snapshot
  ADD INDEX idx_orderbook_canonical_surface_tier_latest
    (canonical_symbol, market_surface, tier, platform, snapshot_ts);

ALTER TABLE t_symbol_volume_snapshot
  ADD INDEX idx_symbol_volume_canonical_surface_latest
    (canonical_symbol, market_surface, platform, snapshot_ts);
