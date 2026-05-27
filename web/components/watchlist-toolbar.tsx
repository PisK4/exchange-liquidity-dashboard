'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { MAX_WATCHLIST, addSymbol, applyURLState, removeSymbol, saveToLocalStorage } from '@/lib/watchlist';
import type { DashboardCategorySymbol } from '@/lib/api/client';
import { SymbolPickerDropdown } from './symbol-picker-dropdown';

// WatchlistToolbar renders the chip row + a "管理自选" button that
// opens the same SymbolPickerDropdown used by the global symbol pill in
// DashboardControls. Two equivalent entry points share one
// implementation so the operator's mental model — "starring is starring,
// no matter where I click" — never breaks.
//
// The component is intentionally state-less for the watchlist itself:
// the canonical list lives in the parent DashboardClient and is mirrored
// here only to render the chips. Add/remove always goes through
// addSymbol / removeSymbol so the dedupe + uppercase + max-length rules
// are enforced in one place; the toolbar also writes back to
// localStorage and replaceState the moment a change is committed so a
// tab-close / page-refresh round-trip preserves the operator's
// last-known intent.
export function WatchlistToolbar({
  items,
  symbols,
  onChange,
}: {
  items: string[];
  symbols: DashboardCategorySymbol[];
  onChange: (next: string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const lookup = useMemo(() => {
    const out = new Map<string, DashboardCategorySymbol>();
    for (const s of symbols) {
      out.set(s.canonical.toUpperCase(), s);
    }
    return out;
  }, [symbols]);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, []);

  function commit(next: string[]) {
    onChange(next);
    saveToLocalStorage(next);
    applyURLState(next);
  }

  function toggleFavorite(symbol: string) {
    const upper = symbol.toUpperCase();
    const isFav = items.some(s => s.toUpperCase() === upper);
    if (isFav) {
      commit(removeSymbol(items, upper));
    } else {
      if (items.length >= MAX_WATCHLIST) return;
      commit(addSymbol(items, upper));
    }
  }

  function remove(symbol: string) {
    commit(removeSymbol(items, symbol));
  }

  return (
    <div className="watchlist-toolbar" ref={ref} data-testid="watchlist-toolbar">
      <span className="watchlist-toolbar-label">自选清单</span>
      <span className="watchlist-chips">
        {items.length === 0 && <span className="watchlist-chip muted">（空）</span>}
        {items.map(symbol => {
          const meta = lookup.get(symbol.toUpperCase());
          return (
            <span className="watchlist-chip" key={symbol} data-testid={`watchlist-chip-${symbol.toUpperCase()}`}>
              <span className="watchlist-chip-label">{meta?.display_name ?? symbol}</span>
              <button
                type="button"
                className="watchlist-chip-remove"
                aria-label={`移除 ${meta?.display_name ?? symbol}`}
                onClick={() => remove(symbol)}
                data-testid={`watchlist-chip-remove-${symbol.toUpperCase()}`}
              >
                ×
              </button>
            </span>
          );
        })}
      </span>
      <span className="watchlist-add">
        <button
          ref={triggerRef}
          type="button"
          className={`pill watchlist-add-trigger ${open ? 'active' : ''}`}
          onClick={() => setOpen(o => !o)}
          aria-haspopup="listbox"
          aria-expanded={open}
          title="管理自选清单（最多 10 个标的）"
          data-testid="watchlist-add-trigger"
        >
          管理自选 ▾
        </button>
        {open && (
          <SymbolPickerDropdown
            symbols={symbols}
            favorites={items}
            onToggleFavorite={toggleFavorite}
            maxFavorites={MAX_WATCHLIST}
            triggerRef={triggerRef}
            onClose={() => setOpen(false)}
            testIdPrefix="watchlist-add"
          />
        )}
      </span>
    </div>
  );
}
