'use client';

import { useState } from 'react';
import { BarChart } from '@/components/chart-primitives';
import { isSelfPlatform, PlatformCell, platformDisplayLabel, platformRowKey } from '@/components/platform-cell';
import { StatusEmptyState } from '@/components/status-empty-state';
import { VerdictBadge } from '@/components/dashboard-shell';
import { bp, money, usdLabel, type FrontendURLLookup, type LiquiditySnapshot, type PlatformRow } from '@/lib/api/client';

// QualityBlock is the Quality Tab counterpart of SymbolBlock: each
// watchlist symbol renders into its own section.panel.span-24 frame
// with KPI row + Spread/Slippage/Imbalance BarCharts + 盘口质量明细
// sub-table stacked vertically. Bucket pill state lives in the block
// (useState) so BTC ±100K and ETH ±1M can be compared independently
// without one block commandeering the other.
//
// The snapshot input is the LiquiditySnapshot-shaped merge produced
// by dashboard-shell.tsx → mergeQualityIntoLiquidity, which overlays
// worst_slippage_bp + verdict from the quality endpoint onto the
// liquidity snapshot. That way one PlatformRow has every field the
// block needs (spread / mid / imbalance / 4 buckets / verdict).
//
// 资金费率 intentionally does NOT appear anywhere in this block: it
// has been moved out of Quality Tab and will live in a follow-up
// dedicated Tab / panel. Keep this file free of funding imports so
// the boundary stays clear.

const edgexAccent = '#6ccf8e';

function signedPct(value?: number) {
  if (typeof value !== 'number') return '—';
  return `${value > 0 ? '+' : ''}${value.toFixed(2)}%`;
}

// imbalanceSignClass paints the Imbalance cell red (BID-heavy) or
// teal (ASK-heavy) so the buy/sell direction is parseable without
// scanning for the "+" / "-" character. Color encodes DIRECTION only,
// not optimality — both directions can be healthy (|x| < 30%) or
// unhealthy (|x| > 30%); the existing Imbalance BarChart already
// flags magnitude via its own threshold-based palette. This is the
// same .sign-positive/.sign-negative pair the 资金费率 Tab uses; the
// classes were deliberately renamed in this commit to drop the
// funding-specific prefix so reuse here doesn't look out of place.
function imbalanceSignClass(value?: number | null): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '';
  if (value > 0) return 'sign-positive';
  if (value < 0) return 'sign-negative';
  return '';
}

// Per-metric "materially significant" thresholds (in bp) for the
// vs-median quality coloring. A diff smaller than the threshold in
// magnitude renders neutral so noise near the median doesn't paint
// the whole table green/red. Numbers chosen as half-an-order-of-
// magnitude of typical operating range for each bucket:
//   spread typical 0.5-5 bp → 0.5 bp threshold
//   50K slippage 0.5-3 bp → 0.5 bp
//   100K slippage 1-5 bp → 1 bp
//   500K slippage 3-15 bp → 2 bp
//   1M slippage 5-30 bp → 5 bp
// Tunable: if operators report too many cells stay neutral, drop the
// thresholds; if too many flip on tiny diffs, raise them.
const QUALITY_THRESHOLD_BP: Record<string, number> = {
  spread: 0.5,
  '50000': 0.5,
  '100000': 1,
  '500000': 2,
  '1000000': 5,
};

// qualityThresholdClass picks q-good (this row's value is materially
// LOWER than median = better for lower-is-better metrics) or q-bad
// (materially HIGHER = worse) based on the signed diff. nil/undefined
// diff (median cohort too small) → neutral; sub-threshold diff →
// neutral. The threshold is metric-specific and lives in
// QUALITY_THRESHOLD_BP above.
function qualityThresholdClass(
  diff: number | null | undefined,
  thresholdKey: string,
): string {
  if (typeof diff !== 'number' || !Number.isFinite(diff)) return '';
  const threshold = QUALITY_THRESHOLD_BP[thresholdKey];
  if (typeof threshold !== 'number' || threshold <= 0) return '';
  if (diff <= -threshold) return 'q-good';
  if (diff >= threshold) return 'q-bad';
  return '';
}

function bucketShortLabel(bucket: string) {
  const amount = Number(bucket);
  if (!Number.isFinite(amount)) return bucket;
  if (amount >= 1_000_000) return `${amount / 1_000_000}M`;
  return `${amount / 1_000}K`;
}

function bucketUSDLabel(bucket: string) {
  const amount = Number(bucket);
  if (!Number.isFinite(amount)) return bucket;
  if (amount >= 1_000_000) return `${amount / 1_000_000}M USD`;
  return `${amount / 1_000}K USD`;
}

function spreadUSD(mid?: number, spreadBp?: number) {
  return typeof spreadBp === 'number' && typeof mid === 'number' ? mid * spreadBp / 10_000 : undefined;
}

function slippageUSD(bucket: string, slippageBp?: number) {
  const amount = Number(bucket);
  return typeof slippageBp === 'number' && Number.isFinite(amount) ? amount * slippageBp / 10_000 : undefined;
}

function rowDisplayAvailable(row: PlatformRow) {
  if (!row.depth_by_tier) {
    return row.depth_status === 'complete' || row.depth_status === 'partial' || row.depth_status === 'aggregated_orderbook' || row.depth_status === 'ws_limited_depth';
  }
  return Object.values(row.depth_by_tier).some(depth => depth?.display_available !== false);
}

export function QualityBlock({
  canonical,
  displayName,
  snapshot,
  buckets,
  lookup,
  defaultBucket,
}: {
  canonical: string;
  displayName: string;
  snapshot: LiquiditySnapshot | null;
  buckets: string[];
  lookup: FrontendURLLookup;
  defaultBucket?: string;
}) {
  const initialBucket = defaultBucket && buckets.includes(defaultBucket)
    ? defaultBucket
    : (buckets.includes('100000') ? '100000' : buckets[0] ?? '100000');
  const [bucket, setBucket] = useState<string>(initialBucket);

  if (!snapshot) {
    return (
      <section
        className="panel quality-block span-24"
        data-testid={`quality-block-${canonical}`}
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
  const rowByKey = new Map(rows.map(row => [platformRowKey(row), row]));
  const edgeRow = rows.find(r => isSelfPlatform(r));
  const edgeSpreadBp = snapshot.kpis?.edgex_spread_bp;
  const edgeSpreadUSD = spreadUSD(edgeRow?.mid_price, edgeSpreadBp);
  const edgeSlippageBp = edgeRow?.worst_slippage_bp?.[bucket];
  const edgeSlippageUSD = slippageUSD(bucket, edgeSlippageBp);

  return (
    <section
      className="panel quality-block span-24"
      data-testid={`quality-block-${canonical}`}
    >
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        <span className="panel-sub">· 盘口质量 · 4 桶滑点</span>
        <span className="spacer" />
        <VerdictBadge verdict={edgeRow?.verdict} />
      </div>
      <div className="grid">
        <section className="panel span-8 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX 当前 spread</span>
            <span className="panel-tag">latest</span>
          </div>
          <div className="big-number">{bp(edgeSpreadBp)}</div>
          <div className="subline">{typeof edgeSpreadUSD === 'number' ? `≈ ${usdLabel(edgeSpreadUSD)}` : '—'}</div>
        </section>
        <section className="panel span-8 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">
              edgeX 滑点 @ {bucketShortLabel(bucket)} USD
            </span>
            <span
              className="pill-group pill-group-mini"
              role="tablist"
              aria-label={`${displayName} 滑点桶`}
            >
              {buckets.map(item => (
                <button
                  key={item}
                  type="button"
                  role="tab"
                  aria-selected={item === bucket}
                  className={`pill pill-mini ${item === bucket ? 'active' : ''}`}
                  onClick={() => setBucket(item)}
                  data-testid={`quality-block-bucket-${canonical}-${item}`}
                >
                  {bucketShortLabel(item)}
                </button>
              ))}
            </span>
          </div>
          <div className="big-number">
            {typeof edgeSlippageBp === 'number' ? bp(edgeSlippageBp) : '—'}
          </div>
          <div className="subline">{typeof edgeSlippageUSD === 'number' ? `≈ ${usdLabel(edgeSlippageUSD)}` : '—'}</div>
        </section>
        <section className="panel span-8 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX Imbalance</span>
            <span className="panel-tag">latest</span>
          </div>
          <div className="big-number">{signedPct(edgeRow?.imbalance_pct)}</div>
          <div className="subline muted">{'(BID-ASK)/合计 · |x|>30% 偏离健康'}</div>
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head">
            <span className="panel-title">Spread (bp)</span>
            <span className="panel-sub">· 买一/卖一相对价差</span>
          </div>
          <BarChart
            rows={rows.map(row => ({
              key: platformRowKey(row),
              label: row.platform,
              displayLabel: platformDisplayLabel(row),
              isSelf: isSelfPlatform(row),
              value: rowDisplayAvailable(row) ? row.spread_bp : undefined,
              status: row.depth_status,
              color: isSelfPlatform(row) ? edgexAccent : '#5794f2',
            }))}
            sort="asc"
            format={(value, row) => {
              const target = row.key ? rowByKey.get(row.key) : undefined;
              const usd = target ? spreadUSD(target.mid_price, value) : undefined;
              return `${(value ?? 0).toFixed(2)} bp · ${usdLabel(usd)}`;
            }}
          />
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head">
            <span className="panel-title">模拟下单滑点 (bp)</span>
            <span className="panel-sub">· 桶 {bucketUSDLabel(bucket)}</span>
          </div>
          <BarChart
            rows={rows.map(row => ({
              key: platformRowKey(row),
              label: row.platform,
              displayLabel: platformDisplayLabel(row),
              isSelf: isSelfPlatform(row),
              value: rowDisplayAvailable(row) ? row.worst_slippage_bp?.[bucket] : undefined,
              status: row.depth_status,
              color: isSelfPlatform(row) ? edgexAccent : '#73bf69',
            }))}
            sort="asc"
            format={value => `${(value ?? 0).toFixed(2)} bp · ${usdLabel(slippageUSD(bucket, value))}`}
          />
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head">
            <span className="panel-title">Bid/Ask Imbalance (%)</span>
            <span className="panel-sub">· (BID-ASK)/合计</span>
          </div>
          <BarChart
            signed
            rows={rows.map(row => ({
              key: platformRowKey(row),
              label: row.platform,
              displayLabel: platformDisplayLabel(row),
              isSelf: isSelfPlatform(row),
              value: rowDisplayAvailable(row) ? row.imbalance_pct : undefined,
              status: row.depth_status,
              color: isSelfPlatform(row) ? edgexAccent : Math.abs(row.imbalance_pct ?? 0) > 30 ? '#f2495c' : '#5794f2',
            }))}
            format={value => signedPct(value)}
          />
        </section>
        <section className="panel span-24">
          <div className="panel-head">
            <span className="panel-title">盘口质量明细</span>
            <span className="panel-sub">
              · Spread/滑点 绿=优于竞品中位数 红=劣于（按桶阈值）· Imbalance 红/青=BID/ASK 偏多（仅方向，|x|&gt;30% 偏离健康）
            </span>
            <span className="panel-tag muted">CSV 可导</span>
          </div>
          <div className="table-wrap">
            <table className="tbl">
              <thead>
                <tr>
                  <th>平台</th>
                  <th className="num">Spread (bp)</th>
                  <th className="num">Mid 价格</th>
                  <th className="num">Imbalance (%)</th>
                  <th className="num">滑点 50K (bp)</th>
                  <th className="num">滑点 100K (bp)</th>
                  <th className="num">滑点 500K (bp)</th>
                  <th className="num">滑点 1M (bp)</th>
                  <th>盘口结论</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(row => (
                  <tr
                    key={platformRowKey(row)}
                    className={isSelfPlatform(row) ? 'r-edgex' : undefined}
                  >
                    <td>
                      <PlatformCell
                        platform={row.platform}
                        displaySymbol={snapshot.symbol ?? displayName}
                        lookup={lookup}
                        displayPlatform={row.display_platform}
                        isEdgex={row.is_edgex}
                        marketSurface={row.market_surface}
                        lineage={row.lineage}
                        venueSymbol={row.venue_symbol}
                        contractId={row.contract_id}
                      />
                    </td>
                    <td className={`num ${qualityThresholdClass(row.vs_median_spread_bp, 'spread')}`}>{bp(row.spread_bp)}</td>
                    <td className="num">{money(row.mid_price)}</td>
                    <td className={`num ${imbalanceSignClass(row.imbalance_pct)}`}>{signedPct(row.imbalance_pct)}</td>
                    <td className={`num ${qualityThresholdClass(row.vs_median_slippage_bp?.['50000'], '50000')}`}>{bp(row.worst_slippage_bp?.['50000'])}</td>
                    <td className={`num ${qualityThresholdClass(row.vs_median_slippage_bp?.['100000'], '100000')}`}>{bp(row.worst_slippage_bp?.['100000'])}</td>
                    <td className={`num ${qualityThresholdClass(row.vs_median_slippage_bp?.['500000'], '500000')}`}>{bp(row.worst_slippage_bp?.['500000'])}</td>
                    <td className={`num ${qualityThresholdClass(row.vs_median_slippage_bp?.['1000000'], '1000000')}`}>{bp(row.worst_slippage_bp?.['1000000'])}</td>
                    <td><VerdictBadge verdict={row.verdict} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </section>
  );
}
