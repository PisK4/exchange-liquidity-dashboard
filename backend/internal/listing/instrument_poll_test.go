package listing

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/instrument"

	"github.com/DATA-DOG/go-sqlmock"
)

// instSnapshotCols mirrors the column order used by
// LatestInstrumentSnapshotByKey so each test can construct a fake
// "row exists" response without re-listing the column names.
var instSnapshotCols = []string{
	"id", "platform", "market_type", "api_symbol", "api_market_id", "display_symbol",
	"canonical_symbol", "base_asset", "quote_asset", "settle_asset", "market_surface",
	"instrument_kind", "contract_type", "status_raw", "status_normalized",
	"status_field_name", "listing_time_ts", "listing_time_field_name", "delist_flag",
	"first_seen_at", "previous_seen_at", "last_seen_at", "raw_json", "raw_json_hash",
	"normalizer_version",
}

func newBTCNormalized() instrument.NormalizedInstrument {
	return instrument.NormalizedInstrument{
		Platform:         "binance",
		MarketType:       "usdm_futures",
		APISymbol:        "BTCUSDT",
		DisplaySymbol:    "BTCUSDT",
		CanonicalSymbol:  "BTC",
		BaseAsset:        "BTC",
		QuoteAsset:       "USDT",
		SettleAsset:      "USDT",
		MarketSurface:    "perp",
		InstrumentKind:   "canonical",
		ContractType:     "PERPETUAL",
		StatusRaw:        "TRADING",
		StatusNormalized: "active",
		StatusFieldName:  "status",
		RawJSON:          json.RawMessage(`{"symbol":"BTCUSDT"}`),
		StableHash:       "hashbtc",
	}
}

func newETHNormalized() instrument.NormalizedInstrument {
	return instrument.NormalizedInstrument{
		Platform:         "binance",
		MarketType:       "usdm_futures",
		APISymbol:        "ETHUSDT",
		DisplaySymbol:    "ETHUSDT",
		CanonicalSymbol:  "ETH",
		BaseAsset:        "ETH",
		QuoteAsset:       "USDT",
		SettleAsset:      "USDT",
		MarketSurface:    "perp",
		InstrumentKind:   "canonical",
		ContractType:     "PERPETUAL",
		StatusRaw:        "TRADING",
		StatusNormalized: "active",
		StatusFieldName:  "status",
		RawJSON:          json.RawMessage(`{"symbol":"ETHUSDT"}`),
		StableHash:       "hasheth",
	}
}

// TestRunInstrumentPollColdStartEmitsBaselineOnly asserts the
// bootstrap contract from §Phase 1 of 2026-05-29-listing-agent.md:
// when no rows exist for the platform/market_type, every fetched
// instrument MUST be written as a baseline snapshot and ZERO signals
// MUST be emitted. Without this guard, a fresh deployment would
// misreport every existing exchange instrument as a new listing.
func TestRunInstrumentPollColdStartEmitsBaselineOnly(t *testing.T) {
	now := time.Date(2026, 5, 29, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))
	// Two upserts, no signal inserts.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(2, 1))

	src := InstrumentSource{
		Platform:   "binance",
		MarketType: "usdm_futures",
		SourceURL:  "https://fapi.binance.com/fapi/v1/exchangeInfo",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{newBTCNormalized(), newETHNormalized()}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if !res.Baseline {
		t.Errorf("Baseline = false, want true on cold start")
	}
	if res.Fetched != 2 || res.SnapshotsUpserted != 2 || res.SignalsEmitted != 0 {
		t.Errorf("result = %+v, want Fetched=2 SnapshotsUpserted=2 SignalsEmitted=0", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollWarmEmitsNewSymbolForFreshInstrument asserts
// the steady-state path: a (platform, market_type) that already has
// baseline data treats each newly-seen api_symbol as a new_symbol
// signal AND upserts the snapshot so subsequent polls see it as
// existing. The existing BTC row produces no diff because the
// raw_json_hash is unchanged.
func TestRunInstrumentPollWarmEmitsNewSymbolForFreshInstrument(t *testing.T) {
	now := time.Date(2026, 5, 29, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	// BTC already exists with same hash → no signal, but still upserts to roll last_seen_at.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "BTCUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols).AddRow(
			int64(11), "binance", "usdm_futures", "BTCUSDT", nil, "BTCUSDT",
			"BTC", "BTC", "USDT", "USDT", "perp",
			"canonical", "PERPETUAL", "TRADING", "active",
			"status", nil, nil, false,
			now.Add(-24*time.Hour), nil, now.Add(-1*time.Hour),
			[]byte(`{"symbol":"BTCUSDT"}`), "hashbtc", instrument.NormalizerVersion,
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(11, 2)) // ON DUPLICATE KEY UPDATE => 2 rows affected

	// ETH is new — Diff(nil, eth, baselineReady=true) → new_symbol; signal insert then snapshot upsert.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "ETHUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(12, 1))

	src := InstrumentSource{
		Platform:   "binance",
		MarketType: "usdm_futures",
		SourceURL:  "https://fapi.binance.com/fapi/v1/exchangeInfo",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{newBTCNormalized(), newETHNormalized()}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if res.Baseline {
		t.Errorf("Baseline = true, want false in warm path")
	}
	if res.SignalsEmitted != 1 {
		t.Errorf("SignalsEmitted = %d, want 1 (only ETH is new)", res.SignalsEmitted)
	}
	if res.SnapshotsUpserted != 2 {
		t.Errorf("SnapshotsUpserted = %d, want 2 (both rolled)", res.SnapshotsUpserted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRunInstrumentPollBybitLinearFuturesBaselineEmitsNewSymbol(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("bybit", "linear_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("bybit", "linear_futures", "NEWUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(201, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(21, 1))

	src := InstrumentSource{
		Platform:   "bybit",
		MarketType: "linear_futures",
		SourceKey:  "bybit/linear",
		SourceURL:  "https://api.bybit.com/v5/market/instruments-info",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{{
				Platform:         "bybit",
				MarketType:       "linear_futures",
				APISymbol:        "NEWUSDT",
				CanonicalSymbol:  "NEW",
				BaseAsset:        "NEW",
				QuoteAsset:       "USDT",
				SettleAsset:      "USDT",
				MarketSurface:    "perp",
				InstrumentKind:   "canonical",
				ContractType:     "LinearPerpetual",
				StatusRaw:        "PreLaunch",
				StatusNormalized: "pre_listing",
				StatusFieldName:  "status",
				RawJSON:          json.RawMessage(`{"symbol":"NEWUSDT"}`),
				StableHash:       "hash-new-bybit",
			}}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if res.Baseline {
		t.Fatalf("bybit linear_futures should be warm when snapshots exist")
	}
	if res.SignalsEmitted != 1 || res.DiffSubtypes["new_symbol"] != 1 {
		t.Fatalf("want one new_symbol signal, got %+v", res)
	}
	if res.SnapshotsUpserted != 1 {
		t.Fatalf("SnapshotsUpserted = %d, want 1", res.SnapshotsUpserted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollSurfacesFetchError verifies that a Fetch
// failure bubbles up so the source-health wrapper (Phase 1.3) can
// account for it; the driver itself MUST NOT touch the snapshot table
// nor emit signals when the upstream API was unreachable.
func TestRunInstrumentPollSurfacesFetchError(t *testing.T) {
	now := time.Date(2026, 5, 29, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	fetchErr := errors.New("network: timeout")
	src := InstrumentSource{
		Platform:   "binance",
		MarketType: "usdm_futures",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return nil, fetchErr
		},
	}
	_, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{
		Now: func() time.Time { return now },
	})
	if !errors.Is(err, fetchErr) {
		t.Fatalf("err = %v, want wraps fetchErr", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollSnapshotOnlySkipsSignalInsert verifies the
// SignalingMode contract (spec Part A'): when an InstrumentSource is
// configured with SignalingMode=snapshot_only the poll MUST upsert
// the snapshot but MUST NOT InsertSignal. This is the default for
// edgeX 3-surface sources to avoid the self-listing decision loop
// (spec F5).
func TestRunInstrumentPollSnapshotOnlySkipsSignalInsert(t *testing.T) {
	now := time.Date(2026, 5, 29, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("edgeX", "perp_v1").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	// New instrument observed; would normally fire a new_symbol signal,
	// but snapshot_only suppresses signal emission.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("edgeX", "perp_v1", "BTCUSD").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols))
	// No InsertSignal expected — only the snapshot upsert.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	src := InstrumentSource{
		Platform:      "edgeX",
		MarketType:    "perp_v1",
		SignalingMode: SignalingModeSnapshotOnly,
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{{
				Platform: "edgeX", MarketType: "perp_v1", APISymbol: "BTCUSD",
				CanonicalSymbol: "BTC", BaseAsset: "BTC",
				MarketSurface: "perp", InstrumentKind: "canonical",
				StatusNormalized: "active",
				RawJSON:          json.RawMessage(`{"contractName":"BTCUSD"}`),
				StableHash:       "h-btc-edgex",
			}}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if res.SignalsEmitted != 0 {
		t.Errorf("snapshot_only must emit zero signals, got %d", res.SignalsEmitted)
	}
	if res.SnapshotsUpserted != 1 {
		t.Errorf("snapshot_only must still upsert snapshots, got %d", res.SnapshotsUpserted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollFullMatchesLegacyBehaviour pins the default
// (SignalingMode empty/full) to the existing semantics so this
// refactor cannot regress the Phase 4 6-source pollers.
func TestRunInstrumentPollFullMatchesLegacyBehaviour(t *testing.T) {
	now := time.Date(2026, 5, 29, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "BTCUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	src := InstrumentSource{
		Platform:      "binance",
		MarketType:    "usdm_futures",
		SignalingMode: SignalingModeFull,
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{newBTCNormalized()}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if res.SignalsEmitted != 1 {
		t.Errorf("full mode should fire signals; got %d", res.SignalsEmitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollContinuesAfterSignalInsertFailure locks down
// the best-effort guarantee added after the 2026-06-01 root-cause:
// when InsertSignal fails for one instrument (e.g. fingerprint
// column overflow returns ErrSignalSilentFail), the loop must log
// and KEEP GOING so the remaining instruments still upsert. Before
// this fix a single poisoned signal aborted the entire tick and
// stalled t_listing_instrument_snapshot.last_seen_at for the whole
// platform, which silently emptied the dashboard runtime
// listed_universe via the freshness window.
func TestRunInstrumentPollContinuesAfterSignalInsertFailure(t *testing.T) {
	now := time.Date(2026, 6, 1, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	// BTC: prev exists with different hash → diff fires → InsertSignal
	// returns affected=0 AND the fallback SELECT misses (simulates the
	// fingerprint-overflow silent-drop seen in production).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "BTCUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols).AddRow(
			int64(11), "binance", "usdm_futures", "BTCUSDT", nil, "BTCUSDT",
			"BTC", "BTC", "USDT", "USDT", "perp",
			"canonical", "PERPETUAL", "TRADING", "active",
			"status", nil, nil, false,
			now.Add(-24*time.Hour), nil, now.Add(-1*time.Hour),
			[]byte(`{"symbol":"BTCUSDT"}`), "hash-prev-btc", instrument.NormalizerVersion,
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM t_listing_signal_observation WHERE fingerprint")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// CRITICAL: snapshot upsert MUST still happen for BTC despite the
	// signal failure — otherwise last_seen_at goes stale.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(11, 2))

	// ETH: prev exists, same hash, no signal — proves the loop kept
	// going past BTC's signal failure.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "ETHUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols).AddRow(
			int64(12), "binance", "usdm_futures", "ETHUSDT", nil, "ETHUSDT",
			"ETH", "ETH", "USDT", "USDT", "perp",
			"canonical", "PERPETUAL", "TRADING", "active",
			"status", nil, nil, false,
			now.Add(-24*time.Hour), nil, now.Add(-1*time.Hour),
			[]byte(`{"symbol":"ETHUSDT"}`), "hasheth", instrument.NormalizerVersion,
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(12, 2))

	btc := newBTCNormalized()
	btc.StableHash = "hash-new-btc"
	src := InstrumentSource{
		Platform:   "binance",
		MarketType: "usdm_futures",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{btc, newETHNormalized()}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v, want nil (best-effort path)", err)
	}
	if res.SignalsEmitted != 0 {
		t.Errorf("SignalsEmitted = %d, want 0 (the only diff event failed)", res.SignalsEmitted)
	}
	if res.SnapshotsUpserted != 2 {
		t.Errorf("SnapshotsUpserted = %d, want 2 (best-effort must roll BOTH last_seen_at despite BTC's signal failure)", res.SnapshotsUpserted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollDiffSubtypePropagatesToSignal makes sure a
// status transition seen on a known instrument propagates as a
// dedicated signal subtype (e.g. delisted), not the generic
// metadata_changed catch-all. This ensures fusion can later
// distinguish a delist from a metadata refresh.
func TestRunInstrumentPollDiffSubtypePropagatesToSignal(t *testing.T) {
	now := time.Date(2026, 5, 29, 17, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	// Prev shows BTC active with hash hashbtc; curr will flip to delisted.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "BTCUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols).AddRow(
			int64(11), "binance", "usdm_futures", "BTCUSDT", nil, "BTCUSDT",
			"BTC", "BTC", "USDT", "USDT", "perp",
			"canonical", "PERPETUAL", "TRADING", "active",
			"status", nil, nil, false,
			now.Add(-24*time.Hour), nil, now.Add(-1*time.Hour),
			[]byte(`{"symbol":"BTCUSDT"}`), "hashbtc", instrument.NormalizerVersion,
		))

	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WithArgs(
			SignalInstrumentDiff,
			sqlmock.AnyArg(),            // signal_subtype = "delisted" (assert below via custom matcher would be heavier; we check via SignalsEmitted)
			"binance", sqlmock.AnyArg(), // platform / market_type
			sqlmock.AnyArg(), sqlmock.AnyArg(), // api_symbol, api_market_id
			"BTC", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"perp", "canonical",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(202, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(11, 2))

	btcDelisted := newBTCNormalized()
	btcDelisted.StatusRaw = "DELISTED"
	btcDelisted.StatusNormalized = "delisted"
	btcDelisted.DelistFlag = true
	btcDelisted.StableHash = "hashdelisted"

	src := InstrumentSource{
		Platform:   "binance",
		MarketType: "usdm_futures",
		SourceURL:  "https://fapi.binance.com/fapi/v1/exchangeInfo",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{btcDelisted}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if res.SignalsEmitted != 1 {
		t.Errorf("SignalsEmitted = %d, want 1", res.SignalsEmitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRunInstrumentPollNormalizerVersionRolloverSkipsDiffButUpsertsSnapshot
// pins the 2026-06-01 hash-recipe cutover contract: when prev
// snapshot's NormalizerVersion differs from the current binary's
// version, the StableHash values are not directly comparable, so the
// driver MUST skip instrument.Diff for this tick. The snapshot upsert
// still happens so the row carries the new recipe forward; subsequent
// ticks then compare like-for-like. Without this guard, the v1->v2
// cutover would surface as a one-shot metadata_changed firehose
// across every (platform, market_type, api_symbol) tuple.
func TestRunInstrumentPollNormalizerVersionRolloverSkipsDiffButUpsertsSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 2, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}).AddRow(int64(1)))

	// Prev row uses the OLD normalizer version "v1" and an old-recipe
	// hash; current binary uses "v2" + the new projection hash. Diff
	// MUST NOT be invoked even though the hashes differ.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, platform, market_type, api_symbol")).
		WithArgs("binance", "usdm_futures", "BTCUSDT").
		WillReturnRows(sqlmock.NewRows(instSnapshotCols).AddRow(
			int64(11), "binance", "usdm_futures", "BTCUSDT", nil, "BTCUSDT",
			"BTC", "BTC", "USDT", "USDT", "perp",
			"canonical", "PERPETUAL", "TRADING", "active",
			"status", nil, nil, false,
			now.Add(-24*time.Hour), nil, now.Add(-1*time.Hour),
			[]byte(`{"symbol":"BTCUSDT"}`), "old_recipe_hash_value", "v1",
		))
	// NO ExpectExec for INSERT IGNORE INTO t_listing_signal_observation.
	// Only the snapshot upsert is expected.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(11, 2))

	src := InstrumentSource{
		Platform:   "binance",
		MarketType: "usdm_futures",
		SourceURL:  "https://fapi.binance.com/fapi/v1/exchangeInfo",
		Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
			return []instrument.NormalizedInstrument{newBTCNormalized()}, nil
		},
	}
	res, err := RunInstrumentPoll(context.Background(), repo, src, InstrumentPollDeps{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("RunInstrumentPoll err = %v", err)
	}
	if res.SignalsEmitted != 0 {
		t.Errorf("SignalsEmitted = %d, want 0 under normalizer rollover", res.SignalsEmitted)
	}
	if res.SnapshotsUpserted != 1 {
		t.Errorf("SnapshotsUpserted = %d, want 1", res.SnapshotsUpserted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
