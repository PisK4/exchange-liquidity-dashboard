'use client';

import { PlatformCell } from '@/components/platform-cell';
import { StatusEmptyState } from '@/components/status-empty-state';
import {
  FUNDING_SIGN_CONVENTION_TOOLTIP,
  directionGlyph,
  formatFundingDelta,
  formatFundingRate8h,
  formatNativeRateWithPeriod,
} from '@/lib/funding-format';
import type { FrontendURLLookup, LiquiditySnapshot } from '@/lib/api/client';

// FundingBlock is the dedicated 资金费率 Tab counterpart of SymbolBlock /
// QualityBlock. Each watchlist symbol renders into its own
// section.panel.span-24 frame with:
//   1. A panel-head carrying the display name + the canonical sign-
//      convention ⓘ tooltip so the operator can read magnitudes without
//      flipping back to the docs.
//   2. Three KPI cards (row-h-sm × span-8): edgeX funding (native big +
//      8h small), competitor median (8h only — cross-period mix has no
//      single native representation), and edgeX vs median delta (8h only,
//      same reason).
//   3. A span-24 detail table holding the contract-truthful data:
//      platform, native rate (with period folded inline as
//      "+0.0025% / 4h"), 8h equivalent, vs median (8h), and rank.
//      Earlier iterations carried a cross-platform BarChart between
//      the KPI cards and the table — first plotting absolute 8h
//      equivalents, then Δ to median. Both forms struggled at the
//      ~±0.005% magnitudes typical of funding rates: the BarChart
//      either compressed every bar into an indistinguishable square,
//      or restated values already visible in the table. After two
//      operator iterations we removed it entirely; the three KPI
//      cards already answer the "where does edgeX sit" question and
//      the detail table answers everything else.
//
// 数据通路：reuse the LiquiditySnapshot fan-out (liquidityByCanonical)
// already produced by DashboardClient — its rows[].funding and
// kpis.edgex_funding_rate_* / competitor_funding_rate_* fields are
// populated by the same backend collector that feeds the Liquidity Tab.
// No new HTTP endpoint or extra round-trip is introduced.

function formatSampleCount(samples?: number) {
  if (typeof samples !== 'number' || samples < 0) return '0';
  return String(samples);
}

// rateSignColorClass picks the CSS class that paints a numeric cell
// based on the sign of the funding-rate value it carries. Positive
// rates (longs pay) render red; negative rates (shorts pay) render
// teal; zero and missing values stay neutral. The classes themselves
// live in globals.css alongside .platform-self and .r-edgex so the
// palette stays in one place — the same .sign-positive/.sign-negative
// pair is reused by 盘口质量明细's Imbalance column for direction
// (BID-heavy vs ASK-heavy), so this helper is intentionally a thin
// sign → class mapping with no funding-specific logic.
function rateSignColorClass(value?: number | null): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '';
  if (value > 0) return 'sign-positive';
  if (value < 0) return 'sign-negative';
  return '';
}

export function FundingBlock({
  canonical,
  displayName,
  snapshot,
  lookup,
}: {
  canonical: string;
  displayName: string;
  snapshot: LiquiditySnapshot | null;
  lookup: FrontendURLLookup;
}) {
  if (!snapshot) {
    return (
      <section
        className="panel funding-block span-24"
        data-testid={`funding-block-${canonical}`}
      >
        <div className="panel-head">
          <span className="panel-title">{displayName}</span>
          <span className="panel-tag muted">未加载</span>
        </div>
        <StatusEmptyState status="stale" message="尚未拉取到该标的的快照" />
      </section>
    );
  }

  const rows = snapshot.rows ?? [];
  const kpis = snapshot.kpis;
  const edgeRow = rows.find(row => row.platform === 'edgeX');
  const edgeFunding = edgeRow?.funding;
  const edgeRate8h = kpis?.edgex_funding_rate_8h;
  const edgePeriod = kpis?.edgex_funding_rate_period_hours ?? edgeFunding?.period_hours;
  const edgeRateNative = edgeFunding?.rate_native;
  const median = kpis?.competitor_funding_rate_median_8h;
  const medianStatus = kpis?.competitor_funding_rate_median_8h_status ?? 'stale';
  const medianSamples = kpis?.competitor_funding_rate_median_8h_samples ?? 0;
  const delta =
    typeof edgeRate8h === 'number' && typeof median === 'number'
      ? edgeRate8h - median
      : null;

  // Detail table sorts ascending by 8h equivalent so the cheapest
  // venues sit at the top. Rows without a usable rate sink to the
  // bottom via Infinity so the visible series stays contiguous.
  const sortedRows = [...rows].sort((a, b) => {
    const av = typeof a.funding?.rate_8h === 'number' ? a.funding.rate_8h : Number.POSITIVE_INFINITY;
    const bv = typeof b.funding?.rate_8h === 'number' ? b.funding.rate_8h : Number.POSITIVE_INFINITY;
    return av - bv;
  });

  return (
    <section
      className="panel funding-block span-24"
      data-testid={`funding-block-${canonical}`}
    >
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        <span className="panel-sub">
          · 跨交易所资金费率对比 · 折算 8h 维度排名
          <span
            className="info-icon"
            aria-label="资金费率 sign convention"
            title={FUNDING_SIGN_CONVENTION_TOOLTIP}
          >
            {' '}ⓘ
          </span>
        </span>
        {/* <span className="panel-tag muted">CoinGecko /derivatives</span> */}
      </div>
      <div className="grid">
        <section className="panel span-8 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX 资金费率</span>
            <span className="panel-tag">原生</span>
          </div>
          <div className="big-number">
            {formatNativeRateWithPeriod(edgeRateNative, edgePeriod)}
          </div>
          <div className="subline">
            8h 当量 {formatFundingRate8h(edgeRate8h)}
          </div>
        </section>
        <section className="panel span-8 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">竞品中位数 (8h)</span>
            <span className="panel-tag">{formatSampleCount(medianSamples)}/9</span>
          </div>
          <div className="big-number">
            {medianStatus === 'complete' ? formatFundingRate8h(median) : '—'}
          </div>
          <div className="subline muted">
            {medianStatus === 'complete'
              ? '跨周期混合，仅能以 8h 当量表达'
              : `样本不足（${medianSamples}/3）暂不展示中位数`}
          </div>
        </section>
        <section className="panel span-8 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX vs 竞品中位数</span>
            <span className="panel-tag">8h 当量</span>
          </div>
          <div className="big-number">
            {medianStatus === 'complete' ? formatFundingDelta(delta) : '—'}
            <span className="muted">{directionGlyph(delta)}</span>
          </div>
          <div className="subline muted">
            {medianStatus === 'complete' && typeof edgeRate8h === 'number' && typeof median === 'number'
              ? `= edgeX 8h (${formatFundingRate8h(edgeRate8h)}) − 中位数 (${formatFundingRate8h(median)})`
              : '中位数缺失，无法计算 delta'}
          </div>
          <div className="subline muted">
            {medianStatus === 'complete' ? '正值 = edgeX 多头付出 > 竞品中位数' : ''}
          </div>
        </section>
        <section className="panel span-24">
          <div className="panel-head">
            <span className="panel-title">资金费率明细</span>
            <span className="panel-sub">
              · 红字=正费率（多头付出）· 青字=负费率（空头付出）
            </span>
            {/* <span className="panel-tag muted">CSV 可导</span> */}
          </div>
          <div className="table-wrap">
            <table className="tbl">
              <thead>
                <tr>
                  <th>平台</th>
                  <th className="num">原生费率</th>
                  <th className="num">8h 折算费率</th>
                  <th className="num">vs 中位数 (8h)</th>
                  <th className="num">正费率排名 (8h)</th>
                  <th className="num">负费率排名 (8h)</th>
                </tr>
              </thead>
              <tbody>
                {sortedRows.map(row => {
                  const f = row.funding;
                  const isUsable = f && typeof f.rate_8h === 'number' && Number.isFinite(f.rate_8h);
                  const rateSignClass = rateSignColorClass(f?.rate_8h);
                  const vsMedianSignClass = rateSignColorClass(f?.vs_median_8h);
                  const rowClass = row.platform === 'edgeX' ? 'r-edgex' : undefined;
                  return (
                    <tr key={row.platform} className={rowClass}>
                      <td>
                        <PlatformCell
                          platform={row.platform}
                          displaySymbol={snapshot.symbol ?? displayName}
                          lookup={lookup}
                        />
                      </td>
                      <td className={`num ${rateSignClass}`}>
                        {isUsable
                          ? formatNativeRateWithPeriod(f?.rate_native, f?.period_hours)
                          : '—'}
                      </td>
                      <td className={`num ${rateSignClass}`}>
                        {isUsable ? formatFundingRate8h(f?.rate_8h) : '—'}
                      </td>
                      <td className={`num ${vsMedianSignClass}`}>
                        {typeof f?.vs_median_8h === 'number'
                          ? formatFundingDelta(f.vs_median_8h)
                          : '—'}
                      </td>
                      <td className="num sign-positive">
                        {typeof f?.rank_positive === 'number' && f.rank_positive > 0 ? f.rank_positive : '—'}
                      </td>
                      <td className="num sign-negative">
                        {typeof f?.rank_negative === 'number' && f.rank_negative > 0 ? f.rank_negative : '—'}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </section>
  );
}
