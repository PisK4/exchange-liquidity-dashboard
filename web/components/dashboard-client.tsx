'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { DashboardShell } from '@/components/dashboard-shell';
import {
  getFrontendURLLookup,
  getJSONWithFallback,
  type DashboardMeta,
  type FrontendURLLookup,
  type LiquiditySnapshot,
  type QualitySnapshot,
  type ShareSnapshot,
  type Top30DivergenceSnapshot,
  type Top30Snapshot,
} from '@/lib/api/client';
import {
  WATCHLIST_DEFAULT_FALLBACK,
  applyURLState,
  loadFromLocalStorage,
  normalizeSymbol,
  resolveWatchlist,
  saveToLocalStorage,
} from '@/lib/watchlist';

type Query = Record<string, string | undefined> & {
  symbol: string;
  window: string;
  platform: string;
};

type DashboardData = {
  meta: DashboardMeta;
  liquidity: LiquiditySnapshot;
  quality: QualitySnapshot;
  share: ShareSnapshot;
  top30: Top30Snapshot;
  top30Divergence: Top30DivergenceSnapshot;
  lookup: FrontendURLLookup;
  // liquidityByCanonical / qualityByCanonical carry one snapshot per
  // watchlist symbol when the watchlist contains more than one entry;
  // for single-entry lists they mirror the primary `liquidity` /
  // `quality` fields so the shell can read from either path without
  // branching at every consumer.
  //
  // Two parallel fan-outs are needed (not just one) because the
  // backend reducers populate different fields on each endpoint:
  // /snapshot/liquidity carries depth_by_tier + spread_bp, while
  // /snapshot/quality is the only place that fills worst_slippage_bp
  // (per USD bucket) and verdict. PlatformRow is a shared TS type
  // across both snapshots, which hides the difference from the type
  // system; a quick `curl /api/snapshot/liquidity?symbol=BTC` confirms
  // worst_slippage_bp comes back null on the liquidity side in
  // production. Without the quality fan-out the QualityCard mini
  // chart degrades to "该标的暂无可绘制的滑点数据" even though the
  // quality endpoint has the data.
  liquidityByCanonical: Record<string, LiquiditySnapshot>;
  qualityByCanonical: Record<string, QualitySnapshot>;
  watchlist: string[];
};

function buildPaths(query: Query) {
  const symbolParam = encodeURIComponent(query.symbol);
  return {
    meta: '/api/dashboard/meta',
    liquidity: `/api/snapshot/liquidity?symbol=${symbolParam}`,
    quality: `/api/snapshot/quality?symbol=${symbolParam}`,
    share: `/api/snapshot/share?window=${query.window}`,
    top30: `/api/snapshot/top30?surface=perp&platform=${query.platform}`,
    top30Divergence: '/api/snapshot/top30/divergence',
  };
}

export function DashboardClient({ query, initialWatchlist = [] }: { query: Query; initialWatchlist?: string[] }) {
  // The SSR pass resolved the watchlist purely from the URL. Once we
  // mount on the client we re-resolve with localStorage available and
  // — if the merged result differs — write the new value back into the
  // URL via replaceState so a refresh / share-link round-trip stays
  // consistent. We keep the initial SSR list as the first state so
  // hydration matches the server-rendered HTML exactly.
  const [watchlist, setWatchlist] = useState<string[]>(() => {
    if (initialWatchlist.length > 0) return initialWatchlist;
    return [normalizeSymbol(query.symbol || WATCHLIST_DEFAULT_FALLBACK)];
  });
  const [data, setData] = useState<DashboardData | null>(null);
  const [fatalError, setFatalError] = useState<Error | null>(null);
  const dataRef = useRef<DashboardData | null>(null);
  dataRef.current = data;

  // Post-mount: pull localStorage and reconcile with the SSR list.
  // pendingHydrationRef tells the side-effect bus to skip its very
  // next watchlist-driven run because that run was caused by the
  // hydration useEffect below (restoring a localStorage list on mount),
  // not by a user gesture. Without this gate the bus would re-write
  // ?symbol= to whatever localStorage held — clobbering a legitimate
  // deep-link like /share?symbol=BTC when localStorage happens to
  // carry an unrelated value from a prior session/test.
  const pendingHydrationRef = useRef(false);
  useEffect(() => {
    const stored = loadFromLocalStorage();
    const resolved = resolveWatchlist({
      urlParam: initialWatchlist.length > 0 ? initialWatchlist.join(',') : null,
      localStorage: stored,
      defaultSymbol: query.symbol || WATCHLIST_DEFAULT_FALLBACK,
    });
    setWatchlist(prev => {
      const same = prev.length === resolved.items.length && prev.every((v, i) => normalizeSymbol(v) === resolved.items[i]);
      if (same) return prev;
      pendingHydrationRef.current = true;
      return resolved.items;
    });
    // Mirror the resolved list back into URL + localStorage so the
    // session converges on a single source of truth. Pass syncSymbol
    // off (default) so a deep-link ?symbol=… isn't overwritten by a
    // stale localStorage chip.
    saveToLocalStorage(resolved.items);
    if (resolved.source !== 'url') {
      applyURLState(resolved.items);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Decision E: removing the last chip must not leave the operator with
  // a blank dashboard. We re-seed the BTC fallback when the watchlist
  // empties out, regardless of how it got there (chip-remove, an empty
  // ?watchlist= URL, or a corrupted localStorage value). The toolbar
  // intentionally stays a pure 'emit the new array' control — the
  // fallback policy belongs in the resolver layer, here.
  useEffect(() => {
    if (watchlist.length === 0) {
      setWatchlist([normalizeSymbol(WATCHLIST_DEFAULT_FALLBACK)]);
    }
  }, [watchlist]);

  // Side-effect bus: every watchlist mutation MUST sync to localStorage
  // (for next-session resume) and to the URL (for share-links). The
  // toolbar does this itself for chip add/remove, but the WatchlistCard
  // 'expand' button and any future programmatic setter would otherwise
  // bypass the sync. Doing it here in one place guarantees the three
  // sources of truth — URL, localStorage, React state — never diverge.
  const hasMountedRef = useRef(false);
  useEffect(() => {
    if (!hasMountedRef.current) {
      hasMountedRef.current = true;
      return;
    }
    // Hydration just re-set watchlist from localStorage; the hydration
    // useEffect already wrote URL + localStorage with the correct
    // (no-syncSymbol) semantics. Skip this run so we don't clobber
    // ?symbol= with the hydrated chip.
    if (pendingHydrationRef.current) {
      pendingHydrationRef.current = false;
      return;
    }
    saveToLocalStorage(watchlist);
    // syncSymbol:true so that when the user collapses to a single chip
    // (via toolbar chip-remove or WatchlistCard expand button) the
    // ?symbol= URL param tracks watchlist[0]; otherwise the V1 detail
    // panels would render whichever symbol's data was last fetched.
    applyURLState(watchlist, { syncSymbol: true });
  }, [watchlist]);

  // Single-chip mode preserves the V1 deep-link contract: clicking a
  // category pill or picking from the symbol dropdown sets ?symbol=X
  // without touching ?watchlist=. When the operator hasn't pinned a
  // multi-symbol list (initialWatchlist empty) we follow the URL so the
  // headline and the lone chip stay in lockstep. This effect is a
  // no-op when ?watchlist= is explicitly provided — that branch keeps
  // its own state untouched even if ?symbol= happens to disagree.
  useEffect(() => {
    if (initialWatchlist.length > 0) return;
    if (watchlist.length !== 1) return;
    const target = normalizeSymbol(query.symbol || WATCHLIST_DEFAULT_FALLBACK);
    if (watchlist[0] !== target) {
      setWatchlist([target]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query.symbol]);

  // Primary fetch path: query.symbol drives the headline liquidity /
  // quality / share / top30 calls — preserving the V1 ?symbol= deep-
  // link contract so category pills, symbol-dropdown clicks, and
  // legacy share URLs keep behaving exactly as they did before the
  // watchlist work landed. The watchlist is fanned out in parallel
  // into liquidityByCanonical so per-card rendering can read each
  // symbol independently without dragging the headline view along.
  //
  // effectiveSymbol couples the headline fetch to watchlist[0] when
  // the watchlist holds a single chip. applyURLState already rewrites
  // ?symbol= in that case so refreshes / share-links land on the same
  // view, but the live React tree won't pick up the URL change until
  // the next Next.js navigation — so we override query.symbol here too
  // and let the buildPaths memoization drive a refetch on collapse.
  // Multi-chip mode falls through to query.symbol unchanged because
  // there is no headline symbol in that mode (the QualityTab and
  // LiquidityTab both render card grids and ignore data.{liquidity,
  // quality} in that branch).
  //
  // When the single chip and query.symbol are merely a case-folding of
  // each other (e.g. query.symbol='BTC-USDT (perp)' vs watchlist[0]=
  // 'BTC-USDT (PERP)' after normalizeSymbol), we prefer query.symbol
  // verbatim. resolveSymbolContext is case-sensitive on legacy
  // display_symbols, so uppercasing them through normalizeSymbol
  // breaks the BTC-USDT (perp) → BTC canonical mapping that legacy
  // bookmarks rely on.
  const effectiveSymbol = watchlist.length === 1
    ? (normalizeSymbol(watchlist[0]) === normalizeSymbol(query.symbol ?? '')
        ? query.symbol
        : watchlist[0])
    : query.symbol;
  const effectiveQuery = useMemo(() => ({ ...query, symbol: effectiveSymbol }), [query, effectiveSymbol]);
  const paths = buildPaths(effectiveQuery);
  const watchlistKey = useMemo(() => watchlist.join(','), [watchlist]);
  const { meta: metaPath, liquidity: liquidityPath, quality: qualityPath, share: sharePath, top30: top30Path, top30Divergence: top30DivergencePath } = paths;

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    // fanOut handles either snapshot type with one generic helper —
    // identical concurrency model, single AbortController coverage,
    // and per-symbol swallow so one bad fetch doesn't blank the rest
    // of the cards.
    const fanOut = async <T,>(pathFor: (sym: string) => string): Promise<Record<string, T>> => {
      const out: Record<string, T> = {};
      await Promise.all(
        watchlist.map(async sym => {
          try {
            const snap = await getJSONWithFallback<T>(pathFor(sym), { signal: controller.signal });
            out[normalizeSymbol(sym)] = snap;
          } catch (err) {
            if (err instanceof DOMException && err.name === 'AbortError') throw err;
          }
        }),
      );
      return out;
    };

    const fetchAll = async () => {
      try {
        const [meta, liquidity, quality, share, top30, top30Divergence, lookup, liquidityByCanonical, qualityByCanonical] = await Promise.all([
          getJSONWithFallback<DashboardMeta>(metaPath, { signal: controller.signal }),
          getJSONWithFallback<LiquiditySnapshot>(liquidityPath, { signal: controller.signal }),
          getJSONWithFallback<QualitySnapshot>(qualityPath, { signal: controller.signal }),
          getJSONWithFallback<ShareSnapshot>(sharePath, { signal: controller.signal }),
          getJSONWithFallback<Top30Snapshot>(top30Path, { signal: controller.signal }),
          getJSONWithFallback<Top30DivergenceSnapshot>(top30DivergencePath, { signal: controller.signal }),
          getFrontendURLLookup(),
          fanOut<LiquiditySnapshot>(sym => `/api/snapshot/liquidity?symbol=${encodeURIComponent(sym)}`),
          fanOut<QualitySnapshot>(sym => `/api/snapshot/quality?symbol=${encodeURIComponent(sym)}`),
        ]);
        if (cancelled || controller.signal.aborted) return;
        // Ensure the headline snapshots are always reachable via the
        // per-symbol maps so card-mode consumers don't need to special-
        // case the first entry. Use effectiveSymbol so the mapping key
        // tracks the actual symbol whose data was just fetched (vs the
        // possibly-stale query.symbol from SSR props).
        const headlineCanonical = normalizeSymbol(effectiveSymbol);
        if (!liquidityByCanonical[headlineCanonical]) {
          liquidityByCanonical[headlineCanonical] = liquidity;
        }
        if (!qualityByCanonical[headlineCanonical]) {
          qualityByCanonical[headlineCanonical] = quality;
        }
        setData({ meta, liquidity, quality, share, top30, top30Divergence, lookup, liquidityByCanonical, qualityByCanonical, watchlist });
      } catch (err) {
        if (cancelled || controller.signal.aborted) return;
        if (err instanceof DOMException && err.name === 'AbortError') return;
        if (!dataRef.current) {
          setFatalError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    };

    fetchAll();

    const intervalMs = (dataRef.current?.meta.refresh_interval_sec || 30) * 1000;
    const timer = window.setInterval(fetchAll, intervalMs);
    return () => {
      cancelled = true;
      controller.abort();
      window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [metaPath, liquidityPath, qualityPath, sharePath, top30Path, top30DivergencePath, watchlistKey, data?.meta.refresh_interval_sec]);

  if (fatalError && !data) {
    throw fatalError;
  }

  if (!data) {
    return null;
  }

  return (
    <DashboardShell
      query={effectiveQuery}
      data={data}
      watchlist={watchlist}
      onWatchlistChange={setWatchlist}
    />
  );
}
