package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const lighterFixturePayload = `{
  "code": "OK",
  "order_book_details": [
    {"symbol":"BTC","market_id":1,"market_type":"perp","status":"active"},
    {"symbol":"ETH","market_id":2,"market_type":"perp","status":"active"},
    {"symbol":"BTC","market_id":100,"market_type":"spot","status":"active"}
  ]
}`

func TestFetchLighterPerpReturnsOnlyPerpSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(lighterFixturePayload))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	// Use an isolated cache for this test so other tests don't poison the
	// global TTL window.
	cache := newRequestCache()
	fetch := newLighterFetcher(deps, srv.URL, "perp", cache)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 perp rows, got %d", len(got))
	}
	for _, n := range got {
		if n.MarketSurface != "perp" {
			t.Fatalf("non-perp row leaked: %+v", n)
		}
	}
}

func TestFetchLighterSpotReturnsOnlySpotSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(lighterFixturePayload))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	cache := newRequestCache()
	fetch := newLighterFetcher(deps, srv.URL, "spot", cache)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 spot row, got %d", len(got))
	}
	if got[0].MarketSurface != "spot" || got[0].MarketType != "spot" {
		t.Fatalf("spot row mis-tagged: %+v", got[0])
	}
}

func TestFetchLighterPerpAndSpotShareHTTPRoundTrip(t *testing.T) {
	// Spec F6: the perp + spot fetchers must reuse a single HTTP
	// round-trip per tick.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(lighterFixturePayload))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	cache := newRequestCache()
	perpFetch := newLighterFetcher(deps, srv.URL, "perp", cache)
	spotFetch := newLighterFetcher(deps, srv.URL, "spot", cache)

	if _, err := perpFetch(context.Background()); err != nil {
		t.Fatalf("perp fetch err = %v", err)
	}
	if _, err := spotFetch(context.Background()); err != nil {
		t.Fatalf("spot fetch err = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("perp + spot should share one HTTP call, got %d", got)
	}
}

func TestFetchLighterPerpAndSpotPreserveBothBTCRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(lighterFixturePayload))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	cache := newRequestCache()
	perpFetch := newLighterFetcher(deps, srv.URL, "perp", cache)
	spotFetch := newLighterFetcher(deps, srv.URL, "spot", cache)
	perp, err := perpFetch(context.Background())
	if err != nil {
		t.Fatalf("perp err = %v", err)
	}
	spot, err := spotFetch(context.Background())
	if err != nil {
		t.Fatalf("spot err = %v", err)
	}
	var perpBTC, spotBTC bool
	for _, n := range perp {
		if n.CanonicalSymbol == "BTC" {
			perpBTC = true
		}
	}
	for _, n := range spot {
		if n.CanonicalSymbol == "BTC" {
			spotBTC = true
		}
	}
	if !perpBTC || !spotBTC {
		t.Fatalf("BTC must survive on both surfaces (perp=%t spot=%t) — snapshot PK (platform,market_type,api_symbol) must not collide", perpBTC, spotBTC)
	}
}
