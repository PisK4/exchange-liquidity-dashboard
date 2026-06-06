package adapter

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
)

var smokeSymbol = flag.String("symbol", "BTC-USDT", "symbol for live adapter smoke")

func TestLiveAdapterSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if os.Getenv("LIVE_ADAPTER_SMOKE") != "1" {
		t.Skip("set LIVE_ADAPTER_SMOKE=1 to run real exchange adapter smoke tests")
	}
	provider := NewLighterWSProvider("", 20*time.Second)
	providerCtx, providerCancel := context.WithCancel(context.Background())
	defer providerCancel()
	go provider.Run(providerCtx, LighterMarketIDs())
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && provider.ReadyCount(LighterMarketIDs()) < len(LighterMarketIDs()) {
		time.Sleep(250 * time.Millisecond)
	}

	platforms := []string{"binance", "okx", "bybit", "bitget", "bingx", "mexc", "gate", "hyperliquid", "edgeX", "lighter"}
	for _, p := range platforms {
		sub := domain.SymbolSub{DisplaySymbol: *smokeSymbol + " (perp)", Platform: p, APISymbol: strings.ReplaceAll(*smokeSymbol, "-", ""), SourceEndpoint: "live-smoke"}
		if p == "okx" {
			sub.APISymbol = *smokeSymbol + "-SWAP"
		}
		if p == "bingx" {
			sub.APISymbol = *smokeSymbol
		}
		if p == "mexc" || p == "gate" {
			sub.APISymbol = strings.ReplaceAll(*smokeSymbol, "-", "_")
		}
		if p == "hyperliquid" {
			sub.APISymbol = strings.TrimSuffix(*smokeSymbol, "-USDT")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		book, err := NewWithLighter(p, 12*time.Second, provider).FetchOrderBook(ctx, sub)
		cancel()
		t.Logf("%s status=%s levels=%d err=%v endpoint=%s", p, book.DepthStatus, book.LevelsReturned, err, book.SourceEndpoint)
	}
}
