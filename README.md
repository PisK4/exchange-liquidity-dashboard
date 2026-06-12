# EdgeX Ops Intelligence

This workspace object hosts **EdgeX Ops Intelligence** (EdgeX 运营决策系统).

In short: **EdgeX Ops Intelligence** is the system; **Liquidity Dashboard** is the listed-market monitoring module inside it.

The current implementation has three active modules under this broader operations decision system:

- **Liquidity Dashboard**: listed-market liquidity, depth, spread, slippage, and share monitoring.
- **Listing Agent**: unlisted-asset opportunity detection, signal deduplication, and Lark delivery workflows.
- **Activity Agent**: competitor operations activity intelligence, review, and delivery workflows.

## Current module map

| Module | Backend/API status | Frontend status | Primary implementation entry |
|---|---|---|---|
| Liquidity Dashboard | Collector, adapters, CoinGecko ingestion, Top30/backfill, health/readiness, and snapshot APIs are implemented. | Default `/` dashboard with monitor/funding/quality/share/top30 tabs. Legacy `/liquidity`, `/quality`, `/share`, `/top30`, and `/funding` routes redirect into the dashboard tabs. | `backend/internal/collector`, `backend/internal/adapter`, `backend/internal/api`, `web/app`, `web/components/dashboard-*` |
| Listing Agent | Worker engine, instrument/announcement pollers, signal fusion, Top30 hot-gap, CEX/DEX divergence, liquidity alerts, decision cards, callback API, and delivery outbox are implemented. | Standalone `/listing` candidate console is implemented, alongside Top30/divergence data, Lark cards, and `/api/listing/*`. | `backend/internal/listing`, `backend/internal/api/listing.go`, `backend/internal/api/listing_callback.go`, `web/app/listing`, `web/components/listing-*`, `docs/feat/listing-agent-*.md` |
| Activity Agent | Ingestion, parser, source health, review/decision, delivery outbox, redrive, worker lease, and `/api/activity/*` APIs are implemented. | Standalone `/activity`, `/activity/events/[id]`, and `/activity/events/[id]/decision` pages are implemented, but not yet merged into a unified Ops module shell. | `backend/internal/activity`, `backend/internal/api/activity.go`, `web/app/activity`, `web/components/activity-*` |

## Documentation map

- `AGENTS.md`: task-oriented engineering guide for AI agents and developers working inside this repository.
- `docs/feat/`: implementation contracts for shipped feature slices, especially Liquidity Dashboard and Listing Agent behavior.
- `docs/feat/listing-agent-symbol-identity-normalization.md`: Listing Agent runtime symbol identity, alias resolution, listed-universe identity semantics, Lark card title mapping, and safe historical backfill contract.
- `docs/plan/`: local execution plans and cleanup records; useful for history, but not the long-term implementation contract.
- `deploy/README.md`: deployment directory index for the current Docker Compose / AWS DEV-like deployment shape.
- `deploy/aws-dev-local-rehearsal.md`: Docker Compose rehearsal guide for the AWS DEV-like deployment shape, including Nacos, AWS Secrets Manager, same-origin reverse proxy, and validation commands.
- `deploy/.env.production.template`: operator-facing production-like environment template; copy to private `deploy/.env` before running the stack.
- `deploy/docker-compose.yaml`: executable Compose template for bundled MySQL, one `--role=all` backend, and one Next.js standalone web container.
- `deploy/nginx/ops-intelligence.conf`: host-level same-origin reverse proxy sample for `/`, `/api/*`, and restricted `/metrics`.
- `backend/docs/runbook.md`: production operations, health/readiness, catalog, release, backup/restore, and module smoke procedures.
- `backend/docs/top30-strategy.md`: Top30 acquisition and native backfill semantics.
- `backend/docs/coverage-matrix.md`: coverage snapshot for the tracked canonical universe.
- `backend/docs/raw-instruments/README.md`: raw instrument dump layout and catalog regeneration workflow.

For AWS DEV / Docker Compose / Nacos / AWS Secrets Manager / frontend
deployment work, start from `deploy/README.md` and
`deploy/aws-dev-local-rehearsal.md`, then cross-check `backend/docs/runbook.md`
and the deploy templates. Treat `docs/plan/*` as historical context unless a
current runbook or code path explicitly points there.

## Quick commands

```bash
cd backend && make test
cd backend && make smoke-api PORT=18080 SYMBOL=BTC-USDT
cd backend && make smoke-listing
cd backend && make smoke-activity
cd web && npm run typecheck && npm run lint && npm run build
cd web && npm run test:e2e
cd deploy && make up && make smoke && make smoke-web
```

Technical identifiers now use the Ops Intelligence hard-cutover naming: Go module `edgex-ops-intelligence/backend`, backend command `cmd/ops-intelligence`, binary `/app/ops-intelligence`, environment variables `OPS_INTELLIGENCE_*`, default database `edgex_ops_intelligence`, and config file `edgex-ops-intelligence.yaml`.

For existing local or deployed MySQL data that still lives in an older database, read `backend/docs/ops-intelligence-db-migration.md` before switching DSNs.

Listing Agent symbol identity is runtime-normalized through
`config/symbol_mapping.yaml`: exchange-native `api_symbol` / `base_asset` are
preserved for audit and API lookup, while business comparison and Lark decision
cards use `canonical_symbol + market_surface + instrument_kind`. Read
`docs/feat/listing-agent-symbol-identity-normalization.md` before changing
Listing aliases, candidate identity, listed-universe reconciliation, or related
backfill commands.
