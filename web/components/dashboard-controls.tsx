import Link from 'next/link';
import { SymbolSearchSelect } from './symbol-search-select';
import type { DashboardCategory, DashboardCategorySymbol } from '@/lib/api/client';

type Query = Record<string, string | undefined>;

const ALL_CATEGORY_KEY = 'all';

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

export function DashboardControls({
  query,
  categories,
  activeCategory,
  activeCanonical,
}: {
  query: Query;
  categories: DashboardCategory[];
  activeCategory: string;
  activeCanonical: string;
}) {
  const visibleSymbols: DashboardCategorySymbol[] =
    activeCategory === ALL_CATEGORY_KEY
      ? categories.flatMap(c => c.symbols)
      : categories.find(c => c.key === activeCategory)?.symbols ?? [];

  return (
    <div className="global-controls">
      <span className="control-label">
        <span>资产类别</span>
        <span className="pill-group" aria-label="资产类别">
          {categories.map(c => {
            const firstSymbol = c.symbols[0]?.canonical;
            return (
              <Link
                key={c.key}
                className={`pill ${c.key === activeCategory ? 'active' : ''}`}
                href={href(query, { category: c.key, symbol: firstSymbol })}
                data-testid={`category-pill-${c.key}`}
              >
                {c.label}
              </Link>
            );
          })}
          <Link
            className={`pill ${activeCategory === ALL_CATEGORY_KEY ? 'active' : ''}`}
            href={href(query, { category: ALL_CATEGORY_KEY })}
            data-testid="category-pill-all"
          >
            全部
          </Link>
        </span>
      </span>
      <span className="control-label">
        <span>交易对</span>
        <SymbolSearchSelect symbols={visibleSymbols} activeCanonical={activeCanonical} query={query} />
      </span>
      <Link className={`pill ${query.coreOnly === '1' ? 'active' : ''}`} href={href(query, { coreOnly: query.coreOnly === '1' ? undefined : '1' })}>
        仅看核心
      </Link>
    </div>
  );
}
