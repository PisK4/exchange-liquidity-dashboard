package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
	activityfetcher "edgex-ops-intelligence/backend/internal/activity/fetcher"
	activityparser "edgex-ops-intelligence/backend/internal/activity/parser"
	"edgex-ops-intelligence/backend/internal/adapter"
	"edgex-ops-intelligence/backend/internal/api"
	"edgex-ops-intelligence/backend/internal/collector"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing"
	listingfetcher "edgex-ops-intelligence/backend/internal/listing/fetcher"
	"edgex-ops-intelligence/backend/internal/marketdata/coingecko"
)

// version is set at link time via -ldflags. Falls back to "dev" for
// local `go run` invocations.
//
//nolint:gochecknoglobals  // ldflags-injected build identifier
var version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	role := flag.String("role", "all", "role: api, collector, listing, activity, or all")
	runOnce := flag.Bool("run-once", false, "run one collection cycle at startup")
	mysqlDSN := flag.String("mysql-dsn", os.Getenv("OPS_INTELLIGENCE_MYSQL_DSN"), "optional MySQL DSN, for example root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true")
	configDir := flag.String("config-dir", "../config", "directory containing EdgeX Ops Intelligence yaml configs")
	catalogReloadInterval := flag.Duration("catalog-reload-interval", 2*time.Second, "polling interval for instrument_catalog.yaml hot reload; 0 disables the watcher")
	rawInstrumentsDir := flag.String("raw-instruments-dir", "docs/raw-instruments", "directory containing per-platform raw instrument dumps used by Top30 backfill")
	runtimeDataDir := flag.String("runtime-data-dir", envOr("OPS_INTELLIGENCE_DATA_DIR", ""), "writable directory for runtime-regenerated files (listed_universe.runtime.yaml). Overrides OPS_INTELLIGENCE_DATA_DIR; empty means write next to --config-dir.")
	showVersion := flag.Bool("version", false, "print the embedded build version and exit")
	flag.Parse()

	if *showVersion {
		log.Printf("edgex-ops-intelligence %s", version)
		os.Exit(0)
	}
	api.Version = version

	cfg, err := config.Load(*configDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store := collector.NewStore(cfg)
	resolvedDSN := resolveMySQLDSN(*mysqlDSN, cfg)
	var listingRepo *listing.Repository
	var activityRepo *activity.Repository
	if resolvedDSN != "" {
		db, err := collector.OpenMySQL(resolvedDSN)
		if err != nil {
			log.Fatalf("connect mysql: %v", err)
		}
		defer db.Close()
		if err := collector.ApplyMigrations(db); err != nil {
			log.Fatalf("apply mysql migrations: %v", err)
		}
		store.AttachDB(db)
		if err := store.LoadLatestFromDB(context.Background()); err != nil {
			log.Printf("load latest snapshots from mysql: %v", err)
		}
		listingRepo = listing.NewRepository(db)
		activityRepo = activity.NewRepository(db)
	} else {
		if roleRequiresMySQL(*role) {
			log.Fatalf("role %q requires MySQL DSN (--mysql-dsn or OPS_INTELLIGENCE_MYSQL_DSN)", *role)
		}
		if roleStartsListing(*role) {
			log.Printf("listing engine disabled: no MySQL DSN configured")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *catalogReloadInterval > 0 {
		catalogPath := filepath.Join(*configDir, "instrument_catalog.yaml")
		go store.WatchCatalog(ctx, catalogPath, *catalogReloadInterval)
		log.Printf("watching %s for frontend metadata hot reload (interval=%s)", catalogPath, *catalogReloadInterval)
	}
	var lighterProvider *adapter.LighterWSProvider
	if roleStartsLiveProviders(*role) {
		lighterProvider = adapter.NewLighterWSProviderWithProxy(cfg.Runtime.LighterWSURL, cfg.Runtime.LighterStaleAfter, cfg.Runtime.ExchangeProxy)
		lighterMarketIDs := lighterMarketIDsFromConfig(cfg)
		go lighterProvider.Run(ctx, lighterMarketIDs)
		waitForLighter(ctx, lighterProvider, lighterMarketIDs)
	}

	if *role == "collector" || *role == "all" {
		c := collector.NewCollectorWithLighter(cfg, store, lighterProvider)
		backfiller := collector.NewSymbolBackfiller(cfg, store, lighterProvider)
		var top30bf *collector.Top30Backfiller
		if err := runCollectionCycle(ctx, c, cfg.Runtime.CollectionInterval); err != nil {
			log.Printf("initial collection completed with errors: %v", err)
		}
		if !*runOnce {
			go func() {
				ticker := time.NewTicker(cfg.Runtime.CollectionInterval)
				defer ticker.Stop()
				for range ticker.C {
					if err := runCollectionCycle(ctx, c, cfg.Runtime.CollectionInterval); err != nil {
						log.Printf("collection completed with errors: %v", err)
					}
				}
			}()
			backfiller.Run(ctx, 14)
		} else {
			if err := backfiller.RunOnce(ctx, 14); err != nil {
				log.Printf("symbol-volume backfill run-once completed with errors: %v", err)
			}
		}
		// Top30Backfiller: only relevant under collector role; runs in its
		// own goroutine and waits for the first CG /derivatives round to
		// land before issuing kline pulls.
		if cfg.Runtime.Backfill.Enabled && !*runOnce {
			var snapshots collector.SnapshotReader
			if listingRepo != nil {
				snapshots = newListingSnapshotReader(listingRepo)
				log.Printf("catalog_resolver: DB-first path armed (hyperliquid/gate/lighter/edgeX read from t_listing_instrument_snapshot)")
			} else {
				log.Printf("catalog_resolver: file-only path (no MySQL DSN; api_symbol resolved from %s)", *rawInstrumentsDir)
			}
			resolver := collector.NewCatalogResolverWithDB(*rawInstrumentsDir, snapshots, 0, nil)
			top30bf = collector.NewTop30Backfiller(cfg, store, lighterProvider, resolver)
			top30bf.Run(ctx)
			log.Printf("top30 backfill scheduled (cold_start=%dd, daily=%02d:%02d UTC, concurrency=%d, rate=%d/s)",
				cfg.Runtime.Backfill.ColdStartDays,
				cfg.Runtime.Backfill.ScheduleUTCHour, cfg.Runtime.Backfill.ScheduleUTCMinute,
				cfg.Runtime.Backfill.PerPlatformConcurrency, cfg.Runtime.Backfill.PerPlatformRatePerSec)
		}

		if cfg.Runtime.CoinGecko.Enabled {
			if cgCollector, err := buildCoinGeckoCollector(cfg, store); err != nil {
				log.Printf("coingecko collector disabled: %v", err)
			} else {
				seedUniversePath := filepath.Join(*configDir, "listed_universe.yaml")
				runtimeUniversePath := resolveRuntimeUniversePath(*runtimeDataDir, *configDir)
				log.Printf("listed_universe loader: seed=%s runtime=%s (hot-reloaded every CollectOnce tick)", seedUniversePath, runtimeUniversePath)
				cgCollector.SetListedUniverseLoader(buildUniverseLoader(runtimeUniversePath, seedUniversePath))
				if top30bf != nil {
					cgCollector.SetTop30BackfillScheduler(top30bf)
				}
				if *runOnce {
					if err := cgCollector.CollectOnce(ctx); err != nil {
						log.Printf("coingecko initial collection failed: %v", err)
					}
				} else {
					go cgCollector.Run(ctx)
				}
			}
		}
	}

	seedUniversePath := filepath.Join(*configDir, "listed_universe.yaml")
	runtimeUniversePath := resolveRuntimeUniversePath(*runtimeDataDir, *configDir)
	universeClosure := buildUniverseLoader(runtimeUniversePath, seedUniversePath)
	listingUniverseLoader := func() (*config.ListedUniverse, error) {
		return universeClosure(), nil
	}
	// Bind the refresh job to the runtime path resolved above so a
	// running listing engine always writes where the consumer
	// closures expect to read. The YAML form may contain
	// "${OPS_INTELLIGENCE_DATA_DIR}" or other env placeholders (operator
	// convenience); we run os.ExpandEnv first so unresolved
	// placeholders never reach the engine. After expansion we also
	// fall back to the resolved default if the field ended up empty
	// or still contains a literal "${" (env var unset).
	cfg.Runtime.ListingAgent.ListedUniverseRefresh.RuntimePath = resolveConfigPath(
		cfg.Runtime.ListingAgent.ListedUniverseRefresh.RuntimePath, runtimeUniversePath, *configDir)
	cfg.Runtime.ListingAgent.ListedUniverseRefresh.SeedPath = resolveConfigPath(
		cfg.Runtime.ListingAgent.ListedUniverseRefresh.SeedPath, seedUniversePath, *configDir)
	resolveListingCallbackSecret(&cfg)
	if roleStartsListing(*role) && cfg.Runtime.ListingAgent.Enabled && listingRepo != nil {
		listingHTTPClient, err := listingfetcher.NewHTTPClient(listingfetcher.DefaultRequestTimeout, cfg.Runtime.ExchangeProxy)
		if err != nil {
			log.Fatalf("listing fetcher http client: %v", err)
		}
		listingHTTPDeps := listingfetcher.HTTPDeps{Client: listingHTTPClient}
		listingSources, err := listingfetcher.BuildListingSources(cfg.Runtime.ListingAgent.Sources, listingHTTPDeps)
		if err != nil {
			log.Fatalf("listing build sources: %v", err)
		}
		for _, src := range listingSources.Instrument {
			log.Printf("listing instrument source armed: platform=%s market_type=%s url=%s", src.Platform, src.MarketType, src.SourceURL)
		}
		for _, src := range listingSources.Announcement {
			log.Printf("listing announcement source armed: platform=%s url=%s", src.Platform, src.SourceURL)
		}
		listingEnrichDeps := buildListingEnrichDeps(cfg, listingRepo, universeClosure)
		engine := listing.NewEngine(cfg, listingRepo, listing.EngineDeps{
			LoadUniverse:        listingUniverseLoader,
			InstrumentSources:   listingSources.Instrument,
			AnnouncementSources: listingSources.Announcement,
			DecisionCardEnrich:  listingEnrichDeps,
		})
		if *runOnce && *role == "listing" {
			summary, err := engine.RunOnce(ctx)
			log.Printf("listing run-once summary: %+v", summary)
			if err != nil {
				log.Printf("listing run-once completed with errors: %v", err)
			}
			return
		}
		go func() {
			if err := engine.Run(ctx); err != nil {
				log.Printf("listing engine stopped: %v", err)
			}
		}()
	}

	if *role == "collector" && *runOnce {
		return
	}

	if roleStartsActivity(*role) {
		if !cfg.Runtime.ActivityAgent.Enabled {
			log.Printf("activity agent disabled by runtime config")
			if *role == "activity" {
				os.Exit(1)
			}
		} else if activityRepo == nil {
			log.Printf("activity agent disabled: no repository configured")
			if *role == "activity" {
				os.Exit(1)
			}
		} else {
			engine := activity.NewEngine(activityRepo, buildActivityEngineConfig(cfg), activity.WithEngineHTTPClient(buildActivityDeliveryHTTPClient(cfg)))
			if *runOnce && *role == "activity" {
				summary, err := engine.RunOnce(ctx)
				log.Printf("activity run-once summary: %+v", summary)
				if err != nil {
					log.Printf("activity run-once completed with errors: %v", err)
					os.Exit(1)
				}
				return
			}
			go func() {
				interval := cfg.Runtime.ActivityAgent.DefaultPollInterval
				if err := engine.Run(ctx, interval, log.Printf); err != nil {
					log.Printf("activity engine stopped: %v", err)
				}
			}()
		}
	}

	if *role == "collector" || *role == "listing" || *role == "activity" {
		select {}
	}

	opts := []api.Option{}
	if listingRepo != nil {
		opts = append(opts, api.WithListingReader(listingRepo))
		opts = append(opts, listingDecisionOptions(cfg, listingRepo)...)
	}
	if activityRepo != nil {
		opts = append(opts, api.WithActivityStore(activityRepo))
		opts = append(opts, api.WithActivityDecisionTokenSecret(os.Getenv(cfg.Runtime.ActivityAgent.DecisionToken.SecretEnv)))
	}
	server := api.NewServer(cfg, store, opts...)
	log.Printf("EdgeX liquidity dashboard API listening on %s", *addr)
	if err := http.ListenAndServe(*addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}

// buildUniverseLoader returns a closure that resolves the listed
// universe on every call. The closure prefers the runtime yaml
// (regenerated every 15 min by listing.RefreshListedUniverseFromSnapshots)
// when present and non-empty; otherwise it falls back to the seed
// yaml produced by `make catalog`.
//
// The closure NEVER returns an error: a missing or malformed file
// degrades to nil so downstream code (CG collector, decision-card
// enricher) keeps rendering its legacy "unknown" state instead of
// failing the tick. Errors are logged on the calling goroutine so
// boot-time misconfiguration still surfaces in operator logs.
func buildUniverseLoader(runtimePath, seedPath string) func() *config.ListedUniverse {
	return func() *config.ListedUniverse {
		if runtimePath != "" {
			if u, err := config.LoadListedUniverse(runtimePath); err == nil && u != nil && u.Loaded() && len(u.BaseAssets("edgeX")) > 0 {
				return u
			} else if err != nil {
				log.Printf("listed_universe runtime load failed (%s): %v; trying seed", runtimePath, err)
			} else {
				log.Printf("listed_universe runtime ignored (%s): edgeX universe empty; trying seed", runtimePath)
			}
		}
		u, err := config.LoadListedUniverse(seedPath)
		if err != nil {
			log.Printf("listed_universe seed load failed (%s): %v; edgeX-listed will fall back to 'unknown'", seedPath, err)
			return nil
		}
		return u
	}
}

// resolveConfigPath expands $ENV placeholders inside a YAML-supplied
// path and falls back to the supplied default when (a) the field is
// empty or (b) expansion left behind an unresolved "${...}" segment
// (i.e. the env var was unset). Returning the default in case (b) is
// what lets a docker-compose YAML write
// "${OPS_INTELLIGENCE_DATA_DIR}/listed_universe.runtime.yaml" once and have
// it work on dev hosts that do not set OPS_INTELLIGENCE_DATA_DIR.
func resolveConfigPath(raw, fallback, configDir string) string {
	trimmed := strings.TrimSpace(raw)
	missingEnv := false
	expanded := os.Expand(trimmed, func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			missingEnv = true
		}
		return v
	})
	if expanded == "" || missingEnv || strings.Contains(expanded, "${") {
		return fallback
	}
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(configDir, expanded)
}

// envOr returns os.Getenv(key) when non-empty, otherwise fallback. Used
// to mirror env vars onto flag defaults without panicking when the env
// var is absent.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// resolveRuntimeUniversePath picks the writable path for the
// refresh job's runtime listed_universe.yaml. Priority:
//  1. --runtime-data-dir flag (or OPS_INTELLIGENCE_DATA_DIR env)
//  2. configDir (legacy / single-binary deployments)
//
// In every case the file lives next to a "listed_universe.runtime.yaml"
// name so the seed (listed_universe.yaml) stays untouched.
func resolveRuntimeUniversePath(runtimeDataDir, configDir string) string {
	if runtimeDataDir != "" {
		return filepath.Join(runtimeDataDir, "listed_universe.runtime.yaml")
	}
	return filepath.Join(configDir, "listed_universe.runtime.yaml")
}

// roleStartsListing reports whether the listing engine should be
// started for the given --role flag.
func roleStartsListing(role string) bool {
	return role == "listing" || role == "collector" || role == "all"
}

func roleStartsActivity(role string) bool {
	return role == "activity" || role == "all"
}

// roleRequiresMySQL reports whether the role cannot operate without a
// MySQL DSN. The listing role is the only role that fail-stops on
// missing DSN today; collector / all degrade gracefully.
func roleRequiresMySQL(role string) bool {
	return role == "listing" || role == "activity"
}

func roleStartsLiveProviders(role string) bool {
	return role == "collector" || role == "all"
}

func runCollectionCycle(ctx context.Context, c *collector.Collector, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = time.Minute
	}
	cycleCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.CollectOnce(cycleCtx)
}

// buildCoinGeckoCollector wires the CoinGecko client + mapping using the
// runtime config. The API key is sourced from the environment variable
// named by cfg.APIKeyEnv (default COINGECKO_DEMO_API_KEY); leaving it
// unset is allowed (the public endpoint still works at lower QPS).
func buildCoinGeckoCollector(cfg config.Config, store *collector.Store) (*collector.CoinGeckoCollector, error) {
	cgCfg := cfg.Runtime.CoinGecko
	apiKey := ""
	if cgCfg.APIKeyEnv != "" {
		apiKey = os.Getenv(cgCfg.APIKeyEnv)
	}
	client, err := coingecko.New(coingecko.Config{
		BaseURL:        cgCfg.BaseURL,
		APIKey:         apiKey,
		Proxy:          cgCfg.Proxy,
		RequestTimeout: cgCfg.RequestTimeout,
	})
	if err != nil {
		return nil, err
	}
	mapping := coingecko.NewMapping(cgCfg.ExchangeID, cgCfg.MarketName)
	log.Printf("coingecko collector wired (interval=%s, base=%s, proxy=%q, exchanges=%d)",
		cgCfg.PullInterval, cgCfg.BaseURL, cgCfg.Proxy, len(cgCfg.ExchangeID))
	return collector.NewCoinGeckoCollector(cgCfg, store, client, mapping), nil
}

func lighterMarketIDsFromConfig(cfg config.Config) []int {
	seen := map[int]struct{}{}
	marketIDs := []int{}
	for _, sub := range cfg.Symbols {
		if sub.Platform != "lighter" || sub.MarketID == nil {
			continue
		}
		marketID := *sub.MarketID
		if _, ok := seen[marketID]; ok {
			continue
		}
		seen[marketID] = struct{}{}
		marketIDs = append(marketIDs, marketID)
	}
	if len(marketIDs) == 0 {
		return append([]int(nil), adapter.LighterMarketIDs()...)
	}
	return marketIDs
}

func waitForLighter(ctx context.Context, provider *adapter.LighterWSProvider, marketIDs []int) {
	if provider == nil || len(marketIDs) == 0 {
		return
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		readyCount := provider.ReadyCount(marketIDs)
		if readyCount == len(marketIDs) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			log.Printf("lighter ws not fully ready before initial collection; continuing with statusized failures (ready=%d/%d)", readyCount, len(marketIDs))
			return
		case <-ticker.C:
		}
	}
}

func resolveMySQLDSN(flagValue string, cfg config.Config) string {
	if flagValue != "" {
		return flagValue
	}
	return cfg.MySQLDSN()
}

func buildActivityEngineConfig(cfg config.Config) activity.EngineConfig {
	aa := cfg.Runtime.ActivityAgent
	activityHTTPFetcher := activityfetcher.NewHTTPFetcher(buildActivityHTTPClient(cfg), cfg.Runtime.HTTPTimeout)
	return activity.EngineConfig{
		Enabled:             aa.Enabled,
		WorkerLeaseTTL:      aa.WorkerLeaseTTL,
		WebhookURL:          resolveActivityWebhookURL(cfg),
		DecisionTokenSecret: os.Getenv(aa.DecisionToken.SecretEnv),
		DashboardBaseURL:    aa.Delivery.DashboardBaseURL,
		MaxPerTick:          aa.Delivery.MaxPerTick,
		SendSpacing:         aa.Delivery.SendSpacing,
		Sources:             buildActivitySources(cfg),
		Fetch: func(ctx context.Context, req activity.FetchRequest) (activity.FetchResult, error) {
			got, err := activityHTTPFetcher.Fetch(ctx, activityfetcher.Request{
				URL:         req.URL,
				Platform:    req.Platform,
				SourceGroup: req.SourceGroup,
				FetchMode:   req.FetchMode,
				Headers:     req.Headers,
			})
			if err != nil {
				return activity.FetchResult{}, err
			}
			return activity.FetchResult{
				Platform:    got.Platform,
				SourceGroup: got.SourceGroup,
				SourceURL:   got.SourceURL,
				FetchMode:   got.FetchMode,
				Payload:     got.Payload,
				PayloadHash: got.PayloadHash,
				ContentHash: got.ContentHash,
				HTTPStatus:  got.HTTPStatus,
				ContentType: got.ContentType,
				FetchedAt:   got.FetchedAt,
			}, nil
		},
		Parse: dispatchActivityParser,
	}
}

func buildActivityHTTPClient(cfg config.Config) *http.Client {
	timeout := cfg.Runtime.HTTPTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	proxyURL := strings.TrimSpace(cfg.Runtime.ActivityAgent.SourceProxy)
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(cfg.Runtime.ExchangeProxy)
	}
	return buildHTTPClientWithProxy(timeout, proxyURL)
}

func buildActivityDeliveryHTTPClient(cfg config.Config) *http.Client {
	timeout := cfg.Runtime.HTTPTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return buildHTTPClientWithProxy(timeout, strings.TrimSpace(cfg.Runtime.ActivityAgent.Delivery.Proxy))
}

func buildHTTPClientWithProxy(timeout time.Duration, proxyURL string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != "" {
		if parsed, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		} else {
			log.Printf("http proxy ignored: %v", err)
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func buildActivitySources(cfg config.Config) []activity.SourceConfig {
	out := make([]activity.SourceConfig, 0, len(cfg.Runtime.ActivityAgent.Sources))
	for _, src := range cfg.Runtime.ActivityAgent.Sources {
		sourceURL := strings.TrimSpace(src.SourceURL)
		if sourceURL == "" {
			sourceURL = defaultActivitySourceURL(src.Platform, src.SourceGroup)
		}
		out = append(out, activity.SourceConfig{
			Platform:               src.Platform,
			SourceGroup:            src.SourceGroup,
			SourceType:             src.SourceType,
			SourceURL:              sourceURL,
			FetchMode:              src.FetchMode,
			PollInterval:           src.PollInterval,
			Enabled:                src.Enabled,
			AutoPushEnabled:        src.AutoPushEnabled,
			RequiresProxy:          src.RequiresProxy,
			RequiresBrowserContext: src.RequiresBrowserContext,
			RequiresLogin:          src.RequiresLogin,
			Personalized:           src.Personalized,
			Headers:                defaultActivityHeaders(src.Platform),
		})
	}
	return out
}

func defaultActivitySourceURL(platform, sourceGroup string) string {
	key := strings.ToLower(strings.TrimSpace(platform)) + "|" + strings.TrimSpace(sourceGroup)
	urls := map[string]string{
		"binance|cms_article_list":       "https://www.binance.com/bapi/composite/v1/public/cms/article/catalog/list/query?catalogId=48&pageNo=1&pageSize=20",
		"okx|help_announcement":          "https://www.okx.com/api/v5/support/announcements?category=latest-announcements&page=1&pageSize=15",
		"bingx|openapi_notice":           "https://open-api.bingx.com/openApi/content/v1/announcement?contentType=LatestPromotions&language=en-us&page=1",
		"gate|launchpool_project_list":   "https://www.gate.com/apiw/v2/earn/launch-pool/project-list?page=1&pageSize=10&sub_website_id=0",
		"mexc|latest_events":             "https://www.mexc.com/announcements/latest-events/ongoing",
		"bybit|announcements_ssr":        "https://announcements.bybit.com/en/?category=&page=1",
		"bitget|support_ongoing_section": "https://www.bitget.com/support/sections/4413154768537",
		"hyperliquid|cloudfront_entries": "https://dzjnlsk4rxci0.cloudfront.net/mainnet/entries.json",
		"lighter|incentive_docs":         "https://docs.lighter.xyz/points-program.md",
	}
	return urls[key]
}

func defaultActivityHeaders(platform string) map[string]string {
	headers := map[string]string{
		"Accept":          "application/json,text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-US,en;q=0.9",
	}
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "bingx":
		headers["X-SOURCE-KEY"] = "openapi"
	case "bybit", "bitget", "mexc", "gate":
		headers["User-Agent"] = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
	}
	return headers
}

func dispatchActivityParser(ctx context.Context, doc activity.RawDocument) ([]activity.ActivityEvent, error) {
	parserDoc := activityparser.RawDocument{
		Platform:      doc.Platform,
		SourceGroup:   doc.SourceGroup,
		SourceURL:     doc.SourceURL,
		FetchMode:     doc.FetchMode,
		Payload:       doc.Payload,
		RequiresLogin: doc.RequiresLogin,
		Personalized:  doc.Personalized,
	}
	switch strings.ToLower(strings.TrimSpace(doc.Platform)) {
	case "binance":
		return activityparser.ParseBinance(ctx, parserDoc)
	case "okx":
		return activityparser.ParseOKX(ctx, parserDoc)
	case "bingx":
		return activityparser.ParseBingX(ctx, parserDoc)
	case "gate":
		return activityparser.ParseGate(ctx, parserDoc)
	case "mexc":
		return activityparser.ParseMEXC(ctx, parserDoc)
	case "bybit":
		return activityparser.ParseBybit(ctx, parserDoc)
	case "bitget":
		return activityparser.ParseBitget(ctx, parserDoc)
	case "hyperliquid":
		return activityparser.ParseHyperliquid(ctx, parserDoc)
	case "lighter":
		return activityparser.ParseLighter(ctx, parserDoc)
	default:
		return activityparser.ParseBinance(ctx, parserDoc)
	}
}

func resolveActivityWebhookURL(cfg config.Config) string {
	envName := strings.TrimSpace(cfg.Runtime.ActivityAgent.Delivery.WebhookURLEnv)
	if envName != "" {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(cfg.Alert.Webhooks.Activity)
}

// resolveListingCallbackSecret mirrors the Delivery.Top30WebhookURLEnv
// pattern: if the operator left Callback.Secret blank in YAML but set
// Callback.SecretEnv, pick the secret up from the environment so it
// never has to be persisted to disk. Leaves the existing Secret in
// place when both are configured (yaml wins, matching the precedence
// the delivery webhook uses).
func resolveListingCallbackSecret(cfg *config.Config) {
	cb := &cfg.Runtime.ListingAgent.DecisionCard.Callback
	if cb.Secret != "" || cb.SecretEnv == "" {
		return
	}
	cb.Secret = os.Getenv(cb.SecretEnv)
}

// buildListingEnrichDeps wires the per-candidate enrichment bundle
// the decision-card renderer consumes (PRD §5 — Market Status,
// Metrics, edgeX 状态). Each sub-fetcher is best-effort: any nil
// hook causes the renderer to degrade that block gracefully
// (placeholder text instead of empty values), which matches the
// PRD's "do-not-fail-the-card-on-enrichment-failure" contract.
//
// Depth: operator decision is to use a single platform (Binance) as
// the depth reference rather than fanning out across competitors —
// the value of a second source is low next to the cost of a wider
// latency tail. buildBinanceDepthFetcher synthesises {CANONICAL}USDT
// API symbols and hits Binance's spot + USDM-perp depth endpoints
// directly; the aggregator runs the spot and perp calls in parallel
// with a per-call deadline.
func buildListingEnrichDeps(cfg config.Config, repo *listing.Repository, universeLoader func() *config.ListedUniverse) listing.DecisionCardEnrichDeps {
	deps := listing.DecisionCardEnrichDeps{
		Now: func() time.Time { return time.Now().UTC() },
	}
	// EdgexListedLookup is installed as a *loader-backed* closure so
	// each enrichment pass sees the latest runtime listed_universe
	// (refreshed every 15 min by the listing engine). Without this
	// indirection the lookup would freeze at startup.
	if universeLoader != nil {
		deps.EdgexListedLookup = listing.BuildEdgexListedLookupLoader(universeLoader)
	}
	if repo != nil {
		deps.MarketStatusLoader = listing.BuildMarketStatusLoader(repo)
	}
	cgCfg := cfg.Runtime.CoinGecko
	apiKey := ""
	if cgCfg.APIKeyEnv != "" {
		apiKey = os.Getenv(cgCfg.APIKeyEnv)
	}
	if cgClient, err := coingecko.New(coingecko.Config{
		BaseURL:        cgCfg.BaseURL,
		APIKey:         apiKey,
		Proxy:          cgCfg.Proxy,
		RequestTimeout: 4 * time.Second,
	}); err == nil {
		deps.CoinGeckoFetcher = listing.BuildCoinGeckoFetcher(cgClient)
	} else {
		log.Printf("listing enrich: coingecko client init failed: %v (market-cap/24h-vol will render n/a)", err)
	}
	depthFetcher := buildBinanceDepthFetcher(cfg.Runtime.ExchangeProxy, 1500*time.Millisecond)
	deps.DepthFetcher = listing.BuildDepthFetcher(depthFetcher, 3*time.Second, 1500*time.Millisecond)
	return deps
}

// listingDecisionOptions wires the Phase 2 callback / dispatch
// surface onto the api server. The callback route stays inert
// (returns 503 from listingCallback) when DecisionCard.Enabled is
// false OR the secret is missing OR the operator whitelist is empty;
// all three conditions must hold for the callback to actually accept
// clicks, so a partial yaml does not accidentally open the route.
func listingDecisionOptions(cfg config.Config, repo *listing.Repository) []api.Option {
	dc := cfg.Runtime.ListingAgent.DecisionCard
	opts := []api.Option{
		api.WithListingDecisionWriter(repo),
		api.WithListingDispatch(listing.NewRepoDispatcher(repo, nil)),
	}
	if dc.Enabled && dc.Callback.Secret != "" && len(dc.Callback.OperatorAllow) > 0 {
		opts = append(opts, api.WithListingCallback(api.ListingCallbackConfig{
			Secret:        dc.Callback.Secret,
			MaxClockSkew:  dc.Callback.MaxClockSkew,
			OperatorAllow: append([]string(nil), dc.Callback.OperatorAllow...),
		}))
		log.Printf("listing callback route armed (operators=%d, max_clock_skew=%s)", len(dc.Callback.OperatorAllow), dc.Callback.MaxClockSkew)
	} else {
		log.Printf("listing callback route disabled (enabled=%t, has_secret=%t, operators=%d)",
			dc.Enabled, dc.Callback.Secret != "", len(dc.Callback.OperatorAllow))
	}
	return opts
}
