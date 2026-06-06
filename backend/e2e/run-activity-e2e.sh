#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.activity-e2e.yaml"
PROJECT="activity-e2e"
DSN='root:root@tcp(127.0.0.1:13307)/edgex_dashboard_activity_e2e?parseTime=true&loc=UTC&multiStatements=true'

cleanup() {
  echo "==> tearing down ${PROJECT}"
  docker compose -p "${PROJECT}" -f "${COMPOSE_FILE}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> starting ${PROJECT} stack"
docker compose -p "${PROJECT}" -f "${COMPOSE_FILE}" up -d

echo "==> waiting for mysql to become healthy"
deadline=$((SECONDS + 90))
while true; do
  status="$(docker inspect -f '{{.State.Health.Status}}' activity-e2e-mysql 2>/dev/null || echo starting)"
  if [[ "${status}" == "healthy" ]]; then
    break
  fi
  if (( SECONDS > deadline )); then
    echo "mysql did not become healthy in 90s (status=${status})" >&2
    docker compose -p "${PROJECT}" -f "${COMPOSE_FILE}" logs mysql | tail -50 >&2 || true
    exit 1
  fi
  sleep 1
done

echo "==> running go test -tags=activity_e2e ./e2e/..."
cd "${BACKEND_DIR}"
ACTIVITY_E2E_MYSQL_DSN="${DSN}" go test -tags=activity_e2e -count=1 -v ./e2e/...
