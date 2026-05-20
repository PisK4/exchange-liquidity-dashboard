import { colorFor } from '@/components/chart-colors';

export function BarChart({ rows, format }: { rows: Array<{ label: string; value?: number; status?: string }>; format: (value?: number) => string }) {
  const max = Math.max(1, ...rows.map(row => row.value ?? 0));
  return (
    <div className="bar-chart">
      {rows.map(row => {
        const value = row.value;
        const width = typeof value === 'number' ? Math.max(2, (value / max) * 100) : 0;
        return (
          <div className="bar-row" key={row.label}>
            <span className={row.label === 'edgeX' ? 'platform-self' : ''}>{row.label}</span>
            <div className="bar-track"><div className="bar-fill" style={{ width: `${width}%`, background: colorFor(row.label) }} /></div>
            <b>{typeof row.value === 'number' ? format(row.value) : '—'}</b>
          </div>
        );
      })}
    </div>
  );
}
