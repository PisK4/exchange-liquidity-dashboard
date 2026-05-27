'use client';

import { useEffect, useRef, useState } from 'react';
import type { DashboardCategorySymbol } from '@/lib/api/client';
import { SymbolPickerDropdown } from './symbol-picker-dropdown';

type Query = Record<string, string | undefined>;

function href(query: Query, patch: Query) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries({ ...query, ...patch })) {
    if (value) params.set(key, value);
  }
  const qs = params.toString();
  return qs ? `/?${qs}` : '/';
}

export function SymbolSearchSelect({
  symbols,
  activeCanonical,
  query,
  watchlist,
  onToggleFavorite,
  maxFavorites,
}: {
  symbols: DashboardCategorySymbol[];
  activeCanonical: string;
  query: Query;
  watchlist: string[];
  onToggleFavorite: (canonical: string) => void;
  maxFavorites: number;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLSpanElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const active = symbols.find(s => s.canonical === activeCanonical);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, []);

  const label = active?.display_name ?? activeCanonical ?? '—';

  return (
    <span className="symbol-select" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        className={`pill symbol-select-trigger ${open ? 'active' : ''}`}
        onClick={() => setOpen(o => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        data-testid="symbol-select-trigger"
      >
        <span>{label}</span>
        <span className="symbol-select-caret" aria-hidden>▾</span>
      </button>
      {open && (
        <SymbolPickerDropdown
          symbols={symbols}
          favorites={watchlist}
          onToggleFavorite={onToggleFavorite}
          maxFavorites={maxFavorites}
          triggerRef={triggerRef}
          onClose={() => setOpen(false)}
          buildHref={(canonical) => href(query, { symbol: canonical })}
          activeCanonical={activeCanonical}
          testIdPrefix="symbol-select"
        />
      )}
    </span>
  );
}
