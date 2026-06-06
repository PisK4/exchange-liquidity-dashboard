// Command coingecko-smoke performs a single CoinGecko /derivatives pull
// and prints a triage table so an operator can sanity-check the live
// integration without booting the full dashboard.
//
// Usage:
//
//	make smoke-coingecko COINGECKO_PROXY=http://127.0.0.1:7897
//
// or directly:
//
//	COINGECKO_PROXY=... COINGECKO_API_KEY_ENV=COINGECKO_DEMO_API_KEY \
//	   go run ./cmd/coingecko-smoke
//
// Output sections:
//   - total ticker count and HTTP endpoint used
//   - number of tickers per recognised internal platform
//   - top 5 (symbol, volume_24h_usd) per recognised internal platform
//   - up to 10 unmapped market_name samples
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/marketdata/coingecko"
)

func main() {
	cfg, err := config.Load("../config")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	cg := cfg.Runtime.CoinGecko
	if !cg.Enabled {
		log.Printf("warning: cfg.Runtime.CoinGecko.Enabled is false; the smoke target still proceeds but the dashboard would not start the collector with this config")
	}
	apiKey := ""
	if envName := cg.APIKeyEnv; envName != "" {
		apiKey = os.Getenv(envName)
	}
	proxy := os.Getenv("COINGECKO_PROXY")
	if proxy == "" {
		proxy = cg.Proxy
	}
	timeout := cg.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client, err := coingecko.New(coingecko.Config{
		BaseURL:        cg.BaseURL,
		APIKey:         apiKey,
		Proxy:          proxy,
		RequestTimeout: timeout,
	})
	if err != nil {
		log.Fatalf("build client: %v", err)
	}
	mapping := coingecko.NewMapping(cg.ExchangeID, cg.MarketName)

	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()
	tickers, endpoint, err := client.FetchDerivatives(ctx)
	if err != nil {
		log.Fatalf("FetchDerivatives: %v", err)
	}
	fmt.Printf("endpoint: %s\n", endpoint)
	fmt.Printf("api_key_env: %s (set=%t)\n", cg.APIKeyEnv, apiKey != "")
	fmt.Printf("proxy: %q\n", proxy)
	fmt.Printf("tickers returned: %d\n\n", len(tickers))

	type bucket struct {
		platform string
		tickers  []coingecko.Ticker
	}
	buckets := map[string]*bucket{}
	unknown := map[string]int{}
	for _, t := range tickers {
		p, ok := mapping.PlatformByMarketName(t.Market)
		if !ok {
			unknown[strings.TrimSpace(t.Market)]++
			continue
		}
		b, exists := buckets[p]
		if !exists {
			b = &bucket{platform: p}
			buckets[p] = b
		}
		b.tickers = append(b.tickers, t)
	}

	platforms := make([]string, 0, len(buckets))
	for k := range buckets {
		platforms = append(platforms, k)
	}
	sort.Strings(platforms)
	fmt.Printf("matched platforms: %d / %d configured\n", len(platforms), len(cg.MarketName))
	for _, platform := range platforms {
		b := buckets[platform]
		sort.Slice(b.tickers, func(i, j int) bool { return b.tickers[i].Volume24HUSD() > b.tickers[j].Volume24HUSD() })
		fmt.Printf("\n[%s] %d tickers\n", platform, len(b.tickers))
		limit := len(b.tickers)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			t := b.tickers[i]
			fmt.Printf("  %2d. %-24s vol_24h_usd=%-15.0f oi_usd=%-15.0f symbol_raw=%s\n",
				i+1, coingecko.NormaliseSymbol(t.Symbol), t.Volume24HUSD(), t.OpenInterestUSD(), t.Symbol)
		}
	}

	if len(unknown) > 0 {
		type entry struct {
			name  string
			count int
		}
		es := make([]entry, 0, len(unknown))
		for k, v := range unknown {
			es = append(es, entry{name: k, count: v})
		}
		sort.Slice(es, func(i, j int) bool { return es[i].count > es[j].count })
		limit := len(es)
		if limit > 10 {
			limit = 10
		}
		fmt.Printf("\nunmapped markets (top %d / %d total):\n", limit, len(es))
		for i := 0; i < limit; i++ {
			fmt.Printf("  %s × %d\n", es[i].name, es[i].count)
		}
	}
}
