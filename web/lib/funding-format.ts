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

// formatPercentAdaptive is the shared precision/sign formatter every
// funding-rate value flows through. CoinGecko's /derivatives endpoint
// returns funding_rate already in percent units (per their docs:
// "Funding rate (in percent)… Example: 0.0095 means 0.0095%") and the
// backend stores the value verbatim — this formatter therefore only
// fixes precision + sign and never multiplies by 100 a second time.
//
// Precision rule (uniform across native / 8h / delta cells so the
// table is internally consistent):
//   - Default 4dp matches the spec mocks and is the smallest
//     precision at which inter-venue spreads (typical 0.0001–0.005%
//     per 8h) remain legible.
//   - When 4dp would collapse a non-zero value to "0.0000" (e.g.
//     Hyperliquid's ~5e-5 native 1h rate, or any platform whose 8h
//     equivalent lands inside ±0.00005% during low-volatility
//     windows), bump to 6dp so the actual magnitude is visible. The
//     6dp ceiling matches CoinGecko's published precision and
//     prevents microscopic numerical noise from masquerading as
//     signal.
//   - Genuine zero stays at the default 4dp ("+0.0000%") so the
//     operator can tell "this venue is actually at 0 right now"
//     apart from "this venue's value is sub-microscopic". Lighter
//     periodically reports rate_8h=0 verbatim from CoinGecko; that
//     reading must NOT get upgraded to 6dp because there is no
//     extra magnitude to expose.
//
// Returns '—' for null / undefined / non-finite values so callers
// can route every funding value through one formatter without
// conditional branches at every call site. AGENTS.md's "no fabricated
// data" rule is preserved at this boundary: status=stale rows carry
// rate=null and we explicitly do NOT render a zero.
function formatPercentAdaptive(rate?: number | null): string {
  if (typeof rate !== 'number' || !Number.isFinite(rate)) return '—';
  let formatted = rate.toFixed(4);
  if (rate !== 0 && parseFloat(formatted) === 0) {
    formatted = rate.toFixed(6);
  }
  const sign = rate >= 0 ? '+' : '';
  return `${sign}${formatted}%`;
}

// formatFundingRate8h renders an 8h-equivalent funding rate. Delegates
// to the shared adaptive formatter so the same precision rule applies
// across the KPI cards, detail-table 8h column, and the
// edgeX-funding-rate subline.
export function formatFundingRate8h(rate?: number | null): string {
  return formatPercentAdaptive(rate);
}

// formatFundingDelta renders a vs-median delta. Shares precision rules
// with formatFundingRate8h so the table is internally consistent —
// previously the two formatters maintained independent precision
// branches and could drift apart on edge cases.
export function formatFundingDelta(delta?: number | null): string {
  return formatPercentAdaptive(delta);
}

// formatNativeRateWithPeriod renders the platform's native funding
// rate plus its native settlement period in a single inline label such
// as "+0.0050% / 4h". This is the canonical "contract-truthful" form
// chosen for the dedicated 资金费率 Tab: operators want to read each
// venue's actual per-period fee, not the cross-platform 8h-normalised
// derivative number. The 8h-equivalent is still computed in the
// backend and displayed as a secondary value so the comparison axis
// stays intact.
//
// Returns '—' when either input is missing / non-finite / non-positive
// so the caller can pipe every cell through a single formatter without
// branching. The period segment is omitted if periodHours is unknown
// rather than guessing 8h, mirroring funding.go's "unknown → unsupported"
// posture: silently defaulting to 8h would mask a config drift.
export function formatNativeRateWithPeriod(rate?: number | null, periodHours?: number | null): string {
  const label = formatPercentAdaptive(rate);
  if (label === '—') return '—';
  if (typeof periodHours === 'number' && Number.isFinite(periodHours) && periodHours > 0) {
    return `${label} / ${periodHours}h`;
  }
  return label;
}

// formatFundingPeriodTag renders the native settlement period as a short
// inline tag (e.g. '4h 计费') so the operator can tell at a glance that
// a normalised 8h-equivalent value was derived from a non-8h native
// period. Returns null when the period is missing, non-positive, or
// exactly 8h — in the V1 platform set this is true for every venue
// except edgeX (4h), Hyperliquid (1h) and Lighter (1h); rendering the
// tag for the 8h cohort would just add chartjunk.
//
// CoinGecko reports edgeX's funding_rate as a constant 0.005 per 4h
// across BTC/ETH/SOL on 2026-05-26 (confirmed by sampling /api/snapshot/
// quality directly), which normalises to a uniform +0.0100% per 8h on
// the dashboard. The tag also doubles as a hint that the value was
// transformed — the displayed number is not the raw upstream reading.
export function formatFundingPeriodTag(periodHours?: number | null): string | null {
  if (typeof periodHours !== 'number' || !Number.isFinite(periodHours) || periodHours <= 0) return null;
  if (periodHours === 8) return null;
  return ` edgex ${periodHours}h 计费, 此处对齐其他交易所8h周期已x2处理`;
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
  // return `${row.platform} · 原始 ${native} / ${period}（已折算为 8h 当量）`;
  return "";
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
