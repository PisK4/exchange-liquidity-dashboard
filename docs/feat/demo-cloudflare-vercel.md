# Demo Deployment: Cloudflare Tunnel + Vercel

## 背景

当前 `edgex-ops-intelligence` 处于快速 demo 阶段：后端和 MySQL 运行在开发机本地，前端部署到 Vercel，通过 Cloudflare Quick Tunnel 暴露一个公网 HTTPS 入口让 Vercel 的访问者能回连到本地 backend。

该方案目标是**最短路径完成演示**，不是生产级部署。本文记录当前 demo 链路上每一个组件的真实配置、踩过的坑，以及失效时的排查清单。

## 当前拓扑

```text
Demo 用户浏览器
        |
        | (HTTPS, fetch 由客户端组件发起)
        v
Vercel Next.js 前端 (exchange-liquidity-dashboard-pisk4s-projects.vercel.app)
        |
        | (fetch 直接打到 NEXT_PUBLIC_API_BASE，不经 Vercel Function)
        v
Cloudflare Quick Tunnel HTTPS URL
        |
        | (Cloudflare 边缘 -> 本地 cloudflared 进程 -> localhost:8080)
        v
本地 edgex-ops-intelligence backend (Docker Compose service `backend`, :8080)
        |
        v
本地 MySQL (Docker Compose service `mysql`, :3306)
```

关键事实：

- **API 调用主要发生在浏览器侧**，不是 SSR。`web/components/dashboard-client.tsx` 是客户端组件，`getJSONWithFallback` 在浏览器里跑。
- 因此 **`NEXT_PUBLIC_API_BASE` 是关键变量**，`SERVER_API_BASE` 在当前架构下基本不被使用（只在极少数 SSR 场景生效）。
- `NEXT_PUBLIC_*` 是 **build-time inline**，改了值必须重新部署，光改 Vercel 环境变量不重新构建是没用的。

## 本地后端启动

项目根目录：

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence
```

启动本地 MySQL + backend：

```bash
docker compose -f deploy/docker-compose.yaml up --build -d mysql backend
```

如果需要在 `/api/health` 里看到具体 git revision，推荐走 `deploy/Makefile` 或显式传 `BUILD_VERSION`。`deploy/docker-compose.yaml` 会把该 env 透传到 `Dockerfile.backend ARG BUILD_VERSION`，再由 Go `-ldflags` 写入 `main.version`：

```bash
cd deploy
BUILD_VERSION="$(git -C .. describe --tags --always --dirty)" docker compose build backend
docker compose up -d backend
```

验证本地后端：

```bash
curl http://127.0.0.1:8080/api/health
```

预期返回：

```json
{"build_version":"v1.0.0-144-g06de02f-dirty","mode":"v1-real-adapter-attempts","ok":true,"service":"edgex-ops-intelligence"}
```

排查版本是否透传：

```bash
curl -s http://127.0.0.1:8080/api/health | jq .build_version
# 期望不是 "dev"；若仍是 "dev"，说明 build 时没有传 BUILD_VERSION 或镜像未重建。
```

### Backend 冷启动与 readiness（当前实现）

早期 demo 版本里，HTTP listener 会等首轮 `CollectOnce` 同步完成后才启动，
因此曾出现约 6–8 分钟的对外服务空窗。**这已经不是当前实现**。

当前 `backend/cmd/ops-intelligence/main.go` 在 `--role=all` 且非 `--run-once`
时会先异步启动 API server，再在后台恢复 MySQL latest snapshots、启动
Lighter WS 和首轮 collector。当前应按两个端点区分状态：

- `/api/health`：liveness，进程起来后应先可用，Docker healthcheck 也指向它。
- `/api/readiness`：严格接流门禁；在 warm cache 恢复或首轮 collection 进入终态前可能返回 503。

因此，demo 链路排查时不要再用“容器启动后 7 分钟内 `:8080` 没监听”作为
当前预期。若 tunnel 返回 502，先检查 `/api/health` 是否可达；若 health 正常
但页面数据仍未就绪，再看 `/api/readiness` 和 `/api/collection-status` 的 startup
字段。详见 `backend/docs/runbook.md` 的 Health and Readiness Probes 章节。

## Cloudflare Tunnel

由于当前没有自有域名托管在 Cloudflare，**走的是 Quick Tunnel**（`*.trycloudflare.com` 临时域名）。Named Tunnel 需要 Cloudflare 上 Active 状态的域名才能配，暂不可行。

Quick Tunnel 的硬限制：

- URL 是随机三/四词的临时地址，cloudflared 进程一旦终止就被 Cloudflare 回收，且**不可恢复**。
- 重新启动一定拿到新 URL，必须同步更新 Vercel 环境变量并 redeploy。
- Cloudflare 对长时间空闲/异常连接会主动回收。

### 当前 demo URL

```text
https://mechanical-moisture-florence-mild.trycloudflare.com
```

> ⚠️ 这个 URL **极易变**——cloudflared 进程重启、Cloudflare 边缘回收、QUIC 故障等任意场景都会换 URL。本文档里写死的值随时可能已过期。**真正的 source of truth 是 `~/.cloudflared-dashboard-url`**，使用前先 `cat` 一下确认。

URL 快速查询：

```bash
cat ~/.cloudflared-dashboard-url
```

### LaunchAgent 后台化方案（当前生效）

直接前台 `cloudflared tunnel --url ...` 的方案缺点是终端关闭 / Mac 睡眠 / SSH 断开都会杀掉隧道。当前 demo 使用 macOS LaunchAgent 托管 `cloudflared`，崩了自动重启，开机自启。

配置文件：`~/Library/LaunchAgents/com.pis.cloudflared-dashboard.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.pis.cloudflared-dashboard</string>

    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/bin/cloudflared</string>
        <string>tunnel</string>
        <string>--no-autoupdate</string>
        <string>--protocol</string>
        <string>http2</string>
        <string>--url</string>
        <string>http://localhost:8080</string>
        <string>--loglevel</string>
        <string>info</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
        <key>Crashed</key>
        <true/>
    </dict>

    <key>ThrottleInterval</key>
    <integer>10</integer>

    <key>StandardOutPath</key>
    <string>/Users/pis/Library/Logs/cloudflared-dashboard.log</string>

    <key>StandardErrorPath</key>
    <string>/Users/pis/Library/Logs/cloudflared-dashboard.log</string>

    <key>ProcessType</key>
    <string>Background</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
```

> **`--protocol http2` 是必选项，不要省**。默认 QUIC 协议在当前网络环境下不稳定（实测会出现 `failed to dial to edge with quic: timeout: no recent network activity` 持续重试，导致边缘返回 530 Error 1033"Cloudflare is currently unable to resolve it"）。强制 HTTP/2 走 TCP 即可正常握手。
>
> 故障特征：本地 `curl localhost:8080/api/health` 正常返回 200，但 `curl $TUNNEL_URL/api/health` 返回 530，且 `~/Library/Logs/cloudflared-dashboard.log` 中持续打印 `Failed to dial a quic connection` / `failed to accept QUIC stream` / `Serve tunnel error` 三连——这就是 QUIC 被网络掐了。

### 另一种隧道失效模式：`Unauthorized: Tunnel not found`

跟 QUIC 不同的、更隐蔽的失效模式。**KeepAlive 救不了，必须人工 unload + load**。

故障特征：

- 本地直连 200，`curl $TUNNEL_URL/api/health` 是 `Could not resolve host`（DNS 都解析不出来）
- `launchctl list | grep cloudflared` 进程**活得好好的**，已经跑了 N 小时
- 但 `~/Library/Logs/cloudflared-dashboard.log` 里在死循环打：

  ```text
  ERR Connection terminated error="Unauthorized: Tunnel not found"
  ERR failed to serve incoming request error="Unauthorized: Tunnel not found"
  ERR Register tunnel error from server side error="Unauthorized: Tunnel not found"
  INF Retrying connection in up to 1m4s
  ```

发生机制：

1. cloudflared 启动时向 Cloudflare 注册了一个临时 Quick Tunnel，拿到 tunnel ID + URL；
2. 中间网络长时间中断（Mac 睡眠 / 切 Wi-Fi / VPN / 出门通勤），Cloudflare 边缘判定该 tunnel 长期失活，**主动从注册表里回收 hostname 和 tunnel ID**；
3. 网络恢复后，cloudflared 进程**毫不知情**，还在用旧的 tunnel ID 重连；
4. 边缘逐个连接返回 `Tunnel not found`，cloudflared 把它当作普通的临时错误一直 retry，永远恢复不了。

为什么 KeepAlive 救不了：进程没崩，launchd 看到的是"健康进程"，但它在做无效操作。

修复：必须 unload + load 让进程**重新注册一个新隧道**。一定会拿到新 URL，必须同步 Vercel 并 Redeploy。

```bash
launchctl unload ~/Library/LaunchAgents/com.pis.cloudflared-dashboard.plist
> ~/Library/Logs/cloudflared-dashboard.log
launchctl load -w ~/Library/LaunchAgents/com.pis.cloudflared-dashboard.plist
# 等 10s，抓新 URL
grep -Eo 'https://[a-z0-9-]+\.trycloudflare\.com' ~/Library/Logs/cloudflared-dashboard.log | head -1 \
  | tee ~/.cloudflared-dashboard-url
```

### 隧道管理速查

```bash
# 看当前公网 URL（任何时刻）
cat ~/.cloudflared-dashboard-url

# 看实时日志（含 URL 首次出现）
tail -f ~/Library/Logs/cloudflared-dashboard.log

# 重启隧道（会换 URL，记得同步 Vercel 并 redeploy）
launchctl kickstart -k gui/$(id -u)/com.pis.cloudflared-dashboard

# 暂停 / 重新加载
launchctl unload  ~/Library/LaunchAgents/com.pis.cloudflared-dashboard.plist
launchctl load -w ~/Library/LaunchAgents/com.pis.cloudflared-dashboard.plist

# 进程状态
launchctl list | grep cloudflared
ps aux | grep -E 'cloudflared.*tunnel' | grep -v grep

# 抓 URL（启动后自动写入快查文件）
grep -Eo 'https://[a-z0-9-]+\.trycloudflare\.com' ~/Library/Logs/cloudflared-dashboard.log | head -1 \
  | tee ~/.cloudflared-dashboard-url

# 防 Mac 睡眠期间网络断（demo 期间建议挂着）
caffeinate -i -s &
echo $! > /tmp/caffeinate.pid
# 取消：kill $(cat /tmp/caffeinate.pid)
```

验证公网后端：

```bash
URL=$(cat ~/.cloudflared-dashboard-url)
curl -sS "$URL/api/health"
# {"mode":"v1-real-adapter-attempts","ok":true,"service":"edgex-ops-intelligence"}
```

## Vercel 前端配置

由于 Next.js 前端位于 `web/` 子目录，Vercel 项目配置：

```text
Framework Preset: Next.js
Root Directory: web
Install Command: npm install
Build Command: npm run build
Output Directory: .next
Node.js Version: 20.x
```

### 环境变量（关键）

| Key | Value | Environments | 说明 |
|---|---|---|---|
| `NEXT_PUBLIC_API_BASE` | 当前 Cloudflare Tunnel URL | Production / Preview / Development | **核心变量**，被浏览器侧 fetch 使用；build-time inline |
| `SERVER_API_BASE` | 同上（保持一致即可） | 同上 | 仅 SSR 极少数路径使用，可选 |

当前值（同样易过期，仍以 `~/.cloudflared-dashboard-url` 为准）：

```env
NEXT_PUBLIC_API_BASE=https://mechanical-moisture-florence-mild.trycloudflare.com
SERVER_API_BASE=https://mechanical-moisture-florence-mild.trycloudflare.com
```

### ⚠️ 改完环境变量必须 Redeploy

`NEXT_PUBLIC_*` 在 Next.js 里是 **build-time 注入**到客户端 JS bundle 的，不是运行时读取。所以：

- 仅在 Vercel UI 改环境变量值 → **不生效**
- 必须 Deployments → 最新部署 → `⋯` → **Redeploy**
- 或推一个空 commit 触发 webhook 重新构建

## 代码侧适配点

`web/lib/api/client.ts` 当前实现：

```ts
const SERVER_API_BASE  = process.env.SERVER_API_BASE ?? process.env.NEXT_PUBLIC_API_BASE ?? 'http://127.0.0.1:8080';
const BROWSER_API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? '';

function apiBase(): string {
  // SSR 走 SERVER_API_BASE，浏览器走 BROWSER_API_BASE
  return typeof window === 'undefined' ? SERVER_API_BASE : BROWSER_API_BASE;
}

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${apiBase()}${path}`, { cache: 'no-store' });
  ...
}
```

要点：

- `dashboard-client.tsx` 等以 `-client` 结尾的组件是客户端组件，所有数据 fetch 在浏览器里跑，因此走 `BROWSER_API_BASE`。
- 如果 `NEXT_PUBLIC_API_BASE` 没设，`BROWSER_API_BASE` 为空字符串，浏览器会 fetch 相对路径（如 `/api/ops-intelligence/meta`），打到 Vercel 自己的域名 → 404 / 超时 → UI 报 "Connection Error"。这是 demo 早期最常见的"Vercel 上一片 Connection Error"症状。
- `fetch(url, { cache: 'no-store' })`：Vercel 不会把后端数据静态缓存，符合实时 demo 需要。

### Backend CORS

`backend/internal/api/server.go` 已设置：

```go
w.Header().Set("Access-Control-Allow-Origin", "*")
w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
```

所以浏览器从 `*.vercel.app` 跨域请求 `*.trycloudflare.com` 不会被拦。Demo 阶段允许全开，公开长期使用前应改为白名单。

## Demo 验证路径

Vercel 部署后重点验证：

```text
/
/liquidity
/quality
/share
/top30
```

成功标志：4 个 Tab 的数据都能加载、`Network` 面板里所有 `*.trycloudflare.com` 的请求 HTTP 200、响应头里能看到 `cf-ray:` 与 `server: cloudflare`。

## 失效时的排查清单

按下面顺序逐项验证，命中哪一步就修哪一步：

1. **本地 backend 是否健康 / ready**
   ```bash
   curl http://127.0.0.1:8080/api/health
   ```
   如果 `/api/health` 不通，说明进程或端口本身不可达，先查容器：
   ```bash
   docker compose -f deploy/docker-compose.yaml ps backend
   ```
   如果 `/api/health` 正常但页面还没数据，再看严格门禁和启动状态：
   ```bash
   curl -sS http://127.0.0.1:8080/api/readiness | jq '.ready, .checks.startup'
   curl -fsS http://127.0.0.1:8080/api/collection-status | jq '.startup'
   ```
   `--role=all` 下 API 会先启动，readiness 可能在 warm cache 或首轮 collection
   完成前短暂返回 503，这是当前设计。

2. **cloudflared 进程是否在跑**
   ```bash
   launchctl list | grep cloudflared    # 期望非空
   ps aux | grep -E 'cloudflared.*tunnel' | grep -v grep
   ```
   挂了：`launchctl load -w ~/Library/LaunchAgents/com.pis.cloudflared-dashboard.plist`

3. **隧道 URL 是否还有效**
   ```bash
   URL=$(cat ~/.cloudflared-dashboard-url)
   curl -sS -m 8 "$URL/api/health"
   ```
   - 拿到 200：隧道 OK，继续看 Vercel。
   - **502 Bad Gateway**：cloudflared 通了但 origin 不可达 → 回 (1) 查 `/api/health`。
   - **530 Cloudflare Error 1033**：cloudflared 进程存在但和边缘 control stream 断了；查日志，若是 `Failed to dial a quic connection` → QUIC 被网络掐，确认 plist 里有 `--protocol http2`。
   - **DNS `Could not resolve host`**：URL 已被 Cloudflare 回收；查日志，若是 `Unauthorized: Tunnel not found` 死循环 → 进程在用废弃 tunnel ID 撞墙，必须 unload + load 重注册（参见上文"另一种隧道失效模式"），新 URL 必须同步 Vercel + Redeploy。

4. **Vercel 环境变量是否是当前 tunnel URL，且已 redeploy**
   - Settings → Environment Variables 看 `NEXT_PUBLIC_API_BASE`
   - Deployments → 最新部署的时间戳是不是改完环境变量之后的

5. **浏览器 DevTools Network 面板**
   - 请求是不是真的打到了 `*.trycloudflare.com`？如果是相对路径或 `localhost`，那是 `NEXT_PUBLIC_API_BASE` 没生效（多半是没 redeploy）。
   - 响应 header 里有没有 `cf-ray:`？没有就不是 Cloudflare 这条链路。

6. **本机网络层**
   - Mac 是否刚从睡眠唤醒（cloudflared 会需要几秒重连，期间 502）
   - 是否切了 Wi-Fi / VPN（cloudflared 会重连，URL 不会变但短暂不可用）

## 风险与限制

- **Quick Tunnel URL 随时可能失效**：cloudflared 重启即换 URL，必须同步 Vercel + redeploy。不适合生产/长期对外。
- **本地电脑休眠 / 断网 / Docker 停止**任一发生，demo 立即不可用。
- **Readiness warm-up**：`--role=all` 会先暴露 `/api/health`，但在 warm cache 或首轮 collection 完成前 `/api/readiness` 可能短暂 503；前端展示数据也可能晚于进程 liveness。
- **CORS 当前全开（`*`）**：demo 友好，但任何来源都能调用 backend，公开长期使用前必须改白名单。
- **没有 Auth / 速率限制**：tunnel URL 谁拿到都能直接请求 backend 数据。

## 后续改进项

### 短期（提升 demo 稳定性，无需买域名）

1. **把 `CollectOnce` 改成异步**：解决 backend 冷启动 7 分钟空窗。在 `main.go` 把 L70 同步调用塞进 goroutine，让 `http.ListenAndServe` 立刻起来。`/api/health` 可以加个轻量字段表示 collector 是否首轮就绪。
2. **`BROWSER_API_BASE` 为空时输出 console.error**：让下次配置错误能在浏览器 console 立刻定位，不用从隧道反推回来。
3. **demo 期间常驻 `caffeinate -i -s`**：阻止 Mac 睡眠把隧道掐了。

### 中期（要稳定固定 URL）

迁到 **Cloudflare Named Tunnel + 自有域名**：

```text
1. 在 Cloudflare Registrar 买一个便宜域名（.xyz 首年 ~$1，.com ~$10/年）
2. cloudflared tunnel login
3. cloudflared tunnel create edgex-ops-intelligence-demo
4. cloudflared tunnel route dns edgex-ops-intelligence-demo dashboard-api.<yourdomain>
5. ~/.cloudflared/config.yml 配 ingress 规则
6. brew services start cloudflared
7. Vercel 环境变量改成 https://dashboard-api.<yourdomain>，从此固定
```

URL 一次配置永久不变，cloudflared 重启不影响。

### 长期（接近生产）

```text
Vercel 前端 + 云上 backend (ECS/Cloud Run) + 云上 MySQL (RDS)
```

后端 + 数据库完全脱离开发机，配合 Auth + 限流 + 监控告警。
