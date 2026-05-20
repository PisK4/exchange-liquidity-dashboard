import Link from 'next/link';
import { symbols } from '@/lib/api/client';

export function SymbolControls({ active, basePath }: { active: string; basePath: string }) {
  return <div className="controls">{symbols.map(symbol => <Link className="control" key={symbol} href={`${basePath}?symbol=${encodeURIComponent(symbol)}`}>{symbol === active ? `● ${symbol}` : symbol}</Link>)}</div>;
}
