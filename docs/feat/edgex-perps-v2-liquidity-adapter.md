# EdgeX Perps V2 Liquidity Adapter Contract

## Status

Current implementation contract for the active EdgeX Perps V2 surface-aware
Liquidity Dashboard data path. This contract complements the upstream research note at
`../../architecture/方案设计/EdgeX运营/盘口数据精炼/EdgeX-Perps-V2-盘口深度采集方案.md`.

## Goals

- Keep EdgeX Perps V1 live-data invariants intact.
- Add EdgeX Perps V2 as a distinct EdgeX market surface, not as fabricated V1
  fallback data.
- Reuse the existing four-tier depth contract from `adapter-four-tier-depth.md`.
- Make runtime rows unique across V1/V2/Spot surfaces before relying on V2
  depth, volume, share, history, Top30, or alert data.

## Runtime identity

Liquidity runtime rows must be uniquely identified by surface-aware fields:

```text
row_key = platform
        + market_surface
        + instrument_kind
        + canonical_symbol
        + venue_symbol_or_contract_id
        + lineage
```

Recommended EdgeX values:

| Surface | platform | market_surface | lineage | display_platform |
|---|---|---|---|---|
| Perps V1 | `edgeX` | `perp_v1` | `edgeX-perp-v1` | `edgeX V1` |
| Perps V2 | `edgeX` | `perp_v2` | `edgeX-perp-v2` | `edgeX V2` |
| Spot | `edgeX` | `spot` | `edgeX-spot` | `edgeX Spot` |

Short-term compatibility aliases may exist internally, but API/UI rows must
publish `platform_group=edgeX`, `is_edgex=true`, and the real `market_surface`
so operators do not confuse V2 with a competitor venue.

## API row metadata

Rows emitted by Liquidity, Quality, Share, Top30, funding, and collection-status
surfaces should expose enough metadata for the frontend to avoid hard-coded
`platform == "edgeX"` decisions:

```text
platform
platform_group
is_edgex
market_surface
instrument_kind
lineage
canonical_symbol
venue_symbol
contract_id
base_asset
quote_asset
source_endpoint
depth_source
```

The legacy `edgex_*` KPI fields may remain during migration, but their meaning
must be documented as either the primary EdgeX surface or an aggregate EdgeX
view. They must not silently switch between V1 and V2.

## REST metadata and fallback path

Pilot symbols are BTCUSDC, ETHUSDC, and SOLUSDC unless a later contract names a
different pilot set. Current V2 orderbook depth prefers the WebSocket local
book when healthy; REST remains the bounded fallback for startup, stale,
sequence-gap, or upstream WS failure cases.

Current V2 orderbook source priority:

| Priority | Collector | Depth source | Source ID | Meaning |
|---|---|---|---|---|
| 1 | `ws_orderbook` | `ws_local_book` | `edgeX-perp-v2-ws-depth-200` | Healthy V2 local book from WebSocket. |
| 2 | `rest_orderbook` | `rest_snapshot` | `edgeX-perp-v2-rest-depth-200` | Explicit bounded REST fallback; never fabricated completeness. |

| Need | V2 source |
|---|---|
| Metadata | `GET https://edgex-prod-v2.edgex.exchange/api/v2/public/meta/getMetaData` |
| Depth | `GET https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getDepth?contractId={id}&level=200` |
| Ticker / 24h volume | `GET https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getTicker?contractId={id}` |

Depth responses use `data[0].bids` / `data[0].asks`. Ticker responses should
prefer `value` as quote-notional USD/USDC volume; `size` is base volume.

REST fallback source metadata:

```text
depth_source = rest_snapshot
source_id = edgeX-perp-v2-rest-depth-200
api_level_cap = 200
```

## Depth completeness rules

V2 `level=200` is not automatically complete for 1% or 2% tiers. Every snapshot
must calculate:

```text
farthest_bid_pct
farthest_ask_pct
strict_complete
policy_acceptance
partial_reason
```

If the returned book does not cover a tier, publish partial lower-bound data via
the existing four-tier depth contract. Do not mark a tier complete only because
200 levels were returned.

## Fallback rules

- Do not fall back from V2 orderbook failure to V1 orderbook success.
- Do not use CoinGecko to fabricate V2 depth, spread, imbalance, or slippage.
- Missing V2 data must surface `unsupported`, `error`, `stale`, `partial`, or
  `insufficient_history` as appropriate.
- Keep MEXC and Gate volume discounts scoped to volume/share only.

## History, share, and Top30

Share/history/backfill infrastructure already exists. V2 integration must make
the keys surface-aware instead of reusing V1 history.

Recommended daily history key dimensions:

```text
day
platform
market_surface
canonical_symbol_or_display_symbol
lineage_or_contract_id
data_source
```

V2 history starts from the first valid V2 row. Do not migrate V1 BTCUSD history
into V2 BTCUSDC history. Until enough V2 history accumulates, return explicit
`insufficient_history` or `partial` statuses.

Top30 listed state should expose which EdgeX surface is listed:

```text
edgeX V1 listed?
edgeX V2 listed?
edgeX Spot listed?
target surface listed?
```

## WebSocket local book

V2 depth prefers a WebSocket local book when the provider is enabled and healthy.
REST remains the bounded fallback so operators still get explicit lower-bound
depth rows instead of fabricated completeness when WS is not ready, stale, or
detects a version gap.

Endpoint:

```text
wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws
```

Expected subscriptions:

```json
{"type":"subscribe","channel":"ticker.all.1s"}
{"type":"subscribe","channel":"depth.30000001.200"}
```

Real depth frames are delivered as `quote-event` messages whose `dataType` and
`data` fields are nested under `content`. The parser must support both this
`quote-event.content` wrapper and the legacy top-level `dataType` / `data`
shape so tests and local replay fixtures remain compatible.

WS depth source metadata:

```text
depth_source = ws_local_book
source_id = edgeX-perp-v2-ws-depth-200
source_endpoint = wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws
```

REST fallback source metadata remains:

```text
depth_source = rest_snapshot
source_id = edgeX-perp-v2-rest-depth-200
source_endpoint = https://edgex-prod-v2.edgex.exchange/api/v2/public/quote/getDepth?contractId={id}&level=200
```

The WS implementation must apply snapshot and changed frames, enforce version
continuity with `startVersion` / `endVersion` / `version`, detect gaps, rebuild
from the next snapshot, reconnect with backoff, mark stale local books, and
surface diagnostics through collection-status. WS failure may fall back to REST
lower-bound display, but must not fabricate strict completeness.

## Operations and data quality

Runbook and collection status should make these states diagnosable:

- V2 metadata stale.
- V2 REST depth or ticker errors.
- V2 row missing.
- V1/V2 surface collision.
- V2 history stuck in `insufficient_history` too long.
- V2 WS stale, sequence reset, REST fallback, parser regression, or rebuild
  loop. REST fallback logs should include the `edgeX perp v2 ws fallback to
  REST` prefix and the concrete snapshot failure reason.

Alerts attributed to V2 must include `platform=edgeX`, `display_platform=edgeX
V2`, `market_surface=perp_v2`, `lineage=edgeX-perp-v2`, `depth_status`,
`snapshot_ts`, and `source_endpoint`.
