package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// BinanceUSDMExchangeInfoURL is the canonical USD-M futures
// exchangeInfo endpoint. Exported so cmd/ops-intelligence wiring can stamp
// the same URL into both the InstrumentSource and the source-state
// audit row.
const BinanceUSDMExchangeInfoURL = "https://fapi.binance.com/fapi/v1/exchangeInfo"

// FetchBinanceUSDM builds the listing-engine InstrumentSource.Fetch
// closure for Binance USD-M futures. The closure GETs the
// exchangeInfo endpoint once, splits the top-level "symbols" array
// into per-row json.RawMessage values, and hands each one to
// instrument.NormalizeBinanceUSDM. Per-row normalize errors are
// counted toward a schema-drift accumulator but do NOT abort the
// fetch — a single drifted row would otherwise blackhole the entire
// CEX's catalog every tick.
func FetchBinanceUSDM(deps HTTPDeps, url string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	if url == "" {
		url = BinanceUSDMExchangeInfoURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Symbols []json.RawMessage `json:"symbols"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("binance usdm: decode envelope: %w", err)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(envelope.Symbols))
		for _, row := range envelope.Symbols {
			n, err := instrument.NormalizeBinanceUSDM(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
