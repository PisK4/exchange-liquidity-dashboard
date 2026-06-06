# Repository Guidelines

## Naming and Scope

- **EdgeX Ops Intelligence** / **EdgeX 运营决策系统** is the current product and system name.
- `edgex-ops-intelligence` is the current repository and technical identifier.
- **Liquidity Dashboard** / **平台流动性看板** is one module under EdgeX Ops Intelligence, focused on listed-market liquidity, depth, spread, slippage, share, Top30, and data-health monitoring.
- **Listing Agent** and **Activity Agent** are sibling capability modules under the same Ops Intelligence system.
- Historical names such as `edgex-dashboard`, `EdgeX Dashboard`, or `edgex_dashboard` may appear only in migration, changelog, or historical-release contexts. Do not use them as the current project or system name.

## Project Background & Reference Documents

This repo (`edgex-ops-intelligence`) currently implements the first production slice of **EdgeX Ops Intelligence** (EdgeX 运营决策系统). Liquidity Dashboard remains the listed-market health monitoring module, while Listing Agent and future operations agents are capability modules under the broader Ops Intelligence system. Before changing data shape, indicator formulas, exchange adapters, or surface taxonomy, read the upstream specs — they are the contractual source of truth, not this README.

- **Liquidity Dashboard original product requirement (V2 PRD)**: `../../architecture/方案设计/EdgeX运营/原需求/平台核心交易对流动性对比dashboard V2.md` — business goals, 4 Tabs, 5min refresh cadence, target exchange list.
- **Liquidity Dashboard requirement breakdown directory**: `../../architecture/方案设计/EdgeX运营/需求梳理/` — full needs analysis, per-exchange API research (CoinGecko / Hyperliquid / EdgeX / Binance / OKX / Lighter / Bitget / Bybit / Gate / MEXC / BingX), 统计口径闭环, 数据源横评, 目标标的交易所覆盖矩阵.
- **Liquidity Dashboard full code design (生产目标)**: `../../architecture/方案设计/EdgeX运营/需求梳理/15-平台流动性Dashboard-代码方案设计.md` — Go + Next.js + MySQL architecture, DB schema, ExchangeAdapter contract, indicator formulas, depth_status / partial_reason taxonomy, milestones M1–M4. This is what the Liquidity Dashboard module must eventually conform to.
- **V1 快速上线 spec (当前阶段)**: `/Users/pis/.factory/specs/2026-05-19-dashboard-v1.md` — the trimmed first-version plan currently being implemented; deviations from the full design (e.g. SQLite/MySQL choice, simplified collector, V1 historical fields stay `insufficient_history`) live here. When in doubt, follow this V1 spec, but never violate guarantees from doc 15 (no fabricated exchange data, MEXC ×0.4 / Gate ×0.5 only on volume/share, EdgeX V1 BTC/ETH/SOL must be real V1 data, Lighter 2% depth must come from WS local book).

## Document Entry Points by Task

Use this section as the fastest path from an engineering task to the right upstream and implementation documents. Upstream docs under `../../architecture/方案设计/EdgeX运营/` explain product intent and research evidence; repo-local docs under `docs/feat/` and `backend/docs/` are closer to implementation contracts and operations.

### System positioning or naming questions

- Start with `../../architecture/方案设计/EdgeX运营/README.md` for current naming, module boundaries, status labels, and search hygiene.
- Read `../../architecture/方案设计/EdgeX运营/深入/运营决策系统路线图.md` for the broader Ops Intelligence system roadmap.
- Read `../../architecture/方案设计/EdgeX运营/原需求/README.md` before interpreting any original PRD.
- Treat `edgex-dashboard`, `EdgeX Dashboard`, and `edgex_dashboard` as historical names only; do not introduce them as current identifiers.

### Liquidity Dashboard data shape, formulas, APIs, or UI

- Start with `../../architecture/方案设计/EdgeX运营/需求梳理/README.md`.
- Read `../../architecture/方案设计/EdgeX运营/原需求/平台核心交易对流动性对比dashboard V2.md` for the original business requirement.
- Read `../../architecture/方案设计/EdgeX运营/需求梳理/13-平台流动性Dashboard-综合评审与统计口径闭环.md` before changing metric semantics or statistical口径.
- Read `../../architecture/方案设计/EdgeX运营/需求梳理/15-平台流动性Dashboard-代码方案设计.md` before changing backend/frontend data contracts or schema.
- Cross-check repo-local contracts in `docs/feat/platform-liquidity-dashboard-api-coverage.md`, `docs/feat/dashboard-contract-hardening.md`, `docs/feat/liquidity-24h-share-cg-fallback.md`, `docs/feat/top30-share-history-backfill.md`, and `docs/feat/liquidity-watchlist.md`.

### Exchange adapter, order book depth, spread, slippage, or proxy work

- Start with `../../architecture/方案设计/EdgeX运营/盘口数据精炼/README.md`.
- Read `../../architecture/方案设计/EdgeX运营/需求梳理/14-平台流动性Dashboard-数据源横评与实现选型.md` for source selection trade-offs.
- Read the relevant platform doc under `../../architecture/方案设计/EdgeX运营/盘口数据精炼/` before changing one exchange adapter.
- Cross-check repo-local contracts in `docs/feat/adapter-four-tier-depth.md` and `docs/feat/native-exchange-proxy.md`.
- Keep the hard rule: MEXC `0.4` and Gate `0.5` discounts apply only to volume/share, never to depth, spread, or slippage.

### Listing Agent / Lark push / Top30 signal work

- Start with `../../architecture/方案设计/EdgeX运营/Listing/README.md`.
- For P1 hot-gap behavior, read `../../architecture/方案设计/EdgeX运营/Listing/2026-05-27-Listing-Agent-P1-主链路方案设计.md` and `docs/feat/listing-agent-top30-hot-gap-push.md`.
- For CEX/DEX divergence, read `../../architecture/方案设计/EdgeX运营/Listing/2026-05-28-Listing-Agent-P2-CEX-DEX-divergence-#2-#5.md` and `docs/feat/listing-agent-divergence-push.md`.
- For liquidity-lag / worst-depth alerts, read `../../architecture/方案设计/EdgeX运营/Listing/2026-05-29-Listing-Agent-Dashboard-Liquidity-Alerts-#10-#11.md` and `docs/feat/listing-agent-liquidity-alert.md`.
- For card callback or catalog changes, read `docs/feat/listing-agent-decision-card-callback.md` and `docs/feat/listing-agent-dynamic-catalog-integration.md`.

### Activity Agent / competitor campaign intelligence work

- Start with `../../architecture/方案设计/EdgeX运营/活动/README.md`.
- Read `../../architecture/方案设计/EdgeX运营/原需求/运营活动AI AGENT.md` for original product intent.
- Read `../../architecture/方案设计/EdgeX运营/活动/运营活动-agent-通用数据模型与-source-health.md` before designing `ActivityEvent`, source health, fetch mode, parser, or auto-push gates.
- Read `../../architecture/方案设计/EdgeX运营/活动/运营活动-agent-交易所活动源拆分总览.md` before prioritizing exchange-specific sources.
- For one exchange, read the corresponding file under `../../architecture/方案设计/EdgeX运营/活动/交易所活动源/`.
- Treat `../../architecture/方案设计/EdgeX运营/活动/调研/` as evidence logs; do not convert a single successful HAR/browser sample into production confidence without source-health gates.

### Production operations, release, or migration work

- Read `backend/docs/runbook.md` for health/readiness, catalog regeneration, release and backup/restore procedures.
- Read `backend/docs/coverage-matrix.md` before changing supported platforms, canonicals, or unresolved symbols.
- Read `backend/docs/ops-intelligence-db-migration.md` before changing DSNs or migrating older local/deployed databases.
- Read `backend/docs/release-notes-v1.md` before preparing a release summary.
- Read `backend/docs/top30-strategy.md` before changing Top30 calculation or display strategy.

## Project Structure & Module Organization

This workspace object implements EdgeX Ops Intelligence V1, with Liquidity Dashboard as the initial listed-market monitoring surface. Backend code lives in `backend/`: `cmd/ops-intelligence` is the entrypoint, `internal/api` serves HTTP routes, `internal/collector` coordinates collection and persistence, `internal/adapter` contains exchange REST adapters, and `internal/indicators` computes depth/spread/slippage metrics. Frontend code lives in `web/` using Next.js App Router; pages are under `web/app`, shared UI under `web/components`, API helpers under `web/lib`. Runtime source-of-truth config is in `config/*.yaml`; Docker assets are in `deploy/`; helper scripts are in `scripts/`.

## Operator Documentation

V1 production tooling and procedures live under `backend/docs/`:
- `runbook.md`: health/readiness probes, common symptoms, catalog re-generation, release procedure, backup/restore.
- `coverage-matrix.md`: 74 canonicals × 10 platforms support matrix; 8 zero-platform canonicals tracked in `config/unresolved_symbols.yaml`.
- `release-notes-v1.md`: per-feature release notes for v1.0.0.

## Build, Test, and Development Commands

- Backend: `cd backend && make test` runs `go test ./...`.
- Backend smoke: `cd backend && make smoke-api PORT=18080 SYMBOL=BTC-USDT` starts a temporary API server and checks core endpoints.
- Live adapter smoke: `cd backend && make smoke-adapters SYMBOL=BTC-USDT` performs public exchange API reads only.
- Frontend: `cd web && npm run lint && npm run typecheck && npm run build` validates UI code.
- E2E: start backend/API first, then `cd web && PLAYWRIGHT_BASE_URL=http://127.0.0.1:3002 npm run test:e2e`.
- Docker: `docker compose -f deploy/docker-compose.yaml up --build` starts MySQL, backend, and web.

## Coding Style & Naming Conventions

Use Go `gofmt`; keep packages lower-case and layered (`api` → `collector`/domain helpers → adapter/store). Do not fabricate successful exchange data: unavailable sources must return explicit `unsupported`, `error`, `stale`, or `insufficient_history`. EdgeX Perp V1 BTC/ETH/SOL is required real V1 data and must not be treated as planned unsupported/TODO. Lighter BTC/ETH/SOL is also required V1 real data: 2% depth must come from the Lighter WS local book (`order_book/{market_id}`), not from REST `orderBookOrders?limit=200` fallback marked complete. Frontend TypeScript should stay typed, component names use PascalCase, and route folders use lower-case names.

## Testing Guidelines

Add Go unit tests beside changed packages as `*_test.go`. Keep adapter tests deterministic unless guarded by `LIVE_ADAPTER_SMOKE=1`. Playwright specs live in `web/e2e`. For changes affecting data shape, run backend tests plus frontend typecheck/build and E2E.

## Security & Configuration Tips

Never commit real credentials. `.env.example` uses local-only defaults; MySQL DSN should remain environment-driven. MEXC `0.4` and Gate `0.5` discounts apply only to volume/share calculations, not depth, spread, or slippage. V1 historical `7d/30d/7d Δ` stays `insufficient_history`.

## Top30 Listed-Universe Dependency

The Top30 tab's "edgeX 已上线?" column is fed by `config/listed_universe.yaml`, regenerated by `make catalog` alongside `instrument_catalog.yaml`. The collector base-matches each Top30 row's symbol against `platforms.edgeX.base_assets`. Stale or missing universe data degrades that column back to "否" silently (no UI badge); run `make catalog` whenever edgeX onboards or delists a contract so the column stays accurate. "竞品 Top30 覆盖" does **not** depend on this file — it is computed live from CoinGecko Top30 cross-tabulation and uses a fixed denominator of 9.

## Listing Agent Lark Push

The Listing Agent surfaces three Lark-card families through the shared `t_listing_delivery_outbox` → `DrainDueOutbox` → webhook pipeline. Read the relevant per-feature doc before editing `backend/internal/listing/`:

- **`docs/feat/listing-agent-top30-hot-gap-push.md`** — #1 Top30 hot-gap card (CoinGecko Top30 → `t_top30_snapshot` → outbox). Source of truth for the shared interactive-card schema, K-line button selection, streak counter, three-state `edgex_listed`, delivery proxy isolation, and push throttling knobs. The contracts called out below originate here and apply to all three families.
- **`docs/feat/listing-agent-divergence-push.md`** — #2-#5 CEX/DEX divergence cards. Same outbox + delivery, shares `Alert.Webhooks.Listing` with hot-gap, but adds canonical-fold resolver, per-category dedupe, and KPI-style grid.
- **`docs/feat/listing-agent-liquidity-alert.md`** — #10 / #11 Dashboard liquidity-lag + worst-depth cards. Adds the generic `t_listing_alert_state` table (reusable by #12 / #13), a first → reissue@6h → clear@3-streak state machine, and routes to a dedicated `Alert.Webhooks.Liquidity` bot so operators can mute liquidity alerts independently from listing announcements.

Cross-family contracts (set by hot-gap, honoured by the others):

- **Card renderer** (`RenderTop30PostMessage`): emits a Lark `msg_type=interactive` card with action-coloured header (`优先上架`=red / `评估上架`=blue / fallback=grey), symbol H1 + streak badge (`🆕 NEW` for day 1, `已第 N 天在榜` for day ≥ 2), 2×2 summary fields, colored-bullet platform list, primary "查看 Top30 详情" + secondary "📈 \<Exchange\> K 线" buttons. **All `plain_text` Lark child elements use `content`, not `text`** — Lark silently drops the element while still returning HTTP 200 / `code=0` if you use `text`, so always eyeball the card in the group; webhook 200 OK does **not** prove the card rendered.
- **K-line button target selection** (`chooseTop30KlineButton`): three-tier priority — (1) if binance is in platforms, jump to Binance K 线 (industry-default reference, even when binance is the edge platform); (2) otherwise walk platforms in rank-ascending order and pick the first one with a known URL template (so the button matches the card body's "最强 \<X\> #N" summary in the binance-absent case); (3) if no template matches at all (e.g. only `lighter`), fall back to Binance — accepting a minor 404 risk over showing no button. URL templates for the 9 platforms (binance / okx / bybit / bitget / gate / mexc / bingx / hyperliquid; lighter has no public K-line page) live in `buildExchangeKlineURL`. Each is the SEO-canonical perpetual chart page; verify a hot perp's URL on the exchange before changing a single template (don't speculatively rewrite all 9 — they have inconsistent conventions like gate.**com** vs gate.io and bitget historically using `_UMCBL` suffix).
- **Streak calculation** (`countTop30Streak`): counts consecutive UTC days strictly before today on `t_listing_signal_observation`, partitioned by `(display_symbol, signal_subtype=action)` so 优先上架 vs 评估上架 are independent lanes. Best-effort: a query failure degrades to `StreakDays=1` (NEW badge) instead of fail-closing the alert.
- **Tier bullets**: use lark_md `<font color='red|orange|grey'>●</font>` instead of emoji — Lark desktop does not bundle Apple/Google color-emoji fonts, so ⭐ / 🔸 / ⚠ render as broken orange diamonds. Reserve emoji for genuinely emoji-shaped semantics (📊 / 📈 / 🔥 / 🆕).
- **Three-state `edgex_listed`** (`NULL` / `0` / `1`) is the contract that gates the alert: only `*EdgexListed == false` triggers. Do not collapse `NULL` into `false` in `mysql_store.edgexListedTinyInt` — `TestEdgexListedTinyIntDistinguishesKnownFalseFromUnknown` locks this down.
- **Per-feature delivery proxy** (`Runtime.listing_agent.delivery.proxy`) is wired through `buildDeliveryHTTPClient` and is **isolated from** `Runtime.exchange_proxy` and CoinGecko's proxy. Production SG/JP boxes leave it empty; local Docker uses `host.docker.internal:7897`. Never escalate it to a process-level `HTTPS_PROXY` env — that pollutes all 9 native exchange adapters.
- **Push throttling knobs** (`Runtime.listing_agent.top30_push.{auto_quiet_after_streak_days, max_per_tick, send_spacing}`): defend the channel against fatigue and UTC-rollover bursts. `auto_quiet_after_streak_days` (default 3) auto-suppresses outbox inserts once a `(symbol, action)` has stayed in the funnel for that many consecutive UTC days — the signal observation row is **still recorded** so the streak counter does not gap-reset and re-trigger a fake `NEW` push later. `max_per_tick` (default 2) caps how many outbox rows the delivery worker drains per engine tick; `send_spacing` (default 10m) staggers `next_attempt_at` across rows inserted in the same producer pass so they serialize across subsequent ticks instead of all firing at once. Combined, these handle the worst-case "many symbols cross the threshold simultaneously at 0:00 UTC" scenario without spamming. Setting any to 0 disables that specific lever.
- **Preview CLI** (`backend/scripts/top30-preview/main.go`): reads the latest `t_top30_snapshot` rows, computes streaks, renders the v2 card, and either prints JSON (`--dry-run`) or POSTs to a Lark webhook. Does **not** touch `t_listing_delivery_outbox` / `t_listing_signal_observation`, so it is safe to run alongside `deploy-backend-1` without dedupe collisions. Use this — not redrive — when iterating on card visuals. **`--config-dir` makes the preview share the production YAML** so webhook / proxy / `dashboard_base_url` / DSN stay in a single source of truth; `host.docker.internal` is auto-rewritten to `127.0.0.1` so the same yaml proxy value works from the host shell.

```bash
# Dry-run: pick up webhook / proxy / dashboard from yaml; only DSN is flag-driven
cd backend && go run ./scripts/top30-preview \
  --config-dir=../config \
  --mysql-dsn="root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true&loc=UTC&charset=utf8mb4" \
  --dry-run --limit=3

# Live preview: same config source as the listing engine
cd backend && go run ./scripts/top30-preview \
  --config-dir=../config \
  --mysql-dsn="root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true&loc=UTC&charset=utf8mb4" \
  --limit=2
```

## Commit & Pull Request Guidelines

Use Conventional Commits, e.g. `feat(edgex-ops-intelligence): add exchange adapter`. PRs should summarize backend/frontend impact, list validation commands, mention known unsupported exchanges (for V1 this excludes EdgeX Perp V1 BTC/ETH/SOL and Lighter BTC/ETH/SOL), and include screenshots or Playwright evidence for UI changes.
