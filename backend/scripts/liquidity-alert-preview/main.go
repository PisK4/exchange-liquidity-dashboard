// Dashboard liquidity-alert (#10 / #11) card preview CLI.
//
// Loads the latest fresh `t_orderbook_snapshot` rows for the
// configured depth tier, runs the same `liquidity.Compute` +
// `liquidity.DecideAction` pipeline the production engine uses, and
// either prints the rendered Lark cards to stdout (--dry-run, default)
// or POSTs each one to a Lark webhook for visual review.
//
// Designed to mirror scripts/divergence-preview/main.go so operators
// learn one workflow. It NEVER touches t_listing_delivery_outbox or
// t_listing_alert_state — every render is a synthetic
// "first-trigger" preview so the same canonical can be re-previewed
// any number of times without polluting state.
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
	"edgex-dashboard/backend/internal/listing/liquidity"
)

func main() {
	var (
		configDir = flag.String("config-dir", "../config", "Path to the dashboard config directory; values from edgex-liquidity-dashboard.yaml are used as defaults when --mysql-dsn / --webhook-url / --proxy / --dashboard-base / --tier-pct are not given")
		dsn       = flag.String("mysql-dsn", "", "MySQL DSN for the dashboard DB; falls back to DASHBOARD_MYSQL_DSN env, then Database.DSN from config-dir")
		webhook   = flag.String("webhook-url", "", "Lark webhook URL; falls back to Alert.Webhooks.Liquidity from config-dir; leave both empty for dry-run (stdout only)")
		proxy     = flag.String("proxy", "", "Optional HTTP(S) proxy for the webhook POST; falls back to Runtime.listing_agent.delivery.proxy from config-dir")
		dashboard = flag.String("dashboard-base", "", "Dashboard base URL inserted into the '查看深度对比' button; falls back to Runtime.listing_agent.delivery.dashboard_base_url from config-dir")
		tierPct   = flag.Float64("tier-pct", 0, "Depth tier as a fractional percentage (e.g. 0.001 for 0.1%); falls back to Runtime.listing_agent.liquidity_alert.depth_tier_pct from config-dir")
		stale     = flag.Duration("stale-after", 0, "Snapshot freshness window; falls back to Runtime.listing_agent.liquidity_alert.stale_after from config-dir")
		lagThresh = flag.Float64("lag-threshold", 0, "Lag threshold ratio (default 0.5 == edgeX < half competitor median); falls back to config")
		minCmp    = flag.Int("min-comparators", 0, "Minimum competitor count (default 3); falls back to config")
		dryRun    = flag.Bool("dry-run", true, "Print rendered card JSON to stdout but never POST (default: true)")
		canonical = flag.String("canonical", "", "Optional canonical symbol filter (e.g. BTC); rendering covers ALL eligible candidates when empty")
		phase     = flag.String("phase", "first", "Phase to render: first | reissue | clear (controls badge color + body copy without touching state)")
		fixture   = flag.String("fixture", "", "Comma-separated synthetic cards to render when real depth data does not naturally trigger anything. Choices: lag_first, lag_reissue, worst_first, clear. Use 'all' to render the full visual matrix. Synthetic cards skip MySQL — useful for visual smoke tests in Lark.")
	)
	flag.Parse()

	cfg, cfgErr := config.Load(*configDir)
	if cfgErr != nil {
		log.Printf("config load %q failed (continuing with flag/env values only): %v", *configDir, cfgErr)
	}
	resolveFromConfig(cfgErr == nil, &cfg, dsn, webhook, proxy, dashboard, tierPct, stale, lagThresh, minCmp)

	if *tierPct <= 0 {
		*tierPct = 0.001
	}
	if *stale <= 0 {
		*stale = 30 * time.Minute
	}
	if *lagThresh <= 0 {
		*lagThresh = 0.5
	}
	if *minCmp <= 0 {
		*minCmp = 3
	}

	ctx := context.Background()
	now := time.Now().UTC()

	previewPhase := liquidity.PhaseFirst
	switch strings.ToLower(strings.TrimSpace(*phase)) {
	case "reissue":
		previewPhase = liquidity.PhaseReissue
	case "clear":
		previewPhase = liquidity.PhaseClear
	}

	// Fixture mode: skip MySQL entirely and render synthetic cards
	// so operators can review the visual layout in Lark even when
	// real depth data does not trigger anything organically.
	if spec := strings.TrimSpace(*fixture); spec != "" {
		cards := buildFixtureCards(spec, *dashboard, *lagThresh, *tierPct, previewPhase, now)
		log.Printf("--fixture=%q → %d synthetic card(s)", spec, len(cards))
		client := buildHTTPClient(*proxy)
		renderAndMaybePost(ctx, client, cards, *webhook, *dryRun)
		return
	}

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

	repo := listing.NewRepository(db)
	tierLabel := fmt.Sprintf("%.2f%%", *tierPct*100)
	matrix, err := repo.LoadFreshDepthMatrix(ctx, tierLabel, *stale, now, cfg.CanonicalIndex)
	if err != nil {
		log.Fatalf("load depth matrix: %v", err)
	}
	if len(matrix) == 0 {
		log.Fatalf("empty depth matrix at tier=%s stale_after=%s — nothing to preview", tierLabel, *stale)
	}
	log.Printf("loaded depth matrix: tier=%s canonicals=%d stale_after=%s", tierLabel, len(matrix), *stale)

	universe, err := loadUniverse(cfg)
	if err != nil {
		log.Fatalf("load universe: %v", err)
	}

	candidates := liquidity.Compute(matrix, universe, canonicalIndexResolver(cfg.CanonicalIndex), liquidity.Config{
		DepthTierPct:     *tierPct,
		LagThreshold:     *lagThresh,
		MinComparators:   *minCmp,
		ReissueInterval:  6 * time.Hour,
		ClearConsecutive: 3,
	}, now)
	log.Printf("Compute → %d candidate(s)", len(candidates))

	if symbol := strings.ToUpper(strings.TrimSpace(*canonical)); symbol != "" {
		filtered := candidates[:0]
		for _, c := range candidates {
			if c.Canonical == symbol {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
		log.Printf("--canonical=%s applied; rendering %d candidate(s)", symbol, len(candidates))
	}

	client := buildHTTPClient(*proxy)
	cards := make([]liquidity.CardPayload, 0, len(candidates))
	for i := range candidates {
		cards = append(cards, buildPreviewCard(candidates[i], previewPhase, *dashboard, *lagThresh))
	}
	renderAndMaybePost(ctx, client, cards, *webhook, *dryRun)
}

// renderAndMaybePost prints every CardPayload as pretty JSON to stdout
// (so operators can audit) and POSTs the un-indented payload bytes to
// the Lark webhook when --dry-run is false and a webhook is configured.
func renderAndMaybePost(ctx context.Context, client *http.Client, cards []liquidity.CardPayload, webhook string, dryRun bool) {
	posted := 0
	for i := range cards {
		card := cards[i]
		body, err := liquidity.RenderLiquidityPostMessage(card)
		if err != nil {
			log.Printf("render %s/%s: %v", card.Kind, card.Canonical, err)
			continue
		}
		var pretty bytes.Buffer
		_ = json.Indent(&pretty, body, "", "  ")
		fmt.Printf("\n=== card[%d] %s · %s · phase=%s ===\n", i, card.Kind, card.Canonical, card.Phase)
		fmt.Println(pretty.String())

		if dryRun || strings.TrimSpace(webhook) == "" {
			continue
		}
		if err := postWebhook(ctx, client, webhook, body); err != nil {
			log.Printf("POST %s/%s failed: %v", card.Kind, card.Canonical, err)
			continue
		}
		posted++
		log.Printf("posted %s/%s to webhook", card.Kind, card.Canonical)
		// Tiny inter-card gap so Lark renders them in declared
		// order rather than a single batched timestamp.
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("done: rendered=%d posted=%d", len(cards), posted)
}

func loadUniverse(cfg config.Config) (*config.ListedUniverse, error) {
	if path := strings.TrimSpace(cfg.Catalog.ListedUniverseFile); path != "" {
		u, err := config.LoadListedUniverse(path)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	return &config.ListedUniverse{}, nil
}

func resolveFromConfig(
	configOK bool,
	cfg *config.Config,
	dsn, webhook, proxy, dashboard *string,
	tierPct *float64,
	stale *time.Duration,
	lagThresh *float64,
	minCmp *int,
) {
	if strings.TrimSpace(*dsn) == "" {
		if env := strings.TrimSpace(os.Getenv("DASHBOARD_MYSQL_DSN")); env != "" {
			*dsn = env
		} else if configOK && strings.TrimSpace(cfg.Database.DSN) != "" {
			*dsn = cfg.Database.DSN
		}
	}
	if strings.TrimSpace(*webhook) == "" && configOK {
		if cfg.Alert.Enabled {
			if u := strings.TrimSpace(cfg.Alert.Webhooks.Liquidity); u != "" {
				*webhook = u
			}
		}
	}
	if strings.TrimSpace(*proxy) == "" && configOK {
		*proxy = rewriteHostInternalForLocalShell(cfg.Runtime.ListingAgent.Delivery.Proxy)
	}
	if strings.TrimSpace(*dashboard) == "" && configOK {
		*dashboard = cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL
	}
	if configOK {
		la := cfg.Runtime.ListingAgent.LiquidityAlert
		if *tierPct <= 0 && la.DepthTierPct > 0 {
			*tierPct = la.DepthTierPct
		}
		if *stale <= 0 && la.StaleAfter > 0 {
			*stale = la.StaleAfter
		}
		if *lagThresh <= 0 && la.LagThreshold > 0 {
			*lagThresh = la.LagThreshold
		}
		if *minCmp <= 0 && la.MinComparators > 0 {
			*minCmp = la.MinComparators
		}
	}
}

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

// canonicalIndexResolver returns a CanonicalResolver bound to the
// loaded CanonicalIndex. nil index → a permissive resolver that
// considers nothing platform-exclusive (so preview can run even when
// the symbol_mapping.yaml is missing).
type permissiveResolver struct{ idx *config.CanonicalIndex }

func (p permissiveResolver) ResolveCanonical(platform, base string) string {
	if p.idx == nil {
		return strings.ToUpper(strings.TrimSpace(base))
	}
	return p.idx.Resolve(platform, base)
}

func (p permissiveResolver) IsPlatformExclusive(canonical string) bool {
	if p.idx == nil {
		return false
	}
	return p.idx.IsPlatformExclusive(canonical)
}

func canonicalIndexResolver(idx *config.CanonicalIndex) liquidity.CanonicalResolver {
	return permissiveResolver{idx: idx}
}

// buildPreviewCard turns one AlertCandidate into a CardPayload with a
// synthetic dedupe key. The preview never reads/writes
// t_listing_alert_state, so we approximate the production state
// machine by setting SeveritySeq=1 and ReissueIdx=1.
func buildPreviewCard(c liquidity.AlertCandidate, phase string, dashboardBase string, lagThreshold float64) liquidity.CardPayload {
	now := c.EvaluatedAt
	first := now
	if phase == liquidity.PhaseReissue {
		first = now.Add(-9 * time.Hour) // pretend the alert has been active for 9h
	} else if phase == liquidity.PhaseClear {
		first = now.Add(-14 * time.Hour) // pretend recovery after 14h
	}
	reissueIdx := 0
	if phase == liquidity.PhaseReissue {
		reissueIdx = 1
	}
	dedupe := fmt.Sprintf("%s|%s|seq1|%s_preview", c.Kind, c.Canonical, phase)
	if phase == liquidity.PhaseReissue {
		dedupe = fmt.Sprintf("%s|%s|seq1|reissue%d_preview", c.Kind, c.Canonical, reissueIdx+1)
	}
	return liquidity.CardPayload{
		Kind:             c.Kind,
		Phase:            phase,
		Canonical:        c.Canonical,
		DisplaySymbol:    c.DisplaySymbol,
		Tier:             c.Tier,
		SeveritySeq:      1,
		ReissueIdx:       reissueIdx,
		FirstTriggeredAt: first,
		EvaluatedAt:      now,
		EdgexDepth:       c.EdgexDepth,
		MedianDepth:      c.MedianDepth,
		Ratio:            c.Ratio,
		LagThreshold:     lagThreshold,
		Comparators:      c.Comparators,
		TotalPlatforms:   c.TotalPlatforms,
		EdgexRank:        c.EdgexRank,
		Platforms:        c.Platforms,
		DashboardURL:     liquidity.BuildDashboardURL(dashboardBase, c.Canonical, c.Tier),
		DedupeKey:        dedupe,
	}
}

// buildFixtureCards returns one CardPayload per requested fixture
// scenario. Spec is comma-separated; "all" expands to every kind.
//
// Scenarios:
//
//	lag_first    — first-trigger #10 (BTC, edgeX = 28% of competitor median)
//	lag_reissue  — second 6h reissue of #10 (ETH, 9h into the alert)
//	worst_first  — first-trigger #11 (SOL, edgeX last among 9 venues)
//	clear        — recovery card for the lag_first BTC scenario
//
// Numbers are chosen to look representative without being identical
// across scenarios so the operator can verify badge color + body copy
// changes between cards.
func buildFixtureCards(spec, dashboardBase string, lagThreshold, tierPct float64, defaultPhase string, now time.Time) []liquidity.CardPayload {
	want := map[string]bool{}
	for _, raw := range strings.Split(spec, ",") {
		k := strings.ToLower(strings.TrimSpace(raw))
		switch k {
		case "all":
			want["lag_first"] = true
			want["lag_reissue"] = true
			want["worst_first"] = true
			want["clear"] = true
		case "lag_first", "lag", "first":
			want["lag_first"] = true
		case "lag_reissue", "reissue":
			want["lag_reissue"] = true
		case "worst_first", "worst", "worst_depth":
			want["worst_first"] = true
		case "clear", "recover":
			want["clear"] = true
		}
	}
	tier := fmt.Sprintf("%.1f%%", tierPct*100)
	dash := func(canonical string) string {
		return liquidity.BuildDashboardURL(dashboardBase, canonical, tier)
	}
	out := make([]liquidity.CardPayload, 0, 4)

	if want["lag_first"] {
		platforms := []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 8_500_000, Rank: 1},
			{Platform: "okx", DepthUSD: 7_100_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 6_200_000, Rank: 3},
			{Platform: "bitget", DepthUSD: 5_800_000, Rank: 4, IsMedian: true},
			{Platform: "gate", DepthUSD: 3_800_000, Rank: 5},
			{Platform: "edgeX", DepthUSD: 2_400_000, Rank: 6, IsEdgex: true},
			{Platform: "bingx", DepthUSD: 1_900_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 1_400_000, Rank: 8},
			{Platform: "hyperliquid", DepthUSD: 800_000, Rank: 9},
		}
		out = append(out, liquidity.CardPayload{
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
			Platforms:        platforms,
			DashboardURL:     dash("BTC"),
			DedupeKey:        "liquidity_lag|BTC|seq1|first_fixture",
		})
	}
	if want["lag_reissue"] {
		platforms := []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 4_200_000, Rank: 1},
			{Platform: "okx", DepthUSD: 3_400_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 2_900_000, Rank: 3, IsMedian: true},
			{Platform: "bitget", DepthUSD: 2_900_000, Rank: 4, IsMedian: true},
			{Platform: "gate", DepthUSD: 2_100_000, Rank: 5},
			{Platform: "edgeX", DepthUSD: 1_050_000, Rank: 6, IsEdgex: true},
			{Platform: "bingx", DepthUSD: 720_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 410_000, Rank: 8},
		}
		out = append(out, liquidity.CardPayload{
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
			Platforms:        platforms,
			DashboardURL:     dash("ETH"),
			DedupeKey:        "liquidity_lag|ETH|seq1|reissue2_fixture",
		})
	}
	if want["worst_first"] {
		platforms := []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 3_100_000, Rank: 1},
			{Platform: "okx", DepthUSD: 2_700_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 2_300_000, Rank: 3},
			{Platform: "bitget", DepthUSD: 2_000_000, Rank: 4, IsMedian: true},
			{Platform: "gate", DepthUSD: 1_700_000, Rank: 5},
			{Platform: "hyperliquid", DepthUSD: 1_400_000, Rank: 6},
			{Platform: "bingx", DepthUSD: 1_100_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 900_000, Rank: 8},
			{Platform: "edgeX", DepthUSD: 480_000, Rank: 9, IsEdgex: true},
		}
		out = append(out, liquidity.CardPayload{
			Kind:             liquidity.KindWorstDepth,
			Phase:            liquidity.PhaseFirst,
			Canonical:        "SOL",
			DisplaySymbol:    "SOL-USDT (perp)",
			Tier:             tier,
			SeveritySeq:      1,
			FirstTriggeredAt: now,
			EvaluatedAt:      now,
			EdgexDepth:       480_000,
			MedianDepth:      2_000_000,
			Ratio:            480_000.0 / 2_000_000.0,
			LagThreshold:     lagThreshold,
			Comparators:      8,
			TotalPlatforms:   9,
			EdgexRank:        9,
			Platforms:        platforms,
			DashboardURL:     dash("SOL"),
			DedupeKey:        "worst_depth|SOL|seq1|first_fixture",
		})
	}
	if want["clear"] {
		// Recovery card: edgeX climbed back to mid-pack on BTC after
		// 14h of lagging. Ratio now sits above LagThreshold.
		platforms := []liquidity.AlertPlatformRow{
			{Platform: "binance", DepthUSD: 8_400_000, Rank: 1},
			{Platform: "okx", DepthUSD: 7_000_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 6_300_000, Rank: 3},
			{Platform: "edgeX", DepthUSD: 6_100_000, Rank: 4, IsEdgex: true},
			{Platform: "bitget", DepthUSD: 5_900_000, Rank: 5, IsMedian: true},
			{Platform: "gate", DepthUSD: 3_700_000, Rank: 6},
			{Platform: "bingx", DepthUSD: 1_800_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 1_400_000, Rank: 8},
			{Platform: "hyperliquid", DepthUSD: 780_000, Rank: 9},
		}
		out = append(out, liquidity.CardPayload{
			Kind:             liquidity.KindLiquidityLag,
			Phase:            liquidity.PhaseClear,
			Canonical:        "BTC",
			DisplaySymbol:    "BTC-USDT (perp)",
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
			Platforms:        platforms,
			DashboardURL:     dash("BTC"),
			DedupeKey:        "liquidity_lag|BTC|seq1|clear_fixture",
		})
	}
	// Silence the "defaultPhase unused" complaint when caller picks
	// fixture mode: defaultPhase is intentionally ignored because
	// each fixture scenario embeds its own phase.
	_ = defaultPhase
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
