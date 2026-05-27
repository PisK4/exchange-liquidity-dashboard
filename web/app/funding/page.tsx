import { redirect } from 'next/navigation';
import { dashboardRedirect, type SearchParams } from '../redirect-tabs';

export default function FundingPage({ searchParams }: { searchParams: SearchParams }) {
  redirect(dashboardRedirect('funding', searchParams));
}
