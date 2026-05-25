import { expect, test } from '@playwright/test';

// docker-smoke.spec.ts runs against the LIVE local Docker stack rather
// than the fixture mock. It intentionally does NOT call routeDashboardAPI
// so every request hits http://127.0.0.1:8080 (the deploy-backend-1
// container) and exercises the real funding-rate normalization, the
// real Top30 / share / quality reducers, and the real MySQL-backed
// snapshot history.
//
// Run with: PLAYWRIGHT_BASE_URL=http://127.0.0.1:3001 npx playwright
// test e2e/docker-smoke.spec.ts --reporter=list
//
// These assertions deliberately stay loose on numeric values (live data
// changes every poll cycle) and tight on STRUCTURE — column headers,
// chip toolbar, KPI panel presence, status badges — so a unit-error or
// missing-route regression is caught without depending on a frozen
// price moment.

test.describe.configure({ mode: 'serial' });

test('default visit renders Liquidity tab with watchlist toolbar and BTC chip', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByTestId('watchlist-toolbar')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('watchlist-chip-BTC')).toBeVisible();
  // Funding KPI panel sits in the headline row.
  await expect(page.locator('section.panel').filter({ hasText: 'edgeX 资金费率' })).toBeVisible();
});

test('funding values render in sane range (|rate| < 0.5% per 8h) with 4dp format', async ({ page }) => {
  await page.goto('/');
  const kpi = page.locator('section.panel').filter({ hasText: 'edgeX 资金费率' });
  await expect(kpi).toBeVisible({ timeout: 15_000 });
  const text = await kpi.locator('.big-number').first().innerText();
  // Match either '—' (no data) or a signed 4dp percent.
  const m = text.match(/^([+-])(\d+\.\d{4})%$/);
  if (m) {
    const value = parseFloat(m[2]);
    // Sanity guard: with the fix, real BTC readings are < 0.5% per 8h.
    // The first cut multiplied by 100 and produced values around 1%
    // which would fail this bound.
    expect(value).toBeLessThan(0.5);
  } else {
    expect(text).toBe('—');
  }
});

test('Liquidity detail table includes 资金费率 (8h) column with at least one signed-percent value', async ({ page }) => {
  await page.goto('/');
  const detail = page.locator('section.panel').filter({ hasText: '深度明细 · 平台 × 档位 (USD)' });
  await expect(detail).toBeVisible({ timeout: 15_000 });
  await expect(detail).toContainText('资金费率 (8h)');
  // Live data: at least one of the 10 platforms (edgeX or competitors)
  // must produce a signed 4dp percent — the rendered table cannot be
  // all '—'.
  const bodyText = await detail.locator('tbody').innerText();
  expect(bodyText).toMatch(/[+-]\d+\.\d{4}%/);
});

test('Quality tab shows the span-24 funding cross-platform chart', async ({ page }) => {
  await page.goto('/?tab=quality');
  const panel = page.locator('section.panel.span-24').filter({ hasText: '资金费率 跨平台对比' });
  await expect(panel).toBeVisible({ timeout: 15_000 });
});

test('multi-symbol URL switches to card grid and adds a 查看明细 expand button per card', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH,SOL');
  for (const sym of ['BTC', 'ETH', 'SOL']) {
    await expect(page.getByTestId(`watchlist-card-${sym}`)).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId(`watchlist-card-expand-${sym}`)).toBeVisible();
  }
  // Depth detail table must be hidden in card mode.
  await expect(page.locator('text=深度明细 · 平台 × 档位 (USD)')).toHaveCount(0);
});

test('each card embeds a mini depth chart that toggles between BID, ASK and 合计 (live data)', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH');
  for (const sym of ['BTC', 'ETH']) {
    await expect(page.getByTestId(`watchlist-card-chart-${sym}`)).toBeVisible({ timeout: 15_000 });
    const canvas = page.getByTestId(`watchlist-card-chart-${sym}`).locator('canvas');
    await expect(canvas).toHaveAttribute('aria-label', new RegExp(`${sym}.*合计深度曲线 BID \\+ ASK`));
  }
  // Per-card side toggle is independent: clicking BTC's BID must not
  // change ETH's selection (this catches a future regression where a
  // shared store accidentally merges the per-card toggle state).
  await page.getByTestId('watchlist-card-side-BTC-bid_usd').click();
  await expect(page.getByTestId('watchlist-card-chart-BTC').locator('canvas')).toHaveAttribute('aria-label', /BTC.*买盘深度曲线 BID/);
  await expect(page.getByTestId('watchlist-card-chart-ETH').locator('canvas')).toHaveAttribute('aria-label', /ETH.*合计深度曲线 BID \+ ASK/);
});

test('clicking 查看明细 collapses to single-symbol view and persists', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH');
  await page.getByTestId('watchlist-card-expand-ETH').click();
  // Single chip, V1 view restored.
  await expect(page.getByTestId('watchlist-chip-ETH')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('watchlist-chip-BTC')).toHaveCount(0);
  await expect(page.locator('section.panel').filter({ hasText: '深度明细 · 平台 × 档位 (USD)' })).toBeVisible();
  await expect(page).toHaveURL(/watchlist=ETH/);
});

test('Quality tab honours the watchlist and renders a QualityCard grid (live data)', async ({ page }) => {
  await page.goto('/?watchlist=BTC,ETH&tab=quality');
  for (const sym of ['BTC', 'ETH']) {
    await expect(page.getByTestId(`quality-card-${sym}`)).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId(`quality-card-chart-${sym}`)).toBeVisible();
    await expect(page.getByTestId(`quality-card-verdict-${sym}`)).toBeVisible();
  }
  // Global bucket bar present; the V1 quality detail table must be
  // hidden in card mode.
  await expect(page.locator('text=盘口质量明细')).toHaveCount(0);
  // KPI rows reflect the spread/imbalance/滑点 family.
  const btc = page.getByTestId('quality-card-BTC');
  await expect(btc).toContainText('edgeX spread');
  await expect(btc).toContainText('滑点 @');
});

test('Top30 / Share tabs still render against live backend', async ({ page }) => {
  await page.goto('/?tab=share');
  // The Share tab includes a panel whose title contains '市占率明细表'.
  // 'edgeX 平台总交易量市占率' alone matches both the KPI label AND the
  // detail-table title, so we anchor on the more specific suffix.
  await expect(page.locator('text=edgeX 平台总交易量市占率明细表')).toBeVisible({ timeout: 15_000 });

  await page.goto('/?tab=top30');
  // The Top30 tab uses a different layout; assert one of its known
  // strings rather than depending on Chinese label exact wording.
  await expect(page.locator('text=各平台 Top30')).toBeVisible({ timeout: 15_000 });
});
