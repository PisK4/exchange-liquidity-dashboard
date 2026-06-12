# EdgeX Ops Intelligence plan index

This directory is a local planning workspace. Most plan bodies remain ignored by
git on purpose: they are useful execution history and scratch context, but they
are not the current implementation contract unless a tracked runbook, feature
contract, or code path explicitly promotes them.

Use this tracked index to decide whether a plan is current, historical, or only a
backlog note before acting on it.

## Status labels

- **Active plan**: still intended for future implementation. Re-check against
  code and current runbooks before executing.
- **Historical implementation plan**: already implemented, partially superseded,
  or preserved for design rationale only.
- **Cleanup backlog**: candidate cleanup work. Validate current code first; do
  not assume every item still applies.
- **Idea backlog**: product or UI idea that needs fresh scoping before coding.
- **Local progress note**: session history and audit trail, not a task source.

## Plan status matrix

| Plan | Status | Current source of truth / next step |
|---|---|---|
| `2026-06-11-aws-deployment-hardening-plan.md` | Historical implementation plan | Use `deploy/README.md`, `deploy/aws-dev-local-rehearsal.md`, `backend/docs/runbook.md`, and executable deploy/config templates for current AWS DEV / Docker Compose / Nacos / AWS SM guidance. |
| `2026-06-12-aws-dev-local-deployment-rehearsal-record.md` | Local progress note | Chinese audit record for the completed AWS DEV-like local Docker rehearsal, including current deploy replacement, legacy `edgex_dashboard` preservation, isolated rehearsal validation, and observed pitfalls. Do not treat it as a new implementation plan. |
| `2026-06-11-source-health-resilience-plan.md` | Active plan | Before execution, re-run the compatibility gate described in the plan and verify current status enums, OpenAPI, frontend consumers, and source-health writers. |
| `2026-06-07-ops-intelligence-module-cleanup-index.md` | Cleanup backlog | Re-audit module ownership and current routes before starting cleanup; do not remove modules based only on this index. |
| `2026-06-07-shared-platform-cleanup-plan.md` | Cleanup backlog | Validate shared platform code and current config references before making changes. |
| `2026-06-07-activity-agent-cleanup-plan.md` | Cleanup backlog | Start from `docs/feat/activity-agent-current-contract.md`, `backend/internal/activity`, and current `/activity` pages instead of this plan alone. |
| `2026-06-07-liquidity-dashboard-cleanup-plan.md` | Cleanup backlog | Start from current Liquidity contracts in `docs/feat/README.md`, `backend/docs/runbook.md`, and dashboard code. |
| `2026-06-07-listing-agent-cleanup-plan.md` | Cleanup backlog | Start from `docs/feat/listing-agent-*.md`, `/listing`, and `backend/internal/listing`; verify callback and delivery contracts before cleanup. |
| `2026-05-27-liquidity-watchlist-chart-redesign.md` | Historical implementation/design note | Use current dashboard code and `docs/feat/liquidity-watchlist.md` for implementation behavior. |
| `2026-05-27-funding-rate-chart-ideas.md` | Idea backlog | Needs fresh product/design scoping before implementation. Current funding behavior is documented in `docs/feat/funding-rate.md`. |
| `2026-05-24-top30-history-trend.md` | Idea backlog | Needs fresh scoping against current Top30/backfill code and `backend/docs/top30-strategy.md`. |
| `progress.md` | Local progress note | Historical local session record only. Do not use it as a current task list. |

## Promotion rule

If a plan becomes an implementation contract, promote the relevant stable facts
into one of the tracked locations below instead of relying on the ignored plan
body:

- `docs/feat/*.md` for feature contracts.
- `backend/docs/runbook.md` for production operations.
- `deploy/README.md` or `deploy/aws-dev-local-rehearsal.md` for deployment.
- `README.md` / `AGENTS.md` for navigation and module boundaries.

After promotion, update this index so future agents do not execute stale plans.
