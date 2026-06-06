import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = { title: 'EdgeX Ops Intelligence', description: 'EdgeX operations intelligence platform' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
