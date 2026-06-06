package listing

import (
	"context"
	"regexp"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing/liquidity"

	"github.com/DATA-DOG/go-sqlmock"
)

func liquidityCfg() config.LiquidityAlertConfig {
	return config.LiquidityAlertConfig{
		Enabled:          true,
		DepthTierPct:     0.001,
		LagThreshold:     0.5,
		MinComparators:   3,
		ReissueInterval:  6 * time.Hour,
		ClearConsecutive: 3,
		StaleAfter:       30 * time.Minute,
		PollInterval:     5 * time.Minute,
		MaxPerTick:       5,
	}
}

func TestProduceLiquidityAlertPushDisabled(t *testing.T) {
	now := time.Date(2026, 5, 29, 3, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	cfg := liquidityCfg()
	cfg.Enabled = false

	res, err := ProduceLiquidityAlertPush(context.Background(), repo, LiquidityAlertDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
		Cfg:          cfg,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.FailClosed != "disabled" {
		t.Fatalf("FailClosed = %q, want disabled", res.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceLiquidityAlertPushFailClosedTierUnset(t *testing.T) {
	now := time.Date(2026, 5, 29, 3, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	cfg := liquidityCfg()
	cfg.DepthTierPct = 0
	res, err := ProduceLiquidityAlertPush(context.Background(), repo, LiquidityAlertDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
		Cfg:          cfg,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.FailClosed != "tier_unset" {
		t.Fatalf("FailClosed = %q, want tier_unset", res.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceLiquidityAlertPushNoSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 29, 3, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})

	mock.ExpectQuery(`FROM t_orderbook_snapshot`).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "display_symbol", "snapshot_ts", "bid_usd", "ask_usd", "total_usd",
		}))

	res, err := ProduceLiquidityAlertPush(context.Background(), repo, LiquidityAlertDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
		Cfg:          liquidityCfg(),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.FailClosed != "no_snapshot" {
		t.Fatalf("FailClosed = %q, want no_snapshot", res.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceLiquidityAlertPushFirstTrigger(t *testing.T) {
	now := time.Date(2026, 5, 29, 3, 0, 0, 0, time.UTC)
	snapshotTS := now.Add(-2 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})

	// 1) Depth matrix: BTC with 4 competitors + edgeX lagging.
	//    edgeX 2.4M / competitor median 6.0M = 0.4 ratio (< 0.5 → lag).
	//    edgeX is NOT the last (mexc lower) → no worst_depth.
	rows := sqlmock.NewRows([]string{
		"platform", "display_symbol", "snapshot_ts", "bid_usd", "ask_usd", "total_usd",
	}).
		AddRow("edgex", "BTC-USDT (perp)", snapshotTS, 1.2e6, 1.2e6, 2.4e6).
		AddRow("binance", "BTC-USDT (perp)", snapshotTS, 4.0e6, 4.5e6, 8.5e6).
		AddRow("okx", "BTC-USDT (perp)", snapshotTS, 3.5e6, 3.6e6, 7.1e6).
		AddRow("bybit", "BTC-USDT (perp)", snapshotTS, 3.0e6, 3.0e6, 6.0e6).
		AddRow("bitget", "BTC-USDT (perp)", snapshotTS, 3.0e6, 3.0e6, 6.0e6).
		AddRow("mexc", "BTC-USDT (perp)", snapshotTS, 0.5e6, 0.5e6, 1.0e6)
	mock.ExpectQuery(`FROM t_orderbook_snapshot`).WillReturnRows(rows)

	// 2) ListActiveAlertStates: nothing active yet.
	mock.ExpectQuery(`FROM t_listing_alert_state\s+WHERE alert_kind IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"alert_kind", "canonical_symbol", "status", "severity_seq", "reissue_count", "clear_streak",
			"first_triggered_at", "last_pushed_at", "last_evaluated_at",
		}))

	// 3) LoadAlertState for liquidity_lag/BTC → no row. sqlmock turns
	//    an empty rows result into sql.ErrNoRows on the QueryRow scan,
	//    which the repo path treats as "no prior state".
	mock.ExpectQuery(`FROM t_listing_alert_state\s+WHERE alert_kind = \? AND canonical_symbol = \?`).
		WithArgs("liquidity_lag", "BTC").
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "severity_seq", "reissue_count", "clear_streak",
			"first_triggered_at", "last_pushed_at", "last_evaluated_at",
		}))

	// 4) insertOutbox + upsert.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_alert_state")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := ProduceLiquidityAlertPush(context.Background(), repo, LiquidityAlertDeps{
		LoadUniverse:  func() (*config.ListedUniverse, error) { return universe, nil },
		Now:           func() time.Time { return now },
		DashboardBase: "https://dashboard.example/liquidity",
		WebhookURL:    "", // disabled → outbox row written with status=disabled, no network
		MaxAttempts:   5,
		Cfg:           liquidityCfg(),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.FailClosed != "" {
		t.Fatalf("unexpected FailClosed = %q", res.FailClosed)
	}
	if res.Candidates != 1 || res.FirstAlerts != 1 || res.OutboxRows != 1 {
		t.Fatalf("result = %+v, want Candidates=1 FirstAlerts=1 OutboxRows=1", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceLiquidityAlertPushSilentWhenWithinCooldown(t *testing.T) {
	now := time.Date(2026, 5, 29, 3, 0, 0, 0, time.UTC)
	snapshotTS := now.Add(-2 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})

	rows := sqlmock.NewRows([]string{
		"platform", "display_symbol", "snapshot_ts", "bid_usd", "ask_usd", "total_usd",
	}).
		AddRow("edgex", "BTC-USDT (perp)", snapshotTS, 1.2e6, 1.2e6, 2.4e6).
		AddRow("binance", "BTC-USDT (perp)", snapshotTS, 4.0e6, 4.5e6, 8.5e6).
		AddRow("okx", "BTC-USDT (perp)", snapshotTS, 3.5e6, 3.6e6, 7.1e6).
		AddRow("bybit", "BTC-USDT (perp)", snapshotTS, 3.0e6, 3.0e6, 6.0e6).
		AddRow("bitget", "BTC-USDT (perp)", snapshotTS, 3.0e6, 3.0e6, 6.0e6).
		AddRow("mexc", "BTC-USDT (perp)", snapshotTS, 0.5e6, 0.5e6, 1.0e6)
	mock.ExpectQuery(`FROM t_orderbook_snapshot`).WillReturnRows(rows)

	// ListActiveAlertStates: one active row, last pushed 1h ago (well
	// within the 6h reissue cooldown).
	activeRows := sqlmock.NewRows([]string{
		"alert_kind", "canonical_symbol", "status", "severity_seq", "reissue_count", "clear_streak",
		"first_triggered_at", "last_pushed_at", "last_evaluated_at",
	}).AddRow(
		"liquidity_lag", "BTC", "active", 1, 0, 0,
		now.Add(-3*time.Hour), now.Add(-1*time.Hour), now.Add(-5*time.Minute),
	)
	mock.ExpectQuery(`FROM t_listing_alert_state\s+WHERE alert_kind IN`).
		WillReturnRows(activeRows)

	// LoadAlertState returns the same active row.
	mock.ExpectQuery(`FROM t_listing_alert_state\s+WHERE alert_kind = \? AND canonical_symbol = \?`).
		WithArgs("liquidity_lag", "BTC").
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "severity_seq", "reissue_count", "clear_streak",
			"first_triggered_at", "last_pushed_at", "last_evaluated_at",
		}).AddRow(
			"active", 1, 0, 0,
			now.Add(-3*time.Hour), now.Add(-1*time.Hour), now.Add(-5*time.Minute),
		))

	// Silent path: upsert state only, no outbox insert.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_alert_state")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	res, err := ProduceLiquidityAlertPush(context.Background(), repo, LiquidityAlertDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
		MaxAttempts:  5,
		Cfg:          liquidityCfg(),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Silent != 1 || res.OutboxRows != 0 {
		t.Fatalf("result = %+v, want Silent=1 OutboxRows=0", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestProduceLiquidityAlertPushIdempotentOnDedupeConflict locks down
// Phase-0 step §4.3 of 2026-05-29-listing-agent.md: when two engine
// instances race on the same (kind, canonical, severity_seq, phase),
// the loser's INSERT IGNORE returns RowsAffected=0 instead of a
// duplicate-key error. The producer MUST treat that as a successful
// no-op:
//
//   - no error bubbles up
//   - UpsertAlertState still runs so the state row converges to the
//     winner's last_evaluated_at (this is safe because both racers
//     computed the same NewState from the same depth matrix)
//   - the outbox does not gain a duplicate row, so the downstream
//     Lark webhook only ever sees one card per (kind, canonical) seq
//
// This is the contract that lets us SKIP introducing a producer-side
// CAS or a worker_lease on the alert state table: dedupe_key UNIQUE
// + INSERT IGNORE on t_listing_delivery_outbox is the source of truth
// for "single push per alert event".
func TestProduceLiquidityAlertPushIdempotentOnDedupeConflict(t *testing.T) {
	now := time.Date(2026, 5, 29, 3, 0, 0, 0, time.UTC)
	snapshotTS := now.Add(-2 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})

	// Same depth matrix as TestProduceLiquidityAlertPushFirstTrigger:
	// BTC with edgeX 2.4M vs competitor median 6.0M → lag fires.
	rows := sqlmock.NewRows([]string{
		"platform", "display_symbol", "snapshot_ts", "bid_usd", "ask_usd", "total_usd",
	}).
		AddRow("edgex", "BTC-USDT (perp)", snapshotTS, 1.2e6, 1.2e6, 2.4e6).
		AddRow("binance", "BTC-USDT (perp)", snapshotTS, 4.0e6, 4.5e6, 8.5e6).
		AddRow("okx", "BTC-USDT (perp)", snapshotTS, 3.5e6, 3.6e6, 7.1e6).
		AddRow("bybit", "BTC-USDT (perp)", snapshotTS, 3.0e6, 3.0e6, 6.0e6).
		AddRow("bitget", "BTC-USDT (perp)", snapshotTS, 3.0e6, 3.0e6, 6.0e6).
		AddRow("mexc", "BTC-USDT (perp)", snapshotTS, 0.5e6, 0.5e6, 1.0e6)
	mock.ExpectQuery(`FROM t_orderbook_snapshot`).WillReturnRows(rows)

	mock.ExpectQuery(`FROM t_listing_alert_state\s+WHERE alert_kind IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"alert_kind", "canonical_symbol", "status", "severity_seq", "reissue_count", "clear_streak",
			"first_triggered_at", "last_pushed_at", "last_evaluated_at",
		}))
	mock.ExpectQuery(`FROM t_listing_alert_state\s+WHERE alert_kind = \? AND canonical_symbol = \?`).
		WithArgs("liquidity_lag", "BTC").
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "severity_seq", "reissue_count", "clear_streak",
			"first_triggered_at", "last_pushed_at", "last_evaluated_at",
		}))

	// INSERT IGNORE collides on dedupe_key — RowsAffected=0 simulates
	// the loser side of a multi-instance race. The producer MUST NOT
	// error out here; the unique-key collision is the intended dedupe.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// State upsert still runs so the row converges to last_evaluated_at=now.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_alert_state")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	res, err := ProduceLiquidityAlertPush(context.Background(), repo, LiquidityAlertDeps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
		MaxAttempts:  5,
		Cfg:          liquidityCfg(),
	})
	if err != nil {
		t.Fatalf("dedupe collision must not surface as error, got %v", err)
	}
	if res.FailClosed != "" {
		t.Fatalf("FailClosed = %q, want empty (dedupe collision is a no-op)", res.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMysqlTierLabelMatchesCollectorFormat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.001, "0.10%"},
		{0.0005, "0.05%"},
		{0.01, "1.00%"},
		{0.02, "2.00%"},
	}
	for _, tc := range cases {
		if got := mysqlTierLabel(tc.in); got != tc.want {
			t.Errorf("mysqlTierLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEventTypeForKind(t *testing.T) {
	if got := eventTypeForKind(liquidity.KindLiquidityLag); got != DeliveryEventLiquidityLag {
		t.Errorf("liquidity_lag → %q, want %q", got, DeliveryEventLiquidityLag)
	}
	if got := eventTypeForKind(liquidity.KindWorstDepth); got != DeliveryEventWorstDepth {
		t.Errorf("worst_depth → %q, want %q", got, DeliveryEventWorstDepth)
	}
}
