package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/domain"
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
	mustWrite(t, filepath.Join(dir, "instrument_catalog.yaml"), `
schema_version: 1
generated_at: "2026-05-20T00:00:00Z"
generated_by: test
canonical_whitelist:
  - canonical: DOGE
    market_surface: perp
    quote: USDT
    confidence: confirmed
platforms:
  binance:
    DOGE:
      api_symbol: DOGEUSDT
      base_asset: DOGE
      quote_asset: USDT
      settle_asset: USDT
      api_level_cap: 1000
      source_endpoint: https://example.invalid/binance-depth
      url_verified: false
  hyperliquid:
    DOGE:
      api_symbol: DOGE
      base_asset: DOGE
      quote_asset: USDC
      settle_asset: USDC
      api_level_cap: 40
      source_endpoint: https://example.invalid/hyperliquid-info
      url_verified: false
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

func TestLoadAppliesCoinGeckoOverrides(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    market_surface: perp
    instrument_kind: canonical
platforms: [edgeX]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  edgeX: https://example.invalid/edgex
`)
	mustWrite(t, filepath.Join(dir, "runtime.yaml"), `
coingecko:
  enabled: true
  base_url: https://example.invalid/cg/v3
  api_key_env: TEST_CG_KEY
  proxy: http://127.0.0.1:7897
  pull_interval: 7m
  cache_ttl: 90s
  request_timeout: 12s
  exchange_id:
    binance: binance_futures
    bybit: bybit
  market_name:
    binance: "Binance (Futures)"
    bybit: "Bybit (Futures)"
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cg := cfg.Runtime.CoinGecko
	if !cg.Enabled {
		t.Fatalf("CoinGecko.Enabled = false, want true")
	}
	if cg.BaseURL != "https://example.invalid/cg/v3" {
		t.Fatalf("BaseURL = %q", cg.BaseURL)
	}
	if cg.APIKeyEnv != "TEST_CG_KEY" {
		t.Fatalf("APIKeyEnv = %q", cg.APIKeyEnv)
	}
	if cg.Proxy != "http://127.0.0.1:7897" {
		t.Fatalf("Proxy = %q", cg.Proxy)
	}
	if cg.PullInterval != 7*time.Minute {
		t.Fatalf("PullInterval = %s", cg.PullInterval)
	}
	if cg.CacheTTL != 90*time.Second {
		t.Fatalf("CacheTTL = %s", cg.CacheTTL)
	}
	if cg.RequestTimeout != 12*time.Second {
		t.Fatalf("RequestTimeout = %s", cg.RequestTimeout)
	}
	if cg.ExchangeID["binance"] != "binance_futures" || cg.ExchangeID["bybit"] != "bybit" {
		t.Fatalf("ExchangeID = %+v", cg.ExchangeID)
	}
	if cg.MarketName["binance"] != "Binance (Futures)" || cg.MarketName["bybit"] != "Bybit (Futures)" {
		t.Fatalf("MarketName = %+v", cg.MarketName)
	}
}

func TestLoadCoinGeckoDefaultsWhenYAMLOmitsBlock(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    market_surface: perp
    instrument_kind: canonical
platforms: [edgeX]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  edgeX: https://example.invalid/edgex
`)
	mustWrite(t, filepath.Join(dir, "runtime.yaml"), `
collection_interval: 90s
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cg := cfg.Runtime.CoinGecko
	if cg.Enabled {
		t.Fatalf("default Enabled should be false, got true")
	}
	if cg.BaseURL == "" || cg.APIKeyEnv == "" || cg.PullInterval == 0 {
		t.Fatalf("expected default CoinGecko config to be populated, got %+v", cg)
	}
	for _, p := range []string{"binance", "okx", "bybit", "bitget", "bingx", "mexc", "gate", "hyperliquid", "lighter"} {
		if cg.ExchangeID[p] == "" {
			t.Fatalf("default ExchangeID missing entry for %q: %+v", p, cg.ExchangeID)
		}
		if cg.MarketName[p] == "" {
			t.Fatalf("default MarketName missing entry for %q: %+v", p, cg.MarketName)
		}
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

func TestApplyCatalogOverlayPopulatesPerPlatformFields(t *testing.T) {
	subs := []domain.SymbolSub{
		{Platform: "edgeX", Canonical: "BTC", APISymbol: "BTC-USDT", DisplaySymbol: "BTC-USDT (perp)"},
		{Platform: "lighter", Canonical: "ETH", APISymbol: "ETH", DisplaySymbol: "ETH-USDT (perp)"},
		{Platform: "mexc", Canonical: "SOL", APISymbol: "SOL_USDT", DisplaySymbol: "SOL-USDT (perp)"},
		{Platform: "binance", Canonical: "DOGE", APISymbol: "DOGEUSDT", DisplaySymbol: "DOGE-USDT (perp)"},
	}
	zero := 0
	cat := Catalog{
		Platforms: map[string]map[string]CatalogSymbol{
			"edgeX": {
				"BTC": {APISymbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", SettleAsset: "USDT", ContractID: "10000001", APILevelCap: 400, FrontendURL: "https://pro.edgex.exchange/trade/BTC-USDT"},
			},
			"lighter": {
				"ETH": {APISymbol: "ETH", BaseAsset: "ETH", QuoteAsset: "USDC", SettleAsset: "USDC", MarketID: &zero, APILevelCap: 0},
			},
			"mexc": {
				"SOL": {APISymbol: "SOL_USDT", BaseAsset: "SOL", QuoteAsset: "USDT", SettleAsset: "USDT", ContractSize: 0.1, APILevelCap: 2000},
			},
		},
	}
	applyCatalogOverlay(subs, cat)

	if got := subs[0]; got.ContractID != "10000001" || got.APILevelCap != 400 || got.FrontendURL == "" {
		t.Fatalf("edgeX overlay missed: %+v", got)
	}
	if got := subs[1]; got.MarketID == nil || *got.MarketID != 0 || got.QuoteAsset != "USDC" {
		t.Fatalf("lighter overlay missed: %+v", got)
	}
	if got := subs[2]; got.ContractSize != 0.1 || got.APILevelCap != 2000 {
		t.Fatalf("mexc overlay missed: %+v", got)
	}
	if got := subs[3]; got.ContractID != "" || got.APILevelCap != 0 {
		t.Fatalf("binance DOGE not in catalog should be untouched, got %+v", got)
	}
}

func TestLoadAppliesCatalogWhenPresent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    market_surface: perp
    instrument_kind: canonical
platforms: [edgeX]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  edgeX: https://example.invalid/edgex
`)
	mustWrite(t, filepath.Join(dir, "instrument_catalog.yaml"), `
schema_version: 1
generated_at: "2026-05-20T00:00:00Z"
generated_by: test
canonical_whitelist:
  - canonical: BTC
    market_surface: perp
    quote: USDT
    confidence: confirmed
platforms:
  edgeX:
    BTC:
      api_symbol: BTC-USDT
      base_asset: BTC
      quote_asset: USDT
      settle_asset: USDT
      api_level_cap: 400
      contract_id: "10000001"
      source_endpoint: https://example.invalid/edgex
      frontend_url: https://pro.edgex.exchange/trade/BTC-USDT
      url_verified: true
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(cfg.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(cfg.Symbols))
	}
	sub := cfg.Symbols[0]
	if sub.ContractID != "10000001" || sub.APILevelCap != 400 || sub.FrontendURL == "" || !sub.URLVerified {
		t.Fatalf("catalog overlay missing: %+v", sub)
	}
}

func TestLoadOKWhenCatalogMissing(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    market_surface: perp
    instrument_kind: canonical
platforms: [edgeX]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  edgeX: https://example.invalid/edgex
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(cfg.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(cfg.Symbols))
	}
	if got := cfg.Symbols[0]; got.ContractID != "" || got.APILevelCap != 0 {
		t.Fatalf("missing catalog should not populate per-platform fields, got %+v", got)
	}
}
