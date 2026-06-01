-- Widen t_listing_signal_observation.fingerprint from VARCHAR(96) to
-- VARCHAR(160). The original migration sized it for short
-- identifier-style fingerprints, but the instrument_diff /
-- announcement_listing producers generate ~191-char plaintext
-- fingerprints (two 64-char sha256 hashes + prefix metadata).
--
-- Under strict sql_mode `INSERT IGNORE` silently demotes the
-- data-too-long error to a warning and drops the row entirely; the
-- subsequent resolve-by-fingerprint SELECT then returns ErrNoRows and
-- callers aborted the entire poll cycle — see the 2026-06-01
-- root-cause writeup for the cascading effect on
-- t_listing_instrument_snapshot freshness.
--
-- The on-disk producers have also switched to sha256-prefixed
-- fingerprints (~80 chars) that already fit in the original 96, but
-- we keep the widened column as defence-in-depth against future
-- drift. Idempotent — re-running is harmless; ApplyMigrations
-- inspects INFORMATION_SCHEMA and skips the ALTER when the column is
-- already at least 160 wide.

ALTER TABLE t_listing_signal_observation
  MODIFY fingerprint VARCHAR(160) NOT NULL;
