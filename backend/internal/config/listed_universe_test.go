package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadListedUniverseMissingFile(t *testing.T) {
	u, err := LoadListedUniverse(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if u == nil {
		t.Fatal("LoadListedUniverse must return a non-nil universe even when the file is absent")
	}
	if u.Loaded() {
		t.Fatal("missing-file universe must report Loaded()=false")
	}
	if u.IsListed("edgeX", "BTC") {
		t.Fatal("unloaded universe must answer IsListed=false")
	}
}

func TestLoadListedUniverseParsesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "listed_universe.yaml")
	yamlBody := `
schema_version: 1
generated_at: "2026-05-20T00:00:00Z"
generated_by: backend/scripts/build-catalog
platforms:
  edgeX:
    base_assets: [BTC, eth, sol, BTC]
  binance:
    base_assets: [BTC, ETH, "  DOGE  ", ""]
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	u, err := LoadListedUniverse(path)
	if err != nil {
		t.Fatalf("LoadListedUniverse: %v", err)
	}
	if !u.Loaded() {
		t.Fatal("Loaded() must be true after successful parse")
	}
	if !u.IsListed("edgeX", "btc") {
		t.Fatal("edgeX should list BTC (case-insensitive)")
	}
	if !u.IsListed("edgeX", "ETH") {
		t.Fatal("edgeX should list ETH after normalisation")
	}
	if u.IsListed("edgeX", "XRP") {
		t.Fatal("edgeX must not list XRP from this fixture")
	}
	if !u.IsListed("binance", "DOGE") {
		t.Fatal("binance DOGE entry must survive whitespace trimming")
	}
	got := u.BaseAssets("edgeX")
	want := []string{"BTC", "ETH", "SOL"}
	if len(got) != len(want) {
		t.Fatalf("edgeX base_assets dedup/sort failed: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("edgeX base_assets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestListedUniverseFromMap(t *testing.T) {
	u := NewListedUniverseFromMap(map[string][]string{
		"edgeX": {"BTC", "ETH"},
	})
	if !u.Loaded() {
		t.Fatal("constructor must mark universe as loaded")
	}
	if !u.IsListed("edgeX", "btc") {
		t.Fatal("case-insensitive lookup failed")
	}
	if u.IsListed("binance", "BTC") {
		t.Fatal("unknown platform must return false")
	}
}

func TestLoadListedUniverseFromRepoYAML(t *testing.T) {
	// Smoke-test the committed config/listed_universe.yaml so a typo there
	// fails fast in CI rather than silently zeroing the edgeX 已上线? column
	// at runtime.
	u, err := LoadListedUniverse("../../../config/listed_universe.yaml")
	if err != nil {
		t.Fatalf("load repo yaml: %v", err)
	}
	if !u.Loaded() {
		t.Skip("config/listed_universe.yaml not present in this workspace, skipping smoke check")
	}
	if !u.IsListed("edgeX", "BTC") {
		t.Fatal("repo listed_universe.yaml must mark BTC as edgeX-listed")
	}
	if !u.IsListed("edgeX", "ETH") || !u.IsListed("edgeX", "SOL") {
		t.Fatal("repo listed_universe.yaml must mark ETH and SOL as edgeX-listed")
	}
	if got := len(u.BaseAssets("edgeX")); got < 30 {
		t.Fatalf("expected at least 30 edgeX bases in committed yaml, got %d", got)
	}
}

func TestListedUniverseNilSafe(t *testing.T) {
	var u *ListedUniverse
	if u.Loaded() {
		t.Fatal("nil receiver must report Loaded()=false")
	}
	if u.IsListed("edgeX", "BTC") {
		t.Fatal("nil receiver IsListed must be false")
	}
	if got := u.BaseAssets("edgeX"); got != nil {
		t.Fatalf("nil receiver BaseAssets must be nil, got %v", got)
	}
}
