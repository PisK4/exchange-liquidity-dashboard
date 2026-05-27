'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

type EnvelopeLineChartProps = {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  unit?: string;
};

type TierStats = {
  median: number | null;
  min: number | null;
  max: number | null;
};

function statsPerTier(series: Series[], tierIdx: number): TierStats {
  const values = series
    .filter(s => s.label !== 'edgeX')
    .map(s => s.values[tierIdx])
    .filter((v): v is number => typeof v === 'number' && v > 0);
  if (values.length === 0) return { median: null, min: null, max: null };
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  const median = sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
  return { median, min: sorted[0], max: sorted[sorted.length - 1] };
}

export function EnvelopeLineChart({ ariaLabel, labels, series, unit = 'USD' }: EnvelopeLineChartProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    chartRef.current?.destroy();

    const stats = labels.map((_, idx) => statsPerTier(series, idx));
    const edgeX = series.find(s => s.label === 'edgeX');
    const edgeXValues = labels.map((_, idx) => {
      const v = edgeX?.values[idx];
      return typeof v === 'number' ? v : null;
    });
    const medianValues = stats.map(s => s.median);
    const minValues = stats.map(s => s.min);
    const maxValues = stats.map(s => s.max);

    const edgeXColor = colorFor('edgeX');
    const bandColor = 'rgba(140, 150, 165, 0.18)';
    const medianColor = 'rgba(200, 205, 215, 0.85)';

    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels,
        datasets: [
          {
            label: '竞品 max',
            data: maxValues,
            borderColor: 'transparent',
            backgroundColor: bandColor,
            pointRadius: 0,
            fill: '+1',
            tension: 0.35,
            order: 4,
          },
          {
            label: '竞品 min',
            data: minValues,
            borderColor: 'transparent',
            backgroundColor: bandColor,
            pointRadius: 0,
            fill: false,
            tension: 0.35,
            order: 5,
          },
          {
            label: '竞品中位数',
            data: medianValues,
            borderColor: medianColor,
            backgroundColor: medianColor,
            borderWidth: 1.5,
            borderDash: [4, 4],
            pointRadius: 3,
            pointBackgroundColor: medianColor,
            pointBorderColor: medianColor,
            tension: 0.35,
            fill: false,
            order: 2,
          },
          {
            label: 'edgeX',
            data: edgeXValues,
            borderColor: edgeXColor,
            backgroundColor: rgba(edgeXColor, 0.25),
            borderWidth: 4,
            pointRadius: 6,
            pointHoverRadius: 8,
            pointBackgroundColor: edgeXColor,
            pointBorderColor: '#ffffff',
            pointBorderWidth: 2,
            tension: 0.35,
            fill: false,
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
            labels: {
              boxWidth: 10,
              color: '#8e8f91',
              font: { size: 10 },
              filter: item => item.text !== '竞品 max' && item.text !== '竞品 min',
            },
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
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-depth-envelope" />
      </div>
    </div>
  );
}
