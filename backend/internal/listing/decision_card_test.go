package listing

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestBuildDecisionCardEventPrepareListingShowsAllButtons asserts the
// happy-path button matrix from spec §Phase 2: a high-confidence
// `prepare_listing` candidate gets all four buttons (准备上线 /
// 进入观察 / 联系MM / 忽略) plus the risk_plan_id pinned to the
// payload so the callback can reconcile the dispatch.
func TestBuildDecisionCardEventPrepareListingShowsAllButtons(t *testing.T) {
	score := 90.0
	c := Candidate{
		ID:              7,
		CanonicalSymbol: "ABC",
		DisplaySymbol:   "ABC-USDT (perp)",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		EvidenceKind:    EvidenceAnnouncementAndAPI,
		ConfidenceLevel: ConfidenceHigh,
		BusinessScore:   &score,
		Recommendation:  RecommendationPrepareListing,
		SourcePlatforms: []string{"binance", "bybit"},
	}
	plan := RiskPlan{ID: 101, CandidateID: 7, TemplateName: RiskTemplateTier1Standard, MMQuoteRequired: true}
	ev := BuildDecisionCardEvent(c, plan, time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC))
	if ev.CandidateID != 7 {
		t.Errorf("CandidateID = %d, want 7", ev.CandidateID)
	}
	if ev.RiskPlanID != 101 {
		t.Errorf("RiskPlanID = %d, want 101", ev.RiskPlanID)
	}
	if ev.DedupeKey != "listing_decision|7|2026-05-30" {
		t.Errorf("DedupeKey = %q", ev.DedupeKey)
	}
	want := map[string]bool{
		DecisionActionPrepareListing: true,
		DecisionActionEnterWatchlist: true,
		DecisionActionContactMM:      true,
		DecisionActionIgnore:         true,
	}
	if len(ev.Actions) != len(want) {
		t.Errorf("len(Actions) = %d, want %d", len(ev.Actions), len(want))
	}
	for _, a := range ev.Actions {
		if !want[a.Action] {
			t.Errorf("unexpected action %q on prepare_listing card", a.Action)
		}
	}
}

// TestBuildDecisionCardEventAnnouncementPendingAPIShowsAllButtons
// locks PRD §6: every evidence_kind surfaces the full 4-button
// matrix. The earlier 3-button safety rail for announcement-only
// candidates has been removed because the cooldown gate +
// risk-plan template already encode the "公告待 API 确认" caution
// (recommendation=pre_assessment + risk_template=pre_assessment).
func TestBuildDecisionCardEventAnnouncementPendingAPIShowsAllButtons(t *testing.T) {
	score := 55.0
	c := Candidate{
		ID:              8,
		CanonicalSymbol: "DEF",
		DisplaySymbol:   "DEF-USDT (perp)",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		EvidenceKind:    EvidenceAnnouncementPendingAPI,
		ConfidenceLevel: ConfidenceMedium,
		BusinessScore:   &score,
		Recommendation:  RecommendationPreAssessment,
		SourcePlatforms: []string{"bybit"},
	}
	plan := RiskPlan{ID: 102, CandidateID: 8, TemplateName: RiskTemplatePreAssessment}
	ev := BuildDecisionCardEvent(c, plan, time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC))
	want := map[string]bool{
		DecisionActionPrepareListing: true,
		DecisionActionEnterWatchlist: true,
		DecisionActionContactMM:      true,
		DecisionActionIgnore:         true,
	}
	if len(ev.Actions) != len(want) {
		t.Errorf("len(Actions) = %d, want %d", len(ev.Actions), len(want))
	}
	for _, a := range ev.Actions {
		if !want[a.Action] {
			t.Errorf("unexpected action %q on announcement_pending_api card", a.Action)
		}
	}
}

// TestRenderDecisionCardPostMessageReturnsInteractiveCard verifies
// the produced Lark envelope is a valid interactive card carrying
// the four buttons as value=action pairs the callback decodes.
func TestRenderDecisionCardPostMessageReturnsInteractiveCard(t *testing.T) {
	ev := DecisionCardEvent{
		CandidateID:     7,
		RiskPlanID:      101,
		CanonicalSymbol: "ABC",
		DisplaySymbol:   "ABC-USDT (perp)",
		EvidenceKind:    EvidenceAnnouncementAndAPI,
		Recommendation:  RecommendationPrepareListing,
		BusinessScore:   80,
		DedupeKey:       "listing_decision|7|2026-05-30",
		Actions: []DecisionCardAction{
			{Action: DecisionActionPrepareListing, Label: "准备上线"},
			{Action: DecisionActionIgnore, Label: "忽略"},
		},
		TriggerTime: time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC),
	}
	body, err := RenderDecisionCardPostMessage(ev)
	if err != nil {
		t.Fatalf("render err = %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed["msg_type"] != "interactive" {
		t.Fatalf("msg_type = %v, want interactive", parsed["msg_type"])
	}
	if _, ok := parsed["card"]; !ok {
		t.Fatalf("body missing card field: %s", string(body))
	}
}

// TestProduceDecisionCardsSkipsCandidatesInIgnoreCooldown is the
// cornerstone of §5 风险控制: once an operator clicks 忽略 the same
// candidate must not be re-pushed within ignore_cooldown (default
// 24h). The producer queries the latest decision and, when it is an
// ignore action within the cooldown window, skips both the
// risk_plan write and the outbox insert.
func TestProduceDecisionCardsSkipsCandidatesInIgnoreCooldown(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	score := 80.0
	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(7), "ABC", "ABC-USDT (perp)", "perp", "canonical",
		LifecycleConfirmedListingCandidate, "已确认候选", EvidenceAnnouncementAndAPI, ConfidenceHigh,
		score, "v1", RecommendationPrepareListing, "准备上线",
		[]byte(`["binance"]`), nil, now.Add(-2*time.Hour), now.Add(-1*time.Hour),
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WillReturnRows(candidateRows)

	// Ignore decision 12h ago → still inside the 24h cooldown.
	ignoredAt := now.Add(-12 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action, callback_ts FROM t_listing_decision")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"action", "callback_ts"}).
			AddRow(DecisionActionIgnore, ignoredAt))

	deps := DecisionCardDeps{
		Now:            func() time.Time { return now },
		IgnoreCooldown: 24 * time.Hour,
		MaxPerTick:     10,
	}
	res, err := ProduceDecisionCards(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.SkippedCooldown != 1 {
		t.Errorf("SkippedCooldown = %d, want 1", res.SkippedCooldown)
	}
	if res.OutboxRows != 0 || res.RiskPlans != 0 {
		t.Errorf("res = %+v, want zero risk plan / outbox writes during cooldown", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestProduceDecisionCardsWritesRiskPlanAndOutboxForFreshCandidate
// covers the steady-state warm path: a candidate with no prior
// decision triggers (1) risk plan write, (2) outbox insert with the
// decision card payload.
func TestProduceDecisionCardsWritesRiskPlanAndOutboxForFreshCandidate(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	score := 80.0
	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(7), "ABC", "ABC-USDT (perp)", "perp", "canonical",
		LifecycleConfirmedListingCandidate, "已确认候选", EvidenceAnnouncementAndAPI, ConfidenceHigh,
		score, "v1", RecommendationPrepareListing, "准备上线",
		[]byte(`["binance"]`), nil, now.Add(-2*time.Hour), now.Add(-1*time.Hour),
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WillReturnRows(candidateRows)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT action, callback_ts FROM t_listing_decision")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"action", "callback_ts"}))

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_risk_plan")).
		WillReturnResult(sqlmock.NewResult(201, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WillReturnResult(sqlmock.NewResult(301, 1))

	deps := DecisionCardDeps{
		Now:            func() time.Time { return now },
		IgnoreCooldown: 24 * time.Hour,
		MaxPerTick:     10,
	}
	res, err := ProduceDecisionCards(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.RiskPlans != 1 {
		t.Errorf("RiskPlans = %d, want 1", res.RiskPlans)
	}
	if res.OutboxRows != 1 {
		t.Errorf("OutboxRows = %d, want 1", res.OutboxRows)
	}
	if res.SkippedCooldown != 0 {
		t.Errorf("SkippedCooldown = %d, want 0", res.SkippedCooldown)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestProduceDecisionCardsSkipsAlreadyListedCandidates covers the
// already_listed safety: when edgeX already lists the asset
// (recommendation=no_action) the producer must not generate a
// decision card; the operator does not need a button matrix for a
// symbol that is already live.
func TestProduceDecisionCardsSkipsAlreadyListedCandidates(t *testing.T) {
	now := time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(9), "BTC", "BTC-USDT (perp)", "perp", "canonical",
		LifecycleAlreadyListed, "edgeX 已上线", EvidenceAnnouncementAndAPI, ConfidenceHigh,
		nil, "v1", RecommendationNoAction, "无需动作",
		[]byte(`["binance"]`), nil, now.Add(-48*time.Hour), now.Add(-1*time.Hour),
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).
		WillReturnRows(candidateRows)

	deps := DecisionCardDeps{
		Now:            func() time.Time { return now },
		IgnoreCooldown: 24 * time.Hour,
		MaxPerTick:     10,
	}
	res, err := ProduceDecisionCards(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.SkippedNoAction != 1 {
		t.Errorf("SkippedNoAction = %d, want 1", res.SkippedNoAction)
	}
	if res.RiskPlans != 0 || res.OutboxRows != 0 {
		t.Errorf("res = %+v, want zero writes for already_listed", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
