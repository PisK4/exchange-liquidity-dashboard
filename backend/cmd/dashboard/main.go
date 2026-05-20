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
	"edgex-dashboard/backend/internal/marketdata/coingecko"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	role := flag.String("role", "all", "role: api, collector, or all")
	runOnce := flag.Bool("run-once", false, "run one collection cycle at startup")
	mysqlDSN := flag.String("mysql-dsn", os.Getenv("DASHBOARD_MYSQL_DSN"), "optional MySQL DSN, for example root:root@tcp(127.0.0.1:3306)/edgex_dashboard?parseTime=true")
	configDir := flag.String("config-dir", "../config", "directory containing dashboard yaml configs")
	catalogReloadInterval := flag.Duration("catalog-reload-interval", 2*time.Second, "polling interval for instrument_catalog.yaml hot reload; 0 disables the watcher")
	flag.Parse()

	cfg, err := config.Load(*configDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store := collector.NewStore(cfg)
	if *mysqlDSN != "" {
		db, err := collector.OpenMySQL(*mysqlDSN)
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
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *catalogReloadInterval > 0 {
		catalogPath := filepath.Join(*configDir, "instrument_catalog.yaml")
		go store.WatchCatalog(ctx, catalogPath, *catalogReloadInterval)
		log.Printf("watching %s for frontend metadata hot reload (interval=%s)", catalogPath, *catalogReloadInterval)
	}
	lighterProvider := adapter.NewLighterWSProvider(cfg.Runtime.LighterWSURL, cfg.Runtime.LighterStaleAfter)
	go lighterProvider.Run(ctx, adapter.LighterMarketIDs())
	waitForLighter(ctx, lighterProvider)
	c := collector.NewCollectorWithLighter(cfg, store, lighterProvider)

	if *role == "collector" || *role == "all" {
		if err := c.CollectOnce(ctx); err != nil {
			log.Printf("initial collection completed with errors: %v", err)
		}
		if !*runOnce {
			go func() {
				ticker := time.NewTicker(cfg.Runtime.CollectionInterval)
				defer ticker.Stop()
				for range ticker.C {
					if err := c.CollectOnce(ctx); err != nil {
						log.Printf("collection completed with errors: %v", err)
					}
				}
			}()
		}
		if cfg.Runtime.CoinGecko.Enabled {
			if cgCollector, err := buildCoinGeckoCollector(cfg, store); err != nil {
				log.Printf("coingecko collector disabled: %v", err)
			} else {
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

	if *role == "collector" && *runOnce {
		return
	}

	if *role == "collector" {
		select {}
	}

	server := api.NewServer(cfg, store)
	log.Printf("EdgeX liquidity dashboard API listening on %s", *addr)
	if err := http.ListenAndServe(*addr, server.Routes()); err != nil {
		log.Fatal(err)
	}
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
