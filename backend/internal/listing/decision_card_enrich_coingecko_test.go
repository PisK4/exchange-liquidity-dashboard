package listing

import (
	"context"
	"errors"
	"testing"

	"edgex-dashboard/backend/internal/marketdata/coingecko"
)

type stubCoinGecko struct {
	searchHits []coingecko.CoinSearchResult
	searchErr  error
	snap       *coingecko.CoinMarketSnapshot
	snapErr    error
	searches   int
	snapshots  int
}

func (s *stubCoinGecko) SearchCoinsBySymbol(ctx context.Context, query string) ([]coingecko.CoinSearchResult, string, error) {
	s.searches++
	return s.searchHits, "https://stub/search", s.searchErr
}

func (s *stubCoinGecko) FetchCoinMarketSnapshot(ctx context.Context, coinID string) (*coingecko.CoinMarketSnapshot, string, error) {
	s.snapshots++
	return s.snap, "https://stub/markets", s.snapErr
}

func TestPickCoinIDForSymbolPicksLowestRankExactMatch(t *testing.T) {
	hits := []coingecko.CoinSearchResult{
		{ID: "btc-fake-1", Symbol: "BTC", MarketCapRank: 99},
		{ID: "bitcoin", Symbol: "BTC", MarketCapRank: 1},
		{ID: "btc-fake-2", Symbol: "BTC", MarketCapRank: 10},
		{ID: "ethereum", Symbol: "ETH", MarketCapRank: 2},
	}
	if got := pickCoinIDForSymbol(hits, "BTC"); got != "bitcoin" {
		t.Errorf("BTC → %q, want bitcoin", got)
	}
	if got := pickCoinIDForSymbol(hits, "btc"); got != "bitcoin" {
		t.Errorf("btc (lower) → %q, want bitcoin", got)
	}
	if got := pickCoinIDForSymbol(hits, "ETH"); got != "ethereum" {
		t.Errorf("ETH → %q, want ethereum", got)
	}
}

func TestPickCoinIDForSymbolFallsBackToUnrankedWhenNoRankedMatch(t *testing.T) {
	hits := []coingecko.CoinSearchResult{
		{ID: "weird-token", Symbol: "WT", MarketCapRank: 0},
	}
	if got := pickCoinIDForSymbol(hits, "WT"); got != "weird-token" {
		t.Errorf("WT → %q, want weird-token", got)
	}
}

func TestPickCoinIDForSymbolEmptyWhenNoSymbolMatch(t *testing.T) {
	hits := []coingecko.CoinSearchResult{
		{ID: "bitcoin", Symbol: "BTC", MarketCapRank: 1},
	}
	if got := pickCoinIDForSymbol(hits, "XYZ"); got != "" {
		t.Errorf("XYZ → %q, want empty", got)
	}
}

func TestBuildCoinGeckoFetcherNilClientReturnsError(t *testing.T) {
	fetch := BuildCoinGeckoFetcher(nil)
	_, _, _, err := fetch(context.Background(), "BTC")
	if err == nil {
		t.Errorf("err = nil, want one (nil client)")
	}
}

func TestBuildCoinGeckoFetcherHappyPath(t *testing.T) {
	stub := &stubCoinGecko{
		searchHits: []coingecko.CoinSearchResult{
			{ID: "bitcoin", Symbol: "BTC", MarketCapRank: 1},
		},
		snap: &coingecko.CoinMarketSnapshot{
			ID: "bitcoin", Symbol: "btc", Name: "Bitcoin",
			MarketCapUSD: 1_000_000_000_000,
			Volume24HUSD: 30_000_000_000,
		},
	}
	fetch := BuildCoinGeckoFetcher(stub)
	mc, vol, id, err := fetch(context.Background(), "BTC")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != "bitcoin" {
		t.Errorf("id = %q, want bitcoin", id)
	}
	if mc == nil || *mc != 1_000_000_000_000 {
		t.Errorf("market cap = %v", mc)
	}
	if vol == nil || *vol != 30_000_000_000 {
		t.Errorf("volume = %v", vol)
	}
}

func TestBuildCoinGeckoFetcherCachesResolvedID(t *testing.T) {
	stub := &stubCoinGecko{
		searchHits: []coingecko.CoinSearchResult{
			{ID: "bitcoin", Symbol: "BTC", MarketCapRank: 1},
		},
		snap: &coingecko.CoinMarketSnapshot{
			ID: "bitcoin", MarketCapUSD: 1, Volume24HUSD: 1,
		},
	}
	fetch := BuildCoinGeckoFetcher(stub)
	for i := 0; i < 3; i++ {
		_, _, _, err := fetch(context.Background(), "BTC")
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if stub.searches != 1 {
		t.Errorf("searches = %d, want 1 (cached)", stub.searches)
	}
	if stub.snapshots != 3 {
		t.Errorf("snapshots = %d, want 3", stub.snapshots)
	}
}

func TestBuildCoinGeckoFetcherSearchErrorPropagates(t *testing.T) {
	stub := &stubCoinGecko{searchErr: errors.New("rate limited")}
	fetch := BuildCoinGeckoFetcher(stub)
	_, _, _, err := fetch(context.Background(), "BTC")
	if err == nil {
		t.Errorf("err = nil, want rate-limited propagation")
	}
}

func TestBuildCoinGeckoFetcherNoMatchReturnsError(t *testing.T) {
	stub := &stubCoinGecko{searchHits: nil}
	fetch := BuildCoinGeckoFetcher(stub)
	_, _, _, err := fetch(context.Background(), "XYZNOTFOUND")
	if err == nil {
		t.Errorf("err = nil, want no-match error")
	}
}

func TestBuildCoinGeckoFetcherZeroValuesReturnNilPtrs(t *testing.T) {
	stub := &stubCoinGecko{
		searchHits: []coingecko.CoinSearchResult{
			{ID: "x", Symbol: "ABC", MarketCapRank: 1},
		},
		snap: &coingecko.CoinMarketSnapshot{ID: "x"},
	}
	fetch := BuildCoinGeckoFetcher(stub)
	mc, vol, _, err := fetch(context.Background(), "ABC")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if mc != nil {
		t.Errorf("market cap = %v, want nil (zero filter)", mc)
	}
	if vol != nil {
		t.Errorf("volume = %v, want nil (zero filter)", vol)
	}
}
