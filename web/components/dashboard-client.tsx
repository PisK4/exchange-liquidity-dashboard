'use client';

import { useEffect, useRef, useState } from 'react';
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
};

function buildPaths(query: Query) {
  // The backend ResolveSymbol() accepts either a canonical (BTC) or a
  // legacy display_symbol ("BTC-USDT (perp)"), so we forward the URL
  // parameter as-is. This preserves bookmarks pointing to the V1 URL
  // shape while letting the new dropdown emit canonical-only links.
  const symbolParam = encodeURIComponent(query.symbol);
  return {
    meta: '/api/dashboard/meta',
    liquidity: `/api/snapshot/liquidity?symbol=${symbolParam}`,
    quality: `/api/snapshot/quality?symbol=${symbolParam}`,
    share: `/api/snapshot/share?window=${query.window}`,
    top30: `/api/snapshot/top30?surface=perp&platform=${query.platform}`,
  };
}

export function DashboardClient({ query }: { query: Query }) {
  const [data, setData] = useState<DashboardData | null>(null);
  const [fatalError, setFatalError] = useState<Error | null>(null);
  const dataRef = useRef<DashboardData | null>(null);
  dataRef.current = data;

  const paths = buildPaths(query);
  const { meta: metaPath, liquidity: liquidityPath, quality: qualityPath, share: sharePath, top30: top30Path } = paths;

  useEffect(() => {
    let cancelled = false;

    const fetchAll = async () => {
      try {
        const [meta, liquidity, quality, share, top30, lookup] = await Promise.all([
          getJSONWithFallback<DashboardMeta>(metaPath),
          getJSONWithFallback<LiquiditySnapshot>(liquidityPath),
          getJSONWithFallback<QualitySnapshot>(qualityPath),
          getJSONWithFallback<ShareSnapshot>(sharePath),
          getJSONWithFallback<Top30Snapshot>(top30Path),
          getFrontendURLLookup(),
        ]);
        if (cancelled) return;
        setData({ meta, liquidity, quality, share, top30, lookup });
      } catch (err) {
        if (cancelled) return;
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
      window.clearInterval(timer);
    };
  }, [metaPath, liquidityPath, qualityPath, sharePath, top30Path, data?.meta.refresh_interval_sec]);

  if (fatalError && !data) {
    throw fatalError;
  }

  if (!data) {
    return null;
  }

  return <DashboardShell query={query} data={data} />;
}
