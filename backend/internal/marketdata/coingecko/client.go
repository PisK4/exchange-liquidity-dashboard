package coingecko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures the CoinGecko derivatives client.
//
// Proxy is interpreted as a literal URL. When non-empty the client builds its
// own *http.Transport with that proxy; when empty the client falls back to a
// plain custom transport that explicitly does NOT consult process-level
// HTTPS_PROXY / HTTP_PROXY env vars (R1 from the v2 plan).
type Config struct {
	BaseURL        string
	APIKey         string
	APIKeyHeader   string
	Proxy          string
	RequestTimeout time.Duration
	UserAgent      string
	Governor       *BudgetGovernor
}

// DefaultAPIKeyHeader is the canonical header CoinGecko Demo and Pro keys use.
const (
	DefaultAPIKeyHeader = "x-cg-demo-api-key"
	DefaultBaseURL      = "https://api.coingecko.com/api/v3"
	DefaultUserAgent    = "edgex-ops-intelligence/coingecko-client"
)

// Client is the minimal HTTP client used by the CoinGecko collector. It is
// deliberately small: callers wrap responses in higher-level mapping logic.
type Client struct {
	cfg        Config
	httpClient *http.Client
	governor   *BudgetGovernor
}

// New constructs a CoinGecko HTTP client honoring the v2 R1 contract:
// process-level proxy env vars are ignored. The transport's Proxy hook is
// only set if cfg.Proxy parses to a usable URL.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.APIKeyHeader == "" {
		cfg.APIKeyHeader = DefaultAPIKeyHeader
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}

	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	if cfg.Proxy != "" {
		u, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("coingecko: parse proxy %q: %w", cfg.Proxy, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("coingecko: proxy %q must include scheme and host", cfg.Proxy)
		}
		tr.Proxy = http.ProxyURL(u)
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Transport: tr,
			Timeout:   cfg.RequestTimeout,
		},
		governor: cfg.Governor,
	}, nil
}

// BaseURL exposes the resolved base URL, useful for source_endpoint lineage.
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// FetchDerivatives executes a single GET against
// /derivatives?include_tickers=unexpired and returns the parsed slice plus
// the raw endpoint string that callers can persist as source_endpoint.
//
// 429s and 5xxs surface as RateLimitedError / HTTPError so the collector can
// log, back off, and keep the rest of the cycle running rather than crash.
func (c *Client) FetchDerivatives(ctx context.Context) ([]Ticker, string, error) {
	endpoint := c.cfg.BaseURL + "/derivatives?include_tickers=unexpired"
	if err := c.beforeRequest(ctx, endpoint, PriorityPrimary); err != nil {
		return nil, endpoint, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		c.afterResponse(endpoint, PriorityPrimary, err)
		return nil, endpoint, err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set(c.cfg.APIKeyHeader, c.cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("coingecko fetch derivatives: %w", err)
		c.afterResponse(endpoint, PriorityPrimary, err)
		return nil, endpoint, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		err := newRateLimitedError(resp, endpoint, string(body))
		c.afterResponse(endpoint, PriorityPrimary, err)
		return nil, endpoint, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		err := &HTTPError{Status: resp.StatusCode, Body: string(body), Endpoint: endpoint}
		c.afterResponse(endpoint, PriorityPrimary, err)
		return nil, endpoint, err
	}
	var tickers []Ticker
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&tickers); err != nil {
		err = fmt.Errorf("coingecko decode derivatives: %w", err)
		c.afterResponse(endpoint, PriorityPrimary, err)
		return nil, endpoint, err
	}
	c.afterResponse(endpoint, PriorityPrimary, nil)
	return tickers, endpoint, nil
}

// VolumeChartPoint is one [timestamp_ms, btc_volume] sample returned by
// GET /exchanges/{id}/volume_chart{,_range}. CoinGecko's docs state the
// volume_chart endpoints accept derivatives exchange IDs (e.g.
// binance_futures), so we reuse the same parser for both spot and
// derivatives backfills.
type VolumeChartPoint struct {
	TimestampMS int64
	VolumeBTC   float64
}

// PricePoint is one [timestamp_ms, usd_price] sample returned by
// GET /coins/{id}/market_chart{,_range}.
type PricePoint struct {
	TimestampMS int64
	PriceUSD    float64
}

// FetchExchangeVolumeChart returns daily BTC volume time-series for the
// given derivatives or spot exchange ID. CoinGecko's docs accept the
// fixed values 1 / 7 / 14 / 30 / 90 / 180 / 365 for `days`; granularity is
// daily once days≥30. This is the Demo-tier endpoint; the /range variant
// requires a Pro subscription.
func (c *Client) FetchExchangeVolumeChart(ctx context.Context, exchangeID string, days int) ([]VolumeChartPoint, string, error) {
	if exchangeID = strings.TrimSpace(exchangeID); exchangeID == "" {
		return nil, "", errors.New("coingecko: exchange id required")
	}
	if days <= 0 {
		days = 30
	}
	q := url.Values{}
	q.Set("days", fmt.Sprintf("%d", days))
	endpoint := fmt.Sprintf("%s/exchanges/%s/volume_chart?%s", c.cfg.BaseURL, exchangeID, q.Encode())
	raw, err := c.getJSONArray(ctx, endpoint)
	if err != nil {
		return nil, endpoint, err
	}
	points, err := parseChartArray(raw)
	if err != nil {
		return nil, endpoint, fmt.Errorf("coingecko volume_chart decode: %w", err)
	}
	out := make([]VolumeChartPoint, 0, len(points))
	for _, p := range points {
		out = append(out, VolumeChartPoint{TimestampMS: p.tsMS, VolumeBTC: p.value})
	}
	return out, endpoint, nil
}

// FetchBitcoinPriceChartRange returns daily BTC USD prices over the given
// range. We use this to convert volume_chart's BTC-denominated samples into
// USD for daily aggregates.
func (c *Client) FetchBitcoinPriceChartRange(ctx context.Context, from, to time.Time) ([]PricePoint, string, error) {
	q := url.Values{}
	q.Set("vs_currency", "usd")
	q.Set("from", fmt.Sprintf("%d", from.UTC().Unix()))
	q.Set("to", fmt.Sprintf("%d", to.UTC().Unix()))
	endpoint := fmt.Sprintf("%s/coins/bitcoin/market_chart/range?%s", c.cfg.BaseURL, q.Encode())
	body, err := c.getJSONBytes(ctx, endpoint)
	if err != nil {
		return nil, endpoint, err
	}
	var payload struct {
		Prices [][]json.Number `json:"prices"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, endpoint, fmt.Errorf("coingecko market_chart decode: %w", err)
	}
	out := make([]PricePoint, 0, len(payload.Prices))
	for _, row := range payload.Prices {
		if len(row) != 2 {
			continue
		}
		tsMS, err := row[0].Int64()
		if err != nil {
			continue
		}
		price, err := row[1].Float64()
		if err != nil {
			continue
		}
		out = append(out, PricePoint{TimestampMS: tsMS, PriceUSD: price})
	}
	return out, endpoint, nil
}

type chartSample struct {
	tsMS  int64
	value float64
}

func parseChartArray(raw [][]json.Number) ([]chartSample, error) {
	out := make([]chartSample, 0, len(raw))
	for i, row := range raw {
		if len(row) != 2 {
			return nil, fmt.Errorf("row %d: expected 2 values, got %d", i, len(row))
		}
		tsMS, err := row[0].Int64()
		if err != nil {
			return nil, fmt.Errorf("row %d ts: %w", i, err)
		}
		v, err := row[1].Float64()
		if err != nil {
			return nil, fmt.Errorf("row %d value: %w", i, err)
		}
		out = append(out, chartSample{tsMS: tsMS, value: v})
	}
	return out, nil
}

// CoinSearchResult is one row from the /search?query=X response,
// narrowed to the fields the Listing Agent decision card enrichment
// uses. The full search response also carries exchanges/categories
// but we only consume `coins`.
type CoinSearchResult struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	MarketCapRank int    `json:"market_cap_rank"`
}

// CoinMarketSnapshot mirrors one row of /coins/markets?vs_currency=usd&ids=X
// narrowed to the two fields the decision card enrichment uses.
type CoinMarketSnapshot struct {
	ID           string  `json:"id"`
	Symbol       string  `json:"symbol"`
	Name         string  `json:"name"`
	MarketCapUSD float64 `json:"market_cap"`
	Volume24HUSD float64 `json:"total_volume"`
}

// SearchCoinsBySymbol issues GET /search?query=X and returns the
// `coins` portion of the response. CoinGecko orders the array by
// market_cap_rank ascending (most-valued coin first), with no-rank
// rows trailing. Callers are expected to filter by exact symbol
// match because /search is a fuzzy text search that returns
// near-misses.
func (c *Client) SearchCoinsBySymbol(ctx context.Context, query string) ([]CoinSearchResult, string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "", errors.New("coingecko: search query required")
	}
	q := url.Values{}
	q.Set("query", query)
	endpoint := c.cfg.BaseURL + "/search?" + q.Encode()
	body, err := c.getJSONBytes(ctx, endpoint)
	if err != nil {
		return nil, endpoint, err
	}
	var payload struct {
		Coins []CoinSearchResult `json:"coins"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, endpoint, fmt.Errorf("coingecko search decode: %w", err)
	}
	return payload.Coins, endpoint, nil
}

// FetchCoinMarketSnapshot fetches /coins/markets?vs_currency=usd&ids=X
// for a single coin id and returns the parsed snapshot. CoinGecko
// returns a list even for one id, so we extract the first element.
// An empty array means the id does not exist; we return (nil, ...)
// rather than an error so callers can downgrade gracefully.
func (c *Client) FetchCoinMarketSnapshot(ctx context.Context, coinID string) (*CoinMarketSnapshot, string, error) {
	coinID = strings.TrimSpace(coinID)
	if coinID == "" {
		return nil, "", errors.New("coingecko: coin id required")
	}
	q := url.Values{}
	q.Set("vs_currency", "usd")
	q.Set("ids", coinID)
	q.Set("sparkline", "false")
	endpoint := c.cfg.BaseURL + "/coins/markets?" + q.Encode()
	body, err := c.getJSONBytes(ctx, endpoint)
	if err != nil {
		return nil, endpoint, err
	}
	var payload []CoinMarketSnapshot
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, endpoint, fmt.Errorf("coingecko coins/markets decode: %w", err)
	}
	if len(payload) == 0 {
		return nil, endpoint, nil
	}
	snap := payload[0]
	return &snap, endpoint, nil
}

func (c *Client) getJSONArray(ctx context.Context, endpoint string) ([][]json.Number, error) {
	body, err := c.getJSONBytes(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var out [][]json.Number
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) getJSONBytes(ctx context.Context, endpoint string) ([]byte, error) {
	priority := requestPriorityForEndpoint(endpoint)
	if err := c.beforeRequest(ctx, endpoint, priority); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		c.afterResponse(endpoint, priority, err)
		return nil, err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set(c.cfg.APIKeyHeader, c.cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("coingecko GET %s: %w", endpoint, err)
		c.afterResponse(endpoint, priority, err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		err := newRateLimitedError(resp, endpoint, string(body))
		c.afterResponse(endpoint, priority, err)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		err := &HTTPError{Status: resp.StatusCode, Body: string(body), Endpoint: endpoint}
		c.afterResponse(endpoint, priority, err)
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	c.afterResponse(endpoint, priority, err)
	return body, err
}

func (c *Client) beforeRequest(ctx context.Context, endpoint string, priority RequestPriority) error {
	if c == nil || c.governor == nil {
		return nil
	}
	return c.governor.BeforeRequest(ctx, endpoint, priority)
}

func (c *Client) afterResponse(endpoint string, priority RequestPriority, err error) {
	if c == nil || c.governor == nil {
		return
	}
	c.governor.AfterResponse(endpoint, priority, err)
}

func requestPriorityForEndpoint(endpoint string) RequestPriority {
	if strings.Contains(endpoint, "/exchanges/") || strings.Contains(endpoint, "/market_chart/range") {
		return PriorityBackfill
	}
	if strings.Contains(endpoint, "/search?") || strings.Contains(endpoint, "/coins/markets?") {
		return PriorityListing
	}
	return PriorityPrimary
}

// HTTPError is returned for any non-2xx response other than 429.
type HTTPError struct {
	Status   int
	Body     string
	Endpoint string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("coingecko: HTTP %d from %s: %s", e.Status, e.Endpoint, truncate(e.Body, 200))
}

// RateLimitedError signals a 429 so callers can back off without treating the
// run as fatal.
type RateLimitedError struct {
	Status        int
	Body          string
	Endpoint      string
	RetryAfter    time.Duration
	RetryAfterRaw string
}

func (e *RateLimitedError) Error() string {
	if e.Endpoint != "" {
		return fmt.Sprintf("coingecko: rate limited (HTTP %d) from %s: %s", e.Status, e.Endpoint, truncate(e.Body, 200))
	}
	return fmt.Sprintf("coingecko: rate limited (HTTP %d): %s", e.Status, truncate(e.Body, 200))
}

func newRateLimitedError(resp *http.Response, endpoint, body string) *RateLimitedError {
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	return &RateLimitedError{
		Status:        resp.StatusCode,
		Body:          body,
		Endpoint:      endpoint,
		RetryAfter:    parseRetryAfter(raw, time.Now().UTC()),
		RetryAfterRaw: raw,
	}
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	if raw == "" {
		return 0
	}
	if d, err := time.ParseDuration(raw + "s"); err == nil {
		return d
	}
	if at, err := http.ParseTime(raw); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// IsRateLimited reports whether err is a RateLimitedError.
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	var rl *RateLimitedError
	return errors.As(err, &rl)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
