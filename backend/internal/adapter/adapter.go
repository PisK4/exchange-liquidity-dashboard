package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"edgex-ops-intelligence/backend/internal/domain"
)

type ExchangeAdapter interface {
	Name() string
	FetchInstruments(ctx context.Context) (CatalogResult, error)
	FetchOrderBook(ctx context.Context, sub domain.SymbolSub) (domain.OrderBookSnapshot, error)
	FetchTicker(ctx context.Context, sub domain.SymbolSub) (domain.VolumeSnapshot, error)
	FetchTop30(ctx context.Context, surface domain.SymbolSub) ([]domain.Top30Row, error)
}

type RESTAdapter struct {
	Platform    string
	Client      *http.Client
	MaxAttempts int
	Lighter     LighterBookProvider
	EdgeXPerpV2 EdgeXPerpV2BookProvider
	limiter     *requestLimiter
}

var (
	errLighterNoLiveLiquidity = errors.New("lighter market has no live liquidity")
	errMarketNoLiveData       = errors.New("market has no live market data")
)

func New(platform string, timeout time.Duration) ExchangeAdapter {
	return RESTAdapter{Platform: platform, Client: newHTTPClient(timeout, ""), MaxAttempts: 2}
}

func NewWithLighter(platform string, timeout time.Duration, lighter LighterBookProvider) ExchangeAdapter {
	return NewWithLighterAndProxy(platform, timeout, lighter, "")
}

// NewWithLighterAndProxy lets callers route the platform's REST traffic
// through an HTTP/HTTPS proxy (e.g. http://host.docker.internal:7897). An
// empty proxy URL keeps the legacy direct-connection behaviour. The proxy
// is opt-in per deployment; production should only enable it when the
// container runtime cannot reach the upstream exchanges directly.
func NewWithLighterAndProxy(platform string, timeout time.Duration, lighter LighterBookProvider, proxy string) ExchangeAdapter {
	return RESTAdapter{Platform: platform, Client: newHTTPClient(timeout, proxy), MaxAttempts: 2, Lighter: lighter}
}

// NewWithLighterProxyAndRateLimit adds a per-adapter request limiter for the
// live collection path while preserving the legacy proxy and Lighter wiring.
func NewWithLighterProxyAndRateLimit(platform string, timeout time.Duration, lighter LighterBookProvider, proxy string, perSec int) ExchangeAdapter {
	return NewWithLiveBooksProxyAndRateLimit(platform, timeout, lighter, nil, proxy, perSec)
}

// NewWithLiveBooksProxyAndRateLimit wires optional WS local-book providers
// into the REST adapter. Providers are platform/surface gated at fetch time,
// so passing the same provider set to every platform keeps collector wiring
// simple without letting one venue's WS source leak into another venue.
func NewWithLiveBooksProxyAndRateLimit(platform string, timeout time.Duration, lighter LighterBookProvider, edgeXPerpV2 EdgeXPerpV2BookProvider, proxy string, perSec int) ExchangeAdapter {
	return RESTAdapter{Platform: platform, Client: newHTTPClient(timeout, proxy), MaxAttempts: 2, Lighter: lighter, EdgeXPerpV2: edgeXPerpV2, limiter: newRequestLimiter(perSec)}
}

func newHTTPClient(timeout time.Duration, proxy string) *http.Client {
	if proxy == "" {
		return &http.Client{Timeout: timeout}
	}
	parsed, err := url.Parse(proxy)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		// Fall back to direct connection on a malformed proxy URL rather
		// than fail the whole collector — the operator will see the
		// failed-direct error in the next collection cycle.
		return &http.Client{Timeout: timeout}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(parsed)
	return &http.Client{Timeout: timeout, Transport: tr}
}

func (a RESTAdapter) Name() string { return a.Platform }

func (a RESTAdapter) FetchOrderBook(ctx context.Context, sub domain.SymbolSub) (domain.OrderBookSnapshot, error) {
	start := time.Now()
	cap := sub.APILevelCap
	if recommended := apiLevelCap(a.Platform); recommended > cap {
		cap = recommended
	}
	if cap == 0 {
		cap = apiLevelCap(a.Platform)
	}
	book := domain.OrderBookSnapshot{
		Platform:       a.Platform,
		DisplaySymbol:  sub.DisplaySymbol,
		SourceEndpoint: sub.SourceEndpoint,
		SnapshotTS:     time.Now().UTC(),
		APILevelCap:    cap,
	}
	domain.ApplyOrderBookSurfaceMeta(&book, sub)
	if sub.APISymbol == "" {
		book.DepthStatus = domain.StatusUnsupported
		book.Error = "no catalog entry for (" + a.Platform + ", " + sub.Canonical + ")"
		return book, nil
	}
	var bids, asks []domain.Level
	var err error
	sourceID := defaultSourceIDForSub(a.Platform, sub)
	depthSource := defaultDepthSourceForSub(a.Platform, sub)
	sourceEndpoint := sourceEndpointForSub(a.Platform, sub)
	switch a.Platform {
	case "binance":
		bids, asks, err = a.fetchBinance(ctx, sub)
	case "okx":
		bids, asks, err = a.fetchOKX(ctx, sub)
	case "bybit":
		bids, asks, err = a.fetchBybit(ctx, sub)
	case "bitget":
		bids, asks, err = a.fetchBitget(ctx, sub)
	case "bingx":
		bids, asks, err = a.fetchBingX(ctx, sub)
	case "mexc":
		bids, asks, err = a.fetchMEXC(ctx, sub)
	case "gate":
		bids, asks, err = a.fetchGate(ctx, sub)
	case "hyperliquid":
		bids, asks, err = a.fetchHyperliquid(ctx, sub)
	case "edgeX":
		bids, asks, sourceID, depthSource, sourceEndpoint, err = a.fetchEdgeXOrderBook(ctx, sub)
	case "lighter":
		bids, asks, err = a.fetchLighter(ctx, sub)
	default:
		err = fmt.Errorf("unsupported platform %s", a.Platform)
	}
	_ = start
	if err != nil {
		book.DepthStatus = domain.StatusError
		book.Error = err.Error()
		if errors.Is(err, errLighterNoLiveLiquidity) || errors.Is(err, errMarketNoLiveData) {
			book.DepthStatus = domain.StatusUnsupported
			return book, nil
		}
		return book, err
	}
	book.Bids = bids
	book.Asks = asks
	book = finalizeBook(book, sourceID, depthSource, sourceEndpoint)
	switch a.Platform {
	case "gate":
		for _, interval := range []string{"10", "100"} {
			if view, viewErr := a.fetchGateAggregatedView(ctx, sub, interval); viewErr == nil {
				book.SourceBooks[view.SourceID] = view
			}
		}
	case "hyperliquid":
		for _, spec := range hyperliquidViewSpecs() {
			if view, viewErr := a.fetchHyperliquidAggregatedView(ctx, sub, spec); viewErr == nil {
				book.SourceBooks[view.SourceID] = view
			}
		}
	case "bitget":
		for _, precision := range []string{"scale0", "scale1", "scale2", "scale3"} {
			if view, viewErr := a.fetchBitgetMergeDepthView(ctx, sub, precision); viewErr == nil {
				book.SourceBooks[view.SourceID] = view
			}
		}
	}
	return book, nil
}

func (a RESTAdapter) FetchTicker(ctx context.Context, sub domain.SymbolSub) (domain.VolumeSnapshot, error) {
	now := time.Now().UTC()
	vol := domain.VolumeSnapshot{Platform: a.Platform, DisplaySymbol: sub.DisplaySymbol, SnapshotTS: now, SourceEndpoint: sub.SourceEndpoint, Status: domain.StatusUnsupported}
	domain.ApplyVolumeSurfaceMeta(&vol, sub)
	if sub.APISymbol == "" {
		vol.Error = "no catalog entry for (" + a.Platform + ", " + sub.Canonical + ")"
		return vol, nil
	}
	var raw float64
	var err error
	unknownPlatform := false
	switch a.Platform {
	case "binance":
		raw, err = a.fetchBinanceVolume(ctx, sub)
	case "okx":
		raw, err = a.fetchOKXVolume(ctx, sub)
	case "bybit":
		raw, err = a.fetchBybitVolume(ctx, sub)
	case "bitget":
		raw, err = a.fetchBitgetVolume(ctx, sub)
	case "bingx":
		raw, err = a.fetchBingXVolume(ctx, sub)
	case "mexc":
		raw, err = a.fetchMEXCVolume(ctx, sub)
	case "gate":
		raw, err = a.fetchGateVolume(ctx, sub)
	case "hyperliquid":
		raw, err = a.fetchHyperliquidVolume(ctx, sub)
	case "edgeX":
		raw, err = a.fetchEdgeXVolume(ctx, sub)
	case "lighter":
		raw, err = a.fetchLighterVolume(ctx, sub)
	default:
		unknownPlatform = true
		err = fmt.Errorf("unsupported platform %s", a.Platform)
	}
	if err != nil {
		vol.Error = err.Error()
		if errors.Is(err, errLighterNoLiveLiquidity) || errors.Is(err, errMarketNoLiveData) {
			vol.Status = domain.StatusUnsupported
			return vol, nil
		}
		// Reserve StatusUnsupported for "we have no adapter / catalog
		// entry for this platform"; transient upstream failures (HTTP
		// 4xx/5xx, timeouts, malformed payloads) get StatusError so
		// downstream KPI logic (e.g. liquidityKPIsLocked CG fallback)
		// can distinguish "platform is broken right now" from "platform
		// is not in scope".
		if !unknownPlatform {
			vol.Status = domain.StatusError
		}
		return vol, err
	}
	vol.Volume24HUSD = raw
	vol.SourceEndpoint = sourceEndpointForSub(a.Platform, sub)
	vol.Status = domain.StatusComplete
	return vol, nil
}

func (a RESTAdapter) FetchTop30(ctx context.Context, surface domain.SymbolSub) ([]domain.Top30Row, error) {
	return nil, fmt.Errorf("top30 live ranking not implemented for %s in V1; API returns explicit insufficient_history/status rows", a.Platform)
}

func (a RESTAdapter) fetchJSON(ctx context.Context, method, url string, body []byte, out any) error {
	attempts := a.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, statusCode, err := a.doJSONRequest(ctx, method, url, body)
		if err == nil && statusCode >= 200 && statusCode < 300 {
			return json.Unmarshal(data, out)
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("http %d: %s", statusCode, strings.TrimSpace(string(data[:min(len(data), 300)])))
		}
		if attempt == attempts || !shouldRetry(statusCode, err) {
			return lastErr
		}
		if err := sleepWithContext(ctx, retryBackoff(attempt)); err != nil {
			return err
		}
	}
	return lastErr
}

func (a RESTAdapter) doJSONRequest(ctx context.Context, method, url string, body []byte) ([]byte, int, error) {
	if a.limiter != nil {
		if err := a.limiter.Wait(ctx); err != nil {
			return nil, 0, err
		}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "edgex-ops-intelligence/0.1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func shouldRetry(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500
}

// retryBackoff returns an exponential-ish backoff with up-to 25% jitter.
// The jitter prevents 10 platform goroutines from synchronising their
// retries on the same upstream when an exchange returns 429/5xx.
func retryBackoff(attempt int) time.Duration {
	base := time.Duration(attempt*attempt) * 300 * time.Millisecond
	jitter := time.Duration(retryJitterFracBP(attempt)) * base / 10000
	return base + jitter
}

type requestLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newRequestLimiter(perSec int) *requestLimiter {
	if perSec <= 0 {
		return nil
	}
	return &requestLimiter{interval: time.Second / time.Duration(perSec)}
}

func (r *requestLimiter) Wait(ctx context.Context) error {
	r.mu.Lock()
	wait := r.interval - time.Since(r.last)
	if wait < 0 {
		wait = 0
	}
	r.last = time.Now().Add(wait)
	r.mu.Unlock()
	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryJitterFracBP returns the per-attempt jitter slice in basis points
// (0 - 2500 = 0 - 25%). It is split out so unit tests can stub randomness
// deterministically; the production path uses time.Now().UnixNano() as
// the entropy source which is good enough for retry de-syncing.
var retryJitterFracBP = func(attempt int) int {
	return int(time.Now().UnixNano() % 2501) // [0, 2500]
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a RESTAdapter) fetchBinance(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct{ Bids, Asks [][]string }
	url := "https://fapi.binance.com/fapi/v1/depth?limit=1000&symbol=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return parseStringLevels(resp.Bids), parseStringLevels(resp.Asks), nil
}

func (a RESTAdapter) fetchOKX(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct{ Bids, Asks [][]string }
	}
	url := "https://www.okx.com/api/v5/market/books-full?sz=5000&instId=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	if isOKXNoLiveMarket(resp.Code, resp.Msg) {
		return nil, nil, fmt.Errorf("okx book %s has no live market data: %s: %w", sub.APISymbol, strings.TrimSpace(resp.Msg), errMarketNoLiveData)
	}
	if resp.Code != "" && resp.Code != "0" {
		return nil, nil, fmt.Errorf("okx book code %s: %s", resp.Code, strings.TrimSpace(resp.Msg))
	}
	if len(resp.Data) == 0 {
		return nil, nil, errors.New("empty okx book")
	}
	// OKX SWAP/FUTURES `sz` is in contracts; convert to base currency via ctVal.
	// SPOT books have contract_size=0 in catalog, which would zero out the size,
	// so only multiply when a positive ctVal is configured for the instrument.
	if sub.ContractSize <= 0 {
		return nil, nil, fmt.Errorf("okx %s: contract_size missing from catalog (run `make catalog`)", sub.Canonical)
	}
	bids := multiplySize(parseStringLevels(resp.Data[0].Bids), sub.ContractSize)
	asks := multiplySize(parseStringLevels(resp.Data[0].Asks), sub.ContractSize)
	return bids, asks, nil
}

func (a RESTAdapter) fetchBybit(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct{ Result struct{ B, A [][]string } }
	url := "https://api.bybit.com/v5/market/orderbook?category=linear&limit=1000&symbol=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return parseStringLevels(resp.Result.B), parseStringLevels(resp.Result.A), nil
}

func (a RESTAdapter) fetchBitget(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct {
		Data struct{ Bids, Asks [][]string }
	}
	url := "https://api.bitget.com/api/v2/mix/market/orderbook?productType=USDT-FUTURES&limit=100&symbol=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return parseStringLevels(resp.Data.Bids), parseStringLevels(resp.Data.Asks), nil
}

func (a RESTAdapter) fetchBitgetMergeDepthView(ctx context.Context, sub domain.SymbolSub, precision string) (domain.BookView, error) {
	var resp struct {
		Data struct{ Bids, Asks [][]any }
	}
	if precision == "" {
		precision = "scale0"
	}
	url := "https://api.bitget.com/api/v2/mix/market/merge-depth?productType=USDT-FUTURES&precision=" + precision + "&limit=100&symbol=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return domain.BookView{}, err
	}
	return domain.BookView{
		SourceID:          "bitget_merge_" + precision,
		Source:            domain.SourceAggregatedOrderbook,
		SourceEndpoint:    url,
		Bids:              parseAnyLevels(resp.Data.Bids),
		Asks:              parseAnyLevels(resp.Data.Asks),
		SnapshotTS:        time.Now().UTC(),
		APILevelCap:       200,
		AggregationParams: map[string]string{"precision": precision, "limit": "100"},
	}, nil
}

func (a RESTAdapter) fetchBingX(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct {
		Data struct{ Bids, Asks [][]any }
	}
	url := "https://open-api.bingx.com/openApi/swap/v2/quote/depth?limit=1000&symbol=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return parseAnyLevels(resp.Data.Bids), parseAnyLevels(resp.Data.Asks), nil
}

func (a RESTAdapter) fetchMEXC(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct {
		Data struct{ Bids, Asks [][]any }
	}
	if sub.ContractSize <= 0 {
		return nil, nil, fmt.Errorf("mexc %s: contract_size missing from catalog (run `make catalog`)", sub.Canonical)
	}
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	url := "https://contract.mexc.com/api/v1/contract/depth/" + contract + "?limit=1000"
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return multiplySize(parseAnyLevels(resp.Data.Bids), sub.ContractSize), multiplySize(parseAnyLevels(resp.Data.Asks), sub.ContractSize), nil
}

func (a RESTAdapter) fetchEdgeXOrderBook(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, string, string, string, error) {
	contractID, err := edgeXContractID(sub)
	if err != nil {
		return nil, nil, "", "", "", err
	}
	if isEdgeXPerpV2(sub) && a.EdgeXPerpV2 != nil {
		bids, asks, _, wsErr := a.EdgeXPerpV2.Snapshot(contractID)
		if wsErr == nil {
			return bids, asks, "edgeX-perp-v2-ws-depth-200", domain.SourceWSLocalBook, a.EdgeXPerpV2.SourceEndpoint(), nil
		}
		log.Printf("edgeX perp v2 ws fallback to REST: display_symbol=%s contract_id=%s market_surface=%s lineage=%s reason=%v", sub.DisplaySymbol, contractID, sub.MarketSurface, sub.Lineage, wsErr)
	}
	bids, asks, err := a.fetchEdgeX(ctx, sub)
	if err != nil {
		return nil, nil, "", "", "", err
	}
	return bids, asks, defaultSourceIDForSub(a.Platform, sub), defaultDepthSourceForSub(a.Platform, sub), sourceEndpointForSub(a.Platform, sub), nil
}

func (a RESTAdapter) fetchEdgeX(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	contractID, err := edgeXContractID(sub)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Data []struct {
			Bids []struct {
				Price string `json:"price"`
				Size  string `json:"size"`
			} `json:"bids"`
			Asks []struct {
				Price string `json:"price"`
				Size  string `json:"size"`
			} `json:"asks"`
		} `json:"data"`
	}
	url := edgeXDepthURL(sub, contractID)
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Data) == 0 {
		return nil, nil, errors.New("empty edgex depth")
	}
	return parseEdgeXLevels(resp.Data[0].Bids), parseEdgeXLevels(resp.Data[0].Asks), nil
}

func (a RESTAdapter) fetchGate(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	return a.fetchGateLevels(ctx, sub, "")
}

func (a RESTAdapter) fetchGateLevels(ctx context.Context, sub domain.SymbolSub, interval string) ([]domain.Level, []domain.Level, error) {
	var resp struct{ Bids, Asks []struct{ P, S any } }
	if sub.QuantoMultiplier <= 0 {
		return nil, nil, fmt.Errorf("gate %s: quanto_multiplier missing from catalog (run `make catalog`)", sub.Canonical)
	}
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	url := "https://api.gateio.ws/api/v4/futures/usdt/order_book?limit=200&with_id=true&contract=" + contract
	if interval != "" {
		url += "&interval=" + interval
	}
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return multiplySize(parseGateLevels(resp.Bids), sub.QuantoMultiplier), multiplySize(parseGateLevels(resp.Asks), sub.QuantoMultiplier), nil
}

func (a RESTAdapter) fetchGateAggregatedView(ctx context.Context, sub domain.SymbolSub, interval string) (domain.BookView, error) {
	if interval == "" {
		interval = "10"
	}
	bids, asks, err := a.fetchGateLevels(ctx, sub, interval)
	if err != nil {
		return domain.BookView{}, err
	}
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	url := "https://api.gateio.ws/api/v4/futures/usdt/order_book?limit=200&with_id=true&contract=" + contract + "&interval=" + interval
	step, _ := strconv.ParseFloat(interval, 64)
	policy := ""
	if interval == "100" {
		policy = domain.PolicyLooseGroupedApprox
	}
	return domain.BookView{
		SourceID:          "gate_agg_" + interval,
		Source:            domain.SourceAggregatedOrderbook,
		SourceEndpoint:    url,
		Bids:              bids,
		Asks:              asks,
		SnapshotTS:        time.Now().UTC(),
		APILevelCap:       400,
		StepUSD:           step,
		PolicyAcceptance:  policy,
		AggregationParams: map[string]string{"interval": interval},
	}, nil
}

func (a RESTAdapter) fetchHyperliquid(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	return a.fetchHyperliquidLevels(ctx, sub, 0, 0)
}

func (a RESTAdapter) fetchHyperliquidLevels(ctx context.Context, sub domain.SymbolSub, sigFigs int, mantissa int) ([]domain.Level, []domain.Level, error) {
	payload := map[string]any{"type": "l2Book", "coin": sub.APISymbol}
	if sigFigs > 0 {
		payload["nSigFigs"] = sigFigs
	}
	if mantissa > 0 {
		payload["mantissa"] = mantissa
	}
	body, _ := json.Marshal(payload)
	var resp struct {
		Levels [][]map[string]any `json:"levels"`
	}
	if err := a.fetchJSON(ctx, http.MethodPost, "https://api.hyperliquid.xyz/info", body, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Levels) < 2 {
		return nil, nil, errors.New("empty hyperliquid levels")
	}
	return parseMapLevels(resp.Levels[0]), parseMapLevels(resp.Levels[1]), nil
}

type hyperliquidViewSpec struct {
	sourceID string
	sigFigs  int
	mantissa int
}

func hyperliquidViewSpecs() []hyperliquidViewSpec {
	return []hyperliquidViewSpec{
		{sourceID: "hyperliquid_s5_m2", sigFigs: 5, mantissa: 2},
		{sourceID: "hyperliquid_s5_m5", sigFigs: 5, mantissa: 5},
		{sourceID: "hyperliquid_s4", sigFigs: 4},
		{sourceID: "hyperliquid_s3", sigFigs: 3},
	}
}

func (a RESTAdapter) fetchHyperliquidAggregatedView(ctx context.Context, sub domain.SymbolSub, spec hyperliquidViewSpec) (domain.BookView, error) {
	bids, asks, err := a.fetchHyperliquidLevels(ctx, sub, spec.sigFigs, spec.mantissa)
	if err != nil {
		return domain.BookView{}, err
	}
	params := map[string]string{"nSigFigs": strconv.Itoa(spec.sigFigs)}
	if spec.mantissa > 0 {
		params["mantissa"] = strconv.Itoa(spec.mantissa)
	}
	return domain.BookView{
		SourceID:          spec.sourceID,
		Source:            domain.SourceAggregatedOrderbook,
		SourceEndpoint:    "https://api.hyperliquid.xyz/info",
		Bids:              bids,
		Asks:              asks,
		SnapshotTS:        time.Now().UTC(),
		APILevelCap:       40,
		AggregationParams: params,
	}, nil
}

func (a RESTAdapter) fetchLighter(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	if a.Lighter == nil {
		return nil, nil, errors.New("lighter ws provider is not configured")
	}
	marketID, err := lighterMarketID(sub)
	if err != nil {
		return nil, nil, err
	}
	bids, asks, _, err := a.Lighter.Snapshot(marketID)
	if err != nil {
		if strings.Contains(err.Error(), "ws book empty") {
			return nil, nil, fmt.Errorf("lighter market %d has no live liquidity: %w", marketID, errLighterNoLiveLiquidity)
		}
		return nil, nil, err
	}
	return bids, asks, nil
}

func (a RESTAdapter) fetchBinanceVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp struct{ QuoteVolume string }
	if err := a.fetchJSON(ctx, http.MethodGet, "https://fapi.binance.com/fapi/v1/ticker/24hr?symbol="+sub.APISymbol, nil, &resp); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(resp.QuoteVolume, 64)
}

func (a RESTAdapter) fetchOKXVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			VolCcy24H string `json:"volCcy24h"`
			Last      string `json:"last"`
		}
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://www.okx.com/api/v5/market/ticker?instId="+sub.APISymbol, nil, &resp); err != nil {
		return 0, err
	}
	if isOKXNoLiveMarket(resp.Code, resp.Msg) {
		return 0, fmt.Errorf("okx ticker %s has no live market data: %s: %w", sub.APISymbol, strings.TrimSpace(resp.Msg), errMarketNoLiveData)
	}
	if resp.Code != "" && resp.Code != "0" {
		return 0, fmt.Errorf("okx ticker code %s: %s", resp.Code, strings.TrimSpace(resp.Msg))
	}
	if len(resp.Data) == 0 {
		return 0, errors.New("empty okx ticker")
	}
	vol, _ := strconv.ParseFloat(resp.Data[0].VolCcy24H, 64)
	last, _ := strconv.ParseFloat(resp.Data[0].Last, 64)
	return vol * last, nil
}

func isOKXNoLiveMarket(code string, msg string) bool {
	normalized := strings.ToLower(strings.TrimSpace(msg))
	if code == "51001" && strings.Contains(normalized, "instrument") && strings.Contains(normalized, "exist") {
		return true
	}
	if code == "51014" && strings.Contains(normalized, "index") && strings.Contains(normalized, "exist") {
		return true
	}
	return false
}

func (a RESTAdapter) fetchBybitVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp struct {
		Result struct {
			List []struct {
				Turnover24H string `json:"turnover24h"`
			}
		}
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://api.bybit.com/v5/market/tickers?category=linear&symbol="+sub.APISymbol, nil, &resp); err != nil {
		return 0, err
	}
	if len(resp.Result.List) == 0 {
		return 0, errors.New("empty bybit ticker")
	}
	return strconv.ParseFloat(resp.Result.List[0].Turnover24H, 64)
}

func (a RESTAdapter) fetchBitgetVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp struct {
		Data []struct {
			UsdtVolume  string `json:"usdtVolume"`
			QuoteVolume string `json:"quoteVolume"`
		}
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://api.bitget.com/api/v2/mix/market/ticker?productType=USDT-FUTURES&symbol="+sub.APISymbol, nil, &resp); err != nil {
		return 0, err
	}
	if len(resp.Data) == 0 {
		return 0, errors.New("empty bitget ticker")
	}
	if resp.Data[0].QuoteVolume != "" {
		return strconv.ParseFloat(resp.Data[0].QuoteVolume, 64)
	}
	return strconv.ParseFloat(resp.Data[0].UsdtVolume, 64)
}

func (a RESTAdapter) fetchBingXVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			QuoteVolume string `json:"quoteVolume"`
			Volume      string `json:"volume"`
			LastPrice   string `json:"lastPrice"`
		}
	}
	url := "https://open-api.bingx.com/openApi/swap/v2/quote/ticker?symbol=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return 0, err
	}
	if resp.Code != 0 {
		msg := strings.TrimSpace(resp.Msg)
		if strings.Contains(strings.ToLower(msg), "pause currently") {
			return 0, fmt.Errorf("bingx ticker %s is paused: %s: %w", sub.APISymbol, msg, errMarketNoLiveData)
		}
		return 0, fmt.Errorf("bingx ticker code %d: %s", resp.Code, msg)
	}
	if resp.Data.QuoteVolume != "" {
		value, err := strconv.ParseFloat(resp.Data.QuoteVolume, 64)
		if err != nil {
			return 0, err
		}
		if value < 0 {
			return 0, errors.New("invalid bingx quote volume")
		}
		return value, nil
	}
	volume, volumeOK := parseAnyFloat(resp.Data.Volume)
	if !volumeOK {
		return 0, errors.New("empty bingx quote volume")
	}
	if volume < 0 {
		return 0, errors.New("invalid bingx quote volume")
	}
	if volume == 0 {
		return 0, nil
	}
	last, lastOK := parseAnyFloat(resp.Data.LastPrice)
	if !lastOK || last <= 0 {
		return 0, errors.New("empty bingx quote volume")
	}
	return volume * last, nil
}

func (a RESTAdapter) fetchMEXCVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp struct {
		Data struct {
			Amount24 any `json:"amount24"`
		}
	}
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://contract.mexc.com/api/v1/contract/ticker?symbol="+contract, nil, &resp); err != nil {
		return 0, err
	}
	amount := anyFloat(resp.Data.Amount24)
	if amount <= 0 {
		return 0, errors.New("empty mexc quote volume")
	}
	return amount, nil
}

func (a RESTAdapter) fetchEdgeXVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	contractID, err := edgeXContractID(sub)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Data []struct {
			Value any `json:"value"`
		} `json:"data"`
	}
	url := edgeXTickerURL(sub, contractID)
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return 0, err
	}
	if len(resp.Data) == 0 {
		return 0, errors.New("empty edgex ticker")
	}
	value, ok := parseAnyFloat(resp.Data[0].Value)
	if !ok {
		return 0, errors.New("empty edgex ticker value")
	}
	if value < 0 {
		return 0, errors.New("invalid edgex ticker value")
	}
	return value, nil
}

func isEdgeXPerpV2(sub domain.SymbolSub) bool {
	surface := strings.ToLower(strings.TrimSpace(sub.MarketSurface))
	lineage := strings.ToLower(strings.TrimSpace(sub.Lineage))
	return surface == "perp_v2" || strings.Contains(lineage, "perp-v2")
}

func edgeXDepthURL(sub domain.SymbolSub, contractID string) string {
	if isEdgeXPerpV2(sub) {
		return "https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getDepth?contractId=" + contractID + "&level=200"
	}
	return "https://pro.edgex.exchange/api/v1/public/quote/getDepth?level=200&contractId=" + contractID
}

func edgeXTickerURL(sub domain.SymbolSub, contractID string) string {
	if isEdgeXPerpV2(sub) {
		return "https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getTicker?contractId=" + contractID
	}
	return "https://pro.edgex.exchange/api/v1/public/quote/getTicker?contractId=" + contractID
}

func sourceEndpointForSub(platform string, sub domain.SymbolSub) string {
	if platform == "edgeX" && isEdgeXPerpV2(sub) {
		return "https://edgex-prod-v2.edgex.exchange/api/v2/public/quote"
	}
	return sub.SourceEndpoint
}

func defaultSourceIDForSub(platform string, sub domain.SymbolSub) string {
	if platform == "edgeX" && isEdgeXPerpV2(sub) {
		return "edgeX-perp-v2-rest-depth-200"
	}
	return defaultSourceID(platform)
}

func defaultDepthSourceForSub(platform string, sub domain.SymbolSub) string {
	if platform == "edgeX" && isEdgeXPerpV2(sub) {
		return domain.SourceRestSnapshot
	}
	return defaultDepthSource(platform)
}

func (a RESTAdapter) fetchGateVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	var resp []struct {
		Volume24HQuote string `json:"volume_24h_quote"`
	}
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://api.gateio.ws/api/v4/futures/usdt/tickers?contract="+contract, nil, &resp); err != nil {
		return 0, err
	}
	if len(resp) == 0 {
		return 0, errors.New("empty gate ticker")
	}
	return strconv.ParseFloat(resp[0].Volume24HQuote, 64)
}

func (a RESTAdapter) fetchHyperliquidVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	body, _ := json.Marshal(map[string]any{"type": "metaAndAssetCtxs"})
	var resp []json.RawMessage
	if err := a.fetchJSON(ctx, http.MethodPost, "https://api.hyperliquid.xyz/info", body, &resp); err != nil {
		return 0, err
	}
	if len(resp) < 2 {
		return 0, errors.New("empty hyperliquid asset contexts")
	}
	var meta struct {
		Universe []struct {
			Name string `json:"name"`
		} `json:"universe"`
	}
	var contexts []struct {
		DayNtlVlm string `json:"dayNtlVlm"`
	}
	if err := json.Unmarshal(resp[0], &meta); err != nil {
		return 0, err
	}
	if err := json.Unmarshal(resp[1], &contexts); err != nil {
		return 0, err
	}
	for i, asset := range meta.Universe {
		if asset.Name == sub.APISymbol && i < len(contexts) {
			return strconv.ParseFloat(contexts[i].DayNtlVlm, 64)
		}
	}
	return 0, fmt.Errorf("hyperliquid asset %s not found", sub.APISymbol)
}

func (a RESTAdapter) fetchLighterVolume(ctx context.Context, sub domain.SymbolSub) (float64, error) {
	marketID, err := lighterMarketID(sub)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Details []struct {
			MarketID              int     `json:"market_id"`
			DailyQuoteTokenVolume float64 `json:"daily_quote_token_volume"`
		} `json:"order_book_details"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://mainnet.zklighter.elliot.ai/api/v1/orderBookDetails?filter=all", nil, &resp); err != nil {
		return 0, err
	}
	for _, detail := range resp.Details {
		if detail.MarketID == marketID && detail.DailyQuoteTokenVolume > 0 {
			return detail.DailyQuoteTokenVolume, nil
		}
	}
	return 0, fmt.Errorf("lighter market %d has no live liquidity: volume not found: %w", marketID, errLighterNoLiveLiquidity)
}

func edgeXContractID(sub domain.SymbolSub) (string, error) {
	if sub.ContractID != "" {
		return sub.ContractID, nil
	}
	return "", fmt.Errorf("edgex %s: contract_id missing from catalog (run `make catalog`)", sub.Canonical)
}

func parseEdgeXLevels(raw []struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}) []domain.Level {
	levels := make([]domain.Level, 0, len(raw))
	for _, row := range raw {
		p, _ := strconv.ParseFloat(row.Price, 64)
		s, _ := strconv.ParseFloat(row.Size, 64)
		if p > 0 && s > 0 {
			levels = append(levels, domain.Level{Price: p, Size: s})
		}
	}
	return levels
}

func parseStringLevels(raw [][]string) []domain.Level {
	levels := make([]domain.Level, 0, len(raw))
	for _, row := range raw {
		if len(row) < 2 {
			continue
		}
		p, _ := strconv.ParseFloat(row[0], 64)
		s, _ := strconv.ParseFloat(row[1], 64)
		if p > 0 && s > 0 {
			levels = append(levels, domain.Level{Price: p, Size: s})
		}
	}
	return levels
}

func parseFloatLevels(raw [][]float64) []domain.Level {
	levels := make([]domain.Level, 0, len(raw))
	for _, row := range raw {
		if len(row) >= 2 && row[0] > 0 && row[1] > 0 {
			levels = append(levels, domain.Level{Price: row[0], Size: row[1]})
		}
	}
	return levels
}

func parseGateLevels(raw []struct{ P, S any }) []domain.Level {
	levels := make([]domain.Level, 0, len(raw))
	for _, row := range raw {
		p := anyFloat(row.P)
		s := anyFloat(row.S)
		if p > 0 && s > 0 {
			levels = append(levels, domain.Level{Price: p, Size: s})
		}
	}
	return levels
}

func parseAnyLevels(raw [][]any) []domain.Level {
	levels := make([]domain.Level, 0, len(raw))
	for _, row := range raw {
		if len(row) < 2 {
			continue
		}
		p := anyFloat(row[0])
		s := anyFloat(row[1])
		if p > 0 && s > 0 {
			levels = append(levels, domain.Level{Price: p, Size: s})
		}
	}
	return levels
}

func parseMapLevels(raw []map[string]any) []domain.Level {
	levels := make([]domain.Level, 0, len(raw))
	for _, row := range raw {
		p := anyFloat(row["px"])
		s := anyFloat(row["sz"])
		if p > 0 && s > 0 {
			levels = append(levels, domain.Level{Price: p, Size: s})
		}
	}
	return levels
}

func multiplySize(levels []domain.Level, multiplier float64) []domain.Level {
	for i := range levels {
		levels[i].Size *= multiplier
	}
	return levels
}

func anyFloat(v any) float64 {
	f, _ := parseAnyFloat(v)
	return f
}

func parseAnyFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		if strings.TrimSpace(x) == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	case map[string]any:
		if px, ok := x["px"]; ok {
			return parseAnyFloat(px)
		}
	}
	return 0, false
}
