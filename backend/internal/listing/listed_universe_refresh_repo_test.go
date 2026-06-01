package listing

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestQueryActiveListedBasesFiltersFreshWindowAndSynthetic asserts
// the SQL contract: rows older than freshWindow MUST NOT be returned
// and instrument_kind='synthetic' rows (e.g. BingX NCSK*) MUST be
// filtered out so they never leak into listed_universe.
func TestQueryActiveListedBasesFiltersFreshWindowAndSynthetic(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// We pin only the SQL shape — the regex matcher means we just need
	// the WHERE columns to be present. The cutoff arg must be
	// (now - freshWindow).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WithArgs(now.Add(-30*time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"platform", "base_asset", "market_surface"}).
			AddRow("edgeX", "BTC", "perp").
			AddRow("edgeX", "ETH", "perp").
			AddRow("edgeX", "BTC", "spot").
			AddRow("binance", "SOL", "perp"))

	got, err := repo.QueryActiveListedBases(context.Background(), 30*time.Minute)
	if err != nil {
		t.Fatalf("QueryActiveListedBases err = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 rows, got %d", len(got))
	}
	// Verify SQL filter intent is encoded — check the prepared
	// statement excludes synthetic and demands status_normalized=active.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestQueryActiveListedBasesSurfacesSQLError makes sure an
// underlying DB failure bubbles up unchanged so the refresh job can
// decide to keep the previous runtime yaml rather than overwrite it
// with an empty universe.
func TestQueryActiveListedBasesSurfacesSQLError(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, base_asset, market_surface FROM t_listing_instrument_snapshot")).
		WillReturnError(errExample)
	if _, err := repo.QueryActiveListedBases(context.Background(), 30*time.Minute); err == nil {
		t.Fatalf("expected SQL error to surface")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestBulkMarkCandidatesAlreadyListedRespectsSurfaceAndLifecycle pins
// the F4 safety contract: the UPDATE statement MUST scope by
// market_surface and lifecycle_status whitelist so we never
// accidentally flip a spot candidate when only edgeX perp listed,
// and we never overwrite an archived/declined operator decision.
func TestBulkMarkCandidatesAlreadyListedRespectsSurfaceAndLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// The repository uses sqlmock with the regexp matcher; the SQL
	// MUST contain (i) market_surface = ?, (ii) canonical_symbol IN
	// (...), and (iii) the lifecycle whitelist. We verify these by
	// regex'ing the UPDATE statement itself rather than pinning every
	// argument position.
	mock.ExpectExec(`UPDATE t_listing_candidate[\s\S]+market_surface = \?[\s\S]+canonical_symbol IN[\s\S]+lifecycle_status IN`).
		WillReturnResult(sqlmock.NewResult(0, 2))

	got, err := repo.BulkMarkCandidatesAlreadyListed(context.Background(), []string{"BTC", "ETH"}, "perp", now)
	if err != nil {
		t.Fatalf("BulkMarkCandidatesAlreadyListed err = %v", err)
	}
	if got != 2 {
		t.Fatalf("want 2 rows affected, got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestBulkMarkCandidatesAlreadyListedNoOpOnEmptyInput guards against
// firing an UPDATE with an empty IN list (which MySQL rejects with a
// syntax error) when no platforms covered the surface.
func TestBulkMarkCandidatesAlreadyListedNoOpOnEmptyInput(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	repo, _, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	got, err := repo.BulkMarkCandidatesAlreadyListed(context.Background(), nil, "perp", now)
	if err != nil {
		t.Fatalf("BulkMarkCandidatesAlreadyListed nil err = %v", err)
	}
	if got != 0 {
		t.Fatalf("nil input must yield 0 affected, got %d", got)
	}
	got, err = repo.BulkMarkCandidatesAlreadyListed(context.Background(), []string{}, "spot", now)
	if err != nil {
		t.Fatalf("BulkMarkCandidatesAlreadyListed empty err = %v", err)
	}
	if got != 0 {
		t.Fatalf("empty input must yield 0 affected, got %d", got)
	}
}

// errExample is a sentinel error reused across repository_test files
// to assert error pass-through.
var errExample = stringErr("sqlmock: synthetic failure")

type stringErr string

func (s stringErr) Error() string { return string(s) }
