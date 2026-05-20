package collector

import (
	"context"
	"fmt"
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

// newBackfillTestCollector wires a fake CoinGecko server that returns a
// /coins/bitcoin/market_chart/range payload plus per-exchange
// /volume_chart/range payloads, then constructs a CoinGeckoCollector pointed
// at it. The fake server fields requests by path, so callers can assert on
// the persisted daily aggregates after BackfillVolumeHistory runs.
func newBackfillTestCollector(t *testing.T, volByExchange map[string][]coingecko.VolumeChartPoint, btc []coingecko.PricePoint) (*CoinGeckoCollector, *Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case path == "/coins/bitcoin/market_chart/range":
			prices := "["
			for i, p := range btc {
				if i > 0 {
					prices += ","
				}
				prices += fmt.Sprintf("[%d,%v]", p.TimestampMS, p.PriceUSD)
			}
			prices += "]"
			_, _ = w.Write([]byte(`{"prices":` + prices + `,"market_caps":[],"total_volumes":[]}`))
		case len(path) > len("/exchanges/") && path[:len("/exchanges/")] == "/exchanges/" && len(path) > len("/volume_chart") && path[len(path)-len("/volume_chart"):] == "/volume_chart":
			// path = /exchanges/<id>/volume_chart
			id := path[len("/exchanges/") : len(path)-len("/volume_chart")]
			points, ok := volByExchange[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			body := "["
			for i, p := range points {
				if i > 0 {
					body += ","
				}
				body += fmt.Sprintf(`[%d,"%v"]`, p.TimestampMS, p.VolumeBTC)
			}
			body += "]"
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unrecognised path ` + path + `"}`))
		}
	}))
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX", "binance", "mexc", "lighter"}
	cfg.Runtime.CoinGecko.Enabled = true
	cfg.Runtime.CoinGecko.PullInterval = time.Hour
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
	col := NewCoinGeckoCollector(cfg.Runtime.CoinGecko, store, client, mapping)
	return col, store, srv
}

// withZeroPace crushes the backfill pacing delay and retry backoffs to
// near-zero so tests run in milliseconds rather than minutes. Restored on
// test cleanup.
func withZeroPace(t *testing.T) {
	t.Helper()
	prevPace := backfillRequestPaceMin
	prevBackoffs := backfillRetryBackoffs
	backfillRequestPaceMin = 0
	backfillRetryBackoffs = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() {
		backfillRequestPaceMin = prevPace
		backfillRetryBackoffs = prevBackoffs
	})
}

func TestBackfillVolumeHistoryConvertsBTCtoUSDAndPersists(t *testing.T) {
	withZeroPace(t)
	day0 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)
	btc := []coingecko.PricePoint{
		{TimestampMS: day0.UnixMilli(), PriceUSD: 60000},
		{TimestampMS: day1.UnixMilli(), PriceUSD: 70000},
	}
	vol := map[string][]coingecko.VolumeChartPoint{
		"binance_futures":   {{TimestampMS: day0.UnixMilli(), VolumeBTC: 10}, {TimestampMS: day1.UnixMilli(), VolumeBTC: 20}},
		"mxc_futures":       {{TimestampMS: day0.UnixMilli(), VolumeBTC: 5}, {TimestampMS: day1.UnixMilli(), VolumeBTC: 7}},
		"lighter_perpetual": {{TimestampMS: day0.UnixMilli(), VolumeBTC: 1}},
	}
	col, store, srv := newBackfillTestCollector(t, vol, btc)
	defer srv.Close()
	if err := col.BackfillVolumeHistory(context.Background(), 7); err != nil {
		t.Fatalf("BackfillVolumeHistory: %v", err)
	}
	store.mu.RLock()
	binance := append([]domain.DailyVolumeAggregate(nil), store.dailyPlatformVolumes["binance"]...)
	mexc := append([]domain.DailyVolumeAggregate(nil), store.dailyPlatformVolumes["mexc"]...)
	lighter := append([]domain.DailyVolumeAggregate(nil), store.dailyPlatformVolumes["lighter"]...)
	store.mu.RUnlock()

	if len(binance) != 2 {
		t.Fatalf("binance expected 2 daily rows, got %d (%+v)", len(binance), binance)
	}
	got := map[time.Time]domain.DailyVolumeAggregate{}
	for _, r := range binance {
		got[r.Day] = r
	}
	if got[day0].Volume24HUSD != 600000 {
		t.Fatalf("binance day0 = %v, want 600000 (10 BTC × $60k)", got[day0].Volume24HUSD)
	}
	if got[day1].Volume24HUSD != 1400000 {
		t.Fatalf("binance day1 = %v, want 1400000 (20 BTC × $70k)", got[day1].Volume24HUSD)
	}
	if got[day0].DataSource != domain.DataSourceCoinGeckoBackfill {
		t.Fatalf("binance day0 data_source = %q, want %q", got[day0].DataSource, domain.DataSourceCoinGeckoBackfill)
	}
	if len(mexc) != 2 || len(lighter) != 1 {
		t.Fatalf("mexc/lighter row counts = %d/%d", len(mexc), len(lighter))
	}
}

func TestBackfillVolumeHistorySkipsDaysWithoutBTCPrice(t *testing.T) {
	withZeroPace(t)
	day0 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)
	// BTC price only covers day0; day1 must be skipped.
	btc := []coingecko.PricePoint{{TimestampMS: day0.UnixMilli(), PriceUSD: 60000}}
	vol := map[string][]coingecko.VolumeChartPoint{
		"binance_futures": {{TimestampMS: day0.UnixMilli(), VolumeBTC: 10}, {TimestampMS: day1.UnixMilli(), VolumeBTC: 20}},
	}
	col, store, srv := newBackfillTestCollector(t, vol, btc)
	defer srv.Close()
	if err := col.BackfillVolumeHistory(context.Background(), 7); err != nil {
		t.Fatalf("BackfillVolumeHistory: %v", err)
	}
	store.mu.RLock()
	binance := append([]domain.DailyVolumeAggregate(nil), store.dailyPlatformVolumes["binance"]...)
	store.mu.RUnlock()
	if len(binance) != 1 {
		t.Fatalf("expected 1 row (day1 dropped), got %d (%+v)", len(binance), binance)
	}
	if !binance[0].Day.Equal(day0) {
		t.Fatalf("expected day0, got %v", binance[0].Day)
	}
}

func TestBackfillVolumeHistoryClampsDaysRange(t *testing.T) {
	withZeroPace(t)
	day0 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	btc := []coingecko.PricePoint{{TimestampMS: day0.UnixMilli(), PriceUSD: 60000}}
	vol := map[string][]coingecko.VolumeChartPoint{"binance_futures": {{TimestampMS: day0.UnixMilli(), VolumeBTC: 1}}}
	col, _, srv := newBackfillTestCollector(t, vol, btc)
	defer srv.Close()
	if err := col.BackfillVolumeHistory(context.Background(), 365); err != nil {
		t.Fatalf("BackfillVolumeHistory with overflow days should not error: %v", err)
	}
}

func TestNextDailyBackfillDelayAlwaysFuture(t *testing.T) {
	cases := []time.Time{
		time.Date(2026, 5, 20, 0, 30, 0, 0, time.UTC),  // before 01:00 → 30m
		time.Date(2026, 5, 20, 1, 0, 0, 0, time.UTC),   // equals 01:00 → next day
		time.Date(2026, 5, 20, 23, 30, 0, 0, time.UTC), // far past 01:00 → next day
	}
	for _, now := range cases {
		d := nextDailyBackfillDelay(now)
		if d <= 0 {
			t.Fatalf("delay must be strictly positive for %v, got %v", now, d)
		}
		if d > 25*time.Hour {
			t.Fatalf("delay must be < 25h, got %v", d)
		}
	}
}
