# EdgeX Ops Intelligence Runbook

Operational procedures for the current EdgeX Ops Intelligence runtime. Treat
this as the canonical source for "an Ops Intelligence module is broken, what do
I do?"; escalate only when a section here does not cover the symptom.

The backend runs three active modules behind one binary:

- **Liquidity Dashboard**: collector/adapters/CoinGecko/Top30/backfill and
  `/api/snapshot/*` surfaces.
- **Listing Agent**: instrument and announcement polling, candidate fusion,
  Top30/divergence/liquidity alert cards, callback, and delivery outbox.
- **Activity Agent**: campaign source ingestion, parser, review/decision,
  delivery outbox, redrive, and `/api/activity/*` surfaces.

## 1. Topology

```
[browser] --> :3001 (Next.js web)  ---SSR-->  :8080 (backend api)
                                                 |
                                                 |---> 10 exchange REST + WS adapters
                                                 |     (binance/okx/bybit/bitget/bingx/
                                                 |      mexc/gate/hyperliquid/lighter/edgeX)
                                                 |
                                                 +---> CoinGecko (Demo API key)
                                                 +---> Listing Agent workers + Lark bots
                                                 +---> Activity Agent workers + Lark bot
                                                 +---> :3306 (MySQL persistence)
```

Single Compose project (`edgex-ops-intelligence`). All bind on 127.0.0.1 by
default; expose via a reverse proxy if external access is required.

Backend roles are selected by `--role`:

| Role | Starts | MySQL required? | Typical use |
|---|---|---:|---|
| `api` | HTTP API only | No | Local API smoke / read-only in-memory checks. |
| `collector` | Liquidity collector, live providers, CoinGecko, backfill, Listing dynamic universe helper when MySQL is present | No, degrades without Listing repository | Data collection worker. |
| `listing` | Listing Agent worker | Yes | Listing run-once / worker isolation. |
| `activity` | Activity Agent worker | Yes | Activity run-once / worker isolation. |
| `all` | API + collector + Listing Agent + Activity Agent | Listing/Activity need MySQL | Docker/production default. |

### 1.1 Deployment config and scoped proxy boundaries

`deploy/docker-compose.yaml` starts the backend with `--role=all`, so a single
deployment may run Liquidity, Listing, and Activity workers together. Operator
secrets belong in `deploy/.env`, Nacos, or a private config-dir; never paste
production webhook URLs, callback secrets, or DSNs into tracked config files.

Current env indirections:

| Setting | Runtime field that reads it |
|---|---|
| `COINGECKO_DEMO_API_KEY` | `Runtime.coingecko.api_key_env` |
| `LARK_LISTING_CALLBACK_SECRET` | `Runtime.listing_agent.decision_card.callback.secret_env` |
| `ACTIVITY_LARK_WEBHOOK_URL` | `Runtime.activity_agent.delivery.webhook_url_env` |
| `ACTIVITY_DECISION_TOKEN_SECRET` | `Runtime.activity_agent.decision_token.secret_env` |
| `LARK_LISTING_TOP30_WEBHOOK_URL` | Only when a private config sets `Runtime.listing_agent.delivery.top30_webhook_url_env` to this name. The tracked config currently routes Listing cards through `Alert.Webhooks.*`. |

Scoped proxy fields are intentionally YAML/config-dir driven:

| Chain | Runtime field |
|---|---|
| Native exchange REST, Lighter WS, and Activity source fallback | `Runtime.exchange_proxy` |
| CoinGecko derivatives ingestion | `Runtime.coingecko.proxy` |
| Per-provider WebSocket clients | `Runtime.ws_providers.*.proxy` |
| Listing Lark delivery | `Runtime.listing_agent.delivery.proxy` |
| Activity source fetches | `Runtime.activity_agent.source_proxy` |
| Activity Lark delivery | `Runtime.activity_agent.delivery.proxy` |

For local Docker on macOS, a private config-dir can point these scoped fields at
`http://host.docker.internal:7897`. In production, leave each scoped proxy blank
when the host can reach exchanges, CoinGecko, and `open.larksuite.com` directly.
Do not replace these fields with process-wide `HTTP_PROXY` / `HTTPS_PROXY`: that
would mix unrelated exchange, CoinGecko, Activity, and Lark egress paths and can
pollute latency or source-health diagnostics. `HTTP_PROXY` / `HTTPS_PROXY` in
`deploy/.env` are reserved for code paths that do not consult the runtime config.

Dashboard links embedded in Lark cards are also config-dir values, not automatic
env overrides. Set `Runtime.listing_agent.delivery.dashboard_base_url` and
`Runtime.activity_agent.delivery.dashboard_base_url` in the selected runtime
config so buttons point at the public Ops Intelligence host in production and at
the local web port during local validation.

## 2. Health and Readiness Probes

Two distinct endpoints with deliberately different semantics:

| Endpoint            | Always 200? | Purpose                                                                 |
|---------------------|-------------|-------------------------------------------------------------------------|
| `/api/health`       | Yes         | Liveness. Container HEALTHCHECK targets this. Surfaces build_version, deps.mysql ping latency, deps.catalog symbol count, goroutine count, and startup state when enabled. |
| `/api/readiness`    | No (200 or 503) | "Should this instance receive traffic?" gate. 503 when catalog is empty, MySQL ping fails, or `--role=all` has neither warm cache nor a terminal first collection. |

Why split: pointing the container HEALTHCHECK at `/api/readiness`
would put the container into a restart loop during transient upstream
exchange outages. `/api/health` always returns 200 once the process is
responsive; readiness is allowed to fail.

For the Docker/production default `--role=all`, the API starts listening before
the MySQL latest-snapshot restore, Lighter WS, and the first Liquidity Collector
cycle finish. Startup readiness uses the `cached_or_collected` policy: traffic is
allowed once either persisted warm cache was restored from MySQL
(`warm_cache.has_usable_data=true`) or the first collector cycle reaches a
terminal state (`complete`, `failed`, or `skipped`). Lighter WS is tracked as a
soft dependency under `startup.lighter_ws`; an upstream WS delay should be
visible in status output, not block liveness.

Smoke commands:

```
make -C deploy smoke              # liveness only
make -C deploy smoke-readiness    # the strict gate
make -C backend smoke-all-startup # role=all fast liveness + startup gate shape
```

`smoke-all-startup` intentionally defaults to no MySQL DSN so it can validate
fast liveness on a laptop or CI host without depending on a pre-created local
database. To exercise warm-cache readiness against a real DB, pass
`STARTUP_MYSQL_DSN='user:pass@tcp(host:3306)/db?parseTime=true'`.

## 3. Common Symptoms

### 3.1 Frontend shows "no data" for one platform

1. `make -C deploy smoke` to confirm backend is up.
2. `curl :8080/api/collection-status | jq '.[] | select(.platform=="<X>")'`
   to see the per-collector status rows.
3. Look for `"skipped: pair in cooldown after consecutive failures"` --
   the collector parks a (platform, canonical) pair after three
   consecutive failures (default; see `Runtime.cooldown_failure_threshold` in
   `config/edgex-ops-intelligence.yaml`). Cooldown clears after `Runtime.cooldown_duration` (default
   5m) or on a single successful collection.
4. Look for `"hint: api_symbol unsupported on platform"` -- the catalog
   resolver did not find a match for that (platform, canonical).
   Run the verifier and check `config/url_verification_report.yaml`.

### 3.2 Backend HEALTHCHECK keeps reporting unhealthy

Either `/api/health` itself is failing (very rare; usually OOM) or the
inner `wget` cannot reach 127.0.0.1:8080 (port mis-bind). Confirm:

```
docker exec -u 65532 edgex-ops-intelligence-backend-1 wget -qO- http://127.0.0.1:8080/api/health
```

If this works but Docker still reports unhealthy, check `interval` and
`timeout` in `deploy/docker-compose.yaml`.

### 3.2.1 Backend is healthy but readiness is 503 during `--role=all` startup

This is expected while a fresh instance has no restored snapshot and the first
collector cycle is still warming in the background. Confirm the split between
liveness and readiness:

```
curl -fsS http://127.0.0.1:8080/api/health | jq '.deps.startup'
curl -sS http://127.0.0.1:8080/api/readiness | jq '.ready, .checks.startup'
curl -fsS http://127.0.0.1:8080/api/collection-status | jq '.startup, .live_providers.lighter_ws'
```

Interpretation:

- `phase="collector_warming_up"` and `reason="collector_warming_up"` means the
  process is alive, but traffic should wait because no usable cache has been
  restored yet and the first collector cycle is not terminal.
- `latest_snapshots.state="running"` means the backend is restoring the latest
  persisted MySQL snapshots in the background. On a large local DB this can be
  slower than the API listener; it should not block `/api/health`.
- `reason="warm_cache_available"` means MySQL restored enough dashboard state
  for the API to serve while live collectors continue refreshing.
- `reason="initial_collection_completed"` means the first collector cycle has
  finished or failed explicitly. Inspect `/api/collection-status` for per-source
  `complete` / `partial` / `error` / `unsupported` details rather than treating
  readiness as proof that every upstream exchange is healthy.
- `lighter_ws.state="timeout"` means the local Lighter book did not become
  fully ready within the startup observation window. It should degrade affected
  Lighter depth rows explicitly; it should not restart the container by itself.

### 3.3 MySQL volume is filling up

V1 collects six 5-minute-cadence snapshot tables 24/7 across 10
platforms. The largest (`t_orderbook_snapshot`) accumulates >1M rows
per week. Run the prune script:

```
# Dry run first -- writes a JSON plan to ${OPS_INTELLIGENCE_DATA_DIR}/prune-history/
make -C backend prune-snapshots-dry DAYS=30

# Apply if the plan looks right
make -C backend prune-snapshots-confirm DAYS=30
```

Floor is 7 days; the script refuses anything shorter so a fat-finger
cannot wipe the same day's data.

### 3.4 Catalog drifted (an exchange renamed an instrument)

```
# Refresh raw dumps + regenerate catalog
make -C backend catalog CATALOG_PROXY=http://127.0.0.1:7897

# Probe every frontend_url and write the report
make -C backend verify-urls CATALOG_PROXY=http://127.0.0.1:7897
```

Operator review the report; commit the regenerated catalog when happy.
`--preserve-url-verified` (default) keeps approvals from prior runs.

### 3.5 0-platform canonical (BIRD / CBPS / XYZ100 / ZM / RIVN / BX / DKNG / EWZ)

These canonicals are listed in `config/unresolved_symbols.yaml` for
ops review. The backend correctly returns a payload with
supported_platform_count=0; the frontend now has an e2e regression
guard (`web/e2e/dashboard.spec.ts: zero-platform canonical renders
without crashing`) so a future config drift cannot ship a UI crash.

### 3.6 Liquidity tab "24h share" shows 0.00% (or flips to "—")

Symptom: the per-symbol Liquidity panel renders `24h share 0.00%` or
`24h share —` (with a `via CG` tag) on otherwise healthy crypto
symbols (BTC/ETH/SOL/DOGE/BNB).

Diagnosis order:

1. Check the new status field on the API surface:

   ```
   curl -fsS 'http://127.0.0.1:8080/api/snapshot/liquidity?symbol=ETH-USDT%20%28perp%29' \
     | jq '.kpis | {edgex_24h_share_pct, edgex_24h_share_status}'
   ```

   - `status = "complete"` → all 10 platforms served native ticker;
     nothing to do.
   - `status = "partial"` → at least one platform fell through to the
     CoinGecko same-day daily aggregate. edgeX is still rendered, but
     a native channel is degraded. Inspect step 2 to find which one.
   - `status = "stale"` → edgeX failed both native and CoinGecko
     today-row. The numeric share is suppressed; the UI shows `—`.
     This means CoinGecko hasn't published an edgeX per-symbol
     aggregate for the current UTC day yet, AND the native ticker is
     broken; treat as a real incident.

2. Find which native ticker is failing:

   ```
   docker compose --project-name edgex-ops-intelligence exec -T mysql \
     mysql -uroot -p"${MYSQL_ROOT_PASSWORD:-root}" edgex_ops_intelligence -e \
     "SELECT platform, status, error_message, snapshot_ts
        FROM t_symbol_volume_snapshot
       WHERE display_symbol='ETH-USDT (perp)'
         AND snapshot_ts >= NOW() - INTERVAL 1 HOUR
       ORDER BY snapshot_ts DESC LIMIT 60;"
   ```

   Look for `status = error` (transient upstream failure;
   `error_message` carries the HTTP / parse diagnostic) versus
   `status = unsupported` (catalog entry missing — different bug,
   re-run `make catalog`).

3. Most common cause: edgeX's `pro.edgex.exchange/api/v1/public/quote/getTicker`
   intermittently returns HTTP 403 with a Cloudflare interstitial
   ("Just a moment..."). The depth endpoint usually stays reachable.
   Mitigations:

   - Confirm the outbound proxy / egress IP. If the dashboard runs
     behind a shared NAT egress that Cloudflare has flagged, route
     edgeX through a clean proxy (`Runtime.exchange_proxy` in the
     main config) or move to a different egress.
   - The fallback path is automatic: while edgeX's native ticker is
     blocked, `liquidityKPIsLocked` borrows CoinGecko's per-symbol
     `/derivatives` reading (the same channel that powers the
     platform-level Share tab). The numeric KPI keeps tracking the
     correct value; only the `status` flips from `complete` to
     `partial` and the UI tags it `via CG`.

4. Note: this fallback only covers symbols CoinGecko indexes — V1
   BTC/ETH/SOL and each platform's Top60 by 24h volume. Long-tail
   synthetics on edgeX (AAPL / GOLD / META / CRCL / HOOD …) have no
   CoinGecko row, so the `status` will go `stale` if their native
   ticker breaks. For those symbols edgeX is usually the only venue,
   so a 100% share is the natural state and a `stale` reading is
   itself the alert.

### 3.7 EdgeX Perps V2 surface data is missing or stale

EdgeX Perps V2 is a separate EdgeX market surface, not a fallback for Perps V1.
When V2 rows are enabled, API/UI rows should expose enough metadata to diagnose
the surface explicitly: `platform=edgeX`, `display_platform=edgeX V2`,
`market_surface=perp_v2`, `lineage=edgeX-perp-v2`, `quote_asset=USDC`, and the
V2 `contract_id`.

Backend-only deployments remain schema-compatible with the legacy frontend
because V2 fields are additive. However, enabling V2 symbols will add additional
`platform=edgeX` rows. If the deployed frontend still assumes one EdgeX row per
symbol, keep V2 symbol rows disabled in production config until that frontend is
ready to render `display_platform` / `market_surface` explicitly.

Diagnosis order:

1. Confirm the row identity on the API surface:

   ```
   curl -fsS 'http://127.0.0.1:8080/api/snapshot/liquidity?symbol=BTC-USDT%20%28perp%29' \
     | jq '.rows[] | select(.platform=="edgeX") | {display_platform, market_surface, lineage, quote_asset, contract_id, depth_status, partial_reason, depth_source, source_id, source_endpoint}'
   ```

   A V2 row must not reuse V1 `BTCUSD` / `10000001` identity. Expected pilot
   values are `BTCUSDC` / `30000001` for BTC, `ETHUSDC` / `30000002` for ETH,
   and `SOLUSDC` / `30000003` for SOL.

2. Check collection status for V2-specific source errors:

   ```
   curl -fsS http://127.0.0.1:8080/api/collection-status \
     | jq '.rows[] | select(.platform=="edgeX" and .market_surface=="perp_v2") | {collector, display_platform, market_surface, lineage, contract_id, source_endpoint, status, error}'
   ```

   `collector=ws_orderbook`, `depth_source=ws_local_book`, and
   `source_id=edgeX-perp-v2-ws-depth-200` mean the V2 local book provider is
   serving depth from WebSocket. `collector=rest_orderbook`,
   `depth_source=rest_snapshot`, and `source_id=edgeX-perp-v2-rest-depth-200`
   mean the collector used the bounded REST snapshot fallback.

   V2 sources are:

   ```text
   wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws
   https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getDepth?contractId={id}&level=200
   https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getTicker?contractId={id}
   ```

   Runtime knobs live under `Runtime.ws_providers.edgeX_perp_v2`: `enabled`,
   `url`, `proxy`, and `stale_after`. The WS provider falls back to REST when a
   local book is not ready, stale, or detects a sequence gap. If both WS and REST
   fail, collection status must carry the concrete upstream error.

3. Interpret depth status honestly:

   - `complete` means the returned book covers the requested tier on both sides.
   - `partial` with `partial_reason=api_level_cap` means V2 REST / depth-200
     did not reach the tier; this is expected for some 1% / 2% snapshots and
     must be shown as a lower-bound, not a full book.
   - `error` / `stale` must not be replaced by V1 data or CoinGecko orderbook
     data. CoinGecko may help volume/share, never depth, spread, imbalance, or
     slippage.

4. If the row is missing entirely, check the surface-aware runtime identity
   first. V1 and V2 must not share a plain `edgeX|BTC-USDT (perp)` storage key;
   keys must include `market_surface` / `lineage` or an equivalent surface
   discriminator.

5. If WS V2 is enabled later, diagnose it separately from REST. The V2 WS host
   uses a V1 path by design:

   ```text
   wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws
   ```

   WS failures may fall back to REST lower-bound snapshots, but must not mark
   incomplete depth as strict-complete.

### 3.8 CoinGecko reports HTTP 429 rate limit

CoinGecko is governed as a finite-budget source. The fix is **not** to rely on
adding `COINGECKO_DEMO_API_KEY` for more quota; the backend must remain safe
without it.

Diagnosis order:

1. Check the governance state:

   ```
   curl -fsS http://127.0.0.1:8080/api/collection-status | jq '.coingecko'
   curl -fsS http://127.0.0.1:8080/api/ops-intelligence/meta | jq '.data_sources.coingecko.governance'
   ```

2. Interpret `state` and `cache_state`:

   - `state = healthy`, `cache_state = live` — normal live pull.
   - `state = cooling_down` — a previous 429 triggered a local cooldown. Check
     `cooldown_until`, `last_endpoint`, and `last_error`.
   - `cache_state = stale_cache` — the dashboard is serving the last derivative
     snapshot inside `stale_cache_ttl`; derived rows should surface `stale`, not
     fresh `complete`.
   - `cache_state = backfill_rate_limited` — the historical backfill yielded
     after a 429. This is expected; it will try again in a later scheduled run.
   - `cache_state = backfill_disabled` — backfill is explicitly disabled by
     `Runtime.coingecko.governance.backfill_enabled=false`.

3. Confirm the active knobs in `config/edgex-ops-intelligence.yaml`:

   ```yaml
   Runtime:
     coingecko:
       cache_ttl: 10m
       governance:
         enabled: true
         requests_per_minute: 4
         burst: 1
         default_cooldown: 15m
         max_cooldown: 1h
         stale_cache_ttl: 2h
         backfill_enabled: true
         backfill_boot_delay: 20m
         backfill_requests_per_minute: 2
         listing_coin_id_cache_ttl: 24h
         listing_market_snapshot_cache_ttl: 1h
   ```

4. If local Docker logs are noisy, do not restart-loop immediately. Wait until
   `cooldown_until`, then confirm that the next main collector tick either
   returns to `live` or continues serving `stale_cache`. Repeated 429s usually
   mean the shared egress IP is exhausted by another process; reduce
   `requests_per_minute` or temporarily set `backfill_enabled=false`.

5. If stale data is older than `stale_cache_ttl`, the collector must return an
   explicit error/stale surface rather than fabricate a successful snapshot.
   Investigate upstream access or egress proxy health before widening the TTL.

## 4. Catalog Re-generation Workflow

Cadence: monthly, plus ad-hoc when an exchange announces a relisting.

```
# 1. Refresh raw instrument dumps (writes backend/docs/raw-instruments/<platform>-<market_type>/<YYYY-MM-DD>.json)
make -C backend catalog-raw CATALOG_PROXY=http://127.0.0.1:7897

# 2. Diff vs current
make -C backend catalog-diff

# 3. Regenerate
make -C backend catalog CATALOG_PROXY=http://127.0.0.1:7897

# 4. Probe frontend URLs
make -C backend verify-urls CATALOG_PROXY=http://127.0.0.1:7897

# 5. Commit
```

## 5. Release Procedure

```
# 1. CI gate
cd backend && make ci

# 2. Frontend gate
cd web && npm run typecheck && npm run lint && npm run build && npm run test:e2e

# 3. Build production image with embedded version
cd deploy && make build-image

# 4. Smoke the embedded version
cd deploy && make test-image

# 5. Deploy
cd deploy && cp .env.production.template .env  # first time only
make up

# 6. Verify
make smoke && make smoke-readiness

# 7. Optional module smoke checks
cd ../backend && make smoke-listing && make smoke-activity
```

For Docker/Compose E2E, run the web check with the Compose port:

```
cd web && PLAYWRIGHT_BASE_URL=http://127.0.0.1:3001 npm run test:e2e
```

## 5.1 Listing Agent checks

Use these checks when `/api/listing/*`, Top30 hot-gap cards, divergence cards,
liquidity alerts, or decision callbacks are suspected:

```
# API/worker run-once against a MySQL DSN; unset webhooks mark outbox rows disabled.
make -C backend smoke-listing MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true'

# Inspect source health and deliveries.
curl -fsS 'http://127.0.0.1:8080/api/listing/source-health' | jq
curl -fsS 'http://127.0.0.1:8080/api/listing/deliveries?limit=20' | jq
```

Webhook routing uses `Alert.Webhooks.Listing` for Top30/divergence cards and
`Alert.Webhooks.Liquidity` for Dashboard liquidity-lag / worst-depth cards.
The legacy `Alert.WebHookP3` is a Listing fallback only.

## 5.2 Activity Agent checks

Use these checks when `/api/activity/*`, campaign ingestion, source health,
review/decision links, or Activity Lark delivery are suspected:

```
# Parser/source smoke; allows partial upstream failures for WAF/network drift.
make -C backend smoke-activity

# Inspect source health and deliveries.
curl -fsS 'http://127.0.0.1:8080/api/activity/source-health' | jq
curl -fsS 'http://127.0.0.1:8080/api/activity/deliveries?limit=20' | jq
```

Activity delivery uses `Alert.Webhooks.Activity` or the configured
`ACTIVITY_LARK_WEBHOOK_URL`; decision links require
`ACTIVITY_DECISION_TOKEN_SECRET` so stale/forged decisions can be rejected.

## 6. Backup and Restore

```
# Backup (writes ./backups/<RFC3339>.sql.gz)
make -C deploy backup-mysql

# Restore
make -C deploy restore-mysql FILE=backups/2026-05-23T10-00-00Z.sql.gz
```

`mysqldump --single-transaction` keeps the dump consistent without
locking writers; the CR remains observable during the dump.

## 7. Invariants (DO NOT VIOLATE)

- **EdgeX BTC/ETH/SOL must be live data**. Never fabricate; use the
  real adapter at all times.
- **Lighter depth must come from the WS local order book** with the
  per-book `staleAfter` window. Synthesising from REST is forbidden.
- **MEXC ×0.4 / Gate ×0.5 is volume_share only**. Never apply these
  multipliers to depth, slippage, spread, or any other quality
  metric.
- **No fabricated data**. If an adapter has no answer, surface
  `unsupported` or `error`; never substitute zeros or placeholder
  numbers.

## 8. Glossary

- **canonical**: the stable business key (BTC, GOLD, TSLA, URNM).
  Independent of any one exchange's symbol scheme.
- **api_symbol**: the platform-specific tradable identifier (BTCUSDT,
  BTC-USD-SWAP, NCSKTSLAU2USD).
- **frontend_url**: the click-through URL in the dashboard that takes
  an operator to the exchange's trading page for that symbol.
- **url_verified**: operator approval flag. Set by
  `verify-frontend-urls --apply` after a successful HEAD/GET probe.
- **cooldown**: per-(platform, canonical) backoff after N consecutive
  failures. Pairs are skipped (with explicit status row) until the
  cooldown expires or a single success clears the counter.
- **staleness_by_category**: per-asset_category staleness threshold.
  crypto:30s, commodity:300s, stock:600s, index_etf:600s; configurable
  via `config/edgex-ops-intelligence.yaml`.
