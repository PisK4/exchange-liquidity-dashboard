package listing

import (
	"context"
	"errors"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

func TestEnrichCandidateForDecisionCardAllSourcesNil(t *testing.T) {
	ctx := context.Background()
	c := Candidate{CanonicalSymbol: "ABC", SourcePlatforms: []string{"binance"}}
	got := EnrichCandidateForDecisionCard(ctx, DecisionCardEnrichDeps{}, c)
	if got.EdgexListedKnown {
		t.Errorf("EdgexListedKnown = true with nil lookup, want false")
	}
	if got.HasMarketStatus() {
		t.Errorf("HasMarketStatus = true with no statuses")
	}
	if len(got.EnrichErrors) != 0 {
		t.Errorf("EnrichErrors = %v, want empty", got.EnrichErrors)
	}
}

func TestEnrichCandidateForDecisionCardAggregatesEvery(t *testing.T) {
	ctx := context.Background()
	c := Candidate{CanonicalSymbol: "ABC", SourcePlatforms: []string{"binance"}}
	mcap := 120_000_000.0
	vol := 35_000_000.0
	statuses := []PlatformMarketStatus{
		{Platform: "binance", Status: StatusActive, StatusLabel: "Perp LIVE", SourceKind: "api"},
	}
	deps := DecisionCardEnrichDeps{
		EdgexListedLookup: func(canonical string) (bool, bool) {
			return false, true
		},
		MarketStatusLoader: func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
			return statuses, nil
		},
		CoinGeckoFetcher: func(ctx context.Context, canonical string) (*float64, *float64, string, error) {
			return &mcap, &vol, "abc-coin", nil
		},
		DepthFetcher: func(ctx context.Context, canonical string, sources []string) (*DepthEvidence, *DepthEvidence, error) {
			return &DepthEvidence{Platform: "binance", USDValue: 580_000, Tier: "2pct"},
				&DepthEvidence{Platform: "binance", USDValue: 1_200_000, Tier: "2pct"},
				nil
		},
	}
	got := EnrichCandidateForDecisionCard(ctx, deps, c)
	if !got.EdgexListedKnown || got.EdgexListed {
		t.Errorf("EdgexListed=%v Known=%v, want listed=false known=true", got.EdgexListed, got.EdgexListedKnown)
	}
	if !got.HasMarketStatus() || got.MarketStatuses[0].Platform != "binance" {
		t.Errorf("MarketStatuses = %+v", got.MarketStatuses)
	}
	if got.MarketCapUSD == nil || *got.MarketCapUSD != mcap {
		t.Errorf("MarketCapUSD = %v, want %v", got.MarketCapUSD, mcap)
	}
	if got.Spot24hVolumeUSD == nil || *got.Spot24hVolumeUSD != vol {
		t.Errorf("Spot24hVolumeUSD = %v, want %v", got.Spot24hVolumeUSD, vol)
	}
	if got.SpotDepth == nil || got.PerpDepth == nil {
		t.Fatalf("depth evidence missing: %+v / %+v", got.SpotDepth, got.PerpDepth)
	}
	if got.CoinGeckoID != "abc-coin" {
		t.Errorf("CoinGeckoID = %q", got.CoinGeckoID)
	}
	if len(got.EnrichErrors) != 0 {
		t.Errorf("EnrichErrors = %v, want empty", got.EnrichErrors)
	}
}

func TestEnrichCandidateForDecisionCardRecordsPerSourceErrors(t *testing.T) {
	ctx := context.Background()
	c := Candidate{CanonicalSymbol: "ABC"}
	deps := DecisionCardEnrichDeps{
		Now: func() time.Time { return time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC) },
		MarketStatusLoader: func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
			return nil, errors.New("db down")
		},
		CoinGeckoFetcher: func(ctx context.Context, canonical string) (*float64, *float64, string, error) {
			return nil, nil, "", errors.New("rate limited")
		},
		DepthFetcher: func(ctx context.Context, canonical string, sources []string) (*DepthEvidence, *DepthEvidence, error) {
			return nil, nil, errors.New("all adapters timed out")
		},
	}
	got := EnrichCandidateForDecisionCard(ctx, deps, c)
	if len(got.EnrichErrors) != 3 {
		t.Errorf("EnrichErrors = %v, want 3 entries", got.EnrichErrors)
	}
	if got.MarketCapUSD != nil || got.Spot24hVolumeUSD != nil {
		t.Errorf("MarketCapUSD / vol should stay nil on coingecko error")
	}
	if got.HasMarketStatus() {
		t.Errorf("HasMarketStatus = true on loader error")
	}
}

func TestEnrichCandidateForDecisionCardUsesRefreshBeforeSnapshotLoader(t *testing.T) {
	ctx := context.Background()
	c := Candidate{CanonicalSymbol: "ABC", SourcePlatforms: []string{"binance"}}
	refreshed := []PlatformMarketStatus{{Platform: "binance", Status: StatusActive, SourceKind: "api"}}
	loaderCalled := false
	deps := DecisionCardEnrichDeps{
		MarketStatusRefresher: func(ctx context.Context, c Candidate) ([]PlatformMarketStatus, error) {
			return refreshed, nil
		},
		MarketStatusLoader: func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
			loaderCalled = true
			return []PlatformMarketStatus{{Platform: "bybit", Status: StatusPreListing, SourceKind: "announcement"}}, nil
		},
		MarketStatusRefreshFallbackToSnapshot: true,
	}
	got := EnrichCandidateForDecisionCard(ctx, deps, c)
	if loaderCalled {
		t.Fatalf("snapshot loader should not run after successful pre-push refresh")
	}
	if len(got.MarketStatuses) != 1 || got.MarketStatuses[0].Platform != "binance" {
		t.Fatalf("MarketStatuses = %+v, want refreshed binance status", got.MarketStatuses)
	}
}

func TestEnrichCandidateForDecisionCardRefreshErrorFallsBackToSnapshotLoader(t *testing.T) {
	ctx := context.Background()
	c := Candidate{CanonicalSymbol: "ABC", SourcePlatforms: []string{"binance"}}
	deps := DecisionCardEnrichDeps{
		MarketStatusRefresher: func(ctx context.Context, c Candidate) ([]PlatformMarketStatus, error) {
			return nil, errors.New("refresh timeout")
		},
		MarketStatusLoader: func(ctx context.Context, canonical string) ([]PlatformMarketStatus, error) {
			return []PlatformMarketStatus{{Platform: "binance", Status: StatusActive, SourceKind: "api"}}, nil
		},
		MarketStatusRefreshFallbackToSnapshot: true,
	}
	got := EnrichCandidateForDecisionCard(ctx, deps, c)
	if len(got.MarketStatuses) != 1 || got.MarketStatuses[0].Platform != "binance" {
		t.Fatalf("MarketStatuses = %+v, want fallback snapshot status", got.MarketStatuses)
	}
	if len(got.EnrichErrors) != 1 || got.EnrichErrors[0] != "market_status_refresh: refresh timeout" {
		t.Fatalf("EnrichErrors = %v, want refresh timeout audit", got.EnrichErrors)
	}
}

func TestBuildCachedMarketStatusRefresherCachesWithinTick(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	fetches := 0
	refresher := BuildCachedMarketStatusRefresher([]InstrumentSource{
		{
			Platform:   "binance",
			MarketType: "usdm_futures",
			Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
				fetches++
				return []instrument.NormalizedInstrument{{
					Platform:         "binance",
					MarketType:       "usdm_futures",
					APISymbol:        "ABCUSDT",
					CanonicalSymbol:  "ABC",
					MarketSurface:    "perp",
					InstrumentKind:   "canonical",
					StatusRaw:        "TRADING",
					StatusNormalized: StatusActive,
				}}, nil
			},
		},
	}, config.ListingMarketStatusRefreshConfig{
		Enabled:             true,
		SourcePlatformsOnly: true,
		MaxConcurrency:      1,
		MaxRequestsPerTick:  10,
		CacheTTL:            time.Minute,
		FallbackToSnapshot:  true,
	}, func() time.Time { return now })
	if refresher == nil {
		t.Fatalf("refresher is nil")
	}
	c := Candidate{CanonicalSymbol: "ABC", SourcePlatforms: []string{"binance"}}
	first, err := refresher(ctx, c)
	if err != nil {
		t.Fatalf("first refresh err = %v", err)
	}
	second, err := refresher(ctx, c)
	if err != nil {
		t.Fatalf("second refresh err = %v", err)
	}
	if fetches != 1 {
		t.Fatalf("fetches = %d, want 1 cached fetch", fetches)
	}
	if len(first) != 1 || len(second) != 1 || first[0].Platform != "binance" || second[0].Platform != "binance" {
		t.Fatalf("refresh results = %+v / %+v", first, second)
	}
}

func TestBuildCachedMarketStatusRefresherFiltersSourcePlatformsAndEdgeX(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	fetched := map[string]int{}
	makeSource := func(platform string) InstrumentSource {
		return InstrumentSource{
			Platform:   platform,
			MarketType: "perp",
			Fetch: func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
				fetched[platform]++
				return []instrument.NormalizedInstrument{{
					Platform:         platform,
					MarketType:       "perp",
					APISymbol:        "ABC-USDT",
					CanonicalSymbol:  "ABC",
					MarketSurface:    "perp",
					InstrumentKind:   "canonical",
					StatusNormalized: StatusActive,
				}}, nil
			},
		}
	}
	refresher := BuildCachedMarketStatusRefresher([]InstrumentSource{
		makeSource("binance"),
		makeSource("bybit"),
		makeSource(edgexListedPlatformName),
	}, config.ListingMarketStatusRefreshConfig{
		Enabled:             true,
		SourcePlatformsOnly: true,
		IncludeEdgex:        true,
		MaxConcurrency:      2,
		MaxRequestsPerTick:  10,
		CacheTTL:            time.Minute,
		FallbackToSnapshot:  true,
	}, func() time.Time { return now })
	statuses, err := refresher(ctx, Candidate{CanonicalSymbol: "ABC", SourcePlatforms: []string{"binance"}})
	if err != nil {
		t.Fatalf("refresh err = %v", err)
	}
	if fetched["binance"] != 1 || fetched[edgexListedPlatformName] != 1 || fetched["bybit"] != 0 {
		t.Fatalf("fetched = %+v, want binance+edgeX only", fetched)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v, want 2", statuses)
	}
}
