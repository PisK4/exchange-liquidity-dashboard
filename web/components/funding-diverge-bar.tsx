'use client';

import { isSelfPlatform, PlatformCell, platformRowKey } from '@/components/platform-cell';
import { formatFundingRate8h } from '@/lib/funding-format';
import type { FrontendURLLookup, PlatformRow } from '@/lib/api/client';

// FundingDivergeBar renders the cross-platform 8h-equivalent funding
// rate as a diverging bar chart anchored at 0%. Positive rates (longs
// pay) extend to the right in red; negative rates (shorts pay) extend
// to the left in teal. The shared cohort scale is max(|rate|) so each
// half independently saturates its axis — earlier single-axis bar
// charts compressed every value into an indistinguishable square at
// the ~±0.005% magnitudes funding rates live at; splitting positive
// and negative onto opposite sides of zero lets the eye read both
// direction (which side pays) and intensity (how extreme) in one
// glance.
//
// edgeX always renders with a white inset border + full-saturation
// fill so the operator's eye lands on it first — mirroring the
// .platform-self / .r-edgex emphasis used elsewhere.
//
// Sort is by rate_8h descending (most positive at top), so the
// visual stack reads "biggest cost to longs → biggest cost to shorts"
// top-to-bottom. Rows without a usable rate sink to the bottom and
// render as muted em-dash; this honours funding.go's "no fabricated
// data" rule by visibly representing the gap rather than collapsing
// the bar to zero.
export function FundingDivergeBar({
  rows,
  displaySymbol,
  lookup,
}: {
  rows: PlatformRow[];
  displaySymbol: string;
  lookup: FrontendURLLookup;
}) {
  const usable = rows.filter(
    row => row.funding && typeof row.funding.rate_8h === 'number' && Number.isFinite(row.funding.rate_8h),
  );
  const unusable = rows.filter(row => !usable.includes(row));

  const cohortMax = usable.reduce((acc, row) => {
    const v = Math.abs(row.funding?.rate_8h ?? 0);
    return v > acc ? v : acc;
  }, 0);

  const sortedUsable = [...usable].sort(
    (a, b) => (b.funding?.rate_8h ?? 0) - (a.funding?.rate_8h ?? 0),
  );
  const ordered = [...sortedUsable, ...unusable];

  return (
    <div className="funding-diverge-chart" data-testid="funding-diverge-chart">
      <div className="funding-diverge-axis" aria-hidden="true">
        <span className="funding-diverge-axis-side">负费率（空头付出）</span>
        <span className="funding-diverge-axis-zero">0%</span>
        <span className="funding-diverge-axis-side funding-diverge-axis-right">正费率（多头付出）</span>
      </div>
      {ordered.map(row => {
        const rate = row.funding?.rate_8h;
        const isUsable = typeof rate === 'number' && Number.isFinite(rate);
        const rowKey = platformRowKey(row);
        const isEdgex = isSelfPlatform(row);
        const rowClass = [
          'funding-diverge-row',
          isEdgex ? 'is-edgex' : '',
          !isUsable ? 'is-muted' : '',
        ]
          .filter(Boolean)
          .join(' ');
        // pctOfMax is the bar half-width as a percentage of the
        // cohort's max absolute rate. Falls back to 0 when the cohort
        // collapsed to all-zero so the bar doesn't divide-by-zero.
        const pctOfMax = isUsable && cohortMax > 0 ? Math.min(Math.abs(rate as number) / cohortMax, 1) : 0;
        const halfWidthPct = pctOfMax * 50;
        const isPositive = isUsable && (rate as number) > 0;
        const isNegative = isUsable && (rate as number) < 0;
        const valueClass = isPositive
          ? 'sign-positive'
          : isNegative
            ? 'sign-negative'
            : 'muted';
        return (
          <div
            key={rowKey}
            className={rowClass}
            data-testid={`funding-diverge-row-${rowKey}`}
          >
            <span className="funding-diverge-label">
              <PlatformCell
                platform={row.platform}
                displaySymbol={displaySymbol}
                lookup={lookup}
                displayPlatform={row.display_platform}
                isEdgex={row.is_edgex}
                marketSurface={row.market_surface}
                lineage={row.lineage}
                venueSymbol={row.venue_symbol}
                contractId={row.contract_id}
              />
            </span>
            <div className="funding-diverge-track">
              <div className="funding-diverge-zero" />
              {isPositive ? (
                <div
                  className="funding-diverge-fill positive"
                  style={{ left: '50%', width: `${halfWidthPct}%` }}
                />
              ) : null}
              {isNegative ? (
                <div
                  className="funding-diverge-fill negative"
                  style={{ right: '50%', width: `${halfWidthPct}%` }}
                />
              ) : null}
            </div>
            <span className={`funding-diverge-value ${valueClass}`}>
              {isUsable ? formatFundingRate8h(rate) : '—'}
            </span>
          </div>
        );
      })}
    </div>
  );
}
