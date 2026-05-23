# V1 Platform x Canonical Coverage Matrix

**Generated**: 2026-05-23
**Source**: `config/instrument_catalog.yaml` (406 entries) and `config/symbol_mapping.yaml` (74 canonicals)

## Summary

- **74 canonicals total** in the symbol whitelist.
- **66 canonicals** have at least one supporting platform.
- **8 canonicals** are zero-platform (BIRD, BX, CBPS, DKNG, EWZ, RIVN, XYZ100, ZM); they are tracked in `config/unresolved_symbols.yaml` for ops review and rendered without crashing the dashboard (e2e regression guard added in C10).

## Coverage Matrix

Cells: `Y` = supported on that platform; `-` = unsupported (no api_symbol in catalog).

| Canonical | binance | bingx | bitget | bybit | edgeX | gate | hyperliquid | lighter | mexc | okx |
|-----------|---------|-------|--------|-------|-------|------|-------------|---------|------|-----|
| AAPL      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| AMD       | Y | Y | Y | - | Y | Y | - | Y | Y | Y |
| AMZN      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| ANTHROPIC | - | - | - | - | - | Y | - | - | Y | Y |
| AVGO      | Y | Y | Y | Y | Y | Y | - | - | Y | Y |
| BABA      | Y | - | Y | Y | - | Y | - | Y | Y | - |
| BNB       | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |
| BOTZ      | - | - | - | - | - | - | - | Y | - | - |
| BTC       | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |
| COIN      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| COPPER    | Y | Y | Y | - | Y | - | - | - | Y | - |
| COST      | - | - | Y | - | - | Y | - | - | Y | Y |
| CRCL      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| CRWV      | Y | - | Y | Y | - | Y | - | Y | Y | Y |
| DIA       | Y | Y | Y | Y | - | Y | - | Y | Y | - |
| DOGE      | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |
| DRAM      | Y | Y | Y | Y | - | Y | - | - | Y | Y |
| ETH       | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |
| EWJ       | Y | Y | Y | Y | - | Y | - | - | Y | Y |
| EWY       | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| GME       | - | Y | Y | - | - | - | - | Y | Y | Y |
| GOLD      | Y | Y | Y | Y | Y | Y | Y | Y | Y | - |
| GOOG      | Y | Y | Y | Y | Y | Y | - | Y | Y | - |
| HIMS      | - | - | - | - | - | Y | - | - | Y | Y |
| HOOD      | Y | Y | Y | Y | Y | Y | - | Y | - | Y |
| HYUNDAI   | - | - | - | - | - | - | - | Y | - | - |
| IAU       | - | - | - | - | Y | Y | - | - | - | - |
| INTC      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| IWM       | - | - | - | Y | - | Y | - | Y | - | Y |
| JP225     | - | Y | - | - | - | - | - | - | Y | - |
| KR200     | - | Y | - | - | - | - | - | - | - | - |
| LITE      | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| LLY       | - | - | Y | Y | - | Y | - | - | Y | Y |
| MAGS      | - | - | - | - | - | - | - | Y | - | - |
| META      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| MRVL      | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| MSFT      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| MSTR      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| MU        | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| NATGAS    | Y | Y | Y | - | Y | - | - | Y | - | - |
| NFLX      | - | Y | Y | - | - | Y | - | - | Y | Y |
| NVDA      | Y | Y | Y | Y | Y | Y | - | Y | - | Y |
| OIL       | Y | Y | - | - | Y | - | - | - | Y | - |
| OPENAI    | - | Y | - | - | - | Y | - | - | Y | Y |
| ORCL      | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| PALLADIUM | Y | Y | Y | - | - | Y | - | - | Y | - |
| PLATINUM  | Y | Y | Y | - | - | Y | - | Y | Y | - |
| PLTR      | Y | Y | Y | Y | Y | Y | - | Y | Y | Y |
| QQQ       | Y | Y | Y | Y | Y | - | - | Y | Y | Y |
| RKLB      | Y | Y | Y | Y | - | Y | - | - | Y | Y |
| SAMSUNG   | - | Y | - | - | - | - | - | Y | Y | - |
| SILVER    | Y | Y | Y | Y | Y | Y | - | Y | Y | - |
| SKHYNIX   | - | Y | - | - | - | - | - | Y | Y | - |
| SLV       | - | - | - | - | Y | - | - | - | - | - |
| SNDK      | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| SOL       | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |
| SOXX      | - | - | - | - | - | - | - | Y | Y | - |
| SPACEX    | - | Y | - | - | - | Y | - | Y | Y | Y |
| TSLA      | Y | Y | Y | Y | Y | Y | - | Y | - | Y |
| TSM       | Y | Y | Y | Y | - | Y | - | Y | Y | Y |
| URA       | - | - | - | - | - | - | - | Y | - | - |
| URNM      | - | - | - | - | - | - | - | - | - | Y |
| US500     | Y | Y | Y | Y | Y | - | Y | Y | Y | - |
| WHEAT     | - | Y | - | - | - | - | - | Y | - | - |
| XLE       | - | Y | - | - | - | - | - | - | Y | Y |
| XRP       | Y | Y | Y | Y | Y | Y | Y | Y | Y | Y |

## Zero-platform canonicals

These eight canonicals are listed in `config/symbol_mapping.yaml` per
the V1 product brief but currently have no supporting exchange. They
are surfaced in `config/unresolved_symbols.yaml` for ops follow-up.

| Canonical | Notes (from §4.3 of the requirements doc) |
|-----------|--------------------------------------------|
| BIRD      | Pre-IPO synthetic candidate; no live exchange listing yet. |
| BX        | Blackstone Inc.; spec lists it but no exchange has launched a perp/synthetic. |
| CBPS      | CB Payments Series; pre-IPO candidate. |
| DKNG      | DraftKings Inc.; no exchange has launched a perp. |
| EWZ       | Brazil ETF; specced but no perp listings on the 10 platforms. |
| RIVN      | Rivian Automotive; no exchange has launched a perp. |
| XYZ100    | Hypothetical / placeholder used in the requirements doc. |
| ZM        | Zoom Video Communications; no exchange has launched a perp. |

## EdgeX-only canonicals (V1 reality check)

EdgeX exposes BTC/ETH/SOL plus a sample of US equity / commodity
synthetics. The V1 invariant is that BTC/ETH/SOL on EdgeX uses **live
data from the real adapter** -- the dashboard never fabricates depth,
volume, or quality metrics for these.

## Counts by platform

| Platform     | Canonicals supported |
|--------------|---------------------:|
| binance      | 43 |
| bingx        | 51 |
| bitget       | 46 |
| bybit        | 39 |
| edgeX        | 30 |
| gate         | 47 |
| hyperliquid  | 8  |
| lighter      | 47 |
| mexc         | 52 |
| okx          | 43 |
| **Total**    | **406 (platform, canonical) entries** |
