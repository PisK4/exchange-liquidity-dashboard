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
//   B. Imbalance cell paints .sign-positive / .sign-negative based on
//      the sign of imbalance_pct. Color encodes direction (BID-heavy
//      vs ASK-heavy) only; magnitude / health is still read from the
//      number itself and the |x|>30% threshold in the panel-sub.

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

test('Imbalance cell paints sign-positive (red) for edgeX BID-heavy row', async ({ page }) => {
  await page.goto('/?tab=quality');
  const block = page.getByTestId('quality-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '盘口质量明细' }).first();
  // Fixture: edgeX imbalance_pct = +6.25 (BID-heavy) → sign-positive (red).
  // td.num indices in the quality detail table:
  //   0=Spread(bp) 1=Mid 2=Imbalance 3..6=滑点 50K/100K/500K/1M
  const edgeRow = detail.locator('tbody tr').filter({ hasText: 'edgeX' });
  const imbalanceCell = edgeRow.locator('td.num').nth(2);
  await expect(imbalanceCell).toContainText('+6.25%');
  await expect(imbalanceCell).toHaveClass(/sign-positive/);
});

test('Imbalance cell paints sign-negative (teal) for ASK-heavy competitor row', async ({ page }) => {
  await page.goto('/?tab=quality');
  const block = page.getByTestId('quality-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '盘口质量明细' }).first();
  // Fixture: every non-edgeX row has imbalance_pct = -3.5 → sign-negative.
  const binanceRow = detail.locator('tbody tr').filter({ hasText: 'binance' });
  const imbalanceCell = binanceRow.locator('td.num').nth(2);
  await expect(imbalanceCell).toContainText('-3.50%');
  await expect(imbalanceCell).toHaveClass(/sign-negative/);
});

test('Imbalance panel-sub explains color is direction only, not optimality', async ({ page }) => {
  await page.goto('/?tab=quality');
  const block = page.getByTestId('quality-block-BTC');
  const detail = block.locator('section.panel').filter({ hasText: '盘口质量明细' }).first();
  // The sub-text must make the "color = direction, magnitude = health"
  // distinction explicit so operators don't read red as "this row is
  // unhealthy" (a healthy +5% BID-heavy row is still red).
  await expect(detail).toContainText('颜色仅表方向');
  await expect(detail).toContainText('30%');
});
