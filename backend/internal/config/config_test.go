package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadReadsYAMLSourceOfTruth(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: DOGE-USDT (perp)
    canonical: DOGE
    market_surface: perp
    instrument_kind: canonical
platforms: [binance, hyperliquid]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  binance: https://example.invalid/binance-depth
  hyperliquid: https://example.invalid/hyperliquid-info
`)
	mustWrite(t, filepath.Join(dir, "runtime.yaml"), `
collection_interval: 90s
display_fallback_window: 45m
depth_tiers: [0.001, 0.02]
slippage_buckets_usd: [25000, 75000]
volume_discounts:
  binance: 0.9
  hyperliquid: 1.0
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Symbols) != 2 {
		t.Fatalf("expected 2 platform subscriptions, got %d", len(cfg.Symbols))
	}
	if cfg.Symbols[0].DisplaySymbol != "DOGE-USDT (perp)" || cfg.Symbols[0].APISymbol != "DOGEUSDT" {
		t.Fatalf("unexpected first symbol mapping: %+v", cfg.Symbols[0])
	}
	if cfg.Symbols[1].APISymbol != "DOGE" {
		t.Fatalf("expected hyperliquid canonical api symbol, got %q", cfg.Symbols[1].APISymbol)
	}
	if cfg.Symbols[0].SourceEndpoint != "https://example.invalid/binance-depth" {
		t.Fatalf("endpoint override not applied: %+v", cfg.Symbols[0])
	}
	if cfg.Runtime.CollectionInterval != 90*time.Second {
		t.Fatalf("collection interval = %s", cfg.Runtime.CollectionInterval)
	}
	if cfg.Runtime.DisplayFallbackWindow != 45*time.Minute {
		t.Fatalf("display fallback window = %s", cfg.Runtime.DisplayFallbackWindow)
	}
	if cfg.Runtime.VolumeDiscounts["binance"] != 0.9 {
		t.Fatalf("volume discount not loaded: %+v", cfg.Runtime.VolumeDiscounts)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadCatalogParsesRepoYAML(t *testing.T) {
	path := filepath.Join("..", "..", "..", "config", "instrument_catalog.yaml")
	cat, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	if cat.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", cat.SchemaVersion)
	}
	if len(cat.CanonicalWhitelist) != 3 {
		t.Fatalf("canonical_whitelist len = %d, want 3", len(cat.CanonicalWhitelist))
	}
	wantPlatforms := []string{"binance", "okx", "bybit", "bitget", "bingx", "mexc", "gate", "hyperliquid", "edgeX", "lighter"}
	for _, p := range wantPlatforms {
		entries, ok := cat.Platforms[p]
		if !ok {
			t.Fatalf("platform %q missing from catalog", p)
		}
		for _, c := range []string{"BTC", "ETH", "SOL"} {
			sym, ok := entries[c]
			if !ok {
				t.Fatalf("platform %q canonical %q missing", p, c)
			}
			if sym.APISymbol == "" || sym.BaseAsset == "" || sym.QuoteAsset == "" {
				t.Fatalf("platform %q canonical %q missing required fields: %+v", p, c, sym)
			}
			if sym.FrontendURL == "" {
				t.Fatalf("platform %q canonical %q missing frontend_url", p, c)
			}
		}
	}
	if got := cat.Platforms["edgeX"]["BTC"].ContractID; got != "10000001" {
		t.Fatalf("edgeX BTC contract_id = %q, want 10000001", got)
	}
	if got := cat.Platforms["lighter"]["ETH"].MarketID; got == nil || *got != 0 {
		t.Fatalf("lighter ETH market_id = %v, want *int(0)", got)
	}
	if got := cat.Platforms["lighter"]["BTC"].MarketID; got == nil || *got != 1 {
		t.Fatalf("lighter BTC market_id = %v, want *int(1)", got)
	}
	if got := cat.Platforms["binance"]["BTC"].MarketID; got != nil {
		t.Fatalf("binance BTC should have nil market_id, got %v", got)
	}
	if got := cat.Platforms["mexc"]["BTC"].ContractSize; got != 0.0001 {
		t.Fatalf("mexc BTC contract_size = %v, want 0.0001", got)
	}
	if got := cat.Platforms["gate"]["BTC"].QuantoMultiplier; got != 0.0001 {
		t.Fatalf("gate BTC quanto_multiplier = %v, want 0.0001", got)
	}
}

func TestLoadCatalogMissingFile(t *testing.T) {
	cat, err := LoadCatalog(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("LoadCatalog on missing file should not error, got %v", err)
	}
	if cat.SchemaVersion != 0 || len(cat.Platforms) != 0 {
		t.Fatalf("missing-file catalog should be zero value, got %+v", cat)
	}
}
