import type { FrontendURLLookup } from '@/lib/api/client';

export function platformDisplayName(platform: string) {
  return platform === 'edgeX' ? 'edgeX ★' : platform;
}

export function PlatformCell({
  platform,
  displaySymbol,
  lookup,
}: {
  platform: string;
  displaySymbol: string;
  lookup: FrontendURLLookup;
}) {
  const href = lookup(platform, displaySymbol);
  const label = platformDisplayName(platform);
  const className = platform === 'edgeX' ? 'platform-self' : undefined;
  if (!href) return <span className={className}>{label}</span>;
  return (
    <a className={`platform-link ${className ?? ''}`} href={href} target="_blank" rel="noopener noreferrer" title={`Open ${platform} ${displaySymbol} page`}>
      {label} <span className="ext-icon" aria-hidden="true">↗</span>
    </a>
  );
}
