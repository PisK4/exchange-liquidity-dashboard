#!/usr/bin/env sh
set -eu

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

curl -fsS "$BASE_URL/api/health" >/dev/null
curl -fsS "$BASE_URL/api/symbols" >/dev/null
curl -fsS "$BASE_URL/api/symbols/coverage" >/dev/null
curl -fsS "$BASE_URL/api/snapshot/liquidity?symbol=BTC-USDT%20%28perp%29" >/dev/null
curl -fsS "$BASE_URL/api/snapshot/quality?symbol=BTC-USDT%20%28perp%29" >/dev/null
curl -fsS "$BASE_URL/api/snapshot/share?window=24h" >/dev/null
curl -fsS "$BASE_URL/api/snapshot/share?window=7d" | grep -q insufficient_history
curl -fsS "$BASE_URL/api/snapshot/top30?surface=perp&platform=binance" | grep -q insufficient_history
curl -fsS "$BASE_URL/api/collection-status" >/dev/null
curl -fsS "$BASE_URL/api/runtime-config" >/dev/null

echo "API smoke passed for $BASE_URL"
