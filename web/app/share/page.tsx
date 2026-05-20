import { BarList } from '@/components/bar-list';
import { KpiCard } from '@/components/kpi-card';
import { PlatformCell } from '@/components/platform-cell';
import { StatusBadge } from '@/components/status-badge';
import { getFrontendURLLookup, getJSON, money, pct } from '@/lib/api/client';

type Share = { window: string; status?: string; denominator_usd?: number; rows: any[]; history?: Record<string, string> };

export default async function SharePage({ searchParams }: { searchParams: { window?: string } }) {
  const window = searchParams.window ?? '24h';
  const [data, lookup] = await Promise.all([
    getJSON<Share>(`/api/snapshot/share?window=${window}`),
    getFrontendURLLookup(),
  ]);
  return <>
    <div className="toolbar"><h2>Market Share</h2><div className="controls"><a className="control" href="/share?window=24h">24h</a><a className="control" href="/share?window=7d">7d</a><a className="control" href="/share?window=30d">30d</a></div></div>
    {data.status ? <section className="panel"><StatusBadge status={data.status} /> <span className="muted">Historical window is not available in V1.</span></section> : <div className="grid">
      <div className="span-4"><KpiCard label="Denominator" value={money(data.denominator_usd)} sub="edgeX + competitors" /></div>
      <div className="span-4"><KpiCard label="7d" value="insufficient_history" sub="disabled in V1" /></div>
      <div className="span-4"><KpiCard label="30d" value="insufficient_history" sub="disabled in V1" /></div>
      <section className="panel span-6"><h2>24h Share</h2><BarList rows={data.rows} labelKey="platform" valueKey="share_pct" /></section>
      <section className="panel span-6"><h2>Volume Table</h2><table><thead><tr><th>Platform</th><th>Adjusted Vol</th><th>Share</th><th>Discount</th><th>Status</th></tr></thead><tbody>{data.rows.map(r => <tr key={r.platform}><td><PlatformCell platform={r.platform} displaySymbol="BTC-USDT (perp)" lookup={lookup} /></td><td>{money(r.adjusted_volume_24h_usd)}</td><td>{pct(r.share_pct)}</td><td>{r.discount}</td><td><StatusBadge status={r.status} /></td></tr>)}</tbody></table></section>
    </div>}
  </>;
}
