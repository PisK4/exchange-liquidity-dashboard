import Link from 'next/link';
import { BarChart } from '@/components/chart-primitives';
import { DashboardControls, PillGroup } from '@/components/dashboard-controls';
import { LineChart } from '@/components/line-chart';
import { PlatformCell, platformDisplayName } from '@/components/platform-cell';
import { ShareTrendChart, type ShareTrendPoint } from '@/components/share-trend-chart';
import { StatusBadge } from '@/components/status-badge';
import { StatusEmptyState } from '@/components/status-empty-state';
import { QualityCard } from '@/components/quality-card';
import { QualityFundingRow } from '@/components/quality-funding-row';
import { SymbolBlock } from '@/components/symbol-block';
import { Top30DivergenceView } from '@/components/top30-divergence-view';
import { WatchlistToolbar } from '@/components/watchlist-toolbar';
import { resolveSymbolContext, type SymbolContext } from '@/components/lib/symbol-context';
import { bp, money, moneyAuto, pct, ratio, type DashboardMeta, type DepthTierMetrics, type FrontendURLLookup, type LiquiditySnapshot, type PlatformFundingRate, type PlatformRow, type QualitySnapshot, type ShareSnapshot, type Top30DivergenceSnapshot, type Top30Row, type Top30Snapshot } from '@/lib/api/client';
import { FUNDING_SIGN_CONVENTION_TOOLTIP, formatFundingDelta, formatFundingRate8h, fundingPeriodTooltip } from '@/lib/funding-format';
import { WATCHLIST_DEFAULT_FALLBACK, normalizeSymbol } from '@/lib/watchlist';

type Query = Record<string, string | undefined>;

type DashboardData = {
  meta: DashboardMeta;
  liquidity: LiquiditySnapshot;
  quality: QualitySnapshot;
  share: ShareSnapshot;
  top30: Top30Snapshot;
  top30Divergence: Top30DivergenceSnapshot;
  lookup: FrontendURLLookup;
  liquidityByCanonical: Record<string, LiquiditySnapshot>;
  qualityByCanonical: Record<string, QualitySnapshot>;
  watchlist: string[];
};

// mergeQualityIntoLiquidity overlays per-row worst_slippage_bp +
// verdict from the quality endpoint onto the liquidity snapshot.
// The merge is keyed by `platform`; rows missing from either side
// degrade gracefully (slippage stays null → card shows the empty
// state). Returns null if `liq` is null since the liquidity side
// owns the KPI block QualityCard needs (spread / share live there,
// not on QualitySnapshot.kpis which only carries funding fields).
function mergeQualityIntoLiquidity(
  liq: LiquiditySnapshot | null,
  qual: QualitySnapshot | null,
): LiquiditySnapshot | null {
  if (!liq) return null;
  if (!qual) return liq;
  const qualByPlatform = new Map<string, QualitySnapshot['rows'][number]>();
  for (const row of qual.rows) qualByPlatform.set(row.platform, row);
  return {
    ...liq,
    rows: liq.rows.map(lrow => {
      const qrow = qualByPlatform.get(lrow.platform);
      if (!qrow) return lrow;
      return {
        ...lrow,
        worst_slippage_bp: qrow.worst_slippage_bp ?? lrow.worst_slippage_bp,
        verdict: qrow.verdict ?? lrow.verdict,
      };
    }),
  };
}

const tabs = [
  ['monitor', '流动性监控'],
  ['quality', '盘口质量'],
  ['share', '市占率'],
  ['top30', 'Top30 成交量'],
] as const;

// tierLabels / tierSeries / displayTierLabel are exported so the
// WatchlistCard mini chart can render the exact same x-axis and series
// shape as the V1 single-symbol detail view. Keeping one definition
// avoids the two surfaces drifting (e.g. someone adding ±5% here but
// not there).
export const tierLabels = ['0.05%', '0.10%', '1.00%', '2.00%'];
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

function isStrictStatus(status?: string) {
  return status === 'complete' || status === 'aggregated_orderbook' || status === 'ws_limited_depth';
}

function snapshotTime(data: DashboardData, tab: string) {
  const ts = tab === 'quality' ? data.quality.snapshot_ts : tab === 'share' ? data.share.snapshot_ts : tab === 'top30' ? data.top30.snapshot_ts : data.liquidity.snapshot_ts;
  return ts ? new Date(ts).toLocaleString('zh-CN', { hour12: false }) : '—';
}

export function tierSeries(rows: PlatformRow[], side: 'bid_usd' | 'ask_usd' | 'total_usd') {
  return rows.map(row => ({
    label: row.platform,
    values: tierLabels.map(tier => {
      const depth = row.depth_by_tier?.[tier];
      return depthDisplayAvailable(row, tier) && typeof depth?.[side] === 'number' ? depth[side] : undefined;
    }),
    statuses: tierLabels.map(tier => tierStatus(row, tier)),
    sources: tierLabels.map(tier => depthSourceLabel(row.depth_by_tier?.[tier]) || undefined),
  }));
}

export function displayTierLabel(tier: string) {
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

function depthDisplayAvailable(row: PlatformRow, tier: string) {
  const depth = row.depth_by_tier?.[tier];
  if (!depth) return false;
  if (typeof depth.display_available === 'boolean') return depth.display_available;
  return isDisplayableStatus(tierStatus(row, tier));
}

function depthStrictComplete(row: PlatformRow, tier: string) {
  const depth = row.depth_by_tier?.[tier];
  if (!depth) return false;
  if (typeof depth.strict_complete === 'boolean') return depth.strict_complete;
  return isStrictStatus(tierStatus(row, tier));
}

function rowDisplayAvailable(row: PlatformRow) {
  const tiers = Object.keys(row.depth_by_tier ?? {});
  if (tiers.length === 0) return isDisplayableStatus(row.depth_status);
  return tiers.some(tier => depthDisplayAvailable(row, tier));
}

function depthUnavailableTooltip(row: PlatformRow, tier: string) {
  const depth = row.depth_by_tier?.[tier];
  if (!depth) return '该交易所当前无该档位盘口数据';
  if (depth.physical_limit) {
    const farthest = typeof depth.farthest_distance_pct === 'number' ? `约 ${depth.farthest_distance_pct.toFixed(2)}%` : '公开接口物理上限';
    return `${row.platform} 公开接口最深${farthest}，无法覆盖 ${tier}`;
  }
  if (!depth.display_available) return '数据过期或错误，暂不可展示';
  return '';
}

function depthLooseTooltip(depth?: DepthTierMetrics) {
  if (!depth || depth.strict_complete !== false || depth.display_available === false) return '';
  if (depth.policy_acceptance === 'loose_grouped_approx') return '该数据为分组近似值，参与排名但以灰色提示';
  return '该数据为下界近似值，真实深度 ≥ 显示值，参与排名但以灰色提示';
}

function depthSourceLabel(depth?: DepthTierMetrics) {
  if (!depth) return '';
  const source = depth.source_id || depth.depth_source;
  if (!source) return '';
  if (depth.policy_acceptance === 'loose_grouped_approx') return `${source} · loose grouped`;
  if (depth.policy_acceptance === 'loose_lower_bound' || depth.depth_status === 'partial') return `${source} · lower-bound`;
  return source;
}

function normalizeDepthLabel(label?: string) {
  if (label === '深度落后') return '落后';
  return label;
}

function depthLabelBadgeClass(label?: string) {
  switch (normalizeDepthLabel(label)) {
    case '达标': return 'b-ok';
    case '偏弱': return 'b-warn';
    case '落后': return 'b-bad';
    default: return 'b-mute';
  }
}

function DepthCell({ row, tier, side }: { row: PlatformRow; tier: string; side: 'bid_usd' | 'ask_usd' | 'total_usd' }) {
  const depth = row.depth_by_tier?.[tier];
  const display = depthDisplayAvailable(row, tier);
  const strict = depthStrictComplete(row, tier);
  const unavailableTitle = depthUnavailableTooltip(row, tier);
  const looseTitle = depthLooseTooltip(depth);
  const sideClass = side === 'bid_usd' ? ' col-bid' : side === 'ask_usd' ? ' col-ask' : '';
  return (
    <td className={`num${sideClass}`}>
      {!display
        ? <div className="muted" title={unavailableTitle}>—</div>
        : <div className={strict ? undefined : 'muted'} title={looseTitle}>{moneyAuto(depth?.[side])}{!strict && <span className="approx-mark" aria-label="部分覆盖">*</span>}</div>}
    </td>
  );
}

export function DashboardShell({
  query,
  data,
  watchlist,
  onWatchlistChange,
}: {
  query: Query;
  data: DashboardData;
  watchlist: string[];
  onWatchlistChange: (next: string[]) => void;
}) {
  const tab = query.tab ?? 'monitor';
  const symbolCtx = resolveSymbolContext(data.meta, query.symbol ?? data.meta.symbols[0] ?? 'BTC');
  const tier = query.tier ?? '0.10%';
  const bucket = query.bucket ?? '100000';
  const platform = query.platform ?? data.top30.platform ?? 'binance';
  const needsControls = tab === 'monitor' || tab === 'quality';
  const categories = data.meta.categories ?? [];
  const activeCategory = query.category ?? 'all';

  return (
    <>
      <header className="topbar">
        <span className="logo">◆ edgeX</span>
        <span className="title">流动性 &amp; 深度监控面板</span>
        <span className="crumb">EdgeX Ops / Liquidity Dashboard</span>
        <span className="spacer" />
        {/* <span className="meta">数据快照 · {snapshotTime(data, tab)} · 自动刷新 {data.meta.refresh_interval_sec || 30}s</span> */}
      </header>
      <nav className="tabs">
        {tabs.map(([key, label]) => (
          <Link className={`tab ${tab === key ? 'active' : ''}`} href={withQuery(query, { tab: key, window: key === 'share' ? '24h' : query.window })} key={key}>
            {label}
          </Link>
        ))}
      </nav>
      <main className="dashboard-main">
        {needsControls ? <DashboardControls query={{ ...query, tab }} categories={categories} activeCategory={activeCategory} activeCanonical={symbolCtx.canonical} /> : null}
        {tab === 'quality' ? <QualityTab data={data} query={query} bucket={bucket} symbolCtx={symbolCtx} watchlist={watchlist} allSymbols={(data.meta.categories ?? []).flatMap(c => c.symbols)} onWatchlistChange={onWatchlistChange} /> : null}
        {tab === 'share' ? <ShareTab data={data.share} query={query} /> : null}
        {tab === 'top30' ? <Top30Tab data={data.top30} divergence={data.top30Divergence} lookup={data.lookup} query={query} platform={platform} /> : null}
        {tab === 'monitor' ? (
          <LiquidityTab
            data={data}
            query={query}
            tier={tier}
            symbolCtx={symbolCtx}
            watchlist={watchlist}
            onWatchlistChange={onWatchlistChange}
          />
        ) : null}
      </main>
      <footer className="footer">edgeX Liquidity Monitor · 正式版</footer>
    </>
  );
}

function FundingKpiPanel({ kpis }: { kpis?: LiquiditySnapshot['kpis'] }) {
  const status = kpis?.competitor_funding_rate_median_8h_status ?? 'stale';
  const median = kpis?.competitor_funding_rate_median_8h;
  const edgexRate = kpis?.edgex_funding_rate_8h;
  const delta = typeof edgexRate === 'number' && typeof median === 'number' ? edgexRate - median : null;
  return (
    <section className="panel span-6 row-h-sm">
      <div className="panel-head">
        <span className="panel-title">
          edgeX 资金费率
          <span className="info-icon" aria-label="资金费率 sign convention" title={FUNDING_SIGN_CONVENTION_TOOLTIP}> ⓘ</span>
        </span>
        <span className="panel-tag">latest</span>
      </div>
      <div className={`big-number${typeof edgexRate === 'number' ? '' : ' muted'}`}>{formatFundingRate8h(edgexRate)}</div>
      <div className="subline">
        vs 竞品 median {status === 'complete' ? formatFundingDelta(delta) : '—'}
      </div>
    </section>
  );
}

function FundingCell({ funding }: { funding?: PlatformFundingRate | null }) {
  const usable = funding && typeof funding.rate_8h === 'number' && Number.isFinite(funding.rate_8h);
  const display = usable ? formatFundingRate8h(funding?.rate_8h) : '—';
  const tooltip = fundingPeriodTooltip(funding ?? undefined);
  return (
    <td className="num" title={tooltip}>{usable ? <span>{display}</span> : <span className="muted">{display}</span>}</td>
  );
}

function LiquidityTab({
  data,
  query,
  tier,
  symbolCtx,
  watchlist,
  onWatchlistChange,
}: {
  data: DashboardData;
  query: Query;
  tier: string;
  symbolCtx: SymbolContext;
  watchlist: string[];
  onWatchlistChange: (next: string[]) => void;
}) {
  const allSymbols = (data.meta.categories ?? []).flatMap(c => c.symbols);

  // Both single-symbol and multi-symbol monitor views render through
  // SymbolBlock now: the watchlist defines the set of blocks to stack
  // vertically; when it is empty we fall back to the URL-resolved
  // symbolCtx so the page is never blank. Each block owns its own
  // tier state so adding BTC + ETH doesn't force them to share the
  // ±0.10% pill toggle.
  const blocks = watchlist.length > 0 ? watchlist : [symbolCtx.canonical];

  return (
    <div className="page-content active">
      <div className="section-bar">
        <span>3.2 · <b>深度对比</b></span>
        <div className="line" />
        <span>{blocks.length} 个标的 · 完整视图</span>
      </div>
      <WatchlistToolbar items={watchlist} symbols={allSymbols} onChange={onWatchlistChange} />
      <div className="grid">
        {blocks.map(sym => {
          const canonical = normalizeSymbol(sym);
          const meta = allSymbols.find(s => s.canonical.toUpperCase() === canonical);
          const snap =
            data.liquidityByCanonical[canonical]
            ?? (canonical === symbolCtx.canonical.toUpperCase() ? data.liquidity : null);
          return (
            <SymbolBlock
              key={canonical}
              canonical={canonical}
              displayName={meta?.display_name ?? sym}
              snapshot={snap}
              lookup={data.lookup}
              defaultTier={tier}
            />
          );
        })}
      </div>
    </div>
  );
}

type SymbolMeta = NonNullable<DashboardMeta['categories']>[number]['symbols'][number];

function QualityTab({ data, query, bucket, symbolCtx, watchlist, allSymbols, onWatchlistChange }: { data: DashboardData; query: Query; bucket: string; symbolCtx: SymbolContext; watchlist: string[]; allSymbols: SymbolMeta[]; onWatchlistChange: (next: string[]) => void }) {
  const rows = data.quality.rows ?? [];
  const rowByPlatform = new Map(rows.map(row => [row.platform, row]));
  const buckets = (data.quality.slippage_buckets_usd ?? [50_000, 100_000, 500_000, 1_000_000]).map(String);
  const symbol = symbolCtx.displaySymbol;

  // Quality watchlist mode mirrors LiquidityTab: when the watchlist
  // holds 2+ symbols, the V1 detail (three BarCharts + 盘口质量明细
  // table + funding span-24 row) collapses to a QualityCard grid.
  // The global SlippagePills above the grid owns query.bucket so all
  // cards render the same volume tier — the entire point of cards is
  // 'compare the same metric across symbols side-by-side'.
  if (watchlist.length > 1) {
    return (
      <div className="page-content active">
        <div className="section-bar">
          <span>3.5 · <b>盘口质量</b></span>
          <div className="line" />
          <span>{watchlist.length} 个标的 · 摘要视图 · 桶 {bucket === '1000000' ? '1M' : bucket === '500000' ? '500K' : bucket === '100000' ? '100K' : '50K'} USD</span>
        </div>
        <WatchlistToolbar items={watchlist} symbols={allSymbols} onChange={onWatchlistChange} />
        <div className="quality-bucket-bar">
          <span className="muted" style={{ fontSize: 12 }}>滑点桶 (USD):</span>
          <SlippagePills buckets={buckets} active={bucket} query={query} />
        </div>
        <div className="grid">
          {watchlist.map(sym => {
            const canonical = normalizeSymbol(sym);
            const meta = allSymbols.find(s => s.canonical.toUpperCase() === canonical);
            // Merge worst_slippage_bp + verdict from the quality
            // fan-out into the liquidity snapshot's rows so QualityCard
            // can read spread/share KPIs from the liquidity side AND
            // slippage/verdict from the quality side via a single
            // PlatformRow-shaped object. Without this merge the mini
            // bar chart degrades to "该标的暂无可绘制的滑点数据"
            // because /api/snapshot/liquidity always reports null on
            // those two fields in production.
            const liq = data.liquidityByCanonical[canonical] ?? null;
            const qual = data.qualityByCanonical[canonical] ?? null;
            const snap = mergeQualityIntoLiquidity(liq, qual);
            return (
              <QualityCard
                key={canonical}
                canonical={canonical}
                displayName={meta?.display_name ?? sym}
                snapshot={snap}
                bucket={bucket}
                buckets={buckets}
                onExpand={() => onWatchlistChange([canonical])}
              />
            );
          })}
        </div>
      </div>
    );
  }

  return (
    <div className="page-content active">
      <div className="section-bar"><span>3.5 · <b>盘口质量</b></span><div className="line" /><span>{symbolCtx.displayName} · Spread · Imbalance · 模拟下单滑点</span></div>
      <WatchlistToolbar items={watchlist} symbols={allSymbols} onChange={onWatchlistChange} />
      <div className="grid">
        <section className="panel span-8 row-h-md">
          <div className="panel-head"><span className="panel-title">Spread (bp)</span><span className="panel-sub">· 买一/卖一相对价差</span></div>
          <BarChart
            rows={rows.map(row => ({ label: row.platform, value: rowDisplayAvailable(row) ? row.spread_bp : undefined, status: row.depth_status, color: row.platform === 'edgeX' ? edgexAccent : '#5794f2' }))}
            sort="asc"
            format={(value, row) => `${(value ?? 0).toFixed(2)} bp · ${spreadUSDLabel(rowByPlatform, row.label, value)}`}
          />
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head"><span className="panel-title">模拟下单滑点 (bp)</span><span className="panel-sub">· 相对中间价</span><div className="panel-actions"><SlippagePills buckets={buckets} active={bucket} query={query} /></div></div>
          <div className="note">档位 <b>可配置</b>: 在运营后台 <code>config.slippage_buckets</code> 维护, 默认 [50K / 100K / 500K / 1M] USD。</div>
          <BarChart
            rows={rows.map(row => ({ label: row.platform, value: rowDisplayAvailable(row) ? row.worst_slippage_bp?.[bucket] : undefined, status: row.depth_status, color: row.platform === 'edgeX' ? edgexAccent : '#73bf69' }))}
            sort="asc"
            format={value => `${(value ?? 0).toFixed(2)} bp · ${usdLabel(slippageUSD(bucket, value), 0)}`}
          />
        </section>
        <section className="panel span-8 row-h-md">
          <div className="panel-head"><span className="panel-title">Bid/Ask Imbalance (%)</span><span className="panel-sub">· (BID深度-ASK深度)/合计</span></div>
          <div className="note">正值=买侧偏厚, 负值=卖侧偏厚, |值| &gt; 30% 视为单边报价偏离健康区间。</div>
          <BarChart
            signed
            rows={rows.map(row => ({ label: row.platform, value: rowDisplayAvailable(row) ? row.imbalance_pct : undefined, status: row.depth_status, color: row.platform === 'edgeX' ? edgexAccent : Math.abs(row.imbalance_pct ?? 0) > 30 ? '#f2495c' : '#5794f2' }))}
            format={value => signedPct(value)}
          />
        </section>
        <section className="panel span-24">
          <div className="panel-head"><span className="panel-title">盘口质量明细</span><span className="panel-sub">· 每行=一个平台</span><span className="panel-tag muted">CSV 可导</span></div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>平台</th><th className="num">Spread (bp)</th><th className="num">Mid 价格</th><th className="num">Imbalance (%)</th><th className="num">滑点 50K (bp)</th><th className="num">滑点 100K (bp)</th><th className="num">滑点 500K (bp)</th><th className="num">滑点 1M (bp)</th><th className="num" title={FUNDING_SIGN_CONVENTION_TOOLTIP}>资金费率 (8h) ⓘ</th><th>盘口结论</th></tr></thead><tbody>{rows.map(row => <tr key={row.platform}><td><PlatformCell platform={row.platform} displaySymbol={symbol} lookup={data.lookup} /></td><td className="num">{bp(row.spread_bp)}</td><td className="num">{money(row.mid_price)}</td><td className="num">{signedPct(row.imbalance_pct)}</td><td className="num">{bp(row.worst_slippage_bp?.['50000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['100000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['500000'])}</td><td className="num">{bp(row.worst_slippage_bp?.['1000000'])}</td><FundingCell funding={row.funding} /><td><VerdictBadge verdict={row.verdict} /></td></tr>)}</tbody></table></div>
        </section>
        <QualityFundingRow rows={rows} kpis={data.quality.kpis} />
      </div>
    </div>
  );
}

function ShareTab({ data, query }: { data: ShareSnapshot; query: Query }) {
  const activeWindow = query.window ?? data.window ?? '24h';
  const rawRows = [...data.rows].sort((a, b) => {
    const av = typeof a.raw_volume_usd === 'number' ? a.raw_volume_usd : Number.NEGATIVE_INFINITY;
    const bv = typeof b.raw_volume_usd === 'number' ? b.raw_volume_usd : Number.NEGATIVE_INFINITY;
    return bv - av;
  });
  const rawDenominator = rawRows.reduce((sum, row) => sum + (row.raw_volume_usd ?? 0), 0);
  const edgexRaw = rawRows.find(row => row.platform === 'edgeX')?.raw_volume_usd;
  const edgexRawShare = rawDenominator > 0 && typeof edgexRaw === 'number' ? edgexRaw / rawDenominator * 100 : undefined;
  return (
    <div className="page-content active">
      <div className="section-bar"><span>3.4 · <b>edgeX 平台总交易量市占率</b></span><div className="line" /><span>全平台 perp 合计</span></div>
      <section className="panel">
        <div className="panel-head">
          <span className="panel-title">edgeX 平台总交易量市占率明细表</span>
          <span className="panel-sub">· 切换口径查看 share + 各平台贡献 · 数据=全平台所有 perp 合计成交量</span>
          <div className="panel-actions"><span className="panel-tag muted">CSV 可导</span><WindowPills active={activeWindow} query={query} /></div>
        </div>
        {data.status === 'unsupported' ? <StatusEmptyState status="unsupported" message={data.reason ?? '当前统计窗口尚未实现'} /> : <>
          <div className="share-kpi-strip">
            <div className="share-primary"><span>当前 edgeX share {activeWindow}</span><b>{pct(edgexRawShare)}</b></div>
            <div><span>edgeX 平台总成交量</span><b>{money(data.kpis?.edgex_total_volume_usd ?? edgexRaw)}</b></div>
            <div><span>分母合计 (含 edgeX)</span><b>{money(rawDenominator)}</b></div>
            <p className="share-kpi-note">
              数据=全平台所有 perp 合计成交量真实值;<br />
              edgeX 自身也计入分母。
            </p>
          </div>
          <div className="table-wrap"><table className="tbl"><thead><tr><th>#</th><th>平台</th><th className="num">成交量 (USD)</th><th className="num">在分母中占比</th><th>占比可视化</th></tr></thead><tbody>{rawRows.map((row, index) => {
            const share = typeof row.raw_volume_usd === 'number' && rawDenominator > 0
              ? row.raw_volume_usd / rawDenominator * 100
              : undefined;
            return (
              <tr key={row.platform}>
                <td>{index + 1}</td>
                <td><span className={row.platform === 'edgeX' ? 'platform-self' : undefined}>{platformDisplayName(row.platform)}</span></td>
                <td className="num"><b>{moneyAuto(row.raw_volume_usd)}</b></td>
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
      <section className="panel row-h-md">
        <div className="panel-head"><span className="panel-title">平台总市占率时序 (近 30d)</span><span className="panel-sub">· 三口径滚动叠加</span></div>
        {(() => {
          const points = (data.trend?.points ?? []) as ShareTrendPoint[];
          const status = data.trend?.status ?? 'unsupported';
          if (!points.length) {
            return <StatusEmptyState status={status} message="历史时序聚合数据不足" />;
          }
          return <ShareTrendChart ariaLabel="edgeX 市占率时序 (近 30 天，24h / 7d / 30d 滚动)" points={points} />;
        })()}
      </section>
    </div>
  );
}

const COMPETITOR_TOTAL = 9;

function isResolvedStatus(status: string | undefined): boolean {
  return !status || status === 'complete';
}

function renderListedCell(row: Top30Row) {
  if (row.edgex_listed_status && !isResolvedStatus(row.edgex_listed_status)) {
    return <StatusBadge status={row.edgex_listed_status} />;
  }
  return row.edgex_listed ? '是' : '否';
}

function renderCoverageCell(row: Top30Row) {
  if (row.competitor_top30_coverage_status && !isResolvedStatus(row.competitor_top30_coverage_status)) {
    return <StatusBadge status={row.competitor_top30_coverage_status} />;
  }
  return `${row.competitor_top30_coverage ?? 0} / ${COMPETITOR_TOTAL}`;
}

const ACTION_CLASS: Record<string, string> = {
  '考虑拉新活动': 'action-hot',
  '优先上架': 'action-listing-prio',
  '评估上架': 'action-listing-eval',
  '保持': 'action-hold',
  '观望': 'action-wait',
};

function renderActionCell(row: Top30Row) {
  if (row.suggested_action_status && !isResolvedStatus(row.suggested_action_status)) {
    return <StatusBadge status={row.suggested_action_status} />;
  }
  const action = row.suggested_action ?? '';
  if (!action) return '';
  const variant = ACTION_CLASS[action];
  return <span className={variant ? `action-cell ${variant}` : 'action-cell'}>{action}</span>;
}

// top30ViewItems exposes the two sub-views of the Top30 tab. "per-platform"
// is the legacy per-exchange Top30 table; "divergence" is the new CEX vs
// DEX aggregate comparison. The active key is read from query.view; empty
// / unknown values fall back to per-platform so existing bookmarks keep
// rendering the table they were captured at.
const top30ViewItems = [
  { key: 'per-platform', label: '各平台 Top30' },
  { key: 'divergence', label: 'CEX vs DEX 对比' },
] as const;

function Top30ViewPills({ active, query }: { active: string; query: Query }) {
  return (
    <span className="pill-group" aria-label="Top30 子视图">
      {top30ViewItems.map(item => (
        <Link className={`pill ${item.key === active ? 'active' : ''}`} href={withQuery(query, { tab: 'top30', view: item.key })} key={item.key}>
          {item.label}
        </Link>
      ))}
    </span>
  );
}

function Top30Tab({ data, divergence, lookup, query, platform }: { data: Top30Snapshot; divergence: Top30DivergenceSnapshot; lookup: FrontendURLLookup; query: Query; platform: string }) {
  const view = query.view === 'divergence' ? 'divergence' : 'per-platform';
  const platforms = ['binance', 'okx', 'bybit', 'bitget', 'mexc', 'gate', 'bingx', 'hyperliquid', 'lighter', 'edgeX'];
  return (
    <div className="page-content active">
      <div className="section-bar">
        <span>4 · <b>Top30 成交量</b></span>
        <div className="line" />
        <Top30ViewPills active={view} query={query} />
      </div>
      {view === 'divergence' ? (
        <Top30DivergenceView snapshot={divergence} lookup={lookup} />
      ) : (
        <section className="panel">
          <div className="panel-head"><span className="panel-title">各平台 Top30 成交量</span><PillGroup items={platforms} active={platform} query={{ ...query, tab: 'top30', view: 'per-platform' }} param="platform" /></div>
          {data.status === 'unsupported' ? <StatusEmptyState status="unsupported" message={data.platform === 'edgeX' ? 'not implemented' : '尚未返回该平台 Top30 排行，等待下一次拉取'} /> : null}
          <div className="table-wrap"><table className="tbl"><thead><tr><th className="num">#</th><th>Symbol</th><th className="num">24h Vol</th><th className="num">7d Vol</th><th className="num">7d Δ</th><th className="num">edgeX 已上线?</th><th className="num">竞品 Top30 覆盖</th><th>建议动作</th></tr></thead><tbody>{data.rows.map(row => <tr key={`${row.rank}-${row.symbol}`}><td className="num">{row.rank}</td><td>{row.symbol}</td><td className="num">{row.status === 'unsupported' ? '—' : money(row.volume_24h_usd)}</td><td className="num">{typeof row.volume_7d_usd === 'number' ? money(row.volume_7d_usd) : <StatusBadge status={row.volume_7d_status ?? 'unsupported'} />}</td><td className="num">{typeof row.delta_7d_pct === 'number' ? pct(row.delta_7d_pct) : <StatusBadge status={row.delta_7d_status ?? 'unsupported'} />}</td><td className="num">{renderListedCell(row)}</td><td className="num">{renderCoverageCell(row)}</td><td>{renderActionCell(row)}</td></tr>)}</tbody></table></div>
        </section>
      )}
    </div>
  );
}
