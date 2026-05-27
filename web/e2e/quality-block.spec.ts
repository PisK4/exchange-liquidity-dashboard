import { expect, test } from '@playwright/test';
import { routeWatchlistAPI } from './fixtures-watchlist';

// quality-block.spec.ts covers visual / behavioural invariants of the
// 盘口质量明细 sub-table inside QualityBlock. Until now the v4
// QualityBlock surface was only exercised indirectly through dashboard
// and watchlist specs; this file isolates the detail-table layer where
// recent visual-encoding changes land.
//
// What this suite locks in (built up across commits A→B→C):
//   A. edgeX row carries the .r-edgex highlight class so operators
//      can locate the monitored venue at a glance — same convention
//      used by the funding tab's detail table.

test.beforeEach(async ({ page }) => {
  await routeWatchlistAPI(page);
});

test('edgeX row in 盘口质量明细 carries the .r-edgex highlight class', async ({ page }) => {
  await page.goto('/?tab=quality');
  const block = page.getByTestId('quality-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '盘口质量明细' }).first();
  await expect(detail).toBeVisible();
  const edgeRow = detail.locator('tbody tr').filter({ hasText: 'edgeX' });
  await expect(edgeRow).toHaveClass(/r-edgex/);
  // Non-edgeX rows must NOT pick up the class — otherwise the highlight
  // degenerates into a row-stripe and loses its "this is the venue you
  // care about" signal.
  const binanceRow = detail.locator('tbody tr').filter({ hasText: 'binance' });
  await expect(binanceRow).not.toHaveClass(/r-edgex/);
});
