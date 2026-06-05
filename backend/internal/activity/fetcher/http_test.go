package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPFetcherFetchesPayloadAndHashes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"abc"}`))
	}))
	defer srv.Close()

	client := NewHTTPFetcher(srv.Client(), 2*time.Second)
	got, err := client.Fetch(context.Background(), Request{URL: srv.URL, Platform: "binance", SourceGroup: "cms_article_list", FetchMode: "http_direct"})
	if err != nil {
		t.Fatalf("Fetch err=%v", err)
	}
	if string(got.Payload) != `{"title":"abc"}` || got.PayloadHash == "" || got.ContentHash == "" || got.HTTPStatus != 200 {
		t.Fatalf("result=%+v", got)
	}
}

func TestUTLSProfileMappingIncludesSpecProfiles(t *testing.T) {
	for _, name := range []string{"chrome120", "safari17_0"} {
		profile, ok := ProfileByName(name)
		if !ok || profile.Name == "" {
			t.Fatalf("ProfileByName(%q)=%+v ok=%v", name, profile, ok)
		}
	}
}
