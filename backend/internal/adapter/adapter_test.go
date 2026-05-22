package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"edgex-dashboard/backend/internal/domain"
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
	if err := provider.applyLighterUpdate(1, lighterWSOrderBook{BeginNonce: 9, Nonce: 11}, time.Now().UnixMilli()); err == nil {
		t.Fatal("expected nonce gap error")
	}
	if _, _, _, err := provider.Snapshot(1); err == nil {
		t.Fatal("expected snapshot to fail after nonce gap")
	}
}
