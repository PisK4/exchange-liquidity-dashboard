# Top30 history trend API plan

## Goal

Expose historical Top30 ranking movement from the retained
`t_top30_snapshot` data without changing the current V1 Top30 table behavior.

## Candidate endpoint

`GET /api/snapshot/top30/trend?platform=<platform>&symbol=<symbol>&window=7d|30d`

Response shape:

```json
{
  "platform": "binance",
  "symbol": "BTC-USDT (perp)",
  "window": "7d",
  "status": "complete",
  "points": [
    {
      "snapshot_ts": "2026-05-24T00:00:00Z",
      "rank": 1,
      "volume_24h_usd": 123456789
    }
  ]
}
```

## Data rules

- Read from `t_top30_snapshot`; do not derive synthetic rankings from ticker
  snapshots.
- Keep CoinGecko as the source for rolling 24h Top30 rank/volume.
- Treat gaps explicitly: return `insufficient_history` when the requested
  window has no retained rows.
- Do not mix native backfill 7d volume into this endpoint; native history
  remains responsible for `volume_7d_usd` and `delta_7d_pct` enrichment.

## Phasing

1. Add repository query over retained `t_top30_snapshot` rows.
2. Add `/api/snapshot/top30/trend` handler and OpenAPI schema.
3. Add frontend chart only after API contract stabilizes.
4. Add retention/index review if query volume increases.
