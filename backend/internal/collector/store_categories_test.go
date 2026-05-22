package collector

import (
	"testing"

	"edgex-dashboard/backend/internal/config"
	"edgex-dashboard/backend/internal/domain"
)

// TestResolveSymbolMapsCanonicalToDisplaySymbol pins the new canonical-keyed
// URL convention: /api/snapshot/liquidity?symbol=BTC must resolve to the
// underlying store key "BTC-USDT (perp)" so the existing storage layer
// (keyed by display_symbol) is unaffected.
func TestResolveSymbolMapsCanonicalToDisplaySymbol(t *testing.T) {
	store := NewStore(config.Default())
	if got := store.ResolveSymbol("BTC"); got != "BTC-USDT (perp)" {
		t.Errorf("ResolveSymbol(BTC) = %q, want BTC-USDT (perp)", got)
	}
	if got := store.ResolveSymbol("btc"); got != "BTC-USDT (perp)" {
		t.Errorf("ResolveSymbol(btc) = %q, want BTC-USDT (perp) (case-insensitive)", got)
	}
}

// TestResolveSymbolPassesThroughLegacyDisplaySymbol pins the alias-replay
// behaviour: legacy URLs like ?symbol=BTC-USDT (perp) must continue to work
// during the C3 frontend migration window.
func TestResolveSymbolPassesThroughLegacyDisplaySymbol(t *testing.T) {
	store := NewStore(config.Default())
	if got := store.ResolveSymbol("BTC-USDT (perp)"); got != "BTC-USDT (perp)" {
		t.Errorf("ResolveSymbol(BTC-USDT (perp)) = %q, want unchanged", got)
	}
}

// TestResolveSymbolPassesThroughUnknown ensures the resolver never invents
// a mapping for input that is neither a canonical nor a known display
// symbol; the API should treat unknown values as plain display_symbols and
// fall back to the missingPlatform path.
func TestResolveSymbolPassesThroughUnknown(t *testing.T) {
	store := NewStore(config.Default())
	if got := store.ResolveSymbol("MADEUP"); got != "MADEUP" {
		t.Errorf("ResolveSymbol(MADEUP) = %q, want unchanged", got)
	}
}

// TestDashboardMetaIncludesCategories pins the new shape /api/dashboard/meta
// must expose so the C3 frontend can drive the dropdown filter without
// hard-coding categories on the client side.
func TestDashboardMetaIncludesCategories(t *testing.T) {
	store := NewStore(config.Default())
	meta := store.DashboardMeta()
	raw, ok := meta["categories"]
	if !ok {
		t.Fatalf("categories field missing from DashboardMeta")
	}
	categories, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("categories type = %T, want []map[string]any", raw)
	}
	if len(categories) == 0 {
		t.Fatalf("categories empty; expected at least crypto bucket")
	}
	first := categories[0]
	if first["key"] != domain.AssetCategoryCrypto {
		t.Errorf("first category key = %v, want crypto", first["key"])
	}
	if first["label"] == "" {
		t.Errorf("first category missing human label")
	}
	symbols, ok := first["symbols"].([]map[string]any)
	if !ok || len(symbols) == 0 {
		t.Fatalf("crypto category must have at least one symbol entry, got %T len %d", first["symbols"], len(symbols))
	}
	canonicals := map[string]bool{}
	for _, sym := range symbols {
		canonicals[asString(sym["canonical"])] = true
		if asString(sym["display_name"]) == "" {
			t.Errorf("symbol %v missing display_name", sym)
		}
		if asString(sym["display_symbol"]) == "" {
			t.Errorf("symbol %v missing display_symbol", sym)
		}
		if _, ok := sym["supported_platform_count"]; !ok {
			t.Errorf("symbol %v missing supported_platform_count", sym)
		}
	}
	for _, want := range []string{"BTC", "ETH", "SOL"} {
		if !canonicals[want] {
			t.Errorf("crypto category missing canonical %q (got %v)", want, canonicals)
		}
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
