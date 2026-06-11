package listing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// utc8Zone is the +08:00 fixed offset zone used for every timestamp
// on the decision card. Stored as a package-level singleton so the
// renderer does not allocate one per card; UTC+8 has no DST so a
// fixed offset is correct year-round.
//
// The label was renamed from "SGT" to "UTC+8" on 2026-06-02 to make
// timestamps directly readable by the China-team operators on the
// Lark group. The instant is unchanged (same +08:00 offset).
var utc8Zone = time.FixedZone("UTC+8", 8*3600)

// formatUTC8 renders t in +08:00 matching the operator sample
// (e.g. "2026-05-02 14:30 UTC+8"). Returns "—" for the zero time so
// the renderer never produces "0001-01-01 ..." gibberish.
func formatUTC8(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(utc8Zone).Format("2006-01-02 15:04 UTC+8")
}

// formatUTC8Short is the shortened variant used inside the per-platform
// Market Status bullet rows: "05-30 10:00 UTC+8". Drops the year so
// the bullet list stays compact on narrow Lark mobile cards.
func formatUTC8Short(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(utc8Zone).Format("01-02 15:04 UTC+8")
}

func formatUTC8MarketStatusTime(t, reference time.Time) string {
	if t.IsZero() {
		return "—"
	}
	local := t.In(utc8Zone)
	if !reference.IsZero() && local.Year() != reference.In(utc8Zone).Year() {
		return local.Format("2006-01-02 15:04 UTC+8")
	}
	return local.Format("01-02 15:04 UTC+8")
}

// Decision actions exposed in the Lark card. Stable enums (not
// labels) so the Lark callback can decode the button value back to
// the action without any locale-specific string matching.
const (
	DecisionActionPrepareListing = "prepare_listing"
	DecisionActionEnterWatchlist = "enter_watchlist"
	DecisionActionContactMM      = "contact_mm"
	DecisionActionIgnore         = "ignore"
)

// DecisionActionLabels maps stable enums to the operator-facing
// Chinese labels rendered on the card buttons. Labels follow PRD §6
// 执行链路 verbatim ("准备上线", not "准备上架").
var DecisionActionLabels = map[string]string{
	DecisionActionPrepareListing: "准备上线",
	DecisionActionEnterWatchlist: "进入观察",
	DecisionActionContactMM:      "联系MM",
	DecisionActionIgnore:         "忽略",
}

// DeliveryEventListingDecisionCandidate is the outbox event_type
// stored for decision cards; the delivery worker routes it the same
// way as other listing events (Lark listing webhook) and the
// callback API uses it for forensic lookups.
const DeliveryEventListingDecisionCandidate = "listing_decision_candidate"

// DecisionCardAction is one button in the card. The Action field is
// the stable enum that flows back through the callback; Label is
// the operator-visible Chinese string.
type DecisionCardAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
}

// DecisionCardEvent is the aggregated payload for one decision card.
// It is what BuildDecisionCardEvent emits, what RenderDecisionCardPostMessage
// turns into a Lark interactive card, and what the callback API
// matches against when verifying a button click.
type DecisionCardEvent struct {
	CandidateID        int64                `json:"candidate_id"`
	RiskPlanID         int64                `json:"risk_plan_id"`
	CanonicalSymbol    string               `json:"canonical_symbol"`
	DisplaySymbol      string               `json:"display_symbol"`
	MarketSurface      string               `json:"market_surface"`
	EvidenceKind       string               `json:"evidence_kind"`
	Recommendation     string               `json:"recommendation"`
	ConfidenceLevel    string               `json:"confidence_level"`
	BusinessScore      float64              `json:"business_score,omitempty"`
	SourcePlatforms    []string             `json:"source_platforms"`
	Actions            []DecisionCardAction `json:"actions"`
	DedupeKey          string               `json:"dedupe_key"`
	TriggerTime        time.Time            `json:"trigger_time"`
	PrimaryListingTime *time.Time           `json:"primary_listing_time,omitempty"`

	// Enrichment is the bundle of data the renderer surfaces in the
	// Market Status / Metrics blocks. Optional; when
	// zero-value the renderer degrades each block gracefully (e.g.
	// shows "无平台状态记录" instead of an empty bullet list).
	Enrichment DecisionCardEnrichment `json:"-"`

	// RiskPlan stays in the backend audit/callback path. The source-
	// first Lark card intentionally does not render risk plan copy.
	RiskPlan RiskPlan `json:"-"`
}

// BuildDecisionCardEvent assembles the per-candidate decision card
// payload. The function is pure: no DB access. Per PRD §6 every
// decision card surfaces the full 4-button matrix (准备上线 /
// 进入观察 / 联系MM / 忽略) regardless of evidence_kind. Already-
// listed candidates are filtered upstream by the producer.
func BuildDecisionCardEvent(c Candidate, plan RiskPlan, triggerTime time.Time) DecisionCardEvent {
	ev := DecisionCardEvent{
		CandidateID:     c.ID,
		RiskPlanID:      plan.ID,
		CanonicalSymbol: c.CanonicalSymbol,
		DisplaySymbol:   c.DisplaySymbol,
		MarketSurface:   c.MarketSurface,
		EvidenceKind:    c.EvidenceKind,
		Recommendation:  c.Recommendation,
		ConfidenceLevel: c.ConfidenceLevel,
		SourcePlatforms: append([]string{}, c.SourcePlatforms...),
		TriggerTime:     triggerTime,
		DedupeKey:       buildDecisionDedupeKey(c.ID, ""),
	}
	if c.BusinessScore != nil {
		ev.BusinessScore = *c.BusinessScore
	}
	ev.RiskPlan = plan
	ev.Actions = standardButtonMatrix()
	return ev
}

func decisionEvidenceSignature(signals []SignalObservation) string {
	fingerprints := make([]string, 0, len(signals))
	for _, signal := range signals {
		fingerprint := strings.TrimSpace(signal.Fingerprint)
		if fingerprint == "" {
			continue
		}
		fingerprints = append(fingerprints, fingerprint)
	}
	sort.Strings(fingerprints)
	sum := sha256.Sum256([]byte(strings.Join(fingerprints, "\n")))
	return fmt.Sprintf("%x", sum)[:16]
}

func buildDecisionDedupeKey(candidateID int64, _ string) string {
	return fmt.Sprintf("listing_decision|%d|first_listing", candidateID)
}

// standardButtonMatrix returns the 4-button set defined in PRD §6.
// We keep it as a function (rather than a package-level var) so the
// returned slice is freshly allocated per call — callers may mutate
// it without leaking state across cards.
func standardButtonMatrix() []DecisionCardAction {
	return []DecisionCardAction{
		{Action: DecisionActionPrepareListing, Label: DecisionActionLabels[DecisionActionPrepareListing]},
		{Action: DecisionActionEnterWatchlist, Label: DecisionActionLabels[DecisionActionEnterWatchlist]},
		{Action: DecisionActionContactMM, Label: DecisionActionLabels[DecisionActionContactMM]},
		{Action: DecisionActionIgnore, Label: DecisionActionLabels[DecisionActionIgnore]},
	}
}

// RenderDecisionCardPostMessage builds the Lark interactive card
// envelope. The layout follows the source-first card contract:
// 基础信息 / Market Status / Metrics / Score / Actions. Risk plan,
// confidence, and recommendation stay in the backend audit path but
// are intentionally not rendered to keep the operator card compact.
//
// Callback contract is unchanged: every button still posts back
// {candidate_id, risk_plan_id, action, dedupe_key}, so the existing
// callback handler does not need to change.
func RenderDecisionCardPostMessage(ev DecisionCardEvent) ([]byte, error) {
	card := buildDecisionCard(ev)
	envelope := map[string]any{"msg_type": "interactive", "card": card}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(envelope); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

func buildDecisionCard(ev DecisionCardEvent) map[string]any {
	elements := []any{
		buildDecisionHeadline(ev),
		buildDecisionBasicInfoFields(ev),
		map[string]any{"tag": "hr"},
		buildDecisionMarketStatusBlock(ev),
		map[string]any{"tag": "hr"},
		buildDecisionMetricsFields(ev),
		map[string]any{"tag": "hr"},
		buildDecisionScoreFields(ev),
	}
	if action := buildDecisionActionRow(ev); action != nil {
		elements = append(elements, map[string]any{"tag": "hr"}, action)
	}
	elements = append(elements, buildDecisionFooterNote(ev))
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": decisionHeaderTemplate(ev.Recommendation),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": decisionHeaderText(ev),
			},
		},
		"elements": elements,
	}
}

// decisionHeaderTemplate returns the Lark header colour for the
// decision card. Operator chose to unify the header across all
// recommendation tiers — every new perp listing detection deserves
// the same visual prominence in the Listing group. We pick red
// because it has the strongest "look at this now" signal in a busy
// chat list, matching the "🚨 New Perp Listing Detected" banner emoji.
func decisionHeaderTemplate(rec string) string {
	return "red"
}

// decisionHeaderText combines the unified banner with the canonical
// symbol so the title bar is informative even when many cards sit
// next to each other in the same Lark group. The banner is the same
// for every recommendation tier (see decisionHeaderTemplate); the
// recommendation label lives in the body.
func decisionHeaderText(ev DecisionCardEvent) string {
	prefix := "🚨 New Perp Listing Detected"
	switch strings.ToLower(strings.TrimSpace(ev.MarketSurface)) {
	case "spot":
		prefix = "🚨 New Spot Listing Detected"
	case "", "perp":
		prefix = "🚨 New Perp Listing Detected"
	default:
		prefix = "🚨 New Listing Detected"
	}
	sym := strings.TrimSpace(ev.CanonicalSymbol)
	if sym == "" {
		sym = strings.TrimSpace(ev.DisplaySymbol)
	}
	if sym == "" {
		return prefix
	}
	return prefix + " · " + sym
}

// buildDecisionHeadline is the body's first row: H1 symbol + a
// muted subtitle showing the discovery channel (Chinese label). The
// channel explains how the agent found the listing; score remains
// source-platform driven.
func buildDecisionHeadline(ev DecisionCardEvent) map[string]any {
	sym := strings.TrimSpace(ev.CanonicalSymbol)
	if sym == "" {
		sym = "—"
	}
	parts := []string{"# " + sym}
	if label := evidenceKindLabel(ev.EvidenceKind); label != "" {
		parts = append(parts, "<font color='grey'>"+label+"</font>")
	}
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": strings.Join(parts, "\n"),
		},
	}
}

// evidenceKindLabel maps the EvidenceKind enum to a short Chinese
// label the operator-facing subtitle renders. Kept inline rather
// than in domain.go because the locale string only lives on the
// card; the read-only API still surfaces the enum verbatim.
func evidenceKindLabel(kind string) string {
	switch kind {
	case EvidenceAnnouncementAndAPI:
		return "API + 公告都已确认"
	case EvidenceInstrumentDiffOnly:
		return "API 已发现"
	case EvidenceAnnouncementPendingAPI:
		return "公告已发现"
	case EvidenceTop30Only:
		return "热度发现"
	case EvidenceManualSeed:
		return "手动加入"
	default:
		return kind
	}
}

// buildDecisionBasicInfoFields renders the Token / Source / Time /
// edgeX status row as a 2x2 grid.
func buildDecisionBasicInfoFields(ev DecisionCardEvent) map[string]any {
	source := primarySourceLabel(ev.SourcePlatforms)
	edgex := edgexListedLabel(ev.Enrichment)
	fields := []any{
		summaryField("Token", canonicalOrDash(ev.CanonicalSymbol)),
		summaryField("edgeX 状态", edgex),
		summaryField("Source", source),
		decisionTimeSummaryField(ev),
	}
	if ev.PrimaryListingTime != nil && !ev.PrimaryListingTime.IsZero() {
		fields = append(fields, summaryField("Listing Time", formatUTC8(*ev.PrimaryListingTime)))
	}
	return map[string]any{
		"tag":    "div",
		"fields": fields,
	}
}

func decisionTimeSummaryField(ev DecisionCardEvent) map[string]any {
	return summaryField("Detected Time", formatUTC8(ev.TriggerTime))
}

func selectPrimaryListingTime(sourcePlatforms []string, signals []SignalObservation) *time.Time {
	if len(signals) == 0 {
		return nil
	}
	priority := make(map[string]int, len(sourcePlatforms))
	for i, platform := range sourcePlatforms {
		priority[strings.ToLower(platform)] = i
	}
	var best *SignalObservation
	bestPriority := len(priority) + 1
	bestSourceRank := 99
	for i := range signals {
		s := &signals[i]
		if s.SignalType != SignalInstrumentDiff {
			continue
		}
		if s.ListingTimeTS == nil || s.ListingTimeTS.IsZero() {
			continue
		}
		p, ok := priority[strings.ToLower(s.SourcePlatform)]
		if !ok {
			p = len(priority)
		}
		sourceRank := listingTimeSourceRank(*s)
		if best == nil || sourceRank < bestSourceRank || (sourceRank == bestSourceRank && (p < bestPriority || (p == bestPriority && s.ObservedAt.After(best.ObservedAt)))) {
			best = s
			bestPriority = p
			bestSourceRank = sourceRank
		}
	}
	if best == nil {
		return nil
	}
	t := *best.ListingTimeTS
	return &t
}

func listingTimeSourceRank(s SignalObservation) int {
	switch s.SignalType {
	case SignalInstrumentDiff:
		return 0
	default:
		return 1
	}
}

// primarySourceLabel renders the "Source: Binance Futures (+N more)"
// summary. PRD §5.2 uses a singular Source field even though
// candidates may have multiple platforms; we pick the highest-
// priority platform and disclose the rest as "+N more" to keep the
// row compact.
func primarySourceLabel(sources []string) string {
	if len(sources) == 0 {
		return "—"
	}
	ordered := append([]string(nil), sources...)
	// Sort by platformPriority then alphabetically.
	for i := 1; i < len(ordered); i++ {
		j := i
		for j > 0 {
			pi := priorityForPlatform(ordered[j])
			pj := priorityForPlatform(ordered[j-1])
			if pi < pj || (pi == pj && ordered[j] < ordered[j-1]) {
				ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
				j--
				continue
			}
			break
		}
	}
	primary := displayNameForPlatform(ordered[0])
	if len(ordered) == 1 {
		return primary
	}
	return fmt.Sprintf("%s (+%d more)", primary, len(ordered)-1)
}

// edgexListedLabel returns the locale-friendly edgeX status string
// honouring the three-state EdgexListedKnown contract.
func edgexListedLabel(e DecisionCardEnrichment) string {
	if e.EdgexListed || hasActiveEdgexMarketStatus(e) {
		return "已上线"
	}
	if !e.EdgexListedKnown {
		return "未知"
	}
	return "未上线"
}

func hasActiveEdgexMarketStatus(e DecisionCardEnrichment) bool {
	for _, ms := range e.MarketStatuses {
		if strings.EqualFold(ms.Platform, edgexListedPlatformName) && ms.Status == StatusActive {
			return true
		}
	}
	return false
}

func shouldSuppressDecisionCardForEdgexLive(e DecisionCardEnrichment) bool {
	return e.EdgexListed || hasActiveEdgexMarketStatus(e)
}

func canonicalOrDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	return s
}

// buildDecisionMarketStatusBlock renders the per-platform status
// timeline. Empty enrichment falls back to a single grey bullet so
// the operator knows the agent looked but found nothing.
func buildDecisionMarketStatusBlock(ev DecisionCardEvent) map[string]any {
	lines := []string{"**Market Status**"}
	if !ev.Enrichment.HasMarketStatus() {
		lines = append(lines, "<font color='grey'>● 无平台状态记录</font>")
	} else {
		for _, ms := range ev.Enrichment.MarketStatuses {
			lines = append(lines, formatMarketStatusLine(ms, ev.TriggerTime))
		}
	}
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": strings.Join(lines, "\n"),
		},
	}
}

func formatMarketStatusLine(ms PlatformMarketStatus, reference time.Time) string {
	bullet := marketStatusBulletFor(ms)
	name := strings.TrimSpace(ms.DisplayName)
	if name == "" {
		name = ms.Platform
	}
	label := ms.StatusLabel
	if label == "" {
		label = marketStatusLabel(ms)
	} else if (ms.Status == StatusPaused || ms.Status == StatusDelisted) && !strings.Contains(label, "当前状态") {
		raw := strings.TrimSpace(ms.StatusRaw)
		if raw == "" {
			label += "（当前状态）"
		} else {
			label += "（当前状态 · API: " + raw + "）"
		}
	}
	parts := []string{fmt.Sprintf("%s **%s**", bullet, name), label}
	if ms.ListingTime != nil && !ms.ListingTime.IsZero() {
		parts = append(parts, "Listing on "+formatMarketStatusListingDate(*ms.ListingTime, reference))
	}
	return strings.Join(parts, " · ")
}

func formatMarketStatusListingDate(t, reference time.Time) string {
	if t.IsZero() {
		return "—"
	}
	local := t.In(utc8Zone)
	if !reference.IsZero() && local.Year() != reference.In(utc8Zone).Year() {
		return local.Format("2006-01-02")
	}
	return local.Format("01-02")
}

func marketStatusBulletFor(ms PlatformMarketStatus) string {
	if isEdgeXEnableDisplayFalse(ms) {
		return "<font color='grey'>●</font>"
	}
	return marketStatusBullet(ms.Status)
}

func isEdgeXEnableDisplayFalse(ms PlatformMarketStatus) bool {
	return strings.EqualFold(ms.Platform, edgexListedPlatformName) && strings.EqualFold(strings.TrimSpace(ms.StatusRaw), "enable_display_false")
}

// marketStatusBullet picks the bullet colour matching the status
// (green = active, orange = pre-listing / announcement, grey = other).
func marketStatusBullet(status string) string {
	switch status {
	case StatusActive:
		return "<font color='green'>●</font>"
	case StatusPreListing:
		return "<font color='orange'>●</font>"
	case StatusPaused, StatusDelisted:
		return "<font color='red'>●</font>"
	default:
		return "<font color='grey'>●</font>"
	}
}

func marketStatusSourceLabel(kind string) string {
	switch kind {
	case "api":
		return "API"
	case "announcement":
		return "公告"
	case "both":
		return "API + 公告"
	default:
		return "—"
	}
}

// buildDecisionMetricsFields renders the Market Cap / Spot 24h Vol /
// 现货深度 / 合约深度 grid.
func buildDecisionMetricsFields(ev DecisionCardEvent) map[string]any {
	mc := nullableUSDLabel(ev.Enrichment.MarketCapUSD)
	vol := nullableUSDLabel(ev.Enrichment.Spot24hVolumeUSD)
	spot := depthEvidenceLabel(ev.Enrichment.SpotDepth)
	perp := depthEvidenceLabel(ev.Enrichment.PerpDepth)
	return map[string]any{
		"tag": "div",
		"fields": []any{
			summaryField("Market Cap", mc),
			summaryField("Spot 24h Vol", vol),
			summaryField("现货深度", spot),
			summaryField("合约深度", perp),
		},
	}
}

func nullableUSDLabel(v *float64) string {
	if !positiveUSDPtr(v) {
		return "n/a"
	}
	return "$" + humanUSD(*v)
}

func depthEvidenceLabel(d *DepthEvidence) string {
	if d == nil {
		return "不可用"
	}
	if d.Tier == "" {
		return fmt.Sprintf("$%s (%s)", humanUSD(d.USDValue), d.Platform)
	}
	return fmt.Sprintf("$%s (%s · %s)", humanUSD(d.USDValue), d.Platform, d.Tier)
}

// buildDecisionRiskPlanBlock renders the "自动参数预案" section.
// Operator decision: do not pre-fill specific values (杠杆 / 杠杆档位 /
// MM 报价 / Funding / Max Position) on the card today — every
// parameter is left to manual alignment so the agent never appears
// to commit to numbers operators have not yet endorsed. The block
// is kept (rather than removed) so operators see at a glance that
// the agent recognised the parameter slots exist; RiskPlan is still
// persisted upstream and reachable via callback for future
// surfacing.
func buildDecisionRiskPlanBlock(ev DecisionCardEvent) map[string]any {
	lines := []string{
		"**自动参数预案**",
		"<font color='grey'>杠杆 / 杠杆档位 / MM 报价 / Funding / Max Position 待规则补齐</font>",
	}
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": strings.Join(lines, "\n"),
		},
	}
}

// buildDecisionScoreFields renders only the source-first score right
// above the action buttons. Internal recommendations still drive
// audit/risk-plan compatibility but are no longer card copy.
func buildDecisionScoreFields(ev DecisionCardEvent) map[string]any {
	scoreLabel := "—"
	if ev.BusinessScore > 0 {
		scoreLabel = fmt.Sprintf("%.0f / 100", ev.BusinessScore)
	}
	return map[string]any{
		"tag": "div",
		"fields": []any{
			summaryField("Score", scoreLabel),
		},
	}
}

// buildDecisionActionRow renders the four callback buttons. Colour
// hierarchy: 准备上线 = primary red-ish (Lark renders primary in
// the header's template colour automatically); the rest stay default
// so the most important button visually stands out.
func buildDecisionActionRow(ev DecisionCardEvent) map[string]any {
	if len(ev.Actions) == 0 {
		return nil
	}
	actions := make([]any, 0, len(ev.Actions))
	for _, a := range ev.Actions {
		btn := map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "plain_text", "content": a.Label},
			"type": decisionButtonType(a.Action),
			"value": map[string]any{
				"candidate_id": ev.CandidateID,
				"risk_plan_id": ev.RiskPlanID,
				"action":       a.Action,
				"dedupe_key":   ev.DedupeKey,
			},
		}
		actions = append(actions, btn)
	}
	return map[string]any{"tag": "action", "actions": actions}
}

func decisionButtonType(action string) string {
	switch action {
	case DecisionActionPrepareListing:
		return "primary"
	case DecisionActionIgnore:
		return "default"
	default:
		return "default"
	}
}

// buildDecisionFooterNote renders the muted footer with dedupe key,
// trigger timestamp, evidence-kind label, and any best-effort
// enrichment error count so the operator can audit the pipeline
// without leaving Lark.
func buildDecisionFooterNote(ev DecisionCardEvent) map[string]any {
	parts := []string{
		"trigger=" + formatUTC8(ev.TriggerTime),
		"evidence=" + evidenceKindLabel(ev.EvidenceKind),
	}
	// CoinGecko ID intentionally omitted from the footer per ops
	// feedback: the card should not surface external data-source
	// names. CoinGeckoID is still kept on DecisionCardEnrichment
	// for log-side audit, just not displayed.
	if metrics := metricFooterSummary(ev.Enrichment); metrics != "" {
		parts = append(parts, "metrics="+metrics)
	}
	if errs := ev.Enrichment.EnrichErrors; len(errs) > 0 {
		parts = append(parts, fmt.Sprintf("enrich_errors=%d", len(errs)))
	}
	parts = append(parts, "dedupe="+ev.DedupeKey)
	return map[string]any{
		"tag": "note",
		"elements": []any{
			map[string]any{"tag": "plain_text", "content": strings.Join(parts, " · ")},
		},
	}
}

func metricFooterSummary(enr DecisionCardEnrichment) string {
	items := make([]string, 0, 4)
	for _, item := range []struct {
		name string
		info MetricInfo
	}{
		{name: "mc", info: enr.MarketCapMetric},
		{name: "vol", info: enr.Spot24hVolumeMetric},
		{name: "spot", info: enr.SpotDepthMetric},
		{name: "perp", info: enr.PerpDepthMetric},
	} {
		if cell := metricFooterCell(item.name, item.info); cell != "" {
			items = append(items, cell)
		}
	}
	return strings.Join(items, " ")
}

func metricFooterCell(name string, info MetricInfo) string {
	if info.Status == "" {
		return ""
	}
	out := name + ":" + string(info.Status)
	if source := metricFooterSourceLabel(info.Source); source != "" {
		out += "/" + source
	}
	return out
}

func metricFooterSourceLabel(source string) string {
	switch strings.TrimSpace(source) {
	case "coingecko":
		return "ext"
	case DecisionCardMetricSourceLiveReference:
		return "live"
	case DecisionCardMetricSourceDBSnapshot, "db_spot_snapshot":
		return "snap"
	case "":
		return ""
	default:
		return "src"
	}
}

// DecisionCardDeps wires the producer's runtime knobs. IgnoreCooldown
// is the §5 default-24h抑制 window — operators can configure it via
// listing_agent.decision_card.ignore_cooldown. MaxPerTick caps the
// number of decision cards produced per engine tick so a fresh
// deploy with hundreds of unfused candidates does not burst the Lark
// channel.
//
// Enrich is the per-candidate enrichment bundle the renderer
// consumes. Leave it zero-value to keep the legacy "no-enrichment"
// behaviour — the renderer degrades each block gracefully so this
// is safe in test setups that have not wired all data sources.
type DecisionCardDeps struct {
	Now                          func() time.Time
	IgnoreCooldown               time.Duration
	MaxPerTick                   int
	HistoricalListingGracePeriod time.Duration
	Enrich                       DecisionCardEnrichDeps
}

// DecisionCardResult is the per-tick summary written onto RunSummary.
type DecisionCardResult struct {
	Considered             int
	RiskPlans              int
	OutboxRows             int
	SkippedCooldown        int
	SkippedNoAction        int
	SkippedNoFreshEvidence int
	SkippedDuplicate       int
	SkippedEdgexLive       int
}

// ProduceDecisionCards is the producer entry point. For each
// actionable candidate it: (1) checks the ignore cooldown against
// the latest decision; (2) derives + persists a risk plan; (3)
// writes the decision card outbox row keyed once per candidate so
// later platform evidence, status transitions, or UTC day boundaries
// cannot re-trigger another "New Listing" card for the same candidate.
func ProduceDecisionCards(ctx context.Context, repo *Repository, deps DecisionCardDeps) (DecisionCardResult, error) {
	if repo == nil {
		return DecisionCardResult{}, errors.New("decision card: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.IgnoreCooldown <= 0 {
		deps.IgnoreCooldown = 24 * time.Hour
	}
	if deps.MaxPerTick <= 0 {
		deps.MaxPerTick = 20
	}
	if deps.HistoricalListingGracePeriod <= 0 {
		deps.HistoricalListingGracePeriod = 48 * time.Hour
	}
	now := deps.Now()

	candidates, err := repo.ListActionableDecisionCandidates(ctx, deps.MaxPerTick)
	if err != nil {
		return DecisionCardResult{}, fmt.Errorf("list actionable decision candidates: %w", err)
	}
	res := DecisionCardResult{Considered: len(candidates)}

	for _, c := range candidates {
		if c.Recommendation == RecommendationNoAction || c.LifecycleStatus == LifecycleAlreadyListed || isNonListingTargetAsset(c.CanonicalSymbol) {
			res.SkippedNoAction++
			continue
		}
		// Cooldown gate: if the latest decision for this candidate is
		// an ignore within the configured window, skip the card.
		action, decisionTS, hasDecision, err := repo.LatestDecisionForCandidate(ctx, c.ID)
		if err != nil {
			return res, fmt.Errorf("latest decision %d: %w", c.ID, err)
		}
		if hasDecision && action == DecisionActionIgnore && now.Sub(decisionTS) < deps.IgnoreCooldown {
			res.SkippedCooldown++
			continue
		}

		signals, err := repo.ListCandidateSignals(ctx, c.ID, false)
		if err != nil {
			return res, fmt.Errorf("list candidate signals %d: %w", c.ID, err)
		}
		freshSignals := freshDecisionEvidenceSignals(signals, deps.HistoricalListingGracePeriod)
		if len(freshSignals) == 0 {
			res.SkippedNoFreshEvidence++
			continue
		}

		hasOutbox, err := repo.HasDecisionOutboxForCandidate(ctx, c.ID)
		if err != nil {
			return res, fmt.Errorf("check decision outbox %d: %w", c.ID, err)
		}
		if hasOutbox {
			res.SkippedDuplicate++
			continue
		}

		enrichment := EnrichCandidateForDecisionCard(ctx, deps.Enrich, c)
		if shouldSuppressDecisionCardForEdgexLive(enrichment) {
			if err := repo.MarkCandidateAlreadyListed(ctx, c.ID, now); err != nil {
				return res, fmt.Errorf("mark already listed %d: %w", c.ID, err)
			}
			res.SkippedEdgexLive++
			continue
		}

		plan := BuildRiskPlanFromCandidate(c, now)
		planID, err := repo.UpsertRiskPlan(ctx, plan)
		if err != nil {
			return res, fmt.Errorf("upsert risk plan %d: %w", c.ID, err)
		}
		plan.ID = planID
		res.RiskPlans++

		ev := BuildDecisionCardEvent(c, plan, now)
		ev.DedupeKey = buildDecisionDedupeKey(c.ID, decisionEvidenceSignature(freshSignals))
		ev.PrimaryListingTime = selectPrimaryListingTime(c.SourcePlatforms, freshSignals)
		ev.Enrichment = enrichment
		payload, err := RenderDecisionCardPostMessage(ev)
		if err != nil {
			return res, fmt.Errorf("render decision card %d: %w", c.ID, err)
		}
		if err := repo.insertOutbox(ctx, DeliveryOutbox{
			EventType:     DeliveryEventListingDecisionCandidate,
			DedupeKey:     ev.DedupeKey,
			TargetChannel: DeliveryChannelLarkTop30,
			Status:        OutboxStatusPending,
			MaxAttempts:   5,
			PayloadJSON:   payload,
			NextAttemptAt: ptrTime(now),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return res, fmt.Errorf("insert decision outbox %d: %w", c.ID, err)
		}
		res.OutboxRows++
	}
	return res, nil
}

func freshDecisionEvidenceSignals(signals []SignalObservation, grace time.Duration) []SignalObservation {
	if len(signals) == 0 {
		return nil
	}
	out := make([]SignalObservation, 0, len(signals))
	for _, s := range signals {
		if !isCandidatePromotingSignal(s) {
			continue
		}
		if isHistoricalListingSignal(s, grace) {
			continue
		}
		out = append(out, s)
	}
	return out
}
