import { loadCached, saveCached } from './cache';

export type ApiStatus = 'complete' | 'partial' | 'aggregated_orderbook' | 'ws_limited_depth' | 'stale' | 'unsupported' | 'error' | 'insufficient_history';
export type DataFreshness = 'live' | 'delayed';

export type SymbolMapping = {
  display_symbol: string;
  canonical: string;
  market_surface: string;
  instrument_kind: string;
  platform: string;
  api_symbol: string;
  base_asset: string;
  quote_asset: string;
  settle_asset: string;
  source_endpoint: string;
  contract_id?: string;
  market_id?: number;
  contract_size?: number;
  quanto_multiplier?: number;
  api_level_cap?: number;
  frontend_url?: string;
  url_verified?: boolean;
  catalog_status?: string;
};

export type SymbolsResponse = {
  symbols: string[];
  mappings: SymbolMapping[];
};

export type FrontendURLLookup = (platform: string, displaySymbol: string) => string | undefined;

export async function getFrontendURLLookup(): Promise<FrontendURLLookup> {
  try {
    const data = await getJSONWithFallback<SymbolsResponse>('/api/symbols');
    const idx = new Map<string, string>();
    for (const m of data.mappings ?? []) {
      if (m.frontend_url) {
        idx.set(`${m.platform}::${m.display_symbol}`, m.frontend_url);
      }
    }
    return (platform: string, displaySymbol: string) => idx.get(`${platform}::${displaySymbol}`);
  } catch {
    return () => undefined;
  }
}

export type PlatformRow = {
  platform: string;
  display_symbol: string;
  snapshot_ts: string;
  source_endpoint: string;
  depth_status: ApiStatus;
  partial_reason?: string;
  error?: string;
  data_freshness?: DataFreshness;
  last_collection_status?: ApiStatus;
  last_collection_error?: string;
  last_collection_ts?: string;
  mid_price?: number;
  spread_bp?: number;
  imbalance_pct?: number;
  depth_by_tier: Record<string, DepthTierMetrics>;
  vs_median_by_tier?: Record<string, number>;
  rank_0_1?: number;
  depth_status_label?: string;
  buy_slippage_bp: Record<string, number>;
  sell_slippage_bp: Record<string, number>;
  worst_slippage_bp?: Record<string, number>;
  verdict?: string;
};

export type DepthTierMetrics = {
  bid_usd: number;
  ask_usd: number;
  total_usd: number;
  depth_status?: ApiStatus;
  partial_reason?: string;
  depth_source?: string;
  source_id?: string;
  source_endpoint?: string;
  levels_returned?: number;
  bid_levels_returned?: number;
  ask_levels_returned?: number;
  api_level_cap?: number;
  farthest_bid_pct?: number;
  farthest_ask_pct?: number;
  farthest_distance_pct?: number;
  aggregation_params?: Record<string, string>;
};

export type CoinGeckoLineage = {
  enabled: boolean;
  exchange_ids?: string[];
  market_names?: string[];
  pull_interval?: string;
  last_pull_ts?: string;
};

export type DataSources = {
  coingecko?: CoinGeckoLineage;
};

export type DashboardMeta = {
  tabs: string[];
  platforms: string[];
  symbols: string[];
  windows: string[];
  depth_tiers: number[];
  slippage_buckets_usd: number[];
  refresh_interval_sec: number;
  volume_discounts: Record<string, number>;
  data_sources?: DataSources;
};

export type LiquiditySnapshot = {
  symbol: string;
  snapshot_ts: string;
  rows: PlatformRow[];
  competitor_median_by_tier?: Record<string, number>;
  kpis?: {
    edgex_depth_by_tier?: Record<string, DepthTierMetrics>;
    edgex_vs_median_by_tier?: Record<string, number>;
    edgex_spread_bp?: number;
    edgex_spread_10m_status?: ApiStatus;
    edgex_24h_share_pct?: number;
    symbol_share_7d_status?: ApiStatus;
    symbol_share_wow_status?: ApiStatus;
  };
};

export type QualitySnapshot = {
  symbol: string;
  snapshot_ts: string;
  slippage_buckets_usd: number[];
  rows: PlatformRow[];
};

export type ShareRow = {
  rank?: number;
  platform: string;
  raw_volume_usd?: number;
  adjusted_volume_usd?: number;
  adjusted_volume_24h_usd?: number;
  share_pct?: number;
  denominator_pct?: number;
  discount?: number;
  status?: ApiStatus;
  data_source?: 'native' | 'coingecko' | string;
  days_seen?: number;
  days_window?: number;
};

export type ShareSnapshot = {
  window: string;
  status?: ApiStatus;
  reason?: string;
  snapshot_ts: string;
  denominator_usd?: number;
  rows: ShareRow[];
  kpis?: {
    edgex_share_pct?: number;
    edgex_total_volume_usd?: number;
    denominator_usd?: number;
  };
  trend?: { status?: ApiStatus; points?: Array<Record<string, number | string>> };
};

export type Top30Row = {
  rank: number;
  platform: string;
  symbol: string;
  volume_24h_usd?: number;
  volume_7d_usd?: number;
  delta_7d_pct?: number;
  volume_7d_status?: ApiStatus;
  delta_7d_status?: ApiStatus;
  edgex_listed?: boolean;
  edgex_listed_status?: ApiStatus;
  competitor_top30_coverage?: number;
  competitor_top30_coverage_status?: ApiStatus;
  suggested_action?: string;
  suggested_action_status?: ApiStatus;
  status?: ApiStatus;
  data_source?: 'native' | 'coingecko' | string;
  source_endpoint?: string;
  snapshot_ts?: string;
  error?: string;
};

export type Top30Snapshot = {
  surface: string;
  platform: string;
  snapshot_ts: string;
  status?: ApiStatus;
  rows: Top30Row[];
};

const SERVER_API_BASE = process.env.SERVER_API_BASE ?? process.env.NEXT_PUBLIC_API_BASE ?? 'http://127.0.0.1:8080';
const BROWSER_API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? '';

function apiBase(): string {
  return typeof window === 'undefined' ? SERVER_API_BASE : BROWSER_API_BASE;
}

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${apiBase()}${path}`, { cache: 'no-store' });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export async function getJSONWithFallback<T>(path: string, key: string = path): Promise<T> {
  try {
    const data = await getJSON<T>(path);
    saveCached(key, data);
    return data;
  } catch (err) {
    const cached = loadCached<T>(key);
    if (cached !== null) return cached;
    throw err;
  }
}

export const symbols = ['BTC-USDT (perp)', 'ETH-USDT (perp)', 'SOL-USDT (perp)'];

export function money(value?: number) {
  if (typeof value !== 'number') return '—';
  if (value === 0) return '$0';
  if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`;
  if (value >= 1_000_000) return `$${(value / 1_000_000).toFixed(2)}M`;
  return `$${value.toFixed(0)}`;
}

export function moneyM(value?: number) {
  return typeof value === 'number' ? `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 1 : 2)}M` : '—';
}

export function pct(value?: number) { return typeof value === 'number' ? `${value.toFixed(2)}%` : '—'; }
export function bp(value?: number) { return typeof value === 'number' && value > 0 ? `${value.toFixed(2)} bp` : '—'; }
export function ratio(value?: number) { return typeof value === 'number' && value > 0 ? `${value.toFixed(2)}×` : '—'; }
