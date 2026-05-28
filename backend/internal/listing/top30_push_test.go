package listing

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBuildTop30PushEventsAggregatesBySymbol(t *testing.T) {
	day := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	listed := false
	rows := []Top30RowForPush{
		{Platform: "binance", Symbol: "ABC-USDT (perp)", Rank: 3, Volume24HUSD: 1000, CoverageCount: 7, EdgexListed: &listed, SuggestedAction: "优先上架", SnapshotTS: day},
		{Platform: "okx", Symbol: "ABC-USDT (perp)", Rank: 8, Volume24HUSD: 800, CoverageCount: 7, EdgexListed: &listed, SuggestedAction: "优先上架", SnapshotTS: day},
		{Platform: "binance", Symbol: "XYZ-USDT (perp)", Rank: 12, Volume24HUSD: 500, CoverageCount: 4, EdgexListed: &listed, SuggestedAction: "评估上架", SnapshotTS: day},
	}
	events := BuildTop30PushEvents(rows, day)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	var abc Top30PushEvent
	for _, e := range events {
		if e.Symbol == "ABC-USDT (perp)" {
			abc = e
		}
	}
	if abc.DedupeKey != "top30_hot_gap|ABC-USDT (perp)|优先上架|2026-05-27" {
		t.Fatalf("dedupe = %q", abc.DedupeKey)
	}
	if len(abc.Platforms) != 2 || abc.MaxCoverage != 7 {
		t.Fatalf("ABC payload = %+v", abc)
	}
}

func TestBuildTop30PushEventsExcludesListedAndUnknown(t *testing.T) {
	day := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	listed := true
	unlisted := false
	rows := []Top30RowForPush{
		{Platform: "binance", Symbol: "BTC-USDT (perp)", EdgexListed: &listed, SuggestedAction: "优先上架", SnapshotTS: day},
		{Platform: "binance", Symbol: "UNK-USDT (perp)", EdgexListed: nil, SuggestedAction: "优先上架", SnapshotTS: day},
		{Platform: "okx", Symbol: "BAD-USDT (perp)", EdgexListed: &unlisted, SuggestedAction: "停止收集", SnapshotTS: day},
	}
	events := BuildTop30PushEvents(rows, day)
	if len(events) != 0 {
		t.Fatalf("expected zero events, got %+v", events)
	}
}

func TestRenderTop30PostMessageProducesInteractiveCard(t *testing.T) {
	event := Top30PushEvent{
		Symbol:       "ABC-USDT (perp)",
		Action:       "优先上架",
		MaxCoverage:  7,
		DashboardURL: "https://dashboard.example.test/top30?symbol=ABC-USDT",
		SnapshotDate: "2026-05-27",
		DedupeKey:    "top30_hot_gap|ABC-USDT (perp)|优先上架|2026-05-27",
		StreakDays:   1,
		TriggerTime:  time.Date(2026, 5, 27, 16, 4, 0, 0, time.UTC),
		Platforms: []Top30PlatformEvidence{
			{Platform: "binance", Rank: 3, Volume24HUSD: 1000},
			{Platform: "okx", Rank: 8, Volume24HUSD: 800},
		},
	}
	body, err := RenderTop30PostMessage(event)
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
		`"template":"red"`,
		"📊 Top 30 热门标的 · 优先上架",
		"# ABC-USDT (perp)",
		"🆕 NEW",
		"7/9 平台",
		"$1.80K",
		"binance #3",
		"<font color='red'>●</font>",
		"**binance**",
		"#3",
		"#8",
		`"type":"primary"`,
		"📊 查看 Top30 详情",
		"dashboard.example.test",
		"📈 Binance K 线",
		"binance.com/en/futures/ABCUSDT",
		"触发时间 2026-05-27 16:04 UTC",
		"top30_hot_gap|ABC-USDT (perp)|优先上架|2026-05-27",
	}
	for _, want := range wantSubstrings {
		if !contains(bs, want) {
			t.Fatalf("missing %q in body: %s", want, bs)
		}
	}
}

func TestRenderTop30PostMessageActionPicksHeaderColour(t *testing.T) {
	cases := map[string]string{
		"优先上架": `"template":"red"`,
		"评估上架": `"template":"blue"`,
		"未知动作": `"template":"grey"`,
	}
	for action, wantTemplate := range cases {
		ev := Top30PushEvent{
			Symbol:      "X-USDT",
			Action:      action,
			MaxCoverage: 1,
			Platforms:   []Top30PlatformEvidence{{Platform: "binance", Rank: 5, Volume24HUSD: 1}},
		}
		body, err := RenderTop30PostMessage(ev)
		if err != nil {
			t.Fatalf("%s render: %v", action, err)
		}
		if !contains(string(body), wantTemplate) {
			t.Fatalf("action %q missing %s in body: %s", action, wantTemplate, body)
		}
	}
}

func TestRenderTop30PostMessageStreakBadgeFormatting(t *testing.T) {
	cases := map[int]string{
		0: "",
		1: "🆕 NEW",
		2: "已第 2 天在榜",
		7: "已第 7 天在榜",
	}
	for days, wantBadge := range cases {
		got := top30StreakBadge(days)
		if got != wantBadge {
			t.Fatalf("top30StreakBadge(%d) = %q, want %q", days, got, wantBadge)
		}
	}
}

func TestRenderTop30PostMessageTierBullet(t *testing.T) {
	ev := Top30PushEvent{
		Symbol:      "X-USDT",
		Action:      "评估上架",
		MaxCoverage: 3,
		Platforms: []Top30PlatformEvidence{
			{Platform: "okx", Rank: 5, Volume24HUSD: 1},
			{Platform: "bitget", Rank: 18, Volume24HUSD: 1},
			{Platform: "binance", Rank: 28, Volume24HUSD: 1},
		},
	}
	body, err := RenderTop30PostMessage(ev)
	if err != nil {
		t.Fatal(err)
	}
	bs := string(body)
	if !contains(bs, "<font color='red'>●</font>") {
		t.Fatalf("rank<=10 should render red bullet: %s", bs)
	}
	if !contains(bs, "<font color='orange'>●</font>") {
		t.Fatalf("rank in 11..20 should render orange bullet: %s", bs)
	}
	if !contains(bs, "<font color='grey'>●</font>") {
		t.Fatalf("rank>=21 should render grey bullet: %s", bs)
	}
	if contains(bs, "(边缘)") {
		t.Fatalf("edge marker should be removed; bullet colour conveys tier: %s", bs)
	}
}

func TestSplitDisplaySymbol(t *testing.T) {
	cases := map[string]struct {
		in          string
		base, quote string
		ok          bool
	}{
		"perp suffix":    {"BEAT-USDT (perp)", "BEAT", "USDT", true},
		"plain dash":     {"BEAT-USDT", "BEAT", "USDT", true},
		"slash":          {"ETH/USDC", "ETH", "USDC", true},
		"lower case":     {"eth-usdt", "ETH", "USDT", true},
		"single token":   {"WEIRD", "", "", false},
		"empty":          {"", "", "", false},
		"only separator": {"-USDT", "", "", false},
	}
	for name, c := range cases {
		base, quote, ok := splitDisplaySymbol(c.in)
		if base != c.base || quote != c.quote || ok != c.ok {
			t.Fatalf("%s: got (%q,%q,%v) want (%q,%q,%v)", name, base, quote, ok, c.base, c.quote, c.ok)
		}
	}
}

func TestBuildExchangeKlineURLAllPlatforms(t *testing.T) {
	cases := map[string]struct {
		platform string
		want     string
	}{
		"binance":     {"binance", "https://www.binance.com/en/futures/BEATUSDT"},
		"okx":         {"okx", "https://www.okx.com/trade-swap/beat-usdt-swap"},
		"bybit":       {"bybit", "https://www.bybit.com/trade/usdt/BEATUSDT"},
		"bitget":      {"bitget", "https://www.bitget.com/futures/usdt/BEATUSDT"},
		"gate":        {"gate", "https://www.gate.com/futures/USDT/BEAT_USDT"},
		"mexc":        {"mexc", "https://www.mexc.com/futures/BEAT_USDT"},
		"bingx":       {"bingx", "https://bingx.com/en/perpetual/BEAT-USDT"},
		"hyperliquid": {"hyperliquid", "https://app.hyperliquid.xyz/trade/BEAT"},
		"lighter":     {"lighter", ""},
		"unknown":     {"someExchange", ""},
		"case mixed":  {"BinAnCe", "https://www.binance.com/en/futures/BEATUSDT"},
	}
	for name, c := range cases {
		got := buildExchangeKlineURL(c.platform, "BEAT", "USDT")
		if got != c.want {
			t.Fatalf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestChooseTop30KlineButtonPrefersBinance(t *testing.T) {
	ev := Top30PushEvent{
		Symbol: "BEAT-USDT (perp)",
		Platforms: []Top30PlatformEvidence{
			{Platform: "okx", Rank: 14},
			{Platform: "binance", Rank: 28},
		},
	}
	label, url := chooseTop30KlineButton(ev)
	if label != "📈 Binance K 线" {
		t.Fatalf("label: got %q want Binance K 线 (binance present, even when edge)", label)
	}
	if url != "https://www.binance.com/en/futures/BEATUSDT" {
		t.Fatalf("url: got %q", url)
	}
}

func TestChooseTop30KlineButtonPicksStrongestNonBinanceWithTemplate(t *testing.T) {
	ev := Top30PushEvent{
		Symbol: "BEAT-USDT (perp)",
		Platforms: []Top30PlatformEvidence{
			{Platform: "okx", Rank: 14},
			{Platform: "bitget", Rank: 17},
			{Platform: "gate", Rank: 19},
		},
	}
	label, url := chooseTop30KlineButton(ev)
	if label != "📈 OKX K 线" {
		t.Fatalf("label: got %q want OKX (strongest by rank)", label)
	}
	if url != "https://www.okx.com/trade-swap/beat-usdt-swap" {
		t.Fatalf("url: got %q", url)
	}
}

func TestChooseTop30KlineButtonSkipsTemplatelessAndFallsThrough(t *testing.T) {
	ev := Top30PushEvent{
		Symbol: "BEAT-USDT (perp)",
		Platforms: []Top30PlatformEvidence{
			{Platform: "lighter", Rank: 12},
			{Platform: "bybit", Rank: 18},
		},
	}
	label, url := chooseTop30KlineButton(ev)
	if label != "📈 Bybit K 线" {
		t.Fatalf("label: got %q want Bybit (skip lighter, next strongest with template)", label)
	}
	if url != "https://www.bybit.com/trade/usdt/BEATUSDT" {
		t.Fatalf("url: got %q", url)
	}
}

func TestChooseTop30KlineButtonFallsBackToBinanceWhenNoTemplate(t *testing.T) {
	ev := Top30PushEvent{
		Symbol: "BEAT-USDT (perp)",
		Platforms: []Top30PlatformEvidence{
			{Platform: "lighter", Rank: 12},
		},
	}
	label, url := chooseTop30KlineButton(ev)
	if label != "📈 Binance K 线" {
		t.Fatalf("label: got %q want Binance (last-ditch fallback when no template matches)", label)
	}
	if url != "https://www.binance.com/en/futures/BEATUSDT" {
		t.Fatalf("url: got %q", url)
	}
}

func TestChooseTop30KlineButtonReturnsEmptyOnUnparseableSymbol(t *testing.T) {
	ev := Top30PushEvent{
		Symbol: "WEIRD",
		Platforms: []Top30PlatformEvidence{
			{Platform: "binance", Rank: 1},
		},
	}
	label, url := chooseTop30KlineButton(ev)
	if label != "" || url != "" {
		t.Fatalf("expected empty label/url for unparseable symbol; got (%q,%q)", label, url)
	}
}

func TestRenderTop30PostMessageOmitsPrimaryButtonWithoutDashboardURL(t *testing.T) {
	ev := Top30PushEvent{
		Symbol:      "X-USDT",
		Action:      "优先上架",
		MaxCoverage: 1,
		Platforms:   []Top30PlatformEvidence{{Platform: "okx", Rank: 5, Volume24HUSD: 1}},
	}
	body, err := RenderTop30PostMessage(ev)
	if err != nil {
		t.Fatal(err)
	}
	bs := string(body)
	if contains(bs, "查看 Top30 详情") {
		t.Fatalf("expected primary button absent when DashboardURL is empty: %s", bs)
	}
	if !contains(bs, "📈 OKX K 线") {
		t.Fatalf("expected secondary K-line button to remain (OKX picked since binance absent): %s", bs)
	}
}

func TestBuildDashboardSymbolURLAppendsQuery(t *testing.T) {
	cases := map[string]struct{ base, sym, want string }{
		"empty base":     {"", "ABC-USDT", ""},
		"plain base":     {"https://x.test/top30", "ABC-USDT (perp)", "https://x.test/top30?symbol=ABC-USDT+%28perp%29"},
		"existing query": {"https://x.test/top30?tab=spot", "ABC-USDT", "https://x.test/top30?tab=spot&symbol=ABC-USDT"},
	}
	for name, c := range cases {
		got := buildDashboardSymbolURL(c.base, c.sym)
		if got != c.want {
			t.Fatalf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestCountTop30StreakWalksConsecutivePriorDays(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"d"}).
		AddRow([]byte("2026-05-27")).
		AddRow([]byte("2026-05-26")).
		AddRow([]byte("2026-05-23")) // gap → streak should stop after 2

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT DATE(observed_at)")).
		WithArgs(SignalTop30HotGap, "优先上架", "ABC-USDT (perp)", "2026-05-28").
		WillReturnRows(rows)

	got, err := repo.countTop30Streak(context.Background(), "ABC-USDT (perp)", "优先上架", now)
	if err != nil {
		t.Fatalf("countTop30Streak: %v", err)
	}
	if got != 2 {
		t.Fatalf("streak = %d, want 2 (yesterday + day-before; gap on 05-23 stops it)", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCountTop30StreakReturnsZeroWhenNoHistory(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT DATE(observed_at)")).
		WithArgs(SignalTop30HotGap, "优先上架", "NEW-USDT", "2026-05-28").
		WillReturnRows(sqlmock.NewRows([]string{"d"}))

	got, err := repo.countTop30Streak(context.Background(), "NEW-USDT", "优先上架", now)
	if err != nil {
		t.Fatalf("countTop30Streak: %v", err)
	}
	if got != 0 {
		t.Fatalf("streak = %d, want 0 (no history → caller adds 1 → NEW badge)", got)
	}
}

func TestCountTop30StreakStopsImmediatelyWhenYesterdayMissing(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"d"}).
		AddRow([]byte("2026-05-26")) // skipped yesterday

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT DATE(observed_at)")).
		WithArgs(SignalTop30HotGap, "评估上架", "GAP-USDT", "2026-05-28").
		WillReturnRows(rows)

	got, err := repo.countTop30Streak(context.Background(), "GAP-USDT", "评估上架", now)
	if err != nil {
		t.Fatalf("countTop30Streak: %v", err)
	}
	if got != 0 {
		t.Fatalf("streak = %d, want 0 (today's push resets a broken run)", got)
	}
}

func TestProduceTop30PushAutoQuietsAtStreakThreshold(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	listed := false
	snapshot := now.Add(-5 * time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(snapshot))
	rows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).AddRow("binance", "ABC-USDT (perp)", 5, 1000.0, 6, listed, "评估上架", snapshot)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(snapshot).
		WillReturnRows(rows)
	streakRows := sqlmock.NewRows([]string{"d"}).
		AddRow([]byte("2026-05-27")).
		AddRow([]byte("2026-05-26")) // 2 prior days → ev.StreakDays = 3
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT DATE(observed_at)")).
		WithArgs(SignalTop30HotGap, "评估上架", "ABC-USDT (perp)", "2026-05-28").
		WillReturnRows(streakRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
		WillReturnResult(sqlmock.NewResult(99, 1))
	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	deps := Top30Deps{
		LoadUniverse:             func() (*config.ListedUniverse, error) { return universe, nil },
		Now:                      func() time.Time { return now },
		WebhookURL:               "https://example.test/hook",
		MaxAttempts:              5,
		StaleAfter:               time.Hour,
		AutoQuietAfterStreakDays: 3,
	}
	res, err := ProduceTop30Push(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceTop30Push err = %v", err)
	}
	if res.Events != 1 || res.Signals != 1 || res.OutboxRows != 0 || res.AutoQuieted != 1 {
		t.Fatalf("result = %+v, want Events=1 Signals=1 OutboxRows=0 AutoQuieted=1", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProduceTop30PushStaggersNextAttemptWithSendSpacing(t *testing.T) {
	now := time.Date(2026, 5, 28, 16, 4, 0, 0, time.UTC)
	repo, mock, cleanup := newRepoWithMock(t, now)
	defer cleanup()
	listed := false
	snapshot := now.Add(-5 * time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT MAX(snapshot_ts) FROM t_top30_snapshot")).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(snapshot))
	// Two distinct symbols → 2 events, both with brand-new streak (returns 0 → ev.StreakDays = 1).
	rows := sqlmock.NewRows([]string{
		"platform", "symbol", "rank_no", "volume_24h_usd", "coverage_count", "edgex_listed", "suggested_action", "snapshot_ts",
	}).
		AddRow("binance", "AAA-USDT (perp)", 5, 1000.0, 6, listed, "评估上架", snapshot).
		AddRow("binance", "BBB-USDT (perp)", 7, 800.0, 5, listed, "评估上架", snapshot)
	mock.ExpectQuery(`SELECT platform, symbol, rank_no.+FROM t_top30_snapshot.+WHERE snapshot_ts`).
		WithArgs(snapshot).
		WillReturnRows(rows)
	// Order is per-event: streak → InsertSignal → insertOutbox, then
	// next event. BuildTop30PushEvents sorts events by Symbol, so
	// AAA goes before BBB; AAA's outbox row gets NextAttemptAt=now and
	// BBB's gets NextAttemptAt=now+spacing.
	spacing := 10 * time.Minute
	expectations := []struct {
		sym         string
		nextAttempt time.Time
	}{
		{"AAA-USDT (perp)", now},
		{"BBB-USDT (perp)", now.Add(spacing)},
	}
	for _, exp := range expectations {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT DATE(observed_at)")).
			WithArgs(SignalTop30HotGap, "评估上架", exp.sym, "2026-05-28").
			WillReturnRows(sqlmock.NewRows([]string{"d"}))
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_signal_observation")).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO t_listing_delivery_outbox")).
			WithArgs(
				DeliveryEventTop30HotGap, sqlmock.AnyArg(), DeliveryChannelLarkTop30, OutboxStatusPending,
				0, 5, exp.nextAttempt, sqlmock.AnyArg(), nil,
				sqlmock.AnyArg(), now, now,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	universe := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	deps := Top30Deps{
		LoadUniverse: func() (*config.ListedUniverse, error) { return universe, nil },
		Now:          func() time.Time { return now },
		WebhookURL:   "https://example.test/hook",
		MaxAttempts:  5,
		StaleAfter:   time.Hour,
		SendSpacing:  spacing,
	}
	res, err := ProduceTop30Push(context.Background(), repo, deps)
	if err != nil {
		t.Fatalf("ProduceTop30Push err = %v", err)
	}
	if res.Events != 2 || res.OutboxRows != 2 || res.AutoQuieted != 0 {
		t.Fatalf("result = %+v, want Events=2 OutboxRows=2 AutoQuieted=0", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
