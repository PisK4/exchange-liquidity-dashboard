package liquidity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mkCard(t *testing.T, kind AlertKind, phase string) CardPayload {
	t.Helper()
	first := mustParse(t, "2026-05-28T09:00:00Z")
	now := mustParse(t, "2026-05-29T03:54:00Z")
	return CardPayload{
		Kind:             kind,
		Phase:            phase,
		Canonical:        "BTC",
		DisplaySymbol:    "BTC-USDT (perp)",
		Tier:             "0.1%",
		SeveritySeq:      1,
		ReissueIdx:       2,
		FirstTriggeredAt: first,
		EvaluatedAt:      now,
		EdgexDepth:       2_400_000,
		MedianDepth:      5_800_000,
		Ratio:            0.4138,
		LagThreshold:     0.5,
		Comparators:      8,
		TotalPlatforms:   9,
		EdgexRank:        6,
		Platforms: []AlertPlatformRow{
			{Platform: "binance", DepthUSD: 8_500_000, Rank: 1},
			{Platform: "okx", DepthUSD: 7_100_000, Rank: 2},
			{Platform: "bybit", DepthUSD: 6_200_000, Rank: 3},
			{Platform: "bitget", DepthUSD: 5_800_000, Rank: 4, IsMedian: true},
			{Platform: "gate", DepthUSD: 3_800_000, Rank: 5},
			{Platform: "edgeX", DepthUSD: 2_400_000, Rank: 6, IsEdgex: true},
			{Platform: "bingx", DepthUSD: 1_900_000, Rank: 7},
			{Platform: "mexc", DepthUSD: 1_400_000, Rank: 8},
			{Platform: "hyperliquid", DepthUSD: 800_000, Rank: 9},
		},
		DashboardURL: "https://dashboard.example/liquidity",
		DedupeKey:    "liquidity_lag|BTC|seq1|reissue2",
	}
}

func decode(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, string(payload))
	}
	return out
}

func TestRenderLiquidityLagCardHeader(t *testing.T) {
	card := mkCard(t, KindLiquidityLag, PhaseReissue)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	m := decode(t, payload)
	if m["msg_type"] != "interactive" {
		t.Errorf("msg_type = %v", m["msg_type"])
	}
	header := m["card"].(map[string]any)["header"].(map[string]any)
	if header["template"] != "orange" {
		t.Errorf("template = %v, want orange", header["template"])
	}
	title := header["title"].(map[string]any)
	if c, _ := title["content"].(string); !strings.Contains(c, "流动性落后") {
		t.Errorf("title content = %q", c)
	}
	if _, hasText := title["text"]; hasText {
		t.Errorf("plain_text must use 'content', not 'text'")
	}
}

func TestRenderWorstDepthCardHeader(t *testing.T) {
	card := mkCard(t, KindWorstDepth, PhaseFirst)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	m := decode(t, payload)
	header := m["card"].(map[string]any)["header"].(map[string]any)
	if header["template"] != "red" {
		t.Errorf("template = %v, want red", header["template"])
	}
}

func TestRenderClearCardGreen(t *testing.T) {
	card := mkCard(t, KindLiquidityLag, PhaseClear)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	m := decode(t, payload)
	header := m["card"].(map[string]any)["header"].(map[string]any)
	if header["template"] != "green" {
		t.Errorf("clear template = %v, want green", header["template"])
	}
	title := header["title"].(map[string]any)
	if c, _ := title["content"].(string); !strings.Contains(c, "恢复") {
		t.Errorf("clear title = %q", c)
	}
}

func TestRenderFooterCarriesDedupeKey(t *testing.T) {
	card := mkCard(t, KindLiquidityLag, PhaseReissue)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(payload), "liquidity_lag|BTC|seq1|reissue2") {
		t.Errorf("footer must include dedupe key, payload=%s", string(payload))
	}
}

func TestRenderPlatformListIncludesAllRowsAndMarkersOnEdgex(t *testing.T) {
	card := mkCard(t, KindLiquidityLag, PhaseFirst)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(payload)
	for _, plat := range []string{"binance", "okx", "bybit", "bitget", "gate", "edgeX", "bingx", "mexc", "hyperliquid"} {
		if !strings.Contains(body, "**"+plat+"**") {
			t.Errorf("missing platform row %q in body", plat)
		}
	}
	if !strings.Contains(body, "← edgeX") {
		t.Errorf("edgeX row must carry ← edgeX marker")
	}
	if !strings.Contains(body, "← 中位数") {
		t.Errorf("median row must carry ← 中位数 marker")
	}
}

func TestRenderEscapeHTMLDisabled(t *testing.T) {
	card := mkCard(t, KindLiquidityLag, PhaseFirst)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The card uses <font color='...'> markup which would otherwise
	// be HTML-escaped by encoding/json's default behaviour. The
	// raw payload must contain literal '<' so Lark's lark_md tag
	// can parse the bullet colour.
	if !strings.Contains(string(payload), "<font color=") {
		t.Errorf("expected unescaped <font color=...> markup in payload, got: %s", string(payload))
	}
}

func TestBuildDashboardURL(t *testing.T) {
	cases := []struct {
		base, canonical, tier, want string
	}{
		{"", "BTC", "0.001", ""},
		{"https://x/liquidity", "BTC", "0.001", "https://x/liquidity?symbol=BTC&tier=0.001"},
		{"https://x/liquidity?foo=1", "ETH", "0.0005", "https://x/liquidity?foo=1&symbol=ETH&tier=0.0005"},
		{"https://x/liquidity", "", "", "https://x/liquidity"},
	}
	for _, tc := range cases {
		got := BuildDashboardURL(tc.base, tc.canonical, tc.tier)
		if got != tc.want {
			t.Errorf("BuildDashboardURL(%q,%q,%q) = %q, want %q", tc.base, tc.canonical, tc.tier, got, tc.want)
		}
	}
}

func TestRenderFirstPhaseBadge(t *testing.T) {
	card := mkCard(t, KindLiquidityLag, PhaseFirst)
	payload, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(payload), "首次触发") {
		t.Errorf("first phase must carry 首次触发 badge")
	}
}

func TestRenderHumanUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.50K"},
		{1_500_000, "1.50M"},
		{1_500_000_000, "1.50B"},
	}
	for _, tc := range cases {
		got := humanUSD(tc.in)
		if got != tc.want {
			t.Errorf("humanUSD(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// guard against accidental regression: render should not call into
// time.Now and should produce identical output for identical inputs.
func TestRenderDeterministicAcrossCalls(t *testing.T) {
	card := mkCard(t, KindWorstDepth, PhaseReissue)
	a, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := RenderLiquidityPostMessage(card)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("non-deterministic render:\nA=%s\nB=%s", string(a), string(b))
	}
	_ = time.Now() // silence linter if any
}
