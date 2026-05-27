'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

type TierRankBarsProps = {
  ariaLabel: string;
  tierLabels: string[];
  displayLabels: string[];
  series: Series[];
  unit?: string;
};

type Row = { platform: string; value: number };

function rowsForTier(series: Series[], tierIdx: number): Row[] {
  return series
    .map(s => ({ platform: s.label, value: s.values[tierIdx] }))
    .filter((r): r is Row => typeof r.value === 'number' && r.value > 0)
    .sort((a, b) => b.value - a.value);
}

function MiniBar({
  ariaLabel,
  tierLabel,
  rows,
  unit,
}: {
  ariaLabel: string;
  tierLabel: string;
  rows: Row[];
  unit: string;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'bar', number[], string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    chartRef.current?.destroy();

    const edgeXIdx = rows.findIndex(r => r.platform === 'edgeX');
    const edgeXRank = edgeXIdx >= 0 ? edgeXIdx + 1 : null;
    const totalRanked = rows.length;

    const config: ChartConfiguration<'bar', number[], string> = {
      type: 'bar',
      data: {
        labels: rows.map(r => r.platform),
        datasets: [
          {
            label: tierLabel,
            data: rows.map(r => r.value),
            backgroundColor: rows.map(r =>
              r.platform === 'edgeX' ? colorFor('edgeX') : rgba(colorFor(r.platform), 0.65),
            ),
            borderColor: rows.map(r =>
              r.platform === 'edgeX' ? '#ffffff' : 'transparent',
            ),
            borderWidth: rows.map(r => (r.platform === 'edgeX' ? 1.5 : 0)),
            borderRadius: 3,
          },
        ],
      },
      options: {
        indexAxis: 'y',
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { display: false },
          tooltip: {
            callbacks: {
              label: ctx => `${ctx.label}: ${moneyAuto(ctx.parsed.x)}`,
            },
          },
          title: {
            display: true,
            text:
              edgeXRank !== null
                ? `${tierLabel} · edgeX ${edgeXRank}/${totalRanked}`
                : `${tierLabel} · edgeX 无数据`,
            color: '#8e8f91',
            font: { size: 11 },
            padding: { top: 2, bottom: 6 },
          },
        },
        scales: {
          x: {
            type: 'logarithmic',
            min: 1,
            grid: { color: '#2c3038' },
            ticks: {
              color: '#8e8f91',
              font: { size: 9 },
              callback: v => moneyAuto(typeof v === 'number' ? v : Number(v)),
            },
            title: { display: true, text: unit, color: '#8e8f91', font: { size: 9 } },
          },
          y: {
            grid: { display: false },
            ticks: { color: '#8e8f91', font: { size: 10 } },
          },
        },
      },
    };

    chartRef.current = new Chart(canvas, config);
    return () => {
      chartRef.current?.destroy();
      chartRef.current = null;
    };
  }, [tierLabel, rows, unit]);

  return (
    <div className="tier-rank-mini">
      <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-depth-rank" />
    </div>
  );
}

export function TierRankBars({ ariaLabel, tierLabels, displayLabels, series, unit = 'USD' }: TierRankBarsProps) {
  return (
    <div className="tier-rank-grid" aria-label={ariaLabel}>
      {tierLabels.map((tier, idx) => (
        <MiniBar
          key={tier}
          ariaLabel={`${ariaLabel} ${displayLabels[idx]}`}
          tierLabel={displayLabels[idx]}
          rows={rowsForTier(series, idx)}
          unit={unit}
        />
      ))}
    </div>
  );
}
