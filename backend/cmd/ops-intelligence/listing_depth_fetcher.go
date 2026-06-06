package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/listing"
)

// buildBinanceDepthFetcher returns a listing.PlatformDepthFetcher
// that queries Binance USD-margined endpoints for an unlisted
// canonical's spot + perp depth at the 2% tier.
//
// Operator decision: only one platform is wired today. Binance has
// the largest cross-asset coverage in the new-listing window and
// its API symbol naming is the simplest to synthesise without an
// instrument-catalog row ({CANONICAL}USDT for both surfaces). When
// Binance answers "invalid symbol" or returns an empty book the
// renderer surfaces "不可用" — we intentionally do not fall back to
// a second exchange so the card stays honest about what was actually
// checked.
//
// The fetcher dials Binance directly rather than going through
// internal/adapter because adapter.fetchBinance is hardcoded to the
// USDM perp endpoint; spot would need a parallel branch there. A
// minimal inline HTTP path keeps the binance-specific naming /
// endpoint knowledge confined to this single file.
func buildBinanceDepthFetcher(proxyURL string, perCallTimeout time.Duration) listing.PlatformDepthFetcher {
	if perCallTimeout <= 0 {
		perCallTimeout = 1500 * time.Millisecond
	}
	client := newBinanceDepthHTTPClient(proxyURL, perCallTimeout)
	const (
		// 0.1% tier — operator chose this over the 2% Top30
		// default because newly listed perps often have a thick
		// "headline" book but sparse top-of-book quotes. 0.1%
		// reflects what an immediately-executable position can
		// actually clear without slipping more than 10 bps, which
		// is the most decision-relevant signal for the listing
		// agent.
		tier      = 0.001
		tierLabel = "0.1pct"
	)
	return func(ctx context.Context, platform, canonical string, kind listing.DepthMarketKind) (float64, string, error) {
		if !strings.EqualFold(platform, "binance") {
			return 0, "", listing.ErrDepthUnavailable
		}
		base := strings.ToUpper(strings.TrimSpace(canonical))
		if base == "" {
			return 0, "", listing.ErrDepthUnavailable
		}
		apiSymbol := base + "USDT"
		bids, asks, err := fetchBinanceDepth(ctx, client, kind, apiSymbol)
		if err != nil {
			if errors.Is(err, listing.ErrDepthUnavailable) {
				return 0, "", listing.ErrDepthUnavailable
			}
			return 0, "", err
		}
		if len(bids) == 0 || len(asks) == 0 {
			return 0, "", listing.ErrDepthUnavailable
		}
		usd, ok := tierDepthUSD(bids, asks, tier)
		if !ok || usd <= 0 {
			return 0, "", listing.ErrDepthUnavailable
		}
		return usd, tierLabel, nil
	}
}

func newBinanceDepthHTTPClient(proxyURL string, timeout time.Duration) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return &http.Client{Timeout: timeout}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(parsed)
	return &http.Client{Transport: tr, Timeout: timeout}
}

// fetchBinanceDepth hits the spot or perp depth endpoint and returns
// raw price/size level slices. Translates the well-known "invalid
// symbol" payload into listing.ErrDepthUnavailable so the aggregator
// silently drops the row instead of recording it as an error — that
// is the expected response for the majority of pre-listing
// candidates.
func fetchBinanceDepth(ctx context.Context, client *http.Client, kind listing.DepthMarketKind, apiSymbol string) ([][2]float64, [][2]float64, error) {
	endpoint := "https://api.binance.com/api/v3/depth?limit=500&symbol=" + apiSymbol
	if kind == listing.DepthKindPerp {
		endpoint = "https://fapi.binance.com/fapi/v1/depth?limit=500&symbol=" + apiSymbol
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusBadRequest {
		// {"code":-1121,"msg":"Invalid symbol."} — unlisted asset.
		return nil, nil, listing.ErrDepthUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("binance depth %s: status=%d body=%s", apiSymbol, resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Bids [][]json.Number `json:"bids"`
		Asks [][]json.Number `json:"asks"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, err
	}
	if parsed.Code != 0 {
		return nil, nil, listing.ErrDepthUnavailable
	}
	return parseBinanceLevels(parsed.Bids), parseBinanceLevels(parsed.Asks), nil
}

func parseBinanceLevels(raw [][]json.Number) [][2]float64 {
	out := make([][2]float64, 0, len(raw))
	for _, lvl := range raw {
		if len(lvl) < 2 {
			continue
		}
		price, perr := strconv.ParseFloat(lvl[0].String(), 64)
		size, serr := strconv.ParseFloat(lvl[1].String(), 64)
		if perr != nil || serr != nil || price <= 0 || size <= 0 {
			continue
		}
		out = append(out, [2]float64{price, size})
	}
	return out
}

// tierDepthUSD totals bid + ask USD value within `tier` (e.g. 0.02
// for 2%) of the mid price. Mid is the average of the best bid and
// best ask; the function returns ok=false when either side is empty.
func tierDepthUSD(bids, asks [][2]float64, tier float64) (float64, bool) {
	if len(bids) == 0 || len(asks) == 0 {
		return 0, false
	}
	bestBid := bids[0][0]
	bestAsk := asks[0][0]
	for _, b := range bids {
		if b[0] > bestBid {
			bestBid = b[0]
		}
	}
	for _, a := range asks {
		if a[0] < bestAsk || bestAsk == 0 {
			bestAsk = a[0]
		}
	}
	mid := (bestBid + bestAsk) / 2
	if mid <= 0 {
		return 0, false
	}
	bidFloor := mid * (1 - tier)
	askCeil := mid * (1 + tier)
	usd := 0.0
	for _, b := range bids {
		if b[0] >= bidFloor {
			usd += b[0] * b[1]
		}
	}
	for _, a := range asks {
		if a[0] <= askCeil {
			usd += a[0] * a[1]
		}
	}
	return usd, true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
