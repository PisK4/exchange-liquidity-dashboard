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

test('depth detail table shows depth values without per-cell source labels', async ({ page }) => {
  await page.goto('/');

  const detailPanel = page.locator('section.panel').filter({ hasText: '深度明细 · 平台 × 档位 (M USD)' });
  await expect(detailPanel).toBeVisible();
  await expect(detailPanel.locator('tbody')).toContainText(/(\d|—)/);
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
