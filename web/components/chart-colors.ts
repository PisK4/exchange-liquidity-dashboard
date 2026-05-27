const palette: Record<string, string> = {
  edgeX: '#6ccf8e',
  binance: '#f3ba2f',
  okx: '#4f8cff',
  bybit: '#ffd166',
  bitget: '#00d4ff',
  // bingx 原 #35d07f 与 edgeX 中绿撞色，挪到天蓝避开 edgeX 绿主色域。
  bingx: '#7dd3fc',
  mexc: '#21c1d6',
  gate: '#7c5cff',
  // hyperliquid 原 #00e5a8 也跟 edgeX 撞色；改成珊瑚粉，跳出蓝/绿/青家族。
  hyperliquid: '#fb7185',
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
