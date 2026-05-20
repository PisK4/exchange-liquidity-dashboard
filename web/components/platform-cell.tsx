import type { FrontendURLLookup } from '@/lib/api/client';

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
  if (!href) return <>{platform}</>;
  return (
    <a className="platform-link" href={href} target="_blank" rel="noopener noreferrer" title={`Open ${platform} ${displaySymbol} page`}>
      {platform} <span className="ext-icon" aria-hidden="true">↗</span>
    </a>
  );
}
