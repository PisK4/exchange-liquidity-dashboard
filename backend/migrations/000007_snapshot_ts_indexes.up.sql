-- Indexes on snapshot_ts (or `day`) for the time-window queries the
-- API surface and the prune-snapshots script issue. Without these the
-- prune DELETE has to do a full table scan, which on a 5M-row
-- t_orderbook_snapshot can take >5 minutes and lock the table for
-- writers. With the index it stays under one second per 10k-row batch.
--
-- Excluded:
--   t_top30_snapshot                        -- has its own 30d auto-prune
--   t_coingecko_platform_volume_snapshot    -- already has idx_cg_platform_ts
ALTER TABLE t_orderbook_snapshot                 ADD INDEX idx_orderbook_snapshot_ts (snapshot_ts);
ALTER TABLE t_book_quality_snapshot              ADD INDEX idx_book_quality_snapshot_ts (snapshot_ts);
ALTER TABLE t_symbol_volume_snapshot             ADD INDEX idx_symbol_volume_snapshot_ts (snapshot_ts);
ALTER TABLE t_collection_status                  ADD INDEX idx_collection_status_snapshot_ts (snapshot_ts);
ALTER TABLE t_daily_volume_aggregate             ADD INDEX idx_daily_volume_aggregate_day (day);
