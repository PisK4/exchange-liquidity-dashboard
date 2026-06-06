package main

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"edgex-ops-intelligence/backend/internal/adapter"
	"edgex-ops-intelligence/backend/internal/config"
	"edgex-ops-intelligence/backend/internal/domain"
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

func TestLighterMarketIDsFromConfigUsesConfiguredCatalogIDs(t *testing.T) {
	marketA := 113
	marketB := 176
	duplicateMarketA := 113
	otherPlatformMarket := 999
	cfg := config.Config{Symbols: []domain.SymbolSub{
		{Platform: "lighter", MarketID: &marketA},
		{Platform: "binance", MarketID: &otherPlatformMarket},
		{Platform: "lighter", MarketID: &marketB},
		{Platform: "lighter", MarketID: &duplicateMarketA},
		{Platform: "lighter"},
	}}

	got := lighterMarketIDsFromConfig(cfg)
	want := []int{113, 176}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lighterMarketIDsFromConfig = %v, want %v", got, want)
	}
}

func TestLighterMarketIDsFromConfigFallsBackToLegacyDefaults(t *testing.T) {
	cfg := config.Config{Symbols: []domain.SymbolSub{{Platform: "lighter"}}}
	got := lighterMarketIDsFromConfig(cfg)
	want := adapter.LighterMarketIDs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lighterMarketIDsFromConfig fallback = %v, want %v", got, want)
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

func TestBuildActivityEngineConfigResolvesEnvWebhookFirst(t *testing.T) {
	t.Setenv("ACTIVITY_WEBHOOK_TEST", "https://env.example/webhook")
	t.Setenv("ACTIVITY_SECRET_TEST", "decision-secret")
	cfg := config.Default()
	cfg.Alert.Webhooks.Activity = "https://yaml.example/webhook"
	cfg.Runtime.ActivityAgent.Delivery.WebhookURLEnv = "ACTIVITY_WEBHOOK_TEST"
	cfg.Runtime.ActivityAgent.DecisionToken.SecretEnv = "ACTIVITY_SECRET_TEST"
	got := buildActivityEngineConfig(cfg)
	if got.WebhookURL != "https://env.example/webhook" {
		t.Fatalf("webhook=%q", got.WebhookURL)
	}
	if got.DecisionTokenSecret != "decision-secret" {
		t.Fatalf("secret not resolved")
	}
}

func TestBuildActivityEngineConfigFallsBackToYAMLWebhook(t *testing.T) {
	t.Setenv("ACTIVITY_SECRET_TEST", "decision-secret")
	cfg := config.Default()
	cfg.Alert.Webhooks.Activity = "https://yaml.example/webhook"
	cfg.Runtime.ActivityAgent.Delivery.WebhookURLEnv = "ACTIVITY_WEBHOOK_TEST"
	cfg.Runtime.ActivityAgent.DecisionToken.SecretEnv = "ACTIVITY_SECRET_TEST"
	got := buildActivityEngineConfig(cfg)
	if got.WebhookURL != "https://yaml.example/webhook" {
		t.Fatalf("webhook=%q", got.WebhookURL)
	}
}

func TestBuildActivityEngineConfigWiresIngestionSources(t *testing.T) {
	cfg := config.Default()
	got := buildActivityEngineConfig(cfg)
	if len(got.Sources) != 9 {
		t.Fatalf("sources len=%d want 9", len(got.Sources))
	}
	if got.Fetch == nil || got.Parse == nil {
		t.Fatalf("activity ingestion fetch/parse must be wired")
	}
	foundBinance := false
	for _, src := range got.Sources {
		if src.Platform == "binance" && src.SourceGroup == "cms_article_list" {
			foundBinance = true
			if src.SourceURL == "" {
				t.Fatalf("binance source URL must be resolved")
			}
		}
	}
	if !foundBinance {
		t.Fatalf("binance cms source missing: %+v", got.Sources)
	}
}

func TestBuildHTTPClientWithProxyConfiguresTransportProxy(t *testing.T) {
	client := buildHTTPClientWithProxy(5*time.Second, "http://127.0.0.1:7897")
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("proxy transport not configured: %+v", client.Transport)
	}
	req, err := http.NewRequest(http.MethodGet, "https://open.larksuite.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy func err=%v", err)
	}
	if proxyURL.String() != "http://127.0.0.1:7897" {
		t.Fatalf("proxy=%s", proxyURL)
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
	fallback := filepath.Join("/var/lib/edgex-ops-intelligence", "listed_universe.runtime.yaml")
	abs := filepath.Join(string(filepath.Separator), "tmp", "listed_universe.runtime.yaml")

	if got := resolveConfigPath(abs, fallback, configDir); got != abs {
		t.Fatalf("resolveConfigPath absolute = %q, want %q", got, abs)
	}
	if got := resolveConfigPath("${OPS_INTELLIGENCE_DATA_DIR}/listed_universe.runtime.yaml", fallback, configDir); got != fallback {
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
