const palette: Record<string, string> = {
  edgeX: '#6ccf8e',
  binance: '#f3ba2f',
  okx: '#4f8cff',
  bybit: '#ffd166',
  bitget: '#00d4ff',
  bingx: '#35d07f',
  mexc: '#21c1d6',
  gate: '#7c5cff',
  hyperliquid: '#00e5a8',
  lighter: '#f2495c',
};

export function colorFor(label: string) {
  return palette[label] ?? '#8fa1b6';
}

export function rgba(hex: string, alpha: number) {
  const value = hex.replace('#', '');
  const r = parseInt(value.slice(0, 2), 16);
  const g = parseInt(value.slice(2, 4), 16);
  const b = parseInt(value.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${alpha})`;
}
