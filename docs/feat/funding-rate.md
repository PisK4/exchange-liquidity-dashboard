# 跨平台资金费率（Funding Rate, 8h 当量）

## 文档版本

- **v1（已废弃）**：funding 内嵌在 Liquidity KPI + Quality 底部 span-24
  BarChart 双位置展示。
- **v2**：funding 完全搬出 Liquidity / Quality，独立为顶部
  「资金费率」Tab（位于 流动性监控 与 盘口质量 之间），与
  Watching List 联动按 symbol 堆叠 FundingBlock。BarChart 在 v2
  round-3 撤掉。
- **v2.1（当前）**：在 FundingBlock 内部追加两类视觉增强 ——
  KPI 卡片旁补 APR 当量副线（`rate_8h × 3 × 365`），三 KPI 卡 vs
  明细表之间新增一块 **零中心 diverging bar 图**（正向右红 / 负向
  左青、按本组 max(|rate|) 归一）。BarChart 仍未恢复，但 diverging
  bar 用独立 CSS namespace（`.funding-diverge-*`），与历史 BarChart
  e2e 守护规则不冲突。本 doc 已按 v2.1 现状重写；v1 背景作为历史
  保留。

## 背景

v1 阶段流动性 Dashboard 只覆盖了深度、Spread、滑点与市占率，缺少
对永续合约**多方/空方持仓成本**的横向观察。运营在 launch 新标的
或追踪行情突变时需要直观比对 edgeX 与竞品的资金费率，但各家原生
结算周期不同（4h / 1h / 8h），不能直接读取百分比。

V1 把 funding KPI 塞进流动性顶部 + Quality Tab 底部 span-24
BarChart 这两个位置，运营反馈两边语义重复、且与 Liquidity /
Quality 的核心叙事抢空间，决定独立为顶部 Tab。

本 feature 在数据链路与展示层补齐这一块，遵循 v2.1 spec 中的两个
强约束：

- **不补假数据**：任何缺失、不支持或样本不足的情形只输出明确的
  `status`，绝不展示假 0。
- **8h 当量**：跨平台一律折算到 8 小时维度，同时保留 `rate_native`
  + `period_hours` 供 UI 显示原始观测。

## 数据来源

来源固定为 CoinGecko `GET /derivatives?include_tickers=unexpired`：

- `ticker.funding_rate` 已经是“原生结算周期的百分比值”（CoinGecko
  官方示例：`0.0095` 表示 `0.0095%`，**不是** decimal 0.95%）；
- 后端按此原始单位存储 `rate_native` / `rate_8h`，sanity 阈值
  `0.5` 对应 0.5% per 8h；
- 前端 `formatFundingRate8h` 直接 `.toFixed(4) + '%'`，**不再
  乘 100**——首版实现错把这单位当 decimal 又乘了一次 100，导致
  +0.01% 显示成 +1.0000%，已修正；
- `ticker.volume_24h_usd` 用于同一 `(platform, display_symbol)` 下
  多 ticker 的 tiebreaker，优先采用成交量最高者；
- 请求 URL 与平台名写入 `source` / `source_endpoint`，便于排障。

CoinGecko 文档对 `funding_rate` 字段的描述偶尔为 null（标的暂未结
算或交易所未上报），后端用 `OptionalFlexibleNumber` 区分“真实 0%”
与“缺失”。

## 8h 归一与 sanity check

核心实现：

- `backend/internal/collector/funding.go`
- `backend/internal/collector/coingecko_collector.go`
- `backend/internal/marketdata/coingecko/types.go`

V1 周期表：

| 平台 | 原生周期 | 8h 归一 |
|---|---:|---|
| Binance / OKX / Bybit / Bitget / BingX / MEXC / Gate | 8h | `× 1` |
| Hyperliquid / Lighter | 1h | `× 8` |
| edgeX | 4h | `× 2` |

异常保护：

- **未知平台**周期不做默认假设，直接降级为 `unsupported`。
- 8h 当量绝对值超过 `0.5%` 或非有限值（NaN/±Inf）视为上游单位漂
  移，降级为 `stale` 并不参与 median 计算。
- `rate_8h` / `rate_native` 均为 nullable（`*float64`），通过
  `omitempty` 区分“没观测到”与“真实 0%”。
- 重复 `(platform, display_symbol)` 走 `canonicalDailyKey` 折叠，
  以最新 `snapshot_ts` 为准。

## 后端 contract

`PlatformSnapshot` 扩展：

```go
type PlatformSnapshot struct {
    // ... 既有字段
    Funding *PlatformFundingRate `json:"funding,omitempty"`
}
```

`PlatformFundingRate` 字段：

```text
platform
display_symbol
rate_8h          // 折算后 8h 当量
rate_native      // 原生周期上的 raw 百分比
period_hours     // 原生周期（h），未知时为 nil
status           // complete | stale | unsupported
source           // coingecko
source_endpoint  // /derivatives?include_tickers=unexpired
snapshot_ts
vs_median_8h     // 与竞品中位数差值，缺失时为 nil
rank_positive    // 正费率组内 1-based 排名（rate_8h > 0），缺失为 nil
rank_negative    // 负费率组内 1-based 排名（rate_8h < 0），缺失为 nil
```

> v2 重要变化：单字段 `rank int` 已替换为 `rank_positive` + `rank_negative`
> 双指针字段。原因见下文「Sign-bucketed rank」。

### Liquidity / Funding Tab 数据通路

`Store.Liquidity` 中调用 `attachFundingLocked` 对每个 `rows[i]` 注入
`funding`，再依次执行：

- `competitorFundingMedian8h`：**严格排除 edgeX**，要求 ≥ 3 个
  `status=complete` 样本，否则返回 `stale`。
- `enrichFundingVsMedianRows`：写 `vs_median_8h = row.rate_8h - median`。
- `enrichFundingRankBySignRows`（v2 新增）：按 `rate_8h` 符号拆成两组，
  各自独立排名 + 1224 平级规则。

资金费率 Tab 复用 `/api/snapshot/liquidity` 数据源 — 无新增 endpoint。
Quality 查询路径仍复用同一份 `funding` map（保持与 Liquidity 数值
一致），但前端 Quality Tab 已不再渲染 funding 列。

### Sign-bucketed rank（v2 算法）

`enrichFundingRankBySignRows`（`backend/internal/collector/store.go`）：

1. 跳过 `status != complete` 或 `rate_8h` 缺失或恰为 0 的行
2. 按 `rate_8h` 符号拆为两个独立 cohort：
   - **正费率组**：`rate_8h > 0`，按 rate 降序排，`rank_positive=1` 为
     最大正值（多头付出最多 / 空头收益最大）
   - **负费率组**：`rate_8h < 0`，按 rate 升序排，`rank_negative=1` 为
     最负值（空头付出最多 / 多头收益最大）
3. 每个 cohort 内部应用 **1224 标准平级规则**：并列费率共享同 rank，
   下一档跳过被占用的位置。例：`[0.01, 0.01, 0.01, 0.005]` → rank
   `[1, 1, 1, 4]`。

> 为什么要拆 cohort：单一 ascending rank 在 ladder 跨过零时语义会反转
> ——"rank 1" 既可能是"最负"也可能是"最小正"。运营关心的是"最极端
> 正"和"最极端负"两个独立维度。
>
> 为什么要 1224 平级：之前 4 家费率全是 0.01% 却拿到 rank 7/8/9/10，
> 这是从"无序数据"里捏造了"顺序"，违反 v2.1 spec 的"不补假数据"
> 约束。1224 让相同费率行显式共享同 rank。

KPI 顶层字段（出现在 `LiquiditySnapshot.kpis` / `QualityKPIs`）：

- `edgex_funding_rate_8h`
- `competitor_funding_rate_median_8h`
- `competitor_funding_rate_median_8h_status`
- `competitor_funding_rate_median_8h_samples`

## 前端展示

主要文件：

- `web/components/dashboard-shell.tsx`（资金费率 Tab tuple，位于
  流动性监控 与 盘口质量 之间）
- `web/components/funding-block.tsx`（per-symbol 跨平台 panel）
- `web/components/funding-diverge-bar.tsx`（v2.1 新增，FundingBlock
  内部的零中心 diverging bar 图）
- `web/app/funding/page.tsx`（`/funding` 重定向到 `/?tab=funding`）
- `web/lib/funding-format.ts`（格式化共享层，含 adaptive precision
  + `formatFundingAPR`）
- `web/app/globals.css`（`.sign-positive`/`.sign-negative`/`.r-edgex`
  + `.funding-diverge-*` + `.apr-hint` 等 funding 相关 class）

### Tab 结构

```
顶部 nav: 流动性监控 | 资金费率 | 盘口质量 | 市占率 | Top30
            ↓
        Watching List 工具栏（与其他 Tab 共享）
            ↓
        For each symbol in watchlist:
          FundingBlock × N（垂直堆叠 span-24）
            ├─ 3 个 KPI 卡片（顶部）
            │   ├─ edgeX 原生费率（含周期 tag）+ 8h 当量 副线 + APR 当量副线
            │   ├─ 竞品中位数 8h + APR 当量副线 + 样本数 + 说明副线
            │   └─ edgeX vs 中位数 Δ + APR 差额副线 + 8h 计算公式副线
            ├─ 8h 当量方向与强度 panel（v2.1 新增）
            │   零中心 diverging bar：正向右红 / 负向左青；
            │   条长按本组 max(|rate|) 归一；
            │   edgeX 行 accent-soft 底色 + 白色 inset 描边强调
            └─ 资金费率明细表（6 列）
                平台 | 原生费率 | 8h 当量 | vs 中位数 (8h) | 正费率排名 | 负费率排名
```

### Sign-color cells（v2 新增）

明细表中 3 个数值列按 cell 自身值的符号着色：

| 颜色 | CSS class | CSS 变量 | 语义 |
|---|---|---|---|
| 红 | `.sign-positive` | `var(--bad)` `#f2495c` | 正费率（多头付出 / 空头收益） |
| 青 | `.sign-negative` | `#4fc3a1`（新增，刻意避开 `--accent` 绿） | 负费率（空头付出 / 多头收益） |
| 中性 | 无 class | 继承 | 零值 / 缺失 |

辅助函数 `rateSignColorClass(value)` 集中决策，避免散落 if-else。

> 早期实现这两个 class 叫 `.funding-positive` / `.funding-negative`，
> 后来 `盘口质量明细` Imbalance 列也要复用同套配色（标方向：BID
> 偏重 / ASK 偏重），class 名被泛化为 `.sign-*` 留在 globals.css 内
> 单一调色板。Diverging bar 的数值标签也走这套。

### edgeX 行高亮

明细表中 `row.platform === 'edgeX'` 的 `<tr>` 会附加 `.r-edgex` class，
通过 `.tbl tr.r-edgex td { background: var(--accent-soft); }` 渲染
淡绿色行底色，与其他 Tab edgeX-vs-竞品 surfaces 的高亮规范一致。
平台名 cell 仍由既有 `.platform-self` 着绿色加粗。

### Diverging bar 图（v2.1 新增）

实现：`web/components/funding-diverge-bar.tsx` + `globals.css` 内
`.funding-diverge-*` 命名空间。

- **形态**：纯 HTML/CSS（不走 Chart.js），三列 grid：平台标签 |
  零中心轨道 | 数值。轨道里 `0%` 居中，正向右、负向左。
- **量纲处理**：本组 `max(|rate|)` 作分母，正负两侧各占一半 (50%
  最大宽)。这恢复了被旧 v2 round-3 单轴 BarChart 压扁的可读性 ——
  典型 ±0.005% 量级在线性 0-anchored 单轴里全被挤成方块，但拆到
  零两侧 + 各自归一后强度对比立刻清晰。
- **edgeX 强调**：行用 `--accent-soft` 淡绿底色，bar fill 加白色
  inset border + 满饱和度色，与 `.platform-self` / `.r-edgex` 的强
  调规范一致。
- **空值处理**：`status != complete` / `rate_8h` 缺失的行排到底部，
  渲染为 muted opacity 的 em-dash，**不画零长 bar**（保留
  funding.go「不补假数据」契约）。
- **排序**：`rate_8h` 降序，最正值在顶 → 最负值在底，可读为「最
  贵给多头 → 最贵给空头」的视觉光谱。
- **CSS namespace 决策**：刻意不复用 `.bar-row` / `.bar-track`，因
  为 `funding-tab.spec.ts` 中有反向断言「`.bar-row` count = 0」用
  来守护历史单轴 BarChart 不复活。Diverging 是不同视觉契约（sign-
  bucketed + 零锚），独立 namespace 避免与该守护打架。

### APR 当量副线（v2.1 新增）

实现：`web/lib/funding-format.ts` 的 `formatFundingAPR(rate8h)` +
`funding-block.tsx` 在三张 KPI 卡的 big-number 旁追加 `≈ +X.XX% APR`
hint。

```ts
// 简单非复利: rate_8h × 3 periods/day × 365 days = rate × 1095
formatFundingAPR(0.0050) // → '+5.48% APR'
```

- **为什么不用复利**：永续 funding cashflow 按 period 结算后直接进
  保证金，并不再投回头寸 notional；`(1+r)^1095 - 1` 会按速率本身
  非线性放大，破坏与 8h 单元格的视觉对齐。运营要的就是「持仓 1
  年大致成本」的回退式估算，简单乘法刚好。
- **视觉层级**：APR 用 `.apr-hint`（小字 + muted + cursor:help），
  per-period 数值仍是主值，APR 是「这个数对我意味着什么」的次级
  翻译。
- **覆盖三张卡**：edgeX KPI、竞品中位数 KPI、Δ KPI（Δ 也加 APR
  差额，便于「edgeX 比中位数年化便宜/贵多少」的直觉判断）。
- **缺失态**：`formatFundingAPR(null) === '—'`，与 per-period
  formatter 同契约。

### Adaptive precision formatter

`funding-format.ts` 抽取了统一的 `formatPercentAdaptive` 骨架，被
`formatFundingRate8h` / `formatFundingDelta` / `formatNativeRateWithPeriod`
共享：

- 默认 4dp 百分号、显式带正负号
- 非有限值（NaN / ±Inf）→ `—`
- **崩塌检测**：若 4dp 表示让非零值显示为 `+0.0000%` / `-0.0000%`，
  回退到 6dp。Hyperliquid 1h 周期的原生费率（0.000025% 量级）正好
  落在这一档，4dp 会丢失全部信息

`formatNativeRateWithPeriod(rate, hours)` 把原生周期 tag 折叠在数值
之后：例 edgeX 4h 周期显示为 `+0.0025% / 4h`，节省一列。

### 移除的旧组件

- `FundingKpiPanel`（v1 Liquidity 顶部 KPI）— 已移除，由 FundingBlock
  KPI 卡片接管
- `QualityFundingRow` span-24 BarChart（v1 Quality 底部）— 已删除
- `FundingCell` 表格列（v1 Liquidity / Quality 明细表）— 已删除
- 跨平台单轴 BarChart 在 FundingBlock 内部也于 v2 round-3 撤掉，明
  细表 + KPI 卡片已经能讲完故事（运营反馈 BarChart 与表格信息冗
  余）。**v2.1 没有恢复它**；新增的 diverging bar 是不同视觉契约
  （sign-bucketed + 零锚 + 双侧独立归一），目的是补"方向 + 强度"
  的可视化，而不是回到旧的"绝对值横向比较"
- v2.1 也短暂尝试过一个「跨 symbol 资金费率横截面热力图」（行=平
  台，列=watchlist symbols），实现完成后又被产品撤回。当前不在
  代码树内，留作后续讨论备选项。相关想法存档见
  `docs/plan/2026-05-27-funding-rate-chart-ideas.md`

## 测试覆盖

| 层 | 文件 / 测试 |
|---|---|
| Funding 单位归一 + sanity | `backend/internal/collector/funding_test.go` |
| CoinGecko nullable 解析 | `backend/internal/marketdata/coingecko/types_test.go` |
| Funding collector wiring | `backend/internal/collector/coingecko_funding_test.go` |
| Store median / vs-median / sign-bucketed rank | `backend/internal/collector/funding_store_test.go`（含 3 个 sign-bucket + 1224 ties 单测） |
| OpenAPI schema 对齐 | `backend/internal/api/openapi_contract_test.go`、`swagger_mirror_test.go` |
| 资金费率 Tab E2E | `web/e2e/funding-tab.spec.ts`（15 specs：Tab 位置 / KPI 卡片 / Δ 公式副线 / 6 列明细表 / sign-bucketed 排名梯 / `.r-edgex` 高亮 / `.funding-positive` class / Hyperliquid 6dp 精度 / Unsupported em-dash / 多 symbol fan-out） |

## 验证命令

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/backend
make test
make build       # 本机校验
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 make build  # 交叉编译 Docker 目标架构
```

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/web
npm run openapi:check
npm run typecheck
npm run lint
npm run build
npm run test:e2e -- e2e/funding-tab.spec.ts
```

## Docker 部署注意

`Dockerfile.backend.local` COPY 预编译二进制（不在容器内 Go 编译），
所以本地 `make build` 后必须确认产物是 `linux/arm64` ELF（而非
macOS 本机的 `darwin/arm64` Mach-O），否则容器跑的是旧二进制：

```bash
file backend/bin/ops-intelligence
# 期望：ELF 64-bit LSB executable, ARM aarch64, ...
```

部署命令（注意 compose project 名为 `deploy` 而不是 `edgex-ops-intelligence`）：

```bash
cd repos/edgex-ops-intelligence/deploy
docker compose --project-name deploy \
  -f docker-compose.yaml -f docker-compose.local.yaml \
  up -d --build backend
```

如果使用主 `Dockerfile.backend` 在容器内 Go 编译，`docker-compose.yaml` 已把 `BUILD_VERSION` 透传给 Dockerfile 的 `ARG BUILD_VERSION`。通过 deploy Makefile 或显式 env 构建后，`/api/health` 应显示 git describe 版本而不是 `dev`：

```bash
BUILD_VERSION="$(git -C .. describe --tags --always --dirty)" docker compose build backend
curl -s http://127.0.0.1:8080/api/health | jq .build_version
```
