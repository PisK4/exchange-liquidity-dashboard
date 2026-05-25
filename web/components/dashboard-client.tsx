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
  lookup: FrontendURLLookup;
  // liquidityByCanonical carries one snapshot per watchlist symbol when
  // the watchlist contains more than one entry; for single-entry lists
  // it mirrors the primary `liquidity` field so the shell can read from
  // either path without branching at every consumer.
  liquidityByCanonical: Record<string, LiquiditySnapshot>;
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
      return resolved.items;
    });
    // Mirror the resolved list back into URL + localStorage so the
    // session converges on a single source of truth.
    saveToLocalStorage(resolved.items);
    if (resolved.source !== 'url') {
      applyURLState(resolved.items);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Primary fetch path: still uses query.symbol for share / top30 /
  // meta (those are watchlist-agnostic) and the first watchlist entry
  // for the headline liquidity/quality view. The full fan-out is
  // performed in parallel into liquidityByCanonical so per-card
  // rendering can read each symbol independently.
  const headlineSymbol = watchlist[0] ?? query.symbol;
  const effectiveQuery: Query = useMemo(() => ({ ...query, symbol: headlineSymbol }), [query, headlineSymbol]);
  const paths = buildPaths(effectiveQuery);
  const watchlistKey = useMemo(() => watchlist.join(','), [watchlist]);
  const { meta: metaPath, liquidity: liquidityPath, quality: qualityPath, share: sharePath, top30: top30Path } = paths;

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    const fanOutLiquidity = async (): Promise<Record<string, LiquiditySnapshot>> => {
      const out: Record<string, LiquiditySnapshot> = {};
      await Promise.all(
        watchlist.map(async sym => {
          try {
            const snap = await getJSONWithFallback<LiquiditySnapshot>(
              `/api/snapshot/liquidity?symbol=${encodeURIComponent(sym)}`,
              { signal: controller.signal },
            );
            out[normalizeSymbol(sym)] = snap;
          } catch (err) {
            if (err instanceof DOMException && err.name === 'AbortError') throw err;
            // Per-symbol fetch failure should not blank out the others;
            // the card renders 'unavailable' on its own.
          }
        }),
      );
      return out;
    };

    const fetchAll = async () => {
      try {
        const [meta, liquidity, quality, share, top30, lookup, liquidityByCanonical] = await Promise.all([
          getJSONWithFallback<DashboardMeta>(metaPath, { signal: controller.signal }),
          getJSONWithFallback<LiquiditySnapshot>(liquidityPath, { signal: controller.signal }),
          getJSONWithFallback<QualitySnapshot>(qualityPath, { signal: controller.signal }),
          getJSONWithFallback<ShareSnapshot>(sharePath, { signal: controller.signal }),
          getJSONWithFallback<Top30Snapshot>(top30Path, { signal: controller.signal }),
          getFrontendURLLookup(),
          fanOutLiquidity(),
        ]);
        if (cancelled || controller.signal.aborted) return;
        // Ensure the headline snapshot is always reachable via the
        // per-symbol map so card-mode consumers don't need to special-
        // case the first entry.
        const headlineCanonical = normalizeSymbol(headlineSymbol);
        if (!liquidityByCanonical[headlineCanonical]) {
          liquidityByCanonical[headlineCanonical] = liquidity;
        }
        setData({ meta, liquidity, quality, share, top30, lookup, liquidityByCanonical, watchlist });
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
  }, [metaPath, liquidityPath, qualityPath, sharePath, top30Path, watchlistKey, data?.meta.refresh_interval_sec]);

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
