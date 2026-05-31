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

func TestFetchBybitAnnouncementsReshapesV5Envelope(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"list":[
					{
						"title":"ABC and XYZ USDT Perpetual Contracts Will Be Listed",
						"description":"Bybit launches ABC and XYZ perpetuals",
						"type":{"title":"New Listings","key":"new_crypto"},
						"tags":["Perpetual","Listings"],
						"url":"https://announcements.bybit.com/en-US/article/abc-xyz-perp-listing-bltf662314c211a8616/",
						"dateTimestamp":1893456000000,
						"publishTime":1893456000000
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitAnnouncements(deps, srv.URL+"/v5/announcements/index")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !strings.Contains(gotQuery, "locale=en-US") || !strings.Contains(gotQuery, "type=new_crypto") {
		t.Fatalf("query must include locale=en-US + type=new_crypto, got %q", gotQuery)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	parsed, err := announcement.ParseBybitAnnouncement(got[0])
	if err != nil {
		t.Fatalf("ParseBybitAnnouncement err = %v", err)
	}
	if parsed.AnnouncementID == "" {
		t.Fatalf("announcement_id must be derived from URL")
	}
	if !strings.Contains(parsed.AnnouncementID, "bltf662314c211a8616") {
		t.Fatalf("announcement_id should include bltf662314c211a8616 slug, got %q", parsed.AnnouncementID)
	}
	if parsed.Title == "" || parsed.PublishedAt == nil {
		t.Fatalf("parsed = %+v", parsed)
	}
	if len(parsed.Symbols) != 2 {
		t.Fatalf("expected 2 symbols (ABC, XYZ), got %+v", parsed.Symbols)
	}
}

func TestFetchBybitAnnouncementsSurfacesRetCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"retCode":10002,"retMsg":"request timeout"}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitAnnouncements(deps, srv.URL)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected error on retCode != 0")
	}
}

func TestFetchBybitAnnouncementsFallsBackToUrlAsIDWhenNoSlug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"result":{"list":[
				{"title":"Some perpetual contract listing","url":"https://announcements.bybit.com/en-US/article/abc/","publishTime":1893456000000}
			]}
		}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBybitAnnouncements(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	var obj map[string]any
	if err := json.Unmarshal(got[0], &obj); err != nil {
		t.Fatalf("reshape produced invalid json: %v", err)
	}
	if obj["id"] == nil || obj["id"] == "" {
		t.Fatalf("id must fall back to a non-empty value, got %v", obj["id"])
	}
}
