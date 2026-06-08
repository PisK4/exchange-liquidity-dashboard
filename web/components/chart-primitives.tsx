import { colorFor } from '@/components/chart-colors';
import { platformDisplayName } from '@/components/platform-cell';

type BarChartRow = {
  key?: string;
  label: string;
  displayLabel?: string;
  isSelf?: boolean;
  value?: number;
  status?: string;
  color?: string;
};

export function BarChart({
  rows,
  format,
  signed = false,
  sort,
}: {
  rows: BarChartRow[];
  format: (value: number | undefined, row: BarChartRow) => string;
  signed?: boolean;
  sort?: 'asc';
}) {
  const orderedRows = sort === 'asc'
    ? [...rows].sort((a, b) => (a.value ?? Number.POSITIVE_INFINITY) - (b.value ?? Number.POSITIVE_INFINITY))
    : rows;
  const max = Math.max(1, ...orderedRows.map(row => Math.abs(row.value ?? 0)));
  return (
    <div className={`bar-chart ${signed ? 'signed' : ''}`}>
      {orderedRows.map(row => {
        const value = row.value;
        const width = typeof value === 'number' ? Math.max(2, (Math.abs(value) / max) * (signed ? 50 : 100)) : 0;
        const isSelf = row.isSelf === true;
        const label = row.displayLabel ?? platformDisplayName(row.label);
        const fillColor = row.color ?? colorFor(row.label);
        const signedStyle = signed
          ? value !== undefined && value < 0
            ? { right: '50%', width: `${width}%`, background: fillColor }
            : { left: '50%', width: `${width}%`, background: fillColor }
          : { width: `${width}%`, background: fillColor };
        return (
          <div className={`bar-row ${signed ? 'signed' : ''}`} key={row.key ?? row.label}>
            <span className={isSelf ? 'platform-self' : ''}>{label}</span>
            <div className={`bar-track ${signed ? 'signed' : ''}`}>
              {signed ? <span className="bar-zero" aria-hidden="true" /> : null}
              <div className="bar-fill" data-testid={signed ? 'signed-bar' : undefined} style={signedStyle} />
            </div>
            <b>{typeof row.value === 'number' ? format(row.value, row) : '—'}</b>
          </div>
        );
      })}
    </div>
  );
}
