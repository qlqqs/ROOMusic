# Core 0 目录操作治理

## Goal

为目录新增停用恢复增加 revision、幂等、审计事件和具体恢复语义，并以真实用例验证变更管理边界

## Requirements

- 前置依赖：首个切片的 library root 新增能力稳定，且多用户任务提供明确 actor/role。
- 增加 library root 停用和恢复，使用 expected revision 防止覆盖并发修改。
- 新增、停用和恢复接收作用域明确的 idempotency key；同 key 同请求返回既有结果，同 key 不同请求返回冲突。
- 为每次具体目录操作保存 actor、operation type、状态、before/after、revision、时间、错误分类和关联 ID。
- 恢复使用记录的 before state 与 expected revision；资源已继续修改时 fail closed。
- 先实现目录操作的具体模型；只有第二种真实持久化业务操作证明合同稳定后，才抽取通用 Change Set/Reversible Executor。

## Acceptance Criteria

- [ ] 新增、停用和恢复在资源状态、revision、幂等记录和审计事件之间保持原子性。
- [ ] 重放相同请求不产生重复副作用；复用 key 提交不同 payload 返回稳定冲突。
- [ ] 过期 revision 和恢复期间发生的新修改不会被覆盖。
- [ ] 管理员可以查询安全的目录操作历史，普通用户不能访问。

## Out of Scope

- Agent 审批状态机、任意工具注册、文件 checkpoint/quarantine、完整 Event Sourcing 和通用工作流引擎。

## Confirmed Facts

- 当前 `library_roots` 只有 `id`、`path`、`created_at`，新增接口使用路径唯一约束并在冲突时复用目录。
- 目录、扫描和诊断已由后端管理员授权；多用户任务已经提供 `admin` / `user` 角色和稳定 actor 身份。
- 数据库启动会按 `backend/migrations/*.sql` 顺序执行迁移，业务操作应在 PostgreSQL 事务内完成。

## Key Decisions

- 目录生命周期采用 `active` / `disabled` 两态，`revision` 从 1 开始，每次成功状态变更递增。
- `Idempotency-Key` 必须绑定 actor、操作类型和规范化 payload；同 key 同 fingerprint 返回原结果，异 fingerprint 返回 `idempotency_conflict`。
- 恢复只依据操作记录中的 before state，并要求资源 revision 与记录的 after revision 一致；冲突时 fail closed。
- 首个具体操作模型直接服务 library root，不提前抽取通用 Change Set 执行器。

## Open Questions

无阻塞问题。目录停用不删除来源数据；扫描器只读取 active 目录，既有来源保留用于诊断。
