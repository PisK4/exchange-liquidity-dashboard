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
						"title":"ABCUSDT and XYZUSDT Perpetual Contracts Will Be Listed",
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
	if parsed.AnnouncementID != "bltf662314c211a8616" {
		t.Fatalf("announcement_id must be the bare Contentstack slug, got %q", parsed.AnnouncementID)
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

// TestDeriveBybitAnnouncementIDExtractsConcatenatedSlug pins the
// SLXUSDT regression: real Bybit URLs concatenate the Contentstack
// slug onto the end of the human-readable URL segment rather than
// emitting it as a standalone path component. The previous extractor
// only recognised the latter form and silently returned the full URL
// as announcement_id, which overflowed downstream fingerprint columns.
func TestDeriveBybitAnnouncementIDExtractsConcatenatedSlug(t *testing.T) {
	cases := map[string]string{
		"https://announcements.bybit.com/en-US/article/new-listing-slxusdt-perpetual-contract-with-up-to-20x-leverage-blte2872c09549e9399":  "blte2872c09549e9399",
		"https://announcements.bybit.com/en-US/article/new-listing-slxusdt-perpetual-contract-with-up-to-20x-leverage-blte2872c09549e9399/": "blte2872c09549e9399",
		"https://announcements.bybit.com/en-US/article/abc-xyz-perp-listing-bltf662314c211a8616/":                                           "bltf662314c211a8616",
		"https://announcements.bybit.com/en-US/article/some-listing-without-slug":                                                           "https://announcements.bybit.com/en-US/article/some-listing-without-slug",
	}
	for url, want := range cases {
		if got := deriveBybitAnnouncementID(url); got != want {
			t.Errorf("deriveBybitAnnouncementID(%q) = %q, want %q", url, got, want)
		}
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
