CREATE TABLE IF NOT EXISTS t_activity_review_item (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  event_id BIGINT UNSIGNED NOT NULL,
  event_version INT NOT NULL,
  content_hash CHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  reason TEXT NULL,
  reviewer VARCHAR(128) NULL,
  reviewed_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  KEY idx_activity_review_status (status, created_at),
  KEY idx_activity_review_event (event_id, event_version)
);

CREATE TABLE IF NOT EXISTS t_activity_delivery_outbox (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  event_type VARCHAR(64) NOT NULL,
  event_id BIGINT UNSIGNED NULL,
  event_version INT NULL,
  dedupe_key VARCHAR(191) NOT NULL UNIQUE,
  target_channel VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempt_count INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 5,
  next_attempt_at DATETIME(3) NULL,
  payload_json JSON NOT NULL,
  last_error TEXT NULL,
  sent_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  CHECK (status IN ('pending','retry','sent','failed','disabled_no_webhook','disabled_missing_secret','muted','redrive_pending')),
  KEY idx_activity_delivery_due (status, next_attempt_at),
  KEY idx_activity_delivery_event (event_type, created_at)
);

CREATE TABLE IF NOT EXISTS t_activity_delivery_attempt (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  outbox_id BIGINT UNSIGNED NOT NULL,
  attempt_no INT NOT NULL,
  status VARCHAR(32) NOT NULL,
  http_status INT NULL,
  error_message TEXT NULL,
  attempted_at DATETIME(3) NOT NULL,
  response_body TEXT NULL,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_activity_delivery_attempt (outbox_id, attempt_no),
  KEY idx_activity_delivery_attempt_outbox (outbox_id, attempted_at)
);

CREATE TABLE IF NOT EXISTS t_activity_worker_lease (
  lease_name VARCHAR(191) NOT NULL PRIMARY KEY,
  owner VARCHAR(191) NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  heartbeat_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL
);
