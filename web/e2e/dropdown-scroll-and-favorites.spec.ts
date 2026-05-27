import { expect, test, type Page, type Route } from '@playwright/test';

// dropdown-scroll-and-favorites.spec.ts validates two related fixes
// landed together:
//   1. fix(edgex-dashboard): make symbol dropdown reliably scrollable —
//      asserts that opening the symbol pill dropdown over a long
//      catalog yields a scrollable list with a visible scrollbar.
//   2. feat(edgex-dashboard): favorite-star symbol picker — asserts
//      that the per-row ★ button toggles the watchlist (URL +
//      localStorage in lockstep) without closing the dropdown, that
//      the cap at MAX_WATCHLIST=10 disables further stars, and that
//      the toolbar 'manage' button opens the same picker.

const now = '2026-05-25T00:00:00.000Z';
const platforms = ['edgeX', 'binance', 'okx', 'bybit', 'bingx'];

// 30 synthetic canonicals so the dropdown comfortably overflows the
// viewport — the bug report was 'cannot scroll among dozens of items'
// and we need enough rows to make scrollHeight > clientHeight regardless
// of viewport size in CI.
const SYMBOL_COUNT = 30;
const symbolList = Array.from({ length: SYMBOL_COUNT }).map((_, i) => {
  const num = String(i + 1).padStart(2, '0');
  const canonical = `SYM${num}`;
  return {
    canonical,
    display_name: `${canonical}-USD`,
    display_symbol: `${canonical}-USDT (perp)`,
    asset_category: 'crypto',
    instrument_kind: 'canonical',
    market_surface: 'perp',
    supported_platform_count: platforms.length,
  };
});

const meta = {
  tabs: ['monitor', 'quality', 'share', 'top30'],
  platforms,
  symbols: symbolList.map(s => s.display_symbol),
  categories: [
    { key: 'crypto', label: '加密货币', symbols: symbolList },
  ],
  windows: ['24h', '7d', '30d'],
  depth_tiers: [0.0005, 0.001, 0.01, 0.02],
  slippage_buckets_usd: [50_000, 100_000, 500_000, 1_000_000],
  refresh_interval_sec: 30,
  volume_discounts: { mexc: 0.4, gate: 0.5 },
};

function liquidityFor(symbolDisplay: string) {
  const meta = symbolList.find(s => s.display_symbol === symbolDisplay || s.canonical === symbolDisplay);
  const display = meta?.display_symbol ?? symbolDisplay;
  const rows = platforms.map((platform, i) => ({
    platform,
    display_symbol: display,
    snapshot_ts: now,
    source_endpoint: 'fixture',
    depth_status: 'complete',
    mid_price: 100 + i,
    spread_bp: 1.0,
    imbalance_pct: 0,
    depth_by_tier: {
      '0.05%': { bid_usd: 1, ask_usd: 1, total_usd: 1, depth_status: 'complete', strict_complete: true, display_available: true },
      '0.10%': { bid_usd: 1, ask_usd: 1, total_usd: 1, depth_status: 'complete', strict_complete: true, display_available: true },
      '1.00%': { bid_usd: 1, ask_usd: 1, total_usd: 1, depth_status: 'complete', strict_complete: true, display_available: true },
      '2.00%': { bid_usd: 1, ask_usd: 1, total_usd: 1, depth_status: 'complete', strict_complete: true, display_available: true },
    },
    vs_median_by_tier: { '0.10%': 1 },
    buy_slippage_bp: { '50000': 1, '100000': 1, '500000': 1, '1000000': 1 },
    sell_slippage_bp: { '50000': 1, '100000': 1, '500000': 1, '1000000': 1 },
    worst_slippage_bp: { '50000': 1, '100000': 1, '500000': 1, '1000000': 1 },
    verdict: '健康',
    funding: { platform, period_hours: 8, rate_native: 0.005, rate_8h: 0.005, status: 'complete', snapshot_ts: now },
  }));
  return {
    symbol: display,
    snapshot_ts: now,
    rows,
    competitor_median_by_tier: { '0.10%': 1 },
    strict_competitor_median_by_tier: { '0.10%': 1 },
    kpis: {
      edgex_24h_share_pct: 10,
      symbol_share_7d_pct: 5,
      edgex_spread_bp: 1.0,
      edgex_spread_10m_bp: 1.0,
      edgex_funding_rate_8h: 0.005,
      competitor_funding_rate_median_8h: 0.005,
      competitor_funding_rate_median_8h_status: 'complete',
      competitor_funding_rate_median_8h_samples: 3,
    },
  };
}

const share = {
  window: '7d', snapshot_ts: now, denominator_usd: 1, rows: [],
  kpis: { edgex_share_pct: 0, edgex_total_volume_usd: 0, denominator_usd: 1 },
  trend: { status: 'complete', points: [] },
};
const top30 = { surface: 'perp', platform: 'binance', snapshot_ts: now, status: 'complete', rows: [] };
const top30Divergence = {
  snapshot_ts: now, status: 'complete',
  cex_platforms: [], dex_platforms: [], significant_rank_delta: 10,
  cex_top30: [], dex_top30: [], divergence_rows: [],
  kpi: { cex_only_count: 0, dex_only_count: 0, heavy_count: 0, aligned_count: 0, edgex_gap_count: 0 },
};

async function routeMany(page: Page) {
  await page.route('**/api/**', async (route: Route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/dashboard/meta') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(meta) });
    if (url.pathname === '/api/symbols') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ symbols: meta.symbols, mappings: [] }) });
    if (url.pathname === '/api/symbols/coverage') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ snapshot_ts: now, rows: [] }) });
    if (url.pathname === '/api/snapshot/liquidity') {
      const symbol = url.searchParams.get('symbol') ?? 'SYM01';
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(liquidityFor(symbol)) });
    }
    if (url.pathname === '/api/snapshot/quality') {
      const symbol = url.searchParams.get('symbol') ?? 'SYM01';
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(liquidityFor(symbol)) });
    }
    if (url.pathname === '/api/snapshot/share') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(share) });
    if (url.pathname === '/api/snapshot/top30/divergence') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(top30Divergence) });
    if (url.pathname === '/api/snapshot/top30') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(top30) });
    if (url.pathname === '/api/collection-status') return route.fulfill({ status: 200, contentType: 'application/json', body: '{"last_run":{"run_id":"fixture","success":2,"failed":0},"rows":[]}' });
    if (url.pathname === '/api/runtime-config') return route.fulfill({ status: 200, contentType: 'application/json', body: '{"collection_interval":"30s"}' });
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
  });
}

test.beforeEach(async ({ page }) => {
  await routeMany(page);
});

test('symbol dropdown is scrollable when the catalog overflows', async ({ page }) => {
  await page.goto('/?symbol=SYM01');
  await page.getByTestId('symbol-select-trigger').click();
  const list = page.locator('.symbol-select-list').first();
  await expect(list).toBeVisible();
  // The list must have content overflow: scrollHeight > clientHeight.
  const { scrollHeight, clientHeight } = await list.evaluate((el) => ({
    scrollHeight: el.scrollHeight,
    clientHeight: el.clientHeight,
  }));
  expect(scrollHeight).toBeGreaterThan(clientHeight);
  // Programmatic scroll lands a non-zero scrollTop, proving the list
  // is actually scrollable rather than visually clipped.
  await list.evaluate((el) => el.scrollBy(0, 200));
  const scrollTop = await list.evaluate((el) => el.scrollTop);
  expect(scrollTop).toBeGreaterThan(0);
});

test('arrow-down keyboard nav scrolls the dropdown to keep highlight visible', async ({ page }) => {
  await page.goto('/?symbol=SYM01');
  await page.getByTestId('symbol-select-trigger').click();
  const input = page.getByTestId('symbol-select-input');
  await input.focus();
  // Press ArrowDown ~25 times to push the highlight past the visible
  // viewport of the dropdown (which holds ~10-15 rows). The post-fix
  // behaviour is that the list's scrollTop advances; the regression
  // would leave it at 0.
  for (let i = 0; i < 25; i++) {
    await input.press('ArrowDown');
  }
  const list = page.locator('.symbol-select-list').first();
  const scrollTop = await list.evaluate((el) => el.scrollTop);
  expect(scrollTop).toBeGreaterThan(0);
});

test('clicking the ★ on a row toggles the watchlist without closing the dropdown', async ({ page }) => {
  await page.goto('/?symbol=SYM01');
  await page.getByTestId('symbol-select-trigger').click();
  const list = page.getByTestId('symbol-select-dropdown');
  await expect(list).toBeVisible();
  const sym02Star = page.getByTestId('symbol-select-star-SYM02');
  await sym02Star.click();
  // Dropdown must remain open after a star click, so the operator can
  // bulk-favorite without re-opening between picks.
  await expect(list).toBeVisible();
  // Toolbar chip materialises for the freshly starred symbol.
  await expect(page.getByTestId('watchlist-chip-SYM02')).toBeVisible();
  // URL syncs.
  await expect(page).toHaveURL(/watchlist=[^&]*SYM02/);
  // localStorage syncs.
  const stored = await page.evaluate(() => window.localStorage.getItem('edgex-dashboard:watchlist:v1'));
  expect(stored).not.toBeNull();
  const parsed = JSON.parse(stored as string) as string[];
  expect(parsed).toContain('SYM02');
  // ★ became filled (aria-pressed="true").
  await expect(sym02Star).toHaveAttribute('aria-pressed', 'true');
  // Click again to unstar; chip should disappear.
  await sym02Star.click();
  await expect(page.getByTestId('watchlist-chip-SYM02')).toHaveCount(0);
  await expect(sym02Star).toHaveAttribute('aria-pressed', 'false');
});

test('star button is disabled once watchlist hits the 10-symbol cap', async ({ page }) => {
  // Pre-seed 10 favorites via URL so the cap is exactly hit.
  const tenSyms = Array.from({ length: 10 }).map((_, i) => `SYM${String(i + 1).padStart(2, '0')}`);
  await page.goto(`/?symbol=SYM01&watchlist=${tenSyms.join(',')}`);
  await page.getByTestId('symbol-select-trigger').click();
  // SYM11 is NOT in the watchlist; its star must be disabled.
  const sym11Star = page.getByTestId('symbol-select-star-SYM11');
  await expect(sym11Star).toBeDisabled();
  // SYM01 IS in the watchlist; its star is still enabled (so the user
  // can free up a slot before adding a new one).
  const sym01Star = page.getByTestId('symbol-select-star-SYM01');
  await expect(sym01Star).toBeEnabled();
});

test('toolbar manage-favorites button opens the same picker', async ({ page }) => {
  await page.goto('/?symbol=SYM01&watchlist=SYM01,SYM02');
  await page.getByTestId('watchlist-add-trigger').click();
  const toolbarPicker = page.getByTestId('watchlist-add-dropdown');
  await expect(toolbarPicker).toBeVisible();
  // Star toggles via the toolbar dropdown also commit to URL +
  // localStorage, identically to the symbol-select path.
  await page.getByTestId('watchlist-add-star-SYM03').click();
  await expect(page.getByTestId('watchlist-chip-SYM03')).toBeVisible();
});
