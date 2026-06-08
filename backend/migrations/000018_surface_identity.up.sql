ALTER TABLE t_symbol_mapping
  ADD COLUMN lineage VARCHAR(64) NOT NULL DEFAULT '' AFTER instrument_kind,
  ADD COLUMN contract_id VARCHAR(96) NOT NULL DEFAULT '' AFTER api_symbol,
  ADD COLUMN base_asset VARCHAR(64) NOT NULL DEFAULT '' AFTER contract_id,
  ADD COLUMN quote_asset VARCHAR(64) NOT NULL DEFAULT '' AFTER base_asset;

ALTER TABLE t_orderbook_snapshot
  ADD COLUMN platform_group VARCHAR(32) NULL AFTER platform,
  ADD COLUMN display_platform VARCHAR(64) NULL AFTER platform_group,
  ADD COLUMN is_edgex TINYINT(1) NOT NULL DEFAULT 0 AFTER display_platform,
  ADD COLUMN canonical_symbol VARCHAR(64) NULL AFTER display_symbol,
  ADD COLUMN venue_symbol VARCHAR(96) NULL AFTER canonical_symbol,
  ADD COLUMN market_surface VARCHAR(32) NULL AFTER venue_symbol,
  ADD COLUMN instrument_kind VARCHAR(32) NULL AFTER market_surface,
  ADD COLUMN lineage VARCHAR(64) NULL AFTER instrument_kind,
  ADD COLUMN contract_id VARCHAR(96) NULL AFTER lineage,
  ADD COLUMN base_asset VARCHAR(64) NULL AFTER contract_id,
  ADD COLUMN quote_asset VARCHAR(64) NULL AFTER base_asset;

ALTER TABLE t_book_quality_snapshot
  ADD COLUMN platform_group VARCHAR(32) NULL AFTER platform,
  ADD COLUMN display_platform VARCHAR(64) NULL AFTER platform_group,
  ADD COLUMN is_edgex TINYINT(1) NOT NULL DEFAULT 0 AFTER display_platform,
  ADD COLUMN canonical_symbol VARCHAR(64) NULL AFTER display_symbol,
  ADD COLUMN venue_symbol VARCHAR(96) NULL AFTER canonical_symbol,
  ADD COLUMN market_surface VARCHAR(32) NULL AFTER venue_symbol,
  ADD COLUMN instrument_kind VARCHAR(32) NULL AFTER market_surface,
  ADD COLUMN lineage VARCHAR(64) NULL AFTER instrument_kind,
  ADD COLUMN contract_id VARCHAR(96) NULL AFTER lineage,
  ADD COLUMN base_asset VARCHAR(64) NULL AFTER contract_id,
  ADD COLUMN quote_asset VARCHAR(64) NULL AFTER base_asset;

ALTER TABLE t_symbol_volume_snapshot
  ADD COLUMN platform_group VARCHAR(32) NULL AFTER platform,
  ADD COLUMN display_platform VARCHAR(64) NULL AFTER platform_group,
  ADD COLUMN is_edgex TINYINT(1) NOT NULL DEFAULT 0 AFTER display_platform,
  ADD COLUMN canonical_symbol VARCHAR(64) NULL AFTER display_symbol,
  ADD COLUMN venue_symbol VARCHAR(96) NULL AFTER canonical_symbol,
  ADD COLUMN market_surface VARCHAR(32) NULL AFTER venue_symbol,
  ADD COLUMN instrument_kind VARCHAR(32) NULL AFTER market_surface,
  ADD COLUMN lineage VARCHAR(64) NULL AFTER instrument_kind,
  ADD COLUMN contract_id VARCHAR(96) NULL AFTER lineage,
  ADD COLUMN base_asset VARCHAR(64) NULL AFTER contract_id,
  ADD COLUMN quote_asset VARCHAR(64) NULL AFTER base_asset;

ALTER TABLE t_collection_status
  ADD COLUMN platform_group VARCHAR(32) NULL AFTER platform,
  ADD COLUMN display_platform VARCHAR(64) NULL AFTER platform_group,
  ADD COLUMN is_edgex TINYINT(1) NOT NULL DEFAULT 0 AFTER display_platform,
  ADD COLUMN canonical_symbol VARCHAR(64) NULL AFTER display_symbol,
  ADD COLUMN venue_symbol VARCHAR(96) NULL AFTER canonical_symbol,
  ADD COLUMN market_surface VARCHAR(32) NULL AFTER venue_symbol,
  ADD COLUMN instrument_kind VARCHAR(32) NULL AFTER market_surface,
  ADD COLUMN lineage VARCHAR(64) NULL AFTER instrument_kind,
  ADD COLUMN contract_id VARCHAR(96) NULL AFTER lineage,
  ADD COLUMN base_asset VARCHAR(64) NULL AFTER contract_id,
  ADD COLUMN quote_asset VARCHAR(64) NULL AFTER base_asset;

ALTER TABLE t_orderbook_snapshot
  ADD INDEX idx_orderbook_surface_latest (platform, display_symbol, market_surface, instrument_kind, lineage, venue_symbol, contract_id, snapshot_ts);

ALTER TABLE t_symbol_volume_snapshot
  ADD INDEX idx_symbol_volume_surface_latest (platform, display_symbol, market_surface, instrument_kind, lineage, venue_symbol, contract_id, id);
