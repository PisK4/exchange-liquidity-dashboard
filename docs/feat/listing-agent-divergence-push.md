
## 背景

`listing-agent-top30-hot-gap-push.md` 把 P1 #1 hot-gap 卡（单 symbol、cross-platform coverage 视角）的链路写到 `t_listing_signal_observation → t_listing_delivery_outbox → DrainDueOutbox → Lark webhook`。Listing Agent P2 在此基础上加了 **4 张 CEX/DEX 分歧卡（#2-#5）**：

```text
t_top30_snapshot
    ↓ Top30RowForPush ← snapshotloader
listing engine (RunOnce, 与 #1 共用 cadence)
    ├── ProduceTop30Push       (#1 卡, 见 hot-gap doc)
    ├── ProduceDivergencePush  (#2-#5 卡, 本 doc 主体)
    │     ↓ 写 t_listing_delivery_outbox (event_type = top30_divergence_*)
    └── DrainDueOutbox
          ↓ POST Lark webhook (与 #1 共用 Listing delivery channel；推荐 Alert.Webhooks.Listing，兼容 Alert.WebHookP3)
          ↓ 写 t_listing_delivery_attempt 审计行
```

每张卡是一个独立 outbox row，`dedupe_key = top30_divergence|<category>|<YYYY-MM-DD>`，同一天同一类别只发一次（`uk_listing_delivery_dedupe`）。

参考设计文档：

- `../../../../architecture/方案设计/EdgeX运营/Listing/2026-05-28-Listing-Agent-P2-CEX-DEX-divergence-#2-#5.md`（业务设计背景；当前 canonicalisation / symbol identity implementation truth 以本仓库 `config/symbol_mapping.yaml`、`backend/internal/config/canonical_index.go` 和 `listing-agent-symbol-identity-normalization.md` 为准）
- `listing-agent-top30-hot-gap-push.md`（#1 卡，本 doc 大量复用其设计 —— 三态语义、proxy 边界、delivery state machine、interactive card schema gotchas）
- `listing-agent-dynamic-catalog-integration.md`（动态 instrument snapshot 与 runtime listed universe 如何影响 `EdgexListed` 三态）

历次主要提交：

```text
e1b8cda feat(edgex-ops-intelligence): extract internal/divergence shared package
197ebdc feat(edgex-ops-intelligence): add #2-#5 divergence push producer
8a90680 feat(edgex-ops-intelligence): wire divergence producer into listing engine
3f04199 feat(edgex-ops-intelligence): add divergence-preview CLI for #2-#5 cards
04f84c5 feat(edgex-ops-intelligence): add lark-push-smoke harness to exercise the full Lark delivery chain
cfdad4e fix(edgex-ops-intelligence): alias-aware divergence canonicalisation
696bc6a feat(edgex-ops-intelligence): expand divergence cex/dex_only cards with per-platform breakdown
831af21 feat(edgex-ops-intelligence): add --fixture flag to divergence-preview CLI
```

## 4 张卡的契约

| Category 常量 | event_type | header | 排序 | 业务问题 |
|---|---|---|---|---|
| `DivergenceCategoryCEXOnly` | `top30_divergence_cex_only` | blue | `CEXRank ASC` | CEX 阵营独有热门、DEX 完全没有 |
| `DivergenceCategoryDEXOnly` | `top30_divergence_dex_only` | purple | `DEXRank ASC` | DEX 阵营独有热门、CEX 完全没有 |
| `DivergenceCategoryHeavyGap` | `top30_divergence_heavy_gap` | orange | `|RankDelta| DESC` | 两阵营都有，但跨阵营 \|Δrank\| ≥ 阈值 |
| `DivergenceCategoryBothHotGap` | `top30_divergence_both_hot_gap` | red | `min(CEXRank, DEXRank) ASC` | 两阵营都热、edgeX 没上 |

每张卡只在 `EdgexListed *bool == &false` 的 canonical 上触发，与 #1 相同的 **三态语义**（见 hot-gap doc §"edgex_listed 三态语义"）—— `nil` 与 `&true` 一律剔出。该三态来自 Top30 snapshot，而 Top30 snapshot 的 edgeX listed 判断会优先消费 `listed_universe.runtime.yaml`，因此 Listing Agent dynamic catalog 会间接影响 divergence 卡过滤结果。

`#4 heavy_gap` 与 `#5 both_hot_gap` **允许同一 canonical 同时出现** —— 设计有意保留：heavy 看的是 rank 差距，bothHot 看的是"两边都热"事实。

## 触发条件与聚合

`BuildDivergencePushEvents(rows, cfg, resolver, topN, day)`（`backend/internal/listing/divergence_push.go`）流程：

1. 把 `[]Top30RowForPush` 转 `[]divergence.InputRow`，同步建 `knownByCanonical: map[canonical]*bool` —— last-write-wins 合并三态。
2. 调 `divergence.Compute(inputs, divergence.Config{CEXPlatforms, DEXPlatforms, SignificantRankDelta, Resolver})`：
   - `aggregateClass(rows, platforms, resolver)`：按 canonical 分桶聚合，**走 resolver**
   - `buildDivergenceRows(cex, dex, threshold)`：outer join + 分类
   - `edgexListedSet(rows, resolver)`：从 listed=true 行抽 canonical
3. `filterDivergenceRowsForAlert(divergence, knownByCanonical)`：三态过滤（剔 nil + 剔 \*true）
4. 按 4 个 category 分桶 → 每桶按业务规则排序 → 截 `topN`
5. 每个**非空**桶发一张卡；空桶不发卡（设计有意）

stale 守护：`ProduceDivergencePush` 在 `now - SnapshotTS > PushCfg.StaleAfter`（默认 30 min）时 fail-closed 返回 `FailClosed: "snapshot_stale"`，不写 outbox。

`Top30DivergencePushConfig`：

```yaml
Runtime:
  listing_agent:
    top30_divergence_push:
      enabled: true
      top_n_per_card: 10
      stale_after: 30m
      send_spacing: 0s   # 与 hot-gap 共用 webhook 时可配 N 秒错峰
```

## Symbol canonicalisation（含别名感知，cfdad4e 后）

This divergence path folds Top30 display/native forms for cross-camp ranking;
the broader Listing Agent runtime identity contract is documented in
`listing-agent-symbol-identity-normalization.md`. Keep the same source of truth
(`config/symbol_mapping.yaml`) for both paths so a venue alias reviewed for one
Listing Agent surface does not drift from the other.

跨交易所同一资产命名差异会造成分桶错位：1000PEPE/PEPE、GOLD/XAU/XAUT/PAXG、XYZ:CL/CL、BRENTOIL/CL/OIL 等。两层处理：

**第 1 层 raw-form**（`divergence.CanonicaliseSymbol`，无状态）

```text
" GOLD(XAU)-USDT (perp) " →  trim+upper
"GOLD(XAU)-USDT (PERP)"   →  剥 " (PERP)"
"GOLD(XAU)-USDT"          →  剥 quote suffix "-USDT/-USDC/-USD/-BUSD/-FDUSD/-TUSD"
"GOLD(XAU)"               →  剥 "XYZ:" 前缀（命中即剥）
"GOLD(XAU)"               →  拆 BASE(ALIAS) 取外层
"GOLD"                    →  剥 "1000"/"10000" 前缀（命中即剥）
"GOLD"                    
```

**第 2 层 alias resolver**（`config.CanonicalIndex.ResolveCanonical(platform, base)`）

从 `config/symbol_mapping.yaml` 的 `symbols[].aliases[platform][]base` 构建反向索引；查找优先级：

```text
1. byPlatform[lower(platform)][upper(base)]  // 命中 → 用对应 canonical
2. crossPlatform[upper(base)]                // 全网唯一别名 fallback
3. 否则 → upper(base)                         // identity
```

冲突保护：若同一 base 被两个 canonical 都声明过（如假设的 `BAT` 既在 X 又在 Y），fallback 不会自动选一个 —— 保留 raw base，让卡片自然显示"两个桶"，运营从视觉发现冲突再去补 YAML。

`*CanonicalIndex` 实现 `divergence.CanonicalResolver` 接口（适配方法 `ResolveCanonical`）。**nil-safe**：注入空 resolver 时所有逻辑退化为 raw-form-only。

resolver 在 divergence 链路上的注入点：

| 调用方 | 来源 | 文件 |
|---|---|---|
| `engine.go` Step 2b | `e.cfg.CanonicalIndex` | `internal/listing/engine.go` |
| `/api/snapshot/top30/divergence` | `s.cfg.CanonicalIndex` | `internal/collector/top30_divergence.go` |
| `scripts/lark-push-smoke` | `cfg.CanonicalIndex` | `backend/scripts/lark-push-smoke/main.go` |
| `scripts/divergence-preview` | `cfg.CanonicalIndex` | `backend/scripts/divergence-preview/main.go` |

`BuildDivergencePushEvents` 内部用同一 resolver 同时折叠：(a) 聚合 inputs、(b) `knownByCanonical` 三态 map 的 key、(c) `edgexListedSet` 已上线 canonical 集合。三处必须用**同一** resolver，否则分桶后 listed 状态与 divergence row 的 Symbol 字段不对齐 → 三态过滤失效。

### 与 V1 流动性监控页同源

V1 页（"第一个页面"）走 `canonical → 各家 api_symbol` 的**正向**；divergence 走 `各家 native symbol → canonical` 的**反向**。两条路径**共用** `symbol_mapping.yaml`，将来给 V1 加 alias（如新 commodity canonical），divergence 卡自动受益。

## 配置（Alert + Runtime）

复用 hot-gap doc §"配置"。webhook：

```yaml
Alert:
  WebHookP3: "https://open.larksuite.com/open-apis/bot/v2/hook/<token>"   # 与 #1 共用

Runtime:
  top30_divergence:
    cex_platforms: [binance, okx, bybit, bitget, bingx, mexc, gate]
    dex_platforms: [edgeX, hyperliquid, lighter]
    significant_rank_delta: 10

  listing_agent:
    top30_divergence_push:
      enabled: true
      top_n_per_card: 10
      stale_after: 30m
      send_spacing: 0s
    delivery:
      dashboard_base_url: "https://exchange-liquidity-dashboard-pisk4s-projects.vercel.app/?tab=top30"
      proxy: "http://host.docker.internal:7897"   # 仅 Lark webhook 用
```

`dashboard_base_url` 直接传到所有 4 张卡 footer 上方的 CTA 按钮 URL；divergence 卡按类别聚合所以不带 `?symbol=` 参数（与 #1 不同），点击进入 Top30 tab 总览。

## 表设计（沿用 P1 schema）

`t_listing_delivery_outbox` 不需要新增列；`event_type` 用枚举：

| event_type 常量 | 值 |
|---|---|
| `DeliveryEventTop30DivergenceCEXOnly` | `top30_divergence_cex_only` |
| `DeliveryEventTop30DivergenceDEXOnly` | `top30_divergence_dex_only` |
| `DeliveryEventTop30DivergenceHeavyGap` | `top30_divergence_heavy_gap` |
| `DeliveryEventTop30DivergenceBothHotGap` | `top30_divergence_both_hot_gap` |

`dedupe_key = top30_divergence|<category>|<YYYY-MM-DD>`，UNIQUE 保证同一类别一天一卡。

`payload_json` 是**渲染好的** Lark interactive body；结构化 `DivergencePushEvent` 仅在内存里流转 —— 由于 #2-#5 是聚合维度的告警（不像 #1 是单 symbol），目前**不**写 `t_listing_signal_observation`（候选融合不需要把"今天有 N 个 dex_only 符号"作为单条 signal 来源）。

## 卡片渲染

`RenderDivergencePostMessage(ev)`（`backend/internal/listing/divergence_push.go`）生成 Lark `msg_type=interactive` body。**4 张卡共享 header/footer/CTA 形态，但 KPI strip 与行格式按"叙事维度"分两套**：

### 双轨：cex_only / dex_only（per-card 视角）vs heavy_gap / both_hot_gap（cross-camp 视角）

| 卡类别 | KPI strip | 行格式 | 叙事 |
|---|---|---|---|
| cex_only / dex_only | **本卡 per-card KPI**（双行 lark_md） | **主行 + 副行**（每家平台 native rank） | "这张卡里，独有阵营有哪些标的，每个标的具体在哪几家平台" |
| heavy_gap / both_hot_gap | legacy 全局 KPI（CEX 独有 / DEX 独有 / 显著分歧 / edgeX 缺口）4 个 short fields | 单行 `● {Sym} · CEX #N / DEX #M · Δ X` | "跨阵营 rank 差 / 两阵营都热"是 cross-camp 故事，per-camp 分布意义不大 |

### cex_only / dex_only 视觉（重构后）

```text
[Header · blue/purple]
  📊 CEX 独有热门 · edgeX 未上线

本卡 13 项 · 5+ 平台 2 / 3-4 平台 5 / 1-2 平台 6 · DEX 阵营 0 家 ❌
CEX 合计 24h $3.50B · 最硬 ALLO（6 家 · 最佳 #14）

─────────────────────
● ALLO · CEX 合计 24h $993.65M · 6 家平台
   okx #11 · bybit #13 · gate #13 · binance #15 · bitget #17 · bingx #27
● GUA · CEX 合计 24h $413.77M · 2 家平台
   gate #14 · binance #18
● ESPORTS · CEX 合计 24h $373.46M · 4 家平台
   bybit #16 · gate #22 · binance #24 · bitget #28
...
─────────────────────
[📊 查看 Top30 详情]

触发时间 2026-05-29 03:54 UTC · top30_divergence|cex_only|2026-05-29
```

### heavy_gap / both_hot_gap 视觉（沿用初版）

```text
[Header · orange/red]
  📊 CEX vs DEX 显著分歧 · edgeX 未上线

CEX 独有 5 | DEX 独有 8 | 显著分歧 6 | edgeX 缺口 14

─────────────────────
● ASTER · CEX #5 / DEX #28 · Δ 23
● TON · CEX #20 / DEX #4 · Δ 16
...
```

### 设计要点

| 元素 | 实现 | rationale |
|---|---|---|
| Header 颜色 | category 直接映射：cex_only=blue / dex_only=purple / heavy_gap=orange / both_hot_gap=red | 4 张卡同群同窗口出现时 header 颜色就是分类标签 |
| KPI strip 分流 | `buildDivergenceKPIStrip` 检测 `ev.CardKPI != nil`：非空走 `buildDivergenceCardKPIStrip`（双行 lark_md），nil 走 legacy 4-field div.fields | 让每张卡用最适合自己叙事的 KPI 形态 |
| per-card KPI 内容 | 第 1 行：`本卡 N 项 · 5+ 平台 X / 3-4 平台 Y / 1-2 平台 Z · {对岸} 阵营 0 家 ❌`<br>第 2 行：`{侧} 合计 24h $X · 最硬 {Sym}（N 家 · 最佳 #R）` | "本卡 N 项"用**截断前**的 eligible 数；"最硬"按 PlatformCount DESC，并列时 Rank ASC tie-break |
| 反向阵营缺位 tag | 仅出现在 KPI strip 末尾，每张 cex_only / dex_only 卡一次（per-row 不重复） | 标题已写"独有"，行内重复会冗余；KPI strip 集中提示更清爽 |
| 主行格式 (cex_only/dex_only) | `● {Symbol} · {Class} 合计 24h ${humanUSD} · {N} 家平台` | 体积按阵营内合计；"N 家平台"保留是为了让副行展开为 0 时仍有最小信号 |
| 副行格式 (cex_only/dex_only) | `　 {plat1} #{r1} · {plat2} #{r2} · ...`（全角空格缩进 + 中点分隔），按 NativeRank ASC | 直接回答用户最初诉求"这个币具体在哪几家平台 + 排几"；缺 NativeRank 的平台只显示名字、不出 `#0` |
| 单行格式 (heavy_gap/both_hot_gap) | `● {Sym} · CEX #N / DEX #M · Δ X` 或 `min #X` | cross-camp 故事，已 expose 双 rank，副行价值不高 |
| 平台数 | aggregate.PlatformCount，**等于 resolver 折叠后桶里的不重复平台数** | 别名感知后 1000PEPE+PEPE 不再各算 1 家 |
| 排序 (cex_only/dex_only) | `(CEXRank ASC, CEXPlatformCount DESC, Symbol ASC)`（dex_only 用 DEX 对应字段） | rank 主键保持心智，PlatformCount tie-break 让"硬信号"（多平台共识）在并列 rank 时优先 |
| CTA 按钮 | 单按钮，直接跳 Top30 tab 总览，不带 `?symbol=` | 类别卡是聚合视角，没有"该 symbol 详情"的概念 |
| Footer dedupe | 与 #1 一致，便于运维 grep outbox | 同 hot-gap |

整数 / 美元数量级 helper `humanUSD` 与 #1 共用。`RenderDivergencePostMessage` 完整模板沿用 hot-gap doc §"Schema gotchas" 的所有红线（`plain_text` 字段名是 `content` 不是 `text`、所有子元素一律 `{tag, content}` 形态、Lark 200 OK ≠ 卡片渲染对，必须人眼对照群里实际显示）。

### 关键数据结构（cex_only / dex_only 新增）

| 类型 | 字段 | 来源 |
|---|---|---|
| `DivergencePushPlatform` | `Platform`, `NativeRank`, `Volume24HUSD` | `BuildDivergencePushEvents` 内 `platformBreakdown` 预计算 → `sortedPlatforms` |
| `DivergencePushRow.CEXPlatformDetails` / `DEXPlatformDetails` | `[]DivergencePushPlatform`（NativeRank ASC） | 渲染副行 |
| `DivergenceCardKPI` | `TotalEligible`, `BroadCount` (≥5), `MidCount` (3-4), `NarrowCount` (1-2), `SideVolUSD`, `StrongestSymbol`/`Platforms`/`BestRank`, `OppositeCampLabel` | `computeDivergenceCardKPI(category, eligible)` 在 TopN 截断前算 |
| `DivergencePushEvent.CardKPI *DivergenceCardKPI` | nil for heavy_gap/both_hot_gap | renderer 分流键 |

`platformBreakdown` 在 `BuildDivergencePushEvents` 内做**第二次** resolver fold（与 `divergence.aggregateClass` 独立）—— 设计有意：让 listing 层包含新字段而不污染 `internal/divergence` 或 `domain` 类型，V1 collector wire schema 保持稳定。**两次 fold 必须用同一 resolver**，否则 canonical 不一致会让副行平台和主行 PlatformCount 对不上。

## Smoke 验证

`backend/scripts/lark-push-smoke/` 是 dedupe-prefix 隔离的端到端验证脚本：

```bash
cd backend
OPS_INTELLIGENCE_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true' \
  go run ./scripts/lark-push-smoke \
  --config-dir ../config --ack [--include divergence] [--skip-cleanup]
```

`--include` 可选 `all / hot_gap / divergence`，默认 all。脚本流程：

```text
phase 1: pre-cleanup, 删 dedupe_key LIKE 'lark_push_test|%' 的残留
phase 2: 取最新 t_top30_snapshot rows
phase 3: 跑 production 的 BuildTop30PushEvents + BuildDivergencePushEvents
phase 4: 写 outbox（dedupe_key 前面塞 "lark_push_test|<nonce>|" 前缀做隔离）
phase 5: DrainDueOutbox 走真实 Lark POST，断言 sent=inserted
phase 6: 重 drain，断言 sent=0（幂等）
phase 7: 重 insert，断言 affected=0（唯一键不可重复）
phase 8: 清理（除非 --skip-cleanup）
```

`DeliveryDeps.DedupeKeyPrefix` 字段（`internal/listing/delivery.go`）让 `loadDueOutbox` 在 SQL 上加 `AND dedupe_key LIKE ?`，所以 smoke 跑 drain 时**只看自己 nonce 前缀的行**，不会误吸生产 outbox row 也不会被生产引擎反吸。

> **smoke 与生产引擎并发安全**：smoke 用 nonce 隔离 dedupe_key 前缀；生产引擎跑 RunOnce 时同样从 `t_listing_delivery_outbox` 取 due rows，但 SQL 在两侧都不冲突。可在生产引擎正常运行时随时跑 smoke。

## Preview CLI（无需 Lark 推送）

`backend/scripts/divergence-preview/` 用于在不打扰 Lark 群的前提下调样式：

```bash
cd backend
OPS_INTELLIGENCE_MYSQL_DSN='...' go run ./scripts/divergence-preview \
  --config-dir ../config [--only cex_only] [--top-n 10] \
  [--fixture heavy_gap,both_hot_gap] [--dry-run]
```

CLI 把 `BuildDivergencePushEvents` 的输出渲染成 Lark interactive body JSON 写到 stdout（`--dry-run` 不打到 webhook）；`--only` 接受类别名过滤，便于复现特定卡的视觉。

**`--fixture`**：生产快照通常**不会**自然产出 #4 heavy_gap / #5 both_hot_gap 卡（在 alias 折叠 + `*EdgexListed==false` 三态过滤后，跨阵营 canonical 几乎都被屏蔽）。`--fixture=heavy_gap[,both_hot_gap]` 注入一组语义合理的合成 event 进入 in-memory 渲染流，**真实**事件仍会渲染（除非被 `--only` 排除）。合成 dedupe_key 用 `_fixture` 后缀（`top30_divergence|heavy_gap|YYYY-MM-DD_fixture`）便于人眼区分。

## 工程实现要点（与 #1 共用之外的）

| 关注点 | 实现位置 | 红线 |
|---|---|---|
| 内部 divergence 包 | `internal/divergence/` | 纯函数，不依赖 config / network / sql |
| `divergence.InputRow.EdgexListed *bool` | `divergence.go` | **三态保留**；上游不要把 `*false` 塌缩成 `nil` |
| `divergence.Config.Resolver` | `divergence.go` | nil-safe；缺省退化为 raw-form-only |
| `CanonicalIndex` 启动构建 | `internal/config/config.go Load()` | 从 `symbol_mapping.yaml` 一次构造，挂在 `Config.CanonicalIndex` |
| `aggregateClass` / `edgexListedSet` 用 resolver | `internal/divergence/divergence.go` | **同一** resolver，否则 listed 状态错位 |
| 4 个注入点同源 | engine + collector + 2 scripts | 见上表 |
| `platformBreakdown` 二次 fold | `BuildDivergencePushEvents` 内 listing 层独立计算 | **同一 resolver** —— 与 `aggregateClass` 不一致会让副行平台与主行 PlatformCount 对不上 |
| `DivergencePushPlatform` / `DivergenceCardKPI` 落在 listing 层 | `internal/listing/divergence_push.go` | 不污染 `internal/divergence` / `domain` 类型，V1 collector wire schema 不动 |
| `computeDivergenceCardKPI` 用截断前 eligible | 同上 | `本卡 N 项` 必须反映**真实**候选总数，不是 TopN 截断后的可见数 |
| `buildDivergenceKPIStrip` 分流 | 同上 | 以 `ev.CardKPI != nil` 为唯一开关；heavy/both_hot 必须保持 nil |
| sort tie-break (cex_only/dex_only) | `(Rank ASC, PlatformCount DESC, Symbol ASC)` | 让"硬信号"在并列 rank 时优先，主键仍是 rank |
| outbox row 状态机 | 与 #1 完全一致 | 见 hot-gap doc §"表设计与状态机" |
| webhook proxy 隔离 | 与 #1 完全一致 | 见 hot-gap doc §"Delivery HTTP client 与 per-feature proxy" |
| 卡片 schema | 与 #1 完全一致 | 见 hot-gap doc §"Schema gotchas" |

## 已知尾巴 / 后续工作

1. **V1 Top30 tab 的 `edgex_listed` 列没走 resolver**：`collector/coingecko_collector.go` `enrichTop30Rows` 仍用 `baseAssetFromSymbol + universe.IsListed` 直接字符串相等。可见后果：V1 Top30 表里 hyperliquid `XYZ:CL` 行的"edgeX 已上线"列会显示"否"（实际 edgeX 列了 CL）。divergence 卡侧已通过 resolver 在聚合期自动修正、不依赖这一列，所以**不阻塞** P2 上线；但 P3 / 持续优化时应把这一列也对齐。
2. **`symbol_mapping.yaml` 别名表覆盖度**：当前 OIL canonical 只列了 binance/edgeX/hyperliquid；bitget/bybit/gate/okx 的 `CL-USDT (perp)` 靠跨平台 fallback。可以在运营补 alias 后撤掉 fallback —— 但 fallback 的冲突保护让它**保留**也安全。
3. **没接 candidate fusion**：divergence 卡现在不写 `t_listing_signal_observation`、不进候选融合。可考虑把"今天 cex_only 列表里某 symbol 连续 N 天上榜"作为 candidate 的额外弱信号源。
4. **`heavy_gap` 与 `both_hot_gap` 重叠**：当前实现允许同一 symbol 同一天进两张卡，是有意保留的设计。若运营反馈"重复打扰"，可在 BuildDivergencePushEvents 内加 `bothHot = bothHot - heavy` 的差集逻辑 —— 但目前没有需求。
