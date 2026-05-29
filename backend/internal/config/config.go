package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	Collection            CollectionConfig            `json:"collection"`
	Backfill              BackfillConfig              `json:"backfill"`
	// StalenessByCategory configures per-asset_category snapshot freshness
	// thresholds. Crypto markets trade 24/7 so a 30s threshold is
	// reasonable; commodity / stock / index_etf surfaces are exposed via
	// synthetic perpetuals whose tick rate is dominated by the
	// underlying market hours, so much wider thresholds avoid false
	// "stale" flags during after-hours.
	StalenessByCategory map[string]time.Duration `json:"staleness_by_category,omitempty"`
	// CooldownFailureThreshold is the number of consecutive failures for a
	// (platform, canonical) pair before the collector pauses collecting
	// it. CooldownDuration is the pause length. Both default to safe
	// values (3 / 5m) and can be overridden in edgex-liquidity-dashboard.yaml.
	CooldownFailureThreshold int           `json:"cooldown_failure_threshold,omitempty"`
	CooldownDuration         time.Duration `json:"cooldown_duration,omitempty"`
	// Top30Divergence configures the CEX-vs-DEX comparison view inside
	// the Top30 tab. CEXPlatforms / DEXPlatforms partition the universe
	// of Top30-producing platforms into two venue classes; any platform
	// not listed in either set is ignored by the divergence aggregator.
	// SignificantRankDelta is the |Δrank| threshold above which a symbol
	// is tagged cex_heavy / dex_heavy instead of aligned.
	Top30Divergence Top30DivergenceConfig `json:"top30_divergence"`
	// ListingAgent is the Listing Agent P1 configuration block. It owns
	// per-source poll intervals, candidate fusion knobs, business score
	// thresholds, the Top30 hot-gap push producer cadence, and the
	// shared delivery outbox webhook target. Listing-only fields live
	// here so the legacy collector can continue to ignore them.
	ListingAgent ListingAgentConfig `json:"listing_agent"`
}

// ListingAgentConfig is the runtime root for the Listing Agent P1
// backend detection main link. Enabled defaults to true; individual
// sources / delivery / Top30 push subsystems can still be toggled
// independently. See architecture/方案设计/EdgeX运营/Listing/
// 2026-05-27-Listing-Agent-P1-主链路方案设计.md §16 and §23 for the
// authoritative knobs.
type ListingAgentConfig struct {
	Enabled             bool                      `json:"enabled"`
	Worker              ListingWorkerConfig       `json:"worker"`
	Sources             ListingSourcesConfig      `json:"sources"`
	Delivery            ListingDeliveryConfig     `json:"delivery"`
	Top30Push           ListingTop30PushConfig    `json:"top30_push"`
	Top30DivergencePush Top30DivergencePushConfig `json:"top30_divergence_push"`
	LiquidityAlert      LiquidityAlertConfig      `json:"liquidity_alert"`
	Candidate           ListingCandidateConfig    `json:"candidate"`
}

// LiquidityAlertConfig is the Dashboard liquidity-lag (#10) /
// worst-depth (#11) alert tuning block. See
// architecture/方案设计/EdgeX运营/Listing/
// 2026-05-29-Listing-Agent-Dashboard-Liquidity-Alerts-#10-#11.md §5
// for the business semantics and
// docs/feat/listing-agent-liquidity-alert.md §8 for runbook context.
//
// Defaults are conservative: Enabled=false so the SG environment
// has to opt in explicitly after observing one cycle of dry-run
// outbox rows.
type LiquidityAlertConfig struct {
	Enabled bool `json:"enabled"`
	// DepthTierPct selects which `t_orderbook_snapshot.tier` row
	// the alerts read. Default 0.001 (0.1%). Valid alternatives in
	// V1: 0.0005 / 0.01 / 0.02.
	DepthTierPct float64 `json:"depth_tier_pct"`
	// LagThreshold is the median multiplier below which #10 fires.
	// 0.5 == "edgeX < 50% of competitor median". Range (0, 1).
	LagThreshold float64 `json:"lag_threshold"`
	// MinComparators is the minimum count of non-edgeX platforms
	// that must report a fresh, successful depth snapshot before
	// either alert can fire. PRD §3.7 specifies <3 must not trigger.
	MinComparators int `json:"min_comparators"`
	// ReissueInterval is the cooldown between repeat pushes while
	// an alert remains active. Default 6h.
	ReissueInterval time.Duration `json:"reissue_interval"`
	// ClearConsecutive is the number of evaluation rounds in a row
	// where the trigger condition must be false before the alert
	// state flips from active → cleared. With PollInterval=5m and
	// ClearConsecutive=3 the effective hysteresis is 15 minutes.
	ClearConsecutive int `json:"clear_consecutive"`
	// StaleAfter bounds the freshness of a single platform's depth
	// snapshot. Snapshots older than this are excluded from the
	// median and rank computation (not zeroed out).
	StaleAfter time.Duration `json:"stale_after"`
	// PollInterval is the engine cadence for re-evaluating alerts.
	// Should be ≥ the depth collector cadence (5m in V1).
	PollInterval time.Duration `json:"poll_interval"`
	// MaxPerTick caps the number of new outbox rows the producer
	// emits per tick. Defense against the "100 canonicals trigger
	// at once after a deploy" burst.
	MaxPerTick int `json:"max_per_tick"`
	// SendSpacing staggers NextAttemptAt across rows written in
	// the same producer pass.
	SendSpacing time.Duration `json:"send_spacing"`
}

// Top30DivergencePushConfig controls the CEX/DEX divergence Lark
// alert cards (#2-#5). One UTC-day card per category at most; the
// engine ticks at the shared ListingAgent cadence. Knobs that overlap
// with the existing Top30 hot-gap push (max_attempts, max_per_tick)
// are reused from top30_push so operators see a single set of dials.
//
// TopNPerCard caps how many rows each card lists; default 10. Empty
// categories are skipped (no empty cards). StaleAfter mirrors
// top30_push.stale_after default (15m). SendSpacing staggers
// NextAttemptAt across the four category cards written in one tick,
// matching top30_push's burst-control story.
type Top30DivergencePushConfig struct {
	Enabled     bool          `json:"enabled"`
	TopNPerCard int           `json:"top_n_per_card"`
	StaleAfter  time.Duration `json:"stale_after"`
	SendSpacing time.Duration `json:"send_spacing"`
}

// ListingWorkerConfig controls the per-source worker lease and the
// delivery retry budget. LeaseTTL bounds how long a single instance can
// hold a source lease; RetryBackoff is the sequence of delays between
// outbox attempts (one entry per follow-up attempt).
type ListingWorkerConfig struct {
	LeaseTTL     time.Duration   `json:"lease_ttl"`
	MaxAttempts  int             `json:"max_attempts"`
	RetryBackoff []time.Duration `json:"retry_backoff"`
}

// ListingSourcesConfig groups the two real source subsystems active in
// P1a. Top30 is intentionally consumed as enrichment, not as a source,
// so it has no entry here.
type ListingSourcesConfig struct {
	InstrumentDiff ListingInstrumentDiffConfig `json:"instrument_diff"`
	Announcement   ListingAnnouncementConfig   `json:"announcement"`
}

// ListingInstrumentDiffConfig controls the per-platform instrument
// pollers (Binance USD-M, Bybit linear, OKX SWAP, Bitget USDT-FUTURES,
// MEXC contract, Hyperliquid perp).
type ListingInstrumentDiffConfig struct {
	Enabled bool                      `json:"enabled"`
	Polls   []ListingSourcePollConfig `json:"polls"`
}

// ListingAnnouncementConfig controls the announcement pollers
// (Bybit, Bitget, Binance CMS).
type ListingAnnouncementConfig struct {
	Enabled bool                      `json:"enabled"`
	Polls   []ListingSourcePollConfig `json:"polls"`
}

// ListingSourcePollConfig is the per-source poll declaration. Enabled
// defaults to true at parse time so partial overrides do not silently
// disable a source.
type ListingSourcePollConfig struct {
	Platform     string        `json:"platform"`
	MarketType   string        `json:"market_type,omitempty"`
	PollInterval time.Duration `json:"poll_interval"`
	Enabled      bool          `json:"enabled"`
}

// ListingDeliveryConfig configures the shared delivery outbox worker.
// Top30WebhookURL / Top30WebhookURLEnv resolve at startup:
//   - if Top30WebhookURL is set, it wins;
//   - otherwise the environment variable named by Top30WebhookURLEnv is
//     resolved at engine start time.
//
// The webhook URL is never persisted to MySQL or printed in logs (see
// repo CLAUDE.md §coding_guidelines). Top30WebhookSecret is forwarded
// to the Lark sign helper when non-empty.
type ListingDeliveryConfig struct {
	Enabled            bool   `json:"enabled"`
	Top30WebhookURL    string `json:"top30_webhook_url,omitempty"`
	Top30WebhookURLEnv string `json:"top30_webhook_url_env,omitempty"`
	Top30WebhookSecret string `json:"-"`
	DashboardBaseURL   string `json:"dashboard_base_url,omitempty"`
	// Proxy is an optional HTTP/HTTPS proxy URL used exclusively by the
	// Lark webhook delivery client. It is intentionally scoped to this
	// config (rather than promoted to a process-level HTTPS_PROXY env)
	// because the 9 native exchange adapters and CoinGecko collector
	// have their own proxy knobs and a process-wide setting would
	// pollute their latency measurements. Leave blank to dial Lark
	// directly.
	Proxy string `json:"proxy,omitempty"`
}

// ListingTop30PushConfig controls the Top30 hot-gap producer worker.
// StaleAfter is the maximum age of the newest t_top30_snapshot row
// before the producer marks the source as stale and stops generating
// new outbox entries.
//
// AutoQuietAfterStreakDays auto-suppresses pushes for a (display_symbol,
// action) pair once it has stayed in the hot-gap funnel for that many
// consecutive UTC days. The signal observation row is still recorded so
// the streak counter stays accurate; only the outbox insert is skipped.
// Set to 0 to disable. Default 3 days.
//
// MaxPerTick caps the number of outbox rows the delivery worker drains
// per engine tick. Combined with SendSpacing this prevents UTC-rollover
// floods. 0 falls back to the delivery default (50). Default 2.
//
// SendSpacing staggers NextAttemptAt across rows inserted in the same
// ProduceTop30Push pass: the i-th row gets (now + i * SendSpacing).
// Drain then naturally serializes them across subsequent ticks. Default
// 10 minutes.
type ListingTop30PushConfig struct {
	Enabled                  bool          `json:"enabled"`
	PollInterval             time.Duration `json:"poll_interval"`
	StaleAfter               time.Duration `json:"stale_after"`
	AutoQuietAfterStreakDays int           `json:"auto_quiet_after_streak_days"`
	MaxPerTick               int           `json:"max_per_tick"`
	SendSpacing              time.Duration `json:"send_spacing"`
}

// ListingCandidateConfig holds candidate fusion knobs that are not
// part of the worker / delivery / sources blocks. MergeWindow is the
// reach-back window used when fusing signals into an existing
// candidate; P1 default is 14 days.
type ListingCandidateConfig struct {
	MergeWindow time.Duration `json:"merge_window"`
}

// Top30DivergenceConfig is the runtime knob for the CEX-vs-DEX Top30
// view. Splitting the assignment out of the code lets ops re-tag a venue
// (e.g. when a new DEX joins the dashboard) without a redeploy. The
// defaults match the V1 roster: 7 CEX + 3 DEX, threshold=10.
type Top30DivergenceConfig struct {
	CEXPlatforms         []string `json:"cex_platforms"`
	DEXPlatforms         []string `json:"dex_platforms"`
	SignificantRankDelta int      `json:"significant_rank_delta"`
}

// StaleThresholdFor returns the configured freshness threshold for the
// given asset_category, falling back through commodity/stock/index_etf
// chain and ultimately to a 5min default. An empty string is treated as
// crypto for backwards compatibility with V1 BTC/ETH/SOL surfaces that
// were created before asset_category existed.
func (r Runtime) StaleThresholdFor(category string) time.Duration {
	if category == "" {
		category = "crypto"
	}
	if r.StalenessByCategory != nil {
		if d, ok := r.StalenessByCategory[category]; ok && d > 0 {
			return d
		}
	}
	switch category {
	case "crypto":
		return 30 * time.Second
	case "commodity":
		return 300 * time.Second
	case "stock", "index_etf":
		return 600 * time.Second
	}
	return 300 * time.Second
}

// CollectionConfig controls the 5-minute native REST collection cycle. It is
// intentionally independent from BackfillConfig so operators can tune live
// orderbook/ticker pressure without changing Top30 history repair traffic.
type CollectionConfig struct {
	PerPlatformConcurrency int `json:"per_platform_concurrency"`
	PerPlatformRatePerSec  int `json:"per_platform_rate_per_sec"`
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
// Proxy is read as a literal URL string from edgex-liquidity-dashboard.yaml; the CoinGecko
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

type DatabaseConfig struct {
	Name            string        `json:"name"`
	Addr            string        `json:"addr"`
	UserName        string        `json:"user_name"`
	Password        string        `json:"-"`
	ParseTime       bool          `json:"parse_time"`
	MaxIdleConn     int           `json:"max_idle_conn"`
	MaxOpenConn     int           `json:"max_open_conn"`
	ConnMaxLifeTime time.Duration `json:"conn_max_life_time"`
	DSN             string        `json:"-"`
}

type AlertConfig struct {
	AppName         string `json:"app_name"`
	Enabled         bool   `json:"enabled"`
	FeishuUid       string `json:"feishu_uid,omitempty"`
	DestPhoneNumber string `json:"dest_phone_number,omitempty"`
	Business        string `json:"business,omitempty"`
	ServerURL       string `json:"server_url,omitempty"`
	// Webhooks routes alerts by business module (listing /
	// liquidity / ...). Replaces the legacy priority-named
	// WebHookP12 / P3 / P45 fields. On Load, when Webhooks.Listing
	// is empty and the legacy WebHookP3 is set, the loader auto-
	// migrates WebHookP3 → Webhooks.Listing so existing nacos
	// configs keep working without redeploy.
	Webhooks AlertWebhooks `json:"webhooks"`

	// Deprecated. Kept so existing nacos / yaml configs continue
	// loading during the rollout. New code MUST NOT read these
	// directly; route lookups go through Webhooks.<channel>.
	WebHookP12 string `json:"webhook_p12,omitempty"`
	WebHookP3  string `json:"webhook_p3,omitempty"`
	WebHookP45 string `json:"webhook_p45,omitempty"`
}

// AlertWebhooks maps each business module to its Lark bot URL.
// listing currently hosts the Top30 hot-gap (#1) and CEX/DEX
// divergence (#2-#5) cards. liquidity hosts the dashboard depth
// alerts (#10 liquidity_lag / #11 worst_depth). Future modules add
// new fields here, NOT new priority lanes.
type AlertWebhooks struct {
	Listing   string `json:"listing,omitempty"   yaml:"Listing"`
	Liquidity string `json:"liquidity,omitempty" yaml:"Liquidity"`
}

type CatalogConfig struct {
	ExchangeEndpointsFile string `json:"exchange_endpoints_file"`
	SymbolMappingFile     string `json:"symbol_mapping_file"`
	InstrumentCatalogFile string `json:"instrument_catalog_file"`
	ListedUniverseFile    string `json:"listed_universe_file"`
}

type Config struct {
	Symbols   []domain.SymbolSub `json:"symbols"`
	Platforms []string           `json:"platforms"`
	Runtime   Runtime            `json:"runtime"`
	Database  DatabaseConfig     `json:"database"`
	Alert     AlertConfig        `json:"alert"`
	Catalog   CatalogConfig      `json:"catalog"`
	// CanonicalIndex is the reverse alias map built from
	// symbol_mapping.yaml at Load time. nil when the YAML cannot be
	// read; callers must treat the receiver as nil-safe.
	CanonicalIndex *CanonicalIndex `json:"-"`
}

func (c Config) MySQLDSN() string {
	if c.Database.DSN != "" {
		return c.Database.DSN
	}
	return c.Database.DSNString()
}

func (d DatabaseConfig) DSNString() string {
	if d.Name == "" || d.Addr == "" || d.UserName == "" {
		return ""
	}
	q := url.Values{}
	if d.ParseTime {
		q.Set("parseTime", "true")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s", d.UserName, d.Password, d.Addr, d.Name)
	if encoded := q.Encode(); encoded != "" {
		dsn += "?" + encoded
	}
	return dsn
}

func Load(configDir string) (Config, error) {
	if configDir == "" {
		configDir = filepath.Join("..", "config")
	}
	loadDotEnv(filepath.Join(configDir, ".env"))
	cfg := Default()

	mainCfg, hasMain, err := loadDashboardConfig(filepath.Join(configDir, "edgex-liquidity-dashboard.yaml"))
	if err != nil {
		return Config{}, err
	}
	if hasMain {
		cfg.Database = mainCfg.Database.toConfig()
		cfg.Alert = mainCfg.Alert.toConfig()
		cfg.Catalog = mainCfg.Catalog.withDefaults()
		runtimeBlock := mainCfg.Runtime
		if !runtimeBlock.hasValues() {
			runtimeBlock = mainCfg.LegacyRuntime()
		}
		cfg.Runtime, err = applyRuntimeFile(cfg.Runtime, runtimeBlock)
		if err != nil {
			return Config{}, err
		}
	} else {
		cfg.Catalog = defaultCatalogConfig()
		cfg.Database.DSN = os.Getenv("DASHBOARD_MYSQL_DSN")
	}

	endpoints, err := loadEndpoints(filepath.Join(configDir, cfg.Catalog.ExchangeEndpointsFile))
	if err != nil {
		return Config{}, err
	}
	if len(endpoints) == 0 {
		endpoints = defaultEndpoints()
	}

	if symbols, platforms, err := loadSymbols(filepath.Join(configDir, cfg.Catalog.SymbolMappingFile)); err != nil {
		return Config{}, err
	} else if len(symbols) > 0 && len(platforms) > 0 {
		cfg.Platforms = platforms
		cfg.Symbols = expandSymbols(symbols, platforms, endpoints)
		cfg.CanonicalIndex = NewCanonicalIndex(symbols)
	}

	if !hasMain {
		runtimeCfg, err := loadRuntime(filepath.Join(configDir, "runtime.yaml"), cfg.Runtime)
		if err != nil {
			return Config{}, err
		}
		cfg.Runtime = runtimeCfg
	}

	cat, err := LoadCatalog(filepath.Join(configDir, cfg.Catalog.InstrumentCatalogFile))
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
		if entry.MarketSurface != "" {
			subs[i].MarketSurface = entry.MarketSurface
		}
		if entry.InstrumentKind != "" {
			subs[i].InstrumentKind = entry.InstrumentKind
		}
		if entry.Lineage != "" {
			subs[i].Lineage = entry.Lineage
		}
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
				DisplayName:    domain.DefaultDisplayName(s.canonical),
				Canonical:      s.canonical,
				AssetCategory:  domain.AssetCategoryCrypto,
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
		Catalog:   defaultCatalogConfig(),
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
			Collection:            defaultCollectionConfig(),
			Backfill:              defaultBackfillConfig(),
			StalenessByCategory: map[string]time.Duration{
				"crypto":    30 * time.Second,
				"commodity": 300 * time.Second,
				"stock":     600 * time.Second,
				"index_etf": 600 * time.Second,
			},
			CooldownFailureThreshold: 3,
			CooldownDuration:         5 * time.Minute,
			Top30Divergence:          defaultTop30DivergenceConfig(),
			ListingAgent:             defaultListingAgentConfig(),
		},
	}
}

// defaultListingAgentConfig seeds the P1 source roster, worker lease,
// delivery, Top30 push, and candidate fusion defaults. Source polls
// mirror the cadences confirmed in §23.4 of the main design.
func defaultListingAgentConfig() ListingAgentConfig {
	return ListingAgentConfig{
		Enabled: true,
		Worker: ListingWorkerConfig{
			LeaseTTL:    2 * time.Minute,
			MaxAttempts: 5,
			RetryBackoff: []time.Duration{
				time.Minute,
				5 * time.Minute,
				15 * time.Minute,
				time.Hour,
			},
		},
		Sources: ListingSourcesConfig{
			InstrumentDiff: ListingInstrumentDiffConfig{
				Enabled: true,
				Polls: []ListingSourcePollConfig{
					{Platform: "binance", MarketType: "usdm_futures", PollInterval: 3 * time.Minute, Enabled: true},
					{Platform: "bybit", MarketType: "linear", PollInterval: 3 * time.Minute, Enabled: true},
					{Platform: "okx", MarketType: "swap", PollInterval: 5 * time.Minute, Enabled: true},
					{Platform: "bitget", MarketType: "usdt_futures", PollInterval: 5 * time.Minute, Enabled: true},
					{Platform: "mexc", MarketType: "contract", PollInterval: 5 * time.Minute, Enabled: true},
					{Platform: "hyperliquid", MarketType: "perp", PollInterval: 3 * time.Minute, Enabled: true},
				},
			},
			Announcement: ListingAnnouncementConfig{
				Enabled: true,
				Polls: []ListingSourcePollConfig{
					{Platform: "bybit", PollInterval: 3 * time.Minute, Enabled: true},
					{Platform: "bitget", PollInterval: 3 * time.Minute, Enabled: true},
					{Platform: "binance", PollInterval: 5 * time.Minute, Enabled: true},
				},
			},
		},
		Delivery: ListingDeliveryConfig{
			Enabled:            true,
			Top30WebhookURLEnv: "LARK_LISTING_TOP30_WEBHOOK_URL",
		},
		Top30Push: ListingTop30PushConfig{
			AutoQuietAfterStreakDays: 3,
			MaxPerTick:               2,
			SendSpacing:              10 * time.Minute,
			Enabled:                  true,
			PollInterval:             5 * time.Minute,
			StaleAfter:               15 * time.Minute,
		},
		Top30DivergencePush: Top30DivergencePushConfig{
			Enabled:     true,
			TopNPerCard: 10,
			StaleAfter:  15 * time.Minute,
			SendSpacing: 30 * time.Second,
		},
		LiquidityAlert: defaultLiquidityAlertConfig(),
		Candidate: ListingCandidateConfig{
			MergeWindow: 14 * 24 * time.Hour,
		},
	}
}

// defaultLiquidityAlertConfig keeps the feature OFF by default; the SG
// rollout flips Enabled=true once the dry-run outbox rows from one
// reissue cycle look clean. The thresholds match the spec §5.2 table.
func defaultLiquidityAlertConfig() LiquidityAlertConfig {
	return LiquidityAlertConfig{
		Enabled:          false,
		DepthTierPct:     0.001,
		LagThreshold:     0.5,
		MinComparators:   3,
		ReissueInterval:  6 * time.Hour,
		ClearConsecutive: 3,
		StaleAfter:       30 * time.Minute,
		PollInterval:     5 * time.Minute,
		MaxPerTick:       5,
		SendSpacing:      0,
	}
}

// defaultTop30DivergenceConfig seeds the V1 venue classification. The 7
// CEX platforms mirror the existing 24h-share denominator; the 3 DEX
// platforms include edgeX itself so the "DEX side" of the comparison
// reflects the entire onchain perp surface the dashboard tracks.
func defaultTop30DivergenceConfig() Top30DivergenceConfig {
	return Top30DivergenceConfig{
		CEXPlatforms:         []string{"binance", "okx", "bybit", "bitget", "mexc", "gate", "bingx"},
		DEXPlatforms:         []string{"hyperliquid", "lighter", "edgeX"},
		SignificantRankDelta: 10,
	}
}

func defaultCollectionConfig() CollectionConfig {
	return CollectionConfig{
		PerPlatformConcurrency: 3,
		PerPlatformRatePerSec:  4,
	}
}

// defaultBackfillConfig keeps Top30 backfill enabled by default; an
// operator who explicitly wants to disable it can set
// `backfill.enabled: false` in edgex-liquidity-dashboard.yaml.
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
	DisplaySymbol  string              `yaml:"display_symbol"`
	DisplayName    string              `yaml:"display_name"`
	Canonical      string              `yaml:"canonical"`
	AssetCategory  string              `yaml:"asset_category"`
	MarketSurface  string              `yaml:"market_surface"`
	InstrumentKind string              `yaml:"instrument_kind"`
	Lineage        string              `yaml:"lineage"`
	BaseAsset      string              `yaml:"base_asset"`
	QuoteAsset     string              `yaml:"quote_asset"`
	SettleAsset    string              `yaml:"settle_asset"`
	Aliases        map[string][]string `yaml:"aliases"`
}

type symbolFile struct {
	Symbols   []symbolYAML `yaml:"symbols"`
	Platforms []string     `yaml:"platforms"`
}

type endpointFile struct {
	Endpoints map[string]string `yaml:"endpoints"`
}

type runtimeFile struct {
	CollectionInterval       string                    `yaml:"collection_interval"`
	HTTPTimeout              string                    `yaml:"http_timeout"`
	ExchangeProxy            string                    `yaml:"exchange_proxy"`
	LighterWSURL             string                    `yaml:"lighter_ws_url"`
	LighterStaleAfter        string                    `yaml:"lighter_stale_after"`
	DisplayFallbackWindow    string                    `yaml:"display_fallback_window"`
	DepthTiers               []float64                 `yaml:"depth_tiers"`
	SlippageBucketsUSD       []float64                 `yaml:"slippage_buckets_usd"`
	VolumeDiscounts          map[string]float64        `yaml:"volume_discounts"`
	CoinGecko                *coinGeckoFile            `yaml:"coingecko"`
	WSProviders              map[string]wsProviderFile `yaml:"ws_providers"`
	Collection               *collectionFile           `yaml:"collection"`
	Backfill                 *backfillFile             `yaml:"backfill"`
	StalenessByCategory      map[string]string         `yaml:"staleness_by_category"`
	CooldownFailureThreshold *int                      `yaml:"cooldown_failure_threshold"`
	CooldownDuration         string                    `yaml:"cooldown_duration"`
	Top30Divergence          *top30DivergenceFile      `yaml:"top30_divergence"`
	ListingAgent             *listingAgentFile         `yaml:"listing_agent"`
}

type listingAgentFile struct {
	Enabled             *bool                    `yaml:"enabled"`
	Worker              *listingWorkerFile       `yaml:"worker"`
	Sources             *listingSourcesFile      `yaml:"sources"`
	Delivery            *listingDeliveryFile     `yaml:"delivery"`
	Top30Push           *listingTop30PushFile    `yaml:"top30_push"`
	Top30DivergencePush *top30DivergencePushFile `yaml:"top30_divergence_push"`
	LiquidityAlert      *liquidityAlertFile      `yaml:"liquidity_alert"`
	Candidate           *listingCandidateFile    `yaml:"candidate"`
}

type liquidityAlertFile struct {
	Enabled          *bool    `yaml:"enabled"`
	DepthTierPct     *float64 `yaml:"depth_tier_pct"`
	LagThreshold     *float64 `yaml:"lag_threshold"`
	MinComparators   *int     `yaml:"min_comparators"`
	ReissueInterval  string   `yaml:"reissue_interval"`
	ClearConsecutive *int     `yaml:"clear_consecutive"`
	StaleAfter       string   `yaml:"stale_after"`
	PollInterval     string   `yaml:"poll_interval"`
	MaxPerTick       *int     `yaml:"max_per_tick"`
	SendSpacing      string   `yaml:"send_spacing"`
}

type top30DivergencePushFile struct {
	Enabled     *bool  `yaml:"enabled"`
	TopNPerCard *int   `yaml:"top_n_per_card"`
	StaleAfter  string `yaml:"stale_after"`
	SendSpacing string `yaml:"send_spacing"`
}

type listingWorkerFile struct {
	LeaseTTL     string   `yaml:"lease_ttl"`
	MaxAttempts  *int     `yaml:"max_attempts"`
	RetryBackoff []string `yaml:"retry_backoff"`
}

type listingSourcesFile struct {
	InstrumentDiff *listingInstrumentDiffFile `yaml:"instrument_diff"`
	Announcement   *listingAnnouncementFile   `yaml:"announcement"`
}

type listingInstrumentDiffFile struct {
	Enabled *bool                   `yaml:"enabled"`
	Polls   []listingSourcePollFile `yaml:"polls"`
}

type listingAnnouncementFile struct {
	Enabled *bool                   `yaml:"enabled"`
	Polls   []listingSourcePollFile `yaml:"polls"`
}

type listingSourcePollFile struct {
	Platform     string `yaml:"platform"`
	MarketType   string `yaml:"market_type"`
	PollInterval string `yaml:"poll_interval"`
	Enabled      *bool  `yaml:"enabled"`
}

type listingDeliveryFile struct {
	Enabled            *bool  `yaml:"enabled"`
	Top30WebhookURL    string `yaml:"top30_webhook_url"`
	Top30WebhookURLEnv string `yaml:"top30_webhook_url_env"`
	Top30WebhookSecret string `yaml:"top30_webhook_secret"`
	DashboardBaseURL   string `yaml:"dashboard_base_url"`
	Proxy              string `yaml:"proxy"`
}

type listingTop30PushFile struct {
	Enabled                  *bool  `yaml:"enabled"`
	PollInterval             string `yaml:"poll_interval"`
	StaleAfter               string `yaml:"stale_after"`
	AutoQuietAfterStreakDays *int   `yaml:"auto_quiet_after_streak_days"`
	MaxPerTick               *int   `yaml:"max_per_tick"`
	SendSpacing              string `yaml:"send_spacing"`
}

type listingCandidateFile struct {
	MergeWindow string `yaml:"merge_window"`
}

type dashboardFile struct {
	runtimeFile `yaml:",inline"`
	Database    databaseFile `yaml:"Database"`
	Alert       alertFile    `yaml:"Alert"`
	Runtime     runtimeFile  `yaml:"Runtime"`
	Catalog     catalogFile  `yaml:"Catalog"`
}

type databaseFile struct {
	Name            string `yaml:"Name"`
	Addr            string `yaml:"Addr"`
	UserName        string `yaml:"UserName"`
	Password        string `yaml:"Password"`
	ParseTime       *bool  `yaml:"ParseTime"`
	MaxIdleConn     int    `yaml:"MaxIdleConn"`
	MaxOpenConn     int    `yaml:"MaxOpenConn"`
	ConnMaxLifeTime string `yaml:"ConnMaxLifeTime"`
	DSN             string `yaml:"DSN"`
}

type alertFile struct {
	AppName         string             `yaml:"AppName"`
	Enabled         *bool              `yaml:"Enabled"`
	FeishuUid       string             `yaml:"FeishuUid"`
	DestPhoneNumber string             `yaml:"DestPhoneNumber"`
	Business        string             `yaml:"Business"`
	ServerURL       string             `yaml:"ServerUrl"`
	Webhooks        *alertWebhooksFile `yaml:"Webhooks"`
	WebHookP12      string             `yaml:"WebHookP12"`
	WebHookP3       string             `yaml:"WebHookP3"`
	WebHookP45      string             `yaml:"WebHookP45"`
}

type alertWebhooksFile struct {
	Listing   string `yaml:"Listing"`
	Liquidity string `yaml:"Liquidity"`
}

type catalogFile struct {
	ExchangeEndpointsFile string `yaml:"ExchangeEndpointsFile"`
	SymbolMappingFile     string `yaml:"SymbolMappingFile"`
	InstrumentCatalogFile string `yaml:"InstrumentCatalogFile"`
	ListedUniverseFile    string `yaml:"ListedUniverseFile"`
}

type top30DivergenceFile struct {
	CEXPlatforms         []string `yaml:"cex_platforms"`
	DEXPlatforms         []string `yaml:"dex_platforms"`
	SignificantRankDelta *int     `yaml:"significant_rank_delta"`
}

type collectionFile struct {
	PerPlatformConcurrency *int `yaml:"per_platform_concurrency"`
	PerPlatformRatePerSec  *int `yaml:"per_platform_rate_per_sec"`
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
	// MarketSurface / InstrumentKind / Lineage are post-merge values written
	// by build-catalog after applying any platform_overrides declared in
	// symbol_mapping.yaml. The runtime adapter consumes the resolved value
	// directly; per-platform override declarations are never re-read here.
	MarketSurface  string `yaml:"market_surface,omitempty" json:"market_surface,omitempty"`
	InstrumentKind string `yaml:"instrument_kind,omitempty" json:"instrument_kind,omitempty"`
	Lineage        string `yaml:"lineage,omitempty" json:"lineage,omitempty"`
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

func defaultCatalogConfig() CatalogConfig {
	return CatalogConfig{
		ExchangeEndpointsFile: "exchange_endpoints.yaml",
		SymbolMappingFile:     "symbol_mapping.yaml",
		InstrumentCatalogFile: "instrument_catalog.yaml",
		ListedUniverseFile:    "listed_universe.yaml",
	}
}

func loadDashboardConfig(path string) (dashboardFile, bool, error) {
	var file dashboardFile
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return file, false, nil
	}
	if err != nil {
		return file, false, err
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return file, false, err
	}
	return file, true, nil
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func (f dashboardFile) LegacyRuntime() runtimeFile {
	return runtimeFile{
		CollectionInterval:       f.CollectionInterval,
		HTTPTimeout:              f.HTTPTimeout,
		ExchangeProxy:            f.ExchangeProxy,
		LighterWSURL:             f.LighterWSURL,
		LighterStaleAfter:        f.LighterStaleAfter,
		DisplayFallbackWindow:    f.DisplayFallbackWindow,
		DepthTiers:               f.DepthTiers,
		SlippageBucketsUSD:       f.SlippageBucketsUSD,
		VolumeDiscounts:          f.VolumeDiscounts,
		CoinGecko:                f.CoinGecko,
		WSProviders:              f.WSProviders,
		Collection:               f.Collection,
		Backfill:                 f.Backfill,
		StalenessByCategory:      f.StalenessByCategory,
		CooldownFailureThreshold: f.CooldownFailureThreshold,
		CooldownDuration:         f.CooldownDuration,
		Top30Divergence:          f.Top30Divergence,
		ListingAgent:             f.ListingAgent,
	}
}

func (f runtimeFile) hasValues() bool {
	return f.CollectionInterval != "" || f.HTTPTimeout != "" || f.ExchangeProxy != "" || f.LighterWSURL != "" ||
		f.LighterStaleAfter != "" || f.DisplayFallbackWindow != "" || len(f.DepthTiers) > 0 ||
		len(f.SlippageBucketsUSD) > 0 || len(f.VolumeDiscounts) > 0 || f.CoinGecko != nil ||
		len(f.WSProviders) > 0 || f.Collection != nil || f.Backfill != nil || len(f.StalenessByCategory) > 0 ||
		f.CooldownFailureThreshold != nil || f.CooldownDuration != "" || f.Top30Divergence != nil ||
		f.ListingAgent != nil
}

func (f catalogFile) withDefaults() CatalogConfig {
	cfg := defaultCatalogConfig()
	if f.ExchangeEndpointsFile != "" {
		cfg.ExchangeEndpointsFile = f.ExchangeEndpointsFile
	}
	if f.SymbolMappingFile != "" {
		cfg.SymbolMappingFile = f.SymbolMappingFile
	}
	if f.InstrumentCatalogFile != "" {
		cfg.InstrumentCatalogFile = f.InstrumentCatalogFile
	}
	if f.ListedUniverseFile != "" {
		cfg.ListedUniverseFile = f.ListedUniverseFile
	}
	return cfg
}

func (f databaseFile) toConfig() DatabaseConfig {
	parseTime := true
	if f.ParseTime != nil {
		parseTime = *f.ParseTime
	}
	cfg := DatabaseConfig{
		Name:        f.Name,
		Addr:        f.Addr,
		UserName:    f.UserName,
		Password:    f.Password,
		ParseTime:   parseTime,
		MaxIdleConn: f.MaxIdleConn,
		MaxOpenConn: f.MaxOpenConn,
		DSN:         f.DSN,
	}
	if f.ConnMaxLifeTime != "" {
		if d, err := time.ParseDuration(f.ConnMaxLifeTime); err == nil {
			cfg.ConnMaxLifeTime = d
		}
	}
	return cfg
}

func (f alertFile) toConfig() AlertConfig {
	enabled := false
	if f.Enabled != nil {
		enabled = *f.Enabled
	}
	var hooks AlertWebhooks
	if f.Webhooks != nil {
		hooks = AlertWebhooks{
			Listing:   strings.TrimSpace(f.Webhooks.Listing),
			Liquidity: strings.TrimSpace(f.Webhooks.Liquidity),
		}
	}
	if hooks.Listing == "" && strings.TrimSpace(f.WebHookP3) != "" {
		hooks.Listing = strings.TrimSpace(f.WebHookP3)
	}
	return AlertConfig{
		AppName:         f.AppName,
		Enabled:         enabled,
		FeishuUid:       f.FeishuUid,
		DestPhoneNumber: f.DestPhoneNumber,
		Business:        f.Business,
		ServerURL:       f.ServerURL,
		Webhooks:        hooks,
		WebHookP12:      f.WebHookP12,
		WebHookP3:       f.WebHookP3,
		WebHookP45:      f.WebHookP45,
	}
}

func loadRuntime(path string, base Runtime) (Runtime, error) {
	var file runtimeFile
	if err := readYAML(path, &file); err != nil {
		return Runtime{}, err
	}
	return applyRuntimeFile(base, file)
}

func applyRuntimeFile(base Runtime, file runtimeFile) (Runtime, error) {
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
	if file.Collection != nil {
		base.Collection = applyCollectionFile(base.Collection, *file.Collection)
	}
	if file.Backfill != nil {
		base.Backfill = applyBackfillFile(base.Backfill, *file.Backfill)
	}
	if len(file.StalenessByCategory) > 0 {
		merged := map[string]time.Duration{}
		for k, v := range base.StalenessByCategory {
			merged[k] = v
		}
		for k, raw := range file.StalenessByCategory {
			if raw == "" {
				continue
			}
			duration, err := time.ParseDuration(raw)
			if err != nil {
				return Runtime{}, fmt.Errorf("staleness_by_category[%q]: %w", k, err)
			}
			merged[k] = duration
		}
		base.StalenessByCategory = merged
	}
	if file.CooldownFailureThreshold != nil && *file.CooldownFailureThreshold > 0 {
		base.CooldownFailureThreshold = *file.CooldownFailureThreshold
	}
	if file.CooldownDuration != "" {
		duration, err := time.ParseDuration(file.CooldownDuration)
		if err != nil {
			return Runtime{}, fmt.Errorf("cooldown_duration: %w", err)
		}
		base.CooldownDuration = duration
	}
	if file.Top30Divergence != nil {
		base.Top30Divergence = applyTop30DivergenceFile(base.Top30Divergence, *file.Top30Divergence)
	}
	if file.ListingAgent != nil {
		la, err := applyListingAgentFile(base.ListingAgent, *file.ListingAgent)
		if err != nil {
			return Runtime{}, err
		}
		base.ListingAgent = la
	}
	return base, nil
}

// applyListingAgentFile overlays the YAML listing_agent block onto the
// default ListingAgentConfig. Unset YAML fields keep their default
// values; durations are parsed with time.ParseDuration; the polls
// arrays fully replace the defaults when non-empty so operators can
// reduce the source roster.
func applyListingAgentFile(base ListingAgentConfig, file listingAgentFile) (ListingAgentConfig, error) {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.Worker != nil {
		w, err := applyListingWorkerFile(base.Worker, *file.Worker)
		if err != nil {
			return ListingAgentConfig{}, err
		}
		base.Worker = w
	}
	if file.Sources != nil {
		s, err := applyListingSourcesFile(base.Sources, *file.Sources)
		if err != nil {
			return ListingAgentConfig{}, err
		}
		base.Sources = s
	}
	if file.Delivery != nil {
		base.Delivery = applyListingDeliveryFile(base.Delivery, *file.Delivery)
	}
	if file.Top30Push != nil {
		t, err := applyListingTop30PushFile(base.Top30Push, *file.Top30Push)
		if err != nil {
			return ListingAgentConfig{}, err
		}
		base.Top30Push = t
	}
	if file.Top30DivergencePush != nil {
		t, err := applyTop30DivergencePushFile(base.Top30DivergencePush, *file.Top30DivergencePush)
		if err != nil {
			return ListingAgentConfig{}, err
		}
		base.Top30DivergencePush = t
	}
	if file.LiquidityAlert != nil {
		la, err := applyLiquidityAlertFile(base.LiquidityAlert, *file.LiquidityAlert)
		if err != nil {
			return ListingAgentConfig{}, err
		}
		base.LiquidityAlert = la
	}
	if file.Candidate != nil {
		c, err := applyListingCandidateFile(base.Candidate, *file.Candidate)
		if err != nil {
			return ListingAgentConfig{}, err
		}
		base.Candidate = c
	}
	return base, nil
}

func applyListingWorkerFile(base ListingWorkerConfig, file listingWorkerFile) (ListingWorkerConfig, error) {
	if file.LeaseTTL != "" {
		d, err := time.ParseDuration(file.LeaseTTL)
		if err != nil {
			return ListingWorkerConfig{}, fmt.Errorf("listing_agent.worker.lease_ttl: %w", err)
		}
		base.LeaseTTL = d
	}
	if file.MaxAttempts != nil && *file.MaxAttempts > 0 {
		base.MaxAttempts = *file.MaxAttempts
	}
	if len(file.RetryBackoff) > 0 {
		out := make([]time.Duration, 0, len(file.RetryBackoff))
		for i, raw := range file.RetryBackoff {
			if raw == "" {
				continue
			}
			d, err := time.ParseDuration(raw)
			if err != nil {
				return ListingWorkerConfig{}, fmt.Errorf("listing_agent.worker.retry_backoff[%d]: %w", i, err)
			}
			out = append(out, d)
		}
		if len(out) > 0 {
			base.RetryBackoff = out
		}
	}
	return base, nil
}

func applyListingSourcesFile(base ListingSourcesConfig, file listingSourcesFile) (ListingSourcesConfig, error) {
	if file.InstrumentDiff != nil {
		if file.InstrumentDiff.Enabled != nil {
			base.InstrumentDiff.Enabled = *file.InstrumentDiff.Enabled
		}
		if len(file.InstrumentDiff.Polls) > 0 {
			polls, err := convertListingPollFiles(file.InstrumentDiff.Polls, "listing_agent.sources.instrument_diff.polls")
			if err != nil {
				return ListingSourcesConfig{}, err
			}
			base.InstrumentDiff.Polls = polls
		}
	}
	if file.Announcement != nil {
		if file.Announcement.Enabled != nil {
			base.Announcement.Enabled = *file.Announcement.Enabled
		}
		if len(file.Announcement.Polls) > 0 {
			polls, err := convertListingPollFiles(file.Announcement.Polls, "listing_agent.sources.announcement.polls")
			if err != nil {
				return ListingSourcesConfig{}, err
			}
			base.Announcement.Polls = polls
		}
	}
	return base, nil
}

func convertListingPollFiles(in []listingSourcePollFile, scope string) ([]ListingSourcePollConfig, error) {
	out := make([]ListingSourcePollConfig, 0, len(in))
	for i, p := range in {
		enabled := true
		if p.Enabled != nil {
			enabled = *p.Enabled
		}
		var pollInterval time.Duration
		if p.PollInterval != "" {
			d, err := time.ParseDuration(p.PollInterval)
			if err != nil {
				return nil, fmt.Errorf("%s[%d].poll_interval: %w", scope, i, err)
			}
			pollInterval = d
		}
		out = append(out, ListingSourcePollConfig{
			Platform:     p.Platform,
			MarketType:   p.MarketType,
			PollInterval: pollInterval,
			Enabled:      enabled,
		})
	}
	return out, nil
}

func applyListingDeliveryFile(base ListingDeliveryConfig, file listingDeliveryFile) ListingDeliveryConfig {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.Top30WebhookURL != "" {
		base.Top30WebhookURL = file.Top30WebhookURL
	}
	if file.Top30WebhookURLEnv != "" {
		base.Top30WebhookURLEnv = file.Top30WebhookURLEnv
	}
	if file.Top30WebhookSecret != "" {
		base.Top30WebhookSecret = file.Top30WebhookSecret
	}
	if file.DashboardBaseURL != "" {
		base.DashboardBaseURL = file.DashboardBaseURL
	}
	if file.Proxy != "" {
		base.Proxy = file.Proxy
	}
	return base
}

func applyListingTop30PushFile(base ListingTop30PushConfig, file listingTop30PushFile) (ListingTop30PushConfig, error) {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.PollInterval != "" {
		d, err := time.ParseDuration(file.PollInterval)
		if err != nil {
			return ListingTop30PushConfig{}, fmt.Errorf("listing_agent.top30_push.poll_interval: %w", err)
		}
		base.PollInterval = d
	}
	if file.StaleAfter != "" {
		d, err := time.ParseDuration(file.StaleAfter)
		if err != nil {
			return ListingTop30PushConfig{}, fmt.Errorf("listing_agent.top30_push.stale_after: %w", err)
		}
		base.StaleAfter = d
	}
	if file.AutoQuietAfterStreakDays != nil {
		if *file.AutoQuietAfterStreakDays < 0 {
			return ListingTop30PushConfig{}, fmt.Errorf("listing_agent.top30_push.auto_quiet_after_streak_days: must be >= 0")
		}
		base.AutoQuietAfterStreakDays = *file.AutoQuietAfterStreakDays
	}
	if file.MaxPerTick != nil {
		if *file.MaxPerTick < 0 {
			return ListingTop30PushConfig{}, fmt.Errorf("listing_agent.top30_push.max_per_tick: must be >= 0")
		}
		base.MaxPerTick = *file.MaxPerTick
	}
	if file.SendSpacing != "" {
		d, err := time.ParseDuration(file.SendSpacing)
		if err != nil {
			return ListingTop30PushConfig{}, fmt.Errorf("listing_agent.top30_push.send_spacing: %w", err)
		}
		if d < 0 {
			return ListingTop30PushConfig{}, fmt.Errorf("listing_agent.top30_push.send_spacing: must be >= 0")
		}
		base.SendSpacing = d
	}
	return base, nil
}

func applyTop30DivergencePushFile(base Top30DivergencePushConfig, file top30DivergencePushFile) (Top30DivergencePushConfig, error) {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.TopNPerCard != nil {
		if *file.TopNPerCard < 0 {
			return Top30DivergencePushConfig{}, fmt.Errorf("listing_agent.top30_divergence_push.top_n_per_card: must be >= 0")
		}
		base.TopNPerCard = *file.TopNPerCard
	}
	if file.StaleAfter != "" {
		d, err := time.ParseDuration(file.StaleAfter)
		if err != nil {
			return Top30DivergencePushConfig{}, fmt.Errorf("listing_agent.top30_divergence_push.stale_after: %w", err)
		}
		base.StaleAfter = d
	}
	if file.SendSpacing != "" {
		d, err := time.ParseDuration(file.SendSpacing)
		if err != nil {
			return Top30DivergencePushConfig{}, fmt.Errorf("listing_agent.top30_divergence_push.send_spacing: %w", err)
		}
		if d < 0 {
			return Top30DivergencePushConfig{}, fmt.Errorf("listing_agent.top30_divergence_push.send_spacing: must be >= 0")
		}
		base.SendSpacing = d
	}
	return base, nil
}

func applyListingCandidateFile(base ListingCandidateConfig, file listingCandidateFile) (ListingCandidateConfig, error) {
	if file.MergeWindow != "" {
		d, err := parseListingDayDuration(file.MergeWindow)
		if err != nil {
			return ListingCandidateConfig{}, fmt.Errorf("listing_agent.candidate.merge_window: %w", err)
		}
		base.MergeWindow = d
	}
	return base, nil
}

func applyLiquidityAlertFile(base LiquidityAlertConfig, file liquidityAlertFile) (LiquidityAlertConfig, error) {
	if file.Enabled != nil {
		base.Enabled = *file.Enabled
	}
	if file.DepthTierPct != nil {
		if *file.DepthTierPct <= 0 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.depth_tier_pct: must be > 0")
		}
		base.DepthTierPct = *file.DepthTierPct
	}
	if file.LagThreshold != nil {
		if *file.LagThreshold <= 0 || *file.LagThreshold >= 1 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.lag_threshold: must be in (0, 1)")
		}
		base.LagThreshold = *file.LagThreshold
	}
	if file.MinComparators != nil {
		if *file.MinComparators < 1 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.min_comparators: must be >= 1")
		}
		base.MinComparators = *file.MinComparators
	}
	if file.ReissueInterval != "" {
		d, err := time.ParseDuration(file.ReissueInterval)
		if err != nil {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.reissue_interval: %w", err)
		}
		if d <= 0 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.reissue_interval: must be > 0")
		}
		base.ReissueInterval = d
	}
	if file.ClearConsecutive != nil {
		if *file.ClearConsecutive < 1 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.clear_consecutive: must be >= 1")
		}
		base.ClearConsecutive = *file.ClearConsecutive
	}
	if file.StaleAfter != "" {
		d, err := time.ParseDuration(file.StaleAfter)
		if err != nil {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.stale_after: %w", err)
		}
		if d <= 0 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.stale_after: must be > 0")
		}
		base.StaleAfter = d
	}
	if file.PollInterval != "" {
		d, err := time.ParseDuration(file.PollInterval)
		if err != nil {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.poll_interval: %w", err)
		}
		if d <= 0 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.poll_interval: must be > 0")
		}
		base.PollInterval = d
	}
	if file.MaxPerTick != nil {
		if *file.MaxPerTick < 0 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.max_per_tick: must be >= 0")
		}
		base.MaxPerTick = *file.MaxPerTick
	}
	if file.SendSpacing != "" {
		d, err := time.ParseDuration(file.SendSpacing)
		if err != nil {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.send_spacing: %w", err)
		}
		if d < 0 {
			return LiquidityAlertConfig{}, fmt.Errorf("listing_agent.liquidity_alert.send_spacing: must be >= 0")
		}
		base.SendSpacing = d
	}
	return base, nil
}

// parseListingDayDuration parses durations that may use the `d` suffix
// (e.g. `14d`), which time.ParseDuration does not support natively.
func parseListingDayDuration(raw string) (time.Duration, error) {
	trim := strings.TrimSpace(raw)
	if strings.HasSuffix(trim, "d") {
		nStr := strings.TrimSpace(strings.TrimSuffix(trim, "d"))
		if nStr == "" {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}
		n, err := strconvParsePositive(nStr)
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(trim)
}

func strconvParsePositive(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func applyTop30DivergenceFile(base Top30DivergenceConfig, file top30DivergenceFile) Top30DivergenceConfig {
	if len(file.CEXPlatforms) > 0 {
		base.CEXPlatforms = append([]string(nil), file.CEXPlatforms...)
	}
	if len(file.DEXPlatforms) > 0 {
		base.DEXPlatforms = append([]string(nil), file.DEXPlatforms...)
	}
	if file.SignificantRankDelta != nil && *file.SignificantRankDelta > 0 {
		base.SignificantRankDelta = *file.SignificantRankDelta
	}
	return base
}

func applyCollectionFile(base CollectionConfig, file collectionFile) CollectionConfig {
	if file.PerPlatformConcurrency != nil {
		base.PerPlatformConcurrency = *file.PerPlatformConcurrency
	}
	if file.PerPlatformRatePerSec != nil {
		base.PerPlatformRatePerSec = *file.PerPlatformRatePerSec
	}
	return base
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
		displayName := symbol.DisplayName
		if displayName == "" {
			displayName = domain.DefaultDisplayName(symbol.Canonical)
		}
		category := valueOr(symbol.AssetCategory, domain.AssetCategoryCrypto)
		for _, platform := range platforms {
			subs = append(subs, domain.SymbolSub{
				DisplaySymbol:  symbol.DisplaySymbol,
				DisplayName:    displayName,
				Canonical:      symbol.Canonical,
				AssetCategory:  category,
				MarketSurface:  valueOr(symbol.MarketSurface, "perp"),
				InstrumentKind: valueOr(symbol.InstrumentKind, "canonical"),
				Lineage:        symbol.Lineage,
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
