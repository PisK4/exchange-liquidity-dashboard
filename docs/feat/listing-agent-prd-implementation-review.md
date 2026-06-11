# Listing Agent 原需求与当前实现 Review

> 本文沉淀对 Listing Agent 原始需求、实现方案与当前代码实现进度的对比结论。对比来源包括：
>
> - 原始需求文档：`architecture/方案设计/EdgeX运营/原需求/Listing Agent.md`
> - 实现方案：`/Users/pis/.factory/specs/2026-05-29-listing-agent.md`
> - 当前进度：`repos/edgex-ops-intelligence/docs/plan/progress.md` 第 11 章 `Listing Agent (post-V1.0.0)`

## 1. 总体结论

Listing Agent 的后端决策链路已经基本建成，并且在原始 PRD 之外扩展出了多类运营告警能力。当前实现已经不是 demo 级别，而是接近生产可运营的后端系统：具备多源采集、信号入库、候选融合、评分推荐、风险方案、Lark 卡片、callback 鉴权、动作分发、watchlist 写入、outbox 投递、source health、Top30 热门缺口、CEX/DEX divergence 与 Dashboard 流动性告警等能力。

但如果严格按原始 PRD 的最终验收口径，仍有若干闭环型能力未完全满足：

1. 6 家平台的公告/页面监测尚未全部覆盖。
2. 风险参数方案仍缺少 funding 初始参数、显式 `MaxPositionUSD` 字段落地，以及 MEXC + Bitget 场景的专门保守规则表达。
3. `准备上线` 的多群分发链路与原 PRD 的“流动性群 + 运营群 + 设计群”存在差异。
4. 竞品领先交易对独立榜单页面未看到完整落地证据。
5. watchlist 自动转入 Dashboard 核心监控的闭环未看到完整落地证据。
6. 真实 Lark 按钮点击闭环尚需用生产或准生产 callback 做一次完整验证。
7. 动态 Catalog 已接入运行时链路：instrument snapshot、DB-first CatalogResolver、runtime listed universe、StableHash 噪声抑制、normalizer rollover guard、sha256 signal fingerprint 与 best-effort poll loop 已成为 Listing Agent 的生产契约。
8. Symbol identity normalization 已成为运行时契约：`config/symbol_mapping.yaml` alias 会在 instrument / announcement / fusion / decision-card refresh 路径生效，保留交易所原生 `api_symbol` / `base_asset`，同时用 `canonical_symbol + market_surface + instrument_kind` 表达业务身份。

因此，当前判断是：**后端核心链路完成度高，原需求主干基本匹配；但原 PRD 的完整运营闭环仍未完全验收通过。**

## 2. 原始 PRD 的核心功能清单

原始 Listing Agent 需求本质上是一个“新永续上币预警 + 上币准备”系统，核心目标不是展示 Dashboard，而是帮助运营及时发现竞品新合约机会并触发后续执行动作。

| 模块 | 原始需求 |
|---|---|
| 新合约/公告监测 | 监控 Binance、Bybit、OKX、Bitget、MEXC、Hyperliquid 的新永续上币信号。 |
| 信号融合 | 按 token / platform 聚合多个平台信号，判断是否构成候选。 |
| 评分推荐 | 按 Binance、Bybit、OKX、Hyperliquid、MEXC、Bitget 组合规则给出分数与建议。 |
| 风险参数方案 | 自动生成 leverage、funding 初始参数等上币准备建议。 |
| Lark 决策卡片 | 推送包含 token、source、time、market status、risk params、score、recommendation 的卡片。 |
| 四个按钮 | `准备上线`、`进入观察`、`联系 MM`、`忽略`。 |
| 动作后续链路 | watchlist 写入、MM 群通知、上币相关群通知、忽略原因记录。 |
| 竞品领先榜单 | 提供独立页面，展示竞品已上、edgeX 未上的交易对榜单和标签。 |
| Dashboard 复用 | 复用 Dashboard 已有行情、深度、成交量、合约规格等能力。 |
| 自动转核心监控 | watchlist 新币在 edgeX 上线后自动进入 Dashboard 核心监控。 |

## 3. 当前实现完成度概览

当前实现相较原始 PRD 有明显扩展。按实现进度与代码链路看，已经完成的主干能力包括：

| 能力 | 当前状态 | 说明 |
|---|---|---|
| API instrument diff / snapshot | 已完成并扩展 | 原 PRD 6 家（Binance、Bybit、OKX、Bitget、MEXC、Hyperliquid）之外，已扩展 BingX、Gate、Lighter，并接入 edgeX perp/spot `snapshot_only` source，用于动态 catalog / universe。 |
| 公告采集 | 部分完成 | 已覆盖 Binance、Bybit、Bitget；OKX、MEXC、Hyperliquid 公告源未看到完整注册。 |
| 冷启动与配方升级保护 | 已完成 | instrument / announcement poll 具备 cold-start baseline；StableHash + NormalizerVersion rollover guard 避免 metadata 噪声与 hash 配方升级误报。 |
| 信号入库 | 已完成 | `t_listing_signal_observation` 支持 fingerprint 幂等；新 `instrument_diff` / `announcement_listing` 使用 sha256-prefixed compact fingerprint，schema 放宽到 `VARCHAR(160)`，并用 `ErrSignalSilentFail` 区分 `INSERT IGNORE` silent-drop 与普通 duplicate。 |
| 候选融合 | 已完成 | `FuseSignals` 支持 fail-closed、edgeX listed universe 检查、候选信号过滤，并将 stablecoin / quote / collateral assets 作为 observation-only，避免 USDC_USD1 等兑换对进入候选。 |
| 评分推荐 | 已完成 | 核心评分规则基本匹配原始 PRD，并增加 announcement-only 的 pre-assessment 保护。 |
| 风险方案 | 部分完成 | 已有 risk plan 版本化和 leverage tiers，但 funding 初始参数等细节不足。 |
| Lark 决策卡片 | 已完成 | 具备 interactive card、按钮矩阵、risk plan 绑定、dedupe key。 |
| Lark callback | 已完成 | 具备 HMAC、timestamp skew、operator allowlist、幂等、dispatch。 |
| 四按钮 dispatch | 大部分完成 | watchlist、MM outbox、ignore audit 已有；prepare_listing 多群分发仍不完全等价于 PRD。 |
| Delivery outbox | 已完成 | 支持 retry、disabled、dedupe、per-feature webhook resolve。 |
| Source health | 已完成 | 可追踪来源健康状态。 |
| Top30 hot-gap | 额外完成 | 原 PRD 外扩展能力。 |
| CEX/DEX divergence | 额外完成 | 原 PRD 外扩展能力。 |
| 流动性告警 | 额外完成 | Dashboard liquidity-lag / worst-depth 告警。 |
| Symbol identity normalization | 已完成并需持续维护 | Runtime alias 归一已接入核心 Listing Agent 路径，`synthetic_futures` 不再被 listed_universe 排除，Lark decision card 展示业务身份而不是交易所原生 base。 |

## 4. 需求匹配度评分

| 模块 | 匹配度 | 结论 |
|---|---:|---|
| 6 家 API 新合约监测 | 9/10 | 主体已完成，仍受个别平台网络/TLS/Cloudflare 稳定性影响。 |
| 6 家公告/页面监测 | 5/10 | 已有 3 家，距离 PRD 的 6 家仍有缺口。 |
| Candidate fusion | 9/10 | 融合逻辑、fail-closed、edgeX listed 检查较完整。 |
| Scoring / recommendation | 9/10 | 分值规则基本匹配，并补了 announcement-only 保护。 |
| Risk plan | 6.5/10 | 有 leverage 模板，但 funding / max position / MEXC+Bitget 显式保守规则不足。 |
| Lark decision card | 8.5/10 | 卡片和按钮已具备，字段完整性仍需按 PRD 逐项验收。 |
| Callback security / idempotency | 9/10 | 安全与幂等设计较完整，需真实 Lark callback 验证。 |
| 4 按钮后续链路 | 7/10 | 主要动作有实现，但 prepare listing 多群分发与 PRD 不完全一致。 |
| Watchlist 写入 | 8/10 | enter_watchlist 已 upsert watchlist。 |
| Watchlist 自动转 Dashboard | 2/10 | schema 预留字段存在，但未看到完整自动转入核心监控闭环。 |
| 竞品领先榜单独立页面 | 2-4/10 | Top30 / divergence 有数据与推送能力，但独立页面与 7 榜单未看到完整证据。 |
| Dashboard 数据复用 | 8/10 | 已复用 Top30、depth、runtime listed universe，并通过 DB-first CatalogResolver 把 Listing Agent instrument snapshot 反哺 native backfill。 |
| 额外运营信号能力 | 9/10 | Top30 hot-gap、divergence、liquidity alert 明显超出原始 PRD。 |
| 生产数据干净度 | 9/10 | metadata_changed 误报已通过 fusion subtype gate、StableHash 投影、BingX/OKX 残余修复和 rollover guard 验证；fingerprint overflow / `INSERT IGNORE` silent-drop 类事故已通过 schema 放宽、sha256 compact fingerprint、typed diagnostic 与 best-effort poll loop 缓解；stablecoin / quote / collateral assets 已通过 fusion observation-only gate 和 decision-card producer gate 降噪。其它 `instrument_diff_only` record_only 弱信号仍需继续明确单发 / 静默策略。 |

## 5. 主要功能缺口

### 5.1 公告源覆盖不完整

原始 PRD 要求 6 家平台都能监测新上币公告或页面变化。当前实现的 announcement source 主要覆盖：

| 平台 | 当前公告覆盖 |
|---|---|
| Binance | 已覆盖 |
| Bybit | 已覆盖 |
| Bitget | 已覆盖 |
| OKX | 未看到完整注册 |
| MEXC | 未看到完整注册 |
| Hyperliquid | 未看到完整注册 |

这不影响 API instrument diff 主链路，但会影响“提前预警”的时效性。

### 5.2 风险参数方案仍偏模板化

当前 `RiskPlan` 已能按 recommendation 生成不同 leverage 模板：

- `prepare_listing`：50x cap，要求 MM quote。
- `watch`：20x conservative。
- `pre_assessment`：不生成具体 leverage。
- `record_only`：记录为主。

但与 PRD 相比仍缺少：

1. funding initial parameters 的实际填充。
2. `MaxPositionUSD` 的显式字段赋值。
3. MEXC + Bitget 场景的专门保守风险说明，而不是仅通过 score=60 -> watch -> 20x 间接体现。
4. 更细的 market status / liquidity evidence 进入 risk plan 的解释链路。

### 5.3 `准备上线` 的多群分发与 PRD 不完全一致

原 PRD 期望 `准备上线` 至少触达：

- 流动性群
- 运营群
- 设计群

当前 dispatch 更接近：

- `prepare_listing` -> `lark_listing_ops`
- `contact_mm` -> `lark_mm`
- `enter_watchlist` -> internal watchlist
- `ignore` -> audit only

这可以满足最小闭环，但与 PRD 的多团队协同还不完全等价。

### 5.4 竞品领先榜单页面未看到完整验收证据

当前 Top30 hot-gap 和 CEX/DEX divergence 能推送运营卡片，也能从数据上发现热门缺口。但原 PRD 要的是独立页面，且包含多类榜单、标签与 cross-judgment。现有证据不足以确认该页面完整落地。

### 5.5 Watchlist 自动转 Dashboard 核心监控未闭环

`t_listing_watchlist` 中已经预留：

- `edgex_listed_at`
- `transferred_to_dashboard_at`

但目前未看到明确的 job 或 service 负责：

1. 定期检查 watchlist token 是否已在 edgeX 上线。
2. 自动加入 Dashboard core-pair / listed universe / monitored universe。
3. 写回 `transferred_to_dashboard_at`。

因此该验收项暂时只能认为未完成或未证实。

### 5.6 生产按钮闭环仍需真实验证

进度中 `t_listing_decision = 0`，说明虽然 callback 与 dispatch 代码存在，但真实运营按钮点击链路尚未形成数据证据。至少需要一次真实或准生产的 Lark interactive card 点击验证：

1. Lark callback 能通过签名校验。
2. operator allowlist 正常。
3. decision record 正常写入。
4. dispatch 产生预期 watchlist/outbox/audit 副作用。
5. 重复点击不会产生重复副作用。

## 6. 超出原始 PRD 的能力

当前实现并不是只做原始 Listing Agent，而是扩成了更完整的运营信号系统。

| 额外能力 | 价值 |
|---|---|
| Top30 hot-gap push | 从 CoinGecko Top30 中发现 edgeX 未上但竞品已热的标的，提升主动选币能力。 |
| Top30 streak | 识别某 token 连续多天在榜，降低一次性噪声。 |
| Auto-quiet | 对连续多天未处理的标的自动降噪，防止群疲劳。 |
| Push burst control | 控制每轮推送数量与间隔，避免 UTC rollover 时刷屏。 |
| CEX/DEX divergence | 识别 CEX 热、DEX 热、双热或重度缺口场景，覆盖比原 PRD 更广。 |
| Liquidity lag alert | 发现 edgeX 相对竞品流动性落后。 |
| Worst-depth alert | 对最差深度交易对做运营提醒。 |
| `t_listing_alert_state` | 通用 first / reissue / clear 告警状态机，可复用于后续告警族。 |
| Delivery outbox | 统一 outbox + retry + disabled + audit，增强投递可靠性。 |
| Source health | 让“数据源是否可用”变成可观测对象。 |
| 冷启动 baseline | 避免部署初期把历史数据误判为新事件。 |
| Fusion fail-closed | listed universe 不可用时不盲目产生候选，避免误报。 |
| Per-feature delivery proxy | Listing delivery proxy 与 exchange proxy 隔离，降低配置污染风险。 |

这些能力明显超出原始 PRD，并且从运营系统角度看是合理扩展。

## 7. 建议的收尾优先级

### P0：先保证当前生产链路可信

| 事项 | 原因 |
|---|---|
| 固化非稳定币 record_only 单发 / 清理策略 | stablecoin / quote / collateral 类旧候选已由 producer gate 跳过；其它历史 `instrument_diff_only` record_only 候选仍需避免每天 UTC 重新生成 outbox。 |
| 持续监控 metadata_changed / listing_time_changed | StableHash 与 timestamp round-trip 已修复，但每次 normalizer extras 改动都必须配套 TDD 与 NormalizerVersion bump。 |
| 持续监控 signal silent-fail 与 snapshot freshness | 关注日志中的 `ErrSignalSilentFail` / `insert signal ... continuing tick`，并确认 `t_listing_instrument_snapshot.last_seen_at` 持续推进，避免 runtime universe 因 source freshness 退化而缩水。 |
| 做一次真实 Lark callback 点击验证 | 当前按钮链路已有代码和 E2E，但仍建议补齐真实或准生产点击证据。 |

### P1：补齐原始 PRD 验收缺口

| 事项 | 原因 |
|---|---|
| 明确是否仍要求 6 家公告源全覆盖 | 如果是验收硬条件，需要补 OKX / MEXC / Hyperliquid；如果不是，应更新 PRD 或验收口径。 |
| 补 risk plan 细节 | 填充 funding initial mode、MaxPositionUSD，并显式表达 MEXC + Bitget conservative scenario。 |
| 补或确认竞品领先榜单页面 | 原 PRD 明确要求独立页面，需要明确现有 Top30/divergence 是否替代该页面。 |
| 实现 watchlist 自动转 Dashboard 核心监控 | 这是从“发现机会”到“上线后监控”的最后一环。 |
| 明确 prepare_listing 多群分发策略 | 当前单 listing ops channel 是否能代表 PRD 的多群协作，需要产品/运营确认。 |

## 8. 最终判断

如果用一句话概括：

> Listing Agent 当前已经完成了“后端信号决策与运营推送系统”的主体，并且额外扩展成了更强的运营信号平台；但若按原始 PRD 逐条验收，仍需要补齐公告全覆盖、risk plan 细节、多群协作、榜单页面、watchlist 自动转 Dashboard，以及真实按钮闭环验证。

建议不要把当前状态描述为“原 PRD 已 100% 完成”，更准确的表述是：

> Listing Agent backend core is mostly implemented and operationally extended, while several PRD-level acceptance closures remain pending.


## 9. 动态 Catalog 集成后的文档口径

动态 Catalog 现在是 Listing Agent 的一等能力，而不是附属脚本：

- `RunInstrumentPoll` 持续刷新 `t_listing_instrument_snapshot`；
- `CatalogResolver` 对 DB-first 平台优先读 snapshot，再回退 `backend/docs/raw-instruments/`；
- `RefreshListedUniverseFromSnapshots` 生成 `listed_universe.runtime.yaml`，供 Top30 enrichment、divergence、liquidity alert 与 candidate reconcile 消费；
- edgeX source 使用 `snapshot_only`，用于已上市集合和 lifecycle closure，不进入候选发现闭环；
- `StableHash` 只覆盖 schema-stable 字段，`NormalizerVersion` rollover guard 负责 hash 配方升级切换；
- signal fingerprint 使用 sha256-prefixed compact key，配合 `fingerprint VARCHAR(160)` 和 `ErrSignalSilentFail` typed diagnostic，避免旧长明文 fingerprint 在 `INSERT IGNORE` 下 silent-drop；
- instrument / announcement poll loop 采用 best-effort：单条 signal 写入失败不会阻断同轮 snapshot upsert，runtime universe 的稳定性以 `last_seen_at` freshness 为核心观测指标；
- stablecoin / quote / collateral assets（如 `USDC`、`USDT`、`USD1`）不作为新 listing 操作标的：fusion 只记录 observation-only，decision-card producer 也跳过历史旧 candidate。
- symbol identity normalization 已进入 normalizer v4：MEXC `EBAYSTOCK` 这类交易所原生 base 通过 `config/symbol_mapping.yaml` alias 归一到业务身份 `EBAY`，但原生 `api_symbol` / `base_asset` 仍保留供审计和 API 跳转；候选身份应按 `canonical_symbol + market_surface + instrument_kind` 判断，而不是只看 `canonical_symbol` 或 `display_symbol`。
- safe backfill 需使用 `backend/cmd/listing-symbol-backfill` dry-run-first；已发送 outbox 是审计历史，不应重写。更大范围 alias 扩展必须先补 exact aliases，再逐映射 dry-run / execute，禁止全局 `*STOCK` 后缀裁剪。

完整工程契约见 `listing-agent-dynamic-catalog-integration.md` 与 `listing-agent-symbol-identity-normalization.md`。
