# Listing Agent Top30 Hot-Gap Lark 推送（CoinGecko Top30 → Outbox → 飞书 / Lark P3 群）

## 背景

`top30-share-history-backfill.md` 把 Top30 数据链路推进到 `t_top30_snapshot` 落库 + `suggested_action` 写入为止——但这只解决了 **看板**：运营要在多个 Tab 之间手动巡视才能注意到 `BEAT-USDT / LFI-USDT` 这种 5+ 平台 24h 高交易量、但 edgeX 还没上线的合约。Listing Agent P1 在此基础上闭环：

```text
Listing Agent instrument poll
    ↓ 写 t_listing_instrument_snapshot
RefreshListedUniverseFromSnapshots
    ↓ 写 listed_universe.runtime.yaml（seed listed_universe.yaml fallback）
CoinGecko collector
    ↓ runtime/seed universe loader 判断 edgex_listed
    ↓ 写 t_top30_snapshot.{edgex_listed, suggested_action}
listing engine (RunOnce, 默认 1 min cadence)
    ├── FuseSignals          (P1a 候选融合, 不在本 doc 范围)
    ├── ProduceTop30Push     (本 doc 主体)
    │     ↓ 写 t_listing_signal_observation + t_listing_delivery_outbox
    └── DrainDueOutbox
          ↓ POST Lark webhook
          ↓ 写 t_listing_delivery_attempt 审计行
```

任意 (symbol, action, snapshot_date) 三元组在同一天内只会产生 **一条** 卡片：`uk_listing_delivery_dedupe` 保证幂等。

参考设计文档：

- `../../../../architecture/方案设计/EdgeX运营/Listing/2026-05-27-Listing-Agent-P1-主链路方案设计.md`
- `../../../../architecture/方案设计/EdgeX运营/Listing/2026-05-27-Listing-Agent-P1-后端检测与Top30推送-实现计划.md`
- `listing-agent-dynamic-catalog-integration.md`（动态 instrument snapshot、DB-first CatalogResolver、runtime listed universe 的主文档）

历次主要提交：

```text
7229207 feat(edgex-ops-intelligence): wire Top30 hot-gap Lark push end-to-end
638e1cf feat(edgex-ops-intelligence): isolate Lark delivery HTTP client and harden audit upsert
```

本轮（卡片视觉 v2）在以上基础上把 `msg_type=post` 升级到 `msg_type=interactive`，加入分色 header / streak 徽章 / colored-bullet tier / Binance K 线副按钮，并修了 `DashboardURL` 一直为空的旧 bug——见下方「卡片渲染（interactive card v2）」与「Streak 计算」两节。

## 触发条件

`BuildTop30PushEvents`（`backend/internal/listing/top30_push.go`）只对同时满足以下条件的 `t_top30_snapshot` 行打包：

| 条件 | 说明 |
|---|---|
| `EdgexListed != nil && *EdgexListed == false` | **三态语义**：必须是"已知未上线"。`NULL`（status 未完成 / catalog 缺失）**绝不**触发。 |
| `SuggestedAction ∈ {"优先上架", "评估上架"}` | 看板里把 `suggested_action` 写成"持有观察" / "无需干预"等的不发卡 |
| 同一 `display_symbol + action` 在当天最新快照里 | 多平台行 fan-in 到一条卡片，platforms 数组按 rank 升序排列 |

聚合输出（`Top30PushEvent`）字段：

```go
Symbol       string                  // display_symbol, e.g. "BEAT-USDT (perp)"
Action       string                  // "优先上架" / "评估上架"
MaxCoverage  int                     // 此 symbol 在多少家平台同时进 Top30
Platforms    []Top30PlatformEvidence // 每条 {Platform, Rank, Volume24HUSD}
DashboardURL string                  // ProduceTop30Push 自动 buildDashboardSymbolURL(deps.DashboardBase, Symbol) 填入；dashboard_base 为空则留空（卡片按钮被抑制）
SnapshotDate string                  // "2026-05-28"
DedupeKey    string                  // "top30_hot_gap|<symbol>|<action>|<date>"
StreakDays   int                     // 1 = 🆕 NEW；>= 2 = "已第 N 天在榜"，由 countTop30Streak 计算（见下文「Streak 计算」）
TriggerTime  time.Time               // 触发时刻 UTC，写入 footer note；与 SnapshotDate 区分"快照时间 vs 告警发出时间"
```

Stale 守护：`ProduceTop30Push` 在 `now - latest > StaleAfter`（默认 30 min）时 fail-closed 返回 `FailClosed: "snapshot_stale"`，**不写** outbox。runtime/seed listed universe loader 都不可用时同样 fail-closed。

## edgex_listed 三态语义（不可塌缩）

CoinGecko collector 在 `backend/internal/collector/mysql_store.go` 写 `t_top30_snapshot` 时必须保留三态；其上游 `edgex_listed` 判断由 runtime/seed listed universe loader 提供，优先使用 Listing Agent 动态生成的 `listed_universe.runtime.yaml`，再回退 `listed_universe.yaml`：

```go
// edgexListedTinyInt 返回 sql 兼容的值：
//   StatusComplete + listed==true  → 1
//   StatusComplete + listed==false → 0
//   StatusComplete + listed==nil   → 1（防御性，理论上不会出现）
//   非 StatusComplete              → NULL（"未知" 状态保留）
//
// 直接 boolToTinyInt(listed) 会把 listed==false 写成 NULL，
// 上面 BuildTop30PushEvents 的 `EdgexListed != nil` 守卫就永远不触发。
func edgexListedTinyInt(listed *bool, listedStatus string) any { ... }
```

**红线**：任何重写 collector 写路径的人必须保留这个 helper 的 3 态语义。`mysql_store_test.go` 的 `TestEdgexListedTinyIntDistinguishesKnownFalseFromUnknown` 锁死了这条契约。

## 配置（Alert + Runtime.listing_agent.delivery）

本卡片的 `edgex_listed` 前置判断依赖动态 Catalog 链路：`Runtime.listing_agent.sources.instrument_diff` 刷新 `t_listing_instrument_snapshot`，`Runtime.listing_agent.listed_universe_refresh` 生成 `listed_universe.runtime.yaml`。本节只列 Lark delivery 相关字段；动态 catalog 配置见 `listing-agent-dynamic-catalog-integration.md`。注意动态 catalog poll loop 的 best-effort 语义：单条 signal insert 失败不应阻断 snapshot refresh，也不应直接让 Top30 hot-gap 误判 catalog 缺失。

webhook target 从 listing 模块**升至顶层** `Alert.WebHookP3`，与其它 EdgeX 服务的 Nacos 告警 contract 对齐：

```yaml
# config/edgex-ops-intelligence.yaml
Alert:
  AppName: edgex-liquidity-dashboard
  Enabled: true
  WebHookP3: "https://open.larksuite.com/open-apis/bot/v2/hook/<token>"  # 永不持久化到 MySQL / 日志

Runtime:
  listing_agent:
    enabled: true
    top30_push:
      poll_interval: 1m
      stale_after: 30m
    worker:
      max_attempts: 5
    delivery:
      enabled: true
      # 历史 fallback：webhook URL 也可以从 delivery 里读
      # （Alert.WebHookP3 优先级更高，此处通常留空）
      top30_webhook_url: ""
      top30_webhook_url_env: ""
      top30_webhook_secret: ""
      dashboard_base_url: ""
      # ★ 仅 Lark webhook 客户端使用的可选代理。**不要**升级为
      # 进程级 HTTPS_PROXY env，那会一并污染 9 家 native exchange
      # adapter 与 CoinGecko collector 的延迟测量。生产环境直连
      # open.larksuite.com 时留空即可。
      proxy: "http://host.docker.internal:7897"
```

`resolveWebhookURL(cfg)` 优先级：

```text
1. cfg.Alert.Enabled && cfg.Alert.WebHookP3 非空  → 用顶层 Alert（推荐）
2. Runtime.listing_agent.delivery.top30_webhook_url 非空  → 用模块字段
3. Runtime.listing_agent.delivery.top30_webhook_url_env 非空  → os.Getenv()
4. 都没配  → outbox row 写 status=disabled，不发网络请求
```

`resolveWebhookURL` 与 yaml/Alert 的契约由 `engine_test.go` 与 `e2e/listing_e2e_test.go` 同时锁定。

## Delivery HTTP client 与 per-feature proxy

`NewEngine`（`backend/internal/listing/engine.go`）按下面的规则装配 delivery 用的 `*http.Client`：

```go
if deps.HTTPClient == nil {
    proxy := strings.TrimSpace(cfg.Runtime.ListingAgent.Delivery.Proxy)
    client, err := buildDeliveryHTTPClient(proxy)
    if err != nil {
        log.Printf("listing engine: delivery proxy %q ignored: %v", proxy, err)
        client = http.DefaultClient
    } else if proxy != "" {
        log.Printf("listing engine: delivery http client routed through proxy %q", proxy)
    }
    deps.HTTPClient = client
}
```

`buildDeliveryHTTPClient` 在 proxy 非空时克隆 `http.DefaultTransport` 并装上 `http.ProxyURL(parsed)`，否则直接返回 `http.DefaultClient`。这把 **listing delivery 的代理彻底与 exchange/CoinGecko 代理隔离**：

| 出向 | 走的 client | 配置位置 |
|---|---|---|
| 9 家 native exchange REST | `adapter.newHTTPClient(timeout, proxy)` | `Runtime.exchange_proxy` |
| Lighter WS | `LighterWSProvider` 自带 dialer | `Runtime.exchange_proxy` |
| CoinGecko | `coingecko.Client` 自带 transport | `Runtime.coingecko.proxy` |
| **Listing Agent Lark webhook** | **`buildDeliveryHTTPClient(...)`** | **`Runtime.listing_agent.delivery.proxy`** |

为什么不复用 `Runtime.exchange_proxy`？因为它的语义是「让原生交易所 REST/WS 拨号走代理」，本意是绕过 GFW 拿到上游金融 API 的数据；如果把 Lark 也并进来，运维在生产部署时为了让 lark webhook 能拨通 SG/JP 直连环境就得绕一圈在 exchange_proxy 上做 NO_PROXY 白名单，复杂度溢出到 exchange 链路。每个 feature 独立 proxy 字段，default `""` = 直连，是最干净的边界。

**也不能用进程级 `HTTPS_PROXY` env**——见 `native-exchange-proxy.md` §"为什么不直接用 HTTPS_PROXY env"，作用域过广 + 配置不可见 + 与 yaml 双轨制。

## 表设计与状态机

### `t_listing_delivery_outbox`

| 列 | 用途 |
|---|---|
| `dedupe_key` | `top30_hot_gap\|<symbol>\|<action>\|<date>`；UNIQUE 保证同一天同一动作只发一次 |
| `target_channel` | 当前固定 `lark_top30`，未来扩展通道时枚举 |
| `status` | `pending` / `retry` / `sent` / `failed` / `disabled` |
| `attempt_count` | 已尝试次数；`>= max_attempts` 即转入 failed |
| `next_attempt_at` | retry 退避时间；`DrainDueOutbox` 只取 `next_attempt_at <= now` 的行 |
| `payload_json` | **渲染好的** Lark `msg_type=interactive` 卡片 body（v1 是 `msg_type=post`），而**不是** raw event JSON |

状态机：

```text
   ┌───────────┐                ┌──────┐
   │  pending  │ ─── 2xx ─────→ │ sent │
   └──────┬────┘                └──────┘
          │ non-2xx / network err
          ↓
   ┌───────────┐  attempts < max  ┌───────┐
   │   retry   │ ←──────────────  │ retry │
   └──────┬────┘                  └───────┘
          │ attempts >= max
          ↓
   ┌──────────┐
   │  failed  │
   └──────────┘
```

webhook URL 为空时所有 row 进 `disabled`，不产生网络调用——便于 smoke test 与 dry-run。

### `t_listing_delivery_attempt`

每次尝试一条审计行，`UNIQUE KEY (outbox_id, attempt_no)`。生产里 `attempt_no` 单调递增，**唯一键不会冲突**。但当运维手工重置 `attempt_count` 重灌某条卡时，新 attempt 可能复用旧 attempt_no → INSERT 撞 1062。

`recordAttempt`（`backend/internal/listing/delivery.go`）通过 `ON DUPLICATE KEY UPDATE` 做 upsert：

```sql
INSERT INTO t_listing_delivery_attempt (...)
VALUES (...)
ON DUPLICATE KEY UPDATE
  status = VALUES(status),
  http_status = VALUES(http_status),
  error_message = VALUES(error_message),
  attempted_at = VALUES(attempted_at),
  response_body = VALUES(response_body),
  latency_ms = VALUES(latency_ms);
```

语义：以最新一次 attempt 的结果覆盖旧记录，符合运维对"这是最新一次尝试"的预期。生产路径单调递增时该冲突分支根本不走，行为不变。

## 卡片渲染（interactive card v2）

`RenderTop30PostMessage(ev)` 生成 Lark `msg_type=interactive` body。卡片由四块组成：动作分色的 header、symbol H1 + streak 徽章的 headline 行、2×2 摘要字段、平台证据列表，最后是双按钮 + footer note：

```text
[Header: blue (评估上架) / red (优先上架)]
  📊 Top 30 热门标的 · 评估上架

# BEAT-USDT (perp)
<font color='blue'>🆕 NEW</font>           ← 或 <font color='grey'>已第 N 天在榜</font>

覆盖 5/9 平台         | 24h 合计 $590.04M
最强 okx #14          | edgeX 未上线
─────────────────────
<font color='red'>●</font>    **okx**     · rank **#14** · 24h $218.79M
<font color='orange'>●</font> **bitget**  · rank **#17** · 24h $48.42M
<font color='orange'>●</font> **bybit**   · rank **#19** · 24h $73.77M
<font color='orange'>●</font> **gate**    · rank **#19** · 24h $21.83M
<font color='grey'>●</font>   **binance** · rank **#28** · 24h $223.85M
─────────────────────
[📊 查看 Top30 详情] (primary, 蓝色填充)
[📈 Binance K 线]   (default, 仅当 binance 在 platforms 里时渲染)

触发时间 2026-05-28 09:25 UTC · top30_hot_gap|BEAT-USDT (perp)|评估上架|2026-05-28
```

设计要点：

| 元素 | 实现 | rationale |
|---|---|---|
| Header 颜色 | `header.template = "red"`（优先上架）/ `"blue"`（评估上架）/ 兜底 `"grey"` | 同一群同时来多张卡时优先级一眼可分；`top30HeaderTemplate(action)` 集中映射 |
| Header 标题 | 稳定的分类标题 `📊 Top 30 热门标的 · {action}`，**不**塞 symbol | 群里多卡刷屏时 header 不应每张都不一样；symbol 下移到 body |
| Symbol H1 | lark_md `# {display_symbol}` | 比 `**bold**` 在 Lark 桌面端字号更大，比 `<font size>` 跨端更稳 |
| Streak 徽章 | `<font color='blue'>🆕 NEW</font>` / `<font color='grey'>已第 N 天在榜</font>` 单独成行 | 区分新热点 vs 持续在榜，避免运营对老告警脱敏 |
| 2×2 摘要字段 | `div.fields` 四个 `is_short=true` 的 lark_md（`覆盖 / 24h 合计 / 最强 / edgeX 状态`） | 不看明细也能秒判 |
| Tier 圆点 | `<font color='red\|orange\|grey'>●</font>`（`rankTierBullet(rank)`：≤10 红，≤20 橙，>20 灰） | **不能用 emoji**——见下方 Schema gotchas；圆点颜色本身就承载了 tier 语义，无需额外文字标记 |
| 平台名加粗 | lark_md `**` | 视觉锚点，不依赖 monospace 字体 |
| 主按钮 | `type="primary"`，文案 `📊 查看 Top30 详情`，URL = `buildDashboardSymbolURL(base, symbol)` | 点击直达对应 token 的 dashboard 视图 |
| 副按钮 | `type="default"`，文案 `📈 <交易所> K 线`，URL 由 `chooseTop30KlineButton(ev)` 按下面的优先级选 | 与正文「最强 X #N」语义一致；详见下方「K 线副按钮目标交易所选择」 |
| Footer note | `触发时间 ... · {dedupe_key}` | 区分"快照时间"（`SnapshotDate`）vs"告警发出时间"（`TriggerTime`）；dedupe key 便于运维查 outbox |

整数 / 美元数量级 helper `humanUSD` 不变，自动按 B / M / K 缩进。

### K 线副按钮目标交易所选择

`chooseTop30KlineButton(ev) (label, url string)`（`backend/internal/listing/top30_push.go`）三层优先级：

1. **binance 在 platforms 里 → 跳 Binance K 线**（哪怕 binance 在该 symbol 上是边缘平台 rank ≥ 25）。理由：Binance 是行业默认参考价格源，URL 模板最稳定；即便边缘，运营看 K 线时仍习惯先看 binance；不引入"按钮跟卡片正文最强平台联动"的额外认知成本。
2. **binance 不在 platforms → 按 rank 升序遍历，找第一个 URL 模板已知的平台**（即"最强且模板已知"那一家）。这一情况下按钮与卡片正文里 `最强 <X> #N` 摘要语义闭环。
3. **都没模板（极端：platforms 只剩 lighter 这种没公开 K 线页的）→ 兜底跳 Binance**。Binance 不在 Top30 ≠ Binance 没上线，URL 通常仍有效；接受少量 404 风险换"始终有按钮"的体验。

#### 9 家交易所 URL 模板（`buildExchangeKlineURL`）

每家 K 线 URL 的 canonical pattern（已通过对应交易所 SEO 索引页 + curl/web search 验证；以 BTC-USDT perp 为例）：

| 平台 | URL 模板 | 注 |
|---|---|---|
| binance | `https://www.binance.com/en/futures/<BASEQUOTE>` | 大写 base+quote 拼接，无分隔符 |
| okx | `https://www.okx.com/trade-swap/<base>-<quote>-swap` | **小写**，带 `-swap` 后缀 |
| bybit | `https://www.bybit.com/trade/usdt/<BASEQUOTE>` | 大写 base+quote 拼接 |
| bitget | `https://www.bitget.com/futures/usdt/<BASEQUOTE>` | 大写 base+quote 拼接，无 `_UMCBL` 后缀（旧版需要） |
| gate | `https://www.gate.com/futures/USDT/<BASE>_<QUOTE>` | **`gate.com`** 不是 `gate.io`；下划线分隔 |
| mexc | `https://www.mexc.com/futures/<BASE>_<QUOTE>` | 统一域名（`futures.mexc.com` 会 redirect 到这里） |
| bingx | `https://bingx.com/en/perpetual/<BASE>-<QUOTE>` | dash 分隔 |
| hyperliquid | `https://app.hyperliquid.xyz/trade/<BASE>` | 单 base symbol，无 quote |
| lighter | （无） | 没有公开 K 线页，触发优先级第 3 层兜底 |

显示名映射 `exchangeDisplayName`：`binance→Binance / okx→OKX / bybit→Bybit / bitget→Bitget / gate→Gate / mexc→MEXC / bingx→BingX / hyperliquid→Hyperliquid / lighter→Lighter`。

> **维护提醒**：交易所改 URL 是常事（如 bitget 历史上 perp 页面带过 `_UMCBL` 后缀，gate 从 `.io` 迁到 `.com`）。每次发现某家点击 404，先去交易所搜某热门 perp 验证 canonical URL，再单家更新 `buildExchangeKlineURL` 即可——不需要改其它 8 家。

注意 `ProduceTop30Push` 把渲染后的 body 写进 **outbox.payload_json**，但同时把**结构化** `Top30PushEvent` JSON 写进 `t_listing_signal_observation.payload_json` —— 下游消费方（候选融合、人工审核）应当读 signal 表的结构化 JSON，**而不是** outbox 的渲染产物。

新 signal family 的 `fingerprint` 应采用短前缀 + sha256 payload 的幂等键形态，避免把多个 hash / symbol / subtype 直接拼成长明文 key。`t_listing_signal_observation.fingerprint` 当前是 `VARCHAR(160)`，但 `instrument_diff:<sha256>` / `announcement_listing:<sha256>` 这类 80-85 字符级 key 才是推荐契约；旧明文 key 超过 `VARCHAR(96)` 曾触发过 `INSERT IGNORE` silent-drop。

### Schema gotchas（踩坑记录）

**Lark interactive card 的 `plain_text` 元素字段名是 `content`，不是 `text`**。我们最初按直觉写成 `text`，结果：

- Lark webhook 仍返回 `code=0` "ok"——**没有 schema 报错**；
- 但 `header.title.text` 内部那个 `text` 字段会被静默丢弃 → 整个彩色 header bar 完全不渲染；
- `button.text.text` 同样被丢弃 → 按钮渲染成空白圆角矩形；
- `note.elements[]` 同样被丢弃 → footer note 失踪。

要点：**Lark 200 OK ≠ 卡片渲染对**，必须人眼对照群里的实际显示验证。

修复后所有 `plain_text` / `lark_md` 子元素一律用：

```json
{"tag": "plain_text", "content": "..."}
{"tag": "lark_md",    "content": "..."}
```

容易混淆的对照（`text` 在 Lark 的 schema 里**只是 button / div 等容器上指向子元素的 property name**，不是那个子元素的字段名）：

| 位置 | 正确写法 |
|---|---|
| `header.title` | `{tag:"plain_text", content:"..."}` |
| `button.text`（property） 的值 | `{tag:"plain_text", content:"..."}` |
| `note.elements[]` 的元素 | `{tag:"plain_text", content:"..."}` |
| `div.text`（property）的值 | `{tag:"lark_md", content:"..."}` |

**字体渲染坑**：Lark 桌面端**不用** Apple/Google 彩色 emoji 字体，常用 unicode emoji 渲染参差不齐：

| Codepoint | 现象 | 替代方案 |
|---|---|---|
| ⭐ U+2B50 | 桌面端橙色钻石/方块 | `<font color='red'>●</font>` |
| 🔸 U+1F538 | 桌面端橙色钻石/方块 | `<font color='orange'>●</font>` |
| ⚠ / ⚠️ | 桌面端可能渲染成纯黑三角，无色 | 用灰色圆点 `<font color='grey'>●</font>` 表达 tier 即可，无需额外文字标记 |

经验法则：**tier / status / severity 类视觉标识用 lark_md `<font color>` 而不是 emoji**。emoji 留给真正"是 emoji 才合适"的语义符号（🔥 火热、📊 仪表盘、📈 K 线、🆕 新）——这些在 Lark 桌面端渲染良好。

### JSON encoding：禁用 HTML escape

`RenderTop30PostMessage` 用 `json.Encoder.SetEscapeHTML(false)` 让 `<font color='red'>` 这类 lark_md 标签直接以可读字节落到 wire 上，而不是 `\u003cfont color='red'\u003e`。两种 Lark 都能正确解析（JSON 标准要求两种 unicode escape 等价），但未 escape 的形式：

- 在 outbox 表 `payload_json` 列里肉眼可读，便于 SQL 排查；
- 与 `top30-preview --dry-run` 印到 stdout 的形式逐字节相同；
- delivery 日志一眼能看出渲染了什么。

切回默认（HTML escape）也工作，只是 audit 体验差。

## Streak 计算（NEW vs 已第 N 天在榜）

`countTop30Streak(ctx, displaySymbol, action, today)`（`backend/internal/listing/top30_push.go`）反查 `t_listing_signal_observation`，计算同一 (display_symbol, signal_subtype=action) 在**严格早于 today** 的 UTC 日期上的连续命中天数：

```sql
SELECT DISTINCT DATE(observed_at) AS d
  FROM t_listing_signal_observation
 WHERE signal_type    = 'top30_hot_gap'
   AND signal_subtype = ?  -- ev.Action
   AND display_symbol = ?  -- ev.Symbol
   AND DATE(observed_at) < ?
 ORDER BY d DESC
 LIMIT 60;
```

Go 层从 `today - 1 day` 倒序往前对齐：第一天对不上就 break。结果 `streak` = 截至昨天为止的连续天数；`ProduceTop30Push` 把 `ev.StreakDays = streak + 1` 注入 event（+1 代表"今天这次推送"是第几天），render 时映射成徽章：

| `StreakDays` | 徽章 |
|---|---|
| 1 | `🆕 NEW`（蓝色）|
| ≥ 2 | `已第 N 天在榜`（灰色）|

**为什么按 (symbol, action) 分别计算 streak，而不是按 symbol 全局**？

因为本工作流里 `优先上架` 与 `评估上架` 是两个**不同强度**的告警 lane。如果 BSB 昨天 `优先上架`、今天降级到 `评估上架`：

- 按 (symbol, action) 算：今天的"评估上架"序列 streak = 1 → 显示 NEW；
- 按 symbol 全局算：今天 streak = 2 → 显示"已第 2 天在榜"。

按 lane 分开更符合"评估上架"是一种**新告警**的产品语义；如果未来想合并视角，把 `signal_subtype = ?` 改成 `signal_subtype IN ('优先上架','评估上架')` 即可。

**时序关系**：streak 查询发生在 `InsertSignal(ctx, signal)` 之前，所以"今天的这一行"还没落库——这是 `< today` 而不是 `<= today` 的原因。如果 streak 查询失败（DB 异常），`ProduceTop30Push` 静默把 `StreakDays = 1`，徽章退化为 NEW，**不 fail-closed 整条告警**——徽章是 UX 锦上添花，不是必要信号。

这里的 streak best-effort 只影响徽章展示；它与 instrument / announcement poll loop 的 best-effort 是两层容错。后者保证单个 signal 写入异常不会让整轮 `t_listing_instrument_snapshot.last_seen_at` 停滞，从而保护 runtime listed universe 和 Top30 enrichment。

## 验证

### 单测

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/backend
go test ./internal/collector/ ./internal/listing/ ./internal/config/ -count=1
```

关键测试用例：

- `TestEdgexListedTinyIntDistinguishesKnownFalseFromUnknown` — 3 态契约
- `TestBuildDeliveryHTTPClientWiresProxyOnlyWhenConfigured` — proxy 仅在配置时装载
- `TestDrainDueOutboxMarksSentOn2xx` / `DisablesWhenNoWebhook` / `AttachesLarkSignWhenSecretSet`
- `TestRenderTop30PostMessageProducesInteractiveCard` — 卡片 msg_type=interactive，header / fields / actions / footer 结构齐全
- `TestRenderTop30PostMessageActionPicksHeaderTemplate` — `优先上架→red` / `评估上架→blue` / 默认 `grey`
- `TestRenderTop30PostMessageStreakBadgeFormatting` — `StreakDays=1` 渲染 NEW，`>=2` 渲染"已第 N 天在榜"
- `TestRenderTop30PostMessageTierBullet` — colored-bullet 分级（红/橙/灰），并锁死"`(边缘)` 文字标记不再出现"
- `TestRenderTop30PostMessageOmitsPrimaryButtonWithoutDashboardURL` — 配置无 `dashboard_base_url` 时主按钮抑制；副 K 线按钮仍渲染
- `TestBuildDashboardSymbolURLAppendsQuery` — `dashboard_base + ?symbol=` 拼接（含 base 已带 query 的边界情形）
- `TestSplitDisplaySymbol` — 解析 `BEAT-USDT (perp)` / `ETH/USDC` / 单 token / 仅分隔符等边界
- `TestBuildExchangeKlineURLAllPlatforms` — 9 家交易所 URL 模板逐家锁定 + lighter / 未知平台返回空
- `TestChooseTop30KlineButtonPrefersBinance` — binance 在 platforms 时一律选 binance（即便 rank 边缘）
- `TestChooseTop30KlineButtonPicksStrongestNonBinanceWithTemplate` — binance 缺席时按 rank 升序找第一个有模板的平台
- `TestChooseTop30KlineButtonSkipsTemplatelessAndFallsThrough` — lighter 这种无模板平台被跳过，继续往下找
- `TestChooseTop30KlineButtonFallsBackToBinanceWhenNoTemplate` — 全员无模板时兜底跳 Binance
- `TestChooseTop30KlineButtonReturnsEmptyOnUnparseableSymbol` — symbol 无法解析时副按钮整个不渲染
- `TestCountTop30Streak{WalksConsecutivePriorDays, ReturnsZeroWhenNoHistory, StopsImmediatelyWhenYesterdayMissing}` — streak 反查的三种场景

### E2E

```bash
go test ./e2e/ -run TestListingAgentE2E_FullPipeline -count=1
```

`backend/e2e/listing_e2e_test.go` 用 `httptest.Server` 模拟 Lark webhook，端到端验证：

- `Alert.WebHookP3` 路径被采用；
- outbox row 从 pending → sent；
- webhook 收到的 body 必须是 `application/json` 且 `msg_type=interactive`。

### 本地容器端到端

前提：`config/edgex-ops-intelligence.yaml` 里 `Alert.Enabled=true` + 真实 `WebHookP3` URL；本地 stack 通常额外需要 `Runtime.listing_agent.delivery.proxy=http://host.docker.internal:7897`（macOS Docker 默认）。

```bash
# 1. 重建镜像（用 docker-compose.local.yaml 走预编译 arm64 binary 避免 proxy.golang.org TLS）
cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/ops-intelligence ./cmd/ops-intelligence
cd ../deploy
docker compose --project-name deploy \
  -f docker-compose.yaml -f docker-compose.local.yaml \
  build backend
docker compose --project-name deploy \
  -f docker-compose.yaml -f docker-compose.local.yaml \
  up -d backend

# 2. 容器自身不应有进程级 HTTPS_PROXY
docker exec deploy-backend-1 printenv | grep -i proxy
# 期望：HTTPS_PROXY= / HTTP_PROXY= 都为空

# 3. boot 日志应当出现：
docker logs deploy-backend-1 | grep "delivery http client routed through proxy"
# 期望：listing engine: delivery http client routed through proxy "http://host.docker.internal:7897"

# 4. 等一个 collection 周期后查看 outbox：
docker exec deploy-mysql-1 mysql -uroot -proot edgex_ops_intelligence -e \
  "SELECT id, status, attempt_count, sent_at FROM t_listing_delivery_outbox"
# 期望：status=sent, sent_at 非空
```

### 非侵入式预览：`scripts/top30-preview`

调试卡片视觉 / 临时 demo 时不要用 redrive——会污染 outbox dedupe，并和运行中的 `deploy-backend-1` listing engine 抢同一行。改用专门的预览 CLI：

```bash
cd backend

# Dry-run：用 yaml config 作为 webhook / proxy / dashboard 的默认源，
# 印到 stdout，0 网络副作用。--mysql-dsn 仍需显式传（DSN 通常不进 yaml）。
go run ./scripts/top30-preview \
  --config-dir=../config \
  --mysql-dsn="root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true&loc=UTC&charset=utf8mb4" \
  --dry-run --limit=3

# Live preview：从 yaml 读 Alert.WebHookP3 + Runtime.listing_agent.delivery.proxy
# / dashboard_base_url，所以一份 yaml 就能同时驱动生产 engine 和预览 CLI。
go run ./scripts/top30-preview \
  --config-dir=../config \
  --mysql-dsn="root:root@tcp(127.0.0.1:3306)/edgex_ops_intelligence?parseTime=true&loc=UTC&charset=utf8mb4" \
  --limit=2

# 想覆盖某项配置时，所有字段都接受 flag 显式 override：
go run ./scripts/top30-preview \
  --config-dir=../config \
  --mysql-dsn="..." \
  --webhook-url="https://open.larksuite.com/open-apis/bot/v2/hook/<test-token>" \
  --proxy="" \
  --dashboard-base=""
```

实现要点（`backend/scripts/top30-preview/main.go`）：

- 读最新 UTC 日的 `t_top30_snapshot` 行，按 `BuildTop30PushEvents` 同样的逻辑聚合；
- 复用 `countTop30Streak` 计算 streak（**直接查 signal 表，不写**）；
- 用生产代码 `RenderTop30PostMessage` 渲染——保证 stdout 和真发出去的 wire 字节一致；
- **不写** `t_listing_delivery_outbox` / `t_listing_signal_observation`，所以可以和运行中的容器并跑而不冲突；
- **`--config-dir` 共享生产 yaml**：webhook（`Alert.WebHookP3` 优先，回退到 `Runtime.listing_agent.delivery.top30_webhook_url[_env]`）、proxy（`Runtime.listing_agent.delivery.proxy`）、dashboard base（`Runtime.listing_agent.delivery.dashboard_base_url`）、DSN（`Database.DSN`）四个字段在 flag 缺省时都会从 yaml 取——单一事实源，避免预览和生产配置漂移；
- **`host.docker.internal` 自动改写**：yaml 里 proxy 通常写成 `http://host.docker.internal:7897`（容器内可用），从宿主 shell 跑预览时 CLI 会自动把它重写成 `http://127.0.0.1:7897`，不需要分别维护两份。

### Redrive 一条卡片

如果群里被吃了或要测代理重连（注意：会改 outbox 行的 `attempt_count`）：

```sql
UPDATE t_listing_delivery_outbox
   SET status='retry',
       next_attempt_at=UTC_TIMESTAMP()
 WHERE id=<row_id>;
```

下一个 engine tick 会重新拨号；新的 attempt_no 写入 `t_listing_delivery_attempt`（与历史 attempt_no 冲突会被 `ON DUPLICATE KEY UPDATE` 吸收）。

## 仍需注意

- **webhook URL / secret 永不落 MySQL / 日志**。`uk_listing_delivery_dedupe` 锁的是 `dedupe_key`，`target_channel` 只存 `lark_top30` 字符串。所有 `*_test.go` 也避免把 URL 写进 fixture，用 `httptest.Server.URL` 注入。
- **proxy 红线**：`proxy: ""` 是生产默认。本地 Docker 写 `host.docker.internal:7897`；线上 SG/JP 直连环境**必须**留空。改 yaml 后必须重启进程，proxy 在 `NewEngine` 装载时一次性拍板。
- **stale_after 默认 30m**，超出会 fail-closed。CoinGecko collector 5 min 周期下基本不可能触发，但运维若刻意停 collector 做演练时要心里有数。
- Top30 dashboard URL：`ProduceTop30Push` 现在自动 `buildDashboardSymbolURL(deps.DashboardBase, ev.Symbol)` 拼出 `?symbol=<display_symbol>`，**无须** caller 显式 set。但 `dashboard_base_url` 仍空时 `DashboardURL == ""`，`buildTop30ActionRow` 会**抑制整个 actions row**（避免渲染 0 个按钮的空白行）——所以"没配 base url 卡片就没按钮"是符合预期的，不是 bug。线上需要按钮时填入对应 web base URL 即可，无须改代码。
- **multi-channel 扩展**：当前 `target_channel` 只有 `lark_top30`；引入第二个 channel（如 telegram / 企微）时建议复用同一 outbox 表，新增 `target_channel` 枚举 + delivery worker 分发，但**不要**在 outbox 行的 `payload_json` 里塞多份渲染——应当根据 `target_channel` 在 delivery worker 渲染前从 signal 表的结构化 payload 重新拼。
