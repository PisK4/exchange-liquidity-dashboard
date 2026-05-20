package config

import (
	"os"
	"path/filepath"
	"time"

	"edgex-dashboard/backend/internal/domain"
	"gopkg.in/yaml.v3"
)

type Runtime struct {
	CollectionInterval    time.Duration      `json:"collection_interval"`
	HTTPTimeout           time.Duration      `json:"http_timeout"`
	LighterWSURL          string             `json:"lighter_ws_url"`
	LighterStaleAfter     time.Duration      `json:"lighter_stale_after"`
	DisplayFallbackWindow time.Duration      `json:"display_fallback_window"`
	DepthTiers            []float64          `json:"depth_tiers"`
	SlippageBucketsUSD    []float64          `json:"slippage_buckets_usd"`
	VolumeDiscounts       map[string]float64 `json:"volume_discounts"`
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
	CollectionInterval    string             `yaml:"collection_interval"`
	HTTPTimeout           string             `yaml:"http_timeout"`
	LighterWSURL          string             `yaml:"lighter_ws_url"`
	LighterStaleAfter     string             `yaml:"lighter_stale_after"`
	DisplayFallbackWindow string             `yaml:"display_fallback_window"`
	DepthTiers            []float64          `yaml:"depth_tiers"`
	SlippageBucketsUSD    []float64          `yaml:"slippage_buckets_usd"`
	VolumeDiscounts       map[string]float64 `yaml:"volume_discounts"`
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
