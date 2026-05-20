import { DashboardClient } from '@/components/dashboard-client';

export const dynamic = 'force-dynamic';

type SearchParams = Record<string, string | string[] | undefined>;

function scalar(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value;
}

export default function Page({ searchParams }: { searchParams: SearchParams }) {
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

  return <DashboardClient query={query} />;
}
