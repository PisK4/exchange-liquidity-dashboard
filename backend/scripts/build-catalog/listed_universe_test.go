package main

import (
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/adapter"
)

func TestBuildListedUniverseUnionsMarketsAndDropsDelisted(t *testing.T) {
	results := map[string]adapter.CatalogResult{
		"edgeX": {
			Platform: "edgeX",
			Markets: []adapter.MarketDump{
				{MarketType: "perp-v1", Instruments: []adapter.Instrument{
					{APISymbol: "BTCUSD", BaseAsset: "BTC", QuoteAsset: "USD", Status: "TRADING"},
					{APISymbol: "ETHUSD", BaseAsset: "ETH", QuoteAsset: "USD", Status: "TRADING"},
					{APISymbol: "OLDUSD", BaseAsset: "OLD", QuoteAsset: "USD", Status: "DELISTED"},
				}},
				{MarketType: "perp-v2", Instruments: []adapter.Instrument{
					{APISymbol: "BTCUSDC", BaseAsset: "BTC", QuoteAsset: "USDC", Status: "TRADING"},
					{APISymbol: "SOLUSDC", BaseAsset: "SOL", QuoteAsset: "USDC", Status: "TRADING"},
				}},
				{MarketType: "spot", Instruments: []adapter.Instrument{
					{APISymbol: "ETHUSDC", BaseAsset: "eth", QuoteAsset: "USDC", Status: ""}, // blank status accepted
				}},
			},
		},
		"binance": {
			Platform: "binance",
			Markets: []adapter.MarketDump{
				{MarketType: "usd-m", Instruments: []adapter.Instrument{
					{APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "TRADING"},
					{APISymbol: "DOGEUSDT", BaseAsset: "DOGE", QuoteAsset: "USDT", Status: "TRADING"},
				}},
			},
		},
		"empty": {
			Platform: "empty",
			Markets: []adapter.MarketDump{
				{MarketType: "perp", Instruments: nil},
			},
		},
	}
	u := buildListedUniverse(time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), results)

	if u.SchemaVersion != 1 {
		t.Fatalf("schema_version expected 1, got %d", u.SchemaVersion)
	}
	if u.GeneratedAt == "" {
		t.Fatal("generated_at must be filled in")
	}
	edgex, ok := u.Platforms["edgeX"]
	if !ok {
		t.Fatal("edgeX platform missing from universe")
	}
	want := []string{"BTC", "ETH", "SOL"} // OLD dropped (DELISTED), case+dedup normalised
	if got := edgex.BaseAssets; !equalStrings(got, want) {
		t.Fatalf("edgeX bases = %v, want %v", got, want)
	}
	if _, ok := u.Platforms["empty"]; ok {
		t.Fatal("platform with no instruments must be omitted")
	}
	if got := u.Platforms["binance"].BaseAssets; !equalStrings(got, []string{"BTC", "DOGE"}) {
		t.Fatalf("binance bases = %v, want [BTC DOGE]", got)
	}
	if n := countListedBases(u); n != 5 {
		t.Fatalf("total bases = %d, want 5 (3 edgeX + 2 binance)", n)
	}
}

func TestIsListedStatusAcceptsBlankAndKnownLiveStates(t *testing.T) {
	for _, ok := range []string{"", "TRADING", "Live", "open", "Active", "1"} {
		if !isListedStatus(ok) {
			t.Fatalf("status %q should be treated as listed", ok)
		}
	}
	for _, bad := range []string{"DELISTED", "BREAK", "OFFLINE", "PAUSED", "0", "false"} {
		if isListedStatus(bad) {
			t.Fatalf("status %q should NOT be treated as listed", bad)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
