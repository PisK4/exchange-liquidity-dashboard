'use client';

import { BarChart } from '@/components/chart-primitives';
import { StatusEmptyState } from '@/components/status-empty-state';
import { bp, pct, type LiquiditySnapshot } from '@/lib/api/client';
import { FUNDING_SIGN_CONVENTION_TOOLTIP, formatFundingDelta, formatFundingPeriodTag, formatFundingRate8h } from '@/lib/funding-format';

// QualityCard reads a LiquiditySnapshot-shaped object: that snapshot
// type owns the spread / share KPIs the card surfaces, while
// PlatformRow (shared between liquidity and quality) carries the
// per-bucket worst_slippage_bp + verdict the bar chart needs.
//
// In production the liquidity endpoint leaves worst_slippage_bp /
// verdict null — only /api/snapshot/quality fills them — so the
// caller is expected to merge the quality fan-out's rows into the
// liquidity snapshot before handing it to the card. See
// dashboard-shell.tsx → buildQualityCardSnapshot for the merge.
//
// Bucket selection is global — the toolbar above the card grid owns
// query.bucket so 'compare 滑点 across symbols at the same volume tier'
// is the default user model. Per-card overrides would defeat that
// comparison, so we intentionally do not expose a per-card pill set.

const verdictBadgeClass: Record<string, string> = {
  healthy: 'b-ok', 健康: 'b-ok',
  watch: 'b-warn', 关注: 'b-warn',
  poor: 'b-bad', 较差: 'b-bad',
  unsupported: 'b-mute',
};

function bucketShortLabel(bucket: string) {
  const amount = Number(bucket);
  if (!Number.isFinite(amount)) return bucket;
  if (amount >= 1_000_000) return `${amount / 1_000_000}M`;
  return `${amount / 1_000}K`;
}

function slippageUSD(bucket: string, slippageBp?: number) {
  const amount = Number(bucket);
  return typeof slippageBp === 'number' && Number.isFinite(amount) ? amount * slippageBp / 10_000 : undefined;
}

function spreadUSD(mid?: number, spreadBp?: number) {
  if (typeof mid !== 'number' || typeof spreadBp !== 'number') return undefined;
  return mid * spreadBp / 10_000;
}

function signedPct(value?: number) {
  if (typeof value !== 'number') return '—';
  return `${value > 0 ? '+' : ''}${value.toFixed(2)}%`;
}

function verdictLabel(verdict?: string) {
  if (!verdict) return '—';
  if (verdict === 'healthy' || verdict === '健康') return '健康';
  if (verdict === 'watch' || verdict === '关注') return '关注';
  if (verdict === 'poor' || verdict === '较差') return '较差';
  if (verdict === 'unsupported') return '未支持';
  return verdict;
}

export function QualityCard({
  canonical,
  displayName,
  snapshot,
  bucket,
  buckets,
  onExpand,
}: {
  canonical: string;
  displayName: string;
  snapshot: LiquiditySnapshot | null;
  bucket: string;
  buckets: string[];
  onExpand?: () => void;
}) {
  if (!snapshot) {
    return (
      <section className="panel quality-card span-8 row-h-md" data-testid={`quality-card-${canonical}`}>
        <div className="panel-head">
          <span className="panel-title">{displayName}</span>
          <span className="panel-tag muted">未加载</span>
        </div>
        <StatusEmptyState status="stale" message="尚未拉取到该标的的快照" />
      </section>
    );
  }

  const kpis = snapshot.kpis;
  const rows = snapshot.rows ?? [];
  const edgeRow = rows.find(r => r.platform === 'edgeX');
  const edgeSlippage = edgeRow?.worst_slippage_bp?.[bucket];
  const edgeSlippageUSD = slippageUSD(bucket, edgeSlippage);
  const edgeSpreadUSD = spreadUSD(edgeRow?.mid_price, kpis?.edgex_spread_bp);
  const fundingDelta =
    typeof kpis?.edgex_funding_rate_8h === 'number' && typeof kpis?.competitor_funding_rate_median_8h === 'number'
      ? kpis.edgex_funding_rate_8h - kpis.competitor_funding_rate_median_8h
      : null;
  const fundingMedianAvailable = kpis?.competitor_funding_rate_median_8h_status === 'complete';

  // Competitor median slippage @ bucket — computed client-side across
  // platforms that report a finite worst_slippage_bp at this bucket.
  // Excludes edgeX so the 'vs median' delta is a true competitor
  // comparison. This is the same approach the V1 detail BarChart uses
  // when it sorts by bp ascending.
  const competitorSlippages = rows
    .filter(r => r.platform !== 'edgeX')
    .map(r => r.worst_slippage_bp?.[bucket])
    .filter((v): v is number => typeof v === 'number' && Number.isFinite(v))
    .sort((a, b) => a - b);
  const competitorMedian = competitorSlippages.length
    ? (competitorSlippages.length % 2 === 1
        ? competitorSlippages[(competitorSlippages.length - 1) / 2]
        : (competitorSlippages[competitorSlippages.length / 2 - 1] + competitorSlippages[competitorSlippages.length / 2]) / 2)
    : undefined;
  const slippageDelta =
    typeof edgeSlippage === 'number' && typeof competitorMedian === 'number'
      ? edgeSlippage - competitorMedian
      : null;

  // Mini BarChart: edgeX 模拟滑点 across all configured USD buckets.
  // x = bucket label ('50K' / '100K' / '500K' / '1M'), y = bp.
  // Each bar is a single edgeX reading at that bucket, not a per-
  // platform comparison — bucket-by-bucket shape is the value here,
  // and any cross-platform comparison is already given by the KPI
  // row's 'vs 中位数' delta.
  const slippageChartRows = buckets.map(b => {
    const bpValue = edgeRow?.worst_slippage_bp?.[b];
    return {
      label: bucketShortLabel(b),
      value: typeof bpValue === 'number' ? bpValue : undefined,
      color: '#73bf69',
    };
  });

  return (
    <section className="panel quality-card span-8 row-h-md" data-testid={`quality-card-${canonical}`}>
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        <span className={`badge ${verdictBadgeClass[edgeRow?.verdict ?? ''] ?? 'b-mute'} quality-card-verdict`} data-testid={`quality-card-verdict-${canonical}`}>
          {verdictLabel(edgeRow?.verdict)}
        </span>
        {onExpand && (
          <button
            type="button"
            className="watchlist-card-expand"
            onClick={onExpand}
            data-testid={`quality-card-expand-${canonical}`}
            title={`展开 ${displayName} 的盘口明细与三 BarChart 视图`}
          >
            查看明细 →
          </button>
        )}
      </div>
      <dl className="watchlist-kpis">
        <div>
          <dt>edgeX spread (10min 均值)</dt>
          <dd>{typeof kpis?.edgex_spread_10m_bp === 'number' ? bp(kpis.edgex_spread_10m_bp) : '—'}</dd>
        </div>
        <div>
          <dt>edgeX spread (当前)</dt>
          <dd>
            {bp(kpis?.edgex_spread_bp)}
            {typeof edgeSpreadUSD === 'number' && (
              <span className="watchlist-funding-delta muted">  · ${edgeSpreadUSD.toFixed(2)}</span>
            )}
          </dd>
        </div>
        <div>
          <dt>Imbalance</dt>
          <dd>{signedPct(edgeRow?.imbalance_pct)}</dd>
        </div>
        <div>
          <dt>滑点 @ {bucketShortLabel(bucket)} USD</dt>
          <dd>
            {typeof edgeSlippage === 'number' ? bp(edgeSlippage) : '—'}
            {typeof edgeSlippageUSD === 'number' && (
              <span className="watchlist-funding-delta muted">  · ${edgeSlippageUSD.toFixed(0)}</span>
            )}
          </dd>
        </div>
        <div>
          <dt>vs 竞品中位数滑点</dt>
          <dd>
            {typeof slippageDelta === 'number'
              ? `${slippageDelta >= 0 ? '+' : ''}${slippageDelta.toFixed(2)} bp`
              : '—'}
          </dd>
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
            {formatFundingPeriodTag(kpis?.edgex_funding_rate_period_hours) && (
              <span className="watchlist-funding-period muted">  · {formatFundingPeriodTag(kpis?.edgex_funding_rate_period_hours)}</span>
            )}
            {fundingMedianAvailable && (
              <span className="watchlist-funding-delta muted">  vs 中位数 {formatFundingDelta(fundingDelta)}</span>
            )}
          </dd>
        </div>
      </dl>
      <div className="watchlist-card-chart-head">
        <span className="watchlist-card-chart-title">edgeX 滑点</span>
        <span className="muted" style={{ fontSize: 10 }}>USD</span>
      </div>
      <div className="quality-card-chart" data-testid={`quality-card-chart-${canonical}`}>
        {slippageChartRows.some(r => typeof r.value === 'number') ? (
          <BarChart
            rows={slippageChartRows}
            format={value => `${(value ?? 0).toFixed(2)} bp · ${typeof value === 'number' ? `$${(slippageUSD(buckets[slippageChartRows.findIndex(r => r.value === value)] ?? bucket, value) ?? 0).toFixed(0)}` : ''}`}
          />
        ) : (
          <div className="watchlist-card-chart-empty muted">该标的暂无可绘制的滑点数据</div>
        )}
      </div>
      <div className="quality-card-foot muted">
        mid {typeof edgeRow?.mid_price === 'number' ? `$${edgeRow.mid_price.toLocaleString()}` : '—'}
        {' · '}
        竞品中位数 @ {bucketShortLabel(bucket)}: {typeof competitorMedian === 'number' ? `${competitorMedian.toFixed(2)} bp` : '—'}
      </div>
    </section>
  );
}
