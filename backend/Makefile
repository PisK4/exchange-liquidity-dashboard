.PHONY: test run smoke-api smoke-adapters swagger migrate-up migrate-down catalog catalog-raw catalog-diff lint ci

PORT ?= 18080
SYMBOL ?= BTC-USDT
MYSQL_DSN ?= root:root@tcp(127.0.0.1:3306)/edgex_dashboard

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

swagger:
	mkdir -p docs
	cp docs/openapi.json docs/swagger.json

migrate-up:
	@echo "Apply backend/migrations/000001_init_schema.up.sql to MySQL DSN: $(MYSQL_DSN)"
	@echo "Use: mysql '$(MYSQL_DSN)' < migrations/000001_init_schema.up.sql"

migrate-down:
	@echo "Apply backend/migrations/000001_init_schema.down.sql to MySQL DSN: $(MYSQL_DSN)"
	@echo "Use: mysql '$(MYSQL_DSN)' < migrations/000001_init_schema.down.sql"

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
