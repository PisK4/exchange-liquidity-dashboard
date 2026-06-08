'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration, TooltipModel } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';

export type Series = {
  key?: string;
  label: string;
  colorKey?: string;
  isSelf?: boolean;
  values: Array<number | undefined>;
  statuses?: Array<string | undefined>;
  sources?: Array<string | undefined>;
};

type LineChartProps = {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  unit?: string;
  // compact=true tunes the chart for a dense card surface (WatchlistCard
  // mini chart): the legend is collapsed to a single horizontal row of
  // small swatches, axis titles are dropped, font sizes shrink to 9px,
  // and the y-axis tick formatter still uses moneyAuto so 12,345,678
  // never renders raw. Everything else (color rules, edgeX accent,
  // dashed-for-loose, tooltip path) stays identical to the default
  // mode so the visual language is consistent with the V1 detail view.
  compact?: boolean;
};

function renderExternalTooltip(chart: Chart, tooltip: TooltipModel<'line'>) {
  const parent = chart.canvas.parentElement;
  const tooltipEl = parent?.querySelector<HTMLDivElement>('[data-testid="chartjs-depth-tooltip"]');
  if (!parent || !tooltipEl) return;

  if (tooltip.opacity === 0) {
    tooltipEl.style.opacity = '0';
    tooltipEl.style.pointerEvents = 'none';
    return;
  }

  tooltipEl.replaceChildren();
  const title = document.createElement('div');
  title.className = 'chartjs-tooltip-title';
  title.textContent = tooltip.title.join(' · ');
  tooltipEl.appendChild(title);

  tooltip.body.forEach((bodyItem, idx) => {
    for (const line of bodyItem.lines) {
      if (!line) continue;
      const row = document.createElement('div');
      row.className = 'chartjs-tooltip-row';

      const swatch = document.createElement('span');
      swatch.className = 'chartjs-tooltip-swatch';
      swatch.style.background = tooltip.labelColors[idx]?.borderColor?.toString() ?? '#8fa1b6';

      const text = document.createElement('span');
      text.textContent = line;

      row.append(swatch, text);
      tooltipEl.appendChild(row);
    }
  });

  const canvasRect = chart.canvas.getBoundingClientRect();
  const parentRect = parent.getBoundingClientRect();
  tooltipEl.style.opacity = '1';
  tooltipEl.style.pointerEvents = 'none';
  tooltipEl.style.left = `${canvasRect.left - parentRect.left + tooltip.caretX}px`;
  tooltipEl.style.top = `${canvasRect.top - parentRect.top + tooltip.caretY}px`;
}

export function LineChart({ ariaLabel, labels, series, unit = 'USD', compact = false }: LineChartProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    chartRef.current?.destroy();
    // 视觉权重：
    //   edgeX  → V1 默认权重（3px 实线），不 hero 加粗、不画静态点
    //   竞品   → 1.5px @ 0.42 alpha 线，退到背景层、不画静态点
    //   所有平台静态 pointRadius=0 让画面只剩"线"，避免点扎堆相互盖；
    //   pointHoverRadius 保留 4–5px 以便 hover 触发 tooltip
    // compact 模式（dead path）参数保持原样不破坏未来回滚。
    const competitorLineAlpha = 0.42;
    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels,
        datasets: series.map(item => {
          const color = colorFor(item.colorKey ?? item.label);
          const isSelf = item.isSelf === true;
          if (isSelf) {
            return {
              label: item.label,
              data: item.values.map(value => (typeof value === 'number' ? value : null)),
              borderColor: color,
              backgroundColor: rgba(color, 0.35),
              borderWidth: compact ? 2 : 3,
              pointRadius: compact ? 2.5 : 0,
              pointHoverRadius: compact ? 3.5 : 5,
              pointBackgroundColor: color,
              pointBorderColor: color,
              pointBorderWidth: 0,
              tension: 0.35,
              fill: false,
              spanGaps: false,
            };
          }
          return {
            label: item.label,
            data: item.values.map(value => (typeof value === 'number' ? value : null)),
            borderColor: compact ? color : rgba(color, competitorLineAlpha),
            backgroundColor: compact ? rgba(color, 0.12) : rgba(color, competitorLineAlpha),
            borderWidth: compact ? 1 : 1.5,
            pointRadius: compact ? 1.2 : 0,
            pointHoverRadius: compact ? 2 : 4,
            pointBackgroundColor: color,
            pointBorderColor: color,
            pointBorderWidth: 0,
            tension: 0.35,
            fill: false,
            spanGaps: false,
          };
        }),
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        // 关闭 entry / data-update 动画。父组件重渲染会反复触发 1000ms
        // 缓动，导致重复 destroy + recreate Chart 视觉上像图自己在抖。
        animation: false,
        layout: compact ? undefined : { padding: { top: 8, right: 12, bottom: 4, left: 4 } },
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: {
            title: { display: !compact, text: '价差档位', color: '#8e8f91' },
            grid: { color: '#2c3038', display: !compact },
            ticks: { color: '#8e8f91', font: compact ? { size: 9 } : undefined },
          },
          y: {
            title: { display: !compact, text: `深度 (${unit})`, color: '#8e8f91' },
            grid: { color: '#2c3038' },
            ticks: {
              color: '#8e8f91',
              font: compact ? { size: 9 } : undefined,
              maxTicksLimit: compact ? 4 : undefined,
              callback: value => moneyAuto(typeof value === 'number' ? value : Number(value)),
            },
          },
        },
        plugins: {
          legend: {
            position: 'bottom',
            display: !compact,
            labels: { boxWidth: 10, color: '#8e8f91', font: { size: 10 } },
          },
          tooltip: {
            enabled: false,
            mode: 'index',
            intersect: false,
            external: ({ chart, tooltip }) => renderExternalTooltip(chart, tooltip),
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
  }, [ariaLabel, labels, series, unit, compact]);

  return (
    <div className="chart-frame">
      <div className="chart-canvas-wrap">
        <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-depth-line" />
        <div className="chartjs-tooltip" data-testid="chartjs-depth-tooltip" aria-hidden="true" />
      </div>
    </div>
  );
}
