package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneScriptDefaultsToDryRun(t *testing.T) {
	plans, err := planPrune(30, time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("planPrune error = %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("expected non-empty plan")
	}
	flags := cliFlags{days: 30, confirm: false, mysqlDSN: "should-not-be-opened"}
	results, err := runPrune(context.Background(), flags, plans)
	if err != nil {
		t.Fatalf("runPrune error = %v", err)
	}
	if len(results) != len(plans) {
		t.Fatalf("results len = %d, want %d", len(results), len(plans))
	}
	for _, r := range results {
		if !r.DryRun {
			t.Errorf("result for %s must be DryRun=true when confirm=false", r.Table)
		}
		if r.Pruned != 0 {
			t.Errorf("result for %s must report 0 rows in dry-run, got %d", r.Table, r.Pruned)
		}
	}
}

func TestPruneScriptRefusesShortRetention(t *testing.T) {
	for _, days := range []int{0, 1, 6} {
		_, err := planPrune(days, time.Now())
		if err == nil {
			t.Errorf("planPrune(days=%d) must error (< minRetentionDays=%d)", days, minRetentionDays)
		}
	}
	if _, err := planPrune(7, time.Now()); err != nil {
		t.Errorf("planPrune(days=7) should succeed at the floor, got %v", err)
	}
}

func TestPruneScriptUsesSnapshotTsForSnapshotTablesAndDayForDailyAggregate(t *testing.T) {
	plans, err := planPrune(30, time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("planPrune error = %v", err)
	}
	wantTimeColumns := map[string]string{
		"t_orderbook_snapshot":                 "snapshot_ts",
		"t_book_quality_snapshot":              "snapshot_ts",
		"t_symbol_volume_snapshot":             "snapshot_ts",
		"t_collection_status":                  "snapshot_ts",
		"t_coingecko_platform_volume_snapshot": "snapshot_ts",
		"t_daily_volume_aggregate":             "day",
	}
	got := map[string]string{}
	for _, p := range plans {
		got[p.Table] = p.TimeColumn
		if !isAllowedTable(p.Table) {
			t.Errorf("plan emits %q which is not on the allow-list", p.Table)
		}
		if !isAllowedTimeColumn(p.TimeColumn) {
			t.Errorf("plan emits time column %q which is not on the allow-list", p.TimeColumn)
		}
	}
	for table, want := range wantTimeColumns {
		if got[table] != want {
			t.Errorf("table %s: time_column = %q, want %q", table, got[table], want)
		}
	}
	// Excluded tables MUST NOT appear in the plan.
	for _, excluded := range []string{
		"t_top30_snapshot",
		"t_collection_run",
		"t_runtime_config",
		"t_symbol_mapping",
		"t_exchange_instrument_catalog",
		"t_platform_volume_snapshot",
	} {
		if _, ok := got[excluded]; ok {
			t.Errorf("excluded table %q must not be in plan", excluded)
		}
	}
}

func TestWriteHistoryWritesParseableJSON(t *testing.T) {
	dir := t.TempDir()
	flags := cliFlags{
		days:      14,
		confirm:   false,
		batchSize: 5000,
		dataDir:   dir,
	}
	results := []prunePlanResult{
		{Table: "t_orderbook_snapshot", TimeColumn: "snapshot_ts", Cutoff: time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC), DryRun: true},
	}
	if err := writeHistory(flags, results); err != nil {
		t.Fatalf("writeHistory error = %v", err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "prune-history"))
	if err != nil {
		t.Fatalf("read prune-history dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 history file, got %d", len(files))
	}
	body, err := os.ReadFile(filepath.Join(dir, "prune-history", files[0].Name()))
	if err != nil {
		t.Fatalf("read history file: %v", err)
	}
	var rec pruneHistory
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if rec.Days != 14 || rec.Confirmed != false {
		t.Errorf("history fields wrong: %+v", rec)
	}
	if len(rec.Results) != 1 || rec.Results[0].Table != "t_orderbook_snapshot" {
		t.Errorf("history results wrong: %+v", rec.Results)
	}
}
