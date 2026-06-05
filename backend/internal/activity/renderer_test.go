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
}

func TestRenderActivityReviewRequiredPostMessageRequiresFiveDecisionButtons(t *testing.T) {
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
	if actions != 5 {
		t.Fatalf("button count=%d want 5; payload=%s", actions, payload)
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
