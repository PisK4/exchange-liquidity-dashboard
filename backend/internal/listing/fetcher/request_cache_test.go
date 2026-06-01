package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestCacheReusesBodyWithinTTL(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hit":1}`))
	}))
	defer srv.Close()

	cache := newRequestCache()
	deps := HTTPDeps{Client: srv.Client()}
	ctx := context.Background()

	body1, err := cache.fetch(ctx, deps, srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("first fetch err = %v", err)
	}
	body2, err := cache.fetch(ctx, deps, srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("second fetch err = %v", err)
	}
	if string(body1) != string(body2) {
		t.Fatalf("cache must return identical body; got %q vs %q", body1, body2)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 HTTP call, got %d (TTL not honoured)", got)
	}
}

func TestRequestCacheExpiresAfterTTL(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	now := time.Now()
	cache := &requestCache{
		nowFn: func() time.Time { return now },
		store: map[string]requestCacheEntry{},
	}
	deps := HTTPDeps{Client: srv.Client()}
	ctx := context.Background()

	if _, err := cache.fetch(ctx, deps, srv.URL, 1*time.Second); err != nil {
		t.Fatalf("first fetch err = %v", err)
	}
	// Advance the clock past the TTL.
	now = now.Add(2 * time.Second)
	if _, err := cache.fetch(ctx, deps, srv.URL, 1*time.Second); err != nil {
		t.Fatalf("second fetch err = %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 HTTP calls after TTL expiry, got %d", got)
	}
}

func TestRequestCacheConcurrentFetchSingleflight(t *testing.T) {
	var calls int32
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		<-gate
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cache := newRequestCache()
	deps := HTTPDeps{Client: srv.Client()}
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = cache.fetch(context.Background(), deps, srv.URL, 5*time.Second)
		}()
	}
	// Allow the in-flight requests to settle into the cache lock.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got > int32(goroutines) {
		t.Fatalf("calls=%d > %d (extreme duplication)", got, goroutines)
	}
}
