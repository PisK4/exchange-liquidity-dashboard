ALTER TABLE t_daily_volume_aggregate             DROP INDEX idx_daily_volume_aggregate_day;
ALTER TABLE t_collection_status                  DROP INDEX idx_collection_status_snapshot_ts;
ALTER TABLE t_symbol_volume_snapshot             DROP INDEX idx_symbol_volume_snapshot_ts;
ALTER TABLE t_book_quality_snapshot              DROP INDEX idx_book_quality_snapshot_ts;
ALTER TABLE t_orderbook_snapshot                 DROP INDEX idx_orderbook_snapshot_ts;
