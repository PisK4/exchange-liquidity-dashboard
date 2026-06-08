package fetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
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
				{"symbol":"ABCUSDT","baseCoin":"ABC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1893456000000","isRwa":"NO"},
				{"symbol":"TSLAUSDT","baseCoin":"TSLA","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1893456000000","isRwa":"YES"}
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

func TestFetchBitgetUSDTFuturesAllNormalizeFailuresSurfaceSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"00000","data":[{"baseCoin":"BAD","quoteCoin":"USDT","symbolStatus":"normal"}]}`))
	}))
	defer srv.Close()

	fetch := FetchBitgetUSDTFutures(HTTPDeps{Client: srv.Client()}, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected schema drift when every raw row fails normalization")
	}
	var drift *instrument.SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("expected SchemaDriftError, got %T %v", err, err)
	}
}
