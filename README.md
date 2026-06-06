# EdgeX Ops Intelligence

This workspace object hosts **EdgeX Ops Intelligence** (EdgeX 运营决策系统).

The existing Liquidity Dashboard and Listing Agent capabilities remain active modules under this broader operations decision system:

- **Liquidity Dashboard**: listed-market liquidity, depth, spread, slippage, and share monitoring.
- **Listing Agent**: unlisted-asset opportunity detection, signal deduplication, and Lark delivery workflows.
- **Activity Agent**: competitor operations activity intelligence, review, and delivery workflows.

Technical identifiers now use the Ops Intelligence hard-cutover naming: Go module `edgex-ops-intelligence/backend`, backend command `cmd/ops-intelligence`, binary `/app/ops-intelligence`, environment variables `OPS_INTELLIGENCE_*`, default database `edgex_ops_intelligence`, and config file `edgex-ops-intelligence.yaml`.

For existing local or deployed MySQL data that still lives in an older database, read `backend/docs/ops-intelligence-db-migration.md` before switching DSNs.
