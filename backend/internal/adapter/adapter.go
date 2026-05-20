package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	return RESTAdapter{Platform: platform, Client: &http.Client{Timeout: timeout}, MaxAttempts: 2}
}

func NewWithLighter(platform string, timeout time.Duration, lighter LighterBookProvider) ExchangeAdapter {
	return RESTAdapter{Platform: platform, Client: &http.Client{Timeout: timeout}, MaxAttempts: 2, Lighter: lighter}
}

func (a RESTAdapter) Name() string { return a.Platform }

func (a RESTAdapter) FetchOrderBook(ctx context.Context, sub domain.SymbolSub) (domain.OrderBookSnapshot, error) {
	start := time.Now()
	book := domain.OrderBookSnapshot{
		Platform:       a.Platform,
		DisplaySymbol:  sub.DisplaySymbol,
		SourceEndpoint: sub.SourceEndpoint,
		SnapshotTS:     time.Now().UTC(),
		APILevelCap:    apiLevelCap(a.Platform),
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
	book.LevelsReturned = min(len(bids), len(asks))
	book.DepthStatus, book.PartialReason = classifyDepth(book)
	book.FarthestDistancePC = farthestDistancePct(book)
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
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
	url := "https://www.okx.com/api/v5/market/books?sz=400&instId=" + sub.APISymbol
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Data) == 0 {
		return nil, nil, errors.New("empty okx book")
	}
	return parseStringLevels(resp.Data[0].Bids), parseStringLevels(resp.Data[0].Asks), nil
}

func (a RESTAdapter) fetchBybit(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct{ Result struct{ B, A [][]string } }
	url := "https://api.bybit.com/v5/market/orderbook?category=linear&limit=500&symbol=" + sub.APISymbol
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

func (a RESTAdapter) fetchBingX(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct {
		Data struct{ Bids, Asks [][]any }
	}
	url := "https://open-api.bingx.com/openApi/swap/v2/quote/depth?limit=1000&symbol=" + strings.TrimSuffix(sub.DisplaySymbol, " (perp)")
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return parseAnyLevels(resp.Data.Bids), parseAnyLevels(resp.Data.Asks), nil
}

func (a RESTAdapter) fetchMEXC(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	var resp struct {
		Data struct{ Bids, Asks [][]any }
	}
	contract := strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	contractSize, err := a.fetchMEXCContractSize(ctx, contract)
	if err != nil {
		return nil, nil, err
	}
	url := "https://contract.mexc.com/api/v1/contract/depth/" + contract + "?limit=1000"
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return multiplySize(parseAnyLevels(resp.Data.Bids), contractSize), multiplySize(parseAnyLevels(resp.Data.Asks), contractSize), nil
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
	var resp struct{ Bids, Asks []struct{ P, S any } }
	contract := strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	multiplier, err := a.fetchGateQuantoMultiplier(ctx, contract)
	if err != nil {
		return nil, nil, err
	}
	url := "https://api.gateio.ws/api/v4/futures/usdt/order_book?limit=200&with_id=true&contract=" + contract
	if err := a.fetchJSON(ctx, http.MethodGet, url, nil, &resp); err != nil {
		return nil, nil, err
	}
	return multiplySize(parseGateLevels(resp.Bids), multiplier), multiplySize(parseGateLevels(resp.Asks), multiplier), nil
}

func (a RESTAdapter) fetchHyperliquid(ctx context.Context, sub domain.SymbolSub) ([]domain.Level, []domain.Level, error) {
	body, _ := json.Marshal(map[string]any{"type": "l2Book", "coin": sub.APISymbol})
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
	url := "https://open-api.bingx.com/openApi/swap/v2/quote/ticker?symbol=" + strings.TrimSuffix(sub.DisplaySymbol, " (perp)")
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
	contract := strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
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
	contract := strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
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

func (a RESTAdapter) fetchMEXCContractSize(ctx context.Context, contract string) (float64, error) {
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://contract.mexc.com/api/v1/contract/detail?symbol="+contract, nil, &resp); err != nil {
		return 0, err
	}
	items, err := parseMEXCContractDetails(resp.Data)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if item.Symbol == contract && item.ContractSize > 0 {
			return item.ContractSize, nil
		}
	}
	return 0, fmt.Errorf("mexc contract size not found for %s", contract)
}

type mexcContractDetail struct {
	Symbol       string  `json:"symbol"`
	ContractSize float64 `json:"contractSize"`
}

func parseMEXCContractDetails(raw json.RawMessage) ([]mexcContractDetail, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("empty mexc contract detail")
	}
	var items []mexcContractDetail
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}
	var item mexcContractDetail
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	return []mexcContractDetail{item}, nil
}

func (a RESTAdapter) fetchGateQuantoMultiplier(ctx context.Context, contract string) (float64, error) {
	var resp struct {
		QuantoMultiplier string `json:"quanto_multiplier"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, "https://api.gateio.ws/api/v4/futures/usdt/contracts/"+contract, nil, &resp); err != nil {
		return 0, err
	}
	multiplier, err := strconv.ParseFloat(resp.QuantoMultiplier, 64)
	if err != nil || multiplier <= 0 {
		return 0, fmt.Errorf("invalid gate quanto_multiplier for %s", contract)
	}
	return multiplier, nil
}

func edgeXContractID(sub domain.SymbolSub) (string, error) {
	symbol := strings.TrimSuffix(sub.DisplaySymbol, " (perp)")
	canonical := strings.TrimSuffix(strings.ReplaceAll(symbol, "-", ""), "USDT")
	if canonical == "" {
		canonical = sub.Canonical
	}
	switch canonical {
	case "BTC":
		return "10000001", nil
	case "ETH":
		return "10000002", nil
	case "SOL":
		return "10000003", nil
	default:
		return "", fmt.Errorf("edgex contract id not configured for %s", sub.DisplaySymbol)
	}
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
	farthest := farthestDistancePct(book)
	if farthest >= 2 {
		return domain.StatusComplete, ""
	}
	if book.APILevelCap > 0 && book.LevelsReturned >= book.APILevelCap {
		return domain.StatusPartial, domain.ReasonAPILevelCap
	}
	if book.APILevelCap > 0 && float64(book.LevelsReturned) < float64(book.APILevelCap)*0.8 {
		return domain.StatusPartial, domain.ReasonSparseBook
	}
	return domain.StatusPartial, domain.ReasonUnknown
}

func apiLevelCap(platform string) int {
	switch platform {
	case "binance":
		return 2000
	case "okx":
		return 800
	case "bybit":
		return 1000
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
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		return 0
	}
	mid := (book.Bids[0].Price + book.Asks[0].Price) / 2
	if mid <= 0 {
		return 0
	}
	farBid := mathAbs(book.Bids[len(book.Bids)-1].Price-mid) / mid * 100
	farAsk := mathAbs(book.Asks[len(book.Asks)-1].Price-mid) / mid * 100
	if farBid > farAsk {
		return farBid
	}
	return farAsk
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
