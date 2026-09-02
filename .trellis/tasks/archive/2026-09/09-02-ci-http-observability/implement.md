# CI 门禁与 HTTP 可观测性实施计划

## 变更边界

- 新增 `.github/workflows/ci.yml`，只编排现有 Go、Node、Compose 和 PostgreSQL
  测试命令。
- 修改 `backend/cmd/roomusic/main.go` 的 HTTP 观测中间件，并在同包测试中锁定
  状态、字节、耗时、路由模板、等级、actor 和脱敏行为。
- 仅在 `backend/cmd/roomusic/security.go` 增加被动 actor 观测写入点；不复制认证
  查询或改变授权逻辑。
- 同步更新 README、后端日志规范和必要的运行合同；不改数据库迁移、业务 API、
  扫描器、前端业务代码或 Redis/Meilisearch 依赖。

## 有序清单

1. [ ] 复核 `.mise.toml`、`package-lock.json`、`scripts/test-integration.sh` 和
       Compose 配置，确定 CI 版本、环境变量、清理和超时约束。
2. [ ] 新增 GitHub Actions workflow：PR/main push/manual triggers、只读权限、
       并发取消、Go/Node setup、后端/前端/集成 job 及失败时清理。
3. [ ] 在前端 job 使用 `npm ci` 和 production build，检查
       `backend/cmd/roomusic/web` 生成资产无漂移；在集成 job 显式运行 PostgreSQL
       18 入口、shell 语法和 Compose 配置校验。
4. [ ] 将 request ID 中间件改为完成时记录：安全 route template、最终状态、实际
       响应字节、非负耗时、稳定事件名和按状态分级；保留 header/error correlation。
5. [ ] 增加被动 observation context 与 actor ID 记录，确认中间件不进行数据库
       查询、不承担权限判断，匿名请求不泄露身份。
6. [ ] 实现兼容的 ResponseWriter 包装（隐式 200、错误响应和必要标准接口），补
       齐中间件/安全测试及敏感信息回归断言。
7. [ ] 更新 README、`logging-guidelines.md` 和 `core0-runtime-contracts.md`，
       记录 CI 入口、事件 schema、版本来源和脱敏边界；检查所有新增文档为简体中文。
8. [ ] 运行本计划的最小质量门禁和 PostgreSQL 集成门禁，执行 Trellis check；
       发现跨层契约偏差时先回到 planning 修正文档，不用补丁掩盖范围问题。

## 验证命令

开发机可执行的等价门禁：

```bash
cd backend && test -z "$(gofmt -l .)" && go test ./... -count=1
cd backend && go test -race ./... -count=1 && go vet ./... && go build ./...
cd frontend && npm ci && npm run lint && npm run typecheck
cd frontend && npm run test -- --run && npm run build
bash -n scripts/*.sh
PG_PASSWORD=ci-placeholder MEILI_MASTER_KEY=ci-placeholder docker compose config --quiet
docker compose -f compose.test.yaml config --quiet
./scripts/test-integration.sh
git diff --check
```

实现完成后还需在 GitHub Actions 运行一次 PR 或手动 workflow，确认 runner 上的
Docker、PostgreSQL 18、生成资产检查和 job 间并行关系真实可用；不能只凭本地 YAML
阅读宣称 CI 已通过。

## 风险文件与回滚点

- `.github/workflows/ci.yml`：action 版本、Go/Node 版本、Compose 插值和清理 trap
  影响远端门禁；可独立回滚，不触碰业务数据。
- `backend/cmd/roomusic/main.go`：ResponseWriter 包装可能影响静态文件/未来流式
  响应；每次修改后先跑中间件测试、生产 build 和 `go test -race`。
- `backend/cmd/roomusic/security.go`：actor 观测只能在认证成功后写入稳定 ID；不
  得引入第二套查询、权限默认值或 token 日志。异常时回退到无 actor 事件。
- 日志 schema：字段改名会影响运维检索；先保留旧 `X-Request-ID` 行为，再添加完成
  事件，避免同时发出两个不同 schema 的 HTTP 事件。

## 启动前 review gate

- [ ] `prd.md`、`design.md`、本文件已阅读，且 CI 平台决策已由用户确认。
- [ ] `implement.jsonl` 与 `check.jsonl` 均包含真实规范上下文。
- [ ] 验收标准可通过本地命令、PostgreSQL 18 集成和 HTTP JSON 日志测试观察。
- [ ] 明确未纳入扫描取消、真实播放、依赖升级、metrics/tracing 和前端大规模拆分。
- [ ] 规划最终摘要已呈现；只有用户随后明确批准实现，才运行 `task.py start`。

## 完成记录

- 实现提交：待主会话提交。
- CI 运行链接：待 GitHub Actions 首次执行后填写。
- 本地验证：已通过后端、前端、Shell、Compose、PostgreSQL 18 集成测试和
  `git diff --check`；GitHub Actions 首次远端运行仍需在推送后确认。
