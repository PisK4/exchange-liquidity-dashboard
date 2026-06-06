# EdgeX Ops Intelligence Runbook

Operational procedures for the V1 production release. Treat this as
the canonical source for "the Ops Intelligence liquidity module is broken, what do I do?";
escalate only when a section here does not cover the symptom.

## 1. Topology

```
[browser] --> :3001 (Next.js web)  ---SSR-->  :8080 (backend api)
                                                 |
                                                 |---> 10 exchange REST + WS adapters
                                                 |     (binance/okx/bybit/bitget/bingx/
                                                 |      mexc/gate/hyperliquid/lighter/edgeX)
                                                 |
                                                 +---> CoinGecko (Demo API key)
                                                 +---> :3306 (MySQL persistence)
```

Single Compose project (`edgex-ops-intelligence`). All bind on 127.0.0.1 by
default; expose via a reverse proxy if external access is required.

## 2. Health and Readiness Probes

Two distinct endpoints with deliberately different semantics:

| Endpoint            | Always 200? | Purpose                                                                 |
|---------------------|-------------|-------------------------------------------------------------------------|
| `/api/health`       | Yes         | Liveness. Container HEALTHCHECK targets this. Surfaces build_version, deps.mysql ping latency, deps.catalog symbol count, goroutine count. |
| `/api/readiness`    | No (200 or 503) | "Should this instance receive traffic?" gate. 503 when catalog is empty or MySQL ping fails. |

Why split: pointing the container HEALTHCHECK at `/api/readiness`
would put the container into a restart loop during transient upstream
exchange outages. `/api/health` always returns 200 once the process is
responsive; readiness is allowed to fail.

Smoke commands:

```
make -C deploy smoke              # liveness only
make -C deploy smoke-readiness    # the strict gate
```

## 3. Common Symptoms

### 3.1 Frontend shows "no data" for one platform

1. `make -C deploy smoke` to confirm backend is up.
2. `curl :8080/api/collection-status | jq '.[] | select(.platform=="<X>")'`
   to see the per-collector status rows.
3. Look for `"skipped: pair in cooldown after consecutive failures"` --
   the collector parks a (platform, canonical) pair after three
   consecutive failures (default; see `cooldown_failure_threshold` in
   runtime.yaml). Cooldown clears after `cooldown_duration` (default
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
   docker exec deploy-mysql-1 mysql -uroot -proot edgex_ops_intelligence -e \
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

## 4. Catalog Re-generation Workflow

Cadence: monthly, plus ad-hoc when an exchange announces a relisting.

```
# 1. Refresh raw instrument dumps (writes backend/docs/raw-instruments/<platform>/<date>.json)
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
cd web && pnpm typecheck && pnpm lint && pnpm e2e

# 3. Build production image with embedded version
cd deploy && make build-image

# 4. Smoke the embedded version
cd deploy && make test-image

# 5. Deploy
cd deploy && cp .env.production.template .env  # first time only
make up

# 6. Verify
make smoke && make smoke-readiness
```

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
  via runtime.yaml.
