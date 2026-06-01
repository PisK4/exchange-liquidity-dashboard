package listing

import (
	"testing"
	"time"
)

func TestFoldMarketStatusRowsAPISourceWins(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	listingTime := now.Add(-3 * time.Hour)
	announcementTime := now.Add(-12 * time.Hour)

	raw := []MarketStatusRow{
		{
			Platform:         "binance",
			MarketType:       "usdm_futures",
			StatusNormalized: StatusActive,
			ListingTimeTS:    &listingTime,
			LastSeenAt:       now,
			SourceKind:       "api",
		},
		{
			Platform:    "binance",
			SourceKind:  "announcement",
			PublishedAt: &announcementTime,
			LastSeenAt:  announcementTime,
		},
	}
	got := foldMarketStatusRows(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 platform, got %d", len(got))
	}
	if got[0].Status != StatusActive {
		t.Errorf("Status = %q, want active (API wins)", got[0].Status)
	}
	if got[0].SourceKind != "both" {
		t.Errorf("SourceKind = %q, want both", got[0].SourceKind)
	}
	if got[0].StatusLabel != "Perp LIVE" {
		t.Errorf("StatusLabel = %q, want 'Perp LIVE'", got[0].StatusLabel)
	}
}

func TestFoldMarketStatusRowsAnnouncementOnlyShowsPreListing(t *testing.T) {
	at := time.Date(2026, 5, 31, 8, 15, 0, 0, time.UTC)
	raw := []MarketStatusRow{
		{
			Platform:    "bybit",
			SourceKind:  "announcement",
			PublishedAt: &at,
			LastSeenAt:  at,
		},
	}
	got := foldMarketStatusRows(raw)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry")
	}
	if got[0].SourceKind != "announcement" {
		t.Errorf("SourceKind = %q", got[0].SourceKind)
	}
	if got[0].Status != StatusPreListing {
		t.Errorf("Status = %q, want pre_listing", got[0].Status)
	}
	if got[0].StatusLabel != "公告刚发布" {
		t.Errorf("StatusLabel = %q, want '公告刚发布'", got[0].StatusLabel)
	}
	if !got[0].OccurredAt.Equal(at) {
		t.Errorf("OccurredAt = %v, want %v", got[0].OccurredAt, at)
	}
}

func TestFoldMarketStatusRowsSortedByPlatformPriority(t *testing.T) {
	now := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	raw := []MarketStatusRow{
		{Platform: "lighter", SourceKind: "api", StatusNormalized: StatusActive, LastSeenAt: now},
		{Platform: "binance", SourceKind: "api", StatusNormalized: StatusActive, LastSeenAt: now},
		{Platform: "okx", SourceKind: "api", StatusNormalized: StatusActive, LastSeenAt: now},
	}
	got := foldMarketStatusRows(raw)
	if got[0].Platform != "binance" {
		t.Errorf("first platform = %q, want binance", got[0].Platform)
	}
	if got[1].Platform != "okx" {
		t.Errorf("second platform = %q, want okx", got[1].Platform)
	}
	if got[2].Platform != "lighter" {
		t.Errorf("third platform = %q, want lighter", got[2].Platform)
	}
}

func TestFoldMarketStatusRowsEmptyInputReturnsEmpty(t *testing.T) {
	got := foldMarketStatusRows(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d entries", len(got))
	}
}

func TestFoldMarketStatusRowsUnknownPlatformDisplayNameFallsBack(t *testing.T) {
	raw := []MarketStatusRow{
		{Platform: "weird-exchange", SourceKind: "api", StatusNormalized: StatusActive},
	}
	got := foldMarketStatusRows(raw)
	if got[0].DisplayName != "weird-exchange" {
		t.Errorf("DisplayName = %q, want fallback to platform name", got[0].DisplayName)
	}
}

func TestBuildMarketStatusLoaderNilRepoReturnsEmpty(t *testing.T) {
	loader := BuildMarketStatusLoader(nil)
	got, err := loader(nil, "ABC")
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %v, want empty", got)
	}
}
