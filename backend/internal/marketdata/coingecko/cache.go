package coingecko

import (
	"sync"
	"time"
)

// TickerCache holds the latest /derivatives response so concurrent callers
// (e.g. API requests + collector probe) don't issue duplicate calls within
// the configured TTL. Zero-value TTL disables the cache (every read returns
// false).
type TickerCache struct {
	mu       sync.RWMutex
	ttl      time.Duration
	tickers  []Ticker
	endpoint string
	at       time.Time
}

// NewTickerCache builds a TickerCache with the given TTL. A TTL of zero or
// negative disables caching.
func NewTickerCache(ttl time.Duration) *TickerCache {
	return &TickerCache{ttl: ttl}
}

// Get returns the cached tickers + source endpoint when fresh. The boolean
// reports whether the cache was hit; on a miss callers should fetch and then
// Put().
func (c *TickerCache) Get(now time.Time) ([]Ticker, string, bool) {
	if c == nil || c.ttl <= 0 {
		return nil, "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.at.IsZero() || now.Sub(c.at) > c.ttl {
		return nil, "", false
	}
	dup := make([]Ticker, len(c.tickers))
	copy(dup, c.tickers)
	return dup, c.endpoint, true
}

// GetStale returns the latest cached value when it is not fresh but still
// inside maxAge. This is only used while the CoinGecko governor is cooling
// down; callers must mark downstream status as stale/cache_served rather than
// treating the data as a new successful upstream pull.
func (c *TickerCache) GetStale(now time.Time, maxAge time.Duration) ([]Ticker, string, bool) {
	if c == nil || maxAge <= 0 {
		return nil, "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.at.IsZero() || len(c.tickers) == 0 || now.Sub(c.at) > maxAge {
		return nil, "", false
	}
	dup := make([]Ticker, len(c.tickers))
	copy(dup, c.tickers)
	return dup, c.endpoint, true
}

// Put records a fresh response. Callers should pass the same now value used
// for the request so successive Get calls share a monotonic view.
func (c *TickerCache) Put(now time.Time, tickers []Ticker, endpoint string) {
	if c == nil || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	stored := make([]Ticker, len(tickers))
	copy(stored, tickers)
	c.tickers = stored
	c.endpoint = endpoint
	c.at = now
}

// LastFetchTS exposes the freshest cached snapshot time, used by status
// reporting. Returns zero time when the cache is empty or disabled.
func (c *TickerCache) LastFetchTS() time.Time {
	if c == nil {
		return time.Time{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.at
}
