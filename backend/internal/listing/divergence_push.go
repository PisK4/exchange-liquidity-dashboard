package listing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/divergence"
	"edgex-dashboard/backend/internal/domain"
)

// DivergencePushEvent is one of the four UTC-day cards produced by
// the divergence push pipeline. One event ≡ one Lark interactive card
// ≡ one outbox row.
//
// Categories are stable wire identifiers (cex_only / dex_only /
// heavy_gap / both_hot_gap) so the dedupe key and the outbox
// event_type column never need to change when the operator-facing
// CategoryLabel evolves.
type DivergencePushEvent struct {
	Category      string                    `json:"category"`
	CategoryLabel string                    `json:"category_label"`
	Rows          []DivergencePushRow       `json:"rows"`
	KPI           domain.Top30DivergenceKPI `json:"kpi"`
	SnapshotTS    time.Time                 `json:"snapshot_ts"`
	SnapshotDate  string                    `json:"snapshot_date"`
	DedupeKey     string                    `json:"dedupe_key"`
	DashboardURL  string                    `json:"dashboard_url,omitempty"`
	TriggerTime   time.Time                 `json:"trigger_time"`
}

// DivergencePushRow is one line on a divergence card. CEXRank /
// DEXRank are *int because each card omits one side depending on its
// category (cex_only has no DEXRank, dex_only has no CEXRank). The
// renderer fans this out into the per-card table layout.
type DivergencePushRow struct {
	Symbol       string   `json:"symbol"`
	CEXRank      *int     `json:"cex_rank,omitempty"`
	DEXRank      *int     `json:"dex_rank,omitempty"`
	RankDelta    *int     `json:"rank_delta,omitempty"`
	CEXVolUSD    *float64 `json:"cex_volume_24h_usd,omitempty"`
	DEXVolUSD    *float64 `json:"dex_volume_24h_usd,omitempty"`
	CEXPlatforms int      `json:"cex_platform_count"`
	DEXPlatforms int      `json:"dex_platform_count"`
}

// DivergenceDeps mirrors the Top30Deps style so the engine can wire
// both producers symmetrically.
type DivergenceDeps struct {
	Now           func() time.Time
	DashboardBase string
	WebhookURL    string
	MaxAttempts   int
	DivergenceCfg config.Top30DivergenceConfig
	PushCfg       config.Top30DivergencePushConfig
}

// DivergencePushResult summarises one ProduceDivergencePush call.
// SkippedEmpty counts categories that had zero eligible rows after
// filtering (no empty cards). SkippedDedupe counts rows that hit a
// pre-existing outbox row for the same (category, day) — the unique
// key on dedupe_key makes the insert a no-op, but we audit-trail
// the count anyway so the engine summary reflects what actually
// reached the wire.
type DivergencePushResult struct {
	Enabled       bool
	SnapshotTS    time.Time
	Produced      int
	Signals       int
	OutboxRows    int
	SkippedEmpty  int
	SkippedDedupe int
	FailClosed    string
}

// divergenceCategoryConfig maps each stable category id to its
// renderer-facing label, header colour, and outbox event_type.
type divergenceCategoryConfig struct {
	Label    string
	Header   string
	EventTyp string
}

var divergenceCategories = map[string]divergenceCategoryConfig{
	DivergenceCategoryCEXOnly: {
		Label:    "CEX 独有热门 · edgeX 未上线",
		Header:   "blue",
		EventTyp: DeliveryEventTop30DivergenceCEXOnly,
	},
	DivergenceCategoryDEXOnly: {
		Label:    "DEX 独有热门 · edgeX 未上线",
		Header:   "purple",
		EventTyp: DeliveryEventTop30DivergenceDEXOnly,
	},
	DivergenceCategoryHeavyGap: {
		Label:    "CEX vs DEX 显著分歧 · edgeX 未上线",
		Header:   "orange",
		EventTyp: DeliveryEventTop30DivergenceHeavyGap,
	},
	DivergenceCategoryBothHotGap: {
		Label:    "两阵营均热 · edgeX 未上线",
		Header:   "red",
		EventTyp: DeliveryEventTop30DivergenceBothHotGap,
	},
}

// BuildDivergencePushEvents adapts the t_top30_snapshot row stream
// into the four divergence cards. The transformation is:
//
//  1. Build a canonical→*bool lookup from rows so the three-state
//     edgex_listed filter survives the divergence aggregation
//     boundary.
//  2. Call divergence.Compute to get the per-class aggregates and the
//     classified divergence rows.
//  3. Filter divergence rows on the *EdgexListed==false red line
//     (nil → drop; *true → drop). This is the alert-side strict
//     filter, separate from the KPI counter on the API path which
//     preserves legacy "any not-known-listed counts" behaviour.
//  4. Per card category, pick rows and sort by the category's natural
//     ordering:
//
//     #2 cex_only      → CEXRank ASC
//     #3 dex_only      → DEXRank ASC
//     #4 heavy_gap     → |RankDelta| DESC
//     #5 both_hot_gap  → min(CEXRank, DEXRank) ASC
//
//  5. Truncate to topN. Empty categories produce no card.
//
// The caller (ProduceDivergencePush) injects DashboardURL and
// TriggerTime; this builder leaves both unset so it can be exercised
// from preview tooling without engine-side state.
func BuildDivergencePushEvents(rows []Top30RowForPush, cfg config.Top30DivergenceConfig, topN int, day time.Time) []DivergencePushEvent {
	if topN <= 0 {
		topN = 10
	}
	inputs := make([]divergence.InputRow, 0, len(rows))
	knownByCanonical := map[string]*bool{}
	for _, row := range rows {
		canonical := divergence.CanonicaliseSymbol(row.Symbol)
		if canonical == "" {
			continue
		}
		inputs = append(inputs, divergence.InputRow{
			Platform:     row.Platform,
			Symbol:       row.Symbol,
			Rank:         row.Rank,
			Volume24HUSD: row.Volume24HUSD,
			Status:       domain.StatusComplete,
			SnapshotTS:   row.SnapshotTS,
			EdgexListed:  row.EdgexListed,
		})
		// Last-write-wins is fine here: if multiple platforms surface
		// the same canonical (e.g. BTC on binance + okx + edgeX), the
		// three-state question collapses to "is there any platform
		// claiming *listed==true?" which we resolve below.
		if known, ok := knownByCanonical[canonical]; ok {
			knownByCanonical[canonical] = mergeListedKnowledge(known, row.EdgexListed)
		} else if row.EdgexListed != nil {
			v := *row.EdgexListed
			knownByCanonical[canonical] = &v
		}
	}
	snapshot := divergence.Compute(inputs, divergence.Config{
		CEXPlatforms:         cfg.CEXPlatforms,
		DEXPlatforms:         cfg.DEXPlatforms,
		SignificantRankDelta: cfg.SignificantRankDelta,
	})

	eligible := filterDivergenceRowsForAlert(snapshot.Divergence, knownByCanonical)

	dayKey := day.UTC().Format("2006-01-02")
	out := make([]DivergencePushEvent, 0, 4)

	emit := func(category string, picked []domain.Top30DivergenceRow, sorter func(a, b domain.Top30DivergenceRow) bool) {
		if len(picked) == 0 {
			return
		}
		sort.SliceStable(picked, func(i, j int) bool { return sorter(picked[i], picked[j]) })
		if len(picked) > topN {
			picked = picked[:topN]
		}
		meta := divergenceCategories[category]
		rowsOut := make([]DivergencePushRow, 0, len(picked))
		for _, row := range picked {
			rowsOut = append(rowsOut, makeDivergencePushRow(row))
		}
		out = append(out, DivergencePushEvent{
			Category:      category,
			CategoryLabel: meta.Label,
			Rows:          rowsOut,
			KPI:           snapshot.KPI,
			SnapshotTS:    snapshot.SnapshotTS,
			SnapshotDate:  dayKey,
			DedupeKey:     fmt.Sprintf("top30_divergence|%s|%s", category, dayKey),
		})
	}

	var cexOnly, dexOnly, heavy, bothHot []domain.Top30DivergenceRow
	for _, row := range eligible {
		switch row.Category {
		case domain.Top30DivergenceCEXOnly:
			cexOnly = append(cexOnly, row)
		case domain.Top30DivergenceDEXOnly:
			dexOnly = append(dexOnly, row)
		case domain.Top30DivergenceCEXHeavy, domain.Top30DivergenceDEXHeavy:
			heavy = append(heavy, row)
		}
		if row.CEXRank != nil && row.DEXRank != nil {
			bothHot = append(bothHot, row)
		}
	}

	emit(DivergenceCategoryCEXOnly, cexOnly, func(a, b domain.Top30DivergenceRow) bool {
		return derefInt(a.CEXRank) < derefInt(b.CEXRank)
	})
	emit(DivergenceCategoryDEXOnly, dexOnly, func(a, b domain.Top30DivergenceRow) bool {
		return derefInt(a.DEXRank) < derefInt(b.DEXRank)
	})
	emit(DivergenceCategoryHeavyGap, heavy, func(a, b domain.Top30DivergenceRow) bool {
		return derefInt(a.RankDelta) > derefInt(b.RankDelta)
	})
	emit(DivergenceCategoryBothHotGap, bothHot, func(a, b domain.Top30DivergenceRow) bool {
		return minRank(a) < minRank(b)
	})

	return out
}

// filterDivergenceRowsForAlert applies the three-state strict filter:
// only canonicals where the known state is *false (i.e. *EdgexListed
// == false) survive. nil and *true are both rejected so a
// catalog-unknown symbol can never trigger an alert (spec red line).
func filterDivergenceRowsForAlert(rows []domain.Top30DivergenceRow, known map[string]*bool) []domain.Top30DivergenceRow {
	out := make([]domain.Top30DivergenceRow, 0, len(rows))
	for _, row := range rows {
		state, ok := known[row.Symbol]
		if !ok || state == nil {
			continue
		}
		if *state {
			continue
		}
		out = append(out, row)
	}
	return out
}

// mergeListedKnowledge collapses two three-state observations onto a
// single *bool using "*true wins, then *false, then nil". So even one
// platform reporting `*listed=true` is enough to suppress the alert
// for a canonical, while a single `*false` reading still beats a nil.
func mergeListedKnowledge(prev, next *bool) *bool {
	if next == nil {
		return prev
	}
	if prev == nil {
		v := *next
		return &v
	}
	if *prev {
		return prev
	}
	if *next {
		v := true
		return &v
	}
	return prev
}

func makeDivergencePushRow(row domain.Top30DivergenceRow) DivergencePushRow {
	out := DivergencePushRow{
		Symbol:       row.Symbol,
		CEXRank:      row.CEXRank,
		DEXRank:      row.DEXRank,
		RankDelta:    row.RankDelta,
		CEXVolUSD:    row.CEXRawVolUSD,
		DEXVolUSD:    row.DEXRawVolUSD,
		CEXPlatforms: row.CEXPlatformCount,
		DEXPlatforms: row.DEXPlatformCount,
	}
	return out
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func minRank(row domain.Top30DivergenceRow) int {
	switch {
	case row.CEXRank != nil && row.DEXRank != nil:
		if *row.CEXRank < *row.DEXRank {
			return *row.CEXRank
		}
		return *row.DEXRank
	case row.CEXRank != nil:
		return *row.CEXRank
	case row.DEXRank != nil:
		return *row.DEXRank
	}
	return 1 << 30
}

// RenderDivergencePostMessage emits the Lark interactive-card body
// for one divergence event. The structure mirrors the Top30 hot-gap
// card (header → headline → KPI summary → table → action button →
// footer) so operators see a consistent visual grammar across the
// listing alert family.
func RenderDivergencePostMessage(ev DivergencePushEvent) ([]byte, error) {
	body := map[string]any{
		"msg_type": "interactive",
		"card":     buildDivergenceCard(ev),
	}
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

func buildDivergenceCard(ev DivergencePushEvent) map[string]any {
	elements := []any{
		buildDivergenceKPIStrip(ev),
		map[string]any{"tag": "hr"},
		buildDivergenceTable(ev),
	}
	if action := buildDivergenceActionRow(ev); action != nil {
		elements = append(elements, map[string]any{"tag": "hr"}, action)
	}
	elements = append(elements, buildDivergenceFooterNote(ev))
	meta := divergenceCategories[ev.Category]
	headerColour := meta.Header
	if headerColour == "" {
		headerColour = "grey"
	}
	title := meta.Label
	if strings.TrimSpace(ev.CategoryLabel) != "" {
		title = ev.CategoryLabel
	}
	return map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": headerColour,
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "📊 " + title,
			},
		},
		"elements": elements,
	}
}

func buildDivergenceKPIStrip(ev DivergencePushEvent) map[string]any {
	kpi := ev.KPI
	return map[string]any{
		"tag": "div",
		"fields": []any{
			summaryField("CEX 独有", fmt.Sprintf("%d", kpi.CEXOnlyCount)),
			summaryField("DEX 独有", fmt.Sprintf("%d", kpi.DEXOnlyCount)),
			summaryField("显著分歧", fmt.Sprintf("%d", kpi.HeavyCount)),
			summaryField("edgeX 缺口", fmt.Sprintf("%d", kpi.EdgexGapCount)),
		},
	}
}

// buildDivergenceTable renders the per-row table. The displayed
// columns differ per category so each card focuses on the dimension
// that matters:
//   - cex_only      → "#CEX rank" + "CEX 24h"
//   - dex_only      → "#DEX rank" + "DEX 24h"
//   - heavy_gap     → "#CEX / #DEX" + "Δ rank"
//   - both_hot_gap  → "#CEX / #DEX" + min(rank)
func buildDivergenceTable(ev DivergencePushEvent) map[string]any {
	if len(ev.Rows) == 0 {
		return map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": "_（无符合的标的）_",
			},
		}
	}
	lines := make([]string, 0, len(ev.Rows))
	for _, row := range ev.Rows {
		lines = append(lines, formatDivergenceRow(ev.Category, row))
	}
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": strings.Join(lines, "\n"),
		},
	}
}

func formatDivergenceRow(category string, row DivergencePushRow) string {
	switch category {
	case DivergenceCategoryCEXOnly:
		return fmt.Sprintf("● **%s** · CEX rank **#%d** · 24h $%s · %d 家平台",
			row.Symbol, derefInt(row.CEXRank), humanUSDPtr(row.CEXVolUSD), row.CEXPlatforms)
	case DivergenceCategoryDEXOnly:
		return fmt.Sprintf("● **%s** · DEX rank **#%d** · 24h $%s · %d 家平台",
			row.Symbol, derefInt(row.DEXRank), humanUSDPtr(row.DEXVolUSD), row.DEXPlatforms)
	case DivergenceCategoryHeavyGap:
		return fmt.Sprintf("● **%s** · CEX #%d / DEX #%d · Δ **%d**",
			row.Symbol, derefInt(row.CEXRank), derefInt(row.DEXRank), derefInt(row.RankDelta))
	case DivergenceCategoryBothHotGap:
		return fmt.Sprintf("● **%s** · CEX #%d / DEX #%d · min #%d",
			row.Symbol, derefInt(row.CEXRank), derefInt(row.DEXRank), minRankOfRow(row))
	default:
		return fmt.Sprintf("● **%s**", row.Symbol)
	}
}

func minRankOfRow(row DivergencePushRow) int {
	switch {
	case row.CEXRank != nil && row.DEXRank != nil:
		if *row.CEXRank < *row.DEXRank {
			return *row.CEXRank
		}
		return *row.DEXRank
	case row.CEXRank != nil:
		return *row.CEXRank
	case row.DEXRank != nil:
		return *row.DEXRank
	}
	return 0
}

func humanUSDPtr(p *float64) string {
	if p == nil {
		return "—"
	}
	return humanUSD(*p)
}

func buildDivergenceActionRow(ev DivergencePushEvent) map[string]any {
	if strings.TrimSpace(ev.DashboardURL) == "" {
		return nil
	}
	return map[string]any{
		"tag": "action",
		"actions": []any{
			map[string]any{
				"tag":  "button",
				"type": "primary",
				"text": map[string]any{
					"tag":     "plain_text",
					"content": "📊 查看 Top30 详情",
				},
				"url": ev.DashboardURL,
			},
		},
	}
}

func buildDivergenceFooterNote(ev DivergencePushEvent) map[string]any {
	parts := []string{"触发时间 " + formatTriggerTime(ev.TriggerTime)}
	if ev.DedupeKey != "" {
		parts = append(parts, ev.DedupeKey)
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

// ProduceDivergencePush is the engine-side producer. It mirrors
// ProduceTop30Push: load latest snapshot rows → fail-close on stale
// → build events → per event write a signal_observation + an outbox
// row (with NextAttemptAt staggered by PushCfg.SendSpacing).
func ProduceDivergencePush(ctx context.Context, repo *Repository, deps DivergenceDeps) (DivergencePushResult, error) {
	if repo == nil {
		return DivergencePushResult{}, errors.New("listing divergence push: repo is nil")
	}
	if !deps.PushCfg.Enabled {
		return DivergencePushResult{Enabled: false}, nil
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	now := deps.Now()
	rows, latest, err := repo.loadTop30LatestRows(ctx)
	if err != nil {
		return DivergencePushResult{Enabled: true}, fmt.Errorf("load top30 rows: %w", err)
	}
	if latest.IsZero() {
		return DivergencePushResult{Enabled: true, FailClosed: "no_snapshot"}, nil
	}
	stale := deps.PushCfg.StaleAfter
	if stale <= 0 {
		stale = 15 * time.Minute
	}
	if now.Sub(latest) > stale {
		return DivergencePushResult{Enabled: true, SnapshotTS: latest, FailClosed: "snapshot_stale"}, nil
	}
	topN := deps.PushCfg.TopNPerCard
	if topN <= 0 {
		topN = 10
	}
	events := BuildDivergencePushEvents(rows, deps.DivergenceCfg, topN, now)
	result := DivergencePushResult{
		Enabled:    true,
		SnapshotTS: latest,
		Produced:   len(events),
	}
	if len(events) < 4 {
		result.SkippedEmpty = 4 - len(events)
	}

	maxAttempts := deps.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	outboxBatchIdx := 0
	for i := range events {
		ev := &events[i]
		ev.TriggerTime = now
		ev.DashboardURL = buildDashboardSymbolURL(deps.DashboardBase, "")
		if strings.TrimSpace(deps.DashboardBase) != "" {
			ev.DashboardURL = deps.DashboardBase
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			return result, fmt.Errorf("marshal divergence event: %w", err)
		}
		fingerprint := fmt.Sprintf("top30_divergence|%s|%s", ev.Category, ev.SnapshotDate)
		signal := SignalObservation{
			SignalType:      SignalTop30Divergence,
			SignalSubtype:   ev.Category,
			SourcePlatform:  "top30",
			CanonicalSymbol: strings.ToUpper(ev.Category),
			DisplaySymbol:   ev.CategoryLabel,
			MarketSurface:   "perp",
			InstrumentKind:  "canonical",
			ObservedAt:      now,
			Fingerprint:     fingerprint,
			PayloadJSON:     payload,
		}
		if _, _, err := repo.InsertSignal(ctx, signal); err != nil {
			return result, fmt.Errorf("insert divergence signal: %w", err)
		}
		result.Signals++

		outboxPayload, err := RenderDivergencePostMessage(*ev)
		if err != nil {
			return result, fmt.Errorf("render divergence card: %w", err)
		}
		status := OutboxStatusPending
		if strings.TrimSpace(deps.WebhookURL) == "" {
			status = OutboxStatusDisabled
		}
		nextAttempt := now
		if deps.PushCfg.SendSpacing > 0 {
			nextAttempt = now.Add(time.Duration(outboxBatchIdx) * deps.PushCfg.SendSpacing)
		}
		eventType := divergenceCategories[ev.Category].EventTyp
		if eventType == "" {
			eventType = "top30_divergence"
		}
		if err := repo.insertOutbox(ctx, DeliveryOutbox{
			EventType:     eventType,
			DedupeKey:     ev.DedupeKey,
			TargetChannel: DeliveryChannelLarkTop30,
			Status:        status,
			MaxAttempts:   maxAttempts,
			PayloadJSON:   outboxPayload,
			NextAttemptAt: ptrTime(nextAttempt),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return result, fmt.Errorf("insert divergence outbox: %w", err)
		}
		result.OutboxRows++
		outboxBatchIdx++
	}
	return result, nil
}
