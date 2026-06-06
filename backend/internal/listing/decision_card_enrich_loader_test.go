package listing

import (
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
)

// TestBuildEdgexListedLookupLoaderResolvesUniverseEachCall pins the
// hot-reload behaviour: when the underlying universe changes between
// two calls, the lookup reflects the new universe immediately.
func TestBuildEdgexListedLookupLoaderResolvesUniverseEachCall(t *testing.T) {
	first := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC"}})
	second := config.NewListedUniverseFromMap(map[string][]string{"edgeX": {"BTC", "DOGE"}})
	current := first
	loader := func() *config.ListedUniverse { return current }
	lookup := BuildEdgexListedLookupLoader(loader)

	if listed, known := lookup("BTC"); !listed || !known {
		t.Fatalf("BTC pre-swap = (%v,%v), want (true,true)", listed, known)
	}
	if listed, known := lookup("DOGE"); listed || !known {
		t.Fatalf("DOGE pre-swap = (%v,%v), want (false,true)", listed, known)
	}
	current = second
	if listed, known := lookup("DOGE"); !listed || !known {
		t.Fatalf("DOGE post-swap = (%v,%v), want (true,true)", listed, known)
	}
}

// TestBuildEdgexListedLookupLoaderHandlesNilLoader guards against a
// missing wiring step — a nil loader must collapse to the "unknown"
// triple-state result so the decision-card renderer keeps degrading
// gracefully.
func TestBuildEdgexListedLookupLoaderHandlesNilLoader(t *testing.T) {
	lookup := BuildEdgexListedLookupLoader(nil)
	if listed, known := lookup("BTC"); listed || known {
		t.Fatalf("nil loader = (%v,%v), want (false,false)", listed, known)
	}
}

// TestBuildEdgexListedLookupLoaderHandlesNilUniverse covers the case
// where the loader resolves to nil (e.g. runtime yaml does not exist
// yet); the lookup must still degrade to unknown.
func TestBuildEdgexListedLookupLoaderHandlesNilUniverse(t *testing.T) {
	lookup := BuildEdgexListedLookupLoader(func() *config.ListedUniverse { return nil })
	if listed, known := lookup("BTC"); listed || known {
		t.Fatalf("nil-universe loader = (%v,%v), want (false,false)", listed, known)
	}
}
