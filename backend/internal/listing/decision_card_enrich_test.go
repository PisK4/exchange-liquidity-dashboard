package listing

import (
	"context"
	"errors"
	"testing"
	"time"
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
