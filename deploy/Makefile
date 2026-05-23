# Operator-facing wrapper around docker compose. Run from this
# directory: `cd deploy && make up`.
#
# COMPOSE_PROJECT_NAME deliberately namespaces these containers as
# `edgex-dashboard-*` so they can coexist on the same host as a
# developer's `deploy-*` containers without conflicting on names or the
# default network.

.PHONY: help up down logs ps smoke smoke-readiness backup-mysql restore-mysql build-image test-image

PROJECT_NAME ?= edgex-dashboard
COMPOSE = docker compose --project-name $(PROJECT_NAME)

# BUILD_VERSION is forwarded into the Dockerfile as a build arg so the
# resulting image has the same `git describe` string the local `make
# build` would embed in the binary. Defaults to "dev" outside a git
# checkout.
BUILD_VERSION ?= $(shell cd .. && git describe --tags --always --dirty 2>/dev/null || echo dev)

help:
	@echo "EdgeX Dashboard deploy targets:"
	@echo "  make up              - bring up MySQL + backend + web in detached mode"
	@echo "  make down            - stop and remove containers (keeps mysql-data volume)"
	@echo "  make logs            - tail backend + web logs"
	@echo "  make ps              - list running containers"
	@echo "  make smoke           - curl /api/health on the published backend port"
	@echo "  make smoke-readiness - curl /api/readiness, fails on non-200"
	@echo "  make backup-mysql    - mysqldump to ./backups/<timestamp>.sql.gz"
	@echo "  make restore-mysql FILE=<path> - restore a mysqldump archive"
	@echo "  make build-image     - rebuild backend image with the current git version"
	@echo "  make test-image      - run the backend image's --version check"

up:
	BUILD_VERSION=$(BUILD_VERSION) $(COMPOSE) up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=200 backend web

ps:
	$(COMPOSE) ps

# `smoke` is liveness; should pass even on a fresh start before any
# data has been collected. `smoke-readiness` is the stricter gate that
# expects a populated catalog and a healthy MySQL.
smoke:
	@PORT=$${BACKEND_PUBLISH_PORT:-8080}; \
	HOST=$${BACKEND_PUBLISH_HOST:-127.0.0.1}; \
	curl -fsS http://$$HOST:$$PORT/api/health | head -200

smoke-readiness:
	@PORT=$${BACKEND_PUBLISH_PORT:-8080}; \
	HOST=$${BACKEND_PUBLISH_HOST:-127.0.0.1}; \
	curl -fsS http://$$HOST:$$PORT/api/readiness | head -200

backup-mysql:
	@mkdir -p backups
	@TS=$$(date -u +%Y%m%dT%H%M%SZ); \
	OUT=backups/$$TS.sql.gz; \
	echo "writing $$OUT"; \
	$(COMPOSE) exec -T mysql mysqldump \
	    -uroot -p$${MYSQL_ROOT_PASSWORD:-root} \
	    --single-transaction \
	    --routines \
	    --triggers \
	    $${MYSQL_DATABASE:-edgex_dashboard} | gzip > $$OUT; \
	ls -lh $$OUT

restore-mysql:
	@if [ -z "$(FILE)" ]; then echo "FILE=<path> is required"; exit 1; fi
	gunzip -c $(FILE) | $(COMPOSE) exec -T mysql mysql \
	    -uroot -p$${MYSQL_ROOT_PASSWORD:-root} $${MYSQL_DATABASE:-edgex_dashboard}

build-image:
	BUILD_VERSION=$(BUILD_VERSION) $(COMPOSE) build backend

# Spins up the backend image in isolation and prints the embedded
# version. Used by CI to confirm BUILD_VERSION made it through the
# Dockerfile -ldflags injection.
test-image:
	BUILD_VERSION=$(BUILD_VERSION) $(COMPOSE) build backend
	$(COMPOSE) run --rm --no-deps backend /app/dashboard --version
