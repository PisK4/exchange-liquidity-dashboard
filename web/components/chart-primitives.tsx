const palette: Record<string, string> = {
  edgeX: '#ff8800',
  binance: '#f3ba2f',
  okx: '#4f8cff',
  bybit: '#ffd166',
  bitget: '#00d4ff',
  bingx: '#35d07f',
  mexc: '#21c1d6',
  gate: '#7c5cff',
  hyperliquid: '#00e5a8',
  lighter: '#f2495c',
};

export type Series = {
  label: string;
  values: Array<number | undefined>;
};

export function colorFor(label: string) {
  return palette[label] ?? '#8fa1b6';
}

export function LineChart({ labels, series, unit = 'M USD' }: { labels: string[]; series: Series[]; unit?: string }) {
  const width = 640;
  const height = 210;
  const padX = 44;
  const padY = 24;
  const max = Math.max(1, ...series.flatMap(item => item.values).filter((value): value is number => typeof value === 'number'));
  const xStep = labels.length > 1 ? (width - padX * 2) / (labels.length - 1) : 0;
  const y = (value: number) => height - padY - (value / max) * (height - padY * 2);
  const x = (idx: number) => padX + idx * xStep;

  return (
    <div className="chart-frame">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${unit} line chart`}>
        {[0, 0.25, 0.5, 0.75, 1].map(mark => (
          <line key={mark} x1={padX} x2={width - padX} y1={y(max * mark)} y2={y(max * mark)} className="grid-line" />
        ))}
        {labels.map((label, idx) => (
          <text key={label} x={x(idx)} y={height - 5} textAnchor="middle" className="axis-label">{label}</text>
        ))}
        {series.map(item => {
          const points = item.values.map((value, idx) => typeof value === 'number' ? `${x(idx)},${y(value)}` : '').filter(Boolean).join(' ');
          return points ? <polyline key={item.label} points={points} fill="none" stroke={colorFor(item.label)} strokeWidth={item.label === 'edgeX' ? 3 : 1.8} strokeLinejoin="round" strokeLinecap="round" /> : null;
        })}
      </svg>
      <div className="legend">
        {series.map(item => <span key={item.label}><i style={{ background: colorFor(item.label) }} />{item.label}</span>)}
      </div>
    </div>
  );
}

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
