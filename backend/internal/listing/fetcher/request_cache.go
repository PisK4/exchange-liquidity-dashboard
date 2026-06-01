package fetcher

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// requestCache is a tiny URL → body cache used by the Lighter
// fetcher so its two sources (lighter/perp + lighter/spot) share a
// single HTTP round-trip per tick. The TTL is configured per fetch
// call; the cache holds entries indefinitely once written but is
// only consulted when (now - entry.fetchedAt) < ttl.
//
// Scope: this is intentionally NOT a general-purpose cache. It is
// instantiated as a package-level singleton (globalLighterRequestCache)
// and used solely by lighter.go. Other fetchers continue to dial the
// HTTP layer directly so their latency measurements stay independent.
type requestCache struct {
	mu    sync.Mutex
	store map[string]requestCacheEntry
	nowFn func() time.Time
}

type requestCacheEntry struct {
	body      []byte
	fetchedAt time.Time
}

func newRequestCache() *requestCache {
	return &requestCache{
		store: map[string]requestCacheEntry{},
		nowFn: time.Now,
	}
}

// fetch returns the body for `url`, either from the cache (when
// younger than ttl) or by issuing a fresh GET through deps. Errors
// from upstream are NOT cached so a transient outage does not
// suppress retries for ttl.
func (c *requestCache) fetch(ctx context.Context, deps HTTPDeps, url string, ttl time.Duration) ([]byte, error) {
	c.mu.Lock()
	if entry, ok := c.store[url]; ok && c.nowFn().Sub(entry.fetchedAt) < ttl {
		body := append([]byte(nil), entry.body...)
		c.mu.Unlock()
		return body, nil
	}
	c.mu.Unlock()

	body, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.store[url] = requestCacheEntry{body: append([]byte(nil), body...), fetchedAt: c.nowFn()}
	c.mu.Unlock()
	return body, nil
}

// globalLighterRequestCache is the package-level singleton shared by
// the perp + spot Lighter fetchers. Keeping it scoped to lighter.go
// (not exposed to other platforms) keeps the contract narrow and
// avoids accidental coupling with the Bybit / OKX / etc. transports.
var globalLighterRequestCache = newRequestCache()
