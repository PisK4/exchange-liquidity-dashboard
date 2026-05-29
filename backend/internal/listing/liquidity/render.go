package liquidity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// CardPayload is everything the renderer needs to draw one Lark
// interactive card. It is the JSON shape persisted into
// t_listing_delivery_outbox.payload_json so the delivery worker can
// re-render or audit the card later without re-querying t_orderbook_snapshot.
type CardPayload struct {
	Kind             AlertKind          `json:"kind"`
	Phase            string             `json:"phase"`
	Canonical        string             `json:"canonical"`
	DisplaySymbol    string             `json:"display_symbol,omitempty"`
	Tier             string             `json:"tier,omitempty"`
	SeveritySeq      int                `json:"severity_seq"`
	ReissueIdx       int                `json:"reissue_idx,omitempty"`
	FirstTriggeredAt time.Time          `json:"first_triggered_at"`
	EvaluatedAt      time.Time          `json:"evaluated_at"`
	EdgexDepth       float64            `json:"edgex_depth_usd"`
	MedianDepth      float64            `json:"median_depth_usd"`
	Ratio            float64            `json:"ratio"`
	LagThreshold     float64            `json:"lag_threshold"`
	Comparators      int                `json:"comparators"`
	TotalPlatforms   int                `json:"total_platforms"`
	EdgexRank        int                `json:"edgex_rank"`
	Platforms        []AlertPlatformRow `json:"platforms"`
	DashboardURL     string             `json:"dashboard_url,omitempty"`
	DedupeKey        string             `json:"dedupe_key,omitempty"`
}

// RenderLiquidityPostMessage returns the JSON body to POST to the
// Lark webhook. Mirrors top30_push.RenderTop30PostMessage's structure
// so the delivery worker can stay format-agnostic.
func RenderLiquidityPostMessage(card CardPayload) ([]byte, error) {
	body := map[string]any{
		"msg_type": "interactive",
		"card":     buildLiquidityCard(card),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}

func buildLiquidityCard(card CardPayload) map[string]any {
	elements := []any{
		buildLiquidityHeadline(card),
		buildLiquidityKPI(card),
		map[string]any{"tag": "hr"},
		buildLiquidityPlatformList(card),
	}
	if action := buildLiquidityActionRow(card); action != nil {
		elements = append(elements, map[string]any{"tag": "hr"}, action)
	}
	elements = append(elements, buildLiquidityFooter(card))
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": liquidityHeaderTemplate(card),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": liquidityHeaderText(card),
			},
		},
		"elements": elements,
	}
}

// liquidityHeaderTemplate maps (kind, phase) → Lark header color:
//   - clear  → green   (recovery, regardless of kind)
//   - liquidity_lag first/reissue → orange
//   - worst_depth   first/reissue → red
func liquidityHeaderTemplate(card CardPayload) string {
	if card.Phase == PhaseClear {
		return "green"
	}
	switch card.Kind {
	case KindWorstDepth:
		return "red"
	case KindLiquidityLag:
		return "orange"
	}
	return "grey"
}

// liquidityHeaderText is the per-card header string. Header acts as
// a stable category label so the Lark group stays scannable.
func liquidityHeaderText(card CardPayload) string {
	switch {
	case card.Phase == PhaseClear:
		return "🟢 流动性告警恢复"
	case card.Kind == KindLiquidityLag:
		return "📉 流动性落后告警"
	case card.Kind == KindWorstDepth:
		return "🚨 极差深度告警"
	}
	return "流动性告警"
}

// buildLiquidityHeadline is "# {Symbol} · {summary}" with optional
// "持续 Xh · 第 N 次" badge for reissue cards.
func buildLiquidityHeadline(card CardPayload) map[string]any {
	tail := ""
	switch card.Kind {
	case KindLiquidityLag:
		tail = fmt.Sprintf("edgeX %s 深度仅 %s 中位数", card.Tier, formatPercent(card.Ratio))
	case KindWorstDepth:
		tail = fmt.Sprintf("edgeX %s 深度倒数第 %d / %d", card.Tier, card.TotalPlatforms-card.EdgexRank+1, card.TotalPlatforms)
	}
	parts := []string{fmt.Sprintf("# %s · %s", displayHeading(card), tail)}
	if badge := buildPhaseBadge(card); badge != "" {
		parts = append(parts, badge)
	}
	return divLarkMD(strings.Join(parts, "\n"))
}

// displayHeading prefers display symbol with a strip-quote treatment;
// falls back to canonical alone when display symbol is empty.
func displayHeading(card CardPayload) string {
	if d := strings.TrimSpace(card.DisplaySymbol); d != "" {
		// Most display symbols include " (perp)" / " (USDT)" suffixes;
		// pull the base out for the H1 since the kind+tier already
		// disambiguate the surface.
		if idx := strings.IndexAny(d, " ("); idx > 0 {
			return strings.TrimSpace(d[:idx])
		}
		return d
	}
	return card.Canonical
}

func buildPhaseBadge(card CardPayload) string {
	switch card.Phase {
	case PhaseFirst:
		return "<font color='red'>🆕 首次触发</font>"
	case PhaseReissue:
		hrs := int(card.EvaluatedAt.Sub(card.FirstTriggeredAt).Hours())
		if hrs < 1 {
			hrs = 1
		}
		return fmt.Sprintf("<font color='grey'>持续 %dh · 第 %d 次提醒</font>", hrs, card.ReissueIdx+1)
	case PhaseClear:
		hrs := int(card.EvaluatedAt.Sub(card.FirstTriggeredAt).Hours())
		if hrs < 1 {
			hrs = 1
		}
		return fmt.Sprintf("<font color='green'>已恢复 · 持续 %dh 后回归正常</font>", hrs)
	}
	return ""
}

// buildLiquidityKPI is the 2x2 summary grid.
func buildLiquidityKPI(card CardPayload) map[string]any {
	edgex := "$" + humanUSD(card.EdgexDepth)
	median := "$" + humanUSD(card.MedianDepth)
	thresh := "—"
	if card.LagThreshold > 0 {
		thresh = formatPercent(card.LagThreshold)
	}
	rank := fmt.Sprintf("#%d / %d", card.EdgexRank, card.TotalPlatforms)

	fields := []any{
		summaryField("edgeX 深度", edgex),
		summaryField("竞品中位数", median+" ("+formatPercent(card.Ratio)+")"),
		summaryField("可比平台", fmt.Sprintf("%d 家", card.Comparators)),
		summaryField("edgeX 排名", rank+" · 落后阈值 "+thresh),
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

func buildLiquidityPlatformList(card CardPayload) map[string]any {
	if len(card.Platforms) == 0 {
		return divLarkMD("—")
	}
	rows := append([]AlertPlatformRow(nil), card.Platforms...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Rank < rows[j].Rank })
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, formatPlatformLine(r))
	}
	return divLarkMD(strings.Join(lines, "\n"))
}

// formatPlatformLine renders one bullet per platform row:
//   - red ● for edgeX (so the operator's own row is the most
//     attention-grabbing colour regardless of where it lands)
//   - blue ● for rows at or above the median (the "healthy" side)
//   - orange ● for rows below the median (the "lagging" side)
func formatPlatformLine(row AlertPlatformRow) string {
	bullet := "<font color='orange'>●</font>"
	switch {
	case row.IsEdgex:
		bullet = "<font color='red'>●</font>"
	case row.IsMedian:
		bullet = "<font color='blue'>●</font>"
	case row.DepthUSD > 0 && !row.IsEdgex && !row.IsMedian:
		bullet = "<font color='orange'>●</font>"
	}
	suffix := ""
	if row.IsEdgex {
		suffix = " ← edgeX"
	} else if row.IsMedian {
		suffix = " ← 中位数"
	}
	return fmt.Sprintf("%s **%s** · 深度 $%s · #%d%s",
		bullet, row.Platform, humanUSD(row.DepthUSD), row.Rank, suffix)
}

func buildLiquidityActionRow(card CardPayload) map[string]any {
	var actions []any
	if dash := strings.TrimSpace(card.DashboardURL); dash != "" {
		actions = append(actions, map[string]any{
			"tag":  "button",
			"type": "primary",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": "📊 查看深度对比",
			},
			"url": dash,
		})
	}
	klineLabel, klineURL := chooseLiquidityKlineButton(card)
	if klineURL != "" {
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
	return map[string]any{"tag": "action", "actions": actions}
}

// chooseLiquidityKlineButton mirrors top30's logic: prefer Binance
// K-line when binance shows up in the platform list (industry-default
// reference price), otherwise walk by rank ASC for the first known
// URL template, else default to Binance.
func chooseLiquidityKlineButton(card CardPayload) (label, urlStr string) {
	base, quote, ok := guessBaseQuote(card)
	if !ok {
		return "", ""
	}
	for _, p := range card.Platforms {
		if strings.EqualFold(p.Platform, "binance") {
			return "📈 Binance K 线", buildExchangeKlineURL("binance", base, quote)
		}
	}
	rows := append([]AlertPlatformRow(nil), card.Platforms...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Rank < rows[j].Rank })
	for _, p := range rows {
		if u := buildExchangeKlineURL(p.Platform, base, quote); u != "" {
			return "📈 " + exchangeDisplayName(p.Platform) + " K 线", u
		}
	}
	return "📈 Binance K 线", buildExchangeKlineURL("binance", base, quote)
}

func guessBaseQuote(card CardPayload) (base, quote string, ok bool) {
	if d := strings.TrimSpace(card.DisplaySymbol); d != "" {
		if b, q, ok2 := splitDisplaySymbol(d); ok2 {
			return b, q, true
		}
	}
	canonical := strings.ToUpper(strings.TrimSpace(card.Canonical))
	if canonical == "" {
		return "", "", false
	}
	return canonical, "USDT", true
}

// splitDisplaySymbol mirrors the helper in top30_push.go. Kept inline
// here so the liquidity package has no cross-file dependency on the
// listing package.
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
	}
	return ""
}

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
	case "edgex":
		return "edgeX"
	}
	return strings.TrimSpace(platform)
}

func buildLiquidityFooter(card CardPayload) map[string]any {
	parts := []string{"触发时间 " + formatTriggerTime(card.EvaluatedAt)}
	if card.DedupeKey != "" {
		parts = append(parts, card.DedupeKey)
	}
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

func divLarkMD(content string) map[string]any {
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": content,
		},
	}
}

// humanUSD renders USD amounts compactly: 1.23B / 5.80M / 250K / 95.
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

// BuildDashboardURL is a small helper used by ProduceLiquidityAlertPush
// to deep-link the primary CTA. Mirrors top30_push.buildDashboardSymbolURL
// but accepts the tier so the page can route to the same depth chart
// the alert is fired on.
func BuildDashboardURL(base, canonical, tier string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	q := url.Values{}
	if canonical != "" {
		q.Set("symbol", canonical)
	}
	if tier != "" {
		q.Set("tier", tier)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	enc := q.Encode()
	if enc == "" {
		return base
	}
	return base + sep + enc
}
