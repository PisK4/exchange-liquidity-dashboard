package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// OKXPublicInstrumentsURL is the public-instruments endpoint. The
// instType query is appended at fetch time so the same base URL
// can host both SWAP polling and (in future phases) SPOT / FUTURES
// pollers.
const OKXPublicInstrumentsURL = "https://www.okx.com/api/v5/public/instruments"

// FetchOKXSwap builds the InstrumentSource.Fetch closure for OKX
// linear perpetual swaps. The closure GETs
// /api/v5/public/instruments?instType=SWAP, unwraps the code/data
// envelope, and feeds each row to instrument.NormalizeOKXSwap.
//
// OKX uses string-valued status codes (code="0" → success). A
// non-"0" code is mapped to an error so source-health can classify
// it transient.
func FetchOKXSwap(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = OKXPublicInstrumentsURL
	}
	url := baseURL
	if !strings.Contains(url, "?") {
		url += "?instType=SWAP"
	} else if !strings.Contains(url, "instType=") {
		url += "&instType=SWAP"
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Code string            `json:"code"`
			Msg  string            `json:"msg"`
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("okx swap: decode envelope: %w", err)
		}
		if envelope.Code != "" && envelope.Code != "0" {
			return nil, fmt.Errorf("okx swap: code=%s msg=%q", envelope.Code, envelope.Msg)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(envelope.Data))
		for _, row := range envelope.Data {
			n, err := instrument.NormalizeOKXSwap(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
