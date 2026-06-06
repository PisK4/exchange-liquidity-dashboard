//go:build activity_e2e
// +build activity_e2e

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-dashboard/backend/internal/activity"
	"edgex-dashboard/backend/internal/collector"
)

func TestActivityEngineProducesAndDeliversOutbox(t *testing.T) {
	dsn := os.Getenv("ACTIVITY_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("ACTIVITY_E2E_MYSQL_DSN not set; run via backend/e2e/run-activity-e2e.sh")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()
	if err := collector.ApplyMigrations(db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	truncateActivityTables(t, db)
	seedActivityEvent(t, db, 1, "binance", "cms_article_detail", "Binance Launchpool ABC", false, true)
	seedActivityEvent(t, db, 2, "mexc", "latest_events", "MEXC M-Day Futures", true, false)

	posted := 0
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode webhook body: %v", err)
		}
		if body["msg_type"] != "interactive" {
			t.Fatalf("webhook body=%+v", body)
		}
		posted++
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer webhook.Close()

	repo := activity.NewRepository(db)
	engine := activity.NewEngine(repo, activity.EngineConfig{
		Enabled:             true,
		OwnerID:             "activity-e2e",
		WorkerLeaseTTL:      time.Minute,
		WebhookURL:          webhook.URL,
		DecisionTokenSecret: "activity-e2e-secret",
		MaxPerTick:          10,
	})
	summary, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !summary.LeaseAcquired || summary.Producer.OutboxRows != 2 || summary.Delivery.Sent != 2 || posted != 2 {
		t.Fatalf("summary=%+v posted=%d", summary, posted)
	}
	var sent, attempts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t_activity_delivery_outbox WHERE status='sent'`).Scan(&sent); err != nil {
		t.Fatalf("count sent: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM t_activity_delivery_attempt WHERE status='sent'`).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if sent != 2 || attempts != 2 {
		t.Fatalf("sent=%d attempts=%d", sent, attempts)
	}
}

func truncateActivityTables(t *testing.T, db *sql.DB) {
	t.Helper()
	tables := []string{
		"t_activity_delivery_attempt",
		"t_activity_delivery_outbox",
		"t_activity_review_item",
		"t_activity_digest_item",
		"t_activity_digest",
		"t_activity_event_symbol",
		"t_activity_event",
		"t_activity_raw_evidence",
		"t_activity_source_state",
		"t_activity_worker_lease",
	}
	for _, table := range tables {
		if _, err := db.Exec("TRUNCATE TABLE " + table); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

func seedActivityEvent(t *testing.T, db *sql.DB, id int64, platform, sourceGroup, title string, needsReview, autoPush bool) {
	t.Helper()
	now := time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC)
	_, err := db.Exec(`INSERT INTO t_activity_event
		(id, platform, source_group, source_external_id, source_url, title, activity_type,
		 target_symbols_json, content_text, content_hash, dedupe_key, confidence_score,
		 needs_human_review, auto_push_allowed, event_status, review_status, event_version,
		 parser_version, source_context_json, parser_warnings_json, reward_pools_json,
		 task_conditions_json, eligibility_rules_json, rich_fields_summary_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, JSON_ARRAY(), ?, ?, ?, ?, ?, ?, 'active', 'pending', 1,
		 'activity-parser-v1', JSON_OBJECT('fetch_mode','http_direct'), JSON_ARRAY(), JSON_ARRAY(),
		 JSON_ARRAY(), JSON_ARRAY(), JSON_OBJECT(), ?, ?)`,
		id, platform, sourceGroup, platform+"-external", "https://"+platform+".example/activity", title, "launchpool",
		title+" summary", platform+"-hash", platform+"|"+sourceGroup+"|"+title, 0.9,
		needsReview, autoPush, now, now)
	if err != nil {
		t.Fatalf("insert activity event: %v", err)
	}
}
