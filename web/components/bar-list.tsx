export function BarList({ rows, valueKey, labelKey }: { rows: Record<string, any>[]; valueKey: string; labelKey: string }) {
  const max = Math.max(1, ...rows.map(r => Number(r[valueKey]) || 0));
  return <div className="bars">{rows.map(row => { const value = Number(row[valueKey]) || 0; return <div className="bar" key={row[labelKey]}><span>{row[labelKey]}</span><div className="track"><div className="fill" style={{ width: `${Math.max(2, value / max * 100)}%` }} /></div><span>{value.toFixed(2)}</span></div>; })}</div>;
}
