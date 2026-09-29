# F2：目录操作幂等指纹绑定目标目录

> 父任务：[Classical 重设计 / Core 1](../../../09-27-classical-core-1/prd.md)。2026-09-28 由[路线修订任务](../09-28-core-1-roadmap-revision/design.md)创建，2026-09-29 完成规划。
> 涉及事务与并发语义，按复杂任务管理：技术设计见 [design.md](design.md)，执行顺序与验证命令见 [implement.md](implement.md)。

## 目标与用户价值

管理员新增、停用、恢复目录时，带同一个 `Idempotency-Key` 重试，只能得到两种结果：原请求的记录结果，或明确的冲突。
不能拿到另一个目录的结果，也不能重复执行。修复后，M1a 的目录管理可以放心重试。

## 已核实事实

- 幂等键取自 `Idempotency-Key` 请求头（`backend/cmd/roomusic/root_operations.go:25`），指纹是规范化 JSON 的 SHA-256（`root_operations.go:27`）。
- `replayOperation`（`root_operations.go:33`–`61`）在事务外按 actor、操作类型和幂等键查记录（`:41`）：指纹一致就重放已存响应（新增返回 201，其余 200），不一致返回 409 `idempotency_conflict`（`:49`）。
- 停用指纹只有 `{status, expected_revision}`（`root_operations.go:151`），恢复指纹只有 `{expected_revision}`（`:220`），都没有路由中的 root ID。
  因此同一键加相同 body 用于另一个目录时，会重放前一个目录的响应，目标目录不变。新增的指纹是 `{path}`（`application.go:254`），不受影响。
  以上是静态推导，实施时先写复现测试。
- 唯一约束为 `UNIQUE(actor_id, operation_type, idempotency_key)`（`backend/migrations/0007_root_operations.sql:20`），与规范规定的键作用域“actor/操作”一致（`.trellis/spec/backend/database-guidelines.md:67`）。
- 并发时，两个同键请求都会在事务外查不到记录：
  - 停用/恢复：第二个请求在 `FOR UPDATE`（`root_operations.go:163`、`:232`）上等到第一个提交，随后因 revision 已变返回 409 `revision_conflict`；
  - 新增：第二个请求写操作记录时违反唯一约束，被映射为 409 `idempotency_conflict`（`root_operations.go:17`）。
  - 两种情况都不会重复执行，但重试拿不到原结果。
- 停用/恢复在访问数据库前不校验路由 ID（`root_operations.go:150`、`:219`）。ID 格式错误时，`$1::uuid` 转换失败被当作 503 `database_unavailable`。
  发行版本与扫描接口已用 `isValidIdentifier`（`catalog_api.go:853`）返回 400 `invalid_id`（如 `scans.go:178`）。
- 规范合同：
  - 同键同请求返回记录结果，同键不同输入返回 `idempotency_conflict`（`database-guidelines.md:67`–`69`，`agent-and-operation-guidelines.md:131`–`132`）。
  - ADR 0003 草案要求指纹包含目标身份，并要求同键并发请求收敛为一次执行（`docs/architecture/adr/0003-governed-operations.md:159`、`:164`、`:282`–`283`）。
- 现有 `TestPostgreSQLRootOperationTransactionsAndConflicts`（`auth_root_operations_integration_test.go:234`）覆盖以下场景：
  - 串行重放
  - 新增键冲突
  - revision 冲突
  - 缺少逆向依据
  - 记录写入失败时回滚
- 集成入口 `./scripts/test-integration.sh` 运行 `-run 'TestPostgreSQL'`；本机 Docker 可用。

## 已裁决

| 编号 | 事项 | 选择 | 依据 |
| --- | --- | --- | --- |
| D1 | 旧记录兼容 | 不兼容。本任务之前记录的停用/恢复幂等键，改动后重试一律按指纹不一致返回 409；不新增迁移，不改写旧记录 | qlqq，2026-09-29：“不需要兼容，项目开发阶段有必要可以直接重构” |
| D2 | 同一幂等键用于另一个目录 | 返回 409 `idempotency_conflict`，不按目标区分键 | 由规范推出：键作用域为 actor/操作，不同输入即冲突（`database-guidelines.md:67`）。若改成按目标区分，需要改规范、唯一约束和迁移 |
| D3 | 同键并发请求 | 串行执行：只执行一次，其余同义请求在首个提交后拿到相同的记录结果 | 由规范推出：同键同请求返回记录结果；ADR 0003 草案要求并发收敛为一次执行 |
| D4 | 路由 root ID 格式错误 | 返回 400 `invalid_id`，与发行版本、扫描接口一致 | 现有模式。指纹需要规范化的目标身份 |

D2–D4 由规范或现有模式推出，随本规划一并批准。

## 需求与验收

| 编号 | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | 目标绑定 | 停用、恢复的指纹包含规范化（小写）的 root ID。用同一键和相同 body 停用或恢复另一个目录时，返回 409 `idempotency_conflict`；两个目录的状态、revision 和操作记录都不变，响应里没有前一个目录的数据 |
| R2 | 同目标重放不变 | 新增、停用、恢复使用同键同 body 同目标重试时，返回首次的状态码和响应，不重复执行；现有集成测试继续通过 |
| R3 | 并发收敛 | 多个同键请求同时到达时，只执行一次：<br>• 停用：8 个并发请求都返回 200 且响应相同，revision 只加 1，只有 1 条停用记录<br>• 新增：8 个并发请求都返回 201 且响应相同，只有 1 条新增记录<br>测试需强制请求真正重叠，不依赖调度运气 |
| R4 | ID 校验 | `PATCH /api/v1/library-roots/not-a-uuid` 和 `POST /api/v1/library-roots/not-a-uuid/restore` 返回 400 `invalid_id`，不进入幂等流程，不写记录 |
| R5 | 重放不绕过授权 | 执行者被降为普通用户后，用原键重放返回 403 `forbidden`，不返回已记录的结果 |
| R6 | 旧记录（D1） | 不写兼容分支，也不写旧记录测试；运行合同写明升级后旧键重试返回 409 |
| R7 | 文档同步 | 以下文档写明新合同，并把缺口标为已修复：<br>• `core0-runtime-contracts.md`：幂等合同、错误矩阵、技术债<br>• `database-guidelines.md`：指纹含目标、同键串行<br>• ADR 0003：缺口段落、渐进迁移第 1 项、第八节验收行、第九节第 4 项、决策记录<br>• 路线图风险段落与 F2 行<br>• 父任务表与架构图状态 |

## 范围外

- 记录失败的操作尝试（表允许 `failed`，但目前只写成功记录），以及通用操作、审批和 Journal：属于 M3。
- 改变键作用域、按目标区分键、修改表结构或新增迁移。
- 成员接口（`application.go:403`、`:491`）也未校验路由 ID，属于同类问题，另行立项。
- `root_operations.go:63` 的 `listRootsGoverned` 没有被引用，留给 B0 清理。
- 前端改动。

## 最终规划决策记录

- D1 由用户（qlqq）于 2026-09-29 裁决；D2–D4 由规范与现有模式推出，随最终规划摘要一并批准。
- 状态：用户于 2026-09-29 明确批准最终规划摘要，进入实施。

## 实施结果（2026-09-29）

- 复现：在未修改的代码上先运行新增测试，动态确认了静态推断的缺陷：
  - 跨目录停用/恢复返回 200，内容是前一个目录的响应，目标目录不变；
  - 路由 ID 格式错误返回 503；
  - 8 个同键并发新增得到 409 `idempotency_conflict`，并发停用得到 409 `revision_conflict`；
  - 降权后的执行者重放本来就返回 403，作为回归保护保留。
- 实现：`root_operations.go` 新增 `beginRootOperation`（READ COMMITTED 事务、事务级 advisory lock、事务内查记录），
  取代 `replayOperation`；新增、停用、恢复三个入口共用。停用/恢复用 `rootIDFromPath` 校验并小写化路由 ID，
  指纹加入 `root_id`。`application.go` 的 `addRoot` 改用同一入口。不改表结构，不新增迁移。
- 与设计的差异：advisory lock 的第二个键在 Go 里用 FNV-1a（32 位）计算，没有使用 PostgreSQL 内部函数 `hashtext`；
  锁的语义不变。
- 测试：`TestPostgreSQLRootOperationIdempotencyBindsTarget`（4 个子测试，覆盖 R1、R4、R5 及大小写规范化）与
  `TestPostgreSQLRootOperationConcurrentRetriesConverge`（新增、停用各 8 个并发请求，用 `LOCK TABLE` 强制重叠，
  超时时打印会话快照）。变异检查：去掉 advisory 锁后两个并发子测试都失败，恢复后通过。
- 文档：运行合同、数据库规范、ADR 0003（状态、决策记录、缺口、渐进迁移、验收行、第九节第 4 项）、路线图、父任务表、架构图已同步（R7）。
