package listing

import "strings"

const BusinessScoreVersion = "v2"

// ScoreInput is the immutable per-candidate input used by the scoring
// gate. Fusion populates it from the latest signals; nothing else
// reaches into the scoring engine.
type ScoreInput struct {
	Platforms       []string
	EvidenceKind    string
	EdgexListed     bool
	MarketSurface   string
	InstrumentKind  string
	ConfidenceLevel string
}

// ScoreResult is what fusion writes back onto the candidate row.
type ScoreResult struct {
	BusinessScore        *float64
	BusinessScoreVersion string
	Recommendation       string
	RecommendationLabel  string
	LifecycleStatus      string
	LifecycleStatusLabel string
	ConfidenceLevel      string
}

// ScoreCandidate applies the source-first v2 score table plus the
// edgeX listed override.
//
//   - edgeX listed  => no_action / already_listed regardless of score.
//   - API / announcement evidence only changes the operator-facing signal
//     wording and confidence; it does NOT change the numeric score nor the
//     perp recommendation tier for the same source platform mix.
//   - spot candidates stay low-risk (record_only / watch) until ops
//     explicitly opts spot into the prepare-listing workflow.
//   - otherwise the score is determined by the combined platform tier,
//     and the recommendation follows the PRD source-platform mapping.
func ScoreCandidate(in ScoreInput) ScoreResult {
	if in.EdgexListed {
		return ScoreResult{
			Recommendation:       RecommendationNoAction,
			RecommendationLabel:  RecommendationLabels[RecommendationNoAction],
			LifecycleStatus:      LifecycleAlreadyListed,
			LifecycleStatusLabel: LifecycleStatusLabels[LifecycleAlreadyListed],
			BusinessScoreVersion: BusinessScoreVersion,
		}
	}

	platforms := normalisePlatforms(in.Platforms)
	score, recommendation := scoreAndRecommend(platforms)

	if strings.EqualFold(in.MarketSurface, "spot") {
		recommendation = RecommendationRecordOnly
		if in.EvidenceKind == EvidenceAnnouncementAndAPI || len(platforms) >= 2 {
			recommendation = RecommendationWatch
		}
	}

	out := ScoreResult{
		BusinessScoreVersion: BusinessScoreVersion,
		Recommendation:       recommendation,
		RecommendationLabel:  RecommendationLabels[recommendation],
		ConfidenceLevel:      deriveConfidence(in, platforms),
	}
	if score > 0 {
		v := score
		out.BusinessScore = &v
	}
	return out
}

func normalisePlatforms(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, p := range in {
		out[strings.ToLower(p)] = struct{}{}
	}
	return out
}

func scoreAndRecommend(platforms map[string]struct{}) (float64, string) {
	has := func(p string) bool { _, ok := platforms[p]; return ok }
	count := len(platforms)
	switch {
	case has("binance") && count >= 2:
		return 90, RecommendationPrepareListing
	case has("binance"):
		return 80, RecommendationPrepareListing
	case (has("bybit") || has("okx") || has("hyperliquid")) && count >= 2:
		return 65, RecommendationWatch
	case has("mexc") && has("bitget"):
		return 60, RecommendationWatch
	case count == 1:
		if has("bybit") || has("okx") || has("hyperliquid") {
			return 55, RecommendationRecordOnly
		}
		return 40, RecommendationRecordOnly
	}
	return 0, RecommendationRecordOnly
}

func deriveConfidence(in ScoreInput, platforms map[string]struct{}) string {
	switch in.EvidenceKind {
	case EvidenceAnnouncementAndAPI:
		if _, ok := platforms["binance"]; ok && len(platforms) >= 2 {
			return ConfidenceHigh
		}
		return ConfidenceMediumHigh
	case EvidenceInstrumentDiffOnly:
		if len(platforms) >= 2 {
			return ConfidenceMediumHigh
		}
		return ConfidenceMedium
	case EvidenceAnnouncementPendingAPI:
		return ConfidenceMedium
	}
	return ConfidenceLow
}
