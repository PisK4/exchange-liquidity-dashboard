package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HyperliquidEntriesURL is the public changelog feed used by the
// Hyperliquid app. Listing Agent consumes it as an announcement source
// for explicit listing entries; the /info meta endpoint remains the
// instrument_diff source of truth for market existence.
const HyperliquidEntriesURL = "https://dzjnlsk4rxci0.cloudfront.net/mainnet/entries.json"

// FetchHyperliquidAnnouncements builds the AnnouncementSource.Fetch
// closure for Hyperliquid's entries feed. The closure GETs the JSON
// payload, unwraps the top-level entries array, and preserves each row
// as a parser-ready object with sourceUrl/sourceModule audit fields.
func FetchHyperliquidAnnouncements(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]json.RawMessage, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = HyperliquidEntriesURL
	}
	return func(ctx context.Context) ([]json.RawMessage, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, baseURL, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("hyperliquid announcements: decode envelope: %w", err)
		}
		out := make([]json.RawMessage, 0, len(envelope.Entries))
		for _, item := range envelope.Entries {
			item["sourceUrl"] = baseURL
			item["sourceModule"] = "hyperliquid_entries"
			encoded, err := json.Marshal(item)
			if err != nil {
				continue
			}
			out = append(out, encoded)
		}
		return out, nil
	}
}
