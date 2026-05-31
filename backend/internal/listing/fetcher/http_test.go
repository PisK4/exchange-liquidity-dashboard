package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewHTTPClientRejectsMalformedProxy locks the fail-loud contract:
// production cmd/dashboard relies on configuration-time validation so
// a typo in `runtime.exchange_proxy` does not silently route every
// fetcher direct-to-internet and bypass the operator's egress
// expectations.
func TestNewHTTPClientRejectsMalformedProxy(t *testing.T) {
	if _, err := NewHTTPClient(5*time.Second, "not a url"); err == nil {
		t.Fatalf("expected error for malformed proxy")
	}
	if _, err := NewHTTPClient(5*time.Second, "://missing-scheme"); err == nil {
		t.Fatalf("expected error for missing scheme")
	}
}

func TestNewHTTPClientAppliesTimeout(t *testing.T) {
	c, err := NewHTTPClient(0, "")
	if err != nil {
		t.Fatalf("NewHTTPClient err = %v", err)
	}
	if c.Timeout != DefaultRequestTimeout {
		t.Fatalf("zero timeout must fall back to DefaultRequestTimeout, got %s", c.Timeout)
	}
}

func TestFetchJSONReturnsBodyOn2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	d := HTTPDeps{Client: srv.Client()}
	got, err := d.fetchJSON(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("fetchJSON err = %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
}

func TestFetchJSONErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	d := HTTPDeps{Client: srv.Client()}
	_, err := d.fetchJSON(context.Background(), http.MethodGet, srv.URL, nil)
	if err == nil {
		t.Fatalf("expected error on 503")
	}
	if !strings.Contains(err.Error(), "http 503") {
		t.Fatalf("error must mention status code, got %v", err)
	}
}

func TestFetchJSONSetsUserAgentAndAccept(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	d := HTTPDeps{Client: srv.Client(), UserAgent: "edgex-test/1.0"}
	if _, err := d.fetchJSON(context.Background(), http.MethodGet, srv.URL, nil); err != nil {
		t.Fatalf("fetchJSON err = %v", err)
	}
	if gotUA != "edgex-test/1.0" {
		t.Fatalf("user-agent override not honored, got %q", gotUA)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept header missing, got %q", gotAccept)
	}
}

func TestFetchJSONPostBodySetsContentType(t *testing.T) {
	var gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d := HTTPDeps{Client: srv.Client()}
	if _, err := d.fetchJSON(context.Background(), http.MethodPost, srv.URL, []byte(`{"type":"meta"}`)); err != nil {
		t.Fatalf("fetchJSON err = %v", err)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type missing on POST, got %q", gotCT)
	}
	if string(gotBody) != `{"type":"meta"}` {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestFetchJSONFailsWhenClientNil(t *testing.T) {
	d := HTTPDeps{}
	if _, err := d.fetchJSON(context.Background(), http.MethodGet, "http://x", nil); err == nil {
		t.Fatalf("expected error when Client is nil")
	}
}
