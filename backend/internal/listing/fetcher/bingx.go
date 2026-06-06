package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"edgex-dashboard/backend/internal/listing/instrument"
)

// BingX public endpoints. The swap endpoint is the same one used by
// build-catalog so production traffic patterns remain unchanged.
const (
	BingXSpotSymbolsURL   = "https://open-api.bingx.com/openApi/spot/v1/common/symbols"
	BingXSwapContractsURL = "https://open-api.bingx.com/openApi/swap/v2/quote/contracts"
)

type bingxSpotEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Symbols []json.RawMessage `json:"symbols"`
	} `json:"data"`
}

type bingxSwapEnvelope struct {
	Code int               `json:"code"`
	Msg  string            `json:"msg"`
	Data []json.RawMessage `json:"data"`
}

// FetchBingXSpot returns the InstrumentSource.Fetch closure for the
// /openApi/spot/v1/common/symbols endpoint. BingX uses code==0 for
// success; anything else is surfaced so PollWithSourceHealth can
// classify it as transient.
func FetchBingXSpot(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = BingXSpotSymbolsURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		body, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var env bingxSpotEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("bingx spot: decode envelope: %w", err)
		}
		if env.Code != 0 {
			return nil, fmt.Errorf("bingx spot: code=%d msg=%q", env.Code, env.Msg)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(env.Data.Symbols))
		for _, row := range env.Data.Symbols {
			n, err := instrument.NormalizeBingXSpotSymbol(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}

// FetchBingXSwap returns the InstrumentSource.Fetch closure for the
// /openApi/swap/v2/quote/contracts endpoint. The synthetic-token
// tagging happens inside the normalizer so the closure here stays
// transport-only.
func FetchBingXSwap(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = BingXSwapContractsURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		body, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var env bingxSwapEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("bingx swap: decode envelope: %w", err)
		}
		if env.Code != 0 {
			return nil, fmt.Errorf("bingx swap: code=%d msg=%q", env.Code, env.Msg)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(env.Data))
		for _, row := range env.Data {
			n, err := instrument.NormalizeBingXSwapSymbol(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
