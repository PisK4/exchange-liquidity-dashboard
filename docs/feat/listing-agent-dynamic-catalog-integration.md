# Listing Agent Dynamic Catalog Integration

## 背景

早期 Dashboard 的 catalog 依赖 `make catalog` 生成的静态文件：

- `config/instrument_catalog.yaml`：少量核心交易对的原生 API symbol / URL / contract metadata；
- `config/listed_universe.yaml`：各平台已上市 base asset 集合；
- `backend/docs/raw-instruments/`：`build-catalog` 抓取的 per-platform raw instrument dump。

Listing Agent Catalog 动态化后，运行时主路径已经变成：**instrument poll → DB snapshot → DB-first CatalogResolver → runtime listed universe → Top30 / backfill / decision / alert 消费**。静态文件仍保留，但定位变为 seed / fallback / audit，而不是唯一真值来源。

---

## 总体数据流

```text
Runtime.listing_agent.sources.instrument_diff
    ↓ BuildListingSources
RunInstrumentPoll
    ↓ upsert
 t_listing_instrument_snapshot
    ├── instrument.Diff → t_listing_signal_observation → FuseSignals → t_listing_candidate
    ├── CatalogResolver SnapshotReader → Top30Backfiller / native history backfill
    └── RefreshListedUniverseFromSnapshots
            ↓ atomic write
       listed_universe.runtime.yaml
            ↓ runtime/seed universe loader
       CoinGecko Top30 enrichment / divergence / liquidity alert / candidate reconcile
```

关键原则：

1. **DB snapshot 是动态 catalog 主路径**：正常运行时，resolver 和 listed-universe refresh 都优先消费 `t_listing_instrument_snapshot`。
2. **静态 catalog 是安全网**：`instrument_catalog.yaml`、`listed_universe.yaml`、`raw-instruments` 继续服务冷启动、DB 不可用、审计回放和少量 convention-only 平台。
3. **edgeX source 只做 snapshot**：edgeX 已上市集合用于 `edgex_listed` enrichment 和 candidate lifecycle reconcile，不应把 edgeX 自己的 instrument 变化重新喂进 listing candidate loop。

---

## Instrument poll 契约

实现入口：

- `backend/internal/listing/fetcher/build.go::BuildListingSources`
- `backend/internal/listing/instrument_poll.go::RunInstrumentPoll`
- `backend/internal/listing/instrument/*.go`
- `backend/internal/listing/repository.go::{UpsertInstrumentSnapshot, LatestInstrumentSnapshotByKey, HasInstrumentBaseline}`

每个 source 有两个维度：

| 维度 | 说明 |
|---|---|
| `platform` / `market_type` | 唯一定位一个交易所 instrument feed，如 `gate/usdt_futures`、`lighter/perp`、`edgeX/perp_v2`。 |
| `signaling_mode` | `full` 会 diff 并发信号；`snapshot_only` 只刷新 DB snapshot，不进入候选融合。 |

`RunInstrumentPoll` 的行为：

1. 没有 baseline 时只写 snapshot，不发历史信号。
2. 有 baseline 时对每个 instrument 做 diff，产生 `new_symbol` / `status_changed` / `delisted` / `listing_time_changed` / `metadata_changed` 等 observation。
3. `snapshot_only` source 跳过 diff，只 upsert snapshot。
4. 单个异常 symbol soft-fail，不应中断同轮其它 snapshot upsert。
5. signal 写入失败同样 best-effort：`LatestInstrumentSnapshotByKey` / `InsertSignal` / `UpsertInstrumentSnapshot` 的单 symbol 失败只记录日志并继续处理后续 symbol，避免一个 poisoned signal 让整个平台 `last_seen_at` 停滞。
6. `NormalizerVersion` 不一致时触发 rollover guard：本 tick 仍 upsert 新 snapshot，但跳过 diff，避免 hash 配方升级造成一次性误报洪峰。

## Candidate fusion target gate

`FuseSignals` 只把真正可作为 edgeX listing opportunity 的资产提升为 `t_listing_candidate`。在 subtype gate（`new_symbol` / `listing_time_changed` / `status_changed→active|pre_listing`）之后，还有一层 target-asset gate：

- 若 `canonical_symbol` 或 `base_asset` 属于稳定币、quote asset 或 collateral asset 集合，则 signal 只作为 observation-only 记录并标记 `fused_at`，不 upsert candidate。
- 当前集合包括 `USDC`、`USDT`、`USD1`、`USD`、`FDUSD`、`TUSD`、`BUSD`、`DAI`、`PYUSD`、`USDE` 等 USD-pegged / collateral 类资产。
- 典型事故案例：Gate spot `USDC_USD1` 会被 normalizer 抽成 `canonical_symbol=USDC`、`base_asset=USDC`、`quote_asset=USD1`，但它只是稳定币兑换对，不是需要运营处理的新 listing 标的，因此不应进入 candidate / decision-card 链路。

这层 gate 不影响 snapshot、DB-first CatalogResolver 或 runtime listed universe；它只限制 Listing Agent 的候选生成。

---

## StableHash 与 normalizer version

`StableHash` 不是 `sha256(raw_json)`。它只覆盖 schema-stable 投影字段：

- 平台、market type、API symbol / market id；
- canonical/base/quote/settle；
- market surface、instrument kind、contract type；
- status raw / normalized / field name；
- delist flag；
- listing time 与字段名；
- sorted per-platform `StableHashExtras`。

噪声字段必须排除：mark price、funding rate、open interest、daily volume、fee re-quote、price-derived minimum quantity 等都不能触发 `metadata_changed`。

配方升级必须 bump `NormalizerVersion`。否则旧 snapshot 的 hash 与新配方 hash 在同一版本下比较，会绕过 rollover guard 并产生误报。

### Signal fingerprint schema

`t_listing_signal_observation.fingerprint` 是 signal 幂等键，不再拼接长明文字段：

| signal_type | fingerprint 形态 | 长度级别 |
|---|---|---:|
| `instrument_diff` | `instrument_diff:<sha256(payload)>` | 80 chars |
| `announcement_listing` | `announcement_listing:<sha256(payload)>` | 85 chars |

历史明文 `instrument_diff` fingerprint 曾把 `prev_hash/new_hash/platform/symbol/subtype` 直接拼接，两个 64 位 hex hash 加上前缀后可超过旧 schema 的 `VARCHAR(96)`。在 MySQL `INSERT IGNORE` 路径下，这类过长值可能被降级为 warning 并 silent-drop，随后 fallback `SELECT id WHERE fingerprint=?` 返回 `sql.ErrNoRows`，表象会误导成“resolve signal id 失败”。

当前防线是三层：

1. 新 fingerprint 使用 sha256 前缀格式，稳定落在 160 chars 内；
2. schema 已放宽到 `fingerprint VARCHAR(160)`；
3. `InsertSignal` 对 `RowsAffected=0 + SELECT miss` 返回 typed `ErrSignalSilentFail`，暴露 `signal_type/source_platform/fingerprint_len` 供排障。

已知平台特例：

| 平台 | extras / 注意事项 |
|---|---|
| Gate futures | `quanto_multiplier` 是历史成交量换算需要的 contract size，保留。 |
| Lighter | `market_id` 等基础字段在 neutral projection 已覆盖；daily/open-interest 类字段排除。 |
| BingX swap | `contractId` / `pricePrecision` / `quantityPrecision` / `size` 保留；`tradeMinQuantity` 排除，因为它由当前价格推导，会随 mark price 抖动。 |
| EdgeX | `contractId` / `baseCoinId` / `quoteCoinId` / `settleCoinId` / `tickSize` / `stepSize` 保留。 |
| OKX / UnixMillis source | `listing_time_ts` 会按秒 round，以匹配 MySQL `TIMESTAMP` 无小数精度的 round-trip 行为。 |

---

## DB-first CatalogResolver

实现入口：

- `backend/internal/collector/catalog_resolver.go`
- `backend/cmd/ops-intelligence/snapshot_reader.go`
- `backend/cmd/ops-intelligence/main.go::NewCatalogResolverWithDB`

`CatalogResolver` 的职责是把 `(platform, canonical)` 解析成 native `domain.SymbolSub`，供 Top30 native history backfill 等链路使用。

运行时优先级：

1. 对 DB-first 平台，先读 `t_listing_instrument_snapshot` 的最新 active canonical instrument。
2. DB 无可用记录时，回退 `backend/docs/raw-instruments/`。
3. 对 convention-only 平台，继续使用平台命名规则和静态 fallback。
4. 找不到 symbol 时返回 `ErrSymbolUnsupported`，调用方必须 skip，不能伪造数据。

DB-first 主要解决的问题：

- 长尾 symbol 不再依赖人工周期性 `make catalog`；
- Gate / Lighter / EdgeX 这类必须依赖 raw instrument metadata 的平台可从 DB snapshot 获取最新 contract metadata；
- Top30 roster 变化后，backfill 能更快解析到新 symbol。

---

## Runtime listed universe

实现入口：

- `backend/internal/listing/listed_universe_refresh.go::RefreshListedUniverseFromSnapshots`
- `backend/cmd/ops-intelligence/main.go::buildUniverseLoader`
- `config/edgex-ops-intelligence.yaml::Runtime.listing_agent.listed_universe_refresh`

`RefreshListedUniverseFromSnapshots` 会从 active instrument snapshot 派生 runtime universe，并原子写入 `listed_universe.runtime.yaml`。runtime universe 的新鲜度只依赖 `t_listing_instrument_snapshot.last_seen_at` 是否落在 `fresh_window` 内；本次修复后，单条 signal 写入失败不应再阻断 snapshot upsert，因此不应再因为一个 fingerprint / constraint 问题导致整个平台 universe 被误清空。

静态 seed 与 runtime universe 的语义不同：`listed_universe.yaml` 是历史 / 全市场 / 人工审计 seed，适合冷启动和故障 fallback；runtime universe 是当前 DB snapshot 中新鲜且 active 的可交易集合。MEXC、Hyperliquid、edgeX 等平台的 seed 会显著宽于当前 active perp surface，因此 refresh 不再长期拿 DB count 与 seed count 做唯一 shrink 判据。

当前 shrink 保护分三层：

1. 若上一轮 `listing/listed_universe/<platform>` 成功写入了 `source_context_json`，优先使用其中的 `db_fresh_active_count` 作为 previous-success baseline；当前 DB count 低于 `shrink_floor * previous_success` 时 fail-closed 并回退 seed。
2. 没有 previous-success baseline 时，默认平台仍使用 `seed_shrink_floor` 对比 seed count，保留原有冷启动安全网。
3. 对 seed 已知过宽的平台，可在 `platform_overrides` 配 `bootstrap_baseline: db_first` 和 `bootstrap_min_count`；首次 DB count 达到最小值即接受 DB runtime，随后转入 previous-success baseline 保护。

| 配置 | 作用 |
|---|---|
| `seed_path` | 静态 seed，通常是 `config/listed_universe.yaml`。 |
| `runtime_path` | 运行时生成文件，优先被 loader 消费。 |
| `fresh_window` | DB snapshot 新鲜度窗口，过期记录不参与 universe。 |
| `seed_shrink_floor` | DB 派生集合低于 seed 数量阈值时回退 seed，避免上游故障把 universe 大面积清空。 |
| `covered_platforms` | 允许由 DB 动态覆盖的平台；seed-only 平台会 pass-through。 |
| `platform_overrides` | 平台级 bootstrap 策略；`edgeX` / `hyperliquid` / `mexc` 使用 `db_first` + `bootstrap_min_count`，避免过宽 seed 造成误报。 |

每次 refresh 都会写入 synthetic source-health 行 `listing/listed_universe/<platform>`。`source_context_json` 中保留本轮决策证据，包括 `baseline_type`、`db_fresh_active_count`、`previous_success_db_fresh_active_count`、`seed_count`、`threshold`、`surface_counts`、`seed_only_sample` 和 `db_only_sample`，用于区分真实 runtime shrink 与 seed/runtime 语义差异。

loader 行为：

```text
listed_universe.runtime.yaml 可读且非空 → 使用 runtime
否则 → fallback listed_universe.yaml
否则 → edgeX listed unknown / fail-closed
```

候选 reconcile：

- refresh 会按 edgeX perp/spot DB-derived base 集合调用 `BulkMarkCandidatesAlreadyListed`；
- 命中 edgeX 的候选会被标记为 `already_listed_on_edgex`，后续 decision card 跳过；
- 只在 Binance/Gate/BingX 等竞品平台上市的 token 不会因此关闭，它们仍是潜在 listing opportunity。

---

## 对下游功能的影响

| 下游 | 影响 |
|---|---|
| Top30 enrichment | `edgex_listed` 优先由 runtime universe 判断；runtime 不可用才退回 seed。 |
| Top30Backfiller | symbol 解析优先读 DB snapshot，降低长尾 `symbol_unsupported`。 |
| Decision card | `already_listed_on_edgex` 可由 edgeX snapshot reconcile 自动关闭；stablecoin / quote / collateral 旧候选也会被 producer gate 跳过。 |
| Divergence push | 三态 `EdgexListed` 来源仍来自 Top30 snapshot，但 Top30 snapshot 现在受 runtime universe 影响。 |
| Liquidity alert | `Compute(matrix, universe, ...)` 的 universe 应理解为 runtime/seed loader 的结果。 |
| Raw-instruments | 从主路径降级为 fallback / cold-start / audit source。 |

---

## 运维排障

### 1. runtime universe 是否生成

```sql
SELECT source_key, status, last_error, updated_at
FROM t_listing_source_state
WHERE source_type='listed_universe_refresh'
ORDER BY updated_at DESC;
```

同时检查 runtime 文件：

```bash
ls -lh config/listed_universe.runtime.yaml
```

Docker 部署中如果设置了 `OPS_INTELLIGENCE_DATA_DIR`，runtime 文件会写到该目录下，而不是 seed config 目录。

### 2. source 是否正常刷新

```sql
SELECT platform, market_type, COUNT(*) AS n, MAX(last_seen_at) AS last_seen
FROM t_listing_instrument_snapshot
GROUP BY platform, market_type
ORDER BY platform, market_type;
```

如果某平台 `last_seen_at` 超过 `fresh_window`，它不会进入 runtime universe。

### 3. shrink floor 触发

现象：runtime universe 某平台突然大幅减少但 seed 正常。

排查：

```sql
SELECT *
FROM t_listing_source_state
WHERE source_key LIKE 'listing/listed_universe/%'
ORDER BY updated_at DESC;
```

`last_error` 包含 `listed_universe shrink_floor triggered` 时，表示系统已按 seed floor 自动回退 seed；包含 `listed_universe runtime shrink triggered` 时，表示当前 DB count 低于上一轮成功 DB baseline。进一步查看 `source_context_json` 可确认 `baseline_type`、`threshold`、`surface_counts` 和样本差异。

### 4. signal insert silent-fail / fingerprint overflow

现象：source health 里出现 `resolve signal id for fingerprint: sql: no rows in result set`，或日志里出现 `ErrSignalSilentFail` / `insert signal ... continuing tick`。

排查：

```sql
SELECT CHARACTER_MAXIMUM_LENGTH
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_NAME='t_listing_signal_observation'
  AND COLUMN_NAME='fingerprint';
```

期望值为 `160`。如果仍是 `96`，说明启动时 schema post-init 没跑到或连接的不是目标库。继续检查：

```sql
SELECT source_platform, signal_type, signal_subtype, LENGTH(fingerprint) AS fingerprint_len, observed_at
FROM t_listing_signal_observation
ORDER BY id DESC
LIMIT 20;
```

新的 `instrument_diff` fingerprint 应为 `instrument_diff:` 前缀 + 64 位 sha256 hex。即使 signal insert 因其它约束失败，instrument poll 也应继续 upsert snapshot；用 §2 的 `MAX(last_seen_at)` 确认平台 freshness 是否仍在推进。

### 5. metadata_changed 噪声

正常情况下，market price / funding / OI / daily volume 抖动不应产生 candidate。

排查：

```sql
SELECT source_platform, signal_subtype, COUNT(*) AS n, MIN(observed_at), MAX(observed_at)
FROM t_listing_signal_observation
WHERE signal_type='instrument_diff'
  AND observed_at > NOW() - INTERVAL 30 MINUTE
GROUP BY source_platform, signal_subtype
ORDER BY n DESC;
```

若 `metadata_changed` 集中爆发，优先检查最近是否改动 `StableHashExtras` 或忘记 bump `NormalizerVersion`。

### 6. listing_time_changed 噪声

Unix millisecond source 的 `ListingTimeTS` 必须 round 到秒，以匹配 MySQL `TIMESTAMP` round-trip。若 OKX 等平台每轮出现 `listing_time_changed`，检查 normalizer 是否绕过了 `nowFromUnixMillis`。

---

## 验证入口

推荐命令：

```bash
cd backend
go test ./internal/listing/... -count=1
go test ./internal/collector/... -count=1
```

关键测试索引：

- `backend/internal/listing/instrument/stable_hash_test.go`
- `backend/internal/listing/instrument_poll_test.go`
- `backend/internal/listing/listed_universe_refresh_test.go`
- `backend/internal/listing/listed_universe_refresh_repo_test.go`
- `backend/internal/collector/catalog_resolver*_test.go`
- `backend/e2e/listing_e2e_test.go`

---

## 与旧静态 catalog 的关系

| 对象 | 现在定位 |
|---|---|
| `config/instrument_catalog.yaml` | Dashboard 核心 symbol seed、URL 验证、legacy/static fallback。 |
| `config/listed_universe.yaml` | runtime universe 的 seed / fallback。 |
| `backend/docs/raw-instruments/` | DB 不可用或冷启动时的 resolver fallback；同时保留原始上游 schema 审计价值。 |
| `make catalog` | 仍用于维护 seed 和 raw dumps，但不再是运行时发现长尾 instrument 的唯一入口。 |
