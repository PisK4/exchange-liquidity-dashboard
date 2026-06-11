# Listing Agent Phase 2：决策卡片 + 按钮回调（#8 / #9 → Action Dispatch → Watchlist）

## 背景

> Current contract update (2026-06-10): decision cards now use source-first
> scoring v2 and a simplified operator-facing card. Numeric score and
> recommendation are derived from source/platform mix, not from API-vs-
> announcement evidence channel. The backend keeps recommendation,
> confidence, and risk plan fields for audit/callback compatibility, but the
> Lark card no longer renders recommendation/confidence/risk-plan copy.

Phase 1 把 Listing Agent 的左半边链路（信号采集 → 候选融合 → Top30 / 分歧 / 流动性推送）打通到飞书群里——但卡片**只读**：运营看到候选后还要回到 dashboard 自己点 / 自己改 SOP，决策动作没有被任何系统捕获。Phase 2 把右半边补齐：

```text
listing engine (RunOnce, 与 Phase 1 共用 cadence)
    ├── Phase 1: ProduceTop30Push / ProduceDivergencePush / ProduceLiquidityAlertPush
    └── Step 2d: ProduceDecisionCards    ← 本 doc 主体（#8）
          ↓ 写 t_listing_risk_plan + t_listing_delivery_outbox(decision_card_*)
          ↓ DrainDueOutbox → bounded market-status refresh → Lark interactive card with 4 个按钮

[运营点按钮]
    ↓ Lark webhook → POST /api/listing/callback   ← 本 doc 主体（#9）
                       ↓ HMAC-SHA256 验签 + ±300s 时钟窗 + open_id 白名单
                       ↓ InsertDecision (INSERT IGNORE on uk_listing_decision_idempotency)
                       ↓ DispatchDecisionAction
                             ├── prepare_listing → t_listing_action_dispatch + outbox(lark_listing_ops)
                             ├── enter_watchlist → t_listing_action_dispatch + t_listing_watchlist (silent)
                             ├── contact_mm     → t_listing_action_dispatch + outbox(lark_mm)
                             └── ignore         → t_listing_action_dispatch only（24h 冷却闸的真值源）
```

任意 `(candidate_id, action, callback_ts_truncated_to_second)` 三元组在数据库里只会有 **一条** decision 行：`uk_listing_decision_idempotency` 保证回调链路全程幂等。

参考设计文档：

- `/Users/pis/.factory/specs/2026-05-29-listing-agent.md`（Phase 2 全套 spec，含按钮矩阵 / 冷却窗 / 分发路由表 / 风险计划模板四组的所有真值来源）
- `listing-agent-top30-hot-gap-push.md`（Phase 1，本 doc 大量复用其设计：outbox 状态机、Lark interactive card schema gotchas、delivery HTTP client、proxy 边界）
- `listing-agent-divergence-push.md`（Phase 1，canonicalisation resolver 注入点的参照）
- `listing-agent-dynamic-catalog-integration.md`（edgeX snapshot → runtime listed universe → candidate already-listed reconcile 的来源说明）

历次主要提交（feat/listing_agent 分支，Phase 2 段）：

```text
4fe05f5 feat(edgex-ops-intelligence): land risk plan domain + repository helpers
1d4f7c5 feat(edgex-ops-intelligence): emit Lark decision cards for actionable candidates
2038ef8 feat(edgex-ops-intelligence): record signed Lark button clicks via callback API
8f734e3 feat(edgex-ops-intelligence): dispatch decisions to watchlist + action_dispatch + notify
4cdcf83 feat(edgex-ops-intelligence): wire decision card producer into engine and config
086d581 test(edgex-ops-intelligence): add e2e coverage for decision card callback flow
```

## 触发条件：哪类候选会被推一张决策卡

`ProduceDecisionCards(ctx, repo, deps)`（`backend/internal/listing/decision_card.go`）按下面的闸门挑候选：

| 闸门 | 说明 |
|---|---|
| `cand.LifecycleStatus ∈ {confirmed_listing_candidate, announced_pending_api, api_detected_no_announcement, pre_assessment_observed}` | 仅对仍可动作的 lifecycle 触发；已上线（`already_listed_on_edgex`）一律跳过。该状态可由 edgeX instrument snapshot 通过 `RefreshListedUniverseFromSnapshots` / `BulkMarkCandidatesAlreadyListed` 自动 reconcile 得到。 |
| `cand.Recommendation ≠ ""` | 评分还没生成 recommendation 的候选不发卡 |
| `cand.CanonicalSymbol` 不属于 stablecoin / quote / collateral target denylist | `USDC`、`USDT`、`USD1`、`USD`、`FDUSD`、`DAI` 等资产不是新 listing 操作标的；即便历史上已有旧 candidate，也跳过 risk plan / outbox，避免类似 Gate `USDC_USD1` 误推。 |
| linked signals 至少存在一条 fresh decision evidence | producer 会读取候选已链接信号，只保留 candidate-promoting 且未超过 `Runtime.listing_agent.candidate.historical_listing_grace_period` 的证据。若所有证据的 exchange-side `listing_time_ts` 都早于 `observed_at - grace_period`，则跳过 risk plan / outbox；原始 signal 保留供审计。 |
| `LatestDecisionForCandidate(candidate_id)` 不在冷却窗内 | 若上一条决策是 `ignore` 且 `now - callback_ts < IgnoreCooldown`（默认 24h），跳过；其它动作的最新决策也按相同窗口冷却 |
| 单 tick `Considered <= MaxPerTick` | 默认 20，避免新部署 burst 飞书群 |


> 注意：只在竞品平台（Binance / Gate / BingX 等）出现、但未进入 edgeX snapshot 的普通 token，不会被自动关闭为 `already_listed_on_edgex`；它们仍是 listing opportunity。稳定币、quote asset、collateral asset 例外：这些资产不作为 Listing Agent 的新 listing 操作标的，fusion 阶段不生成新 candidate，decision producer 阶段也会跳过历史旧 candidate。若 recommendation 为 `record_only`，是否单发或静默记录由 decision producer 的降噪策略决定。

匹配的候选每个产出：

1. 一条 `t_listing_risk_plan` 审计行（recommendation → template 见下方「风险计划模板」）；
2. 一条 `t_listing_delivery_outbox` 行，`event_type = listing_decision_candidate`，`dedupe_key = listing_decision|<candidate_id>|<fresh_evidence_signature>`。

`fresh_evidence_signature` 只由通过 freshness gate 的 linked signal fingerprint 计算；历史 `listing_time_ts` 证据不会改变 dedupe key，也不会被用于选取卡片中的交易所 listing time。`uk_listing_delivery_dedupe` 保证相同候选 + 相同新鲜证据只发一张决策卡；若出现新的 fresh evidence，dedupe key 会变化，但 `LatestDecisionForCandidate` 仍可通过 24h 冷却窗压住重复打扰。

`historical_listing_grace_period` 默认在 `config/edgex-ops-intelligence.yaml` 中配置为 `48h`。调大该值会把更早的交易所 launch/open 时间视为 fresh，可能增加推送；调小则更积极地把晚发现的旧市场 observation-only 化。

## 按钮矩阵（all evidence kinds）

`BuildDecisionCardEvent` 现在对所有 decision-card evidence kinds 使用同一组
`standardButtonMatrix()`。按钮是否可点击不再由 API-vs-公告通道做数值或 UX 降级；通道差异只通过卡片文案解释给运营。

| evidence_kind | prepare_listing | enter_watchlist | contact_mm | ignore | card evidence label |
|---|---|---|---|---|---|
| `announcement_and_api` | ✅ | ✅ | ✅ | ✅ | `API + 公告都已确认` |
| `instrument_diff_only` / `api_detected_no_announcement` | ✅ | ✅ | ✅ | ✅ | `API 已发现` |
| `announcement_pending_api` | ✅ | ✅ | ✅ | ✅ | `公告已发现` |
| `top30_only` | ✅ | ✅ | ✅ | ✅ | `热度发现` |
| `manual_seed` / `pre_assessment_observed` | ✅ | ✅ | ✅ | ✅ | `手动加入` / 原始 kind |

**红线**：四按钮是当前法定契约。前端 / 后端任一处偏离都会让运营看不到本该有的动作。
`decision_card_test.go::TestBuildDecisionCardEventEveryEvidenceKindShowsAllButtons` 锁定所有 evidence kind。

每个按钮在 Lark interactive card 上的 `value` 字段携带：

```json
{
  "candidate_id": 1234,
  "risk_plan_id": 5678,
  "action": "prepare_listing",
  "dedupe_key": "listing_decision|1234|2026-05-30"
}
```

回调 handler 收到 click 时**只信任** `value` 里这四个字段——signature / open_id / timestamp 走 Lark header，candidate 与 action 的真值不能从 URL query 来（防伪造）。

## 风险计划模板（recommendation → template）

`BuildRiskPlanFromCandidate(cand)`（`backend/internal/listing/risk_plan.go`）是纯函数，按 spec §5 给每个候选产出一条模板化的风险计划，落入 `t_listing_risk_plan`：

| recommendation | template | 关键参数 |
|---|---|---|
| `prepare_listing` | `tier1_standard` | leverage_max=50x，MM 必须就位，funding_band 标准，liquidation_buffer 标准 |
| `evaluate_listing` | `tier2_conservative` | leverage_max=20x，不强制 MM，funding_band 收紧 50%，liquidation_buffer +50% |
| `hold_watch` | `pre_assessment` | 仅占位，进 watchlist 时记录"待评估"快照 |
| 其它（默认） | `pre_assessment` | 同上 |

`RiskPlanVersion = "phase2_v1"`，所有行都打上版本戳；下次模板迭代只动 `RiskPlanVersion` + 模板内容，老行不动。`t_listing_risk_plan` 是 **INSERT-only audit**——回滚某次卡片只需要把对应 outbox row 标 `disabled`，risk plan 行保留以备追溯。

`LatestRiskPlanByCandidate` 在回调 handler 之外暂无消费者；但 `DispatchDecisionAction` 的 payload 里会带 `risk_plan_id`，下游 worker（如未来的"自动建仓"模块）就能按 id 取最新计划。

## 决策卡片渲染

`RenderDecisionCardPostMessage(ev)`（`decision_card.go`）生成 Lark `msg_type=interactive` body，结构与 Phase 1 hot-gap 卡同源（header → headline → fields → action row → footer note）：

```text
[Header: New Perp/Spot Listing Detected · <symbol>]

# BEAT
<font color='grey'>API + 公告都已确认</font>      ← evidence_kind label
─────────────────────
Token BEAT                     | edgeX 状态 未上线/未知
Source Binance, Bybit          | Detected Time 2026-05-30 17:25 UTC+8
Listing Time 2026-05-30 18:00 UTC+8（仅真实 API launch/open 时间存在时）
─────────────────────
Market Status（刷新失败则回退 DB snapshot，并在 footer 写 enrich_errors=N）
─────────────────────
Market Cap / Spot 24h Vol / 现货深度 / 合约深度
footer: metrics=mc:ok/ext vol:ok/ext spot:ok/live perp:ok/snap（仅在状态存在时显示）
─────────────────────
Score 90 / 100
─────────────────────
[准备上线] [进入观察] [联系MM] [忽略]

trigger=2026-05-30 17:25 UTC+8 · evidence=API + 公告都已确认 · dedupe=listing_decision|1234|first_listing
```

设计要点（仅记差异；与 hot-gap 卡共享的部分见 `listing-agent-top30-hot-gap-push.md` §"卡片渲染"）：

- **Source-first scoring v2**：`BusinessScoreVersion = v2`。Binance 单源即可约 80 分并进入 `prepare_listing`；Binance + 任一平台约 90；`bybit/okx/hyperliquid` 多源约 65；`mexc+bitget` 约 60；单个 `bybit/okx/hyperliquid` 约 55；其它单源约 40。API-vs-公告证据通道只影响卡片文案，不影响 numeric score。
- **按钮顺序固定**：`prepare_listing → enter_watchlist → contact_mm → ignore`，所有 decision-card evidence kinds 都渲染四个按钮。运营肌肉记忆固定后误点率会降——顺序是 UX 契约。
- **`value` payload**：每个按钮 `value` 都带 `candidate_id / risk_plan_id / action / dedupe_key` 四元组；其中 `dedupe_key` 与 outbox 表对齐，便于回调 handler 反查 outbox。
- **卡片减负**：recommendation、confidence、risk plan 不再渲染到 Lark 卡片。它们仍保留在 backend event / DB audit / callback payload 路径，保证兼容下游审计和动作分发。
- **时间语义**：基础信息区主字段固定展示 `Detected Time`（producer 触发时间）。交易所 API 返回的 launch/open 时间只在存在 fresh instrument-diff evidence 时以 `Listing Time` 单独展示；announcement `published_at` / `createdAt` 不得作为 `Listing Time`。
- **Market Status 行语义**：每个平台默认只展示 `平台 · 状态`，例如 `BingX Futures · Perp LIVE`。`last_seen_at` / poll observation time 只用于排序和审计，不得渲染成 listing/open 时间；只有 API 明确返回 `listing_time_ts` 时才追加 `Listing on MM-DD`（跨年时 `Listing on YYYY-MM-DD`）。Market Status 行默认不再追加 `API` / `公告` source 后缀，source evidence 仍保留在 footer 与后台审计字段中。
- **edgeX 可见性语义**：edgeX `enable_display_false` 表示未在产品前端展示/可能尚未上线，不等于已下架。卡片中应显示为 `未上线（API: enable_display_false）`，而不是 `已下架`；只有明确 `enable_trade_false` 才按下架/不可交易处理。
- **Market Status 预刷新**：渲染前可按 `Runtime.listing_agent.decision_card.market_status_refresh` 对 source platforms + edgeX 做一次 bounded/fail-open instrument API refresh。该 refresh 只读 API、带 timeout/concurrency/request budget/tick cache，不写 snapshot、不写 signal、不改变 candidate；失败时按配置回退 DB snapshot。
- **Metrics enrichment 语义**：卡片正文继续只显示 `Market Cap` / `Spot 24h Vol` / `现货深度` / `合约深度` 四个短标签，不新增 `Token 24h Vol` 或 `CG 24h Vol`。Market Cap 与 Spot 24h Vol 优先使用 CoinGecko token-level markets 数据；当 Spot 24h Vol 缺失时，才从本地 `t_symbol_volume_snapshot` 的 fresh `market_surface='spot'` 行汇总 fallback，明确排除 perp volume。深度不再复用候选 source platforms，而是按 `metric_enrichment.reference_platforms`（默认 Binance）查询 reference venue live depth，并在 live 某侧缺失时从 `t_orderbook_snapshot` fresh rows fallback。footer 的 `metrics=` 仅压缩展示状态/来源族（`ext` / `live` / `snap`），按钮 `value` payload 不变。
- **Metric footer source/status**：`metrics=` 的 source family 只用 `ext`（CoinGecko token-level market data）、`live`（reference venue live depth）和 `snap`（local MySQL snapshot fallback）。status 只表达指标可用性：`ok` 表示正文已渲染数值；`not_found` 表示外部 token-level source 未找到 token；`unsupported` 表示当前 source/surface 不支持；`stale` 表示 newest snapshot 超过 `metric_enrichment.stale_after`；`source_error` 表示 live/external source 请求或解析失败；`no_snapshot` 表示 DB fallback 没有 fresh usable rows。
- **Metric DB fallback 索引**：Spot/Perp depth fallback 依赖 `idx_orderbook_canonical_surface_tier_latest (canonical_symbol, market_surface, tier, platform, snapshot_ts)`；Spot 24h Vol fallback 依赖 `idx_symbol_volume_canonical_surface_latest (canonical_symbol, market_surface, platform, snapshot_ts)`。两者由 migration `000021_listing_metric_snapshot_indexes` 和 backend boot-time post-init guard 共同维护；上线前可用 `EXPLAIN` 确认查询命中这两个索引。
- **schema gotchas**：与 hot-gap 卡完全一致——`plain_text` / `lark_md` 的字段名是 `content` 不是 `text`，emoji 用于语义符号但 tier / status 类视觉用 `<font color>`，`SetEscapeHTML(false)` 让 outbox `payload_json` 肉眼可读。

`market_status_refresh` 推荐默认值：

```yaml
Runtime:
  listing_agent:
    decision_card:
      metric_enrichment:
        reference_platforms: [binance]
        depth_tier_pct: 0.001
        stale_after: 30m
      market_status_refresh:
        enabled: true
        source_platforms_only: true
        include_edgex: true
        per_source_timeout: 1500ms
        total_timeout: 3s
        max_concurrency: 2
        max_requests_per_tick: 12
        cache_ttl: 30s
        fallback_to_snapshot: true
```

## 回调：`POST /api/listing/callback`

`backend/internal/api/listing_callback.go` 的 `listingCallback` handler 是回调链路的总入口。安全栏分四层、按以下顺序判定，任一层失败均回 4xx：

```text
1. HMAC-SHA256 验签           ← 防伪造
       ↓
2. ±MaxClockSkew 时间窗       ← 防重放（默认 300s，与 spec §5 一致）
       ↓
3. operator open_id 白名单    ← 防越权（仅运营 open_id 列表能下决策）
       ↓
4. body 解析 + candidate_id 校验
       ↓
5. InsertDecision (INSERT IGNORE on uk_listing_decision_idempotency)
       ↓
6. DispatchDecisionAction（仅在 inserted==true 且 dispatcher!=nil 时跑）
```

### 签名

签名算法与 spec §5 锁定：

```text
mac = HMAC_SHA256(key=Secret, message=request_timestamp || Secret)
sig = base64(mac.Sum())
```

Header：

| Header | 说明 |
|---|---|
| `X-Lark-Request-Timestamp` | unix epoch 秒，整数字符串 |
| `X-Lark-Signature` | base64 编码的 HMAC |

**why "timestamp || Secret"**：这是 Lark v2 webhook 双向消息的官方契约——运营端把 Lark 透传的 timestamp 给我们的服务，我们用同一个 secret 重算 mac 比对。换其它算法（如 HMAC over body）会让本地预览（`scripts/lark-push-smoke`）和真实回调走两份签名实现，维护成本翻倍。

### 时钟窗

`MaxClockSkew` 默认 300s，对 `|now - request_timestamp|` 双向校验。设到 5 分钟是经验值：

- Lark 推回调常见延迟 < 30s，5 分钟有 10× 余量；
- 运营手机时钟漂移最严重的情况（出差跨时区刚切 SIM）也极少超过 1 分钟；
- 把窗口收到 60s 时，曾观察到正常点击被拒（手机休眠后 Lark 客户端补发延迟）。

### 白名单

`OperatorAllow []string` 是 open_id 数组；空列表语义为「**所有人都不放行**」，所以默认值 nil 让 callback 路由 stay-503（见下方「配置」）。生产部署必须填入运营组真实 open_id 才能解锁这条路由。

**why open_id 而不是 email**：Lark webhook 推回调时载荷里只有 open_id 是稳定主键；email / nickname 都可被运营自己改。

### 幂等

`InsertDecision` 用：

```sql
INSERT IGNORE INTO t_listing_decision (...)
VALUES (...)
-- 撞 uk_listing_decision_idempotency(candidate_id, action, callback_ts) 时
-- LastInsertId 返回 0，函数 SELECT 已有行的 id 返回 (id, inserted=false)
```

`callback_ts` 在 handler 里 **truncate 到秒**——同一次 Lark click 在 1 秒内重传不会落两行。这与 `request_timestamp` 的秒级精度对齐。

返回体（HTTP 200）：

| 字段 | inserted=true（首次） | inserted=false（重传） |
|---|---|---|
| `status` | `"recorded"` | `"already_recorded"` |
| `decision_id` | 新插入行 id | 已有行 id |
| `action` | `value.action` | 同上 |
| `candidate` | `value.candidate_id` | 同上 |
| `dispatch_id` | `>0`（首次时调度成功）| ❌ |
| `watchlist_id` | `>0`（仅 enter_watchlist）| ❌ |
| `outbox_rows` | `1`（prepare_listing / contact_mm）/ `0`（enter_watchlist / ignore）| ❌ |

**why "always 200"**：Lark webhook 对 non-2xx 会标记回调失败并重试，运营群里会出现红叉；幂等情况下回 200 + `status=already_recorded` 才是友好的；前端 / Lark dashboard 看到的体验一致。

## 动作分发表

`DispatchDecisionAction(ctx, repo, dec, cand, deps)`（`backend/internal/listing/action_dispatch.go`）按 `dec.Action` 四路路由：

| Action | t_listing_action_dispatch | 其它写 | rationale |
|---|---|---|---|
| `prepare_listing` | `dispatch_type=listing_ops`, `target_channel=lark_listing_ops`, `status=pending` | 一条 outbox 行（`event_type=listing_action_listing_ops`） | 进入"准备上架" SOP；listing-ops 群单独收一张卡，与原决策卡分离便于 follow-up |
| `enter_watchlist` | `dispatch_type=watchlist`, `target_channel=internal` | UpsertWatchlist (ON DUP KEY on `uk_listing_watchlist_candidate`) | 静默自助操作；不发卡，仅记入 watchlist 表 |
| `contact_mm` | `dispatch_type=mm`, `target_channel=lark_mm` | 一条 outbox 行（`event_type=listing_action_contact_mm`） | MM 团队单独 channel；与 listing-ops 异步推进 |
| `ignore` | `dispatch_type=ignore`, `target_channel=internal` | 无 | 仅写审计行——`ProduceDecisionCards` 的冷却闸**唯一**消费者就是这行 |

`t_listing_action_dispatch` 是 INSERT-only：`status` 起始 `pending`，下游 worker（如 listing-ops 群机器人确认）翻成 `completed / failed`；不做事务回滚，调用方按 `DispatchResult` 自行判断哪一侧落地了。

**why watchlist 单独一张表（不复用 candidate）**：watchlist 是"运营关注"的下游视图，可以独立打标 / 加备注，与候选的 lifecycle 解耦。`UpsertWatchlist` 用 candidate_id 做唯一键——重复点击"进入观察"会刷新备注而不是报错。

### `RepoDispatcher`：API ↔ listing 包的桥

`api` 包不依赖 `repo.GetCandidate`——`RepoDispatcher` 是 `listing.NewRepoDispatcher(repo, nowFn)` 返回的小适配器，实现 `api.DecisionDispatcher` 接口：

```go
func (d *RepoDispatcher) DispatchDecision(ctx context.Context, dec DecisionRecord) (DispatchResult, error) {
    cand, err := d.Repo.GetCandidate(ctx, dec.CandidateID)
    if err != nil { return DispatchResult{}, ... }
    return DispatchDecisionAction(ctx, d.Repo, dec, cand, DispatchDeps{Now: d.Now})
}
```

候选删除（极端：candidate id 被运维手工删）会让 `DispatchDecision` 返回 500——`InsertDecision` 已经落了 decision 行，但下游分发失败，运营会看到回调返回错误。这是有意的：失败可见 > 静默 no-op。

## 配置（Runtime.listing_agent.decision_card）

```yaml
Runtime:
  listing_agent:
    decision_card:
      enabled: false              # ★ 默认关闭，新部署必须显式开启
      ignore_cooldown: 24h        # spec §5 的冷却窗
      max_per_tick: 20            # 单 tick 决策卡上限
      callback:
        secret_env: ""            # 推荐：从 env 注入，never persist
        max_clock_skew: 5m        # ±300s
        operator_allow: []        # ★ 空数组语义为「全部拒绝」，必须填入运营 open_id
```

**配置优先级**（与 hot-gap webhook 一致）：

```text
Callback.Secret 非空              → 直接用 yaml 里的 secret（一般测试场景）
Callback.Secret 为空 + SecretEnv  → os.Getenv(SecretEnv)（生产推荐）
都空                              → /api/listing/callback 返回 503
```

`decision_card.enabled = false`（默认）时：
- engine `RunOnce` Step 2d 不执行，不写 risk plan / outbox；
- `/api/listing/callback` 路由仍由 cmd/ops-intelligence 注册，但 `decisions` writer 没装配时返回 503——双重 disable。

**why 默认关闭**：Phase 2 改动既影响数据写入（4 张新表）也直接对运营群推送，新部署需要显式 opt-in；上线节奏由运营 + DevOps 共同决定。

## 表设计与状态机

涉及的 4 张表均在 migration `000010_listing_agent_phase2.sql`（已在 Phase 0 落库）：

| 表 | 主要列 | 唯一键 | 来源 |
|---|---|---|---|
| `t_listing_risk_plan` | `candidate_id`, `recommendation`, `template`, `version`, `params_json` | `(candidate_id, version, created_at)` | `UpsertRiskPlan`（INSERT-only） |
| `t_listing_decision` | `candidate_id`, `risk_plan_id`, `action`, `operator_open_id`, `reason`, `callback_ts` | `uk_listing_decision_idempotency(candidate_id, action, callback_ts)` | `InsertDecision`（INSERT IGNORE） |
| `t_listing_action_dispatch` | `candidate_id`, `decision_id`, `dispatch_type`, `target_channel`, `status`, `payload_json` | `(decision_id, dispatch_type)` | `InsertActionDispatch`（INSERT-only） |
| `t_listing_watchlist` | `candidate_id`, `canonical_symbol`, `market_surface`, `instrument_kind`, `watch_status`, `watch_reason`, `source_decision_id`, `watch_started_at`, `payload_json` | `uk_listing_watchlist_candidate(candidate_id)` | `UpsertWatchlist`（ON DUP KEY UPDATE） |

`t_listing_delivery_outbox` 是 Phase 1 已有表，Phase 2 新增三个 `event_type` 枚举值：

```text
listing_decision_candidate       ← Step 2d 产出，目标 channel: lark_listing_decision
listing_action_listing_ops       ← prepare_listing 分发，目标 channel: lark_listing_ops
listing_action_contact_mm        ← contact_mm 分发，目标 channel: lark_mm
```

state machine 与 hot-gap 完全一致（pending → retry → sent / failed / disabled），见 `listing-agent-top30-hot-gap-push.md` §"表设计与状态机"。

## 验证

### 单测（`internal/listing/` + `internal/api/`）

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/backend
go test ./internal/listing/ ./internal/api/ -count=1
```

关键测试用例（Phase 2 共 25 个）：

- **风险计划（6）**：`TestBuildRiskPlanFromCandidatePrepareListing` / `EvaluateListing` / `HoldWatch`，`TestRepositoryUpsertRiskPlanInsertsAuditRow`，`TestRepositoryLatestRiskPlanByCandidate{ReturnsLatest, ReturnsNilOnEmpty}` —— 模板矩阵 + INSERT-only 审计 + 空时 nil-on-empty
- **决策卡片（6）**：`TestBuildDecisionCardEventEnforcesButtonMatrix{AnnouncementAndAPI, AnnouncementPendingAPIStripsPrepareListing}`，`TestRenderDecisionCardPostMessageProducesInteractiveEnvelope`，`TestProduceDecisionCards{SkipsAlreadyListed, SkipsRecentIgnoreCooldown, InsertsFreshRowsForActionableCandidates}` —— 按钮矩阵 + 渲染 envelope + 三种闸门
- **回调 API（5）**：`TestListingCallback{RejectsInvalidSignature, RejectsStaleTimestamp, RejectsNonWhitelistedOperator, RecordsValidPrepareListingClickAndTruncatesTimestamp, IsIdempotentOnDoubleClick}` —— 四层安全栏 + 秒级幂等
- **decision repo（2）**：`TestRepositoryInsertDecision{InsertsFreshRow, IsIdempotentOnDuplicateUniqueKey}` —— `uk_listing_decision_idempotency` 行为
- **action dispatch（6）**：`TestRepositoryInsertActionDispatchInsertsAuditRow`，`TestRepositoryUpsertWatchlistMakesEntryIdempotentByCandidate`，`TestDispatchDecisionAction{PrepareListingWritesAuditAndNotifies, EnterWatchlistWritesWatchlistRow, IgnoreOnlyWritesAudit, ContactMMNotifiesMMChannel}` —— 4 路路由表
- **引擎 wiring（1）**：`TestEngineRunOnceProducesDecisionCardsWhenEnabled` —— Step 2d 在 fusion + Top30 之后、delivery 之前执行；`DecisionCard.Enabled=false`（默认）时 5 个 legacy engine 测试不动

### E2E（`backend/e2e/`）

```bash
make e2e-listing
```

`TestListingAgentE2E_DecisionCallbackFlow`（3 个子用例）跑在 docker-compose 起的 MySQL 上：

| 子用例 | 验证 |
|---|---|
| `first prepare_listing click writes decision + dispatch + notify outbox` | 一次点击后：`t_listing_decision` × 1、`t_listing_action_dispatch` × 1（`dispatch_type=listing_ops`）、`t_listing_delivery_outbox WHERE event_type=listing_action_listing_ops AND target_channel=lark_listing_ops` × 1。response.status=`recorded` |
| `second identical click is idempotent` | 同样的 signed payload 再 POST 一次：response.status=`already_recorded`，所有计数仍为 1 |
| `ignore click suppresses next decision card via cooldown` | truncate + reseed 一个新候选；POST ignore；再跑一次 engine：`summary.DecisionCard.SkippedCooldown == 1 && OutboxRows == 0` |

签名工具在 e2e 里手写一次（`hmac.New(sha256.New, secret); mac.Write(ts); mac.Write(secret); base64(...)`），与生产 `computeCallbackSignature` 同算法。**算法换了的话此处会先 break**，是签名实现的回归闸。

### 本地预览（无破坏性）

Phase 2 没有独立的 preview CLI——决策卡片必须依赖真实候选，跑 `scripts/lark-push-smoke` 时把 `Runtime.listing_agent.decision_card.enabled=true` 临时打开，能在测试群里看到完整卡片（含按钮）。**注意**：smoke 工具不会启 callback 路由，按钮点击会 404；要端到端跑回调请用 `make e2e-listing`。

## 仍需注意 / 后续

- **cmd/ops-intelligence 接线**：当前 `backend/cmd/ops-intelligence/main.go` 还没把 `WithListingDecisionWriter` / `WithListingDispatch` / `WithListingCallback` 三个 Option 接入生产二进制；本分支只完成了 engine + API 库层。生产部署前必须补一次 cmd 接线，并把 `Callback.SecretEnv` 解析逻辑（模仿 `Top30WebhookURLEnv`）一起加上。
- **配置 overlay 测试**：`applyListingAgentFile` 还没有 `decision_card` block 的 YAML overlay test；下次接线前补一个，避免 yaml schema 默默漂移。
- **watchlist → dashboard 转移 worker**：spec §5 提到的「edgeX 已上线时 watchlist 自动出列」目前没有 worker；等 Phase 3 观察到第一例真上线 candidate 时再加。
- **回调路由部署 vs disabled**：cmd 接入后，`OperatorAllow` 空数组语义为「全部拒绝」——这是有意设计；DevOps 同学第一次部署千万记得填白名单，否则线上点击会一律 403。
- **secret 安全**：`Callback.Secret` 在 config struct 上打了 `json:"-"`，不会出现在 `/api/config` 返回里；`logf` / `log.Printf` 也都避免拼 secret。运维侧用环境变量注入。
- **dedupe key 与日期边界**：`listing_decision|<id>|YYYY-MM-DD` 用 UTC 日；运营在 UTC 0 时附近点击时，第二天可能因为冷却窗（24h 滚动）和 dedupe key（按日截断）的边界差异看到「卡片消失"」/「卡片重发"」一次。这是 spec 的语义选择——如果未来嫌别扭，把 dedupe key 也改成 24h 滚动窗即可。
