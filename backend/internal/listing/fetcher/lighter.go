package fetcher

import (
	"context"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// LighterOrderBookDetailsURL is the public endpoint that lists every
// orderbook with its market_id + market_type. The query is shared
// across perp + spot pollers, so the fetcher routes both through a
// request-level cache (spec F6).
const LighterOrderBookDetailsURL = "https://mainnet.zklighter.elliot.ai/api/v1/orderBookDetails?filter=all"

// lighterCacheTTL bounds how long a cached body is reused. It is
// intentionally short (60s) — well below the listing fetcher poll
// cadence (typically 3-5 min) — so a perp + spot tick that fires
// within seconds of each other shares a single HTTP call without
// risking staleness across distinct ticks.
const lighterCacheTTL = 60 * time.Second

// FetchLighterPerp returns the InstrumentSource.Fetch closure for the
// perp surface. The closure shares an HTTP round-trip with
// FetchLighterSpot via the package-level cache when invoked within
// lighterCacheTTL.
func FetchLighterPerp(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	return newLighterFetcher(deps, baseURL, "perp", globalLighterRequestCache)
}

// FetchLighterSpot — see FetchLighterPerp.
func FetchLighterSpot(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	return newLighterFetcher(deps, baseURL, "spot", globalLighterRequestCache)
}

// newLighterFetcher is the test seam: callers in tests inject a
// freshly-allocated cache so a TTL window from a previous test does
// not bleed into the assertion. Production wiring uses the package
// singleton via FetchLighterPerp / FetchLighterSpot.
func newLighterFetcher(deps HTTPDeps, baseURL, surface string, cache *requestCache) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = LighterOrderBookDetailsURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		body, err := cache.fetch(ctx, deps, url, lighterCacheTTL)
		if err != nil {
			return nil, err
		}
		return instrument.FilterLighterPayloadBySurface(body, surface)
	}
}
