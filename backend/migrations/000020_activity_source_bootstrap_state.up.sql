SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_source_state'
     AND COLUMN_NAME = 'producer_watermark_at'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_source_state ADD COLUMN producer_watermark_at DATETIME(3) NULL AFTER last_success_at',
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
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_source_state ADD COLUMN bootstrap_completed_at DATETIME(3) NULL AFTER producer_watermark_at',
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
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_event ADD COLUMN source_observed_at DATETIME(3) NULL AFTER rich_fields_summary_json',
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
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_event ADD COLUMN source_producer_watermark_at DATETIME(3) NULL AFTER source_observed_at',
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
     AND COLUMN_NAME = 'source_bootstrap_completed_at'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_event ADD COLUMN source_bootstrap_completed_at DATETIME(3) NULL AFTER source_producer_watermark_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
