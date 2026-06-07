# HTML 设计稿对齐 Feature 记录

## 背景

本 feature 目标是让 `edgex-ops-intelligence` 前端在关键页面上对齐原始 HTML Demo 设计稿的视觉语义和布局表达，尤其是“市占率”和“盘口质量”两个模块。

参考设计稿：

```text
/Users/pis/workspace-intelligence/edgex-intelligence/architecture/方案设计/EdgeX运营/原需求/edgeX · 流动性 & 深度监控面板 (Demo)(3).html
```

对应实现提交：

```text
e90c0ff feat(edgex-ops-intelligence): align liquidity dashboard UI
```

## 对齐范围

### 1. EdgeX 视觉主色与平台标识

已将 EdgeX 强调色从橙色切换为绿色主色调：

```text
#6ccf8e
```

影响范围：

- 顶部 logo；
- active tab / pill；
- `edgeX` 平台行；
- edgeX 图表条形；
- share 可视化条；
- 状态 badge 中的健康绿色语义。

平台名称统一通过 `PlatformCell` / `platformDisplayName` 输出：

```text
edgeX -> edgeX ★
```

### 2. 市占率明细表

已对齐 HTML 设计稿中的“edgeX 平台总交易量市占率明细表”：

- 表头最后一列从 `状态` 改为 `占比可视化`；
- 增加横向 share bar，用于表达各平台在分母中的占比；
- `edgeX` 行展示为 `edgeX ★` 并使用绿色主色；
- 口径切换文案对齐为：
  - `日 (24h)`
  - `周 (7d)`
  - `月 (30d)`
- KPI 区从三张独立卡片调整为参考稿中的横向信息 strip；
- 补充成交量统计口径说明：
  - 数据为全平台所有 perp 合计成交量；
  - MEXC 使用 `×0.4`；
  - Gate 使用 `×0.5`；
  - EdgeX 自身计入分母。

涉及文件：

```text
web/components/dashboard-shell.tsx
web/app/globals.css
web/components/platform-cell.tsx
```

### 3. 盘口质量

已对齐 HTML 设计稿中的“盘口质量”模块：

- `Spread (bp)` 补充说明：`买一/卖一相对价差`；
- `模拟下单滑点 (bp)` 补充说明：`相对中间价`；
- `Bid/Ask Imbalance (%)` 补充公式：`(BID深度-ASK深度)/合计`；
- `模拟下单滑点` 增加“档位可配置”的说明 note；
- `Imbalance` 增加正负含义与健康阈值说明；
- Spread / Slippage 条形图支持按数值升序排序；
- Spread / Slippage 末端标签展示 `bp + USD`；
- Imbalance 保留 signed value，不再取绝对值；
- 盘口质量明细表的滑点列补齐 `(bp)` 单位；
- 盘口结论改为 badge：
  - `健康`
  - `关注`
  - `较差`
  - `unsupported`

涉及文件：

```text
web/components/dashboard-shell.tsx
web/components/chart-primitives.tsx
web/app/globals.css
web/components/platform-cell.tsx
```

### 4. 通用图表基础能力

`BarChart` 组件已扩展：

- 支持 `sort="asc"`；
- 支持 signed bar；
- 支持调用方传入自定义颜色；
- formatter 可访问当前 row；
- 自动将 `edgeX` label 渲染为 `edgeX ★`。

涉及文件：

```text
web/components/chart-primitives.tsx
web/components/chart-colors.ts
```

## 未纳入本次 feature 的范围

- 未修改后端接口、采集逻辑或指标计算口径；
- 未接入新的市占率时序图组件；
- `平台总市占率时序 (近 30d)` 仍沿用当前历史数据不足 / unsupported 的展示路径；
- 7d / 30d 市占率聚合能力仍以当前后端实现为准。

## 回归测试覆盖

新增 Playwright 测试覆盖以下视觉语义：

- 市占率表包含参考设计稿副标题；
- 市占率切换项展示为 `日 (24h)` / `周 (7d)` / `月 (30d)`；
- 市占率最后一列为 `占比可视化`，不再是 `状态`；
- 市占率表中出现 `edgeX ★`；
- `edgeX ★` 使用绿色主色；
- 盘口质量图包含参考说明文案；
- Spread / Slippage 显示 USD 辅助标签；
- Imbalance 保留正负号；
- 明细表滑点列包含 `(bp)`；
- 盘口结论使用 badge。

涉及文件：

```text
web/e2e/dashboard.spec.ts
```

## 验证命令

实现提交前已通过：

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/web
npm run typecheck
npm run lint
SERVER_API_BASE=http://127.0.0.1:18080 npm run build
PLAYWRIGHT_BASE_URL=http://127.0.0.1:3002 npm run test:e2e
```

E2E 结果：

```text
9 passed
```

## 后续建议

如果继续推进设计稿完整对齐，优先级建议如下：

1. 将市占率 7d / 30d 聚合能力补齐，避免切换口径时只能展示 unsupported。
2. 接入真实的市占率近 30d 时序点，再把当前时序 placeholder 替换为 Chart.js 折线图。
3. 进一步统一全局布局密度，例如 panel 高度、controls 分段按钮形态、表格 padding 与参考 HTML 的 Grafana 风格。
