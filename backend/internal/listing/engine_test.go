package listing

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEngineRunOnceReturnsSummary(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// FuseSignals and Top30 push fail closed because the universe is
	// not loaded. Delivery drain still runs and reads an empty due
	// outbox; the engine must remain robust in that path.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
			"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
		}))

	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{Enabled: true}
	engine := NewEngine(cfg, repo, EngineDeps{
		Now: func() time.Time { return now },
		LoadUniverse: func() (*config.ListedUniverse, error) {
			return &config.ListedUniverse{}, nil
		},
	})
	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err = %v", err)
	}
	if summary.Fusion.FailClosed == "" {
		t.Fatalf("expected fusion fail closed, got %+v", summary.Fusion)
	}
	if summary.Top30Push.FailClosed == "" {
		t.Fatalf("expected top30 fail closed, got %+v", summary.Top30Push)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEngineRunOnceDrainsOutboxWhenUniverseLoaded(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// Fusion: no unfused signals.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
			"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
			"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
			"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
			"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
		}))
	// Top30: no snapshot.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	// Delivery drain: no due outbox.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
			"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
		}))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{
		Enabled:   true,
		Worker:    config.ListingWorkerConfig{MaxAttempts: 5},
		Top30Push: config.ListingTop30PushConfig{StaleAfter: time.Hour},
		Delivery:  config.ListingDeliveryConfig{Enabled: true},
	}
	engine := NewEngine(cfg, repo, EngineDeps{
		Now:          func() time.Time { return now },
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
	})
	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err = %v", err)
	}
	if summary.Fusion.FailClosed != "" {
		t.Fatalf("fusion should not fail closed, got %+v", summary.Fusion)
	}
	if summary.Top30Push.FailClosed != "no_snapshot" {
		t.Fatalf("top30 fail = %q, want no_snapshot", summary.Top30Push.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEngineRunOnceProducesDivergencePushWhenEnabled(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	snapshot := now.Add(-2 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	// Fusion: no unfused signals.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
			"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
			"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
			"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
			"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
		}))
	// Top30 hot-gap push: single MAX(snapshot_ts) returning latest, then
	// the row pull. The FOO row carries no suggested_action so the
	// hot-gap producer skips it — the test focuses on the divergence
	// branch.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(snapshot))
	rows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).
		AddRow("binance", "FOO-USDT (perp)", 1, 1000.0, 1, false, "", snapshot)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(snapshot).
		WillReturnRows(rows)
	// Divergence producer: same loader → MAX + WHERE.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(snapshot))
	divRows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).
		AddRow("binance", "FOO-USDT (perp)", 1, 1000.0, 1, false, "", snapshot)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(snapshot).
		WillReturnRows(divRows)
	// One cex_only event → one signal + one outbox row.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Delivery drain: no due outbox (newly inserted rows have
	// next_attempt_at = now, so they are not selected with strict >.
	// Use a row pattern that just returns zero rows to stay simple.)
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
			"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
		}))

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{
		Enabled: true,
		Worker:  config.ListingWorkerConfig{MaxAttempts: 5},
		Top30Push: config.ListingTop30PushConfig{
			StaleAfter:               time.Hour,
			AutoQuietAfterStreakDays: 3,
			MaxPerTick:               5,
		},
		Top30DivergencePush: config.Top30DivergencePushConfig{
			Enabled:     true,
			TopNPerCard: 10,
			StaleAfter:  time.Hour,
			SendSpacing: 30 * time.Second,
		},
		Delivery: config.ListingDeliveryConfig{Enabled: true},
	}
	cfg.Runtime.Top30Divergence = config.Top30DivergenceConfig{
		CEXPlatforms:         []string{"binance"},
		DEXPlatforms:         []string{"hyperliquid"},
		SignificantRankDelta: 10,
	}
	engine := NewEngine(cfg, repo, EngineDeps{
		Now:          func() time.Time { return now },
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
	})
	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err = %v", err)
	}
	if summary.DivergencePush.Produced != 1 || summary.DivergencePush.Signals != 1 || summary.DivergencePush.OutboxRows != 1 {
		t.Fatalf("DivergencePush = %+v, want Produced=1 Signals=1 OutboxRows=1", summary.DivergencePush)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBuildDeliveryHTTPClientWiresProxyOnlyWhenConfigured(t *testing.T) {
	t.Parallel()
	t.Run("blank proxy returns default client", func(t *testing.T) {
		t.Parallel()
		client, err := buildDeliveryHTTPClient("")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if client != nil && client.Transport != nil {
			t.Fatalf("expected DefaultClient (nil transport), got %+v", client.Transport)
		}
	})
	t.Run("valid proxy installs http.ProxyURL transport", func(t *testing.T) {
		t.Parallel()
		client, err := buildDeliveryHTTPClient("http://host.docker.internal:7897")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		tr, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
		}
		if tr.Proxy == nil {
			t.Fatalf("transport.Proxy should be non-nil for configured proxy")
		}
		req, _ := http.NewRequest("GET", "https://open.larksuite.com/", nil)
		got, err := tr.Proxy(req)
		if err != nil {
			t.Fatalf("proxy resolver err: %v", err)
		}
		if got == nil || got.String() != "http://host.docker.internal:7897" {
			t.Fatalf("resolved proxy = %v, want http://host.docker.internal:7897", got)
		}
	})
	t.Run("malformed proxy url surfaces an error", func(t *testing.T) {
		t.Parallel()
		if _, err := buildDeliveryHTTPClient("not-a-url"); err == nil {
			t.Fatalf("expected error for malformed proxy, got nil")
		}
	})
}
