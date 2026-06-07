# Liquidity 24h Share CoinGecko Fallback（24h 份额对 edgeX 原生 ticker 故障的兜底）

## 背景

`edgex-ops-intelligence` 前端的 **流动性监控 Tab** 顶部每个 SymbolBlock 显示一项 KPI —— `24h share = edgeX_volume / Σ all_platforms_volume`。在 V1 production 落地后，运营报告：本应在 1%~3% 区间的 BTC/ETH/SOL/DOGE/BNB 经常被钉在 `0.00%`，与 Platform 级 Share Tab 的 ~0.95% 数字明显矛盾；同时一些只在 edgeX 上市的合成永续（AAPL/META/CRCL/HOOD/GOLD ...）也时有时无地飘到 0。

参考提交：

```text
074cf47 fix(edgex-ops-intelligence): fall back to CoinGecko for 24h share when native ticker is blocked
e72754c feat(edgex-ops-intelligence): add per-chart line/bar toggle on SymbolBlock depth views   # 顺便带走 UI 渲染
```

## 怎么发现的

### 1. 表征观察

直接 curl 后端 KPI，确认问题不是前端渲染：

```bash
for s in "BTC-USDT (perp)" "ETH-USDT (perp)" "SOL-USDT (perp)" "AAPL-USDT (perp)"; do
  enc=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$s'))")
  curl -fsS "http://127.0.0.1:8080/api/snapshot/liquidity?symbol=$enc" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print(f'{\"$s\":<22} share={d[\"kpis\"].get(\"edgex_24h_share_pct\",0):.4f}%')"
done
```

输出（修复前）：

```text
BTC-USDT (perp)        share=1.2056%
ETH-USDT (perp)        share=0.0000%
SOL-USDT (perp)        share=0.0000%
AAPL-USDT (perp)       share=17.6315%
```

BTC 偶尔对，ETH/SOL 长期 0；同一时刻 Platform 级 Share Tab 显示 edgeX 24h ~0.95%。两边数据源不一致 = 同一个底层指标在 KPI 计算路径上出问题。

### 2. 追计算路径

`Store.liquidityKPIsLocked`（`backend/internal/collector/store.go`）原始实现：

```go
// 修复前
var edgexVol, totalVol float64
for _, plat := range allPlatforms {
    snap := s.volumes[plat][canonicalKey]
    if snap == nil || snap.Status != model.StatusComplete {
        continue   // <-- 关键：Status != complete 的整行扔掉
    }
    v := snap.Volume24hUSD
    if plat == "mexc" { v *= 0.4 } else if plat == "gate" { v *= 0.5 }
    if plat == "edgeX" { edgexVol = v }
    totalVol += v
}
share := edgexVol / totalVol * 100
```

数据源 `s.volumes` 只装"原生 ticker"。一旦 edgeX 自己的 `pro.edgex.exchange/api/v1/public/quote/getTicker` 这一轮抓失败：

- `snap.Status` 不是 `complete` → 整行被跳过 → `edgexVol = 0`
- 9 家竞品 ticker 全部 `complete`，照样进分母
- `share = 0 / (Σ 9 家) = 0.00%` ❌

而 Platform 级 Share Tab 用的是另一条路径 —— `s.cgPlatformVolumes`（CoinGecko `/exchanges/{id}/volume_chart`），edgeX 也在里面，所以那边照样能算出 ~0.95%。**两条路径是分裂的**，KPI 层的"严格过滤"撞上 ticker 抖动就翻车。

### 3. 找上游为什么抖

查 `t_symbol_volume_snapshot` 看 edgeX 的 ticker 健康度：

```sql
SELECT status, error_message, snapshot_ts
FROM t_symbol_volume_snapshot
WHERE platform='edgeX' AND display_symbol='ETH-USDT (perp)'
  AND snapshot_ts >= NOW() - INTERVAL 1 HOUR
ORDER BY snapshot_ts DESC LIMIT 12;
```

```text
unsupported   Get "https://pro.edgex.exchange/api/v1/public/quote/getTicker?...":
              HTTP 403 — body starts: <!DOCTYPE html><html><head>...Just a moment...   2026-05-27 07:55
complete      —                                                                        2026-05-27 07:50
unsupported   HTTP 403 — body starts: ...Just a moment...                              2026-05-27 07:45
complete      —                                                                        2026-05-27 07:40
unsupported   HTTP 403 — body starts: ...Just a moment...                              2026-05-27 07:35
complete      —                                                                        2026-05-27 07:30
```

**Cloudflare 403 + "Just a moment..." 拦截页**，每 5min 抓一轮、几乎每隔一轮拦一次。同一个 host 的 `getDepth` 不拦 ——只挡 ticker 端点。本质是 Cloudflare 对 edgeX 部署的 challenge 策略对 dashboard 的出口 IP 评分游走在阈值附近：盘口可过，ticker 触发挑战。

附带发现一个二级 bug：上游被 CF 挡的 HTTP 403 + HTML body，被 `FetchTicker` 当成"该平台不支持此 symbol"分类成了 `StatusUnsupported`。语义错位 —— `unsupported` 应该专门指"目录里没有这个 (platform, canonical) 条目"，传输错误应分类成 `error`。

## 怎么解决的

### 设计权衡

可选方向：

- **A. 治本：让 Cloudflare 不拦**。改出口 IP / 加 UA / 走代理。需运维介入，时间不可控，且 CF 评分会再次漂移，治标不治本。
- **B. 治标 + 写明症状**：KPI 层加二级数据源兜底，同时把"为什么是兜底"显式暴露到 UI。

选 B。原因：

- **CoinGecko 同窗口数据已经在 DB 里**。`coingeckoCollector` 每 5min 写 `t_daily_volume_aggregate`（含 V1 BTC/ETH/SOL + 每家 CG Top60 by 24h volume per platform），KPI 计算需要的"edgeX 当日 USDT 永续 24h 成交额"那一行 *本来就有*。
- **Platform 级 Share Tab 用的就是 CoinGecko**，KPI 改吃同一条通道，反而让前端两个 Tab 数据自洽。
- **生产链路上 ticker / Cloudflare 故障是间歇的**，治本周期长；UI 撞 0% 一秒都很难看。
- **B 不阻断 A**。哪天出口 IP 修好了，native 通道恢复 `complete`，KPI 自动回到 "complete" 状态，不需要二次发版。

### 二级 resolution

新增 `Store.symbolShare24hLocked(displaySymbol) (sharePct float64, status string)`，按平台逐个解析 24h volume，状态机：

```go
// internal/collector/store.go
type volSource int
const (
    srcNone volSource = iota
    srcNative
    srcCoinGecko
)

func (s *Store) symbolShare24hLocked(displaySymbol string) (float64, string) {
    cKey := canonicalDailyKey(displaySymbol)  // BTC-USD → BTC-USDT 之类
    today := time.Now().UTC().Format("2006-01-02")

    var edgexVol, totalVol float64
    var edgexSrc volSource = srcNone
    sawFallback := false

    for _, plat := range allPlatforms {
        v, src := s.resolvePlatformVol24hLocked(plat, displaySymbol, cKey, today)
        if src == srcNone {
            continue
        }
        if src == srcCoinGecko { sawFallback = true }

        // discount 只在计算时叠加，永远不写回 DB
        if plat == "mexc" { v *= 0.4 } else if plat == "gate" { v *= 0.5 }

        if plat == "edgeX" {
            edgexVol = v
            edgexSrc = src
        }
        totalVol += v
    }

    switch {
    case edgexSrc == srcNone || totalVol <= 0:
        return 0, "stale"
    case sawFallback || edgexSrc == srcCoinGecko:
        return edgexVol / totalVol * 100, "partial"
    default:
        return edgexVol / totalVol * 100, "complete"
    }
}

// 按平台的 2-tier resolution
func (s *Store) resolvePlatformVol24hLocked(plat, displaySymbol, cKey, today string) (float64, volSource) {
    // tier 1: native ticker, status=complete
    if snap, ok := s.volumes[plat][displaySymbol]; ok &&
        snap != nil && snap.Status == model.StatusComplete && snap.Volume24hUSD > 0 {
        return snap.Volume24hUSD, srcNative
    }
    // tier 2: CoinGecko same-day daily aggregate
    if dayMap, ok := s.dailySymbolVolumes[plat]; ok {
        if row, ok := dayMap[cKey+"|"+today]; ok && row.VolumeUSD > 0 {
            return row.VolumeUSD, srcCoinGecko
        }
    }
    return 0, srcNone
}
```

`liquidityKPIsLocked` 接入：

```go
sharePct, shareStatus := s.symbolShare24hLocked(displaySymbol)
kpis.Edgex24hSharePct = sharePct
kpis.Edgex24hShareStatus = shareStatus   // "complete" | "partial" | "stale"
```

### 关键不变量

- **discount 仍只在 compute time 叠**。`v *= 0.4 / 0.5` 永远不会写回 `s.volumes` 或 `t_daily_volume_aggregate`，保留原始数据可观测。
- **canonical key 归一**。`BTC-USD (perp)` / `BTC-USDT (perp)` 在 CG 那条路径上要 collapse 到同一行，不能在 fallback 时漏 match。覆盖在 `canonicalDailyKey` 和单测里。
- **fallback 只覆盖 CG 收录的 symbol**。V1 BTC/ETH/SOL + 每家 Top60。边缘合成永续（GOLD/META/CRCL/HOOD ...）没行 → status 直接 `stale` → UI 渲染 `—`，绝不会拿"分母减少 9 倍"骗操作员。

### 二级修：adapter 错误归类

`internal/adapter/adapter.go::FetchTicker` 增加 `unknownPlatform` 标记位：

```go
// 修复前：所有非 200 / parse / EOF 都进 StatusUnsupported
// 修复后：
var unknownPlatform bool
spec, ok := tickerSpecs[platform]
if !ok {
    unknownPlatform = true
}

resp, err := client.Do(req)
if err != nil {
    if unknownPlatform {
        return Ticker{Status: model.StatusUnsupported, ErrMsg: ...}
    }
    return Ticker{Status: model.StatusError, ErrMsg: err.Error()}   // <-- 关键
}
defer resp.Body.Close()
if resp.StatusCode >= 400 {
    return Ticker{Status: model.StatusError, ErrMsg: fmt.Sprintf("HTTP %d — body starts: %s", resp.StatusCode, snippet)}
}
```

`StatusUnsupported` 保留专门含义："catalog 里没有这个 (platform, canonical) 条目，或这个 platform 我们不认识"。CF 403 / 5xx / 超时 / parse 失败一律 `StatusError`，下游 KPI 过滤、collection counters、运营看板得以区分"上游压力"和"配置缺失"。

### API 契约

`backend/docs/openapi.json` + `backend/docs/swagger.json` 在 `LiquidityKPIs` schema 加新字段：

```json
"edgex_24h_share_status": {
  "$ref": "#/components/schemas/ApiStatus",
  "description": "complete = all 10 platforms served from native ticker; partial = at least one platform fell back to CoinGecko same-day aggregate (typically edgeX itself when its REST ticker is blocked by Cloudflare); stale = edgeX failed both channels, share value is suppressed."
}
```

前端类型同步 `web/lib/api/types.gen.ts`。

### 前端渲染

`web/components/symbol-block.tsx`（与 chart toggle 一同进 `e72754c`）：

```tsx
{kpis?.edgex_24h_share_status === 'stale'
  ? <span className="kpi-value muted">—</span>
  : <span className="kpi-value">{(kpis?.edgex_24h_share_pct ?? 0).toFixed(2)}%</span>}
{kpis?.edgex_24h_share_status === 'partial' ? (
  <span className="kpi-tag" title="At least one platform's 24h volume came from CoinGecko (native ticker degraded)">
    via CG
  </span>
) : null}
```

操作员看到 `via CG` 小标签 = 知道是兜底数据，看到 `—` = 知道是数据缺失（不是 edgeX 没量）。

## 验证

### 单测

新加 `backend/internal/collector/store_share24h_test.go`（5 例）：

1. **all-native happy path**：10 家 ticker 全 `complete` → `share = 真实值`、`status = complete`。
2. **Cloudflare 403 fallback**：edgeX ticker `StatusError`，但 `dailySymbolVolumes[edgeX][BTC|today]` 有 → 走兜底，`status = partial`，分母完整。
3. **both-channels stale**：edgeX 两条通道全空 → `share = 0`、`status = stale`。
4. **canonical key collapse**：`BTC-USD` 形态的 native + `BTC-USDT` 形态的 CG → 必须 match 到同一行，不重复计入。
5. **Liquidity API contract**：HTTP handler 层确认 `edgex_24h_share_status` 在 JSON 中暴露。

`go test ./...` 通过；`npm run lint / typecheck / build` 通过。

### 重启实测（容器跑新版本 `v1.0.0-50-g074cf47-dirty`）

```bash
for s in "BTC-USDT (perp)" "ETH-USDT (perp)" "SOL-USDT (perp)" "DOGE-USDT (perp)" \
         "BNB-USDT (perp)" "AAPL-USDT (perp)" "GOLD-USDT (perp)"; do
  enc=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$s'))")
  curl -fsS "http://127.0.0.1:8080/api/snapshot/liquidity?symbol=$enc" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); k=d['kpis']; print(f'{\"$s\":<22} share={k.get(\"edgex_24h_share_pct\",0):>8.4f}%  status={k.get(\"edgex_24h_share_status\",\"(none)\")}')"
done
```

```text
BTC-USDT (perp)         share=  1.2587%  status=partial
ETH-USDT (perp)         share=  1.3684%  status=partial
SOL-USDT (perp)         share=  1.5747%  status=partial
DOGE-USDT (perp)        share=  2.4240%  status=partial
BNB-USDT (perp)         share=  2.0412%  status=partial
AAPL-USDT (perp)        share= 17.6315%  status=partial
GOLD-USDT (perp)        share=  0.0000%  status=stale
```

DB 侧交叉验证 adapter 状态归类已切换：

```sql
SELECT status, COUNT(*) FROM t_symbol_volume_snapshot
WHERE platform='edgeX' AND snapshot_ts >= NOW() - INTERVAL 10 MINUTE
GROUP BY status;
```

```text
complete       4    -- 08:09 那一轮 ticker 抓到了
unsupported    4    -- 08:14 那一轮，老 binary 还在跑（CF 403 被误标）
error          4    -- 08:16 重启后，CF 403 正确归类为 error
```

新版本生效后：CF 403 → `status=error`（adapter 归类正确）→ KPI 走兜底 → UI 显示真实 share + `via CG`。GOLD 等 CG 不收录的合成永续显示 `—` 而不是 0%。

## 已知边界与后续

- **Cloudflare 治本不在本 PR 范围**。本修复只确保 dashboard 在 CF 拦截窗口内仍能产出可信 KPI。运维侧的根治路径见 `backend/docs/runbook.md` §3.6：
  - 检查 dashboard 容器出口 IP，若与共享 NAT 大量挤压 CF 评分，迁到独立出口或挂净化代理（`Runtime.exchange_proxy`）。
  - 中长期可考虑给 edgeX ticker 加 `DisplayFallbackWindow` 风格的"在 X 分钟内复用上一次 complete 读数"机制，目前 depth 路径已经在这么做（见 `native-exchange-proxy.md` 末段），ticker 路径还没复用同一套基础设施。
- **fallback 覆盖率取决于 CG 收录范围**。V1 BTC/ETH/SOL 永久 first-class，其余按各家 Top60 by 24h volume 自然衰减。Long-tail 边缘合成永续仍然只能拿 native ticker，此处 `stale` 是正确状态而非缺陷。
- **新字段对外向后兼容**。老前端 / 老消费者忽略 `edgex_24h_share_status` 不会破坏 `edgex_24h_share_pct` 的语义，仅丢失"是否兜底"的额外信息。

## 配置 / 部署清单

变更范围：

```text
backend/internal/collector/store.go              symbolShare24hLocked + 接入 liquidityKPIsLocked
backend/internal/collector/store_share24h_test.go  5 个新单测
backend/internal/adapter/adapter.go              FetchTicker StatusError/Unsupported 归类修正
backend/docs/openapi.json                        LiquidityKPIs.edgex_24h_share_status
backend/docs/swagger.json                        同上
backend/docs/runbook.md                          §3.6 诊断流程
CHANGELOG.md                                     [Unreleased] 段
web/lib/api/types.gen.ts                         类型同步
web/components/symbol-block.tsx                  UI 渲染（随 e72754c 进）
```

部署侧约束：

- 无新配置开关。`Runtime.exchange_proxy` / `Runtime.coingecko.proxy` 维持原样。
- 兜底依赖 `dailySymbolVolumes` 已有的 5min CoinGecko 写入；若 CoinGecko 接口本身也故障，则 V1 三家会进 `stale` ——此时是真的双源失败，运营应介入。
- 升级路径：直接发布新 binary 即可。CHANGELOG `[Unreleased]` 段会在下次 v1.0.1 release 时收口。
