package api

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

// Version is the human-readable build identifier surfaced by the
// /api/health endpoint. It is "dev" by default and is intended to be
// overridden at link time via -ldflags="-X edgex-ops-intelligence/backend/internal/api.Version=v1.0.0-N-gXXXXX".
var Version = "dev"

const (
	healthServiceName = "edgex-ops-intelligence"
	healthProductName = "edgex-ops-intelligence"
	healthDisplayName = "EdgeX Ops Intelligence"
)

type Server struct {
	cfg                    config.Config
	store                  StoreReader
	listing                ListingReader
	activity               ActivityStore
	decisions              DecisionWriter
	dispatcher             DecisionDispatcher
	callback               ListingCallbackConfig
	activityDecisionSecret string
	activityNow            func() time.Time
}

type StoreReader interface {
	MySQLBacked() bool
	PingDB(context.Context) error
	SnapshotRowCounts(context.Context) (map[string]int64, error)
	Symbols() []string
	SymbolMappings() []domain.SymbolSub
	OpsIntelligenceMeta() map[string]any
	Coverage() map[string]any
	Liquidity(string) map[string]any
	Quality(string) map[string]any
	Share(string) map[string]any
	Top30(string, string) map[string]any
	Top30Divergence() domain.Top30DivergenceSnapshot
	CollectionStatus() map[string]any
	RuntimeConfig() config.Runtime
}

func NewServer(cfg config.Config, store StoreReader, opts ...Option) *Server {
	s := &Server{cfg: cfg, store: store}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/readiness", s.readiness)
	mux.HandleFunc("/api/ops-intelligence/meta", s.opsIntelligenceMeta)
	mux.HandleFunc("/api/symbols", s.symbols)
	mux.HandleFunc("/api/symbols/coverage", s.coverage)
	mux.HandleFunc("/api/snapshot/liquidity", s.liquidity)
	mux.HandleFunc("/api/snapshot/quality", s.quality)
	mux.HandleFunc("/api/snapshot/share", s.share)
	mux.HandleFunc("/api/snapshot/top30", s.top30)
	mux.HandleFunc("/api/snapshot/top30/divergence", s.top30Divergence)
	mux.HandleFunc("/api/collection-status", s.collectionStatus)
	mux.HandleFunc("/api/runtime-config", s.runtimeConfig)
	s.registerListingRoutes(mux)
	s.registerActivityRoutes(mux)
	return cors(mux)
}

// health returns rich liveness + observability JSON. It always returns
// HTTP 200 as long as the process is responsive -- container HEALTHCHECK
// directives MUST point here, NOT at /api/readiness, otherwise an
// upstream exchange hiccup would put the container into a restart loop.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	deps := map[string]any{}

	// MySQL block. Surfaces ping latency + estimated snapshot row counts
	// (information_schema.TABLE_ROWS, O(1)). Using exact COUNT(*) here
	// would lock and scan, which is exactly what we cannot do in a
	// liveness path. When the store is in in-memory mode the deps.mysql
	// block is omitted entirely so callers can tell the two modes
	// apart.
	if s.store.MySQLBacked() {
		mysql := map[string]any{}
		begin := time.Now()
		if err := s.store.PingDB(ctx); err != nil {
			mysql["ok"] = false
			mysql["error"] = err.Error()
		} else {
			mysql["ok"] = true
			mysql["latency_ms"] = time.Since(begin).Milliseconds()
		}
		if counts, err := s.store.SnapshotRowCounts(ctx); err == nil && counts != nil {
			mysql["snapshot_row_counts"] = counts
			mysql["snapshot_row_counts_source"] = "information_schema.TABLE_ROWS (estimate)"
		}
		deps["mysql"] = mysql
	}

	// Catalog freshness. Surfaces the count of platforms x canonicals
	// loaded by config.Load and the runtime catalog generated_at.
	deps["catalog"] = map[string]any{
		"symbols":         len(s.cfg.Symbols),
		"platforms":       len(s.cfg.Platforms),
		"runtime_proxy":   s.cfg.Runtime.ExchangeProxy != "",
		"build_version":   Version,
		"go_version":      runtime.Version(),
		"go_max_procs":    runtime.GOMAXPROCS(0),
		"goroutine_count": runtime.NumGoroutine(),
	}

	writeJSON(w, map[string]any{
		"ok":            true,
		"service":       healthServiceName,
		"product":       healthProductName,
		"display_name":  healthDisplayName,
		"mode":          "v1-real-adapter-attempts",
		"build_version": Version,
		"deps":          deps,
	})
}

// readiness is the "should this instance receive traffic?" gate. It is
// distinct from /api/health in that it MAY return 503 when the local
// process is technically alive but cannot serve a useful response yet
// (e.g. MySQL is unreachable). External load balancers / k8s probes /
// CI smoke tests that need a hard yes/no should hit this endpoint.
//
// Note: container HEALTHCHECK must point at /api/health (liveness), NOT
// here -- otherwise a transient external-dependency outage would put
// the container into a restart loop and amplify the outage.
func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]any{}
	httpStatus := http.StatusOK
	if s.store.MySQLBacked() {
		begin := time.Now()
		if err := s.store.PingDB(ctx); err != nil {
			checks["mysql"] = map[string]any{"ok": false, "error": err.Error()}
			httpStatus = http.StatusServiceUnavailable
		} else {
			checks["mysql"] = map[string]any{"ok": true, "latency_ms": time.Since(begin).Milliseconds()}
		}
	} else {
		checks["mysql"] = map[string]any{"ok": true, "mode": "in_memory"}
	}
	checks["catalog"] = map[string]any{
		"ok":      len(s.cfg.Symbols) > 0,
		"symbols": len(s.cfg.Symbols),
	}
	if len(s.cfg.Symbols) == 0 {
		httpStatus = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ready":         httpStatus == http.StatusOK,
		"build_version": Version,
		"checks":        checks,
	})
}
func (s *Server) symbols(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"symbols": s.store.Symbols(), "mappings": s.store.SymbolMappings()})
}
func (s *Server) opsIntelligenceMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.OpsIntelligenceMeta())
}
func (s *Server) coverage(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.store.Coverage()) }
func (s *Server) liquidity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Liquidity(r.URL.Query().Get("symbol")))
}
func (s *Server) quality(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Quality(r.URL.Query().Get("symbol")))
}
func (s *Server) share(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Share(r.URL.Query().Get("window")))
}
func (s *Server) top30(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Top30(r.URL.Query().Get("surface"), r.URL.Query().Get("platform")))
}
func (s *Server) top30Divergence(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Top30Divergence())
}
func (s *Server) collectionStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.CollectionStatus())
}
func (s *Server) runtimeConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.RuntimeConfig())
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(payload)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
