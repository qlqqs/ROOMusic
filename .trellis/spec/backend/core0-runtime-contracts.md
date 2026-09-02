# Core 0 当前运行合同

## 1. 范围与触发

本合同记录 Core 0 已落地且可回归验证的跨层实现：Go module 启动、
`/api/v1` REST、PostgreSQL 用户与目录状态、扫描终态、前端 decoder 以及
同源生产资产。它由启动失败、权限默认值、用户事务和扫描对账问题触发，
用于防止后续代理依据“尚未选择”的旧模板实现。

当前代码仍是过渡单体：后端位于 `backend/cmd/roomusic`，前端位于
`frontend/src`，尚未拆分为目标 `internal/*`/`features/*` 目录。

## 2. 接口签名

- 生产启动：`go -C backend run ./cmd/roomusic`，由 `scripts/prod.sh` 从仓库根目录调用。
- 开发启动：`./scripts/dev.sh`，默认读取 `.env.dev`，Go 运行在 `backend/`，
  Vite 通过开发配置代理 `/api` 到 `:8080`。
- HTTP：`POST /api/v1/users`、`PATCH /api/v1/users/{id}`、
  `POST /api/v1/library-roots`、`POST /api/v1/scans`。
- 用户更新请求：`{ "disabled": boolean }`；响应：`{ "disabled": boolean }`。
- 目录新增响应：`{ "id": string, "name": string, "status": "active" | "disabled", "revision": integer }`。
- 目录列表项目：`id`、安全 basename `path`/`name`、`status`、`revision`、时间戳；
  不返回原始绝对路径。
- 扫描状态枚举：`running | succeeded | failed | canceled | incomplete`。
- 前端请求入口：`requestApi(path, decoder, options?)`；所有成功响应先经过
  decoder，非 2xx 统一解码为 `ApiRequestError`。

## 3. 合同

### 请求与响应

- 状态变更请求必须通过 Origin 校验、HttpOnly session 和后端角色授权；前端
  角色仅控制展示。
- 错误 envelope 为 `{ "error": { "code": string, "message": string }, "request_id": string }`。
  消息不得包含 SQL、绝对路径、密码、token 或数据库 URL。
- `requestApi` 使用 `credentials: "include"` 和 JSON Content-Type；浏览器不
  读取或存储 session token。
- JSON 请求体上限 1 MiB；搜索词最多 200 字节；发行分页默认 `page=1`、
  `page_size=50`，最大 100；扫描诊断最多 100 条。

### 数据库与扫描

- PostgreSQL 是 Core 0 唯一业务权威，连接实现为 `database/sql + pgx/v5`。
- 启动迁移由 `backend/cmd/roomusic/database.go` 负责：嵌入的
  `backend/migrations/*.sql` 必须从版本 1 连续排序，原始字节使用 lowercase
  SHA-256；执行器在一个事务中持有 `pg_advisory_xact_lock(0x524f4f4d55534943)`，
  成功版本在 `schema_migrations` 中记录文件名、校验和与时间。
- `0008_migration_metadata.sql` 为迁移记录增加 `name`/`checksum`。旧版数据库
  可能只有 0001、0006、0007 三条记录；确认这段连续历史后，0002--0005 只做一次
  元数据基线，不重放 SQL。名称或校验和漂移、未发布版本和无法证明连续历史均
  fail closed；不提供 down migration，生产回退使用备份或 forward-fix。
- 迁移 SQL、DDL 和记录写入同一事务；锁等待取消或任一步失败都会回滚，只有
  提交成功后才执行中断扫描恢复并进入 ready。
- 用户禁用/启用在同一事务中锁定启用管理员集合和目标用户，检查最后管理员，
  更新 `disabled_at`，并在禁用时撤销该用户未撤销 session。
- 目录新增对已存在路径执行幂等 upsert，但不隐式恢复 disabled 状态；响应和
  Operation Journal 使用数据库真实 `status`/`revision`。
- 扫描只读取 `active` roots。unsupported 音频候选、CUE/解析/权限/遍历错误
  将 root 标记为不完整；只有全局 `succeeded` 才允许 `missing` 对账。非音频
  附件（例如 `.jpg`、`.png`、`.txt`）不产生 unsupported 音频诊断。
- `saveObservation` 按 Release 内 `disc_number` 选择或创建 Medium；同一 root
  与规范化相对路径继续复用 Track 身份。
- `canceled` 保留 wire 枚举，但当前没有用户取消端点；扫描 goroutine 仍由进程
  拥有的背景上下文运行。

### 环境与资产

- Node/Go 版本由 `.mise.toml` 管理，依赖安装使用 `npm ci`；`package-lock.json`
  是前端可重复安装依据。当前 `package.json` 的 `latest` 依赖是待治理风险。
- 生产 Vite 构建写入唯一的 `backend/cmd/roomusic/web`；该目录是生成资产，
  不手工编辑。开发 `vite.config.dev.ts` 才允许自定义 Host 和 API 代理。
- `allowedHosts: true` 只放宽 Vite Host 校验；后端仍按
  `ROOMUSIC_PUBLIC_URL`、Origin scheme/host 和 Secure Cookie 校验写请求。
- `scripts/dev.sh` 默认只确保 PostgreSQL、Go 和 Vite；Redis/Meilisearch 是
  Compose/mise 可选服务。`scripts/prod.sh` 不启动 Node 服务。

### CI 质量门禁

- `.github/workflows/ci.yml` 在 Pull Request、推送到 `main` 和手动触发时运行，权限
  固定为 `contents: read`，同一分支的新运行会取消旧运行。
- 工作流使用 `.mise.toml` 中的 Go `1.25.10` 和 Node.js `24.16.0`。后端门禁从
  `backend/` module 运行 `gofmt`、`go test`、`go test -race`、`go vet` 和 `go build`；
  前端使用 `frontend/package-lock.json` 执行 `npm ci`、lint、typecheck、Vitest 和
  production build。
- production build 后必须执行 `git diff --exit-code -- backend/cmd/roomusic/web`，
  确认 Go 内嵌生成资产没有漂移。集成 job 执行 `bash -n scripts/*.sh`、两个 Compose
  配置校验和 `./scripts/test-integration.sh`；后者真实启动 PostgreSQL 18 并在结束时
  清理专用容器和数据卷。
- CI 不启动或依赖 Redis/Meilisearch，不读取生产 `.env`，也不使用真实音乐目录。普通
  测试因未设置 `ROOMUSIC_TEST_DATABASE_URL` 而跳过时，不得被视为 PostgreSQL 集成门禁
  通过。

### HTTP 完成日志

请求中间件在每个请求结束后恰好写出一个 `http.request.completed` 结构化 JSON 事件。
事件字段为：

| 字段 | 合同 |
| --- | --- |
| `event` | 固定为 `http.request.completed` |
| `module` | 固定为 `platform` |
| `message` | 固定为 `http request completed` |
| `request_id` | 有界且经过字符集校验，并回写 `X-Request-ID` |
| `method` | HTTP 方法 |
| `route_template` | ServeMux 注册的路由模板；未匹配时为 `<unmatched>` |
| `status` | 最终状态码；隐式响应默认为 200 |
| `response_bytes` | 实际写入字节数 |
| `duration_ms` | 非负耗时（毫秒） |
| `actor_id` | 可选稳定用户 ID，仅认证成功时出现 |

JSON `slog` handler 另外提供 UTC `time` 和 `level`。状态小于 400 记录为 `INFO`，
400--499 为 `WARN`，500 及以上为 `ERROR`。日志中间件不查询数据库、不执行权限判断；
日志失败不得改变响应、事务或 panic 语义。事件不得包含 query string、请求体、Cookie、
Authorization/session token、密码、数据库 URL、完整 NAS 路径或音频/封面内容；匿名请求
不填充 `actor_id`。

## 4. 校验与错误矩阵

| 条件 | 行为 |
| --- | --- |
| session 缺少/未知 `role` | decoder 拒绝，不能默认为 admin |
| 用户目标不存在 | `404 not_found`，事务回滚 |
| 禁用最后一个启用管理员 | `409 last_admin`，不改用户或 session |
| 用户 SQL/事务失败 | `503 database_unavailable` 或分类错误，不返回成功 |
| 重复注册 disabled root | 返回 `status=disabled` 与真实 revision，不隐式恢复 |
| unsupported 音频/CUE/遍历错误 | 有界诊断，扫描 `incomplete`/`failed`，禁止 `missing` |
| 扫描成功且所有 root 完整 | 允许负向 `missing` 对账 |
| 非音频附件 | 忽略，不改变扫描完整性 |
| 前端非 2xx 或 malformed JSON | `ApiRequestError` 安全回退并保留 request ID |

## 5. 正确、基准与错误案例

- 正确：管理员状态变更锁定同一组管理员行，session 撤销与 `disabled_at` 一起提交；
  两个并发禁用请求至多成功一个。
- 基准：重复 POST 已停用目录返回 disabled；管理员随后使用 restore 端点显式恢复。
- 正确：多碟来源按 `disc_number=1/2` 形成两个 Medium，重扫保持 Track ID。
- 错误：把 `role` 缺失解释为 admin；把 root 原始路径放入列表 JSON；unsupported
  文件仍让扫描 succeeded 并执行 missing；忽略 `Exec` 错误后返回 200。

## 6. 必需测试

- 前端 `api.test.ts`：session role 缺失/未知、创建/更新用户、root active/disabled、
  malformed payload 和错误 envelope fallback。
- 后端单元：终态映射、unsupported 音频候选识别、来源身份规范化和路径 containment。
- PostgreSQL 集成（配置 `ROOMUSIC_TEST_DATABASE_URL` 时）：用户禁用与 session
  原子性、最后管理员/并发保护、未知用户 404、目录幂等与真实状态、扫描 incomplete
  禁止 missing、多 Medium 与 Track 身份稳定。
- HTTP 中间件：隐式 200、显式状态、错误响应的状态/字节统计，路由模板、耗时、日志
  级别、请求 ID 回写、可选 actor_id 以及敏感字段脱敏；每个请求只能有一个完成事件。
- 运行门禁：`npm run lint`、`npm run typecheck`、`npm run test`、
  `gofmt -l .`、`go test ./... -count=1`、`go vet ./...`、`go build ./...`、
  `go test -race ./... -count=1`、`bash -n scripts/*.sh`、两个 Compose 配置校验、
  `./scripts/test-integration.sh` 和 `git diff --check`（按改动取最小集合）。

## 7. 错误与正确示例

### Wrong

```go
_ = db.ExecContext(ctx, "UPDATE users SET disabled_at=NOW() WHERE id=$1", id)
writeJSON(w, 200, map[string]bool{"disabled": true})
```

这会吞掉数据库错误，也没有最后管理员保护或 session 原子撤销。

### Correct

```go
tx, err := db.BeginTx(ctx, nil)
// lock enabled admins and target, validate last-admin invariant
// update user and revoke sessions in the same tx
if err == nil {
    err = tx.Commit()
}
if err != nil {
    _ = tx.Rollback()
    writeAPIError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法更新用户")
    return
}
writeJSON(w, http.StatusOK, map[string]bool{"disabled": requested})
```

前端同样必须调用 `requestApi(..., decodeUpdatedUser)`，禁止把 `unknown` 直接
cast 成 `UserDTO`。

## 后续技术债

- 扫描取消仅保留枚举，尚无持久化取消端点或跨进程任务队列。
- HTTP 请求现在通过 `http.request.completed` 事件记录状态码、耗时和路由模板；用户
  创建/禁用/会话撤销仍无独立 Operation Journal。
- `frontend/src/main.tsx` 与 `api.ts` 仍是 Core 0 过渡单体，feature/router/query
  cache 拆分需单独任务。
