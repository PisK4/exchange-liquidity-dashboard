import type { DashboardMeta } from '@/lib/api/client';

export type SymbolContext = {
  canonical: string;
  displayName: string;
  displaySymbol: string;
  category: string;
};

export function resolveSymbolContext(meta: DashboardMeta, raw: string): SymbolContext {
  const all = meta.categories?.flatMap(c => c.symbols.map(s => ({ ...s, _category: c.key }))) ?? [];
  const upper = (raw ?? '').trim().toUpperCase();
  const byCanon = all.find(s => s.canonical.toUpperCase() === upper);
  if (byCanon) {
    return { canonical: byCanon.canonical, displayName: byCanon.display_name, displaySymbol: byCanon.display_symbol, category: byCanon._category };
  }
  const byDisplay = all.find(s => s.display_symbol.toUpperCase() === upper);
  if (byDisplay) {
    return { canonical: byDisplay.canonical, displayName: byDisplay.display_name, displaySymbol: byDisplay.display_symbol, category: byDisplay._category };
  }
  return { canonical: raw, displayName: raw, displaySymbol: raw, category: 'crypto' };
}
