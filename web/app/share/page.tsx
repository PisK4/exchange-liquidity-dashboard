import { redirect } from 'next/navigation';
import { dashboardRedirect, type SearchParams } from '../redirect-tabs';

export default function SharePage({ searchParams }: { searchParams: SearchParams }) {
  redirect(dashboardRedirect('share', searchParams, { window: '24h' }));
}
