# PostgreSQL 迁移执行器实施计划

## 变更边界

- 最小行为缺口：启动时重复执行所有 SQL，且无法发现并发、漂移和半升级。
- 行为真实拥有者：`backend/cmd/roomusic/database.go` 的数据库启动边界；迁移
  schema 变化由 `backend/migrations/` 显式文件拥有。
- 预期文件：`database.go`、新增 `0008_migration_metadata.sql`、迁移单元/集成
  测试、数据库规范/运行合同、README（说明迁移运维合同）及本任务文档。
- 明确不改：0001--0007、业务 API、扫描/前端、Redis/Meilisearch、第三方迁移
  依赖和 down migration。

## 有序清单

1. [x] 实现迁移描述符加载：版本解析、排序、重复/格式校验、原始字节 SHA-256；
       保留 `applyMigrations` 外部调用点并抽出可测试 FS 边界。
2. [x] 增加 0008 元数据迁移，设计并实现旧 `schema_migrations` 1/6/7 形态的
       一次性基线和名称/哈希漂移检查。
3. [x] 将全套执行包入 `BeginTx` 与固定 `pg_advisory_xact_lock`，显式处理
       `Query`/`Exec`/`Commit`/`Rollback`/context 错误；成功提交后再恢复扫描。
4. [x] 编写无数据库测试覆盖文件发现、哈希、重复和非法版本；编写 PostgreSQL
       18 集成测试覆盖 fresh、rerun、旧库升级、漂移/未知版本、回滚、并发和取消。
5. [x] 更新 README、`database-guidelines.md`、`core0-runtime-contracts.md` 与
       架构决策文档，记录命令、锁 key 语义、SHA-256、基线假设和 forward-fix 回退。
6. [x] 运行格式化、局部 Go 测试、真实集成门禁、静态检查和 Compose 配置校验；
       通过后执行 Trellis quality check 和 spec update。

## 验证命令

```bash
gofmt -w backend/cmd/roomusic/database.go backend/cmd/roomusic/database_migration_test.go
go -C backend test ./... -run 'Migration|PostgreSQLMigration' -count=1
go -C backend test ./... -count=1
go -C backend vet ./...
go -C backend build ./...
./scripts/test-integration.sh
bash -n scripts/*.sh
docker compose -f compose.test.yaml config --quiet
git diff --check
```

若 Docker/PostgreSQL 不可用，必须明确记录集成测试未执行，不能把环境变量缺失
导致的 skip 当作通过；实现仍需先用无数据库测试验证纯逻辑。

## 风险文件与回滚点

- `database.go`：事务/锁/基线分支最易影响启动；每次重构后先跑单元和 fresh 集成。
- `0008_migration_metadata.sql`：只能追加，不能修改已提交版本；若线上失败，
  依靠事务自动回滚后修正执行器或新增 0009 forward-fix。
- 集成夹具：必须使用隔离 schema 和独立连接，不能触碰生产 `.env` 或业务卷。
- 若发现旧库历史不满足连续版本假设，保持 fail closed，记录恢复步骤，不扩大
  自动修复范围。

## 启动前检查

- [x] `prd.md`、`design.md`、本文件已完成并通过用户最终规划摘要批准。
- [x] `implement.jsonl` 与 `check.jsonl` 已填入真实规范上下文。
- [x] `python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-02-postgres-migration-governance`
      已通过并完成 `task.py start`。

## 完成记录

- 实现：`database.go` 增加嵌入迁移发现、连续版本校验、原始字节 SHA-256、事务级
  advisory lock、旧 1/6/7 基线、漂移/未知版本拒绝和提交前 tracking 表复核。
- 迁移：新增 `0008_migration_metadata.sql`，为 `schema_migrations` 增加 `name`
  与 `checksum` 列。
- 测试：新增 fresh、rerun、legacy、0008 已提交缺行、漂移/未来版本、迁移期间
  新增未知版本、回滚后连接复用、并发锁和锁取消集成覆盖。
- 验证记录：
  - `go -C backend test ./... -count=1`
  - `go -C backend test ./... -race -count=1`
  - `go -C backend vet ./...`
  - `go -C backend build ./...`
  - `./scripts/test-integration.sh`（PostgreSQL 18）
  - `bash -n scripts/*.sh`
  - `docker compose -f compose.test.yaml config --quiet`
  - `git diff --check`
