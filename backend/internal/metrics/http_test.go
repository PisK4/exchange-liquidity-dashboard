package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeRoute(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "listing candidate id", path: "/api/listing/candidates/123", want: "/api/listing/candidates/{id}"},
		{name: "activity event id", path: "/api/activity/events/evt-1", want: "/api/activity/events/{id}"},
		{name: "activity review item id", path: "/api/activity/review/items/42", want: "/api/activity/review/items/{id}"},
		{name: "empty", path: "", want: "/"},
		{name: "static", path: "/api/health", want: "/api/health"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRoute(tt.path); got != tt.want {
				t.Fatalf("normalizeRoute(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestMiddlewarePreservesStatusCode(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
}
