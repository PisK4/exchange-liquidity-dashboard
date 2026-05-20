import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = { title: 'edgeX 流动性 & 深度监控面板', description: 'EdgeX platform liquidity dashboard' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
