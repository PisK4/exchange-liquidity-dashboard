package activity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ActivityEventCard struct {
	EventID             int64
	EventVersion        int
	ContentHash         string
	Platform            string
	SourceGroup         string
	FetchMode           string
	SourceHealth        string
	Title               string
	ActivityType        string
	Summary             string
	SourceURL           string
	DedupeKey           string
	TriggerTime         time.Time
	DecisionBaseURL     string
	DecisionTokenSecret string
	ConfirmedRichLines  []string
	CandidateLines      []string
	ReviewReasons       []string
}

func RenderActivityEventAlertPostMessage(card ActivityEventCard) ([]byte, error) {
	lines := []any{
		div(md("# " + truncate(card.Title, 120))),
		div(md(fmt.Sprintf("<font color='red'>新活动</font> · %s · %s", safeDash(card.ActivityType), safeDash(card.Platform)))),
		fields(
			field("Source", fmt.Sprintf("%s · %s · source %s", safeDash(card.SourceGroup), safeDash(card.FetchMode), safeDash(card.SourceHealth))),
			field("摘要", safeDash(card.Summary)),
			field("原文", linkOrDash(card.SourceURL, "打开原文")),
			field("解析状态", "raw ok · rich fields optional"),
		),
	}
	for _, line := range card.ConfirmedRichLines {
		lines = append(lines, div(md("<font color='blue'>●</font> "+line)))
	}
	lines = append(lines, actionButtons(card)...)
	lines = append(lines, div(md(footer(card.TriggerTime, "activity_event|"+card.DedupeKey))))
	return renderPostMessage("高价值运营活动 · 新发现", "red", lines)
}

func RenderActivityReviewRequiredPostMessage(card ActivityEventCard) ([]byte, error) {
	reasons := strings.Join(card.ReviewReasons, " / ")
	if reasons == "" {
		reasons = "parser_low_confidence"
	}
	lines := []any{
		div(md("# " + truncate(card.Title, 120))),
		div(md("<font color='orange'>需要人工确认</font> · " + reasons)),
		fields(
			field("平台", safeDash(card.Platform)),
			field("Source", fmt.Sprintf("%s · %s", safeDash(card.SourceGroup), safeDash(card.FetchMode))),
			field("主要原因", reasons),
			field("建议动作", "选择运营动作；确认页会同时完成事实审核"),
		),
		div(md("<font color='grey'>●</font> Raw 摘要：" + safeDash(card.Summary))),
	}
	for _, line := range card.CandidateLines {
		lines = append(lines, div(md("<font color='orange'>●</font> "+line)))
	}
	lines = append(lines, div(md("<font color='grey'>●</font> 原文："+linkOrDash(card.SourceURL, "打开原文"))))
	lines = append(lines, actionButtons(card)...)
	lines = append(lines, div(md(footer(card.TriggerTime, "activity_review_required|"+card.DedupeKey))))
	return renderPostMessage("运营活动待复核", "purple", lines)
}

func renderPostMessage(title, color string, elements []any) ([]byte, error) {
	payload := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]any{"wide_screen_mode": true},
			"header": map[string]any{
				"template": color,
				"title":    plain(title),
			},
			"elements": elements,
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func actionButtons(card ActivityEventCard) []any {
	actions := []struct {
		label  string
		action string
	}{
		{"立即跟进", DecisionFollowNow},
		{"对标观察", DecisionBenchmarkWatch},
		{"差异化设计", DecisionDifferentiate},
		{"暂不跟进", DecisionNoFollow},
		{"忽略/重复", DecisionIgnoreDuplicate},
	}
	buttons := make([]any, 0, len(actions))
	for _, action := range actions {
		buttons = append(buttons, map[string]any{
			"tag":  "button",
			"text": plain(action.label),
			"type": "default",
			"url":  decisionURL(card, action.action),
		})
	}
	return []any{map[string]any{"tag": "action", "actions": buttons}}
}

func decisionURL(card ActivityEventCard, action string) string {
	base := strings.TrimRight(card.DecisionBaseURL, "/")
	if base == "" {
		base = "/"
	}
	claims := DecisionTokenClaims{
		EventID:      card.EventID,
		EventVersion: card.EventVersion,
		ContentHash:  card.ContentHash,
		Action:       action,
		ExpiresAt:    card.TriggerTime.Add(30 * 24 * time.Hour),
	}
	token, _ := GenerateDecisionToken(claims, card.DecisionTokenSecret)
	q := url.Values{}
	q.Set("action", action)
	q.Set("version", strconv.Itoa(card.EventVersion))
	q.Set("token", token)
	return fmt.Sprintf("%s/activity/events/%d/decision?%s", base, card.EventID, q.Encode())
}

func fields(items ...map[string]any) map[string]any {
	return map[string]any{"tag": "div", "fields": items}
}

func field(label, value string) map[string]any {
	return map[string]any{"is_short": true, "text": md("**" + label + "**\n" + value)}
}

func div(content map[string]any) map[string]any {
	return map[string]any{"tag": "div", "text": content}
}

func plain(content string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": content}
}

func md(content string) map[string]any {
	return map[string]any{"tag": "lark_md", "content": content}
}

func footer(t time.Time, key string) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return fmt.Sprintf("触发时间 %s · %s", t.UTC().Format("2006-01-02 15:04 UTC"), key)
}

func truncate(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max-1]) + "…"
}

func safeDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return strings.TrimSpace(s)
}

func linkOrDash(raw, label string) string {
	if strings.TrimSpace(raw) == "" {
		return "-"
	}
	return fmt.Sprintf("[%s](%s)", label, raw)
}
