import type { Page, Route } from '@playwright/test';

// fixtures-watchlist.ts mirrors the shape of fixtures.ts but extends it
// with the v2.1 fields the watchlist + funding tests need:
//   1. multiple canonical symbols in meta.categories so the toolbar
//      dropdown has real options to filter against.
//   2. per-symbol Liquidity snapshots that vary depth + funding values
//      so a card grid visibly differentiates symbols and a regression
//      that points all cards at the same data would be caught.
//   3. funding fields on each PlatformRow + kpis.* so the Liquidity KPI
//      panel, the table column, the Quality span-24 row and the per-
//      card funding line all have data to render.
//
// We keep this in a dedicated file (instead of extending fixtures.ts)
// because the legacy dashboard.spec.ts tests assert specific values
// (e.g. 'spread 1.23 bp') we don't want to perturb. Each new spec file
// imports its own fixture entry point.

const now = '2026-05-25T00:00:00.000Z';

const depthBase = {
  bid_usd: 1_200_000,
  ask_usd: 1_100_000,
  total_usd: 2_300_000,
  depth_status: 'complete',
  strict_complete: true,
  display_available: true,
};

function depthByTier(scale: number) {
  return {
    '0.05%': { ...depthBase, total_usd: 900_000 * scale },
    '0.10%': { ...depthBase, total_usd: 2_300_000 * scale },
    '1.00%': { ...depthBase, total_usd: 4_800_000 * scale },
    '2.00%': { ...depthBase, total_usd: 8_400_000 * scale },
  };
}

// Each symbol gets a per-platform funding payload. We exercise four
// concrete states the UI must render distinctly:
//   1. edgeX with a native 4h period folded into 8h (×2)
//   2. binance with the canonical 8h period (×1)
//   3. okx with a positive rate distinct from binance (drives the
//      median above edgeX so the delta is negative and visible)
//   4. unsupported platform that must surface as '—' and grey.
function fundingFor(platform: string, symbol: string) {
  // Values follow CoinGecko's /derivatives convention: funding_rate is
  // already in percent units (e.g. 0.0095 means 0.0095%). The backend
  // stores it verbatim; the frontend formatter prints `.toFixed(4) + '%'`
  // without further scaling. Magnitudes here mirror real BTC funding
  // levels (~0.005-0.015% per 8h) so the fixture exercises realistic
  // precision instead of vanishingly small numbers that all round to 0.
  // rank reflects the ascending 8h ordering across the four complete
  // fixture rows: edgeX 0.0050 → 1, bybit 0.0060 → 2, binance 0.0090
  // → 3, okx 0.0120 → 4. vs_median_8h is rate_8h - median (median =
  // 0.0090). Both fields are normally populated by the Go store's
  // enrichFundingVsMedianRows + enrichFundingRankRows; the fixture
  // mirrors that contract so e2e tests exercise the same wire shape
  // the production backend emits.
  if (platform === 'edgeX') {
    return { platform, period_hours: 4, rate_native: 0.0025, rate_8h: 0.0050, status: 'complete', snapshot_ts: now, vs_median_8h: -0.0040, rank: 1 };
  }
  if (platform === 'binance') {
    return { platform, period_hours: 8, rate_native: 0.0090, rate_8h: 0.0090, status: 'complete', snapshot_ts: now, vs_median_8h: 0, rank: 3 };
  }
  if (platform === 'okx') {
    return { platform, period_hours: 8, rate_native: 0.0120, rate_8h: 0.0120, status: 'complete', snapshot_ts: now, vs_median_8h: 0.0030, rank: 4 };
  }
  if (platform === 'bybit') {
    return { platform, period_hours: 8, rate_native: 0.0060, rate_8h: 0.0060, status: 'complete', snapshot_ts: now, vs_median_8h: -0.0030, rank: 2 };
  }
  // hyperliquid settles funding every 1h. The 8h equivalent is tiny
  // (≈+0.0002%) and the native 1h rate is ~1/8 of that (≈+0.000025%).
  // At 4dp the native value collapses to "+0.0000%" — the formatter
  // must bump to 6dp on this case so the actual magnitude survives.
  // Rank 0 here means "do not enter the rank ladder" — the fixture
  // keeps hyperliquid out of the deterministic 1..4 sequence the
  // detail-table rank ladder test asserts on, since the 0.0002% rate
  // would slot in below edgeX and shift every other rank.
  if (platform === 'hyperliquid') {
    return { platform, period_hours: 1, rate_native: 0.000025, rate_8h: 0.0002, status: 'complete', snapshot_ts: now, vs_median_8h: -0.0088, rank: 0 };
  }
  // bingx in the v2.1 catalog is marked unsupported for funding; that
  // status must propagate through to the UI as muted '—'.
  if (platform === 'bingx') {
    return { platform, period_hours: null, rate_native: null, rate_8h: null, status: 'unsupported', snapshot_ts: now };
  }
  void symbol;
  return null;
}

const platforms = ['edgeX', 'binance', 'okx', 'bybit', 'hyperliquid', 'bingx'];

const categorySymbols = [
  { canonical: 'BTC', display_name: 'BTC-USD', display_symbol: 'BTC-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: platforms.length },
  { canonical: 'ETH', display_name: 'ETH-USD', display_symbol: 'ETH-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: platforms.length },
  { canonical: 'SOL', display_name: 'SOL-USD', display_symbol: 'SOL-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: platforms.length },
  { canonical: 'GOLD', display_name: 'GOLD-USD', display_symbol: 'GOLD-USDT (perp)', asset_category: 'commodity', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: platforms.length },
];

const mappings = categorySymbols.flatMap(s =>
  platforms.map(platform => ({
    platform,
    display_symbol: s.display_symbol,
    display_name: s.display_name,
    canonical: s.canonical,
    asset_category: s.asset_category,
    market_surface: 'perp',
    instrument_kind: 'canonical',
    api_symbol: `${s.canonical}USDT`,
    base_asset: s.canonical,
    quote_asset: 'USDT',
    settle_asset: 'USDT',
    source_endpoint: 'fixture',
  })),
);

const meta = {
  tabs: ['monitor', 'quality', 'share', 'top30'],
  platforms,
  symbols: categorySymbols.map(s => s.display_symbol),
  categories: [
    { key: 'crypto', label: '加密货币', symbols: categorySymbols.filter(s => s.asset_category === 'crypto') },
    { key: 'commodity', label: '大宗商品', symbols: categorySymbols.filter(s => s.asset_category === 'commodity') },
  ],
  windows: ['24h', '7d', '30d'],
  depth_tiers: [0.0005, 0.001, 0.01, 0.02],
  slippage_buckets_usd: [50_000, 100_000, 500_000, 1_000_000],
  refresh_interval_sec: 30,
  volume_discounts: { mexc: 0.4, gate: 0.5 },
};

// Per-symbol depth scaling keeps every card visually distinct so the
// snapshot fan-out can be visually verified.
const depthScale: Record<string, number> = { BTC: 1.0, ETH: 0.7, SOL: 0.4, GOLD: 0.25 };

// Per-symbol headline 7d share, so the WatchlistCard 7d share row is
// not identical across cards (regression guard: a bad reducer that
// keyed everything off the first response would surface here).
const sevenDayShare: Record<string, number> = { BTC: 12.34, ETH: 8.7, SOL: 4.2, GOLD: 1.1 };

function liquidityFor(symbolDisplay: string) {
  const meta = categorySymbols.find(s => s.display_symbol === symbolDisplay || s.canonical === symbolDisplay);
  const canonical = meta?.canonical ?? 'BTC';
  const scale = depthScale[canonical] ?? 1;
  const rows = platforms.map((platform, i) => ({
    platform,
    display_symbol: meta?.display_symbol ?? symbolDisplay,
    snapshot_ts: now,
    source_endpoint: 'fixture',
    depth_status: 'complete',
    mid_price: 68_000 + i * 10,
    spread_bp: 1.1 + i * 0.4,
    imbalance_pct: platform === 'edgeX' ? 6.25 : -3.5,
    depth_by_tier: depthByTier(scale),
    vs_median_by_tier: { '0.10%': platform === 'edgeX' ? 1.2 : 0.9 },
    buy_slippage_bp: { '50000': 0.8, '100000': 1.3, '500000': 4.2, '1000000': 8.5 },
    sell_slippage_bp: { '50000': 0.9, '100000': 1.4, '500000': 4.5, '1000000': 8.9 },
    worst_slippage_bp: { '50000': 0.9, '100000': 1.4, '500000': 4.5, '1000000': 8.9 },
    verdict: '健康',
    funding: fundingFor(platform, canonical),
  }));
  // Median is computed across the three complete competitor rows
  // (binance 0.0090, okx 0.0120, bybit 0.0060) = 0.0090.
  return {
    symbol: meta?.display_symbol ?? symbolDisplay,
    snapshot_ts: now,
    rows,
    competitor_median_by_tier: { '0.10%': 1_900_000 * scale },
    strict_competitor_median_by_tier: { '0.10%': 1_900_000 * scale },
    kpis: {
      edgex_24h_share_pct: 10.5,
      symbol_share_7d_pct: sevenDayShare[canonical] ?? 5,
      edgex_spread_bp: 1.1,
      edgex_spread_10m_bp: 1.23,
      edgex_funding_rate_8h: 0.0050,
      competitor_funding_rate_median_8h: 0.0090,
      competitor_funding_rate_median_8h_status: 'complete',
      competitor_funding_rate_median_8h_samples: 3,
    },
  };
}

function qualityFor(symbolDisplay: string) {
  const liq = liquidityFor(symbolDisplay);
  return {
    symbol: liq.symbol,
    snapshot_ts: now,
    slippage_buckets_usd: [50_000, 100_000, 500_000, 1_000_000],
    rows: liq.rows,
    kpis: liq.kpis,
  };
}

const share = {
  window: '7d',
  snapshot_ts: now,
  denominator_usd: 12_000_000_000,
  rows: [
    { rank: 1, platform: 'binance', raw_volume_usd: 9_000_000_000, adjusted_volume_usd: 9_000_000_000, share_pct: 75, status: 'complete', data_source: 'coingecko' },
    { rank: 2, platform: 'edgeX', raw_volume_usd: 3_000_000_000, adjusted_volume_usd: 3_000_000_000, share_pct: 25, status: 'complete', data_source: 'coingecko' },
  ],
  kpis: { edgex_share_pct: 25, edgex_total_volume_usd: 3_000_000_000, denominator_usd: 12_000_000_000 },
  trend: { status: 'complete', points: [{ day: '2026-05-24', edgex_share_pct: 20 }, { day: '2026-05-25', edgex_share_pct: 25 }] },
};

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
  cex_top30: [],
  dex_top30: [],
  divergence_rows: [],
  kpi: { cex_only_count: 0, dex_only_count: 0, heavy_count: 0, aligned_count: 0, edgex_gap_count: 0 },
};

export async function routeWatchlistAPI(page: Page) {
  await page.route('**/api/**', async (route: Route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/dashboard/meta') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(meta) });
    if (url.pathname === '/api/symbols') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ symbols: meta.symbols, mappings }) });
    if (url.pathname === '/api/symbols/coverage') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ snapshot_ts: now, rows: mappings.map(m => ({ platform: m.platform, display_symbol: m.display_symbol, depth_status: 'complete', source_endpoint: 'fixture' })) }) });
    }
    if (url.pathname === '/api/snapshot/liquidity') {
      const symbol = url.searchParams.get('symbol') ?? 'BTC';
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(liquidityFor(symbol)) });
    }
    if (url.pathname === '/api/snapshot/quality') {
      const symbol = url.searchParams.get('symbol') ?? 'BTC';
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(qualityFor(symbol)) });
    }
    if (url.pathname === '/api/snapshot/share') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(share) });
    if (url.pathname === '/api/snapshot/top30/divergence') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(top30Divergence) });
    if (url.pathname === '/api/snapshot/top30') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(top30) });
    if (url.pathname === '/api/collection-status') return route.fulfill({ status: 200, contentType: 'application/json', body: '{"last_run":{"run_id":"fixture","success":2,"failed":0},"rows":[]}' });
    if (url.pathname === '/api/runtime-config') return route.fulfill({ status: 200, contentType: 'application/json', body: '{"collection_interval":"30s"}' });
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
  });
}
