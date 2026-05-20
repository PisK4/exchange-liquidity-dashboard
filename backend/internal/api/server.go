package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"edgex-dashboard/backend/internal/collector"
	"edgex-dashboard/backend/internal/config"
)

type Server struct {
	cfg   config.Config
	store *collector.Store
}

func NewServer(cfg config.Config, store *collector.Store) *Server {
	return &Server{cfg: cfg, store: store}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/symbols", s.symbols)
	mux.HandleFunc("/api/symbols/coverage", s.coverage)
	mux.HandleFunc("/api/snapshot/liquidity", s.liquidity)
	mux.HandleFunc("/api/snapshot/quality", s.quality)
	mux.HandleFunc("/api/snapshot/share", s.share)
	mux.HandleFunc("/api/snapshot/top30", s.top30)
	mux.HandleFunc("/api/collection-status", s.collectionStatus)
	mux.HandleFunc("/api/runtime-config", s.runtimeConfig)
	return cors(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true, "service": "edgex-dashboard", "mode": "v1-real-adapter-attempts"})
}
func (s *Server) symbols(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"symbols": s.store.Symbols(), "mappings": s.store.SymbolMappings()})
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
	writeJSON(w, s.store.Top30(r.URL.Query().Get("surface"), strings.ToLower(r.URL.Query().Get("platform"))))
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
