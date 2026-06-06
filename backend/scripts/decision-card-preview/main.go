// decision-card-preview renders three canonical Listing decision-card
// demo payloads via listing.RenderDecisionCardPostMessage and either
// prints them to stdout (--dry-run) or POSTs them to a Lark webhook
// for live preview in the Listing operator group.
//
// The scenarios are hand-curated to exercise the three biggest visual
// states the renderer produces:
//   - High-confidence "公告 + API 双源确认" → 准备上线 (red header)
//   - Medium-confidence "API 已发现 · 待公告" → 进入观察 (orange header)
//   - Pre-assessment "公告已发布 · 待 API"  → 进入预评估 (blue header)
//
// All three cards carry the standard 4-button row so operators can
// verify Lark click handling end-to-end before we ship.
//
// The CLI is read-only: it never writes to t_listing_outbox_*,
// t_listing_signal_observation, or anywhere else. Safe to run
// alongside the live deploy-backend listing engine.
package main

import (
	"bytes"
	"context"
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

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing"
)

func main() {
	var (
		configDir = flag.String("config-dir", "../config", "Path to dashboard config directory; Alert.Webhooks.Listing and Runtime.listing_agent.delivery values are used as defaults")
		webhook   = flag.String("webhook-url", "", "Lark webhook URL; falls back to Alert.Webhooks.Listing then Runtime.listing_agent.delivery.top30_webhook_url[_env]")
		proxy     = flag.String("proxy", "", "Optional HTTP(S) proxy for the webhook POST; falls back to Runtime.listing_agent.delivery.proxy")
		dryRun    = flag.Bool("dry-run", false, "Print rendered card JSON to stdout but never POST")
		spacing   = flag.Duration("spacing", 2*time.Second, "Sleep between successive POSTs so Lark does not rate-limit")
		only      = flag.String("only", "", "Comma-separated scenario IDs to render (default: all). Valid: announcement_and_api_prepare, instrument_diff_only_watch, announcement_pending_api_pre_assessment")
	)
	flag.Parse()

	cfg, cfgErr := config.Load(*configDir)
	if cfgErr != nil {
		log.Printf("config load %q failed (continuing with flag/env values only): %v", *configDir, cfgErr)
	}
	resolveFromConfig(cfgErr == nil, &cfg, webhook, proxy)

	scenarios := allDemoScenarios()
	if filter := strings.TrimSpace(*only); filter != "" {
		want := map[string]bool{}
		for _, name := range strings.Split(filter, ",") {
			want[strings.TrimSpace(name)] = true
		}
		filtered := scenarios[:0]
		for _, sc := range scenarios {
			if want[sc.name] {
				filtered = append(filtered, sc)
			}
		}
		scenarios = filtered
	}
	if len(scenarios) == 0 {
		log.Fatal("no scenarios to render after --only filter")
	}

	client := buildHTTPClient(*proxy)
	ctx := context.Background()
	posted := 0
	now := time.Now().UTC()
	for i, sc := range scenarios {
		ev := sc.builder(now)
		body, err := listing.RenderDecisionCardPostMessage(ev)
		if err != nil {
			log.Printf("render %s: %v", sc.name, err)
			continue
		}
		var pretty bytes.Buffer
		_ = json.Indent(&pretty, body, "", "  ")
		fmt.Printf("\n=== scenario[%d] %s · %s · %s ===\n", i, sc.name, ev.EvidenceKind, ev.Recommendation)
		fmt.Println(pretty.String())

		if *dryRun || strings.TrimSpace(*webhook) == "" {
			continue
		}
		if err := postWebhook(ctx, client, *webhook, body); err != nil {
			log.Printf("POST %s failed: %v", sc.name, err)
			continue
		}
		posted++
		log.Printf("posted %s · evidence=%s · recommendation=%s to webhook", sc.name, ev.EvidenceKind, ev.Recommendation)
		if i+1 < len(scenarios) && *spacing > 0 {
			time.Sleep(*spacing)
		}
	}
	log.Printf("done: rendered=%d posted=%d (dry_run=%t webhook_set=%t)", len(scenarios), posted, *dryRun, strings.TrimSpace(*webhook) != "")
}

// scenario captures one demo card. The builder closure receives the
// CLI's `now` so every triggerTime reads as "just now" in the Lark
// group, regardless of when the CLI is run.
type scenario struct {
	name    string
	builder func(now time.Time) listing.DecisionCardEvent
}

func allDemoScenarios() []scenario {
	return []scenario{
		{name: "announcement_and_api_prepare", builder: demoAnnouncementAndAPI},
		{name: "instrument_diff_only_watch", builder: demoInstrumentDiffOnly},
		{name: "announcement_pending_api_pre_assessment", builder: demoAnnouncementPending},
	}
}

func demoAnnouncementAndAPI(now time.Time) listing.DecisionCardEvent {
	lev := 50.0
	cap := 450_000_000.0
	vol := 110_000_000.0
	return listing.DecisionCardEvent{
		CandidateID:     90001,
		RiskPlanID:      80001,
		CanonicalSymbol: "PEPE",
		DisplaySymbol:   "PEPE-USDT (perp)",
		EvidenceKind:    listing.EvidenceAnnouncementAndAPI,
		Recommendation:  listing.RecommendationPrepareListing,
		ConfidenceLevel: listing.ConfidenceHigh,
		BusinessScore:   82,
		SourcePlatforms: []string{"binance", "bybit"},
		Actions:         demoActions(),
		DedupeKey:       "preview|announcement_and_api|" + now.Format("20060102T150405Z"),
		TriggerTime:     now,
		Enrichment: listing.DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: []listing.PlatformMarketStatus{
				{Platform: "binance", DisplayName: "Binance Futures", Status: listing.StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api", OccurredAt: now.Add(-22 * time.Hour)},
				{Platform: "bybit", DisplayName: "Bybit Linear", Status: listing.StatusPreListing, StatusLabel: "公告已发", SourceKind: "announcement", OccurredAt: now.Add(-30 * time.Minute)},
			},
			MarketCapUSD:     &cap,
			Spot24hVolumeUSD: &vol,
			SpotDepth:        &listing.DepthEvidence{Platform: "binance", USDValue: 38_000, Tier: "0.1pct"},
			PerpDepth:        &listing.DepthEvidence{Platform: "binance", USDValue: 92_000, Tier: "0.1pct"},
			CoinGeckoID:      "pepe",
		},
		RiskPlan: listing.RiskPlan{
			ID: 80001, CandidateID: 90001, RiskPlanVersion: "v1",
			TemplateName:    listing.RiskTemplateTier1Standard,
			MaxLeverage:     &lev,
			MMQuoteRequired: true,
			LeverageTiersJSON: json.RawMessage(
				`[{"position_usd_max":50000,"max_leverage":50},{"position_usd_max":250000,"max_leverage":20},{"position_usd_max":1000000,"max_leverage":5}]`,
			),
		},
	}
}

func demoInstrumentDiffOnly(now time.Time) listing.DecisionCardEvent {
	lev := 30.0
	cap := 220_000_000.0
	vol := 48_000_000.0
	return listing.DecisionCardEvent{
		CandidateID:     90002,
		RiskPlanID:      80002,
		CanonicalSymbol: "WIF",
		DisplaySymbol:   "WIF-USDT (perp)",
		EvidenceKind:    listing.EvidenceInstrumentDiffOnly,
		Recommendation:  listing.RecommendationWatch,
		ConfidenceLevel: listing.ConfidenceMedium,
		BusinessScore:   58,
		SourcePlatforms: []string{"okx"},
		Actions:         demoActions(),
		DedupeKey:       "preview|instrument_diff_only|" + now.Format("20060102T150405Z"),
		TriggerTime:     now,
		Enrichment: listing.DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: []listing.PlatformMarketStatus{
				{Platform: "okx", DisplayName: "OKX Perp", Status: listing.StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api", OccurredAt: now.Add(-15 * time.Minute)},
			},
			MarketCapUSD:     &cap,
			Spot24hVolumeUSD: &vol,
			SpotDepth:        &listing.DepthEvidence{Platform: "binance", USDValue: 14_500, Tier: "0.1pct"},
			PerpDepth:        &listing.DepthEvidence{Platform: "binance", USDValue: 41_000, Tier: "0.1pct"},
			CoinGeckoID:      "dogwifcoin",
		},
		RiskPlan: listing.RiskPlan{
			ID: 80002, CandidateID: 90002, RiskPlanVersion: "v1",
			TemplateName:    listing.RiskTemplateTier1Standard,
			MaxLeverage:     &lev,
			MMQuoteRequired: false,
			LeverageTiersJSON: json.RawMessage(
				`[{"position_usd_max":50000,"max_leverage":30},{"position_usd_max":250000,"max_leverage":10},{"position_usd_max":1000000,"max_leverage":3}]`,
			),
		},
	}
}

func demoAnnouncementPending(now time.Time) listing.DecisionCardEvent {
	cap := 18_000_000.0
	vol := 3_500_000.0
	return listing.DecisionCardEvent{
		CandidateID:     90003,
		RiskPlanID:      80003,
		CanonicalSymbol: "NEWT",
		DisplaySymbol:   "NEWT-USDT (perp)",
		EvidenceKind:    listing.EvidenceAnnouncementPendingAPI,
		Recommendation:  listing.RecommendationPreAssessment,
		ConfidenceLevel: listing.ConfidenceMediumHigh,
		BusinessScore:   65,
		SourcePlatforms: []string{"bybit"},
		Actions:         demoActions(),
		DedupeKey:       "preview|announcement_pending_api|" + now.Format("20060102T150405Z"),
		TriggerTime:     now,
		Enrichment: listing.DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: []listing.PlatformMarketStatus{
				{Platform: "bybit", DisplayName: "Bybit Linear", Status: listing.StatusPreListing, StatusLabel: "公告刚发布", SourceKind: "announcement", OccurredAt: now.Add(-15 * time.Minute)},
			},
			MarketCapUSD:     &cap,
			Spot24hVolumeUSD: &vol,
			CoinGeckoID:      "newt",
		},
		RiskPlan: listing.RiskPlan{
			ID: 80003, CandidateID: 90003, RiskPlanVersion: "v1",
			TemplateName:      listing.RiskTemplatePreAssessment,
			MMQuoteRequired:   false,
			LeverageTiersJSON: json.RawMessage(`[]`),
		},
	}
}

func demoActions() []listing.DecisionCardAction {
	return []listing.DecisionCardAction{
		{Action: listing.DecisionActionPrepareListing, Label: listing.DecisionActionLabels[listing.DecisionActionPrepareListing]},
		{Action: listing.DecisionActionEnterWatchlist, Label: listing.DecisionActionLabels[listing.DecisionActionEnterWatchlist]},
		{Action: listing.DecisionActionContactMM, Label: listing.DecisionActionLabels[listing.DecisionActionContactMM]},
		{Action: listing.DecisionActionIgnore, Label: listing.DecisionActionLabels[listing.DecisionActionIgnore]},
	}
}

// resolveFromConfig fills empty flags from config. Webhook precedence
// mirrors resolveListingWebhookURL in listing/engine.go so the
// preview lands in the same group the production worker does.
func resolveFromConfig(configOK bool, cfg *config.Config, webhook, proxy *string) {
	if strings.TrimSpace(*webhook) == "" && configOK {
		switch {
		case cfg.Alert.Enabled && strings.TrimSpace(cfg.Alert.Webhooks.Listing) != "":
			*webhook = cfg.Alert.Webhooks.Listing
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

func buildHTTPClient(proxyURL string) *http.Client {
	const timeout = 30 * time.Second
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		log.Printf("ignoring invalid proxy %q: %v", proxyURL, err)
		return &http.Client{Timeout: timeout}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(parsed)
	return &http.Client{Transport: tr, Timeout: timeout}
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
