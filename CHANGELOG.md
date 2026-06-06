# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Liquidity tab "24h share" no longer stalls at 0.00%** when edgeX's
  `pro.edgex.exchange` REST ticker is blocked by Cloudflare (HTTP 403
  with a "Just a moment..." interstitial). `liquidityKPIsLocked` now
  resolves each platform's 24h volume via a two-tier source order:
  (1) native ticker when `Status == complete`; (2) the same-day
  CoinGecko per-symbol daily aggregate (already persisted to
  `dailySymbolVolumes` for V1 BTC/ETH/SOL and every platform's CG
  Top60). MEXC×0.4 / Gate×0.5 discounts continue to apply only at
  compute time.
- **Adapter transport failures now report `Status: error`** instead of
  `unsupported`. `unsupported` is reserved for "no catalog entry /
  unknown platform"; transient 4xx/5xx, timeouts, and parse failures
  surface as `error` so downstream KPI logic, collection counters,
  and operator dashboards can tell upstream pressure from a
  legitimate scope gap.

### Added

- **`edgex_24h_share_status`** field on the Liquidity KPI payload
  (`complete` | `partial` | `stale`). The frontend renders `—` when
  `stale` and tags the value `via CG` when `partial`, so operators
  can distinguish "edgeX really has no volume" from "edgeX native
  ticker is degraded and we are borrowing from CoinGecko".
- Runbook entry **§3.6 Liquidity tab "24h share" shows 0.00%** with
  the diagnosis + mitigation flow for the Cloudflare-403 scenario.

## [1.0.0] - 2026-05-23

First production-ready release. Ships the complete V1 liquidity
dashboard with 10 exchange adapters, 74 canonicals across 7 asset
categories (66 with at least one supporting platform), MySQL
persistence, and the operator tooling needed to run the stack in
production.

### Added

- **Symbol coverage**: 21 firm US equity stocks (CRCL/HOOD/AVGO/MU/SNDK/HIMS/BABA/TSM/CRWV/LITE/GME/RKLB/NFLX/MRVL/ZM/RIVN/LLY/COST/BX/DKNG/ORCL); 7 firm sector/theme ETFs (SLV/URA/BOTZ/MAGS/SOXX/EWZ/XLE); URNM uranium ETF; 6 ambiguous canonicals from §4.3 (ANTHROPIC/SPACEX/OPENAI/BIRD/CBPS/XYZ100). Total catalog: 406 (platform, canonical) entries.
- **`config/unresolved_symbols.yaml`**: ops review backlog with
  `platform_issues` and `canonical_issues` schema (17 entries
  documenting the 8 zero-platform canonicals + 9 lower-coverage
  observations).
- **0-platform e2e regression guard**: new
  `web/e2e/dashboard.spec.ts` "zero-platform canonical renders
  without crashing the dashboard" so future config drift cannot
  ship a UI crash.
- **`Runtime.StalenessByCategory`**: per-asset_category staleness
  thresholds (crypto:30s, commodity:300s, stock:600s, index_etf:600s);
  configurable via runtime.yaml.
- **Per-(platform, canonical) cooldown**: skip pairs that have failed
  `cooldown_failure_threshold` consecutive times (default 3) for
  `cooldown_duration` (default 5m). Cleared on a single success or on
  expiry. Skipped pairs surface explicit status rows.
- **retryBackoff jitter**: up-to-25% jitter so platform goroutines
  don't synchronise their retries onto the same upstream during 429
  storms. Swappable entropy via `retryJitterFracBP`.
- **`/api/health` rich JSON**: build_version (ldflags-injected),
  deps.mysql ping latency + information_schema.TABLE_ROWS estimates,
  deps.catalog symbol/platform counts, Go runtime info. Always
  returns 200; container HEALTHCHECK targets it.
- **`/api/readiness` route**: 503 when catalog is empty or MySQL ping
  fails. In-memory mode still passes.
- **`api.Version` package var + `--version` flag** wired through
  `cmd/ops-intelligence` and the new `make build` target with
  `BUILD_VERSION = git describe`.
- **Snapshot pruning**: `backend/scripts/prune-snapshots/main.go`
  with --days (floor 7), --confirm (default dry-run), --batch-size
  (default 10000), --data-dir. Writes JSON prune-history to
  `${OPS_INTELLIGENCE_DATA_DIR}/prune-history/<RFC3339>.json` on every run.
- **Migration 000007**: time-window indexes on the six snapshot
  tables that lacked them; t_top30_snapshot excluded (own auto-prune)
  and t_coingecko_platform_volume_snapshot excluded (already had
  idx_cg_platform_ts).
- **Production Compose hardening**: mem_limit / cpus on every
  service; backend container runs as nonroot uid 65532 with
  read_only:true + tmpfs /tmp + no-new-privileges; restart:
  unless-stopped; 127.0.0.1-by-default port publishing; persistent
  mysql-data volume.
- **`deploy/.env.production.template`**: tracked starting point with
  empty HTTP_PROXY default; `deploy/.env` gitignored.
- **`deploy/Makefile`**: up/down/logs/ps/smoke/smoke-readiness/
  backup-mysql/restore-mysql/build-image/test-image with
  `--project-name edgex-ops-intelligence` so the production stack is
  namespaced away from any developer's deploy-* containers.
- **`backend/scripts/verify-frontend-urls`**: HEAD-then-GET probe
  with --proxy support; writes
  `config/url_verification_report.yaml`; --apply flag flips
  url_verified=true on entries that respond OK.
- **`build-catalog --preserve-url-verified`**: copies forward
  url_verified=true flags from the prior catalog when the
  (platform, canonical, frontend_url) tuple is unchanged. Default
  true so verify-frontend-urls --apply approvals survive a catalog
  rebuild.
- **`backend/docs/runbook.md`** (194 lines): topology, health vs
  readiness contract, four common-symptom triage flows, catalog
  re-generation workflow, release procedure, backup/restore, V1
  invariants, glossary.
- **`backend/docs/coverage-matrix.md`** (123 lines): 66x10 support
  matrix, zero-platform canonical context, per-platform counts.
- **`backend/docs/release-notes-v1.md`** (142 lines): per-feature
  release notes, compatibility statement, known issues, upgrade
  paths, verification gates.

### Changed

- **Migration 000006**: `t_orderbook_snapshot.partial_reason` widened
  from VARCHAR(32) to VARCHAR(128) so multi-reason joins like
  `max_precision_shortfall,monotonicity_lower_bound` (48 chars) fit.
- **`EnforceTierMonotonicity`**: tier-by-tier lower-bound clamp with
  PartialReason="monotonicity_lower_bound" when a deeper tier reports
  less depth than a shallower one. Adds `ReasonMonotonicityLowerBound`
  and `PolicyLooseLowerBound` constants.
- **Backend Makefile**: forwards CATALOG_PROXY/CATALOG_TIMEOUT/DAYS
  variables; CATALOG_ARGS conditional --proxy; new
  `prune-snapshots-{dry,confirm}` and `verify-urls` targets;
  BUILD_VERSION via git describe + LDFLAGS injection; `make build`
  target writing bin/ops-intelligence.
- **`Dockerfile.backend`**: ARG BUILD_VERSION → -ldflags injection;
  apk add ca-certificates + wget; nonroot 65532:65532 user; HEALTHCHECK
  on /api/health.

### Tests

- 5 cooldown tests (cooldown_test.go).
- 3 retryBackoff jitter tests (retry_test.go).
- 3 staleness/cooldown config tests (config_test.go).
- 3 health/readiness tests (health_test.go).
- 4 prune-snapshots tests (main_test.go).
- 3 verify-frontend-urls probe tests (main_test.go).
- 3 build-catalog merge-preserve tests (merge_test.go).
- 5 EnforceTierMonotonicity tests (types_test.go).
- 1 0-platform canonical e2e test (dashboard.spec.ts).

### Known issues

- `host.docker.internal:7897` proxy default in runtime.yaml only
  resolves inside Docker. Local-only `go run` invocations should
  unset HTTP_PROXY or run with `--exchange-proxy=""` (CLI flag will
  ship in V1.1; for now edit runtime.yaml temporarily).
- 8 zero-platform canonicals (BIRD/BX/CBPS/DKNG/EWZ/RIVN/XYZ100/ZM)
  display in the dropdown but show no platform rows. Operator
  review pending; tracked in `config/unresolved_symbols.yaml`.
- The `confidence: tentative` field in symbol_mapping.yaml is
  scaffolded but not wired into the frontend UI; deferred to V1.1.

### V1 Invariants (verified)

- EdgeX BTC/ETH/SOL: live data via real adapter
  (`pro.edgex.exchange/api/v1/public/quote/getDepth`).
- Lighter 2% depth: from WS local order book via
  `LighterBookProvider.Snapshot`; REST fallback rejected by adapter.
- MEXC ×0.4 / Gate ×0.5: applied only to volume_share, never to
  depth, slippage, or quality metrics.
- No fabricated data: unsupported sources surface
  `unsupported`/`error`/`stale`/`insufficient_history` explicitly.

### Commit chain (this release)

- `9c8dd5f` widen partial_reason and enforce tier monotonicity
- `9e25a5b` C9a add 21 firm US equity stocks
- `8980163` C9b add 7 firm sector/theme ETFs
- `94a63a7` C9c add URNM commodity (single platform, ops review pending)
- `31517a0` C10 add §4.3 ambiguous canonicals and ops review backlog
- `6729e38` C12 collector robustness (cooldown + jitter + category staleness)
- `44c9033` ship 000006 widen migration + tier-monotonicity tests
- `a1ee539` C13 split health/readiness probes + inject build version
- `881119d` C14 add snapshot pruning script + time-window indexes
- `9b54463` C15 harden Docker Compose for production deployment
- `20525d4` C16 add frontend URL verifier + merge-preserve
- `bb92344` C17 add runbook, coverage matrix, release notes
