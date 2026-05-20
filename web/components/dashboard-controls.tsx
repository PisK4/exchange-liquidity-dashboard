import Link from 'next/link';

type Query = Record<string, string | undefined>;

function href(query: Query, patch: Query) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries({ ...query, ...patch })) {
    if (value) params.set(key, value);
  }
  const qs = params.toString();
  return qs ? `/?${qs}` : '/';
}

export function PillGroup({ label, items, active, query, param }: { label?: string; items: string[]; active?: string; query: Query; param: string }) {
  return (
    <span className="pill-group" aria-label={label}>
      {items.map(item => (
        <Link className={`pill ${item === active ? 'active' : ''}`} href={href(query, { [param]: item })} key={item}>
          {item}
        </Link>
      ))}
    </span>
  );
}

export function DashboardControls({ query, symbols, activeSymbol, activeWindow }: { query: Query; symbols: string[]; activeSymbol: string; activeWindow: string }) {
  return (
    <div className="global-controls">
      <label>
        <span>资产类别</span>
        <PillGroup items={['加密货币', '大宗商品', '股票', '指数', '全部']} active={query.category ?? '加密货币'} query={query} param="category" />
      </label>
      <label>
        <span>交易对</span>
        <PillGroup items={symbols} active={activeSymbol} query={query} param="symbol" />
      </label>
      <label>
        <span>统计窗口</span>
        <PillGroup items={['24h', '7d', '30d']} active={activeWindow} query={query} param="window" />
      </label>
      <Link className={`pill ${query.coreOnly === '1' ? 'active' : ''}`} href={href(query, { coreOnly: query.coreOnly === '1' ? undefined : '1' })}>
        仅看核心
      </Link>
    </div>
  );
}
