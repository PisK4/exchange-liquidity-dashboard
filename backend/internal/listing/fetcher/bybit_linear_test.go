package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchBybitLinearDecodesV5Envelope(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"list":[
					{"symbol":"ABCUSDT","status":"Trading","baseCoin":"ABC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"},
					{"symbol":"NEWUSDT","status":"PreLaunch","baseCoin":"NEW","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"}
				]
			}
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitLinear(deps, srv.URL+"/v5/market/instruments-info")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !strings.Contains(gotQuery, "category=linear") {
		t.Fatalf("query must include category=linear, got %q", gotQuery)
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

func TestFetchBybitLinearSurfacesRetCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bybit conventionally returns http 200 with retCode != 0 for
		// upstream rejections (rate limit, invalid param, internal).
		_, _ = w.Write([]byte(`{"retCode":10002,"retMsg":"request timeout"}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitLinear(deps, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on non-zero retCode")
	}
	if !strings.Contains(err.Error(), "10002") {
		t.Fatalf("error must include retCode 10002, got %v", err)
	}
}

func TestFetchBybitLinearEmptyListReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"retCode":0,"result":{"category":"linear","list":[]}}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitLinear(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("empty list must not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}
