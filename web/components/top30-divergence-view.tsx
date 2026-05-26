'use client';

import { useMemo, useState } from 'react';
import { StatusBadge } from '@/components/status-badge';
import { StatusEmptyState } from '@/components/status-empty-state';
import {
  money,
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
              <th className="num">24h Vol</th>
              <th className="num">命中家数</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr><td colSpan={4}><div className="empty-state">该阵营暂无 Top30 数据</div></td></tr>
            ) : rows.map(row => (
              <tr key={row.symbol}>
                <td className="num">{row.rank}</td>
                <td>{row.symbol}</td>
                <td className="num">{money(row.raw_volume_24h_usd)}</td>
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
          <span className="panel-sub">· 阵营内合计 24h 成交量重新排 Top30</span>
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
                <th className="num">CEX 24h vol</th>
                <th className="num">DEX rank</th>
                <th className="num">DEX 24h vol</th>
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
