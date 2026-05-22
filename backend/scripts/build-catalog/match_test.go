package main

import (
	"os"
	"path/filepath"
	"testing"

	"edgex-dashboard/backend/internal/adapter"
)

func TestMatchInstrumentCryptoFallsBackToCanonical(t *testing.T) {
	insts := []adapter.Instrument{
		{APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
		{APISymbol: "BTCUSDT_240329", BaseAsset: "BTC", QuoteAsset: "USDT"},
		{APISymbol: "ETHUSDT", BaseAsset: "ETH", QuoteAsset: "USDT"},
	}
	got, ok := matchInstrument(insts, "BTC", "USDT", "binance", nil)
	if !ok {
		t.Fatalf("expected match for BTC, got none")
	}
	if got.APISymbol != "BTCUSDT" {
		t.Errorf("APISymbol = %q, want BTCUSDT (dated future must be filtered)", got.APISymbol)
	}
}

func TestMatchInstrumentCryptoRespectsExpectedQuote(t *testing.T) {
	insts := []adapter.Instrument{
		{APISymbol: "BTCUSDC", BaseAsset: "BTC", QuoteAsset: "USDC"},
		{APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
	}
	got, ok := matchInstrument(insts, "BTC", "USDT", "binance", nil)
	if !ok {
		t.Fatalf("expected match")
	}
	if got.APISymbol != "BTCUSDT" {
		t.Errorf("expectedQuote=USDT must reject USDC pair, got %q", got.APISymbol)
	}
}

func TestMatchInstrumentAliasPicksConfiguredBase(t *testing.T) {
	insts := []adapter.Instrument{
		{APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
		{APISymbol: "NCSKSAMSUNG2USD-USDT", BaseAsset: "NCSKSAMSUNG2USD", QuoteAsset: "USDT"},
		{APISymbol: "NVDA-USDT", BaseAsset: "NVDA", QuoteAsset: "USDT"},
	}
	got, ok := matchInstrument(insts, "SAMSUNG", "USDT", "bingx", []string{"NCSKSAMSUNG2USD"})
	if !ok {
		t.Fatalf("expected alias match for SAMSUNG -> NCSKSAMSUNG2USD")
	}
	if got.APISymbol != "NCSKSAMSUNG2USD-USDT" {
		t.Errorf("APISymbol = %q, want NCSKSAMSUNG2USD-USDT", got.APISymbol)
	}
}

func TestMatchInstrumentAliasFallsBackThroughMultiple(t *testing.T) {
	insts := []adapter.Instrument{
		{APISymbol: "JP225_USDT", BaseAsset: "JP225", QuoteAsset: "USDT"},
		{APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
	}
	got, ok := matchInstrument(insts, "JP225", "USDT", "mexc", []string{"NIKKEI225", "JPN225", "JP225"})
	if !ok {
		t.Fatalf("expected fallback to the third alias")
	}
	if got.BaseAsset != "JP225" {
		t.Errorf("BaseAsset = %q, want JP225", got.BaseAsset)
	}
}

func TestMatchInstrumentAliasAcceptsOffQuote(t *testing.T) {
	// Synthetic / RWA markets often quote in USDC; the alias path treats
	// expectedQuote as a preference, not a hard filter.
	insts := []adapter.Instrument{
		{APISymbol: "DRAMUSDC-PERP", BaseAsset: "DRAM", QuoteAsset: "USDC"},
	}
	got, ok := matchInstrument(insts, "DRAM", "USDT", "lighter", []string{"DRAM"})
	if !ok {
		t.Fatalf("expected alias match even with off-quote pair")
	}
	if got.QuoteAsset != "USDC" {
		t.Errorf("QuoteAsset = %q, want USDC (alias path is quote-agnostic)", got.QuoteAsset)
	}
}

func TestMatchInstrumentReturnsFalseWhenNoCandidate(t *testing.T) {
	insts := []adapter.Instrument{
		{APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
	}
	if _, ok := matchInstrument(insts, "DOES_NOT_EXIST", "USDT", "binance", nil); ok {
		t.Errorf("expected no match for unknown canonical without aliases")
	}
	if _, ok := matchInstrument(insts, "SAMSUNG", "USDT", "bingx", []string{"NCSKSAMSUNG2USD"}); ok {
		t.Errorf("expected no match when alias is missing from instruments")
	}
}

func TestLoadSymbolWhitelistParsesAliasesAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "symbol_mapping.yaml")
	yaml := `symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    asset_category: crypto
    market_surface: perp
    instrument_kind: canonical
  - display_symbol: SAMSUNG-USDT (perp)
    canonical: SAMSUNG
    asset_category: stock
    instrument_kind: synthetic
    preferred_surface: [perp, synthetic_futures, rwa_spot]
    aliases:
      bingx: [NCSKSAMSUNG2USD]
      lighter: [SAMSUNGUSD, SAMSUNG]
    platform_overrides:
      bingx:
        market_surface: synthetic_futures
        lineage: "bingx:NCSKSAMSUNG2USD@synthetic_futures"
platforms: [binance, bingx, lighter]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write whitelist: %v", err)
	}
	wl, err := loadSymbolWhitelist(path)
	if err != nil {
		t.Fatalf("loadSymbolWhitelist: %v", err)
	}
	if len(wl.symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(wl.symbols))
	}
	samsung := wl.symbols[1]
	if samsung.canonical != "SAMSUNG" {
		t.Fatalf("second symbol canonical = %q", samsung.canonical)
	}
	if got := samsung.aliases["bingx"]; len(got) != 1 || got[0] != "NCSKSAMSUNG2USD" {
		t.Errorf("bingx aliases = %v", got)
	}
	if got := samsung.aliases["lighter"]; len(got) != 2 || got[1] != "SAMSUNG" {
		t.Errorf("lighter aliases = %v", got)
	}
	if got := samsung.preferredSurface; len(got) != 3 || got[1] != "synthetic_futures" {
		t.Errorf("preferred_surface = %v", got)
	}
	override, ok := samsung.platformOverrides["bingx"]
	if !ok {
		t.Fatalf("bingx override missing")
	}
	if override.MarketSurface != "synthetic_futures" {
		t.Errorf("override MarketSurface = %q", override.MarketSurface)
	}
	if override.Lineage != "bingx:NCSKSAMSUNG2USD@synthetic_futures" {
		t.Errorf("override Lineage = %q", override.Lineage)
	}
	// BTC keeps empty alias / override maps and should not crash the consumer.
	btc := wl.symbols[0]
	if len(btc.aliases) != 0 {
		t.Errorf("BTC unexpected aliases = %v", btc.aliases)
	}
	if len(btc.platformOverrides) != 0 {
		t.Errorf("BTC unexpected overrides = %v", btc.platformOverrides)
	}
}
