package instrument

import (
	"encoding/json"
	"testing"
)

func TestNormalizeGateSpotPairActive(t *testing.T) {
	raw := []byte(`{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable"}`)
	got, err := NormalizeGateSpotPair(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.Platform != "gate" || got.MarketType != "spot" {
		t.Fatalf("platform/market_type = %q/%q", got.Platform, got.MarketType)
	}
	if got.CanonicalSymbol != "BTC" || got.BaseAsset != "BTC" || got.QuoteAsset != "USDT" {
		t.Fatalf("canonical/base/quote = %+v", got)
	}
	if got.MarketSurface != "spot" || got.InstrumentKind != "canonical" {
		t.Fatalf("surface/kind = %q/%q", got.MarketSurface, got.InstrumentKind)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status = %q, want active", got.StatusNormalized)
	}
}

func TestNormalizeGateSpotPairUntradeable(t *testing.T) {
	raw := []byte(`{"id":"OLD_USDT","base":"OLD","quote":"USDT","trade_status":"untradable"}`)
	got, err := NormalizeGateSpotPair(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.StatusNormalized != "paused" && got.StatusNormalized != "delisted" {
		t.Fatalf("untradable must NOT be active; got %q", got.StatusNormalized)
	}
}

func TestNormalizeGateFuturesContractActiveQuantoPreserved(t *testing.T) {
	// Spec F5 / risk-table row: quanto_multiplier MUST be preserved into
	// raw_json so CatalogResolver DB-first can read it without re-hitting
	// the raw-instruments dump.
	raw := []byte(`{"name":"BTC_USDT","quanto_multiplier":"0.0001","in_delisting":false}`)
	got, err := NormalizeGateFuturesContract(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.Platform != "gate" || got.MarketType != "usdt_futures" || got.MarketSurface != "perp" {
		t.Fatalf("platform/market_type/surface = %q/%q/%q", got.Platform, got.MarketType, got.MarketSurface)
	}
	if got.CanonicalSymbol != "BTC" || got.BaseAsset != "BTC" || got.QuoteAsset != "USDT" {
		t.Fatalf("canonical/base/quote = %+v", got)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status = %q, want active", got.StatusNormalized)
	}
	var checkRaw map[string]any
	if err := json.Unmarshal(got.RawJSON, &checkRaw); err != nil {
		t.Fatalf("raw_json must parse: %v", err)
	}
	if checkRaw["quanto_multiplier"] != "0.0001" {
		t.Fatalf("quanto_multiplier must survive in raw_json; got %v", checkRaw["quanto_multiplier"])
	}
}

func TestNormalizeGateFuturesContractInDelisting(t *testing.T) {
	raw := []byte(`{"name":"OLD_USDT","quanto_multiplier":"0.001","in_delisting":true}`)
	got, err := NormalizeGateFuturesContract(raw)
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if got.StatusNormalized != "delisted" || !got.DelistFlag {
		t.Fatalf("in_delisting=true must yield delisted; got %+v", got)
	}
}

func TestNormalizeGateFuturesContractMissingNameFails(t *testing.T) {
	raw := []byte(`{"quanto_multiplier":"0.0001"}`)
	_, err := NormalizeGateFuturesContract(raw)
	if err == nil {
		t.Fatalf("expected SchemaDriftError on missing name")
	}
}

func TestNormalizeGateFuturesContractRejectsNonUSDTQuote(t *testing.T) {
	// The fetcher endpoint /api/v4/futures/usdt/contracts is USDT-only
	// already, but defense-in-depth: rows whose name doesn't end with
	// _USDT must not be normalized as USDT-perps.
	raw := []byte(`{"name":"BTC_USD","quanto_multiplier":"0.0001"}`)
	_, err := NormalizeGateFuturesContract(raw)
	if err == nil {
		t.Fatalf("expected error on non-USDT futures contract name")
	}
}
