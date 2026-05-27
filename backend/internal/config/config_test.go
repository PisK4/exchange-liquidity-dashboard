package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/domain"
)

func TestLoadReadsDashboardMainConfigAndDatabase(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbols-dev.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    market_surface: perp
    instrument_kind: canonical
platforms: [binance]
`)
	mustWrite(t, filepath.Join(dir, "endpoints-dev.yaml"), `
endpoints:
  binance: https://example.invalid/binance-depth
`)
	mustWrite(t, filepath.Join(dir, "catalog-dev.yaml"), `
schema_version: 1
generated_at: "2026-05-26T00:00:00Z"
generated_by: test
platforms:
  binance:
    BTC:
      api_symbol: BTCUSDT
      base_asset: BTC
      quote_asset: USDT
      settle_asset: USDT
      source_endpoint: https://example.invalid/binance-depth
`)
	mustWrite(t, filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), `
Database:
  Name: edgex_dashboard
  Addr: mysql.dev:3306
  UserName: dashboard
  Password: secret
  ParseTime: true
  MaxIdleConn: 5
  MaxOpenConn: 12
  ConnMaxLifeTime: 45m
Alert:
  AppName: edgex-liquidity-dashboard
  Enabled: true
  WebHookP12: p12-hook
  WebHookP3: p3-hook
Runtime:
  collection_interval: 2m
  http_timeout: 9s
  exchange_proxy: http://proxy.dev:8080
Catalog:
  ExchangeEndpointsFile: endpoints-dev.yaml
  SymbolMappingFile: symbols-dev.yaml
  InstrumentCatalogFile: catalog-dev.yaml
  ListedUniverseFile: listed-dev.yaml
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.Name != "edgex_dashboard" || cfg.Database.Addr != "mysql.dev:3306" || cfg.Database.UserName != "dashboard" {
		t.Fatalf("database block not loaded: %+v", cfg.Database)
	}
	if cfg.MySQLDSN() != "dashboard:secret@tcp(mysql.dev:3306)/edgex_dashboard?parseTime=true" {
		t.Fatalf("MySQLDSN() = %q", cfg.MySQLDSN())
	}
	if !cfg.Alert.Enabled || cfg.Alert.AppName != "edgex-liquidity-dashboard" || cfg.Alert.WebHookP12 != "p12-hook" {
		t.Fatalf("alert block not loaded: %+v", cfg.Alert)
	}
	if cfg.Runtime.CollectionInterval != 2*time.Minute || cfg.Runtime.HTTPTimeout != 9*time.Second {
		t.Fatalf("runtime block not loaded: %+v", cfg.Runtime)
	}
	if len(cfg.Symbols) != 1 || cfg.Symbols[0].APISymbol != "BTCUSDT" {
		t.Fatalf("catalog file override not applied: %+v", cfg.Symbols)
	}
}

func TestLoadFallsBackToDotEnvWhenDashboardConfigMissing(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".env"), `
DASHBOARD_MYSQL_DSN=env_user:env_pass@tcp(mysql.env:3306)/env_dashboard?parseTime=true
COINGECKO_DEMO_API_KEY=demo-key-from-env-file
`)
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: ETH-USDT (perp)
    canonical: ETH
platforms: [binance]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  binance: https://example.invalid/binance
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MySQLDSN() != "env_user:env_pass@tcp(mysql.env:3306)/env_dashboard?parseTime=true" {
		t.Fatalf("MySQLDSN() = %q", cfg.MySQLDSN())
	}
	if got := os.Getenv("COINGECKO_DEMO_API_KEY"); got != "demo-key-from-env-file" {
		t.Fatalf("COINGECKO_DEMO_API_KEY = %q", got)
	}
}

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
	mustWrite(t, filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), `
collection_interval: 90s
display_fallback_window: 45m
depth_tiers: [0.001, 0.02]
slippage_buckets_usd: [25000, 75000]
volume_discounts:
  binance: 0.9
  hyperliquid: 1.0
ws_providers:
  bybit:
    enabled: true
    url: wss://example.invalid/bybit
    proxy: http://127.0.0.1:7897
    stale_after: 22s
collection:
  per_platform_concurrency: 7
  per_platform_rate_per_sec: 11
backfill:
  per_platform_concurrency: 2
  per_platform_rate_per_sec: 3
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
	if cfg.Runtime.Collection.PerPlatformConcurrency != 7 {
		t.Fatalf("collection concurrency = %d, want 7", cfg.Runtime.Collection.PerPlatformConcurrency)
	}
	if cfg.Runtime.Collection.PerPlatformRatePerSec != 11 {
		t.Fatalf("collection rate = %d, want 11", cfg.Runtime.Collection.PerPlatformRatePerSec)
	}
	if cfg.Runtime.Backfill.PerPlatformConcurrency != 2 || cfg.Runtime.Backfill.PerPlatformRatePerSec != 3 {
		t.Fatalf("backfill limits not loaded independently: %+v", cfg.Runtime.Backfill)
	}
	bybitWS := cfg.Runtime.WSProviders["bybit"]
	if !bybitWS.Enabled || bybitWS.URL != "wss://example.invalid/bybit" || bybitWS.Proxy != "http://127.0.0.1:7897" || bybitWS.StaleAfter != 22*time.Second {
		t.Fatalf("ws provider override not loaded: %+v", bybitWS)
	}
}

func TestLoadCollectionDefaultsIndependentFromBackfill(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
platforms: [binance]
`)
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), `
endpoints:
  binance: https://example.invalid/binance
`)
	mustWrite(t, filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), `
backfill:
  per_platform_concurrency: 9
  per_platform_rate_per_sec: 10
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Runtime.Collection.PerPlatformConcurrency != 3 {
		t.Fatalf("collection concurrency = %d, want default 3", cfg.Runtime.Collection.PerPlatformConcurrency)
	}
	if cfg.Runtime.Collection.PerPlatformRatePerSec != 4 {
		t.Fatalf("collection rate = %d, want default 4", cfg.Runtime.Collection.PerPlatformRatePerSec)
	}
	if cfg.Runtime.Backfill.PerPlatformConcurrency != 9 || cfg.Runtime.Backfill.PerPlatformRatePerSec != 10 {
		t.Fatalf("backfill overrides not applied: %+v", cfg.Runtime.Backfill)
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
	mustWrite(t, filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), `
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
	mustWrite(t, filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), `
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

func TestCommittedRuntimeAlignsCoinGeckoCadence(t *testing.T) {
	cfg, err := Load("../../../config")
	if err != nil {
		t.Fatalf("Load committed config: %v", err)
	}
	if cfg.Runtime.CoinGecko.PullInterval != cfg.Runtime.CollectionInterval {
		t.Fatalf("CoinGecko pull_interval = %s, want collection_interval %s", cfg.Runtime.CoinGecko.PullInterval, cfg.Runtime.CollectionInterval)
	}
	if cfg.Runtime.CoinGecko.PullInterval != 5*time.Minute {
		t.Fatalf("CoinGecko pull_interval = %s, want 5m", cfg.Runtime.CoinGecko.PullInterval)
	}
	if cfg.Runtime.CoinGecko.CacheTTL != 0 {
		t.Fatalf("CoinGecko cache_ttl = %s, want disabled cache", cfg.Runtime.CoinGecko.CacheTTL)
	}
	if cfg.Runtime.Collection.PerPlatformConcurrency <= 0 {
		t.Fatalf("collection per_platform_concurrency must be configured, got %d", cfg.Runtime.Collection.PerPlatformConcurrency)
	}
	if cfg.Runtime.Collection.PerPlatformRatePerSec <= 0 {
		t.Fatalf("collection per_platform_rate_per_sec must be configured, got %d", cfg.Runtime.Collection.PerPlatformRatePerSec)
	}
	raw, err := os.ReadFile("../../../config/edgex-liquidity-dashboard.yaml")
	if err != nil {
		t.Fatalf("read committed edgex-liquidity-dashboard.yaml: %v", err)
	}
	text := string(raw)
	for _, needle := range []string{"collection:", "per_platform_concurrency:", "per_platform_rate_per_sec:"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("edgex-liquidity-dashboard.yaml should explicitly declare %q", needle)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestExpandSymbolsAppliesCategoryAndDisplayName(t *testing.T) {
	subs := expandSymbols([]symbolYAML{
		{
			DisplaySymbol: "BTC-USDT (perp)",
			DisplayName:   "BTC-USD",
			Canonical:     "BTC",
			AssetCategory: "crypto",
			MarketSurface: "perp",
		},
		{
			DisplaySymbol: "GOLD-USDT (perp)",
			Canonical:     "GOLD",
			AssetCategory: "commodity",
		},
		{
			DisplaySymbol:  "TSLA-USDT (perp)",
			Canonical:      "TSLA",
			AssetCategory:  "stock",
			InstrumentKind: "synthetic",
			Lineage:        "bybit:TSLAUSDT@synthetic_perp",
		},
	}, []string{"binance", "bybit"}, map[string]string{
		"binance": "https://example.invalid/binance",
		"bybit":   "https://example.invalid/bybit",
	})

	if len(subs) != 6 {
		t.Fatalf("expected 6 subs (3 symbols × 2 platforms), got %d", len(subs))
	}
	for _, s := range subs {
		switch s.Canonical {
		case "BTC":
			if s.DisplayName != "BTC-USD" {
				t.Errorf("BTC DisplayName = %q, want BTC-USD", s.DisplayName)
			}
			if s.AssetCategory != domain.AssetCategoryCrypto {
				t.Errorf("BTC AssetCategory = %q, want crypto", s.AssetCategory)
			}
		case "GOLD":
			if s.DisplayName != "GOLD-USD" {
				t.Errorf("GOLD DisplayName = %q (default), want GOLD-USD", s.DisplayName)
			}
			if s.AssetCategory != domain.AssetCategoryCommodity {
				t.Errorf("GOLD AssetCategory = %q, want commodity", s.AssetCategory)
			}
		case "TSLA":
			if s.AssetCategory != domain.AssetCategoryStock {
				t.Errorf("TSLA AssetCategory = %q, want stock", s.AssetCategory)
			}
			if s.InstrumentKind != "synthetic" {
				t.Errorf("TSLA InstrumentKind = %q, want synthetic", s.InstrumentKind)
			}
			if s.Lineage != "bybit:TSLAUSDT@synthetic_perp" {
				t.Errorf("TSLA Lineage = %q", s.Lineage)
			}
		}
	}
}

func TestExpandSymbolsDefaultsCategoryToCrypto(t *testing.T) {
	subs := expandSymbols([]symbolYAML{{
		DisplaySymbol: "ETH-USDT (perp)",
		Canonical:     "ETH",
	}}, []string{"binance"}, map[string]string{})
	if len(subs) != 1 {
		t.Fatalf("expected 1 sub, got %d", len(subs))
	}
	if subs[0].AssetCategory != domain.AssetCategoryCrypto {
		t.Errorf("default AssetCategory = %q, want crypto", subs[0].AssetCategory)
	}
	if subs[0].DisplayName != "ETH-USD" {
		t.Errorf("default DisplayName = %q, want ETH-USD", subs[0].DisplayName)
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
	if len(cat.CanonicalWhitelist) < 3 {
		t.Fatalf("canonical_whitelist len = %d, want >= 3", len(cat.CanonicalWhitelist))
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

func TestApplyCatalogOverlayPropagatesMarketSurfaceAndLineage(t *testing.T) {
	subs := []domain.SymbolSub{
		{Platform: "bingx", Canonical: "SAMSUNG", DisplaySymbol: "SAMSUNG-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical"},
		{Platform: "binance", Canonical: "BTC", DisplaySymbol: "BTC-USDT (perp)", MarketSurface: "perp", InstrumentKind: "canonical"},
	}
	cat := Catalog{
		Platforms: map[string]map[string]CatalogSymbol{
			"bingx": {
				"SAMSUNG": {
					APISymbol:      "NCSKSAMSUNG2USD-USDT",
					BaseAsset:      "NCSKSAMSUNG2USD",
					QuoteAsset:     "USDT",
					MarketSurface:  "synthetic_futures",
					InstrumentKind: "synthetic",
					Lineage:        "bingx:NCSKSAMSUNG2USD@synthetic_futures",
				},
			},
			"binance": {
				"BTC": {APISymbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
			},
		},
	}
	applyCatalogOverlay(subs, cat)
	if subs[0].MarketSurface != "synthetic_futures" {
		t.Errorf("SAMSUNG MarketSurface = %q, want synthetic_futures", subs[0].MarketSurface)
	}
	if subs[0].InstrumentKind != "synthetic" {
		t.Errorf("SAMSUNG InstrumentKind = %q, want synthetic", subs[0].InstrumentKind)
	}
	if subs[0].Lineage != "bingx:NCSKSAMSUNG2USD@synthetic_futures" {
		t.Errorf("SAMSUNG Lineage = %q", subs[0].Lineage)
	}
	// Crypto entries without catalog override keep their expandSymbols default.
	if subs[1].MarketSurface != "perp" {
		t.Errorf("BTC MarketSurface = %q, want perp (catalog override empty)", subs[1].MarketSurface)
	}
	if subs[1].Lineage != "" {
		t.Errorf("BTC Lineage = %q, want empty", subs[1].Lineage)
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

func TestRuntimeStaleThresholdForReturnsCategoryDefaults(t *testing.T) {
	r := Runtime{}
	cases := []struct {
		category string
		want     time.Duration
	}{
		{"crypto", 30 * time.Second},
		{"commodity", 300 * time.Second},
		{"stock", 600 * time.Second},
		{"index_etf", 600 * time.Second},
		{"", 30 * time.Second}, // empty falls back to crypto
		{"unknown", 300 * time.Second},
	}
	for _, c := range cases {
		got := r.StaleThresholdFor(c.category)
		if got != c.want {
			t.Errorf("StaleThresholdFor(%q) = %v, want %v", c.category, got, c.want)
		}
	}
}

func TestRuntimeStaleThresholdForRespectsConfiguredOverride(t *testing.T) {
	r := Runtime{StalenessByCategory: map[string]time.Duration{
		"crypto":    45 * time.Second,
		"commodity": 10 * time.Minute,
	}}
	if got, want := r.StaleThresholdFor("crypto"), 45*time.Second; got != want {
		t.Errorf("crypto override = %v, want %v", got, want)
	}
	if got, want := r.StaleThresholdFor("commodity"), 10*time.Minute; got != want {
		t.Errorf("commodity override = %v, want %v", got, want)
	}
	// uncategorised must still fall back to baked-in defaults
	if got, want := r.StaleThresholdFor("stock"), 600*time.Second; got != want {
		t.Errorf("stock fallback = %v, want %v", got, want)
	}
}

func TestLoadListingAgentConfig(t *testing.T) {
	dir := t.TempDir()
	body := `
Database:
  ParseTime: true
Catalog:
  ExchangeEndpointsFile: exchange_endpoints.yaml
  SymbolMappingFile: symbol_mapping.yaml
  InstrumentCatalogFile: instrument_catalog.yaml
  ListedUniverseFile: listed_universe.yaml
Runtime:
  listing_agent:
    enabled: true
    worker:
      lease_ttl: 2m
      max_attempts: 5
      retry_backoff: [1m, 5m, 15m, 1h]
    sources:
      instrument_diff:
        enabled: true
        polls:
          - platform: binance
            market_type: usdm_futures
            poll_interval: 3m
      announcement:
        enabled: true
        polls:
          - platform: bybit
            poll_interval: 3m
    delivery:
      enabled: true
      top30_webhook_url: https://example.test/hook
      top30_webhook_url_env: LARK_LISTING_TOP30_WEBHOOK_URL
      top30_webhook_secret: secret
      dashboard_base_url: https://dashboard.example.test
    top30_push:
      enabled: true
      poll_interval: 5m
      stale_after: 15m
    candidate:
      merge_window: 14d
`
	if err := os.WriteFile(filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exchange_endpoints.yaml"), []byte("endpoints: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "symbol_mapping.yaml"), []byte("platforms: [edgeX]\nsymbols: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instrument_catalog.yaml"), []byte("schema_version: 1\nplatforms: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Runtime.ListingAgent.Enabled {
		t.Fatalf("listing_agent.enabled = false, want true")
	}
	if cfg.Runtime.ListingAgent.Worker.LeaseTTL != 2*time.Minute {
		t.Fatalf("lease ttl = %s", cfg.Runtime.ListingAgent.Worker.LeaseTTL)
	}
	if len(cfg.Runtime.ListingAgent.Worker.RetryBackoff) != 4 {
		t.Fatalf("retry backoff entries = %d", len(cfg.Runtime.ListingAgent.Worker.RetryBackoff))
	}
	if cfg.Runtime.ListingAgent.Worker.RetryBackoff[3] != time.Hour {
		t.Fatalf("retry backoff[3] = %s", cfg.Runtime.ListingAgent.Worker.RetryBackoff[3])
	}
	if got := cfg.Runtime.ListingAgent.Delivery.Top30WebhookURL; got != "https://example.test/hook" {
		t.Fatalf("top30 webhook url = %q", got)
	}
	if got := cfg.Runtime.ListingAgent.Delivery.Top30WebhookSecret; got != "secret" {
		t.Fatalf("top30 webhook secret = %q", got)
	}
	if got := cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL; got != "https://dashboard.example.test" {
		t.Fatalf("dashboard base url = %q", got)
	}
	if got := cfg.Runtime.ListingAgent.Top30Push.StaleAfter; got != 15*time.Minute {
		t.Fatalf("top30 stale_after = %s", got)
	}
	if got := cfg.Runtime.ListingAgent.Top30Push.PollInterval; got != 5*time.Minute {
		t.Fatalf("top30 poll_interval = %s", got)
	}
	if got := cfg.Runtime.ListingAgent.Candidate.MergeWindow; got != 14*24*time.Hour {
		t.Fatalf("merge_window = %s", got)
	}
	// instrument source roster overrides defaults: only binance is configured here.
	if len(cfg.Runtime.ListingAgent.Sources.InstrumentDiff.Polls) != 1 {
		t.Fatalf("instrument polls len = %d", len(cfg.Runtime.ListingAgent.Sources.InstrumentDiff.Polls))
	}
	if got := cfg.Runtime.ListingAgent.Sources.InstrumentDiff.Polls[0]; got.Platform != "binance" || got.MarketType != "usdm_futures" || got.PollInterval != 3*time.Minute || !got.Enabled {
		t.Fatalf("instrument poll[0] = %+v", got)
	}
	if len(cfg.Runtime.ListingAgent.Sources.Announcement.Polls) != 1 {
		t.Fatalf("announcement polls len = %d", len(cfg.Runtime.ListingAgent.Sources.Announcement.Polls))
	}
}

func TestDefaultListingAgentSeedsP1Sources(t *testing.T) {
	cfg := Default()
	la := cfg.Runtime.ListingAgent
	if !la.Enabled {
		t.Fatalf("default listing agent must be enabled")
	}
	if la.Worker.LeaseTTL != 2*time.Minute {
		t.Fatalf("default lease ttl = %s", la.Worker.LeaseTTL)
	}
	if la.Worker.MaxAttempts != 5 {
		t.Fatalf("default max_attempts = %d", la.Worker.MaxAttempts)
	}
	wantPlatforms := map[string]bool{"binance": false, "bybit": false, "okx": false, "bitget": false, "mexc": false, "hyperliquid": false}
	for _, p := range la.Sources.InstrumentDiff.Polls {
		if _, ok := wantPlatforms[p.Platform]; ok {
			wantPlatforms[p.Platform] = true
		}
	}
	for p, seen := range wantPlatforms {
		if !seen {
			t.Fatalf("default instrument source roster missing %s", p)
		}
	}
	if la.Delivery.Top30WebhookURLEnv != "LARK_LISTING_TOP30_WEBHOOK_URL" {
		t.Fatalf("default delivery env name = %q", la.Delivery.Top30WebhookURLEnv)
	}
	if la.Top30Push.StaleAfter != 15*time.Minute {
		t.Fatalf("default stale_after = %s", la.Top30Push.StaleAfter)
	}
	if la.Candidate.MergeWindow != 14*24*time.Hour {
		t.Fatalf("default merge_window = %s", la.Candidate.MergeWindow)
	}
}

func TestLoadAppliesStalenessAndCooldownOverrides(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), `
symbols:
  - display_symbol: BTC-USDT (perp)
    canonical: BTC
    market_surface: perp
    instrument_kind: canonical
platforms: [edgeX]
`)
	mustWrite(t, filepath.Join(dir, "edgex-liquidity-dashboard.yaml"), `
collection_interval: 5m
http_timeout: 5s
staleness_by_category:
  crypto: 60s
  commodity: 600s
cooldown_failure_threshold: 5
cooldown_duration: 10m
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got := cfg.Runtime.StaleThresholdFor("crypto"); got != 60*time.Second {
		t.Errorf("crypto threshold = %v, want 60s", got)
	}
	if got := cfg.Runtime.StaleThresholdFor("commodity"); got != 10*time.Minute {
		t.Errorf("commodity threshold = %v, want 10m", got)
	}
	// stock should still fall back to default 600s since not overridden
	if got := cfg.Runtime.StaleThresholdFor("stock"); got != 600*time.Second {
		t.Errorf("stock threshold = %v, want 600s default", got)
	}
	if got := cfg.Runtime.CooldownFailureThreshold; got != 5 {
		t.Errorf("cooldown threshold = %d, want 5", got)
	}
	if got := cfg.Runtime.CooldownDuration; got != 10*time.Minute {
		t.Errorf("cooldown duration = %v, want 10m", got)
	}
}
