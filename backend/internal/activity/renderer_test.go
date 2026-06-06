package activity

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRenderActivityEventAlertPostMessageUsesActivityCardContract(t *testing.T) {
	now := time.Date(2026, 6, 5, 8, 5, 0, 0, time.UTC)
	payload, err := RenderActivityEventAlertPostMessage(ActivityEventCard{
		EventID:             42,
		EventVersion:        2,
		ContentHash:         "hash-v2",
		Platform:            "Binance",
		SourceGroup:         "cms_article_detail",
		FetchMode:           "http_direct",
		SourceHealth:        SourceStatusOK,
		Title:               "Binance Launchpool: Stake BNB to Earn ABC",
		ActivityType:        "launchpool",
		Summary:             "Binance opened a new Launchpool campaign.",
		SourceURL:           "https://www.binance.com/en/support/announcement/abc",
		DedupeKey:           "binance|cms_article_detail|abc",
		TriggerTime:         now,
		DecisionBaseURL:     "https://dashboard.example.test",
		DecisionTokenSecret: "secret",
		ConfirmedRichLines: []string{
			"Rich: 奖池 300,000 USDT · confirmed",
			"Rich: 窗口 2026-06-05 08:00 UTC → 2026-06-12 08:00 UTC",
		},
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	if !bytes.Contains(payload, []byte("<font color='blue'>")) {
		t.Fatalf("font tags should stay unescaped, got %s", payload)
	}
	for _, label := range []string{"立即跟进", "对标观察", "差异化设计", "暂不跟进", "忽略/重复"} {
		if !bytes.Contains(payload, []byte(label)) {
			t.Fatalf("missing decision button %q in %s", label, payload)
		}
	}
	if strings.Contains(string(payload), "Top30") {
		t.Fatalf("activity card must not render Top30 context: %s", payload)
	}
	if strings.Contains(string(payload), "confidence 0.") || strings.Contains(string(payload), "0.92") {
		t.Fatalf("activity card must not expose numeric confidence: %s", payload)
	}
	for _, forbidden := range []string{"高价值", "**Source**", "**类型**", "主要原因", "建议动作", "Raw 摘要"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("activity event card should not render low-value field %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), "运营活动 · 新发现") || !strings.Contains(string(payload), "<font color='red'>新发现</font> · Binance") {
		t.Fatalf("activity event card should use simplified new-discovery copy: %s", payload)
	}
	if !strings.Contains(string(payload), "查看活动详情") || !strings.Contains(string(payload), "/activity/events/42") {
		t.Fatalf("activity event card should link to internal detail page: %s", payload)
	}
}

func TestRenderActivityReviewRequiredPostMessageRendersDetailAndDecisionButtons(t *testing.T) {
	payload, err := RenderActivityReviewRequiredPostMessage(ActivityEventCard{
		EventID:             7,
		EventVersion:        1,
		ContentHash:         "review-hash",
		Platform:            "MEXC",
		SourceGroup:         "latest_events",
		FetchMode:           "utls_proxy_html",
		SourceHealth:        SourceStatusDegraded,
		Title:               "MEXC Futures M-Day campaign",
		ActivityType:        "futures_trading_competition",
		Summary:             "Trade futures during the event period to share rewards.",
		SourceURL:           "https://www.mexc.com/support/articles/review",
		DedupeKey:           "mexc|latest_events|review",
		TriggerTime:         time.Date(2026, 6, 5, 9, 12, 0, 0, time.UTC),
		DecisionBaseURL:     "https://dashboard.example.test",
		DecisionTokenSecret: "secret",
		ReviewReasons:       []string{"reward_pool_low_confidence", "local_timezone_unknown"},
		CandidateLines:      []string{"奖池候选：$50,000 / 50,000 USDT 二义性"},
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	actions := countCardButtons(decoded)
	if actions != 7 {
		t.Fatalf("button count=%d want 7; payload=%s", actions, payload)
	}
	for _, forbidden := range []string{"高价值", "运营活动推送新发现", "运营活动待复核", "需要人工确认", "复核原因", "**Source**", "**类型**", "主要原因", "建议动作", "Raw 摘要", "reward_pool_low_confidence", "local_timezone_unknown"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("review card should not render review-only or low-value text %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), "运营活动 · 新发现") || !strings.Contains(string(payload), "<font color='red'>新发现</font> · MEXC") {
		t.Fatalf("review card should be presented as a normal new activity card: %s", payload)
	}
	if !strings.Contains(string(payload), "查看活动详情") || !strings.Contains(string(payload), "打开原始来源") {
		t.Fatalf("review card should render detail and source CTAs: %s", payload)
	}
}

func TestRenderActivityCardTruncatesLongContentAndLabelsJSONSources(t *testing.T) {
	longSummary := strings.Repeat("奖励活动", 500)
	payload, err := RenderActivityEventAlertPostMessage(ActivityEventCard{
		EventID:             99,
		EventVersion:        1,
		ContentHash:         "hash",
		Platform:            "Gate",
		FetchMode:           "utls_proxy_json",
		Title:               "Gate Launchpool",
		ActivityType:        "launchpool",
		Summary:             longSummary,
		SourceURL:           "https://gate.example/api/list.json",
		DedupeKey:           "gate|launchpool|99",
		TriggerTime:         time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC),
		DecisionBaseURL:     "https://dashboard.example.test/activity",
		DecisionTokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	if !strings.Contains(string(payload), "已截断") {
		t.Fatalf("long content should be truncated: %s", payload)
	}
	if !strings.Contains(string(payload), "打开源数据") {
		t.Fatalf("json/api source should be labelled source data: %s", payload)
	}
	if strings.Contains(string(payload), "/activity/activity/events/99") || !strings.Contains(string(payload), "https://dashboard.example.test/activity/events/99") {
		t.Fatalf("dashboard base URL should be normalized: %s", payload)
	}
}

func TestRenderActivityCardFormatsMarkdownDocumentationContent(t *testing.T) {
	payload, err := RenderActivityEventAlertPostMessage(ActivityEventCard{
		EventID:             183,
		EventVersion:        1,
		ContentHash:         "lighter-hash",
		Platform:            "lighter",
		FetchMode:           "markdown_doc",
		Title:               "Points Program",
		Summary:             "# Points Program\n\nLighter Season 2 points will be distributed every Friday.<br>Earn points by running organic trading strategies via UI and API.\n\n---\n\n# Agent Instructions: Querying This Documentation\n\nPerform an HTTP GET request on the current page URL with the `ask` query parameter:\n```\nGET https://docs.lighter.xyz/points-program.md?ask=<question>\n```",
		SourceURL:           "https://docs.lighter.xyz/points-program.md",
		DedupeKey:           "lighter|incentive_docs|points",
		TriggerTime:         time.Date(2026, 6, 5, 8, 0, 0, 0, time.UTC),
		DecisionBaseURL:     "https://dashboard.example.test/activity",
		DecisionTokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	payloadText := string(payload)
	if strings.Count(payloadText, "# Points Program") != 1 {
		t.Fatalf("markdown title should only appear as the card title, not duplicated in content: %s", payload)
	}
	for _, forbidden := range []string{"Agent Instructions", "Querying This Documentation", "?ask=<question>", "```", "\n---\n"} {
		if strings.Contains(payloadText, forbidden) {
			t.Fatalf("markdown documentation boilerplate should be removed %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(payloadText, "Lighter Season 2 points will be distributed every Friday.") ||
		!strings.Contains(payloadText, "Earn points by running organic trading strategies") {
		t.Fatalf("activity markdown body should remain readable: %s", payload)
	}
	if !strings.Contains(payloadText, `\nEarn points`) {
		t.Fatalf("markdown line breaks should be preserved for Lark rendering: %s", payload)
	}
}

func TestRenderActivityDailyDigestPostMessageUsesAggregateButtonsOnly(t *testing.T) {
	payload, err := RenderActivityDailyDigestPostMessage(ActivityDigestCard{
		DigestKey:           "2026-06-05",
		Title:               "2026-06-05 竞品活动雷达",
		NewCount:            12,
		UpdatedCount:        5,
		EndingSoonCount:     3,
		ReviewPendingCount:  4,
		RawEventCount:       12,
		RichFieldCount:      4,
		AutoNotifiableCount: 9,
		TriggerTime:         time.Date(2026, 6, 5, 0, 10, 0, 0, time.UTC),
		DashboardBaseURL:    "https://dashboard.example.test",
		ReviewQueueURL:      "https://dashboard.example.test/activity?review_status=pending",
		BulkDecisionURL:     "https://dashboard.example.test/activity?bulk=decision",
		Rows: []ActivityDigestRow{
			{SeverityColor: "red", Platform: "Binance", ActivityType: "launchpool", Title: "Stake BNB to Earn ABC", Summary: "Binance opened a new Launchpool campaign.", SourceHealth: SourceStatusOK, RichLine: "奖池 300,000 USDT · confirmed"},
			{SeverityColor: "orange", Platform: "MEXC", ActivityType: "futures_trading_competition", Title: "M-Day campaign", Summary: "reward/time 候选低置信", SourceHealth: SourceStatusDegraded},
		},
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if countCardButtons(decoded) != 3 {
		t.Fatalf("daily digest should render 3 aggregate CTAs only: %s", payload)
	}
	if strings.Contains(string(payload), "立即跟进") || strings.Contains(string(payload), "Top30") {
		t.Fatalf("daily digest must not render decision buttons or Top30 context: %s", payload)
	}
}

func TestRenderActivityEventUpdatePostMessageRendersDecisionButtons(t *testing.T) {
	payload, err := RenderActivityEventUpdatePostMessage(ActivityEventUpdateCard{
		ActivityEventCard: ActivityEventCard{
			EventID:             88,
			EventVersion:        3,
			ContentHash:         "hash-v3",
			Platform:            "Binance",
			SourceGroup:         "cms_article_detail",
			FetchMode:           "http_direct",
			SourceHealth:        SourceStatusOK,
			Title:               "Binance Launchpool: Stake BNB to Earn ABC",
			ActivityType:        "launchpool",
			Summary:             "Activity window and reward pool updated.",
			SourceURL:           "https://binance.example/update",
			DedupeKey:           "binance|cms|abc",
			TriggerTime:         time.Date(2026, 6, 5, 12, 30, 0, 0, time.UTC),
			DecisionBaseURL:     "https://dashboard.example.test",
			DecisionTokenSecret: "secret",
		},
		ChangeSummary: "活动窗口 / 奖池字段更新",
		OldDecision:   "对标观察 · 已标记 stale",
		ChangeLines:   []string{"窗口：06-05 → 06-12 更新为 06-05 → 06-15", "奖池：300,000 USDT 更新为 500,000 USDT · confirmed"},
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if countCardButtons(decoded) != 7 || !bytes.Contains(payload, []byte("运营活动更新")) {
		t.Fatalf("update card contract mismatch: %s", payload)
	}
}

func TestRenderActivityWeeklyDigestPostMessageUsesAggregateButtonsOnly(t *testing.T) {
	payload, err := RenderActivityWeeklyDigestPostMessage(ActivityWeeklyDigestCard{
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
		TriggerTime:            time.Date(2026, 6, 8, 0, 30, 0, 0, time.UTC),
		DashboardBaseURL:       "https://dashboard.example.test",
		Rows: []string{
			"<font color='red'>●</font> 新玩法：多池 Launchpool + trading threshold 组合增多",
			"<font color='orange'>●</font> 奖池：Binance / Gate / MEXC 明显抬升",
		},
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if countCardButtons(decoded) != 3 || strings.Contains(string(payload), "立即跟进") {
		t.Fatalf("weekly digest should render aggregate CTAs only: %s", payload)
	}
}

func TestRenderActivitySourceHealthPostMessageUsesHealthButtonsOnly(t *testing.T) {
	payload, err := RenderActivitySourceHealthPostMessage(ActivitySourceHealthCard{
		Platform:         "Gate",
		SourceGroup:      "launchpool_project_list",
		FetchMode:        "utls_proxy_json",
		Status:           SourceStatusBlocked,
		ErrorKind:        "schema_drift",
		HTTPStatus:       "200",
		Impact:           "Gate Launchpool 自动推送暂停",
		DisabledUntilUTC: "2026-06-05 10:30 UTC",
		TriggerTime:      time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC),
		SourceHealthURL:  "https://dashboard.example.test/activity/source-health",
		RunnerReportURL:  "https://dashboard.example.test/activity/runs/gate",
	})
	if err != nil {
		t.Fatalf("render err=%v", err)
	}
	assertActivityCardContract(t, payload)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if countCardButtons(decoded) != 2 || strings.Contains(string(payload), "立即跟进") {
		t.Fatalf("source-health should render 2 maintenance CTAs only: %s", payload)
	}
}

func assertActivityCardContract(t *testing.T, payload []byte) {
	t.Helper()
	if !bytes.Contains(payload, []byte(`"msg_type":"interactive"`)) {
		t.Fatalf("msg_type must be interactive: %s", payload)
	}
	if bytes.Contains(payload, []byte(`"text":"`)) {
		t.Fatalf("plain_text/lark_md fields must use content, not text: %s", payload)
	}
	if !bytes.Contains(payload, []byte(`"wide_screen_mode":true`)) {
		t.Fatalf("wide_screen_mode missing: %s", payload)
	}
}

func countCardButtons(node any) int {
	switch v := node.(type) {
	case map[string]any:
		count := 0
		if v["tag"] == "button" {
			count++
		}
		for _, child := range v {
			count += countCardButtons(child)
		}
		return count
	case []any:
		count := 0
		for _, child := range v {
			count += countCardButtons(child)
		}
		return count
	default:
		return 0
	}
}
