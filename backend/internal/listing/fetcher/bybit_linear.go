package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// BybitLinearInstrumentsURL is the v5 instruments-info endpoint used
// for linear (USDT-margined perpetual) contracts. The category query
// parameter is appended at fetch time so production wiring can stamp
// just the base URL into config.
const BybitLinearInstrumentsURL = "https://api.bybit.com/v5/market/instruments-info"

// FetchBybitLinear builds the InstrumentSource.Fetch closure for
// Bybit's linear (USDT-perpetual) catalog. The closure GETs Trading
// and PreLaunch instruments separately, unwraps the retCode/result
// envelope, and feeds each result.list entry to
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
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		statuses := []string{"Trading", "PreLaunch"}
		seen := make(map[string]struct{})
		out := make([]instrument.NormalizedInstrument, 0, 768)
		rawRows := 0
		var normalizeErrs []string

		for _, status := range statuses {
			cursor := ""
			for {
				reqURL, err := bybitLinearRequestURL(baseURL, status, cursor)
				if err != nil {
					return nil, err
				}
				rows, nextCursor, err := fetchBybitLinearRows(ctx, deps, reqURL)
				if err != nil {
					return nil, err
				}
				rawRows += len(rows)
				for _, row := range rows {
					n, err := instrument.NormalizeBybitLinear(row)
					if err != nil {
						if len(normalizeErrs) < 3 {
							normalizeErrs = append(normalizeErrs, err.Error())
						}
						continue
					}
					if _, ok := seen[n.APISymbol]; ok {
						continue
					}
					seen[n.APISymbol] = struct{}{}
					out = append(out, n)
				}
				if strings.TrimSpace(nextCursor) == "" {
					break
				}
				cursor = nextCursor
			}
		}
		if rawRows > 0 && len(out) == 0 {
			return nil, &instrument.SchemaDriftError{Platform: "bybit", Message: fmt.Sprintf("all %d linear rows failed normalization: %s", rawRows, strings.Join(normalizeErrs, "; "))}
		}
		return out, nil
	}
}

func bybitLinearRequestURL(baseURL, status, cursor string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("bybit linear: parse base url: %w", err)
	}
	q := u.Query()
	if q.Get("category") == "" {
		q.Set("category", "linear")
	}
	if q.Get("limit") == "" {
		q.Set("limit", "1000")
	}
	if strings.TrimSpace(status) != "" {
		q.Set("status", status)
	}
	if strings.TrimSpace(cursor) != "" {
		q.Set("cursor", cursor)
	} else {
		q.Del("cursor")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func fetchBybitLinearRows(ctx context.Context, deps HTTPDeps, reqURL string) ([]json.RawMessage, string, error) {
	raw, err := deps.fetchJSON(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	var envelope struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List           []json.RawMessage `json:"list"`
			NextPageCursor string            `json:"nextPageCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, "", fmt.Errorf("bybit linear: decode envelope: %w", err)
	}
	if envelope.RetCode != 0 {
		return nil, "", fmt.Errorf("bybit linear: retCode=%d retMsg=%q", envelope.RetCode, envelope.RetMsg)
	}
	return envelope.Result.List, envelope.Result.NextPageCursor, nil
}
