# EdgeX Ops Intelligence v1.0.0 Release Notes

**Tag**: v1.0.0
**Date**: 2026-05-23

> Naming note: this historical V1 release shipped the Liquidity Dashboard before the broader **EdgeX Ops Intelligence** naming was adopted. The Liquidity Dashboard remains the listed-market health module under EdgeX Ops Intelligence.

This is the first production-ready release. It ships the complete V1
liquidity dashboard with 10 exchange adapters, 74 canonicals (66 with
at least one supporting platform), MySQL persistence, and the
operator tooling needed to keep the data layer healthy in production.

## Highlights

### Symbol coverage (config/instrument_catalog.yaml: 406 entries)

- 10 exchanges: binance, okx, bybit, bitget, bingx, mexc, gate,
  hyperliquid, lighter, edgeX.
- 74 canonicals total in the whitelist; 66 have at least one supporting
  platform (see `backend/docs/coverage-matrix.md`).
- 8 zero-platform canonicals (BIRD/BX/CBPS/DKNG/EWZ/RIVN/XYZ100/ZM)
  are tracked in `config/unresolved_symbols.yaml` and rendered without
  crashing the dashboard. e2e regression guard added in C10.
- Comprehensive ETF / commodity / equity coverage: 21 firm US equity
  stocks (C9a), 7 firm sector/theme ETFs (C9b), URNM uranium ETF
  (C9c), and 6 ambiguous canonicals (C10).

### Liquidity collection

- 5-minute REST + 15-second Lighter WS local order book.
- EdgeX BTC/ETH/SOL via the real adapter (V1 invariant).
- MEXC and Gate volume share applies the official 0.4 / 0.5
  multipliers; depth, slippage, and quality metrics never see those
  multipliers.
- Per-asset_category staleness (C12): crypto 30s, commodity 300s,
  stock and index_etf 600s; configurable via runtime.yaml.
- Per-(platform, canonical) cooldown after consecutive failures
  (C12): default 3 failures triggers 5m cooldown; clears on a single
  success. Skipped pairs surface explicit status rows.
- Up-to-25% jitter on the retryBackoff so 10 platform goroutines
  don't synchronise their retries onto the same upstream during 429
  storms.
- Tier monotonicity enforcement with PartialReason="monotonicity_lower_bound"
  when a deeper tier reports less depth than a shallower one.

### Operator tooling

- **Health/readiness split (C13)**:
  - `/api/health` always returns 200; container HEALTHCHECK targets it.
  - `/api/readiness` returns 503 when catalog is empty or MySQL ping
    fails. Use this for load balancer / k8s readiness probes.
  - `/api/health` JSON includes build_version (ldflags-injected),
    deps.mysql (ping latency + information_schema row count
    estimates), deps.catalog (symbol/platform counts), and Go runtime
    info.
- **Snapshot pruning (C14)**:
  - `make -C backend prune-snapshots-dry DAYS=30` writes a JSON plan.
  - `make -C backend prune-snapshots-confirm DAYS=30` applies it.
  - Dry-run by default; --days floor at 7 to prevent fat-finger
    same-day deletion.
  - Batched DELETE LIMIT 10000.
  - Migration 000007 adds time-window indexes on the six snapshot
    tables that lacked them.
- **Production Compose hardening (C15)**:
  - mem_limit / cpus on every service.
  - Backend container runs as nonroot (uid 65532) with read_only:true
    + tmpfs /tmp + no-new-privileges.
  - 127.0.0.1-by-default port publishing.
  - HEALTHCHECK targets /api/health (liveness, not readiness).
  - Persistent mysql-data volume.
  - `deploy/.env.production.template` tracked; `deploy/.env`
    gitignored.
  - `deploy/Makefile`: up/down/logs/smoke/smoke-readiness/
    backup-mysql/restore-mysql.
- **Frontend URL verifier (C16)**:
  - `make -C backend verify-urls` probes every frontend_url with
    HEAD-then-GET fallback. Writes a yaml report.
  - `--apply` flag flips url_verified=true on entries that respond OK.
  - `build-catalog --preserve-url-verified` (default true) keeps
    operator approvals durable across catalog regenerations.

### Frontend

- Strictly unchanged from the prior shipping baseline (V1 frozen
  scope). 0-platform canonical e2e regression guard added but no UI
  behavior changed.
- localStorage cache namespace parametrized (12a3b97) so a
  multi-tenant deployment doesn't conflict on browser storage.

### Data layer

- 6 snapshot tables now have time-window indexes (migration 000007).
- `partial_reason` widened from VARCHAR(32) to VARCHAR(128) so
  multi-reason joins like `max_precision_shortfall,monotonicity_lower_bound`
  fit (migration 000006).

## Compatibility

- Schema migrations are forward-only; the `down.sql` files are present
  but reversing v1 to v0 is not a supported path.
- The runtime.yaml schema is backwards-compatible: new fields
  (`staleness_by_category`, `cooldown_failure_threshold`,
  `cooldown_duration`) are optional with sensible defaults.

## Known issues

- `host.docker.internal:7897` proxy default in runtime.yaml only
  resolves inside Docker. Local-only `go run` invocations should
  unset HTTP_PROXY or run with `--exchange-proxy=""` (CLI flag will
  ship in V1.1; for now edit runtime.yaml temporarily).
- 8 zero-platform canonicals (BIRD/BX/CBPS/DKNG/EWZ/RIVN/XYZ100/ZM)
  display in the dropdown but show no platform rows. Operator review
  pending; tracked in `config/unresolved_symbols.yaml`.
- The `confidence: tentative` field in symbol_mapping.yaml is
  scaffolded but not wired into the frontend UI; deferred to V1.1.

## Upgrade path

Fresh deploy:
```
cd deploy
cp .env.production.template .env  # edit secrets
make up
make smoke && make smoke-readiness
```

Existing deploy (run from a tagged checkout):
```
cd deploy && make backup-mysql
git fetch && git checkout v1.0.0
make build-image && make up
make smoke && make smoke-readiness
```

## Verification gates

Run before every release:
```
cd backend && make ci
cd web && pnpm typecheck && pnpm lint && pnpm e2e
cd deploy && make test-image      # confirms BUILD_VERSION ldflags injection
cd deploy && make smoke           # liveness
cd deploy && make smoke-readiness # readiness
```
