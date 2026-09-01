# Core 0 目录操作治理执行计划

1. 新增迁移，扩展 library root 生命周期字段并创建操作日志、幂等约束和索引。
2. 提取管理员目录操作的请求解析、canonical fingerprint、事务和错误映射辅助函数。
3. 改造目录新增，新增停用、恢复和操作历史查询端点；扫描器过滤停用目录。
4. 更新前端 DTO 与管理员操作界面，展示 revision、状态和可恢复错误。
5. 增加后端集成测试：原子提交、重复请求、幂等冲突、过期 revision、恢复冲突、普通用户拒绝和路径脱敏。
6. 运行验证：`go test ./...`、`go vet ./...`、`npm run lint`、`npm run typecheck`、`npm run test -- --run`、`git diff --check`。

## 风险文件

- `backend/migrations/0007_root_operations.sql`
- `backend/cmd/roomusic/application.go`、`scanner.go`、新增操作测试
- `frontend/src/api.ts`、`main.tsx`、相关测试

## 激活前检查

- `prd.md`、`design.md`、`implement.md` 已完成且无阻塞问题。
- `implement.jsonl` 与 `check.jsonl` 已加入共享、后端和前端规范条目。
- 获得最终规划摘要批准后再运行 `task.py start`。
