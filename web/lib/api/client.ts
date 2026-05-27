import { getJSONWithFallback } from './fetcher';
import type { SymbolsResponse } from './types';

export { getJSON, getJSONWithFallback } from './fetcher';
export type * from './types';

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

export const symbols = ['BTC-USDT (perp)', 'ETH-USDT (perp)', 'SOL-USDT (perp)'];

export function money(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  if (value === 0) return '$0';
  const abs = Math.abs(value);
  const sign = value < 0 ? '-' : '';
  if (abs >= 1_000_000_000) return `${sign}$${(abs / 1_000_000_000).toFixed(2)}B`;
  if (abs >= 1_000_000) return `${sign}$${(abs / 1_000_000).toFixed(2)}M`;
  if (abs >= 1_000) return `${sign}$${(abs / 1_000).toFixed(2)}k`;
  if (abs >= 100) return `${sign}$${abs.toFixed(0)}`;
  if (abs >= 10) return `${sign}$${abs.toFixed(1)}`;
  if (abs >= 1) return `${sign}$${abs.toFixed(2)}`;
  if (abs >= 0.01) return `${sign}$${abs.toFixed(2)}`;
  return `${sign}<$0.01`;
}

export function usdLabel(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '$—';
  if (value === 0) return '$0';
  const abs = Math.abs(value);
  const sign = value < 0 ? '-' : '';
  if (abs < 0.01) return `${sign}<$0.01`;
  if (abs < 10) return `${sign}$${abs.toFixed(2)}`;
  if (abs < 100) return `${sign}$${abs.toFixed(1)}`;
  return `${sign}$${abs.toFixed(0)}`;
}

export function moneyAuto(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  if (value === 0) return '0';
  const abs = Math.abs(value);
  const sign = value < 0 ? '-' : '';
  const fmt = (v: number) => v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2);
  if (abs >= 1_000_000_000) return `${sign}${fmt(abs / 1_000_000_000)}B`;
  if (abs >= 1_000_000) return `${sign}${fmt(abs / 1_000_000)}M`;
  if (abs >= 1_000) return `${sign}${fmt(abs / 1_000)}k`;
  if (abs >= 1) return `${sign}${abs.toFixed(0)}`;
  return `${sign}${abs.toFixed(2)}`;
}

export function pct(value?: number) { return typeof value === 'number' ? `${value.toFixed(2)}%` : '—'; }
export function bp(value?: number) { return typeof value === 'number' && value > 0 ? `${value.toFixed(2)} bp` : '—'; }
export function ratio(value?: number) { return typeof value === 'number' && value > 0 ? `${value.toFixed(2)}×` : '—'; }

export function fundingRatePct(value?: number | null): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  const formatted = (value * 100).toFixed(4);
  return value >= 0 ? `+${formatted}%` : `${formatted}%`;
}

export function fundingRateDeltaPct(value?: number | null): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  const formatted = (value * 100).toFixed(4);
  return value >= 0 ? `+${formatted}%` : `${formatted}%`;
}

export function fundingPeriodLabel(periodHours?: number): string {
  if (!periodHours || periodHours <= 0) return '—';
  return `${periodHours}h`;
}
