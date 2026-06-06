package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// MEXCContractDetailURL is the v1 contract details endpoint.
const MEXCContractDetailURL = "https://contract.mexc.com/api/v1/contract/detail"

// FetchMEXCContract builds the InstrumentSource.Fetch closure for
// MEXC perpetual contracts. The closure GETs
// /api/v1/contract/detail, unwraps the success/code/data envelope,
// and feeds each row to instrument.NormalizeMEXCContract.
//
// MEXC uses code=0 for success and a "success" boolean field;
// we treat any non-zero code as transient. The list endpoint
// returns the full catalog in one response (no pagination).
func FetchMEXCContract(deps HTTPDeps, url string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	if url == "" {
		url = MEXCContractDetailURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Success bool              `json:"success"`
			Code    int               `json:"code"`
			Message string            `json:"message"`
			Data    []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("mexc contract: decode envelope: %w", err)
		}
		if envelope.Code != 0 {
			return nil, fmt.Errorf("mexc contract: code=%d message=%q", envelope.Code, envelope.Message)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(envelope.Data))
		for _, row := range envelope.Data {
			n, err := instrument.NormalizeMEXCContract(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
