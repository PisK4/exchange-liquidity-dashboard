package listing

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing/announcement"
	"edgex-ops-intelligence/backend/internal/listing/instrument"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectListingRunOnceLeaseAcquired(mock sqlmock.Sqlmock, ownerID string) {
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_worker_lease")).
		WithArgs("listing:run_once", ownerID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT owner_id FROM t_listing_worker_lease WHERE lease_name = ?")).
		WithArgs("listing:run_once").
		WillReturnRows(sqlmock.NewRows([]string{"owner_id"}).AddRow(ownerID))
}

func expectListingRunOnceLeaseReleased(mock sqlmock.Sqlmock, ownerID string) {
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_listing_worker_lease WHERE lease_name = ? AND owner_id = ?")).
		WithArgs("listing:run_once", ownerID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestEngineRunOnceSkipsWhenLeaseNotAcquired(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	const ownerID = "listing-engine-test"
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_worker_lease")).
		WithArgs("listing:run_once", ownerID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT owner_id FROM t_listing_worker_lease WHERE lease_name = ?")).
		WithArgs("listing:run_once").
		WillReturnRows(sqlmock.NewRows([]string{"owner_id"}).AddRow("other-owner"))

	engine := NewEngine(config.Config{}, repo, EngineDeps{
		Now:          func() time.Time { return now },
		OwnerID:      ownerID,
		LoadUniverse: func() (*config.ListedUniverse, error) { return &config.ListedUniverse{}, nil },
	})
	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err = %v", err)
	}
	if summary.LeaseAcquired {
		t.Fatalf("summary.LeaseAcquired = true, want false")
	}
	if !summary.Finished.Equal(now) {
		t.Fatalf("summary.Finished = %s, want %s", summary.Finished, now)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEngineRunOnceReturnsSummary(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	const ownerID = "listing-engine-test"
	expectListingRunOnceLeaseAcquired(mock, ownerID)

	// FuseSignals and Top30 push fail closed because the universe is
	// not loaded. Delivery drain still runs and reads an empty due
	// outbox; the engine must remain robust in that path.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
			"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
		}))
	expectListingRunOnceLeaseReleased(mock, ownerID)

	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{Enabled: true}
	engine := NewEngine(cfg, repo, EngineDeps{
		Now:     func() time.Time { return now },
		OwnerID: ownerID,
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
	const ownerID = "listing-engine-test"
	expectListingRunOnceLeaseAcquired(mock, ownerID)

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
	expectListingRunOnceLeaseReleased(mock, ownerID)

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
		OwnerID:      ownerID,
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
	const ownerID = "listing-engine-test"
	expectListingRunOnceLeaseAcquired(mock, ownerID)

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
	expectListingRunOnceLeaseReleased(mock, ownerID)

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
		OwnerID:      ownerID,
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

// TestEngineRunOnceProducesDecisionCardsWhenEnabled locks the
// Phase 2.5 wiring contract: when DecisionCard.Enabled is true and
// the candidate list returns at least one actionable row, the
// engine runs the ProduceDecisionCards step (risk plan + outbox
// write) AFTER fusion and surfaces the per-tick result on
// summary.DecisionCard.
func TestEngineRunOnceProducesDecisionCardsWhenEnabled(t *testing.T) {
	now := time.Date(2026, 5, 30, 14, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	const ownerID = "listing-engine-test"
	expectListingRunOnceLeaseAcquired(mock, ownerID)

	// Fusion: no unfused signals.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
			"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
			"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
			"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
			"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
		}))
	// Top30: empty snapshot.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	// Decision card step: ListCandidates returns one fresh actionable
	// candidate; latest decision lookup is empty (no cooldown); both
	// the risk plan row and the outbox row land.
	score := 80.0
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
			"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
			"business_score", "business_score_version", "recommendation", "recommendation_label",
			"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
		}).AddRow(
			int64(7), "ABC", "ABC-USDT (perp)", "perp", "canonical",
			LifecycleConfirmedListingCandidate, "已确认候选", EvidenceAnnouncementAndAPI, ConfidenceHigh,
			score, "v1", RecommendationPrepareListing, "准备上线",
			[]byte(`["binance"]`), nil, now.Add(-2*time.Hour), now.Add(-1*time.Hour),
		))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action, callback_ts FROM t_listing_decision")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"action", "callback_ts"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_risk_plan")).
		WillReturnResult(sqlmock.NewResult(201, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.id, s.signal_type, s.signal_subtype")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(fusionSignalColumns()))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(301, 1))
	// Delivery drain: no due rows.
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
			"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
		}))
	expectListingRunOnceLeaseReleased(mock, ownerID)

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{
		Enabled:   true,
		Worker:    config.ListingWorkerConfig{MaxAttempts: 5},
		Top30Push: config.ListingTop30PushConfig{Enabled: true, StaleAfter: time.Hour},
		Delivery:  config.ListingDeliveryConfig{Enabled: true},
		DecisionCard: config.ListingDecisionCardConfig{
			Enabled:        true,
			IgnoreCooldown: 24 * time.Hour,
			MaxPerTick:     10,
		},
	}
	engine := NewEngine(cfg, repo, EngineDeps{
		Now:          func() time.Time { return now },
		OwnerID:      ownerID,
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
	})
	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err = %v", err)
	}
	if summary.DecisionCard.OutboxRows != 1 || summary.DecisionCard.RiskPlans != 1 {
		t.Errorf("DecisionCard = %+v, want OutboxRows=1 RiskPlans=1", summary.DecisionCard)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestEngineRunOnceDrivesInstrumentAndAnnouncementSourcesBeforeFusion
// locks the Phase 1.5 wiring contract: every configured instrument /
// announcement source must be invoked once per tick, wrapped in the
// source-health upsert, BEFORE fusion runs. Running pollers in front
// of fusion means newly inserted signals get picked up in the same
// tick rather than waiting a full cycle, and source health rows are
// visible immediately even on the first ever start.
//
// The test uses cold-start fixtures (no existing snapshots/announcements
// rows) so the expectation surface is small and stable: each source
// produces baseline writes only, no signals, no diff/symbol fan-out.
func TestEngineRunOnceDrivesInstrumentAndAnnouncementSourcesBeforeFusion(t *testing.T) {
	now := time.Date(2026, 5, 29, 19, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	const ownerID = "listing-engine-test"
	expectListingRunOnceLeaseAcquired(mock, ownerID)

	// --- Instrument source (cold start, single BTC) ---
	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/instrument/binance/usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{
			"source_key", "source_type", "platform", "status",
			"last_success_at", "last_error_at", "consecutive_error_count", "schema_drift_count",
			"disabled_until", "last_error", "updated_at",
		}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_instrument_snapshot")).
		WithArgs("binance", "usdm_futures").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_instrument_snapshot")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// --- Announcement source (cold start, single perp ann) ---
	mock.ExpectQuery(regexp.QuoteMeta("SELECT source_key, source_type, platform, status")).
		WithArgs("listing/announcement/bybit").
		WillReturnRows(sqlmock.NewRows([]string{
			"source_key", "source_type", "platform", "status",
			"last_success_at", "last_error_at", "consecutive_error_count", "schema_drift_count",
			"disabled_until", "last_error", "updated_at",
		}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM t_listing_announcement")).
		WithArgs("bybit").
		WillReturnRows(sqlmock.NewRows([]string{"present"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_announcement")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_source_state")).
		WillReturnResult(sqlmock.NewResult(2, 1))

	// --- Fusion: no unfused signals. ---
	mock.ExpectQuery(`SELECT .+ FROM t_listing_signal_observation .+ fused_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "signal_type", "signal_subtype", "source_platform", "market_type", "api_symbol", "api_market_id",
			"canonical_symbol", "display_symbol", "base_asset", "quote_asset", "settle_asset",
			"market_surface", "instrument_kind", "status_raw", "status_normalized", "confidence",
			"observed_at", "source_snapshot_ts", "published_at", "listing_time_ts",
			"source_endpoint", "source_url", "fingerprint", "payload_json", "raw_payload_json", "raw_payload_hash",
		}))
	// --- Top30: no snapshot rows. ---
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	// --- Delivery drain: no due rows. ---
	mock.ExpectQuery(`SELECT .+ FROM t_listing_delivery_outbox WHERE status IN`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "dedupe_key", "target_channel", "status", "attempt_count", "max_attempts",
			"next_attempt_at", "payload_json", "last_error", "sent_at", "created_at", "updated_at",
		}))
	expectListingRunOnceLeaseReleased(mock, ownerID)

	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{
		Enabled:   true,
		Worker:    config.ListingWorkerConfig{MaxAttempts: 5},
		Top30Push: config.ListingTop30PushConfig{StaleAfter: time.Hour},
		Delivery:  config.ListingDeliveryConfig{Enabled: true},
	}

	instrCalls := 0
	annCalls := 0
	engine := NewEngine(cfg, repo, EngineDeps{
		Now:          func() time.Time { return now },
		OwnerID:      ownerID,
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		InstrumentSources: []InstrumentSource{{
			Platform:   "binance",
			MarketType: "usdm_futures",
			SourceKey:  "listing/instrument/binance/usdm_futures",
			Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
				instrCalls++
				return []instrument.NormalizedInstrument{{
					Platform: "binance", MarketType: "usdm_futures", APISymbol: "BTCUSDT",
					CanonicalSymbol: "BTC", MarketSurface: "perp", InstrumentKind: "canonical",
					StatusNormalized: "active", RawJSON: json.RawMessage(`{"symbol":"BTCUSDT"}`),
					StableHash: "engine-test-btc",
				}}, nil
			},
		}},
		AnnouncementSources: []AnnouncementSource{{
			Platform:  "bybit",
			SourceKey: "listing/announcement/bybit",
			Fetch: func(ctx context.Context) ([]json.RawMessage, error) {
				annCalls++
				return []json.RawMessage{json.RawMessage(`{}`)}, nil
			},
			Parse: func(raw json.RawMessage) (announcement.ParsedAnnouncement, error) {
				return announcement.ParsedAnnouncement{
					Platform:        "bybit",
					AnnouncementID:  "ann-engine-001",
					Title:           "ABC Perpetual Contract Listing",
					ParseConfidence: announcement.ConfidenceHigh,
					RawPayloadJSON:  json.RawMessage(`{"id":"ann-engine-001"}`),
					RawPayloadHash:  "engine-test-ann",
				}, nil
			},
		}},
	})

	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err = %v", err)
	}
	if instrCalls != 1 || annCalls != 1 {
		t.Errorf("source calls instrument=%d announcement=%d, want 1/1", instrCalls, annCalls)
	}
	if len(summary.InstrumentPolls) != 1 || !summary.InstrumentPolls[0].Baseline {
		t.Errorf("InstrumentPolls = %+v, want one baseline pass", summary.InstrumentPolls)
	}
	if len(summary.AnnouncementPolls) != 1 || !summary.AnnouncementPolls[0].Baseline {
		t.Errorf("AnnouncementPolls = %+v, want one baseline pass", summary.AnnouncementPolls)
	}
	if len(summary.InstrumentHealth) != 1 || summary.InstrumentHealth[0].Status != SourceStatusOK {
		t.Errorf("InstrumentHealth = %+v, want one ok entry", summary.InstrumentHealth)
	}
	if len(summary.AnnouncementHealth) != 1 || summary.AnnouncementHealth[0].Status != SourceStatusOK {
		t.Errorf("AnnouncementHealth = %+v, want one ok entry", summary.AnnouncementHealth)
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
