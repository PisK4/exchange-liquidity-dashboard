# Native Exchange Proxy（流动性 / 盘口 / Lighter WS 全链路代理化）

## 背景

`edgex-ops-intelligence` 后端在本地 Docker 栈（`deploy/docker-compose.yaml`）里跑时，前端的 **流动性监控** 和 **盘口质量** 两个 Tab 全平台空白 / `EOF`。表现：

- Share Tab 正常（走 CoinGecko）。
- Liquidity / Quality Tab 9 家原生交易所的所有 row `depth_status=error`。
- Lighter 即便加了 REST 代理仍然 `lighter market 1 ws book not ready`。

报告该问题时容器自身健康（当前 liveness 为 `/api/health` 200），DB 通，前端能 SSR。问题完全发生在 backend → 上游交易所的出向链路。

参考提交：

```text
c4cd2ea feat(edgex-ops-intelligence): route native adapters through optional proxy
6db43e1 feat(edgex-ops-intelligence): route Lighter WS through optional proxy
```

## 怎么发现的

### 1. 表征观察

直接 curl 后端 API，确认后端不是宕机，是上游全部失败：

```bash
curl -fsS "http://127.0.0.1:8080/api/snapshot/liquidity?symbol=BTC-USDT%20(perp)" \
  | jq '.rows[] | {platform, depth_status, error}'
```

输出（节选）：

```text
edgeX        error    Get "https://pro.edgex.exchange/api/v1/public/quote/getDepth?...": EOF
binance      error    Get "https://fapi.binance.com/fapi/v1/depth?...":                  EOF
okx          error    Get "https://www.okx.com/api/v5/market/books-full?...":            EOF
bybit        error    Get "https://api.bybit.com/v5/market/orderbook?...":               EOF
bitget       error    Get "https://api.bitget.com/api/v2/mix/market/orderbook?...":      EOF
bingx        error    Get "https://open-api.bingx.com/openApi/swap/v2/quote/depth?...":  EOF
mexc         error    Get "https://contract.mexc.com/api/v1/contract/depth/...":         EOF
gate         error    Get "https://api.gateio.ws/api/v4/futures/usdt/order_book?...":    EOF
hyperliquid  error    Post "https://api.hyperliquid.xyz/info":                           EOF
lighter      error    lighter market 1 ws book not ready
```

**全平台第一个 TCP 读 `EOF`** —— 不是 4xx/5xx，而是连接根本握不上手。这是 GFW 干净阻断海外金融 API 的典型特征。

### 2. 为什么 CoinGecko 不受影响

回看 `internal/marketdata/coingecko/client.go`，CoinGecko 客户端在构造期自己拼 transport：

```go
tr := &http.Transport{ ... }
if cfg.Proxy != "" {
    u, _ := url.Parse(cfg.Proxy)
    tr.Proxy = http.ProxyURL(u)
}
client := &http.Client{ Transport: tr, Timeout: cfg.RequestTimeout }
```

配合 Nacos 渲染到 `/config/edgex-ops-intelligence.yaml` 里的 `Runtime.coingecko.proxy: "http://host.docker.internal:7897"`，CoinGecko 走的就是宿主机本地的 Clash / 代理转出去；自然不受 GFW 影响。

而 9 个原生 adapter 走的是：

```go
// internal/adapter/adapter.go (修复前)
func NewWithLighter(platform string, timeout time.Duration, lighter LighterBookProvider) ExchangeAdapter {
    return RESTAdapter{
        Platform:    platform,
        Client:      &http.Client{Timeout: timeout},
        MaxAttempts: 2,
        Lighter:     lighter,
    }
}
```

`&http.Client{Timeout: timeout}` 没显式 Transport → 使用 `http.DefaultTransport` → **不读** `coingecko.proxy` 这个 CoinGecko 专属配置，**也不读** `HTTPS_PROXY` 环境变量（除非进程级 env 显式设了，而 R1 设计是 *刻意不设*，避免把延迟测量数据污染掉）。

至此根因清楚：**CoinGecko 通道有自己的代理 transport；9 家原生 REST + Lighter WS 全部走直连**，从中国大陆 Docker 内全部撞墙。

### 3. Lighter 单独的子链路问题

REST 代理修好之后再 curl，看到 9/10 平台恢复 `complete` / `partial`，**唯独 lighter 还是 `ws book not ready`**。看后端日志：

```text
lighter ws disconnected: EOF
lighter ws disconnected: EOF
lighter ws disconnected: context deadline exceeded
```

Lighter 不走 REST，走 WebSocket。`internal/adapter/lighter_ws.go`：

```go
dialer := websocket.Dialer{ HandshakeTimeout: 15 * time.Second }
conn, _, err := dialer.DialContext(ctx, p.url, nil)
```

`websocket.Dialer` 有自己独立的拨号路径，**没接** REST 那条 transport，也不读 env vars。所以 REST 代理修复对 WS 路径无效。

## 怎么解决的

总体思路：**Nacos 主配置里的 opt-in 代理配置 `Runtime.exchange_proxy`，同时透传到三处独立的拨号点**。CoinGecko 的 `Runtime.coingecko.proxy` 保持独立不动（语义不同：CoinGecko 与原生交易所可按部署网络分别选择直连或代理）。

### 配置项

```yaml
# /config/edgex-ops-intelligence.yaml
# Nacos dataId: edgex-ops-intelligence.yaml
Runtime:
  collection_interval: 5m
  http_timeout: 12s

  # Optional HTTP/HTTPS proxy for the 9 native exchange REST adapters AND
  # the Lighter WS dialer. Set this when the dashboard runs in a container
  # whose runtime network cannot reach binance / okx / bybit / ... directly
  # (e.g. behind the Great Firewall). Leave blank in production deployments
  # that can reach upstream exchanges over the open internet — latency
  # measurements are clearest without an intermediary.
  exchange_proxy: "http://host.docker.internal:7897"

  coingecko:
    proxy: "http://host.docker.internal:7897"

  ws_providers:
    bitget:
      proxy: "http://host.docker.internal:7897"
    mexc:
      proxy: "http://host.docker.internal:7897"
```

config 解析层：

```go
// internal/config/config.go
type Runtime struct {
    ...
    HTTPTimeout    time.Duration `json:"http_timeout"`
    ExchangeProxy  string        `json:"exchange_proxy,omitempty"`
    LighterWSURL   string        `json:"lighter_ws_url"`
    ...
}
```

### REST adapter（9 家原生交易所）

`adapter.NewWithLighterAndProxy` 接收 proxy URL，克隆 `http.DefaultTransport` 后设置 `Proxy`：

```go
// internal/adapter/adapter.go
func NewWithLighterAndProxy(platform string, timeout time.Duration,
    lighter LighterBookProvider, proxy string) ExchangeAdapter {
    return RESTAdapter{
        Platform:    platform,
        Client:      newHTTPClient(timeout, proxy),
        MaxAttempts: 2,
        Lighter:     lighter,
    }
}

func newHTTPClient(timeout time.Duration, proxy string) *http.Client {
    if proxy == "" {
        return &http.Client{Timeout: timeout}
    }
    parsed, err := url.Parse(proxy)
    if err != nil || parsed.Scheme == "" || parsed.Host == "" {
        // 容错：proxy 写错时退回直连，避免整个 collector 起不来。
        return &http.Client{Timeout: timeout}
    }
    tr := http.DefaultTransport.(*http.Transport).Clone()
    tr.Proxy = http.ProxyURL(parsed)
    return &http.Client{Timeout: timeout, Transport: tr}
}
```

collector 透传：

```go
// internal/collector/collector.go
adapters[p] = adapter.NewWithLighterAndProxy(
    p, cfg.Runtime.HTTPTimeout, lighter, cfg.Runtime.ExchangeProxy,
)
```

### Lighter WS dialer

给 `LighterWSProvider` 加 `proxy` 字段 + proxy-aware 构造函数；旧 `NewLighterWSProvider(url, staleAfter)` 保留供测试和直连部署使用。

```go
// internal/adapter/lighter_ws.go
type LighterWSProvider struct {
    url        string
    proxy      string
    staleAfter time.Duration
    ...
}

func NewLighterWSProviderWithProxy(url string, staleAfter time.Duration, proxy string) *LighterWSProvider {
    ...
    return &LighterWSProvider{url: url, proxy: proxy, staleAfter: staleAfter, books: ...}
}

func (p *LighterWSProvider) runOnce(ctx context.Context, marketIDs []int) error {
    dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
    if p.proxy != "" {
        if parsed, err := url.Parse(p.proxy); err == nil && parsed.Scheme != "" && parsed.Host != "" {
            dialer.Proxy = http.ProxyURL(parsed)
        }
    }
    conn, _, err := dialer.DialContext(ctx, p.url, nil)
    ...
}
```

dashboard 入口透传：

```go
// cmd/ops-intelligence/main.go
lighterProvider := adapter.NewLighterWSProviderWithProxy(
    cfg.Runtime.LighterWSURL,
    cfg.Runtime.LighterStaleAfter,
    cfg.Runtime.ExchangeProxy,
)
```

### 为什么不直接用 `HTTPS_PROXY` env

考虑过 "在 docker-compose 给 backend 设 `HTTPS_PROXY=http://host.docker.internal:7897`"，Go 的 `http.DefaultTransport` 会自动读。但放弃此方案：

- **作用域过广**：会同时影响进程内任何裸用 `http.DefaultClient` 的代码（库代码、未来加的 metrics exporter、健康检查回调 ...）。
- **配置不可见**：yaml 里看不到，代码 review 容易丢失上下文，新人 onboarding 找问题困难。
- **CoinGecko 路径已经有显式 `coingecko.proxy`**，再叠 env vars 是双轨制。

显式配置 `Runtime.exchange_proxy` 与 `Runtime.coingecko.proxy`，语义清晰，opt-in，且可由 Nacos 按环境下发。

## 验证

```bash
cd deploy && docker compose build backend && docker compose up -d backend
sleep 25
curl -fsS "http://127.0.0.1:8080/api/snapshot/liquidity?symbol=BTC-USDT%20(perp)" \
  | python3 -c "
import json,sys
for r in json.load(sys.stdin)['rows']:
    print(f\"{r['platform']:<12} {r['depth_status']}\")
"
```

修复后输出：

```text
edgeX        complete
binance      partial
okx          partial
bybit        partial
bitget       partial
bingx        partial
mexc         partial
gate         complete
hyperliquid  partial
lighter      complete    # 三档深度 7.3M / 20.2M / 102M / 170M USD
```

`partial` 不是失败：部分交易所的公共 depth API 单次最多返回 200 / 1000 档，对 `±2%` 价位档计算的 100% 覆盖是 `complete`，对更远档位是 `partial`。这是 R6 设计的正常状态。

## 验证 fallback 机制为什么没对 lighter 起作用

修复落地后，有同步澄清一个相关问题：**在出问题的窗口内，"上一轮 5min 数据"为什么也没显示给 lighter？**

`displayPlatformSnapshotLocked` 的 fallback 行为（`internal/collector/store.go`）：

1. 当前 latest snapshot 若 `depth_status ∈ {complete, partial, aggregated_orderbook, ws_limited_depth}` 且有 tier 数据 → 直接 `live` 返回。
2. 否则在内存 `platformHistory[platform|symbol]` 反向走，找最近一条 displayable 候选；命中前提是 `candidate.SnapshotTS >= latest.SnapshotTS - DisplayFallbackWindow`（默认 30min），命中后标 `delayed`。
3. 没命中就把当前错误 snapshot 原样吐出去。

对 lighter 这次情形，三个原因叠加导致 fallback 失效：

- **窗口（30min）远短于 outage**。lighter 最后一次成功是 `07:32:48` 附近，恢复是 `12:12:56`，gap 长达 4+ 小时。任何 30min 窗口都跨不过去。
- **`LoadLatestFromDB` 只读 `MAX(snapshot_ts)`**。容器启动时载入的是 lighter 在 DB 里最新那一行 —— 也就是上次容器死前刚写下的 `depth_status=error` 行。`platformHistory` boot 后只有一条 error 种子。
- **fallback 只看 in-memory `platformHistory` slice，不回查 MySQL**。哪怕 DB 里 `07:07~09:xx` 区段确实有 50 条 complete 行（已查证），fallback 也看不到。

结论：fallback 是"按设计正确"的，但当前设计对"长时间外网阻断 + 容器重启"这种组合场景兜不住。

后续改进选项（未实现）：

- **A. 扩大 `display_fallback_window`** 到 6h 量级。最简单，但延迟数据会更老。
- **B. `LoadLatestFromDB` 多加载一条"最近 displayable"作为 history 种子**，fallback 回查 MySQL 找窗内最近 displayable。最贴近"上一轮成功就用上一轮"原始语义；改动更大但行为更对。

## 配置 / 部署清单

变更范围 4 个后端文件 + 主配置 yaml：

```text
backend/internal/config/config.go        Runtime.ExchangeProxy 字段 + yaml 解析
backend/internal/adapter/adapter.go      NewWithLighterAndProxy + newHTTPClient
backend/internal/adapter/lighter_ws.go   LighterWSProvider.proxy + NewLighterWSProviderWithProxy
backend/internal/collector/collector.go  透传 cfg.Runtime.ExchangeProxy 到 adapter
backend/cmd/ops-intelligence/main.go            透传到 lighter provider
config/edgex-ops-intelligence.yaml  Runtime.exchange_proxy: "http://host.docker.internal:7897"
```

部署侧约束：

- 本地 Docker：宿主机需有可达海外的代理（Clash / mihomo / shadowsocks 等），并 listen 在 `host.docker.internal` 可达的端口（macOS Docker Desktop 默认就给 `host.docker.internal` 解析）。
- dev/prod：Nacos dataId `edgex-ops-intelligence.yaml` 渲染并挂载到 `/config/edgex-ops-intelligence.yaml`；应用本身仍按 `--config-dir=/config` 读取文件。
- 生产环境（JP / SG 等境外服务器）：`Runtime.exchange_proxy`、`Runtime.coingecko.proxy`、`Runtime.ws_providers.*.proxy` 留空即可直连，延迟测量保持纯净。
- proxy URL 写错（scheme/host 缺失）：代码会静默回退直连，下一轮 collector 会在错误信息里暴露真实 dial 失败，运维可见。

## 另见：Listing / Activity Lark 推送的独立代理

`Runtime.exchange_proxy`、`Runtime.coingecko.proxy`、`Runtime.ws_providers.*.proxy` 都属于**数据采集出向**链路，目标是上游交易所 / 行情聚合服务。Listing Agent 与 Activity Agent 的 Lark 推送是**告警/运营出向**链路，会拨到 `open.larksuite.com` 的 webhook bot，应该使用各自独立的 HTTP client / scoped proxy：

| 链路 | 配置字段 | 说明 |
|---|---|---|
| Listing Lark delivery | `Runtime.listing_agent.delivery.proxy` | 从 `t_listing_delivery_outbox` 发送 Listing Top30/divergence/liquidity alert cards。 |
| Activity source fallback | `Runtime.activity_agent.source_proxy` 或 `Runtime.activity_agent.collection.source_proxy` | Activity source 抓取兜底代理；不等同于 Lark webhook 代理。 |
| Activity Lark delivery | `Runtime.activity_agent.delivery.proxy` | 从 `t_activity_delivery_outbox` 发送 Activity event/review cards。 |

Listing 详细说明见 `./listing-agent-top30-hot-gap-push.md` §"Delivery HTTP client 与 per-feature proxy"；Activity 当前实现契约见 `./activity-agent-current-contract.md`。

**不要把 exchange / CoinGecko 代理与 Lark 推送代理共用**：

- 二者在生产环境的拨号目标完全不同。海外直连节点上 `exchange_proxy` 留空即可拿到原生交易所数据，但同一台机器可能仍要走代理才能合规地拨 lark webhook（或反过来）。
- 进程级 `HTTPS_PROXY` env 作用域过广（影响任何裸用 `http.DefaultClient` 的代码），与本文档 §"为什么不直接用 `HTTPS_PROXY` env" 的结论一致——所以 Listing / Activity delivery 也走独立 yaml 字段，而不是依赖进程级代理。
