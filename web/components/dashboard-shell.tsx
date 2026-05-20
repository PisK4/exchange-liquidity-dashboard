import Link from 'next/link';
import { AutoRefresh } from '@/components/auto-refresh';
import { BarChart, LineChart } from '@/components/chart-primitives';
import { DashboardControls, PillGroup } from '@/components/dashboard-controls';
import { PlatformCell } from '@/components/platform-cell';
import { StatusBadge } from '@/components/status-badge';
import { StatusEmptyState } from '@/components/status-empty-state';
import { bp, money, moneyM, pct, ratio, type DashboardMeta, type FrontendURLLookup, type LiquiditySnapshot, type PlatformRow, type QualitySnapshot, type ShareSnapshot, type Top30Snapshot } from '@/lib/api/client';

type Query = Record<string, string | undefined>;

type DashboardData = {
  meta: DashboardMeta;
  liquidity: LiquiditySnapshot;
  quality: QualitySnapshot;
  share: ShareSnapshot;
  top30: Top30Snapshot;
  lookup: FrontendURLLookup;
};

const tabs = [
  ['monitor', '流动性监控'],
  ['quality', '盘口质量'],
  ['share', '市占率'],
  ['top30', 'Top30 成交量'],
] as const;

const tierLabels = ['0.05%', '0.10%', '1.00%', '2.00%'];

function withQuery(query: Query, patch: Query) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries({ ...query, ...patch })) {
    if (value) params.set(key, value);
  }
  const qs = params.toString();
  return qs ? `/?${qs}` : '/';
}

function isDisplayableStatus(status?: string) {
  return status === 'complete' || status === 'partial' || status === 'aggregated_orderbook' || status === 'ws_limited_depth';
}

function snapshotTime(data: DashboardData, tab: string) {
  const ts = tab === 'quality' ? data.quality.snapshot_ts : tab === 'share' ? data.share.snapshot_ts : tab === 'top30' ? data.top30.snapshot_ts : data.liquidity.snapshot_ts;
  return ts ? new Date(ts).toLocaleString('zh-CN', { hour12: false }) : '—';
}

function tierSeries(rows: PlatformRow[], side: 'bid_usd' | 'ask_usd' | 'total_usd') {
  return rows.map(row => ({
    label: row.platform,
    values: tierLabels.map(tier => isDisplayableStatus(row.depth_status) && typeof row.depth_by_tier?.[tier]?.[side] === 'number' ? row.depth_by_tier[tier][side] / 1_000_000 : undefined),
  }));
}

export function DashboardShell({ query, data }: { query: Query; data: DashboardData }) {
  const tab = query.tab ?? 'monitor';
  const symbol = query.symbol ?? data.liquidity.symbol ?? data.meta.symbols[0];
  const window = query.window ?? '7d';
  const tier = query.tier ?? '0.10%';
  const bucket = query.bucket ?? '100000';
  const platform = query.platform ?? data.top30.platform ?? 'binance';
  const needsControls = tab === 'monitor' || tab === 'quality';

  return (
    <>
      <AutoRefresh intervalMs={(data.meta.refresh_interval_sec || 30) * 1000} />
      <header className="topbar">
        <span className="logo">◆ edgeX</span>
        <span className="title">流动性 &amp; 深度监控面板</span>
        <span className="crumb">EdgeX Ops / Liquidity Dashboard</span>
        <span className="spacer" />
        <span className="meta">数据快照 · {snapshotTime(data, tab)} · 自动刷新 {data.meta.refresh_interval_sec || 30}s</span>
      </header>
      <nav className="tabs">
        {tabs.map(([key, label]) => (
          <Link className={`tab ${tab === key ? 'active' : ''}`} href={withQuery(query, { tab: key, window: key === 'share' ? '24h' : query.window })} key={key}>
            {label}
          </Link>
        ))}
      </nav>
      <main className="dashboard-main">
        {needsControls ? <DashboardControls query={{ ...query, tab }} symbols={data.meta.symbols} activeSymbol={symbol} activeWindow={window} /> : null}
        {tab === 'quality' ? <QualityTab data={data} query={query} bucket={bucket} symbol={symbol} /> : null}
        {tab === 'share' ? <ShareTab data={data.share} query={query} /> : null}
        {tab === 'top30' ? <Top30Tab data={data.top30} query={query} platform={platform} /> : null}
        {tab === 'monitor' ? <LiquidityTab data={data} query={query} tier={tier} symbol={symbol} /> : null}
      </main>
      <footer className="footer">edgeX Liquidity Monitor · 正式版 · 数据缺失项以 unsupported 展示</footer>
    </>
  );
}

function LiquidityTab({ data, query, tier, symbol }: { data: DashboardData; query: Query; tier: string; symbol: string }) {
  const rows = data.liquidity.rows ?? [];
  const edge = rows.find(row => row.platform === 'edgeX');
  const edgeDepth = edge?.depth_by_tier?.[tier]?.total_usd;
  const edgeRatio = edge?.vs_median_by_tier?.[tier];

  return (
    <div className="page-content active">
      <div className="section-bar"><span>3.2 · <b>深度对比</b></span><div className="line" /><span>{symbol} · 4 档深度</span></div>
      <div className="grid">
        <section className="panel span-6 row-h-sm">
          <div className="panel-head">
            <span className="panel-title">edgeX <span>±{tier}</span> 总深度</span>
            <PillGroup items={tierLabels} active={tier} query={{ ...query, tab: 'monitor' }} param="tier" />
          </div>
          <div className="big-number">{moneyM(edgeDepth)}</div>
          <div className="subline">vs 中位数 {ratio(edgeRatio)}</div>
        </section>
        <section className="panel span-6 row-h-sm">
          <div className="panel-head"><span className="panel-title">当前交易对 7d 市占率</span><span className="panel-tag">单币种</span></div>
          <div className="big-number muted">—</div>
          <StatusBadge status={data.liquidity.kpis?.symbol_share_7d_status ?? 'unsupported'} />
        </section>
        <section className="panel span-6 row-h-sm">
          <div className="panel-head"><span className="panel-title">edgeX spread (10min 均值)</span><span className="panel-tag">盘口</span></div>
          <div className="big-number muted">—</div>
          <StatusBadge status={data.liquidity.kpis?.edgex_spread_10m_status ?? 'unsupported'} />
        </section>
        <section className="panel span-6 row-h-sm">
          <div className="panel-head"><span className="panel-title">edgeX 当前 spread</span><span className="panel-tag">latest</span></div>
          <div className="big-number">{bp(data.liquidity.kpis?.edgex_spread_bp)}</div>
          <div className="subline">24h share {pct(data.liquidity.kpis?.edgex_24h_share_pct)}</div>
        </section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">买盘深度曲线 BID</span></div><LineChart labels={tierLabels.map(t => `±${t}`)} series={tierSeries(rows, 'bid_usd')} /></section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">卖盘深度曲线 ASK</span></div><LineChart labels={tierLabels.map(t => `±${t}`)} series={tierSeries(rows, 'ask_usd')} /></section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">合计深度曲线 BID + ASK</span></div><LineChart labels={tierLabels.map(t => `±${t}`)} series={tierSeries(rows, 'total_usd')} /></section>
        <section className="panel span-24">
          <div className="panel-head"><span className="panel-title">深度明细 · 平台 × 档位 (M USD)</span><span className="panel-sub">· 合计深度 vs 竞品中位数 / 排名</span></div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>平台</th><th className="num">0.05% BID</th><th className="num">0.05% ASK</th><th className="num">0.1% BID</th><th className="num">0.1% ASK</th><th className="num">1% BID</th><th className="num">1% ASK</th><th className="num">2% BID</th><th className="num">2% ASK</th><th className="num">±0.1% 合计</th><th className="num">vs 中位数</th><th className="num">排名</th><th>状态</th></tr></thead><tbody>{rows.map(row => <tr key={row.platform}><td><PlatformCell platform={row.platform} displaySymbol={symbol} lookup={data.lookup} /></td><td className="num">{moneyM(row.depth_by_tier?.['0.05%']?.bid_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['0.05%']?.ask_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['0.10%']?.bid_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['0.10%']?.ask_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['1.00%']?.bid_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['1.00%']?.ask_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['2.00%']?.bid_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['2.00%']?.ask_usd)}</td><td className="num">{moneyM(row.depth_by_tier?.['0.10%']?.total_usd)}</td><td className="num">{ratio(row.vs_median_by_tier?.['0.10%'])}</td><td className="num">{row.rank_0_1 || '—'}</td><td><StatusBadge status={row.depth_status} reason={row.partial_reason} /> <span className="muted">{row.depth_status_label}</span></td></tr>)}</tbody></table></div>
        </section>
      </div>
    </div>
  );
}

function QualityTab({ data, query, bucket, symbol }: { data: DashboardData; query: Query; bucket: string; symbol: string }) {
  const rows = data.quality.rows ?? [];
  const buckets = (data.quality.slippage_buckets_usd ?? [50_000, 100_000, 500_000, 1_000_000]).map(String);
  return (
    <div className="page-content active">
      <div className="section-bar"><span>3.5 · <b>盘口质量</b></span><div className="line" /><span>Spread · Imbalance · 模拟下单滑点</span></div>
      <div className="grid">
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">Spread (bp)</span></div><BarChart rows={rows.map(row => ({ label: row.platform, value: isDisplayableStatus(row.depth_status) ? row.spread_bp : undefined, status: row.depth_status }))} format={value => `${(value ?? 0).toFixed(2)}`} /></section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">模拟下单滑点 (bp)</span><PillGroup items={buckets} active={bucket} query={{ ...query, tab: 'quality' }} param="bucket" /></div><BarChart rows={rows.map(row => ({ label: row.platform, value: isDisplayableStatus(row.depth_status) ? row.worst_slippage_bp?.[bucket] : undefined, status: row.depth_status }))} format={value => `${(value ?? 0).toFixed(2)}`} /></section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">Bid/Ask Imbalance (%)</span></div><BarChart rows={rows.map(row => ({ label: row.platform, value: isDisplayableStatus(row.depth_status) && typeof row.imbalance_pct === 'number' ? Math.abs(row.imbalance_pct) : undefined, status: row.depth_status }))} format={value => `${(value ?? 0).toFixed(2)}%`} /></section>
        <section className="panel span-24">
          <div className="panel-head"><span className="panel-title">盘口质量明细</span><span className="panel-sub">· 每行=一个平台</span></div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>平台</th><th className="num">Spread (bp)</th><th className="num">Mid 价格</th><th className="num">Imbalance (%)</th><th className="num">滑点 50K</th><th className="num">滑点 100K</th><th className="num">滑点 500K</th><th className="num">滑点 1M</th><th>盘口结论</th></tr></thead><tbody>{rows.map(row => <tr key={row.platform}><td><PlatformCell platform={row.platform} displaySymbol={symbol} lookup={data.lookup} /></td><td className="num">{bp(row.spread_bp)}</td><td className="num">{money(row.mid_price)}</td><td className="num">{typeof row.imbalance_pct === 'number' ? row.imbalance_pct.toFixed(2) : '—'}</td><td className="num">{bp(row.worst_slippage_bp?.['50000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['100000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['500000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['1000000'])}</td><td>{row.verdict === 'unsupported' ? <StatusBadge status="unsupported" /> : row.verdict}</td></tr>)}</tbody></table></div>
        </section>
      </div>
    </div>
  );
}

function ShareTab({ data, query }: { data: ShareSnapshot; query: Query }) {
  const activeWindow = query.window ?? data.window ?? '24h';
  return (
    <div className="page-content active">
      <div className="section-bar"><span>3.4 · <b>edgeX 平台总交易量市占率</b></span><div className="line" /><span>全平台 perp 合计 · mexc×0.4, gate×0.5</span></div>
      <section className="panel">
        <div className="panel-head"><span className="panel-title">edgeX 平台总交易量市占率明细表</span><PillGroup items={['24h', '7d', '30d']} active={activeWindow} query={{ ...query, tab: 'share' }} param="window" /></div>
        {data.status === 'unsupported' ? <StatusEmptyState status="unsupported" message={data.reason ?? '当前统计窗口尚未实现'} /> : <>
          <div className="share-kpis">
            <div><span>当前 edgeX share</span><b>{pct(data.kpis?.edgex_share_pct)}</b></div>
            <div><span>edgeX 平台总成交量</span><b>{money(data.kpis?.edgex_total_volume_usd)}</b></div>
            <div><span>分母合计</span><b>{money(data.kpis?.denominator_usd ?? data.denominator_usd)}</b></div>
          </div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>#</th><th>平台</th><th className="num">原始成交量</th><th className="num">折算系数</th><th className="num">折算后成交量</th><th className="num">在分母中占比</th><th>状态</th></tr></thead><tbody>{data.rows.map(row => <tr key={row.platform}><td>{row.rank ?? '—'}</td><td>{row.platform}</td><td className="num">{money(row.raw_volume_usd)}</td><td className="num">{row.discount ?? '—'}</td><td className="num">{money(row.adjusted_volume_usd ?? row.adjusted_volume_24h_usd)}</td><td className="num">{pct(row.denominator_pct ?? row.share_pct)}</td><td><StatusBadge status={row.status} /></td></tr>)}</tbody></table></div>
        </>}
      </section>
      <section className="panel row-h-md"><div className="panel-head"><span className="panel-title">平台总市占率时序 (近 30d)</span><span className="panel-sub">· 三口径滚动叠加</span></div><StatusEmptyState status={data.trend?.status ?? 'unsupported'} message="历史时序聚合尚未实现" /></section>
    </div>
  );
}

function Top30Tab({ data, query, platform }: { data: Top30Snapshot; query: Query; platform: string }) {
  const platforms = ['binance', 'okx', 'bybit', 'bitget', 'mexc', 'gate', 'bingx', 'hyperliquid', 'lighter', 'edgeX'];
  return (
    <div className="page-content active">
      <section className="panel">
        <div className="panel-head"><span className="panel-title">各平台 Top30 成交量</span><PillGroup items={platforms} active={platform} query={{ ...query, tab: 'top30' }} param="platform" /></div>
        {data.status === 'unsupported' ? <StatusEmptyState status="unsupported" message="真实 Top30 排行接口尚未实现，表格保留正式字段结构" /> : null}
        <div className="table-wrap"><table className="tbl"><thead><tr><th className="num">#</th><th>Symbol</th><th className="num">24h Vol</th><th className="num">7d Vol</th><th className="num">7d Δ</th><th className="num">edgeX 已上线?</th><th className="num">竞品 Top30 覆盖</th><th>建议动作</th></tr></thead><tbody>{data.rows.map(row => <tr key={`${row.rank}-${row.symbol}`}><td className="num">{row.rank}</td><td>{row.symbol}</td><td className="num">{row.status === 'unsupported' ? '—' : money(row.volume_24h_usd)}</td><td className="num"><StatusBadge status={row.volume_7d_status ?? 'unsupported'} /></td><td className="num"><StatusBadge status={row.delta_7d_status ?? 'unsupported'} /></td><td className="num">{row.edgex_listed_status ? <StatusBadge status={row.edgex_listed_status} /> : row.edgex_listed ? '是' : '否'}</td><td className="num">{row.competitor_top30_coverage_status ? <StatusBadge status={row.competitor_top30_coverage_status} /> : row.competitor_top30_coverage}</td><td>{row.suggested_action_status ? <StatusBadge status={row.suggested_action_status} /> : row.suggested_action}</td></tr>)}</tbody></table></div>
      </section>
    </div>
  );
}
