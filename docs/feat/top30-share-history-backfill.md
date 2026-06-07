# Top30、Share 历史窗口与 Backfill

## 背景

早期正式版前端只稳定展示 24h 平台市占率，7d/30d、趋势、Top30 以及单币种 7d share 都以 `unsupported` 保留结构。本轮实现把这些能力接入真实数据链路：

- CoinGecko `/derivatives` 提供平台级 24h 成交量、OI、per-symbol 24h 成交量与 Top30 roster；
- 后端按 UTC day 存储 platform / symbol 维度 daily aggregate；
- native daily kline backfill 补齐 Top30 7d Vol、7d Δ 和单币种历史窗口；
- 前端按 `complete` / `partial` / `insufficient_history` 展示历史覆盖度。

## CoinGecko 派生成交量

核心实现：

- `backend/internal/collector/coingecko_collector.go`
- `backend/internal/collector/store.go`
- `backend/internal/marketdata/coingecko/*`

每次 CoinGecko pull 会写入三类数据：

| 数据 | 存储用途 | API 使用 |
|---|---|---|
| 平台级 24h volume / OI | `SaveCoinGeckoPlatformVolumes` | Share 24h 分母与平台排名 |
| 平台级 daily aggregate | `SaveDailyVolumeAggregates` | Share 7d / 30d / trend |
| symbol 级 daily aggregate | `SaveDailyVolumeAggregates` | Liquidity 单币种 7d share / WoW、Top30 7d 窗口 |

口径约束：

- Share 24h 的 10 家平台统一使用 CoinGecko 平台级聚合，避免和原生 ticker 混用导致分母双计。
- MEXC `0.4`、Gate `0.5` 折算只在查询 Share / Volume 时应用，存储层保留 raw USD。
- 缺失或异常平台只输出状态，不输出 `$0` 伪成交量。

## Share 7d / 30d / Trend

核心实现：

- `shareHistoricalLocked(window, days)`
- `shareTrendLocked(days)`
- `historicalShareStatusLocked()`

窗口状态：

| 状态 | 含义 |
|---|---|
| `complete` | 所有平台均覆盖完整窗口 |
| `partial` | 至少存在部分可用历史，但窗口未满 |
| `insufficient_history` | 当前窗口没有可用 daily rows |

API 字段：

- `rows[].days_seen`
- `rows[].days_window`
- `rows[].adjusted_volume_total_usd`
- `rows[].share_pct`
- `trend.points[].share_24h_pct`
- `trend.points[].share_7d_pct`
- `trend.points[].share_30d_pct`
- `trend.points[].platforms_covered`

前端效果：

- 24h / 7d / 30d 切换不再固定落入 unsupported；
- 历史不足时仍展示结构和状态，避免误认为完整窗口；
- 30d trend 可以在已有 daily aggregate 的天数上部分绘制。

## 单币种 7d Share 与 10min Spread

Liquidity KPI 已补齐：

- `symbol_share_7d_pct`
- `symbol_share_7d_status`
- `symbol_share_wow_status`
- `edgex_spread_10m_bp`
- `edgex_spread_10m_status`

计算口径：

- 单币种 7d share 来自 `(platform, display_symbol)` daily aggregate；
- WoW 状态需要当前 7d 与前一 7d 的覆盖度；
- edgeX 10min spread 均值来自最近 10 分钟 `platformHistory` 中可展示且 spread > 0 的样本。

## Top30 生成与 enrichment

核心实现：

- `buildCompetitorCoverage`
- `enrichTop30Rows`
- `Store.SaveTop30`
- `Store.Top30`

生成流程：

1. CoinGecko ticker 先按 `(platform, normalized_symbol)` 聚合，选择成交量最高的 ticker；
2. 每个平台按 `volume_24h_usd` 降序取 Top30；
3. 通过 runtime/seed listed universe loader 判断 edgeX 是否已上线该 base asset：优先读 `listed_universe.runtime.yaml`，失败时回退 `listed_universe.yaml`；
4. 统计竞品 Top30 覆盖数；
5. 推导 `suggested_action` 与对应状态；
6. 写入 store / MySQL，并由 `/api/snapshot/top30` 输出。

> 动态 Catalog 集成后，`listed_universe.runtime.yaml` 由 Listing Agent 的 `RefreshListedUniverseFromSnapshots` 从 `t_listing_instrument_snapshot` 生成；静态 `listed_universe.yaml` 是 seed / fallback。完整链路见 `listing-agent-dynamic-catalog-integration.md`。instrument poll 现在是 best-effort：单条 signal insert 失败不会阻断 snapshot upsert，因此 Top30 enrichment 更应关注 `last_seen_at` freshness，而不是把某条 signal 写入失败直接等同于 catalog 缺失。

字段状态：

- `volume_24h_usd`：CoinGecko 当前快照；
- `volume_7d_usd` / `delta_7d_pct`：由历史 daily aggregate 派生；
- `edgex_listed_status`：runtime/seed universe 都不可用时降级；
- `competitor_top30_coverage_status`：coverage 可计算时为 complete；
- `suggested_action_status`：缺少 runtime/seed listed universe 时为 `insufficient_history`。

## SymbolBackfiller

实现位置：

- `backend/internal/collector/symbol_backfill.go`

用途：

- 对 V1 配置 symbol（当前 BTC/ETH/SOL）拉取 native daily volume history；
- 启动后延迟 2 分钟执行一次，之后每天 UTC 02:00 执行；
- 让单币种 7d share / WoW 不必等待 CoinGecko live row 自然积累 7 天；
- live row 与 CoinGecko row 对同一 slot 有更高优先级，backfill 幂等可覆盖缺口但不会抢占新数据。

## Top30Backfiller

实现位置：

- `backend/internal/collector/top30_backfill.go`
- `backend/internal/collector/catalog_resolver.go`
- `backend/internal/adapter/history.go`
- `backend/cmd/ops-intelligence/snapshot_reader.go`

Catalog 动态化后，`CatalogResolver` 运行时优先通过 `SnapshotReader` 读取 `t_listing_instrument_snapshot`，再回退 `backend/docs/raw-instruments/` 与平台命名 convention。`raw-instruments` 不再是长尾 symbol 解析的唯一来源，而是冷启动、DB 异常和审计回放的安全网。因为 instrument poll 会在单个 signal 写入失败时继续刷新 snapshot，DB-first resolver 的可用性主要由 fetch/source health 和 snapshot freshness 决定，而不是由 `t_listing_signal_observation` 是否成功落某一条 diff 决定。

用途：

- 对每个平台当前 Top30 roster 的 base asset 拉取 native daily kline；
- 用于补齐 Top30 7d Vol / 7d Δ；
- 支持启动后延迟 90 秒 cold-start、每天 UTC `02:30` repair、以及 CoinGecko roster 变化后的 incremental backfill。

并发与限速：

```yaml
backfill:
  enabled: true
  cold_start_days: 14
  daily_repair_days: 3
  per_platform_concurrency: 3
  per_platform_rate_per_sec: 4
  schedule_utc_hour: 2
  schedule_utc_minute: 30
```

跳过与观测：

- `symbol_unsupported`：DB-first resolver、raw dump fallback 与平台 convention 均无法解析该 `(platform, base)`；
- `instrument_not_found`：上游明确返回 instrument 不存在；
- `fetch_failure`：临时请求失败；
- `partial_days`：返回天数不足。

这些 skip reason 会记录在 `Top30BackfillSkipCounts`，便于排查长尾 symbol 缺口。

## 原生采集并发 / 限速

正式采集也增加了平台维度并发与限速，避免多 symbol 扩展后把某个交易所打满：

```yaml
collection:
  per_platform_concurrency: 3
  per_platform_rate_per_sec: 4
```

行为：

- 每个平台独立 semaphore；
- 每个平台独立 rate limiter；
- 单个 `(platform, canonical)` 连续失败可进入 cooldown；
- 单平台失败不阻断其它平台数据写入。

## 仍需注意

- Top30 24h ranking 依赖 CoinGecko ticker 覆盖；某平台无 CoinGecko 数据时仍返回 `unsupported`。
- 7d / 30d / delta 字段取决于 daily aggregate 覆盖度，不能把 `partial` 当成完整窗口。
- Listing Agent instrument poll 正常时，DB snapshot 会减少长尾 symbol 的 `symbol_unsupported`；但 `raw-instruments` 仍需作为冷启动 / DB 异常 / schema 审计 fallback 维护。
- `listed_universe.runtime.yaml` 由运行时刷新生成，seed `listed_universe.yaml` 不应被运行时覆盖；若 runtime 文件缺失或 shrink-floor 触发，系统会回退 seed。
- 如果 Top30 `edgex_listed_status` 突然退化或 runtime universe 某平台缩水，先查 `t_listing_source_state`、`t_listing_instrument_snapshot.last_seen_at` 和 Listing Agent 日志里的 `ErrSignalSilentFail` / `insert signal ... continuing tick`，再判断是否需要刷新静态 catalog。
- 当前 backfill 窗口默认服务 V1 运营视图，扩大 symbol universe 前需评估交易所免费额度。

## 验证命令

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/backend
make ci
make smoke-api PORT=18080 SYMBOL=BTC-USDT
```
