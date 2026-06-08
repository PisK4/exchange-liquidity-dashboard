package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

type fakeLighterBookProvider struct {
	bids []domain.Level
	asks []domain.Level
	err  error
}

func (p fakeLighterBookProvider) Snapshot(marketID int) ([]domain.Level, []domain.Level, time.Time, error) {
	return p.bids, p.asks, time.Now().UTC(), p.err
}

type fakeEdgeXPerpV2BookProvider struct {
	bids     []domain.Level
	asks     []domain.Level
	err      error
	endpoint string
	calls    int
}

func (p *fakeEdgeXPerpV2BookProvider) Snapshot(contractID string) ([]domain.Level, []domain.Level, time.Time, error) {
	p.calls++
	return p.bids, p.asks, time.Now().UTC(), p.err
}

func (p *fakeEdgeXPerpV2BookProvider) SourceEndpoint() string {
	if p.endpoint == "" {
		return defaultEdgeXPerpV2WSURL
	}
	return p.endpoint
}

func TestFetchOrderBookEmptyAPISymbolReturnsUnsupported(t *testing.T) {
	requestCount := 0
	adapter := RESTAdapter{
		Platform: "binance",
		Client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestCount++
				return nil, errors.New("HTTP should not be called for unsupported (platform, canonical) pairs")
			}),
		},
	}
	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:      "binance",
		Canonical:     "DRAM",
		DisplaySymbol: "DRAM-USDT (perp)",
		// APISymbol intentionally empty: no catalog entry on this platform.
	})
	if err != nil {
		t.Fatalf("FetchOrderBook with empty APISymbol should not return error, got %v", err)
	}
	if book.DepthStatus != domain.StatusUnsupported {
		t.Fatalf("DepthStatus = %q, want unsupported", book.DepthStatus)
	}
	if book.Error == "" || !strings.Contains(book.Error, "no catalog entry") {
		t.Fatalf("Error should mention catalog, got %q", book.Error)
	}
	if requestCount != 0 {
		t.Fatalf("expected no HTTP requests, got %d", requestCount)
	}
}

func TestFetchTickerEmptyAPISymbolReturnsUnsupported(t *testing.T) {
	requestCount := 0
	adapter := RESTAdapter{
		Platform: "binance",
		Client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestCount++
				return nil, errors.New("HTTP should not be called for unsupported (platform, canonical) pairs")
			}),
		},
	}
	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "binance",
		Canonical:     "DRAM",
		DisplaySymbol: "DRAM-USDT (perp)",
	})
	if err != nil {
		t.Fatalf("FetchTicker with empty APISymbol should not return error, got %v", err)
	}
	if vol.Status != domain.StatusUnsupported {
		t.Fatalf("Status = %q, want unsupported", vol.Status)
	}
	if requestCount != 0 {
		t.Fatalf("expected no HTTP requests, got %d", requestCount)
	}
}

func TestFetchEdgeXZeroTickerValueCompletes(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"data":[{"value":"0"}]}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "edgeX",
		Canonical:     "AAPL",
		DisplaySymbol: "AAPL-USDT (perp)",
		APISymbol:     "AAPL-USDT",
		ContractID:    "1001",
	})
	if err != nil {
		t.Fatalf("zero EdgeX ticker value should be a valid no-volume snapshot, got %v", err)
	}
	if vol.Status != domain.StatusComplete {
		t.Fatalf("Status = %q, want complete", vol.Status)
	}
	if vol.Volume24HUSD != 0 {
		t.Fatalf("Volume24HUSD = %f, want 0", vol.Volume24HUSD)
	}
}

func TestFetchEdgeXMissingTickerValueErrors(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"data":[{}]}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "edgeX",
		Canonical:     "AAPL",
		DisplaySymbol: "AAPL-USDT (perp)",
		APISymbol:     "AAPL-USDT",
		ContractID:    "1001",
	})
	if err == nil {
		t.Fatalf("missing EdgeX ticker value should remain an error")
	}
	if vol.Status != domain.StatusError {
		t.Fatalf("Status = %q, want error", vol.Status)
	}
	if !strings.Contains(vol.Error, "empty edgex ticker value") {
		t.Fatalf("Error = %q, want empty edgex ticker value", vol.Error)
	}
}

func TestFetchEdgeXPerpV2UsesV2DepthEndpointAndSurfaceMeta(t *testing.T) {
	var requestedURL string
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedURL = req.URL.String()
			return jsonResponse(`{"data":[{"bids":[{"price":"99","size":"1"}],"asks":[{"price":"101","size":"2"}]}]}`), nil
		})},
		MaxAttempts: 1,
	}

	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:       "edgeX",
		Canonical:      "BTC",
		DisplaySymbol:  "BTC-USDT (perp)",
		APISymbol:      "BTCUSDC",
		MarketSurface:  "perp_v2",
		InstrumentKind: "perp",
		Lineage:        "edgeX-perp-v2",
		ContractID:     "30000001",
		BaseAsset:      "BTC",
		QuoteAsset:     "USDC",
	})
	if err != nil {
		t.Fatalf("FetchOrderBook returned error: %v", err)
	}
	if !strings.Contains(requestedURL, "edgex-prod-v2.edgex.exchange/api/v2/public/quote/getDepth") {
		t.Fatalf("requested URL = %q, want V2 depth endpoint", requestedURL)
	}
	if !strings.Contains(requestedURL, "contractId=30000001") {
		t.Fatalf("requested URL = %q, want V2 contract id", requestedURL)
	}
	if book.SourceID != "edgeX-perp-v2-rest-depth-200" {
		t.Fatalf("SourceID = %q, want V2 rest source id", book.SourceID)
	}
	if book.DepthSource != domain.SourceRestSnapshot {
		t.Fatalf("DepthSource = %q, want rest_snapshot", book.DepthSource)
	}
	if book.DisplayPlatform != "edgeX V2" || book.MarketSurface != "perp_v2" || book.ContractID != "30000001" || book.QuoteAsset != "USDC" {
		t.Fatalf("surface meta not propagated: %+v", book)
	}
}

func TestFetchEdgeXPerpV2UsesWSLocalBookWhenReady(t *testing.T) {
	requestCount := 0
	provider := &fakeEdgeXPerpV2BookProvider{
		bids:     []domain.Level{{Price: 100, Size: 1}},
		asks:     []domain.Level{{Price: 101, Size: 2}},
		endpoint: "wss://example.invalid/edgeX-v2/ws",
	}
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestCount++
			return nil, errors.New("REST depth should not be called when V2 WS book is ready")
		})},
		MaxAttempts: 1,
		EdgeXPerpV2: provider,
	}

	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:       "edgeX",
		Canonical:      "BTC",
		DisplaySymbol:  "BTC-USDT (perp)",
		APISymbol:      "BTCUSDC",
		MarketSurface:  "perp_v2",
		InstrumentKind: "perp",
		Lineage:        "edgeX-perp-v2",
		ContractID:     "30000001",
		BaseAsset:      "BTC",
		QuoteAsset:     "USDC",
	})
	if err != nil {
		t.Fatalf("FetchOrderBook returned error: %v", err)
	}
	if requestCount != 0 {
		t.Fatalf("expected no REST requests, got %d", requestCount)
	}
	if provider.calls != 1 {
		t.Fatalf("provider Snapshot calls = %d, want 1", provider.calls)
	}
	if book.SourceID != "edgeX-perp-v2-ws-depth-200" {
		t.Fatalf("SourceID = %q, want V2 WS source id", book.SourceID)
	}
	if book.DepthSource != domain.SourceWSLocalBook {
		t.Fatalf("DepthSource = %q, want ws_local_book", book.DepthSource)
	}
	if book.SourceEndpoint != "wss://example.invalid/edgeX-v2/ws" {
		t.Fatalf("SourceEndpoint = %q", book.SourceEndpoint)
	}
	if len(book.Bids) != 1 || book.Bids[0].Price != 100 || len(book.Asks) != 1 || book.Asks[0].Price != 101 {
		t.Fatalf("unexpected WS book levels: %+v/%+v", book.Bids, book.Asks)
	}
	if book.DisplayPlatform != "edgeX V2" || book.MarketSurface != "perp_v2" || book.ContractID != "30000001" {
		t.Fatalf("surface meta not propagated: %+v", book)
	}
}

func TestFetchEdgeXPerpV2FallsBackToRESTWhenWSNotReady(t *testing.T) {
	var requestedURL string
	provider := &fakeEdgeXPerpV2BookProvider{err: errors.New("ws book not ready")}
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedURL = req.URL.String()
			return jsonResponse(`{"data":[{"bids":[{"price":"99","size":"1"}],"asks":[{"price":"101","size":"2"}]}]}`), nil
		})},
		MaxAttempts: 1,
		EdgeXPerpV2: provider,
	}

	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:       "edgeX",
		Canonical:      "BTC",
		DisplaySymbol:  "BTC-USDT (perp)",
		APISymbol:      "BTCUSDC",
		MarketSurface:  "perp_v2",
		InstrumentKind: "perp",
		Lineage:        "edgeX-perp-v2",
		ContractID:     "30000001",
		BaseAsset:      "BTC",
		QuoteAsset:     "USDC",
	})
	if err != nil {
		t.Fatalf("FetchOrderBook returned error: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider Snapshot calls = %d, want 1", provider.calls)
	}
	if !strings.Contains(requestedURL, "edgex-prod-v2.edgex.exchange/api/v2/public/quote/getDepth") {
		t.Fatalf("requested URL = %q, want V2 REST fallback endpoint", requestedURL)
	}
	if book.SourceID != "edgeX-perp-v2-rest-depth-200" || book.DepthSource != domain.SourceRestSnapshot {
		t.Fatalf("unexpected fallback source: source_id=%q depth_source=%q", book.SourceID, book.DepthSource)
	}
}

func TestFetchEdgeXPerpV1DoesNotUsePerpV2WSProvider(t *testing.T) {
	var requestedURL string
	provider := &fakeEdgeXPerpV2BookProvider{
		bids: []domain.Level{{Price: 100, Size: 1}},
		asks: []domain.Level{{Price: 101, Size: 2}},
	}
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedURL = req.URL.String()
			return jsonResponse(`{"data":[{"bids":[{"price":"98","size":"1"}],"asks":[{"price":"102","size":"2"}]}]}`), nil
		})},
		MaxAttempts: 1,
		EdgeXPerpV2: provider,
	}

	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:       "edgeX",
		Canonical:      "BTC",
		DisplaySymbol:  "BTC-USDT (perp)",
		APISymbol:      "BTCUSD",
		MarketSurface:  "perp_v1",
		InstrumentKind: "perp",
		Lineage:        "edgeX-perp-v1",
		ContractID:     "10000001",
		BaseAsset:      "BTC",
		QuoteAsset:     "USDT",
	})
	if err != nil {
		t.Fatalf("FetchOrderBook returned error: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("V1 should not use V2 provider, calls=%d", provider.calls)
	}
	if !strings.Contains(requestedURL, "pro.edgex.exchange/api/v1/public/quote/getDepth") {
		t.Fatalf("requested URL = %q, want V1 REST endpoint", requestedURL)
	}
	if book.DepthSource == domain.SourceWSLocalBook {
		t.Fatalf("V1 must not be attributed to V2 WS source: %+v", book)
	}
}

func TestFetchEdgeXPerpV2TickerUsesV2EndpointAndSurfaceMeta(t *testing.T) {
	var requestedURL string
	adapter := RESTAdapter{
		Platform: "edgeX",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestedURL = req.URL.String()
			return jsonResponse(`{"data":[{"value":"12345.67"}]}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:       "edgeX",
		Canonical:      "ETH",
		DisplaySymbol:  "ETH-USDT (perp)",
		APISymbol:      "ETHUSDC",
		MarketSurface:  "perp_v2",
		InstrumentKind: "perp",
		Lineage:        "edgeX-perp-v2",
		ContractID:     "30000002",
		BaseAsset:      "ETH",
		QuoteAsset:     "USDC",
	})
	if err != nil {
		t.Fatalf("FetchTicker returned error: %v", err)
	}
	if !strings.Contains(requestedURL, "edgex-prod-v2.edgex.exchange/api/v2/public/quote/getTicker") {
		t.Fatalf("requested URL = %q, want V2 ticker endpoint", requestedURL)
	}
	if vol.Status != domain.StatusComplete || vol.Volume24HUSD != 12345.67 {
		t.Fatalf("unexpected volume row: %+v", vol)
	}
	if vol.DisplayPlatform != "edgeX V2" || vol.MarketSurface != "perp_v2" || vol.ContractID != "30000002" || vol.QuoteAsset != "USDC" {
		t.Fatalf("surface meta not propagated: %+v", vol)
	}
}

func TestFetchBingXZeroBaseVolumeCompletes(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "bingx",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"data":{"volume":"0","lastPrice":"100"}}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "bingx",
		Canonical:     "COPPER",
		DisplaySymbol: "COPPER-USDT (perp)",
		APISymbol:     "COPPER-USDT",
	})
	if err != nil {
		t.Fatalf("zero BingX base volume should be a valid no-volume snapshot, got %v", err)
	}
	if vol.Status != domain.StatusComplete {
		t.Fatalf("Status = %q, want complete", vol.Status)
	}
	if vol.Volume24HUSD != 0 {
		t.Fatalf("Volume24HUSD = %f, want 0", vol.Volume24HUSD)
	}
}

func TestFetchBingXMissingVolumeErrors(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "bingx",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"data":{}}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "bingx",
		Canonical:     "COPPER",
		DisplaySymbol: "COPPER-USDT (perp)",
		APISymbol:     "COPPER-USDT",
	})
	if err == nil {
		t.Fatalf("missing BingX volume should remain an error")
	}
	if vol.Status != domain.StatusError {
		t.Fatalf("Status = %q, want error", vol.Status)
	}
	if !strings.Contains(vol.Error, "empty bingx quote volume") {
		t.Fatalf("Error = %q, want empty bingx quote volume", vol.Error)
	}
}

func TestFetchBingXPausedTickerReturnsUnsupported(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "bingx",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"code":109415,"msg":"NCCOCOPPER2USD-USDT is pause currently,all validted symbols in api:/openApi/swap/v2/quote/contracts, please verify it","data":{}}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "bingx",
		Canonical:     "COPPER",
		DisplaySymbol: "COPPER-USDT (perp)",
		APISymbol:     "NCCOCOPPER2USD-USDT",
	})
	if err != nil {
		t.Fatalf("paused BingX ticker should be statusized instead of returned as collector error: %v", err)
	}
	if vol.Status != domain.StatusUnsupported {
		t.Fatalf("Status = %q, want unsupported", vol.Status)
	}
	if !strings.Contains(vol.Error, "pause currently") || !strings.Contains(vol.Error, "no live market data") {
		t.Fatalf("Error should preserve paused/no-live-market-data reason, got %q", vol.Error)
	}
}

func TestFetchBingXUnexpectedTickerCodeErrors(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "bingx",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"code":100001,"msg":"unexpected upstream error","data":{}}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "bingx",
		Canonical:     "COPPER",
		DisplaySymbol: "COPPER-USDT (perp)",
		APISymbol:     "NCCOCOPPER2USD-USDT",
	})
	if err == nil {
		t.Fatalf("unexpected BingX ticker code should remain an error")
	}
	if vol.Status != domain.StatusError {
		t.Fatalf("Status = %q, want error", vol.Status)
	}
	if !strings.Contains(vol.Error, "bingx ticker code 100001") {
		t.Fatalf("Error = %q, want ticker code reason", vol.Error)
	}
}

func TestFetchOKXMissingInstrumentTickerReturnsUnsupported(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "okx",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"code":"51001","msg":"Instrument ID, Instrument ID code, or Spread ID doesn't exist.","data":[]}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "okx",
		Canonical:     "SPACEX",
		DisplaySymbol: "SPACEX-USDT (perp)",
		APISymbol:     "SPACEX-USDT-SWAP",
	})
	if err != nil {
		t.Fatalf("missing OKX ticker instrument should be statusized instead of returned as collector error: %v", err)
	}
	if vol.Status != domain.StatusUnsupported {
		t.Fatalf("Status = %q, want unsupported", vol.Status)
	}
	if !strings.Contains(vol.Error, "Instrument ID") || !strings.Contains(vol.Error, "no live market data") {
		t.Fatalf("Error should preserve missing-instrument/no-live-market-data reason, got %q", vol.Error)
	}
}

func TestFetchOKXMissingInstrumentBookReturnsUnsupported(t *testing.T) {
	adapter := RESTAdapter{
		Platform: "okx",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"code":"51001","msg":"Instrument ID does not exist."}`), nil
		})},
		MaxAttempts: 1,
	}

	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:      "okx",
		Canonical:     "SPACEX",
		DisplaySymbol: "SPACEX-USDT (perp)",
		APISymbol:     "SPACEX-USDT-SWAP",
		ContractSize:  1,
	})
	if err != nil {
		t.Fatalf("missing OKX book instrument should be statusized instead of returned as collector error: %v", err)
	}
	if book.DepthStatus != domain.StatusUnsupported {
		t.Fatalf("DepthStatus = %q, want unsupported", book.DepthStatus)
	}
	if !strings.Contains(book.Error, "Instrument ID") || !strings.Contains(book.Error, "no live market data") {
		t.Fatalf("Error should preserve missing-instrument/no-live-market-data reason, got %q", book.Error)
	}
}

func TestFetchLighterEmptyBookReturnsUnsupported(t *testing.T) {
	marketID := 176
	adapter := RESTAdapter{
		Platform: "lighter",
		Lighter:  fakeLighterBookProvider{err: errors.New("lighter market 176 ws book empty")},
	}

	book, err := adapter.FetchOrderBook(context.Background(), domain.SymbolSub{
		Platform:      "lighter",
		Canonical:     "GME",
		DisplaySymbol: "GME-USDT (perp)",
		APISymbol:     "GME",
		MarketID:      &marketID,
	})
	if err != nil {
		t.Fatalf("empty Lighter book should be statusized instead of returned as collector error: %v", err)
	}
	if book.DepthStatus != domain.StatusUnsupported {
		t.Fatalf("DepthStatus = %q, want unsupported", book.DepthStatus)
	}
	if !strings.Contains(book.Error, "no live liquidity") {
		t.Fatalf("Error should preserve no-live-liquidity reason, got %q", book.Error)
	}
}

func TestFetchLighterZeroVolumeReturnsUnsupported(t *testing.T) {
	marketID := 176
	adapter := RESTAdapter{
		Platform: "lighter",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(`{"order_book_details":[{"market_id":176,"daily_quote_token_volume":0}]}`), nil
		})},
		MaxAttempts: 1,
	}

	vol, err := adapter.FetchTicker(context.Background(), domain.SymbolSub{
		Platform:      "lighter",
		Canonical:     "GME",
		DisplaySymbol: "GME-USDT (perp)",
		APISymbol:     "GME",
		MarketID:      &marketID,
	})
	if err != nil {
		t.Fatalf("zero Lighter volume should be statusized instead of returned as collector error: %v", err)
	}
	if vol.Status != domain.StatusUnsupported {
		t.Fatalf("Status = %q, want unsupported", vol.Status)
	}
	if !strings.Contains(vol.Error, "no live liquidity") {
		t.Fatalf("Error should preserve no-live-liquidity reason, got %q", vol.Error)
	}
}

func TestRESTAdapterLimiterSpacesEveryHTTPRequestAttempt(t *testing.T) {
	requestCount := 0
	adapter := RESTAdapter{
		Platform: "binance",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestCount++
			return jsonResponse(`{}`), nil
		})},
		MaxAttempts: 1,
		limiter:     newRequestLimiter(20),
	}

	start := time.Now()
	for i := 0; i < 3; i++ {
		var out map[string]any
		if err := adapter.fetchJSON(context.Background(), http.MethodGet, "https://example.invalid/test", nil, &out); err != nil {
			t.Fatalf("fetchJSON(%d): %v", i, err)
		}
	}
	elapsed := time.Since(start)
	if requestCount != 3 {
		t.Fatalf("requestCount=%d want 3", requestCount)
	}
	if elapsed < 90*time.Millisecond {
		t.Fatalf("limiter allowed 3 attempts in %s, want at least 90ms", elapsed)
	}
}

func TestRESTAdapterLimiterHonorsContextCancellationBeforeHTTPRequest(t *testing.T) {
	requestCount := 0
	adapter := RESTAdapter{
		Platform: "binance",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestCount++
			return jsonResponse(`{}`), nil
		})},
		MaxAttempts: 1,
		limiter:     newRequestLimiter(1),
	}

	var out map[string]any
	if err := adapter.fetchJSON(context.Background(), http.MethodGet, "https://example.invalid/test", nil, &out); err != nil {
		t.Fatalf("first fetchJSON: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.fetchJSON(ctx, http.MethodGet, "https://example.invalid/test", nil, &out); err == nil {
		t.Fatalf("second fetchJSON should fail while waiting on cancelled context")
	}
	if requestCount != 1 {
		t.Fatalf("cancelled wait must not issue HTTP request, got %d requests", requestCount)
	}
}

func TestClassifyDepthMarksSparseBookPartialWhenTwoPercentNotCovered(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Bids:        make([]domain.Level, 20),
		Asks:        make([]domain.Level, 20),
		APILevelCap: 80,
	}
	for i := 0; i < 20; i++ {
		book.Bids[i] = domain.Level{Price: 100 - float64(i)*0.01, Size: 1}
		book.Asks[i] = domain.Level{Price: 100.1 + float64(i)*0.01, Size: 1}
	}
	book.LevelsReturned = len(book.Bids) + len(book.Asks)

	status, reason := classifyDepth(book)
	if status != domain.StatusPartial || reason != domain.ReasonSparseBook {
		t.Fatalf("expected partial sparse_book for shallow book, got status=%s reason=%s farthest=%f", status, reason, farthestDistancePct(book))
	}
}

func TestClassifyDepthMarksCompleteWhenTwoPercentIsCovered(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Bids:        []domain.Level{{Price: 99.9, Size: 1}, {Price: 97.9, Size: 1}},
		Asks:        []domain.Level{{Price: 100.1, Size: 1}, {Price: 102.1, Size: 1}},
		APILevelCap: 4,
	}
	book.LevelsReturned = len(book.Bids) + len(book.Asks)

	status, reason := classifyDepth(book)
	if status != domain.StatusComplete || reason != "" {
		t.Fatalf("expected complete when farthest level covers 2%%, got status=%s reason=%s", status, reason)
	}
}

func TestFinalizeBookCountsTotalBidAndAskLevels(t *testing.T) {
	book := domain.OrderBookSnapshot{
		Platform:    "edgeX",
		APILevelCap: 400,
		Bids:        make([]domain.Level, 200),
		Asks:        make([]domain.Level, 200),
	}
	for i := 0; i < 200; i++ {
		book.Bids[i] = domain.Level{Price: 100 - float64(i)*0.004, Size: 1}
		book.Asks[i] = domain.Level{Price: 100.1 + float64(i)*0.004, Size: 1}
	}

	got := finalizeBook(book, "raw", domain.SourceRawOrderbook, "")
	if got.LevelsReturned != 400 || got.BidLevelsReturned != 200 || got.AskLevelsReturned != 200 {
		t.Fatalf("expected bid+ask level counts, got total=%d bid=%d ask=%d", got.LevelsReturned, got.BidLevelsReturned, got.AskLevelsReturned)
	}
	if got.PartialReason != domain.ReasonAPILevelCap {
		t.Fatalf("expected api_level_cap when total cap is filled but 2%% is not covered, got status=%s reason=%s", got.DepthStatus, got.PartialReason)
	}
}

func TestTierDepthMetricsRequiresBothSidesToCoverTier(t *testing.T) {
	book := finalizeBook(domain.OrderBookSnapshot{
		Platform:    "binance",
		APILevelCap: 4,
		Bids: []domain.Level{
			{Price: 99.9, Size: 1},
			{Price: 97.9, Size: 1},
		},
		Asks: []domain.Level{
			{Price: 100.1, Size: 1},
			{Price: 100.2, Size: 1},
		},
	}, "raw", domain.SourceRawOrderbook, "https://example.test/raw")

	got := TierDepthMetrics(book, 0.02)
	if got.DepthStatus != domain.StatusPartial || got.PartialReason != domain.ReasonAPILevelCap {
		t.Fatalf("expected partial/api_level_cap when ask side does not cover tier, got %+v", got)
	}
	if got.FarthestBidPct < 2 || got.FarthestAskPct >= 2 {
		t.Fatalf("expected asymmetric coverage details, got bid=%f ask=%f", got.FarthestBidPct, got.FarthestAskPct)
	}
}

func TestTierDepthMetricsSelectsAggregatedGateBookForDeepTier(t *testing.T) {
	book := finalizeBook(domain.OrderBookSnapshot{
		Platform:       "gate",
		SourceEndpoint: "https://example.test/raw",
		APILevelCap:    4,
		Bids: []domain.Level{
			{Price: 99.9, Size: 1},
			{Price: 99.8, Size: 1},
		},
		Asks: []domain.Level{
			{Price: 100.1, Size: 1},
			{Price: 100.2, Size: 1},
		},
	}, "gate_raw", domain.SourceRawOrderbook, "https://example.test/raw")
	book.SourceBooks["gate_agg_10"] = domain.BookView{
		SourceID:       "gate_agg_10",
		Source:         domain.SourceAggregatedOrderbook,
		SourceEndpoint: "https://example.test/agg?interval=10",
		Bids: []domain.Level{
			{Price: 99.9, Size: 1},
			{Price: 97.9, Size: 1},
		},
		Asks: []domain.Level{
			{Price: 100.1, Size: 1},
			{Price: 102.1, Size: 1},
		},
		APILevelCap: 4,
		StepUSD:     0.1,
	}

	got := TierDepthMetrics(book, 0.02)
	if got.DepthStatus != domain.StatusAggregatedOrderbook || got.SourceID != "gate_agg_10" || got.DepthSource != domain.SourceAggregatedOrderbook {
		t.Fatalf("expected deep tier to use aggregated gate book, got %+v", got)
	}
}

func TestFetchOKXUsesBooksFullLimit5000(t *testing.T) {
	var gotURL string
	a := RESTAdapter{Platform: "okx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(`{"data":[{"bids":[["99","1"]],"asks":[["101","1"]]}]}`), nil
	})}, MaxAttempts: 1}

	_, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC-USDT-SWAP", APILevelCap: 800, ContractSize: 0.01})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	if !strings.Contains(gotURL, "/api/v5/market/books-full") || !strings.Contains(gotURL, "sz=5000") {
		t.Fatalf("expected OKX books-full sz=5000, got %s", gotURL)
	}
}

func TestFetchOKXMultipliesSizeByCtVal(t *testing.T) {
	a := RESTAdapter{Platform: "okx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{"data":[{"bids":[["100","500","0","3"]],"asks":[["101","700","0","4"]]}]}`), nil
	})}, MaxAttempts: 1}

	book, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{
		DisplaySymbol: "BTC-USDT (perp)",
		Canonical:     "BTC",
		APISymbol:     "BTC-USDT-SWAP",
		APILevelCap:   5000,
		ContractSize:  0.01,
	})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	if len(book.Bids) != 1 || book.Bids[0].Size != 5 {
		t.Fatalf("expected bid size 500*0.01=5, got %+v", book.Bids)
	}
	if len(book.Asks) != 1 || book.Asks[0].Size != 7 {
		t.Fatalf("expected ask size 700*0.01=7, got %+v", book.Asks)
	}
}

func TestFetchOKXErrorsWhenContractSizeMissing(t *testing.T) {
	a := RESTAdapter{Platform: "okx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{"data":[{"bids":[["100","1"]],"asks":[["101","1"]]}]}`), nil
	})}, MaxAttempts: 1}

	_, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{
		DisplaySymbol: "BTC-USDT (perp)",
		Canonical:     "BTC",
		APISymbol:     "BTC-USDT-SWAP",
		APILevelCap:   5000,
	})
	if err == nil || !strings.Contains(err.Error(), "contract_size missing") {
		t.Fatalf("expected contract_size missing error, got %v", err)
	}
}

func TestFetchBybitUsesLimit1000(t *testing.T) {
	var gotURL string
	a := RESTAdapter{Platform: "bybit", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(`{"result":{"b":[["99","1"]],"a":[["101","1"]]}}`), nil
	})}, MaxAttempts: 1}

	_, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTCUSDT", APILevelCap: 1000})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	if !strings.Contains(gotURL, "limit=1000") {
		t.Fatalf("expected Bybit limit=1000, got %s", gotURL)
	}
}

func TestFetchBingXUsesAPISymbol(t *testing.T) {
	var gotURL string
	a := RESTAdapter{Platform: "bingx", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return jsonResponse(`{"data":{"bids":[["99","1"]],"asks":[["101","1"]]}}`), nil
	})}, MaxAttempts: 1}

	_, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC/USDT wrong", APISymbol: "BTC-USDT"})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	if !strings.Contains(gotURL, "symbol=BTC-USDT") {
		t.Fatalf("expected BingX request to use APISymbol, got %s", gotURL)
	}
}

func TestFetchBitgetAddsAllMergeScaleBooks(t *testing.T) {
	var urls []string
	a := RESTAdapter{Platform: "bitget", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		urls = append(urls, req.URL.String())
		return jsonResponse(`{"data":{"bids":[["99","1"]],"asks":[["101","1"]]}}`), nil
	})}, MaxAttempts: 1}

	book, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTCUSDT"})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	for _, sourceID := range []string{"bitget_merge_scale0", "bitget_merge_scale1", "bitget_merge_scale2", "bitget_merge_scale3"} {
		if _, ok := book.SourceBooks[sourceID]; !ok {
			t.Fatalf("expected %s source book, got %+v", sourceID, book.SourceBooks)
		}
	}
	joined := strings.Join(urls, " ")
	for _, precision := range []string{"precision=scale0", "precision=scale1", "precision=scale2", "precision=scale3"} {
		if !strings.Contains(joined, precision) {
			t.Fatalf("expected %s request, got %+v", precision, urls)
		}
	}
}

func TestFetchGateAddsAggregatedIntervalBook(t *testing.T) {
	var urls []string
	a := RESTAdapter{Platform: "gate", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		urls = append(urls, req.URL.String())
		return jsonResponse(`{"bids":[{"p":"99","s":"1"}],"asks":[{"p":"101","s":"1"}]}`), nil
	})}, MaxAttempts: 1}

	book, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{Canonical: "BTC", DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC_USDT", QuantoMultiplier: 0.0001})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	if _, ok := book.SourceBooks["gate_agg_10"]; !ok {
		t.Fatalf("expected gate_agg_10 source book, got %+v", book.SourceBooks)
	}
	if _, ok := book.SourceBooks["gate_agg_100"]; !ok {
		t.Fatalf("expected gate_agg_100 source book, got %+v", book.SourceBooks)
	}
	joined := strings.Join(urls, " ")
	if len(urls) != 3 || !strings.Contains(joined, "interval=10") || !strings.Contains(joined, "interval=100") {
		t.Fatalf("expected raw, interval=10, and interval=100 requests, got %+v", urls)
	}
}

func TestFetchHyperliquidAddsAggregatedSigFigBook(t *testing.T) {
	var bodies []string
	a := RESTAdapter{Platform: "hyperliquid", Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(data))
		return jsonResponse(`{"levels":[[{"px":"99","sz":"1"}],[{"px":"101","sz":"1"}]]}`), nil
	})}, MaxAttempts: 1}

	book, err := a.FetchOrderBook(context.Background(), domain.SymbolSub{DisplaySymbol: "BTC-USDT (perp)", APISymbol: "BTC"})
	if err != nil {
		t.Fatalf("FetchOrderBook: %v", err)
	}
	for _, sourceID := range []string{"hyperliquid_s5_m2", "hyperliquid_s5_m5", "hyperliquid_s4", "hyperliquid_s3"} {
		if _, ok := book.SourceBooks[sourceID]; !ok {
			t.Fatalf("expected %s source book, got %+v", sourceID, book.SourceBooks)
		}
	}
	joined := strings.Join(bodies, " ")
	if len(bodies) != 5 || !strings.Contains(joined, `"nSigFigs":5`) || !strings.Contains(joined, `"mantissa":2`) || !strings.Contains(joined, `"mantissa":5`) || !strings.Contains(joined, `"nSigFigs":4`) || !strings.Contains(joined, `"nSigFigs":3`) {
		t.Fatalf("expected raw and Hyperliquid multi-view requests, got %+v", bodies)
	}
}

func TestEdgeXContractIDReturnsCatalogValue(t *testing.T) {
	sub := domain.SymbolSub{Canonical: "BTC", ContractID: "10000001"}
	got, err := edgeXContractID(sub)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "10000001" {
		t.Fatalf("got %q, want 10000001", got)
	}
	if _, err := edgeXContractID(domain.SymbolSub{Canonical: "DOGE"}); err == nil {
		t.Fatal("expected error when ContractID empty")
	}
}

func TestParseEdgeXLevels(t *testing.T) {
	raw := []struct {
		Price string `json:"price"`
		Size  string `json:"size"`
	}{
		{Price: "100.1", Size: "2.5"},
		{Price: "0", Size: "1"},
		{Price: "101.2", Size: ""},
	}
	levels := parseEdgeXLevels(raw)
	if len(levels) != 1 || levels[0].Price != 100.1 || levels[0].Size != 2.5 {
		t.Fatalf("unexpected levels: %+v", levels)
	}
}

func TestShouldRetryTransientFailures(t *testing.T) {
	if !shouldRetry(0, errors.New("timeout")) {
		t.Fatal("expected transport errors to be retried")
	}
	if !shouldRetry(http.StatusTooManyRequests, nil) {
		t.Fatal("expected rate-limit responses to be retried")
	}
	if !shouldRetry(http.StatusBadGateway, nil) {
		t.Fatal("expected 5xx responses to be retried")
	}
	if shouldRetry(http.StatusForbidden, nil) {
		t.Fatal("expected 403 responses not to be retried")
	}
}

func TestLighterMarketIDReturnsCatalogValue(t *testing.T) {
	zero := 0
	one := 1
	if got, _ := lighterMarketID(domain.SymbolSub{MarketID: &zero}); got != 0 {
		t.Fatalf("expected market_id=0, got %d", got)
	}
	if got, _ := lighterMarketID(domain.SymbolSub{MarketID: &one}); got != 1 {
		t.Fatalf("expected market_id=1, got %d", got)
	}
	if _, err := lighterMarketID(domain.SymbolSub{Canonical: "DOGE"}); err == nil {
		t.Fatal("expected error when MarketID nil")
	}
}

func TestLighterSnapshotUpdateAndDelete(t *testing.T) {
	provider := NewLighterWSProvider("", time.Minute)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().UnixMilli(), 0)
	if err := provider.applyLighterUpdate(1, lighterWSOrderBook{
		Asks:       []lighterWSLevel{{Price: "101", Size: "0"}, {Price: "102", Size: "4"}},
		Bids:       []lighterWSLevel{{Price: "98", Size: "5"}},
		BeginNonce: 10,
		Nonce:      11,
	}, time.Now().UnixMilli()); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	bids, asks, _, err := provider.Snapshot(1)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(asks) != 1 || asks[0].Price != 102 || asks[0].Size != 4 {
		t.Fatalf("unexpected asks: %+v", asks)
	}
	if len(bids) != 2 || bids[0].Price != 99 || bids[1].Price != 98 {
		t.Fatalf("unexpected bids: %+v", bids)
	}
}

func TestLighterNonceGapMarksBookUnavailable(t *testing.T) {
	provider := NewLighterWSProvider("", time.Minute)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().UnixMilli(), 0)
	if err := provider.applyLighterUpdate(1, lighterWSOrderBook{BeginNonce: 12, Nonce: 13}, time.Now().UnixMilli()); err == nil {
		t.Fatal("expected nonce gap error")
	}
	if _, _, _, err := provider.Snapshot(1); err == nil {
		t.Fatal("expected snapshot to fail after nonce gap")
	} else if !strings.Contains(err.Error(), "nonce gap") {
		t.Fatalf("snapshot error = %q, want nonce gap detail", err.Error())
	}
}

func TestLighterOverlappingNonceUpdateDoesNotMarkBookUnavailable(t *testing.T) {
	provider := NewLighterWSProvider("", time.Minute)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().UnixMilli(), 0)
	if err := provider.applyLighterUpdate(1, lighterWSOrderBook{
		Asks:       []lighterWSLevel{{Price: "102", Size: "4"}},
		Bids:       []lighterWSLevel{{Price: "98", Size: "5"}},
		BeginNonce: 9,
		Nonce:      11,
	}, time.Now().UnixMilli()); err != nil {
		t.Fatalf("overlapping update should be accepted, got %v", err)
	}
	if _, _, _, err := provider.Snapshot(1); err != nil {
		t.Fatalf("snapshot should remain available after overlapping update: %v", err)
	}
}

func TestLighterQuietMarketUsesProviderHeartbeatForStaleCheck(t *testing.T) {
	provider := NewLighterWSProvider("", 5*time.Second)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().Add(-time.Minute).UnixMilli(), 0)
	provider.markMessageReceived(time.Now().UTC())

	if _, _, _, err := provider.Snapshot(1); err != nil {
		t.Fatalf("quiet market should remain available while provider receives messages: %v", err)
	}
}

func TestLighterSnapshotStalesWhenProviderHeartbeatStops(t *testing.T) {
	provider := NewLighterWSProvider("", 5*time.Second)
	provider.applyLighterSnapshot(1, lighterWSOrderBook{
		Asks:  []lighterWSLevel{{Price: "101", Size: "2"}},
		Bids:  []lighterWSLevel{{Price: "99", Size: "3"}},
		Nonce: 10,
	}, time.Now().Add(-time.Minute).UnixMilli(), 0)

	if _, _, _, err := provider.Snapshot(1); err == nil {
		t.Fatal("expected snapshot to stale when provider heartbeat is also stale")
	} else if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("snapshot error = %q, want stale detail", err.Error())
	}
}
