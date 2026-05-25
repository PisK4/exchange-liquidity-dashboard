package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrettyJSONStableOrder(t *testing.T) {
	in := []byte(`{"b":2,"a":1,"nested":{"y":[3,1,2],"x":true}}`)
	out := prettyJSON(in)
	want := "{\n  \"a\": 1,\n  \"b\": 2,\n  \"nested\": {\n    \"x\": true,\n    \"y\": [\n      3,\n      1,\n      2\n    ]\n  }\n}"
	if string(out) != want {
		t.Fatalf("prettyJSON unstable:\n--- got ---\n%s\n--- want ---\n%s", string(out), want)
	}
}

func TestPrettyJSONPassthroughOnGarbage(t *testing.T) {
	in := []byte("not valid json")
	out := prettyJSON(in)
	if string(out) != "not valid json" {
		t.Fatalf("prettyJSON should fall back to raw bytes, got %q", string(out))
	}
}

func newAdapterPointingAt(t *testing.T, platform string, mux *http.ServeMux) (RESTAdapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(mux)
	a := RESTAdapter{
		Platform:    platform,
		Client:      srv.Client(),
		MaxAttempts: 1,
	}
	return a, srv
}

// rewrites all known production hosts to the test server so the adapter can
// fetch through the existing hardcoded URLs without exposing a baseURL hook.
type rewritingTransport struct {
	upstream  http.RoundTripper
	targetURL string
}

func (rt rewritingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := rt.targetURL
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = strings.TrimPrefix(target, "http://")
	req2.Host = req2.URL.Host
	return rt.upstream.RoundTrip(req2)
}

func TestFetchBinanceInstrumentsParsesThreeMarkets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/exchangeInfo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT"}]}`))
	})
	mux.HandleFunc("/fapi/v1/exchangeInfo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","marginAsset":"USDT","contractType":"PERPETUAL"}]}`))
	})
	mux.HandleFunc("/dapi/v1/exchangeInfo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"symbols":[{"symbol":"BTCUSD_PERP","contractStatus":"TRADING","baseAsset":"BTC","quoteAsset":"USD","marginAsset":"BTC","contractType":"PERPETUAL"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := RESTAdapter{
		Platform:    "binance",
		Client:      &http.Client{Transport: rewritingTransport{upstream: http.DefaultTransport, targetURL: srv.URL}, Timeout: 5 * time.Second},
		MaxAttempts: 1,
	}
	res, err := a.FetchInstruments(context.Background())
	if err != nil {
		t.Fatalf("FetchInstruments error: %v", err)
	}
	if res.Platform != "binance" || len(res.Markets) != 3 {
		t.Fatalf("expected 3 binance markets, got platform=%s markets=%d", res.Platform, len(res.Markets))
	}
	byType := map[string]MarketDump{}
	for _, m := range res.Markets {
		byType[m.MarketType] = m
	}
	if m := byType["usd-m"]; len(m.Instruments) != 1 || m.Instruments[0].APISymbol != "BTCUSDT" || m.Instruments[0].ContractType != "PERPETUAL" {
		t.Fatalf("usd-m parse wrong: %+v", m.Instruments)
	}
	if m := byType["coin-m"]; len(m.Instruments) != 1 || m.Instruments[0].Status != "TRADING" {
		t.Fatalf("coin-m contractStatus fallback failed: %+v", m.Instruments)
	}
	if m := byType["spot"]; len(m.Instruments) != 1 || m.Instruments[0].SettleAsset != "USDT" {
		t.Fatalf("spot settle fallback to quote failed: %+v", m.Instruments)
	}
}

func TestFetchBinanceInstrumentsParsesRawInstrumentFixtures(t *testing.T) {
	fixtures := map[string]string{
		"/api/v3/exchangeInfo":  latestRawInstrumentFixture(t, "binance-spot"),
		"/fapi/v1/exchangeInfo": latestRawInstrumentFixture(t, "binance-usd-m"),
		"/dapi/v1/exchangeInfo": latestRawInstrumentFixture(t, "binance-coin-m"),
	}
	mux := http.NewServeMux()
	for path, file := range fixtures {
		file := file
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, file)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := RESTAdapter{
		Platform:    "binance",
		Client:      &http.Client{Transport: rewritingTransport{upstream: http.DefaultTransport, targetURL: srv.URL}, Timeout: 5 * time.Second},
		MaxAttempts: 1,
	}
	res, err := a.FetchInstruments(context.Background())
	if err != nil {
		t.Fatalf("FetchInstruments with raw fixtures: %v", err)
	}
	if len(res.Markets) != 3 {
		t.Fatalf("expected 3 markets from raw fixtures, got %d", len(res.Markets))
	}
	if !catalogResultHasSymbol(res, "usd-m", "BTCUSDT") {
		t.Fatalf("usd-m raw fixture should parse BTCUSDT, got %+v", res.Markets)
	}
	if !catalogResultHasSymbol(res, "coin-m", "BTCUSD_PERP") {
		t.Fatalf("coin-m raw fixture should parse BTCUSD_PERP, got %+v", res.Markets)
	}
}

func latestRawInstrumentFixture(t *testing.T, marketDir string) string {
	t.Helper()
	pattern := filepath.Join("..", "..", "docs", "raw-instruments", marketDir, "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Skipf("raw instrument fixture missing for %s", marketDir)
	}
	return matches[len(matches)-1]
}

func catalogResultHasSymbol(res CatalogResult, marketType, apiSymbol string) bool {
	for _, market := range res.Markets {
		if market.MarketType != marketType {
			continue
		}
		for _, inst := range market.Instruments {
			if inst.APISymbol == apiSymbol {
				return true
			}
		}
	}
	return false
}

func TestFetchGateInstrumentsExtractsQuantoMultiplier(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/spot/currency_pairs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable"}]`))
	})
	mux.HandleFunc("/api/v4/futures/usdt/contracts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"BTC_USDT","in_delisting":false,"type":"direct","quanto_multiplier":"0.0001"},{"name":"SOL_USDT","in_delisting":false,"type":"direct","quanto_multiplier":"1"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := RESTAdapter{
		Platform:    "gate",
		Client:      &http.Client{Transport: rewritingTransport{upstream: http.DefaultTransport, targetURL: srv.URL}, Timeout: 5 * time.Second},
		MaxAttempts: 1,
	}
	res, err := a.FetchInstruments(context.Background())
	if err != nil {
		t.Fatalf("FetchInstruments error: %v", err)
	}
	if len(res.Markets) != 2 {
		t.Fatalf("expected 2 gate markets, got %d", len(res.Markets))
	}
	for _, m := range res.Markets {
		if m.MarketType != "futures-usdt" {
			continue
		}
		if len(m.Instruments) != 2 {
			t.Fatalf("expected 2 futures contracts, got %d", len(m.Instruments))
		}
		var btc, sol Instrument
		for _, i := range m.Instruments {
			if i.APISymbol == "BTC_USDT" {
				btc = i
			}
			if i.APISymbol == "SOL_USDT" {
				sol = i
			}
		}
		if btc.QuantoMultiplier != 0.0001 || sol.QuantoMultiplier != 1 {
			t.Fatalf("quanto_multiplier wrong: BTC=%v SOL=%v", btc.QuantoMultiplier, sol.QuantoMultiplier)
		}
		if btc.SettleAsset != "USDT" {
			t.Fatalf("expected USDT settle, got %q", btc.SettleAsset)
		}
	}
}

func TestFetchBybitInstrumentsFollowsCursor(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v5/market/instruments-info", func(w http.ResponseWriter, r *http.Request) {
		cat := r.URL.Query().Get("category")
		cursor := r.URL.Query().Get("cursor")
		switch {
		case cat == "linear" && cursor == "":
			_, _ = w.Write([]byte(`{"result":{"list":[{"symbol":"BTCUSDT","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"}],"nextPageCursor":"PAGE2"}}`))
		case cat == "linear" && cursor == "PAGE2":
			_, _ = w.Write([]byte(`{"result":{"list":[{"symbol":"ETHUSDT","status":"Trading","baseCoin":"ETH","quoteCoin":"USDT","settleCoin":"USDT","contractType":"LinearPerpetual"}],"nextPageCursor":""}}`))
		case cat == "spot":
			_, _ = w.Write([]byte(`{"result":{"list":[{"symbol":"BTCUSDT","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT"}],"nextPageCursor":""}}`))
		case cat == "inverse":
			_, _ = w.Write([]byte(`{"result":{"list":[],"nextPageCursor":""}}`))
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := RESTAdapter{
		Platform:    "bybit",
		Client:      &http.Client{Transport: rewritingTransport{upstream: http.DefaultTransport, targetURL: srv.URL}, Timeout: 5 * time.Second},
		MaxAttempts: 1,
	}
	res, err := a.FetchInstruments(context.Background())
	if err != nil {
		t.Fatalf("FetchInstruments error: %v", err)
	}
	var linear MarketDump
	for _, m := range res.Markets {
		if m.MarketType == "linear" {
			linear = m
		}
	}
	if len(linear.Instruments) != 2 {
		t.Fatalf("expected 2 linear instruments from cursor pagination, got %d", len(linear.Instruments))
	}
}

func TestParseEdgeXMetaAcceptsMultipleKeyNames(t *testing.T) {
	raw := []byte(`{"data":{"contractList":[{"contractId":"10000001","contractName":"BTC-USDT","baseCurrency":"BTC","quoteCurrency":"USDT","status":"TRADING"}]}}`)
	insts := parseEdgeXMeta(raw)
	if len(insts) != 1 {
		t.Fatalf("expected 1 instrument, got %d", len(insts))
	}
	if insts[0].APISymbol != "BTC-USDT" || insts[0].ContractID != "10000001" || insts[0].Status != "TRADING" {
		t.Fatalf("unexpected parsed instrument: %+v", insts[0])
	}

	alt := []byte(`{"data":{"symbolList":[{"symbolId":"42","symbolName":"ETH-USDC","baseAsset":"ETH","quoteAsset":"USDC","state":"live"}]}}`)
	insts = parseEdgeXMeta(alt)
	if len(insts) != 1 || insts[0].ContractID != "42" || insts[0].APISymbol != "ETH-USDC" {
		t.Fatalf("symbolList alias not handled: %+v", insts)
	}
}

func TestParseEdgeXMetaJoinsCoinListForBaseAndQuote(t *testing.T) {
	// The live edgeX-perp-v1 payload leaves baseCurrency / quoteCurrency
	// as null and references baseCoinId / quoteCoinId instead. The parser
	// must join the coinList entries so downstream matchInstrument can
	// match canonicals (BTC) against the per-row BaseAsset.
	raw := []byte(`{"data":{
		"coinList":[
			{"coinId":"1000","coinName":"USD"},
			{"coinId":"1001","coinName":"BTC"}
		],
		"contractList":[
			{"contractId":"10000001","contractName":"BTCUSD","baseCoinId":"1001","quoteCoinId":"1000","enableTrade":true,"enableDisplay":true}
		]
	}}`)
	insts := parseEdgeXMeta(raw)
	if len(insts) != 1 {
		t.Fatalf("expected 1 instrument, got %d", len(insts))
	}
	got := insts[0]
	if got.APISymbol != "BTCUSD" {
		t.Errorf("APISymbol = %q, want BTCUSD", got.APISymbol)
	}
	if got.BaseAsset != "BTC" {
		t.Errorf("BaseAsset = %q, want BTC (joined via baseCoinId)", got.BaseAsset)
	}
	if got.QuoteAsset != "USD" {
		t.Errorf("QuoteAsset = %q, want USD (joined via quoteCoinId)", got.QuoteAsset)
	}
	if got.ContractID != "10000001" {
		t.Errorf("ContractID = %q, want 10000001", got.ContractID)
	}
	if got.Status != "TRADING" {
		t.Errorf("Status = %q, want TRADING (derived from enableTrade)", got.Status)
	}
}

func TestFetchLighterInstrumentsSplitsPerpAndSpot(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/orderBookDetails", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"order_book_details":[{"symbol":"BTC","market_id":1,"market_type":"perp","status":"active"},{"symbol":"ETH","market_id":0,"market_type":"perp","status":"active"},{"symbol":"USDC","market_id":3,"market_type":"spot","status":"active"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := RESTAdapter{
		Platform:    "lighter",
		Client:      &http.Client{Transport: rewritingTransport{upstream: http.DefaultTransport, targetURL: srv.URL}, Timeout: 5 * time.Second},
		MaxAttempts: 1,
	}
	res, err := a.FetchInstruments(context.Background())
	if err != nil {
		t.Fatalf("FetchInstruments error: %v", err)
	}
	counts := map[string]int{}
	for _, m := range res.Markets {
		counts[m.MarketType] = len(m.Instruments)
	}
	if counts["perp"] != 2 || counts["spot"] != 1 {
		t.Fatalf("expected perp=2 spot=1, got %v", counts)
	}
}

func TestRawJSONSurvivesRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/orderBookDetails", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"order_book_details":[{"market_id":1,"symbol":"BTC","status":"active","market_type":"perp"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := RESTAdapter{
		Platform:    "lighter",
		Client:      &http.Client{Transport: rewritingTransport{upstream: http.DefaultTransport, targetURL: srv.URL}, Timeout: 5 * time.Second},
		MaxAttempts: 1,
	}
	res, err := a.FetchInstruments(context.Background())
	if err != nil {
		t.Fatalf("FetchInstruments error: %v", err)
	}
	if len(res.Markets) == 0 {
		t.Fatal("no markets returned")
	}
	var parsed map[string]any
	if err := json.Unmarshal(res.Markets[0].RawJSON, &parsed); err != nil {
		t.Fatalf("RawJSON not valid JSON: %v", err)
	}
}
