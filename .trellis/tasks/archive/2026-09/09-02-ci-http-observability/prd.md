# CI 门禁与 HTTP 可观测性

## Goal

为 ROOMusic 建立每次提交都能重复执行的质量门禁，并让每个 HTTP 请求产生
可关联、可检索且不泄露敏感信息的结构化完成事件。完成后，回归失败能够在
合并前被发现，运行人员能够用 `request_id`、路由模板、状态码和耗时定位
接口问题，而不必从散乱的启动日志或数据库记录猜测。

## User Value

- 贡献者在本地和远端使用同一组命令验证 Go、React、迁移集成和生产资产。
- 合并请求不会把未运行的 PostgreSQL 事务回归或失配的内嵌前端资产带入主干。
- 运维人员可以按请求关联 ID 区分成功、客户端错误和服务端错误，并看到安全的
  路由模板、响应状态、响应字节数和耗时。

## Confirmed Facts And Constraints

- 代码仓库的远端是 GitHub；目前没有 `.github/workflows/`，因此远端没有自动 CI
  门禁。
- 后端模块位于 `backend/`，前端模块位于 `frontend/`；Go module 不在仓库根目录。
- 现有可重复验证入口包括 `go -C backend ...`、`npm ci`/前端 npm scripts、
  `./scripts/test-integration.sh` 和 `docker compose -f compose.test.yaml config`。
- 集成脚本只启动 PostgreSQL 18，并在默认情况下清理专用容器和数据卷；Redis 和
  Meilisearch 不是 Core 0 的运行或 CI 必需依赖。
- `backend/cmd/roomusic/main.go:118-128` 的当前中间件只在请求进入前记录
  `request_id`、method 和原始 URL path，尚未记录最终状态码、耗时、路由模板或
  安全 actor ID。
- 请求 ID 已有长度和字符集上限；日志规范禁止记录查询字符串、请求体、cookie、
  token、数据库 URL、完整 NAS 路径和音频/封面内容。
- `frontend/package.json` 使用 `latest` 版本范围；本任务使用已有
  `package-lock.json` 和 `npm ci` 保证 CI 安装可复现，不在本任务内承担依赖升级或
  版本治理。

## Requirements

### R1. 可重复的 CI 门禁

新增仓库级 GitHub Actions CI 工作流，至少在 pull request 和
推送到主分支时执行，并提供手动触发入口。工作流必须：

1. 使用 `.mise.toml` 约定的 Go/Node 主版本（具体版本在设计阶段固定）；
2. 使用 `npm ci`，而不是无锁安装；
3. 执行后端格式检查、单元测试、race（适用时）、vet 和 build；
4. 执行前端 lint、typecheck、Vitest 和 production build；
5. 校验 shell 语法、Compose 测试配置，并显式运行 PostgreSQL 18 集成入口；
6. 确认 production build 后生成的 Go 内嵌静态资源没有未提交漂移；
7. 不启动、不依赖 Redis/Meilisearch，不读取生产 `.env` 或真实音乐库；
8. 失败时返回非零状态，并让每个门禁步骤在 CI 日志中清楚可辨。

### R2. HTTP 完成事件

将现有请求中间件改为在请求结束后发出一个稳定 schema 的结构化 JSON 事件：

- `event` 固定为 `http.request.completed`，`module` 固定为 `platform`，并保留
  `message`、UTC 时间戳和已有 `request_id`；
- 记录 HTTP method、ServeMux 路由模板、最终 status、响应字节数和
  `duration_ms`；路由模板不可退化为包含查询字符串或完整私有文件路径的原始 URL；
- 对成功、4xx 和 5xx 使用与日志规范一致的等级，不把预期的认证失败或版本冲突
  当作未分类的内部错误；
- 在已有认证边界成功识别用户时，可记录稳定且非敏感的 `actor_id`；未认证请求
  不伪造 actor，且绝不记录 session token、cookie 或用户名密码；
- 保证隐式 200、显式状态、空响应和 `http.Error` 都能得到正确的状态/字节统计；
- 保留请求 ID 响应头和现有错误 envelope 的关联行为；日志记录失败不得改变 HTTP
  响应或提交结果。

### R3. 测试与契约

- 增加后端可回归测试，验证事件字段、路由模板、状态/字节/耗时、等级、请求 ID
  传播、actor 可选性和敏感字段不泄露。
- 为 CI 工作流覆盖关键失败边界（格式失败、前端检查失败、PostgreSQL 集成失败、
  生成资产漂移）；CI 不得把未配置数据库造成的测试 skip 当作集成通过。
- 更新 README、后端日志规范或运行合同中受本任务影响的命令和事件 schema；文档
  使用简体中文。

## Acceptance Criteria

- [ ] GitHub Actions 在 PR/主分支推送上执行全部选定门禁，且
      PostgreSQL 集成测试真实连接 PostgreSQL 18；任一步骤失败都会阻止工作流成功。
- [ ] CI 使用锁文件安装前端依赖、正确从 `backend/` 模块运行 Go 命令，并验证
      production build 生成资产无漂移。
- [ ] CI 在没有 Redis/Meilisearch、生产凭据或真实音乐目录时仍可完成 Core 0
      必需检查。
- [ ] 每个 HTTP 请求完成后恰好产生一个 `http.request.completed` 事件，包含
      request ID、method、路由模板、状态码、响应字节数和非负耗时；状态统计覆盖
      隐式 200、显式状态和错误响应。
- [ ] 事件不会包含 query string、请求体、cookie/session token、数据库 URL、
      密码、完整 NAS 路径或音频/封面内容；未认证请求不出现 actor ID。
- [ ] 现有 REST 响应、错误 envelope、`X-Request-ID`、认证授权、扫描和迁移行为
      不发生语义回归；现有局部测试和生产构建通过。

## Out Of Scope

- 不实现持久化扫描取消、跨进程任务队列、Redis/Meilisearch 接入或真实音频播放。
- 不把本任务扩展为完整 metrics/tracing、日志采集平台、告警规则或分布式 tracing。
- 不在本任务内大规模拆分 `frontend/src/main.tsx`/`api.ts`，也不引入 router、
  query-cache 或新的状态库。
- 不修改已共享的数据库迁移，不增加业务表或改变 REST 业务契约。
- 不在本任务内把 `package.json` 的 `latest` 依赖改成固定版本；单独依赖治理任务
  再处理升级策略和供应链审查。

## Risks And Deferred Items

- GitHub-hosted runner 的 Docker/Compose 行为和 PostgreSQL 集成耗时需要在工作流中
  明确超时与清理策略；本地脚本仍是唯一集成测试实现来源。
- `http.ResponseWriter` 包装若遗漏标准接口可能影响未来流式响应；设计阶段需明确
  需要透传的接口并用测试锁定。
- 目前认证函数分散在过渡单体 handler 中；actor 关联应通过窄的请求观测上下文或
  等价机制完成，不能让日志中间件成为新的授权入口。

## Confirmed Planning Decisions

- CI 执行平台确定为 GitHub Actions。
- 工作流在每次 Pull Request、推送到 `main` 以及手动触发时运行；PostgreSQL 18
  集成测试作为必需门禁，而不是可选的 nightly 检查。
- 采用多个清晰的 job（后端、前端、数据库/部署配置）并行执行；每个 job 复用
  仓库已有命令，避免在 CI 中复制一套只在远端有效的脚本。
- 前端依赖本任务只使用 `npm ci` 和现有 lockfile，不顺带升级或固定
  `package.json` 中的 `latest` 版本。

## Planning Decision Record

- 路线：human selection
- 选择者：用户
- 选择结果：采用 GitHub Actions，按 Pull Request、`main` 推送和手动触发运行完整门禁。
- 选择理由：与现有 GitHub 远端和 `scripts/test-integration.sh` 直接匹配，能在合并前
  真实验证 PostgreSQL 事务行为；接受 Docker 运行时和额外 CI 时长成本。

## Artifact Status

- `prd.md`：需求、约束、范围和验收标准已收敛。
- `design.md`：已写入 CI 拓扑、日志 schema、兼容性和回滚设计。
- `implement.md`：已写入有序清单、验证命令和 review gate。
- `implement.jsonl` / `check.jsonl`：已补充真实的后端、前端、日志和质量规范上下文。
- 任务仍为 `planning`；最终规划摘要获用户明确批准前，不执行 `task.py start`，也不
  修改产品代码。
