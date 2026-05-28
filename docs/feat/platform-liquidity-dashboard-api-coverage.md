# 平台流动性 Dashboard 正式版接口覆盖记录

## 状态更新（2026-05-28）

本文是 V1 视觉对齐阶段（2026-05 中旬）的"接口覆盖快照"，描述了**当时**哪些 API 返回真实数据、哪些以 `unsupported` 保留前端结构。**下方"尚未实现或仍存在问题的接口能力"章节里多个 `unsupported` 项目已被后续 feature 落地实现**：

| 当时状态 | 现状 | 由哪个 feature 取代 |
|---|---|---|
| `share?window=7d\|30d` → `status=unsupported` | 已实现，按窗口聚合 share/denominator/trend | `top30-share-history-backfill.md` |
| `trend.status=unsupported` | 已实现，30d 时序点真实绘制 | `top30-share-history-backfill.md` |
| `top30?surface=perp` → 顶层 `unsupported` | 已实现 live ranking + `volume_7d_usd` / `delta_7d_pct` / `edgex_listed` / `competitor_top30_coverage` / `suggested_action` | `top30-share-history-backfill.md` |
| `symbol_share_7d_status` / `symbol_share_wow_status` / `edgex_spread_10m_status` → `unsupported` | 全部 complete，KPI 卡正式显示数值 | `top30-share-history-backfill.md` |

后续又叠加了：

- **盘口深度 4 档多视图选择 + loose 灰度展示** — `adapter-four-tier-depth.md`
- **24h share CoinGecko fallback**（解决 BTC/ETH 偶尔 0.00% 的问题）— `liquidity-24h-share-cg-fallback.md`
- **跨平台资金费率独立 Tab + APR 副线 + diverging bar** — `funding-rate.md`
- **流动性 Watchlist 多 symbol 垂直堆叠 + 每图 line/bar 切换** — `liquidity-watchlist.md`
- **9 家原生 adapter + Lighter WS 走可选代理**（解决容器内 GFW 阻断）— `native-exchange-proxy.md`
- **Listing Agent Top30 hot-gap Lark 推送**（CoinGecko Top30 → outbox → 飞书群）— `listing-agent-top30-hot-gap-push.md`

下方原始章节作为历史记录保留，方便审阅当时的设计决策与 review 修正，但 `unsupported` 字段标注**不再是当前生产状态**——以上方表格 + 链接的新文档为准。

---

## 背景

本轮改造目标是将 HTML 原型中的视觉结构落成 `edgex-dashboard` 正式版前端，同时保持“真实数据优先、不造假”的数据原则。当前后端已经具备部分实时盘口、深度和 24h 成交量能力；暂未实现的历史、市占率趋势和 Top30 live ranking 能力统一以 `unsupported` 返回，并由前端保留正式展示结构。

## Review 结论与修正

实现后进行了两类只读 review：

1. **规格符合性 review**
   - 发现图表对 `stale/error/unsupported` 数据存在被显示为 `0` 的风险。
   - 发现 Top30 未实现时仍返回硬编码 symbol/rank，容易被误读为真实榜单。
   - 发现 Share 缺失成交量平台会显示 `$0`，容易被误读为真实 0 成交量。

2. **代码质量 review**
   - 发现市占率 tab 默认窗口会停留在 `7d`，导致已实现的 `24h` 数据默认不可见。
   - 发现缺少竞品中位数时深度状态会被标记为“达标”。
   - 发现成交量状态按 map 遍历取值，存在非确定性。

已修正：

- 图表只绘制 `complete / partial / aggregated_orderbook / ws_limited_depth` 数据。
- Top30 未实现时返回空 `rows` + 顶层 `status=unsupported`。
- Share 缺失成交量平台不返回数值字段，只返回状态。
- 切换到市占率 tab 时默认使用 `24h`。
- 缺少竞品中位数时深度状态返回 `unsupported`。
- 平台成交量状态改为确定性聚合。

## 已完整支撑当前前端展示的接口

### `GET /api/dashboard/meta`

**前端使用位置**

- `web/app/page.tsx`
- `web/components/dashboard-shell.tsx`
- `web/components/dashboard-controls.tsx`

**支撑能力**

- tabs；
- platforms；
- symbols；
- windows；
- depth tiers；
- slippage buckets；
- refresh interval；
- volume discounts。

**当前状态**

- 已满足正式前端的基础控件和自动刷新展示需求。

### `GET /api/snapshot/liquidity?symbol=...&window=...`

**前端使用位置**

- `web/app/page.tsx`
- `web/components/dashboard-shell.tsx` 的“流动性监控” Tab。

**已支持字段**

- `symbol`
- `snapshot_ts`
- `rows[].depth_by_tier`
- `rows[].depth_status`
- `rows[].partial_reason`
- `rows[].vs_median_by_tier`
- `rows[].rank_0_1`
- `rows[].depth_status_label`
- `competitor_median_by_tier`
- `kpis.edgex_depth_by_tier`
- `kpis.edgex_vs_median_by_tier`
- `kpis.edgex_spread_bp`
- `kpis.edgex_24h_share_pct`

**当前状态**

- 已支撑正式前端的深度 KPI、三张深度图（line / bar 视图可切换）和深度明细表。
- 竞品中位数不包含 edgeX。
- 缺失、异常、过期平台不参与中位数和排名。

**仍以 `unsupported` 展示的字段**

- `kpis.symbol_share_7d_status`
- `kpis.symbol_share_wow_status`
- `kpis.edgex_spread_10m_status`

### `GET /api/snapshot/quality?symbol=...`

**前端使用位置**

- `web/app/page.tsx`
- `web/components/dashboard-shell.tsx` 的“盘口质量” Tab。

**已支持字段**

- `symbol`
- `snapshot_ts`
- `slippage_buckets_usd`
- `rows[].mid_price`
- `rows[].spread_bp`
- `rows[].imbalance_pct`
- `rows[].buy_slippage_bp`
- `rows[].sell_slippage_bp`
- `rows[].worst_slippage_bp`
- `rows[].verdict`
- `rows[].depth_status`

**当前状态**

- 已支撑 Spread、模拟滑点、Imbalance 三类图表和盘口质量明细表。
- 滑点已按相对 `mid` 口径计算。
- 异常盘口不会被表示为“0 spread 的健康盘口”。

### `GET /api/snapshot/share?window=24h`

**前端使用位置**

- `web/app/page.tsx`
- `web/components/dashboard-shell.tsx` 的“市占率” Tab。

**已支持字段**

- `window`
- `snapshot_ts`
- `denominator_usd`
- `kpis.edgex_share_pct`
- `kpis.edgex_total_volume_usd`
- `kpis.denominator_usd`
- `rows[].rank`
- `rows[].platform`
- `rows[].raw_volume_usd`
- `rows[].discount`
- `rows[].adjusted_volume_usd`
- `rows[].denominator_pct`
- `rows[].status`

**当前状态**

- 已支撑正式前端 24h 市占率 KPI 和明细表。
- MEXC `0.4`、Gate `0.5` 折算只应用在成交量/share。
- 缺失成交量的平台只展示状态，不展示 `$0` 伪数值。

## 尚未实现或仍存在问题的接口能力

### `GET /api/snapshot/share?window=7d|30d`

**当前返回**

- `status=unsupported`
- `reason=historical platform share is not implemented yet`
- `rows=[]`
- `trend.status=unsupported`

**对前端影响**

- 7d / 30d 窗口保留正式面板结构，但展示 unsupported 空状态。

**后续补齐建议**

- 增加小时或日级成交量聚合表；
- 由后端按窗口聚合 `raw_volume_usd` / `adjusted_volume_usd`；
- 对外输出与 24h 同构的 rows 和 kpis。

### 市占率近 30d 时序

**当前返回**

- `trend.status=unsupported`

**对前端影响**

- “平台总市占率时序 (近 30d)”面板保留，但内部展示 unsupported。

**后续补齐建议**

- 基于每日聚合表输出 `share_24h_pct / share_7d_pct / share_30d_pct` 时间序列；
- 前端无需重构，只需要将 `trend.points` 接入当前图表容器。

### `GET /api/snapshot/top30?surface=perp&platform=...`

**当前返回**

- 顶层 `status=unsupported`
- `rows=[]`

**对前端影响**

- Top30 Tab 保留正式表头、平台切换和 unsupported 状态说明，但不展示任何伪排名。

**后续补齐建议**

- 在 adapter 层实现各平台 live ranking；
- 后端落库 `rank/symbol/volume_24h_usd/snapshot_ts/source_endpoint/status`；
- 后端补齐 `volume_7d_usd`、`delta_7d_pct`、`edgex_listed`、`competitor_top30_coverage`、`suggested_action`；
- 前端可直接复用当前表格列。

### 单交易对 7d 市占率、WoW、10min spread 均值

**当前返回**

- `symbol_share_7d_status=unsupported`
- `symbol_share_wow_status=unsupported`
- `edgex_spread_10m_status=unsupported`

**对前端影响**

- 流动性监控 KPI 面板展示正式位置和 unsupported 状态。

**后续补齐建议**

- 存储 symbol 级历史成交量快照；
- 存储盘口质量时间序列；
- 后端聚合输出 7d share、WoW pp 和 10min spread avg。

## 当前前端展示覆盖情况

| Tab | 视觉结构 | 当前数据状态 |
|---|---|---|
| 流动性监控 | 已对齐 top KPI、三张曲线、深度明细表 | 深度、排名、中位数使用真实快照；历史类 KPI unsupported |
| 盘口质量 | 已对齐三张质量图和明细表 | 当前盘口指标使用真实快照；缺失平台按状态展示 |
| 市占率 | 已对齐 KPI、明细表、时序面板 | 24h 可展示；7d/30d 和趋势 unsupported |
| Top30 成交量 | 已对齐平台切换和正式表头 | live ranking 未实现，整体 unsupported |

## 验证命令

本记录对应的实现应通过以下命令验证：

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-dashboard/backend
make test
make smoke-api PORT=18080 SYMBOL=BTC-USDT
```

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-dashboard/web
npm run lint
npm run typecheck
npm run build
```
