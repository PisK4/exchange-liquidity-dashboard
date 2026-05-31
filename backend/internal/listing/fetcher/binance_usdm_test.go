package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchBinanceUSDMDecodesSymbolsEnvelope(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"timezone":"UTC",
			"symbols":[
				{"symbol":"ABCUSDT","status":"TRADING","baseAsset":"ABC","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000},
				{"symbol":"XYZUSDT","status":"PRE_LAUNCH","baseAsset":"XYZ","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000}
			]
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceUSDM(deps, srv.URL+"/fapi/v1/exchangeInfo")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 instruments, got %d (%+v)", len(got), got)
	}
	if got[0].CanonicalSymbol != "ABC" || got[0].StatusNormalized != "active" {
		t.Fatalf("first instrument = %+v", got[0])
	}
	if got[1].CanonicalSymbol != "XYZ" || got[1].StatusNormalized != "pre_listing" {
		t.Fatalf("second instrument = %+v", got[1])
	}
	if !strings.HasSuffix(gotPath, "/fapi/v1/exchangeInfo") {
		t.Fatalf("upstream path = %q", gotPath)
	}
}

func TestFetchBinanceUSDMSurfacesSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"timezone":"UTC"}`)) // no symbols field
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceUSDM(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("zero symbols must not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

func TestFetchBinanceUSDMPropagatesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceUSDM(deps, srv.URL)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected error on 429")
	}
}

func TestFetchBinanceUSDMSkipsRowsThatFailNormalization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First row missing symbol → normalizer returns SchemaDriftError
		// for that row only; the second row must still surface.
		_, _ = w.Write([]byte(`{"symbols":[
			{"status":"TRADING","baseAsset":"BAD","quoteAsset":"USDT"},
			{"symbol":"GOODUSDT","status":"TRADING","baseAsset":"GOOD","quoteAsset":"USDT","contractType":"PERPETUAL"}
		]}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceUSDM(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("per-row normalize error must not abort fetch, got %v", err)
	}
	if len(got) != 1 || got[0].CanonicalSymbol != "GOOD" {
		t.Fatalf("want only GOOD row, got %+v", got)
	}
}
