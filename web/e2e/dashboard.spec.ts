import { expect, test } from '@playwright/test';

for (const path of ['/liquidity', '/quality', '/share', '/top30']) {
  test(`${path} renders without mock wording`, async ({ page }) => {
    const errors: string[] = [];
    page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });
    await page.goto(path);
    await expect(page.locator('body')).toContainText('Dashboard');
    await expect(page.locator('body')).not.toContainText(/mock data/i);
    expect(errors).toEqual([]);
  });
}
