package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// BitgetMixContractsURL is the v2 mix contracts endpoint shared by
// every productType (USDT-FUTURES, USDC-FUTURES, COIN-FUTURES).
// productType is appended at fetch time.
const BitgetMixContractsURL = "https://api.bitget.com/api/v2/mix/market/contracts"

// FetchBitgetUSDTFutures builds the InstrumentSource.Fetch closure
// for Bitget's USDT-margined futures catalog. The closure GETs
// /api/v2/mix/market/contracts?productType=USDT-FUTURES, unwraps
// the code/data envelope, and feeds each row to
// instrument.NormalizeBitgetUSDTFutures.
//
// Bitget v2 uses code="00000" for success; anything else maps to an
// error so PollWithSourceHealth backs off on the next tick.
func FetchBitgetUSDTFutures(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BitgetMixContractsURL
	}
	url := baseURL
	if !strings.Contains(url, "?") {
		url += "?productType=USDT-FUTURES"
	} else if !strings.Contains(url, "productType=") {
		url += "&productType=USDT-FUTURES"
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
			return nil, fmt.Errorf("bitget usdt-futures: decode envelope: %w", err)
		}
		if envelope.Code != "" && envelope.Code != "00000" {
			return nil, fmt.Errorf("bitget usdt-futures: code=%s msg=%q", envelope.Code, envelope.Msg)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(envelope.Data))
		var normalizeErrs []string
		for _, row := range envelope.Data {
			n, err := instrument.NormalizeBitgetUSDTFutures(row)
			if err != nil {
				if len(normalizeErrs) < 3 {
					normalizeErrs = append(normalizeErrs, err.Error())
				}
				continue
			}
			out = append(out, n)
		}
		if len(envelope.Data) > 0 && len(out) == 0 {
			return nil, &instrument.SchemaDriftError{Platform: "bitget", Message: fmt.Sprintf("all %d usdt-futures rows failed normalization: %s", len(envelope.Data), strings.Join(normalizeErrs, "; "))}
		}
		return out, nil
	}
}
