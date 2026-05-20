# raw-instruments

Per-(platform, market_type) raw snapshots of each exchange's public
instrument-list endpoint. Written by `backend/scripts/build-catalog` and used
as the audit trail for `config/instrument_catalog.yaml`.

## Layout

```
backend/docs/raw-instruments/
  <platform>-<market_type>/
    <YYYY-MM-DD>.json   # one per crawler run, pretty-printed + key-sorted
```

`<platform>` matches the slug used in `config/symbol_mapping.yaml`'s
`platforms:` list. `<market_type>` is the per-platform slug emitted by
`adapter.FetchInstruments`:

| Platform     | Market types covered                                  |
| ------------ | ----------------------------------------------------- |
| binance      | `spot`, `usd-m`, `coin-m`                             |
| okx          | `spot`, `swap`, `futures`                             |
| bybit        | `spot`, `linear`, `inverse`                           |
| bitget       | `spot`, `usdt-futures`, `coin-futures`, `usdc-futures`|
| bingx        | `spot`, `swap`                                        |
| mexc         | `spot`, `contract`                                    |
| gate         | `spot`, `futures-usdt`                                |
| hyperliquid  | `perp`, `spot`                                        |
| lighter      | `perp`, `spot`                                        |
| edgeX        | `perp-v1`, `perp-v2`, `spot`                          |

## How to refresh

Monthly cadence (or whenever you suspect an exchange has changed):

```bash
cd backend
make catalog            # full refresh (requires network to all 10 exchanges)
```

This fetches every endpoint live, rewrites every `<platform>-<market>/<YYYY-MM-DD>.json`,
and regenerates two yaml files in `config/`:

- `instrument_catalog.yaml` — canonical BTC/ETH/SOL per-platform fields
  consumed by the depth adapters.
- `listed_universe.yaml` — per-platform union of all listed base assets
  (perp + spot), consumed by the CoinGecko collector at runtime to fill the
  "edgeX 已上线?" column on the Top30 tab. Stale or absent universe entries
  collapse that column back to "否" with no badge.

The catalog YAML is the runtime source-of-truth; raw dumps are the audit trail.

If you only have partial network access (e.g. you can reach Gate/Hyperliquid/Lighter
but Binance is blocked), use the `--raw-only` mode to refresh raw dumps without
overwriting the catalog yaml:

```bash
cd backend
make catalog-raw        # raw dumps only, yaml untouched
```

`make catalog` itself will refuse to write a degraded catalog yaml when some
platforms fail; pass `--allow-partial` only if you intentionally want to shrink
the catalog.

## How to verify drift

Inspect git diff after `make catalog`. The raw JSON is pretty-printed with
recursively sorted keys, so meaningful drift (status flip, new symbol,
contract_size change, …) shows up as a localized diff rather than a wholesale
reformat.

CI runs the equivalent of `make catalog-diff`, which fails if the generated
catalog differs from the committed file (timestamp ignored).

## How to clean up old snapshots

Retention is intentionally manual. Delete old dated JSON files as they
outlive their audit usefulness:

```bash
git rm backend/docs/raw-instruments/*/2024-*.json
```

## Why save raw dumps at all

Two reasons:

1. **Reproducibility** — the structured `config/instrument_catalog.yaml`
   filters BTC / ETH / SOL perp; raw dumps preserve the full universe so
   you can rerun the filter for any new canonical without re-hitting the
   exchange.
2. **Drift forensics** — when MEXC `contract_size` flips or Lighter
   `market_id` is reassigned, the raw dump diff identifies what changed
   upstream vs. what changed in our filter logic.

Frontend URLs (`frontend_url`) and the human-verified `url_verified` flag
live in `config/instrument_catalog.yaml`, not here.
