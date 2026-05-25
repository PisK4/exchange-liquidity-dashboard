import { redirect } from 'next/navigation';
import { dashboardRedirect, type SearchParams } from '../redirect-tabs';

export default function QualityPage({ searchParams }: { searchParams: SearchParams }) {
  redirect(dashboardRedirect('quality', searchParams));
}
