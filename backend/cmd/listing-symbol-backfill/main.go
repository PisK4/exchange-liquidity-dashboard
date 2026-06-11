// Command listing-symbol-backfill rewrites Listing Agent business identity
// columns after a symbol alias fix. It is deliberately dry-run by default.
//
// Example:
//
//	go run ./cmd/listing-symbol-backfill \
//	  --from-canonical=EBAYSTOCK --from-surface=perp --from-kind=canonical \
//	  --to-canonical=EBAY --to-surface=synthetic_futures --to-kind=synthetic \
//	  --to-display='EBAY-USDT (perp)'
//
// Add --execute only after the dry-run report is reviewed. Execute mode first
// acquires the normal Listing Agent run-once lease and refuses to proceed when
// pending/retry delivery outbox payloads still contain the old symbol text.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/collector"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing"
)

const listingRunOnceLease = "listing:run_once"

type identity struct {
	Canonical      string
	DisplaySymbol  string
	MarketSurface  string
	InstrumentKind string
}

type tablePlan struct {
	Name         string
	Where        string
	Args         []any
	SetDisplay   bool
	CandidateIDs []int64
}

type tableReport struct {
	Table string
	Rows  int64
}

type report struct {
	Tables                 []tableReport
	PendingRetryOutboxRows int64
	SentOutboxRows         int64
	Executed               bool
}

func main() {
	fromCanonical := flag.String("from-canonical", "", "old canonical_symbol, e.g. EBAYSTOCK")
	fromSurface := flag.String("from-surface", "", "old market_surface, e.g. perp")
	fromKind := flag.String("from-kind", "", "old instrument_kind, e.g. canonical")
	toCanonical := flag.String("to-canonical", "", "new canonical_symbol, e.g. EBAY")
	toSurface := flag.String("to-surface", "", "new market_surface, e.g. synthetic_futures")
	toKind := flag.String("to-kind", "", "new instrument_kind, e.g. synthetic")
	toDisplay := flag.String("to-display", "", "new display_symbol; defaults from --to-canonical/--to-surface")
	mysqlDSN := flag.String("mysql-dsn", os.Getenv("OPS_INTELLIGENCE_MYSQL_DSN"), "MySQL DSN for the EdgeX Ops Intelligence schema")
	configDir := flag.String("config-dir", "../config", "directory containing EdgeX Ops Intelligence yaml configs, used when --mysql-dsn is empty")
	execute := flag.Bool("execute", false, "apply changes; default is dry-run only")
	ownerID := flag.String("owner-id", hostnameOwnerID(), "owner id for the Listing Agent lease in execute mode")
	leaseTTL := flag.Duration("lease-ttl", 2*time.Minute, "Listing Agent lease TTL in execute mode")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall timeout for dry-run or execute")
	flag.Parse()

	from := identity{
		Canonical:      strings.ToUpper(strings.TrimSpace(*fromCanonical)),
		MarketSurface:  strings.ToLower(strings.TrimSpace(*fromSurface)),
		InstrumentKind: strings.ToLower(strings.TrimSpace(*fromKind)),
	}
	to := identity{
		Canonical:      strings.ToUpper(strings.TrimSpace(*toCanonical)),
		DisplaySymbol:  strings.TrimSpace(*toDisplay),
		MarketSurface:  strings.ToLower(strings.TrimSpace(*toSurface)),
		InstrumentKind: strings.ToLower(strings.TrimSpace(*toKind)),
	}
	if to.DisplaySymbol == "" {
		to.DisplaySymbol = defaultDisplaySymbol(to.Canonical, to.MarketSurface)
	}
	if err := validateIdentities(from, to); err != nil {
		log.Fatalf("invalid identity flags: %v", err)
	}

	resolvedDSN := strings.TrimSpace(*mysqlDSN)
	if resolvedDSN == "" {
		cfg, err := config.Load(*configDir)
		if err != nil {
			log.Fatalf("load config for MySQL DSN fallback: %v", err)
		}
		resolvedDSN = cfg.MySQLDSN()
	}
	if resolvedDSN == "" {
		log.Fatalf("--mysql-dsn, OPS_INTELLIGENCE_MYSQL_DSN, or Database config is required")
	}

	db, err := collector.OpenMySQL(resolvedDSN)
	if err != nil {
		log.Fatalf("connect mysql: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *execute {
		repo := listing.NewRepository(db)
		ok, err := repo.AcquireLease(ctx, listingRunOnceLease, *ownerID, *leaseTTL)
		if err != nil {
			log.Fatalf("acquire listing lease: %v", err)
		}
		if !ok {
			log.Fatalf("listing lease %q is held by another owner; aborting execute", listingRunOnceLease)
		}
		defer func() {
			if err := repo.ReleaseLease(context.Background(), listingRunOnceLease, *ownerID); err != nil {
				log.Printf("release listing lease: %v", err)
			}
		}()
	}

	rep, err := runBackfill(ctx, db, from, to, *execute)
	if err != nil {
		log.Fatalf("listing symbol backfill failed: %v", err)
	}
	printReport(from, to, rep)
	if !*execute {
		log.Printf("dry-run only; re-run with --execute after reviewing the report")
	}
}

func validateIdentities(from, to identity) error {
	missing := []string{}
	if from.Canonical == "" {
		missing = append(missing, "from-canonical")
	}
	if from.MarketSurface == "" {
		missing = append(missing, "from-surface")
	}
	if from.InstrumentKind == "" {
		missing = append(missing, "from-kind")
	}
	if to.Canonical == "" {
		missing = append(missing, "to-canonical")
	}
	if to.MarketSurface == "" {
		missing = append(missing, "to-surface")
	}
	if to.InstrumentKind == "" {
		missing = append(missing, "to-kind")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}
	if from.Canonical == to.Canonical && from.MarketSurface == to.MarketSurface && from.InstrumentKind == to.InstrumentKind {
		return errors.New("source and target identity are identical")
	}
	return nil
}

func runBackfill(ctx context.Context, db *sql.DB, from, to identity, execute bool) (report, error) {
	var rep report
	pendingRetry, sent, err := countOutboxPayloads(ctx, db, from.Canonical)
	if err != nil {
		return rep, err
	}
	rep.PendingRetryOutboxRows = pendingRetry
	rep.SentOutboxRows = sent
	if execute && pendingRetry > 0 {
		return rep, fmt.Errorf("found %d pending/retry outbox rows containing %q; fail-closed before rewriting identity", pendingRetry, from.Canonical)
	}

	candidateIDs, sourceCandidateCount, targetCandidateCount, err := candidateState(ctx, db, from, to)
	if err != nil {
		return rep, err
	}
	if execute && sourceCandidateCount > 0 && targetCandidateCount > 0 {
		return rep, fmt.Errorf("target candidate identity %s/%s/%s already exists while source candidate exists; manual merge required", to.Canonical, to.MarketSurface, to.InstrumentKind)
	}

	plans := []tablePlan{
		identityTablePlan("t_listing_instrument_snapshot", from, true),
		identityTablePlan("t_listing_signal_observation", from, true),
		identityTablePlan("t_listing_announcement_symbol", from, true),
		identityTablePlan("t_listing_candidate", from, true),
		watchlistTablePlan(from, candidateIDs),
	}

	if !execute {
		for _, p := range plans {
			rows, err := countPlan(ctx, db, p)
			if err != nil {
				return rep, err
			}
			rep.Tables = append(rep.Tables, tableReport{Table: p.Name, Rows: rows})
		}
		return rep, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()
	for _, p := range plans {
		rows, err := updatePlan(ctx, tx, p, to)
		if err != nil {
			return rep, err
		}
		rep.Tables = append(rep.Tables, tableReport{Table: p.Name, Rows: rows})
	}
	if err := tx.Commit(); err != nil {
		return rep, err
	}
	rep.Executed = true
	return rep, nil
}

func identityTablePlan(table string, from identity, setDisplay bool) tablePlan {
	return tablePlan{
		Name:       table,
		Where:      "canonical_symbol = ? AND market_surface = ? AND instrument_kind = ?",
		Args:       []any{from.Canonical, from.MarketSurface, from.InstrumentKind},
		SetDisplay: setDisplay,
	}
}

func watchlistTablePlan(from identity, candidateIDs []int64) tablePlan {
	clauses := []string{"(canonical_symbol = ? AND market_surface = ? AND instrument_kind = ?)"}
	args := []any{from.Canonical, from.MarketSurface, from.InstrumentKind}
	if len(candidateIDs) > 0 {
		placeholders := make([]string, 0, len(candidateIDs))
		for _, id := range candidateIDs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		clauses = append(clauses, "candidate_id IN ("+strings.Join(placeholders, ",")+")")
	}
	return tablePlan{Name: "t_listing_watchlist", Where: strings.Join(clauses, " OR "), Args: args}
}

func countPlan(ctx context.Context, db *sql.DB, p tablePlan) (int64, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", p.Name, p.Where)
	var n int64
	if err := db.QueryRowContext(ctx, query, p.Args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s: %w", p.Name, err)
	}
	return n, nil
}

func updatePlan(ctx context.Context, tx *sql.Tx, p tablePlan, to identity) (int64, error) {
	sets := []string{"canonical_symbol = ?", "market_surface = ?", "instrument_kind = ?"}
	args := []any{to.Canonical, to.MarketSurface, to.InstrumentKind}
	if p.SetDisplay {
		sets = append(sets, "display_symbol = ?")
		args = append(args, to.DisplaySymbol)
	}
	args = append(args, p.Args...)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s", p.Name, strings.Join(sets, ", "), p.Where)
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("update %s: %w", p.Name, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return rows, nil
}

func candidateState(ctx context.Context, db *sql.DB, from, to identity) ([]int64, int64, int64, error) {
	ids, err := candidateIDs(ctx, db, from)
	if err != nil {
		return nil, 0, 0, err
	}
	targetIDs, err := candidateIDs(ctx, db, to)
	if err != nil {
		return nil, 0, 0, err
	}
	return ids, int64(len(ids)), int64(len(targetIDs)), nil
}

func candidateIDs(ctx context.Context, db *sql.DB, id identity) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM t_listing_candidate WHERE canonical_symbol = ? AND market_surface = ? AND instrument_kind = ?`, id.Canonical, id.MarketSurface, id.InstrumentKind)
	if err != nil {
		return nil, fmt.Errorf("query candidate ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func countOutboxPayloads(ctx context.Context, db *sql.DB, oldCanonical string) (pendingRetry int64, sent int64, err error) {
	like := "%" + oldCanonical + "%"
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t_listing_delivery_outbox WHERE status IN ('pending', 'retry') AND CAST(payload_json AS CHAR) LIKE ?`, like).Scan(&pendingRetry); err != nil {
		return 0, 0, fmt.Errorf("count pending/retry outbox: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM t_listing_delivery_outbox WHERE status = 'sent' AND CAST(payload_json AS CHAR) LIKE ?`, like).Scan(&sent); err != nil {
		return 0, 0, fmt.Errorf("count sent outbox: %w", err)
	}
	return pendingRetry, sent, nil
}

func defaultDisplaySymbol(canonical, surface string) string {
	if canonical == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(surface)) {
	case "spot", "rwa_spot":
		return canonical + "-USDT"
	default:
		return canonical + "-USDT (perp)"
	}
}

func hostnameOwnerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "listing-symbol-backfill"
	}
	return "listing-symbol-backfill:" + host
}

func printReport(from, to identity, rep report) {
	mode := "DRY-RUN"
	if rep.Executed {
		mode = "EXECUTED"
	}
	log.Printf("listing symbol backfill %s: %s/%s/%s -> %s/%s/%s display=%q",
		mode, from.Canonical, from.MarketSurface, from.InstrumentKind, to.Canonical, to.MarketSurface, to.InstrumentKind, to.DisplaySymbol)
	for _, tr := range rep.Tables {
		log.Printf("  table=%s rows=%d", tr.Table, tr.Rows)
	}
	log.Printf("  outbox pending_or_retry_containing_old_symbol=%d", rep.PendingRetryOutboxRows)
	log.Printf("  outbox sent_containing_old_symbol=%d (historical payloads are intentionally untouched)", rep.SentOutboxRows)
}
