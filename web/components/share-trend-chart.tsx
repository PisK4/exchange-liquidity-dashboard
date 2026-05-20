'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';

export type ShareTrendPoint = {
  day: string;
  edgex_share_pct?: number;
  edgex_volume_usd?: number;
  denominator_usd?: number;
  platforms_covered?: number;
};

type Props = {
  ariaLabel: string;
  points: ShareTrendPoint[];
};

export function ShareTrendChart({ ariaLabel, points }: Props) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    chartRef.current?.destroy();

    const labels = points.map(p => p.day);
    const shareSeries = points.map(p => (typeof p.edgex_share_pct === 'number' ? +p.edgex_share_pct.toFixed(3) : null));
    const accent = colorFor('edgeX');

    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels,
        datasets: [
          {
            label: 'edgeX daily share',
            data: shareSeries,
            borderColor: accent,
            backgroundColor: rgba(accent, 0.18),
            borderWidth: 2.4,
            pointRadius: 2.5,
            pointHoverRadius: 4,
            pointBackgroundColor: accent,
            pointBorderColor: accent,
            tension: 0.32,
            fill: true,
            spanGaps: false,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: {
            grid: { color: '#2c3038' },
            ticks: { color: '#8e8f91', maxTicksLimit: 10 },
          },
          y: {
            title: { display: true, text: 'share %', color: '#8e8f91' },
            grid: { color: '#2c3038' },
            ticks: {
              color: '#8e8f91',
              callback: v => `${v}%`,
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
                const v = ctx.parsed.y;
                if (typeof v !== 'number') return '';
                const point = points[ctx.dataIndex];
                const covered = point?.platforms_covered;
                const suffix = typeof covered === 'number' ? ` · ${covered}/10 平台` : '';
                return `${ctx.dataset.label}: ${v.toFixed(3)}%${suffix}`;
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
  }, [ariaLabel, points]);

  return (
    <div className="chart-frame">
      <div className="chart-canvas-wrap">
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="share-trend-line" />
      </div>
    </div>
  );
}
