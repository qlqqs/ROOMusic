# CI 门禁与 HTTP 可观测性技术设计

## 1. 边界与所有权

本任务包含两个相互配合但可分别验证的交付面：

1. `.github/workflows/ci.yml` 负责仓库级构建、测试和集成门禁编排；它调用
   `backend/`、`frontend/` 和 `scripts/` 已有命令，不复制业务逻辑。
2. HTTP 请求观测归 Platform/HTTP 启动边界所有，入口是
   `backend/cmd/roomusic/main.go` 的 `requestIDMiddleware`。中间件只负责
   关联、计时、响应统计和日志分级，不查询数据库、不决定权限，也不改变 REST
   错误语义。

身份模块只在已有 `currentUser` 成功后向被动的请求观测上下文写入稳定 `actor_id`；
中间件不会为了日志自行执行认证查询。这样既满足日志规范，又避免把 Platform
变成 Identity 的第二个授权入口。

## 2. GitHub Actions 工作流

### 2.1 触发和权限

工作流使用以下触发器：

- `pull_request`：验证合并请求；
- `push.branches: [main]`：验证主分支；
- `workflow_dispatch`：允许维护人员手动重跑。

工作流权限固定为 `contents: read`，配置并发组按 workflow/ref 取消同一分支的旧
运行，避免多个扫描型集成容器长期占用 runner。每个 job 设置有限的 timeout，失败
仍执行测试脚本自己的清理 trap。

### 2.2 Job 划分

| Job | 运行内容 | 关键约束 |
| --- | --- | --- |
| `backend` | `gofmt`、普通测试、race、`vet`、`build` | 所有命令从 `backend/` module 运行；版本与 `.mise.toml` 对齐 |
| `frontend` | `npm ci`、lint、typecheck、Vitest、production build | 使用 `frontend/package-lock.json`；构建后检查 Go 内嵌资产无漂移 |
| `integration` | shell 语法、Compose 配置、`./scripts/test-integration.sh` | 只启动 PostgreSQL 18；不得读取生产 `.env` 或真实音乐目录 |

后端和前端 job 可以并行；数据库 job 是独立的硬门禁。Actions 使用稳定的官方
checkout/setup-go/setup-node action，并在实现时固定主版本；Go `1.25.10`、Node
`24.16.0` 与 `.mise.toml` 保持一致。`setup-node` 开启 npm cache，但安装始终使用
`npm ci`，不让 CI 修改 lockfile。

### 2.3 命令与失败语义

后端 job 的最小命令集合为：

```bash
test -z "$(gofmt -l .)"
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

前端 job 在 `frontend/` 执行：

```bash
npm ci
npm run lint
npm run typecheck
npm run test -- --run
npm run build
```

构建完成后执行 `git diff --exit-code -- backend/cmd/roomusic/web`，防止生产静态
资源与源码脱节。集成 job 执行：

```bash
bash -n scripts/*.sh
PG_PASSWORD=ci-placeholder MEILI_MASTER_KEY=ci-placeholder docker compose config --quiet
docker compose -f compose.test.yaml config --quiet
./scripts/test-integration.sh
```

`test-integration.sh` 是 PostgreSQL 18 的唯一启动/清理入口；CI 不设置
`ROOMUSIC_TEST_KEEP_DB=true`，也不把缺少数据库连接导致的普通测试 skip 当作集成
通过。Compose 主配置只做插值校验，不启动 Redis 或 Meilisearch。

## 3. HTTP 观测数据流

```text
请求
  -> request ID 规范化 + 被动 observation context
  -> ServeMux.Handler 解析安全 route template
  -> recording ResponseWriter 交给业务 handler
  -> handler 返回（身份成功时写 actor_id）
  -> defer 统计状态/字节/耗时并发出一个完成事件
```

### 3.1 事件 schema

每个请求完成后发出恰好一个 `slog` 事件：

```json
{
  "event": "http.request.completed",
  "module": "platform",
  "message": "http request completed",
  "request_id": "req-...",
  "method": "GET",
  "route_template": "GET /api/v1/releases/{id}",
  "status": 200,
  "response_bytes": 512,
  "duration_ms": 3,
  "actor_id": "<optional-stable-id>"
}
```

`time`、`level` 由 JSON `slog.Handler` 提供。`actor_id` 仅在已有认证查询成功时
出现；匿名请求不填空字符串，也不伪造身份。状态小于 400 使用 `INFO`，400--499
使用 `WARN`，500 及以上使用 `ERROR`。认证失败、版本冲突等预期 4xx 不记录为
内部错误。

### 3.2 路由和敏感信息

中间件对 `*http.ServeMux` 调用其公开的 `Handler(*http.Request)` 获取已注册模式，
而不是记录 `request.URL.Path`。未匹配请求使用固定的 `"<unmatched>"`。因此资源
ID只以 `{id}` 模板出现，query string、请求体、cookie、Authorization、数据库 URL、
完整 NAS 路径、用户名密码、音频和封面内容都不会进入事件。请求 ID仍沿用现有
字符集和长度限制，并写回 `X-Request-ID` 响应头。

### 3.3 ResponseWriter 兼容性

包装器记录第一次 `WriteHeader` 或隐式 200，并累计实际写入字节；重复
`WriteHeader` 不覆盖首次状态。为不破坏当前静态资源服务，包装器透传当前可能使用
的 `http.Flusher`、`http.Hijacker`、`http.Pusher`、`io.ReaderFrom`，并提供
`Unwrap` 供 `http.ResponseController` 使用。包装器不缓存或检查响应 body。

日志写入在 `defer` 中完成；日志失败不改变响应、事务或 panic 行为。若 handler
发生 panic，先记录状态为 500 的完成事件，再按现有服务器语义重新抛出，不在事件中
写入 panic 文本或堆栈。

## 4. 测试设计

后端新增中间件测试，使用内存 `slog` JSON handler 和 `httptest`：

- 隐式 200、显式 204、`http.Error`/500 的状态和字节统计；
- method、ServeMux route template、非负耗时和恰好一个完成事件；
- 合法/超长请求 ID 的传播与替换；
- 认证上下文中的 actor ID，以及匿名请求不出现 actor；
- query/path/body/token 等敏感值不出现在日志；
- `Flush`/静态文件写入不会改变响应行为。

CI 配置本身通过逐 job 的命令失败语义和 Compose/脚本语法校验验证，不引入新的
工作流测试框架。现有 Go、前端和 PostgreSQL 集成测试继续作为回归基线。

## 5. 兼容性、风险与回滚

- 不改变任何 REST endpoint、错误 envelope、认证授权、扫描、迁移或数据库 schema。
- 若 CI 工作流配置错误，可单独回滚 `.github/workflows/ci.yml`；本地质量命令和
  集成脚本不受影响。
- 若日志字段或包装器实现导致回归，回滚中间件/安全上下文改动即可，不需要数据库
  forward-fix。先以现有 request ID 测试和 HTTP handler 测试作为回滚前基线。
- GitHub runner 的 Docker 启动时间是主要运行成本；通过 job 并行、固定超时和脚本
  清理控制资源。Action SHA 级供应链锁定、依赖版本固定和完整 metrics/tracing
  留作独立后续任务。
