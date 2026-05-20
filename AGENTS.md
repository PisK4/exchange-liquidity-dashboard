# Repository Guidelines

## Project Background & Reference Documents

This repo (`edgex-dashboard`) implements the EdgeX 平台核心交易对流动性对比 Dashboard. Before changing data shape, indicator formulas, exchange adapters, or surface taxonomy, read the upstream specs — they are the contractual source of truth, not this README.

- **Original product requirement (V2 PRD)**: `../../architecture/方案设计/EdgeX运营/原需求/平台核心交易对流动性对比dashboard V2.md` — business goals, 4 Tabs, 5min refresh cadence, target exchange list.
- **Requirement breakdown directory**: `../../architecture/方案设计/EdgeX运营/需求梳理/` — full needs analysis, per-exchange API research (CoinGecko / Hyperliquid / EdgeX / Binance / OKX / Lighter / Bitget / Bybit / Gate / MEXC / BingX), 统计口径闭环, 数据源横评, 目标标的交易所覆盖矩阵.
- **Full code design (生产目标)**: `../../architecture/方案设计/EdgeX运营/需求梳理/15-平台流动性Dashboard-代码方案设计.md` — Go + Next.js + MySQL architecture, DB schema, ExchangeAdapter contract, indicator formulas, depth_status / partial_reason taxonomy, milestones M1–M4. This is what the repo must eventually conform to.
- **V1 快速上线 spec (当前阶段)**: `/Users/pis/.factory/specs/2026-05-19-dashboard-v1.md` — the trimmed first-version plan currently being implemented; deviations from the full design (e.g. SQLite/MySQL choice, simplified collector, V1 historical fields stay `insufficient_history`) live here. When in doubt, follow this V1 spec, but never violate guarantees from doc 15 (no fabricated exchange data, MEXC ×0.4 / Gate ×0.5 only on volume/share, EdgeX V1 BTC/ETH/SOL must be real V1 data, Lighter 2% depth must come from WS local book).

## Project Structure & Module Organization

This workspace object implements the EdgeX platform liquidity Dashboard V1. Backend code lives in `backend/`: `cmd/dashboard` is the entrypoint, `internal/api` serves HTTP routes, `internal/collector` coordinates collection and persistence, `internal/adapter` contains exchange REST adapters, and `internal/indicators` computes depth/spread/slippage metrics. Frontend code lives in `web/` using Next.js App Router; pages are under `web/app`, shared UI under `web/components`, API helpers under `web/lib`. Runtime source-of-truth config is in `config/*.yaml`; Docker assets are in `deploy/`; helper scripts are in `scripts/`.

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

## Commit & Pull Request Guidelines

Use Conventional Commits, e.g. `feat(edgex-dashboard): add exchange adapter`. PRs should summarize backend/frontend impact, list validation commands, mention known unsupported exchanges (for V1 this excludes EdgeX Perp V1 BTC/ETH/SOL and Lighter BTC/ETH/SOL), and include screenshots or Playwright evidence for UI changes.
