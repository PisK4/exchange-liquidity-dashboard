package coingecko

import (
	"sort"
	"testing"
)

func defaultMapping() *Mapping {
	exchange := map[string]string{
		"binance":     "binance_futures",
		"okx":         "okex_swap",
		"bybit":       "bybit",
		"bitget":      "bitget_futures",
		"bingx":       "bingx_futures",
		"mexc":        "mxc_futures",
		"gate":        "gate_futures",
		"hyperliquid": "hyperliquid",
		"lighter":     "lighter",
	}
	market := map[string]string{
		"binance":     "Binance (Futures)",
		"okx":         "OKX (Futures)",
		"bybit":       "Bybit (Futures)",
		"bitget":      "Bitget Futures",
		"bingx":       "BingX (Futures)",
		"mexc":        "MEXC (Futures)",
		"gate":        "Gate (Futures)",
		"hyperliquid": "Hyperliquid (Futures)",
		"lighter":     "Lighter",
	}
	return NewMapping(exchange, market)
}

func TestMappingMarketNameRoundTrip(t *testing.T) {
	m := defaultMapping()
	cases := []struct {
		market   string
		platform string
	}{
		{"Binance (Futures)", "binance"},
		{"OKX (Futures)", "okx"},
		{"Bybit (Futures)", "bybit"},
		{"Bitget Futures", "bitget"},
		{"BingX (Futures)", "bingx"},
		{"MEXC (Futures)", "mexc"},
		{"Gate (Futures)", "gate"},
		{"Hyperliquid (Futures)", "hyperliquid"},
		{"Lighter", "lighter"},
		{"  binance (futures)  ", "binance"}, // case-insensitive trim
	}
	for _, c := range cases {
		p, ok := m.PlatformByMarketName(c.market)
		if !ok || p != c.platform {
			t.Fatalf("PlatformByMarketName(%q) = %q,%v; want %q,true", c.market, p, ok, c.platform)
		}
		if c.platform == "binance" {
			continue
		}
		name, ok := m.MarketNameFor(c.platform)
		if !ok || name == "" {
			t.Fatalf("MarketNameFor(%q) missing", c.platform)
		}
	}
}

func TestMappingExchangeIDRoundTrip(t *testing.T) {
	m := defaultMapping()
	cases := []struct {
		id       string
		platform string
	}{
		{"binance_futures", "binance"},
		{"okex_swap", "okx"},
		{"bybit", "bybit"},
		{"bitget_futures", "bitget"},
		{"bingx_futures", "bingx"},
		{"mxc_futures", "mexc"},
		{"gate_futures", "gate"},
		{"hyperliquid", "hyperliquid"},
		{"lighter", "lighter"},
		{" BINANCE_FUTURES ", "binance"},
	}
	for _, c := range cases {
		p, ok := m.PlatformByExchangeID(c.id)
		if !ok || p != c.platform {
			t.Fatalf("PlatformByExchangeID(%q) = %q,%v; want %q,true", c.id, p, ok, c.platform)
		}
	}
	for _, p := range []string{"binance", "okx", "bybit", "bitget", "bingx", "mexc", "gate", "hyperliquid", "lighter"} {
		id, ok := m.ExchangeIDFor(p)
		if !ok || id == "" {
			t.Fatalf("ExchangeIDFor(%q) missing", p)
		}
	}
}

func TestMappingUnknownMarketReturnsFalse(t *testing.T) {
	m := defaultMapping()
	if _, ok := m.PlatformByMarketName("Random DEX"); ok {
		t.Fatalf("expected unknown market to be rejected")
	}
	if _, ok := m.PlatformByExchangeID("unknown_exchange"); ok {
		t.Fatalf("expected unknown exchange to be rejected")
	}
}

func TestMappingPlatformsCoverAllNine(t *testing.T) {
	m := defaultMapping()
	got := m.Platforms()
	sort.Strings(got)
	want := []string{"bingx", "binance", "bitget", "bybit", "gate", "hyperliquid", "lighter", "mexc", "okx"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("Platforms[%d] = %q, want %q", i, got[i], p)
		}
	}
}

func TestMappingIgnoresEmptyEntries(t *testing.T) {
	m := NewMapping(map[string]string{
		"binance": "binance_futures",
		"":        "ignore_me",
		"okx":     "",
	}, map[string]string{
		"binance": "Binance (Futures)",
		"":        "Ignore Me",
		"okx":     "",
	})
	if _, ok := m.PlatformByMarketName("Ignore Me"); ok {
		t.Fatalf("empty platform key should be skipped")
	}
	if _, ok := m.PlatformByExchangeID("ignore_me"); ok {
		t.Fatalf("empty platform key should be skipped")
	}
	if _, ok := m.ExchangeIDFor("okx"); ok {
		t.Fatalf("empty exchange id should be skipped")
	}
}

func TestNormaliseSymbol(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"BTCUSDT", "BTC-USDT (perp)"},
		{"ETH-USDT", "ETH-USDT (perp)"},
		{"sol-usdt", "SOL-USDT (perp)"},
		{"BTCUSD", "BTC-USD (perp)"},
		{"ETHUSDC", "ETH-USDC (perp)"},
		{"WEIRD", "WEIRD"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormaliseSymbol(c.in); got != c.want {
			t.Fatalf("NormaliseSymbol(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
