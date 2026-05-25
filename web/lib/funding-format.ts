import type { PlatformFundingRate } from '@/lib/api/types';

// FUNDING_SIGN_CONVENTION_TOOLTIP is the canonical tooltip text attached
// to the small (ⓘ) icon next to every funding-rate header / KPI label.
// Centralising it here ensures the explanation stays identical across
// the Liquidity card KPI, the Liquidity detail-table column, the
// Quality summary KPI, and the Quality bottom-strip — three independent
// surfaces that would otherwise drift over time.
//
// The text encodes the three facts an operator needs the first time
// they see the column:
//   1. Sign convention (positive = longs pay shorts).
//   2. The fact that values are already normalised to 8h equivalent.
//   3. The native settlement periods so '0.0098%' isn't mentally
//      compared against 'we usually see 0.01% on Binance' without an
//      apples-to-apples reference.
export const FUNDING_SIGN_CONVENTION_TOOLTIP =
  '正值 = 多头付空头（持多头需支付费用）；负值反之。\n' +
  '跨平台已折算为 8 小时当量；原始周期：edgeX 4h、Hyperliquid 1h、Lighter 1h、其余 8h。\n' +
  '数据源：CoinGecko /derivatives，5min 刷新。';

// formatFundingRate8h renders a percent value with sign. CoinGecko's
// /derivatives endpoint returns funding_rate already expressed in
// percent units (per their public docs: "Funding rate (in percent)…
// Example: 0.0095 means 0.0095%"), and the backend stores the value
// verbatim without re-scaling. Therefore this formatter only needs to
// fix precision + sign — multiplying by 100 a second time would shift
// every reading by two orders of magnitude (a real 0.0095% per 8h on
// Binance would otherwise render as "+0.95%").
//
// 4dp matches the spec mocks and is the smallest precision at which
// inter-venue spreads (typically 0.0001–0.005% per 8h) remain legible.
//
// Returns '—' for null / undefined / non-finite values so callers can
// route every funding value through one formatter without conditional
// branches at every call site. AGENTS.md's 'no fabricated data' rule
// is preserved at this boundary: status=stale rows carry rate8h=null
// and we explicitly NOT render a zero.
export function formatFundingRate8h(rate?: number | null): string {
  if (typeof rate !== 'number' || !Number.isFinite(rate)) return '—';
  const formatted = rate.toFixed(4);
  return rate >= 0 ? `+${formatted}%` : `${formatted}%`;
}

// formatFundingDelta renders a vs-median delta with the same precision
// and unit handling as formatFundingRate8h. The only difference is that
// 0 is shown explicitly as '+0.0000%' instead of '—', because a row
// whose rate is exactly the median is a meaningful observation (the
// operator wants to see 'yes, we are right at the centre').
export function formatFundingDelta(delta?: number | null): string {
  if (typeof delta !== 'number' || !Number.isFinite(delta)) return '—';
  const formatted = delta.toFixed(4);
  return delta >= 0 ? `+${formatted}%` : `${formatted}%`;
}

// directionGlyph returns the neutral arrow character used next to a
// vs-median delta. Decision 'v2 箭头中性化' explicitly rejected green-up
// / red-down so the operator does not subconsciously read 'higher
// funding = better' (it depends entirely on whether edgeX is the long
// or short side; the dashboard doesn't know).
export function directionGlyph(delta?: number | null): string {
  if (typeof delta !== 'number' || !Number.isFinite(delta) || delta === 0) return '';
  return delta > 0 ? ' ↗' : ' ↘';
}

// fundingPeriodTooltip composes the short period-aware caption that
// shows on hover of a funding-rate cell. We embed the period so the
// operator can mentally translate back ('this 0.005% per 8h came from
// edgeX's 0.0025% per 4h reading') without leaving the page.
export function fundingPeriodTooltip(row?: PlatformFundingRate | null): string {
  if (!row) return '暂无该平台资金费率观测';
  if (row.status === 'unsupported') {
    return `${row.platform} 不在 V1 资金费率周期表内，无法折算`;
  }
  if (row.status !== 'complete') {
    return `${row.platform} 当前没有 CoinGecko /derivatives funding_rate 数据，已置为 ${row.status}`;
  }
  const period = typeof row.period_hours === 'number' && row.period_hours > 0 ? `${row.period_hours}h` : '—';
  const native = typeof row.rate_native === 'number' && Number.isFinite(row.rate_native) ? formatFundingRate8h(row.rate_native) : '—';
  return `${row.platform} · 原始 ${native} / ${period}（已折算为 8h 当量）`;
}

// fundingDisplayStatus returns the ApiStatus value the UI should render
// for the row's funding cell. It compresses the three corner cases
// (no observation, stale, unsupported) into a single decision so the
// renderer can switch on one value and the StatusBadge component is
// re-used.
export function fundingDisplayStatus(row?: PlatformFundingRate | null): string {
  if (!row) return 'stale';
  return row.status ?? 'stale';
}
