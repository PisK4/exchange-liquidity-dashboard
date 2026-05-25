import type { components } from './types.gen';

type Schema<Name extends keyof components['schemas']> = components['schemas'][Name];

export type ApiStatus = Schema<'ApiStatus'>;
export type DataFreshness = Schema<'DataFreshness'>;
export type AssetCategoryKey = string;
export type SymbolMapping = Schema<'SymbolMapping'>;
export type DashboardCategorySymbol = Schema<'DashboardCategorySymbol'>;
export type DashboardCategory = Schema<'DashboardCategory'>;
export type SymbolsResponse = Schema<'SymbolsResponse'>;
export type PlatformRow = Schema<'PlatformRow'>;
export type DepthTierMetrics = Schema<'DepthTierMetrics'>;
export type CoinGeckoLineage = Schema<'CoinGeckoLineage'>;
export type DataSources = Schema<'DataSources'>;
export type DashboardMeta = Schema<'DashboardMeta'>;
export type LiquiditySnapshot = Schema<'LiquiditySnapshot'>;
export type QualitySnapshot = Schema<'QualitySnapshot'>;
export type ShareRow = Schema<'ShareRow'>;
export type ShareSnapshot = Schema<'ShareSnapshot'>;
export type Top30Row = Schema<'Top30Row'>;
export type Top30Snapshot = Schema<'Top30Snapshot'>;
