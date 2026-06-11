# AGENTS.md - EdgeX Ops Intelligence

This file is a concise entry and routing guide for AI agents working in
`repos/edgex-ops-intelligence`. Keep it short: detailed behavior belongs in
the indexed feature contracts, runbooks, upstream PRDs, or code.

## 1. Identity and Scope

- Current product/system name: **EdgeX Ops Intelligence** / **EdgeX 运营决策系统**.
- Current repository/technical identifier: `edgex-ops-intelligence`.
- Active modules:
  - **Liquidity Dashboard**: listed-market liquidity, depth, spread, slippage,
    share, Top30, funding, and data-health monitoring.
  - **Listing Agent**: unlisted-asset/listing opportunity signals, Top30 gap,
    CEX/DEX divergence, liquidity alerts, Lark cards, callbacks, and delivery.
  - **Activity Agent**: competitor campaign intelligence, source health,
    review, decisions, redrive, and delivery.
- Historical names (`edgex-dashboard`, `EdgeX Dashboard`, `edgex_dashboard`)
  are allowed only in migration, changelog, or historical-release contexts.

## 2. Non-Negotiable Rules

1. **Code/config are the current implementation source of truth.** When docs
   conflict with code, verify against `backend/internal/*`, `web/app/*`,
   `config/edgex-ops-intelligence.yaml`, migrations, Makefile targets, and
   scripts.
2. **Repo-local contracts outrank upstream design notes for implementation.**
   Start from `README.md`, this file, `docs/feat/README.md`, tracked
   `docs/feat/*.md`, and `backend/docs/runbook.md`; use upstream architecture
   docs for business intent, design history, and evidence.
3. **Do not treat historical plans as current tasks.** V1 bootstrap specs,
   historical Liquidity designs, Listing P1/P2 plans, and Activity preflight
   research are context unless repo-local contracts/code say otherwise.
4. **Do not reintroduce stale names or paths.** Current config is
   `config/edgex-ops-intelligence.yaml`; do not add current guidance that
   points to `runtime.yaml`.
5. **Do not fabricate data or overstate source confidence.** Missing or invalid
   data must surface explicit status such as `unsupported`, `error`, `stale`,
   or `insufficient_history`. A single HAR/browser sample/fetch/webhook 200 is
   not production confidence.
6. **Keep metric invariants.** MEXC `0.4` and Gate `0.5` discounts apply only
   to volume/share, never to depth, spread, or slippage.
7. **Keep live-data invariants.** EdgeX Perp V1 BTC/ETH/SOL, EdgeX Perp V2
   BTC/ETH/SOL, and Lighter BTC/ETH/SOL must use real data paths. EdgeX Perp
   V2 depth should surface as `collector=ws_orderbook` and
   `depth_source=ws_local_book` when its WS local book is healthy; REST depth is
   only an explicit bounded fallback. Lighter 2% depth comes from the WS local
   book, not a REST fallback marked complete.
8. **Keep module boundaries explicit.** Do not mix Liquidity, Listing, and
   Activity semantics unless the shared contract/data flow is documented.
9. **Contract docs must be trackable.** If `docs/feat/*.md` is referenced as an
   implementation contract, it must not be ignored by `.gitignore`.
10. **Verify documentation cleanups.** Use targeted `rg`, `git diff --check`,
    JSON validation for OpenAPI/Swagger, and Makefile dry-runs when command or
    migration guidance changes.

## 3. Documentation Routing

| Task | Read first | Then cross-check |
|---|---|---|
| System naming / module boundaries | `../../architecture/方案设计/EdgeX运营/README.md` | `README.md`, this file |
| Liquidity metrics / API / UI | `docs/feat/README.md` | `../../architecture/方案设计/EdgeX运营/需求梳理/README.md`, `13-*统计口径闭环.md` |
| Exchange adapters / depth / slippage / proxy | `docs/feat/adapter-four-tier-depth.md`, `docs/feat/native-exchange-proxy.md`, `docs/feat/edgex-perps-v2-liquidity-adapter.md` | `../../architecture/方案设计/EdgeX运营/盘口数据精炼/README.md` and the relevant platform doc |
| Top30 / share / history | `backend/docs/top30-strategy.md` | `docs/feat/top30-share-history-backfill.md`, `docs/feat/liquidity-24h-share-cg-fallback.md` |
| Listing Agent / Lark / callbacks | `docs/feat/listing-agent-*.md` | `../../architecture/方案设计/EdgeX运营/Listing/README.md` |
| Listing Agent symbol identity / aliases / backfill | `docs/feat/listing-agent-symbol-identity-normalization.md` | `config/symbol_mapping.yaml`, `backend/internal/config/canonical_index.go`, `backend/docs/runbook.md` |
| Activity Agent / campaign sources | `../../architecture/方案设计/EdgeX运营/活动/README.md` | `../../architecture/方案设计/EdgeX运营/活动/运营活动-agent-通用数据模型与-source-health.md`, `backend/internal/activity/*` |
| Production ops / release / migration | `backend/docs/runbook.md` | `backend/docs/coverage-matrix.md`, `backend/docs/ops-intelligence-db-migration.md`, `backend/docs/release-notes-v1.md` |

## 4. Module Implementation Map

| Module | Backend | Frontend | Primary docs |
|---|---|---|---|
| Liquidity Dashboard | `backend/internal/collector`, `backend/internal/adapter`, `backend/internal/indicators`, `backend/internal/api` | default `/` dashboard plus legacy tab redirects | `docs/feat/README.md`, `backend/docs/runbook.md`, `backend/docs/top30-strategy.md` |
| Listing Agent | `backend/internal/listing`, `backend/internal/api/listing*.go` | no standalone `/listing` console yet; current surfaces are API, Top30/divergence data, and Lark cards | `docs/feat/listing-agent-*.md` |
| Activity Agent | `backend/internal/activity`, `backend/internal/api/activity.go` | `/activity`, `/activity/events/[id]`, `/activity/events/[id]/decision` | upstream Activity README/model docs, `backend/docs/runbook.md` |

## 5. Common Commands

- Backend tests: `cd backend && make test`
- Backend smoke: `cd backend && make smoke-api PORT=18080 SYMBOL=BTC-USDT`
- Role=all startup smoke: `cd backend && make smoke-all-startup PORT=18080`
- Live adapter smoke: `cd backend && make smoke-adapters SYMBOL=BTC-USDT`
- Listing smoke: `cd backend && make smoke-listing`
- Activity smoke: `cd backend && make smoke-activity`
- Frontend checks: `cd web && npm run lint && npm run typecheck && npm run build`
- Local Playwright: `cd web && npm run test:e2e`
- Docker Playwright: `cd web && PLAYWRIGHT_BASE_URL=http://127.0.0.1:3001 npm run test:e2e`
- Docker stack: `docker compose -f deploy/docker-compose.yaml up --build`

### Local Docker backend rebuild guardrails

- Prefer absolute compose paths or run from `repos/edgex-ops-intelligence`; a
  stale shell cwd can resolve `deploy/docker-compose.yaml` against the workspace
  root and fail before touching the real stack.
- If Docker Hub metadata pulls are unavailable, do not keep retrying `--build`.
  Rebuild the Go binaries locally and layer them onto the existing
  `deploy-backend:latest` runtime image instead.
- When copying locally built binaries into the Linux container, cross-compile
  for the image platform (for example `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`).
  Host-default macOS binaries will restart-loop with `exec format error`.
- The historical local Docker database may be `edgex_dashboard`, while compose
  defaults to `edgex_ops_intelligence`. Verify the real DB with MySQL before
  recreating backend; otherwise startup fails with `Unknown database`.
- For `--role=all`, `/api/health` should become reachable before MySQL
  latest-snapshot restore, Lighter WS, and the first collector cycle finish.
  Use `/api/health | jq '.deps.startup'` and `/api/readiness | jq
  '.checks.startup'` to distinguish liveness from readiness.
- Treat Docker health, `/api/readiness`, backend logs, and DB evidence together.
  For Activity delivery, verify `t_activity_delivery_outbox` plus
  `t_activity_delivery_attempt` (`http_status=200` and Lark `code=0`), not only
  webhook responses or a single manual push.

## 6. Change-Trigger Checklist

- API or response shape changes: update OpenAPI/Swagger, frontend API types,
  and relevant `docs/feat` contracts.
- DB schema or migration changes: update migrations, Makefile migration
  guidance, and `backend/docs/runbook.md`.
- Config changes: update `config/edgex-ops-intelligence.yaml`, deployment
  templates, and runbook guidance.
- Adapter/source-health changes: update relevant feature contracts and upstream
  source evidence indexes.
- Lark card/callback/delivery changes: update `docs/feat/listing-agent-*.md`
  or Activity delivery docs, and validate with preview/smoke tooling.
- Documentation entry changes: update affected indexes together (`README.md`,
  `AGENTS.md`, `docs/feat/README.md`, `backend/docs/runbook.md`, and upstream
  module README files when relevant).

## 7. Git and Security Notes

- Do not commit secrets, production DSNs, webhook URLs, or `.env` files.
- Do not auto-commit or push unless the user explicitly asks.
- Use Conventional Commits when asked to commit, for example
  `docs(edgex-ops-intelligence): update module contracts`.
- For PRs, summarize backend/frontend impact and list validation commands.
