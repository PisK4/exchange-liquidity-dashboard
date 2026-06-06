package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edgex-ops-intelligence/backend/internal/listing/announcement"
)

func TestFetchBitgetAnnouncementsReshapesV2Envelope(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code":"00000",
			"msg":"success",
			"requestTime":1688008631614,
			"data":[
				{
					"annId":"12345",
					"annTitle":"ABC USDT-M Perpetual Contract Launch Notice",
					"annDesc":"Bitget launches ABC perpetual",
					"cTime":"1893456000000",
					"language":"en_US",
					"annUrl":"https://www.bitget.com/support/articles/12345",
					"annType":"coin_listings",
					"annSubType":"futures"
				},
				{
					"annId":"12346",
					"annTitle":"XYZ Spot Listing",
					"cTime":"1893456000000",
					"language":"en_US",
					"annUrl":"https://www.bitget.com/support/articles/12346",
					"annType":"coin_listings",
					"annSubType":"spot"
				}
			]
		}`))
	}))
	defer srv.Close()

	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBitgetAnnouncements(deps, srv.URL+"/api/v2/public/annoucements")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if !strings.Contains(gotQuery, "annType=coin_listings") || !strings.Contains(gotQuery, "language=en_US") {
		t.Fatalf("query must request coin_listings + en_US, got %q", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	parsed1, err := announcement.ParseBitgetAnnouncement(got[0])
	if err != nil {
		t.Fatalf("ParseBitgetAnnouncement err = %v", err)
	}
	if parsed1.AnnouncementID != "12345" || parsed1.Title == "" {
		t.Fatalf("first parsed = %+v", parsed1)
	}
	if parsed1.Category != "coin_listings/futures" {
		t.Fatalf("category must combine annType + annSubType, got %q", parsed1.Category)
	}
	if len(parsed1.Symbols) != 1 || parsed1.Symbols[0].CanonicalSymbol != "ABC" {
		t.Fatalf("perp listing must emit ABC symbol, got %+v", parsed1.Symbols)
	}
	parsed2, err := announcement.ParseBitgetAnnouncement(got[1])
	if err != nil {
		t.Fatalf("ParseBitgetAnnouncement second row err = %v", err)
	}
	if len(parsed2.Symbols) != 0 || parsed2.ParseConfidence != "audit_only" {
		t.Fatalf("spot listing must be audit_only with no symbols, got %+v", parsed2)
	}
}

func TestFetchBitgetAnnouncementsSurfacesNonSuccessCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"40001","msg":"bad request"}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBitgetAnnouncements(deps, srv.URL)
	if _, err := fetch(context.Background()); err == nil {
		t.Fatalf("expected error on code != 00000")
	}
}

func TestFetchBitgetAnnouncementsHandlesEmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"00000","msg":"success","data":[]}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBitgetAnnouncements(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("empty data must not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestFetchBitgetAnnouncementsPreservesRawForSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"00000","data":[{"annId":"1","annTitle":"t","cTime":"123","annType":"coin_listings","annSubType":""}]}`))
	}))
	defer srv.Close()
	deps := HTTPDeps{Client: srv.Client()}
	fetch := FetchBitgetAnnouncements(deps, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(got[0], &obj); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if obj["category"] != "coin_listings" {
		t.Fatalf("empty annSubType must produce category=annType, got %v", obj["category"])
	}
}
