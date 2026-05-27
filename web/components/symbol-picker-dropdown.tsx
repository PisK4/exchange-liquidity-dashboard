'use client';

import Link from 'next/link';
import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import type { DashboardCategorySymbol } from '@/lib/api/client';

// SymbolPickerDropdown is the shared dropdown body used by both the
// global SymbolSearchSelect trigger (the "BTC-USD" pill in the
// DashboardControls bar) and the WatchlistToolbar's "管理自选" trigger.
// It renders the search input + sorted option list + per-row favorite
// (★) button. Two equivalent entry points share one implementation so
// every UX detail — flip-up placement, scrollable list, keyboard
// navigation, favorites sorting, MAX cap behaviour — stays in lockstep.
//
// The component is fully controlled: it never owns "open" state. Each
// trigger decides when to mount it, and passes a `triggerRef` so the
// picker can compute flip-up placement against that exact element.
export type SymbolPickerDropdownProps = {
  symbols: DashboardCategorySymbol[];
  favorites: string[];                            // canonical, uppercased
  onToggleFavorite: (canonical: string) => void;
  maxFavorites: number;
  triggerRef: RefObject<HTMLElement | null>;
  onClose: () => void;
  // Optional navigation: when provided, clicking a row body navigates
  // (sets ?symbol=X via Next.js Link). Toolbar use-case omits this and
  // routes the row click straight to the favorite toggle instead, so
  // there is no dead surface in either dropdown.
  buildHref?: (canonical: string) => string;
  // Highlight the currently-active headline symbol. Orthogonal to the
  // favorite state — a row can be "current view" + "starred", or just
  // one, or neither.
  activeCanonical?: string;
  // testid scope so two dropdowns rendered in the same DOM tree don't
  // collide. Defaults to 'symbol-picker'.
  testIdPrefix?: string;
};

function isFavorite(favorites: string[], canonical: string): boolean {
  const upper = canonical.toUpperCase();
  return favorites.some(f => f.toUpperCase() === upper);
}

export function SymbolPickerDropdown({
  symbols,
  favorites,
  onToggleFavorite,
  maxFavorites,
  triggerRef,
  onClose,
  buildHref,
  activeCanonical,
  testIdPrefix = 'symbol-picker',
}: SymbolPickerDropdownProps) {
  const [filter, setFilter] = useState('');
  const [highlight, setHighlight] = useState(0);
  const [placement, setPlacement] = useState<'down' | 'up'>('down');
  const inputRef = useRef<HTMLInputElement>(null);
  const optionRefs = useRef<(HTMLLIElement | null)[]>([]);

  // Filter first, then sort favorites to the top so the operator's
  // long-tail picks become a one-jump landing zone every time the
  // dropdown opens.
  const ordered = useMemo(() => {
    const f = filter.trim().toLowerCase();
    const matched = symbols.filter(s => {
      if (!f) return true;
      return (
        s.canonical.toLowerCase().includes(f) ||
        s.display_name.toLowerCase().includes(f) ||
        s.display_symbol.toLowerCase().includes(f)
      );
    });
    const fav: DashboardCategorySymbol[] = [];
    const rest: DashboardCategorySymbol[] = [];
    for (const s of matched) {
      (isFavorite(favorites, s.canonical) ? fav : rest).push(s);
    }
    return { fav, rest, total: fav.length + rest.length };
  }, [symbols, filter, favorites]);

  const flatList = useMemo(() => [...ordered.fav, ...ordered.rest], [ordered]);

  useEffect(() => {
    const id = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(id);
  }, []);

  useEffect(() => {
    setHighlight(0);
  }, [filter]);

  useEffect(() => {
    const trigger = triggerRef.current;
    if (!trigger) return;
    const rect = trigger.getBoundingClientRect();
    const estimated = Math.min(window.innerHeight * 0.6, 480);
    const spaceBelow = window.innerHeight - rect.bottom;
    setPlacement(spaceBelow < estimated && rect.top > spaceBelow ? 'up' : 'down');
  }, [triggerRef]);

  useEffect(() => {
    optionRefs.current[highlight]?.scrollIntoView({ block: 'nearest' });
  }, [highlight]);

  function handleToggle(canonical: string) {
    onToggleFavorite(canonical);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setHighlight(h => Math.min(h + 1, Math.max(flatList.length - 1, 0)));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlight(h => Math.max(h - 1, 0));
    } else if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const target = flatList[highlight];
      if (!target) return;
      if (buildHref) {
        window.location.href = buildHref(target.canonical);
      } else {
        handleToggle(target.canonical);
      }
    }
  }

  const capReached = favorites.length >= maxFavorites;

  function renderRow(s: DashboardCategorySymbol, idx: number) {
    const fav = isFavorite(favorites, s.canonical);
    const isActive = activeCanonical === s.canonical;
    const isHighlight = idx === highlight;
    const starDisabled = !fav && capReached;
    const star = (
      <button
        type="button"
        className={`symbol-select-star ${fav ? 'on' : ''}`}
        aria-pressed={fav}
        aria-label={fav ? `从自选移除 ${s.display_name}` : `加入自选 ${s.display_name}`}
        title={
          starDisabled
            ? `自选清单最多 ${maxFavorites} 个标的`
            : fav
            ? '从自选移除'
            : '加入自选'
        }
        disabled={starDisabled}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          if (starDisabled) return;
          handleToggle(s.canonical);
        }}
        data-testid={`${testIdPrefix}-star-${s.canonical}`}
      >
        {fav ? '★' : '☆'}
      </button>
    );
    const body = (
      <>
        <span className="symbol-select-name">{s.display_name}</span>
        <span className="symbol-select-meta">{s.supported_platform_count} 平台</span>
      </>
    );
    return (
      <li key={s.canonical} ref={el => { optionRefs.current[idx] = el; }}>
        <span className={`symbol-select-row ${isActive ? 'active' : ''} ${isHighlight ? 'hl' : ''}`}>
          {buildHref ? (
            <Link
              className="symbol-select-option"
              href={buildHref(s.canonical)}
              onClick={() => onClose()}
              onMouseEnter={() => setHighlight(idx)}
              role="option"
              aria-selected={isActive}
              data-testid={`${testIdPrefix}-option-${s.canonical}`}
            >
              {body}
            </Link>
          ) : (
            <button
              type="button"
              className="symbol-select-option"
              onClick={() => handleToggle(s.canonical)}
              onMouseEnter={() => setHighlight(idx)}
              role="option"
              aria-selected={fav}
              data-testid={`${testIdPrefix}-option-${s.canonical}`}
            >
              {body}
            </button>
          )}
          {star}
        </span>
      </li>
    );
  }

  return (
    <div
      className={`symbol-select-dropdown ${placement === 'up' ? 'up' : ''}`}
      role="listbox"
      data-testid={`${testIdPrefix}-dropdown`}
    >
      <input
        ref={inputRef}
        type="text"
        className="symbol-select-input"
        placeholder="搜索 BTC / GOLD / TSLA..."
        value={filter}
        onChange={e => setFilter(e.target.value)}
        onKeyDown={onKeyDown}
        aria-label="搜索交易对"
        data-testid={`${testIdPrefix}-input`}
      />
      <ul className="symbol-select-list">
        {ordered.total === 0 && <li className="symbol-select-empty">没有匹配的标的</li>}
        {ordered.fav.length > 0 && (
          <li className="symbol-select-group" aria-hidden>
            已收藏 ({ordered.fav.length})
          </li>
        )}
        {ordered.fav.map((s, idx) => renderRow(s, idx))}
        {ordered.rest.length > 0 && ordered.fav.length > 0 && (
          <li className="symbol-select-group" aria-hidden>
            全部 ({ordered.rest.length})
          </li>
        )}
        {ordered.rest.map((s, idx) => renderRow(s, ordered.fav.length + idx))}
      </ul>
    </div>
  );
}
