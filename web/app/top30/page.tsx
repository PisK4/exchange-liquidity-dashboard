import { redirect } from 'next/navigation';

export default function Top30Page() {
  redirect('/?tab=top30&platform=binance');
}
