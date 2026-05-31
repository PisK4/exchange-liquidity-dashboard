package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"edgex-dashboard/backend/internal/listing/instrument"
)

// HyperliquidInfoURL is the JSON-RPC-style POST endpoint that
// services all read-only Hyperliquid queries. The fetcher always
// posts the `{"type":"meta"}` body to retrieve the perpetual
// universe.
const HyperliquidInfoURL = "https://api.hyperliquid.xyz/info"

// hyperliquidMetaBody is the canonical POST body. Defined as a
// package-level const so test + production share one source of truth.
const hyperliquidMetaBody = `{"type":"meta"}`

// FetchHyperliquidPerp builds the InstrumentSource.Fetch closure
// for Hyperliquid perpetual contracts. Unlike the other CEX
// endpoints this one is JSON-RPC-style: a single POST to /info
// with body {"type":"meta"} returns {"universe":[...]}. Each entry
// in the universe array is one perpetual asset.
//
// Hyperliquid does not use a code/msg envelope — HTTP status is
// the authoritative success signal, and the only failure mode
// observed in the wild is a malformed body which surfaces as a
// JSON decode error from the envelope step.
func FetchHyperliquidPerp(deps HTTPDeps, url string) func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
	if url == "" {
		url = HyperliquidInfoURL
	}
	body := []byte(hyperliquidMetaBody)
	return func(ctx context.Context) ([]instrument.NormalizedInstrument, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodPost, url, body)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Universe []json.RawMessage `json:"universe"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("hyperliquid perp: decode envelope: %w", err)
		}
		out := make([]instrument.NormalizedInstrument, 0, len(envelope.Universe))
		for _, row := range envelope.Universe {
			n, err := instrument.NormalizeHyperliquidPerp(row)
			if err != nil {
				continue
			}
			out = append(out, n)
		}
		return out, nil
	}
}
