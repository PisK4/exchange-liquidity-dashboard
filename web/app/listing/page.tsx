import { ListingCandidates } from '@/components/listing-candidates';

export const dynamic = 'force-dynamic';

type SearchParams = Record<string, string | string[] | undefined>;

function scalar(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value;
}

function positiveInt(value: string | string[] | undefined) {
  const raw = scalar(value);
  if (!raw) return undefined;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

export default function ListingPage({ searchParams }: { searchParams: SearchParams }) {
  return <ListingCandidates query={{
    status: scalar(searchParams.status),
    evidence_kind: scalar(searchParams.evidence_kind),
    platform: scalar(searchParams.platform),
    symbol: scalar(searchParams.symbol),
    limit: positiveInt(searchParams.limit),
  }} />;
}
