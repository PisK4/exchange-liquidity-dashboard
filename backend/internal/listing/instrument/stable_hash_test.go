package instrument

import (
	"encoding/json"
	"strings"
	"testing"
)

// stableHashTwice runs a normalizer twice with two different raw
// payloads and asserts that both produce identical StableHash. The
// caller crafts the two payloads so they differ only in time-varying
// market fields (mark_price, funding_rate, daily_*, last_trade_price,
// open_interest, fee schedule re-quotes, etc.) that MUST NOT flip
// the hash; see 2026-06-01 incident.
func stableHashTwice(t *testing.T, label string, normalize func(json.RawMessage) (NormalizedInstrument, error), rawA, rawB []byte) {
	t.Helper()
	a, errA := normalize(rawA)
	if errA != nil {
		t.Fatalf("%s normalize A err = %v", label, errA)
	}
	b, errB := normalize(rawB)
	if errB != nil {
		t.Fatalf("%s normalize B err = %v", label, errB)
	}
	if a.StableHash == "" || b.StableHash == "" {
		t.Fatalf("%s StableHash must be populated (a=%q b=%q)", label, a.StableHash, b.StableHash)
	}
	if a.StableHash != b.StableHash {
		t.Fatalf("%s StableHash flipped under market jitter:\n  A=%s\n  B=%s\n  rawA=%s\n  rawB=%s",
			label, a.StableHash, b.StableHash, string(rawA), string(rawB))
	}
}

// stableHashFlips runs a normalizer twice with two raw payloads that
// differ in at least one schema-stable field and asserts the hashes
// DIFFER. Used to guard against the opposite regression (suppression
// too aggressive).
func stableHashFlips(t *testing.T, label string, normalize func(json.RawMessage) (NormalizedInstrument, error), rawA, rawB []byte) {
	t.Helper()
	a, errA := normalize(rawA)
	if errA != nil {
		t.Fatalf("%s normalize A err = %v", label, errA)
	}
	b, errB := normalize(rawB)
	if errB != nil {
		t.Fatalf("%s normalize B err = %v", label, errB)
	}
	if a.StableHash == b.StableHash {
		t.Fatalf("%s StableHash failed to flip on spec change:\n  hash=%s\n  rawA=%s\n  rawB=%s",
			label, a.StableHash, string(rawA), string(rawB))
	}
}

// gate ----------------------------------------------------------------

func TestNormalizeGateFuturesHashStableUnderMarketJitter(t *testing.T) {
	a := []byte(`{"name":"BTC_USDT","quanto_multiplier":"0.0001","in_delisting":false,"last_price":"30000","mark_price":"30001","funding_rate":"0.0001","trade_size":12345}`)
	b := []byte(`{"name":"BTC_USDT","quanto_multiplier":"0.0001","in_delisting":false,"last_price":"30500","mark_price":"30502","funding_rate":"0.00012","trade_size":98765}`)
	stableHashTwice(t, "gate.futures", NormalizeGateFuturesContract, a, b)
}

func TestNormalizeGateFuturesHashFlipsOnQuantoMultiplier(t *testing.T) {
	a := []byte(`{"name":"BTC_USDT","quanto_multiplier":"0.0001","in_delisting":false}`)
	b := []byte(`{"name":"BTC_USDT","quanto_multiplier":"0.001","in_delisting":false}`)
	stableHashFlips(t, "gate.futures.quanto", NormalizeGateFuturesContract, a, b)
}

func TestNormalizeGateSpotHashStableUnderMarketJitter(t *testing.T) {
	a := []byte(`{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable","last":"30000"}`)
	b := []byte(`{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable","last":"31000"}`)
	stableHashTwice(t, "gate.spot", NormalizeGateSpotPair, a, b)
}

// lighter -------------------------------------------------------------

func TestNormalizeLighterHashStableUnderMarketJitter(t *testing.T) {
	wrap := func(raw json.RawMessage) (NormalizedInstrument, error) {
		return NormalizeLighterOrderBookDetail(raw, "perp")
	}
	a := []byte(`{"symbol":"BTC","market_id":1,"market_type":"perp","status":"active","daily_base_token_volume":6292,"daily_price_high":4500,"last_trade_price":4501,"open_interest":2393}`)
	b := []byte(`{"symbol":"BTC","market_id":1,"market_type":"perp","status":"active","daily_base_token_volume":7000,"daily_price_high":4600,"last_trade_price":4570,"open_interest":3100}`)
	stableHashTwice(t, "lighter.perp", wrap, a, b)
}

func TestNormalizeLighterHashFlipsOnMarketID(t *testing.T) {
	wrap := func(raw json.RawMessage) (NormalizedInstrument, error) {
		return NormalizeLighterOrderBookDetail(raw, "perp")
	}
	a := []byte(`{"symbol":"BTC","market_id":1,"market_type":"perp","status":"active"}`)
	b := []byte(`{"symbol":"BTC","market_id":2,"market_type":"perp","status":"active"}`)
	stableHashFlips(t, "lighter.market_id", wrap, a, b)
}

func TestNormalizeLighterHashFlipsOnStatus(t *testing.T) {
	wrap := func(raw json.RawMessage) (NormalizedInstrument, error) {
		return NormalizeLighterOrderBookDetail(raw, "perp")
	}
	a := []byte(`{"symbol":"BTC","market_id":1,"market_type":"perp","status":"active"}`)
	b := []byte(`{"symbol":"BTC","market_id":1,"market_type":"perp","status":"halt"}`)
	stableHashFlips(t, "lighter.status", wrap, a, b)
}

// bingx ---------------------------------------------------------------

func TestNormalizeBingXSwapHashStableUnderFeeJitter(t *testing.T) {
	a := []byte(`{"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":1586275200000,"contractId":"100","pricePrecision":1,"quantityPrecision":4,"size":"0.0001","tradeMinQuantity":0.0001,"feeRate":0.0005,"makerFeeRate":0.0002,"takerFeeRate":0.0005,"triggerFeeRate":"0.00050000"}`)
	b := []byte(`{"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":1586275200000,"contractId":"100","pricePrecision":1,"quantityPrecision":4,"size":"0.0001","tradeMinQuantity":0.0001,"feeRate":0.0006,"makerFeeRate":0.0003,"takerFeeRate":0.0006,"triggerFeeRate":"0.00060000"}`)
	stableHashTwice(t, "bingx.swap", NormalizeBingXSwapSymbol, a, b)
}

func TestNormalizeBingXSwapHashFlipsOnContractID(t *testing.T) {
	a := []byte(`{"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":1586275200000,"contractId":"100","pricePrecision":1,"quantityPrecision":4,"size":"0.0001","tradeMinQuantity":0.0001}`)
	b := []byte(`{"symbol":"BTC-USDT","status":1,"asset":"BTC","quoteAsset":"USDT","launchTime":1586275200000,"contractId":"999","pricePrecision":1,"quantityPrecision":4,"size":"0.0001","tradeMinQuantity":0.0001}`)
	stableHashFlips(t, "bingx.contractId", NormalizeBingXSwapSymbol, a, b)
}

func TestNormalizeBingXSpotHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"symbol":"BTC-USDT","status":1,"baseAsset":"BTC","quoteAsset":"USDT","feeRate":0.001}`)
	b := []byte(`{"symbol":"BTC-USDT","status":1,"baseAsset":"BTC","quoteAsset":"USDT","feeRate":0.0008}`)
	stableHashTwice(t, "bingx.spot", NormalizeBingXSpotSymbol, a, b)
}

// edgex ---------------------------------------------------------------

func TestNormalizeEdgeXContractHashStableUnderJitter(t *testing.T) {
	wrap := func(raw json.RawMessage) (NormalizedInstrument, error) {
		return NormalizeEdgeXContract(raw, "perp_v1", "BTC")
	}
	a := []byte(`{"contractId":"10000001","baseCoinId":"1","contractName":"BTC-USDT","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001","quoteCoinId":"100","settleCoinId":"100","irrelevant_ui_icon":"a"}`)
	b := []byte(`{"contractId":"10000001","baseCoinId":"1","contractName":"BTC-USDT","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001","quoteCoinId":"100","settleCoinId":"100","irrelevant_ui_icon":"b"}`)
	stableHashTwice(t, "edgex.contract", wrap, a, b)
}

func TestNormalizeEdgeXContractHashFlipsOnContractID(t *testing.T) {
	wrap := func(raw json.RawMessage) (NormalizedInstrument, error) {
		return NormalizeEdgeXContract(raw, "perp_v1", "BTC")
	}
	a := []byte(`{"contractId":"10000001","baseCoinId":"1","contractName":"BTC-USDT","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001","quoteCoinId":"100","settleCoinId":"100"}`)
	b := []byte(`{"contractId":"10000002","baseCoinId":"1","contractName":"BTC-USDT","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001","quoteCoinId":"100","settleCoinId":"100"}`)
	stableHashFlips(t, "edgex.contractId", wrap, a, b)
}

func TestNormalizeEdgeXContractHashFlipsOnTickSize(t *testing.T) {
	wrap := func(raw json.RawMessage) (NormalizedInstrument, error) {
		return NormalizeEdgeXContract(raw, "perp_v1", "BTC")
	}
	a := []byte(`{"contractId":"10000001","baseCoinId":"1","contractName":"BTC-USDT","enableTrade":true,"enableDisplay":true,"tickSize":"0.1","stepSize":"0.001"}`)
	b := []byte(`{"contractId":"10000001","baseCoinId":"1","contractName":"BTC-USDT","enableTrade":true,"enableDisplay":true,"tickSize":"0.01","stepSize":"0.001"}`)
	stableHashFlips(t, "edgex.tickSize", wrap, a, b)
}

// okx -----------------------------------------------------------------

func TestNormalizeOKXSwapHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"instId":"BTC-USDT-SWAP","state":"live","baseCcy":"BTC","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000","unhandled":"x"}`)
	b := []byte(`{"instId":"BTC-USDT-SWAP","state":"live","baseCcy":"BTC","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000","unhandled":"y"}`)
	stableHashTwice(t, "okx.swap", NormalizeOKXSwap, a, b)
}

func TestNormalizeOKXSwapHashFlipsOnStatus(t *testing.T) {
	a := []byte(`{"instId":"BTC-USDT-SWAP","state":"live","baseCcy":"BTC","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000"}`)
	b := []byte(`{"instId":"BTC-USDT-SWAP","state":"suspend","baseCcy":"BTC","quoteCcy":"USDT","settleCcy":"USDT","ctType":"linear","listTime":"1893456000000"}`)
	stableHashFlips(t, "okx.swap.state", NormalizeOKXSwap, a, b)
}

// bybit ---------------------------------------------------------------

func TestNormalizeBybitLinearHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"symbol":"BTCUSDT","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000","whatever":"a"}`)
	b := []byte(`{"symbol":"BTCUSDT","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000","whatever":"b"}`)
	stableHashTwice(t, "bybit.linear", NormalizeBybitLinear, a, b)
}

func TestNormalizeBybitLinearHashFlipsOnStatus(t *testing.T) {
	a := []byte(`{"symbol":"BTCUSDT","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"}`)
	b := []byte(`{"symbol":"BTCUSDT","status":"Settling","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual","launchTime":"1893456000000"}`)
	stableHashFlips(t, "bybit.linear.status", NormalizeBybitLinear, a, b)
}

// binance -------------------------------------------------------------

func TestNormalizeBinanceUSDMHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000,"filters":[{"filterType":"PRICE_FILTER","tickSize":"0.10"}]}`)
	b := []byte(`{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000,"filters":[{"filterType":"PRICE_FILTER","tickSize":"0.10"}],"maintMarginPercent":"2.5"}`)
	stableHashTwice(t, "binance.usdm", NormalizeBinanceUSDM, a, b)
}

func TestNormalizeBinanceUSDMHashFlipsOnStatus(t *testing.T) {
	a := []byte(`{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000}`)
	b := []byte(`{"symbol":"BTCUSDT","status":"PAUSED","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL","onboardDate":1893456000000}`)
	stableHashFlips(t, "binance.usdm.status", NormalizeBinanceUSDM, a, b)
}

// bitget --------------------------------------------------------------

func TestNormalizeBitgetUSDTFuturesHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1700000000000","isRwa":false,"whatever":"a"}`)
	b := []byte(`{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1700000000000","isRwa":false,"whatever":"b"}`)
	stableHashTwice(t, "bitget.usdt_futures", NormalizeBitgetUSDTFutures, a, b)
}

func TestNormalizeBitgetUSDTFuturesHashFlipsOnStatus(t *testing.T) {
	a := []byte(`{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","symbolStatus":"normal","openTime":"1700000000000","isRwa":false}`)
	b := []byte(`{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","symbolStatus":"halt","openTime":"1700000000000","isRwa":false}`)
	stableHashFlips(t, "bitget.usdt_futures.status", NormalizeBitgetUSDTFutures, a, b)
}

// mexc ----------------------------------------------------------------

func TestNormalizeMEXCContractHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","state":0,"openingTime":1700000000000,"contractSize":0.0001,"whatever":"a"}`)
	b := []byte(`{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","state":0,"openingTime":1700000000000,"contractSize":0.0001,"whatever":"b"}`)
	stableHashTwice(t, "mexc.contract", NormalizeMEXCContract, a, b)
}

func TestNormalizeMEXCContractHashFlipsOnState(t *testing.T) {
	a := []byte(`{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","state":0,"openingTime":1700000000000,"contractSize":0.0001}`)
	b := []byte(`{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","state":2,"openingTime":1700000000000,"contractSize":0.0001}`)
	stableHashFlips(t, "mexc.contract.state", NormalizeMEXCContract, a, b)
}

// hyperliquid ---------------------------------------------------------

func TestNormalizeHyperliquidPerpHashStableUnderJitter(t *testing.T) {
	a := []byte(`{"name":"BTC","maxLeverage":50,"isDelisted":false}`)
	b := []byte(`{"name":"BTC","maxLeverage":50,"isDelisted":false,"szDecimals":3}`)
	stableHashTwice(t, "hyperliquid.perp", NormalizeHyperliquidPerp, a, b)
}

func TestNormalizeHyperliquidPerpHashFlipsOnMaxLeverage(t *testing.T) {
	a := []byte(`{"name":"BTC","maxLeverage":50,"isDelisted":false}`)
	b := []byte(`{"name":"BTC","maxLeverage":25,"isDelisted":false}`)
	stableHashFlips(t, "hyperliquid.perp.maxLeverage", NormalizeHyperliquidPerp, a, b)
}

func TestNormalizeHyperliquidPerpHashFlipsOnDelisted(t *testing.T) {
	a := []byte(`{"name":"BTC","maxLeverage":50,"isDelisted":false}`)
	b := []byte(`{"name":"BTC","maxLeverage":50,"isDelisted":true}`)
	stableHashFlips(t, "hyperliquid.perp.isDelisted", NormalizeHyperliquidPerp, a, b)
}

// Smoke test: ensure the stable hash is the hex of sha256 (64 chars).
func TestStableHashShape(t *testing.T) {
	got, err := NormalizeBinanceUSDM([]byte(`{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","contractType":"PERPETUAL"}`))
	if err != nil {
		t.Fatalf("normalize err = %v", err)
	}
	if len(got.StableHash) != 64 {
		t.Fatalf("StableHash len = %d, want 64 (sha256 hex)", len(got.StableHash))
	}
	if strings.ToLower(got.StableHash) != got.StableHash {
		t.Fatalf("StableHash must be lowercase hex: %q", got.StableHash)
	}
}
