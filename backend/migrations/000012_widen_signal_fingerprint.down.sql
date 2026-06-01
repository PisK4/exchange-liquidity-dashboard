-- Reverts 000012_widen_signal_fingerprint.up.sql. Be aware that
-- running this against a database that already contains
-- production-shape (sha256-prefixed, ~80-char) fingerprints is safe
-- because they all fit inside 96 chars; reverting against a DB with
-- legacy plaintext (~191-char) fingerprints will fail because MySQL
-- refuses to truncate non-empty rows. In that case clear the table
-- first or skip the down migration.

ALTER TABLE t_listing_signal_observation
  MODIFY fingerprint VARCHAR(96) NOT NULL;
