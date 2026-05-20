import { StatusBadge } from '@/components/status-badge';
import type { ApiStatus } from '@/lib/api/client';

export function StatusEmptyState({ status = 'unsupported', message }: { status?: ApiStatus | string; message: string }) {
  return (
    <div className="empty-state">
      <StatusBadge status={status} />
      <span>{message}</span>
    </div>
  );
}
