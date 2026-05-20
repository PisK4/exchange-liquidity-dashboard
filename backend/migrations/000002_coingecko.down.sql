-- v2 plan M3: down migration drops only the new tables introduced by 000002.
-- The extension columns on t_top30_snapshot and t_daily_volume_aggregate are
-- intentionally kept because real data may have been written into them; a
-- rollback that DROPped those columns would silently destroy production rows.
DROP TABLE IF EXISTS t_coingecko_platform_volume_snapshot;
