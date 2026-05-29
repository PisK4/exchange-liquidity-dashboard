package listing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/listing/liquidity"
)

// LiquidityAlertDeps wires the dependencies for one ProduceLiquidityAlertPush
// tick. Most fields mirror Top30Deps so engine.RunOnce composes the
// same way; the resolver / universe / index are injected so unit
// tests can plug in fakes.
type LiquidityAlertDeps struct {
	LoadUniverse  func() (*config.ListedUniverse, error)
	Now           func() time.Time
	DashboardBase string
	WebhookURL    string
	MaxAttempts   int
	Cfg           config.LiquidityAlertConfig
	Resolver      liquidity.CanonicalResolver
	Index         *config.CanonicalIndex
}

// LiquidityAlertResult is the per-tick summary surfaced via RunSummary.
type LiquidityAlertResult struct {
	Candidates  int    // total compute candidates (lag + worst across canonicals)
	FirstAlerts int    // new alert rows pushed
	Reissued    int    // 6h reissue rows pushed
	Cleared     int    // recovery cards pushed
	Silent      int    // state updates that did NOT push (cooldown / streak building)
	OutboxRows  int    // total outbox rows enqueued this tick
	FailClosed  string // non-empty when the producer short-circuited
}

// ProduceLiquidityAlertPush is the producer entry point for #10 / #11.
// Idempotency contract:
//   - At most one outbox row per (kind, canonical, dedupe_key); the
//     UNIQUE KEY on t_listing_delivery_outbox.dedupe_key + INSERT
//     IGNORE makes re-runs in the same minute a no-op.
//   - State is updated for EVERY evaluated canonical (active or
//     freshly seen) so clear_streak / last_evaluated_at stay accurate.
//
// Fail-closed:
//   - Cfg.Enabled == false              → FailClosed = "disabled"
//   - DepthTierPct ≤ 0                  → FailClosed = "tier_unset"
//   - LoadUniverse error or empty       → FailClosed = "universe_*"
//   - Empty depth matrix (no fresh data) → FailClosed = "no_snapshot"
func ProduceLiquidityAlertPush(ctx context.Context, repo *Repository, deps LiquidityAlertDeps) (LiquidityAlertResult, error) {
	if repo == nil {
		return LiquidityAlertResult{}, errors.New("liquidity push: repo is nil")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if !deps.Cfg.Enabled {
		return LiquidityAlertResult{FailClosed: "disabled"}, nil
	}
	if deps.Cfg.DepthTierPct <= 0 {
		return LiquidityAlertResult{FailClosed: "tier_unset"}, nil
	}
	if deps.LoadUniverse == nil {
		return LiquidityAlertResult{FailClosed: "universe_loader_missing"}, nil
	}
	universe, err := deps.LoadUniverse()
	if err != nil {
		return LiquidityAlertResult{FailClosed: "universe_load_error"}, fmt.Errorf("load universe: %w", err)
	}
	if universe == nil || !universe.Loaded() {
		return LiquidityAlertResult{FailClosed: "universe_not_loaded"}, nil
	}

	now := deps.Now()
	tierLabel := mysqlTierLabel(deps.Cfg.DepthTierPct)
	staleAfter := deps.Cfg.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}
	matrix, err := repo.LoadFreshDepthMatrix(ctx, tierLabel, staleAfter, now, deps.Index)
	if err != nil {
		return LiquidityAlertResult{}, fmt.Errorf("load depth matrix: %w", err)
	}
	if len(matrix) == 0 {
		return LiquidityAlertResult{FailClosed: "no_snapshot"}, nil
	}

	resolver := deps.Resolver
	if resolver == nil && deps.Index != nil {
		resolver = canonicalIndexResolver{idx: deps.Index}
	}
	candidates := liquidity.Compute(matrix, universe, resolver, liquidity.Config{
		DepthTierPct:     deps.Cfg.DepthTierPct,
		LagThreshold:     deps.Cfg.LagThreshold,
		MinComparators:   deps.Cfg.MinComparators,
		ReissueInterval:  deps.Cfg.ReissueInterval,
		ClearConsecutive: deps.Cfg.ClearConsecutive,
	}, now)

	result := LiquidityAlertResult{Candidates: len(candidates)}

	// Index the triggered candidates so the active-state sweep can
	// detect "active but no candidate this tick → clear path".
	triggered := make(map[string]*liquidity.AlertCandidate, len(candidates))
	for i := range candidates {
		c := &candidates[i]
		triggered[stateKey(c.Kind, c.Canonical)] = c
	}

	activeStates, err := repo.ListActiveAlertStates(ctx, []liquidity.AlertKind{liquidity.KindLiquidityLag, liquidity.KindWorstDepth})
	if err != nil {
		return result, fmt.Errorf("list active alert states: %w", err)
	}

	// Build the union of "candidates this tick" ∪ "currently active".
	// We need deterministic iteration order so smoke runs are
	// reproducible — sort by (kind, canonical).
	type workItem struct {
		kind      liquidity.AlertKind
		canonical string
		candidate *liquidity.AlertCandidate // nil for clear-only sweep
	}
	seen := make(map[string]bool, len(candidates)+len(activeStates))
	items := make([]workItem, 0, len(candidates)+len(activeStates))
	for i := range candidates {
		c := &candidates[i]
		key := stateKey(c.Kind, c.Canonical)
		seen[key] = true
		items = append(items, workItem{kind: c.Kind, canonical: c.Canonical, candidate: c})
	}
	for _, st := range activeStates {
		key := stateKey(st.Kind, st.Canonical)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, workItem{kind: st.Kind, canonical: st.Canonical, candidate: nil})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].kind == items[j].kind {
			return items[i].canonical < items[j].canonical
		}
		return string(items[i].kind) < string(items[j].kind)
	})

	cfg := liquidity.Config{
		DepthTierPct:     deps.Cfg.DepthTierPct,
		LagThreshold:     deps.Cfg.LagThreshold,
		MinComparators:   deps.Cfg.MinComparators,
		ReissueInterval:  deps.Cfg.ReissueInterval,
		ClearConsecutive: deps.Cfg.ClearConsecutive,
	}
	maxAttempts := deps.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	outboxBatchIdx := 0
	maxPerTick := deps.Cfg.MaxPerTick
	for _, item := range items {
		if maxPerTick > 0 && result.OutboxRows >= maxPerTick {
			break
		}
		prev, _, err := repo.LoadAlertState(ctx, item.kind, item.canonical)
		if err != nil {
			return result, fmt.Errorf("load alert state %s/%s: %w", item.kind, item.canonical, err)
		}
		// Ensure prev carries Kind/Canonical so DecideAction never
		// returns NewState with empty discriminators.
		prev.Kind = item.kind
		prev.Canonical = item.canonical

		isTriggered := item.candidate != nil
		decision := liquidity.DecideAction(prev, isTriggered, item.kind, item.canonical, cfg, now)

		// Silent path: only update state.
		if decision.Action == liquidity.ActionSilent {
			result.Silent++
			if err := repo.UpsertAlertState(ctx, decision.NewState, nil); err != nil {
				return result, fmt.Errorf("upsert alert state silent: %w", err)
			}
			continue
		}

		var (
			card        liquidity.CardPayload
			cardJSON    []byte
			payloadJSON []byte
		)
		card = buildLiquidityCardPayload(item, decision, prev, deps, now)
		card.DedupeKey = decision.DedupeKey
		payloadJSON, err = liquidity.RenderLiquidityPostMessage(card)
		if err != nil {
			return result, fmt.Errorf("render liquidity card: %w", err)
		}
		cardJSON, err = json.Marshal(card)
		if err != nil {
			return result, fmt.Errorf("marshal severity snapshot: %w", err)
		}

		status := OutboxStatusPending
		if strings.TrimSpace(deps.WebhookURL) == "" {
			status = OutboxStatusDisabled
		}
		nextAttempt := now
		if deps.Cfg.SendSpacing > 0 {
			nextAttempt = now.Add(time.Duration(outboxBatchIdx) * deps.Cfg.SendSpacing)
		}
		eventType := eventTypeForKind(item.kind)
		if err := repo.insertOutbox(ctx, DeliveryOutbox{
			EventType:     eventType,
			DedupeKey:     decision.DedupeKey,
			TargetChannel: DeliveryChannelLarkLiquidity,
			Status:        status,
			MaxAttempts:   maxAttempts,
			PayloadJSON:   payloadJSON,
			NextAttemptAt: ptrTime(nextAttempt),
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			return result, fmt.Errorf("insert outbox: %w", err)
		}
		outboxBatchIdx++
		result.OutboxRows++

		if err := repo.UpsertAlertState(ctx, decision.NewState, cardJSON); err != nil {
			return result, fmt.Errorf("upsert alert state pushed: %w", err)
		}

		switch decision.Action {
		case liquidity.ActionFirstTrigger:
			result.FirstAlerts++
		case liquidity.ActionReissue:
			result.Reissued++
		case liquidity.ActionClear:
			result.Cleared++
		}
	}
	return result, nil
}

// canonicalIndexResolver adapts *config.CanonicalIndex to the
// liquidity.CanonicalResolver interface. It exists so callers can
// pass a nil deps.Resolver and have the producer build a default
// resolver from deps.Index (the common production wiring).
type canonicalIndexResolver struct {
	idx *config.CanonicalIndex
}

func (c canonicalIndexResolver) ResolveCanonical(platform, base string) string {
	if c.idx == nil {
		return strings.ToUpper(strings.TrimSpace(base))
	}
	return c.idx.Resolve(platform, base)
}

func (c canonicalIndexResolver) IsPlatformExclusive(canonical string) bool {
	if c.idx == nil {
		return false
	}
	return c.idx.IsPlatformExclusive(canonical)
}

func stateKey(kind liquidity.AlertKind, canonical string) string {
	return string(kind) + "|" + canonical
}

func eventTypeForKind(kind liquidity.AlertKind) string {
	switch kind {
	case liquidity.KindLiquidityLag:
		return DeliveryEventLiquidityLag
	case liquidity.KindWorstDepth:
		return DeliveryEventWorstDepth
	}
	return string(kind)
}

// mysqlTierLabel mirrors collector.go's `fmt.Sprintf("%.2f%%", pct*100)`
// formula so the producer's read SELECT lands on rows that were
// actually persisted by the depth collector. Keep this in sync with
// internal/collector/collector.go: any format drift here would
// silently break every alert.
func mysqlTierLabel(tierPct float64) string {
	return fmt.Sprintf("%.2f%%", tierPct*100)
}

func buildLiquidityCardPayload(
	item struct {
		kind      liquidity.AlertKind
		canonical string
		candidate *liquidity.AlertCandidate
	},
	decision liquidity.ActionDecision,
	prev liquidity.AlertState,
	deps LiquidityAlertDeps,
	now time.Time,
) liquidity.CardPayload {
	card := liquidity.CardPayload{
		Kind:             item.kind,
		Phase:            decision.Phase,
		Canonical:        item.canonical,
		Tier:             mysqlTierLabel(deps.Cfg.DepthTierPct),
		SeveritySeq:      decision.NewState.SeveritySeq,
		ReissueIdx:       decision.NewState.ReissueCount,
		FirstTriggeredAt: decision.NewState.FirstTriggeredAt,
		EvaluatedAt:      now,
		LagThreshold:     deps.Cfg.LagThreshold,
		DashboardURL:     liquidity.BuildDashboardURL(deps.DashboardBase, item.canonical, mysqlTierLabel(deps.Cfg.DepthTierPct)),
	}
	if item.candidate != nil {
		c := item.candidate
		card.DisplaySymbol = c.DisplaySymbol
		card.Tier = c.Tier
		card.EdgexDepth = c.EdgexDepth
		card.MedianDepth = c.MedianDepth
		card.Ratio = c.Ratio
		card.Comparators = c.Comparators
		card.TotalPlatforms = c.TotalPlatforms
		card.EdgexRank = c.EdgexRank
		card.Platforms = c.Platforms
		card.DashboardURL = liquidity.BuildDashboardURL(deps.DashboardBase, item.canonical, c.Tier)
	}
	// Clear cards carry the FIRST trigger timestamp (so the operator
	// can see how long the lag persisted), not now.
	if decision.Phase == liquidity.PhaseClear && !prev.FirstTriggeredAt.IsZero() {
		card.FirstTriggeredAt = prev.FirstTriggeredAt
	}
	return card
}
