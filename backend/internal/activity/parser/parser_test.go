package parser

import (
	"context"
	"testing"

	"edgex-ops-intelligence/backend/internal/activity"
)

func TestNinePlatformParsersProduceActivityEvents(t *testing.T) {
	cases := []struct {
		name   string
		parse  func(context.Context, RawDocument) ([]activity.ActivityEvent, error)
		doc    RawDocument
		wantTy string
	}{
		{"binance", ParseBinance, fixtureDoc("binance", "cms_article_detail", `{"id":"bn-1","title":"Binance Launchpool ABC","summary":"Stake BNB to earn ABC","url":"https://binance.example/a","activity_type":"launchpool","symbols":["ABC"]}`), "launchpool"},
		{"okx", ParseOKX, fixtureDoc("okx", "help_announcement", `{"id":"okx-1","title":"OKX Airdrop Campaign","summary":"Complete tasks to share rewards","url":"https://okx.example/a","activity_type":"airdrop_campaign","symbols":["OKB"]}`), "airdrop_campaign"},
		{"bingx", ParseBingX, fixtureDoc("bingx", "openapi_notice", `{"id":"bx-1","title":"BingX Trading Competition","summary":"Trade futures to win","url":"https://bingx.example/a","activity_type":"futures_trading_competition","symbols":["BTC"]}`), "futures_trading_competition"},
		{"gate", ParseGate, fixtureDoc("gate", "launchpool_project_list", `{"id":"gate-1","title":"Gate Launchpool CTR","summary":"Launchpool project list entry","url":"https://gate.example/a","activity_type":"launchpool","symbols":["CTR"],"reward_pools":[{"token":"USDT","amount":"100000"}]}`), "launchpool"},
		{"mexc", ParseMEXC, fixtureDoc("mexc", "latest_events", `{"id":"mexc-1","title":"MEXC M-Day Futures","summary":"M-Day campaign","url":"https://mexc.example/a","activity_type":"futures_trading_competition","symbols":["MX"]}`), "futures_trading_competition"},
		{"bybit", ParseBybit, fixtureDoc("bybit", "announcements_ssr", `{"id":"bb-1","title":"Bybit Rewards Campaign","summary":"Rewards hub public announcement","url":"https://bybit.example/a","activity_type":"airdrop_campaign","symbols":["MNT"]}`), "airdrop_campaign"},
		{"bitget", ParseBitget, fixtureDoc("bitget", "support_ongoing_section", `{"id":"bg-1","title":"Bitget PoolX Campaign","summary":"PoolX ongoing activity","url":"https://bitget.example/a","activity_type":"launchpool","symbols":["BGB"]}`), "launchpool"},
		{"hyperliquid", ParseHyperliquid, fixtureDoc("hyperliquid", "cloudfront_entries", `{"id":"hl-1","title":"Hyperliquid Listing Update","summary":"Non-CEX update event","url":"https://hyperliquid.example/a","activity_type":"non_cex_update_event","symbols":["HYPE"]}`), "non_cex_update_event"},
		{"lighter", ParseLighter, fixtureDoc("lighter", "incentive_docs", `{"id":"lt-1","title":"Lighter LPP Incentive Rules","summary":"Liquidity points program markdown snapshot","url":"https://lighter.example/a","activity_type":"incentive_rule_snapshot","symbols":["ETH"]}`), "incentive_rule_snapshot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events, err := c.parse(context.Background(), c.doc)
			if err != nil {
				t.Fatalf("parse err=%v", err)
			}
			if len(events) != 1 {
				t.Fatalf("events len=%d want 1", len(events))
			}
			ev := events[0]
			if ev.Platform != c.doc.Platform || ev.SourceGroup != c.doc.SourceGroup || ev.ActivityType != c.wantTy || ev.DedupeKey == "" {
				t.Fatalf("event=%+v", ev)
			}
			if ev.NeedsHumanReview || !ev.AutoPushAllowed {
				t.Fatalf("public fixture should be auto-push eligible: %+v", ev)
			}
			if len(ev.TargetSymbols) != 1 {
				t.Fatalf("symbols=%+v", ev.TargetSymbols)
			}
		})
	}
}

func TestParserRequiresReviewForLoginOrPersonalizedSource(t *testing.T) {
	doc := fixtureDoc("bybit", "rewards_hub", `{"id":"p1","title":"Personal Rewards","summary":"User-specific task","url":"https://bybit.example/p","activity_type":"airdrop_campaign"}`)
	doc.RequiresLogin = true
	doc.Personalized = true
	events, err := ParseBybit(context.Background(), doc)
	if err != nil {
		t.Fatalf("parse err=%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len=%d want 1", len(events))
	}
	if !events[0].NeedsHumanReview || events[0].AutoPushAllowed {
		t.Fatalf("personalized/login source must be review-only: %+v", events[0])
	}
}

func TestParserExtractsActivityEventsFromJSONLists(t *testing.T) {
	doc := fixtureDoc("bingx", "openapi_notice", `{
		"code": 0,
		"data": [
			{"id":"promo-1","title":"BingX Futures Trading Competition","summary":"Trade futures to share rewards","url":"https://bingx.example/promo-1","activity_type":"futures_trading_competition","symbols":["BTC"]},
			{"id":"promo-2","name":"BingX Listing Giveaway","content":"New listing giveaway","link":"https://bingx.example/promo-2","symbols":["ETH"]}
		]
	}`)
	events, err := ParseBingX(context.Background(), doc)
	if err != nil {
		t.Fatalf("parse err=%v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len=%d want 2: %+v", len(events), events)
	}
	if events[1].ActivityType == "" || events[1].SourceURL == "" {
		t.Fatalf("event should be normalized: %+v", events[1])
	}
}

func TestParserPrefersArticleURLOverSourceURL(t *testing.T) {
	doc := fixtureDoc("gate", "launchpool_project_list", `{
		"id":"gate-article-1",
		"title":"Gate Launchpool ABC",
		"summary":"Launchpool project list entry",
		"sourceUrl":"https://gate.example/api/list.json",
		"articleUrl":"https://gate.example/article/abc",
		"activity_type":"launchpool"
	}`)
	events, err := ParseGate(context.Background(), doc)
	if err != nil {
		t.Fatalf("parse err=%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len=%d want 1", len(events))
	}
	if events[0].SourceURL != "https://gate.example/article/abc" {
		t.Fatalf("SourceURL=%q want article URL", events[0].SourceURL)
	}
}

func TestParserExtractsActivityEventFromHTMLOrMarkdown(t *testing.T) {
	doc := fixtureDoc("lighter", "incentive_docs", "# Lighter Points Program\n\nRetail users receive weekly points for trading and liquidity activity.")
	doc.FetchMode = "markdown_doc"
	events, err := ParseLighter(context.Background(), doc)
	if err != nil {
		t.Fatalf("parse err=%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len=%d want 1", len(events))
	}
	if events[0].Title != "Lighter Points Program" || events[0].ActivityType != "incentive_rule_snapshot" {
		t.Fatalf("event=%+v", events[0])
	}
}

func fixtureDoc(platform, group, body string) RawDocument {
	return RawDocument{
		Platform:    platform,
		SourceGroup: group,
		SourceURL:   "https://" + platform + ".example/source",
		FetchMode:   "http_direct",
		Payload:     []byte(body),
	}
}
