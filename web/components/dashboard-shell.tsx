import Link from 'next/link';
import { AutoRefresh } from '@/components/auto-refresh';
import { BarChart } from '@/components/chart-primitives';
import { DashboardControls, PillGroup } from '@/components/dashboard-controls';
import { LineChart } from '@/components/line-chart';
import { PlatformCell, platformDisplayName } from '@/components/platform-cell';
import { StatusBadge } from '@/components/status-badge';
import { StatusEmptyState } from '@/components/status-empty-state';
import { bp, money, moneyM, pct, ratio, type DashboardMeta, type DepthTierMetrics, type FrontendURLLookup, type LiquiditySnapshot, type PlatformRow, type QualitySnapshot, type ShareSnapshot, type Top30Snapshot } from '@/lib/api/client';

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
const edgexAccent = '#6ccf8e';
const shareWindowItems = [
  { label: '日 (24h)', value: '24h' },
  { label: '周 (7d)', value: '7d' },
  { label: '月 (30d)', value: '30d' },
];

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
    values: tierLabels.map(tier => {
      const depth = row.depth_by_tier?.[tier];
      return isDisplayableStatus(tierStatus(row, tier)) && typeof depth?.[side] === 'number' ? depth[side] / 1_000_000 : undefined;
    }),
    statuses: tierLabels.map(tier => tierStatus(row, tier)),
    sources: tierLabels.map(tier => depthSourceLabel(row.depth_by_tier?.[tier]) || undefined),
  }));
}

function displayTierLabel(tier: string) {
  const value = Number.parseFloat(tier);
  return Number.isFinite(value) ? `±${value}%` : `±${tier}`;
}

function WindowPills({ active, query }: { active: string; query: Query }) {
  return (
    <span className="pill-group" aria-label="市占率统计窗口">
      {shareWindowItems.map(item => (
        <Link className={`pill ${item.value === active ? 'active' : ''}`} href={withQuery(query, { tab: 'share', window: item.value })} key={item.value}>
          {item.label}
        </Link>
      ))}
    </span>
  );
}

function moneyInMillions(value?: number) {
  return typeof value === 'number' ? Math.round(value / 1_000_000).toLocaleString('en-US') : '—';
}

function signedPct(value?: number) {
  if (typeof value !== 'number') return '—';
  return `${value > 0 ? '+' : ''}${value.toFixed(2)}%`;
}

function spreadUSD(row: PlatformRow, spreadBp?: number) {
  return typeof spreadBp === 'number' && typeof row.mid_price === 'number' ? row.mid_price * spreadBp / 10_000 : undefined;
}

function slippageUSD(bucket: string, slippageBp?: number) {
  const amount = Number(bucket);
  return typeof slippageBp === 'number' && Number.isFinite(amount) ? amount * slippageBp / 10_000 : undefined;
}

function spreadUSDLabel(rowByPlatform: Map<string, PlatformRow>, label: string, spreadBp?: number) {
  const row = rowByPlatform.get(label);
  return usdLabel(row ? spreadUSD(row, spreadBp) : undefined);
}

function usdLabel(value?: number, digits = 2) {
  return typeof value === 'number' ? `$${value.toFixed(digits)}` : '$—';
}

function bucketLabel(bucket: string) {
  const amount = Number(bucket);
  if (!Number.isFinite(amount)) return bucket;
  if (amount >= 1_000_000) return `${amount / 1_000_000}M USD`;
  return `${amount / 1_000}K USD`;
}

function SlippagePills({ buckets, active, query }: { buckets: string[]; active: string; query: Query }) {
  return (
    <span className="pill-group" aria-label="滑点档位">
      {buckets.map(item => (
        <Link className={`pill ${item === active ? 'active' : ''}`} href={withQuery(query, { tab: 'quality', bucket: item })} key={item}>
          {bucketLabel(item)}
        </Link>
      ))}
    </span>
  );
}

function VerdictBadge({ verdict }: { verdict?: string }) {
  if (verdict === 'unsupported') return <StatusBadge status="unsupported" />;
  const label = verdict === 'healthy' || verdict === '健康' ? '健康' : verdict === 'watch' || verdict === '关注' ? '关注' : verdict === 'poor' || verdict === '较差' ? '较差' : verdict || '—';
  const cls = label === '健康' ? 'b-ok' : label === '关注' ? 'b-warn' : label === '较差' ? 'b-bad' : 'b-mute';
  return <span className={`badge ${cls}`}>{label}</span>;
}

function tierStatus(row: PlatformRow, tier: string) {
  return row.depth_by_tier?.[tier]?.depth_status ?? row.depth_status;
}

function tierReason(row: PlatformRow, tier: string) {
  const depth = row.depth_by_tier?.[tier];
  return depth?.partial_reason ?? (depth?.depth_status ? undefined : row.partial_reason);
}

function depthSourceLabel(depth?: DepthTierMetrics) {
  if (!depth) return '';
  const source = depth.source_id || depth.depth_source;
  if (!source) return '';
  return depth.depth_status === 'partial' ? `${source} · lower-bound` : source;
}

function DepthCell({ row, tier, side }: { row: PlatformRow; tier: string; side: 'bid_usd' | 'ask_usd' | 'total_usd' }) {
  const depth = row.depth_by_tier?.[tier];
  return (
    <td className="num">
      <div>{moneyM(depth?.[side])}</div>
    </td>
  );
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
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">买盘深度曲线 BID</span></div><LineChart ariaLabel="买盘深度曲线 BID" labels={tierLabels.map(displayTierLabel)} series={tierSeries(rows, 'bid_usd')} /></section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">卖盘深度曲线 ASK</span></div><LineChart ariaLabel="卖盘深度曲线 ASK" labels={tierLabels.map(displayTierLabel)} series={tierSeries(rows, 'ask_usd')} /></section>
        <section className="panel span-8 row-h-md"><div className="panel-head"><span className="panel-title">合计深度曲线 BID + ASK</span></div><LineChart ariaLabel="合计深度曲线 BID + ASK" labels={tierLabels.map(displayTierLabel)} series={tierSeries(rows, 'total_usd')} /></section>
        <section className="panel span-24">
          <div className="panel-head"><span className="panel-title">深度明细 · 平台 × 档位 (M USD)</span><span className="panel-sub">· 合计深度 vs 竞品中位数 / 排名</span></div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>平台</th><th className="num">0.05% BID</th><th className="num">0.05% ASK</th><th className="num">0.1% BID</th><th className="num">0.1% ASK</th><th className="num">1% BID</th><th className="num">1% ASK</th><th className="num">2% BID</th><th className="num">2% ASK</th><th className="num">±0.1% 合计</th><th className="num">vs 中位数</th><th className="num">排名</th><th>状态</th></tr></thead><tbody>{rows.map(row => <tr key={row.platform}><td><PlatformCell platform={row.platform} displaySymbol={symbol} lookup={data.lookup} /></td><DepthCell row={row} tier="0.05%" side="bid_usd" /><DepthCell row={row} tier="0.05%" side="ask_usd" /><DepthCell row={row} tier="0.10%" side="bid_usd" /><DepthCell row={row} tier="0.10%" side="ask_usd" /><DepthCell row={row} tier="1.00%" side="bid_usd" /><DepthCell row={row} tier="1.00%" side="ask_usd" /><DepthCell row={row} tier="2.00%" side="bid_usd" /><DepthCell row={row} tier="2.00%" side="ask_usd" /><DepthCell row={row} tier="0.10%" side="total_usd" /><td className="num">{ratio(row.vs_median_by_tier?.['0.10%'])}</td><td className="num">{row.rank_0_1 || '—'}</td><td><StatusBadge status={row.depth_status} reason={row.partial_reason} /> <span className="muted">{row.depth_status_label}</span></td></tr>)}</tbody></table></div>
        </section>
      </div>
    </div>
  );
}

function QualityTab({ data, query, bucket, symbol }: { data: DashboardData; query: Query; bucket: string; symbol: string }) {
  const rows = data.quality.rows ?? [];
  const rowByPlatform = new Map(rows.map(row => [row.platform, row]));
  const buckets = (data.quality.slippage_buckets_usd ?? [50_000, 100_000, 500_000, 1_000_000]).map(String);
  return (
    <div className="page-content active">
      <div className="section-bar"><span>3.5 · <b>盘口质量</b></span><div className="line" /><span>Spread · Imbalance · 模拟下单滑点</span></div>
      <div className="grid">
        <section className="panel span-8 row-h-md">
          <div className="panel-head"><span className="panel-title">Spread (bp)</span><span className="panel-sub">· 买一/卖一相对价差</span></div>
          <BarChart
            rows={rows.map(row => ({ label: row.platform, value: isDisplayableStatus(row.depth_status) ? row.spread_bp : undefined, status: row.depth_status, color: row.platform === 'edgeX' ? edgexAccent : '#5794f2' }))}
            sort="asc"
            format={(value, row) => `${(value ?? 0).toFixed(2)} bp · ${spreadUSDLabel(rowByPlatform, row.label, value)}`}
          />
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head"><span className="panel-title">模拟下单滑点 (bp)</span><span className="panel-sub">· 相对中间价</span><div className="panel-actions"><SlippagePills buckets={buckets} active={bucket} query={query} /></div></div>
          <div className="note">档位 <b>可配置</b>: 在运营后台 <code>config.slippage_buckets</code> 维护, 默认 [50K / 100K / 500K / 1M] USD。</div>
          <BarChart
            rows={rows.map(row => ({ label: row.platform, value: isDisplayableStatus(row.depth_status) ? row.worst_slippage_bp?.[bucket] : undefined, status: row.depth_status, color: row.platform === 'edgeX' ? edgexAccent : '#73bf69' }))}
            sort="asc"
            format={value => `${(value ?? 0).toFixed(2)} bp · ${usdLabel(slippageUSD(bucket, value), 0)}`}
          />
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head"><span className="panel-title">Bid/Ask Imbalance (%)</span><span className="panel-sub">· (BID深度-ASK深度)/合计</span></div>
          <div className="note">正值=买侧偏厚, 负值=卖侧偏厚, |值| &gt; 30% 视为单边报价偏离健康区间。</div>
          <BarChart
            signed
            rows={rows.map(row => ({ label: row.platform, value: isDisplayableStatus(row.depth_status) ? row.imbalance_pct : undefined, status: row.depth_status, color: row.platform === 'edgeX' ? edgexAccent : Math.abs(row.imbalance_pct ?? 0) > 30 ? '#f2495c' : '#5794f2' }))}
            format={value => signedPct(value)}
          />
        </section>
        <section className="panel span-24">
          <div className="panel-head"><span className="panel-title">盘口质量明细</span><span className="panel-sub">· 每行=一个平台</span><span className="panel-tag muted">CSV 可导</span></div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>平台</th><th className="num">Spread (bp)</th><th className="num">Mid 价格</th><th className="num">Imbalance (%)</th><th className="num">滑点 50K (bp)</th><th className="num">滑点 100K (bp)</th><th className="num">滑点 500K (bp)</th><th className="num">滑点 1M (bp)</th><th>盘口结论</th></tr></thead><tbody>{rows.map(row => <tr key={row.platform}><td><PlatformCell platform={row.platform} displaySymbol={symbol} lookup={data.lookup} /></td><td className="num">{bp(row.spread_bp)}</td><td className="num">{money(row.mid_price)}</td><td className="num">{signedPct(row.imbalance_pct)}</td><td className="num">{bp(row.worst_slippage_bp?.['50000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['100000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['500000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['1000000'])}</td><td><VerdictBadge verdict={row.verdict} /></td></tr>)}</tbody></table></div>
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
        <div className="panel-head">
          <span className="panel-title">edgeX 平台总交易量市占率明细表</span>
          <span className="panel-sub">· 切换口径查看 share + 各平台贡献 · 数据=全平台所有 perp 合计成交量</span>
          <div className="panel-actions"><span className="panel-tag muted">CSV 可导</span><WindowPills active={activeWindow} query={query} /></div>
        </div>
        {data.status === 'unsupported' ? <StatusEmptyState status="unsupported" message={data.reason ?? '当前统计窗口尚未实现'} /> : <>
          <div className="share-kpi-strip">
            <div className="share-primary"><span>当前 edgeX share {activeWindow}</span><b>{pct(data.kpis?.edgex_share_pct)}</b></div>
            <div><span>edgeX 平台总成交量</span><b>{money(data.kpis?.edgex_total_volume_usd)}</b></div>
            <div><span>分母合计 (含 edgeX, mexc×0.4, gate×0.5)</span><b>{money(data.kpis?.denominator_usd ?? data.denominator_usd)}</b></div>
            <p className="share-kpi-note">
              数据=全平台所有 perp 合计成交量;<br />
              其他平台原始 vol 与折算后 vol 区别仅作用于 mexc / gate;<br />
              edgeX 自身也计入分母。
            </p>
          </div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>#</th><th>平台</th><th className="num">原始成交量 (M USD)</th><th className="num">折算系数</th><th className="num">折算后 (M USD)</th><th className="num">在分母中占比</th><th>占比可视化</th></tr></thead><tbody>{data.rows.map(row => {
            const share = row.denominator_pct ?? row.share_pct;
            const discount = row.discount ?? 1;
            return (
              <tr key={row.platform}>
                <td>{row.rank ?? '—'}</td>
                <td><span className={row.platform === 'edgeX' ? 'platform-self' : undefined}>{platformDisplayName(row.platform)}</span></td>
                <td className="num">{moneyInMillions(row.raw_volume_usd)}</td>
                <td className="num">{discount < 1 ? <span className="badge b-warn">×{discount}</span> : <span className="muted">—</span>}</td>
                <td className="num"><b>{moneyInMillions(row.adjusted_volume_usd ?? row.adjusted_volume_24h_usd)}</b></td>
                <td className="num">{pct(share)}</td>
                <td>
                  <div className="share-ratio-track" data-testid="share-ratio-bar" aria-label={`${row.platform} denominator share ${pct(share)}`}>
                    <div className="share-ratio-fill" style={{ width: `${Math.min(Math.max(share ?? 0, 0) * 1.5, 100)}%`, background: row.platform === 'edgeX' ? edgexAccent : '#5794f2' }} />
                  </div>
                </td>
              </tr>
            );
          })}</tbody></table></div>
        </>}
      </section>
      <section className="panel row-h-md"><div className="panel-head"><span className="panel-title">平台总市占率时序 (近 30d)</span><span className="panel-sub">· 三口径滚动叠加</span></div><StatusEmptyState status={data.trend?.status ?? 'unsupported'} message="历史时序聚合数据不足" /></section>
    </div>
  );
}

function Top30Tab({ data, query, platform }: { data: Top30Snapshot; query: Query; platform: string }) {
  const platforms = ['binance', 'okx', 'bybit', 'bitget', 'mexc', 'gate', 'bingx', 'hyperliquid', 'lighter', 'edgeX'];
  return (
    <div className="page-content active">
      <section className="panel">
        <div className="panel-head"><span className="panel-title">各平台 Top30 成交量</span><PillGroup items={platforms} active={platform} query={{ ...query, tab: 'top30' }} param="platform" /></div>
        {data.status === 'unsupported' ? <StatusEmptyState status="unsupported" message={data.platform === 'edgeX' ? 'not implemented' : '尚未返回该平台 Top30 排行，等待下一次拉取'} /> : null}
        <div className="table-wrap"><table className="tbl"><thead><tr><th className="num">#</th><th>Symbol</th><th className="num">24h Vol</th><th className="num">7d Vol</th><th className="num">7d Δ</th><th className="num">edgeX 已上线?</th><th className="num">竞品 Top30 覆盖</th><th>建议动作</th></tr></thead><tbody>{data.rows.map(row => <tr key={`${row.rank}-${row.symbol}`}><td className="num">{row.rank}</td><td>{row.symbol}</td><td className="num">{row.status === 'unsupported' ? '—' : money(row.volume_24h_usd)}</td><td className="num">{typeof row.volume_7d_usd === 'number' ? money(row.volume_7d_usd) : <StatusBadge status={row.volume_7d_status ?? 'unsupported'} />}</td><td className="num">{typeof row.delta_7d_pct === 'number' ? pct(row.delta_7d_pct) : <StatusBadge status={row.delta_7d_status ?? 'unsupported'} />}</td><td className="num">{row.edgex_listed_status ? <StatusBadge status={row.edgex_listed_status} /> : row.edgex_listed ? '是' : '否'}</td><td className="num">{row.competitor_top30_coverage_status ? <StatusBadge status={row.competitor_top30_coverage_status} /> : row.competitor_top30_coverage}</td><td>{row.suggested_action_status ? <StatusBadge status={row.suggested_action_status} /> : row.suggested_action}</td></tr>)}</tbody></table></div>
      </section>
    </div>
  );
}
