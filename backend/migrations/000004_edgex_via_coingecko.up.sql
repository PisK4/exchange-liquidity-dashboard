-- edgeX 24h / 7d / 30d Share KPIs are now sourced exclusively from
-- CoinGecko (same path as the 9 competitors). The native ticker is still
-- collected for the Liquidity tab's per-symbol display, but its per-day
-- mirror into t_daily_volume_aggregate is gone. Drop any legacy edgeX
-- 'native' daily rows so the priority dedupe inside the Go store does
-- not let stale native data win over fresh CoinGecko rows.
DELETE FROM t_daily_volume_aggregate
WHERE platform = 'edgeX' AND data_source = 'native';
