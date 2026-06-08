package fetcher

import (
	"net/http"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/config"
)

func TestBuildListingSourcesAssemblesFullDefaultRoster(t *testing.T) {
	cfg := defaultListingAgentCfg()
	deps := HTTPDeps{Client: &http.Client{Timeout: time.Second}}
	got, err := BuildListingSources(cfg.Sources, deps)
	if err != nil {
		t.Fatalf("build err = %v", err)
	}
	if len(got.Instrument) != 15 {
		t.Fatalf("want 15 instrument sources, got %d", len(got.Instrument))
	}
	if len(got.Announcement) != 4 {
		t.Fatalf("want 4 announcement sources, got %d", len(got.Announcement))
	}
	wantInstr := map[string][]string{
		"binance":     {"usdm_futures"},
		"bybit":       {"linear_futures"},
		"okx":         {"swap"},
		"bitget":      {"usdt_futures"},
		"mexc":        {"contract"},
		"hyperliquid": {"perp"},
		"edgeX":       {"perp_v1", "perp_v2", "spot"},
		"bingx":       {"spot", "swap"},
		"gate":        {"spot", "usdt_futures"},
		"lighter":     {"perp", "spot"},
	}
	for _, src := range got.Instrument {
		mts, ok := wantInstr[src.Platform]
		if !ok {
			t.Fatalf("unexpected instrument platform %s", src.Platform)
		}
		var match bool
		for _, mt := range mts {
			if mt == src.MarketType {
				match = true
				break
			}
		}
		if !match {
			t.Fatalf("unexpected instrument source %s/%s", src.Platform, src.MarketType)
		}
		if src.Fetch == nil {
			t.Fatalf("%s/%s missing Fetch closure", src.Platform, src.MarketType)
		}
		if src.SourceKey == "" || src.SourceURL == "" {
			t.Fatalf("%s/%s missing source key/url", src.Platform, src.MarketType)
		}
		if src.Platform == "bybit" && src.SourceKey != "bybit/linear" {
			t.Fatalf("bybit source key must stay backward-compatible, got %q", src.SourceKey)
		}
	}
	wantAnn := map[string]bool{"bybit": true, "bitget": true, "binance": true, "hyperliquid": true}
	for _, src := range got.Announcement {
		if !wantAnn[src.Platform] {
			t.Fatalf("unexpected announcement source %s", src.Platform)
		}
		if src.Fetch == nil || src.Parse == nil {
			t.Fatalf("%s announcement source missing Fetch/Parse", src.Platform)
		}
	}
}

func TestBuildListingSourcesSkipsDisabledSubsystems(t *testing.T) {
	cfg := defaultListingAgentCfg()
	cfg.Sources.InstrumentDiff.Enabled = false
	got, err := BuildListingSources(cfg.Sources, HTTPDeps{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got.Instrument) != 0 {
		t.Fatalf("instrument subsystem disabled but got %d sources", len(got.Instrument))
	}
	if len(got.Announcement) != 4 {
		t.Fatalf("announcement subsystem must still be enabled, got %d", len(got.Announcement))
	}
}

func TestBuildListingSourcesSkipsDisabledPolls(t *testing.T) {
	cfg := defaultListingAgentCfg()
	cfg.Sources.InstrumentDiff.Polls[0].Enabled = false
	cfg.Sources.Announcement.Polls[1].Enabled = false
	got, err := BuildListingSources(cfg.Sources, HTTPDeps{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got.Instrument) != 14 {
		t.Fatalf("want 14 instrument sources after disabling one, got %d", len(got.Instrument))
	}
	if len(got.Announcement) != 3 {
		t.Fatalf("want 3 announcement sources after disable, got %d", len(got.Announcement))
	}
}

func TestBuildListingSourcesRejectsUnknownPlatformMarketTypeCombo(t *testing.T) {
	cfg := config.ListingSourcesConfig{
		InstrumentDiff: config.ListingInstrumentDiffConfig{
			Enabled: true,
			Polls:   []config.ListingSourcePollConfig{{Platform: "foobar", MarketType: "swap", Enabled: true}},
		},
	}
	if _, err := BuildListingSources(cfg, HTTPDeps{}); err == nil {
		t.Fatalf("expected error on unknown platform/market_type combo")
	}
}

func TestBuildListingSourcesRejectsUnknownAnnouncementPlatform(t *testing.T) {
	cfg := config.ListingSourcesConfig{
		Announcement: config.ListingAnnouncementConfig{
			Enabled: true,
			Polls:   []config.ListingSourcePollConfig{{Platform: "kraken", Enabled: true}},
		},
	}
	if _, err := BuildListingSources(cfg, HTTPDeps{}); err == nil {
		t.Fatalf("expected error on unknown announcement platform")
	}
}

// defaultListingAgentCfg returns a copy of the production default
// roster so each test starts from a clean, known state.
func defaultListingAgentCfg() config.ListingAgentConfig {
	return config.Default().Runtime.ListingAgent
}
