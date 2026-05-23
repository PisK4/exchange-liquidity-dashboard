// prune-snapshots is the operator-facing tool that trims time-series
// snapshot tables down to the last N days of data. It is intentionally
// dry-run by default: an explicit --confirm flag is required to issue
// any DELETE statements.
//
// Excluded from pruning:
//   - t_top30_snapshot          : has its own 30d auto-prune in the writer
//   - t_collection_run          : per-run summary, used by audits
//   - t_runtime_config          : current-config snapshot, not history
//   - t_symbol_mapping          : current catalog, not history
//   - t_exchange_instrument_catalog : current catalog, not history
//
// Each successful run writes a JSON prune-history record under
// $DASHBOARD_DATA_DIR/prune-history/<RFC3339>.json so operators can
// reconstruct exactly what was deleted when. The history file is
// written even on dry-run so plans can be reviewed before --confirm.
//
// Usage:
//
//	go run ./scripts/prune-snapshots --days 30                # dry run
//	go run ./scripts/prune-snapshots --days 30 --confirm      # apply
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// minRetentionDays is the floor for --days. The choice of 7 protects
// against fat-fingered short retentions that would wipe the very same
// day's data the operator is trying to inspect. This is intentionally
// not configurable.
const minRetentionDays = 7

// defaultBatchSize is the LIMIT for each DELETE statement. We loop
// until the affected count is zero. 10,000 keeps lock-hold time per
// statement under one second on the indexed tables.
const defaultBatchSize = 10000

// prunePlan describes a single DELETE plan that the runner will
// execute. Each plan deletes WHERE <timeColumn> < cutoff in
// batch-sized chunks.
type prunePlan struct {
	Table      string    `json:"table"`
	TimeColumn string    `json:"time_column"`
	Cutoff     time.Time `json:"cutoff"`
}

// prunePlanResult is the per-table outcome surfaced in the
// prune-history JSON.
type prunePlanResult struct {
	Table      string    `json:"table"`
	TimeColumn string    `json:"time_column"`
	Cutoff     time.Time `json:"cutoff"`
	Pruned     int64     `json:"pruned_rows"`
	DryRun     bool      `json:"dry_run"`
}

// pruneHistory is the on-disk record. We deliberately surface days +
// confirm + cutoffs so a reviewer can reconstruct the call site.
type pruneHistory struct {
	Timestamp time.Time         `json:"timestamp"`
	Days      int               `json:"days"`
	Confirmed bool              `json:"confirmed"`
	BatchSize int               `json:"batch_size"`
	Results   []prunePlanResult `json:"results"`
}

type cliFlags struct {
	mysqlDSN  string
	days      int
	confirm   bool
	batchSize int
	dataDir   string
}

func main() {
	flags := parseFlags()
	plans, err := planPrune(flags.days, time.Now().UTC())
	if err != nil {
		log.Fatalf("plan prune: %v", err)
	}
	if !flags.confirm {
		log.Printf("dry-run: planning %d table prunes; pass --confirm to apply", len(plans))
	}
	results, err := runPrune(context.Background(), flags, plans)
	if err != nil {
		log.Fatalf("apply prune: %v", err)
	}
	if err := writeHistory(flags, results); err != nil {
		log.Fatalf("write history: %v", err)
	}
	for _, r := range results {
		log.Printf("table=%s cutoff=%s pruned_rows=%d dry_run=%v",
			r.Table, r.Cutoff.Format(time.RFC3339), r.Pruned, r.DryRun)
	}
}

func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.mysqlDSN, "mysql-dsn", os.Getenv("DASHBOARD_MYSQL_DSN"), "MySQL DSN; falls back to $DASHBOARD_MYSQL_DSN")
	flag.IntVar(&f.days, "days", 0, "retention window in days (must be >= 7)")
	flag.BoolVar(&f.confirm, "confirm", false, "set true to actually issue DELETEs (default dry-run)")
	flag.IntVar(&f.batchSize, "batch-size", defaultBatchSize, "rows deleted per DELETE statement")
	flag.StringVar(&f.dataDir, "data-dir", defaultDataDir(), "root directory under which prune-history/ is written")
	flag.Parse()
	return f
}

func defaultDataDir() string {
	if v := os.Getenv("DASHBOARD_DATA_DIR"); v != "" {
		return v
	}
	return "."
}

// planPrune returns the static prune plan for the configured days
// horizon. Returns an error when days < minRetentionDays so dry-run
// runs surface the violation early rather than after opening MySQL.
func planPrune(days int, now time.Time) ([]prunePlan, error) {
	if days < minRetentionDays {
		return nil, fmt.Errorf("--days=%d must be >= %d (refusing to prune recent data)", days, minRetentionDays)
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	tables := []prunePlan{
		{Table: "t_orderbook_snapshot", TimeColumn: "snapshot_ts", Cutoff: cutoff},
		{Table: "t_book_quality_snapshot", TimeColumn: "snapshot_ts", Cutoff: cutoff},
		{Table: "t_symbol_volume_snapshot", TimeColumn: "snapshot_ts", Cutoff: cutoff},
		{Table: "t_platform_volume_snapshot", TimeColumn: "snapshot_ts", Cutoff: cutoff},
		{Table: "t_collection_status", TimeColumn: "snapshot_ts", Cutoff: cutoff},
		{Table: "t_coingecko_platform_volume_snapshot", TimeColumn: "snapshot_ts", Cutoff: cutoff},
		{Table: "t_daily_volume_aggregate", TimeColumn: "day", Cutoff: cutoff},
	}
	return tables, nil
}

// runPrune applies the supplied plan. When flags.confirm is false it
// returns synthesised results with Pruned=0 and DryRun=true and never
// touches the database. When flags.mysqlDSN is empty it returns the
// same dry-run output regardless of confirm so unit tests can drive
// the branching logic without a live MySQL.
func runPrune(ctx context.Context, flags cliFlags, plans []prunePlan) ([]prunePlanResult, error) {
	if !flags.confirm || flags.mysqlDSN == "" {
		out := make([]prunePlanResult, len(plans))
		for i, p := range plans {
			out[i] = prunePlanResult{
				Table:      p.Table,
				TimeColumn: p.TimeColumn,
				Cutoff:     p.Cutoff,
				DryRun:     true,
			}
		}
		return out, nil
	}
	db, err := sql.Open("mysql", flags.mysqlDSN)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	out := make([]prunePlanResult, 0, len(plans))
	for _, plan := range plans {
		pruned, err := executePruneBatched(ctx, db, plan, flags.batchSize)
		if err != nil {
			return out, fmt.Errorf("prune %s: %w", plan.Table, err)
		}
		out = append(out, prunePlanResult{
			Table:      plan.Table,
			TimeColumn: plan.TimeColumn,
			Cutoff:     plan.Cutoff,
			Pruned:     pruned,
			DryRun:     false,
		})
	}
	return out, nil
}

// executePruneBatched issues DELETE ... WHERE col < cutoff LIMIT N in
// a loop until no further rows match. Each batch commits implicitly so
// MySQL replication and reads stay healthy on multi-million-row
// tables.
func executePruneBatched(ctx context.Context, db *sql.DB, plan prunePlan, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if !isAllowedTable(plan.Table) {
		return 0, fmt.Errorf("refusing to prune %q (not on allow-list)", plan.Table)
	}
	if !isAllowedTimeColumn(plan.TimeColumn) {
		return 0, fmt.Errorf("refusing to prune on column %q (not on allow-list)", plan.TimeColumn)
	}
	stmt := fmt.Sprintf("DELETE FROM %s WHERE %s < ? LIMIT %d", plan.Table, plan.TimeColumn, batchSize)
	var total int64
	for {
		res, err := db.ExecContext(ctx, stmt, plan.Cutoff)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(batchSize) {
			return total, nil
		}
	}
}

// isAllowedTable / isAllowedTimeColumn are belt-and-braces guards so a
// future planPrune typo cannot accidentally issue DELETE on a table or
// column the operator did not intend.
func isAllowedTable(name string) bool {
	switch name {
	case "t_orderbook_snapshot",
		"t_book_quality_snapshot",
		"t_symbol_volume_snapshot",
		"t_platform_volume_snapshot",
		"t_collection_status",
		"t_coingecko_platform_volume_snapshot",
		"t_daily_volume_aggregate":
		return true
	}
	return false
}

func isAllowedTimeColumn(name string) bool {
	return name == "snapshot_ts" || name == "day"
}

// writeHistory persists a prune-history record. It is non-fatal if the
// directory cannot be created -- in that case we just log and skip,
// since the operator already has the per-table summary on stderr. We
// do, however, return the error so CI / cron can surface it.
func writeHistory(flags cliFlags, results []prunePlanResult) error {
	if flags.dataDir == "" {
		return errors.New("data-dir not set; refusing to silently drop history")
	}
	dir := filepath.Join(flags.dataDir, "prune-history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir prune-history: %w", err)
	}
	now := time.Now().UTC()
	rec := pruneHistory{
		Timestamp: now,
		Days:      flags.days,
		Confirmed: flags.confirm,
		BatchSize: flags.batchSize,
		Results:   results,
	}
	name := fmt.Sprintf("%s.json", now.Format("2006-01-02T15-04-05Z"))
	path := filepath.Join(dir, name)
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
