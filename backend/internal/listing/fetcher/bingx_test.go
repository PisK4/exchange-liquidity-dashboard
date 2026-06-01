package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchBingXSpotDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"symbols":[
            {"symbol":"BTC-USDT","status":1,"baseAsset":"BTC","quoteAsset":"USDT"},
            {"symbol":"NEW-USDT","status":0,"baseAsset":"NEW","quoteAsset":"USDT"}
        ]}}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBingXSpot(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 spot symbols, got %d", len(got))
	}
	if got[0].Platform != "bingx" || got[0].MarketType != "spot" {
		t.Fatalf("first row platform/market_type = %q/%q", got[0].Platform, got[0].MarketType)
	}
}

func TestFetchBingXSwapDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":[
            {"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":1893456000000},
            {"symbol":"NCSKTSLA-USDT","status":1,"asset":"NCSKTSLA","quoteAsset":"USDT","launchTime":1893456000000}
        ]}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBingXSwap(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 swap symbols, got %d", len(got))
	}
	// NCSK* must come through normalized with instrument_kind=synthetic;
	// the refresh job is responsible for filtering at query time.
	var sawSynthetic bool
	for _, n := range got {
		if n.InstrumentKind == "synthetic" {
			sawSynthetic = true
		}
	}
	if !sawSynthetic {
		t.Fatalf("NCSK* must come back tagged synthetic")
	}
}

func TestFetchBingXSpotSurfacesNonZeroCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":80001,"msg":"unauthorized","data":{"symbols":[]}}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBingXSpot(deps, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected non-zero code to surface as error")
	}
}
