package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/listing/announcement"
)

func withFastHyperliquidAnnouncementRetry(t *testing.T) {
	t.Helper()
	old := hyperliquidAnnouncementRetryDelay
	hyperliquidAnnouncementRetryDelay = time.Millisecond
	t.Cleanup(func() { hyperliquidAnnouncementRetryDelay = old })
}

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

func TestFetchHyperliquidAnnouncementsRetriesTruncatedJSON(t *testing.T) {
	withFastHyperliquidAnnouncementRetry(t)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			_, _ = w.Write([]byte(`{"entries":[`))
			return
		}
		_, _ = w.Write([]byte(`{"entries":[{"hash":"h1","title":"New listing: ABC-USD perps"}]}`))
	}))
	defer srv.Close()

	fetch := FetchHyperliquidAnnouncements(HTTPDeps{Client: srv.Client()}, srv.URL)
	got, err := fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch err = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want retry once", requests)
	}
	if len(got) != 1 {
		t.Fatalf("want one row after retry, got %d", len(got))
	}
}

func TestFetchHyperliquidAnnouncementsReportsTruncatedJSONContext(t *testing.T) {
	withFastHyperliquidAnnouncementRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":[`))
	}))
	defer srv.Close()

	fetch := FetchHyperliquidAnnouncements(HTTPDeps{Client: srv.Client()}, srv.URL)
	_, err := fetch(context.Background())
	if err == nil {
		t.Fatalf("expected truncated JSON error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "decode envelope url=") || !strings.Contains(msg, "bytes=") || !strings.Contains(msg, "attempt=2/2") {
		t.Fatalf("error lacks diagnostics: %v", err)
	}
}
