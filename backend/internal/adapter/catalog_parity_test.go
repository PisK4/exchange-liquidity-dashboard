package adapter

import (
	"path/filepath"
	"testing"

	"edgex-dashboard/backend/internal/config"
)

// TestCatalogCoversAllRuntimePairs is the post-Step-6 completeness gate. The
// legacy hardcoded helpers (edgeXContractID switch, lighterMarketID switch,
// apiSymbol formatting) have been deleted; runtime depends entirely on
// instrument_catalog.yaml. This test asserts the committed catalog has an
// entry for every (platform, canonical) the runtime subscribes to AND that
// each entry has the per-platform fields necessary for the adapter to make a
// successful API call.
func TestCatalogCoversAllRuntimePairs(t *testing.T) {
	path := filepath.Join("..", "..", "..", "config", "instrument_catalog.yaml")
	cat, err := config.LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	platforms := []string{"edgeX", "binance", "okx", "bybit", "bitget", "bingx", "mexc", "gate", "hyperliquid", "lighter"}
	canonicals := []string{"BTC", "ETH", "SOL"}
	for _, p := range platforms {
		entries, ok := cat.Platforms[p]
		if !ok {
			t.Errorf("catalog missing platform %q", p)
			continue
		}
		for _, c := range canonicals {
			entry, ok := entries[c]
			if !ok {
				t.Errorf("catalog missing %s/%s", p, c)
				continue
			}
			if entry.APISymbol == "" {
				t.Errorf("%s/%s api_symbol empty", p, c)
			}
			if entry.BaseAsset == "" || entry.QuoteAsset == "" || entry.SettleAsset == "" {
				t.Errorf("%s/%s base/quote/settle missing", p, c)
			}
			if entry.FrontendURL == "" {
				t.Errorf("%s/%s frontend_url empty", p, c)
			}
			if entry.SourceEndpoint == "" {
				t.Errorf("%s/%s source_endpoint empty", p, c)
			}
			switch p {
			case "edgeX":
				if entry.ContractID == "" {
					t.Errorf("edgeX/%s contract_id empty", c)
				}
			case "lighter":
				if entry.MarketID == nil {
					t.Errorf("lighter/%s market_id nil", c)
				}
			case "mexc":
				if entry.ContractSize <= 0 {
					t.Errorf("mexc/%s contract_size missing (%v)", c, entry.ContractSize)
				}
			case "okx":
				if entry.ContractSize <= 0 {
					t.Errorf("okx/%s contract_size missing (%v)", c, entry.ContractSize)
				}
			case "gate":
				if entry.QuantoMultiplier <= 0 {
					t.Errorf("gate/%s quanto_multiplier missing (%v)", c, entry.QuantoMultiplier)
				}
			}
		}
	}
}
