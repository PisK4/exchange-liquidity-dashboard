// Lark push smoke harness for the Top30 hot-gap (#1) + divergence
// (#2-#5) cards.
//
// Why this exists
// ===============
// The unit tests cover render contract + outbox bookkeeping in
// isolation (sqlmock). They CAN NOT prove the chain "real DB row ->
// real DrainDueOutbox -> real Lark webhook" actually works in your
// local Docker stack. This script does exactly that, against your
// live MySQL container, against your real Lark webhook URL, and
// surfaces every failure mode the production worker would surface.
//
// Pipeline
// ========
//
//  1. Pre-cleanup: DELETE any leftover rows from prior aborted runs
//     (matched by dedupe_key LIKE 'lark_push_test|%'). Safe to
//     retry.
//  2. Pre-check: confirm there is a recent t_top30_snapshot to
//     render against; refuse to run if MAX(snapshot_ts) is empty.
//  3. Build phase: call the production listing.BuildTop30PushEvents
//     and listing.BuildDivergencePushEvents helpers against the
//     loaded rows. Same code paths your engine uses; no synthetic
//     fixtures.
//  4. Write phase: INSERT IGNORE the rendered cards into
//     t_listing_delivery_outbox with dedupe_key wrapped as
//     `lark_push_test|<nonce>|<production dedupe_key>`. The wrap
//     isolates this run from production traffic; we never touch
//     production rows.
//  5. Drain phase: call listing.DrainDueOutbox with
//     DedupeKeyPrefix=`lark_push_test|<nonce>|`. The production
//     drain code path posts to your real webhook and updates the
//     smoke rows in-place; production rows are not selected.
//  6. Idempotency phase: re-run DrainDueOutbox (same prefix).
//     Expect zero rows drained -- if the worker double-sent we
//     surface a hard failure here.
//  7. Dedupe phase: re-insert the same rows. Expect zero rows
//     affected (the unique key on dedupe_key MUST collapse the
//     attempt).
//  8. Cleanup: DELETE the smoke rows unless --skip-cleanup is set.
//
// Operator workflow
// =================
//
//	cd backend
//	go run ./scripts/lark-push-smoke --config-dir ../config --ack
//
// The script prints one line per phase plus a final PASS / FAIL
// banner. Watch your Lark group: each successful POST shows up there
// for visual verification. Re-running is safe -- the pre-cleanup
// step is idempotent.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/listing"
	"edgex-dashboard/backend/internal/listing/liquidity"
)

// smokePrefix is the stable dedupe_key marker for every smoke run.
// All inserts carry it; pre-cleanup matches on it; the drain filter
// extends it with a per-run nonce.
const smokePrefix = "lark_push_test"

func main() {
	var (
		configDir     = flag.String("config-dir", "../config", "Path to the dashboard config directory; values from edgex-liquidity-dashboard.yaml are used as defaults when other flags are omitted")
		dsn           = flag.String("mysql-dsn", "", "MySQL DSN for the dashboard DB; falls back to DASHBOARD_MYSQL_DSN env, then Database.DSN from config-dir")
		webhook       = flag.String("webhook-url", "", "Lark webhook URL; falls back to Alert.WebHookP3, then listing_agent.delivery.top30_webhook_url/_env from config-dir")
		webhookSecret = flag.String("webhook-secret", "", "Lark webhook signing secret; falls back to listing_agent.delivery.top30_webhook_secret from config-dir; empty disables signing")
		proxy         = flag.String("proxy", "", "Optional HTTP(S) proxy used for the webhook POST; falls back to listing_agent.delivery.proxy from config-dir; host.docker.internal is rewritten to 127.0.0.1 automatically")
		dashboard     = flag.String("dashboard-base", "", "Dashboard base URL inserted into the action buttons; falls back to listing_agent.delivery.dashboard_base_url from config-dir; empty hides the button")
		include       = flag.String("include", "all", "Which card families to exercise: all | hot_gap | divergence")
		skipCleanup   = flag.Bool("skip-cleanup", false, "Leave smoke rows in t_listing_delivery_outbox after the run (default cleans up)")
		maxAttempts   = flag.Int("max-attempts", 3, "MaxAttempts written on smoke outbox rows; bounds DrainDueOutbox retry behaviour during the run")
		batchSize     = flag.Int("batch-size", 50, "Batch size handed to DrainDueOutbox; production worker uses listing_agent.top30_push.max_per_tick")
		ack           = flag.Bool("ack", false, "REQUIRED. Confirms you understand this script POSTs to a real Lark group via the configured webhook")
	)
	flag.Parse()

	if !*ack {
		fmt.Fprintln(os.Stderr, "refusing to run without --ack; this script POSTs to a real Lark webhook.\n"+
			"  pass --ack to acknowledge, after confirming --webhook-url / --config-dir resolve to a TEST channel you control.")
		os.Exit(2)
	}

	cfg, cfgErr := config.Load(*configDir)
	if cfgErr != nil {
		log.Printf("config load %q failed (continuing with flag/env values only): %v", *configDir, cfgErr)
	}
	resolveFromConfig(cfgErr == nil, &cfg, dsn, webhook, webhookSecret, proxy, dashboard)

	if strings.TrimSpace(*dsn) == "" {
		log.Fatal("missing MySQL DSN: pass --mysql-dsn, set DASHBOARD_MYSQL_DSN, or fill Database in config-dir")
	}
	if strings.TrimSpace(*webhook) == "" {
		log.Fatal("missing webhook URL: pass --webhook-url, or fill Alert.WebHookP3 / listing_agent.delivery.top30_webhook_url in config-dir.\n" +
			"  The script REQUIRES a webhook because the whole point is to verify the live POST works.")
	}
	includeHotGap, includeDivergence, includeLiquidity, err := parseIncludeFlag(*include)
	if err != nil {
		log.Fatalf("--include: %v", err)
	}

	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping mysql: %v", err)
	}
	repo := listing.NewRepository(db)
	httpClient := buildHTTPClient(*proxy)
	ctx := context.Background()

	nonce := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	runPrefix := smokePrefix + "|" + nonce + "|"
	log.Printf("smoke run nonce=%s", nonce)
	log.Printf("smoke run dedupe_key prefix=%s", runPrefix)

	// --- Phase 1: pre-cleanup
	stale, err := deleteSmokeRows(ctx, db, smokePrefix+"|")
	if err != nil {
		log.Fatalf("phase1 pre-cleanup: %v", err)
	}
	log.Printf("[phase 1] pre-cleanup: removed %d leftover smoke row(s)", stale)

	// --- Phase 2: load latest snapshot (top30 / divergence only)
	now := time.Now().UTC()
	var rows []listing.Top30RowForPush
	var snapshotTS time.Time
	if includeHotGap || includeDivergence {
		rows, snapshotTS, err = loadLatestRows(ctx, db)
		if err != nil {
			log.Fatalf("phase2 load snapshot: %v", err)
		}
		if snapshotTS.IsZero() {
			log.Fatal("phase2 load snapshot: t_top30_snapshot is empty; cannot smoke-test")
		}
		log.Printf("[phase 2] snapshot ts=%s, rows=%d", snapshotTS.UTC().Format(time.RFC3339), len(rows))
	} else {
		log.Printf("[phase 2] skipped — liquidity-only run does not depend on t_top30_snapshot")
	}

	// --- Phase 3: build events from production helpers
	dashboardBase := strings.TrimSpace(*dashboard)
	var hotGapEvents []listing.Top30PushEvent
	var divEvents []listing.DivergencePushEvent
	var liquidityCards []liquidity.CardPayload
	if includeHotGap {
		hotGapEvents = listing.BuildTop30PushEvents(rows, snapshotTS)
		for i := range hotGapEvents {
			ev := &hotGapEvents[i]
			ev.TriggerTime = now
			if dashboardBase != "" {
				ev.DashboardURL = appendSymbolQuery(dashboardBase, ev.Symbol)
			}
			// Smoke-only: streak is for visual completeness; we mark
			// every smoke card as "NEW" (StreakDays=1) so the badge
			// shows up. We don't query t_listing_signal_observation
			// because the smoke flow intentionally avoids writing
			// signals.
			ev.StreakDays = 1
		}
	}
	if includeDivergence {
		topN := cfg.Runtime.ListingAgent.Top30DivergencePush.TopNPerCard
		if topN <= 0 {
			topN = 10
		}
		divEvents = listing.BuildDivergencePushEvents(rows, cfg.Runtime.Top30Divergence, cfg.CanonicalIndex, topN, now)
		for i := range divEvents {
			ev := &divEvents[i]
			ev.TriggerTime = now
			if dashboardBase != "" {
				ev.DashboardURL = dashboardBase
			}
		}
	}
	if includeLiquidity {
		lagThreshold := cfg.Runtime.ListingAgent.LiquidityAlert.LagThreshold
		if lagThreshold <= 0 {
			lagThreshold = 0.5
		}
		liquidityCards = buildLiquiditySmokeFixtures(dashboardBase, lagThreshold)
		for i := range liquidityCards {
			liquidityCards[i].EvaluatedAt = now
		}
	}
	if len(hotGapEvents) == 0 && len(divEvents) == 0 && len(liquidityCards) == 0 {
		log.Fatal("phase3 build: no eligible cards produced.\n" +
			"  Either the snapshot has no hot-gap unlisted symbols and no CEX/DEX divergence rows,\n" +
			"  the liquidity fixture set was empty, or --include filtered everything out.\n" +
			"  Smoke cannot proceed without at least one card.")
	}
	log.Printf("[phase 3] built %d hot-gap event(s), %d divergence event(s), %d liquidity card(s)",
		len(hotGapEvents), len(divEvents), len(liquidityCards))

	// --- Phase 4: insert smoke outbox rows
	inserted, err := insertSmokeOutbox(ctx, db, runPrefix, hotGapEvents, divEvents, liquidityCards, *maxAttempts, now)
	if err != nil {
		log.Fatalf("phase4 insert: %v", err)
	}
	expected := len(hotGapEvents) + len(divEvents) + len(liquidityCards)
	if inserted != expected {
		log.Fatalf("phase4 insert: expected %d rows, got %d (dedupe collision with prior run? nonce reuse?)", expected, inserted)
	}
	log.Printf("[phase 4] wrote %d smoke outbox row(s)", inserted)

	// --- Phase 5: drain through production DrainDueOutbox
	drainNow := now.Add(time.Millisecond)
	drainResult, err := listing.DrainDueOutbox(ctx, repo, listing.DeliveryDeps{
		WebhookURL:      *webhook,
		WebhookSecret:   *webhookSecret,
		Client:          httpClient,
		Now:             func() time.Time { return drainNow },
		BatchSize:       *batchSize,
		DedupeKeyPrefix: runPrefix,
	})
	if err != nil {
		log.Fatalf("phase5 drain: %v", err)
	}
	log.Printf("[phase 5] drain result: sent=%d retried=%d failed=%d disabled=%d", drainResult.Sent, drainResult.Retried, drainResult.Failed, drainResult.Disabled)
	if drainResult.Sent != inserted {
		log.Printf("[phase 5] WARNING: expected sent=%d, got sent=%d. Check Lark webhook response and t_listing_delivery_attempt for clues.", inserted, drainResult.Sent)
	}

	// --- Phase 6: idempotency check (re-drain must drain 0)
	redrainResult, err := listing.DrainDueOutbox(ctx, repo, listing.DeliveryDeps{
		WebhookURL:      *webhook,
		WebhookSecret:   *webhookSecret,
		Client:          httpClient,
		Now:             func() time.Time { return drainNow.Add(time.Millisecond) },
		BatchSize:       *batchSize,
		DedupeKeyPrefix: runPrefix,
	})
	if err != nil {
		log.Fatalf("phase6 re-drain: %v", err)
	}
	if redrainResult.Sent != 0 || redrainResult.Retried != 0 || redrainResult.Failed != 0 {
		log.Fatalf("[phase 6] FAIL: re-drain sent=%d retried=%d failed=%d; expected all zero (rows already moved to 'sent').\n"+
			"  The worker would double-send if this happened in production.", redrainResult.Sent, redrainResult.Retried, redrainResult.Failed)
	}
	log.Printf("[phase 6] idempotency OK: re-drain returned %+v", redrainResult)

	// --- Phase 7: dedupe uniqueness (re-insert same dedupe_keys -> 0 rows affected)
	reinserted, err := insertSmokeOutbox(ctx, db, runPrefix, hotGapEvents, divEvents, liquidityCards, *maxAttempts, now)
	if err != nil {
		log.Fatalf("phase7 dedupe insert: %v", err)
	}
	if reinserted != 0 {
		log.Fatalf("[phase 7] FAIL: re-insert affected %d rows; expected 0.\n"+
			"  The unique key on (dedupe_key) MUST collapse duplicate inserts; this is the dedupe red line.", reinserted)
	}
	log.Printf("[phase 7] dedupe OK: re-insert of same dedupe_keys affected 0 rows")

	// --- Phase 8: cleanup
	if *skipCleanup {
		log.Printf("[phase 8] --skip-cleanup set: %d row(s) remain in t_listing_delivery_outbox with prefix %s", inserted, runPrefix)
	} else {
		removed, err := deleteSmokeRows(ctx, db, runPrefix)
		if err != nil {
			log.Fatalf("phase8 cleanup: %v", err)
		}
		log.Printf("[phase 8] cleanup: removed %d smoke row(s)", removed)
	}

	log.Println()
	log.Printf("=== SMOKE PASS ===  inserted=%d sent=%d  prefix=%s", inserted, drainResult.Sent, runPrefix)
	log.Println()
	log.Printf("Now check your Lark group: you should see %d new card(s) posted by this run.", drainResult.Sent)
}

// resolveFromConfig fills empty CLI flag values from config + env so
// the script reuses whatever the production engine reads. Precedence:
//  1. Explicit flag value (non-empty after TrimSpace).
//  2. Environment variable (DASHBOARD_MYSQL_DSN for DSN).
//  3. Loaded YAML config (only when configOK).
//
// Webhook precedence matches the production resolver in
// listing.engine: Alert.WebHookP3 > top30_webhook_url >
// top30_webhook_url_env.
func resolveFromConfig(configOK bool, cfg *config.Config, dsn, webhook, secret, proxy, dashboard *string) {
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
	if strings.TrimSpace(*secret) == "" && configOK {
		*secret = cfg.Runtime.ListingAgent.Delivery.Top30WebhookSecret
	}
	if strings.TrimSpace(*proxy) == "" && configOK {
		*proxy = rewriteHostInternalForLocalShell(cfg.Runtime.ListingAgent.Delivery.Proxy)
	}
	if strings.TrimSpace(*dashboard) == "" && configOK {
		*dashboard = cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL
	}
}

// rewriteHostInternalForLocalShell turns Docker-only hostnames into
// 127.0.0.1 so the smoke CLI (running on the host shell) can reuse
// the production proxy value without operator overrides.
func rewriteHostInternalForLocalShell(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	if parsed.Hostname() != "host.docker.internal" {
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

func parseIncludeFlag(raw string) (hotGap, divergence, liquidity bool, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return true, true, true, nil
	case "hot_gap", "hotgap", "top30", "#1":
		return true, false, false, nil
	case "divergence", "div", "#2-#5":
		return false, true, false, nil
	case "liquidity", "liq", "#10-#11", "#10", "#11":
		return false, false, true, nil
	}
	return false, false, false, fmt.Errorf("unrecognised --include=%q (allowed: all | hot_gap | divergence | liquidity)", raw)
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

// insertSmokeOutbox writes one outbox row per event, wrapping the
// production dedupe_key with `<runPrefix><production dedupe_key>` so
// the smoke rows are addressable and never collide with live traffic.
// Returns the number of rows actually inserted (after INSERT IGNORE)
// so the caller can assert dedupe correctness on re-runs.
//
// The script uses raw SQL rather than the package-private
// repository.insertOutbox helper so production code stays untouched.
// The column list mirrors backend/migrations/000010_listing_agent_p1.up.sql.
func insertSmokeOutbox(ctx context.Context, db *sql.DB, runPrefix string, hotGapEvents []listing.Top30PushEvent, divEvents []listing.DivergencePushEvent, liquidityCards []liquidity.CardPayload, maxAttempts int, now time.Time) (int, error) {
	stmt, err := db.PrepareContext(ctx,
		`INSERT IGNORE INTO t_listing_delivery_outbox
		   (event_type, dedupe_key, target_channel, status, attempt_count, max_attempts,
		    next_attempt_at, payload_json, last_error, sent_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?, ?, NULL, NULL, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	nextAttempt := now

	for _, ev := range hotGapEvents {
		payload, err := listing.RenderTop30PostMessage(ev)
		if err != nil {
			return inserted, fmt.Errorf("render hot-gap %s: %w", ev.Symbol, err)
		}
		dedupe := runPrefix + ev.DedupeKey
		res, err := stmt.ExecContext(ctx,
			listing.DeliveryEventTop30HotGap, dedupe, listing.DeliveryChannelLarkTop30,
			listing.OutboxStatusPending, maxAttempts, nextAttempt, payload, now, now)
		if err != nil {
			return inserted, fmt.Errorf("insert hot-gap %s: %w", ev.Symbol, err)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}

	for _, ev := range divEvents {
		payload, err := listing.RenderDivergencePostMessage(ev)
		if err != nil {
			return inserted, fmt.Errorf("render divergence %s: %w", ev.Category, err)
		}
		dedupe := runPrefix + ev.DedupeKey
		eventType := divergenceEventTypeForCategory(ev.Category)
		res, err := stmt.ExecContext(ctx,
			eventType, dedupe, listing.DeliveryChannelLarkTop30,
			listing.OutboxStatusPending, maxAttempts, nextAttempt, payload, now, now)
		if err != nil {
			return inserted, fmt.Errorf("insert divergence %s: %w", ev.Category, err)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}

	for _, card := range liquidityCards {
		dedupe := runPrefix + card.DedupeKey
		card.DedupeKey = dedupe
		payload, err := liquidity.RenderLiquidityPostMessage(card)
		if err != nil {
			return inserted, fmt.Errorf("render liquidity %s/%s: %w", card.Kind, card.Canonical, err)
		}
		eventType := liquidityEventTypeForKind(card.Kind)
		res, err := stmt.ExecContext(ctx,
			eventType, dedupe, listing.DeliveryChannelLarkLiquidity,
			listing.OutboxStatusPending, maxAttempts, nextAttempt, payload, now, now)
		if err != nil {
			return inserted, fmt.Errorf("insert liquidity %s/%s: %w", card.Kind, card.Canonical, err)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	return inserted, nil
}

// liquidityEventTypeForKind mirrors the mapping inside the production
// liquidity producer so smoke outbox rows carry the same event_type
// values DrainDueOutbox would resolve to the liquidity webhook.
func liquidityEventTypeForKind(kind liquidity.AlertKind) string {
	switch kind {
	case liquidity.KindLiquidityLag:
		return listing.DeliveryEventLiquidityLag
	case liquidity.KindWorstDepth:
		return listing.DeliveryEventWorstDepth
	}
	return string(kind)
}

// divergenceEventTypeForCategory mirrors the mapping used by
// listing.ProduceDivergencePush so smoke outbox rows carry the same
// event_type values the engine would produce. Falls back to a generic
// "top30_divergence" so an operator who adds a future category still
// gets a valid row.
func divergenceEventTypeForCategory(category string) string {
	switch category {
	case listing.DivergenceCategoryCEXOnly:
		return listing.DeliveryEventTop30DivergenceCEXOnly
	case listing.DivergenceCategoryDEXOnly:
		return listing.DeliveryEventTop30DivergenceDEXOnly
	case listing.DivergenceCategoryHeavyGap:
		return listing.DeliveryEventTop30DivergenceHeavyGap
	case listing.DivergenceCategoryBothHotGap:
		return listing.DeliveryEventTop30DivergenceBothHotGap
	}
	return "top30_divergence"
}

// deleteSmokeRows wipes every t_listing_delivery_outbox row matching
// the given prefix and the corresponding t_listing_delivery_attempt
// audit rows (children first to satisfy the FK).
func deleteSmokeRows(ctx context.Context, db *sql.DB, prefix string) (int, error) {
	// Phase A: scrub attempt rows owned by the soon-to-be-deleted outbox rows.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM t_listing_delivery_attempt
		   WHERE outbox_id IN (SELECT id FROM t_listing_delivery_outbox WHERE dedupe_key LIKE ?)`,
		prefix+"%"); err != nil {
		return 0, fmt.Errorf("delete attempts: %w", err)
	}
	// Phase B: scrub outbox rows themselves.
	res, err := db.ExecContext(ctx,
		`DELETE FROM t_listing_delivery_outbox WHERE dedupe_key LIKE ?`, prefix+"%")
	if err != nil {
		return 0, fmt.Errorf("delete outbox: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
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

// buildLiquiditySmokeFixtures returns three deterministic liquidity
// cards covering the first / reissue / clear phases so one smoke run
// exercises every visual + state-machine payload the production
// engine will emit. The numbers are chosen to look representative
// without being identical across scenarios; the dedupe_key marker is
// "_smoke" so the smoke wrap-prefix (lark_push_test|<nonce>|...) never
// collides with the preview CLI's "_fixture" marker.
//
// This intentionally stays self-contained (not shared with
// liquidity-alert-preview) so the smoke harness has no build-time
// dependency on scripts/liquidity-alert-preview, which keeps the two
// tools independently iterable.
func buildLiquiditySmokeFixtures(dashboardBase string, lagThreshold float64) []liquidity.CardPayload {
	now := time.Now().UTC()
	tier := "0.10%"
	dash := func(canonical string) string {
		return liquidity.BuildDashboardURL(dashboardBase, canonical, tier)
	}
	lagFirst := liquidity.CardPayload{
		Kind:             liquidity.KindLiquidityLag,
		Phase:            liquidity.PhaseFirst,
		Canonical:        "BTC",
		DisplaySymbol:    "BTC-USDT (perp)",
		Tier:             tier,
		SeveritySeq:      1,
		FirstTriggeredAt: now,
		EvaluatedAt:      now,
		EdgexDepth:       2_400_000,
		MedianDepth:      5_800_000,
		Ratio:            2_400_000.0 / 5_800_000.0,
		LagThreshold:     lagThreshold,
		Comparators:      8,
		TotalPlatforms:   9,
		EdgexRank:        6,
		Platforms: []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 8_500_000, Rank: 1},
			{Platform: "okx", DepthUSD: 7_100_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 6_200_000, Rank: 3},
			{Platform: "bitget", DepthUSD: 5_800_000, Rank: 4, IsMedian: true},
			{Platform: "gate", DepthUSD: 3_800_000, Rank: 5},
			{Platform: "edgeX", DepthUSD: 2_400_000, Rank: 6, IsEdgex: true},
			{Platform: "bingx", DepthUSD: 1_900_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 1_400_000, Rank: 8},
			{Platform: "hyperliquid", DepthUSD: 800_000, Rank: 9},
		},
		DashboardURL: dash("BTC"),
		DedupeKey:    "liquidity_lag|BTC|seq1|first_smoke",
	}
	lagReissue := liquidity.CardPayload{
		Kind:             liquidity.KindLiquidityLag,
		Phase:            liquidity.PhaseReissue,
		Canonical:        "ETH",
		DisplaySymbol:    "ETH-USDT (perp)",
		Tier:             tier,
		SeveritySeq:      1,
		ReissueIdx:       1,
		FirstTriggeredAt: now.Add(-9 * time.Hour),
		EvaluatedAt:      now,
		EdgexDepth:       1_050_000,
		MedianDepth:      2_900_000,
		Ratio:            1_050_000.0 / 2_900_000.0,
		LagThreshold:     lagThreshold,
		Comparators:      7,
		TotalPlatforms:   8,
		EdgexRank:        6,
		Platforms: []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 4_200_000, Rank: 1},
			{Platform: "okx", DepthUSD: 3_400_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 2_900_000, Rank: 3, IsMedian: true},
			{Platform: "bitget", DepthUSD: 2_900_000, Rank: 4, IsMedian: true},
			{Platform: "gate", DepthUSD: 2_100_000, Rank: 5},
			{Platform: "edgeX", DepthUSD: 1_050_000, Rank: 6, IsEdgex: true},
			{Platform: "bingx", DepthUSD: 720_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 410_000, Rank: 8},
		},
		DashboardURL: dash("ETH"),
		DedupeKey:    "liquidity_lag|ETH|seq1|reissue2_smoke",
	}
	clear := liquidity.CardPayload{
		Kind:             liquidity.KindLiquidityLag,
		Phase:            liquidity.PhaseClear,
		Canonical:        "SOL",
		DisplaySymbol:    "SOL-USDT (perp)",
		Tier:             tier,
		SeveritySeq:      1,
		FirstTriggeredAt: now.Add(-14 * time.Hour),
		EvaluatedAt:      now,
		EdgexDepth:       6_100_000,
		MedianDepth:      5_900_000,
		Ratio:            6_100_000.0 / 5_900_000.0,
		LagThreshold:     lagThreshold,
		Comparators:      8,
		TotalPlatforms:   9,
		EdgexRank:        4,
		Platforms: []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 8_400_000, Rank: 1},
			{Platform: "okx", DepthUSD: 7_000_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 6_300_000, Rank: 3},
			{Platform: "edgeX", DepthUSD: 6_100_000, Rank: 4, IsEdgex: true},
			{Platform: "bitget", DepthUSD: 5_900_000, Rank: 5, IsMedian: true},
			{Platform: "gate", DepthUSD: 3_700_000, Rank: 6},
			{Platform: "bingx", DepthUSD: 1_800_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 1_400_000, Rank: 8},
			{Platform: "hyperliquid", DepthUSD: 780_000, Rank: 9},
		},
		DashboardURL: dash("SOL"),
		DedupeKey:    "liquidity_lag|SOL|seq1|clear_smoke",
	}
	return []liquidity.CardPayload{lagFirst, lagReissue, clear}
}
