package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"edgex-ops-intelligence/backend/internal/activity"
	"edgex-ops-intelligence/backend/internal/config"
)

var requiredPlatforms = []string{"binance", "okx", "bingx", "gate", "mexc", "bybit", "bitget", "hyperliquid", "lighter"}

type sourceCoverageReport struct {
	OK               bool     `json:"ok"`
	Platforms        []string `json:"platforms"`
	MissingPlatforms []string `json:"missing_platforms"`
	EnabledSources   int      `json:"enabled_sources"`
}

func main() {
	var (
		configDir    = flag.String("config-dir", "../config", "Path to the EdgeX Ops Intelligence config directory")
		allowPartial = flag.Bool("allow-partial", false, "Warn instead of failing when some V1 platforms are missing")
	)
	flag.Parse()

	cfg, err := config.Load(*configDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := evaluateSmokeReadiness(cfg, *allowPartial); err != nil {
		log.Fatal(err)
	}
	report := validateSourceCoverage(cfg.Runtime.ActivityAgent.Sources)
	body, _ := json.MarshalIndent(report, "", "  ")
	fmt.Printf("activity smoke source coverage:\n%s\n", body)
	if err := renderSmokeCards(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("activity smoke PASS")
}

func evaluateSmokeReadiness(cfg config.Config, allowPartial bool) error {
	if !cfg.Runtime.ActivityAgent.Enabled {
		return errors.New("activity_agent.enabled=false")
	}
	report := validateSourceCoverage(cfg.Runtime.ActivityAgent.Sources)
	if !report.OK && !allowPartial {
		return fmt.Errorf("missing Activity V1 platforms: %s", strings.Join(report.MissingPlatforms, ","))
	}
	return nil
}

func validateSourceCoverage(sources []config.ActivitySourceConfig) sourceCoverageReport {
	seen := map[string]bool{}
	enabled := 0
	for _, source := range sources {
		platform := strings.ToLower(strings.TrimSpace(source.Platform))
		if platform == "" {
			continue
		}
		seen[platform] = true
		if source.Enabled {
			enabled++
		}
	}
	report := sourceCoverageReport{EnabledSources: enabled}
	for _, platform := range requiredPlatforms {
		if seen[platform] {
			report.Platforms = append(report.Platforms, platform)
		} else {
			report.MissingPlatforms = append(report.MissingPlatforms, platform)
		}
	}
	report.OK = len(report.MissingPlatforms) == 0
	return report
}

func renderSmokeCards() error {
	now := time.Date(2026, 6, 5, 8, 5, 0, 0, time.UTC)
	event := activity.ActivityEventCard{
		EventID:             1,
		EventVersion:        1,
		ContentHash:         "smoke-hash",
		Platform:            "Binance",
		SourceGroup:         "cms_article_detail",
		FetchMode:           "http_direct",
		SourceHealth:        activity.SourceStatusOK,
		Title:               "Activity smoke event",
		ActivityType:        "launchpool",
		Summary:             "Smoke renderer contract event.",
		SourceURL:           "https://example.test/activity",
		DedupeKey:           "activity-smoke|event",
		TriggerTime:         now,
		DecisionBaseURL:     "https://dashboard.example.test",
		DecisionTokenSecret: "smoke-secret",
	}
	renderers := []func() ([]byte, error){
		func() ([]byte, error) { return activity.RenderActivityEventAlertPostMessage(event) },
		func() ([]byte, error) { return activity.RenderActivityReviewRequiredPostMessage(event) },
		func() ([]byte, error) {
			return activity.RenderActivityEventUpdatePostMessage(activity.ActivityEventUpdateCard{ActivityEventCard: event, ChangeSummary: "smoke update"})
		},
		func() ([]byte, error) {
			return activity.RenderActivityDailyDigestPostMessage(activity.ActivityDigestCard{DigestKey: "smoke-daily", Title: "Smoke Daily", TriggerTime: now})
		},
		func() ([]byte, error) {
			return activity.RenderActivityWeeklyDigestPostMessage(activity.ActivityWeeklyDigestCard{DigestKey: "smoke-weekly", Title: "Smoke Weekly", TriggerTime: now})
		},
		func() ([]byte, error) {
			return activity.RenderActivitySourceHealthPostMessage(activity.ActivitySourceHealthCard{Platform: "Gate", SourceGroup: "launchpool_project_list", Status: activity.SourceStatusOK, ErrorKind: "recovered", TriggerTime: now})
		},
	}
	for i, render := range renderers {
		payload, err := render()
		if err != nil {
			return fmt.Errorf("render smoke card %d: %w", i, err)
		}
		if !strings.Contains(string(payload), `"msg_type":"interactive"`) {
			return fmt.Errorf("render smoke card %d missing interactive msg_type", i)
		}
	}
	return nil
}
