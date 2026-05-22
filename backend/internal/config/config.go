package config

import (
	"os"
	"path/filepath"
	"time"

	"edgex-dashboard/backend/internal/domain"
	"gopkg.in/yaml.v3"
)

type Runtime struct {
	CollectionInterval    time.Duration               `json:"collection_interval"`
	HTTPTimeout           time.Duration               `json:"http_timeout"`
	ExchangeProxy         string                      `json:"exchange_proxy,omitempty"`
	LighterWSURL          string                      `json:"lighter_ws_url"`
	LighterStaleAfter     time.Duration               `json:"lighter_stale_after"`
	DisplayFallbackWindow time.Duration               `json:"display_fallback_window"`
	DepthTiers            []float64                   `json:"depth_tiers"`
	SlippageBucketsUSD    []float64                   `json:"slippage_buckets_usd"`
	VolumeDiscounts       map[string]float64          `json:"volume_discounts"`
	CoinGecko             CoinGeckoConfig             `json:"coingecko"`
	WSProviders           map[string]WSProviderConfig `json:"ws_providers,omitempty"`
	Backfill              BackfillConfig              `json:"backfill"`
}

// BackfillConfig drives the Top30 daily kline backfill. Defaults are tuned
// to be safe in any free-tier exchange budget: ColdStartDays=14 means a
// brand-new deployment hydrates the 7d Vol / 7d Δ window (with one extra
// day of slack) on first boot; DailyRepairDays=3 keeps the rolling
// re-pull cheap; PerPlatformConcurrency=3 + PerPlatformRatePerSec=4 keeps
// the request fan-out below known free-tier ceilings for binance / okx /
// bybit / mexc / gate. ScheduleUTCHour=2 fires the daily run after the
// CoinGecko 01:00 backfill to let the higher-priority CG row land first.
type BackfillConfig struct {
	Enabled                bool `json:"enabled"`
	ColdStartDays          int  `json:"cold_start_days"`
	DailyRepairDays        int  `json:"daily_repair_days"`
	PerPlatformConcurrency int  `json:"per_platform_concurrency"`
	PerPlatformRatePerSec  int  `json:"per_platform_rate_per_sec"`
	ScheduleUTCHour        int  `json:"schedule_utc_hour"`
	ScheduleUTCMinute      int  `json:"schedule_utc_minute"`
}

// CoinGeckoConfig controls the CoinGecko derivatives ingestion path.
//
// Proxy is read as a literal URL string from runtime.yaml; the CoinGecko
// client builds its own *http.Transport from this URL. Process-level
// HTTPS_PROXY / HTTP_PROXY env vars are intentionally NOT consulted, so that
// turning the CoinGecko proxy on never silently routes the other 9 native
// exchange adapters through 127.0.0.1.
type CoinGeckoConfig struct {
	Enabled        bool              `json:"enabled"`
	BaseURL        string            `json:"base_url"`
	APIKeyEnv      string            `json:"api_key_env"`
	Proxy          string            `json:"proxy,omitempty"`
	PullInterval   time.Duration     `json:"pull_interval"`
	CacheTTL       time.Duration     `json:"cache_ttl"`
	RequestTimeout time.Duration     `json:"request_timeout"`
	ExchangeID     map[string]string `json:"exchange_id,omitempty"`
	MarketName     map[string]string `json:"market_name,omitempty"`
}

type WSProviderConfig struct {
	Enabled    bool          `json:"enabled"`
	URL        string        `json:"url"`
	Proxy      string        `json:"proxy,omitempty"`
	StaleAfter time.Duration `json:"stale_after"`
}

type Config struct {
	Symbols   []domain.SymbolSub `json:"symbols"`
	Platforms []string           `json:"platforms"`
	Runtime   Runtime            `json:"runtime"`
}

func Load(configDir string) (Config, error) {
	if configDir == "" {
		configDir = filepath.Join("..", "config")
	}
	cfg := Default()

	endpoints, err := loadEndpoints(filepath.Join(configDir, "exchange_endpoints.yaml"))
	if err != nil {
		return Config{}, err
	}
	if len(endpoints) == 0 {
		endpoints = defaultEndpoints()
	}

	if symbols, platforms, err := loadSymbols(filepath.Join(configDir, "symbol_mapping.yaml")); err != nil {
		return Config{}, err
	} else if len(symbols) > 0 && len(platforms) > 0 {
		cfg.Platforms = platforms
		cfg.Symbols = expandSymbols(symbols, platforms, endpoints)
	}

	runtimeCfg, err := loadRuntime(filepath.Join(configDir, "runtime.yaml"), cfg.Runtime)
	if err != nil {
		return Config{}, err
	}
	cfg.Runtime = runtimeCfg

	cat, err := LoadCatalog(filepath.Join(configDir, "instrument_catalog.yaml"))
	if err != nil {
		return Config{}, err
	}
	if len(cat.Platforms) > 0 {
		applyCatalogOverlay(cfg.Symbols, cat)
	}
	return cfg, nil
}

// applyCatalogOverlay populates per-(platform, canonical) catalog fields onto
// each SymbolSub in place. Entries missing from catalog are silently left as
// produced by expandSymbols / Default — the consuming adapter will error at
// fetch time if a required field (contract_id, market_id, contract_size,
// quanto_multiplier) is missing.
func applyCatalogOverlay(subs []domain.SymbolSub, cat Catalog) {
	if len(cat.Platforms) == 0 {
		return
	}
	for i := range subs {
		platform := cat.Platforms[subs[i].Platform]
		if platform == nil {
			continue
		}
		entry, ok := platform[subs[i].Canonical]
		if !ok {
			continue
		}
		if entry.APISymbol != "" {
			subs[i].APISymbol = entry.APISymbol
		}
		if entry.BaseAsset != "" {
			subs[i].BaseAsset = entry.BaseAsset
		}
		if entry.QuoteAsset != "" {
			subs[i].QuoteAsset = entry.QuoteAsset
		}
		if entry.SettleAsset != "" {
			subs[i].SettleAsset = entry.SettleAsset
		}
		if entry.SourceEndpoint != "" {
			subs[i].SourceEndpoint = entry.SourceEndpoint
		}
		subs[i].ContractID = entry.ContractID
		subs[i].MarketID = entry.MarketID
		subs[i].ContractSize = entry.ContractSize
		subs[i].QuantoMultiplier = entry.QuantoMultiplier
		subs[i].APILevelCap = entry.APILevelCap
		subs[i].FrontendURL = entry.FrontendURL
		subs[i].URLVerified = entry.URLVerified
		subs[i].CatalogStatus = entry.CatalogStatus
	}
}

// Default returns a minimal Config seed with platforms/symbols listed but no
// per-platform-specific fields (api_symbol, contract_id, market_id, ...).
// Those are populated by applyCatalogOverlay from config/instrument_catalog.yaml.
// Tests that only need a SymbolSub skeleton can use Default(); production
// callers always go through Load() which requires the catalog yaml.
func Default() Config {
	platforms := []string{"edgeX", "binance", "okx", "bybit", "bitget", "bingx", "mexc", "gate", "hyperliquid", "lighter"}
	baseSymbols := []struct{ canonical, api string }{
		{"BTC", "BTC-USDT"},
		{"ETH", "ETH-USDT"},
		{"SOL", "SOL-USDT"},
	}

	var subs []domain.SymbolSub
	for _, s := range baseSymbols {
		for _, p := range platforms {
			subs = append(subs, domain.SymbolSub{
				DisplaySymbol:  s.api + " (perp)",
				Canonical:      s.canonical,
				MarketSurface:  "perp",
				InstrumentKind: "canonical",
				Platform:       p,
				BaseAsset:      s.canonical,
				QuoteAsset:     "USDT",
				SettleAsset:    "USDT",
				SourceEndpoint: endpointFor(p),
			})
		}
	}

	return Config{
		Symbols:   subs,
		Platforms: platforms,
		Runtime: Runtime{
			CollectionInterval:    5 * time.Minute,
			HTTPTimeout:           5 * time.Second,
			LighterWSURL:          "wss://mainnet.zklighter.elliot.ai/stream?readonly=true",
			LighterStaleAfter:     15 * time.Second,
			DisplayFallbackWindow: 30 * time.Minute,
			DepthTiers:            []float64{0.0005, 0.001, 0.01, 0.02},
			SlippageBucketsUSD:    []float64{50_000, 100_000, 500_000, 1_000_000},
			VolumeDiscounts:       map[string]float64{"mexc": 0.4, "gate": 0.5},
			CoinGecko:             defaultCoinGeckoConfig(),
			WSProviders:           defaultWSProviderConfig(),
			Backfill:              defaultBackfillConfig(),
		},
	}
}

// defaultBackfillConfig keeps Top30 backfill enabled by default; an
// operator who explicitly wants to disable it can set
// `backfill.enabled: false` in runtime.yaml.
func defaultBackfillConfig() BackfillConfig {
	return BackfillConfig{
		Enabled:                true,
		ColdStartDays:          14,
		DailyRepairDays:        3,
		PerPlatformConcurrency: 3,
		PerPlatformRatePerSec:  4,
		ScheduleUTCHour:        2,
		ScheduleUTCMinute:      30,
	}
}

func defaultWSProviderConfig() map[string]WSProviderConfig {
	return map[string]WSProviderConfig{
		"bitget": {
			Enabled:    true,
			URL:        "wss://stream.bitget.com/public/v1/stream",
			StaleAfter: 15 * time.Second,
		},
		"mexc": {
			Enabled:    true,
			URL:        "wss://futures.mexc.com/edge?Trans-Protocol=JSON",
			StaleAfter: 15 * time.Second,
		},
		"okx": {
			Enabled:    true,
			URL:        "wss://wspri.okx.com:8443/ws/v5/ipublic",
			StaleAfter: 15 * time.Second,
		},
		"bybit": {
			Enabled:    false,
			URL:        "wss://ws2.bybit.com/realtime_w",
			StaleAfter: 15 * time.Second,
		},
		"bingx": {
			Enabled:    false,
			URL:        "wss://open-api-swap.bingx.com/swap-market",
			StaleAfter: 15 * time.Second,
		},
	}
}

func defaultCoinGeckoConfig() CoinGeckoConfig {
	return CoinGeckoConfig{
		Enabled:        false,
		BaseURL:        "https://api.coingecko.com/api/v3",
		APIKeyEnv:      "COINGECKO_DEMO_API_KEY",
		Proxy:          "",
		PullInterval:   15 * time.Minute,
		CacheTTL:       5 * time.Minute,
		RequestTimeout: 30 * time.Second,
		ExchangeID: map[string]string{
			"binance":     "binance_futures",
			"okx":         "okex_swap",
			"bybit":       "bybit",
			"bitget":      "bitget_futures",
			"bingx":       "bingx_futures",
			"mexc":        "mxc_futures",
			"gate":        "gate_futures",
			"hyperliquid": "hyperliquid",
			"lighter":     "lighter",
		},
		MarketName: map[string]string{
			"binance":     "Binance (Futures)",
			"okx":         "OKX (Futures)",
			"bybit":       "Bybit (Futures)",
			"bitget":      "Bitget Futures",
			"bingx":       "BingX (Futures)",
			"mexc":        "MEXC (Futures)",
			"gate":        "Gate (Futures)",
			"hyperliquid": "Hyperliquid (Futures)",
			"lighter":     "Lighter",
		},
	}
}

type symbolYAML struct {
	DisplaySymbol  string `yaml:"display_symbol"`
	Canonical      string `yaml:"canonical"`
	MarketSurface  string `yaml:"market_surface"`
	InstrumentKind string `yaml:"instrument_kind"`
	BaseAsset      string `yaml:"base_asset"`
	QuoteAsset     string `yaml:"quote_asset"`
	SettleAsset    string `yaml:"settle_asset"`
}

type symbolFile struct {
	Symbols   []symbolYAML `yaml:"symbols"`
	Platforms []string     `yaml:"platforms"`
}

type endpointFile struct {
	Endpoints map[string]string `yaml:"endpoints"`
}

type runtimeFile struct {
	CollectionInterval    string                    `yaml:"collection_interval"`
	HTTPTimeout           string                    `yaml:"http_timeout"`
	ExchangeProxy         string                    `yaml:"exchange_proxy"`
	LighterWSURL          string                    `yaml:"lighter_ws_url"`
	LighterStaleAfter     string                    `yaml:"lighter_stale_after"`
	DisplayFallbackWindow string                    `yaml:"display_fallback_window"`
	DepthTiers            []float64                 `yaml:"depth_tiers"`
	SlippageBucketsUSD    []float64                 `yaml:"slippage_buckets_usd"`
	VolumeDiscounts       map[string]float64        `yaml:"volume_discounts"`
	CoinGecko             *coinGeckoFile            `yaml:"coingecko"`
	WSProviders           map[string]wsProviderFile `yaml:"ws_providers"`
	Backfill              *backfillFile             `yaml:"backfill"`
}

type coinGeckoFile struct {
	Enabled        *bool             `yaml:"enabled"`
	BaseURL        string            `yaml:"base_url"`
	APIKeyEnv      string            `yaml:"api_key_env"`
	Proxy          string            `yaml:"proxy"`
	PullInterval   string            `yaml:"pull_interval"`
	CacheTTL       string            `yaml:"cache_ttl"`
	RequestTimeout string            `yaml:"request_timeout"`
	ExchangeID     map[string]string `yaml:"exchange_id"`
	MarketName     map[string]string `yaml:"market_name"`
}

type wsProviderFile struct {
	Enabled    *bool  `yaml:"enabled"`
	URL        string `yaml:"url"`
	Proxy      string `yaml:"proxy"`
	StaleAfter string `yaml:"stale_after"`
}

type backfillFile struct {
	Enabled                *bool `yaml:"enabled"`
	ColdStartDays          *int  `yaml:"cold_start_days"`
	DailyRepairDays        *int  `yaml:"daily_repair_days"`
	PerPlatformConcurrency *int  `yaml:"per_platform_concurrency"`
	PerPlatformRatePerSec  *int  `yaml:"per_platform_rate_per_sec"`
	ScheduleUTCHour        *int  `yaml:"schedule_utc_hour"`
	ScheduleUTCMinute      *int  `yaml:"schedule_utc_minute"`
}

// Catalog mirrors instrument_catalog.yaml. It is the per-(platform, canonical)
// resolution layer: how each platform names a canonical symbol, what extra
// per-platform fields (contract_id / market_id / contract_size /
// quanto_multiplier) are needed to call its public endpoints, and which
// front-end URL a human reviewer can click to spot-check that the trading
// pair really exists. Generated by backend/scripts/build-catalog (Step 3);
// frontend_url and url_verified are human-editable after click-through.
type Catalog struct {
	SchemaVersion      int                                 `yaml:"schema_version" json:"schema_version"`
	GeneratedAt        string                              `yaml:"generated_at" json:"generated_at"`
	GeneratedBy        string                              `yaml:"generated_by" json:"generated_by"`
	CanonicalWhitelist []CatalogWhitelistEntry             `yaml:"canonical_whitelist" json:"canonical_whitelist"`
	Platforms          map[string]map[string]CatalogSymbol `yaml:"platforms" json:"platforms"`
}

type CatalogWhitelistEntry struct {
	Canonical     string `yaml:"canonical" json:"canonical"`
	MarketSurface string `yaml:"market_surface" json:"market_surface"`
	Quote         string `yaml:"quote" json:"quote"`
	Confidence    string `yaml:"confidence" json:"confidence"`
}

type CatalogSymbol struct {
	APISymbol        string  `yaml:"api_symbol" json:"api_symbol"`
	BaseAsset        string  `yaml:"base_asset" json:"base_asset"`
	QuoteAsset       string  `yaml:"quote_asset" json:"quote_asset"`
	SettleAsset      string  `yaml:"settle_asset" json:"settle_asset"`
	APILevelCap      int     `yaml:"api_level_cap" json:"api_level_cap"`
	ContractID       string  `yaml:"contract_id,omitempty" json:"contract_id,omitempty"`
	MarketID         *int    `yaml:"market_id,omitempty" json:"market_id,omitempty"`
	ContractSize     float64 `yaml:"contract_size,omitempty" json:"contract_size,omitempty"`
	QuantoMultiplier float64 `yaml:"quanto_multiplier,omitempty" json:"quanto_multiplier,omitempty"`
	SourceEndpoint   string  `yaml:"source_endpoint" json:"source_endpoint"`
	CatalogStatus    string  `yaml:"catalog_status,omitempty" json:"catalog_status,omitempty"`
	FrontendURL      string  `yaml:"frontend_url,omitempty" json:"frontend_url,omitempty"`
	URLVerified      bool    `yaml:"url_verified" json:"url_verified"`
}

// LoadCatalog parses instrument_catalog.yaml. Missing file returns an empty
// catalog without error so existing call sites keep working. This is the only
// surface added in Step 1; Load() does not consume it yet.
func LoadCatalog(path string) (Catalog, error) {
	var cat Catalog
	if err := readYAML(path, &cat); err != nil {
		return Catalog{}, err
	}
	return cat, nil
}

func loadSymbols(path string) ([]symbolYAML, []string, error) {
	var file symbolFile
	if err := readYAML(path, &file); err != nil {
		return nil, nil, err
	}
	return file.Symbols, file.Platforms, nil
}

func loadEndpoints(path string) (map[string]string, error) {
	var file endpointFile
	if err := readYAML(path, &file); err != nil {
		return nil, err
	}
	return file.Endpoints, nil
}

func loadRuntime(path string, base Runtime) (Runtime, error) {
	var file runtimeFile
	if err := readYAML(path, &file); err != nil {
		return Runtime{}, err
	}
	if file.CollectionInterval != "" {
		duration, err := time.ParseDuration(file.CollectionInterval)
		if err != nil {
			return Runtime{}, err
		}
		base.CollectionInterval = duration
	}
	if file.HTTPTimeout != "" {
		duration, err := time.ParseDuration(file.HTTPTimeout)
		if err != nil {
			return Runtime{}, err
		}
		base.HTTPTimeout = duration
	}
	if file.ExchangeProxy != "" {
		base.ExchangeProxy = file.ExchangeProxy
	}
	if file.LighterWSURL != "" {
		base.LighterWSURL = file.LighterWSURL
	}
	if file.LighterStaleAfter != "" {
		duration, err := time.ParseDuration(file.LighterStaleAfter)
		if err != nil {
			return Runtime{}, err
		}
		base.LighterStaleAfter = duration
	}
	if file.DisplayFallbackWindow != "" {
		duration, err := time.ParseDuration(file.DisplayFallbackWindow)
		if err != nil {
			return Runtime{}, err
		}
		base.DisplayFallbackWindow = duration
	}
	if len(file.DepthTiers) > 0 {
		base.DepthTiers = file.DepthTiers
	}
	if len(file.SlippageBucketsUSD) > 0 {
		base.SlippageBucketsUSD = file.SlippageBucketsUSD
	}
	if len(file.VolumeDiscounts) > 0 {
		base.VolumeDiscounts = file.VolumeDiscounts
	}
	if file.CoinGecko != nil {
		cg, err := applyCoinGeckoFile(base.CoinGecko, *file.CoinGecko)
		if err != nil {
			return Runtime{}, err
		}
		base.CoinGecko = cg
	}
	if len(file.WSProviders) > 0 {
		providers, err := applyWSProviderFile(base.WSProviders, file.WSProviders)
		if err != nil {
			return Runtime{}, err
		}
		base.WSProviders = providers
	}
	if file.Backfill != nil {
		base.Backfill = applyBackfillFile(base.Backfill, *file.Backfill)
	}
	return base, nil
}

// applyBackfillFile overrides any non-nil field from the yaml on top of
// the defaults so partial yaml stanzas (e.g. just `enabled: false`) work.
func applyBackfillFile(base BackfillConfig, file backfillFile) BackfillConfig {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.ColdStartDays != nil {
		base.ColdStartDays = *file.ColdStartDays
	}
	if file.DailyRepairDays != nil {
		base.DailyRepairDays = *file.DailyRepairDays
	}
	if file.PerPlatformConcurrency != nil {
		base.PerPlatformConcurrency = *file.PerPlatformConcurrency
	}
	if file.PerPlatformRatePerSec != nil {
		base.PerPlatformRatePerSec = *file.PerPlatformRatePerSec
	}
	if file.ScheduleUTCHour != nil {
		base.ScheduleUTCHour = *file.ScheduleUTCHour
	}
	if file.ScheduleUTCMinute != nil {
		base.ScheduleUTCMinute = *file.ScheduleUTCMinute
	}
	return base
}

func applyWSProviderFile(base map[string]WSProviderConfig, file map[string]wsProviderFile) (map[string]WSProviderConfig, error) {
	out := make(map[string]WSProviderConfig, len(base)+len(file))
	for k, v := range base {
		out[k] = v
	}
	for platform, entry := range file {
		cfg := out[platform]
		if entry.Enabled != nil {
			cfg.Enabled = *entry.Enabled
		}
		if entry.URL != "" {
			cfg.URL = entry.URL
		}
		if entry.Proxy != "" {
			cfg.Proxy = entry.Proxy
		}
		if entry.StaleAfter != "" {
			d, err := time.ParseDuration(entry.StaleAfter)
			if err != nil {
				return nil, err
			}
			cfg.StaleAfter = d
		}
		out[platform] = cfg
	}
	return out, nil
}

func applyCoinGeckoFile(base CoinGeckoConfig, file coinGeckoFile) (CoinGeckoConfig, error) {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.BaseURL != "" {
		base.BaseURL = file.BaseURL
	}
	if file.APIKeyEnv != "" {
		base.APIKeyEnv = file.APIKeyEnv
	}
	if file.Proxy != "" {
		base.Proxy = file.Proxy
	}
	if file.PullInterval != "" {
		d, err := time.ParseDuration(file.PullInterval)
		if err != nil {
			return CoinGeckoConfig{}, err
		}
		base.PullInterval = d
	}
	if file.CacheTTL != "" {
		d, err := time.ParseDuration(file.CacheTTL)
		if err != nil {
			return CoinGeckoConfig{}, err
		}
		base.CacheTTL = d
	}
	if file.RequestTimeout != "" {
		d, err := time.ParseDuration(file.RequestTimeout)
		if err != nil {
			return CoinGeckoConfig{}, err
		}
		base.RequestTimeout = d
	}
	if len(file.ExchangeID) > 0 {
		base.ExchangeID = file.ExchangeID
	}
	if len(file.MarketName) > 0 {
		base.MarketName = file.MarketName
	}
	return base, nil
}

func readYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}

func expandSymbols(symbols []symbolYAML, platforms []string, endpoints map[string]string) []domain.SymbolSub {
	subs := make([]domain.SymbolSub, 0, len(symbols)*len(platforms))
	for _, symbol := range symbols {
		base := symbol.BaseAsset
		if base == "" {
			base = symbol.Canonical
		}
		quote := symbol.QuoteAsset
		if quote == "" {
			quote = "USDT"
		}
		settle := symbol.SettleAsset
		if settle == "" {
			settle = "USDT"
		}
		for _, platform := range platforms {
			subs = append(subs, domain.SymbolSub{
				DisplaySymbol:  symbol.DisplaySymbol,
				Canonical:      symbol.Canonical,
				MarketSurface:  valueOr(symbol.MarketSurface, "perp"),
				InstrumentKind: valueOr(symbol.InstrumentKind, "canonical"),
				Platform:       platform,
				BaseAsset:      base,
				QuoteAsset:     quote,
				SettleAsset:    settle,
				SourceEndpoint: endpoints[platform],
			})
		}
	}
	return subs
}

func defaultEndpoints() map[string]string {
	out := make(map[string]string)
	for _, platform := range Default().Platforms {
		out[platform] = endpointFor(platform)
	}
	return out
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func endpointFor(platform string) string {
	switch platform {
	case "edgeX":
		return "https://pro.edgex.exchange"
	case "binance":
		return "https://fapi.binance.com/fapi/v1/depth"
	case "okx":
		return "https://www.okx.com/api/v5/market/books"
	case "bybit":
		return "https://api.bybit.com/v5/market/orderbook"
	case "bitget":
		return "https://api.bitget.com/api/v2/mix/market/orderbook"
	case "bingx":
		return "https://open-api.bingx.com/openApi/swap/v2/quote/depth"
	case "mexc":
		return "https://contract.mexc.com/api/v1/contract/depth"
	case "gate":
		return "https://api.gateio.ws/api/v4/futures/usdt/order_book"
	case "hyperliquid":
		return "https://api.hyperliquid.xyz/info"
	case "lighter":
		return "https://mainnet.zklighter.elliot.ai/api/v1/orderBooks"
	default:
		return ""
	}
}
