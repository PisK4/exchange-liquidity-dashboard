package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// BybitLinearInstrumentsURL is the v5 instruments-info endpoint used
// for linear (USDT-margined perpetual) contracts. The category query
// parameter is appended at fetch time so production wiring can stamp
// just the base URL into config.
const BybitLinearInstrumentsURL = "https://api.bybit.com/v5/market/instruments-info"

// FetchBybitLinear builds the InstrumentSource.Fetch closure for
// Bybit's linear (USDT-perpetual) catalog. The closure GETs
// /v5/market/instruments-info?category=linear, unwraps the
// retCode/result envelope, and feeds each result.list entry to
// instrument.NormalizeBybitLinear.
//
// Bybit's v5 transport convention is HTTP 200 + retCode != 0 for
// errors; a non-zero retCode is therefore surfaced as an error so
// PollWithSourceHealth can classify it transient and back off the
// next tick. Per-row normalize errors are swallowed for the same
// reason as Binance USDM (see binance_usdm.go).
func FetchBybitLinear(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BybitLinearInstrumentsURL
	}
	url := baseURL
	if !strings.Contains(url, "?") {
		url += "?category=linear&limit=1000"
	} else if !strings.Contains(url, "category=") {
		url += "&category=linear&limit=1000"
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			RetCode int    `json:"retCode"`
			RetMsg  string `json:"retMsg"`
			Result  struct {
				List []json.RawMessage `json:"list"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("bybit linear: decode envelope: %w", err)
		}
		if envelope.RetCode != 0 {
			return nil, fmt.Errorf("bybit linear: retCode=%d retMsg=%q", envelope.RetCode, envelope.RetMsg)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(envelope.Result.List))
		for _, row := range envelope.Result.List {
			n, err := instrument.NormalizeBybitLinear(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
