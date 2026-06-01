package listing

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// updateGolden, when -update is passed, rewrites the testdata files
// instead of asserting. Useful after intentional renderer changes:
//
//	go test ./internal/listing/ -run TestDecisionCardGolden -update
//
// CI should never set this flag; the assertions catch drift.
var updateGolden = flag.Bool("update", false, "rewrite decision-card golden fixtures")

// goldenScenario is a single fixture row. Each row pairs a logical
// scenario name (used both as the fixture filename and the t.Run
// sub-test name) with a fully wired DecisionCardEvent. The renderer
// is exercised end-to-end and the prettified JSON is compared
// byte-for-byte with testdata/decision_card_golden/<name>.json.
//
// Fixture coverage: 5 evidence_kind paths × the typical recommendation
// for each, plus 3 degradation paths (CG miss, status empty, depth
// nil). Together they pin down the wire format every operator sees.
type goldenScenario struct {
	name string
	ev   DecisionCardEvent
}

func TestDecisionCardGolden(t *testing.T) {
	scenarios := []goldenScenario{
		{"announcement_and_api_prepare", goldenAnnouncementAndAPIPrepare()},
		{"instrument_diff_only_watch", goldenInstrumentDiffOnlyWatch()},
		{"announcement_pending_api_pre_assessment", goldenAnnouncementPendingPreAssessment()},
		{"top30_only_record", goldenTop30OnlyRecord()},
		{"manual_seed_watch", goldenManualSeedWatch()},
		{"degraded_coingecko_unavailable", goldenDegradedCoinGecko()},
		{"degraded_empty_market_status", goldenDegradedEmptyStatus()},
		{"degraded_depth_unavailable", goldenDegradedDepth()},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			got, err := RenderDecisionCardPostMessage(sc.ev)
			if err != nil {
				t.Fatalf("render err = %v", err)
			}
			// Re-parse and re-marshal with indentation so diffs are
			// readable and ordering is deterministic — the
			// encoder we use already sorts map keys.
			var parsed any
			if err := json.Unmarshal(got, &parsed); err != nil {
				t.Fatalf("parse rendered card: %v", err)
			}
			pretty, err := json.MarshalIndent(parsed, "", "  ")
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			path := filepath.Join("testdata", "decision_card_golden", sc.name+".json")
			if *updateGolden {
				if err := os.WriteFile(path, append(pretty, '\n'), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to bootstrap)", path, err)
			}
			want = bytes.TrimRight(want, "\n")
			pretty = bytes.TrimRight(pretty, "\n")
			if !bytes.Equal(want, pretty) {
				t.Errorf("golden drift for %s\n--- want ---\n%s\n--- got ---\n%s", sc.name, want, pretty)
			}
		})
	}
}

// ---- scenario builders ----------------------------------------------------

func fxTime(y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, time.UTC)
}

func fxPtrFloat(v float64) *float64 { return &v }

func fxStandardRiskPlan(candidateID int64) RiskPlan {
	lev := 50.0
	return RiskPlan{
		ID: 901, CandidateID: candidateID, RiskPlanVersion: "v1",
		TemplateName:    RiskTemplateTier1Standard,
		MaxLeverage:     &lev,
		MMQuoteRequired: true,
		LeverageTiersJSON: json.RawMessage(
			`[{"position_usd_max":50000,"max_leverage":50},{"position_usd_max":250000,"max_leverage":20},{"position_usd_max":1000000,"max_leverage":5}]`,
		),
	}
}

func fxPreAssessmentRiskPlan(candidateID int64) RiskPlan {
	return RiskPlan{
		ID: 902, CandidateID: candidateID, RiskPlanVersion: "v1",
		TemplateName:      RiskTemplatePreAssessment,
		MMQuoteRequired:   false,
		LeverageTiersJSON: json.RawMessage(`[]`),
	}
}

func goldenAnnouncementAndAPIPrepare() DecisionCardEvent {
	return DecisionCardEvent{
		CandidateID:     201,
		RiskPlanID:      901,
		CanonicalSymbol: "PEPE",
		DisplaySymbol:   "PEPE-USDT (perp)",
		EvidenceKind:    EvidenceAnnouncementAndAPI,
		Recommendation:  RecommendationPrepareListing,
		ConfidenceLevel: ConfidenceHigh,
		BusinessScore:   82,
		SourcePlatforms: []string{"binance", "bybit"},
		Actions:         standardButtonMatrix(),
		DedupeKey:       "listing_decision|201|2026-05-31",
		TriggerTime:     fxTime(2026, 5, 31, 6, 30),
		Enrichment: DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: []PlatformMarketStatus{
				{Platform: "binance", DisplayName: "Binance Futures", Status: StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api", OccurredAt: fxTime(2026, 5, 30, 2, 0)},
				{Platform: "bybit", DisplayName: "Bybit Linear", Status: StatusPreListing, StatusLabel: "公告已发", SourceKind: "announcement", OccurredAt: fxTime(2026, 5, 31, 0, 15)},
			},
			MarketCapUSD:     fxPtrFloat(450_000_000),
			Spot24hVolumeUSD: fxPtrFloat(110_000_000),
			SpotDepth:        &DepthEvidence{Platform: "binance", USDValue: 820_000, Tier: "2pct"},
			PerpDepth:        &DepthEvidence{Platform: "binance", USDValue: 1_800_000, Tier: "2pct"},
			CoinGeckoID:      "pepe",
		},
		RiskPlan: fxStandardRiskPlan(201),
	}
}

func goldenInstrumentDiffOnlyWatch() DecisionCardEvent {
	return DecisionCardEvent{
		CandidateID:     202,
		RiskPlanID:      902,
		CanonicalSymbol: "WIF",
		DisplaySymbol:   "WIF-USDT (perp)",
		EvidenceKind:    EvidenceInstrumentDiffOnly,
		Recommendation:  RecommendationWatch,
		ConfidenceLevel: ConfidenceMedium,
		BusinessScore:   58,
		SourcePlatforms: []string{"okx"},
		Actions:         standardButtonMatrix(),
		DedupeKey:       "listing_decision|202|2026-05-31",
		TriggerTime:     fxTime(2026, 5, 31, 8, 0),
		Enrichment: DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: []PlatformMarketStatus{
				{Platform: "okx", DisplayName: "OKX Perp", Status: StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api", OccurredAt: fxTime(2026, 5, 31, 7, 45)},
			},
			MarketCapUSD:     fxPtrFloat(220_000_000),
			Spot24hVolumeUSD: fxPtrFloat(48_000_000),
			SpotDepth:        &DepthEvidence{Platform: "okx", USDValue: 310_000, Tier: "2pct"},
			PerpDepth:        &DepthEvidence{Platform: "okx", USDValue: 760_000, Tier: "2pct"},
			CoinGeckoID:      "dogwifcoin",
		},
		RiskPlan: fxStandardRiskPlan(202),
	}
}

func goldenAnnouncementPendingPreAssessment() DecisionCardEvent {
	return DecisionCardEvent{
		CandidateID:     203,
		RiskPlanID:      903,
		CanonicalSymbol: "NEWT",
		DisplaySymbol:   "NEWT-USDT (perp)",
		EvidenceKind:    EvidenceAnnouncementPendingAPI,
		Recommendation:  RecommendationPreAssessment,
		ConfidenceLevel: ConfidenceMediumHigh,
		BusinessScore:   65,
		SourcePlatforms: []string{"bybit"},
		Actions:         standardButtonMatrix(),
		DedupeKey:       "listing_decision|203|2026-05-31",
		TriggerTime:     fxTime(2026, 5, 31, 10, 15),
		Enrichment: DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: []PlatformMarketStatus{
				{Platform: "bybit", DisplayName: "Bybit Linear", Status: StatusPreListing, StatusLabel: "公告刚发布", SourceKind: "announcement", OccurredAt: fxTime(2026, 5, 31, 10, 0)},
			},
			MarketCapUSD:     fxPtrFloat(18_000_000),
			Spot24hVolumeUSD: fxPtrFloat(3_500_000),
			CoinGeckoID:      "newt",
		},
		RiskPlan: fxPreAssessmentRiskPlan(203),
	}
}

func goldenTop30OnlyRecord() DecisionCardEvent {
	return DecisionCardEvent{
		CandidateID:     204,
		RiskPlanID:      904,
		CanonicalSymbol: "HYPE",
		DisplaySymbol:   "HYPE-USDT (perp)",
		EvidenceKind:    EvidenceTop30Only,
		Recommendation:  RecommendationRecordOnly,
		ConfidenceLevel: ConfidenceLow,
		BusinessScore:   42,
		SourcePlatforms: []string{"hyperliquid", "bybit"},
		Actions:         standardButtonMatrix(),
		DedupeKey:       "listing_decision|204|2026-05-31",
		TriggerTime:     fxTime(2026, 5, 31, 12, 0),
		Enrichment: DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: true,
			MarketStatuses: nil,
			MarketCapUSD:    fxPtrFloat(95_000_000),
			Spot24hVolumeUSD: fxPtrFloat(15_000_000),
			SpotDepth:        &DepthEvidence{Platform: "bybit", USDValue: 90_000, Tier: "2pct"},
			PerpDepth:        &DepthEvidence{Platform: "hyperliquid", USDValue: 240_000, Tier: "2pct"},
			CoinGeckoID:      "hyperliquid",
		},
		RiskPlan: fxStandardRiskPlan(204),
	}
}

func goldenManualSeedWatch() DecisionCardEvent {
	return DecisionCardEvent{
		CandidateID:     205,
		RiskPlanID:      905,
		CanonicalSymbol: "ABCDEF",
		DisplaySymbol:   "ABCDEF-USDT (manual)",
		EvidenceKind:    EvidenceManualSeed,
		Recommendation:  RecommendationWatch,
		ConfidenceLevel: ConfidenceMedium,
		BusinessScore:   55,
		SourcePlatforms: []string{},
		Actions:         standardButtonMatrix(),
		DedupeKey:       "listing_decision|205|2026-05-31",
		TriggerTime:     fxTime(2026, 5, 31, 14, 30),
		Enrichment: DecisionCardEnrichment{
			EdgexListed: false, EdgexListedKnown: false,
			MarketCapUSD:     fxPtrFloat(35_000_000),
			Spot24hVolumeUSD: fxPtrFloat(2_000_000),
			CoinGeckoID:      "abcdef",
		},
		RiskPlan: fxStandardRiskPlan(205),
	}
}

func goldenDegradedCoinGecko() DecisionCardEvent {
	ev := goldenAnnouncementAndAPIPrepare()
	ev.CandidateID = 301
	ev.RiskPlanID = 911
	ev.DedupeKey = "listing_decision|301|2026-05-31"
	ev.Enrichment.MarketCapUSD = nil
	ev.Enrichment.Spot24hVolumeUSD = nil
	ev.Enrichment.CoinGeckoID = ""
	ev.Enrichment.EnrichErrors = []string{"coingecko: 429 too many requests"}
	return ev
}

func goldenDegradedEmptyStatus() DecisionCardEvent {
	ev := goldenInstrumentDiffOnlyWatch()
	ev.CandidateID = 302
	ev.RiskPlanID = 912
	ev.DedupeKey = "listing_decision|302|2026-05-31"
	ev.Enrichment.MarketStatuses = nil
	ev.Enrichment.EnrichErrors = []string{"market_status: no rows"}
	return ev
}

func goldenDegradedDepth() DecisionCardEvent {
	ev := goldenAnnouncementAndAPIPrepare()
	ev.CandidateID = 303
	ev.RiskPlanID = 913
	ev.DedupeKey = "listing_decision|303|2026-05-31"
	ev.Enrichment.SpotDepth = nil
	ev.Enrichment.PerpDepth = nil
	ev.Enrichment.EnrichErrors = []string{"depth: all platforms unavailable"}
	return ev
}
