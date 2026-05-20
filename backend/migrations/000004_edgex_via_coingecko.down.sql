-- No-op down: the deletion is informational and cannot be reconstructed
-- without re-running the legacy native collector against the original
-- exchange APIs. Leaving this as a marker so the migration sequence stays
-- contiguous.
SELECT 'noop' AS down_migration;
