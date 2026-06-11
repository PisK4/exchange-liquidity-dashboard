# Listing Agent Symbol Identity Normalization

Status: implementation contract
Last updated: 2026-06-11

This document defines the repo-local contract for Listing Agent symbol identity
normalization. It is the implementation reference for exchange-native symbol
aliases, runtime canonical identity, Lark card titles, listed-universe matching,
and safe historical backfill.

When this document conflicts with upstream design notes, the current source of
truth is code/config in this repository:

- `config/symbol_mapping.yaml`
- `backend/internal/config/canonical_index.go`
- `backend/internal/listing/symbol_identity.go`
- `backend/internal/listing/instrument/types.go`
- `backend/cmd/listing-symbol-backfill`

## 1. Background

Some exchanges expose non-crypto and synthetic perpetual markets with native
base symbols that are not the business identity operators expect to compare
across venues. MEXC stock-style perpetuals are the motivating incident:

| Exchange-native field | Example |
|---|---|
| `api_symbol` | `EBAYSTOCK_USDT` |
| `base_asset` | `EBAYSTOCK` |
| Exchange URL | `https://www.mexc.com/futures/EBAYSTOCK_USDT?type=linear_swap` |

If Listing Agent treats `EBAYSTOCK` as the candidate identity, Lark decision
cards and cross-platform comparisons produce a separate bucket from `EBAY`.
The operator-facing listing opportunity should instead be keyed and displayed
as the business identity `EBAY`, while preserving the exchange-native fields
for audit, source URLs, and API lookup.

## 2. Field semantics

Listing Agent must preserve two layers of identity.

### 2.1 Exchange-native identity

These fields describe what the source exchange actually returned. Do not rewrite
them during canonical normalization.

| Field | Meaning | Example |
|---|---|---|
| `api_symbol` | Native exchange API symbol / market id. | `EBAYSTOCK_USDT` |
| `base_asset` | Native exchange base asset. | `EBAYSTOCK` |
| `quote_asset` | Native exchange quote asset. | `USDT` |
| `source_url` / source metadata | Audit and operator jump target. | MEXC futures URL |

### 2.2 Business identity

These fields define how Listing Agent compares, deduplicates, scores, and
renders the opportunity.

| Field | Meaning | Example |
|---|---|---|
| `canonical_symbol` | Cross-platform business identity. | `EBAY` |
| `display_symbol` | UI/Lark display label. | `EBAY-USDT (perp)` |
| `display_name` | Optional human label. | `EBAY-USD` |
| `asset_category` | Asset class for review/audit. | `stock` |
| `market_surface` | Listing surface semantics. | `synthetic_futures` |
| `instrument_kind` | Instrument identity class. | `synthetic` |

Candidate identity is not `canonical_symbol` alone. The semantic key is:

```text
canonical_symbol + market_surface + instrument_kind
```

For the MEXC EBAY case, the expected identity is:

```text
canonical_symbol = EBAY
display_symbol   = EBAY-USDT (perp)
base_asset        = EBAYSTOCK
api_symbol        = EBAYSTOCK_USDT
market_surface   = synthetic_futures
instrument_kind  = synthetic
```

## 3. Source of truth

`config/symbol_mapping.yaml` is the runtime source of truth for known aliases.
Its `symbols[].aliases[platform][]` entries are consumed by both catalog-style
tooling and live Listing Agent ingestion. They are not build-only metadata.

Example:

```yaml
- canonical: EBAY
  display_symbol: EBAY-USDT (perp)
  display_name: EBAY-USD
  asset_category: stock
  market_surface: synthetic_futures
  instrument_kind: synthetic
  aliases:
    mexc: [EBAYSTOCK]
```

Alias entries should be exact when a venue exposes synthetic names such as
`EBAYSTOCK`, `AMCSTOCK`, `OUSTSTOCK`, or `PLSTOCK`. Do not apply a blind global
suffix-strip rule such as `*STOCK -> *`: it can conflate unrelated assets and
hide source-specific product semantics.

## 4. Runtime resolution order

`CanonicalIndex.ResolveIdentity(platform, base)` returns a `CanonicalIdentity`.
The resolver order is:

1. **Platform alias**: exact case-insensitive match for
   `aliases[platform][]`.
2. **Cross-platform alias**: only when the same alias maps to one canonical
   across all platforms.
3. **Canonical identity fallback**: when the base equals a single configured
   canonical with metadata.
4. **No match fallback**: uppercase base, `Matched=false`.
5. **Ambiguous match**: keep raw identity, `Matched=false`, `MatchKind=ambiguous`.

Conflict handling is fail-closed. If a raw base or alias is ambiguous, runtime
normalization must not pick one canonical silently. Operators should add a more
specific platform alias to `symbol_mapping.yaml`.

## 5. Runtime ingestion wiring

The identity applicator is intentionally early in the Listing Agent pipeline so
all downstream rows share the same semantic key.

| Path | Contract |
|---|---|
| Instrument poll | Normalize `NormalizedInstrument` before stable hash, diff, snapshot, and signal write. Preserve `api_symbol` and `base_asset`. |
| Announcement poll | Normalize parsed announcement symbols before announcement-symbol persistence and signal creation. |
| Signal fusion | Re-apply identity as a safety net for old or externally seeded unfused signals. |
| Decision-card market status refresh | Normalize live instruments before matching them to a candidate. |

`NormalizerVersion` is bumped when the hash-relevant identity recipe changes.
The v4 recipe applies runtime aliases before snapshot and signal persistence.
Because `StableHash` includes canonical identity fields, the version bump lets
the instrument poller upsert the new normalized snapshot while suppressing a
one-shot diff rollover caused only by the recipe change.

## 6. listed_universe semantics

`listed_universe` must not exclude `synthetic_futures` or other synthetic rows
just because they are non-canonical crypto assets. Listing Agent can and should
push synthetic opportunities when they satisfy the signal/scoring contract.

The runtime universe is identity-aware:

```text
IsListedIdentity(platform, canonical_symbol, market_surface, instrument_kind)
```

Behavior:

- If a platform has identity `entries`, exact identity matching is
  authoritative.
- If a platform has no identity entries, fallback to the legacy base-asset set.
- Synthetic rows from active snapshots should remain in runtime universe and in
  candidate reconciliation decisions.

This prevents a future `EBAY` spot/canonical product from incorrectly closing a
separate `EBAY synthetic_futures/synthetic` listing candidate, and also prevents
the reverse.

## 7. Decision-card display semantics

Lark decision cards display the business identity, not the exchange-native
base. The header should use `canonical_symbol` first, then `display_symbol` as a
fallback.

Title mapping:

| `market_surface` | Title prefix |
|---|---|
| `perp` | `New Perp Listing Detected` |
| missing | `New Perp Listing Detected` |
| `synthetic_futures` | `New Perp Listing Detected` |
| `spot` | `New Spot Listing Detected` |
| unknown | `New Listing Detected` |

For the MEXC EBAY case, the expected operator-facing card title is:

```text
New Perp Listing Detected · EBAY
```

Native fields such as `EBAYSTOCK` and `EBAYSTOCK_USDT` may still appear in
source evidence, source URLs, debug payloads, and market-status audit fields.

## 8. Safe historical backfill

Historical rows may already contain the old identity. Use the dedicated CLI;
do not patch MySQL by hand.

Dry run first:

```bash
cd backend
go run ./cmd/listing-symbol-backfill \
  --mysql-dsn 'user:pass@tcp(host:3306)/edgex_ops_intelligence?parseTime=true' \
  --from-canonical EBAYSTOCK --from-surface perp --from-kind canonical \
  --to-canonical EBAY --to-surface synthetic_futures --to-kind synthetic \
  --to-display 'EBAY-USDT (perp)'
```

Apply only after reviewing the dry-run report:

```bash
cd backend
go run ./cmd/listing-symbol-backfill \
  --mysql-dsn 'user:pass@tcp(host:3306)/edgex_ops_intelligence?parseTime=true' \
  --from-canonical EBAYSTOCK --from-surface perp --from-kind canonical \
  --to-canonical EBAY --to-surface synthetic_futures --to-kind synthetic \
  --to-display 'EBAY-USDT (perp)' \
  --execute
```

Safety rules:

- Execute mode acquires the Listing Agent run-once lease before changing rows.
- Candidate conflicts fail closed when both source and target identities exist.
- Pending/retry outbox payloads containing the old symbol fail closed, because
  they could still be delivered after the identity rewrite.
- Sent outbox payloads are audit history and must not be rewritten.
- Backfill updates identity columns in place; it does not delete and recreate
  candidates.

For broad alias expansion, first generate an alias gap report from DB evidence,
then add exact aliases to `symbol_mapping.yaml`, then run one dry-run/apply pair
per reviewed mapping. Do not bulk-strip suffixes across platforms.

## 9. Validation checklist

- `cd backend && go test ./internal/config/... ./internal/listing/...`
- `cd backend && make smoke-listing`
- Confirm `config/symbol_mapping.yaml` contains exact aliases for reviewed
  synthetic/native symbols.
- Confirm local/prod snapshots preserve native `base_asset` and `api_symbol`
  while normalizing `canonical_symbol`, `display_symbol`, `market_surface`, and
  `instrument_kind`.
- Confirm `t_listing_delivery_outbox` has no pending/retry rows containing the
  old symbol before executing backfill.
- Confirm historical sent outbox rows remain unchanged as audit evidence.

## 10. Related contracts

- `listing-agent-dynamic-catalog-integration.md`
- `listing-agent-signal-coverage.md`
- `listing-agent-decision-card-callback.md`
- `backend/docs/runbook.md`
