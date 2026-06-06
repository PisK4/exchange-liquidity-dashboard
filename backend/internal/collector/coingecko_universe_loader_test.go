package collector

import (
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
)

// TestSetListedUniverseLoaderHotReloadsAcrossTicks pins the F1
// contract: the CG collector resolves the listed_universe on every
// enrichment pass, not on startup. The test swaps the loader output
// between two enrich calls and asserts the second tick sees the new
// universe.
func TestSetListedUniverseLoaderHotReloadsAcrossTicks(t *testing.T) {
	first := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	second := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC", "DOGE"}})
	current := first
	c := &CoinGeckoCollector{}
	c.SetListedUniverseLoader(func() *config.ListedUniverse { return current })

	rows := map[string][]domain.Top30Row{
		"edgeX": {{Symbol: "BTC-USDT (perp)"}, {Symbol: "DOGE-USDT (perp)"}},
	}
	enrichTop30Rows(rows, nil, c.listedUniverse())

	if got := rows["edgeX"]; len(got) != 2 || got[0].EdgexListed != true || got[1].EdgexListed != false {
		t.Fatalf("first tick rows = %+v (BTC must be listed, DOGE not)", got)
	}

	// Swap loader output between ticks.
	current = second
	rows2 := map[string][]domain.Top30Row{
		"edgeX": {{Symbol: "BTC-USDT (perp)"}, {Symbol: "DOGE-USDT (perp)"}},
	}
	enrichTop30Rows(rows2, nil, c.listedUniverse())
	if got := rows2["edgeX"]; got[1].EdgexListed != true {
		t.Fatalf("second tick should see DOGE as listed after loader swap; got %+v", got)
	}
}

// TestSetListedUniverseLoaderNilLoaderDegradesGracefully ensures a
// nil loader (or a loader that returns nil) does NOT crash and does
// NOT overwrite the previous status — the Top30 column simply stays
// on its legacy "false" default.
func TestSetListedUniverseLoaderNilLoaderDegradesGracefully(t *testing.T) {
	c := &CoinGeckoCollector{}
	c.SetListedUniverseLoader(nil)
	if got := c.listedUniverse(); got != nil {
		t.Fatalf("nil loader must yield nil universe, got %+v", got)
	}
	c.SetListedUniverseLoader(func() *config.ListedUniverse { return nil })
	if got := c.listedUniverse(); got != nil {
		t.Fatalf("loader returning nil must yield nil universe, got %+v", got)
	}
}
