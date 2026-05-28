// Top30 hot-gap card preview CLI.
//
// Loads the latest t_top30_snapshot rows, materialises Top30PushEvent
// objects via the production listing.BuildTop30PushEvents helper,
// computes the per-(symbol, action) streak from t_listing_signal_observation
// (without writing today's row), renders the new interactive Lark card
// via listing.RenderTop30PostMessage, and either prints the JSON to
// stdout (--dry-run, default) or POSTs it to a Lark webhook for live
// preview in a chat group.
//
// Designed for ad-hoc operator use during the Top30 card redesign.
// Does NOT touch t_listing_delivery_outbox or t_listing_signal_observation,
// so it is safe to run alongside the live deploy-backend listing
// engine without colliding on dedupe keys.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/listing"
)

func main() {
	var (
		configDir = flag.String("config-dir", "../config", "Path to the dashboard config directory; values from edgex-liquidity-dashboard.yaml are used as defaults when --mysql-dsn / --webhook-url / --proxy / --dashboard-base are not given")
		dsn       = flag.String("mysql-dsn", "", "MySQL DSN for the dashboard DB; falls back to DASHBOARD_MYSQL_DSN env, then Database.DSN from config-dir")
		webhook   = flag.String("webhook-url", "", "Lark webhook URL; falls back to Alert.WebHookP3 from config-dir; leave both empty for dry-run (stdout only)")
		proxy     = flag.String("proxy", "", "Optional HTTP(S) proxy for the webhook POST; falls back to Runtime.listing_agent.delivery.proxy from config-dir. Note: host CLI typically needs 127.0.0.1:<port> while the in-container value is host.docker.internal:<port>")
		dashboard = flag.String("dashboard-base", "", "Dashboard base URL inserted into the '查看 Top30 详情' button; falls back to Runtime.listing_agent.delivery.dashboard_base_url from config-dir; empty hides the button")
		dryRun    = flag.Bool("dry-run", false, "Print rendered card JSON to stdout but never POST")
		limit     = flag.Int("limit", 0, "Cap the number of events rendered/posted (0 = no cap)")
	)
	flag.Parse()

	cfg, cfgErr := config.Load(*configDir)
	if cfgErr != nil {
		log.Printf("config load %q failed (continuing with flag/env values only): %v", *configDir, cfgErr)
	}
	resolveFromConfig(cfgErr == nil, &cfg, dsn, webhook, proxy, dashboard)

	if strings.TrimSpace(*dsn) == "" {
		log.Fatal("missing MySQL DSN: pass --mysql-dsn, set DASHBOARD_MYSQL_DSN, or fill Database in config-dir")
	}
	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping mysql: %v", err)
	}

	ctx := context.Background()
	rows, latest, err := loadLatestRows(ctx, db)
	if err != nil {
		log.Fatalf("load top30 rows: %v", err)
	}
	if latest.IsZero() {
		log.Fatal("no rows in t_top30_snapshot")
	}
	log.Printf("latest snapshot ts=%s rows=%d", latest.UTC().Format(time.RFC3339), len(rows))

	events := listing.BuildTop30PushEvents(rows, latest)
	log.Printf("built %d eligible events", len(events))
	if *limit > 0 && len(events) > *limit {
		events = events[:*limit]
		log.Printf("--limit=%d applied; rendering %d events", *limit, len(events))
	}

	client := buildHTTPClient(*proxy)
	now := time.Now().UTC()
	posted := 0
	for i := range events {
		ev := &events[i]
		ev.TriggerTime = now
		if base := strings.TrimSpace(*dashboard); base != "" {
			ev.DashboardURL = appendSymbolQuery(base, ev.Symbol)
		}
		streak, err := loadPriorStreak(ctx, db, ev.Symbol, ev.Action, now)
		if err != nil {
			log.Printf("streak query for %q (%s) failed: %v (defaulting to 0)", ev.Symbol, ev.Action, err)
			streak = 0
		}
		ev.StreakDays = streak + 1

		body, err := listing.RenderTop30PostMessage(*ev)
		if err != nil {
			log.Printf("render %s: %v", ev.Symbol, err)
			continue
		}
		var pretty bytes.Buffer
		_ = json.Indent(&pretty, body, "", "  ")
		fmt.Printf("\n=== event[%d] %s · %s · streak=%d (prior=%d) · cov=%d ===\n",
			i, ev.Symbol, ev.Action, ev.StreakDays, streak, ev.MaxCoverage)
		fmt.Println(pretty.String())

		if *dryRun || strings.TrimSpace(*webhook) == "" {
			continue
		}
		if err := postWebhook(ctx, client, *webhook, body); err != nil {
			log.Printf("POST %s failed: %v", ev.Symbol, err)
			continue
		}
		posted++
		log.Printf("posted %s · %s to webhook", ev.Symbol, ev.Action)
	}
	log.Printf("done: rendered=%d posted=%d", len(events), posted)
}

// resolveFromConfig fills empty CLI flag values from config + env. Precedence:
//  1. Explicit flag value (non-empty after TrimSpace).
//  2. Environment variable (DASHBOARD_MYSQL_DSN for DSN; nothing for the others).
//  3. Loaded YAML config (only when configOK).
//
// Webhook: matches the production resolver (Alert.WebHookP3 first, then
// Runtime.listing_agent.delivery.top30_webhook_url, then *_url_env).
//
// Proxy: the YAML value typically points at host.docker.internal:<port>,
// which only resolves inside Docker. From the host shell that the
// preview CLI runs in, we transparently rewrite host.docker.internal →
// 127.0.0.1 so the same config works for both runtimes.
func resolveFromConfig(configOK bool, cfg *config.Config, dsn, webhook, proxy, dashboard *string) {
	if strings.TrimSpace(*dsn) == "" {
		if env := strings.TrimSpace(os.Getenv("DASHBOARD_MYSQL_DSN")); env != "" {
			*dsn = env
		} else if configOK && strings.TrimSpace(cfg.Database.DSN) != "" {
			*dsn = cfg.Database.DSN
		}
	}
	if strings.TrimSpace(*webhook) == "" && configOK {
		switch {
		case cfg.Alert.Enabled && strings.TrimSpace(cfg.Alert.WebHookP3) != "":
			*webhook = cfg.Alert.WebHookP3
		case strings.TrimSpace(cfg.Runtime.ListingAgent.Delivery.Top30WebhookURL) != "":
			*webhook = cfg.Runtime.ListingAgent.Delivery.Top30WebhookURL
		case strings.TrimSpace(cfg.Runtime.ListingAgent.Delivery.Top30WebhookURLEnv) != "":
			if v := strings.TrimSpace(os.Getenv(cfg.Runtime.ListingAgent.Delivery.Top30WebhookURLEnv)); v != "" {
				*webhook = v
			}
		}
	}
	if strings.TrimSpace(*proxy) == "" && configOK {
		*proxy = rewriteHostInternalForLocalShell(cfg.Runtime.ListingAgent.Delivery.Proxy)
	}
	if strings.TrimSpace(*dashboard) == "" && configOK {
		*dashboard = cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL
	}
}

// rewriteHostInternalForLocalShell turns Docker-only hostnames into
// 127.0.0.1 so the preview CLI can reuse the production config value
// without operator overrides. Returns the original string if it does
// not match any rewritten host.
func rewriteHostInternalForLocalShell(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	host := parsed.Hostname()
	if host != "host.docker.internal" {
		return raw
	}
	port := parsed.Port()
	if port == "" {
		parsed.Host = "127.0.0.1"
	} else {
		parsed.Host = "127.0.0.1:" + port
	}
	return parsed.String()
}

func loadLatestRows(ctx context.Context, db *sql.DB) ([]listing.Top30RowForPush, time.Time, error) {
	var latest sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT MAX(snapshot_ts) FROM t_top30_snapshot`).Scan(&latest); err != nil {
		return nil, time.Time{}, fmt.Errorf("MAX(snapshot_ts): %w", err)
	}
	if !latest.Valid {
		return nil, time.Time{}, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT platform, symbol, rank_no, COALESCE(volume_24h_usd,0),
		         COALESCE(coverage_count,0), edgex_listed, suggested_action, snapshot_ts
		    FROM t_top30_snapshot
		   WHERE snapshot_ts = ?`, latest.Time)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var out []listing.Top30RowForPush
	for rows.Next() {
		var r listing.Top30RowForPush
		var listed sql.NullBool
		var action sql.NullString
		var ts time.Time
		if err := rows.Scan(&r.Platform, &r.Symbol, &r.Rank, &r.Volume24HUSD,
			&r.CoverageCount, &listed, &action, &ts); err != nil {
			return nil, time.Time{}, err
		}
		if listed.Valid {
			v := listed.Bool
			r.EdgexListed = &v
		}
		r.SuggestedAction = action.String
		r.SnapshotTS = ts
		out = append(out, r)
	}
	return out, latest.Time, rows.Err()
}

// loadPriorStreak counts consecutive UTC days strictly before today on
// which a top30_hot_gap signal was already emitted for the same
// (display_symbol, action). Mirrors listing.countTop30Streak so the
// preview renders the same badge value that the production worker
// would compute on the next tick.
func loadPriorStreak(ctx context.Context, db *sql.DB, displaySymbol, action string, today time.Time) (int, error) {
	todayStr := today.UTC().Format("2006-01-02")
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT DATE(observed_at) AS d
		   FROM t_listing_signal_observation
		  WHERE signal_type    = 'top30_hot_gap'
		    AND signal_subtype = ?
		    AND display_symbol = ?
		    AND DATE(observed_at) < ?
		  ORDER BY d DESC
		  LIMIT 60`, action, displaySymbol, todayStr)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	streak := 0
	expected := today.UTC().AddDate(0, 0, -1)
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return 0, err
		}
		var d string
		switch v := raw.(type) {
		case time.Time:
			d = v.UTC().Format("2006-01-02")
		case []byte:
			d = string(v)
		case string:
			d = v
		default:
			return 0, fmt.Errorf("unexpected DATE type %T", raw)
		}
		if d != expected.Format("2006-01-02") {
			break
		}
		streak++
		expected = expected.AddDate(0, 0, -1)
	}
	return streak, rows.Err()
}

func appendSymbolQuery(base, symbol string) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "symbol=" + url.QueryEscape(symbol)
}

func buildHTTPClient(proxyURL string) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return &http.Client{Timeout: 10 * time.Second}
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		log.Printf("ignoring invalid proxy %q: %v", proxyURL, err)
		return &http.Client{Timeout: 10 * time.Second}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(parsed)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func postWebhook(ctx context.Context, client *http.Client, webhookURL string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook non-2xx: %d body=%s", resp.StatusCode, string(respBody))
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err == nil {
		if code, ok := parsed["code"]; ok && fmt.Sprintf("%v", code) != "0" {
			return errors.New("webhook returned non-zero code: " + string(respBody))
		}
	}
	return nil
}
