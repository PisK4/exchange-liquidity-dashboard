package adapter

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"

	"edgex-dashboard/backend/internal/domain"
)

// TestBinanceDailyHistoryReturnsQuoteVolume verifies index 7 (quote volume)
// is consumed and that the UTC-day key matches the open_time of each row.
func TestBinanceDailyHistoryReturnsQuoteVolume(t *testing.T) {
	var gotURL string
	body := `[
        [1700006400000,"30000","30100","29900","30050","100",1700092799999,"3005000",1,"50","1500000","0"],
        [1700092800000,"30050","30200","30000","30180","120",1700179199999,"3621600",1,"60","1810800","0"]
    ]`
	a := RESTAdapter{Platform: "binance", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}

	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTCUSDT"}, 2)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !strings.Contains(gotURL, "interval=1d") || !strings.Contains(gotURL, "limit=2") {
		t.Fatalf("expected interval=1d & limit=2 in URL, got %s", gotURL)
	}
	if math.Abs(rows[0].Volume24HUSD-3005000) > 1 {
		t.Fatalf("expected quote vol 3,005,000, got %v", rows[0].Volume24HUSD)
	}
	if rows[0].DataSource != domain.DataSourceNativeBackfill {
		t.Fatalf("expected native_backfill data source, got %s", rows[0].DataSource)
	}
	if rows[0].DisplaySymbol != "BTC-USDT (perp)" {
		t.Fatalf("expected display symbol echoed, got %s", rows[0].DisplaySymbol)
	}
}

// TestBybitDailyHistoryReadsTurnover keeps the index 6 contract in place.
// Bybit returns strings inside the list, so this also exercises the
// strconv.Parse path.
func TestBybitDailyHistoryReadsTurnover(t *testing.T) {
	body := `{"result":{"list":[
        ["1700006400000","30000","30100","29900","30050","10","301000"],
        ["1700092800000","30050","30200","30000","30180","12","362160"]
    ]}}`
	a := RESTAdapter{Platform: "bybit", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}

	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTCUSDT"}, 2)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if math.Abs(rows[0].Volume24HUSD-301000) > 1 {
		t.Fatalf("expected turnover 301k, got %v", rows[0].Volume24HUSD)
	}
}

// TestOKXDailyHistoryReadsVolCcyQuote uses index 7 (volCcyQuote) and skips
// rows whose quote volume is missing.
func TestOKXDailyHistoryReadsVolCcyQuote(t *testing.T) {
	body := `{"data":[
        ["1700006400000","30000","30100","29900","30050","20","600000","601200","1"],
        ["1700093400000","30050","30200","30000","30180","0","0","0","1"]
    ]}`
	a := RESTAdapter{Platform: "okx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}

	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC-USDT-SWAP"}, 2)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 valid row (zero-volume row skipped), got %d", len(rows))
	}
	if math.Abs(rows[0].Volume24HUSD-601200) > 1 {
		t.Fatalf("expected volCcyQuote ≈ 601,200, got %v", rows[0].Volume24HUSD)
	}
}

// TestGateDailyHistoryUsesQuantoMultiplier checks the v×c×multiplier formula
// and surfaces the catalog-required error when QuantoMultiplier is zero.
func TestGateDailyHistoryUsesQuantoMultiplier(t *testing.T) {
	body := `[{"t":1700006400,"v":12000,"c":"30000"},{"t":1700092800,"v":15000,"c":"30200"}]`
	a := RESTAdapter{Platform: "gate", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}

	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC_USDT", QuantoMultiplier: 0.0001, Canonical: "BTC"}, 2)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	expected := 12000 * 0.0001 * 30000.0 // 36,000 USDT
	if math.Abs(rows[0].Volume24HUSD-expected) > 0.01 {
		t.Fatalf("expected USDT vol %v, got %v", expected, rows[0].Volume24HUSD)
	}

	if _, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC_USDT", Canonical: "BTC"}, 2); err == nil {
		t.Fatal("expected error when QuantoMultiplier is missing")
	}
}

// TestLighterDailyHistoryReadsQuoteVolume confirms the candles `V` field is
// the USD quote volume and the URL carries 1d resolution.
func TestLighterDailyHistoryReadsQuoteVolume(t *testing.T) {
	var gotURL string
	body := `{"code":200,"r":"1d","c":[
        {"t":1700006400000,"o":30000,"h":30100,"l":29900,"c":30050,"v":15,"V":451500,"i":"x"},
        {"t":1700092800000,"o":30050,"h":30200,"l":30000,"c":30180,"v":20,"V":604000,"i":"y"}
    ]}`
	a := RESTAdapter{Platform: "lighter", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}

	marketID := 0
	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", MarketID: &marketID}, 2)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if !strings.Contains(gotURL, "resolution=1d") || !strings.Contains(gotURL, "market_id=0") {
		t.Fatalf("expected resolution=1d & market_id=0 in URL, got %s", gotURL)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if math.Abs(rows[0].Volume24HUSD-451500) > 1 {
		t.Fatalf("expected quote vol 451,500, got %v", rows[0].Volume24HUSD)
	}
}

// TestEdgeXDailyHistoryParsesNestedDataList pins the production shape
// returned by pro.edgex.exchange/getKline: `{ code, data: { dataList:
// [{ klineTime, value, ... }], nextPageOffsetData } }`. value is the USDT
// quote volume; klineTime is a stringified ms timestamp.
func TestEdgeXDailyHistoryParsesNestedDataList(t *testing.T) {
	body := `{"code":"SUCCESS","data":{"dataList":[
        {"klineTime":"1779148800000","value":"766728797.7716","size":"9990","close":"76800"},
        {"klineTime":"1779062400000","value":"844016006.7708","size":"10984","close":"76962"}
    ],"nextPageOffsetData":""}}`
	a := RESTAdapter{Platform: "edgeX", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}
	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", Canonical: "BTC", ContractID: "10000001"}, 2)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if math.Abs(rows[0].Volume24HUSD-766728797.7716) > 0.01 {
		t.Fatalf("expected USDT quote vol ≈ 766,728,797, got %v", rows[0].Volume24HUSD)
	}
	if rows[0].DataSource != domain.DataSourceNativeBackfill {
		t.Fatalf("expected native_backfill source, got %s", rows[0].DataSource)
	}
}

// TestLighterDailyHistoryAcceptsNumericInstrumentID guards the production
// shape where `i` (the per-candle instrument id) is a numeric value rather
// than the string the docs implied; the previous string-typed struct would
// fail to unmarshal even though `i` is unused.
func TestLighterDailyHistoryAcceptsNumericInstrumentID(t *testing.T) {
	body := `{"code":200,"r":"1d","c":[
        {"t":1778112000000,"o":2350.03,"h":2351.8,"l":2278.21,"c":2290.04,"v":211370,"V":489238890.20,"i":19466304043}
    ]}`
	a := RESTAdapter{Platform: "lighter", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}
	marketID := 0
	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "ETH-USDT (perp)", MarketID: &marketID}, 1)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 1 || math.Abs(rows[0].Volume24HUSD-489238890.20) > 0.01 {
		t.Fatalf("expected one row with quote vol ≈ 489,238,890, got %+v", rows)
	}
}

// TestUnsupportedPlatformReturnsError keeps the orchestrator honest: an
// adapter pinned to an unrecognised platform must not silently emit nil
// rows.
func TestUnsupportedPlatformReturnsError(t *testing.T) {
	a := RESTAdapter{Platform: "unknown"}
	if _, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{}, 7); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

// TestBingXDailyHistoryHappyPath asserts the standard array shape still
// decodes correctly under the new RawMessage-first path.
func TestBingXDailyHistoryHappyPath(t *testing.T) {
	body := `{"code":0,"msg":"","data":[{"open":"30000","close":"30500","volume":"100","time":1700006400000}]}`
	a := RESTAdapter{Platform: "bingx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}
	rows, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC-USDT"}, 1)
	if err != nil {
		t.Fatalf("FetchDailyVolumeHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if math.Abs(rows[0].Volume24HUSD-3050000) > 1 {
		t.Fatalf("expected vol*close = 100*30500 = 3,050,000, got %v", rows[0].Volume24HUSD)
	}
}

// TestBingXDailyHistoryNotExistReturnsSentinel pins the BingX commodity /
// index regression: code 109425 with `data: {}` must surface as
// adapter.ErrInstrumentNotFound rather than a JSON unmarshal error so the
// Top30 backfiller can skip the symbol silently.
func TestBingXDailyHistoryNotExistReturnsSentinel(t *testing.T) {
	body := `{"code":109425,"msg":"GOLD-USDT not exist","data":{}}`
	a := RESTAdapter{Platform: "bingx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, MaxAttempts: 1}
	_, err := a.FetchDailyVolumeHistory(context.Background(), domain.SymbolSub{DisplaySymbol: "GOLD-USDT (perp)", APISymbol: "GOLD-USDT"}, 14)
	if err == nil || !errors.Is(err, ErrInstrumentNotFound) {
		t.Fatalf("expected ErrInstrumentNotFound, got %v", err)
	}
}
