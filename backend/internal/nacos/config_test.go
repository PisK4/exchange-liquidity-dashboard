package nacos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBootstrapDefaults(t *testing.T) {
	cfg, err := LoadBootstrap(t.TempDir())
	if err != nil {
		t.Fatalf("LoadBootstrap() error = %v", err)
	}
	if cfg.ServerIP != "127.0.0.1" || cfg.ServerPort != 8848 || cfg.GroupName != "DEFAULT_GROUP" {
		t.Fatalf("defaults not loaded: %+v", cfg)
	}
}

func TestLoadBootstrapServerAddrOverride(t *testing.T) {
	t.Setenv("NACOS_CONFIG_SERVER_ADDR", "nacos.dev:9848")
	cfg, err := LoadBootstrap(t.TempDir())
	if err != nil {
		t.Fatalf("LoadBootstrap() error = %v", err)
	}
	if cfg.ServerIP != "nacos.dev" || cfg.ServerPort != 9848 {
		t.Fatalf("server addr override = %+v", cfg)
	}
}

func TestLoadBootstrapSplitServerOverrideWins(t *testing.T) {
	t.Setenv("NACOS_CONFIG_SERVER_ADDR", "nacos.dev:9848")
	t.Setenv("NACOS_CONFIG_SERVER_IP", "10.0.0.8")
	t.Setenv("NACOS_CONFIG_SERVER_PORT", "8848")
	cfg, err := LoadBootstrap(t.TempDir())
	if err != nil {
		t.Fatalf("LoadBootstrap() error = %v", err)
	}
	if cfg.ServerIP != "10.0.0.8" || cfg.ServerPort != 8848 {
		t.Fatalf("split override should win: %+v", cfg)
	}
}

func TestLoadBootstrapEnvOverrides(t *testing.T) {
	t.Setenv("NACOS_CONFIG_NAMESPACE", "ops-dev")
	t.Setenv("NACOS_CONFIG_GROUP", "OPS_GROUP")
	t.Setenv("NACOS_CONFIG_NAME", "custom.yaml")
	t.Setenv("NACOS_CONFIG_USERNAME", "user")
	t.Setenv("NACOS_CONFIG_PASSWORD", "pass")
	cfg, err := LoadBootstrap(t.TempDir())
	if err != nil {
		t.Fatalf("LoadBootstrap() error = %v", err)
	}
	if cfg.ClientNamespaceID != "ops-dev" || cfg.GroupName != "OPS_GROUP" || cfg.ConfigName != "custom.yaml" || cfg.Username != "user" || cfg.Password != "pass" {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
}

func TestLoadBootstrapReadsYAML(t *testing.T) {
	dir := t.TempDir()
	data := []byte("ServerIP: nacos.yaml\nServerPort: 8848\nClientNamespaceID: yaml-dev\nGroupName: YAML_GROUP\nConfigName: edgex-ops-intelligence.yaml\n")
	if err := os.WriteFile(filepath.Join(dir, "nacos.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap() error = %v", err)
	}
	if cfg.ServerIP != "nacos.yaml" || cfg.ClientNamespaceID != "yaml-dev" || cfg.GroupName != "YAML_GROUP" {
		t.Fatalf("yaml not loaded: %+v", cfg)
	}
}
