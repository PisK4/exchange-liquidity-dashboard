import { ActivityDecisionConfirm } from '@/components/activity-decision-confirm';
import { normalizeActivityDecisionAction } from '@/lib/api/activity';

export const dynamic = 'force-dynamic';

type SearchParams = Record<string, string | string[] | undefined>;

function scalar(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value;
}

export default function ActivityDecisionPage({ params, searchParams }: { params: { id: string }; searchParams: SearchParams }) {
  return <ActivityDecisionConfirm
    id={params.id}
    action={normalizeActivityDecisionAction(scalar(searchParams.action))}
    version={Number(scalar(searchParams.version) ?? '0')}
    token={scalar(searchParams.token) ?? ''}
  />;
}
