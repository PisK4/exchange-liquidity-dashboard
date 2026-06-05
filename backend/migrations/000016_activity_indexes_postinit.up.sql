CREATE INDEX idx_activity_event_status_updated ON t_activity_event (event_status, updated_at);
CREATE INDEX idx_activity_event_review_auto ON t_activity_event (review_status, auto_push_allowed);
CREATE INDEX idx_activity_delivery_due ON t_activity_delivery_outbox (status, next_attempt_at);
