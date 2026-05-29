// Top30 CEX/DEX divergence card preview CLI (#2-#5).
//
// Loads the latest t_top30_snapshot rows, materialises the four
// divergence DivergencePushEvent objects via the production
// listing.BuildDivergencePushEvents helper, renders the interactive
// Lark card via listing.RenderDivergencePostMessage, and either prints
// the JSON to stdout (--dry-run, default) or POSTs it to a Lark
// webhook for live preview in a chat group.
//
// Designed for ad-hoc operator use during the divergence card rollout.
// Does NOT touch t_listing_delivery_outbox or
// t_listing_signal_observation so it is safe to run alongside the live
// deploy-backend listing engine without colliding on dedupe keys.
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
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/listing"
)

func main() {
	var (
		configDir = flag.String("config-dir", "../config", "Path to the dashboard config directory; values from edgex-liquidity-dashboard.yaml are used as defaults when --mysql-dsn / --webhook-url / --proxy / --dashboard-base are not given")
		dsn       = flag.String("mysql-dsn", "", "MySQL DSN for the dashboard DB; falls back to DASHBOARD_MYSQL_DSN env, then Database.DSN from config-dir")
		webhook   = flag.String("webhook-url", "", "Lark webhook URL; falls back to Alert.WebHookP3 from config-dir; leave both empty for dry-run (stdout only)")
		proxy     = flag.String("proxy", "", "Optional HTTP(S) proxy for the webhook POST; falls back to Runtime.listing_agent.delivery.proxy from config-dir")
		dashboard = flag.String("dashboard-base", "", "Dashboard base URL inserted into the '查看 Top30 详情' button; falls back to Runtime.listing_agent.delivery.dashboard_base_url from config-dir; empty hides the button")
		topN      = flag.Int("top-n", 10, "Max rows per card (default 10)")
		dryRun    = flag.Bool("dry-run", false, "Print rendered card JSON to stdout but never POST")
		only      = flag.String("only", "", "Optional comma-separated category filter (cex_only,dex_only,heavy_gap,both_hot_gap)")
		fixture   = flag.String("fixture", "", "Comma-separated categories to inject synthetic events for (heavy_gap,both_hot_gap); useful when real data does not naturally trigger those cards. Real events from the snapshot are still emitted unless --only filters them out.")
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

	now := time.Now().UTC()
	events := listing.BuildDivergencePushEvents(rows, cfg.Runtime.Top30Divergence, cfg.CanonicalIndex, *topN, now)
	log.Printf("built %d eligible divergence events", len(events))
	if fix := strings.TrimSpace(*fixture); fix != "" {
		injected := buildFixtureEvents(fix, now)
		events = append(events, injected...)
		log.Printf("--fixture=%q applied; injected %d synthetic event(s)", fix, len(injected))
	}
	events = filterCategories(events, *only)
	if onlyTrim := strings.TrimSpace(*only); onlyTrim != "" {
		log.Printf("--only=%q applied; rendering %d events", onlyTrim, len(events))
	}

	client := buildHTTPClient(*proxy)
	posted := 0
	for i := range events {
		ev := &events[i]
		ev.TriggerTime = now
		if base := strings.TrimSpace(*dashboard); base != "" {
			ev.DashboardURL = base
		}

		body, err := listing.RenderDivergencePostMessage(*ev)
		if err != nil {
			log.Printf("render %s: %v", ev.Category, err)
			continue
		}
		var pretty bytes.Buffer
		_ = json.Indent(&pretty, body, "", "  ")
		fmt.Printf("\n=== event[%d] %s · rows=%d ===\n", i, ev.Category, len(ev.Rows))
		fmt.Println(pretty.String())

		if *dryRun || strings.TrimSpace(*webhook) == "" {
			continue
		}
		if err := postWebhook(ctx, client, *webhook, body); err != nil {
			log.Printf("POST %s failed: %v", ev.Category, err)
			continue
		}
		posted++
		log.Printf("posted %s to webhook", ev.Category)
	}
	log.Printf("done: rendered=%d posted=%d", len(events), posted)
}

// resolveFromConfig fills empty CLI flag values from config + env.
// Precedence mirrors top30-preview/main.go so the two tools accept the
// same operator workflow.
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
// without operator overrides.
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

// buildFixtureEvents returns synthetic DivergencePushEvent values for
// the categories listed in `spec` (comma-separated). It is used during
// card-visual smoke tests for #4 heavy_gap and #5 both_hot_gap when
// the production snapshot does not naturally produce those categories
// (e.g. after alias resolution + three-state filtering, no cross-camp
// canonical satisfies edgex_listed=false). The synthesised rows use
// plausible symbol names and rank/volume ranges so the card looks
// representative; downstream rendering is identical to production.
//
// dedupe_key uses a `_fixture` suffix so anyone who copies the rendered
// card JSON into the outbox by mistake can spot the synthetic origin.
func buildFixtureEvents(spec string, triggerTime time.Time) []listing.DivergencePushEvent {
	day := triggerTime.UTC().Format("2006-01-02")
	wantHeavy := false
	wantBothHot := false
	for _, raw := range strings.Split(spec, ",") {
		switch strings.TrimSpace(strings.ToLower(raw)) {
		case listing.DivergenceCategoryHeavyGap, "heavy":
			wantHeavy = true
		case listing.DivergenceCategoryBothHotGap, "both", "both_hot":
			wantBothHot = true
		}
	}

	ip := func(v int) *int { return &v }
	fp := func(v float64) *float64 { return &v }

	kpi := domain.Top30DivergenceKPI{
		CEXOnlyCount:  5,
		DEXOnlyCount:  8,
		HeavyCount:    6,
		AlignedCount:  4,
		EdgexGapCount: 14,
	}

	var out []listing.DivergencePushEvent
	if wantHeavy {
		out = append(out, listing.DivergencePushEvent{
			Category:      listing.DivergenceCategoryHeavyGap,
			CategoryLabel: "CEX vs DEX 显著分歧 · edgeX 未上线",
			KPI:           kpi,
			SnapshotTS:    triggerTime,
			SnapshotDate:  day,
			DedupeKey:     fmt.Sprintf("top30_divergence|%s|%s_fixture", listing.DivergenceCategoryHeavyGap, day),
			TriggerTime:   triggerTime,
			Rows: []listing.DivergencePushRow{
				{Symbol: "ASTER", CEXRank: ip(5), DEXRank: ip(28), RankDelta: ip(23), CEXVolUSD: fp(1.42e9), DEXVolUSD: fp(38.4e6), CEXPlatforms: 6, DEXPlatforms: 1},
				{Symbol: "TON", CEXRank: ip(20), DEXRank: ip(4), RankDelta: ip(16), CEXVolUSD: fp(412e6), DEXVolUSD: fp(540e6), CEXPlatforms: 5, DEXPlatforms: 2},
				{Symbol: "SHIB", CEXRank: ip(8), DEXRank: ip(22), RankDelta: ip(14), CEXVolUSD: fp(880e6), DEXVolUSD: fp(72.1e6), CEXPlatforms: 7, DEXPlatforms: 1},
				{Symbol: "LDO", CEXRank: ip(25), DEXRank: ip(11), RankDelta: ip(14), CEXVolUSD: fp(266e6), DEXVolUSD: fp(198e6), CEXPlatforms: 4, DEXPlatforms: 2},
				{Symbol: "WIF", CEXRank: ip(15), DEXRank: ip(3), RankDelta: ip(12), CEXVolUSD: fp(623e6), DEXVolUSD: fp(710e6), CEXPlatforms: 5, DEXPlatforms: 2},
			},
		})
	}
	if wantBothHot {
		out = append(out, listing.DivergencePushEvent{
			Category:      listing.DivergenceCategoryBothHotGap,
			CategoryLabel: "两阵营均热 · edgeX 未上线",
			KPI:           kpi,
			SnapshotTS:    triggerTime,
			SnapshotDate:  day,
			DedupeKey:     fmt.Sprintf("top30_divergence|%s|%s_fixture", listing.DivergenceCategoryBothHotGap, day),
			TriggerTime:   triggerTime,
			Rows: []listing.DivergencePushRow{
				{Symbol: "HYPE", CEXRank: ip(18), DEXRank: ip(1), RankDelta: ip(17), CEXVolUSD: fp(310e6), DEXVolUSD: fp(1.08e9), CEXPlatforms: 4, DEXPlatforms: 2},
				{Symbol: "WIF", CEXRank: ip(15), DEXRank: ip(3), RankDelta: ip(12), CEXVolUSD: fp(623e6), DEXVolUSD: fp(710e6), CEXPlatforms: 5, DEXPlatforms: 2},
				{Symbol: "ASTER", CEXRank: ip(5), DEXRank: ip(6), RankDelta: ip(1), CEXVolUSD: fp(1.42e9), DEXVolUSD: fp(412e6), CEXPlatforms: 6, DEXPlatforms: 1},
				{Symbol: "TIA", CEXRank: ip(11), DEXRank: ip(9), RankDelta: ip(2), CEXVolUSD: fp(720e6), DEXVolUSD: fp(184e6), CEXPlatforms: 5, DEXPlatforms: 2},
				{Symbol: "DYDX", CEXRank: ip(20), DEXRank: ip(15), RankDelta: ip(5), CEXVolUSD: fp(258e6), DEXVolUSD: fp(98e6), CEXPlatforms: 3, DEXPlatforms: 2},
			},
		})
	}
	return out
}

func filterCategories(events []listing.DivergencePushEvent, only string) []listing.DivergencePushEvent {
	only = strings.TrimSpace(only)
	if only == "" {
		return events
	}
	keep := map[string]struct{}{}
	for _, raw := range strings.Split(only, ",") {
		k := strings.TrimSpace(raw)
		if k == "" {
			continue
		}
		keep[k] = struct{}{}
	}
	if len(keep) == 0 {
		return events
	}
	out := make([]listing.DivergencePushEvent, 0, len(events))
	for _, ev := range events {
		if _, ok := keep[ev.Category]; ok {
			out = append(out, ev)
		}
	}
	return out
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
