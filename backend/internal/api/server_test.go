package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
	"edgex-ops-intelligence/backend/internal/startup"
)

type fakeStoreReader struct {
	meta             map[string]any
	symbols          []string
	mappings         []domain.SymbolSub
	coverage         map[string]any
	liquidity        map[string]any
	quality          map[string]any
	share            map[string]any
	top30            map[string]any
	top30Divergence  domain.Top30DivergenceSnapshot
	collectionStatus map[string]any
	runtime          config.Runtime
}

func TestCollectionStatusIncludesStartupAndLiveProviderState(t *testing.T) {
	startupState := startup.New("all")
	startupState.MarkAPIListening()
	startupState.MarkLighterStarted(2)
	startupState.MarkLighterProgress(1, 2)

	store := fakeStoreReader{
		mappings:         []domain.SymbolSub{{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)"}},
		collectionStatus: map[string]any{"success": 1},
	}
	server := NewServer(
		config.Config{Symbols: store.mappings, Platforms: []string{"edgeX"}},
		store,
		WithStartupStatus(startupState),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/collection-status", nil)
	w := httptest.NewRecorder()
	server.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["success"] != float64(1) {
		t.Fatalf("success = %#v", got["success"])
	}
	startupBlock, ok := got["startup"].(map[string]any)
	if !ok {
		t.Fatalf("startup block missing: %#v", got)
	}
	if startupBlock["role"] != "all" || startupBlock["api_listening"] != true {
		t.Fatalf("startup block = %#v", startupBlock)
	}
	liveProviders, ok := got["live_providers"].(map[string]any)
	if !ok {
		t.Fatalf("live_providers missing: %#v", got)
	}
	lighterWS, ok := liveProviders["lighter_ws"].(map[string]any)
	if !ok {
		t.Fatalf("lighter_ws missing: %#v", liveProviders)
	}
	if lighterWS["state"] != startup.StatePartial || lighterWS["ready_count"] != float64(1) {
		t.Fatalf("lighter_ws = %#v", lighterWS)
	}
}

func (f fakeStoreReader) MySQLBacked() bool                { return false }
func (f fakeStoreReader) PingDB(ctx context.Context) error { return nil }
func (f fakeStoreReader) SnapshotRowCounts(ctx context.Context) (map[string]int64, error) {
	return nil, nil
}
func (f fakeStoreReader) Symbols() []string                   { return f.symbols }
func (f fakeStoreReader) SymbolMappings() []domain.SymbolSub  { return f.mappings }
func (f fakeStoreReader) OpsIntelligenceMeta() map[string]any { return f.meta }
func (f fakeStoreReader) Coverage() map[string]any            { return f.coverage }
func (f fakeStoreReader) Liquidity(symbol string) map[string]any {
	out := cloneMap(f.liquidity)
	out["requested_symbol"] = symbol
	return out
}
func (f fakeStoreReader) Quality(symbol string) map[string]any {
	out := cloneMap(f.quality)
	out["requested_symbol"] = symbol
	return out
}
func (f fakeStoreReader) Share(window string) map[string]any {
	out := cloneMap(f.share)
	out["requested_window"] = window
	return out
}
func (f fakeStoreReader) Top30(surface, platform string) map[string]any {
	out := cloneMap(f.top30)
	out["requested_surface"] = surface
	out["requested_platform"] = platform
	return out
}
func (f fakeStoreReader) Top30Divergence() domain.Top30DivergenceSnapshot { return f.top30Divergence }
func (f fakeStoreReader) CollectionStatus() map[string]any                { return f.collectionStatus }
func (f fakeStoreReader) RuntimeConfig() config.Runtime                   { return f.runtime }

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestSnapshotHandlersReadThroughStoreReader(t *testing.T) {
	store := fakeStoreReader{
		meta:      map[string]any{"service": "meta"},
		symbols:   []string{"BTC-USDT (perp)"},
		mappings:  []domain.SymbolSub{{Platform: "edgeX", DisplaySymbol: "BTC-USDT (perp)"}},
		coverage:  map[string]any{"rows": []any{}},
		liquidity: map[string]any{"kind": "liquidity"},
		quality:   map[string]any{"kind": "quality"},
		share:     map[string]any{"kind": "share"},
		top30:     map[string]any{"kind": "top30"},
		top30Divergence: domain.Top30DivergenceSnapshot{
			Status:               domain.StatusComplete,
			CEXPlatforms:         []string{"binance", "okx"},
			DEXPlatforms:         []string{"hyperliquid", "edgeX"},
			SignificantRankDelta: 10,
			CEXTop30: []domain.Top30AggregateRow{
				{Rank: 1, Symbol: "BTC", AdjustedVolume24HUSD: 100, RawVolume24HUSD: 100, PlatformCount: 2},
			},
			DEXTop30: []domain.Top30AggregateRow{
				{Rank: 1, Symbol: "BTC", AdjustedVolume24HUSD: 80, RawVolume24HUSD: 80, PlatformCount: 2},
			},
			Divergence: []domain.Top30DivergenceRow{
				{Symbol: "BTC", Category: domain.Top30DivergenceAligned, EdgexListed: true, EdgexListedStatus: domain.StatusComplete},
			},
			KPI: domain.Top30DivergenceKPI{AlignedCount: 1},
		},
		collectionStatus: map[string]any{"success": 1},
		runtime:          config.Runtime{Collection: config.CollectionConfig{PerPlatformConcurrency: 3}},
	}
	server := NewServer(config.Config{Symbols: store.mappings, Platforms: []string{"edgeX"}}, store)

	tests := []struct {
		name string
		path string
		want map[string]any
	}{
		{name: "meta", path: "/api/ops-intelligence/meta", want: map[string]any{"service": "meta"}},
		{name: "symbols", path: "/api/symbols", want: map[string]any{"symbols": []any{"BTC-USDT (perp)"}}},
		{name: "coverage", path: "/api/symbols/coverage", want: map[string]any{"rows": []any{}}},
		{name: "liquidity", path: "/api/snapshot/liquidity?symbol=BTC", want: map[string]any{"kind": "liquidity", "requested_symbol": "BTC"}},
		{name: "quality", path: "/api/snapshot/quality?symbol=ETH", want: map[string]any{"kind": "quality", "requested_symbol": "ETH"}},
		{name: "share", path: "/api/snapshot/share?window=7d", want: map[string]any{"kind": "share", "requested_window": "7d"}},
		{name: "top30", path: "/api/snapshot/top30?surface=perp&platform=binance", want: map[string]any{"kind": "top30", "requested_surface": "perp", "requested_platform": "binance"}},
		{name: "top30_divergence", path: "/api/snapshot/top30/divergence", want: map[string]any{"status": "complete", "significant_rank_delta": float64(10)}},
		{name: "collection", path: "/api/collection-status", want: map[string]any{"success": float64(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			server.Routes().ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			var got map[string]any
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for key, want := range tt.want {
				if !reflect.DeepEqual(got[key], want) {
					t.Fatalf("%s = %#v, want %#v (full=%#v)", key, got[key], want, got)
				}
			}
		})
	}
}
