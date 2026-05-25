'use client';

import { useState } from 'react';
import { displayTierLabel, tierLabels, tierSeries } from '@/components/dashboard-shell';
import { LineChart } from '@/components/line-chart';
import { StatusEmptyState } from '@/components/status-empty-state';
import { bp, moneyAuto, pct, ratio, type LiquiditySnapshot } from '@/lib/api/client';
import { FUNDING_SIGN_CONVENTION_TOOLTIP, formatFundingDelta, formatFundingRate8h } from '@/lib/funding-format';

// DepthSide enumerates the three sides the V1 detail view shows as
// three separate charts (BID / ASK / 合计). The watchlist mini chart
// collapses these into one chart slot with a small pill-group toggle,
// so the operator can flip between them without giving up vertical
// space for three stacked charts per card.
type DepthSide = 'bid_usd' | 'ask_usd' | 'total_usd';
const SIDE_ITEMS: Array<{ value: DepthSide; label: string; aria: string }> = [
  { value: 'total_usd', label: '合计', aria: '合计深度曲线 BID + ASK' },
  { value: 'bid_usd', label: 'BID', aria: '买盘深度曲线 BID' },
  { value: 'ask_usd', label: 'ASK', aria: '卖盘深度曲线 ASK' },
];

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
// onExpand collapses the watchlist back to this single symbol so the
// operator can drop into the full V1 detail view (depth curves, depth
// detail table, funding column, etc.). Implemented as a click rather
// than a pure Link so we can also clear the localStorage entries for
// the other chips via the same code path that drives chip-remove.
export function WatchlistCard({
  canonical,
  displayName,
  snapshot,
  onExpand,
}: {
  canonical: string;
  displayName: string;
  snapshot: LiquiditySnapshot | null;
  onExpand?: () => void;
}) {
  // Each card owns its own DepthSide state so the operator can land on
  // BTC's ASK chart and ETH's 合计 chart simultaneously without one
  // toggle commandeering the others. The hook must run before any
  // early return to satisfy React's rules-of-hooks (same order every
  // render), so we declare it at the very top of the component body.
  const [side, setSide] = useState<DepthSide>('total_usd');

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
  const rows = snapshot.rows ?? [];
  const edgeRow = rows.find(r => r.platform === 'edgeX');
  const edgeDepth = edgeRow?.depth_by_tier?.[tier]?.total_usd;
  const edgeRatio = edgeRow?.vs_median_by_tier?.[tier];
  const fundingDelta =
    typeof kpis?.edgex_funding_rate_8h === 'number' && typeof kpis?.competitor_funding_rate_median_8h === 'number'
      ? kpis.edgex_funding_rate_8h - kpis.competitor_funding_rate_median_8h
      : null;
  const fundingMedianAvailable = kpis?.competitor_funding_rate_median_8h_status === 'complete';
  const sideItem = SIDE_ITEMS.find(item => item.value === side) ?? SIDE_ITEMS[0];
  const chartSeries = tierSeries(rows, side);
  const chartHasData = chartSeries.some(s => s.values.some(v => typeof v === 'number'));

  return (
    <section className="panel watchlist-card span-8 row-h-md" data-testid={`watchlist-card-${canonical}`}>
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        {onExpand && (
          <button
            type="button"
            className="watchlist-card-expand"
            onClick={onExpand}
            data-testid={`watchlist-card-expand-${canonical}`}
            title={`展开 ${displayName} 的深度曲线与明细表`}
          >
            查看明细 →
          </button>
        )}
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
            资金费率
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
      <div className="watchlist-card-chart-head">
        <span className="watchlist-card-chart-title">深度曲线</span>
        <span className="pill-group pill-group-mini" role="tablist" aria-label={`${displayName} 深度曲线方向`}>
          {SIDE_ITEMS.map(item => (
            <button
              key={item.value}
              type="button"
              role="tab"
              aria-selected={item.value === side}
              className={`pill pill-mini ${item.value === side ? 'active' : ''}`}
              onClick={() => setSide(item.value)}
              data-testid={`watchlist-card-side-${canonical}-${item.value}`}
            >
              {item.label}
            </button>
          ))}
        </span>
      </div>
      <div className="watchlist-card-chart" data-testid={`watchlist-card-chart-${canonical}`}>
        {chartHasData ? (
          <LineChart
            compact
            ariaLabel={`${canonical} ${sideItem.aria}`}
            labels={tierLabels.map(displayTierLabel)}
            series={chartSeries}
          />
        ) : (
          <div className="watchlist-card-chart-empty muted">该标的暂无可绘制的深度数据</div>
        )}
      </div>
    </section>
  );
}
