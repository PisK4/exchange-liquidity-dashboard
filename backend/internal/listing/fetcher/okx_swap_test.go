package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchOKXSwapDecodesDataEnvelope(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code":"0",
			"msg":"",
			"data":[
				{"instId":"ABC-USDT-SWAP","state":"live","baseCcy":"ABC","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000"},
				{"instId":"NEW-USDT-SWAP","state":"preopen","baseCcy":"NEW","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000"}
			]
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchOKXSwap(deps, srv.URL+"/api/v5/public/instruments")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !strings.Contains(gotQuery, "instType=SWAP") {
		t.Fatalf("query must request instType=SWAP, got %q", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 instruments, got %d", len(got))
	}
	if got[0].CanonicalSymbol != "ABC" || got[0].StatusNormalized != "active" {
		t.Fatalf("first instrument = %+v", got[0])
	}
	if got[1].CanonicalSymbol != "NEW" || got[1].StatusNormalized != "pre_listing" {
		t.Fatalf("second instrument = %+v", got[1])
	}
}

func TestFetchOKXSwapSurfacesNonZeroCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"50011","msg":"Request too frequent"}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchOKXSwap(deps, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on non-zero code")
	}
	if !strings.Contains(err.Error(), "50011") {
		t.Fatalf("error must include code 50011, got %v", err)
	}
}
