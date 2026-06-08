ALTER TABLE t_symbol_volume_snapshot
  DROP INDEX idx_symbol_volume_surface_latest;

ALTER TABLE t_orderbook_snapshot
  DROP INDEX idx_orderbook_surface_latest;

ALTER TABLE t_collection_status
  DROP COLUMN quote_asset,
  DROP COLUMN base_asset,
  DROP COLUMN contract_id,
  DROP COLUMN lineage,
  DROP COLUMN instrument_kind,
  DROP COLUMN market_surface,
  DROP COLUMN venue_symbol,
  DROP COLUMN canonical_symbol,
  DROP COLUMN is_edgex,
  DROP COLUMN display_platform,
  DROP COLUMN platform_group;

ALTER TABLE t_symbol_volume_snapshot
  DROP COLUMN quote_asset,
  DROP COLUMN base_asset,
  DROP COLUMN contract_id,
  DROP COLUMN lineage,
  DROP COLUMN instrument_kind,
  DROP COLUMN market_surface,
  DROP COLUMN venue_symbol,
  DROP COLUMN canonical_symbol,
  DROP COLUMN is_edgex,
  DROP COLUMN display_platform,
  DROP COLUMN platform_group;

ALTER TABLE t_book_quality_snapshot
  DROP COLUMN quote_asset,
  DROP COLUMN base_asset,
  DROP COLUMN contract_id,
  DROP COLUMN lineage,
  DROP COLUMN instrument_kind,
  DROP COLUMN market_surface,
  DROP COLUMN venue_symbol,
  DROP COLUMN canonical_symbol,
  DROP COLUMN is_edgex,
  DROP COLUMN display_platform,
  DROP COLUMN platform_group;

ALTER TABLE t_orderbook_snapshot
  DROP COLUMN quote_asset,
  DROP COLUMN base_asset,
  DROP COLUMN contract_id,
  DROP COLUMN lineage,
  DROP COLUMN instrument_kind,
  DROP COLUMN market_surface,
  DROP COLUMN venue_symbol,
  DROP COLUMN canonical_symbol,
  DROP COLUMN is_edgex,
  DROP COLUMN display_platform,
  DROP COLUMN platform_group;

ALTER TABLE t_symbol_mapping
  DROP COLUMN quote_asset,
  DROP COLUMN base_asset,
  DROP COLUMN contract_id,
  DROP COLUMN lineage;
