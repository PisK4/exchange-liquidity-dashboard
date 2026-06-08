package fetcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
	if got.AttemptCount != 1 || got.ElapsedMS < 0 {
		t.Fatalf("metadata=%+v", got)
	}
}

func TestHTTPFetcherRetriesTransportErrorThenSucceeds(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, io.EOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})}
	fetcher := NewHTTPFetcher(client, time.Second, WithRetryBackoff(func(attempt int) time.Duration { return 0 }), WithProxyUsed(true))

	got, err := fetcher.Fetch(context.Background(), Request{URL: "https://example.test/activity", Platform: "bingx", SourceGroup: "openapi_notice", FetchMode: "http_direct_json"})
	if err != nil {
		t.Fatalf("Fetch err=%v", err)
	}
	if calls != 2 || got.AttemptCount != 2 || got.ProxyUsed != true || string(got.Payload) != "ok" {
		t.Fatalf("calls=%d result=%+v", calls, got)
	}
}

func TestHTTPFetcherRetriesRetryableHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`retry later`))
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`ok`))
			}))
			defer srv.Close()

			fetcher := NewHTTPFetcher(srv.Client(), 2*time.Second, WithRetryBackoff(func(attempt int) time.Duration { return 0 }))
			got, err := fetcher.Fetch(context.Background(), Request{URL: srv.URL, Platform: "gate", SourceGroup: "launchpool_project_list", FetchMode: "utls_proxy_json"})
			if err != nil {
				t.Fatalf("Fetch err=%v", err)
			}
			if calls != 2 || got.HTTPStatus != http.StatusOK || got.AttemptCount != 2 {
				t.Fatalf("calls=%d result=%+v", calls, got)
			}
		})
	}
}

func TestHTTPFetcherDoesNotRetryStable4xx(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`not allowed`))
			}))
			defer srv.Close()

			fetcher := NewHTTPFetcher(srv.Client(), 2*time.Second, WithRetryBackoff(func(attempt int) time.Duration { return 0 }))
			got, err := fetcher.Fetch(context.Background(), Request{URL: srv.URL, Platform: "okx", SourceGroup: "help_announcement", FetchMode: "http_direct"})
			if err != nil {
				t.Fatalf("Fetch err=%v", err)
			}
			if calls != 1 || got.HTTPStatus != status || got.AttemptCount != 1 {
				t.Fatalf("calls=%d result=%+v", calls, got)
			}
		})
	}
}

func TestHTTPFetcherWrapsFetchErrorMetadata(t *testing.T) {
	fetcher := NewHTTPFetcher(http.DefaultClient, time.Second, WithRetryBackoff(func(attempt int) time.Duration { return 0 }))
	_, err := fetcher.Fetch(context.Background(), Request{URL: "://bad-url", Platform: "binance", SourceGroup: "cms_article_list", FetchMode: "http_direct"})
	if err == nil {
		t.Fatalf("Fetch err=nil")
	}
	var fetchErr *activity.FetchError
	if !errors.As(err, &fetchErr) {
		t.Fatalf("err=%T %[1]v, want FetchError", err)
	}
	if fetchErr.Metadata.SourceURL != "://bad-url" || fetchErr.Metadata.FetchMode != "http_direct" || fetchErr.Metadata.AttemptCount != 1 || fetchErr.Metadata.LastErrorMessage == "" {
		t.Fatalf("metadata=%+v", fetchErr.Metadata)
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
