import { ActivityRadar } from '@/components/activity-radar';

export const dynamic = 'force-dynamic';

type SearchParams = Record<string, string | string[] | undefined>;

function scalar(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value;
}

export default function ActivityPage({ searchParams }: { searchParams: SearchParams }) {
  return <ActivityRadar query={{
    platform: scalar(searchParams.platform),
    review_status: scalar(searchParams.review_status),
    status: scalar(searchParams.status),
  }} />;
}
