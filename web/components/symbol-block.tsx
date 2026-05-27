'use client';

import { useState } from 'react';
import { displayTierLabel, tierLabels, tierSeries } from '@/components/dashboard-shell';
import { LineChart } from '@/components/line-chart';
import { SmallMultiplesBarChart } from '@/components/small-multiples-bar-chart';
import { StatusEmptyState } from '@/components/status-empty-state';
import { bp, moneyAuto, pct, ratio, type FrontendURLLookup, type LiquiditySnapshot, type PlatformRow } from '@/lib/api/client';

type DepthChartMode = 'line' | 'bar';

const depthChartModeItems: Array<{ value: DepthChartMode; label: string }> = [
  { value: 'line', label: '曲线' },
  { value: 'bar', label: '条形' },
];

type DepthSide = 'bid_usd' | 'ask_usd' | 'total_usd';

function DepthChartSection({
  canonical,
  displayName,
  rows,
  side,
  sideKey,
  title,
}: {
  canonical: string;
  displayName: string;
  rows: PlatformRow[];
  side: DepthSide;
  sideKey: 'bid' | 'ask' | 'total';
  title: string;
}) {
  const [mode, setMode] = useState<DepthChartMode>('line');
  const series = tierSeries(rows, side);
  const displayLabels = tierLabels.map(displayTierLabel);
  const ariaTitle = `${displayName} ${title}`;
  const sectionClass = mode === 'line' ? 'panel span-8 row-h-md' : 'panel span-24';

  return (
    <section className={sectionClass}>
      <div className="panel-head">
        <span className="panel-title">{title}</span>
        <span className="panel-sub">
          {mode === 'line' ? '· 曲线视图' : '· 档位分布，4 档独立 X 轴，平台按深度降序'}
        </span>
        <span
          className="pill-group pill-group-mini symbol-block-mode-toggle"
          role="tablist"
          aria-label={`${ariaTitle} 视图`}
        >
          {depthChartModeItems.map(item => (
            <button
              key={item.value}
              type="button"
              role="tab"
              aria-selected={mode === item.value}
              className={`pill pill-mini ${mode === item.value ? 'active' : ''}`}
              onClick={() => setMode(item.value)}
              data-testid={`symbol-block-mode-${canonical}-${sideKey}-${item.value}`}
            >
              {item.label}
            </button>
          ))}
        </span>
      </div>
      {mode === 'line' ? (
        <LineChart ariaLabel={`${ariaTitle} 曲线`} labels={displayLabels} series={series} />
      ) : (
        <SmallMultiplesBarChart
          ariaLabel={`${ariaTitle} 档位分布`}
          tierLabels={tierLabels}
          displayLabels={displayLabels}
          series={series}
        />
      )}
    </section>
  );
}

// SymbolBlock renders one symbol's full V1 liquidity view inside a
// dedicated outer section.panel frame. Single-symbol and multi-symbol
// monitor views both reuse this block, so adding a second symbol is
// just stacking another block underneath instead of collapsing the
// existing one into a thumbnail. Each block owns its own tier state
// so the operator can compare BTC ±0.10% against ETH ±1% without one
// pill group commandeering the other.
export function SymbolBlock({
  canonical,
  displayName,
  snapshot,
  lookup: _lookup,
  defaultTier,
}: {
  canonical: string;
  displayName: string;
  snapshot: LiquiditySnapshot | null;
  lookup: FrontendURLLookup;
  defaultTier?: string;
}) {
  const [tier, setTier] = useState<string>(() => {
    if (defaultTier && tierLabels.includes(defaultTier)) return defaultTier;
    return '0.10%';
  });

  if (!snapshot) {
    return (
      <section
        className="panel symbol-block span-24"
        data-testid={`symbol-block-${canonical}`}
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
  const edgeRow = rows.find(row => row.platform === 'edgeX');
  const edgeDepth = edgeRow?.depth_by_tier?.[tier]?.total_usd;
  const edgeRatio = edgeRow?.vs_median_by_tier?.[tier];
  const kpis = snapshot.kpis;

  return (
    <section
      className="panel symbol-block span-24"
      data-testid={`symbol-block-${canonical}`}
    >
      <div className="panel-head">
        <span className="panel-title">{displayName}</span>
        <span className="panel-sub">· 流动性监控 · 4 档深度</span>
      </div>
      <div className="grid">
        <section className="panel span-6 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">
              edgeX <span>±{tier}</span> 总深度
            </span>
            <span
              className="pill-group pill-group-mini"
              role="tablist"
              aria-label={`${displayName} 档位`}
            >
              {tierLabels.map(item => (
                <button
                  key={item}
                  type="button"
                  role="tab"
                  aria-selected={item === tier}
                  className={`pill pill-mini ${item === tier ? 'active' : ''}`}
                  onClick={() => setTier(item)}
                  data-testid={`symbol-block-tier-${canonical}-${item}`}
                >
                  {item}
                </button>
              ))}
            </span>
          </div>
          <div className="big-number">{moneyAuto(edgeDepth)}</div>
          <div className="subline">vs 中位数 {ratio(edgeRatio)}</div>
        </section>
        <section className="panel span-6 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">当前交易对 7d 市占率</span>
            <span className="panel-tag">单币种</span>
          </div>
          {typeof kpis?.symbol_share_7d_pct === 'number' ? (
            <div className="big-number">{pct(kpis.symbol_share_7d_pct)}</div>
          ) : (
            <div className="big-number muted">—</div>
          )}
        </section>
        {/*
        已隐藏：edgeX spread (10min 均值)
        <section className="panel span-6 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX spread (10min 均值)</span>
            <span className="panel-tag">盘口</span>
          </div>
          {typeof kpis?.edgex_spread_10m_bp === 'number' ? (
            <div className="big-number">{bp(kpis.edgex_spread_10m_bp)}</div>
          ) : (
            <div className="big-number muted">—</div>
          )}
        </section>
        */}
        <section className="panel span-6 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX 当前 spread</span>
            <span className="panel-tag">latest</span>
          </div>
          <div className="big-number">{bp(kpis?.edgex_spread_bp)}</div>
          <div className="subline">
            24h share{' '}
            {kpis?.edgex_24h_share_status === 'stale'
              ? '—'
              : pct(kpis?.edgex_24h_share_pct)}
          </div>
        </section>
        {/*
        已隐藏：edgeX 资金费率 (FundingKpiPanel)
        <FundingKpiPanel kpis={kpis} />
        */}
        <DepthChartSection
          canonical={canonical}
          displayName={displayName}
          rows={rows}
          side="bid_usd"
          sideKey="bid"
          title="买盘深度 BID"
        />
        <DepthChartSection
          canonical={canonical}
          displayName={displayName}
          rows={rows}
          side="ask_usd"
          sideKey="ask"
          title="卖盘深度 ASK"
        />
        <DepthChartSection
          canonical={canonical}
          displayName={displayName}
          rows={rows}
          side="total_usd"
          sideKey="total"
          title="合计深度 BID + ASK"
        />
        {/*
        已隐藏：深度明细 · 平台 × 档位 (USD) 表格
        <section className="panel span-24">
          <div className="panel-head">
            <span className="panel-title">深度明细 · 平台 × 档位 (USD)</span>
            <span className="panel-sub">· 合计深度 vs 竞品中位数 / 排名</span>
          </div>
          <div className="table-wrap">
            <table className="tbl">
              ...
            </table>
          </div>
        </section>
        */}
      </div>
    </section>
  );
}
