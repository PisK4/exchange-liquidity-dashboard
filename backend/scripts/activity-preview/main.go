package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
	"edgex-ops-intelligence/backend/internal/config"
)

var allActivityCards = []string{"daily", "event", "update", "review", "weekly", "health"}

type previewCard struct {
	Name    string
	Payload []byte
}

func main() {
	var (
		configDir = flag.String("config-dir", "../config", "Path to the EdgeX Ops Intelligence config directory")
		cardFlag  = flag.String("card", "all", "Card names to render: all or comma-separated daily,event,update,review,weekly,health")
		dryRun    = flag.Bool("dry-run", true, "Print rendered card JSON to stdout")
		dashboard = flag.String("dashboard-base", "", "EdgeX Ops Intelligence base URL for CTA/deep-link buttons")
		secret    = flag.String("decision-token-secret", "", "Preview-only decision token secret; falls back to YAML then preview-secret")
	)
	flag.Parse()

	cfg, err := config.Load(*configDir)
	if err == nil && strings.TrimSpace(*dashboard) == "" {
		*dashboard = cfg.Runtime.ActivityAgent.Delivery.DashboardBaseURL
	}
	if strings.TrimSpace(*secret) == "" {
		if err == nil && strings.TrimSpace(cfg.Runtime.ActivityAgent.DecisionToken.Secret) != "" {
			*secret = strings.TrimSpace(cfg.Runtime.ActivityAgent.DecisionToken.Secret)
		}
		if strings.TrimSpace(*secret) == "" {
			*secret = "preview-secret"
		}
	}
	names, err := parseCardSelection(*cardFlag)
	if err != nil {
		log.Fatal(err)
	}
	cards, err := buildPreviewCards(names, firstNonEmpty(*dashboard, "https://dashboard.example.test"), *secret)
	if err != nil {
		log.Fatal(err)
	}
	for _, card := range cards {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, card.Payload, "", "  "); err != nil {
			log.Fatalf("indent %s: %v", card.Name, err)
		}
		fmt.Printf("\n=== activity card: %s ===\n%s\n", card.Name, pretty.String())
	}
	if !*dryRun {
		log.Printf("activity-preview is render-only; use activity-smoke for live webhook validation")
	}
}

func parseCardSelection(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return append([]string(nil), allActivityCards...), nil
	}
	allowed := map[string]bool{}
	for _, name := range allActivityCards {
		allowed[name] = true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if !allowed[name] {
			return nil, errors.New("unknown activity card: " + name)
		}
		out = append(out, name)
	}
	return out, nil
}

func buildPreviewCards(names []string, dashboardBase, decisionSecret string) ([]previewCard, error) {
	now := time.Date(2026, 6, 5, 8, 5, 0, 0, time.UTC)
	out := make([]previewCard, 0, len(names))
	for _, name := range names {
		payload, err := renderPreviewCard(name, dashboardBase, decisionSecret, now)
		if err != nil {
			return nil, err
		}
		out = append(out, previewCard{Name: name, Payload: payload})
	}
	return out, nil
}

func renderPreviewCard(name, dashboardBase, decisionSecret string, now time.Time) ([]byte, error) {
	event := activity.ActivityEventCard{
		EventID:             42,
		EventVersion:        2,
		ContentHash:         "hash-v2",
		Platform:            "Binance",
		SourceGroup:         "cms_article_detail",
		FetchMode:           "http_direct",
		SourceHealth:        activity.SourceStatusOK,
		Title:               "Binance Launchpool: Stake BNB to Earn ABC",
		ActivityType:        "launchpool",
		Summary:             "Binance opened a new Launchpool campaign.",
		SourceURL:           "https://www.binance.com/en/support/announcement/abc",
		DedupeKey:           "binance|cms_article_detail|abc",
		TriggerTime:         now,
		DecisionBaseURL:     dashboardBase,
		DecisionTokenSecret: decisionSecret,
		ConfirmedRichLines:  []string{"Rich: 奖池 300,000 USDT · confirmed", "Rich: 窗口 2026-06-05 08:00 UTC → 2026-06-12 08:00 UTC"},
	}
	switch name {
	case "daily":
		return activity.RenderActivityDailyDigestPostMessage(activity.ActivityDigestCard{
			DigestKey:           "2026-06-05",
			Title:               "2026-06-05 竞品活动雷达",
			NewCount:            12,
			UpdatedCount:        5,
			EndingSoonCount:     3,
			ReviewPendingCount:  4,
			RawEventCount:       12,
			RichFieldCount:      4,
			AutoNotifiableCount: 9,
			TriggerTime:         now,
			DashboardBaseURL:    strings.TrimRight(dashboardBase, "/") + "/activity",
			ReviewQueueURL:      strings.TrimRight(dashboardBase, "/") + "/activity?review_status=pending",
			BulkDecisionURL:     strings.TrimRight(dashboardBase, "/") + "/activity?bulk=decision",
			Rows: []activity.ActivityDigestRow{
				{SeverityColor: "red", Platform: "Binance", ActivityType: "launchpool", Title: "Stake BNB to Earn ABC", Summary: "Binance opened a new Launchpool campaign.", SourceHealth: activity.SourceStatusOK, RichLine: "奖池 300,000 USDT · confirmed"},
				{SeverityColor: "orange", Platform: "MEXC", ActivityType: "futures_trading_competition", Title: "M-Day campaign", Summary: "reward/time 候选低置信", SourceHealth: activity.SourceStatusDegraded},
			},
		})
	case "event":
		return activity.RenderActivityEventAlertPostMessage(event)
	case "update":
		return activity.RenderActivityEventUpdatePostMessage(activity.ActivityEventUpdateCard{
			ActivityEventCard: event,
			ChangeSummary:     "活动窗口 / 奖池字段更新",
			OldDecision:       "对标观察 · 已标记 stale",
			ChangeLines:       []string{"窗口：06-05 → 06-12 更新为 06-05 → 06-15", "奖池：300,000 USDT 更新为 500,000 USDT · confirmed"},
		})
	case "review":
		event.EventID = 7
		event.Platform = "MEXC"
		event.SourceGroup = "latest_events"
		event.FetchMode = "utls_proxy_html"
		event.SourceHealth = activity.SourceStatusDegraded
		event.Title = "MEXC Futures M-Day campaign"
		event.ActivityType = "futures_trading_competition"
		event.Summary = "Trade futures during the event period to share rewards."
		event.SourceURL = "https://www.mexc.com/support/articles/review"
		event.DedupeKey = "mexc|latest_events|review"
		event.ReviewReasons = []string{"reward_pool_low_confidence", "local_timezone_unknown"}
		event.CandidateLines = []string{"奖池候选：$50,000 / 50,000 USDT 二义性"}
		return activity.RenderActivityReviewRequiredPostMessage(event)
	case "weekly":
		return activity.RenderActivityWeeklyDigestPostMessage(activity.ActivityWeeklyDigestCard{
			DigestKey:              "2026-W23",
			Title:                  "2026-W23 竞品运营趋势",
			RawActivityCount:       48,
			RichConfirmedCount:     17,
			NewPlaybookCount:       4,
			PendingSuggestionCount: 6,
			HotActivityTypes:       "launchpool / trading competition",
			RewardPoolTrend:        "confirmed subset +32% WoW",
			ThemeSummary:           "AI / BTC / TradFi 股票",
			DecisionSummary:        "跟进 6 · 对标 9 · 差异化 3 · 暂不跟进 4",
			TriggerTime:            now.Add(72 * time.Hour),
			DashboardBaseURL:       dashboardBase,
			Rows: []string{
				"<font color='red'>●</font> 新玩法：多池 Launchpool + trading threshold 组合增多",
				"<font color='orange'>●</font> 奖池：Binance / Gate / MEXC 明显抬升",
			},
		})
	case "health":
		return activity.RenderActivitySourceHealthPostMessage(activity.ActivitySourceHealthCard{
			Platform:         "Gate",
			SourceGroup:      "launchpool_project_list",
			FetchMode:        "utls_proxy_json",
			Status:           activity.SourceStatusBlocked,
			ErrorKind:        "schema_drift",
			HTTPStatus:       "200",
			Impact:           "Gate Launchpool 自动推送暂停",
			DisabledUntilUTC: "2026-06-05 10:30 UTC",
			TriggerTime:      now.Add(2 * time.Hour),
			SourceHealthURL:  strings.TrimRight(dashboardBase, "/") + "/activity/source-health",
			RunnerReportURL:  strings.TrimRight(dashboardBase, "/") + "/activity/runs/gate",
		})
	default:
		return nil, errors.New("unknown activity card: " + name)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
