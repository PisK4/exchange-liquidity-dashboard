package listing

import (
	"context"
	"errors"
	"fmt"
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

// TestFuseSignalsSkipsTop30AggregatorSignals locks the §Phase 1.2
// fusion contract: only the candidate-bearing signal types
// (instrument_diff + announcement_listing) should produce candidates.
// Top30 hot-gap and divergence signals are aggregator markers and
// MUST be marked fused_at without ever appearing in a candidate
// row. A regression here surfaces as bogus mojibake candidates
// (e.g. canonical_symbol = "CEX_ONLY") flowing through to
// ProduceDecisionCards and the Lark group.
func TestFuseSignalsSkipsTop30AggregatorSignals(t *testing.T) {
	now := time.Date(2026, 5, 30, 3, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(1702), SignalTop30Divergence, "cex_only", "top30", nil, nil, nil,
		"CEX_ONLY", "CEX 独有热门 · edgeX 未上线", nil, nil, nil,
		"perp", "canonical", nil, nil, nil,
		now, nil, nil, nil,
		nil, nil, "top30_divergence|cex_only|2026-05-30", []byte(`{}`), nil, nil,
	).AddRow(
		int64(1703), SignalTop30HotGap, "评估上架", "top30", nil, nil, nil,
		"BEAT", "BEAT-USDT (perp)", nil, nil, nil,
		"perp", "canonical", nil, nil, nil,
		now, nil, nil, nil,
		nil, nil, "top30_hot_gap|BEAT-USDT (perp)|评估上架|2026-05-30", []byte(`{}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	// Both rows are aggregator markers — the producer must still
	// mark them fused so the next tick does not re-process them,
	// but it must NOT INSERT into t_listing_candidate.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(1702)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(1703)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 0 {
		t.Fatalf("Candidates = %d, want 0 (aggregator signals must not produce candidates)", result.Candidates)
	}
	if result.SkippedAggregator != 2 {
		t.Fatalf("SkippedAggregator = %d, want 2", result.SkippedAggregator)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestFuseSignalsSkipsMetadataChangedSignal locks the 2026-06-01
// incident contract: instrument_diff/metadata_changed signals are
// observation-only. They MUST be MarkSignalFused so the unfused queue
// drains, but they MUST NOT upsert into t_listing_candidate (which
// would surface as a bogus "🚨 New Perp Listing Detected" Lark card
// for an already-listed token like GLM).
//
// A regression here is precisely the 2026-06-01 hash-noise incident.
func TestFuseSignalsSkipsMetadataChangedSignal(t *testing.T) {
	now := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(9001), SignalInstrumentDiff, DiffMetadataChanged, "gate", "futures_usdt", "GLM_USDT", nil,
		"GLM", "GLM-USDT (perp)", "GLM", "USDT", "USDT",
		"perp", "canonical", "trading", "active", nil,
		now, nil, nil, nil,
		nil, nil, "instrument_diff:abc", []byte(`{"diff_subtype":"metadata_changed"}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	// Only the MarkSignalFused happens; NO INSERT INTO t_listing_candidate.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9001)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 0 {
		t.Fatalf("Candidates = %d, want 0 (metadata_changed must be observation-only)", result.Candidates)
	}
	if result.SkippedObservationOnly != 1 {
		t.Fatalf("SkippedObservationOnly = %d, want 1", result.SkippedObservationOnly)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestFuseSignalsPromotesNewSymbolSignal pins the positive path: a
// genuine new_symbol signal still elevates to a candidate so the
// observation-only gate cannot accidentally suppress real listings.
func TestFuseSignalsPromotesNewSymbolSignal(t *testing.T) {
	now := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(9100), SignalInstrumentDiff, DiffNewSymbol, "gate", "futures_usdt", "NEWTOKEN_USDT", nil,
		"NEWTOKEN", "NEWTOKEN-USDT (perp)", "NEWTOKEN", "USDT", "USDT",
		"perp", "canonical", "trading", "active", nil,
		now, nil, nil, nil,
		nil, nil, "instrument_diff:xyz", []byte(`{"diff_subtype":"new_symbol"}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).
		WillReturnResult(sqlmock.NewResult(77, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(77), int64(9100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9100)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 || result.Signals != 1 {
		t.Fatalf("counts = %+v, want 1/1", result)
	}
	if result.SkippedObservationOnly != 0 {
		t.Fatalf("SkippedObservationOnly = %d, want 0", result.SkippedObservationOnly)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestFuseSignalsPromotesStatusChangedToActive: status_changed with
// status_to ∈ {active, pre_listing} is a real listing event and must
// elevate to candidate.
func TestFuseSignalsPromotesStatusChangedToActive(t *testing.T) {
	now := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(9200), SignalInstrumentDiff, DiffStatusChanged, "binance", "usdm_futures", "WAKEUP", nil,
		"WAKEUP", "WAKEUP-USDT (perp)", "WAKEUP", "USDT", "USDT",
		"perp", "canonical", "TRADING", "active", nil,
		now, nil, nil, nil,
		nil, nil, "instrument_diff:wakeup", []byte(`{"diff_subtype":"status_changed","status_to":"active"}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).
		WillReturnResult(sqlmock.NewResult(88, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(88), int64(9200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9200)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 {
		t.Fatalf("Candidates = %d, want 1", result.Candidates)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestFuseSignalsSkipsStatusChangedToPaused: status_changed where the
// target state is paused/inactive/delisted is the OPPOSITE of a new
// listing — must NOT generate a "New Perp Listing Detected" card.
func TestFuseSignalsSkipsStatusChangedToPaused(t *testing.T) {
	now := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(9300), SignalInstrumentDiff, DiffStatusChanged, "binance", "usdm_futures", "GLM", nil,
		"GLM", "GLM-USDT (perp)", "GLM", "USDT", "USDT",
		"perp", "canonical", "BREAK", "paused", nil,
		now, nil, nil, nil,
		nil, nil, "instrument_diff:glm_paused", []byte(`{"diff_subtype":"status_changed","status_to":"paused"}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9300)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 0 {
		t.Fatalf("Candidates = %d, want 0", result.Candidates)
	}
	if result.SkippedObservationOnly != 1 {
		t.Fatalf("SkippedObservationOnly = %d, want 1", result.SkippedObservationOnly)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestFuseSignalsAnnouncementUnaffectedByInstrumentSubtypeGate
// guarantees the observation-only gate only applies to
// instrument_diff signals — announcement_listing must still always
// elevate (that path has its own confidence model).
func TestFuseSignalsAnnouncementUnaffectedByInstrumentSubtypeGate(t *testing.T) {
	now := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(9400), SignalAnnouncementListing, AnnouncementPerpListing, "bybit", nil, nil, nil,
		"NEWANN", "NEWANN-USDT (perp)", "NEWANN", "USDT", "USDT",
		"perp", "canonical", nil, nil, nil,
		now, nil, nil, nil,
		nil, nil, "ann:newann", []byte(`{}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).
		WillReturnResult(sqlmock.NewResult(99, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(99), int64(9400)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9400)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 {
		t.Fatalf("Candidates = %d, want 1", result.Candidates)
	}
	if result.SkippedObservationOnly != 0 {
		t.Fatalf("SkippedObservationOnly = %d, want 0", result.SkippedObservationOnly)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsSkipsRelistedAsObservationOnly(t *testing.T) {
	now := time.Date(2026, 6, 2, 2, 18, 0, 0, time.UTC)
	listingTime := time.Date(2025, 11, 14, 7, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}).AddRow(
		int64(9500), SignalInstrumentDiff, DiffRelisted, "bingx", "swap", "NCSKGE2USD-USDT", nil,
		"NCSKGE2USD", "NCSKGE2USD-USDT", "NCSKGE2USD", "USDT", "USDT",
		"perp", "synthetic", "1", "active", nil,
		now, nil, nil, listingTime,
		nil, nil, "instrument_diff:bingx:ncskge:relisted", []byte(`{"diff_subtype":"relisted","status_from":"delisted","status_to":"active","listing_time_to":"2025-11-14T07:00:00Z"}`), nil, nil,
	)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9500)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 0 {
		t.Fatalf("Candidates = %d, want 0 (relisted must not produce New Perp Listing)", result.Candidates)
	}
	if result.SkippedObservationOnly != 1 {
		t.Fatalf("SkippedObservationOnly = %d, want 1", result.SkippedObservationOnly)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func fusionSignalColumns() []string {
	return []string{
		"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
		"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
		"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
		"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
		"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
	}
}

func fusionCandidateColumns() []string {
	return []string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}
}

func addFusionInstrumentSignal(rows *sqlmock.Rows, id int64, platform, subtype, canonical, status string, observedAt time.Time, listingTime *time.Time) *sqlmock.Rows {
	apiSymbol := canonical + "USDT"
	return rows.AddRow(
		id, SignalInstrumentDiff, subtype, platform, "swap", apiSymbol, nil,
		canonical, canonical+"-USDT (perp)", canonical, "USDT", "USDT",
		"perp", "canonical", status, status, nil,
		observedAt, nil, nil, nullTimePtr(listingTime),
		nil, nil, "instrument_diff:"+platform+":"+canonical+":"+subtype+":"+idString(id), []byte(`{"diff_subtype":"`+subtype+`"}`), nil, nil,
	)
}

func addFusionAnnouncementSignal(rows *sqlmock.Rows, id int64, platform, canonical string, observedAt time.Time) *sqlmock.Rows {
	return rows.AddRow(
		id, SignalAnnouncementListing, AnnouncementPerpListing, platform, nil, nil, nil,
		canonical, canonical+"-USDT (perp)", canonical, "USDT", "USDT",
		"perp", "canonical", nil, nil, nil,
		observedAt, nil, nil, nil,
		nil, nil, "announcement_listing:"+platform+":"+canonical+":"+idString(id), []byte(`{}`), nil, nil,
	)
}

func idString(id int64) string { return fmt.Sprintf("%d", id) }

func expectNoCandidateByKey(mock sqlmock.Sqlmock, canonical string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WithArgs(canonical, "perp", "canonical").
		WillReturnRows(sqlmock.NewRows(fusionCandidateColumns()))
}

func TestFuseSignalsNewSymbolWithHistoricalListingTimeMarksAlreadyListedNoAction(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	listingTime := now.Add(-30 * 24 * time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(rows, 9600, "okx", DiffNewSymbol, "SPCX", StatusActive, now, &listingTime)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).WillReturnRows(rows)
	expectNoCandidateByKey(mock, "SPCX")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).WithArgs(
		"SPCX", "SPCX-USDT (perp)", "perp", "canonical",
		LifecycleAlreadyListed, "竞品已历史上线", EvidenceInstrumentDiffOnly, ConfidenceLow,
		sqlmock.AnyArg(), BusinessScoreVersion, RecommendationNoAction, RecommendationLabels[RecommendationNoAction],
		sqlmock.AnyArg(), sqlmock.AnyArg(), now, now,
	).WillReturnResult(sqlmock.NewResult(120, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(120), int64(9600)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9600)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 || result.Signals != 1 {
		t.Fatalf("counts = %+v, want 1 candidate / 1 signal", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsNewSymbolWithRecentListingTimeStillPromotesCandidate(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	listingTime := now.Add(-2 * time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(rows, 9601, "okx", DiffNewSymbol, "FRESH", StatusActive, now, &listingTime)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).WillReturnResult(sqlmock.NewResult(121, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(121), int64(9601)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9601)).WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 || result.SkippedObservationOnly != 0 {
		t.Fatalf("result = %+v, want promoted candidate", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsNewSymbolWithFutureListingTimeStillPromotesCandidate(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	listingTime := now.Add(2 * time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(rows, 9602, "bingx", DiffNewSymbol, "FUTR", StatusPreListing, now, &listingTime)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).WillReturnResult(sqlmock.NewResult(122, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(122), int64(9602)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9602)).WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 {
		t.Fatalf("Candidates = %d, want 1", result.Candidates)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsHistoricalAndAnnouncementDoesNotDowngradeGroup(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	listingTime := now.Add(-10 * 24 * time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(rows, 9603, "okx", DiffNewSymbol, "MIXANN", StatusActive, now, &listingTime)
	addFusionAnnouncementSignal(rows, 9604, "bybit", "MIXANN", now)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).WithArgs(
		"MIXANN", "MIXANN-USDT (perp)", "perp", "canonical",
		LifecycleAnnouncedPendingAPI, LifecycleStatusLabels[LifecycleAnnouncedPendingAPI], EvidenceAnnouncementPendingAPI, ConfidenceMedium,
		sqlmock.AnyArg(), BusinessScoreVersion, RecommendationPreAssessment, RecommendationLabels[RecommendationPreAssessment],
		sqlmock.AnyArg(), sqlmock.AnyArg(), now, now,
	).WillReturnResult(sqlmock.NewResult(123, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(123), int64(9603)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(123), int64(9604)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9603)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9604)).WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 || result.Signals != 2 {
		t.Fatalf("counts = %+v, want 1 candidate / 2 signals", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsHistoricalAndFreshInstrumentDoesNotDowngradeGroup(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	historicalTime := now.Add(-10 * 24 * time.Hour)
	freshTime := now.Add(2 * time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(rows, 9605, "okx", DiffNewSymbol, "MIXAPI", StatusActive, now, &historicalTime)
	addFusionInstrumentSignal(rows, 9606, "binance", DiffNewSymbol, "MIXAPI", StatusPreListing, now, &freshTime)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_candidate")).WithArgs(
		"MIXAPI", "MIXAPI-USDT (perp)", "perp", "canonical",
		LifecycleAPIDetectedNoAnnouncement, LifecycleStatusLabels[LifecycleAPIDetectedNoAnnouncement], EvidenceInstrumentDiffOnly, ConfidenceMedium,
		sqlmock.AnyArg(), BusinessScoreVersion, RecommendationPrepareListing, RecommendationLabels[RecommendationPrepareListing],
		sqlmock.AnyArg(), sqlmock.AnyArg(), now, now,
	).WillReturnResult(sqlmock.NewResult(124, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(124), int64(9605)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(124), int64(9606)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9605)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9606)).WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 1 || result.Signals != 2 {
		t.Fatalf("counts = %+v, want 1 candidate / 2 signals", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuseSignalsHistoricalDoesNotDowngradeExistingActionableCandidate(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	listingTime := now.Add(-10 * 24 * time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(rows, 9607, "okx", DiffNewSymbol, "KEEP", StatusActive, now, &listingTime)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WithArgs("KEEP", "perp", "canonical").
		WillReturnRows(sqlmock.NewRows(fusionCandidateColumns()).AddRow(
			int64(130), "KEEP", "KEEP-USDT (perp)", "perp", "canonical",
			LifecycleConfirmedListingCandidate, LifecycleStatusLabels[LifecycleConfirmedListingCandidate], EvidenceAnnouncementAndAPI, ConfidenceHigh,
			90.0, BusinessScoreVersion, RecommendationPrepareListing, RecommendationLabels[RecommendationPrepareListing],
			[]byte(`["binance","bybit"]`), nil, now.Add(-24*time.Hour), now.Add(-2*time.Hour),
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_candidate_signal")).WithArgs(int64(130), int64(9607)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE t_listing_signal_observation SET fused_at")).WithArgs(now, int64(9607)).WillReturnResult(sqlmock.NewResult(0, 1))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	result, err := FuseSignals(context.Background(), repo, FusionDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("FuseSignals err = %v", err)
	}
	if result.Candidates != 0 || result.Signals != 1 {
		t.Fatalf("counts = %+v, want linked signal without candidate overwrite", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
