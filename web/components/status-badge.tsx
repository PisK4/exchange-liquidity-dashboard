import type { ApiStatus } from '@/lib/api/client';

export function StatusBadge({ status, reason }: { status?: ApiStatus | string; reason?: string }) {
  const value = status || 'stale';
  return <span className={`badge ${value}`}>{reason ? `${value}/${reason}` : value}</span>;
}
