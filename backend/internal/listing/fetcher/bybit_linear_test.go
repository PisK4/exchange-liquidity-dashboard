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

func TestFetchBybitLinearDecodesV5Envelope(t *testing.T) {
	var gotStatuses []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStatuses = append(gotStatuses, r.URL.Query().Get("status"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("status") {
		case "Trading":
			_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"list":[
					{"symbol":"ABCUSDT","status":"Trading","baseCoin":"ABC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"}
				]
			}
		}`))
		case "PreLaunch":
			_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"list":[
					{"symbol":"NEWUSDT","status":"PreLaunch","baseCoin":"NEW","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"}
				]
			}
		}`))
		default:
			http.Error(w, "missing status", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitLinear(deps, srv.URL+"/v5/market/instruments-info")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(gotStatuses) != 2 || gotStatuses[0] != "Trading" || gotStatuses[1] != "PreLaunch" {
		t.Fatalf("expected Trading and PreLaunch requests, got %v", gotStatuses)
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

func TestFetchBybitLinearPaginatesAndDedupes(t *testing.T) {
	var sawCursor bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := r.URL.Query().Get("status")
		cursor := r.URL.Query().Get("cursor")
		if status == "Trading" && cursor == "" {
			_, _ = w.Write([]byte(`{"retCode":0,"result":{"list":[
				{"symbol":"ABCUSDT","status":"Trading","baseCoin":"ABC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"},
				{"symbol":"DUPUSDT","status":"Trading","baseCoin":"DUP","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"}
			],"nextPageCursor":"next-page"}}`))
			return
		}
		if status == "Trading" && cursor == "next-page" {
			sawCursor = true
			_, _ = w.Write([]byte(`{"retCode":0,"result":{"list":[
				{"symbol":"PAGEUSDT","status":"Trading","baseCoin":"PAGE","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"},
				{"symbol":"DUPUSDT","status":"Trading","baseCoin":"DUP","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"}
			]}}`))
			return
		}
		if status == "PreLaunch" {
			_, _ = w.Write([]byte(`{"retCode":0,"result":{"list":[
				{"symbol":"NEWUSDT","status":"PreLaunch","baseCoin":"NEW","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"},
				{"symbol":"DUPUSDT","status":"PreLaunch","baseCoin":"DUP","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"}
			]}}`))
			return
		}
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer srv.Close()

	fetch := FetchBybitLinear(HTTPDeps{Client: srv.Client()}, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !sawCursor {
		t.Fatalf("expected fetcher to follow nextPageCursor")
	}
	if len(got) != 4 {
		t.Fatalf("want 4 unique instruments, got %d (%+v)", len(got), got)
	}
	seen := map[string]bool{}
	for _, inst := range got {
		seen[inst.APISymbol] = true
	}
	for _, sym := range []string{"ABCUSDT", "DUPUSDT", "PAGEUSDT", "NEWUSDT"} {
		if !seen[sym] {
			t.Fatalf("missing %s from result %+v", sym, got)
		}
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

func TestFetchBybitLinearAllNormalizeFailuresSurfaceSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") == "Trading" {
			_, _ = w.Write([]byte(`{"retCode":0,"result":{"list":[{"status":"Trading","baseCoin":"BAD"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"retCode":0,"result":{"list":[]}}`))
	}))
	defer srv.Close()

	fetch := FetchBybitLinear(HTTPDeps{Client: srv.Client()}, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected schema drift when every raw row fails normalization")
	}
	var drift *instrument.SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("expected SchemaDriftError, got %T %v", err, err)
	}
}
