'use client';

import Link from 'next/link';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { DashboardCategorySymbol } from '@/lib/api/client';

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
}: {
  symbols: DashboardCategorySymbol[];
  activeCanonical: string;
  query: Query;
}) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const [highlight, setHighlight] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const active = symbols.find(s => s.canonical === activeCanonical);
  const filtered = useMemo(() => {
    const f = filter.trim().toLowerCase();
    if (!f) return symbols;
    return symbols.filter(
      s =>
        s.canonical.toLowerCase().includes(f) ||
        s.display_name.toLowerCase().includes(f) ||
        s.display_symbol.toLowerCase().includes(f),
    );
  }, [symbols, filter]);

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
      setHighlight(0);
      const id = window.setTimeout(() => inputRef.current?.focus(), 0);
      return () => window.clearTimeout(id);
    }
  }, [open]);

  useEffect(() => {
    setHighlight(0);
  }, [filter]);

  const label = active?.display_name ?? activeCanonical ?? '—';

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setHighlight(h => Math.min(h + 1, Math.max(filtered.length - 1, 0)));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlight(h => Math.max(h - 1, 0));
    } else if (e.key === 'Escape') {
      e.preventDefault();
      setOpen(false);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const target = filtered[highlight];
      if (target) {
        window.location.href = href(query, { symbol: target.canonical });
      }
    }
  }

  return (
    <span className="symbol-select" ref={ref}>
      <button
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
        <div className="symbol-select-dropdown" role="listbox" data-testid="symbol-select-dropdown">
          <input
            ref={inputRef}
            type="text"
            className="symbol-select-input"
            placeholder="搜索 BTC / GOLD / TSLA..."
            value={filter}
            onChange={e => setFilter(e.target.value)}
            onKeyDown={onKeyDown}
            aria-label="搜索交易对"
            data-testid="symbol-select-input"
          />
          <ul className="symbol-select-list">
            {filtered.length === 0 && <li className="symbol-select-empty">没有匹配的标的</li>}
            {filtered.map((s, idx) => {
              const isActive = s.canonical === activeCanonical;
              const isHighlight = idx === highlight;
              return (
                <li key={s.canonical}>
                  <Link
                    className={`symbol-select-option ${isActive ? 'active' : ''} ${isHighlight ? 'hl' : ''}`}
                    href={href(query, { symbol: s.canonical })}
                    onClick={() => setOpen(false)}
                    onMouseEnter={() => setHighlight(idx)}
                    role="option"
                    aria-selected={isActive}
                    data-testid={`symbol-select-option-${s.canonical}`}
                  >
                    <span className="symbol-select-name">{s.display_name}</span>
                    <span className="symbol-select-meta">{s.supported_platform_count} 平台</span>
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </span>
  );
}
