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
}

// DefaultAPIKeyHeader is the canonical header CoinGecko Demo and Pro keys use.
const (
	DefaultAPIKeyHeader = "x-cg-demo-api-key"
	DefaultBaseURL      = "https://api.coingecko.com/api/v3"
	DefaultUserAgent    = "edgex-dashboard/coingecko-client"
)

// Client is the minimal HTTP client used by the CoinGecko collector. It is
// deliberately small: callers wrap responses in higher-level mapping logic.
type Client struct {
	cfg        Config
	httpClient *http.Client
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, endpoint, err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set(c.cfg.APIKeyHeader, c.cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, endpoint, fmt.Errorf("coingecko fetch derivatives: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, endpoint, &RateLimitedError{Status: resp.StatusCode, Body: string(body)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, endpoint, &HTTPError{Status: resp.StatusCode, Body: string(body), Endpoint: endpoint}
	}
	var tickers []Ticker
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&tickers); err != nil {
		return nil, endpoint, fmt.Errorf("coingecko decode derivatives: %w", err)
	}
	return tickers, endpoint, nil
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
	Status int
	Body   string
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("coingecko: rate limited (HTTP %d): %s", e.Status, truncate(e.Body, 200))
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
