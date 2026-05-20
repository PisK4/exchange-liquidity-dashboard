.PHONY: test run smoke-api smoke-adapters smoke-coingecko backfill-share swagger migrate-up migrate-down catalog catalog-raw catalog-diff lint ci

PORT ?= 18080
SYMBOL ?= BTC-USDT
MYSQL_DSN ?= root:root@tcp(127.0.0.1:3306)/edgex_dashboard?parseTime=true
BACKFILL_DAYS ?= 30
COINGECKO_API_KEY_ENV ?= COINGECKO_DEMO_API_KEY
COINGECKO_PROXY ?=

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

run:
	go run ./cmd/dashboard --role=all --addr=:$${PORT:-$(PORT)}

smoke-api:
	tmp_bin=$$(mktemp -t edgex-dashboard-smoke.XXXXXX); \
	go build -o $$tmp_bin ./cmd/dashboard; \
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
# /coins/bitcoin/market_chart/range. The runtime.yaml CoinGecko block
# supplies the proxy / api_key_env / exchange mapping; runtime.yaml.proxy is
# used as-is and process-level HTTPS_PROXY env vars are intentionally
# ignored (R1). Idempotent: any pre-existing live row beats a backfill row
# for the same (platform, day, display_symbol) slot.
backfill-share:
	DASHBOARD_MYSQL_DSN="$(MYSQL_DSN)" \
	go run ./cmd/coingecko-backfill --days=$(BACKFILL_DAYS)

swagger:
	mkdir -p docs
	cp docs/openapi.json docs/swagger.json

migrate-up:
	@echo "Apply backend/migrations/*.up.sql to MySQL DSN: $(MYSQL_DSN)"
	@echo "Use: mysql '$(MYSQL_DSN)' < migrations/000001_init_schema.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000002_coingecko.up.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000003_widen_data_source.up.sql"

migrate-down:
	@echo "Apply backend/migrations/*.down.sql to MySQL DSN: $(MYSQL_DSN)"
	@echo "Use: mysql '$(MYSQL_DSN)' < migrations/000003_widen_data_source.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000002_coingecko.down.sql"
	@echo "Then: mysql '$(MYSQL_DSN)' < migrations/000001_init_schema.down.sql"

# Regenerate config/instrument_catalog.yaml + backend/docs/raw-instruments/*.
# Run monthly (or any time an exchange ships a catalog change). Must be run
# from a network that can reach all 10 exchange APIs.
catalog:
	go run ./scripts/build-catalog \
	    --whitelist ../config/symbol_mapping.yaml \
	    --output ../config/instrument_catalog.yaml \
	    --raw-dir docs/raw-instruments

# Refresh raw-instruments snapshots only, without touching the catalog yaml.
# Use this when you only have partial network access and just want to update
# the audit trail for the exchanges you can reach.
catalog-raw:
	go run ./scripts/build-catalog \
	    --whitelist ../config/symbol_mapping.yaml \
	    --raw-dir docs/raw-instruments \
	    --raw-only

# CI guard: dry-run the crawler and fail if the produced catalog differs
# (semantically, excluding generated_at) from the committed file.
catalog-diff:
	go run ./scripts/build-catalog \
	    --whitelist ../config/symbol_mapping.yaml \
	    --raw-dir docs/raw-instruments \
	    --dry-run \
	    --diff-against ../config/instrument_catalog.yaml
