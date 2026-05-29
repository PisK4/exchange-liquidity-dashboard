package listing

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestBuildRiskPlanFromCandidatePrepareListingUsesHighLeverageTier
// locks the §23.5 mapping that drives the Phase 2 decision card:
// `prepare_listing` candidates with at least one tier-1 platform
// (binance / okx) get the "standard" 50x template; the template
// name is the stable enum that the card rendering keys on, not a
// floating-point that frontend has to round-trip.
func TestBuildRiskPlanFromCandidatePrepareListingUsesHighLeverageTier(t *testing.T) {
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	score := 90.0
	c := Candidate{
		ID:              42,
		CanonicalSymbol: "ABC",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		EvidenceKind:    EvidenceAnnouncementAndAPI,
		ConfidenceLevel: ConfidenceHigh,
		BusinessScore:   &score,
		Recommendation:  RecommendationPrepareListing,
		SourcePlatforms: []string{"binance", "bybit"},
	}
	plan := BuildRiskPlanFromCandidate(c, now)
	if plan.CandidateID != 42 {
		t.Errorf("CandidateID = %d, want 42", plan.CandidateID)
	}
	if plan.TemplateName != RiskTemplateTier1Standard {
		t.Errorf("TemplateName = %q, want %q", plan.TemplateName, RiskTemplateTier1Standard)
	}
	if plan.MaxLeverage == nil || *plan.MaxLeverage != 50 {
		t.Errorf("MaxLeverage = %v, want 50", plan.MaxLeverage)
	}
	if !plan.MMQuoteRequired {
		t.Errorf("MMQuoteRequired = false, want true for prepare_listing")
	}
	if plan.GeneratedAt != now {
		t.Errorf("GeneratedAt = %v, want %v", plan.GeneratedAt, now)
	}
	if len(plan.LeverageTiersJSON) == 0 {
		t.Errorf("LeverageTiersJSON empty; want JSON-encoded tier table")
	}
	if !json.Valid(plan.LeverageTiersJSON) {
		t.Errorf("LeverageTiersJSON not valid JSON: %s", string(plan.LeverageTiersJSON))
	}
	// SourceEvidenceJSON must carry evidence_kind + platforms verbatim
	// so the decision card render can rebuild it without re-deriving.
	var evidence map[string]any
	if err := json.Unmarshal(plan.SourceEvidenceJSON, &evidence); err != nil {
		t.Fatalf("SourceEvidenceJSON not JSON: %v", err)
	}
	if evidence["evidence_kind"] != EvidenceAnnouncementAndAPI {
		t.Errorf("evidence_kind = %v", evidence["evidence_kind"])
	}
}

// TestBuildRiskPlanFromCandidateWatchUsesConservativeTemplate covers
// the §23.5 row for `watch` recommendations (Bybit/OKX-only or
// MEXC+Bitget combos): leverage cap drops to 20x and MM quote is
// no longer required; the operator can still upgrade by editing the
// card before clicking "prepare listing", but the default is the
// cautious tier.
func TestBuildRiskPlanFromCandidateWatchUsesConservativeTemplate(t *testing.T) {
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	score := 65.0
	c := Candidate{
		ID:              43,
		CanonicalSymbol: "DEF",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		EvidenceKind:    EvidenceInstrumentDiffOnly,
		ConfidenceLevel: ConfidenceMediumHigh,
		BusinessScore:   &score,
		Recommendation:  RecommendationWatch,
		SourcePlatforms: []string{"bybit", "okx"},
	}
	plan := BuildRiskPlanFromCandidate(c, now)
	if plan.TemplateName != RiskTemplateTier2Conservative {
		t.Errorf("TemplateName = %q, want %q", plan.TemplateName, RiskTemplateTier2Conservative)
	}
	if plan.MaxLeverage == nil || *plan.MaxLeverage != 20 {
		t.Errorf("MaxLeverage = %v, want 20", plan.MaxLeverage)
	}
	if plan.MMQuoteRequired {
		t.Errorf("MMQuoteRequired = true, want false for watch recommendation")
	}
}

// TestBuildRiskPlanFromCandidatePreAssessmentUsesPlaceholderTemplate
// covers the announcement_pending_api branch: there is no instrument
// to size yet, so the plan is intentionally a placeholder template
// with nil leverage / no MM quote — the decision card will surface
// "进入预评估" rather than "准备上架" and the plan only exists to
// keep the audit log complete.
func TestBuildRiskPlanFromCandidatePreAssessmentUsesPlaceholderTemplate(t *testing.T) {
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	score := 55.0
	c := Candidate{
		ID:              44,
		CanonicalSymbol: "GHI",
		MarketSurface:   "perp",
		InstrumentKind:  "canonical",
		EvidenceKind:    EvidenceAnnouncementPendingAPI,
		ConfidenceLevel: ConfidenceMedium,
		BusinessScore:   &score,
		Recommendation:  RecommendationPreAssessment,
		SourcePlatforms: []string{"bybit"},
	}
	plan := BuildRiskPlanFromCandidate(c, now)
	if plan.TemplateName != RiskTemplatePreAssessment {
		t.Errorf("TemplateName = %q, want %q", plan.TemplateName, RiskTemplatePreAssessment)
	}
	if plan.MaxLeverage != nil {
		t.Errorf("MaxLeverage = %v, want nil for pre_assessment", plan.MaxLeverage)
	}
	if plan.MMQuoteRequired {
		t.Errorf("MMQuoteRequired = true, want false for pre_assessment")
	}
}

// TestRepositoryUpsertRiskPlanInsertsAuditRow covers the audit-log
// shape: the row is INSERT-only (each generation writes a new row
// for trace), but the helper returns the new auto-increment id so
// the producer can wire the risk_plan into the decision card payload.
func TestRepositoryUpsertRiskPlanInsertsAuditRow(t *testing.T) {
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	maxLev := 50.0
	plan := RiskPlan{
		CandidateID:        7,
		RiskPlanVersion:    RiskPlanVersion,
		TemplateName:       RiskTemplateTier1Standard,
		MaxLeverage:        &maxLev,
		LeverageTiersJSON:  json.RawMessage(`[{"position_usd_max":10000,"max_leverage":50}]`),
		MMQuoteRequired:    true,
		SourceEvidenceJSON: json.RawMessage(`{"evidence_kind":"announcement_and_api","platforms":["binance"]}`),
		GeneratedAt:        now,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO t_listing_risk_plan")).
		WillReturnResult(sqlmock.NewResult(101, 1))

	id, err := repo.UpsertRiskPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("UpsertRiskPlan err = %v", err)
	}
	if id != 101 {
		t.Errorf("id = %d, want 101", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryLatestRiskPlanByCandidateReturnsNewest exercises the
// read path used by the decision card producer to attach the latest
// plan to the candidate payload without re-deriving it.
func TestRepositoryLatestRiskPlanByCandidateReturnsNewest(t *testing.T) {
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "candidate_id", "risk_plan_version", "template_name", "max_leverage", "max_position_usd",
		"leverage_tiers_json", "funding_initial_mode", "mm_quote_required", "risk_notes_json",
		"source_evidence_json", "generated_at", "approved_at", "created_at",
	}).AddRow(
		int64(101), int64(7), "v1", RiskTemplateTier1Standard, 50.0, nil,
		[]byte(`[{"position_usd_max":10000,"max_leverage":50}]`), nil, 1, nil,
		[]byte(`{"evidence_kind":"announcement_and_api"}`), now, nil, now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, candidate_id, risk_plan_version, template_name")).
		WithArgs(int64(7)).
		WillReturnRows(rows)

	plan, err := repo.LatestRiskPlanByCandidate(context.Background(), 7)
	if err != nil {
		t.Fatalf("LatestRiskPlanByCandidate err = %v", err)
	}
	if plan == nil {
		t.Fatalf("plan = nil, want row")
	}
	if plan.ID != 101 || plan.CandidateID != 7 || plan.TemplateName != RiskTemplateTier1Standard {
		t.Errorf("plan fields wrong: %+v", plan)
	}
	if plan.MaxLeverage == nil || *plan.MaxLeverage != 50 {
		t.Errorf("plan.MaxLeverage = %v", plan.MaxLeverage)
	}
	if !plan.MMQuoteRequired {
		t.Errorf("plan.MMQuoteRequired = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestRepositoryLatestRiskPlanByCandidateReturnsNilOnEmpty verifies
// the producer's safe path: when no risk_plan row exists yet (first
// time the producer sees the candidate) the helper returns nil
// rather than an error so the caller branch on it cleanly.
func TestRepositoryLatestRiskPlanByCandidateReturnsNilOnEmpty(t *testing.T) {
	now := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, candidate_id, risk_plan_version, template_name")).
		WithArgs(int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "candidate_id", "risk_plan_version", "template_name", "max_leverage", "max_position_usd",
			"leverage_tiers_json", "funding_initial_mode", "mm_quote_required", "risk_notes_json",
			"source_evidence_json", "generated_at", "approved_at", "created_at",
		}))

	plan, err := repo.LatestRiskPlanByCandidate(context.Background(), 99)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if plan != nil {
		t.Errorf("plan = %+v, want nil for missing row", plan)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
