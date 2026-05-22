package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edgex-dashboard/backend/internal/domain"
)

// ErrInstrumentNotFound is returned by adapter daily-history fetchers when
// the upstream exchange responds with a "symbol does not exist" payload
// (e.g. BingX code 109425 returns `data: {}` instead of an array). Callers
// — notably the Top30 backfill goroutine — treat this as a permanent
// per-(platform, base) skip rather than a retriable failure, so a noisy
// commodity ticker (GOLD/NASDAQ100/OIL/SILVER on bingx) doesn't keep
// generating warning logs every backfill round.
var ErrInstrumentNotFound = errors.New("instrument not found on exchange")

// DailyVolumeHistoryFetcher is an optional capability that exchange adapters
// can satisfy in order to back-fill per-(platform, display_symbol) 7d / 14d
// daily USD volume from their native kline / candlestick endpoints. The
// collector layer calls this once on boot and once a day so the V1 7d 市占率
// KPI can return real numbers instead of waiting for the rolling daily
// writer to accumulate seven UTC days of /derivatives readings.
//
// Implementations MUST:
//   - return USD-denominated daily volume (quote_volume / turnover, not the
//     base-asset coin count) so AdjustedVolume() can apply MEXC×0.4 /
//     Gate×0.5 discounts at query time without further conversion;
//   - snap each daily row's Day to UTC 00:00 of the corresponding day;
//   - leave DataSource as DataSourceNativeBackfill so the in-memory dedup
//     and MySQL UPSERT routes know this row yields to any live coingecko /
//     native row landing on the same slot;
//   - return an error (do not return a partial slice + nil) when the upstream
//     call fails, so the orchestrator can log it and skip the platform for
//     that round.
type DailyVolumeHistoryFetcher interface {
	FetchDailyVolumeHistory(ctx context.Context, sub domain.SymbolSub, days int) ([]domain.DailyVolumeAggregate, error)
}

// FetchDailyVolumeHistory dispatches to the per-platform implementation. The
// method is defined on RESTAdapter so the New / NewWithLighter / NewWithProxy
// constructors all expose the capability automatically; platforms without a
// kline endpoint return an "unsupported" error and the orchestrator falls
// back to CoinGecko-only accumulation.
func (a RESTAdapter) FetchDailyVolumeHistory(ctx context.Context, sub domain.SymbolSub, days int) ([]domain.DailyVolumeAggregate, error) {
	if days <= 0 {
		days = 14
	}
	if days > 30 {
		days = 30
	}
	now := time.Now().UTC()
	switch a.Platform {
	case "binance":
		return a.fetchBinanceDailyHistory(ctx, sub, days, now)
	case "okx":
		return a.fetchOKXDailyHistory(ctx, sub, days, now)
	case "bybit":
		return a.fetchBybitDailyHistory(ctx, sub, days, now)
	case "bitget":
		return a.fetchBitgetDailyHistory(ctx, sub, days, now)
	case "bingx":
		return a.fetchBingXDailyHistory(ctx, sub, days, now)
	case "mexc":
		return a.fetchMEXCDailyHistory(ctx, sub, days, now)
	case "gate":
		return a.fetchGateDailyHistory(ctx, sub, days, now)
	case "hyperliquid":
		return a.fetchHyperliquidDailyHistory(ctx, sub, days, now)
	case "edgeX":
		return a.fetchEdgeXDailyHistory(ctx, sub, days, now)
	case "lighter":
		return a.fetchLighterDailyHistory(ctx, sub, days, now)
	default:
		return nil, fmt.Errorf("daily volume history not supported for %s", a.Platform)
	}
}

// startOfUTCDayMS truncates a millisecond timestamp to UTC 00:00 of the same
// calendar day. Mirrors collector.startOfUTCDay but for ms-precision inputs.
func startOfUTCDayMS(ms int64) time.Time {
	t := time.UnixMilli(ms).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// looksLikeJSONArray reports whether a json.RawMessage payload begins
// with '[' (modulo leading whitespace). Used by adapters whose upstream
// alternates between `data: []` (success) and `data: {}` (error) so we
// can branch without trying both unmarshals.
func looksLikeJSONArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

func backfillRow(platform string, sub domain.SymbolSub, day time.Time, volume float64, endpoint string, now time.Time) domain.DailyVolumeAggregate {
	return domain.DailyVolumeAggregate{
		Day:            day,
		Platform:       platform,
		DisplaySymbol:  sub.DisplaySymbol,
		Volume24HUSD:   volume,
		Status:         domain.StatusComplete,
		DataSource:     domain.DataSourceNativeBackfill,
		SourceEndpoint: endpoint,
		SnapshotTS:     now,
	}
}

// --- per-exchange implementations -------------------------------------------------

// Binance USDT-M futures klines: `[[open_time, open, high, low, close, vol,
// close_time, quote_vol, count, taker_buy_vol, taker_buy_quote_vol, ignore]]`.
// Quote vol (index 7) is USDT, exactly what we want.
func (a RESTAdapter) fetchBinanceDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	endpoint := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?interval=1d&limit=%d&symbol=%s", days, sub.APISymbol)
	var resp [][]any
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp))
	for _, row := range resp {
		if len(row) < 8 {
			continue
		}
		openMS, ok := anyInt64(row[0])
		if !ok {
			continue
		}
		quote := anyFloatLoose(row[7])
		if quote <= 0 {
			continue
		}
		out = append(out, backfillRow("binance", sub, startOfUTCDayMS(openMS), quote, endpoint, now))
	}
	return out, nil
}

// OKX swap candles: `{ data: [[ts, o, h, l, c, vol, volCcy, volCcyQuote, confirm]] }`.
// volCcyQuote (index 7) is the quote-currency (USDT) volume.
func (a RESTAdapter) fetchOKXDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	endpoint := fmt.Sprintf("https://www.okx.com/api/v5/market/candles?bar=1D&limit=%d&instId=%s", days, sub.APISymbol)
	var resp struct {
		Data [][]any `json:"data"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp.Data))
	for _, row := range resp.Data {
		if len(row) < 8 {
			continue
		}
		ts, ok := anyInt64(row[0])
		if !ok {
			continue
		}
		quote := anyFloatLoose(row[7])
		if quote <= 0 {
			continue
		}
		out = append(out, backfillRow("okx", sub, startOfUTCDayMS(ts), quote, endpoint, now))
	}
	return out, nil
}

// Bybit v5 linear kline: `{ result: { list: [[startTime, open, high, low,
// close, volume, turnover]] } }`. turnover (index 6) is USDT quote volume.
func (a RESTAdapter) fetchBybitDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	endpoint := fmt.Sprintf("https://api.bybit.com/v5/market/kline?category=linear&interval=D&limit=%d&symbol=%s", days, sub.APISymbol)
	var resp struct {
		Result struct {
			List [][]string `json:"list"`
		} `json:"result"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp.Result.List))
	for _, row := range resp.Result.List {
		if len(row) < 7 {
			continue
		}
		ts, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			continue
		}
		quote, err := strconv.ParseFloat(row[6], 64)
		if err != nil || quote <= 0 {
			continue
		}
		out = append(out, backfillRow("bybit", sub, startOfUTCDayMS(ts), quote, endpoint, now))
	}
	return out, nil
}

// Bitget USDT-FUTURES candles: data[] = [ts, open, high, low, close,
// baseVol, quoteVol] (each string). Index 6 is the USDT quote volume.
func (a RESTAdapter) fetchBitgetDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	endpoint := fmt.Sprintf("https://api.bitget.com/api/v2/mix/market/candles?productType=USDT-FUTURES&granularity=1D&limit=%d&symbol=%s", days, sub.APISymbol)
	var resp struct {
		Data [][]any `json:"data"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp.Data))
	for _, row := range resp.Data {
		if len(row) < 7 {
			continue
		}
		ts, ok := anyInt64(row[0])
		if !ok {
			continue
		}
		quote := anyFloatLoose(row[6])
		if quote <= 0 {
			continue
		}
		out = append(out, backfillRow("bitget", sub, startOfUTCDayMS(ts), quote, endpoint, now))
	}
	return out, nil
}

// BingX swap v3 klines: `{ data: [{open, close, high, low, volume, time}] }`.
// The `volume` field is in quote (USDT) currency for the linear swap.
//
// BingX commodity / index "wrapper" contracts (GOLD, NASDAQ100, OIL,
// SILVER) are reported by CoinGecko under their bare names but only
// exist on BingX REST under prefixed symbols like NCFXAUD2USD-USDT —
// querying the bare name returns code 109425 with `data: {}` (an
// object, not an array). To avoid the unmarshal error spamming every
// backfill round we decode `data` into json.RawMessage first and
// surface ErrInstrumentNotFound on the object case so the caller skips
// the symbol silently.
func (a RESTAdapter) fetchBingXDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	endpoint := fmt.Sprintf("https://open-api.bingx.com/openApi/swap/v3/quote/klines?interval=1d&limit=%d&symbol=%s", days, sub.APISymbol)
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Code != 0 || len(envelope.Data) == 0 || !looksLikeJSONArray(envelope.Data) {
		// 109425 = "symbol not exist". Other non-zero codes (rate-limit,
		// auth, etc.) come back with code != 0 too but are usually
		// retriable; we only swallow the explicit not-exist code as
		// permanent and surface the rest as transient errors.
		if envelope.Code == 109425 || strings.Contains(envelope.Msg, "not exist") {
			return nil, fmt.Errorf("%w: bingx %s (code=%d msg=%q)",
				ErrInstrumentNotFound, sub.APISymbol, envelope.Code, envelope.Msg)
		}
		if envelope.Code != 0 {
			return nil, fmt.Errorf("bingx kline error: code=%d msg=%q", envelope.Code, envelope.Msg)
		}
		// `data: {}` with code=0 is unexpected but defensible — treat as
		// not-found to avoid hard-fail.
		return nil, fmt.Errorf("%w: bingx %s (empty data)", ErrInstrumentNotFound, sub.APISymbol)
	}
	var rows []struct {
		Open   string `json:"open"`
		Close  string `json:"close"`
		Volume string `json:"volume"`
		Time   int64  `json:"time"`
	}
	if err := json.Unmarshal(envelope.Data, &rows); err != nil {
		return nil, fmt.Errorf("bingx kline decode: %w", err)
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(rows))
	for _, row := range rows {
		vol, _ := strconv.ParseFloat(row.Volume, 64)
		if vol <= 0 {
			continue
		}
		close, _ := strconv.ParseFloat(row.Close, 64)
		// BingX `volume` on linear perpetuals is base-asset (BTC) volume.
		// Convert to USD via close price; absent a close fall back to
		// open. Skip the row if we cannot derive a positive USDT value.
		usd := vol * close
		if usd <= 0 {
			open, _ := strconv.ParseFloat(row.Open, 64)
			usd = vol * open
		}
		if usd <= 0 {
			continue
		}
		out = append(out, backfillRow("bingx", sub, startOfUTCDayMS(row.Time), usd, endpoint, now))
	}
	return out, nil
}

// MEXC USDT perpetual contract kline: `{ data: { time: [], open: [], close:
// [], high: [], low: [], vol: [], amount: [] } }`. amount[] is USDT quote
// volume; time[] entries are unix seconds.
func (a RESTAdapter) fetchMEXCDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	end := time.Now().UTC().Unix()
	start := end - int64(days)*86400
	endpoint := fmt.Sprintf("https://contract.mexc.com/api/v1/contract/kline/%s?interval=Day1&start=%d&end=%d", contract, start, end)
	var resp struct {
		Data struct {
			Time   []int64   `json:"time"`
			Amount []float64 `json:"amount"`
		} `json:"data"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	n := len(resp.Data.Time)
	if n != len(resp.Data.Amount) {
		return nil, errors.New("mexc kline: time/amount length mismatch")
	}
	out := make([]domain.DailyVolumeAggregate, 0, n)
	for i := 0; i < n; i++ {
		if resp.Data.Amount[i] <= 0 {
			continue
		}
		day := startOfUTCDayMS(resp.Data.Time[i] * 1000)
		out = append(out, backfillRow("mexc", sub, day, resp.Data.Amount[i], endpoint, now))
	}
	return out, nil
}

// Gate USDT futures candlesticks: each row carries `{ t: unix_seconds, v:
// base_contracts, c: close, ... }`. Multiplying by sub.QuantoMultiplier (the
// per-contract base-asset size, already loaded from instrument_catalog.yaml)
// and the close price gives USDT quote volume.
func (a RESTAdapter) fetchGateDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	if sub.QuantoMultiplier <= 0 {
		return nil, fmt.Errorf("gate %s: quanto_multiplier missing from catalog (run `make catalog`)", sub.Canonical)
	}
	contract := sub.APISymbol
	if contract == "" {
		contract = strings.ReplaceAll(strings.TrimSuffix(sub.DisplaySymbol, " (perp)"), "-", "_")
	}
	endpoint := fmt.Sprintf("https://api.gateio.ws/api/v4/futures/usdt/candlesticks?interval=1d&limit=%d&contract=%s", days, contract)
	var resp []struct {
		Timestamp int64  `json:"t"`
		Volume    any    `json:"v"`
		Close     string `json:"c"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp))
	for _, row := range resp {
		vol := anyFloatLoose(row.Volume)
		closeP, _ := strconv.ParseFloat(row.Close, 64)
		if vol <= 0 || closeP <= 0 {
			continue
		}
		usd := vol * sub.QuantoMultiplier * closeP
		if usd <= 0 {
			continue
		}
		out = append(out, backfillRow("gate", sub, startOfUTCDayMS(row.Timestamp*1000), usd, endpoint, now))
	}
	return out, nil
}

// Hyperliquid `candleSnapshot`: `[{t, T, s, i, o, c, h, l, v, n}, ...]`.
// `v` is base-asset volume; `c` (close) is USD price. Multiplying gives a
// best-effort USDT quote volume per day; for perp BTC / ETH / SOL this
// matches the platform's own `dayNtlVlm` reading within rounding.
func (a RESTAdapter) fetchHyperliquidDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	coin := sub.APISymbol
	if coin == "" {
		coin = strings.TrimSuffix(strings.SplitN(sub.DisplaySymbol, "-", 2)[0], " ")
	}
	startMS := time.Now().UTC().AddDate(0, 0, -days).UnixMilli()
	endMS := time.Now().UTC().UnixMilli()
	body, _ := json.Marshal(map[string]any{
		"type": "candleSnapshot",
		"req": map[string]any{
			"coin":      coin,
			"interval":  "1d",
			"startTime": startMS,
			"endTime":   endMS,
		},
	})
	endpoint := "https://api.hyperliquid.xyz/info"
	var resp []struct {
		Time   int64  `json:"t"`
		Close  string `json:"c"`
		Volume string `json:"v"`
	}
	if err := a.fetchJSON(ctx, http.MethodPost, endpoint, body, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp))
	for _, row := range resp {
		closeP, _ := strconv.ParseFloat(row.Close, 64)
		vol, _ := strconv.ParseFloat(row.Volume, 64)
		usd := vol * closeP
		if usd <= 0 {
			continue
		}
		out = append(out, backfillRow("hyperliquid", sub, startOfUTCDayMS(row.Time), usd, endpoint, now))
	}
	return out, nil
}

// EdgeX getKline returns `{ data: [{ klineTime, open, close, size, value, ... }] }`
// where `value` is the USDT quote volume. We pull DAY_1 candles bounded by
// timestamps so a fresh contract that listed mid-window still returns the
// rows it actually has.
func (a RESTAdapter) fetchEdgeXDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	contractID, err := edgeXContractID(sub)
	if err != nil {
		return nil, err
	}
	endpoint := "https://pro.edgex.exchange/api/v1/public/quote/getKline?priceType=LAST_PRICE&klineType=DAY_1&size=" +
		strconv.Itoa(days) + "&contractId=" + contractID
	type klineRow struct {
		KlineTime any `json:"klineTime"`
		Value     any `json:"value"`
	}
	var resp struct {
		Data struct {
			DataList []klineRow `json:"dataList"`
		} `json:"data"`
		// Older proxies / mocks sometimes flatten to top-level `dataList`
		// or even directly to `data: []`; keep those paths as fallbacks
		// so the production endpoint shape isn't the only one accepted.
		DataList    []klineRow `json:"dataList"`
		FlatDataArr []klineRow `json:"data_list"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	rows := resp.Data.DataList
	if len(rows) == 0 {
		rows = resp.DataList
	}
	if len(rows) == 0 {
		rows = resp.FlatDataArr
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(rows))
	for _, row := range rows {
		ts, ok := anyInt64(row.KlineTime)
		if !ok {
			continue
		}
		value := anyFloatLoose(row.Value)
		if value <= 0 {
			continue
		}
		out = append(out, backfillRow("edgeX", sub, startOfUTCDayMS(ts), value, endpoint, now))
	}
	return out, nil
}

// Lighter `GET /api/v1/candles?market_id=..&resolution=1d`:
//
//	{ code, r, c: [{ t, o, h, l, c, v, V, i }] }
//
// where `V` is the USD-quote volume.
func (a RESTAdapter) fetchLighterDailyHistory(ctx context.Context, sub domain.SymbolSub, days int, now time.Time) ([]domain.DailyVolumeAggregate, error) {
	marketID, err := lighterMarketID(sub)
	if err != nil {
		return nil, err
	}
	startSec := time.Now().UTC().AddDate(0, 0, -days).Unix()
	endSec := time.Now().UTC().Unix()
	endpoint := fmt.Sprintf(
		"https://mainnet.zklighter.elliot.ai/api/v1/candles?market_id=%d&resolution=1d&start_timestamp=%d&end_timestamp=%d&count_back=%d&set_timestamp_to_end=false",
		marketID, startSec, endSec, days,
	)
	var resp struct {
		Candles []struct {
			Time     int64 `json:"t"`
			QuoteVol any   `json:"V"`
			Close    any   `json:"c"`
			Volume   any   `json:"v"`
		} `json:"c"`
	}
	if err := a.fetchJSON(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.DailyVolumeAggregate, 0, len(resp.Candles))
	for _, c := range resp.Candles {
		usd := anyFloatLoose(c.QuoteVol)
		if usd <= 0 {
			base := anyFloatLoose(c.Volume)
			closeP := anyFloatLoose(c.Close)
			usd = base * closeP
		}
		if usd <= 0 {
			continue
		}
		out = append(out, backfillRow("lighter", sub, startOfUTCDayMS(c.Time), usd, endpoint, now))
	}
	return out, nil
}

// anyInt64 best-effort extracts a millisecond timestamp from a JSON value
// that may arrive as a JSON number (float64), int64, or a string.
func anyInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return i, true
		}
		f, err := t.Float64()
		if err == nil {
			return int64(f), true
		}
	case string:
		i, err := strconv.ParseInt(t, 10, 64)
		if err == nil {
			return i, true
		}
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

// anyFloatLoose mirrors anyFloat from adapter.go but also accepts json.Number
// — kline payloads sometimes encode amounts as numbers and sometimes as
// strings depending on the exchange.
func anyFloatLoose(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return anyFloat(v)
}
