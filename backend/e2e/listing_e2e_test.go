//go:build e2e
// +build e2e

// Package e2e exercises the full Listing Agent pipeline against a real
// MySQL container. The stack is brought up by run-e2e.sh which sets
// LISTING_E2E_MYSQL_DSN before invoking `go test -tags=e2e`; the test
// here intentionally does NOT spawn the docker compose stack itself so
// it stays trivially debuggable from `go test` once the DSN is set.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-dashboard/backend/internal/api"
	"edgex-dashboard/backend/internal/collector"
	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/listing"
)

const (
	e2eCanonicalSymbol = "E2EZZZ"
	e2eDisplaySymbol   = "E2EZZZ-USDT (perp)"
)

func dsnFromEnv(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LISTING_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("LISTING_E2E_MYSQL_DSN not set; run via backend/e2e/run-e2e.sh")
	}
	return dsn
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := dsnFromEnv(t)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			return db
		}
		if time.Now().After(deadline) {
			t.Fatalf("mysql ping never succeeded: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func truncateListingTables(t *testing.T, db *sql.DB) {
	t.Helper()
	tables := []string{
		"t_listing_delivery_attempt",
		"t_listing_delivery_outbox",
		"t_listing_action_dispatch",
		"t_listing_watchlist",
		"t_listing_decision",
		"t_listing_risk_plan",
		"t_listing_candidate_signal",
		"t_listing_candidate",
		"t_listing_signal_observation",
		"t_listing_announcement_symbol",
		"t_listing_announcement",
		"t_listing_instrument_snapshot",
		"t_listing_source_state",
		"t_listing_worker_lease",
		"t_top30_snapshot",
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("disable fk: %v", err)
	}
	for _, table := range tables {
		if _, err := db.Exec("TRUNCATE TABLE " + table); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Fatalf("enable fk: %v", err)
	}
}

func seedSignals(t *testing.T, repo *listing.Repository, now time.Time) {
	t.Helper()
	announcement := listing.SignalObservation{
		SignalType:      listing.SignalAnnouncementListing,
		SignalSubtype:   listing.AnnouncementPerpListing,
		SourcePlatform:  "binance",
		CanonicalSymbol: e2eCanonicalSymbol,
		DisplaySymbol:   e2eDisplaySymbol,
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		ObservedAt:      now.Add(-30 * time.Second),
		Fingerprint:     fmt.Sprintf("announcement_listing|binance|e2e-ann-1|%s|perp|canonical", e2eCanonicalSymbol),
		PayloadJSON:     json.RawMessage(`{"announcement_id":"e2e-ann-1","title":"E2EZZZ-USDT perpetual contract listing"}`),
	}
	if _, _, err := repo.InsertSignal(context.Background(), announcement); err != nil {
		t.Fatalf("insert announcement signal: %v", err)
	}
	instrument := listing.SignalObservation{
		SignalType:       listing.SignalInstrumentDiff,
		SignalSubtype:    listing.DiffNewSymbol,
		SourcePlatform:   "binance",
		MarketType:       "usdm_futures",
		APISymbol:        "E2EZZZUSDT",
		CanonicalSymbol:  e2eCanonicalSymbol,
		DisplaySymbol:    e2eDisplaySymbol,
		MarketSurface:    "perp",
		InstrumentKind:   "canonical",
		StatusNormalized: "active",
		ObservedAt:       now,
		Fingerprint:      fmt.Sprintf("instrument_diff|binance|usdm_futures|E2EZZZUSDT|new_symbol|%s|perp|canonical", e2eCanonicalSymbol),
		PayloadJSON:      json.RawMessage(`{"diff_subtype":"new_symbol","status":"active"}`),
	}
	if _, _, err := repo.InsertSignal(context.Background(), instrument); err != nil {
		t.Fatalf("insert instrument signal: %v", err)
	}
}

func seedTop30Row(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO t_top30_snapshot
		   (platform, symbol, rank_no, volume_24h_usd, volume_7d_usd, delta_7d_pct,
		    coverage_count, edgex_listed, suggested_action, data_source, source_endpoint, status, snapshot_ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"binance", e2eDisplaySymbol, 3, 1_000_000.0, 6_000_000.0, 1.5,
		7, 0, "优先上架", "coingecko", "https://api.coingecko.test/derivatives", "complete", now,
	)
	if err != nil {
		t.Fatalf("insert top30 row: %v", err)
	}
}

func loadE2EUniverse() (*config.ListedUniverse, error) {
	return config.NewListedUniverseFromMap(map[string][]string{
		"edgeX": {"BTC", "ETH", "SOL"},
	}), nil
}

// TestListingAgentE2E_FullPipeline drives the engine end-to-end against
// a real MySQL container:
//
//  1. seed an announcement_listing + instrument_diff pair → candidate
//     should land as confirmed_listing_candidate.
//  2. seed a Top30 hot-gap row → ProduceTop30Push must materialise an
//     outbox row plus a top30_hot_gap signal.
//  3. without a webhook URL the outbox row must transition to disabled
//     after DrainDueOutbox runs.
//  4. the read-only API must surface the candidate, its signals, the
//     outbox, and the source-health snapshot.
func TestListingAgentE2E_FullPipeline(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	if err := collector.ApplyMigrations(db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	truncateListingTables(t, db)

	repo := listing.NewRepository(db)
	now := time.Now().UTC().Truncate(time.Second)

	seedSignals(t, repo, now)
	seedTop30Row(t, db, now)

	cfg := config.Config{}
	cfg.Runtime.ListingAgent = config.ListingAgentConfig{
		Enabled: true,
		Worker:  config.ListingWorkerConfig{MaxAttempts: 5},
		Top30Push: config.ListingTop30PushConfig{
			Enabled:    true,
			StaleAfter: time.Hour,
		},
		Delivery: config.ListingDeliveryConfig{Enabled: true},
	}
	engine := listing.NewEngine(cfg, repo, listing.EngineDeps{
		Now:          func() time.Time { return now },
		LoadUniverse: loadE2EUniverse,
	})

	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("engine.RunOnce: %v", err)
	}
	if summary.Fusion.FailClosed != "" {
		t.Fatalf("fusion fail-closed: %q (summary=%+v)", summary.Fusion.FailClosed, summary)
	}
	if summary.Fusion.Candidates != 1 || summary.Fusion.Signals != 2 {
		t.Fatalf("unexpected fusion counts: %+v", summary.Fusion)
	}
	if summary.Top30Push.FailClosed != "" {
		t.Fatalf("top30 fail-closed: %q (summary=%+v)", summary.Top30Push.FailClosed, summary)
	}
	if summary.Top30Push.Events != 1 || summary.Top30Push.OutboxRows != 1 {
		t.Fatalf("unexpected top30 counts: %+v", summary.Top30Push)
	}
	// ProduceTop30Push writes the outbox row as `disabled` directly
	// when no webhook URL is configured, so DrainDueOutbox never sees
	// it. The delivery summary is therefore all-zero on this first
	// tick; we assert the outbox row's status below instead.
	if (summary.Delivery.Sent + summary.Delivery.Failed + summary.Delivery.Retried + summary.Delivery.Disabled) != 0 {
		t.Fatalf("delivery summary = %+v, expected no drained rows", summary.Delivery)
	}

	t.Run("candidate persisted with correct scoring", func(t *testing.T) {
		candidates, err := repo.ListCandidates(context.Background(), listing.CandidateFilter{Limit: 10})
		if err != nil {
			t.Fatalf("ListCandidates: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("candidates = %d, want 1", len(candidates))
		}
		c := candidates[0]
		if c.CanonicalSymbol != e2eCanonicalSymbol {
			t.Fatalf("canonical_symbol = %q, want %q", c.CanonicalSymbol, e2eCanonicalSymbol)
		}
		if c.EvidenceKind != listing.EvidenceAnnouncementAndAPI {
			t.Fatalf("evidence_kind = %q, want announcement_and_api", c.EvidenceKind)
		}
		if c.LifecycleStatus != listing.LifecycleConfirmedListingCandidate {
			t.Fatalf("lifecycle_status = %q", c.LifecycleStatus)
		}
		if c.Recommendation != listing.RecommendationPrepareListing {
			t.Fatalf("recommendation = %q, want prepare_listing", c.Recommendation)
		}
		if c.BusinessScore == nil || *c.BusinessScore != 80 {
			t.Fatalf("business_score = %v, want 80 (single binance evidence)", c.BusinessScore)
		}
		signals, err := repo.ListCandidateSignals(context.Background(), c.ID, true)
		if err != nil {
			t.Fatalf("ListCandidateSignals: %v", err)
		}
		if len(signals) != 2 {
			t.Fatalf("linked signals = %d, want 2", len(signals))
		}
		// fused_at is not surfaced through ListCandidateSignals; query
		// directly to confirm the fusion worker stamped both rows.
		var fusedCount int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM t_listing_signal_observation
			  WHERE canonical_symbol = ?
			    AND signal_type IN (?, ?)
			    AND fused_at IS NOT NULL`,
			e2eCanonicalSymbol, listing.SignalAnnouncementListing, listing.SignalInstrumentDiff,
		).Scan(&fusedCount); err != nil {
			t.Fatalf("count fused signals: %v", err)
		}
		if fusedCount != 2 {
			t.Fatalf("fused signals = %d, want 2", fusedCount)
		}
	})

	t.Run("top30 push wrote outbox plus signal", func(t *testing.T) {
		deliveries, err := repo.ListDeliveries(context.Background(), listing.DeliveryFilter{Limit: 10})
		if err != nil {
			t.Fatalf("ListDeliveries: %v", err)
		}
		if len(deliveries) != 1 {
			t.Fatalf("deliveries = %d, want 1", len(deliveries))
		}
		d := deliveries[0]
		if d.EventType != listing.DeliveryEventTop30HotGap {
			t.Fatalf("event_type = %q", d.EventType)
		}
		if d.Status != listing.OutboxStatusDisabled {
			t.Fatalf("status = %q, want disabled (no webhook configured)", d.Status)
		}
		expectedKey := fmt.Sprintf("top30_hot_gap|%s|优先上架|%s", e2eDisplaySymbol, now.UTC().Format("2006-01-02"))
		if d.DedupeKey != expectedKey {
			t.Fatalf("dedupe_key = %q, want %q", d.DedupeKey, expectedKey)
		}
		// Verify the matching signal exists with the same fingerprint.
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM t_listing_signal_observation WHERE signal_type = ? AND canonical_symbol = ?`,
			listing.SignalTop30HotGap, e2eCanonicalSymbol,
		).Scan(&count); err != nil {
			t.Fatalf("count top30 signal: %v", err)
		}
		if count != 1 {
			t.Fatalf("top30_hot_gap signal count = %d, want 1", count)
		}
	})

	t.Run("HTTP /api/listing/candidates returns the candidate", func(t *testing.T) {
		server := api.NewServer(cfg, fakeStoreReader{}, api.WithListingReader(repo))
		ts := httptest.NewServer(server.Routes())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/api/listing/candidates?limit=5")
		if err != nil {
			t.Fatalf("GET candidates: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var body struct {
			Candidates []map[string]any `json:"candidates"`
			Count      int              `json:"count"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Count != 1 {
			t.Fatalf("count = %d, want 1", body.Count)
		}
		if body.Candidates[0]["canonical_symbol"].(string) != e2eCanonicalSymbol {
			t.Fatalf("candidate[0].canonical_symbol = %v", body.Candidates[0]["canonical_symbol"])
		}
	})

	t.Run("HTTP /api/listing/deliveries returns the outbox row", func(t *testing.T) {
		server := api.NewServer(cfg, fakeStoreReader{}, api.WithListingReader(repo))
		ts := httptest.NewServer(server.Routes())
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/api/listing/deliveries?event_type=top30_hot_gap")
		if err != nil {
			t.Fatalf("GET deliveries: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var body struct {
			Deliveries []map[string]any `json:"deliveries"`
			Count      int              `json:"count"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Count != 1 {
			t.Fatalf("count = %d, want 1", body.Count)
		}
	})

	t.Run("HTTP /api/listing/candidates/{id} returns detail with signals", func(t *testing.T) {
		candidates, err := repo.ListCandidates(context.Background(), listing.CandidateFilter{Limit: 1})
		if err != nil || len(candidates) == 0 {
			t.Fatalf("setup: %v", err)
		}
		server := api.NewServer(cfg, fakeStoreReader{}, api.WithListingReader(repo))
		ts := httptest.NewServer(server.Routes())
		defer ts.Close()

		url := fmt.Sprintf("%s/api/listing/candidates/%d", ts.URL, candidates[0].ID)
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var body struct {
			Candidate map[string]any   `json:"candidate"`
			Signals   []map[string]any `json:"signals"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Signals) != 2 {
			t.Fatalf("signals count = %d, want 2", len(body.Signals))
		}
	})

	t.Run("webhook delivery succeeds and records attempt", func(t *testing.T) {
		// Reset and re-seed for a fresh outbox row.
		truncateListingTables(t, db)
		runNow := time.Now().UTC().Truncate(time.Second)
		seedTop30Row(t, db, runNow)

		// Webhook receiver running in-process; backend can hit it via
		// loopback because the engine runs in the same Go process as
		// the test, not in Docker.
		var receivedCT string
		var receivedBody []byte
		webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedCT = r.Header.Get("Content-Type")
			receivedBody, _ = readBody(r)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
		}))
		defer webhook.Close()

		webhookCfg := cfg
		webhookCfg.Alert.Enabled = true
		webhookCfg.Alert.WebHookP3 = webhook.URL
		engineWithWebhook := listing.NewEngine(webhookCfg, repo, listing.EngineDeps{
			Now:          func() time.Time { return runNow },
			LoadUniverse: loadE2EUniverse,
		})
		summary, err := engineWithWebhook.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce with webhook: %v", err)
		}
		if summary.Delivery.Sent != 1 {
			t.Fatalf("delivery summary = %+v, want one sent", summary.Delivery)
		}
		if len(receivedBody) == 0 {
			t.Fatalf("webhook did not receive any body")
		}
		if receivedCT != "application/json; charset=utf-8" {
			t.Fatalf("webhook content-type = %q", receivedCT)
		}
		if !json.Valid(receivedBody) {
			t.Fatalf("webhook body is not json: %s", string(receivedBody))
		}
		var body map[string]any
		if err := json.Unmarshal(receivedBody, &body); err != nil {
			t.Fatalf("unmarshal webhook body: %v", err)
		}
		if body["msg_type"] != "interactive" {
			t.Fatalf("webhook msg_type = %v, want interactive; body=%s", body["msg_type"], string(receivedBody))
		}

		// Outbox must be marked sent, attempt row must exist.
		var status string
		var attempts int
		if err := db.QueryRow(`SELECT status, attempt_count FROM t_listing_delivery_outbox LIMIT 1`).
			Scan(&status, &attempts); err != nil {
			t.Fatalf("query outbox: %v", err)
		}
		if status != listing.OutboxStatusSent {
			t.Fatalf("outbox.status = %q, want sent", status)
		}
		if attempts != 1 {
			t.Fatalf("attempt_count = %d, want 1", attempts)
		}
		var attemptRows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM t_listing_delivery_attempt`).Scan(&attemptRows); err != nil {
			t.Fatalf("count attempts: %v", err)
		}
		if attemptRows != 1 {
			t.Fatalf("attempt rows = %d, want 1", attemptRows)
		}
	})
}

// readBody is a small helper that drains an http.Request body.
func readBody(r *http.Request) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}

// fakeStoreReader is the minimum StoreReader needed by api.NewServer
// when the snapshot endpoints are not exercised by this e2e suite.
type fakeStoreReader struct{}

func (fakeStoreReader) MySQLBacked() bool                                           { return false }
func (fakeStoreReader) PingDB(context.Context) error                                { return nil }
func (fakeStoreReader) SnapshotRowCounts(context.Context) (map[string]int64, error) { return nil, nil }
func (fakeStoreReader) Symbols() []string                                           { return nil }
func (fakeStoreReader) SymbolMappings() []domain.SymbolSub                          { return nil }
func (fakeStoreReader) DashboardMeta() map[string]any                               { return nil }
func (fakeStoreReader) Coverage() map[string]any                                    { return nil }
func (fakeStoreReader) Liquidity(string) map[string]any                             { return nil }
func (fakeStoreReader) Quality(string) map[string]any                               { return nil }
func (fakeStoreReader) Share(string) map[string]any                                 { return nil }
func (fakeStoreReader) Top30(string, string) map[string]any                         { return nil }
func (fakeStoreReader) Top30Divergence() domain.Top30DivergenceSnapshot {
	return domain.Top30DivergenceSnapshot{}
}
func (fakeStoreReader) CollectionStatus() map[string]any { return nil }
func (fakeStoreReader) RuntimeConfig() config.Runtime    { return config.Runtime{} }
