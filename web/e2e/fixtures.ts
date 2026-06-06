import type { Page, Route } from '@playwright/test';

const now = '2026-05-25T00:00:00.000Z';
const depth = {
  bid_usd: 1_200_000,
  ask_usd: 1_100_000,
  total_usd: 2_300_000,
  depth_status: 'complete',
  strict_complete: true,
  display_available: true,
};

const depthByTier = {
  '0.05%': { ...depth, total_usd: 900_000 },
  '0.10%': depth,
  '1.00%': { ...depth, total_usd: 4_800_000 },
  '2.00%': { ...depth, total_usd: 8_400_000 },
};

const platforms = ['edgeX', 'binance'];
const mappings = platforms.map(platform => ({
  platform,
  display_symbol: 'BTC-USDT (perp)',
  display_name: 'BTC-USD',
  canonical: 'BTC',
  asset_category: 'crypto',
  market_surface: 'perp',
  instrument_kind: 'canonical',
  api_symbol: 'BTCUSDT',
  base_asset: 'BTC',
  quote_asset: 'USDT',
  settle_asset: 'USDT',
  source_endpoint: 'fixture',
}));

const meta = {
  tabs: ['monitor', 'quality', 'share', 'top30'],
  platforms,
  symbols: ['BTC-USDT (perp)'],
  categories: [{ key: 'crypto', label: '加密货币', symbols: [{ canonical: 'BTC', display_name: 'BTC-USD', display_symbol: 'BTC-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: 2 }] }],
  windows: ['24h', '7d', '30d'],
  depth_tiers: [0.0005, 0.001, 0.01, 0.02],
  slippage_buckets_usd: [50_000, 100_000, 500_000, 1_000_000],
  refresh_interval_sec: 30,
  volume_discounts: { mexc: 0.4, gate: 0.5 },
};

function liquidity() {
  return {
    symbol: 'BTC-USDT (perp)',
    snapshot_ts: now,
    rows: platforms.map((platform, i) => ({
      platform,
      display_symbol: 'BTC-USDT (perp)',
      snapshot_ts: now,
      source_endpoint: 'fixture',
      depth_status: 'complete',
      mid_price: 68_000 + i * 10,
      spread_bp: 1.1 + i,
      imbalance_pct: platform === 'edgeX' ? 6.25 : -3.5,
      depth_by_tier: depthByTier,
      vs_median_by_tier: { '0.10%': platform === 'edgeX' ? 1.2 : 0.9 },
      buy_slippage_bp: { '50000': 0.8, '100000': 1.3, '500000': 4.2, '1000000': 8.5 },
      sell_slippage_bp: { '50000': 0.9, '100000': 1.4, '500000': 4.5, '1000000': 8.9 },
      worst_slippage_bp: { '50000': 0.9, '100000': 1.4, '500000': 4.5, '1000000': 8.9 },
      verdict: '健康',
    })),
    competitor_median_by_tier: { '0.10%': 1_900_000 },
    strict_competitor_median_by_tier: { '0.10%': 1_900_000 },
    kpis: { edgex_24h_share_pct: 10.5, symbol_share_7d_pct: 12.34, edgex_spread_bp: 1.1, edgex_spread_10m_bp: 1.23 },
  };
}

function quality() {
  return {
    symbol: 'BTC-USDT (perp)',
    snapshot_ts: now,
    slippage_buckets_usd: [50_000, 100_000, 500_000, 1_000_000],
    rows: liquidity().rows,
  };
}

function share(window: string) {
  return {
    window,
    snapshot_ts: now,
    denominator_usd: 12_000_000_000,
    rows: [
      { rank: 1, platform: 'binance', raw_volume_usd: 9_000_000_000, adjusted_volume_usd: 9_000_000_000, share_pct: 75, status: 'complete', data_source: 'coingecko' },
      { rank: 2, platform: 'edgeX', raw_volume_usd: 3_000_000_000, adjusted_volume_usd: 3_000_000_000, share_pct: 25, status: 'complete', data_source: 'coingecko' },
    ],
    kpis: { edgex_share_pct: 25, edgex_total_volume_usd: 3_000_000_000, denominator_usd: 12_000_000_000 },
    trend: { status: 'complete', points: [{ day: '2026-05-24', edgex_share_pct: 20 }, { day: '2026-05-25', edgex_share_pct: 25 }] },
  };
}

const top30 = {
  surface: 'perp',
  platform: 'binance',
  snapshot_ts: now,
  status: 'complete',
  rows: [{ rank: 1, platform: 'binance', symbol: 'BTC-USDT (perp)', volume_24h_usd: 2_000_000_000, volume_7d_usd: 12_000_000_000, delta_7d_pct: 3.2, edgex_listed: true, status: 'complete', data_source: 'coingecko' }],
};

const top30Divergence = {
  snapshot_ts: now,
  status: 'complete',
  cex_platforms: ['binance', 'okx', 'bybit', 'bitget', 'mexc', 'gate', 'bingx'],
  dex_platforms: ['hyperliquid', 'lighter', 'edgeX'],
  significant_rank_delta: 10,
  cex_top30: [
    { rank: 1, symbol: 'BTC', adjusted_volume_24h_usd: 5_000_000_000, raw_volume_24h_usd: 5_400_000_000, platform_count: 7 },
    { rank: 2, symbol: 'ETH', adjusted_volume_24h_usd: 3_000_000_000, raw_volume_24h_usd: 3_200_000_000, platform_count: 7 },
    { rank: 3, symbol: 'PEPE', adjusted_volume_24h_usd: 800_000_000, raw_volume_24h_usd: 900_000_000, platform_count: 5 },
  ],
  dex_top30: [
    { rank: 1, symbol: 'BTC', adjusted_volume_24h_usd: 1_500_000_000, raw_volume_24h_usd: 1_500_000_000, platform_count: 3 },
    { rank: 2, symbol: 'HYPE', adjusted_volume_24h_usd: 900_000_000, raw_volume_24h_usd: 900_000_000, platform_count: 2 },
    { rank: 3, symbol: 'ETH', adjusted_volume_24h_usd: 600_000_000, raw_volume_24h_usd: 600_000_000, platform_count: 3 },
  ],
  divergence_rows: [
    { symbol: 'PEPE', cex_rank: 3, cex_adjusted_volume_24h_usd: 800_000_000, dex_rank: null, dex_adjusted_volume_24h_usd: null, rank_delta: null, category: 'cex_only', edgex_listed: false, edgex_listed_status: 'complete' },
    { symbol: 'HYPE', cex_rank: null, cex_adjusted_volume_24h_usd: null, dex_rank: 2, dex_adjusted_volume_24h_usd: 900_000_000, rank_delta: null, category: 'dex_only', edgex_listed: false, edgex_listed_status: 'complete' },
    { symbol: 'BTC', cex_rank: 1, cex_adjusted_volume_24h_usd: 5_000_000_000, dex_rank: 1, dex_adjusted_volume_24h_usd: 1_500_000_000, rank_delta: 0, category: 'aligned', edgex_listed: true, edgex_listed_status: 'complete' },
    { symbol: 'ETH', cex_rank: 2, cex_adjusted_volume_24h_usd: 3_000_000_000, dex_rank: 3, dex_adjusted_volume_24h_usd: 600_000_000, rank_delta: 1, category: 'aligned', edgex_listed: true, edgex_listed_status: 'complete' },
  ],
  kpi: { cex_only_count: 1, dex_only_count: 1, heavy_count: 0, aligned_count: 2, edgex_gap_count: 0 },
};

export async function routeDashboardAPI(page: Page) {
  await page.route('**/api/**', async (route: Route) => {
    const url = new URL(route.request().url());
    const payload =
      url.pathname === '/api/ops-intelligence/meta' ? meta :
      url.pathname === '/api/symbols' ? { symbols: ['BTC-USDT (perp)'], mappings } :
      url.pathname === '/api/symbols/coverage' ? { snapshot_ts: now, rows: mappings.map(m => ({ platform: m.platform, display_symbol: m.display_symbol, depth_status: 'complete', source_endpoint: 'fixture' })) } :
      url.pathname === '/api/snapshot/liquidity' ? liquidity() :
      url.pathname === '/api/snapshot/quality' ? quality() :
      url.pathname === '/api/snapshot/share' ? share(url.searchParams.get('window') ?? '24h') :
      url.pathname === '/api/snapshot/top30/divergence' ? top30Divergence :
      url.pathname === '/api/snapshot/top30' ? top30 :
      url.pathname === '/api/collection-status' ? { last_run: { run_id: 'fixture', success: 2, failed: 0 }, rows: [] } :
      url.pathname === '/api/runtime-config' ? { collection_interval: '30s' } :
      null;
    if (!payload) {
      await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(payload) });
  });
}
