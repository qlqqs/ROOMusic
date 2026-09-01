# Core 0 权限与目录事务验收测试

## Goal

以可重复的自动化测试证明 Core 0 多用户权限边界和目录操作治理的关键安全、一致性合同，为父任务最终归档补齐证据。

## Background

- 父任务 6 个产品子任务均已完成并归档，后端与前端质量命令通过。
- 最终集成审查确认目录治理 API 缺少专门测试，多用户管理端点也缺少完整权限矩阵测试。
- 当前 Go 测试主要使用 `httptest` 和纯函数测试，尚未引入 PostgreSQL 测试容器或 SQL mock 库。

## Requirements

- 测试普通用户可访问共享浏览/搜索资源，但对用户管理、目录管理、扫描启动和敏感诊断获得稳定 403。
- 测试禁用用户、撤销会话和过期/撤销 token 在下一次请求立即获得 401。
- 测试目录新增、停用、恢复在资源状态、revision、操作日志和幂等结果之间原子提交。
- 测试相同 actor/operation/key 和相同 fingerprint 重放原响应且不重复副作用；不同 fingerprint 返回 `idempotency_conflict`。
- 测试过期 revision、重复停用、恢复期间后续修改和缺少匹配 before state 均返回 `revision_conflict` 且不覆盖资源。
- 测试操作历史仅管理员可见，响应不含完整服务器路径、密码、session token 或数据库细节。

## Acceptance Criteria

- [x] 权限矩阵覆盖普通用户与管理员的核心读写端点，并断言稳定 401/403 错误码。
- [x] 禁用和会话撤销即时生效具有数据库集成测试。
- [x] 目录新增、停用、恢复、重放、幂等冲突、revision 冲突和恢复冲突具有 PostgreSQL 集成测试。
- [x] 失败操作不会留下部分资源状态或重复操作日志。
- [x] `go test ./...`、`go vet ./...`、前端现有质量门禁、生产构建和 Compose 配置验证全部通过。
- [x] 父任务最终验收结论更新为通过，并完成父任务归档。

## Out of Scope

- 新增产品功能、改变现有 REST 合同、引入通用测试平台、性能压测或浏览器端 E2E 框架。

## Key Decisions

- 使用真实 PostgreSQL 进行事务与约束测试；若本机 Compose PostgreSQL 可用，则通过隔离 schema/database 运行，不用 mock 代替事务证据。
- 权限路由测试与数据库状态测试共用最小测试 fixture，测试完成后清理隔离数据。
- 仅在测试揭示现有实现违反已批准合同时修复产品代码，并为修复保留回归断言。

## Open Questions

无阻塞问题。

## Goal

补齐多用户权限矩阵和目录治理幂等、revision、恢复冲突的自动化验收测试

## Requirements

- TBD

## Acceptance Criteria

- [ ] TBD

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
