package listing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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
// Chinese labels rendered on the card buttons.
var DecisionActionLabels = map[string]string{
	DecisionActionPrepareListing: "准备上架",
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
	CandidateID     int64                `json:"candidate_id"`
	RiskPlanID      int64                `json:"risk_plan_id"`
	CanonicalSymbol string               `json:"canonical_symbol"`
	DisplaySymbol   string               `json:"display_symbol"`
	EvidenceKind    string               `json:"evidence_kind"`
	Recommendation  string               `json:"recommendation"`
	ConfidenceLevel string               `json:"confidence_level"`
	BusinessScore   float64              `json:"business_score,omitempty"`
	SourcePlatforms []string             `json:"source_platforms"`
	Actions         []DecisionCardAction `json:"actions"`
	DedupeKey       string               `json:"dedupe_key"`
	TriggerTime     time.Time            `json:"trigger_time"`
}

// BuildDecisionCardEvent assembles the per-candidate decision card
// payload. The function is pure: no DB access. It owns the
// evidence_kind gated button matrix:
//
//   - announcement_and_api or instrument_diff_only with high
//     confidence → all four buttons (准备上架 / 进入观察 / 联系MM /
//     忽略).
//   - announcement_pending_api → no 准备上架 (per §5 公告误报 risk
//     control); only 进入观察 / 联系MM / 忽略.
//   - everything else (already_listed candidates filtered earlier
//     by the producer) → empty Actions; the caller must skip such
//     cards before they reach the outbox.
func BuildDecisionCardEvent(c Candidate, plan RiskPlan, triggerTime time.Time) DecisionCardEvent {
	ev := DecisionCardEvent{
		CandidateID:     c.ID,
		RiskPlanID:      plan.ID,
		CanonicalSymbol: c.CanonicalSymbol,
		DisplaySymbol:   c.DisplaySymbol,
		EvidenceKind:    c.EvidenceKind,
		Recommendation:  c.Recommendation,
		ConfidenceLevel: c.ConfidenceLevel,
		SourcePlatforms: append([]string{}, c.SourcePlatforms...),
		TriggerTime:     triggerTime,
		DedupeKey:       fmt.Sprintf("listing_decision|%d|%s", c.ID, triggerTime.UTC().Format("2006-01-02")),
	}
	if c.BusinessScore != nil {
		ev.BusinessScore = *c.BusinessScore
	}
	ev.Actions = buttonMatrixFor(c.EvidenceKind)
	return ev
}

func buttonMatrixFor(evidence string) []DecisionCardAction {
	base := []DecisionCardAction{
		{Action: DecisionActionPrepareListing, Label: DecisionActionLabels[DecisionActionPrepareListing]},
		{Action: DecisionActionEnterWatchlist, Label: DecisionActionLabels[DecisionActionEnterWatchlist]},
		{Action: DecisionActionContactMM, Label: DecisionActionLabels[DecisionActionContactMM]},
		{Action: DecisionActionIgnore, Label: DecisionActionLabels[DecisionActionIgnore]},
	}
	if evidence == EvidenceAnnouncementPendingAPI {
		out := make([]DecisionCardAction, 0, len(base)-1)
		for _, a := range base {
			if a.Action == DecisionActionPrepareListing {
				continue
			}
			out = append(out, a)
		}
		return out
	}
	return base
}

// RenderDecisionCardPostMessage builds the Lark interactive card
// envelope. Keeping the structure minimal: header + summary fields +
// buttons. Renderer changes are intentionally cheap because the
// callback contract lives in the button `value` payload, not the
// visual layout.
func RenderDecisionCardPostMessage(ev DecisionCardEvent) ([]byte, error) {
	header := map[string]any{
		"title":    map[string]any{"tag": "plain_text", "content": fmt.Sprintf("[%s] %s 决策候选", ev.EvidenceKind, ev.DisplaySymbol)},
		"template": "blue",
	}
	fields := []map[string]any{
		{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**Symbol**\n%s", ev.CanonicalSymbol)}},
		{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**Recommendation**\n%s", ev.Recommendation)}},
		{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**Confidence**\n%s", ev.ConfidenceLevel)}},
		{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**Score**\n%.0f", ev.BusinessScore)}},
	}
	actions := make([]map[string]any, 0, len(ev.Actions))
	for _, a := range ev.Actions {
		btn := map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "plain_text", "content": a.Label},
			"type": "primary",
			"value": map[string]any{
				"candidate_id": ev.CandidateID,
				"risk_plan_id": ev.RiskPlanID,
				"action":       a.Action,
				"dedupe_key":   ev.DedupeKey,
			},
		}
		if a.Action == DecisionActionIgnore {
			btn["type"] = "default"
		}
		actions = append(actions, btn)
	}
	card := map[string]any{
		"header": header,
		"elements": []map[string]any{
			{"tag": "div", "fields": fields},
			{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**Source platforms**: %v", ev.SourcePlatforms)}},
			{"tag": "action", "actions": actions},
			{"tag": "note", "elements": []map[string]any{{"tag": "plain_text", "content": fmt.Sprintf("dedupe_key=%s trigger=%s", ev.DedupeKey, ev.TriggerTime.UTC().Format(time.RFC3339))}}},
		},
	}
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

// DecisionCardDeps wires the producer's runtime knobs. IgnoreCooldown
// is the §5 default-24h抑制 window — operators can configure it via
// listing_agent.decision_card.ignore_cooldown. MaxPerTick caps the
// number of decision cards produced per engine tick so a fresh
// deploy with hundreds of unfused candidates does not burst the Lark
// channel.
type DecisionCardDeps struct {
	Now            func() time.Time
	IgnoreCooldown time.Duration
	MaxPerTick     int
}

// DecisionCardResult is the per-tick summary written onto RunSummary.
type DecisionCardResult struct {
	Considered      int
	RiskPlans       int
	OutboxRows      int
	SkippedCooldown int
	SkippedNoAction int
}

// ProduceDecisionCards is the producer entry point. For each
// actionable candidate it: (1) checks the ignore cooldown against
// the latest decision; (2) derives + persists a risk plan; (3)
// writes the decision card outbox row keyed on
// `listing_decision|{candidate_id}|YYYY-MM-DD` so re-runs within
// the same UTC day are no-ops.
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
	now := deps.Now()

	candidates, err := repo.ListCandidates(ctx, CandidateFilter{Limit: deps.MaxPerTick})
	if err != nil {
		return DecisionCardResult{}, fmt.Errorf("list candidates: %w", err)
	}
	res := DecisionCardResult{Considered: len(candidates)}

	for _, c := range candidates {
		if c.Recommendation == RecommendationNoAction || c.LifecycleStatus == LifecycleAlreadyListed {
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

		plan := BuildRiskPlanFromCandidate(c, now)
		planID, err := repo.UpsertRiskPlan(ctx, plan)
		if err != nil {
			return res, fmt.Errorf("upsert risk plan %d: %w", c.ID, err)
		}
		plan.ID = planID
		res.RiskPlans++

		ev := BuildDecisionCardEvent(c, plan, now)
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
