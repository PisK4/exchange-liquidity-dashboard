# Adapter 4 档盘口深度采集（多视图选择 + loose 展示契约）

## 背景

平台流动性 Dashboard V1 最初的盘口采集以“单 raw order book”为主：每个交易所通常只拉一个 REST book，然后按 `0.05% / 0.10% / 1% / 2%` 四档直接计算深度。

这在浅档可用，但在 BTC/ETH 等高价标的上会出现两个问题：

- 公开 REST book 档数不足，1%/2% 深档只能得到 lower-bound；
- 部分交易所需要按不同聚合粒度维护多路 view，例如 Bitget `scale0..3`、Gate `interval=10/100`、Hyperliquid `s5_m2/s5_m5/s4/s3`。

本轮 feature 的目标是把 Adapter 从“单 raw book”升级为：

```text
per-tier 多视图选择
+ strict / loose / physical_limit 状态化
+ loose 灰色展示并参与排名
+ physical_limit 横线展示
+ 后续 WS provider feature flag 化
```

参考设计文档：

- `../../../../architecture/方案设计/EdgeX运营/需求梳理/18-平台流动性Dashboard-Adapter4档采集与多Token扩展方案.md`

## 当前实现状态

已完成并拆成 4 个本地提交：

```text
9c6070e feat(edgex-dashboard): add depth display contract fields
3ff992e feat(edgex-dashboard): render loose depth availability
ecea8b2 feat(edgex-dashboard): select per-tier REST depth views
2542983 feat(edgex-dashboard): add websocket provider flags
```

当前状态：

- 后端 / 前端 / config 均已通过本地验证；
- Docker backend + web 已重建并启动；
- `http://127.0.0.1:8080/api/health` 已返回 `ok=true`；
- `http://127.0.0.1:3001/` 已返回 `200 OK`；
- `GET /api/snapshot/liquidity?symbol=BTC-USDT%20%28perp%29` 已返回含新字段的数据。

## API contract 变更

### `TierDepthMetrics`

新增字段：

```go
StrictComplete       bool   `json:"strict_complete"`
DisplayAvailable     bool   `json:"display_available"`
PolicyAcceptance     string `json:"policy_acceptance,omitempty"`
PhysicalLimit        bool   `json:"physical_limit,omitempty"`
UnofficialUIEndpoint bool   `json:"unofficial_ui_endpoint,omitempty"`
```

语义：

| 字段 | 含义 |
|---|---|
| `strict_complete` | 当前 tier 是否满足 strict 覆盖要求 |
| `display_available` | 前端是否展示数值；也是当前排名 / median 的参与门槛 |
| `policy_acceptance` | `raw_strict` / `aggregated_strict` / `loose_lower_bound` / `loose_grouped_approx` |
| `physical_limit` | 该平台公开接口对该 symbol/tier 物理不可达 |
| `unofficial_ui_endpoint` | 数据来自非文档化 UI WS endpoint |

### `BookView`

新增字段：

```go
StepUSD       float64 `json:"step_usd,omitempty"`
ResolutionPct float64 `json:"resolution_pct,omitempty"`
```

用途：

- `StepUSD` 表示聚合桶宽；
- raw book 默认用相邻价格差中位数推导；
- selector 用它判断 aggregated view 的 resolution 是否足够细。

## 状态与展示规则

本 feature **没有新增 `depth_status` taxonomy**，仍复用既有状态：

```text
complete / partial / aggregated_orderbook / ws_limited_depth / stale / unsupported / error
```

展示与排名规则：

| 场景 | strict_complete | display_available | 前端 | 参与排名 |
|---|---:|---:|---|---:|
| raw strict | true | true | 正常数值 | 是 |
| aggregated strict | true | true | 正常数值 | 是 |
| loose lower-bound | false | true | 灰色数值 + tooltip | 是 |
| loose grouped approx | false | true | 灰色数值 + tooltip | 是 |
| physical limit | false | false | `—` + tooltip | 否 |
| stale / error / unsupported | false | false | `—` | 否 |

兼容逻辑：

- 旧数据没有新 bool 字段时，后端会按 `depth_status` derive 默认值；
- `complete / aggregated_orderbook / ws_limited_depth` 默认可展示且 strict；
- `partial` 默认 loose 可展示；
- `stale / error / unsupported` 默认不可展示。

## MySQL 持久化

新增 migration：

```text
backend/migrations/000005_depth_contract_fields.up.sql
backend/migrations/000005_depth_contract_fields.down.sql
```

新增列：

```sql
strict_complete
display_available
policy_acceptance
physical_limit
unofficial_ui_endpoint
```

后端启动时 `ApplyMigrations` 也会对已有 `t_orderbook_snapshot` 表做幂等补列，避免只执行 init schema 的环境漏掉新字段。

## REST 多视图实现

### Bitget

修复原有 bug：之前 `fetchBitgetMergeDepthView` 写死 `precision=scale0`，但 `scale0` 只能覆盖很浅的范围，不应被用于 1%/2% 深档。

现在拉取：

```text
bitget_merge_scale0
bitget_merge_scale1
bitget_merge_scale2
bitget_merge_scale3
```

parser 改为 `parseAnyLevels`，兼容 JSON number / string。

### Gate

现在拉取：

```text
raw
gate_agg_10
gate_agg_100
```

策略：

- `interval=10` 优先；
- `interval=100` 只作为更深档 fallback；
- 桶宽过粗时会标为 loose grouped approx，而不是 strict。

### Hyperliquid

现在拉取：

```text
raw
hyperliquid_s5_m2
hyperliquid_s5_m5
hyperliquid_s4
hyperliquid_s3
```

参数：

```json
{"nSigFigs":5,"mantissa":2}
{"nSigFigs":5,"mantissa":5}
{"nSigFigs":4}
{"nSigFigs":3}
```

后续仍需要在 WS / catalog 阶段补 mid 数量级切换下的 view plan 与 debounce。

## per-tier selector

核心逻辑在 `backend/internal/adapter/adapter.go`：

```text
1. 收集 SourceBooks 中所有 view
2. 按 StepUSD 从细到粗排序
3. 对每个 tier 选择第一个满足：
   - bid/ask 双边 farthest >= tier
   - aggregated view 的 StepUSD <= tier * mid / 4
4. 若无 strict view，则选双边覆盖最远的 view 作为 loose
```

注意：

- resolution 约束只对 aggregated view 生效；
- raw view 即使相邻 tick 中位数较粗，也不因该启发式直接降级；
- loose 值仍参与排名，因为它是 lower-bound / grouped approx，不会虚高真实深度。

## 前端行为

变更位置：

- `web/lib/api/client.ts`
- `web/components/dashboard-shell.tsx`
- `web/e2e/dashboard.spec.ts`

行为：

- `display_available=true && strict_complete=true`：正常数值；
- `display_available=true && strict_complete=false`：灰色数值 + tooltip；
- `display_available=false`：表格显示 `—`，曲线断点；
- quality tab 的 spread/slippage/imbalance 也以 `display_available` 作为展示门槛。

## WS provider feature flags

`config/edgex-liquidity-dashboard.yaml` 的 `Runtime.ws_providers` 新增（dev/prod 由 Nacos dataId `edgex-liquidity-dashboard.yaml` 渲染到 `/config/edgex-liquidity-dashboard.yaml`）：

```yaml
Runtime:
  ws_providers:
    bitget:
      enabled: true
    mexc:
      enabled: true
    okx:
      enabled: true
    bybit:
      enabled: false
    bingx:
      enabled: false
```

当前只是完成配置层与默认值：

- Bitget / MEXC / OKX 默认启用，为下一步 WS provider 接入预留；
- Bybit / BingX 默认关闭，等待长测后再放开；
- 实际 WS provider 接线仍是后续模块。

## 验证记录

已执行通过：

```bash
cd backend && make test
cd backend && make lint
cd web && npm run lint
cd web && npm run typecheck
cd web && npm run build
```

Docker 重建启动：

```bash
docker compose -f deploy/docker-compose.yaml up -d --build backend web
```

检查结果：

```bash
curl -fsS http://127.0.0.1:8080/api/health
curl -fsSI http://127.0.0.1:3001/
curl -fsS 'http://127.0.0.1:8080/api/snapshot/liquidity?symbol=BTC-USDT%20%28perp%29'
```

结果：

- backend health 返回 `ok=true`；
- web 返回 `200 OK`；
- liquidity API 返回包含 `strict_complete / display_available / policy_acceptance` 的 depth tier 数据。

## 已知限制与后续

1. `physical_limit` 真实标注仍需由后续 `view_plan / catalog-probe` 接入，不在本轮硬编码平台/币种。
2. OKX / MEXC / Bybit / BingX 的 UI WS provider 尚未接入，只完成 feature flag 配置。
3. Hyperliquid 已有多 REST view，但 view plan 的数量级边界重选与 30s debounce 仍在后续模块。
4. `make catalog` / `make catalog-probe` 拆分与 probe diff 防抖尚未实现。
5. 当前本地 Docker 环境依赖 `/config/edgex-liquidity-dashboard.yaml` 里的 `Runtime.exchange_proxy=http://host.docker.internal:7897`，若宿主机没有对应代理，部分上游交易所仍可能返回 error；JP / SG 等直连可达环境应在 Nacos 中将相关 proxy 字段留空。
