# Liquidity Alert (#10 + #11) — 工程文档

> **关联 spec**：
> `../../../../architecture/方案设计/EdgeX运营/Listing/2026-05-29-Listing-Agent-Dashboard-Liquidity-Alerts-#10-#11.md`
>
> **兄弟文档**：`listing-agent-top30-hot-gap-push.md`（#1 卡）、`listing-agent-divergence-push.md`（#2-#5 卡）—— 三篇共享 outbox / delivery / Lark interactive card schema 约定，调整前互相对照。动态 Catalog / runtime listed universe 入口见 `listing-agent-dynamic-catalog-integration.md`。
>
> **关联表**：`t_orderbook_snapshot`（输入）、`t_listing_alert_state`（新增状态表）、`t_listing_delivery_outbox`（共享投递）

本文是 #10 liquidity_lag / #11 worst_depth 两张告警卡的工程化说明，覆盖代码组织、表契约、状态机实现、配置迁移和排障指引。业务设计见 spec。

---

## 1. 代码组织

```
backend/internal/listing/liquidity/
  types.go        # AlertKind / AlertCandidate / PlatformDepthRow / Config
  compute.go      # Compute(matrix, universe, exclusion, cfg) → []AlertCandidate
  state.go        # DecideAction(candidate, state, cfg, now) → ActionDecision
  render.go       # RenderLiquidityPostMessage(card) → Lark interactive payload
  *_test.go       # 单元测试
backend/internal/listing/
  engine.go               # ProduceLiquidityAlertPush 接入
  delivery.go             # resolveWebhookForChannel(event_type)
  liquidity_push.go       # ProduceLiquidityAlertPush 实现（编排 Compute + state + outbox）
  liquidity_push_test.go
  repository.go           # +LoadFreshDepthMatrix +LoadAlertState +UpsertAlertState
backend/scripts/liquidity-alert-preview/
  main.go         # CLI 预览（与 top30-preview 同构）
backend/migrations/
  000011_listing_alert_state.up.sql
  000011_listing_alert_state.down.sql
```

依赖方向（不可破坏）：

```
engine.go → liquidity_push.go → liquidity (pure pkg) + repository.go
liquidity_push.go → delivery.go
liquidity (pure pkg) 不依赖 repository / db / http
```

---

## 2. 输入：从 t_orderbook_snapshot 取深度矩阵

### 2.1 SQL 模板

`LoadFreshDepthMatrix(ctx, now, staleAfter, tier)` 读取每个 surface-aware row
的最新可展示深度。V1/V2/Spot 不能只靠 `(platform, display_symbol)` 去重，
否则 `platform=edgeX` 的不同 surface 会互相覆盖。

```sql
SELECT s.platform,
       COALESCE(s.platform_group, ''), COALESCE(s.display_platform, ''), COALESCE(s.is_edgex, 0),
       s.display_symbol, COALESCE(s.canonical_symbol, ''), COALESCE(s.venue_symbol, ''),
       COALESCE(s.market_surface, ''), COALESCE(s.instrument_kind, ''), COALESCE(s.lineage, ''),
       COALESCE(s.contract_id, ''), COALESCE(s.base_asset, ''), COALESCE(s.quote_asset, ''),
       COALESCE(s.depth_source, ''), COALESCE(s.source_id, ''), COALESCE(s.source_endpoint, ''),
       s.snapshot_ts,
       COALESCE(s.bid_usd, 0), COALESCE(s.ask_usd, 0), COALESCE(s.total_usd, 0)
  FROM t_orderbook_snapshot s
  JOIN (
    SELECT platform, display_symbol,
           COALESCE(market_surface, '') AS market_surface,
           COALESCE(instrument_kind, '') AS instrument_kind,
           COALESCE(lineage, '') AS lineage,
           COALESCE(venue_symbol, '') AS venue_symbol,
           COALESCE(contract_id, '') AS contract_id,
           MAX(snapshot_ts) AS snapshot_ts
      FROM t_orderbook_snapshot
     WHERE tier = ?
       AND snapshot_ts >= ?              -- now - staleAfter
       AND depth_status IN ('complete','partial','aggregated_orderbook','ws_limited_depth')
     GROUP BY platform, display_symbol,
              COALESCE(market_surface, ''), COALESCE(instrument_kind, ''),
              COALESCE(lineage, ''), COALESCE(venue_symbol, ''), COALESCE(contract_id, '')
  ) latest
    ON s.platform = latest.platform
   AND s.display_symbol = latest.display_symbol
   AND COALESCE(s.market_surface, '') = latest.market_surface
   AND COALESCE(s.instrument_kind, '') = latest.instrument_kind
   AND COALESCE(s.lineage, '') = latest.lineage
   AND COALESCE(s.venue_symbol, '') = latest.venue_symbol
   AND COALESCE(s.contract_id, '') = latest.contract_id
   AND s.snapshot_ts = latest.snapshot_ts
 WHERE s.tier = ?;
```

`partial` 行会进入矩阵，因为告警需要比较“当前可展示的流动性下界”；
`error` / `stale` / `unsupported` / `insufficient_history` 不进入矩阵。

### 2.2 canonical 折叠

每行 `(platform, display_symbol)` 通过 `CanonicalIndex.Resolve(platform, baseFromDisplay)` 折叠到 canonical：

- `display_symbol` 形如 `BTC-USDT (perp)` / `XAU-USDT (perp)` / `1000PEPE-USDT (perp)` 等
- 先复用 `divergence.CanonicaliseSymbol(displaySymbol)` 把 raw form 剥到 base
- 再用 `CanonicalIndex.Resolve(platform, base)` 做 alias-aware 反查
- 最终矩阵：`map[canonical]map[platform]PlatformDepthRow`
- 如果同一个 canonical 下同一 platform 存在多个 surface（例如 `edgeX V1` 与
  `edgeX V2`），当前告警仍按平台级别比较，保留该 platform 深度最大的 surface，
  并把该 surface 的 `display_platform` / `market_surface` / `depth_source` 等归因字段带入卡片。

### 2.3 PlatformDepthRow

```go
type PlatformDepthRow struct {
    Platform        string
    PlatformGroup   string
    DisplayPlatform string
    IsEdgeX         bool
    DisplaySymbol   string
    CanonicalSymbol string
    VenueSymbol     string
    MarketSurface   string
    InstrumentKind  string
    Lineage         string
    ContractID      string
    BaseAsset       string
    QuoteAsset      string
    DepthSource     string
    SourceID        string
    SourceEndpoint  string
    Tier            string
    DepthUSD        float64    // total_usd (双边合计)
    BidUSD          float64
    AskUSD          float64
    SnapshotTS      time.Time
}
```

Lark 卡片的 `AllPlatforms` 会继承这些 surface/source 字段。对 EdgeX V2，
卡片应能显示 `edgeX V2`、`perp_v2`、`edgeX-perp-v2`、`contract_id`、
`depth_source`、`source_id` 与 `source_endpoint`，便于定位是 WS local book、
REST fallback 还是上游错误导致告警。

---

## 3. 排除规则：CanonicalIndex.IsPlatformExclusive

新增方法：

```go
// IsPlatformExclusive 当且仅当该 canonical 在 symbol_mapping.yaml
// 中只有一个平台声明 alias 时返回 true。divergence 包用 crossPlatform
// fallback 容忍配置不全；#10/#11 必须更严格——特色标的没法做横向比较。
func (idx *CanonicalIndex) IsPlatformExclusive(canonical string) bool
```

实现：在 `NewCanonicalIndex` 构造时多维护一个 `platformCount map[canonical]int`，记录每个 canonical 被声明 alias 的平台数。`IsPlatformExclusive` 直接返回 `platformCount[canonical] <= 1`。

边界：

- canonical 未声明 → 返回 false（保守地参与比较，让告警暴露问题）。
- canonical 只在 edgeX 一家声明 → 返回 true（典型的"我们独家"标的，比对无意义）。
- canonical 在 ≥2 家声明 → 返回 false（进入比较流程）。

---

## 4. Compute 算法

```go
func Compute(
    matrix map[string]map[string]PlatformDepthRow,
    universe *config.ListedUniverse,
    exclusion func(canonical string) bool,
    cfg Config,
) []AlertCandidate {
    out := make([]AlertCandidate, 0)
    for canonical, perPlatform := range matrix {
        if !universe.IsListed("edgeX", canonical) {
            continue
        }
        if exclusion(canonical) {
            continue
        }
        edgex, ok := perPlatform["edgeX"]
        if !ok {
            // edgeX 列在 universe 里但没采到 / stale → 不评估（避免 false positive）
            continue
        }
        comparators := comparatorDepths(perPlatform)  // 不含 edgeX
        if len(comparators) < cfg.MinComparators {
            continue
        }
        allDepths := append(comparators, edgex.DepthUSD)
        sort.Sort(sort.Reverse(sort.Float64Slice(allDepths)))
        edgexRank := rankOf(edgex.DepthUSD, allDepths)
        median := medianOf(comparators)

        if edgex.DepthUSD < median*cfg.LagThreshold {
            out = append(out, AlertCandidate{
                Kind:         KindLiquidityLag,
                Canonical:    canonical,
                EdgexDepth:   edgex.DepthUSD,
                Median:       median,
                Ratio:        edgex.DepthUSD / median,
                Comparators:  len(comparators),
                Rank:         edgexRank,
                AllPlatforms: collectSortedPlatforms(perPlatform),
                Snapshot:     latestSnapshotTS(perPlatform),
            })
        }
        if edgexRank == len(allDepths) { // 1-indexed 深度垫底（spec §4.2 冻结）
            out = append(out, AlertCandidate{
                Kind: KindWorstDepth,
                ...
            })
        }
    }
    sort.Slice(out, ...)
    return out
}
```

### Universe 来源

`Compute(matrix, universe, ...)` 的 `universe` 不应再理解为单纯的静态 `config/listed_universe.yaml`。Dashboard runtime 会优先加载 Listing Agent 刷新的 `listed_universe.runtime.yaml`，失败时才回退 seed yaml；因此 #10/#11 对 edgeX 是否已上线的判断会随 `t_listing_instrument_snapshot` 动态更新。完整链路见 `listing-agent-dynamic-catalog-integration.md`。

动态 catalog poll loop 已改成 best-effort：单条 `t_listing_signal_observation` 写入失败只应降级为日志与 source-health 线索，不应阻断 `t_listing_instrument_snapshot.last_seen_at` 刷新。因此 liquidity alert 的 universe 异常通常应先按 snapshot freshness / source health 排查，而不是直接调 alert 阈值。

### 边界细节

- **median 算法**：使用经典中位数（even N 取两中间值平均），单家平台数据为 NaN/Inf 直接丢弃
- **rank 1-based vs 0-based**：对外 `Rank=1` 是最强、`Rank=N` 是垫底（N = 总平台数包含 edgeX）。Phase 0（spec §4.2）把 #11 worst_depth 冻结为"edgeX 垫底"，即 1-indexed `edgexRank == TotalPlatforms`；旧 PRD 中的"倒数第二"说法**不**再使用，避免与"垫底"语义混淆
- **edgeX 数据缺失**：不评估，但不报错（V1 一致：不允许用 0 占位）
- **edgex.DepthUSD == 0**：不参与，记录到日志 `lhs zero depth`，但不发卡（深度为 0 通常意味着采集异常或休市）

---

## 5. 状态机：state.go

### 5.1 `ActionDecision`

```go
type Action int
const (
    ActionSilent Action = iota
    ActionFirstTrigger    // status 无 / cleared → active, seq+1
    ActionReissue          // active 持续 ≥6h，发心跳
    ActionClear            // active 但连续 clear_consecutive 次不触发
)

type ActionDecision struct {
    Action       Action
    NewState     AlertState  // CAS upsert 用
    DedupeKey    string
    SeverityIdx  int          // reissue 次数（用于卡片标题"第 N 次提醒"）
}
```

### 5.2 dedupe_key 构造

```go
// 首次：     "<kind>|<canonical>|seq<N>|first"
// 心跳:      "<kind>|<canonical>|seq<N>|reissue<M>"
// 恢复:      "<kind>|<canonical>|seq<N>|clear"
func buildDedupeKey(kind AlertKind, canonical string, seq, reissueIdx int, phase string) string {
    switch phase {
    case "first":
        return fmt.Sprintf("%s|%s|seq%d|first", kind, canonical, seq)
    case "reissue":
        return fmt.Sprintf("%s|%s|seq%d|reissue%d", kind, canonical, seq, reissueIdx)
    case "clear":
        return fmt.Sprintf("%s|%s|seq%d|clear", kind, canonical, seq)
    }
}
```

### 5.3 状态转移表

| 当前 | 本轮 triggered | 心跳冷却 | 动作 | new state |
|---|---|---|---|---|
| nil / cleared | false | — | Silent | unchanged |
| nil / cleared | true | — | FirstTrigger | active, seq+1, clear_streak=0, last_pushed=now |
| active | true | <6h | Silent | clear_streak=0, last_evaluated=now |
| active | true | ≥6h | Reissue | clear_streak=0, last_pushed=now, last_evaluated=now |
| active | false | — | clear_streak++ | last_evaluated=now |
| active | false | clear_streak>=N | Clear | status=cleared, last_pushed=now |

### 5.4 CAS 写入

`UpsertAlertState(ctx, prev, next)`：

```sql
UPDATE t_listing_alert_state
   SET status = ?, severity_seq = ?, last_pushed_at = ?,
       last_evaluated_at = ?, clear_streak = ?, last_severity_json = ?
 WHERE alert_kind = ? AND canonical_symbol = ?
   AND last_evaluated_at = ?       -- prev.last_evaluated_at；CAS guard
```

若 `RowsAffected == 0`：另一实例抢先了，本 tick silent。

新行（首次触发）：`INSERT ... ON DUPLICATE KEY UPDATE` 兜底竞态。

---

## 6. 输出：outbox payload

复用 `t_listing_delivery_outbox`，仅 `event_type` 和 `payload_json` 不同：

```json
{
  "card_version": "v1",
  "kind": "liquidity_lag",
  "canonical": "BTC",
  "display_symbol": "BTC-USDT (perp)",
  "phase": "reissue",
  "severity_seq": 1,
  "reissue_index": 3,
  "first_triggered_at": "2026-05-28T09:54:00Z",
  "evaluated_at": "2026-05-29T03:54:00Z",
  "kpi": {
    "edgex_depth_usd": 2400000,
    "median_depth_usd": 5800000,
    "ratio": 0.4138,
    "lag_threshold": 0.5,
    "comparators": 8,
    "rank": 6,
    "total_platforms": 9
  },
  "platforms": [
    {"platform": "binance", "display_platform": "binance", "depth_usd": 8500000, "rank": 1},
    {
      "platform": "edgeX",
      "display_platform": "edgeX V2",
      "platform_group": "edgeX",
      "is_edgex": true,
      "market_surface": "perp_v2",
      "lineage": "edgeX-perp-v2",
      "contract_id": "30000001",
      "depth_source": "ws_local_book",
      "source_id": "edgeX-perp-v2-ws-depth-200",
      "source_endpoint": "wss://edgex-quote-prod-v2.edgex.exchange/api/v1/public/ws",
      "depth_usd": 2400000,
      "rank": 6
    },
    ...
  ],
  "dashboard_url": "https://dashboard/liquidity?symbol=BTC&tier=0.001"
}
```

`event_type` 取值：`liquidity_lag` / `worst_depth`。
`target_channel` 取 `Alert.Webhooks.liquidity`（fallback 到空 → outbox 标记 disabled，不阻塞产出）。

---

## 7. webhook 路由：delivery.go

新增：

```go
func resolveWebhookForChannel(cfg config.Config, eventType string) string {
    switch eventType {
    case "liquidity_lag", "worst_depth":
        if w := strings.TrimSpace(cfg.Alert.Webhooks.Liquidity); w != "" {
            return w
        }
        return ""  // 不 fallback 到 listing 通道，避免噪声混群
    case "top30_hot_gap":
        fallthrough
    case "top30_divergence_cex_only", "top30_divergence_dex_only",
         "top30_divergence_heavy_gap", "top30_divergence_both_hot_gap":
        if w := strings.TrimSpace(cfg.Alert.Webhooks.Listing); w != "" {
            return w
        }
        if cfg.Alert.Enabled && strings.TrimSpace(cfg.Alert.WebHookP3) != "" {
            return cfg.Alert.WebHookP3  // 向后兼容
        }
        return ""
    }
    return ""
}
```

`DrainDueOutbox` 改为读 outbox 行的 `event_type` 后调用 `resolveWebhookForChannel`，而不是从全局 cfg 直接读 P3。

---

## 8. 配置迁移

### 8.1 Alert.Webhooks 结构

`internal/config/config.go`:

```go
type AlertWebhooks struct {
    Listing   string `mapstructure:"listing" yaml:"listing"`
    Liquidity string `mapstructure:"liquidity" yaml:"liquidity"`
}

type AlertConfig struct {
    AppName  string         `mapstructure:"AppName"`
    Enabled  bool           `mapstructure:"Enabled"`
    Webhooks AlertWebhooks  `mapstructure:"Webhooks"`

    // Deprecated. Kept so existing nacos config doesn't fail-load
    // during the rollout. New code MUST NOT read these directly.
    WebHookP12 string `mapstructure:"WebHookP12"`
    WebHookP3  string `mapstructure:"WebHookP3"`
    WebHookP45 string `mapstructure:"WebHookP45"`
}
```

加载后立即做 fallback：

```go
// auto-migrate legacy WebHookP3 → Webhooks.listing if the new field is empty
if cfg.Alert.Webhooks.Listing == "" && cfg.Alert.WebHookP3 != "" {
    cfg.Alert.Webhooks.Listing = cfg.Alert.WebHookP3
}
```

### 8.2 LiquidityAlert 块

```go
type LiquidityAlertConfig struct {
    Enabled          bool          `mapstructure:"enabled"`
    DepthTierPct     float64       `mapstructure:"depth_tier_pct"`
    LagThreshold     float64       `mapstructure:"lag_threshold"`
    MinComparators   int           `mapstructure:"min_comparators"`
    ReissueInterval  time.Duration `mapstructure:"reissue_interval"`
    ClearConsecutive int           `mapstructure:"clear_consecutive"`
    StaleAfter       time.Duration `mapstructure:"stale_after"`
    PollInterval     time.Duration `mapstructure:"poll_interval"`
    MaxPerTick       int           `mapstructure:"max_per_tick"`
    SendSpacing      time.Duration `mapstructure:"send_spacing"`
}
```

默认值（`config.go` 的 `Defaults()`）：

| 字段 | 默认 |
|---|---|
| Enabled | false（默认关闭，让 SG 灰度后才开） |
| DepthTierPct | 0.001 |
| LagThreshold | 0.5 |
| MinComparators | 3 |
| ReissueInterval | 6h |
| ClearConsecutive | 3 |
| StaleAfter | 30m |
| PollInterval | 5m |
| MaxPerTick | 5 |
| SendSpacing | 0 |

---

## 9. Engine 接入

`engine.go` 加 Step 2c：

```go
liquidity, liquidityErr := ProduceLiquidityAlertPush(ctx, e.repo, LiquidityDeps{
    Now:               e.deps.Now,
    DashboardBase:     e.cfg.Runtime.ListingAgent.Delivery.DashboardBaseURL,
    LiquidityCfg:      e.cfg.Runtime.ListingAgent.LiquidityAlert,
    Resolver:          e.cfg.CanonicalIndex,
    Universe:          e.deps.LoadUniverse,
    PlatformExclusive: e.cfg.CanonicalIndex.IsPlatformExclusive,
    MaxAttempts:       e.cfg.Runtime.ListingAgent.Worker.MaxAttempts,
})
summary.LiquidityAlert = liquidity
if liquidityErr != nil {
    e.deps.Logger.Printf("listing engine: liquidity alert error: %v", liquidityErr)
}
```

不短路其它 step（保持与 P1/P2 一致的"per-stage fail-closed"）。

---

## 10. 预览 CLI

`backend/scripts/liquidity-alert-preview/`：

```bash
# Dry-run（不写 outbox，不更新 state，仅渲染并打印 JSON）
cd backend && go run ./scripts/liquidity-alert-preview \
  --config-dir=../config \
  --mysql-dsn="..." \
  --canonical=BTC --tier=0.001 \
  --dry-run

# Live preview（POST 真实 webhook，不写 outbox / state）
cd backend && go run ./scripts/liquidity-alert-preview \
  --config-dir=../config --mysql-dsn="..." \
  --canonical=BTC --tier=0.001 \
  --phase=first
```

`--phase=first|reissue|clear` 让运营预览三种卡片形态。

---

## 11. 排障 (runbook 补丁)

| 现象 | 排查路径 |
|---|---|
| 群里没收到任何 #10/#11 卡 | 1) `Runtime.listing_agent.liquidity_alert.enabled` 是否 true；2) `Alert.Webhooks.liquidity` 是否填值；3) `SELECT * FROM t_listing_delivery_outbox WHERE event_type IN ('liquidity_lag','worst_depth') ORDER BY id DESC LIMIT 10`；4) attempts 表里看 last_error |
| 一发就是几十张卡刷屏 | `t_listing_alert_state` 是否丢数据（CAS 失败、迁移没跑）；检查 `clear_streak` 是否一直 0；调大 `clear_consecutive` 临时止血 |
| 触发了一次就再也不发 | `severity_seq` 没递增（cleared → active 流程有 bug）；查 state 表是否一直 `status=cleared` 但又触发了 |
| 某 canonical 一直被排除 | `CanonicalIndex.IsPlatformExclusive(canonical)` 看 platformCount；可能 symbol_mapping.yaml 漏配 |
| edgeX 已上市判断突然异常 / 告警大面积停发 | 先查 `listed_universe.runtime.yaml` 的 `generated_at` 与 base_assets 数量，再查 `t_listing_source_state` 和 `t_listing_instrument_snapshot.last_seen_at`；若日志出现 `ErrSignalSilentFail` / `insert signal ... continuing tick`，说明 signal 写入异常但 snapshot 理论上仍应继续刷新 |
| 中位数偏离常识 | `LoadFreshDepthMatrix` 的 stale_after 太短，把活跃平台过滤掉了；或者某平台 collector 卡死（看 `t_collection_status`） |

---

## 12. 测试矩阵

| 测试 | 覆盖 |
|---|---|
| `compute_test.TestComputeLiquidityLagBasic` | 基本中位数计算 + 触发 |
| `compute_test.TestComputeWorstDepthOnly` | 倒数第二但不到 lag 阈值 |
| `compute_test.TestComputeBothKindsAtOnce` | 同 canonical 同时触发两种 |
| `compute_test.TestComputeMinComparatorsGuard` | <3 家不触发 |
| `compute_test.TestComputeSkipsExclusive` | 平台特色标的跳过 |
| `compute_test.TestComputeSkipsNonListed` | edgeX 未上线跳过 |
| `state_test.TestDecideActionFirstTrigger` | 无 state → FirstTrigger |
| `state_test.TestDecideActionReissueCooldown` | active 中冷却内 silent |
| `state_test.TestDecideActionReissueAfter6h` | active 中超 6h reissue |
| `state_test.TestDecideActionClearAfterStreak` | clear_streak 满 → clear |
| `state_test.TestDecideActionReentryIncrementsSeq` | cleared → active 时 seq+1 |
| `render_test.TestRenderLiquidityLagCard` | 卡片字段、颜色、KPI 行 |
| `render_test.TestRenderWorstDepthCard` | header=red、倒数第二话术 |
| `render_test.TestRenderClearCard` | 恢复卡 header=green |
| `repository_test.TestLoadFreshDepthMatrix` | SQL 含 stale 过滤、canonical 折叠 |
| `repository_test.TestUpsertAlertStateCAS` | CAS 冲突时 RowsAffected=0 |
| `engine_test.TestRunOnceLiquidityAlert` | end-to-end engine tick |

---

## 13. 灰度建议

1. SG 环境先把 `enabled: true` 打开，`Alert.Webhooks.liquidity` 留空——只产生 outbox 行，不推送，验证算法不误报
2. 观察 1-2 天，确认 candidates / state 表的转移符合预期
3. 配置 liquidity webhook 群（建议新建群，避免污染 listing 通道），开始推送
4. 跑一周后评估：reissue 次数 / clear 频率 / 是否有抖动伪触发；调 `reissue_interval` 或 `clear_consecutive`
