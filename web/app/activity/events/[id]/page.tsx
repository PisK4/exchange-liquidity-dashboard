import { ActivityEventDetail } from '@/components/activity-event-detail';

export const dynamic = 'force-dynamic';

export default function ActivityEventPage({ params }: { params: { id: string } }) {
  return <ActivityEventDetail id={params.id} />;
}
