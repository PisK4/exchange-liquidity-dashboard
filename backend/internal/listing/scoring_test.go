package listing

import "testing"

func TestBusinessScoreBinancePlusOther(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:      []string{"binance", "bybit"},
		EvidenceKind:   EvidenceAnnouncementAndAPI,
		MarketSurface:  "perp",
		InstrumentKind: "canonical",
	})
	if got.BusinessScore == nil || *got.BusinessScore != 90 {
		t.Fatalf("score = %v, want 90", got.BusinessScore)
	}
	if got.Recommendation != RecommendationPrepareListing {
		t.Fatalf("recommendation = %q, want prepare_listing", got.Recommendation)
	}
	if got.RecommendationLabel != RecommendationLabels[RecommendationPrepareListing] {
		t.Fatalf("label = %q", got.RecommendationLabel)
	}
}

func TestBusinessScoreBinanceOnly(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:    []string{"binance"},
		EvidenceKind: EvidenceInstrumentDiffOnly,
	})
	if got.BusinessScore == nil || *got.BusinessScore != 80 {
		t.Fatalf("score = %v, want 80", got.BusinessScore)
	}
	if got.Recommendation != RecommendationPrepareListing {
		t.Fatalf("recommendation = %q", got.Recommendation)
	}
}

func TestBusinessScoreTwoTier2Watch(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:    []string{"bybit", "okx"},
		EvidenceKind: EvidenceAnnouncementAndAPI,
	})
	if got.BusinessScore == nil || *got.BusinessScore != 65 {
		t.Fatalf("score = %v, want 65", got.BusinessScore)
	}
	if got.Recommendation != RecommendationWatch {
		t.Fatalf("recommendation = %q", got.Recommendation)
	}
}

func TestBusinessScoreMexcBitget(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:    []string{"mexc", "bitget"},
		EvidenceKind: EvidenceAnnouncementAndAPI,
	})
	if got.BusinessScore == nil || *got.BusinessScore != 60 {
		t.Fatalf("score = %v, want 60", got.BusinessScore)
	}
	if got.Recommendation != RecommendationWatch {
		t.Fatalf("recommendation = %q", got.Recommendation)
	}
}

func TestBusinessScoreSingleNonBinance(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:    []string{"mexc"},
		EvidenceKind: EvidenceInstrumentDiffOnly,
	})
	if got.Recommendation != RecommendationRecordOnly {
		t.Fatalf("recommendation = %q, want record_only", got.Recommendation)
	}
	if got.RecommendationLabel != RecommendationLabels[RecommendationRecordOnly] {
		t.Fatalf("label = %q", got.RecommendationLabel)
	}
}

func TestAnnouncementOnlyForcesPreAssessment(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:    []string{"binance"},
		EvidenceKind: EvidenceAnnouncementPendingAPI,
	})
	if got.Recommendation != RecommendationPreAssessment {
		t.Fatalf("recommendation = %q, want pre_assessment", got.Recommendation)
	}
	if got.RecommendationLabel != "进入预评估" {
		t.Fatalf("label = %q", got.RecommendationLabel)
	}
}

func TestSpotAnnouncementOnlyStaysRecordOnly(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:     []string{"binance"},
		EvidenceKind:  EvidenceAnnouncementPendingAPI,
		MarketSurface: "spot",
	})
	if got.Recommendation != RecommendationRecordOnly {
		t.Fatalf("spot announcement-only recommendation = %q, want record_only", got.Recommendation)
	}
}

func TestSpotDualEvidenceCanWatchButNotPrepareListing(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:     []string{"binance", "hyperliquid"},
		EvidenceKind:  EvidenceAnnouncementAndAPI,
		MarketSurface: "spot",
	})
	if got.Recommendation != RecommendationWatch {
		t.Fatalf("spot dual-evidence recommendation = %q, want watch", got.Recommendation)
	}
	if got.BusinessScore == nil || *got.BusinessScore != 90 {
		t.Fatalf("spot score should preserve platform-tier score, got %v", got.BusinessScore)
	}
}

func TestEdgexListedForcesNoAction(t *testing.T) {
	got := ScoreCandidate(ScoreInput{
		Platforms:    []string{"binance", "bybit"},
		EvidenceKind: EvidenceAnnouncementAndAPI,
		EdgexListed:  true,
	})
	if got.Recommendation != RecommendationNoAction {
		t.Fatalf("recommendation = %q, want no_action", got.Recommendation)
	}
	if got.LifecycleStatus != LifecycleAlreadyListed {
		t.Fatalf("lifecycle = %q, want already_listed", got.LifecycleStatus)
	}
}
