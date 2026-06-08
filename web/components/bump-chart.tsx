'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

// BumpChart 把 Y 轴从"USD"切换为"排名"：
//   - 每个 tier 内对所有平台按 USD 深度降序，得到 rank ∈ [1, N]
//   - Y 轴反向（1 在顶部），edgeX 哪怕排第 9 也是图上明显可见的一条线，
//     不再"贴 X 轴"
//   - 节点旁直接打 USD，保留绝对量的可读性
//   - edgeX 用 5px hero 实线 + 6px 白描边圆点，竞品 1.5px @ 0.42 alpha
//
// 注意：bump chart 牺牲了"绝对差距"的图形比例（rank 之间间距是均匀的），
//   它回答的是"谁在哪个档位是冠军 / edgeX 在多少名"。配合数值标签，
//   既能读出名次也能读出 USD。
type BumpChartProps = {
  ariaLabel: string;
  tierLabels: string[];
  displayLabels: string[];
  series: Series[];
};

type RankedSeries = {
  label: string;
  colorKey?: string;
  isSelf?: boolean;
  ranks: Array<number | null>;
  values: Array<number | undefined>;
};

function buildRanks(tierLabels: string[], series: Series[]): RankedSeries[] {
  const N = series.length;
  const ranksByLabel = new Map<string, Array<number | null>>();
  series.forEach(s => ranksByLabel.set(s.label, Array(tierLabels.length).fill(null)));

  tierLabels.forEach((_tier, tierIdx) => {
    const rows = series
      .map(s => ({ label: s.label, value: s.values[tierIdx] }))
      .filter((r): r is { label: string; value: number } => typeof r.value === 'number' && Number.isFinite(r.value));
    rows.sort((a, b) => b.value - a.value);
    rows.forEach((row, rank) => {
      const arr = ranksByLabel.get(row.label);
      if (arr) arr[tierIdx] = rank + 1;
    });
    series.forEach(s => {
      const v = s.values[tierIdx];
      if (!(typeof v === 'number' && Number.isFinite(v))) {
        const arr = ranksByLabel.get(s.label);
        if (arr) arr[tierIdx] = N;
      }
    });
  });

  return series.map(s => ({
    label: s.label,
    colorKey: s.colorKey,
    isSelf: s.isSelf,
    ranks: ranksByLabel.get(s.label) ?? Array(tierLabels.length).fill(null),
    values: s.values,
  }));
}

const valueLabelPlugin = {
  id: 'bumpValueLabel',
  afterDatasetsDraw(chart: Chart) {
    const { ctx } = chart;
    ctx.save();
    ctx.font = '10px ui-sans-serif, system-ui, sans-serif';
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'left';
    chart.data.datasets.forEach((dataset, datasetIdx) => {
      const meta = chart.getDatasetMeta(datasetIdx);
      const datasetWithMeta = dataset as unknown as { usdValues?: Array<number | undefined>; isSelf?: boolean };
      const usdValues = datasetWithMeta.usdValues ?? [];
      meta.data.forEach((point, idx) => {
        if (datasetWithMeta.isSelf !== true) return;
        const usd = usdValues[idx];
        if (typeof usd !== 'number') return;
        const { x, y } = point.tooltipPosition(false);
        ctx.fillStyle = '#ffffff';
        ctx.fillText(`  ${moneyAuto(usd)}`, x + 4, y);
      });
    });
    ctx.restore();
  },
};

export function BumpChart({ ariaLabel, tierLabels, displayLabels, series }: BumpChartProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    chartRef.current?.destroy();

    const ranked = buildRanks(tierLabels, series);
    const N = series.length;

    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels: displayLabels,
        datasets: ranked.map(item => {
          const color = colorFor(item.colorKey ?? item.label);
          const isSelf = item.isSelf === true;
          return {
            label: item.label,
            data: item.ranks,
            usdValues: item.values,
            isSelf,
            borderColor: isSelf ? color : rgba(color, 0.42),
            backgroundColor: rgba(color, isSelf ? 0.35 : 0.12),
            borderWidth: isSelf ? 5 : 1.5,
            pointRadius: isSelf ? 6 : 4,
            pointHoverRadius: isSelf ? 7 : 5,
            pointBackgroundColor: color,
            pointBorderColor: isSelf ? '#ffffff' : '#0e1318',
            pointBorderWidth: isSelf ? 2 : 1,
            tension: 0.35,
            spanGaps: true,
            fill: false,
          } as unknown as ChartConfiguration<'line', Array<number | null>, string>['data']['datasets'][number];
        }),
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        layout: { padding: { top: 18, right: 90, bottom: 4, left: 4 } },
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: {
            title: { display: true, text: '价差档位', color: '#8e8f91' },
            grid: { color: '#2c3038' },
            ticks: { color: '#8e8f91' },
          },
          y: {
            reverse: true,
            min: 0.5,
            max: N + 0.5,
            title: { display: true, text: '排名 (1 = 深度最高)', color: '#8e8f91' },
            grid: { color: '#2c3038' },
            ticks: {
              color: '#8e8f91',
              stepSize: 1,
              callback: value => {
                const num = typeof value === 'number' ? value : Number(value);
                return Number.isInteger(num) ? `#${num}` : '';
              },
            },
          },
        },
        plugins: {
          legend: {
            position: 'bottom',
            labels: { boxWidth: 10, color: '#8e8f91', font: { size: 10 } },
          },
          tooltip: {
            mode: 'index',
            intersect: false,
            callbacks: {
              label: ctx => {
                const rank = ctx.parsed.y;
                const datasetIdx = ctx.datasetIndex;
                const dataIdx = ctx.dataIndex;
                const usd = ranked[datasetIdx]?.values[dataIdx];
                const usdStr = typeof usd === 'number' ? moneyAuto(usd) : '—';
                return `${ctx.dataset.label}: #${rank} · ${usdStr}`;
              },
            },
          },
        },
      },
    };

    chartRef.current = new Chart(canvas, {
      ...config,
      plugins: [valueLabelPlugin],
    });

    return () => {
      chartRef.current?.destroy();
      chartRef.current = null;
    };
  }, [ariaLabel, tierLabels, displayLabels, series]);

  return (
    <div className="chart-frame">
      <div className="chart-canvas-wrap">
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-bump" />
      </div>
    </div>
  );
}
