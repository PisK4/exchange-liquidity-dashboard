import { BarList } from '@/components/bar-list';
import { SymbolControls } from '@/components/controls';
import { PlatformCell } from '@/components/platform-cell';
import { StatusBadge } from '@/components/status-badge';
import { bp, getFrontendURLLookup, getJSON, type PlatformRow } from '@/lib/api/client';

type Quality = { symbol: string; rows: PlatformRow[]; slippage_buckets_usd: number[] };

export default async function QualityPage({ searchParams }: { searchParams: { symbol?: string; bucket?: string } }) {
  const symbol = searchParams.symbol ?? 'BTC-USDT (perp)';
  const bucket = searchParams.bucket ?? '100000';
  const [data, lookup] = await Promise.all([
    getJSON<Quality>(`/api/snapshot/quality?symbol=${encodeURIComponent(symbol)}`),
    getFrontendURLLookup(),
  ]);
  const spreadRows = data.rows.map(r => ({ platform: r.platform, spread: r.spread_bp || 0 }));
  const slipRows = data.rows.map(r => ({ platform: r.platform, buy: r.buy_slippage_bp?.[bucket] || 0, sell: r.sell_slippage_bp?.[bucket] || 0 }));
  return <>
    <div className="toolbar"><h2>Order Book Quality</h2><div className="controls"><SymbolControls active={symbol} basePath="/quality" />{data.slippage_buckets_usd.map(v => <a className="control" href={`/quality?symbol=${encodeURIComponent(symbol)}&bucket=${v}`} key={v}>{v / 1000}K</a>)}</div></div>
    <div className="grid">
      <section className="panel span-6"><h2>Spread</h2><BarList rows={spreadRows} labelKey="platform" valueKey="spread" /></section>
      <section className="panel span-6"><h2>Buy Slippage {Number(bucket) / 1000}K</h2><BarList rows={slipRows} labelKey="platform" valueKey="buy" /></section>
      <section className="panel span-12"><h2>Quality Detail</h2><table><thead><tr><th>Platform</th><th>Spread</th><th>Imbalance</th><th>Buy</th><th>Sell</th><th>Status</th><th>Freshness</th></tr></thead><tbody>{data.rows.map(r => <tr key={r.platform}><td><PlatformCell platform={r.platform} displaySymbol={symbol} lookup={lookup} /></td><td>{bp(r.spread_bp)}</td><td>{r.imbalance_pct?.toFixed(2) ?? '-'}</td><td>{bp(r.buy_slippage_bp?.[bucket])}</td><td>{bp(r.sell_slippage_bp?.[bucket])}</td><td><StatusBadge status={r.depth_status} reason={r.partial_reason} /></td><td>{r.data_freshness === 'delayed' ? <span className="badge delayed">delayed</span> : <span className="muted">live</span>}</td></tr>)}</tbody></table></section>
    </div>
  </>;
}
