package instrument

import (
	"encoding/json"
	"strings"
	"testing"
)

// edgeXSamplePayload is a minimal but realistic /api/v1/public/meta/getMetaData
// envelope: one perp v1 contract (BTCUSD) with enableTrade=true plus a
// disabled contract (XYZUSD with enableDisplay=false) to exercise the
// status branch.
const edgeXSamplePayload = `{
  "code": "SUCCESS",
  "data": {
    "coinList": [
      {"coinId": "1000", "coinName": "BTC"},
      {"coinId": "1001", "coinName": "ETH"},
      {"coinId": "9999", "coinName": "XYZ"}
    ],
    "contractList": [
      {"contractId": "10000001", "baseCoinId": "1000", "contractName": "BTCUSD", "enableTrade": true, "enableDisplay": true, "tickSize": "0.1", "stepSize": "0.001"},
      {"contractId": "10000002", "baseCoinId": "1001", "contractName": "ETHUSD", "enableTrade": true, "enableDisplay": true, "tickSize": "0.01", "stepSize": "0.001"},
      {"contractId": "10009999", "baseCoinId": "9999", "contractName": "XYZUSD", "enableTrade": false, "enableDisplay": true}
    ]
  }
}`

func TestNormalizeEdgeXContractActivePerpV1(t *testing.T) {
	contract := []byte(`{"contractId":"10000001","baseCoinId":"1000","contractName":"BTCUSD","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001"}`)
	got, err := NormalizeEdgeXContract(contract, "perp_v1", "BTC")
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.Platform != "edgeX" || got.MarketType != "perp_v1" {
		t.Fatalf("platform/market_type = %q/%q", got.Platform, got.MarketType)
	}
	if got.CanonicalSymbol != "BTC" || got.BaseAsset != "BTC" {
		t.Fatalf("canonical/base = %q/%q", got.CanonicalSymbol, got.BaseAsset)
	}
	if got.APISymbol != "BTCUSD" {
		t.Fatalf("api_symbol = %q, want BTCUSD", got.APISymbol)
	}
	if got.MarketSurface != "perp" || got.InstrumentKind != "canonical" {
		t.Fatalf("market_surface/instrument_kind = %q/%q", got.MarketSurface, got.InstrumentKind)
	}
	if got.StatusNormalized != "active" {
		t.Fatalf("status_normalized = %q, want active", got.StatusNormalized)
	}
	// Spec F1: contract_id, tick_size, step_size must be preserved into raw_json
	// (CatalogResolver DB-first reads them).
	var checkRaw map[string]any
	if err := json.Unmarshal(got.RawJSON, &checkRaw); err != nil {
		t.Fatalf("raw_json must parse: %v", err)
	}
	if got, ok := checkRaw["contractId"].(string); !ok || got != "10000001" {
		t.Fatalf("raw_json.contractId = %v; want 10000001", checkRaw["contractId"])
	}
	if got.StableHash == "" {
		t.Fatalf("raw_json_hash must be populated")
	}
}

func TestNormalizeEdgeXContractSpot(t *testing.T) {
	contract := []byte(`{"contractId":"20000001","baseCoinId":"1000","contractName":"BTCUSDT","enableTrade":true,"enableDisplay":true}`)
	got, err := NormalizeEdgeXContract(contract, "spot", "BTC")
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.MarketType != "spot" || got.MarketSurface != "spot" {
		t.Fatalf("market_type/market_surface = %q/%q, want spot/spot", got.MarketType, got.MarketSurface)
	}
}

func TestNormalizeEdgeXContractDelistedByEnableTrade(t *testing.T) {
	contract := []byte(`{"contractId":"10009999","baseCoinId":"9999","contractName":"XYZUSD","enableTrade":false,"enableDisplay":true}`)
	got, err := NormalizeEdgeXContract(contract, "perp_v1", "XYZ")
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "delisted" || !got.DelistFlag {
		t.Fatalf("enable_trade=false must yield delisted; got %+v", got)
	}
}

func TestNormalizeEdgeXContractUnknownByEnableDisplay(t *testing.T) {
	contract := []byte(`{"contractId":"10009999","baseCoinId":"9999","contractName":"XYZUSD","enableTrade":true,"enableDisplay":false}`)
	got, err := NormalizeEdgeXContract(contract, "perp_v1", "XYZ")
	if err != nil {
		t.Fatalf("Normalize err = %v", err)
	}
	if got.StatusNormalized != "unknown" || got.DelistFlag {
		t.Fatalf("enable_display=false must yield unknown without DelistFlag; got %+v", got)
	}
	if got.StatusRaw != "enable_display_false" {
		t.Fatalf("StatusRaw = %q, want enable_display_false", got.StatusRaw)
	}
}

func TestNormalizeEdgeXContractMissingContractName(t *testing.T) {
	contract := []byte(`{"contractId":"10000001","baseCoinId":"1000","enableTrade":true,"enableDisplay":true}`)
	_, err := NormalizeEdgeXContract(contract, "perp_v1", "BTC")
	if err == nil {
		t.Fatalf("expected SchemaDriftError on missing contractName")
	}
	if !strings.Contains(err.Error(), "contractName") && !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error should reference missing contractName; got %v", err)
	}
}

func TestNormalizeEdgeXContractRejectsUnknownMarketType(t *testing.T) {
	contract := []byte(`{"contractId":"10000001","baseCoinId":"1000","contractName":"BTCUSD","enableTrade":true,"enableDisplay":true}`)
	_, err := NormalizeEdgeXContract(contract, "futures_quarterly", "BTC")
	if err == nil {
		t.Fatalf("expected error on unknown market_type")
	}
}

func TestParseEdgeXMetaPayloadJoinsBaseFromCoinList(t *testing.T) {
	out, err := ParseEdgeXMetaPayload([]byte(edgeXSamplePayload), "perp_v1")
	if err != nil {
		t.Fatalf("ParseEdgeXMetaPayload err = %v", err)
	}
	// Expect 3 contracts. XYZ is disabled for trading in this fixture, so it
	// still emits a delisted snapshot row.
	if len(out) != 3 {
		t.Fatalf("want 3 normalized contracts, got %d (%+v)", len(out), out)
	}
	gotBases := map[string]string{}
	for _, n := range out {
		gotBases[n.APISymbol] = n.BaseAsset
	}
	if gotBases["BTCUSD"] != "BTC" || gotBases["ETHUSD"] != "ETH" || gotBases["XYZUSD"] != "XYZ" {
		t.Fatalf("base mapping wrong: %+v", gotBases)
	}
}

func TestParseEdgeXMetaPayloadSkipsContractsWithUnknownBaseCoinId(t *testing.T) {
	payload := []byte(`{"code":"SUCCESS","data":{"coinList":[{"coinId":"1000","coinName":"BTC"}],"contractList":[{"contractId":"10000001","baseCoinId":"1000","contractName":"BTCUSD","enableTrade":true,"enableDisplay":true},{"contractId":"99999999","baseCoinId":"99999","contractName":"ORPHANUSD","enableTrade":true,"enableDisplay":true}]}}`)
	out, err := ParseEdgeXMetaPayload(payload, "perp_v1")
	if err != nil {
		t.Fatalf("ParseEdgeXMetaPayload err = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("orphan contracts (unknown baseCoinId) must be skipped; got %d", len(out))
	}
	if out[0].APISymbol != "BTCUSD" {
		t.Fatalf("unexpected survivor: %s", out[0].APISymbol)
	}
}
