export function KpiCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return <section className="panel kpi"><div className="label">{label}</div><div className="value">{value}</div>{sub ? <div className="sub">{sub}</div> : null}</section>;
}
