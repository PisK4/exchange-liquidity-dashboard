package listing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"edgex-ops-intelligence/backend/internal/marketdata/coingecko"
)

// CoinGeckoClient is the subset of *coingecko.Client the decision
// card enrichment consumes. Defined as an interface so tests inject
// a deterministic stub without spinning up an HTTP server.
type CoinGeckoClient interface {
	SearchCoinsBySymbol(ctx context.Context, query string) ([]coingecko.CoinSearchResult, string, error)
	FetchCoinMarketSnapshot(ctx context.Context, coinID string) (*coingecko.CoinMarketSnapshot, string, error)
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
func BuildCoinGeckoFetcher(cg CoinGeckoClient) func(ctx context.Context, canonical string) (*float64, *float64, string, error) {
	if cg == nil {
		return func(context.Context, string) (*float64, *float64, string, error) {
			return nil, nil, "", errors.New("coingecko client not configured")
		}
	}
	var (
		mu   sync.Mutex
		seen = make(map[string]string)
	)
	return func(ctx context.Context, canonical string) (*float64, *float64, string, error) {
		canonical = strings.TrimSpace(canonical)
		if canonical == "" {
			return nil, nil, "", errors.New("canonical required")
		}
		key := strings.ToUpper(canonical)

		mu.Lock()
		id, cached := seen[key]
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
			mu.Lock()
			seen[key] = id
			mu.Unlock()
		}

		snap, _, err := cg.FetchCoinMarketSnapshot(ctx, id)
		if err != nil {
			return nil, nil, id, fmt.Errorf("fetch market %s: %w", id, err)
		}
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
