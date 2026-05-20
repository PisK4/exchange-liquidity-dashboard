package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
	"edgex-dashboard/backend/internal/marketdata/coingecko"
)

// fakeDerivativesPayload covers 3 platforms (binance, mexc, lighter) so the
// collector exercises both the multi-platform aggregation path and the
// unknown-market filter (Bitget below is intentionally NOT in the test cfg).
//
// Binance lists BTCUSDT twice with different volumes to exercise the
// best-per-symbol dedup branch (max wins).
const fakeDerivativesPayload = `[
  {"market":"Binance (Futures)","symbol":"BTCUSDT","volume_24h":1000,"open_interest":500,"converted_volume":{"usd":1000}},
  {"market":"Binance (Futures)","symbol":"BTCUSDT","volume_24h":900,"open_interest":250,"converted_volume":{"usd":900}},
  {"market":"Binance (Futures)","symbol":"ETHUSDT","volume_24h":800,"open_interest":300,"converted_volume":{"usd":800}},
  {"market":"MEXC (Futures)","symbol":"BTCUSDT","volume_24h":600,"open_interest":200,"converted_volume":{"usd":600}},
  {"market":"MEXC (Futures)","symbol":"ETHUSDT","volume_24h":400,"open_interest":100,"converted_volume":{"usd":400}},
  {"market":"Lighter Perpetual","symbol":"BTCUSDT","volume_24h":300,"open_interest":50,"converted_volume":{"usd":300}},
  {"market":"Bitget (Futures)","symbol":"BTCUSDT","volume_24h":99999,"open_interest":0,"converted_volume":{"usd":99999}}
]`

func newTestCoinGeckoCollector(t *testing.T) (*CoinGeckoCollector, *Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fakeDerivativesPayload))
	}))
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "mexc", "lighter"}
	cfg.Runtime.CoinGecko.Enabled = true
	cfg.Runtime.CoinGecko.PullInterval = 50 * time.Millisecond
	cfg.Runtime.CoinGecko.CacheTTL = 0
	cfg.Runtime.CoinGecko.BaseURL = srv.URL
	cfg.Runtime.CoinGecko.ExchangeID = map[string]string{
		"binance": "binance_futures",
		"mexc":    "mxc_futures",
		"lighter": "lighter_perpetual",
	}
	cfg.Runtime.CoinGecko.MarketName = map[string]string{
		"binance": "Binance (Futures)",
		"mexc":    "MEXC (Futures)",
		"lighter": "Lighter Perpetual",
	}
	store := NewStore(cfg)
	client, err := coingecko.New(coingecko.Config{BaseURL: srv.URL, RequestTimeout: 2 * time.Second})
	if err != nil {
		srv.Close()
		t.Fatalf("client: %v", err)
	}
	mapping := coingecko.NewMapping(cfg.Runtime.CoinGecko.ExchangeID, cfg.Runtime.CoinGecko.MarketName)
	collector := NewCoinGeckoCollector(cfg.Runtime.CoinGecko, store, client, mapping)
	return collector, store, srv
}

func TestCoinGeckoCollectorAggregatesPerPlatform(t *testing.T) {
	col, store, srv := newTestCoinGeckoCollector(t)
	defer srv.Close()
	if err := col.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	// Binance: max(BTCUSDT:1000, BTC-USDT-PERP:900)=1000 + ETHUSDT:800 = 1800
	// MEXC:    600 + 400 = 1000
	// Lighter: 300
	share := store.Share("24h")
	rows := share["rows"].([]map[string]any)
	byPlatform := map[string]map[string]any{}
	for _, r := range rows {
		byPlatform[r["platform"].(string)] = r
	}
	if v := byPlatform["binance"]["raw_volume_usd"].(float64); v != 1800 {
		t.Fatalf("binance raw expected 1800 (dedup canonical perp), got %v", v)
	}
	if v := byPlatform["mexc"]["raw_volume_usd"].(float64); v != 1000 {
		t.Fatalf("mexc raw expected 1000, got %v", v)
	}
	if v := byPlatform["lighter"]["raw_volume_usd"].(float64); v != 300 {
		t.Fatalf("lighter raw expected 300, got %v", v)
	}
	if byPlatform["binance"]["data_source"] != domain.DataSourceCoinGecko {
		t.Fatalf("binance row must carry data_source=coingecko, got %+v", byPlatform["binance"])
	}
}

func TestCoinGeckoCollectorBuildsTop30PerPlatform(t *testing.T) {
	col, store, srv := newTestCoinGeckoCollector(t)
	defer srv.Close()
	if err := col.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	top := store.Top30("perp", "binance")
	if top["status"] != domain.StatusComplete {
		t.Fatalf("binance top30 status expected complete, got %+v", top)
	}
	rows := top["rows"].([]domain.Top30Row)
	if len(rows) != 2 {
		t.Fatalf("binance top30 should have 2 distinct symbols, got %d (%+v)", len(rows), rows)
	}
	// Best-per-symbol picked the higher BTC entry (1000 over 900).
	if rows[0].Symbol != "BTC-USDT (perp)" || rows[0].Volume24HUSD != 1000 {
		t.Fatalf("top1 expected BTC@1000, got %+v", rows[0])
	}
	if rows[1].Symbol != "ETH-USDT (perp)" || rows[1].Volume24HUSD != 800 {
		t.Fatalf("top2 expected ETH@800, got %+v", rows[1])
	}
	if rows[0].DataSource != domain.DataSourceCoinGecko {
		t.Fatalf("top30 data_source expected coingecko, got %+v", rows[0])
	}
	if rows[0].Volume7DStatus != domain.StatusInsufficientHistory {
		t.Fatalf("7d status expected insufficient_history on fresh boot, got %+v", rows[0])
	}
}

func TestCoinGeckoCollectorEmitsDailyUPSERT(t *testing.T) {
	col, store, srv := newTestCoinGeckoCollector(t)
	defer srv.Close()
	if err := col.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	// Second call same UTC day must REPLACE, not append: assertion is that
	// after two CollectOnce calls there is exactly one daily row per platform.
	if err := col.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce(2): %v", err)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, platform := range []string{"binance", "mexc", "lighter"} {
		rows := store.dailyPlatformVolumes[platform]
		if len(rows) != 1 {
			t.Fatalf("%s daily rollup must collapse to 1 row after re-run, got %d", platform, len(rows))
		}
		if rows[0].DataSource != domain.DataSourceCoinGecko {
			t.Fatalf("%s daily row data_source expected coingecko, got %+v", platform, rows[0])
		}
	}
}

func TestCoinGeckoCollectorAdvancesLastPullTS(t *testing.T) {
	col, store, srv := newTestCoinGeckoCollector(t)
	defer srv.Close()
	before := time.Now().UTC()
	if err := col.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	meta := store.DashboardMeta()
	ds := meta["data_sources"].(map[string]any)
	cg := ds["coingecko"].(map[string]any)
	last, ok := cg["last_pull_ts"].(time.Time)
	if !ok {
		t.Fatalf("last_pull_ts missing or wrong type, got %T", cg["last_pull_ts"])
	}
	if last.Before(before) {
		t.Fatalf("last_pull_ts %s should be >= %s", last, before)
	}
}

func TestCoinGeckoCollectorRateLimitedReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":{"error_code":429}}`))
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Runtime.CoinGecko.Enabled = true
	cfg.Runtime.CoinGecko.MarketName = map[string]string{"binance": "Binance (Futures)"}
	cfg.Runtime.CoinGecko.ExchangeID = map[string]string{"binance": "binance_futures"}
	cfg.Runtime.CoinGecko.BaseURL = srv.URL
	store := NewStore(cfg)
	client, _ := coingecko.New(coingecko.Config{BaseURL: srv.URL, RequestTimeout: time.Second})
	mapping := coingecko.NewMapping(cfg.Runtime.CoinGecko.ExchangeID, cfg.Runtime.CoinGecko.MarketName)
	col := NewCoinGeckoCollector(cfg.Runtime.CoinGecko, store, client, mapping)
	err := col.CollectOnce(context.Background())
	if err == nil {
		t.Fatalf("expected error from 429 response")
	}
	if !coingecko.IsRateLimited(err) {
		t.Fatalf("error should be classified as rate limited, got %v", err)
	}
}

func TestCoinGeckoCollectorIgnoresUnknownMarkets(t *testing.T) {
	col, store, srv := newTestCoinGeckoCollector(t)
	defer srv.Close()
	if err := col.CollectOnce(context.Background()); err != nil {
		t.Fatalf("CollectOnce: %v", err)
	}
	// Bitget is in the payload but NOT in cfg.MarketName, so Top30 for
	// bitget must come back unsupported.
	top := store.Top30("perp", "bitget")
	if top["status"] != domain.StatusUnsupported {
		t.Fatalf("bitget should remain unsupported, got %+v", top)
	}
}
