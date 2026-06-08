'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

type DualRangeLineChartProps = {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  unit?: string;
  // Index slice for the two sub-panels. Defaults split the 4 tiers as
  // low = [0, 1] (±0.05%, ±0.10%) and high = [2, 3] (±1%, ±2%).
  lowIndices?: number[];
  highIndices?: number[];
};

// Module-level constants so default props share a stable reference. Without
// this, every parent render produces a brand new [0,1] / [2,3] array, the
// SubChart useEffect dep [pickIdx] ticks, Chart.js destroys + recreates
// the chart, and the user sees an endless rebuild + entry animation loop.
const DEFAULT_LOW_INDICES = [0, 1];
const DEFAULT_HIGH_INDICES = [2, 3];

function buildConfig(
  labels: string[],
  series: Series[],
  pickIdx: number[],
  unit: string,
  showLegend: boolean,
): ChartConfiguration<'line', Array<number | null>, string> {
  const pickLabels = pickIdx.map(i => labels[i]);
  const datasets = series.map(item => {
    const color = colorFor(item.colorKey ?? item.label);
    const isSelf = item.isSelf === true;
    return {
      label: item.label,
      data: pickIdx.map(i => {
        const v = item.values[i];
        return typeof v === 'number' ? v : null;
      }),
      borderColor: color,
      backgroundColor: rgba(color, isSelf ? 0.35 : 0.12),
      borderWidth: isSelf ? 3 : 1.6,
      pointRadius: isSelf ? 4 : 2,
      pointHoverRadius: isSelf ? 5 : 3,
      pointBackgroundColor: color,
      pointBorderColor: color,
      tension: 0.35,
      fill: false,
      spanGaps: false,
    };
  });

  return {
    type: 'line',
    data: { labels: pickLabels, datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      interaction: { mode: 'index', intersect: false },
      scales: {
        x: {
          title: { display: true, text: '价差档位', color: '#8e8f91' },
          grid: { color: '#2c3038' },
          ticks: { color: '#8e8f91' },
        },
        y: {
          beginAtZero: true,
          title: { display: true, text: `深度 (${unit})`, color: '#8e8f91' },
          grid: { color: '#2c3038' },
          ticks: {
            color: '#8e8f91',
            callback: value => moneyAuto(typeof value === 'number' ? value : Number(value)),
          },
        },
      },
      plugins: {
        legend: {
          position: 'bottom',
          display: showLegend,
          labels: { boxWidth: 10, color: '#8e8f91', font: { size: 10 } },
        },
        tooltip: {
          mode: 'index',
          intersect: false,
          callbacks: {
            label: ctx => {
              const value = ctx.parsed.y;
              if (typeof value !== 'number') return '';
              return `${ctx.dataset.label}: ${moneyAuto(value)} @ ${ctx.label}`;
            },
          },
        },
      },
    },
  };
}

function SubChart({
  ariaLabel,
  labels,
  series,
  pickIdx,
  unit,
  title,
  showLegend,
}: {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  pickIdx: number[];
  unit: string;
  title: string;
  showLegend: boolean;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    chartRef.current?.destroy();
    chartRef.current = new Chart(canvas, buildConfig(labels, series, pickIdx, unit, showLegend));
    return () => {
      chartRef.current?.destroy();
      chartRef.current = null;
    };
  }, [labels, series, pickIdx, unit, showLegend]);

  return (
    <div className="dual-range-pane">
      <div className="dual-range-pane-title">{title}</div>
      <div className="dual-range-pane-canvas">
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-depth-dual-range" />
      </div>
    </div>
  );
}

export function DualRangeLineChart({
  ariaLabel,
  labels,
  series,
  unit = 'USD',
  lowIndices = DEFAULT_LOW_INDICES,
  highIndices = DEFAULT_HIGH_INDICES,
}: DualRangeLineChartProps) {
  return (
    <div className="dual-range-grid" aria-label={ariaLabel}>
      <SubChart
        ariaLabel={`${ariaLabel} 低价档位`}
        labels={labels}
        series={series}
        pickIdx={lowIndices}
        unit={unit}
        title="低价档位（贴近盘口）"
        showLegend={false}
      />
      <SubChart
        ariaLabel={`${ariaLabel} 高价档位`}
        labels={labels}
        series={series}
        pickIdx={highIndices}
        unit={unit}
        title="高价档位（远离盘口）"
        showLegend
      />
    </div>
  );
}
