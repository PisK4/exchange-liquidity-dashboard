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
//   5. Cross-platform BarChart sort order = ascending 8h equivalent.
//   6. Detail table exposes the per-row columns the operator needs
//      (native rate, period, 8h, vs median, rank, status).
//   7. Unsupported platforms surface explicitly (StatusBadge) instead
//      of as fabricated zero — preserving funding.go's "no defaults"
//      contract at the UI boundary.
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

test('cross-platform BarChart sorts ascending on 8h equivalent and shows dual-format labels', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  // The cross-platform chart panel has panel-title "跨平台资金费率对比"
  // and contains the BarChart rows.
  const chartPanel = block.locator('section.panel').filter({ hasText: '跨平台资金费率对比' }).first();
  await expect(chartPanel).toBeVisible();
  // Each bar row's <b> tag carries the formatted label. Capture them in
  // DOM order and verify the order matches ascending rate_8h:
  //   edgeX (0.0050) → bybit (0.0060) → binance (0.0090) → okx (0.0120)
  // → bingx (unsupported, last via Infinity sort fallback).
  const labels = await chartPanel.locator('.bar-row b').allInnerTexts();
  expect(labels.length).toBeGreaterThanOrEqual(4);
  // edgeX row carries the dual-format label.
  expect(labels[0]).toContain('+0.0025% / 4h');
  expect(labels[0]).toContain('8h ≈ +0.0050%');
  // bybit second, with its 8h-only label since native period is 8h.
  expect(labels[1]).toContain('+0.0060% / 8h');
  // binance third.
  expect(labels[2]).toContain('+0.0090% / 8h');
  // okx fourth.
  expect(labels[3]).toContain('+0.0120% / 8h');
  // Footnote explains the bar-length convention so operators don't
  // misread the chart.
  await expect(chartPanel).toContainText('条形长度 = 8h 当量');
});

test('detail table exposes native / period / 8h / vs median / rank / timestamp / status columns', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '资金费率明细' }).first();
  await expect(detail).toBeVisible();
  for (const header of ['平台', '原生费率', '原生周期', '8h 当量', 'vs 中位数 (8h)', '排名', '数据源时间戳', '状态']) {
    await expect(detail).toContainText(header);
  }
  // edgeX row: native rate 0.0025 (no period suffix in this column),
  // period 4h, 8h equivalent +0.0050%, status badge "complete".
  await expect(detail.locator('tbody')).toContainText('+0.0025%');
  await expect(detail.locator('tbody')).toContainText('4h');
  await expect(detail.locator('tbody')).toContainText('+0.0050%');
});

test('unsupported platform (bingx) renders explicit em-dash and unsupported status badge', async ({ page }) => {
  await page.goto('/?tab=funding');
  const block = page.getByTestId('funding-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '资金费率明细' }).first();
  // The bingx row exists but its rate cells must be '—' and the
  // status column must surface the StatusBadge.
  const bingxRow = detail.locator('tbody tr').filter({ hasText: 'bingx' });
  await expect(bingxRow).toBeVisible();
  await expect(bingxRow).toContainText('—');
  await expect(bingxRow).toContainText('unsupported');
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
