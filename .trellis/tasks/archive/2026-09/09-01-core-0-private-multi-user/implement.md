# Core 0 私有多用户执行计划

1. 添加迁移与模型约束：角色、禁用时间、索引及既有数据默认管理员。
2. 重构认证上下文和授权 helper，更新 setup、login、me 及所有现有 handler 的权限矩阵。
3. 实现用户列表、创建、启用/禁用和会话撤销 API，补充稳定错误码与审计日志字段。
4. 更新前端 DTO、管理员用户管理控件和普通用户只读视图。
5. 编写后端权限矩阵、禁用/撤销即时生效、最后管理员保护测试；更新前端 API/交互测试。
6. 运行最小验证：`go test ./backend/...`、`go vet ./backend/...`、`npm --prefix frontend run lint`、`npm --prefix frontend run typecheck`、`npm --prefix frontend run test`。

## 风险文件

- `backend/cmd/roomusic/application.go`、`security.go`、`backend/migrations/*`、对应测试文件。
- `frontend/src/api.ts`、`frontend/src/main.tsx` 及测试。

## 激活前检查

- PRD、设计和执行计划已审查；`implement.jsonl` 与 `check.jsonl` 已包含真实上下文条目。
- 用户明确批准最终规划摘要后，才运行 `task.py start`。
