'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

type DotPlotChartProps = {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  unit?: string;
};

function tierMedian(series: Series[], tierIdx: number): number | null {
  const values = series
    .filter(s => s.label !== 'edgeX')
    .map(s => s.values[tierIdx])
    .filter((v): v is number => typeof v === 'number');
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
}

export function DotPlotChart({ ariaLabel, labels, series, unit = 'USD' }: DotPlotChartProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    chartRef.current?.destroy();

    const medianValues = labels.map((_, idx) => tierMedian(series, idx));

    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels,
        datasets: [
          ...series.map(item => {
            const color = colorFor(item.label);
            const isSelf = item.label === 'edgeX';
            return {
              label: item.label,
              data: item.values.map(value => (typeof value === 'number' ? value : null)),
              borderColor: 'transparent',
              backgroundColor: isSelf ? color : rgba(color, 0.55),
              borderWidth: 0,
              showLine: false,
              pointRadius: isSelf ? 7 : 4,
              pointHoverRadius: isSelf ? 9 : 6,
              pointStyle: isSelf ? 'rectRot' : 'circle',
              pointBackgroundColor: isSelf ? color : rgba(color, 0.7),
              pointBorderColor: isSelf ? '#ffffff' : color,
              pointBorderWidth: isSelf ? 2 : 1,
              order: isSelf ? 0 : 2,
            };
          }),
          {
            label: '竞品中位数',
            data: medianValues.map(v => (v === null ? null : v)),
            borderColor: 'rgba(220, 220, 220, 0.85)',
            backgroundColor: 'rgba(220, 220, 220, 0.85)',
            borderWidth: 0,
            showLine: false,
            pointRadius: 9,
            pointHoverRadius: 11,
            pointStyle: 'line',
            pointBorderColor: 'rgba(220, 220, 220, 0.85)',
            pointBorderWidth: 2,
            order: 1,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: {
            title: { display: true, text: '价差档位', color: '#8e8f91' },
            grid: { color: '#2c3038' },
            ticks: { color: '#8e8f91' },
          },
          y: {
            type: 'logarithmic',
            min: 1,
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
            labels: { boxWidth: 10, color: '#8e8f91', font: { size: 10 }, filter: item => item.text !== 'edgeX' || true },
          },
          tooltip: {
            mode: 'index',
            intersect: false,
            callbacks: {
              label: context => {
                const value = context.parsed.y;
                if (typeof value !== 'number') return '';
                return `${context.dataset.label}: ${moneyAuto(value)} @ ${context.label}`;
              },
            },
          },
        },
      },
    };

    chartRef.current = new Chart(canvas, config);
    return () => {
      chartRef.current?.destroy();
      chartRef.current = null;
    };
  }, [ariaLabel, labels, series, unit]);

  return (
    <div className="chart-frame">
      <div className="chart-canvas-wrap">
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-depth-dotplot" />
      </div>
    </div>
  );
}
