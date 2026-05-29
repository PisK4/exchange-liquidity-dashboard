package listing

import (
	"encoding/json"
	"strings"
	"time"
)

// RiskPlanVersion is bumped whenever the leverage table or the
// template selection rules change. It is persisted on every row so
// historical decision cards keep their original sizing intent even
// after the production table is retuned.
const RiskPlanVersion = "v1"

// Risk template names. Stable enums (not free-form strings) so
// the decision card renderer can switch on them without re-parsing
// the JSON tiers; the operator-facing label is derived in the card
// producer, not stored on the row, to keep i18n changes cheap.
const (
	RiskTemplateTier1Standard     = "tier1_standard"
	RiskTemplateTier2Conservative = "tier2_conservative"
	RiskTemplateRecordOnly        = "record_only"
	RiskTemplatePreAssessment     = "pre_assessment"
)

// RiskPlan mirrors one row of t_listing_risk_plan. The struct is
// INSERT-only: every regeneration writes a new row keyed on
// (candidate_id, generated_at) so the audit log keeps every plan
// version the producer ever attached to a card.
type RiskPlan struct {
	ID                 int64
	CandidateID        int64
	RiskPlanVersion    string
	TemplateName       string
	MaxLeverage        *float64
	MaxPositionUSD     *float64
	LeverageTiersJSON  json.RawMessage
	FundingInitialMode string
	MMQuoteRequired    bool
	RiskNotesJSON      json.RawMessage
	SourceEvidenceJSON json.RawMessage
	GeneratedAt        time.Time
	ApprovedAt         *time.Time
	CreatedAt          time.Time
}

// BuildRiskPlanFromCandidate derives the risk plan for the given
// candidate. The function is pure: it does not touch the database
// and does not look up source state, so it stays unit-testable
// independently of the producer.
//
// Template selection follows the §23.5 PRD table:
//
//   - prepare_listing + tier-1 platform (binance|okx) → tier1_standard
//     (50x leverage cap, MM quote required).
//   - prepare_listing without tier-1 platform → tier1_standard
//     conservatively kept because the recommendation gate has already
//     vouched for the platform mix (binance-only or binance+1).
//   - watch / record_only → tier2_conservative (20x cap, no MM quote).
//   - pre_assessment → pre_assessment placeholder (nil leverage),
//     since the instrument is not yet live and there is nothing
//     concrete to size.
//   - everything else (no_action, blank recommendation) →
//     record_only with no leverage cap.
func BuildRiskPlanFromCandidate(c Candidate, generatedAt time.Time) RiskPlan {
	plan := RiskPlan{
		CandidateID:     c.ID,
		RiskPlanVersion: RiskPlanVersion,
		GeneratedAt:     generatedAt,
	}

	tierOne := false
	for _, p := range c.SourcePlatforms {
		switch strings.ToLower(p) {
		case "binance", "okx":
			tierOne = true
		}
	}

	switch c.Recommendation {
	case RecommendationPrepareListing:
		plan.TemplateName = RiskTemplateTier1Standard
		lev := 50.0
		plan.MaxLeverage = &lev
		plan.MMQuoteRequired = true
		plan.LeverageTiersJSON = json.RawMessage(`[{"position_usd_max":50000,"max_leverage":50},{"position_usd_max":250000,"max_leverage":20},{"position_usd_max":1000000,"max_leverage":5}]`)
	case RecommendationWatch:
		plan.TemplateName = RiskTemplateTier2Conservative
		lev := 20.0
		plan.MaxLeverage = &lev
		plan.MMQuoteRequired = false
		plan.LeverageTiersJSON = json.RawMessage(`[{"position_usd_max":25000,"max_leverage":20},{"position_usd_max":100000,"max_leverage":5}]`)
	case RecommendationPreAssessment:
		plan.TemplateName = RiskTemplatePreAssessment
		plan.MMQuoteRequired = false
		plan.LeverageTiersJSON = json.RawMessage(`[]`)
	case RecommendationRecordOnly:
		plan.TemplateName = RiskTemplateRecordOnly
		plan.MMQuoteRequired = false
		plan.LeverageTiersJSON = json.RawMessage(`[]`)
	default:
		plan.TemplateName = RiskTemplateRecordOnly
		plan.MMQuoteRequired = false
		plan.LeverageTiersJSON = json.RawMessage(`[]`)
	}

	// tierOne kept for forward-compat: future tier-1-only knobs
	// (e.g. funding initial mode) hook in here without renaming
	// the template enum.
	_ = tierOne

	evidence := map[string]any{
		"evidence_kind":    c.EvidenceKind,
		"confidence":       c.ConfidenceLevel,
		"recommendation":   c.Recommendation,
		"business_score":   c.BusinessScore,
		"source_platforms": c.SourcePlatforms,
	}
	raw, _ := json.Marshal(evidence)
	plan.SourceEvidenceJSON = raw
	return plan
}
