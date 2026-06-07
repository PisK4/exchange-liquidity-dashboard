# Top30 acquisition strategy

## Source of truth

Top30 rows come from CoinGecko `GET /api/v3/derivatives?include_tickers=unexpired`.
The collector maps CoinGecko `market` display names to internal platform ids,
normalizes symbols, keeps the highest 24h USD volume ticker per
`(platform, display_symbol)`, then sorts each platform by rolling 24h volume
and stores the top 30 rows in `t_top30_snapshot`.

## Cadence

`config/edgex-ops-intelligence.yaml` controls this through
`Runtime.coingecko.pull_interval` and keeps it aligned with the dashboard
collection cadence. This improves ranking freshness for CoinGecko's rolling
24h volume, especially around ranks 25-30 where symbols can move in and out.

`cache_ttl` is `0s`: the periodic collector is the only live reader today, so
an in-process CoinGecko ticker cache would obscure freshness without saving
meaningful request volume.

This cadence change does not fill 7d/30d history faster. History is populated
by native exchange backfill, not by the CoinGecko `/derivatives` response.

## 24h vs historical volume

- 24h Top30 ranking and platform share use CoinGecko derivatives data for a
  consistent cross-exchange view.
- 7d volume and 7d delta use native exchange daily kline backfill because
  CoinGecko Demo endpoints do not provide perp-symbol-level 7d history.

## Insufficient history states

Top30 rows start with `insufficient_history` for 7d volume and 7d delta. The
API fills these fields only after `dailySymbolVolumes` has at least seven
positive UTC-day rows for the current window. Delta additionally needs seven
positive UTC-day rows in the previous window.

Common causes:

1. A symbol just entered Top30 and has not reached the next backfill run.
2. `CatalogResolver` cannot map the CoinGecko base asset to a platform
   instrument.
3. A platform adapter returns `instrument_not_found` for a CoinGecko-reported
   market.
4. A non-USD/USDC/USDT quote cannot be canonicalized into the dashboard's
   current `BASE-USDT (perp)` daily key.
5. The native kline endpoint has fewer than seven positive UTC-day rows in
   the window.

The current collector wires `Top30Backfiller` after the first CoinGecko
derivatives round, so newly observed Top30 symbols can schedule native kline
backfill through the platform catalog resolver. Remaining product debt: expose
the concrete skip reason separately from ordinary history warm-up instead of
collapsing all unavailable history into `insufficient_history`.
