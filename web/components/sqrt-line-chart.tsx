'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration, TooltipModel } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

type SqrtLineChartProps = {
  ariaLabel: string;
  labels: string[];
  series: Series[];
  unit?: string;
};

// Chart.js 没有原生 sqrt 刻度。这里用 "数据预变换 + 显示反变换" 的常用方案：
//   1. dataset.data 写入 sqrt(value)，让 Chart.js 用线性轴绘制
//   2. Y 轴 tick callback 把 sqrt(value) 平方回 USD 再格式化
//   3. tooltip label 同样平方回 USD，让用户读到的永远是真实金额
//
// 与 linear 对照评估时，这套做法保证：
//   - 大档位 (~250M) 与小档位 (~1M) 在视觉上不再相差 250 倍，会被压缩
//     到约 sqrt(250)/sqrt(1) ≈ 16 倍，低档位 + edgeX 都能"抬起来"
//   - 但 USD 数量级仍诚实可读（读 tooltip / Y 轴标签即可）

function sqrtTransform(v: number | undefined): number | null {
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 0) return null;
  return Math.sqrt(v);
}

function renderExternalTooltip(chart: Chart, tooltip: TooltipModel<'line'>) {
  const parent = chart.canvas.parentElement;
  const tooltipEl = parent?.querySelector<HTMLDivElement>('[data-testid="chartjs-sqrt-tooltip"]');
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

export function SqrtLineChart({ ariaLabel, labels, series, unit = 'USD' }: SqrtLineChartProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'line', Array<number | null>, string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    chartRef.current?.destroy();

    const config: ChartConfiguration<'line', Array<number | null>, string> = {
      type: 'line',
      data: {
        labels,
        datasets: series.map(item => {
          const color = colorFor(item.colorKey ?? item.label);
          const isSelf = item.isSelf === true;
          return {
            label: item.label,
            data: item.values.map(sqrtTransform),
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
        }),
      },
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
            title: { display: true, text: `深度 (${unit}, sqrt 刻度)`, color: '#8e8f91' },
            grid: { color: '#2c3038' },
            ticks: {
              color: '#8e8f91',
              callback: value => {
                const num = typeof value === 'number' ? value : Number(value);
                return moneyAuto(num * num);
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
            enabled: false,
            mode: 'index',
            intersect: false,
            external: ({ chart, tooltip }) => renderExternalTooltip(chart, tooltip),
            callbacks: {
              label: context => {
                const sqrtY = context.parsed.y;
                if (typeof sqrtY !== 'number') return '';
                const usd = sqrtY * sqrtY;
                return `${context.dataset.label}: ${moneyAuto(usd)} @ ${context.label}`;
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
        <canvas
          ref={canvasRef}
          role="img"
          aria-label={ariaLabel}
          data-chart-library="chartjs-sqrt-line"
        />
        <div
          className="chartjs-tooltip"
          data-testid="chartjs-sqrt-tooltip"
          aria-hidden="true"
        />
      </div>
    </div>
  );
}
