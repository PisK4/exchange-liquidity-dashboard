package listing

import (
	"encoding/json"
	"testing"
	"time"
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

func TestRenderTop30PostMessageContainsSymbolAndAction(t *testing.T) {
	event := Top30PushEvent{
		Symbol:       "ABC-USDT (perp)",
		Action:       "优先上架",
		MaxCoverage:  7,
		DashboardURL: "https://dashboard.example.test/top30?symbol=ABC-USDT",
		SnapshotDate: "2026-05-27",
		Platforms: []Top30PlatformEvidence{
			{Platform: "binance", Rank: 3, Volume24HUSD: 1000},
			{Platform: "okx", Rank: 8, Volume24HUSD: 800},
		},
	}
	body, err := RenderTop30PostMessage(event)
	if err != nil {
		t.Fatal(err)
	}
	bs := string(body)
	for _, want := range []string{`"msg_type":"post"`, "ABC-USDT", "优先上架", "binance", "dashboard.example.test"} {
		if !contains(bs, want) {
			t.Fatalf("missing %q in body: %s", want, bs)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not json: %v", err)
	}
	if decoded["msg_type"] != "post" {
		t.Fatalf("msg_type = %v, want post", decoded["msg_type"])
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
