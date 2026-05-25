import { redirect } from 'next/navigation';
import { dashboardRedirect, type SearchParams } from '../redirect-tabs';

export default function LiquidityPage({ searchParams }: { searchParams: SearchParams }) {
  redirect(dashboardRedirect('monitor', searchParams));
}
