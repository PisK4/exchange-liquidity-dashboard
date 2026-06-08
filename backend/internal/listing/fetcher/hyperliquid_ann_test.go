package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edgex-ops-intelligence/backend/internal/listing/announcement"
)

func TestFetchHyperliquidAnnouncementsUnwrapsEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"entries":[
				{"hash":"h1","title":"New listing: NIL-USD perps","createdAt":"2026-05-30T10:00:00Z"},
				{"hash":"h2","title":"Added spot PUMP","createdAt":"2026-05-30T11:00:00Z"}
			]
		}`))
	}))
	defer srv.Close()

	fetch := FetchHyperliquidAnnouncements(HTTPDeps{Client: srv.Client()}, srv.URL+"/mainnet/entries.json")
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	var row map[string]any
	if err := json.Unmarshal(got[0], &row); err != nil {
		t.Fatalf("invalid row json: %v", err)
	}
	if row["sourceUrl"] != srv.URL+"/mainnet/entries.json" || row["sourceModule"] != "hyperliquid_entries" {
		t.Fatalf("source metadata not injected: %+v", row)
	}
	parsed, err := announcement.ParseHyperliquidAnnouncement(got[0])
	if err != nil {
		t.Fatalf("ParseHyperliquidAnnouncement err = %v", err)
	}
	if len(parsed.Symbols) != 1 || parsed.Symbols[0].CanonicalSymbol != "NIL" {
		t.Fatalf("unexpected parsed symbols = %+v", parsed.Symbols)
	}
}

func TestFetchHyperliquidAnnouncementsRejectsSchemaDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	fetch := FetchHyperliquidAnnouncements(HTTPDeps{Client: srv.Client()}, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("missing entries should decode as an empty list, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty list on absent entries, got %d", len(got))
	}
}
