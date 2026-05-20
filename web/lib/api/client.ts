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
    const data = await getJSON<SymbolsResponse>('/api/symbols');
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
  depth_by_tier: Record<string, { bid_usd: number; ask_usd: number; total_usd: number }>;
  buy_slippage_bp: Record<string, number>;
  sell_slippage_bp: Record<string, number>;
};

const SERVER_API_BASE = process.env.SERVER_API_BASE ?? process.env.NEXT_PUBLIC_API_BASE ?? 'http://127.0.0.1:8080';

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${SERVER_API_BASE}${path}`, { cache: 'no-store' });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export const symbols = ['BTC-USDT (perp)', 'ETH-USDT (perp)', 'SOL-USDT (perp)'];

export function money(value?: number) {
  if (!value) return '-';
  if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`;
  if (value >= 1_000_000) return `$${(value / 1_000_000).toFixed(2)}M`;
  return `$${value.toFixed(0)}`;
}

export function pct(value?: number) { return typeof value === 'number' ? `${value.toFixed(2)}%` : '-'; }
export function bp(value?: number) { return typeof value === 'number' && value > 0 ? `${value.toFixed(2)} bp` : '-'; }
