package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"edgex-dashboard/backend/internal/listing/instrument"
)

// Gate public endpoints. Both return a top-level JSON array; there
// is no envelope, so HTTP 200 + non-array body counts as schema
// drift.
const (
	GateSpotCurrencyPairsURL    = "https://api.gateio.ws/api/v4/spot/currency_pairs"
	GateUSDTFuturesContractsURL = "https://api.gateio.ws/api/v4/futures/usdt/contracts"
)

// FetchGateSpot returns the InstrumentSource.Fetch closure for the
// /spot/currency_pairs endpoint.
func FetchGateSpot(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = GateSpotCurrencyPairsURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		body, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("gate spot: decode array: %w", err)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(rows))
		for _, row := range rows {
			n, err := instrument.NormalizeGateSpotPair(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}

// FetchGateUSDTFutures returns the InstrumentSource.Fetch closure for
// /api/v4/futures/usdt/contracts. quanto_multiplier reaches the
// CatalogResolver via the raw_json column on the snapshot row.
func FetchGateUSDTFutures(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = GateUSDTFuturesContractsURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		body, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("gate usdt futures: decode array: %w", err)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(rows))
		for _, row := range rows {
			n, err := instrument.NormalizeGateFuturesContract(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
