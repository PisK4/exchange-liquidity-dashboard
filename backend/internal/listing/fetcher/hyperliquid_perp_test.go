package fetcher

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchHyperliquidPerpDecodesUniverse(t *testing.T) {
	var gotBody []byte
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"universe":[
				{"name":"ABC","maxLeverage":50,"isDelisted":false},
				{"name":"OLD","maxLeverage":3,"isDelisted":true}
			]
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchHyperliquidPerp(deps, srv.URL+"/info")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("hyperliquid /info must be POST, got %s", gotMethod)
	}
	if !strings.Contains(string(gotBody), `"type":"meta"`) {
		t.Fatalf("request body must include type=meta, got %q", gotBody)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 perps, got %d", len(got))
	}
	if got[0].CanonicalSymbol != "ABC" || got[0].StatusNormalized != "active" {
		t.Fatalf("active row = %+v", got[0])
	}
	if got[1].CanonicalSymbol != "OLD" || got[1].StatusNormalized != "delisted" || !got[1].DelistFlag {
		t.Fatalf("delisted row = %+v", got[1])
	}
}

func TestFetchHyperliquidPerpSurfacesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limit", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchHyperliquidPerp(deps, srv.URL)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected error on 429")
	}
}
