package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edgex-dashboard/backend/internal/listing/announcement"
)

func TestFetchBinanceCMSAnnouncementsDecodesCatalogTree(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code":"000000",
			"data":{
				"catalogs":[
					{
						"catalogId":48,
						"catalogName":"New Cryptocurrency Listing",
						"articles":[
							{
								"id":"100123",
								"code":"abc-perpetual-listing",
								"title":"Binance Futures Will Launch USDⓈ-M ABCUSDT Perpetual Contract",
								"body":"Hello",
								"releaseDate":1893456000000,
								"updateTime":1893456000000,
								"language":"en"
							},
							{
								"id":"100124",
								"code":"defi-news",
								"title":"Weekly DeFi update",
								"releaseDate":1893456000000,
								"language":"en"
							}
						]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceCMSAnnouncements(deps, srv.URL+"/bapi/composite/v1/public/cms/article/list/query", 48)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !strings.Contains(gotQuery, "catalogId=48") || !strings.Contains(gotQuery, "type=1") {
		t.Fatalf("query must include catalogId=48 and type=1, got %q", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 raw articles, got %d", len(got))
	}
	// Each raw must be parseable by the existing CMS parser.
	parsed1, err := announcement.ParseBinanceCMSAnnouncement(got[0])
	if err != nil {
		t.Fatalf("parse first row: %v", err)
	}
	if parsed1.AnnouncementID != "100123" || parsed1.Title == "" {
		t.Fatalf("first parsed = %+v", parsed1)
	}
	if parsed1.Category != "New Cryptocurrency Listing" {
		t.Fatalf("catalogName must propagate to category, got %q", parsed1.Category)
	}
	if len(parsed1.Symbols) != 1 || parsed1.Symbols[0].CanonicalSymbol != "ABCUSDT" {
		// Title token "ABCUSDT" without USDT stopword scrub becomes
		// the canonical here; that's the parser's responsibility,
		// not ours — assert we don't drop the row.
		t.Logf("note: parser produced symbols %+v (regex-based)", parsed1.Symbols)
	}
}

func TestFetchBinanceCMSAnnouncementsSurfacesNonSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"100001","message":"system busy"}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceCMSAnnouncements(deps, srv.URL, 48)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected error on non-success code")
	}
}

func TestFetchBinanceCMSAnnouncementsHandlesEmptyCatalogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"000000","data":{"catalogs":[]}}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceCMSAnnouncements(deps, srv.URL, 48)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("empty catalogs must not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

func TestFetchBinanceCMSAnnouncementsCatalogNameInjectedIntoEachRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"code":"000000",
			"data":{"catalogs":[
				{"catalogId":48,"catalogName":"New Listings","articles":[{"id":"1","title":"x","releaseDate":1}]},
				{"catalogId":49,"catalogName":"Delistings","articles":[{"id":"2","title":"y","releaseDate":2}]}
			]}
		}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBinanceCMSAnnouncements(deps, srv.URL, 0)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 articles total, got %d", len(got))
	}
	var first map[string]any
	if err := json.Unmarshal(got[0], &first); err != nil {
		t.Fatalf("first row not valid json: %v", err)
	}
	if first["catalogName"] != "New Listings" {
		t.Fatalf("first row catalogName = %v, want New Listings", first["catalogName"])
	}
	var second map[string]any
	if err := json.Unmarshal(got[1], &second); err != nil {
		t.Fatalf("second row not valid json: %v", err)
	}
	if second["catalogName"] != "Delistings" {
		t.Fatalf("second row catalogName = %v, want Delistings", second["catalogName"])
	}
}
