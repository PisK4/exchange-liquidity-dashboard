package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	logCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgex_ops_log_count_total",
		Help: "Total number of application log events recorded through the metrics-aware logger facade.",
	}, []string{"level", "module", "func", "line"})

	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgex_ops_http_requests_total",
		Help: "Total number of HTTP requests handled by the Ops Intelligence backend.",
	}, []string{"method", "route", "status"})

	httpServerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "edgex_ops_http_server_duration_milliseconds",
		Help:    "HTTP server request duration in milliseconds for the Ops Intelligence backend.",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
	}, []string{"method", "route", "status"})
)

// ReportLog increments the application log counter for logger facade users.
// Keep labels low-cardinality; do not include free-form log messages.
func ReportLog(level, module, funcName string, line int) {
	logCount.WithLabelValues(level, module, funcName, strconv.Itoa(line)).Inc()
}

// ReportErrorLog mirrors the bridge-server error-log counter hook while
// keeping the label set bounded for Prometheus safety.
func ReportErrorLog(module, funcName string, line int) {
	ReportLog("error", module, funcName, line)
}

// ObserveHTTPServer records one handled HTTP request.
func ObserveHTTPServer(method, route string, statusCode int, duration time.Duration) {
	status := strconv.Itoa(statusCode)
	httpRequests.WithLabelValues(method, route, status).Inc()
	httpServerDuration.WithLabelValues(method, route, status).Observe(float64(duration.Milliseconds()))
}
