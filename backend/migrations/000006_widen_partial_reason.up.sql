-- Widen partial_reason so multiple comma-joined reasons fit (e.g.
-- "max_precision_shortfall,monotonicity_lower_bound" = 48 chars).
ALTER TABLE t_orderbook_snapshot MODIFY partial_reason VARCHAR(128);
