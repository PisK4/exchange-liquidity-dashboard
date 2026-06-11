# EdgeX Ops Intelligence feature contracts

This directory contains feature-level implementation contracts for EdgeX Ops
Intelligence. Treat these files as repo-local engineering contracts that sit
between upstream product/design docs and code.

## Current module map

| Module | Current implementation status | Primary contract docs |
|---|---|---|
| Liquidity Dashboard | Implemented as the default listed-market monitoring surface: collection, adapters, indicators, Top30/backfill, health/readiness APIs, and dashboard tabs. | `platform-liquidity-dashboard-api-coverage.md`, `dashboard-contract-hardening.md`, `adapter-four-tier-depth.md`, `edgex-perps-v2-liquidity-adapter.md`, `top30-share-history-backfill.md`, `liquidity-24h-share-cg-fallback.md`, `coingecko-rate-limit-governance.md`, `liquidity-watchlist.md`, `funding-rate.md` |
| Listing Agent | Implemented as backend workers plus Lark delivery/callback contracts. No standalone `/listing` web console exists yet; current surfaces are `/api/listing/*`, Top30/divergence data, and Lark cards. | `listing-agent-symbol-identity-normalization.md`, `listing-agent-top30-hot-gap-push.md`, `listing-agent-divergence-push.md`, `listing-agent-liquidity-alert.md`, `listing-agent-decision-card-callback.md`, `listing-agent-dynamic-catalog-integration.md`, `listing-agent-signal-coverage.md`, `listing-agent-prd-implementation-review.md` |
| Activity Agent | Implemented in code as ingestion, parser, source health, review/decision, delivery outbox, redrive, and `/activity` frontend pages. Upstream Activity docs remain business intent and source evidence; the current runtime source matrix and delivery gates are repo-local. | `activity-agent-current-contract.md`; code: `backend/internal/activity`, `backend/internal/api/activity.go`, `web/app/activity`. Upstream: `architecture/方案设计/EdgeX运营/活动/运营活动-agent-通用数据模型与-source-health.md` |

## Status labels

- **Implementation contract**: use when changing code, schema, API response,
  worker state machine, delivery card, or operational command behavior.
- **Historical design**: keep for context, but verify against current code and
  runbook before implementing.
- **Research evidence**: use as source API evidence only; it is not a product
  or implementation commitment by itself.

## Current hard guarantees

- Do not fabricate successful exchange data. Missing or invalid data must be
  surfaced as `unsupported`, `error`, `stale`, or `insufficient_history`.
- MEXC `0.4` and Gate `0.5` discounts apply only to volume/share metrics, not
  depth, spread, or slippage.
- EdgeX Perp V1 BTC/ETH/SOL, EdgeX Perp V2 BTC/ETH/SOL, and Lighter
  BTC/ETH/SOL are live-data invariants. Healthy EdgeX Perp V2 orderbook depth
  must surface as `collector=ws_orderbook` / `depth_source=ws_local_book`; REST
  `rest_orderbook` / `rest_snapshot` is an explicit bounded fallback, not a
  fabricated complete source.
- CoinGecko is a finite-budget external source: all backend CoinGecko calls use
  the shared process-local governance contract and must degrade via cooldown,
  stale cache, or explicit errors instead of retry storms.
- Lark cards must preserve their documented schema contracts; webhook HTTP 200
  is not sufficient proof that the card rendered correctly.
- Listing Agent symbol identity uses `config/symbol_mapping.yaml` aliases at
  runtime. Exchange-native `api_symbol` / `base_asset` must be preserved for
  audit/API lookup, while `canonical_symbol + market_surface + instrument_kind`
  is the semantic identity for candidates, listed-universe matching, and Lark
  decision-card titles. See `listing-agent-symbol-identity-normalization.md`.
