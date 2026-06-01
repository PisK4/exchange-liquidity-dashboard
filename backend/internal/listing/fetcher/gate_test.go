package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGateSpotDecodesArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
            {"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable"},
            {"id":"ETH_USDT","base":"ETH","quote":"USDT","trade_status":"tradable"},
            {"id":"OLD_USDT","base":"OLD","quote":"USDT","trade_status":"untradable"}
        ]`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchGateSpot(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 spot pairs, got %d", len(got))
	}
	if got[0].Platform != "gate" || got[0].MarketType != "spot" || got[0].MarketSurface != "spot" {
		t.Fatalf("first row platform/market_type/surface = %q/%q/%q", got[0].Platform, got[0].MarketType, got[0].MarketSurface)
	}
}

func TestFetchGateUSDTFuturesPreservesQuantoMultiplier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
            {"name":"BTC_USDT","quanto_multiplier":"0.0001","in_delisting":false},
            {"name":"ETH_USDT","quanto_multiplier":"0.01","in_delisting":false}
        ]`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchGateUSDTFutures(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 futures contracts, got %d", len(got))
	}
	if got[0].MarketType != "usdt_futures" || got[0].MarketSurface != "perp" {
		t.Fatalf("first row market_type/surface = %q/%q", got[0].MarketType, got[0].MarketSurface)
	}
}

func TestFetchGateSpotSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchGateSpot(deps, srv.URL)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected error on HTTP 503")
	}
}
