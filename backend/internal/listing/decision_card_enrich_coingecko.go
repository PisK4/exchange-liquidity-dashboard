package listing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"edgex-ops-intelligence/backend/internal/marketdata/coingecko"
)

// CoinGeckoClient is the subset of *coingecko.Client the decision
// card enrichment consumes. Defined as an interface so tests inject
// a deterministic stub without spinning up an HTTP server.
type CoinGeckoClient interface {
	SearchCoinsBySymbol(ctx context.Context, query string) ([]coingecko.CoinSearchResult, string, error)
	FetchCoinMarketSnapshot(ctx context.Context, coinID string) (*coingecko.CoinMarketSnapshot, string, error)
}

type CoinGeckoFetcherOptions struct {
	CoinIDCacheTTL         time.Duration
	MarketSnapshotCacheTTL time.Duration
	Now                    func() time.Time
}

type coinIDCacheEntry struct {
	id        string
	expiresAt time.Time
}

type marketSnapshotCacheEntry struct {
	snap      *coingecko.CoinMarketSnapshot
	expiresAt time.Time
}

// BuildCoinGeckoFetcher adapts a CoinGeckoClient into the
// CoinGeckoFetcher contract on DecisionCardEnrichDeps. The returned
// closure does two HTTP calls per invocation:
//
//  1. /search?query={canonical} to resolve canonical → coin id; we
//     pick the highest-market-cap coin whose symbol matches the
//     canonical (case-insensitive). This filters out fuzzy
//     near-misses (e.g. searching "BTC" returns multiple BTC-named
//     tokens; the bitcoin row has market_cap_rank=1).
//  2. /coins/markets?vs_currency=usd&ids={id} to fetch the market
//     cap + total 24h volume.
//
// Per spec C5 we share resolved ids across the producer run via an
// in-memory map so two candidates with the same canonical (e.g.
// from two different evidence kinds) only burn one /search call.
// The map is keyed by upper-case canonical and never bleeds across
// engine ticks because BuildCoinGeckoFetcher returns a fresh closure
// each call.
func BuildCoinGeckoFetcher(cg CoinGeckoClient, options ...CoinGeckoFetcherOptions) func(ctx context.Context, canonical string) (*float64, *float64, string, error) {
	if cg == nil {
		return func(context.Context, string) (*float64, *float64, string, error) {
			return nil, nil, "", errors.New("coingecko client not configured")
		}
	}
	opts := CoinGeckoFetcherOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	var (
		mu          sync.Mutex
		seen        = make(map[string]coinIDCacheEntry)
		marketSnaps = make(map[string]marketSnapshotCacheEntry)
	)
	return func(ctx context.Context, canonical string) (*float64, *float64, string, error) {
		canonical = strings.TrimSpace(canonical)
		if canonical == "" {
			return nil, nil, "", errors.New("canonical required")
		}
		key := strings.ToUpper(canonical)
		now := opts.Now()

		mu.Lock()
		idEntry, cached := seen[key]
		if cached && !idEntry.expiresAt.IsZero() && now.After(idEntry.expiresAt) {
			delete(seen, key)
			cached = false
		}
		id := idEntry.id
		mu.Unlock()
		if !cached {
			results, _, err := cg.SearchCoinsBySymbol(ctx, canonical)
			if err != nil {
				return nil, nil, "", fmt.Errorf("search %s: %w", canonical, err)
			}
			id = pickCoinIDForSymbol(results, canonical)
			if id == "" {
				return nil, nil, "", fmt.Errorf("no coin id found for symbol %q", canonical)
			}
			expiresAt := time.Time{}
			if opts.CoinIDCacheTTL > 0 {
				expiresAt = now.Add(opts.CoinIDCacheTTL)
			}
			mu.Lock()
			seen[key] = coinIDCacheEntry{id: id, expiresAt: expiresAt}
			mu.Unlock()
		}

		if opts.MarketSnapshotCacheTTL > 0 {
			mu.Lock()
			entry, ok := marketSnaps[id]
			if ok && !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
				delete(marketSnaps, id)
				ok = false
			}
			mu.Unlock()
			if ok && entry.snap != nil {
				return coinMarketSnapshotValues(entry.snap, id)
			}
		}

		snap, _, err := cg.FetchCoinMarketSnapshot(ctx, id)
		if err != nil {
			return nil, nil, id, fmt.Errorf("fetch market %s: %w", id, err)
		}
		if snap == nil {
			return nil, nil, id, fmt.Errorf("no market data for id %q", id)
		}
		if opts.MarketSnapshotCacheTTL > 0 {
			mu.Lock()
			marketSnaps[id] = marketSnapshotCacheEntry{snap: snap, expiresAt: now.Add(opts.MarketSnapshotCacheTTL)}
			mu.Unlock()
		}
		return coinMarketSnapshotValues(snap, id)
	}
}

func coinMarketSnapshotValues(snap *coingecko.CoinMarketSnapshot, id string) (*float64, *float64, string, error) {
	if snap == nil {
		return nil, nil, id, fmt.Errorf("no market data for id %q", id)
	}
	mc := snap.MarketCapUSD
	vol := snap.Volume24HUSD
	var mcPtr, volPtr *float64
	if mc > 0 {
		mcPtr = &mc
	}
	if vol > 0 {
		volPtr = &vol
	}
	return mcPtr, volPtr, id, nil
}

// pickCoinIDForSymbol filters /search hits to coins whose symbol
// matches the canonical case-insensitively, then picks the one with
// the lowest (best) MarketCapRank. CoinGecko already returns the
// list ordered by market_cap_rank, but ranks of 0 (or missing)
// trail real ranks, so we cannot just take results[0].
func pickCoinIDForSymbol(results []coingecko.CoinSearchResult, canonical string) string {
	want := strings.ToUpper(strings.TrimSpace(canonical))
	if want == "" {
		return ""
	}
	bestID := ""
	bestRank := 0
	for _, r := range results {
		if strings.ToUpper(strings.TrimSpace(r.Symbol)) != want {
			continue
		}
		// MarketCapRank == 0 means unranked; prefer any ranked
		// match over unranked.
		if r.MarketCapRank > 0 {
			if bestID == "" || bestRank == 0 || r.MarketCapRank < bestRank {
				bestID = r.ID
				bestRank = r.MarketCapRank
			}
			continue
		}
		if bestID == "" {
			bestID = r.ID
		}
	}
	return bestID
}
