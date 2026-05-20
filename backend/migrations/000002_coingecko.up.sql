-- v2 plan: introduce CoinGecko-sourced rollup tables and extend existing
-- Top30 / daily aggregate tables so the dashboard can persist competitor
-- 24h volume, 7d / 30d windows, and live Top30 rankings without polluting
-- the native orderbook tables.

-- New table: latest CoinGecko per-platform 24h aggregate. Inserts only;
-- LoadLatestFromDB picks the row with MAX(id) per platform at boot.
CREATE TABLE IF NOT EXISTS t_coingecko_platform_volume_snapshot (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    platform VARCHAR(32) NOT NULL,
    snapshot_ts TIMESTAMP NOT NULL,
    volume_24h_usd DECIMAL(28,2),
    open_interest_usd DECIMAL(28,2),
    data_source VARCHAR(16) NOT NULL DEFAULT 'coingecko',
    source_endpoint VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    INDEX idx_cg_platform_ts (platform, snapshot_ts)
);

-- Extend Top30 with CoinGecko-derived columns. Volume7DUSD / Delta7DPct stay
-- NULL until daily aggregates accumulate 7 contiguous days; status fields on
-- Top30Row already encode insufficient_history / partial / complete.
ALTER TABLE t_top30_snapshot
    ADD COLUMN volume_7d_usd DECIMAL(28,8) NULL AFTER volume_24h_usd,
    ADD COLUMN delta_7d_pct DECIMAL(10,4) NULL AFTER volume_7d_usd,
    ADD COLUMN coverage_count INT NULL AFTER delta_7d_pct,
    ADD COLUMN edgex_listed TINYINT(1) NULL AFTER coverage_count,
    ADD COLUMN suggested_action VARCHAR(64) NULL AFTER edgex_listed,
    ADD COLUMN data_source VARCHAR(16) NOT NULL DEFAULT 'coingecko' AFTER suggested_action,
    ADD COLUMN source_endpoint VARCHAR(255) NULL AFTER data_source;

-- Extend daily volume aggregate so it can host both native (edgeX) and
-- CoinGecko-sourced rows for the same UTC day, with a per-(day, platform,
-- symbol, source) uniqueness constraint so the collector's UPSERT
-- (run-every-15-minutes) is idempotent.
ALTER TABLE t_daily_volume_aggregate
    ADD COLUMN data_source VARCHAR(16) NOT NULL DEFAULT 'native' AFTER status,
    ADD COLUMN source_endpoint VARCHAR(255) NULL AFTER data_source,
    ADD COLUMN snapshot_ts TIMESTAMP NULL AFTER source_endpoint;

ALTER TABLE t_daily_volume_aggregate
    ADD UNIQUE KEY uk_day_platform_symbol_source (day, platform, display_symbol, data_source);
