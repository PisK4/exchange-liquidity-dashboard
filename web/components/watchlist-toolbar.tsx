'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { MAX_WATCHLIST, addSymbol, applyURLState, removeSymbol, saveToLocalStorage } from '@/lib/watchlist';
import type { DashboardCategorySymbol } from '@/lib/api/client';

// WatchlistToolbar renders the chip row + 'add' button that drives the
// multi-symbol Liquidity view. It is intentionally state-less — the
// canonical watchlist lives in the parent DashboardClient — and only
// emits onChange with the new array. The component owns the dropdown
// open/closed state and the search filter because those are pure UI
// concerns nobody else needs to know about.
//
// Adding / removing always goes through addSymbol / removeSymbol so the
// dedupe + uppercase + max-length rules are enforced in one place; the
// toolbar also writes back to localStorage and replaceState the moment
// the change is committed so a tab-close / page-refresh round-trip
// preserves the operator's last-known intent.
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
  const [filter, setFilter] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const lookup = useMemo(() => {
    const out = new Map<string, DashboardCategorySymbol>();
    for (const s of symbols) {
      out.set(s.canonical.toUpperCase(), s);
    }
    return out;
  }, [symbols]);

  const filtered = useMemo(() => {
    const f = filter.trim().toLowerCase();
    const alreadyIn = new Set(items.map(s => s.toUpperCase()));
    return symbols
      .filter(s => !alreadyIn.has(s.canonical.toUpperCase()))
      .filter(s => {
        if (!f) return true;
        return (
          s.canonical.toLowerCase().includes(f) ||
          s.display_name.toLowerCase().includes(f) ||
          s.display_symbol.toLowerCase().includes(f)
        );
      });
  }, [symbols, items, filter]);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, []);

  useEffect(() => {
    if (open) {
      setFilter('');
      const id = window.setTimeout(() => inputRef.current?.focus(), 0);
      return () => window.clearTimeout(id);
    }
  }, [open]);

  function commit(next: string[]) {
    onChange(next);
    saveToLocalStorage(next);
    applyURLState(next);
  }

  function add(symbol: string) {
    if (items.length >= MAX_WATCHLIST) return;
    commit(addSymbol(items, symbol));
    setOpen(false);
  }

  function remove(symbol: string) {
    commit(removeSymbol(items, symbol));
  }

  const capped = items.length >= MAX_WATCHLIST;

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
          type="button"
          className={`pill watchlist-add-trigger ${open ? 'active' : ''}`}
          onClick={() => setOpen(o => !o)}
          aria-haspopup="listbox"
          aria-expanded={open}
          disabled={capped}
          title={capped ? `最多 ${MAX_WATCHLIST} 个标的` : '添加标的到自选'}
          data-testid="watchlist-add-trigger"
        >
          + 添加标的
        </button>
        {open && !capped && (
          <div className="symbol-select-dropdown" role="listbox" data-testid="watchlist-add-dropdown">
            <input
              ref={inputRef}
              type="text"
              className="symbol-select-input"
              placeholder="搜索 BTC / GOLD / TSLA..."
              value={filter}
              onChange={e => setFilter(e.target.value)}
              aria-label="搜索可添加的标的"
            />
            <ul className="symbol-select-list">
              {filtered.length === 0 && <li className="symbol-select-empty">没有可添加的标的</li>}
              {filtered.map(s => (
                <li key={s.canonical}>
                  <button
                    type="button"
                    className="symbol-select-option"
                    onClick={() => add(s.canonical)}
                    data-testid={`watchlist-add-option-${s.canonical}`}
                  >
                    <span className="symbol-select-name">{s.display_name}</span>
                    <span className="symbol-select-meta">{s.supported_platform_count} 平台</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </span>
    </div>
  );
}
