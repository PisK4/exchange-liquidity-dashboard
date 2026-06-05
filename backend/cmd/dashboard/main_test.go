package main

import (
	"os"
	"path/filepath"
	"testing"

	"edgex-dashboard/backend/internal/config"
)

func TestRoleStartsLiveProviders(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "api", want: false},
		{role: "collector", want: true},
		{role: "all", want: true},
		{role: "", want: false},
	}

	for _, tt := range tests {
		if got := roleStartsLiveProviders(tt.role); got != tt.want {
			t.Fatalf("roleStartsLiveProviders(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestActivityRoleRequiresMySQLAndStartsOnlyActivityWorker(t *testing.T) {
	if !roleStartsActivity("activity") || !roleStartsActivity("all") {
		t.Fatalf("activity worker should start for activity and all roles")
	}
	if roleStartsActivity("api") || roleStartsActivity("collector") || roleStartsActivity("listing") {
		t.Fatalf("activity worker should not start for unrelated roles")
	}
	if !roleRequiresMySQL("activity") {
		t.Fatalf("activity role must fail-fast without MySQL")
	}
}

func TestResolveMySQLDSNUsesFlagBeforeConfig(t *testing.T) {
	cfg := config.Config{Database: config.DatabaseConfig{DSN: "from-config"}}
	if got := resolveMySQLDSN("from-flag", cfg); got != "from-flag" {
		t.Fatalf("resolveMySQLDSN flag = %q", got)
	}
	if got := resolveMySQLDSN("", cfg); got != "from-config" {
		t.Fatalf("resolveMySQLDSN config = %q", got)
	}
}

func TestResolveConfigPathRelativeToConfigDir(t *testing.T) {
	configDir := filepath.Join("..", "config")
	fallback := filepath.Join(configDir, "listed_universe.yaml")

	got := resolveConfigPath("listed_universe.yaml", fallback, configDir)
	want := filepath.Join(configDir, "listed_universe.yaml")
	if got != want {
		t.Fatalf("resolveConfigPath relative = %q, want %q", got, want)
	}
}

func TestResolveConfigPathKeepsAbsoluteAndFallsBackOnUnresolvedEnv(t *testing.T) {
	configDir := filepath.Join("..", "config")
	fallback := filepath.Join("/var/lib/edgex-dashboard", "listed_universe.runtime.yaml")
	abs := filepath.Join(string(filepath.Separator), "tmp", "listed_universe.runtime.yaml")

	if got := resolveConfigPath(abs, fallback, configDir); got != abs {
		t.Fatalf("resolveConfigPath absolute = %q, want %q", got, abs)
	}
	if got := resolveConfigPath("${DASHBOARD_DATA_DIR}/listed_universe.runtime.yaml", fallback, configDir); got != fallback {
		t.Fatalf("resolveConfigPath unresolved env = %q, want fallback %q", got, fallback)
	}
}

func TestBuildUniverseLoaderFallsBackToSeedWhenRuntimeEdgeXEmpty(t *testing.T) {
	dir := t.TempDir()
	runtimePath := filepath.Join(dir, "listed_universe.runtime.yaml")
	seedPath := filepath.Join(dir, "listed_universe.yaml")

	if err := os.WriteFile(runtimePath, []byte(`schema_version: 1
generated_at: "2026-06-02T06:04:42Z"
generated_by: listing-agent/refresh
platforms:
  edgeX:
    base_assets: []
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedPath, []byte(`schema_version: 1
generated_at: "2026-06-01T00:00:00Z"
generated_by: build-catalog
platforms:
  edgeX:
    base_assets: [BTC]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	u := buildUniverseLoader(runtimePath, seedPath)()
	if u == nil || !u.IsListed("edgeX", "BTC") {
		t.Fatalf("loader must fall back to seed when runtime edgeX universe is empty")
	}
}
