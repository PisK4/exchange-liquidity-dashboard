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
	CardKPI       *DivergenceCardKPI        `json:"card_kpi,omitempty"`
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
//
// CEXPlatformDetails / DEXPlatformDetails carry the per-platform
// native ranks that contributed to the canonical bucket. The renderer
// uses these for the cex_only / dex_only two-line layout so operators
// see "binance #15 · okx #18 · ..." instead of just a count. Both
// slices are sorted by NativeRank ASC; missing or unresolved
// platforms simply do not appear.
type DivergencePushRow struct {
	Symbol             string                   `json:"symbol"`
	CEXRank            *int                     `json:"cex_rank,omitempty"`
	DEXRank            *int                     `json:"dex_rank,omitempty"`
	RankDelta          *int                     `json:"rank_delta,omitempty"`
	CEXVolUSD          *float64                 `json:"cex_volume_24h_usd,omitempty"`
	DEXVolUSD          *float64                 `json:"dex_volume_24h_usd,omitempty"`
	CEXPlatforms       int                      `json:"cex_platform_count"`
	DEXPlatforms       int                      `json:"dex_platform_count"`
	CEXPlatformDetails []DivergencePushPlatform `json:"cex_platform_details,omitempty"`
	DEXPlatformDetails []DivergencePushPlatform `json:"dex_platform_details,omitempty"`
}

// DivergencePushPlatform is one entry of a row's per-platform
// breakdown. NativeRank is the platform-local Top30 rank as reported
// by the source adapter — distinct from the canonical-aggregate rank
// computed by divergence.aggregateClass. Volume is the platform's own
// 24h figure pre-aggregation.
type DivergencePushPlatform struct {
	Platform     string  `json:"platform"`
	NativeRank   int     `json:"native_rank"`
	Volume24HUSD float64 `json:"volume_24h_usd,omitempty"`
}

// DivergenceCardKPI is the per-card summary strip used by the
// cex_only / dex_only renderers. It replaces the (cross-card) global
// KPI on those two cards so operators see the distribution and
// "headline pick" of the category they are currently looking at.
//
// TotalEligible is the count before TopN truncation; the field
// triplet (BroadCount/MidCount/NarrowCount) bins canonicals by
// PlatformCount so the operator can tell at a glance how many of the
// listed candidates are "broad" (5+ platforms) vs "narrow" (1-2).
// SideVolUSD sums the in-camp 24h volume across all eligible
// canonicals so the strip carries one quick aggregate figure.
//
// OppositeCampLabel is the human-facing "DEX" / "CEX" name used in
// the reverse-camp tag ("DEX 阵营 0 家 ❌") shown once on the strip
// instead of repeating on every row.
type DivergenceCardKPI struct {
	TotalEligible      int     `json:"total_eligible"`
	BroadCount         int     `json:"broad_count"`
	MidCount           int     `json:"mid_count"`
	NarrowCount        int     `json:"narrow_count"`
	SideVolUSD         float64 `json:"side_volume_24h_usd"`
	StrongestSymbol    string  `json:"strongest_symbol,omitempty"`
	StrongestPlatforms int     `json:"strongest_platform_count,omitempty"`
	StrongestBestRank  int     `json:"strongest_best_rank,omitempty"`
	OppositeCampLabel  string  `json:"opposite_camp_label,omitempty"`
}

// DivergenceDeps mirrors the Top30Deps style so the engine can wire
// both producers symmetrically.
//
// Resolver carries the alias-aware canonical lookup built from
// symbol_mapping.yaml. When non-nil the producer folds platform
// aliases (PAXG/XAUT/XAU → GOLD; CL/BRENTOIL → OIL; 1000PEPE/PEPE)
// before the divergence aggregation so cross-platform buckets merge
// onto a single canonical row. A nil resolver preserves legacy
// identity behaviour for tests and preview tooling that have not
// wired the config layer yet.
type DivergenceDeps struct {
	Now           func() time.Time
	DashboardBase string
	WebhookURL    string
	MaxAttempts   int
	DivergenceCfg config.Top30DivergenceConfig
	PushCfg       config.Top30DivergencePushConfig
	Resolver      divergence.CanonicalResolver
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
//
//  2. Call divergence.Compute to get the per-class aggregates and the
//     classified divergence rows.
//
//  3. Filter divergence rows on the *EdgexListed==false red line
//     (nil → drop; *true → drop). This is the alert-side strict
//     filter, separate from the KPI counter on the API path which
//     preserves legacy "any not-known-listed counts" behaviour.
//
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
func BuildDivergencePushEvents(rows []Top30RowForPush, cfg config.Top30DivergenceConfig, resolver divergence.CanonicalResolver, topN int, day time.Time) []DivergencePushEvent {
	if topN <= 0 {
		topN = 10
	}
	cexSet := platformMembershipSet(cfg.CEXPlatforms)
	dexSet := platformMembershipSet(cfg.DEXPlatforms)

	inputs := make([]divergence.InputRow, 0, len(rows))
	knownByCanonical := map[string]*bool{}

	// platformBreakdown captures (canonical, side, platform) → best
	// (lowest) native rank + volume so the renderer can fan out a
	// per-platform sub-row beneath each canonical bucket. We
	// recompute the resolver fold locally instead of leaning on
	// aggregateClass so the schema stays additive to the listing
	// layer — domain types and V1 collector wire format are
	// untouched.
	platformBreakdown := map[string]map[string]map[string]*platformAggInternal{}
	storePlatform := func(canonical, side, platform string, rank int, vol float64) {
		if _, ok := platformBreakdown[canonical]; !ok {
			platformBreakdown[canonical] = map[string]map[string]*platformAggInternal{}
		}
		if _, ok := platformBreakdown[canonical][side]; !ok {
			platformBreakdown[canonical][side] = map[string]*platformAggInternal{}
		}
		entry, ok := platformBreakdown[canonical][side][platform]
		if !ok {
			platformBreakdown[canonical][side][platform] = &platformAggInternal{rank: rank, volume: vol}
			return
		}
		if rank > 0 && (entry.rank == 0 || rank < entry.rank) {
			entry.rank = rank
		}
		if vol > entry.volume {
			entry.volume = vol
		}
	}

	for _, row := range rows {
		canonical := divergence.CanonicaliseSymbol(row.Symbol)
		if canonical == "" {
			continue
		}
		if resolver != nil {
			if resolved := resolver.ResolveCanonical(row.Platform, canonical); resolved != "" {
				canonical = resolved
			}
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
		if _, ok := cexSet[row.Platform]; ok {
			storePlatform(canonical, "cex", row.Platform, row.Rank, row.Volume24HUSD)
		} else if _, ok := dexSet[row.Platform]; ok {
			storePlatform(canonical, "dex", row.Platform, row.Rank, row.Volume24HUSD)
		}
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
		Resolver:             resolver,
	})

	eligible := filterDivergenceRowsForAlert(snapshot.Divergence, knownByCanonical)

	dayKey := day.UTC().Format("2006-01-02")
	out := make([]DivergencePushEvent, 0, 4)

	emit := func(category string, picked []domain.Top30DivergenceRow, sorter func(a, b domain.Top30DivergenceRow) bool) {
		if len(picked) == 0 {
			return
		}
		sort.SliceStable(picked, func(i, j int) bool { return sorter(picked[i], picked[j]) })
		card := computeDivergenceCardKPI(category, picked)
		if len(picked) > topN {
			picked = picked[:topN]
		}
		meta := divergenceCategories[category]
		rowsOut := make([]DivergencePushRow, 0, len(picked))
		for _, row := range picked {
			r := makeDivergencePushRow(row)
			if sides, ok := platformBreakdown[row.Symbol]; ok {
				r.CEXPlatformDetails = sortedPlatforms(sides["cex"])
				r.DEXPlatformDetails = sortedPlatforms(sides["dex"])
			}
			rowsOut = append(rowsOut, r)
		}
		out = append(out, DivergencePushEvent{
			Category:      category,
			CategoryLabel: meta.Label,
			Rows:          rowsOut,
			KPI:           snapshot.KPI,
			CardKPI:       card,
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

	// cex_only / dex_only get a tie-break on PlatformCount DESC so a
	// (rank #15, 5 platforms) candidate outranks a (rank #15, 1
	// platform) one — the user-facing visual benefit of "硬信号优先"
	// without changing the primary rank-ASC mental model.
	emit(DivergenceCategoryCEXOnly, cexOnly, func(a, b domain.Top30DivergenceRow) bool {
		if ra, rb := derefInt(a.CEXRank), derefInt(b.CEXRank); ra != rb {
			return ra < rb
		}
		if a.CEXPlatformCount != b.CEXPlatformCount {
			return a.CEXPlatformCount > b.CEXPlatformCount
		}
		return a.Symbol < b.Symbol
	})
	emit(DivergenceCategoryDEXOnly, dexOnly, func(a, b domain.Top30DivergenceRow) bool {
		if ra, rb := derefInt(a.DEXRank), derefInt(b.DEXRank); ra != rb {
			return ra < rb
		}
		if a.DEXPlatformCount != b.DEXPlatformCount {
			return a.DEXPlatformCount > b.DEXPlatformCount
		}
		return a.Symbol < b.Symbol
	})
	emit(DivergenceCategoryHeavyGap, heavy, func(a, b domain.Top30DivergenceRow) bool {
		return derefInt(a.RankDelta) > derefInt(b.RankDelta)
	})
	emit(DivergenceCategoryBothHotGap, bothHot, func(a, b domain.Top30DivergenceRow) bool {
		return minRank(a) < minRank(b)
	})

	return out
}

// platformMembershipSet builds a lookup set for "is platform X in
// camp Y?" checks. Platform names are case-sensitive on the wire
// (config + collector agree on the casing) so no lowercase fold.
func platformMembershipSet(platforms []string) map[string]struct{} {
	out := make(map[string]struct{}, len(platforms))
	for _, p := range platforms {
		out[p] = struct{}{}
	}
	return out
}

// sortedPlatforms turns a side bucket into a NativeRank-ASC slice.
func sortedPlatforms(bucket map[string]*platformAggInternal) []DivergencePushPlatform {
	if len(bucket) == 0 {
		return nil
	}
	out := make([]DivergencePushPlatform, 0, len(bucket))
	for platform, entry := range bucket {
		if entry == nil {
			continue
		}
		out = append(out, DivergencePushPlatform{
			Platform:     platform,
			NativeRank:   entry.rank,
			Volume24HUSD: entry.volume,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NativeRank != out[j].NativeRank {
			if out[i].NativeRank == 0 {
				return false
			}
			if out[j].NativeRank == 0 {
				return true
			}
			return out[i].NativeRank < out[j].NativeRank
		}
		return out[i].Platform < out[j].Platform
	})
	return out
}

// platformAggInternal is the file-scope mirror of the anonymous
// platformAgg type used inside BuildDivergencePushEvents; declared
// here so helper functions can reference it.
type platformAggInternal struct {
	rank   int
	volume float64
}

// computeDivergenceCardKPI produces the per-card KPI strip for
// cex_only and dex_only categories. For heavy_gap / both_hot_gap we
// return nil so the renderer falls back to the legacy global KPI
// strip; those cards present a cross-camp story that benefits less
// from per-card distribution numbers.
func computeDivergenceCardKPI(category string, eligible []domain.Top30DivergenceRow) *DivergenceCardKPI {
	switch category {
	case DivergenceCategoryCEXOnly, DivergenceCategoryDEXOnly:
	default:
		return nil
	}
	side := DivergenceCardKPI{TotalEligible: len(eligible)}
	if category == DivergenceCategoryCEXOnly {
		side.OppositeCampLabel = "DEX"
	} else {
		side.OppositeCampLabel = "CEX"
	}
	bestPlatforms, bestRank := 0, 1<<30
	bestSymbol := ""
	for _, row := range eligible {
		var pc int
		var vol *float64
		var rank *int
		if category == DivergenceCategoryCEXOnly {
			pc = row.CEXPlatformCount
			vol = row.CEXAdjustedVolUSD
			rank = row.CEXRank
		} else {
			pc = row.DEXPlatformCount
			vol = row.DEXAdjustedVolUSD
			rank = row.DEXRank
		}
		switch {
		case pc >= 5:
			side.BroadCount++
		case pc >= 3:
			side.MidCount++
		case pc >= 1:
			side.NarrowCount++
		}
		if vol != nil {
			side.SideVolUSD += *vol
		}
		r := 1 << 30
		if rank != nil {
			r = *rank
		}
		if pc > bestPlatforms || (pc == bestPlatforms && r < bestRank) {
			bestPlatforms = pc
			bestRank = r
			bestSymbol = row.Symbol
		}
	}
	if bestSymbol != "" {
		side.StrongestSymbol = bestSymbol
		side.StrongestPlatforms = bestPlatforms
		if bestRank < 1<<30 {
			side.StrongestBestRank = bestRank
		}
	}
	return &side
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
	// cex_only / dex_only carry a per-card KPI computed at build
	// time. heavy_gap / both_hot_gap keep the legacy four-field
	// global strip because their natural narrative is cross-camp
	// rather than per-camp distribution.
	if ev.CardKPI != nil {
		return buildDivergenceCardKPIStrip(ev)
	}
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

// buildDivergenceCardKPIStrip renders the per-card distribution strip
// used by cex_only / dex_only cards. Two lines of lark_md so the
// content stays compact while surfacing four signals at once:
//   - total eligible canonicals + breadth distribution
//   - opposite-camp absence tag (once, replacing per-row reminders)
//   - in-camp 24h aggregate volume + "strongest pick" callout
func buildDivergenceCardKPIStrip(ev DivergencePushEvent) map[string]any {
	c := ev.CardKPI
	sideLabel := "CEX"
	if ev.Category == DivergenceCategoryDEXOnly {
		sideLabel = "DEX"
	}
	line1 := fmt.Sprintf("**本卡 %d 项** · 5+ 平台 **%d** / 3-4 平台 **%d** / 1-2 平台 **%d** · %s 阵营 **0 家** ❌",
		c.TotalEligible, c.BroadCount, c.MidCount, c.NarrowCount, c.OppositeCampLabel)
	strongest := "—"
	if c.StrongestSymbol != "" {
		if c.StrongestBestRank > 0 {
			strongest = fmt.Sprintf("**%s**（%d 家 · 最佳 #%d）", c.StrongestSymbol, c.StrongestPlatforms, c.StrongestBestRank)
		} else {
			strongest = fmt.Sprintf("**%s**（%d 家）", c.StrongestSymbol, c.StrongestPlatforms)
		}
	}
	line2 := fmt.Sprintf("%s 合计 24h **$%s** · 最硬 %s", sideLabel, humanUSD(c.SideVolUSD), strongest)
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": line1 + "\n" + line2,
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
		main := fmt.Sprintf("● **%s** · CEX 合计 24h **$%s** · %d 家平台",
			row.Symbol, humanUSDPtr(row.CEXVolUSD), row.CEXPlatforms)
		if sub := formatPlatformBreakdown(row.CEXPlatformDetails); sub != "" {
			return main + "\n　 " + sub
		}
		return main
	case DivergenceCategoryDEXOnly:
		main := fmt.Sprintf("● **%s** · DEX 合计 24h **$%s** · %d 家平台",
			row.Symbol, humanUSDPtr(row.DEXVolUSD), row.DEXPlatforms)
		if sub := formatPlatformBreakdown(row.DEXPlatformDetails); sub != "" {
			return main + "\n　 " + sub
		}
		return main
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

// formatPlatformBreakdown renders the per-platform sub-row used by
// cex_only / dex_only cards. Empty / nil-rank platforms are skipped
// so a missing native rank does not surface a confusing "#0" badge.
// Format: "binance #15 · okx #18 · bybit #21" (no bullet, no quote
// suffix, no per-row 24h figures — the main row already carries the
// camp-level total).
func formatPlatformBreakdown(details []DivergencePushPlatform) string {
	if len(details) == 0 {
		return ""
	}
	parts := make([]string, 0, len(details))
	for _, d := range details {
		if d.NativeRank > 0 {
			parts = append(parts, fmt.Sprintf("%s #%d", d.Platform, d.NativeRank))
		} else {
			parts = append(parts, d.Platform)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
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
	events := BuildDivergencePushEvents(rows, deps.DivergenceCfg, deps.Resolver, topN, now)
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
