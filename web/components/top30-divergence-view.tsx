'use client';

import { useMemo, useState } from 'react';
import {
  CartesianGrid,
  ReferenceLine,
  ResponsiveContainer,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
  ZAxis,
} from 'recharts';
import { StatusBadge } from '@/components/status-badge';
import { StatusEmptyState } from '@/components/status-empty-state';
import {
  money,
  moneyAuto,
  type FrontendURLLookup,
  type Top30AggregateRow,
  type Top30DivergenceKPI,
  type Top30DivergenceRow,
  type Top30DivergenceSnapshot,
} from '@/lib/api/client';

type FilterKey = 'all' | 'cex_only' | 'dex_only' | 'heavy';

const filterPills: { key: FilterKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'cex_only', label: '仅 CEX 阵营' },
  { key: 'dex_only', label: '仅 DEX 阵营' },
  { key: 'heavy', label: '显著分歧' },
];

// categoryBadge maps the backend-emitted category constant to the
// existing 4-tone badge palette used elsewhere in the dashboard. We
// deliberately collapse cex_only / cex_heavy onto the same "info" tone
// and the DEX counterparts onto the warn tone so the operator can scan
// the table by colour rather than by reading every label.
function categoryBadge(category: string): { label: string; cls: string } {
  switch (category) {
    case 'cex_only':
      return { label: 'CEX 独有', cls: 'b-warn' };
    case 'dex_only':
      return { label: 'DEX 独有', cls: 'b-warn' };
    case 'cex_heavy':
      return { label: 'CEX 偏热', cls: 'b-warn' };
    case 'dex_heavy':
      return { label: 'DEX 偏热', cls: 'b-warn' };
    case 'aligned':
      return { label: '对齐', cls: 'b-ok' };
    default:
      return { label: category, cls: 'b-mute' };
  }
}

function isHeavy(category: string): boolean {
  return category === 'cex_heavy' || category === 'dex_heavy';
}

function filterRows(rows: Top30DivergenceRow[], filter: FilterKey): Top30DivergenceRow[] {
  if (filter === 'all') return rows;
  if (filter === 'heavy') return rows.filter(row => isHeavy(row.category));
  return rows.filter(row => row.category === filter);
}

// scatterChartData turns the join into recharts-ready points. Symbols
// missing from one side are pinned to "off-chart" (rank=33 = aggregate
// limit + 3) so they don't overlap with the legitimate rank=30 row;
// without this nudge cex_only and dex_only points would visually look
// like an aligned #30 on the missing axis.
const OFF_CHART_RANK = 33;
const AXIS_MAX = 34;

type ScatterPoint = {
  symbol: string;
  cex: number;
  dex: number;
  category: string;
  cexRank: number | null;
  dexRank: number | null;
};

function buildScatterPoints(rows: Top30DivergenceRow[]): ScatterPoint[] {
  return rows.map(row => ({
    symbol: row.symbol,
    cex: typeof row.cex_rank === 'number' ? row.cex_rank : OFF_CHART_RANK,
    dex: typeof row.dex_rank === 'number' ? row.dex_rank : OFF_CHART_RANK,
    category: row.category,
    cexRank: typeof row.cex_rank === 'number' ? row.cex_rank : null,
    dexRank: typeof row.dex_rank === 'number' ? row.dex_rank : null,
  }));
}

// scatterFillFor: aligned (overlap on diagonal) gets the accent green;
// heavy + only categories all get the warn-yellow used in the badge
// system so the operator can read both visualisations the same way.
function scatterFillFor(category: string): string {
  if (category === 'aligned') return '#6ccf8e';
  return '#f2cc0c';
}

function ScatterTooltip({ active, payload }: { active?: boolean; payload?: Array<{ payload: ScatterPoint }> }) {
  if (!active || !payload || payload.length === 0) return null;
  const p = payload[0].payload;
  const { label } = categoryBadge(p.category);
  return (
    <div style={{
      background: 'rgba(14,16,19,.96)',
      border: '1px solid #2a2e36',
      borderRadius: 4,
      padding: '6px 9px',
      fontSize: 11,
      color: '#e8eaed',
      lineHeight: 1.45,
      boxShadow: '0 10px 28px rgba(0,0,0,.36)',
    }}>
      <div style={{ fontWeight: 600, marginBottom: 4 }}>{p.symbol}</div>
      <div>CEX rank: {p.cexRank ?? '未上榜'}</div>
      <div>DEX rank: {p.dexRank ?? '未上榜'}</div>
      <div style={{ color: '#9aa0a6', marginTop: 2 }}>{label}</div>
    </div>
  );
}

function KpiStrip({ kpi, signifThreshold }: { kpi: Top30DivergenceKPI; signifThreshold: number }) {
  return (
    <div className="share-kpi-strip">
      <div className="share-primary">
        <span>CEX 独有热门</span>
        <b>{kpi.cex_only_count}</b>
      </div>
      <div>
        <span>DEX 独有热门</span>
        <b>{kpi.dex_only_count}</b>
      </div>
      <div>
        <span>显著分歧 (|Δrank| ≥ {signifThreshold})</span>
        <b>{kpi.heavy_count}</b>
      </div>
      <div>
        <span>阵营对齐</span>
        <b>{kpi.aligned_count}</b>
      </div>
      <div>
        <span>两阵营均热 · edgeX 待补</span>
        <b>{kpi.edgex_gap_count}</b>
      </div>
    </div>
  );
}

function AggregateTable({ title, sub, rows, platforms }: { title: string; sub: string; rows: Top30AggregateRow[]; platforms: string[] }) {
  return (
    <section className="panel span-12">
      <div className="panel-head">
        <span className="panel-title">{title}</span>
        <span className="panel-sub">· {sub}</span>
        <span className="panel-tag muted">{platforms.join(' / ')}</span>
      </div>
      <div className="table-wrap">
        <table className="tbl">
          <thead>
            <tr>
              <th className="num">#</th>
              <th>Symbol</th>
              <th className="num">折算后 24h Vol</th>
              <th className="num">原始 24h Vol</th>
              <th className="num">命中家数</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr><td colSpan={5}><div className="empty-state">该阵营暂无 Top30 数据</div></td></tr>
            ) : rows.map(row => (
              <tr key={row.symbol}>
                <td className="num">{row.rank}</td>
                <td>{row.symbol}</td>
                <td className="num">{money(row.adjusted_volume_24h_usd)}</td>
                <td className="num muted">{moneyAuto(row.raw_volume_24h_usd)}</td>
                <td className="num">{row.platform_count} / {platforms.length}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export function Top30DivergenceView({ snapshot, lookup: _lookup }: { snapshot: Top30DivergenceSnapshot; lookup: FrontendURLLookup }) {
  const [filter, setFilter] = useState<FilterKey>('all');
  const rowsFiltered = useMemo(() => filterRows(snapshot.divergence_rows, filter), [snapshot.divergence_rows, filter]);
  const scatterPoints = useMemo(() => buildScatterPoints(snapshot.divergence_rows), [snapshot.divergence_rows]);
  const scatterByCategory = useMemo(() => {
    const out: Record<string, ScatterPoint[]> = {};
    for (const p of scatterPoints) {
      (out[p.category] ??= []).push(p);
    }
    return out;
  }, [scatterPoints]);

  if (snapshot.status === 'unsupported') {
    return (
      <section className="panel">
        <div className="panel-head"><span className="panel-title">CEX vs DEX 阵营分歧</span></div>
        <StatusEmptyState status="unsupported" message="Top30 聚合数据暂未生成，等待下一次采集周期" />
      </section>
    );
  }

  return (
    <div className="grid">
      <section className="panel span-24">
        <div className="panel-head">
          <span className="panel-title">CEX vs DEX 阵营分歧</span>
          <span className="panel-sub">· 阵营内合计 24h 成交量重新排 Top30 · 折算系数 mexc×0.4, gate×0.5</span>
        </div>
        <KpiStrip kpi={snapshot.kpi} signifThreshold={snapshot.significant_rank_delta} />
      </section>

      <AggregateTable
        title="CEX 阵营 Top30"
        sub="阵营内合计 24h vol 排序"
        rows={snapshot.cex_top30}
        platforms={snapshot.cex_platforms}
      />
      <AggregateTable
        title="DEX 阵营 Top30"
        sub="阵营内合计 24h vol 排序"
        rows={snapshot.dex_top30}
        platforms={snapshot.dex_platforms}
      />

      <section className="panel span-24 row-h-md">
        <div className="panel-head">
          <span className="panel-title">CEX rank × DEX rank 散点图</span>
          <span className="panel-sub">· 越靠近对角线 = 两阵营越对齐 · 角落圆点 = 仅一边热门 (未上榜=33)</span>
        </div>
        {scatterPoints.length === 0 ? (
          <StatusEmptyState status="partial" message="无可比较的 symbol" />
        ) : (
          <div style={{ height: 280, padding: '8px 10px 10px' }}>
            <ResponsiveContainer width="100%" height="100%">
              <ScatterChart margin={{ top: 10, right: 24, bottom: 36, left: 36 }}>
                <CartesianGrid stroke="rgba(255,255,255,.08)" />
                <XAxis
                  type="number"
                  dataKey="cex"
                  name="CEX rank"
                  domain={[0, AXIS_MAX]}
                  ticks={[1, 5, 10, 15, 20, 25, 30, 33]}
                  reversed
                  stroke="#9aa0a6"
                  tick={{ fill: '#9aa0a6', fontSize: 11 }}
                  label={{ value: 'CEX 阵营 rank', position: 'insideBottom', offset: -20, fill: '#9aa0a6', fontSize: 11 }}
                />
                <YAxis
                  type="number"
                  dataKey="dex"
                  name="DEX rank"
                  domain={[0, AXIS_MAX]}
                  ticks={[1, 5, 10, 15, 20, 25, 30, 33]}
                  reversed
                  stroke="#9aa0a6"
                  tick={{ fill: '#9aa0a6', fontSize: 11 }}
                  label={{ value: 'DEX 阵营 rank', angle: -90, position: 'insideLeft', offset: 4, fill: '#9aa0a6', fontSize: 11 }}
                />
                <ZAxis type="number" range={[60, 60]} />
                <ReferenceLine
                  segment={[{ x: 1, y: 1 }, { x: 30, y: 30 }]}
                  stroke="#6ccf8e"
                  strokeDasharray="4 4"
                  ifOverflow="hidden"
                />
                <Tooltip cursor={{ stroke: '#4b5563', strokeDasharray: '3 3' }} content={<ScatterTooltip />} />
                {Object.entries(scatterByCategory).map(([category, points]) => (
                  <Scatter key={category} name={categoryBadge(category).label} data={points} fill={scatterFillFor(category)} />
                ))}
              </ScatterChart>
            </ResponsiveContainer>
          </div>
        )}
      </section>

      <section className="panel span-24">
        <div className="panel-head">
          <span className="panel-title">阵营分歧明细</span>
          <span className="panel-sub">· 按 |Δrank| 倒序 · 单边 = 未上榜阵营</span>
          <div className="panel-actions">
            <span className="pill-group" aria-label="分歧过滤">
              {filterPills.map(pill => (
                <button
                  key={pill.key}
                  type="button"
                  className={`pill${filter === pill.key ? ' active' : ''}`}
                  onClick={() => setFilter(pill.key)}
                >
                  {pill.label}
                </button>
              ))}
            </span>
          </div>
        </div>
        <div className="table-wrap">
          <table className="tbl">
            <thead>
              <tr>
                <th>Symbol</th>
                <th className="num">CEX rank</th>
                <th className="num">CEX 24h vol (折算)</th>
                <th className="num">DEX rank</th>
                <th className="num">DEX 24h vol (折算)</th>
                <th className="num">|Δrank|</th>
                <th>分歧类型</th>
                <th>edgeX 已上线?</th>
              </tr>
            </thead>
            <tbody>
              {rowsFiltered.length === 0 ? (
                <tr><td colSpan={8}><div className="empty-state">当前筛选下无 symbol</div></td></tr>
              ) : rowsFiltered.map(row => {
                const cat = categoryBadge(row.category);
                const listedStatus = row.edgex_listed_status;
                return (
                  <tr key={row.symbol}>
                    <td>{row.symbol}</td>
                    <td className="num">{typeof row.cex_rank === 'number' ? `#${row.cex_rank}` : <span className="muted">未上榜</span>}</td>
                    <td className="num">{typeof row.cex_adjusted_volume_24h_usd === 'number' ? money(row.cex_adjusted_volume_24h_usd) : <span className="muted">—</span>}</td>
                    <td className="num">{typeof row.dex_rank === 'number' ? `#${row.dex_rank}` : <span className="muted">未上榜</span>}</td>
                    <td className="num">{typeof row.dex_adjusted_volume_24h_usd === 'number' ? money(row.dex_adjusted_volume_24h_usd) : <span className="muted">—</span>}</td>
                    <td className="num">{typeof row.rank_delta === 'number' ? row.rank_delta : <span className="muted">—</span>}</td>
                    <td><span className={`badge ${cat.cls}`}>{cat.label}</span></td>
                    <td>{listedStatus && listedStatus !== 'complete'
                      ? <StatusBadge status={listedStatus} />
                      : (row.edgex_listed ? '是' : <span className="muted">否</span>)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

      </section>
    </div>
  );
}
