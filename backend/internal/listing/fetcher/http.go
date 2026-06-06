package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewHTTPClient returns the *http.Client every fetcher should share
// in production. The transport explicitly does NOT consult
// process-level HTTP_PROXY / HTTPS_PROXY env vars; the only proxy
// hook is the caller-supplied url. This mirrors the contract enforced
// by the CoinGecko client (R1 from the v2 plan) so the 9 native
// exchange adapters' direct-dial paths are not affected by changes
// to the listing fetcher proxy setting.
//
// A blank proxy returns a default-transport client; an invalid proxy
// returns an error so cmd/ops-intelligence fails loud rather than silently
// dialing direct.
func NewHTTPClient(timeout time.Duration, proxy string) (*http.Client, error) {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if p := strings.TrimSpace(proxy); p != "" {
		parsed, err := url.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("fetcher: parse proxy %q: %w", p, err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("fetcher: proxy %q missing scheme or host", p)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// fetchJSON performs a single GET (or POST with body) and returns
// the raw response body. Non-2xx status codes are surfaced as an
// error containing the status code; the body is included only when
// shorter than 256 bytes so a CEX returning a 500-page HTML error
// doesn't blow up the listing engine log.
//
// The caller is responsible for unmarshaling the body into the
// platform-specific envelope and feeding individual rows to the
// normalizer / parser.
func (d HTTPDeps) fetchJSON(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	if d.Client == nil {
		return nil, fmt.Errorf("fetcher: HTTPDeps.Client is nil")
	}
	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("fetcher: build request %s %s: %w", method, url, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", d.effectiveUserAgent())
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetcher: do %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetcher: read body %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 256 {
			snippet = snippet[:256] + "...[truncated]"
		}
		return nil, fmt.Errorf("fetcher: %s %s returned http %d: %s", method, url, resp.StatusCode, snippet)
	}
	return raw, nil
}
