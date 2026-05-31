package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// BybitAnnouncementsURL is the v5 official announcements index. The
// locale and type parameters are appended at fetch time. type
// "new_crypto" maps to the New Listings stream which is what the
// listing agent should subscribe to; other types (delistings,
// security, latest_activities) flow through different channels.
const BybitAnnouncementsURL = "https://api.bybit.com/v5/announcements/index"

// FetchBybitAnnouncements builds the AnnouncementSource.Fetch
// closure for Bybit's announcement index. The closure GETs
// /v5/announcements/index?locale=en-US&type=new_crypto, unwraps
// the retCode/result.list envelope, and reshapes each row into the
// flat record the existing ParseBybitAnnouncement parser expects.
//
// Field mapping (live API -> parser schema):
//
//	dateTimestamp / publishTime -> publishTime  (millis)
//	url                         -> url
//	type.title                  -> category
//	(no native id)              -> id  (derived from the
//	                                    Contentstack slug in url,
//	                                    falling back to the full
//	                                    URL hash when no slug found)
//
// The Bybit announcement payload does not carry an explicit id
// field. We derive a stable per-article id from the trailing
// `blt<hex>` segment in the URL — Contentstack content IDs are
// stable per article. Articles without such a slug fall back to
// the full URL as id, which still satisfies the parser's
// non-empty contract.
func FetchBybitAnnouncements(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]json.RawMessage, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BybitAnnouncementsURL
	}
	url := baseURL
	if !strings.Contains(url, "?") {
		url += "?locale=en-US&type=new_crypto&page=1&limit=20"
	} else {
		if !strings.Contains(url, "locale=") {
			url += "&locale=en-US"
		}
		if !strings.Contains(url, "type=") {
			url += "&type=new_crypto"
		}
		if !strings.Contains(url, "page=") {
			url += "&page=1"
		}
		if !strings.Contains(url, "limit=") {
			url += "&limit=20"
		}
	}
	return func(ctx context.Context) ([]json.RawMessage, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			RetCode int    `json:"retCode"`
			RetMsg  string `json:"retMsg"`
			Result  struct {
				List []struct {
					Title       string `json:"title"`
					Description string `json:"description"`
					URL         string `json:"url"`
					Type        struct {
						Title string `json:"title"`
						Key   string `json:"key"`
					} `json:"type"`
					DateTimestamp json.Number `json:"dateTimestamp"`
					PublishTime   json.Number `json:"publishTime"`
				} `json:"list"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("bybit announcements: decode envelope: %w", err)
		}
		if envelope.RetCode != 0 {
			return nil, fmt.Errorf("bybit announcements: retCode=%d retMsg=%q", envelope.RetCode, envelope.RetMsg)
		}
		out := make([]json.RawMessage, 0, len(envelope.Result.List))
		for _, item := range envelope.Result.List {
			pubMillis := pickNonEmptyNumber(item.PublishTime, item.DateTimestamp)
			row := map[string]any{
				"id":          deriveBybitAnnouncementID(item.URL),
				"title":       item.Title,
				"description": item.Description,
				"url":         item.URL,
				"category":    item.Type.Title,
				"publishTime": pubMillis,
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				continue
			}
			out = append(out, encoded)
		}
		return out, nil
	}
}

// deriveBybitAnnouncementID extracts the trailing Contentstack slug
// (matching `blt<hex>`) from the Bybit announcement URL when
// present. Contentstack assigns these IDs once and never changes
// them, so they form a stable per-article fingerprint. When no slug
// is present (e.g. legacy URLs) the full URL is returned as a
// fallback so the parser's non-empty contract still holds.
func deriveBybitAnnouncementID(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	trimmed := strings.TrimRight(url, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return trimmed
	}
	tail := trimmed[idx+1:]
	if strings.HasPrefix(tail, "blt") {
		return tail
	}
	// Some URLs embed the slug before a trailing path segment; try
	// to find the blt prefix anywhere in the path.
	parts := strings.Split(trimmed, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasPrefix(parts[i], "blt") {
			return parts[i]
		}
	}
	return trimmed
}

// pickNonEmptyNumber returns the first non-empty json.Number from
// the inputs, preserving the original numeric string so downstream
// parsers can use json.Number.Int64() without rounding.
func pickNonEmptyNumber(candidates ...json.Number) json.Number {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}
