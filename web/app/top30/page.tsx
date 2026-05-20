import { StatusBadge } from '@/components/status-badge';
import { getJSON, money } from '@/lib/api/client';

type Top30 = { surface: string; platform: string; rows: any[] };
const platforms = ['binance', 'okx', 'bybit', 'bitget', 'bingx', 'mexc', 'gate', 'hyperliquid', 'edgeX', 'lighter'];

export default async function Top30Page({ searchParams }: { searchParams: { platform?: string } }) {
  const platform = searchParams.platform ?? 'binance';
  const data = await getJSON<Top30>(`/api/snapshot/top30?surface=perp&platform=${platform}`);
  return <>
    <div className="toolbar"><h2>Top30</h2><div className="controls">{platforms.map(p => <a className="control" key={p} href={`/top30?platform=${p}`}>{p === platform ? `● ${p}` : p}</a>)}</div></div>
    <section className="panel"><table><thead><tr><th>Rank</th><th>Symbol</th><th>24h Vol</th><th>7d Vol</th><th>7d Δ</th><th>Status</th><th>Action</th></tr></thead><tbody>{data.rows.map(row => <tr key={`${row.rank}-${row.symbol}`}><td>{row.rank}</td><td>{row.symbol}</td><td>{money(row.volume_24h_usd)}</td><td><StatusBadge status={row.volume_7d_status} /></td><td><StatusBadge status={row.delta_7d_status} /></td><td><StatusBadge status={row.status} /></td><td className="muted">{row.suggested_action}</td></tr>)}</tbody></table></section>
  </>;
}
