package listing

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFuseSignalsFailClosedWhenUniverseUnloaded(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// No SELECT on unfused signals should occur because universe load
	// fails closed before any read. Use empty expectations.

	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) {
			return &config.ListedUniverse{}, nil // not loaded
		},
		Now: func() time.Time { return now },
	})
	if !errors.Is(err, ErrFusionFailClosed) {
		t.Fatalf("err = %v, want ErrFusionFailClosed", err)
	}
	if result.FailClosed != "universe_not_loaded" {
		t.Fatalf("result.FailClosed = %q", result.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsPromotesAnnouncementAndAPIToConfirmedCandidate(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(1), SignalAnnouncementListing, AnnouncementPerpListing, "binance", nil, nil, nil,
		"ABC", "ABC-USDT (perp)", "ABC", "USDT", "USDT",
		"perp", "canonical", nil, nil, nil,
		now, nil, nil, nil,
		nil, nil, "announcement_listing|binance|a1|ABC|perp|canonical", []byte(`{}`), nil, nil,
	).AddRow(
		int64(2), SignalInstrumentDiff, DiffNewSymbol, "binance", "usdm_futures", "ABCUSDT", nil,
		"ABC", "ABC-USDT (perp)", "ABC", "USDT", "USDT",
		"perp", "canonical", "TRADING", "active", nil,
		now, nil, nil, nil,
		nil, nil, "instrument_diff|binance|usdm_futures|ABCUSDT|new_symbol|ABC|perp|canonical", []byte(`{}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).
		WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(11), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(11), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.FailClosed != "" {
		t.Fatalf("unexpected fail_closed: %q", result.FailClosed)
	}
	if result.Candidates != 1 || result.Signals != 2 {
		t.Fatalf("counts = %+v, want 1 candidate / 2 signals", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
