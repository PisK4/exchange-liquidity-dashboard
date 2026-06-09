package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// HyperliquidEntriesURL is the public changelog feed used by the
// Hyperliquid app. Listing Agent consumes it as an announcement source
// for explicit listing entries; the /info meta endpoint remains the
// instrument_diff source of truth for market existence.
const HyperliquidEntriesURL = "https://dzjnlsk4rxci0.cloudfront.net/mainnet/entries.json"

const hyperliquidAnnouncementMaxAttempts = 2

var hyperliquidAnnouncementRetryDelay = 100 * time.Millisecond

// FetchHyperliquidAnnouncements builds the AnnouncementSource.Fetch
// closure for Hyperliquid's entries feed. The closure GETs the JSON
// payload, unwraps the top-level entries array, and preserves each row
// as a parser-ready object with sourceUrl/sourceModule audit fields.
func FetchHyperliquidAnnouncements(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]json.RawMessage, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = HyperliquidEntriesURL
	}
	return func(ctx context.Context) ([]json.RawMessage, error) {
		var lastErr error
		for attempt := 1; attempt <= hyperliquidAnnouncementMaxAttempts; attempt++ {
			raw, err := deps.fetchJSON(ctx, http.MethodGet, baseURL, nil)
			if err != nil {
				lastErr = fmt.Errorf("hyperliquid announcements: fetch entries url=%s attempt=%d/%d: %w", baseURL, attempt, hyperliquidAnnouncementMaxAttempts, err)
				if !shouldRetryHyperliquidAnnouncement(err) || attempt == hyperliquidAnnouncementMaxAttempts {
					return nil, lastErr
				}
				if err := sleepHyperliquidAnnouncementRetry(ctx); err != nil {
					return nil, err
				}
				continue
			}
			var envelope struct {
				Entries []map[string]any `json:"entries"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				lastErr = fmt.Errorf("hyperliquid announcements: decode envelope url=%s bytes=%d attempt=%d/%d: %w", baseURL, len(raw), attempt, hyperliquidAnnouncementMaxAttempts, err)
				if !shouldRetryHyperliquidAnnouncement(err) || attempt == hyperliquidAnnouncementMaxAttempts {
					return nil, lastErr
				}
				if err := sleepHyperliquidAnnouncementRetry(ctx); err != nil {
					return nil, err
				}
				continue
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
		return nil, lastErr
	}
}

func shouldRetryHyperliquidAnnouncement(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "unexpected end of json") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "tls handshake timeout")
}

func sleepHyperliquidAnnouncementRetry(ctx context.Context) error {
	if hyperliquidAnnouncementRetryDelay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(hyperliquidAnnouncementRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
