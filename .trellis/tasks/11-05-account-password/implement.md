# 账户与密码：执行计划

需求见 [prd.md](prd.md)，合同与结构见 [design.md](design.md)。子 agent 不提交 git，每步由主会话验收后提交。测试按 `AGENTS.md` 的范围约束：每步只跑相关的最小集合，收尾时跑一次真实服务用例。

## 步骤

| 步骤 | 执行者 | 内容 | 依据 |
| --- | --- | --- | --- |
| 1 后端 | GPT-6-Astra（high） | `identity/passwords.go`；两条密码路由；`postgres.Connect`；`config.DatabaseURL`；`cmd/roomusic` 的子命令分派与 `reset_password.go`；`go get golang.org/x/term@v0.45.0`；`scripts/reset-password.sh`；已忽略候选的列表函数与 `ignored` 参数。只写实现，不写新测试；保证编译、`go vet` 与既有相关测试通过 | design §1–§3 |
| 2 前端 | Claude Opus 5.5（high） | auth 的改密码对话框与侧栏入口；members 的重置对话框与表格按钮；governance 候选页的“待处理 / 已忽略”与恢复；decoder；fake API。文案按 design §4.4 与全局文案规则。只写实现，不写新测试；保证 typecheck、lint 与既有相关测试通过 | design §4 |
| 3a 后端测试 | GPT-6.1-Sol（high） | 5.1 的 PostgreSQL 集成测试、5.2 的命令行测试、`routes_test.go`、1.4 的 8 个样本录制；执行并回报 | design §1.4、§5.1–§5.2 |
| 3b 前端测试 | GPT-6.1-Sol（high） | 5.3 的 Vitest；`api-contract.test.ts` 登记 8 个样本；5.4 的模拟 Playwright（含五宽、axe）；5.5 的 `e2e/real/account.real.ts`；执行并回报 | design §5.3–§5.5 |
| 4 收尾 | 主会话 | design §7 的合同、规范、路线图与 README 同步；导出树验证一次；真实服务用例一次（由 GPT-6.1-Sol 执行）；截图复核；实施结果写回 PRD；归档 | design §7 |

步骤 1 与 2 并行：前端按 design 的合同写，先用 fake API。3a 在步骤 1 验收后开始；3b 在步骤 2 与 3a 验收后开始（要用 3a 录制的样本）。测试发现的实现缺陷打回给对应的实现 agent 返工。

## 每步的验证命令（最小集合）

- 步骤 1：
  - `cd backend && gofmt -l . && go vet ./... && go build ./...`
  - 既有相关测试：`go test -race -count=1 ./internal/identity/... ./internal/platform/postgres/... ./internal/platform/config/... ./cmd/roomusic/...`
- 步骤 2：
  - `cd frontend && npm run typecheck && npm run lint`
  - 与改动直接相关的既有 Vitest 文件
- 步骤 3a：
  - 步骤 1 的命令，加上隔离测试库上的 `go test -race -count=1 ./internal/app/ -run 'Password|Identity|Routes|APISamples'`、`./internal/catalog/governance/ -run 'ArtistIgnoredCandidates|ArtistCandidate'`、艺人样本测试与 `./cmd/roomusic/...`
  - `go test ./internal/... -run DependencyRules`（包依赖规则）
- 步骤 3b：
  - 新增和改动的 Vitest 文件、`src/test/api-contract.test.ts`
  - `npx playwright test e2e/auth.spec.ts e2e/members.spec.ts e2e/artists-governance.spec.ts e2e/responsive.spec.ts e2e/a11y.spec.ts --retries=0`
- 步骤 4（只跑一次）：
  - 对 `git archive` 导出的干净树执行 `npm ci` 和构建，确认内嵌资产
  - `scripts/playback-e2e.sh` 跑 `account.real.ts` 和既有的 real 用例（`--retries=0`）
  - 1440 与 375 两种宽度下截图复核：身份区、两个对话框、成员表、候选页两个视图

## 审查关口

- 步骤 1 验收：
  - 新增 handler 里没有 SQL；
  - 命令行不调用 `postgres.Open`，输出里没有密码和数据库地址；
  - `go.mod` 只多了 x/term 和 x/sys，`go` 版本不变；
  - 不带 `ignored` 参数时，候选列表的 SQL 一字不变。
- 步骤 2 验收：
  - 当前密码错误（403）不跳转到登录页；
  - 管理员自己那一行没有“重置密码”；
  - 文案没有防御性和多余的说明。
- 步骤 3a / 3b 验收：
  - 8 个样本都已登记；
  - Playwright 用 `--retries=0` 通过；
  - 真实服务用例不改 `playback-admin` 的密码。

## 风险与回滚点

- `cmd/roomusic/main.go` 的分派：不带参数时必须走原来的路径，`scripts/prod.sh`、`dev.sh` 不受影响。
- `postgres.Open` 拆成 `Connect` 加迁移：迁移测试必须保持通过。
- 每一步单独提交，出问题按提交 revert。没有迁移，回滚不涉及数据。
