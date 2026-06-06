package fetcher

import (
	"context"
	"net/http"
	"strings"

	"edgex-ops-intelligence/backend/internal/listing/instrument"
)

// EdgeX public getMetaData endpoints. The same JSON shape is used by
// all three surfaces; only the host differs. The current production
// hosts are sourced from build-catalog so they stay aligned with the
// canonical raw-instruments dump location.
const (
	EdgeXPerpV1MetaURL = "https://pro.edgex.exchange/api/v1/public/meta/getMetaData"
	EdgeXPerpV2MetaURL = "https://edgex-prod-v2.edgex.exchange/api/v2/public/meta/getMetaData"
	EdgeXSpotMetaURL   = "https://spot.edgex.exchange/api/v1/public/meta/getMetaData"
)

// FetchEdgeXPerpV1 returns the InstrumentSource.Fetch closure for the
// edgeX v1 perp surface. The closure GETs getMetaData, unwraps the
// data envelope, and feeds each contractList entry to
// instrument.NormalizeEdgeXContract via the shared
// ParseEdgeXMetaPayload helper.
func FetchEdgeXPerpV1(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	return fetchEdgeXMeta(deps, baseURL, EdgeXPerpV1MetaURL, "perp_v1")
}

// FetchEdgeXPerpV2 — see FetchEdgeXPerpV1.
func FetchEdgeXPerpV2(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	return fetchEdgeXMeta(deps, baseURL, EdgeXPerpV2MetaURL, "perp_v2")
}

// FetchEdgeXSpot — see FetchEdgeXPerpV1.
func FetchEdgeXSpot(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	return fetchEdgeXMeta(deps, baseURL, EdgeXSpotMetaURL, "spot")
}

// fetchEdgeXMeta consolidates the three surfaces into a single
// closure factory. marketType is captured by the closure so the
// normalizer stamps the correct (perp_v1 / perp_v2 / spot) value on
// every snapshot row.
func fetchEdgeXMeta(deps HTTPDeps, baseURL, defaultURL, marketType string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = defaultURL
	}
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		body, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		return instrument.ParseEdgeXMetaPayload(body, marketType)
	}
}
