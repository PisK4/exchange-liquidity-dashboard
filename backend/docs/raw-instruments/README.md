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
| hyperliquid  | `perp`, `spot`, `perpdex-*` when emitted by adapter   |
| lighter      | `perp`, `spot`                                        |
| edgeX        | `perp-v1`, `perp-v2`, `spot`                          |

## How to refresh

### Automated (preferred)

`.github/workflows/catalog-refresh.yml` runs `make catalog` on the 1st of
every month at 03:30 UTC and opens a PR with the regenerated dumps + yaml.
You can also trigger it on demand from the GitHub Actions UI ("Run
workflow"). Review the diff per the PR checklist, merge, and redeploy the
backend so the new dumps land at `/app/docs/raw-instruments` for the
Top30 backfill goroutine to consume.

### Manual

Use this when GitHub-hosted runners are blocked from one of the
exchanges (binance / okx are the typical culprits) and the automated PR
fails:

```bash
cd backend
make catalog            # full refresh (requires network to all 10 exchanges)
```

This fetches every endpoint live, rewrites every `<platform>-<market>/<YYYY-MM-DD>.json`,
and regenerates two yaml files in `config/`:

- `instrument_catalog.yaml` — canonical whitelist per-platform fields
  consumed by depth adapters, Top30 backfill/catalog resolution, and listed
  universe regeneration.
- `listed_universe.yaml` — per-platform union of all listed base assets
  (perp + spot), consumed by the CoinGecko collector at runtime to fill the
  "edgeX 已上线?" column on the Top30 tab. Stale or absent universe entries
  collapse that column back to "否" with no badge.

Current runtime resolution is DB-first where dynamic Listing snapshots are
available: `t_listing_instrument_snapshot`, the runtime listed-universe file,
and the seed yaml work together to drive CatalogResolver and the CoinGecko
"edgeX 已上线?" checks. The static catalog YAML is still the cold-start seed,
adapter fallback, and review artifact; raw dumps are the audit trail behind
that seed. Do not treat `make catalog` as the only way production runtime state
changes once dynamic catalog ingestion is enabled.

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
   filters the configured canonical whitelist into runtime-ready instruments;
   raw dumps preserve the full exchange universe so you can rerun the filter
   for any new canonical without re-hitting the exchange.
2. **Drift forensics** — when MEXC `contract_size` flips or Lighter
   `market_id` is reassigned, the raw dump diff identifies what changed
   upstream vs. what changed in our filter logic.

Frontend URLs (`frontend_url`) and the human-verified `url_verified` flag
live in `config/instrument_catalog.yaml`, not here.
