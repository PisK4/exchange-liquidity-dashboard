-- Widen data_source from VARCHAR(16) to VARCHAR(32) so values like
-- "coingecko_backfill" (18 chars) can be stored. CoinGecko backfill rows
-- are written by /app/coingecko-backfill (one-shot CLI) and the periodic
-- runDailyBackfill loop, both of which use the longer source label.
ALTER TABLE t_daily_volume_aggregate
    MODIFY COLUMN data_source VARCHAR(32) NOT NULL DEFAULT 'native';

ALTER TABLE t_top30_snapshot
    MODIFY COLUMN data_source VARCHAR(32) NOT NULL DEFAULT 'coingecko';

ALTER TABLE t_coingecko_platform_volume_snapshot
    MODIFY COLUMN data_source VARCHAR(32) NOT NULL DEFAULT 'coingecko';
