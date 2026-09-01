# ROOMusic Core 0 最终集成审查

## 结论

Core 0 的 7 个交付子任务均已完成。最终集成验收通过，可以归档父任务。

## 验收证据

- 使用真实 PostgreSQL 隔离 schema 执行权限和目录事务测试，覆盖管理员/普通用户权限矩阵、禁用/撤销/过期会话即时失效、目录新增/停用/恢复、幂等重放与冲突、revision 冲突、恢复冲突、操作历史脱敏和事务回滚。
- 全新 schema 顺序执行全部迁移，确认 Core 0 只依赖 PostgreSQL；Redis 和 Meilisearch 未启动且不参与验收路径。
- 后端通过测试、静态检查和构建；前端通过 lint、类型检查、测试和生产构建；Compose 配置有效。
- 生产前端构建产物已与 React/TypeScript 源码同步，并继续由 Go 应用同源托管。

## 验收期间修复

- 修复 `0007_root_operations.sql` 中重复的 `request_id` 列，确保全新数据库可执行迁移。
- 修复 pgx 下 JSONB 参数绑定和 PostgreSQL 多态函数参数类型推断，确保目录操作日志可写入。
- 仅将 PostgreSQL 唯一约束错误映射为 `idempotency_conflict`，其他日志持久化失败返回 `operation_failed` 并回滚资源状态。

## 实际验证命令

```bash
ROOMUSIC_TEST_DATABASE_URL='postgres://roomusic:***@localhost:5432/roomusic?sslmode=disable' go test ./... -count=1
go test ./...
go vet ./...
go build ./...
npm run lint
npm run typecheck
npm run test -- --run
npm run build
docker compose config --quiet
git diff --check
```

## 范围确认

- 未引入 Redis、Meilisearch、GraphQL、Agent runtime、插件运行时或独立生产前端服务依赖。
- Music Steward 三种模式在 Core 0 中仍只保留产品与后端执行边界契约，不包含可运行 Agent。
