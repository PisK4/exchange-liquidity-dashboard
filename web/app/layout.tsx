import type { Metadata } from 'next';
import Link from 'next/link';
import './globals.css';

export const metadata: Metadata = { title: 'EdgeX Liquidity Dashboard', description: 'V1 liquidity dashboard' };

const tabs = [
  ['Liquidity', '/liquidity'],
  ['Quality', '/quality'],
  ['Market Share', '/share'],
  ['Top30', '/top30']
];

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header className="topbar">
          <div>
            <div className="eyebrow">EdgeX Ops</div>
            <h1>Platform Liquidity Dashboard</h1>
          </div>
          <nav className="tabs">{tabs.map(([label, href]) => <Link key={href} href={href}>{label}</Link>)}</nav>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
