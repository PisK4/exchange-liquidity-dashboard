// Command coingecko-backfill issues a one-shot historical pull against
// CoinGecko's /exchanges/{id}/volume_chart/range plus
// /coins/bitcoin/market_chart/range and UPSERTs the resulting USD daily
// volumes into t_daily_volume_aggregate with data_source="coingecko_backfill".
//
// Usage:
//
//	make backfill-share                              # default 30 days
//	go run ./cmd/coingecko-backfill --days=7         # custom window
//	OPS_INTELLIGENCE_MYSQL_DSN=root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true \
//	    go run ./cmd/coingecko-backfill --days=30
//
// The backfill is idempotent: rows where a live coingecko/native daily
// aggregate already exists are skipped, so re-running this command never
// overwrites fresh data.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"edgex-ops-intelligence/backend/internal/collector"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/marketdata/coingecko"
)

func main() {
	days := flag.Int("days", 30, "number of days to backfill (clamped 1..30)")
	configDir := flag.String("config-dir", "../config", "directory containing EdgeX Ops Intelligence yaml configs")
	mysqlDSN := flag.String("mysql-dsn", os.Getenv("OPS_INTELLIGENCE_MYSQL_DSN"), "MySQL DSN for the EdgeX Ops Intelligence schema")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall timeout for the backfill run")
	flag.Parse()

	cfg, err := config.Load(*configDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cgCfg := cfg.Runtime.CoinGecko
	if cgCfg.BaseURL == "" {
		cgCfg.BaseURL = coingecko.DefaultBaseURL
	}

	store := collector.NewStore(cfg)
	resolvedDSN := *mysqlDSN
	if resolvedDSN == "" {
		resolvedDSN = cfg.MySQLDSN()
	}
	if resolvedDSN == "" {
		log.Fatalf("--mysql-dsn, OPS_INTELLIGENCE_MYSQL_DSN, or Database config is required: backfill must persist to MySQL")
	}
	db, err := collector.OpenMySQL(resolvedDSN)
	if err != nil {
		log.Fatalf("connect mysql: %v", err)
	}
	defer db.Close()
	if err := collector.ApplyMigrations(db); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	store.AttachDB(db)
	if err := store.LoadLatestFromDB(context.Background()); err != nil {
		log.Printf("load latest snapshots from mysql: %v", err)
	}

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
		log.Fatalf("build client: %v", err)
	}
	mapping := coingecko.NewMapping(cgCfg.ExchangeID, cgCfg.MarketName)
	col := collector.NewCoinGeckoCollector(cgCfg, store, client, mapping)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	log.Printf("coingecko backfill starting: days=%d base=%s proxy=%q api_key_set=%t",
		*days, cgCfg.BaseURL, cgCfg.Proxy, apiKey != "")
	if err := col.BackfillVolumeHistory(ctx, *days); err != nil {
		log.Fatalf("backfill failed: %v", err)
	}
	log.Printf("coingecko backfill completed")
}
