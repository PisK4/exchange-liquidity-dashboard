# CoinGecko rate-limit governance

## Status

Implementation contract. This document defines how EdgeX Ops Intelligence uses
CoinGecko without assuming that a demo API key or a higher quota is available.

## Background

The local Docker runtime starts one backend process with `--role=all`, so the
Liquidity Dashboard collector, CoinGecko historical backfill, and Listing Agent
enrichment can all call CoinGecko from the same process and egress IP. Without a
shared budget, a transient burst can trigger HTTP 429 and then keep retrying in
parallel, making the instance noisy while still not producing fresher data.

CoinGecko is therefore treated as a finite-budget external source rather than a
normal best-effort HTTP dependency.

## Consumers

| Consumer | Endpoints | Priority | Required behaviour |
|---|---|---|---|
| Liquidity Dashboard main collector | `/derivatives?include_tickers=unexpired` | primary | Use the shared budget. On 429/cooldown, serve stale cached derivatives when within `stale_cache_ttl`; mark derived rows `stale`. |
| Historical backfill | `/coins/bitcoin/market_chart/range`, `/exchanges/{id}/volume_chart` | backfill | Start after `backfill_boot_delay`; use low-priority budget; stop the current run on 429/cooldown. |
| Listing Decision Card enrichment | `/search`, `/coins/markets` | listing | Use the shared budget and local TTL caches for symbol→coin-id and market snapshot enrichment. |

## Configuration

Runtime configuration lives under `Runtime.coingecko` in
`config/edgex-ops-intelligence.yaml`.

```yaml
Runtime:
  coingecko:
    cache_ttl: 10m
    governance:
      enabled: true
      requests_per_minute: 4
      burst: 1
      default_cooldown: 15m
      max_cooldown: 1h
      stale_cache_ttl: 2h
      backfill_enabled: true
      backfill_boot_delay: 20m
      backfill_requests_per_minute: 2
      listing_coin_id_cache_ttl: 24h
      listing_market_snapshot_cache_ttl: 1h
```

The `COINGECKO_DEMO_API_KEY` environment variable may still be configured, but
the system must remain safe when it is absent. Do not depend on key-based quota
increases as the primary mitigation.

## Runtime contract

1. All CoinGecko HTTP calls in the backend must go through the shared
   `BudgetGovernor` wired in `cmd/ops-intelligence`.
2. A CoinGecko HTTP 429 must preserve the endpoint and `Retry-After` header in
   `RateLimitedError`.
3. `Retry-After` drives cooldown duration when present; otherwise
   `default_cooldown` is used and capped by `max_cooldown`.
4. The main collector may serve stale derivative cache during cooldown, but rows
   written from that cache must not be reported as fresh `complete` rows.
5. Backfill is lower priority than the main dashboard collector and must yield
   after the first 429/cooldown in a run.
6. Listing enrichment must avoid repeated `/search` and `/coins/markets` bursts
   via TTL caches.
7. Observability surfaces must expose governance state so operators can
   distinguish healthy, cooling-down, disabled, stale-cache, and backfill-yield
   states.

## Observability

`/api/collection-status` exposes a `coingecko` object containing the governor
snapshot, including `state`, `cache_state`, `last_endpoint`, `last_error`, and
cooldown metadata when available.

`/api/ops-intelligence/meta` exposes the same object under
`data_sources.coingecko.governance` together with the usual CoinGecko source
metadata.

## Non-goals

- Distributed cross-process quotas with Redis/MySQL coordination.
- Replacing CoinGecko as the historical volume source.
- Treating stale-cache results as live successful upstream pulls.
