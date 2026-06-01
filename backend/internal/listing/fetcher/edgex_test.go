package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const edgeXMetaSamplePayload = `{
  "code":"SUCCESS",
  "data":{
    "coinList":[
      {"coinId":"1000","coinName":"BTC"},
      {"coinId":"1001","coinName":"ETH"}
    ],
    "contractList":[
      {"contractId":"10000001","baseCoinId":"1000","contractName":"BTCUSD","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001"},
      {"contractId":"10000002","baseCoinId":"1001","contractName":"ETHUSD","enableTrade":true,"enableDisplay":true,"tickSize":"0.01","stepSize":"0.001"}
    ]
  }
}`

func TestFetchEdgeXPerpV1ReturnsNormalizedContracts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(edgeXMetaSamplePayload))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchEdgeXPerpV1(deps, srv.URL+"/api/v1/public/meta/getMetaData")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 contracts, got %d", len(got))
	}
	if got[0].Platform != "edgeX" || got[0].MarketType != "perp_v1" {
		t.Fatalf("first row platform/market_type = %q/%q", got[0].Platform, got[0].MarketType)
	}
}

func TestFetchEdgeXPerpV2UsesPerpV2MarketType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(edgeXMetaSamplePayload))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchEdgeXPerpV2(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) == 0 || got[0].MarketType != "perp_v2" {
		t.Fatalf("first row market_type = %q, want perp_v2", got[0].MarketType)
	}
}

func TestFetchEdgeXSpotUsesSpotMarketType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(edgeXMetaSamplePayload))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchEdgeXSpot(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) == 0 || got[0].MarketType != "spot" || got[0].MarketSurface != "spot" {
		t.Fatalf("first row market_type/surface = %q/%q", got[0].MarketType, got[0].MarketSurface)
	}
}

func TestFetchEdgeXSurfacesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchEdgeXPerpV1(deps, srv.URL)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected HTTP error to surface")
	}
}
