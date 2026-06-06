package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

func newReloadStore(t *testing.T) *Store {
	t.Helper()
	cfg := config.Default()
	cfg.Platforms = []string{"edgeX"}
	cfg.Symbols = []domain.SymbolSub{
		{
			DisplaySymbol: "BTC-USDT (perp)",
			Canonical:     "BTC",
			Platform:      "edgeX",
			APISymbol:     "BTC-USDT",
			ContractID:    "10000001",
			APILevelCap:   400,
			FrontendURL:   "https://pro.edgex.exchange/trade/BTC-USDT",
			URLVerified:   false,
		},
	}
	return NewStore(cfg)
}

func TestReloadCatalogFrontendMetaUpdatesOnlyDisplayFields(t *testing.T) {
	store := newReloadStore(t)

	cat := config.Catalog{
		Platforms: map[string]map[string]config.CatalogSymbol{
			"edgeX": {
				"BTC": {
					// adapter-critical fields here MUST be ignored by
					// the hot-reload path even if they differ; only
					// FrontendURL / URLVerified / CatalogStatus apply.
					APISymbol:     "WILL-NOT-APPLY",
					ContractID:    "9999999",
					APILevelCap:   1,
					FrontendURL:   "https://pro.edgex.exchange/en-US/trade/BTCUSD",
					URLVerified:   true,
					CatalogStatus: "TRADING",
				},
			},
		},
	}
	if got := store.ReloadCatalogFrontendMeta(cat); got != 1 {
		t.Fatalf("ReloadCatalogFrontendMeta changed count = %d, want 1", got)
	}

	mappings := store.SymbolMappings()
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	got := mappings[0]
	if got.FrontendURL != "https://pro.edgex.exchange/en-US/trade/BTCUSD" {
		t.Errorf("FrontendURL not hot-reloaded: %q", got.FrontendURL)
	}
	if !got.URLVerified {
		t.Errorf("URLVerified not hot-reloaded")
	}
	if got.CatalogStatus != "TRADING" {
		t.Errorf("CatalogStatus = %q, want TRADING", got.CatalogStatus)
	}
	if got.APISymbol != "BTC-USDT" || got.ContractID != "10000001" || got.APILevelCap != 400 {
		t.Errorf("adapter-critical fields must not change on hot reload: %+v", got)
	}
}

func TestReloadCatalogFrontendMetaNoopWhenUnchanged(t *testing.T) {
	store := newReloadStore(t)
	cat := config.Catalog{
		Platforms: map[string]map[string]config.CatalogSymbol{
			"edgeX": {
				"BTC": {FrontendURL: "https://pro.edgex.exchange/trade/BTC-USDT"},
			},
		},
	}
	if got := store.ReloadCatalogFrontendMeta(cat); got != 0 {
		t.Fatalf("reload with identical metadata should report 0 changes, got %d", got)
	}
}

func TestReloadCatalogFrontendMetaUnknownEntryLeavesSlice(t *testing.T) {
	store := newReloadStore(t)
	cat := config.Catalog{
		Platforms: map[string]map[string]config.CatalogSymbol{
			"edgeX": {
				"ETH": {FrontendURL: "https://example/eth"},
			},
		},
	}
	if got := store.ReloadCatalogFrontendMeta(cat); got != 0 {
		t.Fatalf("reload with no overlap should report 0 changes, got %d", got)
	}
	if got := store.SymbolMappings()[0].FrontendURL; got != "https://pro.edgex.exchange/trade/BTC-USDT" {
		t.Errorf("untouched symbol FrontendURL = %q", got)
	}
}

func TestWatchCatalogPicksUpMtimeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instrument_catalog.yaml")
	initial := `schema_version: 1
platforms:
  edgeX:
    BTC:
      api_symbol: BTC-USDT
      base_asset: BTC
      quote_asset: USDT
      settle_asset: USDT
      api_level_cap: 400
      contract_id: "10000001"
      source_endpoint: https://pro.edgex.exchange
      frontend_url: https://pro.edgex.exchange/trade/BTC-USDT
      url_verified: false
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	store := newReloadStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		store.WatchCatalog(ctx, path, 20*time.Millisecond)
		close(done)
	}()

	updated := `schema_version: 1
platforms:
  edgeX:
    BTC:
      api_symbol: BTC-USDT
      base_asset: BTC
      quote_asset: USDT
      settle_asset: USDT
      api_level_cap: 400
      contract_id: "10000001"
      source_endpoint: https://pro.edgex.exchange
      frontend_url: https://pro.edgex.exchange/en-US/trade/BTCUSD
      url_verified: true
`
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("bump initial mtime: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("write updated catalog: %v", err)
	}
	future2 := future.Add(2 * time.Second)
	if err := os.Chtimes(path, future2, future2); err != nil {
		t.Fatalf("bump updated mtime: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := store.SymbolMappings()[0].FrontendURL
		if got == "https://pro.edgex.exchange/en-US/trade/BTCUSD" {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("WatchCatalog did not pick up updated frontend_url; current = %q",
		store.SymbolMappings()[0].FrontendURL)
}
