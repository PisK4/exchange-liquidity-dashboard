SET @idx_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.STATISTICS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_event'
     AND INDEX_NAME = 'idx_activity_event_status_updated'
);
SET @ddl := IF(@idx_exists = 0,
  'CREATE INDEX idx_activity_event_status_updated ON t_activity_event (event_status, updated_at)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.STATISTICS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_event'
     AND INDEX_NAME = 'idx_activity_event_review_auto'
);
SET @ddl := IF(@idx_exists = 0,
  'CREATE INDEX idx_activity_event_review_auto ON t_activity_event (review_status, auto_push_allowed)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @idx_exists := (
  SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.STATISTICS
   WHERE TABLE_SCHEMA = DATABASE()
     AND TABLE_NAME = 't_activity_delivery_outbox'
     AND INDEX_NAME = 'idx_activity_delivery_due'
);
SET @ddl := IF(@idx_exists = 0,
  'CREATE INDEX idx_activity_delivery_due ON t_activity_delivery_outbox (status, next_attempt_at)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
