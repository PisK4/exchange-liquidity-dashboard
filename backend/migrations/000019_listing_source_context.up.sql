ALTER TABLE t_listing_source_state
  ADD COLUMN source_context_json JSON NULL AFTER last_error;
