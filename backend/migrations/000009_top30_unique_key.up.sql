DELETE t1 FROM t_top30_snapshot t1
INNER JOIN t_top30_snapshot t2
WHERE t1.id < t2.id
  AND t1.platform = t2.platform
  AND t1.symbol = t2.symbol
  AND t1.snapshot_ts = t2.snapshot_ts;

ALTER TABLE t_top30_snapshot
    ADD UNIQUE KEY uk_top30_platform_symbol_ts (platform, symbol, snapshot_ts);
