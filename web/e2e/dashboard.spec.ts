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
