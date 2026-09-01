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
