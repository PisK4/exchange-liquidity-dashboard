package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// BinanceCMSArticleListURL is the bapi composite endpoint operators
// commonly use to scrape Binance public announcements. The endpoint
// is documented in the dev community (see Phase 4 spec notes) and
// while it is not part of the official spot/margin API the
// type=1&catalogId=<n> form has been stable for years.
//
// Operators can override catalogId at config time when Binance
// re-numbers the perpetual-listing catalog.
const BinanceCMSArticleListURL = "https://www.binance.com/bapi/composite/v1/public/cms/article/list/query"

// DefaultBinanceCMSCatalogID is the "New Cryptocurrency Listing"
// catalog (covers spot + futures listings; classifyTitle filters out
// the spot ones at parse time). Operators may override via the
// per-platform poll override.
const DefaultBinanceCMSCatalogID = 48

// FetchBinanceCMSAnnouncements builds the AnnouncementSource.Fetch
// closure for the Binance bapi CMS article list. The closure GETs
// /bapi/composite/v1/public/cms/article/list/query?type=1&catalogId=<n>,
// peels data.catalogs[].articles[], stamps catalogName into each
// article (so the existing announcement.ParseBinanceCMSAnnouncement
// parser can hand it back as Category), and emits one
// json.RawMessage per article.
//
// Binance bapi uses code="000000" for success (string typed). A
// non-zero / non-"000000" / non-empty-and-non-"000000" code surfaces
// as a transient error so the source-health wrapper backs off.
func FetchBinanceCMSAnnouncements(deps HTTPDeps, baseURL string, catalogID int) func(ctx context.Context) ([]json.RawMessage, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BinanceCMSArticleListURL
	}
	if catalogID <= 0 {
		catalogID = DefaultBinanceCMSCatalogID
	}
	url := baseURL
	if !strings.Contains(url, "?") {
		url += fmt.Sprintf("?type=1&catalogId=%d&pageNo=1&pageSize=20", catalogID)
	} else {
		if !strings.Contains(url, "type=") {
			url += fmt.Sprintf("&type=1")
		}
		if !strings.Contains(url, "catalogId=") {
			url += fmt.Sprintf("&catalogId=%d", catalogID)
		}
		if !strings.Contains(url, "pageNo=") {
			url += "&pageNo=1"
		}
		if !strings.Contains(url, "pageSize=") {
			url += "&pageSize=20"
		}
	}
	return func(ctx context.Context) ([]json.RawMessage, error) {
		raw, err := deps.fetchJSON(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Catalogs []struct {
					CatalogID   int               `json:"catalogId"`
					CatalogName string            `json:"catalogName"`
					Articles    []json.RawMessage `json:"articles"`
				} `json:"catalogs"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("binance cms: decode envelope: %w", err)
		}
		if envelope.Code != "" && envelope.Code != "000000" {
			return nil, fmt.Errorf("binance cms: code=%s message=%q", envelope.Code, envelope.Message)
		}
		var out []json.RawMessage
		for _, cat := range envelope.Data.Catalogs {
			for _, art := range cat.Articles {
				// Stamp catalogName onto each article so the parser
				// (which expects a flat record) can hand it back as
				// the Category field without us needing to teach the
				// parser about the catalog tree.
				reshaped, err := injectStringField(art, "catalogName", cat.CatalogName)
				if err != nil {
					continue
				}
				out = append(out, reshaped)
			}
		}
		return out, nil
	}
}

// injectStringField rewrites the JSON object `raw` with the given
// key set to `value`. The function deliberately re-marshals through
// a generic map so existing fields stay verbatim (including ones
// the parser does not declare) and the result remains a valid JSON
// object regardless of upstream schema drift.
func injectStringField(raw json.RawMessage, key, value string) (json.RawMessage, error) {
	if value == "" {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	if _, exists := obj[key]; exists {
		return raw, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	obj[key] = encoded
	return json.Marshal(obj)
}
