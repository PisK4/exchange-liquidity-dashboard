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
| `LARK_LISTING_TOP30_WEBHOOK_URL` | Only when a private config sets `Runtime.listing_agent.delivery.top30_webhook_url_env` to this name. The tracked config currently routes Listing cards through `Alert.Push.Listing`, mirrored to legacy `Alert.Webhooks.Listing` for compatibility. |
| `LARK_ACTIVITY_WEBHOOK_URL` | Only when the selected runtime config sets `Runtime.activity_agent.delivery.webhook_url_env` to this name. Direct `Runtime.activity_agent.delivery.webhook_url` and `Alert.Push.Activity` values still work. |
| `ACTIVITY_DECISION_TOKEN_SECRET` | Only when the selected runtime config sets `Runtime.activity_agent.decision_token.secret_env` to this name. Direct `Runtime.activity_agent.decision_token.secret` still wins when non-empty. |

Business webhook routes should use `Alert.Push.Listing`,
`Alert.Push.Activity`, and `Alert.Push.Liquidity` in the selected YAML/Nacos
config. `Alert.Webhooks.*` remains a legacy compatibility input only; when
`Alert.Push.*` is set, it wins. Activity decision-token values can be provided
either as direct YAML/Nacos values, AWS Secrets Manager-resolved values, or
through env indirection. For env indirection, keep the tracked fields empty and
point `Runtime.activity_agent.delivery.webhook_url_env` and
`Runtime.activity_agent.decision_token.secret_env` at names supplied by
`deploy/.env` or the process environment. Do not commit production webhook URLs
or decision-token secrets to tracked config files.

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

### 1.2 MySQL schema bootstrap and manual migrations

When a MySQL DSN is configured, backend startup opens MySQL and runs
`collector.ApplyMigrations(db)` before repositories and workers are attached.
This startup step is fail-fast: if the configured database cannot be reached or
schema bootstrap fails, the process exits instead of running workers against an
unknown schema.

Important implementation details:

- `collector.ApplyMigrations` does **not** read `backend/migrations/*.sql`.
- It executes the compiled `initSchemaSQL` string in
  `backend/internal/collector/mysql_store.go`.
- It then runs `applySchemaPostInit`, which performs a small set of
  idempotent `INFORMATION_SCHEMA`-guarded repairs for existing databases.
- `backend/schema/ops-intelligence-schema.sql` is a human-reviewable
  consolidated fresh-database schema snapshot. Keep it in sync with
  `initSchemaSQL`, `applySchemaPostInit`, and the latest migration number.
- `backend/migrations/*.sql` are manual/auditable chronological scripts.
  `make -C backend migrate-up` and `make -C backend migrate-down` print
  operator-facing command-order references; they do not execute SQL.

Fresh database behavior:

- A fresh schema can be bootstrapped by starting the backend with
  `OPS_INTELLIGENCE_MYSQL_DSN` configured, or by explicitly applying
  `backend/schema/ops-intelligence-schema.sql` to an already selected empty
  database.
- The MySQL database itself must still exist first. Docker can create it on an
  empty volume through `MYSQL_DATABASE`; otherwise create it manually.

Existing database behavior:

- `CREATE TABLE IF NOT EXISTS` will not widen columns, add missing columns, or
  add indexes to pre-existing tables.
- The post-init path currently guards the Listing signal fingerprint width,
  Listing metric fallback indexes, and Activity source/bootstrap timestamp
  columns.
- For historical database upgrades, review `backend/migrations/*.sql` and apply
  missing migrations explicitly in order, after a backup and schema audit.

Before production schema work:

1. Take a MySQL backup.
2. Compare `ls backend/migrations/*.up.sql | sort` with
   `make -C backend migrate-up` output.
3. Verify the active schema with `INFORMATION_SCHEMA.COLUMNS` and
   `INFORMATION_SCHEMA.STATISTICS`.
4. Restart and check `/api/health` plus `/api/readiness`.

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

### 2.1 Metrics endpoint

The backend exposes Prometheus metrics on the main API port:

```bash
curl -fsS http://127.0.0.1:8080/metrics
```

The first metrics set is intentionally small and process-local:

- `edgex_ops_http_requests_total`
- `edgex_ops_http_server_duration_milliseconds`
- `edgex_ops_log_count_total` for log events emitted through the new
  metrics-aware logger facade

`/metrics` is a scrape endpoint, not a liveness or readiness probe. Keep Docker
and load-balancer liveness pointed at `/api/health`, and keep traffic gating on
`/api/readiness`. The HTTP middleware skips `/metrics` itself to avoid scrape
traffic polluting request latency metrics.

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

### 3.3.1 Listing decision-card metrics still show `n/a` / `不可用`

Decision-card metric enrichment is best-effort and must expose missing data
explicitly instead of fabricating successful values. The compact card body keeps
the stable labels `Market Cap`, `Spot 24h Vol`, `现货深度`, and `合约深度`; the
footer `metrics=` fragment carries the source/status shorthand:

| Footer source | Meaning |
|---|---|
| `ext` | External token-level market data, currently CoinGecko `/coins/markets`. |
| `live` | Live reference-venue depth check, controlled by `Runtime.listing_agent.decision_card.metric_enrichment.reference_platforms` (default `binance`). |
| `snap` | Local MySQL snapshot fallback (`t_orderbook_snapshot` or spot-only `t_symbol_volume_snapshot`). |

| Footer status | Meaning |
|---|---|
| `ok` | Metric value is available and rendered in the card body. |
| `not_found` | The preferred external/token-level source did not find the token. |
| `unsupported` | The metric is not supported for the current source or market surface. |
| `stale` | The newest local snapshot is older than `metric_enrichment.stale_after`. |
| `source_error` | The live or external source failed before producing a valid metric. |
| `no_snapshot` | The local DB fallback had no fresh usable snapshot rows. |

If a card still renders `n/a` / `不可用`, check in this order:

1. Confirm CoinGecko governance is not exhausted if both `Market Cap` and
   `Spot 24h Vol` are missing. CoinGecko remains the preferred source for token
   market cap and all-market spot 24h volume.
2. Confirm `Runtime.listing_agent.decision_card.metric_enrichment` in the
   active config. `reference_platforms` should include at least one depth venue
   supported by the live fetcher (currently Binance), `depth_tier_pct` defaults
   to `0.001`, and `stale_after` defaults to `30m`.
3. Confirm the DB fallback indexes exist before relying on snapshot fallback:

```
MYSQL_DB=${MYSQL_DATABASE:-edgex_ops_intelligence}

docker compose -f deploy/docker-compose.yaml exec mysql \
  mysql -uroot -proot "${MYSQL_DB}" -N -e \
  "SELECT TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX, COLUMN_NAME
     FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND INDEX_NAME IN ('idx_orderbook_canonical_surface_tier_latest',
                         'idx_symbol_volume_canonical_surface_latest')
    ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX;"
```

Legacy local Docker volumes may still use `edgex_dashboard`; verify with
`SHOW DATABASES` before running the check if the backend was originally booted
from the historical compose stack.

The expected indexes are:

- `idx_orderbook_canonical_surface_tier_latest (canonical_symbol, market_surface, tier, platform, snapshot_ts)` for spot/perp depth fallback.
- `idx_symbol_volume_canonical_surface_latest (canonical_symbol, market_surface, platform, snapshot_ts)` for Spot 24h Vol snapshot fallback.

These indexes can arrive through two supported paths:

- backend startup: `applySchemaPostInit` guards and creates the two metric
  fallback indexes when missing;
- manual migration workflow:
  `backend/migrations/000021_listing_metric_snapshot_indexes.up.sql`.

Fresh databases get the base tables from compiled `initSchemaSQL`, then the
same startup post-init guard creates these indexes if they are not already in
the table definition. For large local/prod databases, run `EXPLAIN` on the
candidate-specific snapshot queries and verify the `key` column uses the two
indexes above. Spot 24h Vol fallback intentionally filters
`market_surface='spot'`; do not include perp rows to inflate the card metric.

### 3.4 Catalog drifted (an exchange renamed an instrument)

```
# Refresh raw dumps + regenerate catalog
make -C backend catalog CATALOG_PROXY=http://127.0.0.1:7897

# Probe every frontend_url and write the report
make -C backend verify-urls CATALOG_PROXY=http://127.0.0.1:7897
```

Operator review the report; commit the regenerated catalog when happy.
`--preserve-url-verified` (default) keeps approvals from prior runs.

### 3.4.1 Listing listed_universe reports schema_drift

The synthetic sources `listing/listed_universe/<platform>` describe the
runtime universe refresh, not the parser health of the underlying exchange
source. A `schema_drift` row means the refresh failed closed to the seed
universe because the DB-derived active universe looked too small for the
selected baseline.

Check the decision context first:

```
docker exec deploy-mysql-1 mysql -uroot -proot edgex_dashboard -e \
  "SELECT source_key, status, last_error,
          JSON_UNQUOTE(JSON_EXTRACT(source_context_json,'$.baseline_type')) AS baseline_type,
          JSON_EXTRACT(source_context_json,'$.db_fresh_active_count') AS db_count,
          JSON_EXTRACT(source_context_json,'$.previous_success_db_fresh_active_count') AS previous_success,
          JSON_EXTRACT(source_context_json,'$.seed_count') AS seed_count,
          JSON_EXTRACT(source_context_json,'$.threshold') AS threshold,
          JSON_EXTRACT(source_context_json,'$.surface_counts') AS surface_counts,
          updated_at
     FROM t_listing_source_state
    WHERE source_key LIKE 'listing/listed_universe/%'
    ORDER BY updated_at DESC;"
```

Interpretation:

- `baseline_type=previous_success` means the current DB count is compared with
  the previous successful DB-derived count. A failure here is a real runtime
  shrink signal unless the previous context was manually corrupted.
- `baseline_type=seed_floor` means this platform is still in cold-start / seed
  safety-net mode. The seed is a historical/full-market fallback, so compare
  `surface_counts` before assuming the instrument source is broken.
- `baseline_type=db_first_bootstrap` applies to seed-wide platforms such as
  `edgeX`, `hyperliquid`, and `mexc`; once `bootstrap_min_count` is met, the
  DB runtime universe is accepted and future refreshes use previous-success
  baseline instead of the wide seed.

If `db_count` is unexpectedly low, verify snapshot freshness:

```
docker exec deploy-mysql-1 mysql -uroot -proot edgex_dashboard -e \
  "SELECT platform, market_type, market_surface, status_normalized,
          COUNT(*) AS rows, COUNT(DISTINCT base_asset) AS bases,
          MAX(last_seen_at) AS last_seen
     FROM t_listing_instrument_snapshot
    GROUP BY platform, market_type, market_surface, status_normalized
    ORDER BY platform, market_type, market_surface, status_normalized;"
```

Do not clear `schema_drift` manually unless the DB snapshot and runtime file are
also healthy. A successful next refresh will write `status=ok` and replace
`source_context_json` with the accepted decision evidence.

### 3.4.2 Listing decision card shows an old exchange listing time

The Listing Agent keeps exchange-side `listing_time_ts` values from native
instrument APIs for audit and card context. A venue can legitimately return a
launch/open timestamp that is weeks or months old when the local baseline first
observes the symbol. Those late discoveries must not become new listing pushes.

Current gate:

- Config key: `Runtime.listing_agent.candidate.historical_listing_grace_period`.
- Tracked default: `48h` in `config/edgex-ops-intelligence.yaml`.
- Fusion behavior: candidate-shaped `instrument_diff` / `announcement_listing`
  signals whose `listing_time_ts <= observed_at - grace_period` are marked
  fused and counted as `SkippedHistorical`; they remain in
  `t_listing_signal_observation` but do not create/update candidates or link to
  existing actionable candidates.
- Decision-card behavior: the producer re-checks linked evidence and skips a
  candidate when no fresh candidate-promoting signal remains. Dedupe and
  exchange listing time selection use only fresh evidence.
- Card time semantics: `Detected Time` is the primary card field; a venue
  launch/open timestamp is shown separately as `Exchange Listing Time` when
  present.

If operators report a BP/SPCX-like card where the exchange listing time is old,
check whether the outbox row was created before this gate was deployed, then
inspect linked signals:

```
SELECT s.id, s.signal_type, s.signal_subtype, s.source_platform,
       s.status_normalized, s.observed_at, s.listing_time_ts,
       TIMESTAMPDIFF(HOUR, s.listing_time_ts, s.observed_at) AS age_hours
FROM t_listing_candidate_signal cs
JOIN t_listing_signal_observation s ON s.id = cs.signal_id
WHERE cs.candidate_id = <candidate_id>
ORDER BY s.observed_at DESC;
```

Tuning impact: increasing the grace period admits older exchange-side listing
evidence as fresh and can raise push volume; decreasing it suppresses late
discoveries more aggressively. Keep it large enough to cover expected poll or
deployment gaps, but short enough to avoid treating historical venue metadata as
a new listing opportunity.

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
     | jq '.rows[] | select(.platform=="edgeX" and .market_surface=="perp_v2") | {collector, display_platform, market_surface, lineage, contract_id, depth_source, source_id, source_endpoint, status, error}'
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

5. Diagnose the active WS local-book path separately from REST. The V2 WS host
   uses a V1 path by design:

   ```text
   wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws
   ```

   If V2 rows keep reporting `rest_orderbook` / `rest_snapshot` while
   `Runtime.ws_providers.edgeX_perp_v2.enabled=true` and the WS endpoint is
   reachable, treat it as a WS local-book regression. Check the V2 WS parser
   for real `quote-event.content` frames, sequence gaps, `stale_after`, proxy
   connectivity, and `edgeX perp v2 ws fallback to REST` logs before accepting
   REST as the steady state. WS failures may fall back to REST lower-bound
   snapshots, but must not mark incomplete depth as strict-complete.

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

## 5.1 Activity Agent checks

Activity ingestion wakes on the scheduler interval, but each source obeys its
own poll interval. The tracked default Activity poll interval is 30 minutes for
all configured sources, including Lighter. Confirm the effective runtime state
from MySQL rather than guessing from the config file baked into a Docker image:

```
docker exec deploy-mysql-1 mysql -uroot -proot edgex_dashboard -e \
  "SELECT platform, source_group, source_status, last_http_status,
          COALESCE(last_error_kind,'') AS last_error_kind,
          poll_interval_seconds, last_checked_at, last_success_at,
          JSON_UNQUOTE(JSON_EXTRACT(source_context_json,'$.attempt_count')) AS attempt_count,
          JSON_UNQUOTE(JSON_EXTRACT(source_context_json,'$.elapsed_ms')) AS elapsed_ms,
          JSON_UNQUOTE(JSON_EXTRACT(source_context_json,'$.proxy_used')) AS proxy_used,
          TIMESTAMPDIFF(MINUTE,last_checked_at,UTC_TIMESTAMP()) AS checked_min_ago
     FROM t_activity_source_state
    ORDER BY platform, source_group;"
```

Expected healthy shape:

- `source_status=ok` and `last_http_status=200` for reachable sources.
- `/api/activity/source-health` exposes both `status` and `source_status`;
  `status` is a compatibility alias so generic health tooling does not show a
  null status.
- `last_checked_at` means the source was evaluated by ingestion; `last_success_at`
  means the most recent successful fetch/parse or unchanged 2xx sample. A source
  can be `degraded` while still having a recent historical success.
- `poll_interval_seconds=1800` for the default 30 minute cadence.
- `source_context_json` carries fetch diagnostics such as `attempt_count`,
  `elapsed_ms`, `proxy_used`, `source_url`, and `fetch_mode`. Fetch failures
  also set `last_error_message` so Binance/BingX-style intermittent network
  issues can be separated from parser or delivery failures.
- Worker summaries include `SkipReasons` when sources are skipped before fetch,
  for example `SkipReasons:map[poll_interval:9]`. This is expected between
  scheduled polls; `disabled_config` and `disabled_until` require operator
  attention.

The ingestion path intentionally suppresses repeated raw/event writes when a
successful 2xx fetch returns the same `content_hash` as the previous successful
sample. In that case the worker updates `t_activity_source_state` only and the
run summary increments `UnchangedSources`; it does not insert a new
`t_activity_raw_evidence` row, parse the payload, or upsert
`t_activity_event`. Non-2xx responses are still persisted as raw evidence for
diagnostics even when the content hash is unchanged.

Useful log filter:

```
docker logs --since=10m deploy-backend-1 2>&1 \
  | rg -i "activity|fetch_error|parser_error|delivery.*failed|panic|fatal"
```

Useful delivery backlog check:

```
docker exec deploy-mysql-1 mysql -uroot -proot edgex_dashboard -e \
  "SELECT target_channel, status, COUNT(*) AS cnt
     FROM t_activity_delivery_outbox
    GROUP BY target_channel,status
    ORDER BY target_channel,status;"
```

Only `sent` rows means delivery has no pending/retry backlog. Pending or retry
rows should be diagnosed together with `t_activity_delivery_attempt`, backend
logs, and the Lark webhook response body; a webhook 200 alone is not enough
evidence that delivery succeeded end-to-end.

## 5.2 Listing Agent checks

Use these checks when `/api/listing/*`, Top30 hot-gap cards, divergence cards,
liquidity alerts, or decision callbacks are suspected:

```
# API/worker run-once against a MySQL DSN; unset webhooks mark outbox rows disabled.
make -C backend smoke-listing MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true'

# Inspect source health and deliveries.
curl -fsS 'http://127.0.0.1:8080/api/listing/source-health' | jq
curl -fsS 'http://127.0.0.1:8080/api/listing/deliveries?limit=20' | jq
```

Rows with `source_type=listed_universe_refresh` are synthetic health rows for
the dynamic listed-universe safety net. A `schema_drift` status on these rows
means the DB-derived active base list fell below the configured shrink floor and
the runtime universe fell back to the seed list; it is not the same as a parser
schema drift from an instrument or announcement fetcher. When a platform later
passes the DB-derived shrink-floor check, the synthetic row is written back to
`ok` so old fallback errors do not remain as active problems.

For Hyperliquid announcements, `unexpected EOF` usually indicates a CloudFront,
proxy, or transport truncation. The fetcher reports the URL, stage, payload byte
count, and attempt count in `last_error` so operators can distinguish transport
truncation from parser-level schema drift.

MEXC ticker volume uses quote-volume fields first (`amount24`, `turnover24`,
`quoteVolume`, `usdtVolume`) and falls back to base volume times a usable price.
A real zero volume is preserved as a complete zero-volume sample; missing fields
or negative values remain explicit adapter errors. The MEXC 0.4 discount still
applies only to volume/share, never to depth, spread, or slippage.

Webhook routing uses `Alert.Webhooks.Listing` for Top30/divergence cards and
`Alert.Webhooks.Liquidity` for Dashboard liquidity-lag / worst-depth cards.
The legacy `Alert.WebHookP3` is a Listing fallback only.

### 5.2.1 Listing symbol identity and safe backfill

Listing Agent keeps two symbol layers:

- Native exchange fields (`api_symbol`, `base_asset`, `quote_asset`) stay as the
  venue exposes them. For example MEXC may report `api_symbol=EBAYSTOCK_USDT`
  and `base_asset=EBAYSTOCK`.
- Business identity fields (`canonical_symbol`, `display_symbol`,
  `market_surface`, `instrument_kind`) are normalized through
  `config/symbol_mapping.yaml` aliases at runtime. For the same MEXC contract,
  the expected identity is `canonical_symbol=EBAY`,
  `display_symbol=EBAY-USDT (perp)`, `market_surface=synthetic_futures`, and
  `instrument_kind=synthetic`.

`listed_universe` is identity-aware. Do not remove synthetic/proxy/RWA rows from
the universe just because they are not plain crypto perps; Listing cards may be
valid for those instruments. The already-listed check uses
`canonical_symbol + market_surface + instrument_kind` when runtime entries are
available, and only falls back to legacy base-only matching for old seed-only
platform files.

When adding a new venue alias, update `config/symbol_mapping.yaml`. Exact aliases
are preferred over broad suffix stripping: add `mexc: [EBAYSTOCK]` for EBAY, not
a generic `*STOCK` rule that could collapse an unrelated crypto symbol. Runtime
normalization uses the same alias source of truth as catalog generation.

For a larger cleanup, generate an alias gap report before editing config or
data. At minimum, inspect active synthetic/native-looking rows and already-sent
card payloads so operators can review one exact mapping at a time:

```sql
SELECT platform, canonical_symbol, display_symbol, base_asset, api_symbol,
       market_surface, instrument_kind, COUNT(*) AS rows, MAX(last_seen_at) AS last_seen
  FROM t_listing_instrument_snapshot
 WHERE canonical_symbol LIKE '%STOCK'
    OR base_asset LIKE '%STOCK'
 GROUP BY platform, canonical_symbol, display_symbol, base_asset, api_symbol,
          market_surface, instrument_kind
 ORDER BY platform, canonical_symbol;

SELECT id, status, created_at, JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.card.header.title.content')) AS title
  FROM t_listing_delivery_outbox
 WHERE event_type = 'listing_decision_candidate'
   AND CAST(payload_json AS CHAR) LIKE '%STOCK%'
 ORDER BY id DESC
 LIMIT 50;
```

Use the report to add exact aliases to `symbol_mapping.yaml`, then dry-run and
apply one reviewed mapping at a time. Do not run a global SQL replacement or a
global suffix-strip backfill.

If historical rows were already written under the wrong identity, run the safe
backfill in two phases. Dry-run is mandatory and is the default:

```
cd backend
OPS_INTELLIGENCE_MYSQL_DSN='user:pass@tcp(host:3306)/edgex_ops_intelligence?parseTime=true' \
  go run ./cmd/listing-symbol-backfill \
    --from-canonical=EBAYSTOCK --from-surface=perp --from-kind=canonical \
    --to-canonical=EBAY --to-surface=synthetic_futures --to-kind=synthetic \
    --to-display='EBAY-USDT (perp)'
```

Review the row counts before applying. Execute mode acquires the normal Listing
Agent `listing:run_once` lease first, so it should not race a worker tick:

```
cd backend
OPS_INTELLIGENCE_MYSQL_DSN='user:pass@tcp(host:3306)/edgex_ops_intelligence?parseTime=true' \
  go run ./cmd/listing-symbol-backfill \
    --from-canonical=EBAYSTOCK --from-surface=perp --from-kind=canonical \
    --to-canonical=EBAY --to-surface=synthetic_futures --to-kind=synthetic \
    --to-display='EBAY-USDT (perp)' \
    --execute
```

Backfill guardrails:

- The command updates business identity columns in snapshots, signal
  observations, announcement symbols, candidates, and watchlist rows in one
  transaction.
- Sent delivery outbox payloads are historical audit evidence and are not
  rewritten. A previously sent wrong card should stay visible in delivery audit;
  only future ingestion/card creation should use the corrected identity.
- Pending/retry delivery outbox rows that still contain the old symbol cause
  execute mode to fail closed. Inspect, redrive, cancel, or manually reconcile
  those rows before rerunning; this prevents a backfill from continuously
  pushing stale/wrong Lark cards.
- If both source and target candidate identities already exist, execute mode
  fails closed and requires a manual merge decision so candidate IDs and outbox
  dedupe semantics are not accidentally split.

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

Activity delivery uses `Alert.Webhooks.Activity` or
`Runtime.activity_agent.delivery.webhook_url`; decision links require
`Runtime.activity_agent.decision_token.secret` so stale/forged decisions can be
rejected. Keep these values in private YAML/Nacos config, not in the tracked
sample config.

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
