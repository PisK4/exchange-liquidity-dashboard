package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeOneSucceedsOnHEAD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("expected HEAD, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	got := probeOne(srv.Client(), probeTask{platform: "binance", canonical: "BTC", url: srv.URL}, 5*time.Second)
	if !got.OK {
		t.Fatalf("expected OK, got %+v", got)
	}
	if got.Method != http.MethodHead {
		t.Errorf("method = %s, want HEAD", got.Method)
	}
}

func TestProbeOneFallsBackToGETWhenHEADRejected(t *testing.T) {
	var headCalled, getCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			headCalled = true
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodGet:
			getCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello"))
		}
	}))
	defer srv.Close()
	got := probeOne(srv.Client(), probeTask{platform: "okx", canonical: "ETH", url: srv.URL}, 5*time.Second)
	if !headCalled || !getCalled {
		t.Fatalf("expected both HEAD then GET, got head=%v get=%v", headCalled, getCalled)
	}
	if !got.OK {
		t.Fatalf("expected OK after GET fallback, got %+v", got)
	}
	if got.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", got.Method)
	}
}

func TestProbeOneFailsWhenBothMethodsReject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	got := probeOne(srv.Client(), probeTask{platform: "bingx", canonical: "ZM", url: srv.URL}, 5*time.Second)
	if got.OK {
		t.Fatalf("expected !OK, got %+v", got)
	}
	if got.HTTPStatus != http.StatusForbidden {
		t.Errorf("http_status = %d, want 403", got.HTTPStatus)
	}
	if got.Error == "" {
		t.Errorf("expected error message on failure")
	}
}
