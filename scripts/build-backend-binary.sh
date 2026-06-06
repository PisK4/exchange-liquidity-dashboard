#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-arm64}"

mkdir -p "$ROOT_DIR/backend/bin"
cd "$ROOT_DIR/backend"

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
  GOCACHE="${GOCACHE:-/private/tmp/edgex-ops-intelligence-go-build}" \
  GOMODCACHE="${GOMODCACHE:-/private/tmp/edgex-ops-intelligence-go-mod}" \
  go build -o "$ROOT_DIR/backend/bin/ops-intelligence" ./cmd/ops-intelligence

echo "Built backend/bin/ops-intelligence for $GOOS/$GOARCH"
