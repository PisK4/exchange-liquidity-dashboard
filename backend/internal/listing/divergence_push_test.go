package listing

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
)

func divergenceCfg() config.Top30DivergenceConfig {
	return config.Top30DivergenceConfig{
		CEXPlatforms:         []string{"binance", "okx", "bybit", "bitget", "mexc", "gate", "bingx"},
		DEXPlatforms:         []string{"hyperliquid", "lighter", "edgeX"},
		SignificantRankDelta: 10,
	}
}

func divPushCfg() config.Top30DivergencePushConfig {
	return config.Top30DivergencePushConfig{
		Enabled:     true,
		TopNPerCard: 10,
		StaleAfter:  15 * time.Minute,
		SendSpacing: 30 * time.Second,
	}
}

func divBoolPtr(b bool) *bool { return &b }

// rowForDiv is a compact helper returning Top30RowForPush values
// suitable for divergence input.
func rowForDiv(platform string, rank int, symbol string, vol float64, listed *bool, snapshot time.Time) Top30RowForPush {
	return Top30RowForPush{
		Platform:     platform,
		Symbol:       symbol,
		Rank:         rank,
		Volume24HUSD: vol,
		EdgexListed:  listed,
		SnapshotTS:   snapshot,
	}
}

func TestBuildDivergencePushEvents_ThreeStateFiltersOutNilAndListed(t *testing.T) {
	day := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	listed := true
	unlisted := false
	// FOO is on both CEX and DEX, unlisted → counts for both_hot_gap.
	// BAR is on CEX only, listed=true → must NOT appear.
	// BAZ is on CEX only, listed=nil  → must NOT appear (red line: nil rejected).
	// QUX is on CEX only, listed=false → cex_only candidate (eligible).
	rows := []Top30RowForPush{
		rowForDiv("binance", 1, "FOO", 1000, &unlisted, day),
		rowForDiv("hyperliquid", 1, "FOO", 800, &unlisted, day),
		rowForDiv("binance", 2, "BAR", 500, &listed, day),
		rowForDiv("binance", 3, "BAZ", 400, nil, day),
		rowForDiv("binance", 4, "QUX", 300, &unlisted, day),
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 10, day)
	for _, ev := range events {
		for _, row := range ev.Rows {
			if row.Symbol == "BAR" {
				t.Fatalf("BAR (listed=true) must not appear in any card; got in %s", ev.Category)
			}
			if row.Symbol == "BAZ" {
				t.Fatalf("BAZ (listed=nil) must not appear in any card; got in %s", ev.Category)
			}
		}
	}
}

func TestBuildDivergencePushEvents_DedupeKeyFormat(t *testing.T) {
	day := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	unlisted := false
	rows := []Top30RowForPush{
		rowForDiv("binance", 1, "FOO", 1000, &unlisted, day),
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 10, day)
	if len(events) != 1 {
		t.Fatalf("expected exactly one cex_only card, got %d (events=%+v)", len(events), events)
	}
	ev := events[0]
	if ev.Category != DivergenceCategoryCEXOnly {
		t.Fatalf("category = %q, want cex_only", ev.Category)
	}
	want := "top30_divergence|cex_only|2026-05-28"
	if ev.DedupeKey != want {
		t.Fatalf("dedupe_key = %q, want %q", ev.DedupeKey, want)
	}
	if ev.SnapshotDate != "2026-05-28" {
		t.Fatalf("snapshot_date = %q, want 2026-05-28", ev.SnapshotDate)
	}
}

func TestBuildDivergencePushEvents_EmptyCategoryProducesNoCard(t *testing.T) {
	day := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	unlisted := false
	// Only one platform with FOO; the symbol is cex_only. dex_only,
	// heavy_gap and both_hot_gap will all be empty and must NOT be
	// emitted as zero-row cards.
	rows := []Top30RowForPush{
		rowForDiv("binance", 1, "FOO", 1000, &unlisted, day),
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 10, day)
	categories := map[string]bool{}
	for _, ev := range events {
		categories[ev.Category] = true
	}
	if categories[DivergenceCategoryDEXOnly] {
		t.Fatalf("dex_only must be omitted when empty")
	}
	if categories[DivergenceCategoryHeavyGap] {
		t.Fatalf("heavy_gap must be omitted when empty")
	}
	if categories[DivergenceCategoryBothHotGap] {
		t.Fatalf("both_hot_gap must be omitted when empty")
	}
}

func TestBuildDivergencePushEvents_FourFiveOverlapAllowed(t *testing.T) {
	// Build a heavy-gap symbol: ranks 1 (CEX) vs 12 (DEX) → |Δ|=11 ≥ 10
	// → cex_heavy. Same symbol must show up in both #4 (heavy_gap) and
	// #5 (both_hot_gap) — they're separate cards by design.
	day := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	unlisted := false
	rows := []Top30RowForPush{
		rowForDiv("binance", 1, "XYZ", 1000, &unlisted, day),
	}
	for i := 2; i <= 9; i++ {
		rows = append(rows, rowForDiv("binance", i, "CXFILLER"+itoaTwo(i), 900-float64(i), &unlisted, day))
	}
	rows = append(rows, rowForDiv("binance", 10, "CXFINAL", 50, &unlisted, day))
	for i := 1; i <= 11; i++ {
		rows = append(rows, rowForDiv("hyperliquid", i, "DXFILLER"+itoaTwo(i), 900-float64(i), &unlisted, day))
	}
	rows = append(rows, rowForDiv("hyperliquid", 12, "XYZ", 100, &unlisted, day))

	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 30, day)
	inHeavy := false
	inBothHot := false
	for _, ev := range events {
		for _, row := range ev.Rows {
			if row.Symbol != "XYZ" {
				continue
			}
			if ev.Category == DivergenceCategoryHeavyGap {
				inHeavy = true
			}
			if ev.Category == DivergenceCategoryBothHotGap {
				inBothHot = true
			}
		}
	}
	if !inHeavy {
		t.Fatalf("XYZ should appear in heavy_gap card")
	}
	if !inBothHot {
		t.Fatalf("XYZ should appear in both_hot_gap card (overlap with heavy is expected)")
	}
}

func TestBuildDivergencePushEvents_TopNTruncatesPerCard(t *testing.T) {
	day := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	unlisted := false
	var rows []Top30RowForPush
	for i := 1; i <= 20; i++ {
		rows = append(rows, rowForDiv("binance", i, "SYM"+itoaTwo(i), 1000-float64(i), &unlisted, day))
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 3, day)
	for _, ev := range events {
		if len(ev.Rows) > 3 {
			t.Fatalf("category %s exceeded TopN=3: got %d rows", ev.Category, len(ev.Rows))
		}
	}
}

func TestRenderDivergencePostMessage_HeaderColourPerCategory(t *testing.T) {
	cases := map[string]string{
		DivergenceCategoryCEXOnly:    `"template":"blue"`,
		DivergenceCategoryDEXOnly:    `"template":"purple"`,
		DivergenceCategoryHeavyGap:   `"template":"orange"`,
		DivergenceCategoryBothHotGap: `"template":"red"`,
	}
	for cat, want := range cases {
		ev := DivergencePushEvent{
			Category:      cat,
			CategoryLabel: cat,
			Rows:          []DivergencePushRow{{Symbol: "XYZ"}},
			SnapshotDate:  "2026-05-28",
			DedupeKey:     "top30_divergence|" + cat + "|2026-05-28",
		}
		body, err := RenderDivergencePostMessage(ev)
		if err != nil {
			t.Fatalf("%s render: %v", cat, err)
		}
		if !strings.Contains(string(body), want) {
			t.Fatalf("category %q missing %s in body: %s", cat, want, body)
		}
	}
}

func TestRenderDivergencePostMessage_InteractiveCardShape(t *testing.T) {
	cexRank, dexRank := 2, 14
	cexVol, dexVol := 5e8, 3e7
	delta := 12
	ev := DivergencePushEvent{
		Category:      DivergenceCategoryHeavyGap,
		CategoryLabel: "CEX vs DEX 显著分歧 · edgeX 未上线",
		Rows: []DivergencePushRow{
			{
				Symbol: "XYZ", CEXRank: &cexRank, DEXRank: &dexRank,
				CEXVolUSD: &cexVol, DEXVolUSD: &dexVol, RankDelta: &delta,
				CEXPlatforms: 5, DEXPlatforms: 1,
			},
		},
		KPI:          divergenceTestKPI(),
		SnapshotDate: "2026-05-28",
		DedupeKey:    "top30_divergence|heavy_gap|2026-05-28",
		DashboardURL: "https://dashboard.example.test/top30",
		TriggerTime:  time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC),
	}
	body, err := RenderDivergencePostMessage(ev)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not json: %v", err)
	}
	if decoded["msg_type"] != "interactive" {
		t.Fatalf("msg_type = %v, want interactive", decoded["msg_type"])
	}
	bs := string(body)
	wantSubstrings := []string{
		"CEX vs DEX 显著分歧",
		"XYZ",
		"top30_divergence|heavy_gap|2026-05-28",
		"触发时间 2026-05-28 16:04 UTC",
		"📊 查看 Top30 详情",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(bs, want) {
			t.Fatalf("missing %q in body:\n%s", want, bs)
		}
	}
}

func TestProduceDivergencePush_WritesSignalsAndStaggersOutbox(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	snapshot := now.Add(-5 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(snapshot))
	// Two CEX rows for two different cex_only symbols, one DEX-only.
	rows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).
		AddRow("binance", "AAA-USDT (perp)", 1, 1000.0, 1, false, "", snapshot).
		AddRow("binance", "BBB-USDT (perp)", 2, 800.0, 1, false, "", snapshot).
		AddRow("hyperliquid", "CCC-USDC (perp)", 1, 500.0, 1, false, "", snapshot)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(snapshot).
		WillReturnRows(rows)

	// Two categories non-empty: cex_only (2 rows) and dex_only (1 row).
	// SendSpacing = 30s → first card NextAttemptAt=now, second card=now+30s.
	spacing := 30 * time.Second

	// Each card: 1× insert signal + 1× insert outbox.
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), DeliveryChannelLarkTop30, OutboxStatusPending,
			0, 5, now, sqlmock.AnyArg(), nil,
			sqlmock.AnyArg(), now, now,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), DeliveryChannelLarkTop30, OutboxStatusPending,
			0, 5, now.Add(spacing), sqlmock.AnyArg(), nil,
			sqlmock.AnyArg(), now, now,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	deps := DivergenceDeps{
		Now:           func() time.Time { return now },
		DashboardBase: "https://dashboard.example.test/top30",
		WebhookURL:    "https://example.test/hook",
		MaxAttempts:   5,
		DivergenceCfg: divergenceCfg(),
		PushCfg:       divPushCfg(),
	}
	res, err := ProduceDivergencePush(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDivergencePush err = %v", err)
	}
	if res.Produced != 2 || res.Signals != 2 || res.OutboxRows != 2 {
		t.Fatalf("result = %+v, want Produced=2 Signals=2 OutboxRows=2", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceDivergencePush_DisabledEarlyExit(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	deps := DivergenceDeps{
		Now:           func() time.Time { return now },
		WebhookURL:    "https://example.test/hook",
		MaxAttempts:   5,
		DivergenceCfg: divergenceCfg(),
		PushCfg: config.Top30DivergencePushConfig{
			Enabled:     false,
			TopNPerCard: 10,
			StaleAfter:  15 * time.Minute,
			SendSpacing: 30 * time.Second,
		},
	}
	res, err := ProduceDivergencePush(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDivergencePush err = %v", err)
	}
	if res.Produced != 0 || res.Signals != 0 || res.OutboxRows != 0 {
		t.Fatalf("disabled producer must short-circuit; got %+v", res)
	}
	// No SQL must have been issued.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceDivergencePush_WebhookEmptyMarksDisabled(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	snapshot := now.Add(-5 * time.Minute)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(snapshot))
	rows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).AddRow("binance", "AAA-USDT (perp)", 1, 1000.0, 1, false, "", snapshot)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(snapshot).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), DeliveryChannelLarkTop30, OutboxStatusDisabled,
			0, 5, sqlmock.AnyArg(), sqlmock.AnyArg(), nil,
			sqlmock.AnyArg(), now, now,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	deps := DivergenceDeps{
		Now:           func() time.Time { return now },
		WebhookURL:    "",
		MaxAttempts:   5,
		DivergenceCfg: divergenceCfg(),
		PushCfg:       divPushCfg(),
	}
	res, err := ProduceDivergencePush(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDivergencePush err = %v", err)
	}
	if res.OutboxRows != 1 {
		t.Fatalf("OutboxRows = %d, want 1", res.OutboxRows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceDivergencePush_FailClosedOnStale(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	stale := now.Add(-time.Hour)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(stale))
	// loadTop30LatestRows is shared with the Top30 hot-gap path and
	// issues the row SELECT unconditionally; the stale check fires
	// only after both queries return. Mock the second SELECT so the
	// shared loader semantics don't have to change just for this fail-
	// closed branch.
	staleRows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).AddRow("binance", "AAA-USDT (perp)", 1, 1000.0, 1, false, "", stale)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(stale).
		WillReturnRows(staleRows)

	deps := DivergenceDeps{
		Now:           func() time.Time { return now },
		WebhookURL:    "https://example.test/hook",
		MaxAttempts:   5,
		DivergenceCfg: divergenceCfg(),
		PushCfg:       divPushCfg(),
	}
	res, err := ProduceDivergencePush(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceDivergencePush err = %v", err)
	}
	if res.FailClosed != "snapshot_stale" {
		t.Fatalf("FailClosed = %q, want snapshot_stale", res.FailClosed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// TestBuildDivergencePushEvents_PopulatesPlatformDetails locks in the
// per-platform sub-row data plumbing. ALLO appears on 5 CEX venues
// at distinct ranks; the resulting cex_only row must surface all 5
// in NativeRank-ASC order so the renderer can build the "binance #15
// · okx #18 · bybit #21 · ..." sub-line.
func TestBuildDivergencePushEvents_PopulatesPlatformDetails(t *testing.T) {
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	unlisted := false
	rows := []Top30RowForPush{
		rowForDiv("binance", 15, "ALLO", 200e6, &unlisted, day),
		rowForDiv("okx", 18, "ALLO", 180e6, &unlisted, day),
		rowForDiv("bybit", 21, "ALLO", 150e6, &unlisted, day),
		rowForDiv("bitget", 24, "ALLO", 120e6, &unlisted, day),
		rowForDiv("bingx", 27, "ALLO", 100e6, &unlisted, day),
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 10, day)
	if len(events) != 1 || events[0].Category != DivergenceCategoryCEXOnly {
		t.Fatalf("expected single cex_only event, got %+v", events)
	}
	ev := events[0]
	if len(ev.Rows) != 1 || ev.Rows[0].Symbol != "ALLO" {
		t.Fatalf("expected ALLO row only, got %+v", ev.Rows)
	}
	got := ev.Rows[0].CEXPlatformDetails
	want := []DivergencePushPlatform{
		{Platform: "binance", NativeRank: 15, Volume24HUSD: 200e6},
		{Platform: "okx", NativeRank: 18, Volume24HUSD: 180e6},
		{Platform: "bybit", NativeRank: 21, Volume24HUSD: 150e6},
		{Platform: "bitget", NativeRank: 24, Volume24HUSD: 120e6},
		{Platform: "bingx", NativeRank: 27, Volume24HUSD: 100e6},
	}
	if len(got) != len(want) {
		t.Fatalf("CEXPlatformDetails len = %d, want %d (got %+v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("CEXPlatformDetails[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(ev.Rows[0].DEXPlatformDetails) != 0 {
		t.Fatalf("cex_only row must not carry DEX details, got %+v", ev.Rows[0].DEXPlatformDetails)
	}
}

// TestBuildDivergencePushEvents_TieBreakOnPlatformCount asserts the
// new secondary sort: when two cex_only canonicals share the same
// CEXRank, the one with more contributing platforms wins. This makes
// "硬信号" (broad consensus) bubble to the top within a tied rank.
func TestBuildDivergencePushEvents_TieBreakOnPlatformCount(t *testing.T) {
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	unlisted := false
	// Two canonicals both yielding CEXRank=1 in the aggregate by
	// virtue of being the only members of the CEX universe each. We
	// engineer the tie by giving them equal aggregated volume, then
	// look at the sort tie-break (broad > narrow).
	rows := []Top30RowForPush{
		rowForDiv("binance", 1, "BROAD", 100, &unlisted, day),
		rowForDiv("okx", 1, "BROAD", 100, &unlisted, day),
		rowForDiv("bybit", 1, "BROAD", 100, &unlisted, day),
		rowForDiv("bitget", 1, "NARROW", 300, &unlisted, day),
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 10, day)
	if len(events) != 1 {
		t.Fatalf("expected single cex_only event, got %d", len(events))
	}
	// NARROW has higher single-platform volume (300) so it should
	// take aggregate rank 1; BROAD aggregates to 300 too (100×3) → tie.
	// The new tie-break on CEXPlatformCount DESC makes BROAD (3
	// platforms) outrank NARROW (1 platform) when ranks tie.
	var broadIdx, narrowIdx = -1, -1
	for i, r := range events[0].Rows {
		switch r.Symbol {
		case "BROAD":
			broadIdx = i
		case "NARROW":
			narrowIdx = i
		}
	}
	if broadIdx < 0 || narrowIdx < 0 {
		t.Fatalf("rows missing BROAD or NARROW: %+v", events[0].Rows)
	}
	// Both rows have aggregate rank 1 (tie on volume) — assert
	// PlatformCount tie-break ordering.
	if derefInt(events[0].Rows[broadIdx].CEXRank) == derefInt(events[0].Rows[narrowIdx].CEXRank) {
		if broadIdx > narrowIdx {
			t.Fatalf("on a tied CEXRank, BROAD (3 platforms) must outrank NARROW (1 platform); got positions BROAD=%d NARROW=%d", broadIdx, narrowIdx)
		}
	}
}

// TestBuildDivergencePushEvents_CardKPIForCEXOnly checks the per-card
// KPI strip metadata: distribution buckets, total volume, and the
// "strongest" pick (PlatformCount DESC, Rank ASC).
func TestBuildDivergencePushEvents_CardKPIForCEXOnly(t *testing.T) {
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	unlisted := false
	// Engineer three CEX-only canonicals with distinct breadth:
	//   ALLO: 5 platforms → BroadCount
	//   GUA : 3 platforms → MidCount
	//   ESP : 1 platform  → NarrowCount
	rows := []Top30RowForPush{
		rowForDiv("binance", 15, "ALLO", 200e6, &unlisted, day),
		rowForDiv("okx", 18, "ALLO", 180e6, &unlisted, day),
		rowForDiv("bybit", 21, "ALLO", 150e6, &unlisted, day),
		rowForDiv("bitget", 24, "ALLO", 120e6, &unlisted, day),
		rowForDiv("bingx", 27, "ALLO", 100e6, &unlisted, day),

		rowForDiv("binance", 22, "GUA", 80e6, &unlisted, day),
		rowForDiv("okx", 25, "GUA", 70e6, &unlisted, day),
		rowForDiv("mexc", 29, "GUA", 50e6, &unlisted, day),

		rowForDiv("bingx", 28, "ESP", 60e6, &unlisted, day),
	}
	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 10, day)
	if len(events) != 1 {
		t.Fatalf("expected single cex_only event, got %+v", events)
	}
	card := events[0].CardKPI
	if card == nil {
		t.Fatalf("CardKPI must be populated for cex_only category")
	}
	if card.TotalEligible != 3 {
		t.Fatalf("TotalEligible = %d, want 3", card.TotalEligible)
	}
	if card.BroadCount != 1 || card.MidCount != 1 || card.NarrowCount != 1 {
		t.Fatalf("distribution = (%d,%d,%d), want (1,1,1)", card.BroadCount, card.MidCount, card.NarrowCount)
	}
	if card.OppositeCampLabel != "DEX" {
		t.Fatalf("OppositeCampLabel = %q, want DEX", card.OppositeCampLabel)
	}
	// "最硬" = highest PlatformCount → ALLO (5 平台)
	if card.StrongestSymbol != "ALLO" {
		t.Fatalf("StrongestSymbol = %q, want ALLO", card.StrongestSymbol)
	}
	if card.StrongestPlatforms != 5 {
		t.Fatalf("StrongestPlatforms = %d, want 5", card.StrongestPlatforms)
	}
	totalVol := 200e6 + 180e6 + 150e6 + 120e6 + 100e6 + 80e6 + 70e6 + 50e6 + 60e6
	if card.SideVolUSD != totalVol {
		t.Fatalf("SideVolUSD = %v, want %v", card.SideVolUSD, totalVol)
	}
}

// TestBuildDivergencePushEvents_CardKPINilForHeavyAndBothHot makes
// sure heavy_gap and both_hot_gap still ship the legacy global KPI
// strip (i.e. CardKPI stays nil).
func TestBuildDivergencePushEvents_CardKPINilForHeavyAndBothHot(t *testing.T) {
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	unlisted := false
	rows := []Top30RowForPush{
		rowForDiv("binance", 1, "XYZ", 1000, &unlisted, day),
	}
	for i := 2; i <= 9; i++ {
		rows = append(rows, rowForDiv("binance", i, "CXFILL"+itoaTwo(i), 900-float64(i), &unlisted, day))
	}
	rows = append(rows, rowForDiv("binance", 10, "CXEND", 50, &unlisted, day))
	for i := 1; i <= 11; i++ {
		rows = append(rows, rowForDiv("hyperliquid", i, "DXFILL"+itoaTwo(i), 900-float64(i), &unlisted, day))
	}
	rows = append(rows, rowForDiv("hyperliquid", 12, "XYZ", 100, &unlisted, day))

	events := BuildDivergencePushEvents(rows, divergenceCfg(), nil, 30, day)
	for _, ev := range events {
		switch ev.Category {
		case DivergenceCategoryHeavyGap, DivergenceCategoryBothHotGap:
			if ev.CardKPI != nil {
				t.Fatalf("category %s must keep legacy global KPI (CardKPI=nil); got %+v", ev.Category, ev.CardKPI)
			}
		}
	}
}

// TestRenderDivergencePostMessage_CEXOnlyTwoLineFormat asserts the
// new sub-row "binance #15 · okx #18 · ..." appears verbatim, and
// the KPI strip carries the per-card distribution + opposite-camp
// absence tag.
func TestRenderDivergencePostMessage_CEXOnlyTwoLineFormat(t *testing.T) {
	cex := 15
	cexVol := 857.75e6
	ev := DivergencePushEvent{
		Category:      DivergenceCategoryCEXOnly,
		CategoryLabel: "CEX 独有热门 · edgeX 未上线",
		Rows: []DivergencePushRow{
			{
				Symbol:       "ALLO",
				CEXRank:      &cex,
				CEXVolUSD:    &cexVol,
				CEXPlatforms: 5,
				CEXPlatformDetails: []DivergencePushPlatform{
					{Platform: "binance", NativeRank: 15},
					{Platform: "okx", NativeRank: 18},
					{Platform: "bybit", NativeRank: 21},
					{Platform: "bitget", NativeRank: 24},
					{Platform: "bingx", NativeRank: 27},
				},
			},
		},
		CardKPI: &DivergenceCardKPI{
			TotalEligible:      13,
			BroadCount:         2,
			MidCount:           5,
			NarrowCount:        6,
			SideVolUSD:         3.5e9,
			StrongestSymbol:    "ALLO",
			StrongestPlatforms: 5,
			StrongestBestRank:  15,
			OppositeCampLabel:  "DEX",
		},
		SnapshotDate: "2026-05-29",
		DedupeKey:    "top30_divergence|cex_only|2026-05-29",
		DashboardURL: "https://dashboard.example.test/?tab=top30",
		TriggerTime:  time.Date(2026, 5, 29, 3, 54, 0, 0, time.UTC),
	}
	body, err := RenderDivergencePostMessage(ev)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		// KPI strip (new per-card format)
		"本卡 13 项",
		"5+ 平台 **2**",
		"3-4 平台 **5**",
		"1-2 平台 **6**",
		"DEX 阵营 **0 家** ❌",
		"CEX 合计 24h **$3.50B**",
		"最硬 **ALLO**（5 家 · 最佳 #15）",
		// Per-row main + sub
		"ALLO",
		"CEX 合计 24h **$857.75M**",
		"binance #15 · okx #18 · bybit #21 · bitget #24 · bingx #27",
	}
	bs := string(body)
	for _, w := range want {
		if !strings.Contains(bs, w) {
			t.Fatalf("missing %q in body:\n%s", w, bs)
		}
	}
	// Ensure the legacy four-field global KPI strip is NOT present
	// for this card (sanity: the new strip replaced it).
	if strings.Contains(bs, "**显著分歧**") {
		t.Fatalf("legacy global KPI strip leaked into cex_only card:\n%s", bs)
	}
}

// TestRenderDivergencePostMessage_DEXOnlyOppositeCampLabel mirrors the
// CEX-only test on the DEX side so the symmetric "CEX 阵营 0 家 ❌"
// path is exercised.
func TestRenderDivergencePostMessage_DEXOnlyOppositeCampLabel(t *testing.T) {
	dex := 6
	dexVol := 267.75e6
	ev := DivergencePushEvent{
		Category:      DivergenceCategoryDEXOnly,
		CategoryLabel: "DEX 独有热门 · edgeX 未上线",
		Rows: []DivergencePushRow{
			{
				Symbol:       "XYZ100",
				DEXRank:      &dex,
				DEXVolUSD:    &dexVol,
				DEXPlatforms: 1,
				DEXPlatformDetails: []DivergencePushPlatform{
					{Platform: "hyperliquid", NativeRank: 6},
				},
			},
		},
		CardKPI: &DivergenceCardKPI{
			TotalEligible:      7,
			BroadCount:         0,
			MidCount:           0,
			NarrowCount:        7,
			SideVolUSD:         432e6,
			StrongestSymbol:    "XYZ100",
			StrongestPlatforms: 1,
			StrongestBestRank:  6,
			OppositeCampLabel:  "CEX",
		},
		SnapshotDate: "2026-05-29",
		DedupeKey:    "top30_divergence|dex_only|2026-05-29",
		TriggerTime:  time.Date(2026, 5, 29, 3, 54, 0, 0, time.UTC),
	}
	body, err := RenderDivergencePostMessage(ev)
	if err != nil {
		t.Fatal(err)
	}
	bs := string(body)
	for _, w := range []string{
		"CEX 阵营 **0 家** ❌",
		"DEX 合计 24h",
		"hyperliquid #6",
	} {
		if !strings.Contains(bs, w) {
			t.Fatalf("missing %q in body:\n%s", w, bs)
		}
	}
}

func divergenceTestKPI() domain.Top30DivergenceKPI {
	return domain.Top30DivergenceKPI{
		CEXOnlyCount:  3,
		DEXOnlyCount:  1,
		HeavyCount:    2,
		AlignedCount:  4,
		EdgexGapCount: 6,
	}
}

func itoaTwo(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
