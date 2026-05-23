package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edgex-dashboard/backend/internal/collector"
	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

func TestHealthSurfacesBuildVersionAndCatalogStats(t *testing.T) {
	prev := Version
	Version = "v1.0.0-test"
	defer func() { Version = prev }()

	cfg := config.Config{
		Symbols:   []domain.SymbolSub{{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC"}},
		Platforms: []string{"edgeX"},
	}
	store := collector.NewStore(cfg)
	srv := NewServer(cfg, store)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.health(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health must always return 200 (liveness), got %d", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["build_version"] != "v1.0.0-test" {
		t.Errorf("build_version = %v, want v1.0.0-test", got["build_version"])
	}
	deps, ok := got["deps"].(map[string]any)
	if !ok {
		t.Fatalf("deps section missing or wrong type: %v", got["deps"])
	}
	if _, ok := deps["mysql"]; ok {
		t.Errorf("in-memory mode should omit deps.mysql, got %v", deps["mysql"])
	}
	cat, ok := deps["catalog"].(map[string]any)
	if !ok {
		t.Fatalf("deps.catalog missing: %v", deps)
	}
	if cat["symbols"].(float64) != 1 {
		t.Errorf("catalog.symbols = %v, want 1", cat["symbols"])
	}
}

func TestReadinessIsServiceUnavailableWithEmptyCatalog(t *testing.T) {
	cfg := config.Config{Symbols: []domain.SymbolSub{}, Platforms: []string{}}
	store := collector.NewStore(cfg)
	srv := NewServer(cfg, store)

	req := httptest.NewRequest(http.MethodGet, "/api/readiness", nil)
	w := httptest.NewRecorder()
	srv.readiness(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty catalog must surface 503 readiness, got %d", w.Code)
	}
	var got map[string]any
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got["ready"] != false {
		t.Errorf("ready = %v, want false", got["ready"])
	}
}

func TestReadinessIsOKWithLoadedCatalogAndInMemoryStore(t *testing.T) {
	cfg := config.Config{
		Symbols:   []domain.SymbolSub{{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC"}},
		Platforms: []string{"edgeX"},
	}
	store := collector.NewStore(cfg)
	srv := NewServer(cfg, store)

	req := httptest.NewRequest(http.MethodGet, "/api/readiness", nil)
	w := httptest.NewRecorder()
	srv.readiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("loaded catalog + in-memory store must be ready, got %d", w.Code)
	}
}
