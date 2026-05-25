import { redirect } from 'next/navigation';
import { dashboardRedirect, type SearchParams } from '../redirect-tabs';

export default function Top30Page({ searchParams }: { searchParams: SearchParams }) {
  redirect(dashboardRedirect('top30', searchParams, { platform: 'binance' }));
}
