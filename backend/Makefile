.PHONY: build test run smoke-api smoke-all-startup smoke-adapters smoke-coingecko smoke-listing smoke-activity e2e-listing e2e-activity backfill-share swagger migrate-up migrate-down catalog catalog-raw catalog-diff lint ci prune-snapshots-dry prune-snapshots-confirm verify-urls

PORT ?= 18080
SYMBOL ?= BTC-USDT
MYSQL_DSN ?= root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true
STARTUP_MYSQL_DSN ?=
BACKFILL_DAYS ?= 30
COINGECKO_API_KEY_ENV ?= COINGECKO_DEMO_API_KEY
COINGECKO_PROXY ?=

# build-catalog network controls. CATALOG_PROXY is forwarded via --proxy
# to every per-platform adapter; CATALOG_TIMEOUT bounds each adapter's
# fetch. Both default empty / 60s; set them on the make command line to
# refresh from a non-direct network:
#   make catalog CATALOG_PROXY=http://127.0.0.1:7897
CATALOG_PROXY ?=
CATALOG_TIMEOUT ?= 60s
CATALOG_ARGS = --whitelist ../config/symbol_mapping.yaml \
               --output ../config/instrument_catalog.yaml \
               --raw-dir docs/raw-instruments \
               --timeout $(CATALOG_TIMEOUT)
ifneq ($(strip $(CATALOG_PROXY)),)
CATALOG_ARGS += --proxy $(CATALOG_PROXY)
endif

# prune-snapshots controls. DAYS is required and must be >= 7. The script
# defaults to dry-run; only the *-confirm target adds --confirm.
DAYS ?= 30

test:
	go test ./...

lint:
	@diff=$$(gofmt -l .); \
	if [ -n "$$diff" ]; then \
		echo "gofmt found issues in:"; \
		echo "$$diff"; \
		exit 1; \
	fi
	go vet ./...

ci: lint test

# Resolve the build version from git. Used by `make build` and `make run`
# to inject the same value the production Docker image will see at link
# time. Falls back to "dev" outside a git checkout.
BUILD_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -X main.version=$(BUILD_VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/ops-intelligence ./cmd/ops-intelligence

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/ops-intelligence --role=all --addr=:$${PORT:-$(PORT)}

smoke-api:
	tmp_bin=$$(mktemp -t edgex-ops-intelligence-smoke.XXXXXX); \
	go build -o $$tmp_bin ./cmd/ops-intelligence; \
	$$tmp_bin --role=api --addr=:$(PORT) & \
	pid=$$!; \
	trap 'kill $$pid 2>/dev/null || true; rm -f $$tmp_bin' EXIT; \
	i=0; \
	until curl -fsS http://127.0.0.1:$(PORT)/api/health >/dev/null || [ $$i -ge 20 ]; do \
		i=$$((i + 1)); \
		sleep 0.5; \
	done; \
	curl -fsS http://127.0.0.1:$(PORT)/api/health; \
	curl -fsS 'http://127.0.0.1:$(PORT)/api/snapshot/liquidity?symbol=$(SYMBOL)%20%28perp%29'

smoke-all-startup:
	tmp_bin=$$(mktemp -t edgex-ops-intelligence-all-smoke.XXXXXX); \
	readiness_body=$$(mktemp -t edgex-ops-intelligence-readiness.XXXXXX); \
	go build -o $$tmp_bin ./cmd/ops-intelligence; \
	OPS_INTELLIGENCE_MYSQL_DSN="$(STARTUP_MYSQL_DSN)" $$tmp_bin --role=all --addr=:$(PORT) --mysql-dsn="$(STARTUP_MYSQL_DSN)" & \
	pid=$$!; \
	trap 'kill $$pid 2>/dev/null || true; rm -f $$tmp_bin $$readiness_body' EXIT; \
	i=0; \
	until curl -fsS http://127.0.0.1:$(PORT)/api/health >/dev/null || [ $$i -ge 30 ]; do \
		i=$$((i + 1)); \
		sleep 0.5; \
	done; \
	curl -fsS http://127.0.0.1:$(PORT)/api/health; \
	readiness_status=$$(curl -sS -o $$readiness_body -w '%{http_code}' http://127.0.0.1:$(PORT)/api/readiness); \
	cat $$readiness_body; \
	if [ "$$readiness_status" != "200" ] && [ "$$readiness_status" != "503" ]; then \
		echo "unexpected readiness status $$readiness_status"; \
		exit 1; \
	fi; \
	curl -fsS http://127.0.0.1:$(PORT)/api/collection-status >/dev/null

smoke-adapters:
	LIVE_ADAPTER_SMOKE=1 go test ./internal/adapter -run TestLiveAdapterSmoke -count=1 -v -args -symbol=$(SYMBOL)

# Hit the live CoinGecko /derivatives endpoint once and dump a triage table:
# how many tickers came back, how many platforms got mapped, and the top
# 5 24h volume entries per recognised platform. Honors COINGECKO_PROXY (set
# this to e.g. http://127.0.0.1:7897 when CoinGecko is not reachable from
# the calling environment) and COINGECKO_API_KEY_ENV (defaults to the same
# COINGECKO_DEMO_API_KEY env var the dashboard collector reads).
smoke-coingecko:
	COINGECKO_PROXY="$(COINGECKO_PROXY)" \
	COINGECKO_API_KEY_ENV="$(COINGECKO_API_KEY_ENV)" \
	go run ./cmd/coingecko-smoke

# Back-fill BACKFILL_DAYS (default 30) of per-platform daily volume from
# CoinGecko's /exchanges/{id}/volume_chart/range, converted to USD via
# /coins/bitcoin/market_chart/range. The repo config
# (`config/edgex-ops-intelligence.yaml`) Runtime.coingecko block supplies
# the proxy / api_key_env / exchange mapping; Runtime.coingecko.proxy is used
# as-is and process-level HTTPS_PROXY env vars are intentionally ignored (R1).
# Idempotent: any pre-existing live row beats a backfill row
# for the same (platform, day, display_symbol) slot.
backfill-share:
	OPS_INTELLIGENCE_MYSQL_DSN="$(MYSQL_DSN)" \
	go run ./cmd/coingecko-backfill --days=$(BACKFILL_DAYS)

# Run one full Listing Agent tick (fusion + Top30 push + delivery
# drain) against the supplied MYSQL_DSN and exit. Listing role
# requires a MySQL DSN; webhook URL is optional and unset webhook
# URLs cause Top30 outbox rows to be marked disabled instead of
# attempting network delivery.
smoke-listing:
	OPS_INTELLIGENCE_MYSQL_DSN="$(MYSQL_DSN)" \
	go run ./cmd/ops-intelligence --role=listing --run-once --mysql-dsn="$(MYSQL_DSN)"

smoke-activity:
	go run ./scripts/activity-smoke --config-dir=../config --allow-partial

# Spin up an isolated `listing-e2e` MySQL container on port 13306,
# run the build-tagged e2e Go test suite end-to-end, and tear the
# container down regardless of pass/fail. Designed to never touch
# the normal Ops Intelligence deploy stack (project name
# `edgex-ops-intelligence`, commonly on ports 3306 / 8080 / 3001).
e2e-listing:
	./e2e/run-e2e.sh

e2e-activity:
	./e2e/run-activity-e2e.sh

swagger:
	mkdir -p docs
	cp docs/openapi.json docs/swagger.json

migrate-up:
	@echo "Apply backend/migrations/*.up.sql to MySQL DSN: $(MYSQL_DSN)"
	@echo "Use: mysql '$(MYSQL_DSN)' < migrations/000001_init_schema.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000002_coingecko.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000003_widen_data_source.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000004_edgex_via_coingecko.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000005_depth_contract_fields.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000006_widen_partial_reason.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000007_snapshot_ts_indexes.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000008_drop_empty_tables.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000009_top30_unique_key.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000010_listing_agent_p1.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000011_listing_alert_state.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000012_widen_signal_fingerprint.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000013_activity_source_event.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000014_activity_event_children.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000015_activity_delivery_review.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000016_activity_indexes_postinit.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000017_activity_source_poll_state.up.sql"

migrate-down:
	@echo "Apply backend/migrations/*.down.sql to MySQL DSN: $(MYSQL_DSN)"
	@echo "Use: mysql '$(MYSQL_DSN)' < migrations/000017_activity_source_poll_state.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000016_activity_indexes_postinit.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000015_activity_delivery_review.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000014_activity_event_children.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000013_activity_source_event.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000012_widen_signal_fingerprint.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000011_listing_alert_state.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000010_listing_agent_p1.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000009_top30_unique_key.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000008_drop_empty_tables.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000007_snapshot_ts_indexes.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000006_widen_partial_reason.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000005_depth_contract_fields.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000004_edgex_via_coingecko.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000003_widen_data_source.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000002_coingecko.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000001_init_schema.down.sql"

# Regenerate config/instrument_catalog.yaml + backend/docs/raw-instruments/*.
# Run monthly (or any time an exchange ships a catalog change). Must be run
# from a network that can reach all 10 exchange APIs; pass CATALOG_PROXY
# when the build host cannot reach the exchanges directly.
catalog:
	go run ./scripts/build-catalog $(CATALOG_ARGS)

# Refresh raw-instruments snapshots only, without touching the catalog yaml.
# Use this when you only have partial network access and just want to update
# the audit trail for the exchanges you can reach.
catalog-raw:
	go run ./scripts/build-catalog $(CATALOG_ARGS) --raw-only

# CI guard: dry-run the crawler and fail if the produced catalog differs
# (semantically, excluding generated_at) from the committed file.
catalog-diff:
	go run ./scripts/build-catalog $(CATALOG_ARGS) \
	    --dry-run \
	    --diff-against ../config/instrument_catalog.yaml

# Manual snapshot retention. Default mode is dry-run: prints estimated
# row counts per table for the configured cutoff, no DELETE issued.
# *-confirm performs the deletion in batches of 10000. DAYS must be >= 7
# (the script refuses smaller windows). Snapshots older than DAYS days
# from t_orderbook_snapshot, t_book_quality_snapshot,
# t_symbol_volume_snapshot, t_collection_status,
# t_coingecko_platform_volume_snapshot are eligible.
# t_top30_snapshot is intentionally excluded — it has its own 30-day
# retention inside the collector.
prune-snapshots-dry:
	OPS_INTELLIGENCE_MYSQL_DSN="$(MYSQL_DSN)" \
	go run ./scripts/prune-snapshots --days $(DAYS)
prune-snapshots-confirm:
	OPS_INTELLIGENCE_MYSQL_DSN="$(MYSQL_DSN)" \
	go run ./scripts/prune-snapshots --days $(DAYS) --confirm

# One-off audit: HEAD/GET each frontend_url in instrument_catalog.yaml,
# write a verification report to config/url_verification_report.yaml,
# and update url_verified flags via build-catalog merge-preserve. Honors
# CATALOG_PROXY for environments behind an HTTP proxy.
verify-urls:
	go run ./scripts/verify-frontend-urls \
	    --catalog ../config/instrument_catalog.yaml \
	    --report ../config/url_verification_report.yaml \
	    $(if $(strip $(CATALOG_PROXY)),--proxy $(CATALOG_PROXY))
