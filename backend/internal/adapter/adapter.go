package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/domain"
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
}

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
	var bids, asks []domain.Level
	var err error
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
		bids, asks, err = a.fetchEdgeX(ctx, sub)
	case "lighter":
		bids, asks, err = a.fetchLighter(ctx, sub)
	default:
		err = fmt.Errorf("unsupported platform %s", a.Platform)
	}
	_ = start
	if err != nil {
		book.DepthStatus = domain.StatusError
		book.Error = err.Error()
		return book, err
	}
	book.Bids = bids
	book.Asks = asks
	book = finalizeBook(book, defaultSourceID(a.Platform), defaultDepthSource(a.Platform), sub.SourceEndpoint)
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
	var raw float64
	var err error
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
		err = fmt.Errorf("unsupported platform %s", a.Platform)
	}
	if err != nil {
		vol.Error = err.Error()
		return vol, err
	}
	vol.Volume24HUSD = raw
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
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "edgex-dashboard/0.1")
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

func retryBackoff(attempt int) time.Duration {
	return time.Duration(attempt*attempt) * 300 * time.Millisecond
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
		Data []struct{ Bids, Asks [][]string }
	}
	url := "https://www.okx.com/api/v5/market/books-full?sz=5000&instId=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
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
	url := "https://pro.edgex.exchange/api/v1/public/quote/getDepth?level=200&contractId=" + contractID
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
		Data []struct {
			VolCcy24H string `json:"volCcy24h"`
			Last      string `json:"last"`
		}
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://www.okx.com/api/v5/market/ticker?instId="+sub.APISymbol, nil, &resp); err != nil {
		return 0, err
	}
	if len(resp.Data) == 0 {
		return 0, errors.New("empty okx ticker")
	}
	vol, _ := strconv.ParseFloat(resp.Data[0].VolCcy24H, 64)
	last, _ := strconv.ParseFloat(resp.Data[0].Last, 64)
	return vol * last, nil
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
	if resp.Data.QuoteVolume != "" {
		return strconv.ParseFloat(resp.Data.QuoteVolume, 64)
	}
	volume, _ := strconv.ParseFloat(resp.Data.Volume, 64)
	last, _ := strconv.ParseFloat(resp.Data.LastPrice, 64)
	if volume <= 0 || last <= 0 {
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
	url := "https://pro.edgex.exchange/api/v1/public/quote/getTicker?contractId=" + contractID
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return 0, err
	}
	if len(resp.Data) == 0 {
		return 0, errors.New("empty edgex ticker")
	}
	value := anyFloat(resp.Data[0].Value)
	if value <= 0 {
		return 0, errors.New("empty edgex ticker value")
	}
	return value, nil
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
	return 0, fmt.Errorf("lighter market %d volume not found", marketID)
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

func finalizeBook(book domain.OrderBookSnapshot, sourceID, depthSource, sourceEndpoint string) domain.OrderBookSnapshot {
	if sourceID == "" {
		sourceID = "raw"
	}
	if depthSource == "" {
		depthSource = domain.SourceRawOrderbook
	}
	if sourceEndpoint == "" {
		sourceEndpoint = book.SourceEndpoint
	}
	book.SourceID = sourceID
	book.DepthSource = depthSource
	book.SourceEndpoint = sourceEndpoint
	book.BidLevelsReturned = len(book.Bids)
	book.AskLevelsReturned = len(book.Asks)
	book.LevelsReturned = book.BidLevelsReturned + book.AskLevelsReturned
	book.FarthestBidPct, book.FarthestAskPct = farthestSideDistancePct(book.Bids, book.Asks)
	book.FarthestDistancePC = maxFloat(book.FarthestBidPct, book.FarthestAskPct)
	book.DepthStatus, book.PartialReason = classifyDepth(book)
	if book.SourceBooks == nil {
		book.SourceBooks = map[string]domain.BookView{}
	}
	view := domain.BookView{
		SourceID:       sourceID,
		Source:         depthSource,
		SourceEndpoint: sourceEndpoint,
		Bids:           book.Bids,
		Asks:           book.Asks,
		SnapshotTS:     book.SnapshotTS,
		APILevelCap:    book.APILevelCap,
	}
	book.SourceBooks[sourceID] = enrichBookViewMetrics(view, midPrice(book.Bids, book.Asks))
	return book
}

func TierDepthMetrics(book domain.OrderBookSnapshot, tier float64) domain.TierDepthMetrics {
	view := selectBookView(book, tier)
	mid := midPrice(book.Bids, book.Asks)
	if mid <= 0 {
		mid = midPrice(view.Bids, view.Asks)
	}
	bidFloor := mid * (1 - tier)
	askCeil := mid * (1 + tier)
	var bidUSD, askUSD float64
	for _, level := range view.Bids {
		if level.Price >= bidFloor {
			bidUSD += level.Price * level.Size
		}
	}
	for _, level := range view.Asks {
		if level.Price <= askCeil {
			askUSD += level.Price * level.Size
		}
	}
	bidLevels := len(view.Bids)
	askLevels := len(view.Asks)
	levels := bidLevels + askLevels
	view = enrichBookViewMetrics(view, mid)
	farBid, farAsk := farthestSideDistancePctFromMid(view.Bids, view.Asks, mid)
	status, reason := classifyDepthView(view.Source, farBid, farAsk, tier*100, levels, view.APILevelCap)
	resolutionOK := viewResolutionOK(view, tier, mid)
	if farBid >= tier*100 && farAsk >= tier*100 && !resolutionOK {
		status = domain.StatusPartial
		reason = domain.ReasonFeedTruncation
	}
	metric := domain.TierDepthMetrics{
		BidUSD:               bidUSD,
		AskUSD:               askUSD,
		TotalUSD:             bidUSD + askUSD,
		DepthStatus:          status,
		PartialReason:        reason,
		DepthSource:          view.Source,
		SourceID:             view.SourceID,
		SourceEndpoint:       view.SourceEndpoint,
		LevelsReturned:       levels,
		BidLevelsReturned:    bidLevels,
		AskLevelsReturned:    askLevels,
		APILevelCap:          view.APILevelCap,
		FarthestBidPct:       farBid,
		FarthestAskPct:       farAsk,
		FarthestDistancePct:  maxFloat(farBid, farAsk),
		AggregationParams:    view.AggregationParams,
		PolicyAcceptance:     policyAcceptanceForView(view.Source, status),
		UnofficialUIEndpoint: view.UnofficialUIEndpoint,
	}
	if view.PolicyAcceptance == domain.PolicyLooseGroupedApprox || view.PolicyAcceptance == domain.PolicyLooseLowerBound {
		metric.DepthStatus = domain.StatusPartial
		metric.StrictComplete = false
		metric.PolicyAcceptance = view.PolicyAcceptance
		if metric.PartialReason == "" {
			metric.PartialReason = domain.ReasonFeedTruncation
		}
	}
	domain.DeriveDepthMetricsDefaults(book.DepthStatus, &metric)
	return metric
}

func selectBookView(book domain.OrderBookSnapshot, tier float64) domain.BookView {
	mid := midPrice(book.Bids, book.Asks)
	candidates := make([]domain.BookView, 0, len(book.SourceBooks)+1)
	if book.SourceBooks != nil {
		for _, view := range book.SourceBooks {
			candidates = append(candidates, enrichBookViewMetrics(view, mid))
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, enrichBookViewMetrics(domain.BookView{
			SourceID:       book.SourceID,
			Source:         book.DepthSource,
			SourceEndpoint: book.SourceEndpoint,
			Bids:           book.Bids,
			Asks:           book.Asks,
			SnapshotTS:     book.SnapshotTS,
			APILevelCap:    book.APILevelCap,
		}, mid))
	}
	if mid <= 0 && len(candidates) > 0 {
		mid = midPrice(candidates[0].Bids, candidates[0].Asks)
		for i := range candidates {
			candidates[i] = enrichBookViewMetrics(candidates[i], mid)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return viewStepForSort(candidates[i]) < viewStepForSort(candidates[j])
	})
	targetPct := tier * 100
	maxStep := tier * mid / 4
	for _, view := range candidates {
		farBid, farAsk := farthestSideDistancePctFromMid(view.Bids, view.Asks, mid)
		if farBid >= targetPct && farAsk >= targetPct && (view.StepUSD <= 0 || view.StepUSD <= maxStep || view.Source != domain.SourceAggregatedOrderbook) && view.PolicyAcceptance == "" {
			return view
		}
	}
	best := candidates[0]
	bestCoverage := -1.0
	for _, view := range candidates {
		farBid, farAsk := farthestSideDistancePctFromMid(view.Bids, view.Asks, mid)
		coverage := minFloat(farBid, farAsk)
		if coverage > bestCoverage {
			best = view
			bestCoverage = coverage
		}
	}
	return best
}

func viewStepForSort(view domain.BookView) float64 {
	if view.StepUSD <= 0 {
		return 0
	}
	return view.StepUSD
}

func viewResolutionOK(view domain.BookView, tier, mid float64) bool {
	if view.Source != domain.SourceAggregatedOrderbook {
		return true
	}
	if view.StepUSD <= 0 || mid <= 0 {
		return true
	}
	return view.StepUSD <= tier*mid/4
}

func multiplySize(levels []domain.Level, multiplier float64) []domain.Level {
	for i := range levels {
		levels[i].Size *= multiplier
	}
	return levels
}

func anyFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case map[string]any:
		if px, ok := x["px"]; ok {
			return anyFloat(px)
		}
	}
	return 0
}

func classifyDepth(book domain.OrderBookSnapshot) (string, string) {
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	levels := book.LevelsReturned
	if levels == 0 {
		levels = len(book.Bids) + len(book.Asks)
	}
	farBid, farAsk := farthestSideDistancePct(book.Bids, book.Asks)
	return classifyDepthView(book.DepthSource, farBid, farAsk, 2, levels, book.APILevelCap)
}

func classifyDepthView(source string, farBid, farAsk, targetPct float64, levels, apiMax int) (string, string) {
	if levels == 0 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	covered := farBid >= targetPct && farAsk >= targetPct
	if covered {
		if source == domain.SourceAggregatedOrderbook {
			return domain.StatusAggregatedOrderbook, ""
		}
		if source == domain.SourceWSLimitedDepth {
			return domain.StatusWSLimitedDepth, ""
		}
		return domain.StatusComplete, ""
	}
	if apiMax > 0 && levels >= apiMax {
		return domain.StatusPartial, domain.ReasonAPILevelCap
	}
	if apiMax > 0 && float64(levels) < float64(apiMax)*0.8 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	return domain.StatusPartial, domain.ReasonUnknown
}

func policyAcceptanceForView(source, status string) string {
	switch status {
	case domain.StatusComplete:
		return domain.PolicyRawStrict
	case domain.StatusAggregatedOrderbook, domain.StatusWSLimitedDepth:
		return domain.PolicyAggregatedStrict
	case domain.StatusPartial:
		if source == domain.SourceAggregatedOrderbook {
			return domain.PolicyLooseGroupedApprox
		}
		return domain.PolicyLooseLowerBound
	default:
		return ""
	}
}

func enrichBookViewMetrics(view domain.BookView, mid float64) domain.BookView {
	if view.StepUSD <= 0 {
		view.StepUSD = medianAdjacentStepUSD(view.Bids, view.Asks)
	}
	if view.ResolutionPct <= 0 && view.StepUSD > 0 && mid > 0 {
		view.ResolutionPct = view.StepUSD / mid * 100
	}
	if view.PolicyAcceptance == "" {
		view.PolicyAcceptance = policyAcceptanceForView(view.Source, "")
	}
	return view
}

func medianAdjacentStepUSD(bids, asks []domain.Level) float64 {
	diffs := make([]float64, 0, maxInt(len(bids)-1, 0)+maxInt(len(asks)-1, 0))
	appendDiffs := func(levels []domain.Level) {
		for i := 1; i < len(levels); i++ {
			diff := mathAbs(levels[i].Price - levels[i-1].Price)
			if diff > 0 {
				diffs = append(diffs, diff)
			}
		}
	}
	appendDiffs(bids)
	appendDiffs(asks)
	if len(diffs) == 0 {
		return 0
	}
	sort.Float64s(diffs)
	mid := len(diffs) / 2
	if len(diffs)%2 == 1 {
		return diffs[mid]
	}
	return (diffs[mid-1] + diffs[mid]) / 2
}

func apiLevelCap(platform string) int {
	switch platform {
	case "binance":
		return 2000
	case "okx":
		return 10000
	case "bybit":
		return 2000
	case "bitget":
		return 200
	case "bingx":
		return 2000
	case "mexc":
		return 2000
	case "gate":
		return 400
	case "hyperliquid":
		return 40
	case "edgeX":
		return 400
	case "lighter":
		return 0
	default:
		return 0
	}
}

func farthestDistancePct(book domain.OrderBookSnapshot) float64 {
	farBid, farAsk := farthestSideDistancePct(book.Bids, book.Asks)
	return maxFloat(farBid, farAsk)
}

func farthestSideDistancePct(bids, asks []domain.Level) (float64, float64) {
	return farthestSideDistancePctFromMid(bids, asks, midPrice(bids, asks))
}

func farthestSideDistancePctFromMid(bids, asks []domain.Level, mid float64) (float64, float64) {
	if len(bids) == 0 || len(asks) == 0 || mid <= 0 {
		return 0, 0
	}
	farBid := mathAbs(bids[len(bids)-1].Price-mid) / mid * 100
	farAsk := mathAbs(asks[len(asks)-1].Price-mid) / mid * 100
	return farBid, farAsk
}

func midPrice(bids, asks []domain.Level) float64 {
	if len(bids) == 0 || len(asks) == 0 {
		return 0
	}
	return (bids[0].Price + asks[0].Price) / 2
}

func defaultSourceID(platform string) string {
	switch platform {
	case "okx":
		return "okx_books_full"
	case "bybit":
		return "bybit_raw_1000"
	case "lighter":
		return "lighter_ws"
	default:
		return "raw"
	}
}

func defaultDepthSource(platform string) string {
	if platform == "lighter" {
		return domain.SourceWSLocalBook
	}
	return domain.SourceRawOrderbook
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
