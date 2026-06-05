package listing

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"regexp"
	"strings"
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

func TestDecisionEvidenceSignatureStableAcrossSignalOrder(t *testing.T) {
	signalsA := []SignalObservation{
		{Fingerprint: "instrument_diff:okx:CARD:new_symbol:9700"},
		{Fingerprint: "announcement_listing:bitget:CARD:9000"},
	}
	signalsB := []SignalObservation{
		{Fingerprint: "announcement_listing:bitget:CARD:9000"},
		{Fingerprint: "instrument_diff:okx:CARD:new_symbol:9700"},
	}

	if got := decisionEvidenceSignature(signalsA); got != "b96183cc9624bc17" {
		t.Fatalf("signature A = %q, want b96183cc9624bc17", got)
	}
	if got := decisionEvidenceSignature(signalsB); got != "b96183cc9624bc17" {
		t.Fatalf("signature B = %q, want same order-independent signature", got)
	}
}

func TestDecisionEvidenceSignatureEmptySetIsStable(t *testing.T) {
	if got := decisionEvidenceSignature(nil); got != "e3b0c44298fc1c14" {
		t.Fatalf("empty signature = %q, want stable sha256 empty prefix", got)
	}
}

func TestBuildDecisionDedupeKeyIgnoresTriggerDate(t *testing.T) {
	signals := []SignalObservation{{Fingerprint: "instrument_diff:okx:CARD:new_symbol:9700"}}
	signature := decisionEvidenceSignature(signals)

	first := buildDecisionDedupeKey(12, signature)
	second := buildDecisionDedupeKey(12, signature)

	if first != "listing_decision|12|a11c8399d2945cca" {
		t.Fatalf("dedupe key = %q, want evidence signature key", first)
	}
	if second != first {
		t.Fatalf("same evidence produced different dedupe keys: %q vs %q", first, second)
	}
}

func TestBuildDecisionDedupeKeyChangesWhenEvidenceChanges(t *testing.T) {
	oneSignal := []SignalObservation{{Fingerprint: "instrument_diff:okx:CARD:new_symbol:9700"}}
	twoSignals := []SignalObservation{
		{Fingerprint: "instrument_diff:okx:CARD:new_symbol:9700"},
		{Fingerprint: "announcement_listing:bitget:CARD:9000"},
	}

	oldKey := buildDecisionDedupeKey(12, decisionEvidenceSignature(oneSignal))
	newKey := buildDecisionDedupeKey(12, decisionEvidenceSignature(twoSignals))

	if oldKey == newKey {
		t.Fatalf("dedupe key did not change after evidence changed: %q", oldKey)
	}
	if newKey != "listing_decision|12|b96183cc9624bc17" {
		t.Fatalf("new evidence key = %q, want listing_decision|12|b96183cc9624bc17", newKey)
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.id, s.signal_type, s.signal_subtype")).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(fusionSignalColumns()))
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

func TestProduceDecisionCardsSkipsStablecoinQuoteCollateralCandidate(t *testing.T) {
	now := time.Date(2026, 6, 2, 10, 4, 50, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(13389), "USDC", "USDC_USD1", "spot", "canonical",
		LifecycleAPIDetectedNoAnnouncement, LifecycleStatusLabels[LifecycleAPIDetectedNoAnnouncement], EvidenceInstrumentDiffOnly, ConfidenceLow,
		nil, BusinessScoreVersion, RecommendationRecordOnly, RecommendationLabels[RecommendationRecordOnly],
		[]byte(`["gate"]`), nil, now.Add(-12*time.Second), now.Add(-12*time.Second),
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).WillReturnRows(candidateRows)

	res, err := ProduceDecisionCards(context.Background(), repo, DecisionCardDeps{Now: func() time.Time { return now }, MaxPerTick: 10})
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.RiskPlans != 0 || res.OutboxRows != 0 {
		t.Fatalf("res = %+v, want stablecoin candidate skipped before card generation", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

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

func TestProduceDecisionCardsSkipsNoActionRecommendationEvenWhenLifecycleActionable(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(10), "HIST", "HIST-USDT (perp)", "perp", "canonical",
		LifecycleAPIDetectedNoAnnouncement, LifecycleStatusLabels[LifecycleAPIDetectedNoAnnouncement], EvidenceInstrumentDiffOnly, ConfidenceLow,
		nil, BusinessScoreVersion, RecommendationNoAction, RecommendationLabels[RecommendationNoAction],
		[]byte(`["okx"]`), nil, now.Add(-30*24*time.Hour), now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).WillReturnRows(candidateRows)

	res, err := ProduceDecisionCards(context.Background(), repo, DecisionCardDeps{Now: func() time.Time { return now }, MaxPerTick: 10})
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.SkippedNoAction != 1 || res.RiskPlans != 0 || res.OutboxRows != 0 {
		t.Fatalf("res = %+v, want no_action gate skip", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceDecisionCardsSkipsAlreadyListedLifecycleEvenWhenRecommendationActionable(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(11), "HIST2", "HIST2-USDT (perp)", "perp", "canonical",
		LifecycleAlreadyListed, LifecycleStatusLabels[LifecycleAlreadyListed], EvidenceInstrumentDiffOnly, ConfidenceLow,
		nil, BusinessScoreVersion, RecommendationPrepareListing, RecommendationLabels[RecommendationPrepareListing],
		[]byte(`["okx"]`), nil, now.Add(-30*24*time.Hour), now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).WillReturnRows(candidateRows)

	res, err := ProduceDecisionCards(context.Background(), repo, DecisionCardDeps{Now: func() time.Time { return now }, MaxPerTick: 10})
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.SkippedNoAction != 1 || res.RiskPlans != 0 || res.OutboxRows != 0 {
		t.Fatalf("res = %+v, want already_listed gate skip", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceDecisionCardsUsesLinkedSignalListingTimeInPayload(t *testing.T) {
	now := time.Date(2026, 6, 2, 7, 45, 0, 0, time.UTC)
	listingTime := time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	score := 80.0
	candidateRows := sqlmock.NewRows([]string{
		"id", "canonical_symbol", "display_symbol", "market_surface", "instrument_kind",
		"lifecycle_status", "lifecycle_status_label", "evidence_kind", "confidence_level",
		"business_score", "business_score_version", "recommendation", "recommendation_label",
		"source_platforms_json", "top30_enrichment_json", "first_observed_at", "last_observed_at",
	}).AddRow(
		int64(12), "CARD", "CARD-USDT (perp)", "perp", "canonical",
		LifecycleConfirmedListingCandidate, LifecycleStatusLabels[LifecycleConfirmedListingCandidate], EvidenceAnnouncementAndAPI, ConfidenceHigh,
		score, BusinessScoreVersion, RecommendationPrepareListing, RecommendationLabels[RecommendationPrepareListing],
		[]byte(`["okx"]`), nil, now.Add(-2*time.Hour), now.Add(-1*time.Hour),
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, canonical_symbol, display_symbol")).WillReturnRows(candidateRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action, callback_ts FROM t_listing_decision")).WithArgs(int64(12)).WillReturnRows(sqlmock.NewRows([]string{"action", "callback_ts"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_risk_plan")).WillReturnResult(sqlmock.NewResult(202, 1))
	signalRows := sqlmock.NewRows(fusionSignalColumns())
	addFusionInstrumentSignal(signalRows, 9700, "okx", DiffNewSymbol, "CARD", StatusActive, now, &listingTime)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.id, s.signal_type, s.signal_subtype")).WithArgs(int64(12)).WillReturnRows(signalRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).WithArgs(
		DeliveryEventListingDecisionCandidate,
		"listing_decision|12|a11c8399d2945cca",
		DeliveryChannelLarkTop30,
		OutboxStatusPending,
		0,
		5,
		now,
		payloadContainsAll("Listing Time", "2026-05-07 17:00 UTC+8", "trigger=2026-06-02 15:45 UTC+8", "dedupe=listing_decision|12|a11c8399d2945cca"),
		nil,
		nil,
		now,
		now,
	).WillReturnResult(sqlmock.NewResult(302, 1))

	res, err := ProduceDecisionCards(context.Background(), repo, DecisionCardDeps{Now: func() time.Time { return now }, MaxPerTick: 10})
	if err != nil {
		t.Fatalf("ProduceDecisionCards err = %v", err)
	}
	if res.OutboxRows != 1 {
		t.Fatalf("OutboxRows = %d, want 1", res.OutboxRows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

type payloadContains string

func (p payloadContains) Match(v driver.Value) bool {
	var s string
	switch x := v.(type) {
	case []byte:
		s = string(x)
	case string:
		s = x
	default:
		return false
	}
	for _, needle := range strings.Split(string(p), "\x00") {
		if needle != "" && !strings.Contains(s, needle) {
			return false
		}
	}
	return true
}

func payloadContainsAll(needles ...string) payloadContains {
	return payloadContains(strings.Join(needles, "\x00"))
}
