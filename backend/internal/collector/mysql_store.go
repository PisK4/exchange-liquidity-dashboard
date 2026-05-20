package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-dashboard/backend/internal/domain"
)

const initSchemaSQL = `
CREATE TABLE IF NOT EXISTS t_symbol_mapping (id BIGINT AUTO_INCREMENT PRIMARY KEY, display_symbol VARCHAR(96) NOT NULL, canonical VARCHAR(32) NOT NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, platform VARCHAR(32) NOT NULL, api_symbol VARCHAR(96) NOT NULL, source_endpoint VARCHAR(255) NOT NULL);
CREATE TABLE IF NOT EXISTS t_exchange_instrument_catalog (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, api_symbol VARCHAR(96) NOT NULL, status VARCHAR(32) NOT NULL, updated_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS t_orderbook_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, tier VARCHAR(16) NOT NULL DEFAULT '', bid_usd DECIMAL(28,8), ask_usd DECIMAL(28,8), total_usd DECIMAL(28,8), depth_status VARCHAR(32) NOT NULL, partial_reason VARCHAR(32), depth_source VARCHAR(32), source_id VARCHAR(64), levels_returned INT, bid_levels_returned INT, ask_levels_returned INT, api_level_cap INT, farthest_bid_pct DECIMAL(18,8), farthest_ask_pct DECIMAL(18,8), farthest_distance_pct DECIMAL(18,8), source_endpoint VARCHAR(255), aggregation_params_json JSON, error_message TEXT, depth_json JSON, buy_slippage_json JSON, sell_slippage_json JSON);
CREATE TABLE IF NOT EXISTS t_book_quality_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, spread_bp DECIMAL(18,8), imbalance_pct DECIMAL(18,8));
CREATE TABLE IF NOT EXISTS t_symbol_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL, source_endpoint VARCHAR(255), error_message TEXT);
CREATE TABLE IF NOT EXISTS t_platform_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,8), discount DECIMAL(10,4));
CREATE TABLE IF NOT EXISTS t_top30_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, symbol VARCHAR(96) NOT NULL, rank_no INT NOT NULL, volume_24h_usd DECIMAL(28,8), volume_7d_usd DECIMAL(28,8) NULL, delta_7d_pct DECIMAL(10,4) NULL, coverage_count INT NULL, edgex_listed TINYINT(1) NULL, suggested_action VARCHAR(64) NULL, data_source VARCHAR(32) NOT NULL DEFAULT 'coingecko', source_endpoint VARCHAR(255) NULL, status VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL);
CREATE TABLE IF NOT EXISTS t_daily_volume_aggregate (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96), day DATE NOT NULL, volume_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL, data_source VARCHAR(32) NOT NULL DEFAULT 'native', source_endpoint VARCHAR(255) NULL, snapshot_ts TIMESTAMP NULL, UNIQUE KEY uk_day_platform_symbol_source (day, platform, display_symbol, data_source));
CREATE TABLE IF NOT EXISTS t_coingecko_platform_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,2), open_interest_usd DECIMAL(28,2), data_source VARCHAR(32) NOT NULL DEFAULT 'coingecko', source_endpoint VARCHAR(255) NOT NULL, status VARCHAR(32) NOT NULL, INDEX idx_cg_platform_ts (platform, snapshot_ts));
CREATE TABLE IF NOT EXISTS t_collection_run (id BIGINT AUTO_INCREMENT PRIMARY KEY, run_id VARCHAR(64) NOT NULL, started_at TIMESTAMP NOT NULL, completed_at TIMESTAMP, success_count INT, failed_count INT);
CREATE TABLE IF NOT EXISTS t_collection_status (id BIGINT AUTO_INCREMENT PRIMARY KEY, run_id VARCHAR(64) NOT NULL, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96), collector VARCHAR(32) NOT NULL, source_endpoint VARCHAR(255), status VARCHAR(32) NOT NULL, error_message TEXT, snapshot_ts TIMESTAMP NOT NULL, latency_ms BIGINT NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS t_runtime_config (id BIGINT AUTO_INCREMENT PRIMARY KEY, config_key VARCHAR(96) NOT NULL UNIQUE, config_value TEXT NOT NULL, updated_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
`

func OpenMySQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ApplyMigrations(db *sql.DB) error {
	for _, stmt := range strings.Split(initSchemaSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed at %q: %w", firstLine(stmt), err)
		}
	}
	return nil
}

func (s *Store) AttachDB(db *sql.DB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
}

func (s *Store) persistSymbolMappingsLocked(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM t_symbol_mapping`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO t_symbol_mapping (display_symbol, canonical, market_surface, instrument_kind, platform, api_symbol, source_endpoint) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, sub := range s.cfg.Symbols {
		if _, err := stmt.ExecContext(ctx, sub.DisplaySymbol, sub.Canonical, sub.MarketSurface, sub.InstrumentKind, sub.Platform, sub.APISymbol, sub.SourceEndpoint); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) persistPlatformSnapshot(ctx context.Context, row domain.PlatformSnapshot) error {
	db := s.db
	if db == nil {
		return nil
	}
	rows := platformSnapshotOrderbookRows(row)
	for _, tier := range sortedOrderbookTiers(rows) {
		dbRow := rows[tier]
		_, err := db.ExecContext(ctx, `INSERT INTO t_orderbook_snapshot (platform, display_symbol, snapshot_ts, tier, bid_usd, ask_usd, total_usd, depth_status, partial_reason, depth_source, source_id, levels_returned, bid_levels_returned, ask_levels_returned, api_level_cap, farthest_bid_pct, farthest_ask_pct, farthest_distance_pct, source_endpoint, aggregation_params_json, error_message, depth_json, buy_slippage_json, sell_slippage_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, dbRow.Platform, dbRow.DisplaySymbol, dbRow.SnapshotTS, dbRow.Tier, dbRow.BidUSD, dbRow.AskUSD, dbRow.TotalUSD, dbRow.DepthStatus, nullString(dbRow.PartialReason), nullString(dbRow.DepthSource), nullString(dbRow.SourceID), nullInt(dbRow.LevelsReturned), nullInt(dbRow.BidLevelsReturned), nullInt(dbRow.AskLevelsReturned), nullInt(dbRow.APILevelCap), nullFloat(dbRow.FarthestBidPct), nullFloat(dbRow.FarthestAskPct), nullFloat(dbRow.FarthestDistancePct), dbRow.SourceEndpoint, dbRow.AggregationParamsJSON, nullString(dbRow.Error), dbRow.DepthJSON, dbRow.BuySlippageJSON, dbRow.SellSlippageJSON)
		if err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO t_book_quality_snapshot (platform, display_symbol, snapshot_ts, spread_bp, imbalance_pct) VALUES (?, ?, ?, ?, ?)`, row.Platform, row.DisplaySymbol, row.SnapshotTS, row.SpreadBP, row.Imbalance)
	return err
}

func (s *Store) persistVolume(ctx context.Context, row domain.VolumeSnapshot) error {
	db := s.db
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `INSERT INTO t_symbol_volume_snapshot (platform, display_symbol, snapshot_ts, volume_24h_usd, status, source_endpoint, error_message) VALUES (?, ?, ?, ?, ?, ?, ?)`, row.Platform, row.DisplaySymbol, row.SnapshotTS, row.Volume24HUSD, row.Status, row.SourceEndpoint, nullString(row.Error))
	return err
}

func (s *Store) persistCoinGeckoPlatformVolume(ctx context.Context, row domain.PlatformVolumeAggregate) error {
	db := s.db
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO t_coingecko_platform_volume_snapshot
		   (platform, snapshot_ts, volume_24h_usd, open_interest_usd, data_source, source_endpoint, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.Platform, row.SnapshotTS, row.Volume24HUSD, row.OpenInterestUSD,
		defaultString(row.DataSource, domain.DataSourceCoinGecko),
		row.SourceEndpoint, row.Status,
	)
	return err
}

// persistDailyVolumeAggregate UPSERTs by (day, platform, display_symbol,
// data_source). It also enforces priority semantics across data_source so a
// coingecko_backfill row never displaces a coingecko or native row for the
// same (platform, day, display_symbol), and conversely a fresh coingecko or
// native row evicts any earlier backfill row for that same (platform, day,
// display_symbol). This mirrors the in-memory mergeDailyAggregate dedup so
// reload-from-MySQL surfaces the same shape the running collector observes.
func (s *Store) persistDailyVolumeAggregate(ctx context.Context, row domain.DailyVolumeAggregate) error {
	db := s.db
	if db == nil {
		return nil
	}
	source := defaultString(row.DataSource, domain.DataSourceNative)

	// Backfill rows yield to any live row already covering the same slot.
	if source == domain.DataSourceCoinGeckoBackfill {
		hasLive, err := s.hasLiveDailyRow(ctx, db, row)
		if err != nil {
			return err
		}
		if hasLive {
			return nil
		}
	} else {
		// Live rows clear out any stale backfill row for the same slot so
		// the share aggregation cannot double-count after reload.
		if err := s.deleteBackfillDailyRow(ctx, db, row); err != nil {
			return err
		}
	}

	if row.DisplaySymbol == "" {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM t_daily_volume_aggregate
			   WHERE day = ? AND platform = ? AND display_symbol IS NULL AND data_source = ?`,
			row.Day, row.Platform, source,
		); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO t_daily_volume_aggregate
			   (platform, display_symbol, day, volume_usd, status, data_source, source_endpoint, snapshot_ts)
			 VALUES (?, NULL, ?, ?, ?, ?, ?, ?)`,
			row.Platform, row.Day, row.Volume24HUSD, row.Status, source, nullString(row.SourceEndpoint), nullTimePtr(row.SnapshotTS),
		)
		return err
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO t_daily_volume_aggregate
		   (platform, display_symbol, day, volume_usd, status, data_source, source_endpoint, snapshot_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   volume_usd = VALUES(volume_usd),
		   status = VALUES(status),
		   source_endpoint = VALUES(source_endpoint),
		   snapshot_ts = VALUES(snapshot_ts)`,
		row.Platform, row.DisplaySymbol, row.Day, row.Volume24HUSD, row.Status, source, nullString(row.SourceEndpoint), nullTimePtr(row.SnapshotTS),
	)
	return err
}

// hasLiveDailyRow reports whether a coingecko or native row already exists
// for the (day, platform, display_symbol) slot of `row`. Used to short-circuit
// backfill writes.
func (s *Store) hasLiveDailyRow(ctx context.Context, db *sql.DB, row domain.DailyVolumeAggregate) (bool, error) {
	var n int
	if row.DisplaySymbol == "" {
		err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM t_daily_volume_aggregate
			   WHERE day = ? AND platform = ? AND display_symbol IS NULL
			     AND data_source IN (?, ?)`,
			row.Day, row.Platform, domain.DataSourceCoinGecko, domain.DataSourceNative,
		).Scan(&n)
		return n > 0, err
	}
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM t_daily_volume_aggregate
		   WHERE day = ? AND platform = ? AND display_symbol = ?
		     AND data_source IN (?, ?)`,
		row.Day, row.Platform, row.DisplaySymbol, domain.DataSourceCoinGecko, domain.DataSourceNative,
	).Scan(&n)
	return n > 0, err
}

// deleteBackfillDailyRow removes any coingecko_backfill row for the same
// (day, platform, display_symbol) slot as `row`. Invoked when a fresh
// coingecko / native row arrives so the live row stands alone.
func (s *Store) deleteBackfillDailyRow(ctx context.Context, db *sql.DB, row domain.DailyVolumeAggregate) error {
	if row.DisplaySymbol == "" {
		_, err := db.ExecContext(ctx,
			`DELETE FROM t_daily_volume_aggregate
			   WHERE day = ? AND platform = ? AND display_symbol IS NULL
			     AND data_source = ?`,
			row.Day, row.Platform, domain.DataSourceCoinGeckoBackfill,
		)
		return err
	}
	_, err := db.ExecContext(ctx,
		`DELETE FROM t_daily_volume_aggregate
		   WHERE day = ? AND platform = ? AND display_symbol = ?
		     AND data_source = ?`,
		row.Day, row.Platform, row.DisplaySymbol, domain.DataSourceCoinGeckoBackfill,
	)
	return err
}

func (s *Store) persistTop30(ctx context.Context, platform string, rows []domain.Top30Row) error {
	db := s.db
	if db == nil {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM t_top30_snapshot WHERE platform = ? AND snapshot_ts < DATE_SUB(NOW(), INTERVAL 30 DAY)`,
		platform,
	); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO t_top30_snapshot
		   (platform, symbol, rank_no, volume_24h_usd, volume_7d_usd, delta_7d_pct, coverage_count, edgex_listed, suggested_action, data_source, source_endpoint, status, snapshot_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range rows {
		var v7d, d7d sql.NullFloat64
		if row.Volume7DUSD != nil {
			v7d = sql.NullFloat64{Float64: *row.Volume7DUSD, Valid: true}
		}
		if row.Delta7DPct != nil {
			d7d = sql.NullFloat64{Float64: *row.Delta7DPct, Valid: true}
		}
		if _, err := stmt.ExecContext(ctx,
			platform, row.Symbol, row.Rank, row.Volume24HUSD,
			v7d, d7d,
			nullInt(row.CoverageCount),
			boolToTinyInt(row.EdgexListed),
			nullString(row.Action),
			defaultString(row.DataSource, domain.DataSourceCoinGecko),
			nullString(row.SourceEndpoint),
			row.Status,
			row.SnapshotTS,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolToTinyInt(b bool) sql.NullInt64 {
	if !b {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: 1, Valid: true}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nullTimePtr(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func (s *Store) persistStatus(ctx context.Context, rows []domain.CollectionStatus, run RunSummary) error {
	db := s.db
	if db == nil {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.persistSymbolMappingsLocked(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO t_collection_run (run_id, started_at, completed_at, success_count, failed_count) VALUES (?, ?, ?, ?, ?)`, run.RunID, run.StartedAt, run.CompletedAt, run.Success, run.Failed); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO t_collection_status (run_id, platform, display_symbol, collector, source_endpoint, status, error_message, snapshot_ts, latency_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, run.RunID, row.Platform, row.DisplaySymbol, row.Collector, row.SourceEndpoint, row.Status, nullString(row.Error), row.SnapshotTS, row.LatencyMS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadLatestFromDB(ctx context.Context) error {
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT s.platform, s.display_symbol, s.snapshot_ts, s.tier, COALESCE(s.bid_usd,0), COALESCE(s.ask_usd,0), COALESCE(s.total_usd,0), s.depth_status, COALESCE(s.partial_reason,''), COALESCE(s.depth_source,''), COALESCE(s.source_id,''), COALESCE(s.levels_returned,0), COALESCE(s.bid_levels_returned,0), COALESCE(s.ask_levels_returned,0), COALESCE(s.api_level_cap,0), COALESCE(s.farthest_bid_pct,0), COALESCE(s.farthest_ask_pct,0), COALESCE(s.farthest_distance_pct,0), COALESCE(s.source_endpoint,''), COALESCE(s.aggregation_params_json,'{}'), COALESCE(s.error_message,''), COALESCE(s.depth_json,'{}'), COALESCE(s.buy_slippage_json,'{}'), COALESCE(s.sell_slippage_json,'{}') FROM t_orderbook_snapshot s JOIN (SELECT platform, display_symbol, MAX(snapshot_ts) AS snapshot_ts FROM t_orderbook_snapshot GROUP BY platform, display_symbol) latest ON latest.platform = s.platform AND latest.display_symbol = s.display_symbol AND latest.snapshot_ts = s.snapshot_ts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := map[string]domain.PlatformSnapshot{}
	for rows.Next() {
		var (
			platform, displaySymbol, tier, status, reason, source, sourceID, sourceEndpoint, aggJSON, errMsg, depthJSON, buyJSON, sellJSON string
			snapshotTS                                                                                                                     time.Time
			depth                                                                                                                          domain.DepthMetrics
		)
		if err := rows.Scan(&platform, &displaySymbol, &snapshotTS, &tier, &depth.BidUSD, &depth.AskUSD, &depth.TotalUSD, &status, &reason, &source, &sourceID, &depth.LevelsReturned, &depth.BidLevelsReturned, &depth.AskLevelsReturned, &depth.APILevelCap, &depth.FarthestBidPct, &depth.FarthestAskPct, &depth.FarthestDistancePct, &sourceEndpoint, &aggJSON, &errMsg, &depthJSON, &buyJSON, &sellJSON); err != nil {
			return err
		}
		k := key(platform, displaySymbol)
		row := loaded[k]
		if row.DepthByTier == nil {
			row.Platform = platform
			row.DisplaySymbol = displaySymbol
			row.SnapshotTS = snapshotTS
			row.SourceEndpoint = sourceEndpoint
			row.DepthStatus = status
			row.PartialReason = reason
			row.Error = errMsg
			row.DepthByTier = map[string]domain.DepthMetrics{}
			_ = json.Unmarshal([]byte(buyJSON), &row.BuySlippageBP)
			_ = json.Unmarshal([]byte(sellJSON), &row.SellSlippageBP)
		}
		if tier != "" {
			depth.DepthStatus = status
			depth.PartialReason = reason
			depth.DepthSource = source
			depth.SourceID = sourceID
			depth.SourceEndpoint = sourceEndpoint
			_ = json.Unmarshal([]byte(aggJSON), &depth.AggregationParams)
			row.DepthByTier[tier] = depth
		} else {
			_ = json.Unmarshal([]byte(depthJSON), &row.DepthByTier)
		}
		loaded[k] = row
	}
	for _, row := range loaded {
		row.DepthStatus, row.PartialReason = summarizeDepthStatus(row.DepthByTier, row.DepthStatus, row.PartialReason)
		s.SavePlatformSnapshot(row)
	}
	volRows, err := db.QueryContext(ctx, `SELECT platform, display_symbol, snapshot_ts, COALESCE(source_endpoint,''), volume_24h_usd, status, COALESCE(error_message,'') FROM t_symbol_volume_snapshot s WHERE id IN (SELECT MAX(id) FROM t_symbol_volume_snapshot GROUP BY platform, display_symbol)`)
	if err != nil {
		return err
	}
	defer volRows.Close()
	for volRows.Next() {
		var row domain.VolumeSnapshot
		if err := volRows.Scan(&row.Platform, &row.DisplaySymbol, &row.SnapshotTS, &row.SourceEndpoint, &row.Volume24HUSD, &row.Status, &row.Error); err != nil {
			return err
		}
		s.SaveVolume(row)
	}
	if err := s.loadCoinGeckoPlatformVolumes(ctx, db); err != nil {
		return err
	}
	if err := s.loadDailyVolumeAggregates(ctx, db); err != nil {
		return err
	}
	if err := s.loadTop30(ctx, db); err != nil {
		return err
	}
	return rows.Err()
}

func (s *Store) loadCoinGeckoPlatformVolumes(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, snapshot_ts, COALESCE(volume_24h_usd,0), COALESCE(open_interest_usd,0),
		        COALESCE(data_source,''), COALESCE(source_endpoint,''), COALESCE(status,'')
		 FROM t_coingecko_platform_volume_snapshot s
		 WHERE id IN (SELECT MAX(id) FROM t_coingecko_platform_volume_snapshot GROUP BY platform)`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	batch := []domain.PlatformVolumeAggregate{}
	for rows.Next() {
		var row domain.PlatformVolumeAggregate
		if err := rows.Scan(&row.Platform, &row.SnapshotTS, &row.Volume24HUSD, &row.OpenInterestUSD, &row.DataSource, &row.SourceEndpoint, &row.Status); err != nil {
			return err
		}
		batch = append(batch, row)
	}
	if len(batch) > 0 {
		s.SaveCoinGeckoPlatformVolumes(batch)
	}
	return rows.Err()
}

func (s *Store) loadDailyVolumeAggregates(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, COALESCE(display_symbol,'') AS display_symbol, day,
		        COALESCE(volume_usd,0), COALESCE(status,''),
		        COALESCE(data_source,'native'), COALESCE(source_endpoint,'')
		 FROM t_daily_volume_aggregate
		 WHERE day >= DATE_SUB(UTC_DATE(), INTERVAL 60 DAY)`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	batch := []domain.DailyVolumeAggregate{}
	for rows.Next() {
		var row domain.DailyVolumeAggregate
		if err := rows.Scan(&row.Platform, &row.DisplaySymbol, &row.Day, &row.Volume24HUSD, &row.Status, &row.DataSource, &row.SourceEndpoint); err != nil {
			return err
		}
		batch = append(batch, row)
	}
	if len(batch) > 0 {
		s.SaveDailyVolumeAggregates(batch)
	}
	return rows.Err()
}

func (s *Store) loadTop30(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, symbol, rank_no, COALESCE(volume_24h_usd,0), volume_7d_usd, delta_7d_pct,
		        COALESCE(coverage_count,0), COALESCE(edgex_listed,0), COALESCE(suggested_action,''),
		        COALESCE(data_source,''), COALESCE(source_endpoint,''), COALESCE(status,''),
		        snapshot_ts
		 FROM t_top30_snapshot s
		 WHERE (platform, snapshot_ts) IN (SELECT platform, MAX(snapshot_ts) FROM t_top30_snapshot GROUP BY platform)
		 ORDER BY platform, rank_no`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	byPlatform := map[string][]domain.Top30Row{}
	for rows.Next() {
		var (
			row    domain.Top30Row
			v7d    sql.NullFloat64
			d7d    sql.NullFloat64
			listed int
		)
		if err := rows.Scan(&row.Platform, &row.Symbol, &row.Rank, &row.Volume24HUSD, &v7d, &d7d, &row.CoverageCount, &listed, &row.Action, &row.DataSource, &row.SourceEndpoint, &row.Status, &row.SnapshotTS); err != nil {
			return err
		}
		if v7d.Valid {
			val := v7d.Float64
			row.Volume7DUSD = &val
			row.Volume7DStatus = domain.StatusComplete
		} else {
			row.Volume7DStatus = domain.StatusInsufficientHistory
		}
		if d7d.Valid {
			val := d7d.Float64
			row.Delta7DPct = &val
			row.Delta7DStatus = domain.StatusComplete
		} else {
			row.Delta7DStatus = domain.StatusInsufficientHistory
		}
		row.EdgexListed = listed != 0
		byPlatform[row.Platform] = append(byPlatform[row.Platform], row)
	}
	for platform, list := range byPlatform {
		s.SaveTop30(platform, list)
	}
	return rows.Err()
}

type orderbookDBRow struct {
	Platform              string
	DisplaySymbol         string
	SnapshotTS            time.Time
	Tier                  string
	BidUSD                float64
	AskUSD                float64
	TotalUSD              float64
	DepthStatus           string
	PartialReason         string
	DepthSource           string
	SourceID              string
	LevelsReturned        int
	BidLevelsReturned     int
	AskLevelsReturned     int
	APILevelCap           int
	FarthestBidPct        float64
	FarthestAskPct        float64
	FarthestDistancePct   float64
	SourceEndpoint        string
	AggregationParamsJSON string
	Error                 string
	DepthJSON             string
	BuySlippageJSON       string
	SellSlippageJSON      string
}

func platformSnapshotOrderbookRows(row domain.PlatformSnapshot) map[string]orderbookDBRow {
	depthJSON, _ := json.Marshal(row.DepthByTier)
	buyJSON, _ := json.Marshal(row.BuySlippageBP)
	sellJSON, _ := json.Marshal(row.SellSlippageBP)
	out := map[string]orderbookDBRow{}
	if len(row.DepthByTier) == 0 {
		out[""] = orderbookDBRow{
			Platform:              row.Platform,
			DisplaySymbol:         row.DisplaySymbol,
			SnapshotTS:            row.SnapshotTS,
			DepthStatus:           row.DepthStatus,
			PartialReason:         row.PartialReason,
			SourceEndpoint:        row.SourceEndpoint,
			Error:                 row.Error,
			AggregationParamsJSON: "{}",
			DepthJSON:             string(depthJSON),
			BuySlippageJSON:       string(buyJSON),
			SellSlippageJSON:      string(sellJSON),
		}
		return out
	}
	for tier, depth := range row.DepthByTier {
		paramsJSON, _ := json.Marshal(depth.AggregationParams)
		if string(paramsJSON) == "null" {
			paramsJSON = []byte("{}")
		}
		status := depth.DepthStatus
		if status == "" {
			status = row.DepthStatus
		}
		sourceEndpoint := depth.SourceEndpoint
		if sourceEndpoint == "" {
			sourceEndpoint = row.SourceEndpoint
		}
		out[tier] = orderbookDBRow{
			Platform:              row.Platform,
			DisplaySymbol:         row.DisplaySymbol,
			SnapshotTS:            row.SnapshotTS,
			Tier:                  tier,
			BidUSD:                depth.BidUSD,
			AskUSD:                depth.AskUSD,
			TotalUSD:              depth.TotalUSD,
			DepthStatus:           status,
			PartialReason:         depth.PartialReason,
			DepthSource:           depth.DepthSource,
			SourceID:              depth.SourceID,
			LevelsReturned:        depth.LevelsReturned,
			BidLevelsReturned:     depth.BidLevelsReturned,
			AskLevelsReturned:     depth.AskLevelsReturned,
			APILevelCap:           depth.APILevelCap,
			FarthestBidPct:        depth.FarthestBidPct,
			FarthestAskPct:        depth.FarthestAskPct,
			FarthestDistancePct:   depth.FarthestDistancePct,
			SourceEndpoint:        sourceEndpoint,
			AggregationParamsJSON: string(paramsJSON),
			Error:                 row.Error,
			DepthJSON:             string(depthJSON),
			BuySlippageJSON:       string(buyJSON),
			SellSlippageJSON:      string(sellJSON),
		}
	}
	return out
}

func sortedOrderbookTiers(rows map[string]orderbookDBRow) []string {
	tiers := make([]string, 0, len(rows))
	for tier := range rows {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	return tiers
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func nullFloat(v float64) sql.NullFloat64 {
	if v == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
