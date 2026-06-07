# 流动性自选清单（Liquidity Watchlist）

## 背景

V1 流动性 Dashboard 一次只能聚焦一个 symbol，运营在 launch 窗口
内同时关注 3–5 个标的时必须在 dropdown 之间来回切换，丢失上下文。

本 feature 在 Liquidity Tab 引入「自选清单」模式：

- 链接可分享（URL 参数）；
- 浏览器可记忆（localStorage）；
- 多标的并行 fan-out 拉取，单失败不影响其它卡片；
- 空清单自动回退到默认 BTC，避免空白页。

实现遵守 v2.1 spec 的 6 条决策（见 `architecture/方案设计/EdgeX 运营/
需求梳理/01-平台流动性 Dashboard-需求梳理.md`）。

## v2 更新（2026-05-27）：Liquidity Tab 统一为 SymbolBlock 垂直堆叠

`commit 6c51382 feat(edgex-ops-intelligence): unify liquidity monitor into
stacked SymbolBlocks` 把 Liquidity Tab 的 dual-mode 渲染合一：

**之前（v1 watchlist）**：
- 单 symbol → V1 完整视图（KPI + 深度曲线 + 深度明细表）
- 多 symbol → `WatchlistCard` 缩略卡片网格 + 「查看明细 →」按钮回到单 symbol

**现在（v2 watchlist）**：
- 不论 watchlist 长度（1 或 N），全部走 `SymbolBlock`，每个 symbol
  一个独立 `<section.panel.span-24>` 帧垂直堆叠
- 每个 SymbolBlock 内部就是 V1 的"完整视图子集"：edgeX 深度 KPI
  + 7d 市占率 + 当前 spread 三个 row-h-sm 卡 + BID / ASK / 合计三
  条 row-h-md 深度曲线
- **每个 block 拥有自己的档位 pill 状态**（`useState`），可以让 BTC
  设 ±0.10%、ETH 同时设 ±1%，互不干扰
- 添加第二个 symbol 不再把第一个塌成缩略图、也不再强制共享档位

**Liquidity Tab 隐藏（保留 JSX 注释）的三块面板**（与该 commit
一致，便于日后回滚）：
1. `edgeX spread (10min 均值)` row-h-sm 面板
2. `FundingKpiPanel`（edgeX 资金费率 + vs 中位数 delta + 注脚）
3. `深度明细 · 平台 × 档位 (USD)` 跨平台表格

辅助函数（`FundingKpiPanel` / `DepthCell` / `FundingCell` /
`depthLabelBadgeClass` 等）保留在 `dashboard-shell.tsx` 中，未来回滚
不需要 code resurrection。

**Quality Tab 不受影响**：仍走 `QualityCard` 网格（dual-mode 见下方
"UI 路由策略"表格的 Quality 列），SlippagePills 仍是 Quality 多 symbol
模式独有的全局桶切换条。

## v3 更新（2026-05-27）：每张深度图独立 line / bar 切换

`commit e72754c feat(edgex-ops-intelligence): add per-chart line/bar toggle
on SymbolBlock depth views` 把 SymbolBlock 内 BID / ASK / 合计三张
深度图从"固定折线"升级为"可切换 line / bar 视图"，并把此前 v2 末
尾的"显示方案对比预览"（sqrt / dual-range / bump 候选）从 SymbolBlock
完全移除。

设计要点：

- **per-chart 切换，不是 SymbolBlock 级全局切换**：每张深度图各自
  持有 `useState<DepthChartMode>('line')`，pill-group 渲染在该图自
  己的 panel-head 右侧（`margin-left: auto`）；BTC 的 BID 切到 bar
  不会影响 BTC 的 ASK 或其它 SymbolBlock；
- **bar 视图 = small-multiples 横向条形**：4 个 tier 各占一个独立
  X 轴 panel，平台按 USD 深度降序排列，edgeX 实色 + 白描边强调，
  竞品压暗，每根条尾直接打 USD 标签（不依赖 hover）。这才彻底解决
  线性单图里"低档位被高档位 250M 压扁、edgeX 贴 X 轴"的根因；
- **动态 span**：line 模式 → `panel span-8 row-h-md`（三图并排）；
  bar 模式 → `panel span-24`（独占一行，给 2×2 small-multiples 留
  足空间）。混合模式（如 BID bar + ASK line + 合计 line）在 24-col
  grid 里自然换行：BID 独占第一行，ASK + 合计 在第二行（剩 8 col
  空白可接受）；
- **临时预览全部下线**：sqrt-line-chart / dual-range-line-chart /
  bump-chart 三个组件文件按用户指示**保留在仓库里**以便日后复用，
  但不再在 SymbolBlock 中挂载。`chart-plugins.ts` 的
  `edgeXValueLabelPlugin` 仍由 EdgeXHeroChart 等候选组件依赖。

新组件 / testid：

- `web/components/small-multiples-bar-chart.tsx` — 正式版 bar 视图，
  每 tier 一个 `<canvas>`，2×2 grid 高度 460px；
- `DepthChartSection` 是 `symbol-block.tsx` 内部抽出的小组件，
  封装"panel-head + toggle + line/bar 渲染"；
- `data-testid="symbol-block-mode-${canonical}-${bid|ask|total}-${line|bar}"`
  标识每个 toggle pill。

## 状态来源与合并优先级

合并优先级（在 `web/lib/watchlist.ts` 的 `resolveWatchlist` 中实现）：

```text
URL ?watchlist=...   >   localStorage   >   [WATCHLIST_DEFAULT_FALLBACK]
```

行为细节：

- URL 形式：`?watchlist=BTC,ETH,SOL`，逗号分隔、大小写不敏感、
  自动去重 + 上限 10 个。
- localStorage key：`edgex-ops-intelligence:watchlist:v1`（v1 后缀方便未
  来升级）。
- SSR 阶段只读取 URL，绝不访问 `window`，防止 hydration mismatch。
- CSR mount 后读取 localStorage 并和 SSR 列表合并，差异时通过
  `history.replaceState` 把合并后的 list 回填到 URL，保证“刷新一
  下” 行为一致。
- 任何 mutation（add/remove）都走 `dedupeAndCap`，再写回 URL +
  localStorage，避免三处 source-of-truth 漂移。
- 空清单 → 回退到 `WATCHLIST_DEFAULT_FALLBACK = 'BTC'`，由
  `DashboardClient` 中的 effect 强制注入，Toolbar 本身保持纯受
  控组件。
- V1 上限 `MAX_WATCHLIST = 10`，避免 fan-out 请求数过载与 toolbar
  chip 行 wrap。

## 数据加载（Fan-out + AbortController）

核心位置：

- `web/components/dashboard-client.tsx`
- `web/lib/api/fetcher.ts`

加载流程：

1. headline 路径（meta / liquidity / quality / share / top30）仍
   使用 `query.symbol` 作为单 symbol 视图入口，**保留 V1 deep-link
   契约**：分类切换 / dropdown / 旧链接都不变。
2. 与 headline 并行，按 watchlist 中每个 symbol 调一次 `/api/
   snapshot/liquidity?symbol=…`，结果汇聚到
   `liquidityByCanonical: Record<canonical, LiquiditySnapshot>`。
3. 一个 AbortController 覆盖整个 cycle；下一次 refresh 启动前先
   `controller.abort()`，避免“快速增删 chip → 大量 ghost 请求”。
4. 单 symbol fan-out 失败被吞掉（卡片单独降级），AbortError 必
   须穿透不能 fall-through 到 cache（在 `getJSONWithFallback` 中
   显式 rethrow）。

UI 路由策略：

| 状态 | LiquidityTab 行为（v2，统一 SymbolBlock） | QualityTab 行为（保持 dual-mode） |
|---|---|---|
| `watchlist.length === 1` | `WatchlistToolbar` + 单个 `SymbolBlock` span-24（即原 V1 单 symbol 完整子集，独占帧） | 保留 V1 单 symbol 视图（Spread / 模拟滑点 / Imbalance 三 BarChart + 盘口质量明细 + funding span-24 行） |
| `watchlist.length > 1`  | `WatchlistToolbar` + `SymbolBlock × N` 垂直堆叠（每个 block 独占 span-24，自带档位 pill 状态） | 卡片网格：`WatchlistToolbar` + 全局 SlippagePills + `QualityCard × N` |
| `watchlist.length === 0` | 由 effect 注入 BTC fallback，回到上面的第一种（单 SymbolBlock） | 同 Quality 单 symbol |

> 注意：v2 之后 Liquidity Tab **不再有"卡片缩略 ↔ 详情"模式切换**，
> "查看明细 →" 按钮也随之消失（之前 `WatchlistCard` 右上角的入口）。
> 想从多 symbol 收敛回单 symbol，删除 toolbar 上多余的 chip 即可。

Share / Top30 不进入卡片模式，仍由 `window` / `platform` 控制，避免视图横向膨胀。

切换 Tab 时 watchlist 状态自动保留 —— 它由 `DashboardClient` 顶
层管理，URL `?watchlist=` 是全局参数，所以从 Liquidity 切到 Quality
不会丢失自选。

### Quality 卡片的数据来源（零额外 fan-out）

`QualityCard` 不会触发新的 `/api/snapshot/quality` 请求 —— 它直接
从 `liquidityByCanonical[canonical].rows` 派生：每个平台的
`PlatformRow` 已经携带 `spread_bp` / `mid_price` / `imbalance_pct`
/ `worst_slippage_bp` (NumberMap by USD bucket) / `verdict` /
`funding`，正好等于盘口质量明细表的列。对一个 5 symbol 自选清单
而言，省了 5 次额外 API 请求，且与 Liquidity 卡片读同一份数据，
保证两个 Tab 看到的快照绝对一致。

如果未来 Quality 需要"纯 quality" 字段（比如 `kpis.competitor_
slippage_median_by_bucket`），再升级到对称 fan-out（`qualityBy
Canonical`）；当前阶段不引入。

## 组件结构

| 组件 | 职责 |
|---|---|
| `WatchlistToolbar` | chips 行 + add 下拉（带搜索 / max-cap 禁用）+ 每个 chip 的 remove ×；提交时同步写 URL + localStorage |
| `SymbolBlock` (v2 新增 / v3 升级) | **Liquidity Tab 唯一的 symbol 渲染单元**：单个 symbol 的完整流动性视图（edgeX 深度 + 7d 市占率 + 当前 spread + BID / ASK / 合计三张深度图）；v3 起每张深度图通过内嵌 `DepthChartSection` 提供 line / bar 视图切换，line=折线 span-8、bar=small-multiples span-24；自带 `useState<tier>` 让多 symbol 各自独立切换档位 pill；外层用 `<section.panel.span-24>` 包成独立帧，多 symbol 通过垂直堆叠呈现。`data-testid="symbol-block-${canonical}"` |
| `SmallMultiplesBarChart` (v3 新增) | bar 视图的渲染组件：4 个 tier 一个 2×2 grid，每个 panel 拥有独立 X 轴量纲，平台按 USD 深度降序，edgeX 实色 + 白描边强调，每根条尾直接打 USD 标签 |
| `WatchlistCard` | **Quality Tab 之前用过的流动性卡片，v2 之后 Liquidity Tab 已不再使用**（文件保留以便日后回滚）：edgeX 深度 / vs 中位数 / spread10m / spread 当前 / 7d share / 资金费率 + delta；底部迷你深度曲线 + 合计/BID/ASK 切换胶囊；右上角「查看明细 →」 |
| `QualityCard` | 盘口质量卡片（**仍是 Quality Tab 多 symbol 模式的渲染主力**）：edgeX spread 10min / spread 当前 (bp+USD) / Imbalance / 滑点 @ bucket (bp+USD) / vs 竞品中位数滑点 / 7d share / 资金费率 + delta / verdict 徽章；底部嵌入 **edgeX 滑点 by bucket 迷你 BarChart**；右上角「查看明细 →」 |
| `DashboardClient` | 状态总线：解析 URL、调和 localStorage、fan-out 拉数据、empty 回退、watchlist 任意变更后通过同一 effect 同步 URL + localStorage（含「查看明细」、chip add/remove、空回退） |
| `dashboard-shell.tsx#LiquidityTab` | v2：watchlist 任意长度都走 `WatchlistToolbar` + `SymbolBlock × N`，不再 dual-mode |
| `dashboard-shell.tsx#QualityTab` | 仍 dual-mode：单 symbol 走 V1 完整视图，多 symbol 走 `SlippagePills` + `QualityCard × N` |

样式落在 `web/app/globals.css`：

- `.watchlist-toolbar` / `.watchlist-chip*` 与 dashboard panel 配色
  一致；
- `.symbol-block` (v2 新增) 是 SymbolBlock 的外层 panel 帧；与
  Liquidity Tab 内嵌 `.grid` + `span-N` 协作，让 KPI 行（span-6 ×
  3）和深度曲线行（span-8 × 3）在 24 列 grid 内自然分布；
- `.watchlist-card` / `.quality-card` 都用 `flex-direction: column`；
  `.watchlist-kpis` 默认两列，窄屏自动塌成一列（`watchlist-card`
  v2 之后 Liquidity Tab 不再使用，但样式保留供 Quality 复用 / 回滚使用）；
- `.watchlist-card-chart` (160px) / `.quality-card-chart` (140px)
  控制迷你图高度；
- `.pill-group-mini .pill-mini` 用于卡内 BID/ASK/合计 切换、以及
  SymbolBlock 顶部的档位 pill 选择条（每个 block 一组、互不串号）；
- `.quality-bucket-bar` 是 Quality 卡片网格顶部的全局桶切换条；
- `.info-icon` 通用 `ⓘ` 提示样式，资金费率 tooltip 复用。

## SymbolBlock 行为约定（v2）

落在 `web/components/symbol-block.tsx`，单元素 24-column grid 内嵌：

| 区块 | 跨度 | 数据来源 |
|---|---|---|
| 顶部 panel-head | span-24 | `displayName` + 「· 流动性监控 · 4 档深度」固定副标题 |
| edgeX ±tier 总深度 | span-6 row-h-sm | `rows.find(p => p.platform === 'edgeX').depth_by_tier[tier].total_usd`；副标题展示 vs 中位数比例；右上角 `pill-group-mini` 切换 `tier` |
| 当前交易对 7d 市占率 | span-6 row-h-sm | `kpis.symbol_share_7d_pct` |
| edgeX 当前 spread | span-6 row-h-sm | `kpis.edgex_spread_bp` + 24h share 副标 |
| BID 深度（DepthChartSection） | line=span-8 row-h-md / bar=span-24 | `tierSeries(rows, 'bid_usd')`；自带 line / bar 切换 pill |
| ASK 深度（DepthChartSection） | line=span-8 row-h-md / bar=span-24 | `tierSeries(rows, 'ask_usd')`；自带 line / bar 切换 pill |
| 合计深度（DepthChartSection） | line=span-8 row-h-md / bar=span-24 | `tierSeries(rows, 'total_usd')`；自带 line / bar 切换 pill |

行为约定：

- **独立 tier 状态**：每个 SymbolBlock 用 `useState<string>(defaultTier
  ?? '0.10%')` 管理自己的档位 pill。父层的 `query.tier` 仅作为初始
  值传入 `defaultTier`，之后各 block 独立演化，避免「调 BTC 档位
  连带改了 ETH 大数」的串号问题；
- **空快照降级**：snapshot 为 null 时直接渲染 `StatusEmptyState
  status="stale" message="尚未拉取到该标的的快照"`，单 block 失
  败不影响其它 block；
- **隐藏面板可恢复**：edgeX 10min spread / FundingKpiPanel / 深度
  明细表均以 JSX 注释形式保留在文件中，未来按 spec 决策恢复时不
  需要去 git 历史里挖；
- **数据复用**：所有 block 都从 `data.liquidityByCanonical` 取
  snapshot；当某 canonical 与 URL 上的 `symbolCtx.canonical` 一致
  且 fan-out 还未填进 dict 时，回退使用 `data.liquidity`，保证
  headline path 与 watchlist path 不会撞车。

> ✅ v3 起 SymbolBlock 已不再挂任何"显示方案对比预览"。bar 视图被
> 选中作为 line 的对等正式视图，由每张图的 `DepthChartSection` 内
> 部 toggle 切换；sqrt-line-chart / dual-range-line-chart / bump-chart
> 等候选组件文件按用户指示**保留在仓库**以便日后复用，但已不再被
> SymbolBlock 引用。EdgeXHeroChart 及其专用 CSS（`.hero-chart-wrap`、
> `.tier-rank-grid`、`.dual-range-grid` 等）也是同一原因保留。

## 测试影响 & follow-up

v2 SymbolBlock 改造没有同步更新现有 e2e 选择器，存在已知偏差：

- `web/e2e/watchlist.spec.ts` 与 `web/e2e/docker-smoke.spec.ts` 中
  对 Liquidity Tab 多 symbol 视图的断言仍以 `watchlist-card-${sym}`
  / `watchlist-card-chart-${sym}` / `watchlist-card-side-${sym}-…`
  / `watchlist-card-expand-${sym}` 为目标，这些 testid 在 v2 已被
  `symbol-block-${canonical}` 单一 testid 取代；
- 「查看明细 →」相关用例 (`watchlist-card-expand-*`) 现已无对应
  入口，必须改为「删除 toolbar 上多余 chip」的等价操作；
- 「卡内合计/BID/ASK 切换」相关断言（`watchlist-card-side-*`）在
  v2 SymbolBlock 中变成了三张并排的独立深度图，不再有切换胶囊，
  因此对应用例需要重新设计或迁移成 SymbolBlock 内部的档位 pill
  断言（`pill-group-mini` 上的 `0.05% / 0.10% / 1% / 2%`）；
- v3 起每张深度图新增 line / bar 视图 toggle，testid 形如
  `symbol-block-mode-${canonical}-${bid|ask|total}-${line|bar}`，
  现有 e2e 用例尚未覆盖（默认 line 模式行为不变）；
- Quality Tab 用例不受影响，QualityCard testid 与切换流均保留。

Follow-up TODO（不在本 commit 范围内）：把上述 e2e 用例迁移到
`symbol-block-*` 选择器，并把「收敛到单 symbol」的入口测试从「查
看明细按钮」换成「删除 chip 直到剩 1 个」。

## 测试覆盖

> ⚠️ v2 SymbolBlock 改造尚未同步 e2e 选择器，下列断言中针对 Liquidity
> Tab `watchlist-card-*` testid 与「查看明细」按钮的用例**当前是失败
> 状态**，需按上文「测试影响 & follow-up」迁移到 `symbol-block-*`
> 选择器。Quality Tab 部分仍然全部有效。

### Mock-fixture 套件：`web/e2e/watchlist.spec.ts`

绑定 `fixtures-watchlist.ts`，覆盖：

- 默认访问展示 BTC fallback chip + toolbar；
- `?watchlist=BTC,ETH,SOL` 渲染 chip 并切换到卡片模式；
- 下拉添加 → URL + localStorage 同步；
- 删除最后一个 chip → 回退到 BTC，URL / storage 持久化为 `['BTC']`；
- localStorage 预置 → URL 反向回填；
- URL 优先于 localStorage；
- funding KPI 渲染 `+0.0050%` + vs 中位数 delta（数值符合
  CoinGecko 百分比单位约定）；
- Liquidity 明细表「资金费率 (8h)」列含 `unsupported → —`；
- Quality 底部 span-24 funding panel 含「3 样本」中位数注脚；
- 4 卡片视图深度数值真正不同（防 reducer 把所有卡指向同一份数据
  的回归 guard）；
- 卡片底部迷你深度曲线 + 合计/BID/ASK 切换；
- 切换胶囊后 aria-label 翻转，不同卡片间状态相互独立；
- 「查看明细」收敛 watchlist 到该 symbol；
- **Quality Tab 自选清单镜像**：`?watchlist=…&tab=quality` 切换到
  `QualityCard` 网格，KPI / verdict / 滑点 mini chart 全部渲染；
- **Quality 桶切换条**：点击 1M USD pill，所有卡片同步切换 `@
  bucket` 标签；
- **Quality 「查看明细」**：收敛 watchlist 到该 symbol + 回到 V1
  三 BarChart + 盘口质量明细表视图；
- add 按钮在 `MAX_WATCHLIST=10` 时禁用。

辅助测试覆盖原有 V1 deep-link：

- 切换分类 pill 仍设置 `?symbol=X`，单 chip 模式跟随 URL；
- legacy `?symbol=BTC-USDT%20(perp)` 链接继续走 ResolveSymbol。

### Live-backend 套件：`web/e2e/docker-smoke.spec.ts`

直打本地 Docker (`http://127.0.0.1:3001` → `:8080`)，不 mock 任何
API，覆盖：

- 默认访问 + Liquidity toolbar + BTC chip；
- funding KPI 数值落在 sanity bound `|rate| < 0.5%`（catches 100×
  scale 回归）；
- Liquidity 详情表 funding 列 + 至少一个有符号百分比；
- Quality span-24 funding 跨平台 chart；
- 多 symbol 卡片网格 + 每张卡的「查看明细」按钮；
- 卡片迷你深度曲线 + 合计/BID/ASK 切换 + 跨卡独立状态；
- **Quality Tab 卡片网格**：live 数据下 KPI 行包含 spread / 滑
  点；明细表在卡片模式下被隐藏；
- 「查看明细」回收到单 symbol + URL/localStorage 同步；
- Top30 / Share Tabs 在 live 后端下可达。

## 验证命令

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/web
npm run lint
npx tsc --noEmit
npm run build

# fixture-mock suite
npx playwright test e2e/watchlist.spec.ts e2e/dashboard.spec.ts --reporter=list

# live Docker stack regression
PLAYWRIGHT_BASE_URL=http://127.0.0.1:3001 \
    npx playwright test e2e/docker-smoke.spec.ts --reporter=list
```

## v3 更新（2026-05-27）：dropdown 滚动修复 + 收藏星标统一入口

测试人员反馈在交易对选择下拉框（`SymbolSearchSelect`）里看到几十
个标的但无法向下滑动。结合 `commit af52977 fix(edgex-ops-intelligence):
make symbol dropdown reliably scrollable` 与 `commit 6d4db18
feat(edgex-ops-intelligence): add favorite-star symbol picker shared across
controls and watchlist toolbar`，本次同时落地了两件事：

### 滚动修复（不改产品语义）

- `.symbol-select-dropdown` 的 `max-height` 从写死的 `320px` 改为
  `min(60vh, 480px)`，大屏上长 catalog 多出可视空间。
- `.symbol-select-list` 加 `flex: 1 1 auto; min-height: 0`，让 flex
  column 子项始终能塌缩到 max-height；`overscroll-behavior: contain`
  阻止滚动事件逃出 dropdown 后挪动整页；`scrollbar-gutter: stable`
  避免悬停时滚动条出现造成抖动。
- 显式定制暗色主题滚动条（8px 厚 `#4b5563`），覆盖 macOS 默认隐
  形的 overlay scrollbar，让"可滚动"这一事实从视觉上一眼可见。
- `SymbolSearchSelect` 的键盘 ↓/↑ 现在会 `scrollIntoView({ block:
  'nearest' })` 把高亮项带入可视区，避免高亮跑出而被误判为"卡住"。
- Trigger 距视口底部不足时 dropdown 自动 flip-up（计算
  `getBoundingClientRect`，加 `.symbol-select-dropdown.up` 让定位从
  `top: calc(100% + 4px)` 翻成 `bottom: calc(100% + 4px)`）。
- 拆掉 `DashboardControls` 里的 `<label>` wrapper，避免原生 label
  把 dropdown 内的点击事件重路由给 `<input>`。

### 收藏星标（数据层 100% 复用 watchlist）

**核心决策**：收藏 = watchlist。不增加新的 storage key、不增加新
的 URL 参数、不增加新的 fan-out 通道，所有现成的去重 / 上限 /
URL 同步 / localStorage / SymbolBlock 渲染流程都直接复用。

新增 `web/components/symbol-picker-dropdown.tsx` 共享子组件，
作为 dropdown body：搜索输入 + 收藏置顶分组（"已收藏 (N) / 全部
(M)"）+ 每行 ★ 按钮。两处 trigger 都嵌入它：

| 入口 | Trigger | 行为 |
|---|---|---|
| `SymbolSearchSelect`（DashboardControls 里的"BTC-USD ▾"） | pill 按钮 | 行体点击 = 跳 `?symbol=X`（保留 V1 deep-link）；★ = toggle 收藏（不关 dropdown） |
| `WatchlistToolbar`（多 symbol toolbar） | 按钮文案从"+ 添加标的"改为"管理自选 ▾" | 行体点击 = toggle 收藏（无 href）；★ = toggle 收藏 |

跨入口规则一致：
- 已达 `MAX_WATCHLIST=10` 时，未收藏行的 ★ 显示为 `disabled` 并
  带 tooltip"自选清单最多 10 个标的"；已收藏行的 ★ 仍可点（用
  来腾位置）。
- ★ 点击 `e.preventDefault(); e.stopPropagation()` 不关闭 dropdown，
  支持快速连续收藏多个。
- 数据流：picker → `onToggleFavorite(canonical)` → `DashboardControls`
  里的统一 `toggleFavorite` → `addSymbol` / `removeSymbol`（来自
  `lib/watchlist.ts`）→ `onWatchlistChange(next)` → `DashboardClient`
  里现成的 effect 总线把 `next` 同步到 URL `?watchlist=` 与
  localStorage，并触发 `liquidityByCanonical` fan-out 重算。
- 后端无任何改动；新加入的标的立即出现在下方 SymbolBlock 列表中。

### Toolbar 行为变更

`WatchlistToolbar` 上的"+ 添加标的"按钮已被替换为"管理自选 ▾"，
它在任何容量下都启用 —— 因为操作员需要在到达 cap 时打开 dropdown
来腾出位置，而不是被锁住。**`watchlist.spec.ts` 的对应用例已更新**
为断言 trigger 永远 enabled，且打开 dropdown 后已收藏行的 ★ 仍可点。

### testid 兼容性

为保留对历史 e2e 用例的兼容，picker 在 SymbolSearchSelect 上下文
里继续暴露 `symbol-select-dropdown` / `symbol-select-input` /
`symbol-select-option-{canonical}`，在 toolbar 上下文里继续暴露
`watchlist-add-dropdown` / `watchlist-add-option-{canonical}`。
新增 testid：`symbol-select-star-{canonical}` /
`watchlist-add-star-{canonical}`。

### 新增测试覆盖：`web/e2e/dropdown-scroll-and-favorites.spec.ts`

inline 一个 30 个 symbol 的 fixture，使 dropdown 列表必然溢出，覆盖：

1. dropdown 打开后 `scrollHeight > clientHeight` 且 `scrollBy(0, 200)`
   后 `scrollTop > 0`，证明列表真正可滚（防 macOS overlay scrollbar
   隐形导致的"看似不可滚"误判 + flex column 子项缺 `min-height: 0`
   的回归）。
2. 键盘 ↓ 25 次后 `scrollTop > 0`，证明高亮跟随滚动（防键盘 nav
   不带视图同步滚动的回归）。
3. 点 SYM02 行的 ★ 后：dropdown 仍可见、watchlist-chip-SYM02 渲染、
   URL `?watchlist=` 含 SYM02、localStorage 同步、`aria-pressed`
   翻转；再点 ★ 后 chip 消失。
4. URL 预置 10 个收藏后，第 11 个未收藏的 ★ `disabled`，第 1 个已
   收藏的 ★ 仍 `enabled`。
5. 点 toolbar"管理自选 ▾"打开同款 dropdown，星标 SYM03 → toolbar
   chip 立即出现。

### 已知遗留（未在本次 commit 范围内）

`v2 SymbolBlock 改造没有同步更新现有 e2e 选择器` 一节描述的
`watchlist-card-*` / `watchlist-card-expand-*` testid 失败仍然存
在，需要后续按 SymbolBlock 的 `symbol-block-{canonical}` 选择器
重写那些用例。本次只修了由 toolbar 按钮改名引起的 `add button
disables at MAX_WATCHLIST cap (10)` 一条。

## v4 更新（2026-05-27）：盘口质量 Tab 统一为 QualityBlock + 资金费率搬出

运营反馈在多 symbol 模式下，"盘口质量" Tab 还在沿用旧版 `QualityCard`
缩略卡片网格 + 全局 SlippagePills 桶切换的形态，与已经升级到
SymbolBlock 垂直堆叠的"流动性监控"不对齐；同时希望把"资金费率"
彻底从盘口质量里搬出，后续单独做一个 Tab / 顶层 panel 承载。

### 主要改动

- 新增 `web/components/quality-block.tsx`：盘口质量 Tab 的 SymbolBlock
  对位组件。单个 `section.panel.span-24` 大框，内嵌 24-column grid：
  - **3 张 row-h-sm KPI 卡**（span-8 × 3）：edgeX 当前 spread（bp · USD）/
    edgeX 滑点 @ 桶（bp · USD，右上角 `pill-group-mini` 桶切换）/
    edgeX Imbalance
  - **3 张 row-h-md BarChart**（span-8 × 3）：Spread / 模拟滑点（跟随
    该 block 的桶状态，不读全局 query.bucket）/ Imbalance
  - **span-24 盘口质量明细子表**：平台 × Spread / Mid / Imbalance / 4
    个滑点桶 / 盘口结论；**已移除"资金费率 (8h)"列**
  - 顶部 panel-head 右侧附 verdict 徽章；空 snapshot 走
    `StatusEmptyState status="stale"` 单 block 降级
  - **每个 block 自带 bucket pill 独立状态**（`useState`），类比
    SymbolBlock 的 tier；让 BTC 看 1M、ETH 同时看 100K 互不串号
  - `data-testid="quality-block-{canonical}"`，桶按钮 testid
    `quality-block-bucket-{canonical}-{bucket}`
- `dashboard-shell.tsx#QualityTab` 重写为与 LiquidityTab v2 同构：不论
  watchlist 长度（含 0 → fallback / 1 / N），都走 `QualityBlock × N`
  垂直堆叠；删除原来的 dual-mode（多 symbol QualityCard 网格 vs 单
  symbol V1 三 BarChart + 明细表 + QualityFundingRow）以及 Tab 顶部
  全局 `SlippagePills` 桶切换条
- 「资金费率」彻底搬出 Quality Tab：
  - 不再渲染 `<QualityFundingRow>` span-24 跨平台对比面板
  - QualityBlock 内部明细子表不含"资金费率 (8h)"列
  - `dashboard-shell.tsx` 删除 `FundingKpiPanel` / `FundingCell` 私有
    helper（之前都是 Quality V1 视图独占的），并清理对应的
    funding-format / PlatformFundingRate 导入
  - 旧的 `quality-funding-row.tsx` / `quality-card.tsx` / `watchlist-card.tsx`
    **文件保留**，便于未来"资金费率"独立 Tab 复用其 BarChart 结构
    与回滚

### UI 路由策略（v4）

| 状态 | LiquidityTab | QualityTab（v4 新） |
|---|---|---|
| `watchlist.length === 1` | `WatchlistToolbar` + 单个 `SymbolBlock` span-24 | `WatchlistToolbar` + 单个 `QualityBlock` span-24 |
| `watchlist.length > 1`  | `WatchlistToolbar` + `SymbolBlock × N` 垂直堆叠 | `WatchlistToolbar` + `QualityBlock × N` 垂直堆叠 |
| `watchlist.length === 0` | effect 注入 BTC fallback，回到第一种 | effect 注入 BTC fallback，回到第一种 |

> v4 之后，「盘口质量」Tab 与 "流动性监控" Tab 完全同构：都是
> WatchlistToolbar + N 个 span-24 大框垂直堆叠，每个 block 内部独立
> 切换自己的桶 / 档位 pill。

### 数据加载（保持现状）

QualityBlock 不增加新的 fan-out 请求，复用 LiquidityTab 已经准备好的
`data.liquidityByCanonical` + `data.qualityByCanonical` 两份字典，
在父层通过 `mergeQualityIntoLiquidity(liq, qual)` 合并成单一
LiquiditySnapshot-shaped 对象传入 block。slippage / verdict 来自
`qualityByCanonical`，spread / mid / imbalance 来自 `liquidityByCanonical`，
两 Tab 数据严格一致。

### 样式调整

`web/app/globals.css`：把 `.symbol-block` 的四条 panel 帧规则扩展为
`.symbol-block, .quality-block` 选择器组，让两个 block 共享同一外框
配色与内嵌 grid 间距，不引入新规则集。`.quality-card*` /
`.quality-bucket-bar` / `.watchlist-card*` 等旧样式**保留**，配合
quality-card.tsx / watchlist-card.tsx 一起作为回滚备份。

### 资金费率的后续去向（follow-up）

> **状态更新（2026-05-27）**：已落地为独立「资金费率」Tab，位于
> 流动性监控 与 盘口质量 之间，详见
> [funding-rate.md](./funding-rate.md) v2 章节。`quality-funding-row.tsx`
> 已不再使用并被删除（commit b90d398）；新 Tab 由
> `funding-block.tsx` 实现，提供 3 个 KPI 卡片 + 6 列明细表（含
> sign-bucketed 双 rank 列与 sign-color cell）。下方原始 v4 follow-up
> 留作演进上下文：

资金费率从 Quality Tab 搬出后，落地为独立 Tab / 顶层 panel 的工作
**不在本 commit 范围内**：

- 暂时从 dashboard 隐藏（与 SymbolBlock 内已注释隐藏的
  `FundingKpiPanel` 状态对齐）
- 后续单独设计新 Tab（如 `tab=funding`）或顶层 panel，使用现有
  `quality-funding-row.tsx`（保留未删除）+ `QualityKPIs.competitor_
  funding_rate_median_8h*` 字段；预计与"市占率" / "Top30" 平级

### 测试影响 & follow-up

v4 的改造没有同步更新现有 e2e 选择器，新增已知偏差：

- `web/e2e/watchlist.spec.ts` 与 `web/e2e/docker-smoke.spec.ts` 中所有
  对 Quality Tab 的断言（`quality-card-*` testid / 「查看明细 →」
  按钮 / 全局 `SlippagePills` 桶切换 / 盘口质量明细表的"资金费率
  (8h)"列 / `QualityFundingRow` span-24 跨平台对比面板）现已无对应
  DOM，需要按以下映射迁移到 v4 选择器：
  - `quality-card-{sym}` → `quality-block-{canonical}`
  - 「全局桶切换」→ 每个 block 内部的 `quality-block-bucket-{canonical}-{bucket}`
  - 「查看明细 →」→ 通过删除 toolbar 上多余 chip 收敛到单 symbol（已无
    专用入口）
  - 「资金费率列 / QualityFundingRow」断言 → 全部删除（v4 已无对应
    DOM；现已由独立的 `web/e2e/funding-tab.spec.ts` 覆盖资金费率
    Tab，参见 funding-rate.md v2 测试覆盖章节）

Follow-up TODO（不在本 commit 范围内）：把 v2 SymbolBlock 与 v4
QualityBlock 两轮改造遗留的 e2e 用例统一迁移（资金费率部分已完成，
其余 quality / liquidity 选择器迁移仍 pending）。

### 验证命令

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/web
npm run lint
npx tsc --noEmit
npm run build
```

E2E 套件因上述 testid 漂移会出现已知失败，迁移完成前**不在 v4
commit 的验证范围内**。
