SET @col_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_source_state'
     AND COLUMN_NAME = 'last_checked_at'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_source_state ADD COLUMN last_checked_at DATETIME(3) NULL AFTER disabled_until',
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
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE t_activity_source_state ADD COLUMN last_success_at DATETIME(3) NULL AFTER last_checked_at',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
