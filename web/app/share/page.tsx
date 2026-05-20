import { redirect } from 'next/navigation';

export default function SharePage() {
  redirect('/?tab=share&window=24h');
}
