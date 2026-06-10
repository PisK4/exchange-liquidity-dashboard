SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_event'
     AND COLUMN_NAME = 'source_bootstrap_completed_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_event DROP COLUMN source_bootstrap_completed_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_event'
     AND COLUMN_NAME = 'source_producer_watermark_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_event DROP COLUMN source_producer_watermark_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_event'
     AND COLUMN_NAME = 'source_observed_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_event DROP COLUMN source_observed_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_source_state'
     AND COLUMN_NAME = 'bootstrap_completed_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_source_state DROP COLUMN bootstrap_completed_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_source_state'
     AND COLUMN_NAME = 'producer_watermark_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_source_state DROP COLUMN producer_watermark_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_source_state'
     AND COLUMN_NAME = 'last_success_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_source_state DROP COLUMN last_success_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_source_state'
     AND COLUMN_NAME = 'last_checked_at'
);
SET @ddl := IF(@col_exists > 0,
  'ALTER TABLE t_activity_source_state DROP COLUMN last_checked_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
