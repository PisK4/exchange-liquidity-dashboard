package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"edgex-dashboard/backend/internal/adapter"
	"edgex-dashboard/backend/internal/api"
	"edgex-dashboard/backend/internal/collector"
	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/listing"
	"edgex-dashboard/backend/internal/marketdata/coingecko"
)

// version is set at link time via -ldflags. Falls back to "dev" for
// local `go run` invocations.
//
//nolint:gochecknoglobals  // ldflags-injected build identifier
var version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	role := flag.String("role", "all", "role: api, collector, or all")
	runOnce := flag.Bool("run-once", false, "run one collection cycle at startup")
	mysqlDSN := flag.String("mysql-dsn", os.Getenv("DASHBOARD_MYSQL_DSN"), "optional MySQL DSN, for example root:root@tcp(127.0.0.1:3306)/edgex_dashboard?parseTime=true")
	configDir := flag.String("config-dir", "../config", "directory containing dashboard yaml configs")
	catalogReloadInterval := flag.Duration("catalog-reload-interval", 2*time.Second, "polling interval for instrument_catalog.yaml hot reload; 0 disables the watcher")
	rawInstrumentsDir := flag.String("raw-instruments-dir", "docs/raw-instruments", "directory containing per-platform raw instrument dumps used by Top30 backfill")
	showVersion := flag.Bool("version", false, "print the embedded build version and exit")
	flag.Parse()

	if *showVersion {
		log.Printf("edgex-dashboard %s", version)
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
	} else {
		if roleRequiresMySQL(*role) {
			log.Fatalf("role %q requires MySQL DSN (--mysql-dsn or DASHBOARD_MYSQL_DSN)", *role)
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
		go lighterProvider.Run(ctx, adapter.LighterMarketIDs())
		waitForLighter(ctx, lighterProvider)
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
			resolver := collector.NewCatalogResolver(*rawInstrumentsDir)
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
				universePath := filepath.Join(*configDir, "listed_universe.yaml")
				universe, uErr := config.LoadListedUniverse(universePath)
				if uErr != nil {
					log.Printf("listed_universe load failed (%s): %v; Top30 'edgeX 已上线?' column will fall back to false", universePath, uErr)
				} else if !universe.Loaded() {
					log.Printf("listed_universe.yaml not found at %s; Top30 'edgeX 已上线?' column will fall back to false until `make catalog`", universePath)
				} else {
					log.Printf("listed_universe.yaml loaded (%d platforms, edgeX bases=%d)",
						len(universe.Platforms), len(universe.BaseAssets("edgeX")))
				}
				cgCollector.SetListedUniverse(universe)
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

	universePath := filepath.Join(*configDir, "listed_universe.yaml")
	listingUniverseLoader := func() (*config.ListedUniverse, error) {
		return config.LoadListedUniverse(universePath)
	}
	if roleStartsListing(*role) && cfg.Runtime.ListingAgent.Enabled && listingRepo != nil {
		engine := listing.NewEngine(cfg, listingRepo, listing.EngineDeps{
			LoadUniverse: listingUniverseLoader,
			HTTPClient:   http.DefaultClient,
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

	if *role == "collector" || *role == "listing" {
		select {}
	}

	opts := []api.Option{}
	if listingRepo != nil {
		opts = append(opts, api.WithListingReader(listingRepo))
	}
	server := api.NewServer(cfg, store, opts...)
	log.Printf("EdgeX liquidity dashboard API listening on %s", *addr)
	if err := http.ListenAndServe(*addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
}

// roleStartsListing reports whether the listing engine should be
// started for the given --role flag.
func roleStartsListing(role string) bool {
	return role == "listing" || role == "collector" || role == "all"
}

// roleRequiresMySQL reports whether the role cannot operate without a
// MySQL DSN. The listing role is the only role that fail-stops on
// missing DSN today; collector / all degrade gracefully.
func roleRequiresMySQL(role string) bool {
	return role == "listing"
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

func waitForLighter(ctx context.Context, provider *adapter.LighterWSProvider) {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if provider.ReadyCount(adapter.LighterMarketIDs()) == len(adapter.LighterMarketIDs()) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			log.Printf("lighter ws not fully ready before initial collection; continuing with statusized failures")
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
