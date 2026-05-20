package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"edgex-dashboard/backend/internal/adapter"
	"edgex-dashboard/backend/internal/api"
	"edgex-dashboard/backend/internal/collector"
	"edgex-dashboard/backend/internal/config"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	role := flag.String("role", "all", "role: api, collector, or all")
	runOnce := flag.Bool("run-once", false, "run one collection cycle at startup")
	mysqlDSN := flag.String("mysql-dsn", os.Getenv("DASHBOARD_MYSQL_DSN"), "optional MySQL DSN, for example root:root@tcp(127.0.0.1:3306)/edgex_dashboard?parseTime=true")
	flag.Parse()

	cfg, err := config.Load("../config")
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
		if *role == "api" {
			if err := store.LoadLatestFromDB(context.Background()); err != nil {
				log.Printf("load latest snapshots from mysql: %v", err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
