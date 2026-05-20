import { DashboardShell } from '@/components/dashboard-shell';
import { getFrontendURLLookup, getJSON, type DashboardMeta, type LiquiditySnapshot, type QualitySnapshot, type ShareSnapshot, type Top30Snapshot } from '@/lib/api/client';

export const dynamic = 'force-dynamic';

type SearchParams = Record<string, string | string[] | undefined>;

function scalar(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value;
}

export default async function Page({ searchParams }: { searchParams: SearchParams }) {
  const tab = scalar(searchParams.tab) ?? 'monitor';
  const query = {
    tab,
    symbol: scalar(searchParams.symbol) ?? 'BTC-USDT (perp)',
    window: scalar(searchParams.window) ?? (tab === 'share' ? '24h' : '7d'),
    tier: scalar(searchParams.tier) ?? '0.10%',
    bucket: scalar(searchParams.bucket) ?? '100000',
    platform: scalar(searchParams.platform) ?? 'binance',
    category: scalar(searchParams.category),
    coreOnly: scalar(searchParams.coreOnly),
  };
  const symbolParam = encodeURIComponent(query.symbol);
  const [meta, liquidity, quality, share, top30, lookup] = await Promise.all([
    getJSON<DashboardMeta>('/api/dashboard/meta'),
    getJSON<LiquiditySnapshot>(`/api/snapshot/liquidity?symbol=${symbolParam}&window=${query.window}`),
    getJSON<QualitySnapshot>(`/api/snapshot/quality?symbol=${symbolParam}`),
    getJSON<ShareSnapshot>(`/api/snapshot/share?window=${query.window}`),
    getJSON<Top30Snapshot>(`/api/snapshot/top30?surface=perp&platform=${query.platform}`),
    getFrontendURLLookup(),
  ]);

  return <DashboardShell query={query} data={{ meta, liquidity, quality, share, top30, lookup }} />;
}
