package coingecko

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const sampleDerivatives = `[
  {
    "market": "Binance (Futures)",
    "symbol": "BTCUSDT",
    "index_id": "BTC",
    "price": "70000.5",
    "spread": "0.01",
    "open_interest": 5234567890,
    "volume_24h": 12345678901,
    "contract_type": "perpetual",
    "last_traded_at": 1716200000,
    "converted_volume": {"usd": 12345678900}
  },
  {
    "market": "MEXC (Futures)",
    "symbol": "ETHUSDT",
    "index_id": "ETH",
    "price": 3450.25,
    "spread": null,
    "open_interest": "210000000",
    "volume_24h": "987654321",
    "contract_type": "perpetual",
    "last_traded_at": "2026-05-20T01:23:45Z",
    "converted_volume": {"usd": "987654321"}
  }
]`

func TestNewClientRejectsBadProxy(t *testing.T) {
	if _, err := New(Config{Proxy: "://"}); err == nil {
		t.Fatalf("expected proxy parse error, got nil")
	}
	if _, err := New(Config{Proxy: "no-scheme.local"}); err == nil {
		t.Fatalf("expected missing-scheme error, got nil")
	}
}

func TestNewClientWithoutProxyDoesNotUseEnv(t *testing.T) {
	// R1: even with HTTPS_PROXY set, the constructed transport must not
	// pick it up. We assert that by inspecting the transport directly.
	t.Setenv("HTTPS_PROXY", "http://example.invalid:9999")
	t.Setenv("HTTP_PROXY", "http://example.invalid:9999")
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", c.httpClient.Transport)
	}
	if tr.Proxy != nil {
		t.Fatalf("transport.Proxy should be nil when no proxy configured")
	}
}

func TestNewClientWithProxyParsesURL(t *testing.T) {
	c, err := New(Config{Proxy: "http://127.0.0.1:7897"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr := c.httpClient.Transport.(*http.Transport)
	if tr.Proxy == nil {
		t.Fatalf("expected proxy hook to be set")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid/x", nil)
	got, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	want, _ := url.Parse("http://127.0.0.1:7897")
	if got.String() != want.String() {
		t.Fatalf("Proxy URL = %q, want %q", got, want)
	}
}

func TestFetchDerivativesSendsAPIKeyHeader(t *testing.T) {
	var seenHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeader = r.Header.Get(DefaultAPIKeyHeader)
		if !strings.HasSuffix(r.URL.Path, "/derivatives") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("include_tickers") != "unexpired" {
			t.Errorf("missing include_tickers=unexpired query")
		}
		_, _ = w.Write([]byte(sampleDerivatives))
	}))
	defer srv.Close()

	c, err := New(Config{
		BaseURL:        srv.URL,
		APIKey:         "TEST-DEMO-KEY",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tickers, endpoint, err := c.FetchDerivatives(ctx)
	if err != nil {
		t.Fatalf("FetchDerivatives: %v", err)
	}
	if seenHeader != "TEST-DEMO-KEY" {
		t.Fatalf("API key header = %q", seenHeader)
	}
	if !strings.HasSuffix(endpoint, "/derivatives?include_tickers=unexpired") {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if len(tickers) != 2 {
		t.Fatalf("expected 2 tickers, got %d", len(tickers))
	}
	if tickers[0].Market != "Binance (Futures)" || tickers[0].Symbol != "BTCUSDT" {
		t.Fatalf("first ticker = %+v", tickers[0])
	}
	if got := tickers[0].Volume24HUSD(); got != 12345678900 {
		t.Fatalf("ticker0 Volume24HUSD = %v", got)
	}
	if got := tickers[1].Volume24HUSD(); got != 987654321 {
		t.Fatalf("ticker1 Volume24HUSD = %v", got)
	}
	if tickers[1].LastTradedAt.Time().IsZero() {
		t.Fatalf("ticker1 last_traded_at should parse RFC3339")
	}
}

func TestFetchDerivativesRateLimitSurfaces429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":{"error_code":429}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, RequestTimeout: 2 * time.Second})
	_, _, err := c.FetchDerivatives(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !IsRateLimited(err) {
		t.Fatalf("IsRateLimited should be true, got err=%v", err)
	}
}

func TestFetchDerivativesHTTPErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, RequestTimeout: 2 * time.Second})
	_, _, err := c.FetchDerivatives(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if IsRateLimited(err) {
		t.Fatalf("500 should not be classified as rate limited")
	}
}

func TestFlexibleNumberAcceptsStringAndNumeric(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{`12345.5`, 12345.5},
		{`"12345.5"`, 12345.5},
		{`null`, 0},
		{`""`, 0},
	}
	for _, c := range cases {
		var v FlexibleNumber
		if err := json.Unmarshal([]byte(c.in), &v); err != nil {
			t.Fatalf("unmarshal %q: %v", c.in, err)
		}
		if float64(v) != c.want {
			t.Fatalf("FlexibleNumber(%q) = %v, want %v", c.in, float64(v), c.want)
		}
	}
}

func TestFlexibleTimeAcceptsEpochAndRFC3339(t *testing.T) {
	var epoch FlexibleTime
	if err := json.Unmarshal([]byte(`1716200000`), &epoch); err != nil {
		t.Fatalf("epoch unmarshal: %v", err)
	}
	if epoch.Time().Unix() != 1716200000 {
		t.Fatalf("epoch unix = %d", epoch.Time().Unix())
	}
	var rfc FlexibleTime
	if err := json.Unmarshal([]byte(`"2026-05-20T01:23:45Z"`), &rfc); err != nil {
		t.Fatalf("rfc unmarshal: %v", err)
	}
	if rfc.Time().UTC().Format(time.RFC3339) != "2026-05-20T01:23:45Z" {
		t.Fatalf("rfc time = %s", rfc.Time())
	}
	var zero FlexibleTime
	if err := json.Unmarshal([]byte(`null`), &zero); err != nil {
		t.Fatalf("null unmarshal: %v", err)
	}
	if !zero.Time().IsZero() {
		t.Fatalf("null should produce zero time")
	}
}

func TestTickerCacheHonoursTTL(t *testing.T) {
	cache := NewTickerCache(10 * time.Millisecond)
	now := time.Now()
	cache.Put(now, []Ticker{{Market: "x"}}, "endpoint")
	if got, _, ok := cache.Get(now.Add(5 * time.Millisecond)); !ok || len(got) != 1 {
		t.Fatalf("cache should be fresh: ok=%v got=%v", ok, got)
	}
	if _, _, ok := cache.Get(now.Add(50 * time.Millisecond)); ok {
		t.Fatalf("cache should be stale after TTL")
	}
	disabled := NewTickerCache(0)
	disabled.Put(now, []Ticker{{Market: "y"}}, "endpoint")
	if _, _, ok := disabled.Get(now); ok {
		t.Fatalf("zero-ttl cache must always miss")
	}
}

func TestFetchExchangeVolumeChartParsesPairs(t *testing.T) {
	var seenPath, seenHeader, seenDays string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenHeader = r.Header.Get(DefaultAPIKeyHeader)
		seenDays = r.URL.Query().Get("days")
		_, _ = w.Write([]byte(`[[1716163200000,"123.45"],[1716249600000,"678.90"]]`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, APIKey: "K", RequestTimeout: 2 * time.Second})
	pts, endpoint, err := c.FetchExchangeVolumeChart(context.Background(), "binance_futures", 30)
	if err != nil {
		t.Fatalf("FetchExchangeVolumeChart: %v", err)
	}
	if seenPath != "/exchanges/binance_futures/volume_chart" {
		t.Fatalf("path = %q", seenPath)
	}
	if seenHeader != "K" {
		t.Fatalf("api key header = %q", seenHeader)
	}
	if seenDays != "30" {
		t.Fatalf("days = %q", seenDays)
	}
	if !strings.Contains(endpoint, "/exchanges/binance_futures/volume_chart?") {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
	if pts[0].TimestampMS != 1716163200000 || pts[0].VolumeBTC != 123.45 {
		t.Fatalf("point0 = %+v", pts[0])
	}
	if pts[1].VolumeBTC != 678.90 {
		t.Fatalf("point1 volume = %v", pts[1].VolumeBTC)
	}
}

func TestFetchExchangeVolumeChartRejectsEmptyID(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://example", RequestTimeout: time.Second})
	if _, _, err := c.FetchExchangeVolumeChart(context.Background(), "  ", 30); err == nil {
		t.Fatalf("expected error for empty exchange id")
	}
}

func TestFetchBitcoinPriceChartRangeParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("vs_currency"); got != "usd" {
			t.Errorf("vs_currency = %q", got)
		}
		_, _ = w.Write([]byte(`{"prices":[[1716163200000,69050.5],[1716249600000,68000.0]],"market_caps":[],"total_volumes":[]}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, RequestTimeout: 2 * time.Second})
	from := time.Unix(1716163200, 0).UTC()
	to := time.Unix(1716336000, 0).UTC()
	pts, endpoint, err := c.FetchBitcoinPriceChartRange(context.Background(), from, to)
	if err != nil {
		t.Fatalf("FetchBitcoinPriceChartRange: %v", err)
	}
	if !strings.Contains(endpoint, "/coins/bitcoin/market_chart/range?") {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if len(pts) != 2 || pts[0].PriceUSD != 69050.5 || pts[1].PriceUSD != 68000.0 {
		t.Fatalf("price points = %+v", pts)
	}
}

func TestFetchExchangeVolumeChartSurfaces429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":{"error_code":429}}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, RequestTimeout: 2 * time.Second})
	_, _, err := c.FetchExchangeVolumeChart(context.Background(), "binance_futures", 30)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsRateLimited(err) {
		t.Fatalf("expected rate-limited, got %v", err)
	}
}
