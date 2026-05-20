import { BarList } from '@/components/bar-list';
import { SymbolControls } from '@/components/controls';
import { KpiCard } from '@/components/kpi-card';
import { StatusBadge } from '@/components/status-badge';
import { bp, getJSON, money, pct, type PlatformRow } from '@/lib/api/client';

type Liquidity = { symbol: string; snapshot_ts: string; kpis: any; rows: PlatformRow[] };

export default async function LiquidityPage({ searchParams }: { searchParams: { symbol?: string } }) {
  const symbol = searchParams.symbol ?? 'BTC-USDT (perp)';
  const data = await getJSON<Liquidity>(`/api/snapshot/liquidity?symbol=${encodeURIComponent(symbol)}`);
  const depthRows = data.rows.map(r => ({ platform: r.platform, total: r.depth_by_tier?.['0.10%']?.total_usd ? r.depth_by_tier['0.10%'].total_usd / 1_000_000 : 0 }));
  return <>
    <div className="toolbar"><h2>Liquidity</h2><SymbolControls active={symbol} basePath="/liquidity" /></div>
    <div className="grid">
      <div className="span-3"><KpiCard label="edgeX 0.10% depth" value={money(data.kpis?.edgex_depth_by_tier?.['0.10%']?.total_usd)} sub="BID + ASK" /></div>
      <div className="span-3"><KpiCard label="edgeX 24h share" value={pct(data.kpis?.edgex_24h_share_pct)} sub="MEXC/Gate adjusted" /></div>
      <div className="span-3"><KpiCard label="edgeX spread" value={bp(data.kpis?.edgex_spread_bp)} sub="latest snapshot" /></div>
      <div className="span-3"><KpiCard label="Snapshot" value={new Date(data.snapshot_ts).toLocaleTimeString()} sub="UTC source timestamps" /></div>
      <section className="panel span-6"><h2>0.10% Total Depth</h2><BarList rows={depthRows} labelKey="platform" valueKey="total" /></section>
      <section className="panel span-6"><h2>Status</h2><table><tbody>{data.rows.map(r => <tr key={r.platform}><td>{r.platform}</td><td><StatusBadge status={r.depth_status} reason={r.partial_reason} /> {r.data_freshness === 'delayed' ? <span className="badge delayed">delayed</span> : null}</td><td className={r.data_freshness === 'delayed' ? 'muted' : 'error'}>{r.data_freshness === 'delayed' ? `last collection ${r.last_collection_status}: ${r.last_collection_error ?? ''}` : r.error}</td></tr>)}</tbody></table></section>
      <section className="panel span-12"><h2>Platform × Tier</h2><table><thead><tr><th>Platform</th><th>0.05%</th><th>0.10%</th><th>1.00%</th><th>2.00%</th><th>Status</th><th>Freshness</th><th>Source</th></tr></thead><tbody>{data.rows.map(r => <tr key={r.platform}><td>{r.platform}</td><td>{money(r.depth_by_tier?.['0.05%']?.total_usd)}</td><td>{money(r.depth_by_tier?.['0.10%']?.total_usd)}</td><td>{money(r.depth_by_tier?.['1.00%']?.total_usd)}</td><td>{money(r.depth_by_tier?.['2.00%']?.total_usd)}</td><td><StatusBadge status={r.depth_status} reason={r.partial_reason} /></td><td>{r.data_freshness === 'delayed' ? <span className="badge delayed">delayed</span> : <span className="muted">live</span>}</td><td className="muted">{r.source_endpoint}</td></tr>)}</tbody></table></section>
    </div>
  </>;
}
