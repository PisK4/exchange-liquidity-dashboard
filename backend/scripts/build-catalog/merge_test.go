package main

import (
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
)

func TestMergeURLVerifiedPropagatesFlagWhenURLUnchanged(t *testing.T) {
	prior := config.Catalog{Platforms: map[string]map[string]config.CatalogSymbol{
		"binance": {
			"BTC": {APISymbol: "BTCUSDT", FrontendURL: "https://x/BTC", URLVerified: true},
		},
	}}
	fresh := config.Catalog{Platforms: map[string]map[string]config.CatalogSymbol{
		"binance": {
			"BTC": {APISymbol: "BTCUSDT", FrontendURL: "https://x/BTC", URLVerified: false},
			"ETH": {APISymbol: "ETHUSDT", FrontendURL: "https://x/ETH", URLVerified: false},
		},
	}}
	mergeURLVerifiedFromPrior(&fresh, prior)
	if !fresh.Platforms["binance"]["BTC"].URLVerified {
		t.Errorf("BTC verified flag must propagate when URL unchanged")
	}
	if fresh.Platforms["binance"]["ETH"].URLVerified {
		t.Errorf("ETH had no prior approval, flag must remain false")
	}
}

func TestMergeURLVerifiedResetsFlagWhenURLChanges(t *testing.T) {
	prior := config.Catalog{Platforms: map[string]map[string]config.CatalogSymbol{
		"okx": {
			"BTC": {FrontendURL: "https://old/BTC", URLVerified: true},
		},
	}}
	fresh := config.Catalog{Platforms: map[string]map[string]config.CatalogSymbol{
		"okx": {
			"BTC": {FrontendURL: "https://new/BTC", URLVerified: false},
		},
	}}
	mergeURLVerifiedFromPrior(&fresh, prior)
	if fresh.Platforms["okx"]["BTC"].URLVerified {
		t.Errorf("URL changed; flag must reset to false (operator approved a different URL)")
	}
}

func TestMergeURLVerifiedHandlesMissingPlatformInPrior(t *testing.T) {
	prior := config.Catalog{Platforms: map[string]map[string]config.CatalogSymbol{
		"binance": {
			"BTC": {FrontendURL: "https://x/BTC", URLVerified: true},
		},
	}}
	fresh := config.Catalog{Platforms: map[string]map[string]config.CatalogSymbol{
		"bybit": {
			"BTC": {FrontendURL: "https://y/BTC", URLVerified: false},
		},
	}}
	mergeURLVerifiedFromPrior(&fresh, prior)
	if fresh.Platforms["bybit"]["BTC"].URLVerified {
		t.Errorf("bybit had no prior data; flag must stay false")
	}
}
