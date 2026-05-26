package main

import (
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

func TestResolveMySQLDSNUsesFlagBeforeConfig(t *testing.T) {
	cfg := config.Config{Database: config.DatabaseConfig{DSN: "from-config"}}
	if got := resolveMySQLDSN("from-flag", cfg); got != "from-flag" {
		t.Fatalf("resolveMySQLDSN flag = %q", got)
	}
	if got := resolveMySQLDSN("", cfg); got != "from-config" {
		t.Fatalf("resolveMySQLDSN config = %q", got)
	}
}
