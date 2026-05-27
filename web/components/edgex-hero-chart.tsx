'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { edgeXValueLabelPlugin } from '@/components/chart-plugins';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

type EdgeXHeroChartProps = {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  unit?: string;
  lowIndices?: number[];
  highIndices?: number[];
};

// Module-level constants so default props share a stable reference across
// renders. Without this, EdgeXHeroChart({ ... }) creates a brand new
// [0, 1] / [2, 3] array on every parent render, which would change the
// pickIdx prop reference on SubChart, re-run its useEffect, and re-create
// the underlying Chart.js instance — manifested as an endless rebuild +
// entry-animation loop (the "图会自己动" bug).
const DEFAULT_LOW_INDICES = [0, 1];
const DEFAULT_HIGH_INDICES = [2, 3];

function statsPerTier(series: Series[], tierIdx: number) {
  const competitors = series
    .filter(s => s.label !== 'edgeX')
    .map(s => s.values[tierIdx])
    .filter((v): v is number => typeof v === 'number' && v > 0);
  if (competitors.length === 0) {
    return { median: null as number | null, min: null as number | null, max: null as number | null };
  }
  const sorted = [...competitors].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  const median = sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
  return { median, min: sorted[0], max: sorted[sorted.length - 1] };
}

function edgexRank(series: Series[], tierIdx: number): { rank: number | null; total: number } {
  const entries = series
    .map(s => ({ platform: s.label, value: s.values[tierIdx] }))
    .filter((r): r is { platform: string; value: number } => typeof r.value === 'number' && r.value > 0)
    .sort((a, b) => b.value - a.value);
  const idx = entries.findIndex(r => r.platform === 'edgeX');
  return { rank: idx >= 0 ? idx + 1 : null, total: entries.length };
}

function buildConfig(
  labels: string[],
  series: Series[],
  pickIdx: number[],
  unit: string,
  showLegend: boolean,
): ChartConfiguration<'line', Array<number | null>, string> {
  const pickLabels = pickIdx.map(i => labels[i]);
  const edgeX = series.find(s => s.label === 'edgeX');
  const edgeXValues = pickIdx.map(i => {
    const v = edgeX?.values[i];
    return typeof v === 'number' ? v : null;
  });
  const stats = pickIdx.map(i => statsPerTier(series, i));
  const medianValues = stats.map(s => s.median);

  const edgeXColor = colorFor('edgeX');
  // 竞品视觉权重分两层：
  //   线 (0.42 opacity) - 让 slope 可读，但不喧宾夺主
  //   点 (0.95 opacity + 深色描边) - 每个平台在每个档位的"身份点"清晰
  // 之前 0.18 太透明，9 条叠在一起就成了一团灰雾。
  const competitorLineAlpha = 0.42;
  const competitorPointAlpha = 0.95;
  // 中位数改用琥珀色，与 edgeX 绿、竞品多色都不冲突，
  // 一眼读为"参考基准线"。
  const medianColor = '#ffce54';
  // 点描边色：取暗色面板背景的近似值，让点像 sticker 一样从颜色块上"贴"出来。
  const pointStrokeColor = '#0e1318';

  const competitorDatasets = series
    .filter(s => s.label !== 'edgeX')
    .map(item => {
      const color = colorFor(item.label);
      return {
        label: item.label,
        data: pickIdx.map(i => {
          const v = item.values[i];
          return typeof v === 'number' ? v : null;
        }),
        borderColor: rgba(color, competitorLineAlpha),
        backgroundColor: rgba(color, competitorLineAlpha),
        borderWidth: 1.5,
        pointRadius: 4,
        pointHoverRadius: 6,
        pointBackgroundColor: rgba(color, competitorPointAlpha),
        pointBorderColor: pointStrokeColor,
        pointBorderWidth: 1.5,
        tension: 0.35,
        fill: false,
        spanGaps: false,
        order: 5,
      };
    });

  return {
    type: 'line',
    data: {
      labels: pickLabels,
      datasets: [
        ...competitorDatasets,
        {
          label: '竞品中位数',
          data: medianValues,
          borderColor: medianColor,
          backgroundColor: medianColor,
          borderWidth: 2,
          borderDash: [6, 5],
          pointRadius: 0,
          pointHoverRadius: 4,
          pointBackgroundColor: medianColor,
          pointBorderColor: medianColor,
          tension: 0.35,
          fill: false,
          order: 3,
        },
        {
          label: 'edgeX',
          data: edgeXValues,
          borderColor: edgeXColor,
          backgroundColor: rgba(edgeXColor, 0.25),
          borderWidth: 5,
          pointRadius: 7,
          pointHoverRadius: 9,
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
      // Disable Chart.js entry / data-update animations. Repeated parent
      // re-renders would otherwise restart a 1000ms ease every time and
      // the value-label plugin redraws the floating USD numbers on every
      // animation frame — the user perceives it as the chart "drifting".
      animation: false,
      interaction: { mode: 'index', intersect: false },
      layout: { padding: { top: 28, right: 18, bottom: 4, left: 4 } },
      scales: {
        x: {
          title: { display: true, text: '价差档位', color: '#8e8f91', font: { size: 10 } },
          grid: { color: '#2c3038' },
          ticks: { color: '#8e8f91', font: { size: 10 } },
        },
        y: {
          beginAtZero: true,
          title: { display: true, text: `深度 (${unit})`, color: '#8e8f91', font: { size: 10 } },
          grid: { color: '#2c3038' },
          ticks: {
            color: '#8e8f91',
            font: { size: 10 },
            callback: value => moneyAuto(typeof value === 'number' ? value : Number(value)),
          },
        },
      },
      plugins: {
        legend: {
          display: showLegend,
          position: 'bottom',
          align: 'start',
          labels: {
            boxWidth: 8,
            boxHeight: 8,
            color: '#c5cad3',
            font: { size: 10 },
            padding: 8,
            usePointStyle: true,
          },
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
    chartRef.current = new Chart(canvas, {
      ...buildConfig(labels, series, pickIdx, unit, showLegend),
      plugins: [edgeXValueLabelPlugin],
    });
    return () => {
      chartRef.current?.destroy();
      chartRef.current = null;
    };
  }, [labels, series, pickIdx, unit, showLegend]);

  const rankInfo = pickIdx.map(i => ({ tier: labels[i], ...edgexRank(series, i) }));

  return (
    <div className="dual-range-pane">
      <div className="dual-range-pane-title">
        <span>{title}</span>
        <span className="dual-range-pane-rank">
          {rankInfo.map(r => (
            <span key={r.tier} className="rank-chip">
              <span className="rank-chip-tier">{r.tier}</span>
              <span className="rank-chip-rank">
                {r.rank !== null && r.total > 0 ? `edgeX ${r.rank}/${r.total}` : 'edgeX 无数据'}
              </span>
            </span>
          ))}
        </span>
      </div>
      <div className="dual-range-pane-canvas">
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-depth-edgex-hero" />
      </div>
    </div>
  );
}

export function EdgeXHeroChart({
  ariaLabel,
  labels,
  series,
  unit = 'USD',
  lowIndices = DEFAULT_LOW_INDICES,
  highIndices = DEFAULT_HIGH_INDICES,
}: EdgeXHeroChartProps) {
  return (
    <div className="hero-chart-wrap" aria-label={ariaLabel}>
      <div className="dual-range-grid">
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
          showLegend={false}
        />
      </div>
      <SharedLegend series={series} />
    </div>
  );
}

function SharedLegend({ series }: { series: Series[] }) {
  const competitors = series.filter(s => s.label !== 'edgeX');
  return (
    <div className="hero-legend">
      <span className="hero-legend-item hero-legend-edgex">
        <span className="hero-legend-swatch hero-legend-line-edgex" />
        edgeX
      </span>
      <span className="hero-legend-item hero-legend-median">
        <span className="hero-legend-swatch hero-legend-line-median" />
        竞品中位数
      </span>
      <span className="hero-legend-divider" />
      {competitors.map(item => (
        <span key={item.label} className="hero-legend-item">
          <span
            className="hero-legend-swatch hero-legend-dot"
            style={{ background: colorFor(item.label) }}
          />
          {item.label}
        </span>
      ))}
    </div>
  );
}
