import type { Plugin } from 'chart.js';
import { moneyAuto } from '@/lib/api/client';

export const edgeXValueLabelPlugin: Plugin<'line'> = {
  id: 'edgex-value-label',
  afterDatasetsDraw(chart) {
    const { ctx } = chart;
    const dataset = chart.data.datasets.find(d => d.label === 'edgeX');
    if (!dataset) return;
    const dsIndex = chart.data.datasets.indexOf(dataset);
    const meta = chart.getDatasetMeta(dsIndex);
    if (!meta || meta.hidden) return;

    ctx.save();
    ctx.font = '600 11px ui-sans-serif, system-ui, -apple-system, sans-serif';
    ctx.fillStyle = '#dfe6dc';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'bottom';
    ctx.shadowColor = 'rgba(0, 0, 0, 0.7)';
    ctx.shadowBlur = 4;
    meta.data.forEach((point, idx) => {
      const value = dataset.data[idx];
      if (typeof value !== 'number') return;
      const x = point.x;
      const y = point.y - 12;
      ctx.fillText(moneyAuto(value), x, y);
    });
    ctx.restore();
  },
};
