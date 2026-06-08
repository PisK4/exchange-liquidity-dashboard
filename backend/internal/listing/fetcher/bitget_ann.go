package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// BitgetAnnouncementsURL is the v2 public announcements endpoint
// documented at
// https://www.bitget.com/api-doc/common/notice/Get-All-Notices.
// Caller picks the annType / language at config time; the default
// query targets coin_listings in English which is the right
// subscription for the listing agent.
const BitgetAnnouncementsURL = "https://api.bitget.com/api/v2/public/annoucements"

// FetchBitgetAnnouncements builds the AnnouncementSource.Fetch
// closure for Bitget's v2 public announcements API. The closure
// GETs /api/v2/public/annoucements?annType=coin_listings&language=en_US&limit=20,
// unwraps the code/data envelope, and reshapes each entry into the
// flat record the existing ParseBitgetAnnouncement parser expects.
//
// Field mapping (live API -> parser schema):
//
//	annId      -> announceId
//	annTitle   -> announceTitle
//	annUrl     -> announceUrl
//	annType+annSubType -> category  (joined with "/")
//	cTime      -> publishTime  (millis)
//	language   -> language
//
// Bitget returns code="00000" on success; anything else surfaces
// as a transient error so source-health can back off.
func FetchBitgetAnnouncements(deps HTTPDeps, baseURL string) func(ctx context.Context) ([]json.RawMessage, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = BitgetAnnouncementsURL
	}
	url := baseURL
	if !strings.Contains(url, "?") {
		url += "?annType=coin_listings&language=en_US&limit=20"
	} else {
		if !strings.Contains(url, "annType=") {
			url += "&annType=coin_listings"
		}
		if !strings.Contains(url, "language=") {
			url += "&language=en_US"
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
			Code string `json:"code"`
			Msg  string `json:"msg"`
			Data []struct {
				AnnID      string      `json:"annId"`
				AnnTitle   string      `json:"annTitle"`
				AnnDesc    string      `json:"annDesc"`
				CTime      json.Number `json:"cTime"`
				Language   string      `json:"language"`
				AnnURL     string      `json:"annUrl"`
				AnnType    string      `json:"annType"`
				AnnSubType string      `json:"annSubType"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("bitget announcements: decode envelope: %w", err)
		}
		if envelope.Code != "" && envelope.Code != "00000" {
			return nil, fmt.Errorf("bitget announcements: code=%s msg=%q", envelope.Code, envelope.Msg)
		}
		out := make([]json.RawMessage, 0, len(envelope.Data))
		for _, item := range envelope.Data {
			category := item.AnnType
			if item.AnnSubType != "" {
				category = item.AnnType + "/" + item.AnnSubType
			}
			row := map[string]any{
				"announceId":    item.AnnID,
				"announceTitle": item.AnnTitle,
				"announceUrl":   item.AnnURL,
				"category":      category,
				"language":      item.Language,
				"publishTime":   item.CTime,
				"description":   item.AnnDesc,
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
