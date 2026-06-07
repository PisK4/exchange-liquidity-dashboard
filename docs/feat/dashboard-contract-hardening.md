# Dashboard 合约化与 CI/E2E 加固

## 背景

随着 Dashboard 从 V1 原型进入多数据源、多历史窗口、多前端交互状态，单靠手工 curl 很容易漏掉 API shape 漂移、前端类型漂移和采集读模型不一致。本轮加固目标是把后端 API、OpenAPI、生成的 TypeScript 类型、前端 E2E 和 CI 串成一条可验证链路。

## 后端 API 解耦

实现位置：

- `backend/internal/api/server.go`
- `backend/internal/api/server_test.go`

变更：

- `Server` 不再直接依赖 `*collector.Store`，改为依赖 `StoreReader` interface；
- API handler 测试使用 fake store reader 覆盖 health、readiness、snapshot 等 HTTP contract；
- MySQL readiness / health 行为在接口层可独立验证。

收益：

- API 层可以在不启动 collector / MySQL / 交易所 adapter 的情况下进行 contract 测试；
- 后续替换 store 实现或引入缓存层时，HTTP 层变更面更小。

## StoreSnapshot 读模型

实现位置：

- `backend/internal/collector/store_snapshot.go`
- `backend/internal/collector/store_snapshot_test.go`
- `backend/internal/collector/store.go`

变更：

- Store 写路径在更新关键 map 后发布 `StoreSnapshot`；
- `Snapshot()` 返回 clone，调用方无法修改 store 内部 map/slice；
- `OpsIntelligenceMeta`、`CollectionStatus`、`RuntimeConfig` 等只读路径可使用 snapshot，降低锁内复杂度。

收益：

- API 读取看到的是一致的 store 视图；
- 避免 handler 侧无意修改 store 内存结构；
- 为后续进一步拆分 `store.go` 留出稳定读模型边界。

## OpenAPI 与前端类型生成

实现位置：

- `backend/docs/openapi.json`
- `backend/docs/swagger.json`
- `backend/internal/api/openapi_contract_test.go`
- `web/lib/api/types.gen.ts`
- `web/lib/api/types.ts`
- `web/lib/api/client.ts`
- `web/package.json`

变更：

- OpenAPI 补齐主要响应 schema：Meta、Liquidity、Quality、Share、Top30、Funding、CollectionStatus 等；
- `swagger.json` 必须与 `openapi.json` 同步；
- 前端使用 `openapi-typescript` 生成 `types.gen.ts`；
- `client.ts` 从生成类型 re-export 业务别名，避免手写类型与后端 schema 漂移；
- `npm run openapi:check` 会重新生成类型，并用 git diff / git ls-files 检查漂移与漏提交。

CI 中对应步骤：

```bash
npm run openapi:check
npm run typecheck
```

## 前端 fetcher 与确定性 E2E

实现位置：

- `web/lib/api/fetcher.ts`
- `web/e2e/fixtures.ts`
- `web/e2e/dashboard.spec.ts`
- `web/playwright.config.ts`

变更：

- 抽出 fetcher，统一处理 JSON fallback、错误和 AbortSignal；
- Playwright 使用确定性 fixture 路由拦截 API，不依赖 live 交易所或 CoinGecko；
- Playwright config 增加 `webServer`，CI 可自启动 Next 服务；
- legacy route redirect 保留原 query，避免旧链接丢失 symbol/window/platform 等上下文。

CI 中对应步骤：

```bash
npx playwright install --with-deps chromium
npm run test:e2e
```

## Adapter 与指标测试覆盖

新增/强化测试：

- `backend/internal/indicators/property_test.go`：指标性质测试，覆盖中位数、深度、折算等基础行为；
- `backend/internal/adapter/instruments_test.go`：Binance raw instrument fixture 解析；
- `backend/internal/collector/funding*_test.go`：funding 归一化、nullable 解析、median、store attach；
- `backend/internal/collector/store_coingecko_test.go`：Share/Top30/CoinGecko 聚合 contract；
- `backend/internal/collector/top30_backfill*_test.go`：backfill gap detection / skip / concurrency 相关行为。

## Adapter bookview 拆分

实现位置：

- `backend/internal/adapter/bookview.go`
- `backend/internal/adapter/adapter.go`

变更：

- 将 orderbook view finalization、per-tier selector、depth classification 等 helper 从 `adapter.go` 拆到 `bookview.go`；
- 保持行为不变，降低 adapter 主文件复杂度；
- 为后续 per-platform WS / REST view plan 继续拆分留边界。

## GitHub Actions CI

实现位置：

- `.github/workflows/ci.yml`

当前 CI：

```text
backend:
  gofmt
  go vet
  go build
  go test

frontend:
  npm ci
  npm run openapi:check
  npm run typecheck
  npm run lint
  npm run build
  npx playwright install --with-deps chromium
  npm run test:e2e
```

并已显式设置：

```yaml
permissions:
  contents: read
```

## 本地验证基线

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/backend
make ci
make smoke-api PORT=18080 SYMBOL=BTC-USDT
```

```bash
cd /Users/pis/workspace-intelligence/edgex-intelligence/repos/edgex-ops-intelligence/web
npm run openapi:check
npm run typecheck
npm run lint
npm run build
npm run test:e2e
```

## 后续可继续拆分

- `backend/internal/collector/store.go` 仍偏大，可继续按 Share / Top30 / Funding / KPI 拆分；
- 前端 `dashboard-shell.tsx` 仍承载多个 Tab，可继续拆为 tab-level component；
- OpenAPI 目前覆盖响应 shape，后续可补请求参数 schema 与错误响应 schema。
