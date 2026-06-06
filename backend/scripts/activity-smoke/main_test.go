package main

import (
	"testing"

	"edgex-ops-intelligence/backend/internal/config"
)

func TestValidateSourceCoverageRequiresNinePlatforms(t *testing.T) {
	cfg := config.Default()
	report := validateSourceCoverage(cfg.Runtime.ActivityAgent.Sources)
	if !report.OK || len(report.Platforms) != 9 {
		t.Fatalf("report=%+v", report)
	}

	cfg.Runtime.ActivityAgent.Sources = cfg.Runtime.ActivityAgent.Sources[:1]
	report = validateSourceCoverage(cfg.Runtime.ActivityAgent.Sources)
	if report.OK || len(report.MissingPlatforms) == 0 {
		t.Fatalf("expected missing platforms, report=%+v", report)
	}
}

func TestActivitySmokeAllowPartialControlsMissingSourceFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.ActivityAgent.Sources = cfg.Runtime.ActivityAgent.Sources[:1]
	if err := evaluateSmokeReadiness(cfg, false); err == nil {
		t.Fatalf("expected failure without allow-partial")
	}
	if err := evaluateSmokeReadiness(cfg, true); err != nil {
		t.Fatalf("allow-partial should tolerate missing platforms: %v", err)
	}
}
