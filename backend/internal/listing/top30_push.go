package listing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/config"
)

// top30CoverageDenominator is the fixed denominator for the
// "X/Y 平台" summary field on the Lark card. The Top30 cross-tabulation
// always considers the same 9 competitor exchanges (per
// repos/edgex-dashboard/AGENTS.md), independent of the listed_universe.
const top30CoverageDenominator = 9

// Top30RowForPush is the per-row projection of t_top30_snapshot used
// by the push producer. The CoinGecko / Top30 collector writes the
// raw snapshot; this struct narrows it down to fields the listing
// delivery worker actually consumes.
type Top30RowForPush struct {
	Platform        string
	Symbol          string
	Rank            int
	Volume24HUSD    float64
	CoverageCount   int
	EdgexListed     *bool
	SuggestedAction string
	SnapshotTS      time.Time
}

// Top30PlatformEvidence is one entry inside the per-symbol push
// event. Sorted by Rank ascending so the rendered card surfaces the
// strongest platform first.
type Top30PlatformEvidence struct {
	Platform     string  `json:"platform"`
	Rank         int     `json:"rank"`
	Volume24HUSD float64 `json:"volume_24h_usd"`
}

// Top30PushEvent is the aggregated payload that becomes a single
// outbox row + Lark card. Display symbol is the t_top30_snapshot
// symbol verbatim so the rendered card matches what operators see on
// the Top30 tab.
type Top30PushEvent struct {
	Symbol       string                  `json:"symbol"`
	Action       string                  `json:"action"`
	MaxCoverage  int                     `json:"max_coverage"`
	Platforms    []Top30PlatformEvidence `json:"platforms"`
	DashboardURL string                  `json:"dashboard_url"`
	SnapshotDate string                  `json:"snapshot_date"`
	DedupeKey    string                  `json:"dedupe_key"`
	// StreakDays is the consecutive-day run length up to and
	// including this push. 1 = first time today (renders as 🆕 NEW),
	// >=2 renders as "已第 N 天在榜". Filled by ProduceTop30Push
	// before render; BuildTop30PushEvents leaves it at zero.
	StreakDays int `json:"streak_days,omitempty"`
	// TriggerTime is the wall clock at which the push was produced;
	// surfaced in the card footer so operators distinguish "alert
	// fired now" from "snapshot was taken a moment earlier".
	TriggerTime time.Time `json:"trigger_time,omitempty"`
}

// Eligible Top30 push actions. Anything outside this set is treated
// as a non-actionable row and never produces a push.
var top30PushActions = map[string]struct{}{
	"优先上架": {},
	"评估上架": {},
}

// BuildTop30PushEvents groups eligible rows by display symbol and
// action, sorts platforms by rank, and computes the dedupe key. The
// caller decides what to do with the events (DB insert + outbox).
func BuildTop30PushEvents(rows []Top30RowForPush, day time.Time) []Top30PushEvent {
	type key struct{ symbol, action string }
	groups := make(map[key]*Top30PushEvent)
	order := make([]key, 0)
	dayKey := day.UTC().Format("2006-01-02")
	for _, row := range rows {
		if row.EdgexListed == nil || *row.EdgexListed {
			continue
		}
		if _, ok := top30PushActions[row.SuggestedAction]; !ok {
			continue
		}
		k := key{row.Symbol, row.SuggestedAction}
		ev, ok := groups[k]
		if !ok {
			ev = &Top30PushEvent{
				Symbol:       row.Symbol,
				Action:       row.SuggestedAction,
				SnapshotDate: dayKey,
				DedupeKey:    fmt.Sprintf("top30_hot_gap|%s|%s|%s", row.Symbol, row.SuggestedAction, dayKey),
			}
			groups[k] = ev
			order = append(order, k)
		}
		if row.CoverageCount > ev.MaxCoverage {
			ev.MaxCoverage = row.CoverageCount
		}
		ev.Platforms = append(ev.Platforms, Top30PlatformEvidence{
			Platform:     row.Platform,
			Rank:         row.Rank,
			Volume24HUSD: row.Volume24HUSD,
		})
	}
	out := make([]Top30PushEvent, 0, len(order))
	for _, k := range order {
		ev := groups[k]
		sort.Slice(ev.Platforms, func(i, j int) bool { return ev.Platforms[i].Rank < ev.Platforms[j].Rank })
		out = append(out, *ev)
	}
	return out
}

// RenderTop30PostMessage returns the JSON body the Lark / Feishu
// webhook expects. The output is an interactive card (msg_type=
// interactive) with three regions:
//
//  1. Header — action-coloured (优先上架=red, 评估上架=blue) plus a
//     streak badge (🆕 NEW or "已第 N 天在榜");
//  2. Summary fields (2x2 grid) and the per-platform rank list with
//     tier emojis (⭐ ≤10 / 🔸 ≤20 / · ≥21) and a ⚠ glyph for
//     near-the-edge ranks;
//  3. A URL-only "查看 Top30 详情" button plus a footer note carrying
//     the trigger timestamp and dedupe key.
//
// The card has no callback buttons by design: the original product
// requirement is a monitoring alert, not a decision flow, so we keep
// the integration surface to a plain incoming webhook.
func RenderTop30PostMessage(ev Top30PushEvent) ([]byte, error) {
	body := map[string]any{
		"msg_type": "interactive",
		"card":     buildTop30Card(ev),
	}
	// We deliberately turn off the default HTML-escaping so the lark_md
	// `<font color='...'>` tags reach Lark as readable bytes rather
	// than \u003cfont\u003e. Lark accepts both forms, but the
	// unescaped wire payload is what shows up in delivery logs and
	// matches the dry-run preview the operator inspects on stdout.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

func buildTop30Card(ev Top30PushEvent) map[string]any {
	elements := []any{
		buildTop30Headline(ev),
		buildTop30SummaryFields(ev),
		map[string]any{"tag": "hr"},
		buildTop30PlatformList(ev),
	}
	if action := buildTop30ActionRow(ev); action != nil {
		elements = append(elements, map[string]any{"tag": "hr"}, action)
	}
	elements = append(elements, buildTop30FooterNote(ev))
	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": top30HeaderTemplate(ev.Action),
			// Lark interactive cards expect plain_text elements to
			// carry their string in `content`. Earlier revisions used
			// `text`, which silently dropped the entire header bar
			// and emptied the action button at runtime even though
			// Lark accepted the surrounding card without an error.
			"title": map[string]any{
				"tag":     "plain_text",
				"content": top30HeaderText(ev),
			},
		},
		"elements": elements,
	}
}

// top30HeaderTemplate maps the recommended action to a Lark card
// header colour. Anything outside the known set falls back to grey so
// the card still renders rather than being rejected by Lark.
func top30HeaderTemplate(action string) string {
	switch action {
	case "优先上架":
		return "red"
	case "评估上架":
		return "blue"
	default:
		return "grey"
	}
}

// top30HeaderText is the per-card header string. The header acts as a
// stable category label ("Top 30 热门标的 · {action}") rather than a
// symbol-specific banner, so a noisy alert stream stays scannable
// even when the same Lark group receives many cards in a row. The
// symbol, streak badge, and per-token detail live in the body.
func top30HeaderText(ev Top30PushEvent) string {
	action := strings.TrimSpace(ev.Action)
	if action == "" {
		action = "上架观察"
	}
	return "📊 Top 30 热门标的 · " + action
}

// buildTop30Headline renders the body's first row: an H1-bold display
// symbol on its own line, followed by the streak badge on the next
// line. Splitting the lines lets the badge stay visible even on
// narrow Lark mobile cards where a long display symbol would
// otherwise wrap and bury the badge.
func buildTop30Headline(ev Top30PushEvent) map[string]any {
	parts := []string{"# " + ev.Symbol}
	if badge := top30StreakBadge(ev.StreakDays); badge != "" {
		colour := "blue"
		if ev.StreakDays >= 2 {
			colour = "grey"
		}
		parts = append(parts, "<font color='"+colour+"'>"+badge+"</font>")
	}
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": strings.Join(parts, "\n"),
		},
	}
}

// top30StreakBadge renders the per-day streak badge in the header.
// Day 1 (the first time today's push fires for this symbol+action)
// surfaces 🆕 NEW so operators do not miss genuinely new hot tokens
// in a noisy alert stream. From Day 2 onwards the badge counts the
// run length so persistent items are visibly distinguished from
// one-off spikes.
func top30StreakBadge(days int) string {
	switch {
	case days <= 0:
		return ""
	case days == 1:
		return "🆕 NEW"
	default:
		return fmt.Sprintf("已第 %d 天在榜", days)
	}
}

func buildTop30SummaryFields(ev Top30PushEvent) map[string]any {
	totalVolume, strongest := top30Aggregate(ev.Platforms)
	strongestText := "—"
	if strongest != nil {
		strongestText = fmt.Sprintf("%s #%d", strongest.Platform, strongest.Rank)
	}
	fields := []any{
		summaryField("覆盖", fmt.Sprintf("%d/%d 平台", ev.MaxCoverage, top30CoverageDenominator)),
		summaryField("24h 合计", "$"+humanUSD(totalVolume)),
		summaryField("最强", strongestText),
		summaryField("edgeX", "未上线"),
	}
	return map[string]any{
		"tag":    "div",
		"fields": fields,
	}
}

func summaryField(label, value string) map[string]any {
	return map[string]any{
		"is_short": true,
		"text": map[string]any{
			"tag":     "lark_md",
			"content": fmt.Sprintf("**%s**\n%s", label, value),
		},
	}
}

// top30Aggregate sums 24h volume across the per-platform evidences
// and returns the strongest (lowest-rank) entry. The slice is sorted
// by rank ascending in BuildTop30PushEvents, but we recompute the
// strongest defensively so callers can render partial slices.
func top30Aggregate(platforms []Top30PlatformEvidence) (float64, *Top30PlatformEvidence) {
	total := 0.0
	var strongest *Top30PlatformEvidence
	for i := range platforms {
		total += platforms[i].Volume24HUSD
		if strongest == nil || platforms[i].Rank < strongest.Rank {
			strongest = &platforms[i]
		}
	}
	return total, strongest
}

func buildTop30PlatformList(ev Top30PushEvent) map[string]any {
	var lines []string
	for _, p := range ev.Platforms {
		lines = append(lines, formatTop30PlatformLine(p))
	}
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": strings.Join(lines, "\n"),
		},
	}
}

// formatTop30PlatformLine renders one bullet for the per-platform
// list. We avoid emoji for the rank-tier indicator because Lark
// desktop falls back to a non-colour glyph font for several common
// emoji code points (⭐ / 🔸 render as orange diamond stand-ins on
// macOS Lark, which looks broken). Instead each row is prefixed with
// a coloured bullet via lark_md `<font color>` markup, which Lark
// renders consistently across desktop and mobile. The bullet colour
// alone conveys tier — earlier revisions appended a "(边缘)" suffix
// for rank ≥ 25 but it cluttered the line; the grey bullet already
// signals the same information.
func formatTop30PlatformLine(p Top30PlatformEvidence) string {
	return fmt.Sprintf("%s **%s** · rank **#%d** · 24h $%s",
		rankTierBullet(p.Rank), p.Platform, p.Rank, humanUSD(p.Volume24HUSD))
}

// rankTierBullet returns a coloured filled-circle prefix that maps
// rank-tier to colour:
//   - red    for top-tier (rank ≤ 10): the "really hot" platforms;
//   - orange for mid-tier (rank 11..20): noteworthy;
//   - grey   for low-tier (rank ≥ 21): edge-of-Top30 evidence.
func rankTierBullet(rank int) string {
	switch {
	case rank <= 10:
		return "<font color='red'>●</font>"
	case rank <= 20:
		return "<font color='orange'>●</font>"
	default:
		return "<font color='grey'>●</font>"
	}
}

// buildTop30ActionRow assembles the bottom action row. The primary
// action is the dashboard deep-link (when configured); the secondary
// action is the K-line page chosen by chooseTop30KlineButton (Binance
// preferred when in platforms, otherwise the strongest non-binance
// platform whose K-line URL pattern is known).
func buildTop30ActionRow(ev Top30PushEvent) map[string]any {
	var actions []any
	if dash := strings.TrimSpace(ev.DashboardURL); dash != "" {
		actions = append(actions, map[string]any{
			"tag":  "button",
			"type": "primary",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": "📊 查看 Top30 详情",
			},
			"url": dash,
		})
	}
	if klineLabel, klineURL := chooseTop30KlineButton(ev); klineURL != "" {
		actions = append(actions, map[string]any{
			"tag":  "button",
			"type": "default",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": klineLabel,
			},
			"url": klineURL,
		})
	}
	if len(actions) == 0 {
		return nil
	}
	return map[string]any{
		"tag":     "action",
		"actions": actions,
	}
}

// splitDisplaySymbol parses "BEAT-USDT (perp)" / "ETH/USDC" /
// "BEAT-USDT" into upper-case (base, quote). Returns ok=false on
// shapes that lack a separator (e.g. hyperliquid-style single-base
// rows that already get pre-handled by the caller).
func splitDisplaySymbol(symbol string) (base, quote string, ok bool) {
	s := strings.TrimSpace(symbol)
	if i := strings.IndexAny(s, " ("); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	switch {
	case strings.Contains(s, "-"):
		parts := strings.SplitN(s, "-", 2)
		base, quote = parts[0], parts[1]
	case strings.Contains(s, "/"):
		parts := strings.SplitN(s, "/", 2)
		base, quote = parts[0], parts[1]
	default:
		return "", "", false
	}
	base = strings.ToUpper(strings.TrimSpace(base))
	quote = strings.ToUpper(strings.TrimSpace(quote))
	if base == "" || quote == "" {
		return "", "", false
	}
	return base, quote, true
}

// buildExchangeKlineURL returns the canonical USDⓢ-M perpetual chart
// page URL for a given (platform, base, quote). Empty string means
// "no template known" — the caller should treat that as a signal to
// move on or fall back. URL patterns were verified against each
// exchange's SEO-canonical product page (see
// docs/feat/listing-agent-top30-hot-gap-push.md "K 线副按钮目标 9 家
// 交易所 URL 模板").
func buildExchangeKlineURL(platform, base, quote string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "binance":
		return "https://www.binance.com/en/futures/" + base + quote
	case "okx":
		return "https://www.okx.com/trade-swap/" + strings.ToLower(base) + "-" + strings.ToLower(quote) + "-swap"
	case "bybit":
		return "https://www.bybit.com/trade/usdt/" + base + quote
	case "bitget":
		return "https://www.bitget.com/futures/usdt/" + base + quote
	case "gate":
		return "https://www.gate.com/futures/USDT/" + base + "_" + quote
	case "mexc":
		return "https://www.mexc.com/futures/" + base + "_" + quote
	case "bingx":
		return "https://bingx.com/en/perpetual/" + base + "-" + quote
	case "hyperliquid":
		return "https://app.hyperliquid.xyz/trade/" + base
	default:
		// e.g. "lighter" — no public K-line page; caller falls back.
		return ""
	}
}

// exchangeDisplayName maps the lower-case adapter id used inside
// Top30PlatformEvidence to a human-friendly label suitable for the
// "📈 <Name> K 线" button text.
func exchangeDisplayName(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "binance":
		return "Binance"
	case "okx":
		return "OKX"
	case "bybit":
		return "Bybit"
	case "bitget":
		return "Bitget"
	case "gate":
		return "Gate"
	case "mexc":
		return "MEXC"
	case "bingx":
		return "BingX"
	case "hyperliquid":
		return "Hyperliquid"
	case "lighter":
		return "Lighter"
	default:
		return strings.TrimSpace(platform)
	}
}

// chooseTop30KlineButton picks the secondary K-line button target.
// Priority:
//  1. binance is in platforms → Binance K 线 (industry-default reference;
//     URL pattern is the most stable; operators look at Binance charts
//     by habit even when binance is the edge platform on a particular
//     symbol — they want a familiar reference price).
//  2. binance is NOT in platforms → walk platforms in rank-ascending
//     order (i.e. strongest first; the slice is pre-sorted by
//     BuildTop30PushEvents) and return the first one with a known URL
//     template. This makes the button congruent with the card body's
//     "最强 <X> #N" summary in the binance-absent case.
//  3. No template matched (e.g. platforms only contains "lighter") →
//     fall back to Binance. Even though binance is not in this
//     symbol's Top30, Binance has likely listed the perpetual; the
//     URL is generally still valid. We accept a small 404 risk over
//     showing no button at all.
//
// Returns ("", "") only when the display symbol cannot be parsed at
// all — in that case buildTop30ActionRow drops the secondary button.
func chooseTop30KlineButton(ev Top30PushEvent) (label, url string) {
	base, quote, ok := splitDisplaySymbol(ev.Symbol)
	if !ok {
		return "", ""
	}

	for _, p := range ev.Platforms {
		if strings.EqualFold(p.Platform, "binance") {
			return "📈 Binance K 线", buildExchangeKlineURL("binance", base, quote)
		}
	}

	for _, p := range ev.Platforms {
		if u := buildExchangeKlineURL(p.Platform, base, quote); u != "" {
			return "📈 " + exchangeDisplayName(p.Platform) + " K 线", u
		}
	}

	return "📈 Binance K 线", buildExchangeKlineURL("binance", base, quote)
}

func buildTop30FooterNote(ev Top30PushEvent) map[string]any {
	parts := []string{"触发时间 " + formatTriggerTime(ev.TriggerTime)}
	if ev.DedupeKey != "" {
		parts = append(parts, ev.DedupeKey)
	}
	// Lark's interactive card schema requires plain_text children of
	// a `note` element to carry their string in `content`, not `text`.
	// (`text` is the field name on plain_text used inside `header.title`
	// and inside `button` children, where the schema is different.)
	return map[string]any{
		"tag": "note",
		"elements": []any{
			map[string]any{
				"tag":     "plain_text",
				"content": strings.Join(parts, " · "),
			},
		},
	}
}

func formatTriggerTime(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// buildDashboardSymbolURL appends a ?symbol=<display_symbol> query so
// the "查看 Top30 详情" button deep-links to the operator's symbol of
// interest. When base is empty (no dashboard configured) the button
// is suppressed by buildTop30ActionRow.
func buildDashboardSymbolURL(base, symbol string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "symbol=" + url.QueryEscape(symbol)
}

func humanUSD(v float64) string {
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", v/1_000_000_000)
	case v >= 1_000_000:
		return fmt.Sprintf("%.2fM", v/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.2fK", v/1_000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// ProduceTop30Push is the public producer entry point. It loads the
// latest Top30 snapshot rows, fail-closes when stale or when the
// listed universe is unavailable, materialises Top30PushEvents, and
// writes the matching signal + outbox rows.
type Top30Deps struct {
	LoadUniverse             func() (*config.ListedUniverse, error)
	Now                      func() time.Time
	DashboardBase            string
	WebhookURL               string
	MaxAttempts              int
	StaleAfter               time.Duration
	AutoQuietAfterStreakDays int
	SendSpacing              time.Duration
}

type Top30PushResult struct {
	Events               int
	Signals              int
	OutboxRows           int
	AutoQuieted          int
	SkippedAlreadyListed int
	FailClosed           string
}

func ProduceTop30Push(ctx context.Context, repo *Repository, deps Top30Deps) (Top30PushResult, error) {
	if repo == nil {
		return Top30PushResult{}, errors.New("listing top30 push: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	universe, err := deps.LoadUniverse()
	if err != nil {
		return Top30PushResult{FailClosed: "universe_load_error"}, fmt.Errorf("load universe: %w", err)
	}
	if universe == nil || !universe.Loaded() || len(universe.BaseAssets("edgeX")) == 0 {
		return Top30PushResult{FailClosed: "universe_not_loaded"}, nil
	}
	now := deps.Now()
	rows, latest, err := repo.loadTop30LatestRows(ctx)
	if err != nil {
		return Top30PushResult{}, fmt.Errorf("load top30 rows: %w", err)
	}
	if latest.IsZero() {
		return Top30PushResult{FailClosed: "no_snapshot"}, nil
	}
	stale := deps.StaleAfter
	if stale <= 0 {
		stale = 30 * time.Minute
	}
	if now.Sub(latest) > stale {
		return Top30PushResult{FailClosed: "snapshot_stale"}, nil
	}
	events := BuildTop30PushEvents(rows, latest)
	result := Top30PushResult{Events: len(events)}
	// outboxBatchIdx tracks the index among events that ACTUALLY get
	// inserted into the outbox in this pass. It is the input to
	// SendSpacing staggering, so that auto-quieted events do not
	// "consume" a slot in the staggering schedule.
	outboxBatchIdx := 0
	for i := range events {
		ev := &events[i]
		if universe.IsListed("edgeX", extractBase(ev.Symbol)) {
			result.SkippedAlreadyListed++
			continue
		}
		ev.TriggerTime = now
		ev.DashboardURL = buildDashboardSymbolURL(deps.DashboardBase, ev.Symbol)
		// Streak counts consecutive prior days that already emitted a
		// signal for the same (display_symbol, action). The current
		// push has not been written yet, so the badge value is
		// (prior consecutive days + 1).
		priorStreak, err := repo.countTop30Streak(ctx, ev.Symbol, ev.Action, now)
		if err != nil {
			// Streak is a pure UX nicety; do not fail-close the alert
			// just because the lookup hiccuped.
			priorStreak = 0
		}
		ev.StreakDays = priorStreak + 1
		signalPayload, err := json.Marshal(ev)
		if err != nil {
			return Top30PushResult{Events: len(events)}, fmt.Errorf("marshal event: %w", err)
		}
		signal := SignalObservation{
			SignalType:      SignalTop30HotGap,
			SignalSubtype:   ev.Action,
			SourcePlatform:  "top30",
			CanonicalSymbol: strings.ToUpper(extractBase(ev.Symbol)),
			DisplaySymbol:   ev.Symbol,
			MarketSurface:   "perp",
			InstrumentKind:  "canonical",
			ObservedAt:      now,
			Fingerprint:     fmt.Sprintf("top30_hot_gap|%s|%s|%s", ev.Symbol, ev.Action, ev.SnapshotDate),
			PayloadJSON:     signalPayload,
		}
		// Always record the signal observation, even when we auto-quiet
		// the outbox push. The streak counter MUST keep counting so a
		// gap day doesn't reset the streak and re-trigger a NEW-flag
		// push on day N+1.
		if _, _, err := repo.InsertSignal(ctx, signal); err != nil {
			return Top30PushResult{}, fmt.Errorf("insert top30 signal: %w", err)
		}
		result.Signals++
		// Auto-quiet: same (symbol, action) has been pushing for
		// N consecutive days; suppress the alert so the channel does
		// not fatigue. Operators can still see the symbol on the
		// Dashboard Top30 tab.
		if deps.AutoQuietAfterStreakDays > 0 && ev.StreakDays >= deps.AutoQuietAfterStreakDays {
			result.AutoQuieted++
			continue
		}
		outboxPayload, err := RenderTop30PostMessage(*ev)
		if err != nil {
			return Top30PushResult{Events: len(events)}, fmt.Errorf("render top30 post message: %w", err)
		}
		var status string
		if strings.TrimSpace(deps.WebhookURL) == "" {
			status = OutboxStatusDisabled
		} else {
			status = OutboxStatusPending
		}
		maxAttempts := deps.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 5
		}
		// Stagger NextAttemptAt across the kept events. The 0-th row
		// is due immediately; subsequent rows wait i*SendSpacing so the
		// drain worker naturally serializes them across ticks. When
		// SendSpacing is 0 the stagger collapses to "all due now".
		nextAttempt := now
		if deps.SendSpacing > 0 {
			nextAttempt = now.Add(time.Duration(outboxBatchIdx) * deps.SendSpacing)
		}
		if err := repo.insertOutbox(ctx, DeliveryOutbox{
			EventType:     DeliveryEventTop30HotGap,
			DedupeKey:     ev.DedupeKey,
			TargetChannel: DeliveryChannelLarkTop30,
			Status:        status,
			MaxAttempts:   maxAttempts,
			PayloadJSON:   outboxPayload,
			NextAttemptAt: ptrTime(nextAttempt),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return Top30PushResult{}, fmt.Errorf("insert outbox: %w", err)
		}
		result.OutboxRows++
		outboxBatchIdx++
	}
	return result, nil
}

func extractBase(displaySymbol string) string {
	s := strings.TrimSpace(displaySymbol)
	if idx := strings.Index(s, "-"); idx > 0 {
		return s[:idx]
	}
	if idx := strings.Index(s, "/"); idx > 0 {
		return s[:idx]
	}
	if idx := strings.IndexAny(s, " ("); idx > 0 {
		return s[:idx]
	}
	return s
}

func ptrTime(t time.Time) *time.Time { return &t }

// loadTop30LatestRows returns every row tied to the most recent
// snapshot_ts across t_top30_snapshot, plus that timestamp.
func (r *Repository) loadTop30LatestRows(ctx context.Context) ([]Top30RowForPush, time.Time, error) {
	if r.db == nil {
		return nil, time.Time{}, errors.New("listing top30: no db attached")
	}
	var latest sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT MAX(snapshot_ts) FROM t_top30_snapshot`).Scan(&latest); err != nil {
		return nil, time.Time{}, err
	}
	if !latest.Valid {
		return nil, time.Time{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT platform, symbol, rank_no, COALESCE(volume_24h_usd, 0),
		         COALESCE(coverage_count, 0), edgex_listed, suggested_action, snapshot_ts
		    FROM t_top30_snapshot
		   WHERE snapshot_ts = ?`, latest.Time)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var out []Top30RowForPush
	for rows.Next() {
		var row Top30RowForPush
		var listed sql.NullBool
		var action sql.NullString
		var ts time.Time
		if err := rows.Scan(&row.Platform, &row.Symbol, &row.Rank, &row.Volume24HUSD,
			&row.CoverageCount, &listed, &action, &ts); err != nil {
			return nil, time.Time{}, err
		}
		if listed.Valid {
			v := listed.Bool
			row.EdgexListed = &v
		}
		row.SuggestedAction = action.String
		row.SnapshotTS = ts
		out = append(out, row)
	}
	return out, latest.Time, rows.Err()
}

// countTop30Streak returns how many consecutive UTC days *strictly
// before* today already have a top30_hot_gap signal for the given
// (display_symbol, action). The current push is not yet persisted, so
// callers add 1 to convert this prior-streak into a 1-based "Day N"
// badge value (1 = NEW, 2+ = "已第 N 天在榜").
//
// We scan distinct DATE(observed_at) values in descending order and
// stop at the first day that does not equal yesterday, yesterday-1,
// etc. The 60-row cap is a safety net so a pathological history
// cannot force the worker into a long scan.
func (r *Repository) countTop30Streak(ctx context.Context, displaySymbol, action string, today time.Time) (int, error) {
	if r.db == nil {
		return 0, errors.New("listing top30: no db attached")
	}
	todayStr := today.UTC().Format("2006-01-02")
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT DATE(observed_at) AS d
		   FROM t_listing_signal_observation
		  WHERE signal_type    = ?
		    AND signal_subtype = ?
		    AND display_symbol = ?
		    AND DATE(observed_at) < ?
		  ORDER BY d DESC
		  LIMIT 60`,
		SignalTop30HotGap, action, displaySymbol, todayStr,
	)
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
		dStr, err := scanDateString(raw)
		if err != nil {
			return 0, err
		}
		if dStr != expected.Format("2006-01-02") {
			break
		}
		streak++
		expected = expected.AddDate(0, 0, -1)
	}
	return streak, rows.Err()
}

// scanDateString normalises sql.Scan output for a DATE column. MySQL
// drivers may surface DATE as time.Time, []byte, or string depending
// on driver flags; we accept all three and reject anything else
// rather than silently miscount the streak.
func scanDateString(raw any) (string, error) {
	switch v := raw.(type) {
	case time.Time:
		return v.UTC().Format("2006-01-02"), nil
	case []byte:
		return string(v), nil
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("countTop30Streak: unexpected DATE scan type %T", raw)
	}
}

// insertOutbox writes one outbox row. The unique key on dedupe_key
// gives natural idempotency, so callers encode their own idempotency
// window or evidence version into the key and INSERT IGNORE turns
// duplicate producer runs into no-ops.
func (r *Repository) insertOutbox(ctx context.Context, o DeliveryOutbox) error {
	if r.db == nil {
		return errors.New("listing top30: no db attached")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT IGNORE INTO t_listing_delivery_outbox
		   (event_type, dedupe_key, target_channel, status,
		    attempt_count, max_attempts, next_attempt_at, payload_json, last_error,
		    sent_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.EventType, o.DedupeKey, o.TargetChannel, o.Status,
		o.AttemptCount, o.MaxAttempts, nullTimePtr(o.NextAttemptAt),
		[]byte(o.PayloadJSON), nullString(o.LastError),
		nullTimePtr(o.SentAt), o.CreatedAt, o.UpdatedAt,
	)
	return err
}
