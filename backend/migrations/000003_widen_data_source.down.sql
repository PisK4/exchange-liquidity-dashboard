-- Revert data_source columns back to VARCHAR(16). Be careful: this will
-- truncate any row with a longer source label (e.g. coingecko_backfill).
ALTER TABLE t_daily_volume_aggregate
    MODIFY COLUMN data_source VARCHAR(16) NOT NULL DEFAULT 'native';

ALTER TABLE t_top30_snapshot
    MODIFY COLUMN data_source VARCHAR(16) NOT NULL DEFAULT 'coingecko';

ALTER TABLE t_coingecko_platform_volume_snapshot
    MODIFY COLUMN data_source VARCHAR(16) NOT NULL DEFAULT 'coingecko';
