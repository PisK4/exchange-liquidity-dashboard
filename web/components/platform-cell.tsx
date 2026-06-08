import type { FrontendURLLookup } from '@/lib/api/client';

export type PlatformSurfaceMeta = {
  platform: string;
  display_symbol?: string;
  display_platform?: string;
  is_edgex?: boolean;
  market_surface?: string;
  lineage?: string;
  venue_symbol?: string;
  contract_id?: string;
};

export function platformDisplayName(platform: string) {
  return platform === 'edgeX' ? 'edgeX ★' : platform;
}

export function platformDisplayLabel(row: PlatformSurfaceMeta) {
  return row.display_platform || platformDisplayName(row.platform);
}

export function isSelfPlatform(row: PlatformSurfaceMeta) {
  return row.is_edgex ?? row.platform === 'edgeX';
}

export function platformRowKey(row: PlatformSurfaceMeta) {
  const displaySymbol = row.display_symbol ?? '';
  if (!row.market_surface && !row.lineage && !row.venue_symbol && !row.contract_id) {
    return `${row.platform}::${displaySymbol}`;
  }
  return `${row.platform}::${displaySymbol}::${row.market_surface ?? ''}::${row.lineage ?? ''}::${row.venue_symbol ?? ''}::${row.contract_id ?? ''}`;
}

export function PlatformCell({
  platform,
  displaySymbol,
  lookup,
  displayPlatform,
  isEdgex,
  marketSurface,
  lineage,
  venueSymbol,
  contractId,
}: {
  platform: string;
  displaySymbol: string;
  lookup: FrontendURLLookup;
  displayPlatform?: string;
  isEdgex?: boolean;
  marketSurface?: string;
  lineage?: string;
  venueSymbol?: string;
  contractId?: string;
}) {
  const href = lookup(platform, displaySymbol, { marketSurface, lineage, venueSymbol, contractId });
  const label = platformDisplayLabel({ platform, display_platform: displayPlatform });
  const self = isSelfPlatform({ platform, is_edgex: isEdgex });
  const className = self ? 'platform-self' : undefined;
  if (!href) return <span className={className}>{label}</span>;
  return (
    <a className={`platform-link ${className ?? ''}`} href={href} target="_blank" rel="noopener noreferrer" title={`Open ${platform} ${displaySymbol} page`}>
      {label} <span className="ext-icon" aria-hidden="true">↗</span>
    </a>
  );
}
