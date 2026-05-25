import { StatusEmptyState } from '@/components/status-empty-state';
import { bp, moneyAuto, pct, ratio, type LiquiditySnapshot } from '@/lib/api/client';
import { FUNDING_SIGN_CONVENTION_TOOLTIP, formatFundingDelta, formatFundingRate8h } from '@/lib/funding-format';

// WatchlistCard renders one symbol's vitals as a compact summary tile.
// The grid layout mirrors the per-row content of the existing single-
// symbol Liquidity view but condensed into one card, so the operator
// can scan multiple symbols side-by-side without scrolling vertically.
//
// Five KPI rows (mirroring spec §3.4 mock):
//   1. edgeX ±0.1% 总深度  - the headline depth number
//   2. vs 竞品中位数        - depth ratio at the same tier
//   3. spread (10min 均值)  - smoothed spread reading
//   4. spread (当前)        - latest tick spread
//   5. 7d share             - per-symbol 7d share %
//   6. 资金费率 (8h 当量)   - normalised funding + vs-median delta
//
// Each KPI gracefully renders '—' when the underlying value is missing
// so the card never collapses to an empty box; if the entire snapshot
// failed to load (e.g. the symbol resolver couldn't find a canonical
// match) we surface a StatusEmptyState instead of the KPI grid.
export function WatchlistCard({
  canonical,
  displayName,
  snapshot,
}: {
  canonical: string;
  displayName: string;
  snapshot: LiquiditySnapshot | null;
}) {
  if (!snapshot) {
    return (
      <section className="panel watchlist-card span-8 row-h-md" data-testid={`watchlist-card-${canonical}`}>
        <div className="panel-head">
          <span className="panel-title">{displayName}</span>
          <span className="panel-tag muted">未加载</span>
        </div>
        <StatusEmptyState status="stale" message="尚未拉取到该标的的快照" />
      </section>
    );
  }

  const kpis = snapshot.kpis;
  const tier = '0.10%';
  const edgeRow = snapshot.rows?.find(r => r.platform === 'edgeX');
  const edgeDepth = edgeRow?.depth_by_tier?.[tier]?.total_usd;
  const edgeRatio = edgeRow?.vs_median_by_tier?.[tier];
  const fundingDelta =
    typeof kpis?.edgex_funding_rate_8h === 'number' && typeof kpis?.competitor_funding_rate_median_8h === 'number'
      ? kpis.edgex_funding_rate_8h - kpis.competitor_funding_rate_median_8h
      : null;
  const fundingMedianAvailable = kpis?.competitor_funding_rate_median_8h_status === 'complete';

  return (
    <section className="panel watchlist-card span-8 row-h-md" data-testid={`watchlist-card-${canonical}`}>
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        <span className="panel-tag muted">自选</span>
      </div>
      <dl className="watchlist-kpis">
        <div>
          <dt>edgeX ±0.1% 总深度</dt>
          <dd>{moneyAuto(edgeDepth)}</dd>
        </div>
        <div>
          <dt>vs 竞品中位数</dt>
          <dd>{ratio(edgeRatio)}</dd>
        </div>
        <div>
          <dt>spread (10min 均值)</dt>
          <dd>{typeof kpis?.edgex_spread_10m_bp === 'number' ? bp(kpis.edgex_spread_10m_bp) : '—'}</dd>
        </div>
        <div>
          <dt>spread (当前)</dt>
          <dd>{bp(kpis?.edgex_spread_bp)}</dd>
        </div>
        <div>
          <dt>7d share</dt>
          <dd>{typeof kpis?.symbol_share_7d_pct === 'number' ? pct(kpis.symbol_share_7d_pct) : '—'}</dd>
        </div>
        <div>
          <dt>
            资金费率 (8h 当量)
            <span className="info-icon" aria-label="资金费率 sign convention" title={FUNDING_SIGN_CONVENTION_TOOLTIP}> ⓘ</span>
          </dt>
          <dd>
            {formatFundingRate8h(kpis?.edgex_funding_rate_8h)}
            {fundingMedianAvailable && (
              <span className="watchlist-funding-delta muted">  vs 中位数 {formatFundingDelta(fundingDelta)}</span>
            )}
          </dd>
        </div>
      </dl>
    </section>
  );
}
