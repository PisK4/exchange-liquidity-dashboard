package listing

import (
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
)

func TestBuildEdgexListedLookupNilUniverseReturnsUnknown(t *testing.T) {
	lookup := BuildEdgexListedLookup(nil)
	listed, known := lookup("BTC")
	if listed || known {
		t.Errorf("nil universe → got (%v,%v), want (false,false)", listed, known)
	}
}

func TestBuildEdgexListedLookupUnloadedUniverseReturnsUnknown(t *testing.T) {
	u := &config.ListedUniverse{Platforms: map[string]config.ListedPlatform{}}
	lookup := BuildEdgexListedLookup(u)
	listed, known := lookup("BTC")
	if listed || known {
		t.Errorf("unloaded universe → got (%v,%v), want (false,false)", listed, known)
	}
}

func TestBuildEdgexListedLookupListedSymbolReturnsTrue(t *testing.T) {
	u := config.NewListedUniverseFromMap(map[string][]string{
		"edgeX": {"BTC", "ETH", "SOL"},
	})
	lookup := BuildEdgexListedLookup(u)
	listed, known := lookup("BTC")
	if !listed || !known {
		t.Errorf("BTC listed → got (%v,%v), want (true,true)", listed, known)
	}
	listed, known = lookup("btc")
	if !listed || !known {
		t.Errorf("btc (lower) listed → got (%v,%v), want (true,true)", listed, known)
	}
}

func TestBuildEdgexListedLookupUnlistedSymbolReturnsFalseKnown(t *testing.T) {
	u := config.NewListedUniverseFromMap(map[string][]string{
		"edgeX": {"BTC", "ETH"},
	})
	lookup := BuildEdgexListedLookup(u)
	listed, known := lookup("XYZ")
	if listed || !known {
		t.Errorf("XYZ unlisted → got (%v,%v), want (false,true)", listed, known)
	}
}

func TestBuildEdgexListedLookupMissingEdgexPlatformReturnsUnknown(t *testing.T) {
	u := config.NewListedUniverseFromMap(map[string][]string{
		"binance": {"BTC", "ETH"},
	})
	lookup := BuildEdgexListedLookup(u)
	listed, known := lookup("BTC")
	if listed || known {
		t.Errorf("missing edgeX platform → got (%v,%v), want (false,false)", listed, known)
	}
}
