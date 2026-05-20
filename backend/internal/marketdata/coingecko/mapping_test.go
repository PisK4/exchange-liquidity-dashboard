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
		// Plain shapes.
		{"BTCUSDT", "BTC-USDT (perp)"},
		{"ETH-USDT", "ETH-USDT (perp)"},
		{"sol-usdt", "SOL-USDT (perp)"},
		{"BTCUSD", "BTC-USD (perp)"},
		{"ETHUSDC", "ETH-USDC (perp)"},
		{"WEIRD", "WEIRD"},
		{"", ""},
		// OKX shapes — "-SWAP" suffix must collapse.
		{"BTC-USDT-SWAP", "BTC-USDT (perp)"},
		{"ETH-USDC-SWAP", "ETH-USDC (perp)"},
		// Bitget shapes — "_UMCBL"/"_DMCBL"/"_CMCBL" suffixes must collapse.
		{"BTCUSDT_UMCBL", "BTC-USDT (perp)"},
		{"BTCUSD_DMCBL", "BTC-USD (perp)"},
		{"BTCUSDC_CMCBL", "BTC-USDC (perp)"},
		// MEXC / Gate / Lighter shapes — "_" between base and quote must drop.
		{"BTC_USDT", "BTC-USDT (perp)"},
		{"ETH_USDC", "ETH-USDC (perp)"},
		{"1000PEPE_USDT", "1000PEPE-USDT (perp)"},
		// Idempotent: running normaliser on an already-normalised string
		// returns the same value so retries from history merge cleanly.
		{"BTC-USDT (perp)", "BTC-USDT (perp)"},
	}
	for _, c := range cases {
		if got := NormaliseSymbol(c.in); got != c.want {
			t.Fatalf("NormaliseSymbol(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormaliseSymbolConvergesAcrossNineVenues(t *testing.T) {
	// All nine of these forms describe the SAME BTC USDT perp; if any of
	// them diverge from "BTC-USDT (perp)" the Top30 coverage column will
	// under-count the symbol's competitor reach (regression guard for the
	// 2026-05-20 cov=3/9 bug).
	rawForms := []string{
		"BTCUSDT",       // binance / bybit (compact)
		"BTC-USDT",      // bingx
		"BTC-USDT-SWAP", // okx
		"BTCUSDT_UMCBL", // bitget V1 USDT-M
		"BTC_USDT",      // mexc / gate / lighter
	}
	const want = "BTC-USDT (perp)"
	for _, raw := range rawForms {
		if got := NormaliseSymbol(raw); got != want {
			t.Fatalf("NormaliseSymbol(%q) = %q, want %q", raw, got, want)
		}
	}
}
