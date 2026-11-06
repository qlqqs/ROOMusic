# 版本化镜像发布与双向迁移：执行计划

设计：[design.md](design.md)。依赖 [builtin-backup](../11-06-builtin-backup/implement.md) 的 B1–B4 已提交（`MigrationBackups`、`buildinfo` 读取函数、Dockerfile 基础）。
分工与通用约束同 builtin-backup 执行计划“所有派活 prompt 都要写明的约束”。

## 步骤

| 步骤 | 执行者 | 内容 | 设计章节 |
| --- | --- | --- | --- |
| V1 | astra | 版本注入：Dockerfile `ARG ROOMUSIC_VERSION` / `ROOMUSIC_COMMIT` 经 ldflags 写入 `buildinfo`、`roomusic --version`、启动日志；执行器：`.down.sql` 加载与校验（版本 ≥ 31 必须有，0001–0030 不得有）、回退列由执行器在**备份之后**添加、启动判定流程（升级 / 降级 / 拒绝）、`SchemaAheadError` 与 `RollbackFailedError`、`platform.migration.*` 日志、删除不可达的 `future version` 分支；Dockerfile 多架构兼容（固定多架构索引摘要）；`ci.yml` 加 `workflow_call` 与 actionlint；`.github/workflows/release.yml`；`scripts/release-check.sh` | §2、§3、§6、§7 |
| V2 | Sol | 按 §4、§11 编写并运行：执行器单元测试、`TestPostgreSQLMigrationRollback*` 集成测试（含不可回退、不连续、回退报错整体回滚、回退后再升级、库缺回退列 + 有待执行迁移时迁移前备份不挂起）、往返快照测试框架与 `TestPostgreSQLSchemaSnapshotDetectsChanges`、`buildinfo` 与 `--version`；actionlint；本机 `docker build`（amd64，带版本参数）后 `--version` 输出正确 | §4、§11 |
| V3 | 主会话 | 审阅；通过后提交 | — |

升降级演练（A5）在 config-trim 完成后与过渡演练一起做，见 [父任务执行计划](../11-06-deploy-ux/implement.md) P4。

## 验证命令（最小集）

```bash
cd backend && gofmt -l . && go vet ./... && go test ./internal/platform/... ./cmd/roomusic/
./scripts/test-integration.sh            # 须看到 TestPostgreSQLMigrationRollback*、TestPostgreSQLSchemaSnapshotDetectsChanges 为 PASS
docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.12 -color
```

## 回滚点

- V1 是一次提交；执行器改动有问题时整体回退该提交，已发布迁移文件不受影响（本任务不新增业务迁移）。
