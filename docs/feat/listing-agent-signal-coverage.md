# Listing Agent signal coverage contract

Status: implementation contract
Last updated: 2026-06-08

This contract defines the Listing Agent coverage extension for venues whose
instrument diff alone is insufficient to produce timely listing signals. It is
repo-local and must be kept in sync with `backend/internal/listing/*` and
`config/edgex-ops-intelligence.yaml`.

## Goals

- Keep `instrument_diff` as the market-existence source of truth.
- Add native `announcement_listing` coverage where an exchange exposes a
  reliable listing/changelog feed.
- Keep Activity Agent data as a supplemental backfill/audit source, not as an
  unbounded candidate source.
- Preserve low-risk behavior for spot announcements until operations explicitly
  opts spot into the prepare-listing workflow.

## Hyperliquid two-channel coverage

Hyperliquid uses two independent Listing Agent inputs:

| Channel | Source | Current role |
|---|---|---|
| `instrument_diff` | `POST https://api.hyperliquid.xyz/info` with `{"type":"meta"}` | Market existence, active/delisted state, baseline/diff snapshots. |
| `announcement_listing` | `GET https://dzjnlsk4rxci0.cloudfront.net/mainnet/entries.json` | Explicit listing/changelog titles used to discover listing intent before or alongside API diffs. |

Implementation entry points:

- Fetcher: `backend/internal/listing/fetcher/hyperliquid_ann.go`
- Parser: `backend/internal/listing/announcement/hyperliquid.go`
- Wiring: `backend/internal/listing/fetcher/build.go`
- Defaults: `backend/internal/config/config.go`
- Runtime config: `config/edgex-ops-intelligence.yaml`

Parser behavior:

- `New listing: NIL-USD perps` emits one high-confidence perp symbol.
- `New listing: HYPER-USD, ZORA-USD, and INIT-USDC perps` emits multiple perp
  symbols and preserves USD/USDC quote in `display_symbol`.
- `Added spot PUMP` and `Enabled spot BTC` emit spot symbols with medium
  confidence, but downstream scoring keeps spot low-risk.
- Delisting or validator delist titles are audit-only and never emit listing
  child signals.

The source injects `sourceModule=hyperliquid_entries` into the parser payload.
`announcement_listing` signal payloads may include `source_module` for audit and
dedupe review.

## Candidate and card rules

- Perp announcement signals remain candidate-bearing.
- Spot announcement signals can become candidates, but scoring only permits:
  - `record_only` for announcement-only / single-source spot evidence.
  - `watch` for dual-source or multi-platform spot evidence.
  - never `prepare_listing` under the current contract.
- Decision card titles are surface-aware:
  - `perp` or missing surface: `New Perp Listing Detected`
  - `spot`: `New Spot Listing Detected`
  - unknown surfaces: `New Listing Detected`

## Activity-derived supplemental path

Activity Agent often observes exchange activity/listing pages before the Listing
Agent has structured announcement coverage. To avoid mixing module semantics,
Activity-derived Listing ingestion must follow this contract:

1. **Allowlist only.** The first allowed pattern is Hyperliquid entries-style
   titles copied from Activity events whose source group is Hyperliquid
   `cloudfront_entries` or equivalent verified changelog evidence.
2. **Parser reuse.** Activity-derived Hyperliquid rows must be reshaped into the
   same raw shape consumed by `ParseHyperliquidAnnouncement`, with
   `sourceModule=activity_agent`.
3. **No generic import.** Do not import all Activity events with
   `activity_type=listing_trading_campaign` or `delisting_signal`; many are
   campaign/audit records and lack structured target symbols.
4. **Dedupe by native announcement identity.** When Activity provides the same
   Hyperliquid `hash`/`id` as the native feed, the existing announcement upsert
   and signal fingerprint should collapse duplicates.
5. **Delisting remains audit-only.** Activity-derived delist records must not
   become prepare-listing candidates.

This keeps Activity as a backfill/extractor input while Listing Agent owns
candidate fusion, scoring, cards, and delivery.

## Bybit and Bitget low-risk optimization

Bybit and Bitget remain announcement sources. This change only expands
observability and recent-listing coverage:

- Parser title compatibility now accepts listing/launch titles containing
  futures, USDT-M, USDT-margined, contract trading, or linear contract phrases.
- Announcement poll results include `ParseSkipReasons` so operators can
  distinguish audit-only spot/pre-market/irrelevant rows from parser failures.
- Bitget fetch window uses `limit=20` for a wider recent-listing page while
  preserving the `annType=coin_listings&language=en_US` filter.

No change here should materially increase Bybit/Bitget push strength without a
separate scoring/card contract update.

## Validation checklist

- `cd backend && go test ./internal/listing/...`
- `cd backend && make smoke-listing`
- Confirm `config/edgex-ops-intelligence.yaml` includes Hyperliquid under both
  `instrument_diff.polls` and `announcement.polls`.
