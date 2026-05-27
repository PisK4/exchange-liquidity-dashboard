'use client';

import { BarChart } from '@/components/chart-primitives';
import { PlatformCell } from '@/components/platform-cell';
import { StatusEmptyState } from '@/components/status-empty-state';
import {
  FUNDING_SIGN_CONVENTION_TOOLTIP,
  directionGlyph,
  formatFundingDelta,
  formatFundingRate8h,
  formatNativeRateWithPeriod,
  fundingDisplayStatus,
} from '@/lib/funding-format';
import type { FrontendURLLookup, LiquiditySnapshot, PlatformRow } from '@/lib/api/client';

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
//   3. A span-24 BarChart of "Δ to competitor median" (rate_8h − median).
//      Earlier iterations plotted absolute 8h equivalents, but the
//      magnitudes are ~±0.005-0.01% and the visual rendering compressed
//      every bar into an indistinguishable square around the signed
//      zero line; worse, the right-side dual-format labels duplicated
//      the detail table verbatim. Rebasing the chart against the
//      competitor median moves zero to the comparison anchor operators
//      care about ("vs the market") and dramatically expands the
//      effective dynamic range — the signal the table cannot give.
//      edgeX is colored accent green; competitors blue; unsupported grey.
//   4. A span-24 detail table holding the contract-truthful data:
//      platform, native rate (with period folded inline as
//      "+0.0025% / 4h"), 8h equivalent, vs median (8h), and rank.
//      Earlier columns (native period, snapshot timestamp, status)
//      were dropped per operator feedback — period is now inside the
//      native-rate cell, the snapshot timestamp is redundant with the
//      tab-level refresh tag, and missing data already surfaces as '—'.
//
// 数据通路：reuse the LiquiditySnapshot fan-out (liquidityByCanonical)
// already produced by DashboardClient — its rows[].funding and
// kpis.edgex_funding_rate_* / competitor_funding_rate_* fields are
// populated by the same backend collector that feeds the Liquidity Tab.
// No new HTTP endpoint or extra round-trip is introduced.

const edgexAccent = '#6ccf8e';
const competitorColor = '#5794f2';
const unsupportedColor = '#6b7280';

function pickFundingRows(rows: PlatformRow[]) {
  return rows.filter(row => {
    return row.funding && typeof row.funding.rate_8h === 'number' && Number.isFinite(row.funding.rate_8h);
  });
}

function formatSampleCount(samples?: number) {
  if (typeof samples !== 'number' || samples < 0) return '0';
  return String(samples);
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

  const usableRows = pickFundingRows(rows);
  // Detail table keeps the absolute 8h-ascending order operators
  // recognised from the prior bar chart.
  const sortedRows = [...rows].sort((a, b) => {
    const av = typeof a.funding?.rate_8h === 'number' ? a.funding.rate_8h : Number.POSITIVE_INFINITY;
    const bv = typeof b.funding?.rate_8h === 'number' ? b.funding.rate_8h : Number.POSITIVE_INFINITY;
    return av - bv;
  });
  // For the Δ-to-median chart we sort by delta (rate_8h - median)
  // ascending so the most-below-median venues sit at the top and the
  // most-above sit at the bottom. Rows without a usable rate fall to
  // the bottom via Infinity so the visible series is contiguous.
  const deltaRows = medianStatus === 'complete' && typeof median === 'number'
    ? [...rows].sort((a, b) => {
        const av = typeof a.funding?.rate_8h === 'number' ? a.funding.rate_8h - median : Number.POSITIVE_INFINITY;
        const bv = typeof b.funding?.rate_8h === 'number' ? b.funding.rate_8h - median : Number.POSITIVE_INFINITY;
        return av - bv;
      })
    : [];

  return (
    <section
      className="panel funding-block span-24"
      data-testid={`funding-block-${canonical}`}
    >
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        <span className="panel-sub">
          · 跨交易所资金费率对比 · 8h 归一
          <span
            className="info-icon"
            aria-label="资金费率 sign convention"
            title={FUNDING_SIGN_CONVENTION_TOOLTIP}
          >
            {' '}ⓘ
          </span>
        </span>
        <span className="panel-tag muted">CoinGecko /derivatives</span>
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
            {medianStatus === 'complete'
              ? '正值 = 多头付出 > 竞品中位数'
              : '中位数缺失，无法计算 delta'}
          </div>
        </section>
        <section className="panel span-24 row-h-md">
          <div className="panel-head">
            <span className="panel-title">对竞品中位数偏离 (Δ, 8h)</span>
            <span className="panel-sub">
              · 零点 = 竞品中位数 · 正值 = 比竞品贵, 负值 = 比竞品便宜
            </span>
            <span className="panel-tag muted">5min 刷新</span>
          </div>
          {medianStatus !== 'complete' || typeof median !== 'number' ? (
            <StatusEmptyState
              status="stale"
              message={
                usableRows.length === 0
                  ? '尚未观测到该 symbol 的 funding 数据，等待下一次拉取'
                  : `竞品样本不足 3 家（${medianSamples}/3），暂不展示偏离图`
              }
            />
          ) : (
            <BarChart
              signed
              rows={deltaRows.map(row => {
                const f = row.funding;
                const usable = f && typeof f.rate_8h === 'number' && Number.isFinite(f.rate_8h);
                const delta = usable ? (f!.rate_8h as number) - median : undefined;
                let color: string;
                if (!usable) {
                  color = unsupportedColor;
                } else if (row.platform === 'edgeX') {
                  color = edgexAccent;
                } else {
                  color = competitorColor;
                }
                return {
                  label: row.platform,
                  value: delta,
                  status: fundingDisplayStatus(f),
                  color,
                };
              })}
              format={(value, row) => {
                const original = rows.find(r => r.platform === row.label);
                const f = original?.funding;
                const rate8h = formatFundingRate8h(f?.rate_8h);
                const deltaLabel = formatFundingDelta(value);
                return `Δ ${deltaLabel} (8h ${rate8h})`;
              }}
            />
          )}
          <p className="panel-foot-note">
            Δ = 8h 当量 − 竞品中位数 ({formatFundingRate8h(median)})。括号内为该平台原始 8h 当量。
          </p>
        </section>
        <section className="panel span-24">
          <div className="panel-head">
            <span className="panel-title">资金费率明细</span>
            <span className="panel-sub">· 每行=一个平台 · 原生费率含周期 tag</span>
            <span className="panel-tag muted">CSV 可导</span>
          </div>
          <div className="table-wrap">
            <table className="tbl">
              <thead>
                <tr>
                  <th>平台</th>
                  <th className="num">原生费率</th>
                  <th className="num">8h 当量</th>
                  <th className="num">vs 中位数 (8h)</th>
                  <th className="num">排名</th>
                </tr>
              </thead>
              <tbody>
                {sortedRows.map(row => {
                  const f = row.funding;
                  const isUsable = f && typeof f.rate_8h === 'number' && Number.isFinite(f.rate_8h);
                  return (
                    <tr key={row.platform}>
                      <td>
                        <PlatformCell
                          platform={row.platform}
                          displaySymbol={snapshot.symbol ?? displayName}
                          lookup={lookup}
                        />
                      </td>
                      <td className="num">
                        {isUsable
                          ? formatNativeRateWithPeriod(f?.rate_native, f?.period_hours)
                          : '—'}
                      </td>
                      <td className="num">
                        {isUsable ? formatFundingRate8h(f?.rate_8h) : '—'}
                      </td>
                      <td className="num">
                        {typeof f?.vs_median_8h === 'number'
                          ? formatFundingDelta(f.vs_median_8h)
                          : '—'}
                      </td>
                      <td className="num">
                        {typeof f?.rank === 'number' && f.rank > 0 ? f.rank : '—'}
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
