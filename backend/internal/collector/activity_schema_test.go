package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActivitySchemaIncludedInInitSchemaSQL(t *testing.T) {
	for _, table := range []string{
		"t_activity_source_state",
		"t_activity_raw_evidence",
		"t_activity_event",
		"t_activity_event_symbol",
		"t_activity_digest",
		"t_activity_digest_item",
		"t_activity_review_item",
		"t_activity_delivery_outbox",
		"t_activity_delivery_attempt",
		"t_activity_worker_lease",
	} {
		if !contains(initSchemaSQL, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("initSchemaSQL missing CREATE TABLE for %s", table)
		}
	}
	for _, forbidden := range []string{"t_activity_event_reward_pool", "t_activity_event_task_condition", "t_activity_event_eligibility_rule", "webhook_url"} {
		if contains(initSchemaSQL, forbidden) {
			t.Fatalf("initSchemaSQL must not include %s", forbidden)
		}
	}
	for _, required := range []string{
		"payload_text LONGTEXT",
		"payload_size_bytes BIGINT UNSIGNED NOT NULL",
		"payload_truncated TINYINT(1) NOT NULL DEFAULT 0",
		"dedupe_key VARCHAR(191) NOT NULL UNIQUE",
		"reward_pools_json JSON",
		"task_conditions_json JSON",
		"eligibility_rules_json JSON",
		"rich_fields_summary_json JSON",
		"uk_activity_event_symbol",
		"disabled_no_webhook",
		"disabled_missing_secret",
	} {
		if !contains(initSchemaSQL, required) {
			t.Fatalf("initSchemaSQL missing required snippet %q", required)
		}
	}
}

func TestActivityMigrationUpAndDownExist(t *testing.T) {
	migrations := map[string][]string{
		"000013_activity_source_event": {
			"t_activity_source_state",
			"t_activity_raw_evidence",
			"t_activity_event",
		},
		"000014_activity_event_children": {
			"t_activity_event_symbol",
		},
		"000015_activity_delivery_review": {
			"t_activity_delivery_outbox",
			"t_activity_delivery_attempt",
			"t_activity_review_item",
			"t_activity_worker_lease",
		},
		"000016_activity_indexes_postinit": {
			"idx_activity_event_status_updated",
		},
	}
	for name, snippets := range migrations {
		upPath := filepath.Join("..", "..", "migrations", name+".up.sql")
		downPath := filepath.Join("..", "..", "migrations", name+".down.sql")
		up, err := os.ReadFile(upPath)
		if err != nil {
			t.Fatalf("read %s: %v", upPath, err)
		}
		down, err := os.ReadFile(downPath)
		if err != nil {
			t.Fatalf("read %s: %v", downPath, err)
		}
		for _, snippet := range snippets {
			if !contains(string(up), snippet) {
				t.Fatalf("%s missing %s", upPath, snippet)
			}
		}
		if !contains(string(down), "DROP") {
			t.Fatalf("%s should contain DROP statements", downPath)
		}
	}
}
