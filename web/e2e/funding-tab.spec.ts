import { expect, test } from '@playwright/test';
import { routeWatchlistAPI } from './fixtures-watchlist';

// funding-tab.spec.ts covers the dedicated 资金费率 Tab introduced
// between 流动性监控 and 盘口质量. The suite locks in:
//
//   1. Tab placement / hyperlink — operators expect the new tab to sit
//      directly between Liquidity and Quality, so a regression that
//      reorders the tuple in dashboard-shell.tsx must be caught here.
//   2. FundingBlock is rendered per watchlist symbol, mirroring the
//      v2 stacked-block layout used by SymbolBlock and QualityBlock.
//   3. Dual-format labels — primary number is the native rate (e.g.
//      "+0.0025% / 4h" for edgeX), 8h-equivalent appears as a subline.
//      Regressions that revert to "8h only" or "native only" would
//      defeat the whole purpose of this surface.
//   4. Median + delta cards correctly surface as 8h-only with sample
//      counts, because a cross-period median has no native counterpart.
//   5. The 跨平台 BarChart was removed in iteration round-2. Earlier
//      cuts plotted absolute 8h equivalents and then Δ-to-median;
//      both struggled at the ~±0.005% magnitudes typical of funding
//      rates. The three KPI cards plus the detail table now carry the
//      full story — this spec includes a negative assertion that no
//      cross-platform comparison chart panel resurfaces.
//   6. Detail table exposes the slim column set operators asked for
//      after iteration round-1: 平台, 原生费率 (with period folded
//      inline as "+0.0025% / 4h"), 8h 当量, vs 中位数 (8h), 排名 —
//      dropping the prior 原生周期 / 数据源时间戳 / 状态 columns.
//   7. Unsupported platforms surface explicitly with em-dash cells
//      and a real rank ('—') instead of fabricated zero — preserving
//      funding.go's "no defaults" contract at the UI boundary.
//   8. Watchlist multi-symbol fan-out — adding ETH to the chip list
//      stacks a second FundingBlock with its own KPI / chart / table.

test.beforeEach(async ({ page }) => {
  await routeWatchlistAPI(page);
});

test('top nav surfaces the 资金费率 tab between 流动性监控 and 盘口质量', async ({ page }) => {
  await page.goto('/');
  // Capture the tabs in DOM order and assert the funding entry sits
  // between monitor and quality. We use a regex on innerText rather
  // than testid because the tab links share the same .tab class and
  // their identity is defined by their position in the tuple.
  const labels = await page.locator('nav.tabs a.tab').allInnerTexts();
  const trimmed = labels.map(l => l.trim());
  expect(trimmed).toEqual(['流动性监控', '资金费率', '盘口质量', '市占率', 'Top30 成交量']);
});

test('clicking the 资金费率 tab navigates to ?tab=funding and renders a FundingBlock', async ({ page }) => {
  await page.goto('/');
  await page.locator('nav.tabs a.tab').filter({ hasText: '资金费率' }).click();
  await expect(page).toHaveURL(/tab=funding/);
  // Default fallback: single BTC chip → exactly one FundingBlock.
  await expect(page.getByTestId('funding-block-BTC')).toBeVisible();
});

test('/funding redirect lands on the funding tab', async ({ page }) => {
  await page.goto('/funding');
  await expect(page).toHaveURL(/tab=funding/);
  await expect(page.getByTestId('funding-block-BTC')).toBeVisible();
});

test('edgeX KPI card shows native 4h rate as primary value with 8h subline', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  // Locate the edgeX KPI card (panel-title "edgeX 资金费率") within the
  // current block — we use `.locator` chaining instead of role queries
  // because the section markup uses panel-title spans, not headings.
  const edgexCard = block.locator('section.panel').filter({ hasText: 'edgeX 资金费率' }).first();
  await expect(edgexCard).toBeVisible();
  // Fixture: rate_native=0.0025 with period_hours=4 → "+0.0025% / 4h".
  await expect(edgexCard).toContainText('+0.0025% / 4h');
  // 8h equivalent (0.0050) lives in the subline beneath the big number.
  await expect(edgexCard).toContainText('8h 当量 +0.0050%');
});

test('竞品中位数 card shows 8h-only with sample count and explanatory subline', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const medianCard = block.locator('section.panel').filter({ hasText: '竞品中位数 (8h)' }).first();
  await expect(medianCard).toBeVisible();
  // Fixture median = 0.0090, 3 samples.
  await expect(medianCard).toContainText('+0.0090%');
  await expect(medianCard).toContainText('3/9');
  // Subline reinforces why no native counterpart is shown.
  await expect(medianCard).toContainText('跨周期混合，仅能以 8h 当量表达');
});

test('vs 竞品中位数 delta card shows signed 8h delta with directional glyph', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const deltaCard = block.locator('section.panel').filter({ hasText: 'edgeX vs 竞品中位数' }).first();
  await expect(deltaCard).toBeVisible();
  // delta = 0.0050 - 0.0090 = -0.0040 → "-0.0040%".
  await expect(deltaCard).toContainText('-0.0040%');
});

test('no cross-platform BarChart panel resurfaces between KPI cards and detail table', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  // Iteration round-2 removed both the absolute-8h and Δ-to-median
  // chart panels. Guard against either form coming back by asserting
  // the historical panel titles are absent.
  await expect(block.locator('text=跨平台资金费率对比')).toHaveCount(0);
  await expect(block.locator('text=对竞品中位数偏离')).toHaveCount(0);
  // No BarChart instance should be embedded inside the funding block.
  await expect(block.locator('.bar-row')).toHaveCount(0);
});

test('detail table exposes slim 5-column shape (平台 / 原生费率 / 8h / vs 中位数 / 排名)', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '资金费率明细' }).first();
  await expect(detail).toBeVisible();
  // Expected headers (the slim set chosen after iteration round-1).
  for (const header of ['平台', '原生费率', '8h 当量', 'vs 中位数 (8h)', '排名']) {
    await expect(detail).toContainText(header);
  }
  // The dropped headers must NOT appear.
  for (const dropped of ['原生周期', '数据源时间戳', '状态']) {
    await expect(detail.locator('thead')).not.toContainText(dropped);
  }
  // edgeX row: native rate cell now has period folded inline as
  // "+0.0025% / 4h" instead of split across two columns.
  const edgeRow = detail.locator('tbody tr').filter({ hasText: 'edgeX' });
  await expect(edgeRow).toContainText('+0.0025% / 4h');
  await expect(edgeRow).toContainText('+0.0050%');
  // Rank populated by the backend enrichFundingRankRows helper —
  // edgeX is the cheapest of the 4 complete rows → rank 1.
  await expect(edgeRow.locator('td.num').last()).toHaveText('1');
});

test('detail table renders the full ascending rank ladder for complete rows', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '资金费率明细' }).first();
  // Ranks: edgeX=1, bybit=2, binance=3, okx=4.
  const ranks: Record<string, string> = { edgeX: '1', bybit: '2', binance: '3', okx: '4' };
  for (const [platform, rank] of Object.entries(ranks)) {
    const row = detail.locator('tbody tr').filter({ hasText: platform });
    await expect(row.locator('td.num').last()).toHaveText(rank);
  }
});

test('1h-period venue (hyperliquid) bumps native rate precision to 6dp instead of collapsing to +0.0000%', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '资金费率明细' }).first();
  // Hyperliquid fixture: rate_native=0.000025 with period_hours=1.
  // At the dashboard's default 4dp precision this would collapse to
  // "+0.0000% / 1h" — looking like missing data. The formatter must
  // detect the collapse and re-render with 6dp so the actual
  // magnitude is visible to the operator.
  const hlRow = detail.locator('tbody tr').filter({ hasText: 'hyperliquid' });
  await expect(hlRow).toBeVisible();
  await expect(hlRow).toContainText('+0.000025% / 1h');
  // Sanity: the misleading 4dp collapsed form must NOT appear in the row.
  await expect(hlRow).not.toContainText('+0.0000% / 1h');
});

test('unsupported platform (bingx) renders em-dash in every numeric cell including 排名', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '资金费率明细' }).first();
  // bingx fixture is unsupported → backend cannot assign a rank, the
  // table cell must surface '—' rather than a fabricated zero.
  const bingxRow = detail.locator('tbody tr').filter({ hasText: 'bingx' });
  await expect(bingxRow).toBeVisible();
  await expect(bingxRow).toContainText('—');
  await expect(bingxRow.locator('td.num').last()).toHaveText('—');
});

test('multi-symbol watchlist stacks one FundingBlock per chip', async ({ page }) => {
  await page.goto('/?tab=funding&watchlist=BTC,ETH,SOL');
  for (const sym of ['BTC', 'ETH', 'SOL']) {
    await expect(page.getByTestId(`funding-block-${sym}`)).toBeVisible();
  }
  // Each block independently renders the edgeX KPI card with the same
  // native rate (fixture funding values are identical across symbols,
  // only depth scales differ) — proves the fan-out is wired per block.
  for (const sym of ['BTC', 'ETH', 'SOL']) {
    const block = page.getByTestId(`funding-block-${sym}`);
    await expect(block.locator('section.panel').filter({ hasText: 'edgeX 资金费率' })).toContainText('+0.0025% / 4h');
  }
});

test('watchlist toolbar present on funding tab and adding a chip stacks another block', async ({ page }) => {
  await page.goto('/?tab=funding&watchlist=BTC');
  await expect(page.getByTestId('watchlist-toolbar')).toBeVisible();
  // Single chip → single block.
  await expect(page.getByTestId('funding-block-BTC')).toBeVisible();
  await expect(page.getByTestId('funding-block-ETH')).toHaveCount(0);
  // Add ETH via the dropdown.
  await page.getByTestId('watchlist-add-trigger').click();
  await page.getByTestId('watchlist-add-option-ETH').click();
  await expect(page.getByTestId('funding-block-ETH')).toBeVisible();
  // URL is replaceState-updated with the merged list.
  await expect(page).toHaveURL(/watchlist=BTC%2CETH|watchlist=BTC,ETH/);
});
