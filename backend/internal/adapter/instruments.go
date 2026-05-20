package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// CatalogResult is the raw + parsed instrument list returned by an adapter's
// FetchInstruments call. The crawler (backend/scripts/build-catalog, Step 3)
// consumes it to (a) write per-(platform, market_type) raw dumps under
// backend/docs/raw-instruments/, and (b) filter canonical BTC/ETH/SOL perp
// down to config/instrument_catalog.yaml.
type CatalogResult struct {
	Platform  string       `json:"platform"`
	FetchedAt time.Time    `json:"fetched_at"`
	Markets   []MarketDump `json:"markets"`
}

// MarketDump is one (platform, market_type) snapshot. RawJSON is the
// pretty-printed, key-sorted bytes of the upstream response (so Git diff stays
// readable when MEXC contract_size etc. drifts).
type MarketDump struct {
	MarketType  string          `json:"market_type"`
	SourceURL   string          `json:"source_url"`
	RawJSON     json.RawMessage `json:"raw_json,omitempty"`
	Instruments []Instrument    `json:"instruments"`
}

// Instrument is the canonicalized projection over an upstream catalog row.
// Optional fields use omitempty to keep YAML / JSON tidy for platforms that
// do not need them.
type Instrument struct {
	APISymbol        string  `json:"api_symbol"`
	BaseAsset        string  `json:"base_asset"`
	QuoteAsset       string  `json:"quote_asset"`
	SettleAsset      string  `json:"settle_asset,omitempty"`
	Status           string  `json:"status,omitempty"`
	ContractType     string  `json:"contract_type,omitempty"`
	ContractID       string  `json:"contract_id,omitempty"`
	MarketID         int     `json:"market_id,omitempty"`
	ContractSize     float64 `json:"contract_size,omitempty"`
	QuantoMultiplier float64 `json:"quanto_multiplier,omitempty"`
}

// FetchInstruments dispatches to the per-platform implementation.
func (a RESTAdapter) FetchInstruments(ctx context.Context) (CatalogResult, error) {
	now := time.Now().UTC()
	switch a.Platform {
	case "binance":
		return a.fetchBinanceInstruments(ctx, now)
	case "okx":
		return a.fetchOKXInstruments(ctx, now)
	case "bybit":
		return a.fetchBybitInstruments(ctx, now)
	case "bitget":
		return a.fetchBitgetInstruments(ctx, now)
	case "bingx":
		return a.fetchBingXInstruments(ctx, now)
	case "mexc":
		return a.fetchMEXCInstruments(ctx, now)
	case "gate":
		return a.fetchGateInstruments(ctx, now)
	case "hyperliquid":
		return a.fetchHyperliquidInstruments(ctx, now)
	case "lighter":
		return a.fetchLighterInstruments(ctx, now)
	case "edgeX":
		return a.fetchEdgeXInstruments(ctx, now)
	default:
		return CatalogResult{}, fmt.Errorf("unknown platform %q", a.Platform)
	}
}

// ===== Binance =====

type binanceSymbolEntry struct {
	Symbol         string `json:"symbol"`
	Status         string `json:"status"`
	ContractStatus string `json:"contractStatus"`
	BaseAsset      string `json:"baseAsset"`
	QuoteAsset     string `json:"quoteAsset"`
	MarginAsset    string `json:"marginAsset"`
	ContractType   string `json:"contractType"`
}

func (a RESTAdapter) fetchBinanceInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "binance", FetchedAt: fetchedAt}
	type marketDef struct {
		marketType string
		url        string
	}
	for _, m := range []marketDef{
		{"spot", "https://api.binance.com/api/v3/exchangeInfo"},
		{"usd-m", "https://fapi.binance.com/fapi/v1/exchangeInfo"},
		{"coin-m", "https://dapi.binance.com/dapi/v1/exchangeInfo"},
	} {
		raw, err := a.fetchRaw(ctx, http.MethodGet, m.url, nil)
		if err != nil {
			return res, fmt.Errorf("binance %s: %w", m.marketType, err)
		}
		var body struct {
			Symbols []binanceSymbolEntry `json:"symbols"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return res, fmt.Errorf("binance %s decode: %w", m.marketType, err)
		}
		insts := make([]Instrument, 0, len(body.Symbols))
		for _, s := range body.Symbols {
			status := s.Status
			if status == "" {
				status = s.ContractStatus
			}
			settle := s.MarginAsset
			if settle == "" && m.marketType == "spot" {
				settle = s.QuoteAsset
			}
			insts = append(insts, Instrument{
				APISymbol:    s.Symbol,
				BaseAsset:    s.BaseAsset,
				QuoteAsset:   s.QuoteAsset,
				SettleAsset:  settle,
				Status:       status,
				ContractType: s.ContractType,
			})
		}
		res.Markets = append(res.Markets, MarketDump{
			MarketType:  m.marketType,
			SourceURL:   m.url,
			RawJSON:     prettyJSON(raw),
			Instruments: insts,
		})
	}
	return res, nil
}

// ===== OKX =====

func (a RESTAdapter) fetchOKXInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "okx", FetchedAt: fetchedAt}
	for _, instType := range []string{"SPOT", "SWAP", "FUTURES"} {
		url := "https://www.okx.com/api/v5/public/instruments?instType=" + instType
		raw, err := a.fetchRaw(ctx, http.MethodGet, url, nil)
		if err != nil {
			return res, fmt.Errorf("okx %s: %w", instType, err)
		}
		var body struct {
			Data []struct {
				InstID    string `json:"instId"`
				InstType  string `json:"instType"`
				BaseCcy   string `json:"baseCcy"`
				QuoteCcy  string `json:"quoteCcy"`
				SettleCcy string `json:"settleCcy"`
				State     string `json:"state"`
				CtType    string `json:"ctType"`
				CtValCcy  string `json:"ctValCcy"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return res, fmt.Errorf("okx %s decode: %w", instType, err)
		}
		marketType := mapLower(instType)
		insts := make([]Instrument, 0, len(body.Data))
		for _, d := range body.Data {
			base := d.BaseCcy
			quote := d.QuoteCcy
			if base == "" && d.CtValCcy != "" {
				base = d.CtValCcy
			}
			insts = append(insts, Instrument{
				APISymbol:    d.InstID,
				BaseAsset:    base,
				QuoteAsset:   quote,
				SettleAsset:  d.SettleCcy,
				Status:       d.State,
				ContractType: d.CtType,
			})
		}
		res.Markets = append(res.Markets, MarketDump{
			MarketType:  marketType,
			SourceURL:   url,
			RawJSON:     prettyJSON(raw),
			Instruments: insts,
		})
	}
	return res, nil
}

// ===== Bybit =====

func (a RESTAdapter) fetchBybitInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "bybit", FetchedAt: fetchedAt}
	for _, category := range []string{"spot", "linear", "inverse"} {
		insts, rawCombined, sourceURL, err := a.fetchBybitCategory(ctx, category)
		if err != nil {
			return res, fmt.Errorf("bybit %s: %w", category, err)
		}
		res.Markets = append(res.Markets, MarketDump{
			MarketType:  category,
			SourceURL:   sourceURL,
			RawJSON:     prettyJSON(rawCombined),
			Instruments: insts,
		})
	}
	return res, nil
}

func (a RESTAdapter) fetchBybitCategory(ctx context.Context, category string) ([]Instrument, []byte, string, error) {
	base := "https://api.bybit.com/v5/market/instruments-info"
	insts := make([]Instrument, 0, 256)
	pages := make([]json.RawMessage, 0, 4)
	cursor := ""
	sourceURL := base + "?category=" + category
	for page := 0; page < 10; page++ {
		url := sourceURL + "&limit=1000"
		if cursor != "" {
			url = url + "&cursor=" + cursor
		}
		raw, err := a.fetchRaw(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, nil, sourceURL, err
		}
		pages = append(pages, raw)
		var body struct {
			Result struct {
				List []struct {
					Symbol       string `json:"symbol"`
					Status       string `json:"status"`
					BaseCoin     string `json:"baseCoin"`
					QuoteCoin    string `json:"quoteCoin"`
					SettleCoin   string `json:"settleCoin"`
					ContractType string `json:"contractType"`
				} `json:"list"`
				NextPageCursor string `json:"nextPageCursor"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, nil, sourceURL, err
		}
		for _, it := range body.Result.List {
			insts = append(insts, Instrument{
				APISymbol:    it.Symbol,
				BaseAsset:    it.BaseCoin,
				QuoteAsset:   it.QuoteCoin,
				SettleAsset:  it.SettleCoin,
				Status:       it.Status,
				ContractType: it.ContractType,
			})
		}
		cursor = body.Result.NextPageCursor
		if cursor == "" {
			break
		}
	}
	combined, _ := json.Marshal(map[string]any{"category": category, "pages": pages})
	return insts, combined, sourceURL, nil
}

// ===== Bitget =====

func (a RESTAdapter) fetchBitgetInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "bitget", FetchedAt: fetchedAt}

	spotURL := "https://api.bitget.com/api/v2/spot/public/symbols"
	spotRaw, err := a.fetchRaw(ctx, http.MethodGet, spotURL, nil)
	if err != nil {
		return res, fmt.Errorf("bitget spot: %w", err)
	}
	var spotBody struct {
		Data []struct {
			Symbol    string `json:"symbol"`
			Status    string `json:"status"`
			BaseCoin  string `json:"baseCoin"`
			QuoteCoin string `json:"quoteCoin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(spotRaw, &spotBody); err != nil {
		return res, fmt.Errorf("bitget spot decode: %w", err)
	}
	spotInsts := make([]Instrument, 0, len(spotBody.Data))
	for _, d := range spotBody.Data {
		spotInsts = append(spotInsts, Instrument{
			APISymbol:  d.Symbol,
			BaseAsset:  d.BaseCoin,
			QuoteAsset: d.QuoteCoin,
			Status:     d.Status,
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "spot",
		SourceURL:   spotURL,
		RawJSON:     prettyJSON(spotRaw),
		Instruments: spotInsts,
	})

	for _, product := range []struct {
		market     string
		productKey string
	}{
		{"usdt-futures", "USDT-FUTURES"},
		{"coin-futures", "COIN-FUTURES"},
		{"usdc-futures", "USDC-FUTURES"},
	} {
		url := "https://api.bitget.com/api/v2/mix/market/contracts?productType=" + product.productKey
		raw, err := a.fetchRaw(ctx, http.MethodGet, url, nil)
		if err != nil {
			return res, fmt.Errorf("bitget %s: %w", product.market, err)
		}
		var body struct {
			Data []struct {
				Symbol       string `json:"symbol"`
				SymbolStatus string `json:"symbolStatus"`
				BaseCoin     string `json:"baseCoin"`
				QuoteCoin    string `json:"quoteCoin"`
				SettleCoin   string `json:"settleCoin"`
				IsRwa        string `json:"isRwa"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return res, fmt.Errorf("bitget %s decode: %w", product.market, err)
		}
		insts := make([]Instrument, 0, len(body.Data))
		for _, d := range body.Data {
			contractType := ""
			if d.IsRwa == "true" || d.IsRwa == "1" {
				contractType = "rwa"
			}
			insts = append(insts, Instrument{
				APISymbol:    d.Symbol,
				BaseAsset:    d.BaseCoin,
				QuoteAsset:   d.QuoteCoin,
				SettleAsset:  d.SettleCoin,
				Status:       d.SymbolStatus,
				ContractType: contractType,
			})
		}
		res.Markets = append(res.Markets, MarketDump{
			MarketType:  product.market,
			SourceURL:   url,
			RawJSON:     prettyJSON(raw),
			Instruments: insts,
		})
	}
	return res, nil
}

// ===== BingX =====

func (a RESTAdapter) fetchBingXInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "bingx", FetchedAt: fetchedAt}

	spotURL := "https://open-api.bingx.com/openApi/spot/v1/common/symbols"
	spotRaw, err := a.fetchRaw(ctx, http.MethodGet, spotURL, nil)
	if err != nil {
		return res, fmt.Errorf("bingx spot: %w", err)
	}
	var spotBody struct {
		Data struct {
			Symbols []struct {
				Symbol string `json:"symbol"`
				Status int    `json:"status"`
			} `json:"symbols"`
		} `json:"data"`
	}
	if err := json.Unmarshal(spotRaw, &spotBody); err != nil {
		return res, fmt.Errorf("bingx spot decode: %w", err)
	}
	spotInsts := make([]Instrument, 0, len(spotBody.Data.Symbols))
	for _, s := range spotBody.Data.Symbols {
		base, quote, ok := splitDashSymbol(s.Symbol)
		if !ok {
			continue
		}
		spotInsts = append(spotInsts, Instrument{
			APISymbol:  s.Symbol,
			BaseAsset:  base,
			QuoteAsset: quote,
			Status:     strconv.Itoa(s.Status),
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "spot",
		SourceURL:   spotURL,
		RawJSON:     prettyJSON(spotRaw),
		Instruments: spotInsts,
	})

	swapURL := "https://open-api.bingx.com/openApi/swap/v2/quote/contracts"
	swapRaw, err := a.fetchRaw(ctx, http.MethodGet, swapURL, nil)
	if err != nil {
		return res, fmt.Errorf("bingx swap: %w", err)
	}
	var swapBody struct {
		Data []struct {
			Symbol   string `json:"symbol"`
			Status   int    `json:"status"`
			Asset    string `json:"asset"`
			Currency string `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(swapRaw, &swapBody); err != nil {
		return res, fmt.Errorf("bingx swap decode: %w", err)
	}
	swapInsts := make([]Instrument, 0, len(swapBody.Data))
	for _, d := range swapBody.Data {
		base := d.Asset
		quote := d.Currency
		if base == "" || quote == "" {
			b, q, ok := splitDashSymbol(d.Symbol)
			if ok {
				if base == "" {
					base = b
				}
				if quote == "" {
					quote = q
				}
			}
		}
		swapInsts = append(swapInsts, Instrument{
			APISymbol:   d.Symbol,
			BaseAsset:   base,
			QuoteAsset:  quote,
			SettleAsset: quote,
			Status:      strconv.Itoa(d.Status),
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "swap",
		SourceURL:   swapURL,
		RawJSON:     prettyJSON(swapRaw),
		Instruments: swapInsts,
	})
	return res, nil
}

func splitDashSymbol(s string) (string, string, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// ===== MEXC =====

func (a RESTAdapter) fetchMEXCInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "mexc", FetchedAt: fetchedAt}

	spotURL := "https://api.mexc.com/api/v3/exchangeInfo"
	spotRaw, err := a.fetchRaw(ctx, http.MethodGet, spotURL, nil)
	if err != nil {
		return res, fmt.Errorf("mexc spot: %w", err)
	}
	var spotBody struct {
		Symbols []struct {
			Symbol     string `json:"symbol"`
			Status     string `json:"status"`
			BaseAsset  string `json:"baseAsset"`
			QuoteAsset string `json:"quoteAsset"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(spotRaw, &spotBody); err != nil {
		return res, fmt.Errorf("mexc spot decode: %w", err)
	}
	spotInsts := make([]Instrument, 0, len(spotBody.Symbols))
	for _, s := range spotBody.Symbols {
		spotInsts = append(spotInsts, Instrument{
			APISymbol:  s.Symbol,
			BaseAsset:  s.BaseAsset,
			QuoteAsset: s.QuoteAsset,
			Status:     s.Status,
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "spot",
		SourceURL:   spotURL,
		RawJSON:     prettyJSON(spotRaw),
		Instruments: spotInsts,
	})

	contractURL := "https://contract.mexc.com/api/v1/contract/detail"
	contractRaw, err := a.fetchRaw(ctx, http.MethodGet, contractURL, nil)
	if err != nil {
		return res, fmt.Errorf("mexc contract: %w", err)
	}
	var contractBody struct {
		Data []struct {
			Symbol       string  `json:"symbol"`
			State        int     `json:"state"`
			BaseCoin     string  `json:"baseCoin"`
			QuoteCoin    string  `json:"quoteCoin"`
			SettleCoin   string  `json:"settleCoin"`
			ContractSize float64 `json:"contractSize"`
			IsHidden     int     `json:"isHidden"`
		} `json:"data"`
	}
	if err := json.Unmarshal(contractRaw, &contractBody); err != nil {
		return res, fmt.Errorf("mexc contract decode: %w", err)
	}
	contractInsts := make([]Instrument, 0, len(contractBody.Data))
	for _, d := range contractBody.Data {
		contractInsts = append(contractInsts, Instrument{
			APISymbol:    d.Symbol,
			BaseAsset:    d.BaseCoin,
			QuoteAsset:   d.QuoteCoin,
			SettleAsset:  d.SettleCoin,
			Status:       strconv.Itoa(d.State),
			ContractSize: d.ContractSize,
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "contract",
		SourceURL:   contractURL,
		RawJSON:     prettyJSON(contractRaw),
		Instruments: contractInsts,
	})
	return res, nil
}

// ===== edgeX =====

func (a RESTAdapter) fetchEdgeXInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "edgeX", FetchedAt: fetchedAt}
	for _, m := range []struct {
		marketType string
		url        string
	}{
		{"perp-v1", "https://pro.edgex.exchange/api/v1/public/meta/getMetaData"},
		{"perp-v2", "https://edgex-prod-v2.edgex.exchange/api/v2/public/meta/getMetaData"},
		{"spot", "https://spot.edgex.exchange/api/v1/public/meta/getMetaData"},
	} {
		raw, err := a.fetchRaw(ctx, http.MethodGet, m.url, nil)
		if err != nil {
			res.Markets = append(res.Markets, MarketDump{
				MarketType: m.marketType,
				SourceURL:  m.url,
			})
			continue
		}
		insts := parseEdgeXMeta(raw)
		res.Markets = append(res.Markets, MarketDump{
			MarketType:  m.marketType,
			SourceURL:   m.url,
			RawJSON:     prettyJSON(raw),
			Instruments: insts,
		})
	}
	return res, nil
}

// parseEdgeXMeta best-effort extracts contract / instrument records from the
// edgeX meta payload. The schema varies subtly between V1/V2/spot, so we look
// for the common shape: data.{contractList|instrumentList|symbolList}[].{contractId|instrumentId|symbolId, contractName|symbolName, baseCurrency, quoteCurrency, ...}
func parseEdgeXMeta(raw []byte) []Instrument {
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	candidates := []string{"contractList", "instrumentList", "symbolList"}
	for _, key := range candidates {
		raw, ok := envelope.Data[key]
		if !ok {
			continue
		}
		var rows []map[string]any
		if err := json.Unmarshal(raw, &rows); err != nil {
			continue
		}
		insts := make([]Instrument, 0, len(rows))
		for _, row := range rows {
			inst := Instrument{
				APISymbol:   stringFromMap(row, "contractName", "symbolName", "instrumentName", "symbol"),
				BaseAsset:   stringFromMap(row, "baseCurrency", "baseAsset", "baseCoin"),
				QuoteAsset:  stringFromMap(row, "quoteCurrency", "quoteAsset", "quoteCoin"),
				SettleAsset: stringFromMap(row, "settleCurrency", "settleAsset", "settleCoin"),
				Status:      stringFromMap(row, "status", "state", "tradeStatus"),
				ContractID:  stringFromMap(row, "contractId", "instrumentId", "symbolId"),
			}
			if inst.APISymbol == "" && inst.ContractID == "" {
				continue
			}
			insts = append(insts, inst)
		}
		if len(insts) > 0 {
			return insts
		}
	}
	return nil
}

func stringFromMap(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				return x
			}
		case float64:
			if x != 0 {
				return strconv.FormatFloat(x, 'f', -1, 64)
			}
		}
	}
	return ""
}

func mapLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out[i] = c
	}
	return string(out)
}

// ===== Gate =====

func (a RESTAdapter) fetchGateInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "gate", FetchedAt: fetchedAt}

	spotURL := "https://api.gateio.ws/api/v4/spot/currency_pairs"
	spotRaw, err := a.fetchRaw(ctx, http.MethodGet, spotURL, nil)
	if err != nil {
		return res, fmt.Errorf("gate spot: %w", err)
	}
	var spotItems []struct {
		ID          string `json:"id"`
		Base        string `json:"base"`
		Quote       string `json:"quote"`
		TradeStatus string `json:"trade_status"`
	}
	if err := json.Unmarshal(spotRaw, &spotItems); err != nil {
		return res, fmt.Errorf("gate spot decode: %w", err)
	}
	spotInsts := make([]Instrument, 0, len(spotItems))
	for _, it := range spotItems {
		spotInsts = append(spotInsts, Instrument{
			APISymbol:  it.ID,
			BaseAsset:  it.Base,
			QuoteAsset: it.Quote,
			Status:     it.TradeStatus,
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "spot",
		SourceURL:   spotURL,
		RawJSON:     prettyJSON(spotRaw),
		Instruments: spotInsts,
	})

	futURL := "https://api.gateio.ws/api/v4/futures/usdt/contracts"
	futRaw, err := a.fetchRaw(ctx, http.MethodGet, futURL, nil)
	if err != nil {
		return res, fmt.Errorf("gate futures: %w", err)
	}
	var futItems []struct {
		Name             string `json:"name"`
		InDelisting      bool   `json:"in_delisting"`
		Type             string `json:"type"`
		QuantoMultiplier string `json:"quanto_multiplier"`
	}
	if err := json.Unmarshal(futRaw, &futItems); err != nil {
		return res, fmt.Errorf("gate futures decode: %w", err)
	}
	futInsts := make([]Instrument, 0, len(futItems))
	for _, it := range futItems {
		base, quote, ok := splitGateContract(it.Name)
		if !ok {
			continue
		}
		mult, _ := strconv.ParseFloat(it.QuantoMultiplier, 64)
		status := "trading"
		if it.InDelisting {
			status = "delisting"
		}
		futInsts = append(futInsts, Instrument{
			APISymbol:        it.Name,
			BaseAsset:        base,
			QuoteAsset:       quote,
			SettleAsset:      "USDT",
			Status:           status,
			ContractType:     it.Type,
			QuantoMultiplier: mult,
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "futures-usdt",
		SourceURL:   futURL,
		RawJSON:     prettyJSON(futRaw),
		Instruments: futInsts,
	})
	return res, nil
}

func splitGateContract(name string) (string, string, bool) {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '_' {
			return name[:i], name[i+1:], true
		}
	}
	return "", "", false
}

// ===== Lighter =====

func (a RESTAdapter) fetchLighterInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "lighter", FetchedAt: fetchedAt}
	url := "https://mainnet.zklighter.elliot.ai/api/v1/orderBookDetails?filter=all"
	raw, err := a.fetchRaw(ctx, http.MethodGet, url, nil)
	if err != nil {
		return res, fmt.Errorf("lighter: %w", err)
	}
	var body struct {
		Details []struct {
			Symbol     string `json:"symbol"`
			MarketID   int    `json:"market_id"`
			MarketType string `json:"market_type"`
			Status     string `json:"status"`
		} `json:"order_book_details"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return res, fmt.Errorf("lighter decode: %w", err)
	}
	perp := make([]Instrument, 0)
	spot := make([]Instrument, 0)
	for _, d := range body.Details {
		inst := Instrument{
			APISymbol:  d.Symbol,
			BaseAsset:  d.Symbol,
			QuoteAsset: "USDC",
			Status:     d.Status,
			MarketID:   d.MarketID,
		}
		switch d.MarketType {
		case "perp", "":
			perp = append(perp, inst)
		case "spot":
			spot = append(spot, inst)
		default:
			perp = append(perp, inst)
		}
	}
	pretty := prettyJSON(raw)
	res.Markets = append(res.Markets,
		MarketDump{MarketType: "perp", SourceURL: url, RawJSON: pretty, Instruments: perp},
		MarketDump{MarketType: "spot", SourceURL: url, RawJSON: pretty, Instruments: spot},
	)
	return res, nil
}

// ===== Hyperliquid =====

func (a RESTAdapter) fetchHyperliquidInstruments(ctx context.Context, fetchedAt time.Time) (CatalogResult, error) {
	res := CatalogResult{Platform: "hyperliquid", FetchedAt: fetchedAt}

	perpURL := "https://api.hyperliquid.xyz/info"
	perpBody, _ := json.Marshal(map[string]any{"type": "metaAndAssetCtxs"})
	perpRaw, err := a.fetchRaw(ctx, http.MethodPost, perpURL, perpBody)
	if err != nil {
		return res, fmt.Errorf("hyperliquid perp: %w", err)
	}
	var perpResp []json.RawMessage
	if err := json.Unmarshal(perpRaw, &perpResp); err != nil {
		return res, fmt.Errorf("hyperliquid perp decode: %w", err)
	}
	var meta struct {
		Universe []struct {
			Name       string `json:"name"`
			SzDecimals int    `json:"szDecimals"`
			IsDelisted bool   `json:"isDelisted"`
		} `json:"universe"`
	}
	if len(perpResp) > 0 {
		if err := json.Unmarshal(perpResp[0], &meta); err != nil {
			return res, fmt.Errorf("hyperliquid universe decode: %w", err)
		}
	}
	perpInsts := make([]Instrument, 0, len(meta.Universe))
	for _, u := range meta.Universe {
		status := "live"
		if u.IsDelisted {
			status = "delisted"
		}
		perpInsts = append(perpInsts, Instrument{
			APISymbol:   u.Name,
			BaseAsset:   u.Name,
			QuoteAsset:  "USDC",
			SettleAsset: "USDC",
			Status:      status,
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "perp",
		SourceURL:   perpURL,
		RawJSON:     prettyJSON(perpRaw),
		Instruments: perpInsts,
	})

	spotBody, _ := json.Marshal(map[string]any{"type": "spotMeta"})
	spotRaw, err := a.fetchRaw(ctx, http.MethodPost, perpURL, spotBody)
	if err != nil {
		return res, fmt.Errorf("hyperliquid spot: %w", err)
	}
	var spotMeta struct {
		Tokens []struct {
			Name        string `json:"name"`
			SzDecimals  int    `json:"szDecimals"`
			WeiDecimals int    `json:"weiDecimals"`
			Index       int    `json:"index"`
		} `json:"tokens"`
		Universe []struct {
			Name   string `json:"name"`
			Tokens []int  `json:"tokens"`
			Index  int    `json:"index"`
		} `json:"universe"`
	}
	if err := json.Unmarshal(spotRaw, &spotMeta); err != nil {
		return res, fmt.Errorf("hyperliquid spotMeta decode: %w", err)
	}
	tokenByIdx := make(map[int]string, len(spotMeta.Tokens))
	for _, t := range spotMeta.Tokens {
		tokenByIdx[t.Index] = t.Name
	}
	spotInsts := make([]Instrument, 0, len(spotMeta.Universe))
	for _, p := range spotMeta.Universe {
		if len(p.Tokens) < 2 {
			continue
		}
		base, baseOK := tokenByIdx[p.Tokens[0]]
		quote, quoteOK := tokenByIdx[p.Tokens[1]]
		if !baseOK || !quoteOK {
			continue
		}
		spotInsts = append(spotInsts, Instrument{
			APISymbol:  p.Name,
			BaseAsset:  base,
			QuoteAsset: quote,
			Status:     "live",
		})
	}
	res.Markets = append(res.Markets, MarketDump{
		MarketType:  "spot",
		SourceURL:   perpURL,
		RawJSON:     prettyJSON(spotRaw),
		Instruments: spotInsts,
	})
	return res, nil
}

// ===== shared helpers =====

func (a RESTAdapter) fetchRaw(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	attempts := a.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, status, err := a.doJSONRequest(ctx, method, url, body)
		if err == nil && status >= 200 && status < 300 {
			return data, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("http %d", status)
		}
		if attempt == attempts || !shouldRetry(status, err) {
			return nil, lastErr
		}
		if err := sleepWithContext(ctx, retryBackoff(attempt)); err != nil {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("fetchRaw: unknown error")
	}
	return nil, lastErr
}

// prettyJSON returns the input bytes re-encoded as pretty-printed JSON with
// recursively sorted map keys, so committed raw dumps produce stable diffs
// across crawler runs.
func prettyJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	v = sortMaps(v)
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return out
}

func sortMaps(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = sortMaps(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = sortMaps(item)
		}
		return out
	default:
		return v
	}
}
