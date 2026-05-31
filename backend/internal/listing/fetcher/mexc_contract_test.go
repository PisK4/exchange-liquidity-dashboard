package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchMEXCContractDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success":true,
			"code":0,
			"data":[
				{"symbol":"ABC_USDT","baseCoin":"ABC","quoteCoin":"USDT","state":0,"openingTime":1893456000000},
				{"symbol":"NEW_USDT","baseCoin":"NEW","quoteCoin":"USDT","state":1,"openingTime":1893456000000}
			]
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchMEXCContract(deps, srv.URL+"/api/v1/contract/detail")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].CanonicalSymbol != "ABC" || got[0].StatusNormalized != "active" {
		t.Fatalf("first instrument = %+v", got[0])
	}
	if got[1].CanonicalSymbol != "NEW" || got[1].StatusNormalized != "pre_listing" {
		t.Fatalf("second instrument = %+v", got[1])
	}
}

func TestFetchMEXCContractSurfacesNonZeroCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"code":500,"message":"internal error"}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchMEXCContract(deps, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on non-zero code")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error must include code, got %v", err)
	}
}
