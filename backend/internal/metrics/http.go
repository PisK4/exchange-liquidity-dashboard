package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Middleware records basic HTTP server metrics for the standard-library mux.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		ObserveHTTPServer(r.Method, normalizeRoute(r.URL.Path), recorder.status, time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("wrapped response writer does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func normalizeRoute(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/listing/candidates/"):
		return "/api/listing/candidates/{id}"
	case strings.HasPrefix(path, "/api/activity/events/"):
		return "/api/activity/events/{id}"
	case strings.HasPrefix(path, "/api/activity/review/items/"):
		return "/api/activity/review/items/{id}"
	case path == "":
		return "/"
	default:
		return path
	}
}
