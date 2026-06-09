package listing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"

	"github.com/DATA-DOG/go-sqlmock"
)

const refreshSeedYAML = `schema_version: 1
generated_at: "2026-05-01T00:00:00Z"
generated_by: build-catalog
platforms:
  edgeX:
    base_assets: [BTC, ETH, SOL, OLD]
  binance:
    base_assets: [BTC, ETH, SOL, DOGE]
  bingx:
    base_assets: [BTC, ETH]
`

// writeSeed creates a temp seed yaml and returns its path.
func writeSeed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.yaml")
	if err := os.WriteFile(path, []byte(refreshSeedYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRefreshListedUniverseHappyPathWritesAtomicallyAndReconciles
// asserts the joint behaviour: (1) DB-derived bases land in the
// runtime yaml, (2) BulkMarkCandidatesAlreadyListed fires once per
// surface, (3) the file is written atomically (no .tmp leftover).
func TestRefreshListedUniverseHappyPathWritesAtomicallyAndReconciles(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp").
			AddRow("edgeX", "ETH", "perp").
			AddRow("edgeX", "SOL", "perp").
			AddRow("edgeX", "BTC", "spot").
			AddRow("binance", "BTC", "perp").
			AddRow("binance", "ETH", "perp").
			AddRow("binance", "SOL", "perp").
			AddRow("binance", "DOGE", "perp").
			AddRow("bingx", "BTC", "spot").
			AddRow("bingx", "ETH", "spot"))
	// DB-derived platforms clear any previous listed_universe fallback state.
	expectListedUniversePreviousStateMissing(mock, "edgeX")
	expectListedUniverseOK(mock, "edgeX", now)
	expectListedUniversePreviousStateMissing(mock, "binance")
	expectListedUniverseOK(mock, "binance", now)
	expectListedUniversePreviousStateMissing(mock, "bingx")
	expectListedUniverseOK(mock, "bingx", now)
	// Two BulkMark calls: edgeX perp (3 bases) + edgeX spot (1 base).
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	metrics := NewInMemoryMetrics()

	res, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX", "binance", "bingx"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          metrics,
	})
	if err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	// Atomic write: temp file MUST be cleaned up.
	if _, err := os.Stat(runtimePath); err != nil {
		t.Fatalf("runtime file missing: %v", err)
	}
	if _, err := os.Stat(runtimePath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf(".tmp leftover after refresh")
	}
	if got := res.PlatformsFromDB; len(got) != 3 {
		t.Fatalf("PlatformsFromDB = %v, want 3", got)
	}
	if res.PlatformsFromSeed != nil && len(res.PlatformsFromSeed) > 0 {
		t.Fatalf("shrink fallback unexpectedly fired: %v", res.PlatformsFromSeed)
	}
	if got := res.PerpReconciled + res.SpotReconciled; got != 3 {
		t.Fatalf("reconciled rows = %d, want 3", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRefreshListedUniverseShrinkFloorFallsBackToSeed asserts the F7
// safety net: if a platform's DB row count drops below the shrink
// floor relative to seed, we (a) keep the seed list for that
// platform, (b) record a source-health error, (c) bump the
// listed_universe_shrink_fallback_total counter.
func TestRefreshListedUniverseShrinkFloorFallsBackToSeed(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// edgeX should have 4 bases (per seed) but DB returns only 1 — under 50%.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp").
			AddRow("binance", "BTC", "perp").
			AddRow("binance", "ETH", "perp").
			AddRow("binance", "SOL", "perp").
			AddRow("binance", "DOGE", "perp"))
	// Source-health writes: edgeX shrink fallback, binance DB-derived OK.
	expectListedUniversePreviousStateMissing(mock, "edgeX")
	expectListedUniverseShrink(mock, "edgeX", "listed_universe shrink_floor triggered: db=1 seed=4 floor=0.50", now)
	expectListedUniversePreviousStateMissing(mock, "binance")
	expectListedUniverseOK(mock, "binance", now)
	// edgeX perp BulkMark with the seed-derived bases — 1 base from DB.
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	metrics := NewInMemoryMetrics()

	res, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX", "binance"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          metrics,
	})
	if err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	var seenSeed bool
	for _, p := range res.PlatformsFromSeed {
		if p == "edgeX" {
			seenSeed = true
		}
	}
	if !seenSeed {
		t.Fatalf("edgeX must fall back to seed on shrink, got PlatformsFromSeed=%v", res.PlatformsFromSeed)
	}
	if got := metrics.Value("listed_universe_shrink_fallback_total", "edgeX"); got != 1 {
		t.Fatalf("shrink_fallback counter for edgeX = %v, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefreshListedUniverseFallsBackToSeedWhenDBEmpty(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}))
	// Source-health writes for the seed fallback on each covered platform.
	expectListedUniversePreviousStateMissing(mock, "edgeX")
	expectListedUniverseShrink(mock, "edgeX", "listed_universe shrink_floor triggered: db=0 seed=4 floor=0.50", now)
	expectListedUniversePreviousStateMissing(mock, "binance")
	expectListedUniverseShrink(mock, "binance", "listed_universe shrink_floor triggered: db=0 seed=4 floor=0.50", now)

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	metrics := NewInMemoryMetrics()

	res, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX", "binance"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          metrics,
	})
	if err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	if len(res.PlatformsFromSeed) != 2 {
		t.Fatalf("PlatformsFromSeed = %v, want edgeX and binance", res.PlatformsFromSeed)
	}
	loaded, err := config.LoadListedUniverse(runtimePath)
	if err != nil {
		t.Fatalf("load runtime universe: %v", err)
	}
	if !loaded.IsListed("edgeX", "BTC") {
		t.Fatalf("runtime universe must preserve seed edgeX BTC when DB is empty")
	}
	if got := metrics.Value("listed_universe_shrink_fallback_total", "edgeX"); got != 1 {
		t.Fatalf("shrink_fallback counter for edgeX = %v, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRefreshListedUniverseSurfaceSplitDoesNotCloseSpotWhenOnlyPerpListed
// pins the F4 split: an edgeX-only-perp listing of BTC MUST NOT
// auto-close the spot BTC candidate.
func TestRefreshListedUniverseSurfaceSplitDoesNotCloseSpotWhenOnlyPerpListed(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// edgeX has BTC only on perp, NOT on spot.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp").
			AddRow("edgeX", "ETH", "perp").
			AddRow("edgeX", "SOL", "perp").
			AddRow("edgeX", "OLD", "perp"))
	expectListedUniversePreviousStateMissing(mock, "edgeX")
	expectListedUniverseOK(mock, "edgeX", now)
	// Exactly ONE BulkMark call expected (perp side); spot side
	// is skipped because canonicalBases is empty.
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 2))

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")

	if _, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          NewInMemoryMetrics(),
	}); err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRefreshListedUniverseHonoursMissingSeed ensures a missing seed
// path is tolerated (an early deploy may not have one) — the DB
// path wins and shrink-floor only triggers when the seed actually
// has a baseline.
func TestRefreshListedUniverseHonoursMissingSeed(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp"))
	expectListedUniversePreviousStateMissing(mock, "edgeX")
	expectListedUniverseOK(mock, "edgeX", now)
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	if _, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         filepath.Join(t.TempDir(), "missing.yaml"),
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          NewInMemoryMetrics(),
	}); err != nil {
		t.Fatalf("missing seed should not error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefreshListedUniverseDBFirstBootstrapAcceptsDBBelowSeedFloor(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp"))
	expectListedUniversePreviousStateMissing(mock, "edgeX")
	expectListedUniverseOK(mock, "edgeX", now)
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	res, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX"},
		ShrinkFloor:      0.5,
		PlatformOverrides: map[string]config.ListedUniverseRefreshPlatformPolicy{
			"edgeX": {BootstrapBaseline: "db_first", BootstrapMinCount: 1},
		},
		Now:     now,
		Metrics: NewInMemoryMetrics(),
	})
	if err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	if len(res.PlatformsFromSeed) != 0 {
		t.Fatalf("db_first bootstrap should accept DB, got seed fallback %v", res.PlatformsFromSeed)
	}
	loaded, err := config.LoadListedUniverse(runtimePath)
	if err != nil {
		t.Fatalf("load runtime universe: %v", err)
	}
	if got := loaded.BaseAssets("edgeX"); len(got) != 1 || got[0] != "BTC" {
		t.Fatalf("runtime edgeX bases = %v, want [BTC]", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefreshListedUniversePreviousSuccessBaselineAllowsRuntimeBelowSeed(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp"))
	expectListedUniversePreviousStateOK(mock, "edgeX", 2, now.Add(-15*time.Minute))
	expectListedUniverseOK(mock, "edgeX", now)
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	res, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          NewInMemoryMetrics(),
	})
	if err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	if len(res.PlatformsFromSeed) != 0 {
		t.Fatalf("previous-success baseline should accept DB, got seed fallback %v", res.PlatformsFromSeed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRefreshListedUniversePreviousSuccessBaselineFailsClosedOnRuntimeShrink(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp"))
	expectListedUniversePreviousStateOK(mock, "edgeX", 4, now.Add(-15*time.Minute))
	expectListedUniverseShrink(mock, "edgeX", "listed_universe runtime shrink triggered: db=1 previous_success=4 floor=0.50", now)
	mock.ExpectExec(`UPDATE t_listing_candidate`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	seed := writeSeed(t)
	runtimePath := filepath.Join(t.TempDir(), "listed_universe.runtime.yaml")
	res, err := RefreshListedUniverseFromSnapshots(context.Background(), repo, ListedUniverseRefreshArgs{
		SeedPath:         seed,
		RuntimePath:      runtimePath,
		FreshWindow:      30 * time.Minute,
		CoveredPlatforms: []string{"edgeX"},
		ShrinkFloor:      0.5,
		Now:              now,
		Metrics:          NewInMemoryMetrics(),
	})
	if err != nil {
		t.Fatalf("RefreshListedUniverseFromSnapshots err = %v", err)
	}
	if len(res.PlatformsFromSeed) != 1 || res.PlatformsFromSeed[0] != "edgeX" {
		t.Fatalf("runtime shrink should fail closed to seed, got %v", res.PlatformsFromSeed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func expectListedUniverseOK(mock sqlmock.Sqlmock, platform string, now time.Time) {
	mock.ExpectExec(`INSERT INTO t_listing_source_state`).
		WithArgs(
			"listing/listed_universe/"+platform,
			"listed_universe_refresh",
			platform,
			SourceStatusOK,
			sqlmock.AnyArg(),
			nil,
			0,
			0,
			nil,
			nil,
			sqlmock.AnyArg(),
			now,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectListedUniverseShrink(mock sqlmock.Sqlmock, platform string, msg string, now time.Time) {
	mock.ExpectExec(`INSERT INTO t_listing_source_state`).
		WithArgs(
			"listing/listed_universe/"+platform,
			"listed_universe_refresh",
			platform,
			SourceStatusSchemaDrift,
			nil,
			sqlmock.AnyArg(),
			0,
			0,
			nil,
			msg,
			sqlmock.AnyArg(),
			now,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

var listedUniverseSourceStateCols = []string{
	"source_key", "source_type", "platform", "status",
	"last_success_at", "last_error_at", "consecutive_error_count", "schema_drift_count",
	"disabled_until", "last_error", "source_context_json", "updated_at",
}

func expectListedUniversePreviousStateMissing(mock sqlmock.Sqlmock, platform string) {
	mock.ExpectQuery(`SELECT source_key, source_type, platform, status`).
		WithArgs("listing/listed_universe/" + platform).
		WillReturnRows(sqlmock.NewRows(listedUniverseSourceStateCols))
}

func expectListedUniversePreviousStateOK(mock sqlmock.Sqlmock, platform string, dbCount int, lastSuccess time.Time) {
	ctx := fmt.Sprintf(`{"schema_version":1,"db_fresh_active_count":%d,"previous_success_db_fresh_active_count":%d}`, dbCount, dbCount)
	mock.ExpectQuery(`SELECT source_key, source_type, platform, status`).
		WithArgs("listing/listed_universe/" + platform).
		WillReturnRows(sqlmock.NewRows(listedUniverseSourceStateCols).
			AddRow(
				"listing/listed_universe/"+platform,
				"listed_universe_refresh",
				platform,
				SourceStatusOK,
				lastSuccess,
				nil,
				0,
				0,
				nil,
				nil,
				[]byte(ctx),
				lastSuccess,
			))
}
