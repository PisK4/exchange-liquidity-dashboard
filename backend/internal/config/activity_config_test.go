package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultActivityAgentSeedsNinePlatformSources(t *testing.T) {
	cfg := Default()
	aa := cfg.Runtime.ActivityAgent
	if !aa.Enabled {
		t.Fatalf("default activity_agent.enabled=false")
	}
	if aa.DefaultPollInterval != 30*time.Minute {
		t.Fatalf("default poll interval=%s want 30m", aa.DefaultPollInterval)
	}
	if aa.Collection.DefaultPollInterval != 30*time.Minute {
		t.Fatalf("default collection poll interval=%s want 30m", aa.Collection.DefaultPollInterval)
	}
	if aa.Scheduler.IngestionInterval != 5*time.Minute || aa.Scheduler.ProducerInterval != time.Minute || aa.Scheduler.DeliveryInterval != 30*time.Second {
		t.Fatalf("scheduler defaults=%+v", aa.Scheduler)
	}
	if aa.WorkerLeaseTTL != 2*time.Minute {
		t.Fatalf("lease ttl=%s want 2m", aa.WorkerLeaseTTL)
	}
	if aa.DecisionToken.SecretEnv != "ACTIVITY_DECISION_TOKEN_SECRET" || aa.DecisionToken.TTL != 30*24*time.Hour {
		t.Fatalf("decision token defaults=%+v", aa.DecisionToken)
	}
	seen := map[string]bool{}
	for _, src := range aa.Sources {
		seen[src.Platform] = true
		if !src.Enabled || !src.AutoPushEnabled || src.PollInterval != 30*time.Minute {
			t.Fatalf("bad default source %+v", src)
		}
	}
	for _, platform := range []string{"binance", "okx", "bingx", "gate", "mexc", "bybit", "bitget", "hyperliquid", "lighter"} {
		if !seen[platform] {
			t.Fatalf("default activity sources missing %s; got %+v", platform, aa.Sources)
		}
	}
	if aa.Delivery.WebhookURLEnv != "ACTIVITY_LARK_WEBHOOK_URL" {
		t.Fatalf("activity webhook env=%q", aa.Delivery.WebhookURLEnv)
	}
	if aa.Delivery.MaxPerTick != 20 || aa.Delivery.SendSpacing != 2*time.Second {
		t.Fatalf("activity delivery defaults=%+v", aa.Delivery)
	}
}

func TestLoadActivityAgentConfigAndWebhook(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), "endpoints: {}\n")
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), "platforms: [edgeX]\nsymbols: []\n")
	mustWrite(t, filepath.Join(dir, "instrument_catalog.yaml"), "schema_version: 1\nplatforms: {}\n")
	mustWrite(t, filepath.Join(dir, "edgex-ops-intelligence.yaml"), `
Alert:
  Enabled: true
  Webhooks:
    Activity: https://example.test/activity-hook
Runtime:
  activity_agent:
    enabled: true
    default_poll_interval: 30m
    worker_lease_ttl: 90s
    source_proxy: http://127.0.0.1:7897
    decision_token:
      secret_env: TEST_ACTIVITY_DECISION_SECRET
      ttl: 48h
    delivery:
      enabled: true
      webhook_url_env: TEST_ACTIVITY_WEBHOOK
      collect_only_without_webhook: true
      proxy: http://127.0.0.1:7898
      dashboard_base_url: https://dashboard.example.test
      max_per_tick: 3
      send_spacing: 5s
      source_health_cooldown: 30m
      event_update_cooldown: 45m
    sources:
      - platform: binance
        source_group: cms_article_list
        fetch_mode: http_direct
        poll_interval: 10m
        enabled: true
        auto_push_enabled: false
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}
	if got := cfg.Alert.Webhooks.Activity; got != "https://example.test/activity-hook" {
		t.Fatalf("Activity webhook=%q", got)
	}
	aa := cfg.Runtime.ActivityAgent
	if aa.DefaultPollInterval != 30*time.Minute || aa.WorkerLeaseTTL != 90*time.Second || aa.SourceProxy != "http://127.0.0.1:7897" {
		t.Fatalf("activity runtime not loaded: %+v", aa)
	}
	if aa.DecisionToken.SecretEnv != "TEST_ACTIVITY_DECISION_SECRET" || aa.DecisionToken.TTL != 48*time.Hour {
		t.Fatalf("decision token config=%+v", aa.DecisionToken)
	}
	if aa.Delivery.WebhookURLEnv != "TEST_ACTIVITY_WEBHOOK" || aa.Delivery.MaxPerTick != 3 || aa.Delivery.SendSpacing != 5*time.Second {
		t.Fatalf("delivery config=%+v", aa.Delivery)
	}
	if len(aa.Sources) != 1 || aa.Sources[0].Platform != "binance" || aa.Sources[0].AutoPushEnabled {
		t.Fatalf("sources=%+v", aa.Sources)
	}
}

func TestLoadActivityAgentNestedSchedulerCollectionAndDelivery(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "exchange_endpoints.yaml"), "endpoints: {}\n")
	mustWrite(t, filepath.Join(dir, "symbol_mapping.yaml"), "platforms: [edgeX]\nsymbols: []\n")
	mustWrite(t, filepath.Join(dir, "instrument_catalog.yaml"), "schema_version: 1\nplatforms: {}\n")
	mustWrite(t, filepath.Join(dir, "edgex-ops-intelligence.yaml"), `
Runtime:
  activity_agent:
    enabled: true
    scheduler:
      ingestion_interval: 4m
      producer_interval: 45s
      delivery_interval: 15s
    collection:
      default_poll_interval: 1h
      default_timeout: 20s
      source_proxy: http://127.0.0.1:7897
    delivery:
      enabled: true
      webhook_url_env: TEST_ACTIVITY_WEBHOOK
      max_per_tick: 9
      send_spacing: 3s
    sources:
      - platform: gate
        source_group: launchpool_project_list
        requires_proxy: true
        collection:
          enabled: true
          fetch_mode: utls_proxy_json
          poll_interval: 1h
          source_url: https://gate.example/launchpool
          timeout: 25s
          proxy: http://127.0.0.1:7899
        delivery:
          enabled: true
          auto_push_enabled: false
          target_channel: lark_activity_gate
          webhook_url_env: TEST_ACTIVITY_GATE_WEBHOOK
          max_per_tick: 2
          send_spacing: 1s
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load err=%v", err)
	}
	aa := cfg.Runtime.ActivityAgent
	if aa.Scheduler.IngestionInterval != 4*time.Minute || aa.Scheduler.ProducerInterval != 45*time.Second || aa.Scheduler.DeliveryInterval != 15*time.Second {
		t.Fatalf("scheduler=%+v", aa.Scheduler)
	}
	if aa.Collection.DefaultPollInterval != time.Hour || aa.Collection.DefaultTimeout != 20*time.Second || aa.Collection.SourceProxy != "http://127.0.0.1:7897" {
		t.Fatalf("collection=%+v", aa.Collection)
	}
	if aa.Delivery.MaxPerTick != 9 || aa.Delivery.SendSpacing != 3*time.Second {
		t.Fatalf("delivery=%+v", aa.Delivery)
	}
	if len(aa.Sources) != 1 {
		t.Fatalf("sources len=%d", len(aa.Sources))
	}
	src := aa.Sources[0]
	if src.Platform != "gate" || src.SourceGroup != "launchpool_project_list" || !src.Enabled || src.AutoPushEnabled {
		t.Fatalf("source basics=%+v", src)
	}
	if src.FetchMode != "utls_proxy_json" || src.PollInterval != time.Hour || src.SourceURL != "https://gate.example/launchpool" {
		t.Fatalf("source collection mirror=%+v", src)
	}
	if src.Collection.Timeout != 25*time.Second || src.Collection.Proxy != "http://127.0.0.1:7899" {
		t.Fatalf("source collection=%+v", src.Collection)
	}
	if src.Delivery.TargetChannel != "lark_activity_gate" || src.Delivery.WebhookURLEnv != "TEST_ACTIVITY_GATE_WEBHOOK" || src.Delivery.MaxPerTick != 2 || src.Delivery.SendSpacing != time.Second {
		t.Fatalf("source delivery=%+v", src.Delivery)
	}
}
