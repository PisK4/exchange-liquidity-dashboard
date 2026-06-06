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

type ActivityDigestCard struct {
	DigestKey           string
	Title               string
	NewCount            int
	UpdatedCount        int
	EndingSoonCount     int
	ReviewPendingCount  int
	RawEventCount       int
	RichFieldCount      int
	AutoNotifiableCount int
	TriggerTime         time.Time
	DashboardBaseURL    string
	ReviewQueueURL      string
	BulkDecisionURL     string
	Rows                []ActivityDigestRow
}

type ActivityDigestRow struct {
	SeverityColor string
	Platform      string
	ActivityType  string
	Title         string
	Summary       string
	SourceHealth  string
	RichLine      string
}

type ActivityEventUpdateCard struct {
	ActivityEventCard
	ChangeSummary string
	OldDecision   string
	ChangeLines   []string
}

type ActivityWeeklyDigestCard struct {
	DigestKey              string
	Title                  string
	RawActivityCount       int
	RichConfirmedCount     int
	NewPlaybookCount       int
	PendingSuggestionCount int
	HotActivityTypes       string
	RewardPoolTrend        string
	ThemeSummary           string
	DecisionSummary        string
	TriggerTime            time.Time
	DashboardBaseURL       string
	Rows                   []string
}

type ActivitySourceHealthCard struct {
	Platform         string
	SourceGroup      string
	FetchMode        string
	Status           string
	ErrorKind        string
	HTTPStatus       string
	Impact           string
	DisabledUntilUTC string
	TriggerTime      time.Time
	SourceHealthURL  string
	RunnerReportURL  string
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

func RenderActivityDailyDigestPostMessage(card ActivityDigestCard) ([]byte, error) {
	title := card.Title
	if title == "" {
		title = card.DigestKey + " 竞品活动雷达"
	}
	lines := []any{
		div(md("# " + truncate(title, 120))),
		div(md(fmt.Sprintf("新增 %d · 更新 %d · 即将结束 %d · 待复核 %d", card.NewCount, card.UpdatedCount, card.EndingSoonCount, card.ReviewPendingCount))),
		fields(
			field("Raw events", strconv.Itoa(card.RawEventCount)),
			field("Rich fields", strconv.Itoa(card.RichFieldCount)),
			field("可自动通知", strconv.Itoa(card.AutoNotifiableCount)),
			field("Review pending", strconv.Itoa(card.ReviewPendingCount)),
		),
	}
	for _, row := range card.Rows {
		color := safeColor(row.SeverityColor, "blue")
		lines = append(lines, div(md(fmt.Sprintf("<font color='%s'>●</font> **%s** · %s · %s", color, safeDash(row.Platform), safeDash(row.ActivityType), safeDash(row.Title)))))
		lines = append(lines, div(md(fmt.Sprintf("　 摘要：%s · source %s", safeDash(row.Summary), safeDash(row.SourceHealth)))))
		if strings.TrimSpace(row.RichLine) != "" {
			lines = append(lines, div(md("　 Rich: "+row.RichLine)))
		}
	}
	lines = append(lines, ctaButtons([]cardCTA{
		{Label: "查看 Activity Radar", URL: firstNonEmpty(card.DashboardBaseURL, "/activity")},
		{Label: "查看待复核队列", URL: firstNonEmpty(card.ReviewQueueURL, "/activity?review_status=pending")},
		{Label: "批量判断", URL: firstNonEmpty(card.BulkDecisionURL, "/activity?bulk=decision")},
	})...)
	lines = append(lines, div(md(footer(card.TriggerTime, "activity_daily_digest|"+safeDash(card.DigestKey)))))
	return renderPostMessage("运营活动雷达 · 日报", "blue", lines)
}

func RenderActivityEventUpdatePostMessage(card ActivityEventUpdateCard) ([]byte, error) {
	lines := []any{
		div(md("# " + truncate(card.Title, 120))),
		div(md(fmt.Sprintf("<font color='orange'>关键字段变化</font> · event_version=%d", card.EventVersion))),
		fields(
			field("Source", fmt.Sprintf("%s · %s · source %s", safeDash(card.SourceGroup), safeDash(card.FetchMode), safeDash(card.SourceHealth))),
			field("变化摘要", safeDash(card.ChangeSummary)),
			field("原文", linkOrDash(card.SourceURL, "打开原文")),
			field("旧决策", safeDash(card.OldDecision)),
		),
	}
	for _, line := range card.ChangeLines {
		lines = append(lines, div(md("<font color='orange'>●</font> "+line)))
	}
	lines = append(lines, actionButtons(card.ActivityEventCard)...)
	lines = append(lines, div(md(footer(card.TriggerTime, fmt.Sprintf("activity_event_update|%d|v%d", card.EventID, card.EventVersion)))))
	return renderPostMessage("运营活动更新 · 需要重新判断", "orange", lines)
}

func RenderActivityWeeklyDigestPostMessage(card ActivityWeeklyDigestCard) ([]byte, error) {
	title := card.Title
	if title == "" {
		title = card.DigestKey + " 竞品运营趋势"
	}
	lines := []any{
		div(md("# " + truncate(title, 120))),
		div(md(fmt.Sprintf("Raw 活动 %d · Rich-confirmed %d · 新玩法候选 %d · 待确认建议 %d", card.RawActivityCount, card.RichConfirmedCount, card.NewPlaybookCount, card.PendingSuggestionCount))),
		fields(
			field("最热活动类型", safeDash(card.HotActivityTypes)),
			field("奖池变化", safeDash(card.RewardPoolTrend)),
			field("活动主题", safeDash(card.ThemeSummary)),
			field("人工决策", safeDash(card.DecisionSummary)),
		),
	}
	for _, row := range card.Rows {
		lines = append(lines, div(md(row)))
	}
	base := firstNonEmpty(card.DashboardBaseURL, "/activity")
	lines = append(lines, ctaButtons([]cardCTA{
		{Label: "查看周报详情", URL: strings.TrimRight(base, "/") + "/activity?digest=weekly"},
		{Label: "查看活动列表", URL: strings.TrimRight(base, "/") + "/activity"},
		{Label: "批量判断", URL: strings.TrimRight(base, "/") + "/activity?bulk=decision"},
	})...)
	lines = append(lines, div(md(footer(card.TriggerTime, "activity_weekly_digest|"+safeDash(card.DigestKey)))))
	return renderPostMessage("运营活动雷达 · 周报草稿", "blue", lines)
}

func RenderActivitySourceHealthPostMessage(card ActivitySourceHealthCard) ([]byte, error) {
	color := "orange"
	if card.Status == SourceStatusBlocked {
		color = "red"
	} else if card.Status == SourceStatusOK {
		color = "green"
	}
	lines := []any{
		div(md("# " + safeDash(card.Platform) + " " + safeDash(card.SourceGroup))),
		div(md(fmt.Sprintf("<font color='%s'>%s</font> · fetch_mode=%s", color, safeDash(card.ErrorKind), safeDash(card.FetchMode)))),
		fields(
			field("最近状态", safeDash(card.Status)),
			field("HTTP / Error", safeDash(card.HTTPStatus)+" / "+safeDash(card.ErrorKind)),
			field("影响", safeDash(card.Impact)),
			field("降级", "disabled_until="+safeDash(card.DisabledUntilUTC)),
		),
	}
	lines = append(lines, ctaButtons([]cardCTA{
		{Label: "查看 source health", URL: firstNonEmpty(card.SourceHealthURL, "/activity/source-health")},
		{Label: "打开 runner report", URL: firstNonEmpty(card.RunnerReportURL, "/activity")},
	})...)
	lines = append(lines, div(md(footer(card.TriggerTime, "activity_source_health|"+strings.ToLower(card.Platform)+"|"+card.SourceGroup))))
	return renderPostMessage("Activity Source Health · "+safeDash(card.ErrorKind), color, lines)
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

type cardCTA struct {
	Label string
	URL   string
}

func ctaButtons(ctas []cardCTA) []any {
	buttons := make([]any, 0, len(ctas))
	for _, cta := range ctas {
		buttons = append(buttons, map[string]any{
			"tag":  "button",
			"text": plain(cta.Label),
			"type": "default",
			"url":  cta.URL,
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

func safeColor(color, fallback string) string {
	switch color {
	case "red", "orange", "blue", "grey", "green", "purple":
		return color
	default:
		return fallback
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
