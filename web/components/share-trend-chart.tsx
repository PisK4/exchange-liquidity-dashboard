'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';

export type ShareTrendPoint = {
  day: string;
  edgex_share_pct?: number;
  share_24h_pct?: number;
  share_7d_pct?: number;
  share_30d_pct?: number;
  days_7d?: number;
  days_30d?: number;
  edgex_volume_usd?: number;
  denominator_usd?: number;
  platforms_covered?: number;
};

type Props = {
  ariaLabel: string;
  points: ShareTrendPoint[];
};

const SHARE_24H_COLOR = '#5794f2';
const SHARE_7D_COLOR = colorFor('edgeX');
const SHARE_30D_COLOR = '#ff8800';

function readNumber(value: number | undefined): number | null {
  return typeof value === 'number' ? +value.toFixed(3) : null;
}

export function ShareTrendChart({ ariaLabel, points }: Props) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    chartRef.current?.destroy();

    const labels = points.map(p => p.day);
    const series24h = points.map(p => readNumber(p.share_24h_pct ?? p.edgex_share_pct));
    const series7d = points.map(p => readNumber(p.share_7d_pct));
    const series30d = points.map(p => readNumber(p.share_30d_pct));

    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels,
        datasets: [
          {
            label: '24h share',
            data: series24h,
            borderColor: SHARE_24H_COLOR,
            backgroundColor: rgba(SHARE_24H_COLOR, 0.08),
            borderWidth: 1.6,
            pointRadius: 0,
            pointHoverRadius: 3,
            tension: 0.35,
            fill: false,
            spanGaps: false,
          },
          {
            label: '7d share',
            data: series7d,
            borderColor: SHARE_7D_COLOR,
            backgroundColor: rgba(SHARE_7D_COLOR, 0.22),
            borderWidth: 2.5,
            pointRadius: 0,
            pointHoverRadius: 4,
            tension: 0.35,
            fill: true,
            spanGaps: false,
          },
          {
            label: '30d share',
            data: series30d,
            borderColor: SHARE_30D_COLOR,
            backgroundColor: rgba(SHARE_30D_COLOR, 0.06),
            borderWidth: 1.6,
            borderDash: [4, 4],
            pointRadius: 0,
            pointHoverRadius: 3,
            tension: 0.35,
            fill: false,
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
              callback: value => `${Number(value).toFixed(1)}%`,
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
                let suffix = '';
                if (ctx.dataset.label === '7d share' && typeof point?.days_7d === 'number') {
                  suffix = ` · ${point.days_7d}/7d`;
                } else if (ctx.dataset.label === '30d share' && typeof point?.days_30d === 'number') {
                  suffix = ` · ${point.days_30d}/30d`;
                } else if (ctx.dataset.label === '24h share' && typeof point?.platforms_covered === 'number') {
                  suffix = ` · ${point.platforms_covered}/10 平台`;
                }
                return `${ctx.dataset.label}: ${v.toFixed(2)}%${suffix}`;
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
