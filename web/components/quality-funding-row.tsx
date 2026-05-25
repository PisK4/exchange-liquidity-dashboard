import { BarChart } from '@/components/chart-primitives';
import { StatusEmptyState } from '@/components/status-empty-state';
import {
  FUNDING_SIGN_CONVENTION_TOOLTIP,
  formatFundingDelta,
  formatFundingRate8h,
  fundingDisplayStatus,
  fundingPeriodTooltip,
} from '@/lib/funding-format';
import type { PlatformRow, QualityKPIs } from '@/lib/api/types';

const edgexAccent = '#6ccf8e';
const competitorColor = '#5794f2';
const unsupportedColor = '#6b7280';

// QualityFundingRow renders the v2-new span-24 panel on the bottom of
// the 盘口质量 tab. The span-24 layout was picked over the original
// review proposal (a 3→4 panel split at the top) because 9 competitor
// labels do not fit horizontally inside a span-6 BarChart without
// truncation, and an unreadable axis defeats the purpose of having the
// data on the dashboard.
//
// The panel intentionally re-uses BarChart instead of building a custom
// renderer so the visual language (edgeX accent, status fades, hover
// tooltip) matches the Spread / 滑点 / Imbalance trio above it. The
// only change is colour selection: any row whose funding status isn't
// complete is rendered as a muted grey so the operator can tell at a
// glance which platforms are reporting vs missing.
export function QualityFundingRow({ rows, kpis }: { rows: PlatformRow[]; kpis?: QualityKPIs }) {
  const usableRows = rows.filter(row => {
    return row.funding && typeof row.funding.rate_8h === 'number' && Number.isFinite(row.funding.rate_8h);
  });
  const status = kpis?.competitor_funding_rate_median_8h_status ?? 'stale';
  const samples = kpis?.competitor_funding_rate_median_8h_samples ?? 0;
  const median = kpis?.competitor_funding_rate_median_8h;
  const edgexRate = kpis?.edgex_funding_rate_8h;

  if (usableRows.length === 0) {
    return (
      <section className="panel span-24">
        <div className="panel-head">
          <span className="panel-title">
            资金费率 跨平台对比
            <span className="info-icon" aria-label="资金费率 sign convention" title={FUNDING_SIGN_CONVENTION_TOOLTIP}> ⓘ</span>
          </span>
          <span className="panel-sub">· CoinGecko /derivatives · 5min 刷新</span>
        </div>
        <StatusEmptyState status="stale" message="尚未观测到该 symbol 的 funding 数据，等待下一次拉取" />
      </section>
    );
  }

  return (
    <section className="panel span-24">
      <div className="panel-head">
        <span className="panel-title">
          资金费率 跨平台对比
          <span className="info-icon" aria-label="资金费率 sign convention" title={FUNDING_SIGN_CONVENTION_TOOLTIP}> ⓘ</span>
        </span>
        <span className="panel-sub">
          · edgeX {formatFundingRate8h(edgexRate)} · 竞品中位数 {status === 'complete' ? formatFundingRate8h(median) : '—'} ({samples} 样本)
        </span>
      </div>
      <BarChart
        signed
        rows={rows.map(row => {
          const f = row.funding;
          const usable = f && typeof f.rate_8h === 'number' && Number.isFinite(f.rate_8h);
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
            value: usable ? (f?.rate_8h as number) : undefined,
            status: fundingDisplayStatus(f),
            color,
          };
        })}
        format={(value, row) => {
          const original = rows.find(r => r.platform === row.label);
          const tip = fundingPeriodTooltip(original?.funding);
          return `${formatFundingRate8h(value)} · ${tip}`;
        }}
      />
      {status === 'complete' && (
        <p className="panel-foot-note">
          虚线 / 灰色 = 竞品中位数 {formatFundingRate8h(median)}。edgeX 相对中位数 {formatFundingDelta(kpis?.edgex_funding_rate_8h && typeof median === 'number' ? kpis.edgex_funding_rate_8h - median : null)}。
        </p>
      )}
      {status !== 'complete' && samples > 0 && (
        <p className="panel-foot-note muted">竞品样本不足 3 家，暂不展示中位数（{samples} / 3）。</p>
      )}
    </section>
  );
}
