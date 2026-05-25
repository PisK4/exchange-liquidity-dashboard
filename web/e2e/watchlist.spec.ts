import { expect, test } from '@playwright/test';
import { routeWatchlistAPI } from './fixtures-watchlist';

test.beforeEach(async ({ page }) => {
  await routeWatchlistAPI(page);
});

// The watchlist suite focuses on the user-visible side of v2.1: the
// URL/localStorage merge, the chip toolbar mutations, fan-out card
// rendering, the funding KPI / column / span-24 strip, and a guarantee
// that funding values are 8h-normalised text (no '%-per-4h' regressions).
//
// Each test creates a fresh BrowserContext via Playwright's default
// test isolation so localStorage is empty at start; tests that need a
// seeded list use page.addInitScript before navigation.

test('default visit shows BTC fallback and the watchlist toolbar', async ({ page }) => {
  await page.goto('/');
  // Toolbar visible with the BTC fallback chip rendered.
  const toolbar = page.getByTestId('watchlist-toolbar');
  await expect(toolbar).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-BTC')).toBeVisible();
  // Add button is enabled (we are well under MAX_WATCHLIST).
  await expect(page.getByTestId('watchlist-add-trigger')).toBeEnabled();
});

test('URL ?watchlist=BTC,ETH,SOL hydrates three chips and renders cards', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH,SOL');
  for (const sym of ['BTC', 'ETH', 'SOL']) {
    await expect(page.getByTestId(`watchlist-chip-${sym}`)).toBeVisible();
  }
  // length > 1 must switch to card mode (no depth detail table visible).
  await expect(page.getByTestId('watchlist-card-BTC')).toBeVisible();
  await expect(page.getByTestId('watchlist-card-ETH')).toBeVisible();
  await expect(page.getByTestId('watchlist-card-SOL')).toBeVisible();
  await expect(page.locator('text=深度明细 · 平台 × 档位 (USD)')).toHaveCount(0);
});

test('adding a symbol via the dropdown commits to URL and localStorage', async ({ page }) => {
  await page.goto('/?watchlist=BTC');
  await page.getByTestId('watchlist-add-trigger').click();
  await page.getByTestId('watchlist-add-option-ETH').click();
  // URL is replaceState-rewritten with the merged list.
  await expect(page).toHaveURL(/watchlist=BTC%2CETH|watchlist=BTC,ETH/);
  // localStorage holds the same canonical-uppercase value.
  const stored = await page.evaluate(() => window.localStorage.getItem('edgex-dashboard:watchlist:v1'));
  expect(stored).not.toBeNull();
  expect(JSON.parse(stored as string)).toEqual(['BTC', 'ETH']);
});

test('removing the last chip restores BTC fallback and persists it to localStorage', async ({ page }) => {
  await page.goto('/?watchlist=ETH');
  await page.getByTestId('watchlist-chip-remove-ETH').click();
  // The DashboardClient effect re-seeds BTC when the list empties out;
  // the side-effect bus then writes the resolved single-chip list back
  // to localStorage so a refresh keeps the BTC fallback intact.
  await expect(page.getByTestId('watchlist-chip-BTC')).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-ETH')).toHaveCount(0);
  const stored = await page.evaluate(() => window.localStorage.getItem('edgex-dashboard:watchlist:v1'));
  expect(stored).not.toBeNull();
  expect(JSON.parse(stored as string)).toEqual(['BTC']);
});

test('localStorage seeded before mount populates the watchlist when URL is bare', async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem('edgex-dashboard:watchlist:v1', JSON.stringify(['BTC', 'ETH']));
  });
  await page.goto('/');
  // Post-mount reconciliation should pull BTC + ETH from storage and
  // backfill the URL via replaceState.
  await expect(page.getByTestId('watchlist-chip-BTC')).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-ETH')).toBeVisible();
  await expect(page).toHaveURL(/watchlist=BTC%2CETH|watchlist=BTC,ETH/);
});

test('URL parameter wins over localStorage on direct navigation', async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem('edgex-dashboard:watchlist:v1', JSON.stringify(['BTC']));
  });
  await page.goto('/?watchlist=ETH,SOL');
  // The URL list (ETH,SOL) takes precedence over the storage list (BTC).
  await expect(page.getByTestId('watchlist-chip-ETH')).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-SOL')).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-BTC')).toHaveCount(0);
});

test('funding KPI panel shows edgeX 8h rate with sign and tooltip', async ({ page }) => {
  await page.goto('/');
  // The KPI card sits next to spread / share and renders the 4dp percent
  // figure. Fixtures (CoinGecko percent units): edgex_funding_rate_8h
  // = 0.0050 → "+0.0050%".
  const fundingCard = page.locator('section.panel').filter({ hasText: 'edgeX 资金费率' });
  await expect(fundingCard).toBeVisible();
  await expect(fundingCard).toContainText('+0.0050%');
  // vs-median delta: 0.0050 - 0.0090 = -0.0040 → "-0.0040%".
  await expect(fundingCard).toContainText('-0.0040%');
});

test('Liquidity detail table includes a 资金费率 (8h) column with per-row values', async ({ page }) => {
  await page.goto('/');
  const detail = page.locator('section.panel').filter({ hasText: '深度明细 · 平台 × 档位 (USD)' });
  await expect(detail).toBeVisible();
  await expect(detail).toContainText('资金费率 (8h)');
  // edgeX row: rate_8h 0.0050 → +0.0050%
  await expect(detail.locator('tbody')).toContainText('+0.0050%');
  // bingx row: status=unsupported → muted '—'
  await expect(detail.locator('tbody')).toContainText('—');
});

test('Quality tab renders the span-24 funding panel with median footnote', async ({ page }) => {
  await page.goto('/?tab=quality');
  const fundingPanel = page.locator('section.panel.span-24').filter({ hasText: '资金费率 跨平台对比' });
  await expect(fundingPanel).toBeVisible();
  // Footer note must reference the competitor median value when status
  // is complete (3 samples in our fixture).
  await expect(fundingPanel).toContainText('竞品中位数');
  await expect(fundingPanel).toContainText('3 样本');
});

test('Quality detail table includes the same 资金费率 column', async ({ page }) => {
  await page.goto('/?tab=quality');
  const detail = page.locator('section.panel').filter({ hasText: '盘口质量明细' });
  await expect(detail).toBeVisible();
  await expect(detail).toContainText('资金费率 (8h)');
  await expect(detail.locator('tbody')).toContainText('+0.0050%');
});

test('watchlist cards render one section per symbol with distinct depth values', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH,SOL,GOLD');
  // Four cards.
  for (const sym of ['BTC', 'ETH', 'SOL', 'GOLD']) {
    await expect(page.getByTestId(`watchlist-card-${sym}`)).toBeVisible();
  }
  // BTC scale=1.0 → 2.30M, ETH scale=0.7 → 1.61M (rendered as 1.61M).
  const btc = page.getByTestId('watchlist-card-BTC');
  const eth = page.getByTestId('watchlist-card-ETH');
  // Both cards expose the funding row (edgeX 0.0050% identical across
  // symbols in fixtures); the depth row MUST differ.
  await expect(btc).toContainText('2.30M');
  await expect(eth).not.toContainText('2.30M');
  await expect(eth).toContainText('1.61M');
});

test('each watchlist card renders a mini depth chart with a BID/ASK/合计 toggle and chart aria-label updates on click', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH,SOL');
  for (const sym of ['BTC', 'ETH', 'SOL']) {
    // Chart container present per card.
    await expect(page.getByTestId(`watchlist-card-chart-${sym}`)).toBeVisible();
    // Default side is 合计.
    const canvas = page.getByTestId(`watchlist-card-chart-${sym}`).locator('canvas');
    await expect(canvas).toHaveAttribute('aria-label', new RegExp(`${sym}.*合计深度曲线 BID \\+ ASK`));
    // Three side toggle buttons present.
    await expect(page.getByTestId(`watchlist-card-side-${sym}-total_usd`)).toBeVisible();
    await expect(page.getByTestId(`watchlist-card-side-${sym}-bid_usd`)).toBeVisible();
    await expect(page.getByTestId(`watchlist-card-side-${sym}-ask_usd`)).toBeVisible();
  }
  // Switch BTC to BID — aria-label flips, other cards stay on 合计.
  await page.getByTestId('watchlist-card-side-BTC-bid_usd').click();
  await expect(page.getByTestId('watchlist-card-chart-BTC').locator('canvas')).toHaveAttribute('aria-label', /BTC.*买盘深度曲线 BID/);
  await expect(page.getByTestId('watchlist-card-chart-ETH').locator('canvas')).toHaveAttribute('aria-label', /ETH.*合计深度曲线 BID \+ ASK/);
  // Switch BTC again to ASK.
  await page.getByTestId('watchlist-card-side-BTC-ask_usd').click();
  await expect(page.getByTestId('watchlist-card-chart-BTC').locator('canvas')).toHaveAttribute('aria-label', /BTC.*卖盘深度曲线 ASK/);
});

test('clicking 查看明细 on a card collapses the watchlist to that single symbol and reveals the V1 detail view', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH,SOL');
  // Multi-symbol → card grid; depth detail table must be hidden.
  await expect(page.locator('text=深度明细 · 平台 × 档位 (USD)')).toHaveCount(0);
  // Click ETH's 查看明细 button.
  await page.getByTestId('watchlist-card-expand-ETH').click();
  // Watchlist now contains only ETH; the chip toolbar still shows ETH
  // and the V1 single-symbol detail view (depth detail table) is back.
  await expect(page.getByTestId('watchlist-chip-ETH')).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-BTC')).toHaveCount(0);
  await expect(page.locator('text=深度明细 · 平台 × 档位 (USD)')).toBeVisible();
  // URL and localStorage reflect the collapsed state via the
  // side-effect bus in DashboardClient.
  await expect(page).toHaveURL(/watchlist=ETH/);
  const stored = await page.evaluate(() => window.localStorage.getItem('edgex-dashboard:watchlist:v1'));
  expect(JSON.parse(stored as string)).toEqual(['ETH']);
});

test('Quality tab switches to QualityCard grid when watchlist has >1 symbol and shares the watchlist with Liquidity tab', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH,SOL&tab=quality');
  // The V1 quality detail (3 BarCharts + 盘口质量明细 table) is hidden
  // in card mode.
  await expect(page.locator('text=盘口质量明细')).toHaveCount(0);
  // Three QualityCards rendered, with the same testid namespace used
  // by the Liquidity-side cards' chip toolbar (so toggling tabs
  // preserves the toolbar identity for the operator).
  for (const sym of ['BTC', 'ETH', 'SOL']) {
    await expect(page.getByTestId(`quality-card-${sym}`)).toBeVisible();
    await expect(page.getByTestId(`quality-card-expand-${sym}`)).toBeVisible();
    await expect(page.getByTestId(`quality-card-chart-${sym}`)).toBeVisible();
    await expect(page.getByTestId(`quality-card-verdict-${sym}`)).toBeVisible();
  }
  // Same WatchlistToolbar present in quality mode.
  await expect(page.getByTestId('watchlist-toolbar')).toBeVisible();
  // The KPI rows reflect quality fields — spread (10min), 滑点 @ bucket, 资金费率.
  const btc = page.getByTestId('quality-card-BTC');
  await expect(btc).toContainText('edgeX spread (10min 均值)');
  await expect(btc).toContainText('滑点 @ 100K USD');
  await expect(btc).toContainText('vs 竞品中位数滑点');
  await expect(btc).toContainText('资金费率');
});

test('Quality bucket pill bar at the top of the card grid changes the @ bucket label across all cards', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH&tab=quality&bucket=100000');
  const btc = page.getByTestId('quality-card-BTC');
  const eth = page.getByTestId('quality-card-ETH');
  await expect(btc).toContainText('滑点 @ 100K USD');
  await expect(eth).toContainText('滑点 @ 100K USD');
  // Click the 1M USD pill in the global bucket bar.
  const oneMPill = page.locator('a.pill').filter({ hasText: '1M USD' }).first();
  await oneMPill.click();
  await expect(btc).toContainText('滑点 @ 1M USD');
  await expect(eth).toContainText('滑点 @ 1M USD');
});

test('clicking 查看明细 on a QualityCard collapses to single-symbol V1 quality view', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH&tab=quality');
  await page.getByTestId('quality-card-expand-ETH').click();
  // Single chip, V1 quality detail table re-appears.
  await expect(page.getByTestId('watchlist-chip-ETH')).toBeVisible();
  await expect(page.getByTestId('watchlist-chip-BTC')).toHaveCount(0);
  await expect(page.locator('section.panel').filter({ hasText: '盘口质量明细' })).toBeVisible();
  await expect(page).toHaveURL(/watchlist=ETH/);
});

test('add button disables at MAX_WATCHLIST cap (10)', async ({ page }) => {
  // We only have 4 fixture symbols; build a URL with the 4 available
  // symbols duplicated up to 10 entries — dedupeAndCap collapses dupes
  // so the trigger should NOT disable. Then add via addInitScript to
  // seed exactly 10 unique entries to assert the disable behaviour.
  await page.addInitScript(() => {
    window.localStorage.setItem(
      'edgex-dashboard:watchlist:v1',
      JSON.stringify(['BTC', 'ETH', 'SOL', 'GOLD', 'A', 'B', 'C', 'D', 'E', 'F']),
    );
  });
  await page.goto('/');
  await expect(page.getByTestId('watchlist-add-trigger')).toBeDisabled();
});
