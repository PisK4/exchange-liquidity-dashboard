package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchBitgetUSDTFuturesDecodesDataEnvelope(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code":"00000",
			"msg":"success",
			"data":[
				{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1893456000000","isRwa":false},
				{"symbol":"TSLAUSDT","baseCoin":"TSLA","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1893456000000","isRwa":true}
			]
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBitgetUSDTFutures(deps, srv.URL+"/api/v2/mix/market/contracts")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !strings.Contains(gotQuery, "productType=USDT-FUTURES") {
		t.Fatalf("query must request productType=USDT-FUTURES, got %q", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 instruments, got %d", len(got))
	}
	if got[0].CanonicalSymbol != "ABC" || got[0].InstrumentKind != "canonical" {
		t.Fatalf("first instrument = %+v", got[0])
	}
	if got[1].CanonicalSymbol != "TSLA" || got[1].InstrumentKind != "rwa" {
		t.Fatalf("second instrument (RWA) = %+v", got[1])
	}
}

func TestFetchBitgetUSDTFuturesSurfacesNonZeroCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"40404","msg":"not found"}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBitgetUSDTFutures(deps, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on non-success code")
	}
	if !strings.Contains(err.Error(), "40404") {
		t.Fatalf("error must include code, got %v", err)
	}
}
