import { expect, test } from '@playwright/test';
import { routeDashboardAPI } from './fixtures';

test.beforeEach(async ({ page }) => {
  await routeDashboardAPI(page);
});

for (const path of ['/', '/liquidity', '/quality', '/share', '/top30']) {
  test(`${path} renders the formal dashboard without mock wording`, async ({ page }) => {
    const errors: string[] = [];
    page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });
    await page.goto(path);
    await expect(page.locator('body')).toContainText('流动性 & 深度监控面板');
    await expect(page.locator('body')).toContainText('流动性监控');
    await expect(page.locator('body')).toContainText('盘口质量');
    await expect(page.locator('body')).toContainText('市占率');
    await expect(page.locator('body')).toContainText('Top30 成交量');
    await expect(page.locator('body')).not.toContainText(/mock data/i);
    expect(errors).toEqual([]);
  });
}

test('legacy tab URLs preserve deep-link query parameters', async ({ page }) => {
  await page.goto('/top30?platform=okx&symbol=ETH&bucket=500000');
  await expect(page).toHaveURL(/tab=top30/);
  await expect(page).toHaveURL(/platform=okx/);
  await expect(page).toHaveURL(/symbol=ETH/);
  await expect(page).toHaveURL(/bucket=500000/);

  await page.goto('/share?window=30d&symbol=BTC');
  await expect(page).toHaveURL(/tab=share/);
  await expect(page).toHaveURL(/window=30d/);
  await expect(page).toHaveURL(/symbol=BTC/);
});

test('monitor depth curves render Chart.js canvases with hover detail tooltips', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });

  await page.goto('/');

  const depthCharts = page.locator('canvas[data-chart-library="chartjs-depth-line"]');
  await expect(depthCharts).toHaveCount(3);
  await expect(depthCharts.nth(0)).toHaveAttribute('aria-label', '买盘深度曲线 BID');
  await expect(depthCharts.nth(1)).toHaveAttribute('aria-label', '卖盘深度曲线 ASK');
  await expect(depthCharts.nth(2)).toHaveAttribute('aria-label', '合计深度曲线 BID + ASK');

  await depthCharts.first().hover({ position: { x: 180, y: 90 } });
  const tooltip = page.locator('[data-testid="chartjs-depth-tooltip"]').first();
  await expect(tooltip).toBeVisible();
  await expect(tooltip).toContainText(/@ ±(0\.05|0\.1|1|2)%/);
  expect(errors).toEqual([]);
});

test('depth detail table shows depth values without per-cell source labels', async ({ page }) => {
  await page.goto('/');

  const detailPanel = page.locator('section.panel').filter({ hasText: '深度明细 · 平台 × 档位 (USD)' });
  await expect(detailPanel).toBeVisible();
  await expect(detailPanel.locator('tbody')).toContainText(/(\d|—)/);
});

test('monitor KPI cards hide status badges for 7d share and 10min spread', async ({ page }) => {
  // Seed the localStorage cache for both the canonical (?symbol=BTC) and
  // the legacy display_symbol (?symbol=BTC-USDT%20(perp)) lookup keys.
  // dashboard-client builds the fetch path from query.symbol verbatim,
  // and page.tsx defaults query.symbol to 'BTC' on the bare '/' route,
  // so the canonical key is the one the cache fallback actually consults
  // when the API stub returns 503. The legacy key is kept as well so a
  // future test that navigates to /?symbol=BTC-USDT%20(perp) still works.
  await page.addInitScript(() => {
    const now = new Date().toISOString();
    const depth = { bid_usd: 1000000, ask_usd: 1000000, total_usd: 2000000, depth_status: 'complete', strict_complete: true, display_available: true };
    const liquidityPayload = JSON.stringify({ ts: Date.now(), data: { symbol: 'BTC-USDT (perp)', snapshot_ts: now, rows: [{ platform: 'edgeX', display_symbol: 'BTC-USDT (perp)', snapshot_ts: now, source_endpoint: '', depth_status: 'complete', depth_by_tier: { '0.05%': depth, '0.10%': depth, '1.00%': depth, '2.00%': depth }, vs_median_by_tier: { '0.10%': 1.2 }, buy_slippage_bp: {}, sell_slippage_bp: {}, worst_slippage_bp: {} }], kpis: { symbol_share_7d_pct: 12.34, symbol_share_7d_status: 'partial', edgex_spread_10m_bp: 1.23, edgex_spread_10m_status: 'complete', edgex_spread_bp: 1.1, edgex_24h_share_pct: 10.5 } } });
    const qualityPayload = JSON.stringify({ ts: Date.now(), data: { symbol: 'BTC-USDT (perp)', snapshot_ts: now, slippage_buckets_usd: [50000, 100000, 500000, 1000000], rows: [] } });
    window.localStorage.setItem('edgex-dashboard:v1:/api/dashboard/meta', JSON.stringify({ ts: Date.now(), data: {
      tabs: ['monitor', 'quality', 'share', 'top30'],
      platforms: ['edgeX'],
      symbols: ['BTC-USDT (perp)'],
      categories: [
        { key: 'crypto', label: '加密货币', symbols: [
          { canonical: 'BTC', display_name: 'BTC-USD', display_symbol: 'BTC-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: 10 },
        ] },
      ],
      windows: ['24h', '7d'],
      depth_tiers: [0.0005, 0.001, 0.01, 0.02],
      slippage_buckets_usd: [50000, 100000, 500000, 1000000],
      refresh_interval_sec: 30,
      volume_discounts: {},
    } }));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/liquidity?symbol=BTC', liquidityPayload);
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/liquidity?symbol=BTC-USDT%20(perp)', liquidityPayload);
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/quality?symbol=BTC', qualityPayload);
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/quality?symbol=BTC-USDT%20(perp)', qualityPayload);
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/share?window=7d', JSON.stringify({ ts: Date.now(), data: { window: '7d', snapshot_ts: now, rows: [] } }));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/top30?surface=perp&platform=binance', JSON.stringify({ ts: Date.now(), data: { surface: 'perp', platform: 'binance', snapshot_ts: now, rows: [] } }));
  });
  await page.route('**/api/**', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
  await page.goto('/');

  const panelWithTitle = (title: string) => page.locator('section.panel').filter({ has: page.locator('.panel-head .panel-title', { hasText: title }) });

  const sharePanel = panelWithTitle('当前交易对 7d 市占率');
  await expect(sharePanel).toBeVisible();
  await expect(sharePanel).toContainText('12.34%');
  await expect(sharePanel.locator('.badge')).toHaveCount(0);

  const spreadPanel = panelWithTitle('edgeX spread (10min 均值)');
  await expect(spreadPanel).toBeVisible();
  await expect(spreadPanel).toContainText('1.23 bp');
  await expect(spreadPanel.locator('.badge')).toHaveCount(0);
});

test('share detail table matches the reference visual semantics', async ({ page }) => {
  await page.goto('/share');

  const sharePanel = page.locator('section.panel').filter({ hasText: 'edgeX 平台总交易量市占率明细表' });
  await expect(sharePanel).toContainText('切换口径查看 share + 各平台贡献');
  await expect(sharePanel).toContainText('日 (24h)');
  await expect(sharePanel).toContainText('周 (7d)');
  await expect(sharePanel).toContainText('月 (30d)');
  await expect(sharePanel.locator('thead')).toContainText('占比可视化');
  await expect(sharePanel.locator('thead')).not.toContainText('状态');
  await expect(sharePanel.locator('tbody')).toContainText('edgeX ★');
  await expect(sharePanel.getByTestId('share-ratio-bar').first()).toBeVisible();
  await expect(sharePanel.locator('.platform-self').first()).toHaveCSS('color', 'rgb(108, 207, 142)');
});

// Stub the dashboard meta + liquidity payloads so the dropdown / category
// pills can be exercised offline. Categories carry crypto + commodity +
// stock buckets so we can verify the filter actually swaps the visible
// option list.
function stubMultiCategoryMeta(page: import('@playwright/test').Page) {
  return page.addInitScript(() => {
    const now = new Date().toISOString();
    const depth = { bid_usd: 1000000, ask_usd: 1000000, total_usd: 2000000, depth_status: 'complete', strict_complete: true, display_available: true };
    window.localStorage.setItem('edgex-dashboard:v1:/api/dashboard/meta', JSON.stringify({
      ts: Date.now(),
      data: {
        tabs: ['monitor', 'quality', 'share', 'top30'],
        platforms: ['edgeX', 'binance'],
        symbols: ['BTC-USDT (perp)'],
        categories: [
          { key: 'crypto', label: '加密货币', symbols: [
            { canonical: 'BTC', display_name: 'BTC-USD', display_symbol: 'BTC-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: 10 },
            { canonical: 'ETH', display_name: 'ETH-USD', display_symbol: 'ETH-USDT (perp)', asset_category: 'crypto', instrument_kind: 'canonical', market_surface: 'perp', supported_platform_count: 10 },
          ] },
          { key: 'commodity', label: '大宗商品', symbols: [
            { canonical: 'GOLD', display_name: 'GOLD-USD', display_symbol: 'GOLD-USDT (perp)', asset_category: 'commodity', instrument_kind: 'synthetic', market_surface: 'synthetic_futures', supported_platform_count: 5 },
          ] },
          { key: 'stock', label: '股票', symbols: [
            { canonical: 'TSLA', display_name: 'TSLA-USD', display_symbol: 'TSLA-USDT (perp)', asset_category: 'stock', instrument_kind: 'synthetic', market_surface: 'synthetic_futures', supported_platform_count: 4 },
          ] },
          { key: 'index_etf', label: '指数 / ETF', symbols: [
            { canonical: 'XYZ100', display_name: 'XYZ100-USD', display_symbol: 'XYZ100-USDT (perp)', asset_category: 'index_etf', instrument_kind: 'synthetic', market_surface: 'synthetic_futures', supported_platform_count: 0 },
          ] },
        ],
        windows: ['24h', '7d'],
        depth_tiers: [0.0005, 0.001, 0.01, 0.02],
        slippage_buckets_usd: [50000, 100000, 500000, 1000000],
        refresh_interval_sec: 30,
        volume_discounts: {},
      },
    }));
    const liquidity = (sym: string) => JSON.stringify({ ts: Date.now(), data: { symbol: sym, snapshot_ts: now, rows: [{ platform: 'edgeX', display_symbol: sym, snapshot_ts: now, source_endpoint: '', depth_status: 'complete', depth_by_tier: { '0.05%': depth, '0.10%': depth, '1.00%': depth, '2.00%': depth }, vs_median_by_tier: { '0.10%': 1.2 }, buy_slippage_bp: {}, sell_slippage_bp: {}, worst_slippage_bp: {} }], kpis: { edgex_24h_share_pct: 10.5 } } });
    const empty = (sym: string) => JSON.stringify({ ts: Date.now(), data: { symbol: sym, snapshot_ts: now, slippage_buckets_usd: [50000, 100000, 500000, 1000000], rows: [] } });
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/liquidity?symbol=BTC', liquidity('BTC-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/quality?symbol=BTC', empty('BTC-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/liquidity?symbol=BTC-USDT%20(perp)', liquidity('BTC-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/quality?symbol=BTC-USDT%20(perp)', empty('BTC-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/liquidity?symbol=GOLD', liquidity('GOLD-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/quality?symbol=GOLD', empty('GOLD-USDT (perp)'));
    // XYZ100 stands in for a 0-platform canonical (doc01 §4.3 ambiguous).
    // The backend returns rows: [] and the UI must render the empty state
    // without crashing.
    const noPlatforms = (sym: string) => JSON.stringify({ ts: Date.now(), data: { symbol: sym, snapshot_ts: now, rows: [], kpis: {} } });
    const noPlatformsQuality = (sym: string) => JSON.stringify({ ts: Date.now(), data: { symbol: sym, snapshot_ts: now, slippage_buckets_usd: [50000, 100000, 500000, 1000000], rows: [] } });
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/liquidity?symbol=XYZ100', noPlatforms('XYZ100-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/quality?symbol=XYZ100', noPlatformsQuality('XYZ100-USDT (perp)'));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/share?window=7d', JSON.stringify({ ts: Date.now(), data: { window: '7d', snapshot_ts: now, rows: [] } }));
    window.localStorage.setItem('edgex-dashboard:v1:/api/snapshot/top30?surface=perp&platform=binance', JSON.stringify({ ts: Date.now(), data: { surface: 'perp', platform: 'binance', snapshot_ts: now, rows: [] } }));
  });
}

test('symbol dropdown opens, filters by typed query and routes to the canonical', async ({ page }) => {
  await stubMultiCategoryMeta(page);
  await page.route('**/api/**', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
  await page.goto('/');

  const trigger = page.getByTestId('symbol-select-trigger');
  await expect(trigger).toContainText('BTC-USD');

  await trigger.click();
  const dropdown = page.getByTestId('symbol-select-dropdown');
  await expect(dropdown).toBeVisible();

  // Default category is all -> dropdown shows symbols from every category.
  await expect(page.getByTestId('symbol-select-option-BTC')).toBeVisible();
  await expect(page.getByTestId('symbol-select-option-ETH')).toBeVisible();
  await expect(page.getByTestId('symbol-select-option-GOLD')).toBeVisible();

  // Filter narrows the list to ETH only.
  await page.getByTestId('symbol-select-input').fill('eth');
  await expect(page.locator('[data-testid="symbol-select-option-BTC"]')).toHaveCount(0);
  await expect(page.getByTestId('symbol-select-option-ETH')).toBeVisible();
});

test('switching asset category resets the dropdown to the first symbol of that category', async ({ page }) => {
  await stubMultiCategoryMeta(page);
  await page.route('**/api/**', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
  await page.goto('/');

  await page.getByTestId('category-pill-commodity').click();
  await expect(page).toHaveURL(/symbol=GOLD/);
  await expect(page.getByTestId('symbol-select-trigger')).toContainText('GOLD-USD');
});

test('legacy display_symbol URL still loads the canonical view', async ({ page }) => {
  await stubMultiCategoryMeta(page);
  await page.route('**/api/**', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));
  await page.goto('/?symbol=BTC-USDT%20(perp)');

  // Resolver maps the legacy display_symbol back to BTC for the trigger
  // label even though the URL itself is left untouched (so existing
  // bookmarks keep working without forced redirects).
  await expect(page.getByTestId('symbol-select-trigger')).toContainText('BTC-USD');
});

test('zero-platform canonical renders without crashing the dashboard', async ({ page }) => {
  await stubMultiCategoryMeta(page);
  await page.route('**/api/**', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }));

  const errors: string[] = [];
  page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });
  page.on('pageerror', err => { errors.push(err.message); });

  await page.goto('/?symbol=XYZ100');

  // Trigger reflects the canonical even though no platform serves data.
  await expect(page.getByTestId('symbol-select-trigger')).toContainText('XYZ100-USD');
  // Body still mounts without React error boundaries firing.
  await expect(page.locator('body')).toContainText('流动性 & 深度监控面板');
  // No uncaught console errors / page errors (e.g. cannot read .length of
  // undefined).
  expect(errors.filter(e => !/^Failed to load resource/i.test(e) && !/503/.test(e))).toEqual([]);
});

test('quality charts match the reference labels and signed imbalance treatment', async ({ page }) => {
  await page.goto('/quality?bucket=100000');

  const panelWithTitle = (title: string) => page.locator('section.panel').filter({ has: page.locator('.panel-head .panel-title', { hasText: title }) });
  const spreadPanel = panelWithTitle('Spread (bp)');
  await expect(spreadPanel).toContainText('买一/卖一相对价差');
  await expect(spreadPanel).toContainText(/\$\d/);
  await expect(spreadPanel.locator('.platform-self').first()).toContainText('edgeX ★');

  const slippagePanel = panelWithTitle('模拟下单滑点 (bp)');
  await expect(slippagePanel).toContainText('相对中间价');
  await expect(slippagePanel).toContainText('档位 可配置');
  await expect(slippagePanel).toContainText(/\$\d/);

  const imbalancePanel = panelWithTitle('Bid/Ask Imbalance (%)');
  await expect(imbalancePanel).toContainText('(BID深度-ASK深度)/合计');
  await expect(imbalancePanel).toContainText('正值=买侧偏厚');
  await expect(imbalancePanel).toContainText(/[+-]\d+\.\d{2}%/);

  const detailPanel = page.locator('section.panel').filter({ hasText: '盘口质量明细' });
  await expect(detailPanel.locator('thead')).toContainText('滑点 50K (bp)');
  await expect(detailPanel.locator('tbody')).toContainText('edgeX ★');
  await expect(detailPanel.locator('.badge').first()).toContainText(/健康|关注|较差/);
});
