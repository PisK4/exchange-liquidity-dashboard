import { expect, test } from '@playwright/test';

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
