'use client';

import { useEffect, useRef } from 'react';
import Chart from 'chart.js/auto';
import type { ChartConfiguration } from 'chart.js';
import { colorFor, rgba } from '@/components/chart-colors';
import { moneyAuto } from '@/lib/api/client';
import type { Series } from '@/components/line-chart';

// SmallMultiplesBarChart 把"4 个档位 × N 个平台"的离散数据从折线图
// 形态切换为 2×2 小倍数横向条形图：
//   - 每个 panel 对应一个 tier，X 轴量纲独立自适应（这才解决"低档位
//     被高档位的 250M 压扁"的根因）
//   - 平台在 panel 内按 USD 深度降序排列，edgeX 强调（实色 + 描边），
//     竞品压暗
//   - 数值直接打在条尾，不依赖 hover；档位标题里写出该 panel 的最大
//     量纲，避免量级误读
type SmallMultiplesBarChartProps = {
  ariaLabel: string;
  tierLabels: string[];
  displayLabels: string[];
  series: Series[];
};

type PanelRow = { label: string; value: number; colorKey?: string; isSelf?: boolean };
type DepthDataset = ChartConfiguration<'bar', number[], string>['data']['datasets'][number] & {
  selfFlags?: boolean[];
};

function buildPanelConfig(
  rows: PanelRow[],
): ChartConfiguration<'bar', number[], string> {
  const sorted = [...rows].sort((a, b) => b.value - a.value);
  const dataset: DepthDataset = {
    label: '深度',
    data: sorted.map(r => r.value),
    selfFlags: sorted.map(r => r.isSelf === true),
    backgroundColor: sorted.map(r => {
      const c = colorFor(r.colorKey ?? r.label);
      const self = r.isSelf === true;
      return self ? c : rgba(c, 0.45);
    }),
    borderColor: sorted.map(r => {
      const c = colorFor(r.colorKey ?? r.label);
      const self = r.isSelf === true;
      return self ? '#ffffff' : rgba(c, 0.85);
    }),
    borderWidth: sorted.map(r => (r.isSelf === true ? 1.5 : 0.6)),
    borderRadius: 2,
    barPercentage: 0.82,
    categoryPercentage: 0.92,
  };
  return {
    type: 'bar',
    data: {
      labels: sorted.map(r => r.label),
      datasets: [dataset],
    },
    options: {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      layout: { padding: { top: 4, right: 56, bottom: 2, left: 2 } },
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: ctx => `${ctx.label}: ${moneyAuto(ctx.parsed.x)}`,
          },
        },
      },
      scales: {
        x: {
          beginAtZero: true,
          grid: { color: 'rgba(255,255,255,0.05)' },
          ticks: {
            color: '#8e8f91',
            font: { size: 9 },
            maxTicksLimit: 4,
            callback: value => moneyAuto(typeof value === 'number' ? value : Number(value)),
          },
        },
        y: {
          grid: { display: false },
          ticks: {
            color: '#c5cad3',
            font: { size: 10 },
            autoSkip: false,
          },
        },
      },
    },
  };
}

const valueLabelPlugin = {
  id: 'smBarValueLabel',
  afterDatasetsDraw(chart: Chart) {
    const { ctx } = chart;
    const meta = chart.getDatasetMeta(0);
    if (!meta) return;
    ctx.save();
    ctx.font = '10px ui-sans-serif, system-ui, sans-serif';
    ctx.textBaseline = 'middle';
    meta.data.forEach((bar, idx) => {
      const raw = chart.data.datasets[0].data[idx];
      if (typeof raw !== 'number' || !Number.isFinite(raw)) return;
      const isSelf = (chart.data.datasets[0] as DepthDataset).selfFlags?.[idx] === true;
      ctx.fillStyle = isSelf ? '#ffffff' : '#c5cad3';
      const { x, y } = bar.tooltipPosition(false);
      ctx.fillText(`  ${moneyAuto(raw)}`, x, y);
    });
    ctx.restore();
  },
};

function PanelChart({
  ariaLabel,
  rows,
}: {
  ariaLabel: string;
  rows: PanelRow[];
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const chartRef = useRef<Chart<'bar', number[], string> | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    chartRef.current?.destroy();
    const config = buildPanelConfig(rows);
    chartRef.current = new Chart(canvas, {
      ...config,
      plugins: [valueLabelPlugin],
    });
    return () => {
      chartRef.current?.destroy();
      chartRef.current = null;
    };
  }, [rows]);

  return (
    <div className="sm-bar-canvas">
      <canvas ref={canvasRef} role="img" aria-label={ariaLabel} data-chart-library="chartjs-sm-bar" />
    </div>
  );
}

export function SmallMultiplesBarChart({
  ariaLabel,
  tierLabels,
  displayLabels,
  series,
}: SmallMultiplesBarChartProps) {
  const panels = tierLabels.map((_tier, tierIdx) => {
    const rows: PanelRow[] = [];
    for (const s of series) {
      const value = s.values[tierIdx];
      if (typeof value === 'number' && Number.isFinite(value)) {
        rows.push({ label: s.label, value, colorKey: s.colorKey, isSelf: s.isSelf });
      }
    }
    const maxValue = rows.reduce((acc, r) => (r.value > acc ? r.value : acc), 0);
    return {
      title: displayLabels[tierIdx] ?? `tier ${tierIdx}`,
      maxValue,
      rows,
    };
  });

  return (
    <div className="sm-bar-grid" aria-label={ariaLabel}>
      {panels.map((panel, idx) => (
        <div className="sm-bar-pane" key={panel.title}>
          <div className="sm-bar-pane-title">
            <span className="sm-bar-pane-tier">{panel.title}</span>
            <span className="sm-bar-pane-scale">max {moneyAuto(panel.maxValue)}</span>
          </div>
          {panel.rows.length === 0 ? (
            <div className="sm-bar-pane-empty">无数据</div>
          ) : (
            <PanelChart ariaLabel={`${ariaLabel} ${panel.title}`} rows={panel.rows} />
          )}
        </div>
      ))}
    </div>
  );
}
