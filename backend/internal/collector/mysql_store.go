package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-dashboard/backend/internal/domain"
)

const initSchemaSQL = `
CREATE TABLE IF NOT EXISTS t_symbol_mapping (id BIGINT AUTO_INCREMENT PRIMARY KEY, display_symbol VARCHAR(96) NOT NULL, canonical VARCHAR(32) NOT NULL, market_surface VARCHAR(32) NOT NULL, instrument_kind VARCHAR(32) NOT NULL, platform VARCHAR(32) NOT NULL, api_symbol VARCHAR(96) NOT NULL, source_endpoint VARCHAR(255) NOT NULL);
CREATE TABLE IF NOT EXISTS t_exchange_instrument_catalog (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, api_symbol VARCHAR(96) NOT NULL, status VARCHAR(32) NOT NULL, updated_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS t_orderbook_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, depth_status VARCHAR(32) NOT NULL, partial_reason VARCHAR(32), levels_returned INT, farthest_distance_pct DECIMAL(18,8), source_endpoint VARCHAR(255), error_message TEXT, depth_json JSON, buy_slippage_json JSON, sell_slippage_json JSON);
CREATE TABLE IF NOT EXISTS t_book_quality_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, spread_bp DECIMAL(18,8), imbalance_pct DECIMAL(18,8));
CREATE TABLE IF NOT EXISTS t_symbol_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL, source_endpoint VARCHAR(255), error_message TEXT);
CREATE TABLE IF NOT EXISTS t_platform_volume_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL, volume_24h_usd DECIMAL(28,8), discount DECIMAL(10,4));
CREATE TABLE IF NOT EXISTS t_top30_snapshot (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, symbol VARCHAR(96) NOT NULL, rank_no INT NOT NULL, volume_24h_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL, snapshot_ts TIMESTAMP NOT NULL);
CREATE TABLE IF NOT EXISTS t_daily_volume_aggregate (id BIGINT AUTO_INCREMENT PRIMARY KEY, platform VARCHAR(32) NOT NULL, display_symbol VARCHAR(96), day DATE NOT NULL, volume_usd DECIMAL(28,8), status VARCHAR(32) NOT NULL);
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
	depthJSON, _ := json.Marshal(row.DepthByTier)
	buyJSON, _ := json.Marshal(row.BuySlippageBP)
	sellJSON, _ := json.Marshal(row.SellSlippageBP)
	_, err := db.ExecContext(ctx, `INSERT INTO t_orderbook_snapshot (platform, display_symbol, snapshot_ts, depth_status, partial_reason, levels_returned, farthest_distance_pct, source_endpoint, error_message, depth_json, buy_slippage_json, sell_slippage_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, row.Platform, row.DisplaySymbol, row.SnapshotTS, row.DepthStatus, nullString(row.PartialReason), len(row.DepthByTier), nil, row.SourceEndpoint, nullString(row.Error), string(depthJSON), string(buyJSON), string(sellJSON))
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO t_book_quality_snapshot (platform, display_symbol, snapshot_ts, spread_bp, imbalance_pct) VALUES (?, ?, ?, ?, ?)`, row.Platform, row.DisplaySymbol, row.SnapshotTS, row.SpreadBP, row.Imbalance)
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
	rows, err := db.QueryContext(ctx, `SELECT platform, display_symbol, snapshot_ts, depth_status, COALESCE(partial_reason,''), COALESCE(source_endpoint,''), COALESCE(error_message,''), COALESCE(depth_json,'{}'), COALESCE(buy_slippage_json,'{}'), COALESCE(sell_slippage_json,'{}') FROM t_orderbook_snapshot s WHERE id IN (SELECT MAX(id) FROM t_orderbook_snapshot GROUP BY platform, display_symbol)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row domain.PlatformSnapshot
		var depthJSON, buyJSON, sellJSON string
		if err := rows.Scan(&row.Platform, &row.DisplaySymbol, &row.SnapshotTS, &row.DepthStatus, &row.PartialReason, &row.SourceEndpoint, &row.Error, &depthJSON, &buyJSON, &sellJSON); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(depthJSON), &row.DepthByTier)
		_ = json.Unmarshal([]byte(buyJSON), &row.BuySlippageBP)
		_ = json.Unmarshal([]byte(sellJSON), &row.SellSlippageBP)
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
	return rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
