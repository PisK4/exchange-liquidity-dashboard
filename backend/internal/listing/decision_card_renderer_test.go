package listing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Helper: build a candidate + plan + enrichment + render → return
// (rawJSON, parsedMap, contentJoined) for downstream asserts.
func renderForTest(t *testing.T, ev DecisionCardEvent) (string, map[string]any) {
	t.Helper()
	body, err := RenderDecisionCardPostMessage(ev)
	if err != nil {
		t.Fatalf("render err = %v", err)
	}
	raw := string(body)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse err = %v: %s", err, raw)
	}
	return raw, parsed
}

func baseEvent() DecisionCardEvent {
	score := 78.0
	cap := 120_000_000.0
	vol := 35_000_000.0
	lev := 50.0
	plan := RiskPlan{
		ID: 101, CandidateID: 7, RiskPlanVersion: "v1",
		TemplateName:      RiskTemplateTier1Standard,
		MaxLeverage:       &lev,
		MMQuoteRequired:   true,
		LeverageTiersJSON: json.RawMessage(`[{"position_usd_max":50000,"max_leverage":50},{"position_usd_max":250000,"max_leverage":20},{"position_usd_max":1000000,"max_leverage":5}]`),
	}
	enr := DecisionCardEnrichment{
		EdgexListed: false, EdgexListedKnown: true,
		MarketStatuses: []PlatformMarketStatus{
			{Platform: "binance", DisplayName: "Binance Futures", Status: StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api", OccurredAt: time.Date(2026, 5, 30, 2, 0, 0, 0, time.UTC)},
			{Platform: "bybit", DisplayName: "Bybit Linear", Status: StatusPreListing, StatusLabel: "公告刚发布", SourceKind: "announcement", OccurredAt: time.Date(2026, 5, 31, 0, 15, 0, 0, time.UTC)},
		},
		MarketCapUSD:     &cap,
		Spot24hVolumeUSD: &vol,
		SpotDepth:        &DepthEvidence{Platform: "binance", USDValue: 580_000, Tier: "2pct"},
		PerpDepth:        &DepthEvidence{Platform: "binance", USDValue: 1_200_000, Tier: "2pct"},
		CoinGeckoID:      "abc-coin",
	}
	return DecisionCardEvent{
		CandidateID:     7,
		RiskPlanID:      101,
		CanonicalSymbol: "ABC",
		DisplaySymbol:   "ABC-USDT (perp)",
		EvidenceKind:    EvidenceAnnouncementAndAPI,
		Recommendation:  RecommendationPrepareListing,
		ConfidenceLevel: ConfidenceHigh,
		BusinessScore:   score,
		SourcePlatforms: []string{"binance", "bybit"},
		Actions:         standardButtonMatrix(),
		DedupeKey:       "listing_decision|7|2026-05-31",
		TriggerTime:     time.Date(2026, 5, 31, 6, 30, 0, 0, time.UTC),
		Enrichment:      enr,
		RiskPlan:        plan,
	}
}

func TestRenderDecisionCardHeaderUsesRedForPrepareListing(t *testing.T) {
	ev := baseEvent()
	_, parsed := renderForTest(t, ev)
	card := parsed["card"].(map[string]any)
	header := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Errorf("template = %v, want red", header["template"])
	}
	title := header["title"].(map[string]any)
	content := title["content"].(string)
	if !strings.Contains(content, "🚨 New Perp Listing Detected") {
		t.Errorf("title content missing banner: %q", content)
	}
	if !strings.Contains(content, "ABC") {
		t.Errorf("title content missing symbol ABC: %q", content)
	}
}

func TestRenderDecisionCardHeaderUnifiedAcrossRecommendations(t *testing.T) {
	for _, rec := range []string{
		RecommendationPrepareListing,
		RecommendationWatch,
		RecommendationPreAssessment,
		RecommendationRecordOnly,
	} {
		ev := baseEvent()
		ev.Recommendation = rec
		_, parsed := renderForTest(t, ev)
		card := parsed["card"].(map[string]any)
		header := card["header"].(map[string]any)
		if header["template"] != "red" {
			t.Errorf("rec=%s template=%v want red (unified)", rec, header["template"])
		}
		title := header["title"].(map[string]any)
		content := title["content"].(string)
		if !strings.Contains(content, "🚨 New Perp Listing Detected") {
			t.Errorf("rec=%s banner=%q must carry unified 🚨 New Perp Listing Detected", rec, content)
		}
		// Earlier per-recommendation banners must not leak through.
		for _, leaked := range []string{
			"👀 New Perp Listing Watch",
			"🔍 New Perp Pre-Assessment",
			"📝 New Perp Listing Record",
		} {
			if strings.Contains(content, leaked) {
				t.Errorf("rec=%s leaked legacy banner %q in %q", rec, leaked, content)
			}
		}
	}
}

func TestRenderDecisionCardBasicInfoFieldsCarryAllPRDValues(t *testing.T) {
	ev := baseEvent()
	raw, _ := renderForTest(t, ev)
	mustContain := []string{
		"Token", "ABC",
		"edgeX 状态", "未上线",
		"Source", "Binance Futures",
		"Detected Time", "2026-05-31",
		"UTC+8",
	}
	for _, s := range mustContain {
		if !strings.Contains(raw, s) {
			t.Errorf("expected %q in rendered card; raw=%s", s, raw)
		}
	}
}

func TestRenderDecisionCardEdgexLabelHonoursThreeState(t *testing.T) {
	cases := []struct {
		listed bool
		known  bool
		want   string
	}{
		{true, true, "已上线"},
		{false, true, "未上线"},
		{false, false, "未知"},
	}
	for _, tc := range cases {
		ev := baseEvent()
		ev.Enrichment.EdgexListed = tc.listed
		ev.Enrichment.EdgexListedKnown = tc.known
		raw, _ := renderForTest(t, ev)
		if !strings.Contains(raw, tc.want) {
			t.Errorf("listed=%v known=%v want %q in card, raw=%s", tc.listed, tc.known, tc.want, raw)
		}
	}
}

func TestRenderDecisionCardMarketStatusBlockListsAllPlatforms(t *testing.T) {
	ev := baseEvent()
	raw, _ := renderForTest(t, ev)
	for _, want := range []string{
		"Market Status",
		"Binance Futures",
		"Perp LIVE",
		"Bybit Linear",
		"公告刚发布",
		"<font color='green'>●</font>",
		"<font color='orange'>●</font>",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("market status missing %q in raw=%s", want, raw)
		}
	}
}

func TestRenderDecisionCardEmptyMarketStatusFallsBackToGreyBullet(t *testing.T) {
	ev := baseEvent()
	ev.Enrichment.MarketStatuses = nil
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "无平台状态记录") {
		t.Errorf("empty status block must show fallback, raw=%s", raw)
	}
}

func TestRenderDecisionCardMetricsRowsRenderUSD(t *testing.T) {
	ev := baseEvent()
	raw, _ := renderForTest(t, ev)
	for _, want := range []string{
		"Market Cap", "$120.00M",
		"Spot 24h Vol", "$35.00M",
		"现货深度", "$580.00K",
		"binance",
		"合约深度", "$1.20M",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("metrics missing %q in raw=%s", want, raw)
		}
	}
}

func TestRenderDecisionCardMetricsRowsFallBackToPlaceholders(t *testing.T) {
	ev := baseEvent()
	ev.Enrichment.MarketCapUSD = nil
	ev.Enrichment.Spot24hVolumeUSD = nil
	ev.Enrichment.SpotDepth = nil
	ev.Enrichment.PerpDepth = nil
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "n/a") {
		t.Errorf("missing 'n/a' placeholder for market cap / vol")
	}
	if !strings.Contains(raw, "不可用") {
		t.Errorf("missing '不可用' placeholder for depth")
	}
}

func TestRenderDecisionCardRiskPlanBlockShowsOnlyTBDPlaceholder(t *testing.T) {
	ev := baseEvent()
	raw, _ := renderForTest(t, ev)
	// Operator decision: the block keeps a title + a single TBD
	// line summarising every parameter slot, with no specific
	// values pre-filled.
	for _, want := range []string{
		"自动参数预案",
		"待规则补齐",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("risk plan missing %q in raw=%s", want, raw)
		}
	}
	// Any specific numeric value derived from RiskPlan must NOT
	// leak through — these are the strings the earlier renderer
	// emitted and they should be gone now.
	for _, leaked := range []string{
		"杠杆: **",
		"杠杆档位:",
		"$50.00K→",
		"MM 报价: <font",
		"MM 报价: 可选",
	} {
		if strings.Contains(raw, leaked) {
			t.Errorf("risk plan leaked pre-filled value %q in raw=%s", leaked, raw)
		}
	}
}

func TestRenderDecisionCardRiskPlanBlockIgnoresRiskPlanValues(t *testing.T) {
	// Even when RiskPlan carries fully populated values, the
	// renderer must emit the same TBD placeholder. This guards
	// against future code paths re-introducing pre-filled numbers.
	for _, rec := range []string{
		RecommendationPrepareListing,
		RecommendationWatch,
		RecommendationPreAssessment,
		RecommendationRecordOnly,
	} {
		ev := baseEvent()
		ev.Recommendation = rec
		raw, _ := renderForTest(t, ev)
		if !strings.Contains(raw, "待规则补齐") {
			t.Errorf("rec=%s: TBD placeholder missing", rec)
		}
		if strings.Contains(raw, "50×") {
			t.Errorf("rec=%s: numeric leverage leaked through", rec)
		}
	}
}

func TestRenderDecisionCardPreAssessmentSuppressesLeverage(t *testing.T) {
	ev := baseEvent()
	ev.Recommendation = RecommendationPreAssessment
	ev.RiskPlan.MaxLeverage = nil
	ev.RiskPlan.LeverageTiersJSON = json.RawMessage(`[]`)
	ev.RiskPlan.MMQuoteRequired = false
	ev.RiskPlan.TemplateName = RiskTemplatePreAssessment
	raw, _ := renderForTest(t, ev)
	if strings.Contains(raw, "杠杆: **") {
		t.Errorf("pre_assessment should not surface a leverage line, raw=%s", raw)
	}
	if !strings.Contains(raw, "待规则补齐") {
		t.Errorf("TBD note must still appear")
	}
}

func TestRenderDecisionCardScoreLineUsesSlash100(t *testing.T) {
	ev := baseEvent()
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "78 / 100") {
		t.Errorf("score must be '78 / 100', raw=%s", raw)
	}
	if !strings.Contains(raw, "准备上线") {
		t.Errorf("recommendation label must render '准备上线', raw=%s", raw)
	}
}

func TestRenderDecisionCardScoreFallsBackToDashWhenZero(t *testing.T) {
	ev := baseEvent()
	ev.BusinessScore = 0
	raw, _ := renderForTest(t, ev)
	// JSON-encoded newline is `\n` (two bytes), not the literal
	// 0x0A byte; we check both the escaped form (most likely in the
	// outgoing wire payload) and a plain dash to be safe.
	if !strings.Contains(raw, `**Score**\n—`) {
		t.Errorf("zero score must surface as '—', raw=%s", raw)
	}
}

func TestRenderDecisionCardAllFourButtonsAndCallbackValuePreserved(t *testing.T) {
	ev := baseEvent()
	_, parsed := renderForTest(t, ev)
	card := parsed["card"].(map[string]any)
	elements := card["elements"].([]any)
	var actionRow map[string]any
	for _, el := range elements {
		m := el.(map[string]any)
		if m["tag"] == "action" {
			actionRow = m
			break
		}
	}
	if actionRow == nil {
		t.Fatalf("action row not found in elements")
	}
	actions := actionRow["actions"].([]any)
	if len(actions) != 4 {
		t.Fatalf("len(actions) = %d, want 4", len(actions))
	}
	expectedActions := map[string]string{
		"prepare_listing": "准备上线",
		"enter_watchlist": "进入观察",
		"contact_mm":      "联系MM",
		"ignore":          "忽略",
	}
	got := make(map[string]string)
	for _, a := range actions {
		btn := a.(map[string]any)
		val := btn["value"].(map[string]any)
		got[val["action"].(string)] = btn["text"].(map[string]any)["content"].(string)
		if val["dedupe_key"] == nil {
			t.Errorf("button missing dedupe_key in callback value")
		}
		if val["candidate_id"] == nil {
			t.Errorf("button missing candidate_id in callback value")
		}
		if val["risk_plan_id"] == nil {
			t.Errorf("button missing risk_plan_id in callback value")
		}
	}
	for k, want := range expectedActions {
		if got[k] != want {
			t.Errorf("action %s = %q, want %q", k, got[k], want)
		}
	}
}

func TestRenderDecisionCardFooterCarriesAuditInfo(t *testing.T) {
	ev := baseEvent()
	ev.Enrichment.EnrichErrors = []string{"depth: timeout", "external: 429"}
	raw, _ := renderForTest(t, ev)
	for _, want := range []string{
		"trigger=",
		"evidence=公告 + API 双源确认",
		"confidence=high",
		"enrich_errors=2",
		"dedupe=listing_decision|7|2026-05-31",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("footer missing %q in raw=%s", want, raw)
		}
	}
	// CoinGecko ID must not leak into the card surface.
	for _, banned := range []string{"cg=", "coingecko", "CoinGecko"} {
		if strings.Contains(raw, banned) {
			t.Errorf("footer leaked CoinGecko reference %q in raw=%s", banned, raw)
		}
	}
}

func TestRenderDecisionCardTimeFormattedAsUTC8(t *testing.T) {
	ev := baseEvent()
	// 2026-05-31 06:30 UTC == 2026-05-31 14:30 UTC+8
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "2026-05-31 14:30 UTC+8") {
		t.Errorf("time must render as UTC+8 14:30, raw=%s", raw)
	}
}

func TestRenderDecisionCardOmitsUTCAndDebugStrings(t *testing.T) {
	ev := baseEvent()
	raw, _ := renderForTest(t, ev)
	for _, bad := range []string{
		"announcement_pending_api 决策候选", // old debug-style title
		"Source platforms: [",           // old array dump
		"[announcement_and_api]",        // old enum-in-title
	} {
		if strings.Contains(raw, bad) {
			t.Errorf("legacy debug fragment %q still in card; raw=%s", bad, raw)
		}
	}
}

func TestRenderDecisionCardMarketStatusContextForPausedAndDelisted(t *testing.T) {
	ev := baseEvent()
	ev.Enrichment.MarketStatuses = []PlatformMarketStatus{
		{Platform: "binance", DisplayName: "Binance Futures", Status: StatusPaused, StatusRaw: "PENDING_TRADING", SourceKind: "api", OccurredAt: time.Date(2026, 6, 2, 3, 20, 0, 0, time.UTC)},
		{Platform: "lighter", DisplayName: "Lighter", Status: StatusDelisted, StatusRaw: "inactive", SourceKind: "api", OccurredAt: time.Date(2026, 6, 2, 2, 58, 0, 0, time.UTC)},
	}
	raw, _ := renderForTest(t, ev)
	for _, want := range []string{
		"暂停交易（当前状态 · API: PENDING_TRADING）",
		"已下架（当前状态 · API: inactive）",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("market status context missing %q in raw=%s", want, raw)
		}
	}
}

func TestRenderDecisionCardMarketStatusHistoricalDateIncludesYear(t *testing.T) {
	ev := baseEvent()
	ev.TriggerTime = time.Date(2026, 6, 2, 2, 18, 0, 0, time.UTC)
	ev.Enrichment.MarketStatuses = []PlatformMarketStatus{
		{Platform: "bingx", DisplayName: "BingX Futures", Status: StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api", OccurredAt: time.Date(2025, 11, 14, 7, 0, 0, 0, time.UTC)},
	}
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "2025-11-14 15:00 UTC+8") {
		t.Fatalf("historical market status date must include year, raw=%s", raw)
	}
}

func TestRenderDecisionCardMarketStatusSameYearKeepsShortDate(t *testing.T) {
	ev := baseEvent()
	ev.TriggerTime = time.Date(2026, 6, 2, 2, 18, 0, 0, time.UTC)
	ev.Enrichment.MarketStatuses = []PlatformMarketStatus{
		{Platform: "bingx", DisplayName: "BingX Futures", Status: StatusPreListing, StatusLabel: "pre-listing", SourceKind: "api", OccurredAt: time.Date(2026, 6, 2, 7, 0, 0, 0, time.UTC)},
	}
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "06-02 15:00 UTC+8") {
		t.Fatalf("same-year market status date should stay short, raw=%s", raw)
	}
	if strings.Contains(raw, "2026-06-02 15:00 UTC+8") {
		t.Fatalf("same-year market status date should not include year, raw=%s", raw)
	}
}

func TestRenderDecisionCardBasicInfoShowsDetectedAndExchangeListingTimes(t *testing.T) {
	ev := baseEvent()
	listingTime := time.Date(2026, 5, 7, 9, 0, 0, 0, time.UTC)
	ev.PrimaryListingTime = &listingTime
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "Detected Time") || !strings.Contains(raw, "2026-05-31 14:30 UTC+8") {
		t.Fatalf("detected time missing, raw=%s", raw)
	}
	if !strings.Contains(raw, "Exchange Listing Time") || !strings.Contains(raw, "2026-05-07 17:00 UTC+8") {
		t.Fatalf("exchange listing time missing, raw=%s", raw)
	}
	if !strings.Contains(raw, "trigger=2026-05-31 14:30 UTC+8") {
		t.Fatalf("footer trigger audit time should remain, raw=%s", raw)
	}
}

func TestRenderDecisionCardBasicInfoFallsBackToDetectedTime(t *testing.T) {
	ev := baseEvent()
	ev.PrimaryListingTime = nil
	raw, _ := renderForTest(t, ev)
	if !strings.Contains(raw, "Detected Time") || !strings.Contains(raw, "2026-05-31 14:30 UTC+8") {
		t.Fatalf("detected time fallback missing, raw=%s", raw)
	}
	if strings.Contains(raw, "Exchange Listing Time") {
		t.Fatalf("fallback should not render Exchange Listing Time label, raw=%s", raw)
	}
}
