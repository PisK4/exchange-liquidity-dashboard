package secrets

import (
	"context"
	"fmt"
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
)

type fakeResolver map[string]string

func (f fakeResolver) Get(key string) (string, error) {
	if v, ok := f[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("missing %s", key)
}

func TestResolveDisabledDoesNotMutateConfig(t *testing.T) {
	t.Setenv("AWS_SM_ENABLE", "false")
	cfg := config.Config{Database: config.DatabaseConfig{Addr: "mysql.local"}, Aws: config.AwsConfig{DBAddr: "DB_ADDR"}}
	if err := Resolve(context.Background(), &cfg, fakeResolver{"DB_ADDR": "mysql.dev"}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if cfg.Database.Addr != "mysql.local" {
		t.Fatalf("disabled resolver mutated addr: %+v", cfg.Database)
	}
}

func TestResolveWritesRuntimeSecrets(t *testing.T) {
	t.Setenv("AWS_SM_ENABLE", "true")
	t.Setenv("USE_LOCAL_CONFIG", "true")
	cfg := config.Config{
		Database: config.DatabaseConfig{Name: "ops", ParseTime: true},
		Aws: config.AwsConfig{
			DBAddr:                "DB_ADDR",
			DBUser:                "DB_USER",
			DBPass:                "DB_PASS",
			CoinGeckoAPIKey:       "CG_KEY",
			ListingCallbackSecret: "LISTING_SECRET",
			ActivityDecisionToken: "ACTIVITY_TOKEN",
		},
		Alert: config.AlertConfig{Push: config.AlertPush{Listing: "https://dev.example/listing"}},
	}
	err := Resolve(context.Background(), &cfg, fakeResolver{
		"DB_ADDR":        "mysql.dev:3306",
		"DB_USER":        "ops",
		"DB_PASS":        "secret",
		"CG_KEY":         "cg-secret",
		"LISTING_SECRET": "listing-secret",
		"ACTIVITY_TOKEN": "activity-token",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if cfg.Database.Addr != "mysql.dev:3306" || cfg.Database.UserName != "ops" || cfg.Database.Password != "secret" {
		t.Fatalf("database secrets not resolved: %+v", cfg.Database)
	}
	if cfg.Runtime.CoinGecko.APIKey != "cg-secret" {
		t.Fatalf("coingecko key = %q", cfg.Runtime.CoinGecko.APIKey)
	}
	if cfg.Runtime.ListingAgent.DecisionCard.Callback.Secret != "listing-secret" {
		t.Fatalf("listing callback secret not resolved")
	}
	if cfg.Runtime.ActivityAgent.DecisionToken.Secret != "activity-token" {
		t.Fatalf("activity decision token not resolved")
	}
	if cfg.Alert.Push.Listing != "https://dev.example/listing" {
		t.Fatalf("Alert.Push webhook should not be overwritten: %+v", cfg.Alert.Push)
	}
}

func TestResolveProdModeFailsFast(t *testing.T) {
	t.Setenv("AWS_SM_ENABLE", "true")
	t.Setenv("USE_LOCAL_CONFIG", "false")
	cfg := config.Config{Aws: config.AwsConfig{DBAddr: "MISSING"}}
	if err := Resolve(context.Background(), &cfg, fakeResolver{}); err == nil {
		t.Fatalf("Resolve() expected prod failure")
	}
}

func TestResolveLocalModeIgnoresMissingKeys(t *testing.T) {
	t.Setenv("AWS_SM_ENABLE", "true")
	t.Setenv("USE_LOCAL_CONFIG", "true")
	cfg := config.Config{Database: config.DatabaseConfig{Addr: "mysql.local"}, Aws: config.AwsConfig{DBAddr: "MISSING"}}
	if err := Resolve(context.Background(), &cfg, fakeResolver{}); err != nil {
		t.Fatalf("Resolve() local mode error = %v", err)
	}
	if cfg.Database.Addr != "mysql.local" {
		t.Fatalf("missing local key should not mutate addr: %+v", cfg.Database)
	}
}
