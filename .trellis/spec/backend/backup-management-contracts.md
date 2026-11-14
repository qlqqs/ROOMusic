# 备份管理合同（删除、批量删除与保留规则）

> 来源任务：`.trellis/tasks/archive/2026-11/11-06-settings-page-and-user-menu`（`design.md` §1、§1b、§1c）。备份的生成、调度、校验与恢复见
> [部署环境与 CI 合同](./deployment-environment-contracts.md)“环境与资产”。

## 1. 范围与触发

- 管理员在“设置 › 备份”删除单份或多份备份，并设置自动清理规则（保留天数、保留份数）。
- 触发：新增三个写接口、Overview 增加 `retention` 字段、备份目录新增规则文件。改动这些接口、规则文件格式或清理算法时更新本文件。

## 2. 接口签名

| 方法与路径 | 说明 |
| --- | --- |
| `DELETE /api/v1/system/backups/{name}` | 单删；200 返回最新 `Overview` |
| `POST /api/v1/system/backups/deletions` | 批量删；体 `{"names": [...]}`，1–500 个 |
| `PUT /api/v1/system/backups/retention` | 保存规则；体 `{"keep_days", "keep_count", "confirm"}` |
| `GET /api/v1/system/backups` | 现有接口；`Overview` 多 `retention` |

三个写接口都先 `RequireSameOrigin` 再 `RequireAdmin`（跨源的匿名请求得到 403 `origin_*` 而不是 401）。Service 入口：
`DeleteBackup(ctx, name, actorID)`、`DeleteBackups(ctx, names, actorID)`（`files.go`），`SaveRetention(ctx, rule, confirm, actorID)`、`retain()`（`retention.go`）。

## 3. 合同

- **文件安全**：名字先过 `FileNamePattern` 白名单，再经 `os.OpenRoot(备份目录)` + `root.Lstat`；不是普通文件（符号链接、目录、FIFO）一律按不存在处理，链接与目标都不动。
  `.partial` 临时文件、状态文件、规则文件的名字不匹配白名单，删不到。
- **批量响应**：`{"deleted": [...], "missing": [...], "failed": [...], "overview": {...}}`，每个名字（去重后、保持请求顺序）恰好进一个名单；
  `missing`（不合规、不存在、非普通文件）按成功处理；部分或全部失败仍是 200。
- **规则**：`Retention{keep_days *int (1–3650), keep_count *int (1–1000)}`，`null` 表示不限；两项都是上限。
- **规则文件**：备份目录里的 `.roomusic-backup-retention.json`（`{"version": 1, "keep_days", "keep_count", "saved_at"}`，0600，临时文件 + rename 原子替换）。
  不放数据库：恢复数据库不会把规则改回旧值；迁移前备份时新表可能还没建。不存在时有效规则是 `{keep_days: ROOMUSIC_BACKUP_KEEP_DAYS, keep_count: null}`。
  文件损坏（JSON 错、版本错、缺键、超范围、超 4096 字节、非普通文件）时**不回退**环境变量：Overview 的 `retention` 为 `null`，自动清理跳过并记 `backup.retention_unreadable`。
- **清理算法** `retentionPlan(files, rule, now)`：文件按新到旧；第一份永远保留；其余“严格早于 now − N 天”或“下标 ≥ M”任一成立就删；四种来源一起计。
  备份成功后的 `retain()` 与 `SaveRetention` 共用它，并由 `retentionMu` 串行；与手动删除、CLI 进程不加锁，`ENOENT` 静默跳过。
- **保存语义（D6）**：在同一临界区内算计划；计划非空且 `confirm` 不是 `true` 时返回 `{"confirmation_required": true, "deletions": n}`，
  **不写规则文件、不删任何文件**；否则写规则文件，再按那一刻的计划清理，返回 `{"confirmation_required": false, "deleted", "failed", "overview"}`。
  确认后的实际删除数可能与确认框里的数不同，以响应为准。
- **日志**：`backup.deleted`（`file`、`actor_id`）、`backup.delete_failed`（`file`、`reason`）、`backup.retention.saved`（`keep_days`/`keep_count`，不限记 `unlimited`，`actor_id`）、
  `backup.retention.deleted`（`file`、`trigger`=`backup`|`saved`）、`backup.retention_failed`、`backup.retention_unreadable`。不进“操作记录”。

## 4. 校验与错误矩阵

| 情形 | 结果 |
| --- | --- |
| 未登录 / 无 Origin / 跨源 / 非管理员 | 401 `unauthorized` / 403 `origin_required` / 403 `origin_forbidden` / 403 `forbidden`，不删任何文件、不改规则 |
| 单删：名字不合规、不存在、非普通文件、目录不存在 | 404 `backup_not_found` |
| 删除或写规则时文件系统报错（不可写、只读） | 503 `backup_directory_not_writable`（批量时进 `failed`，仍 200） |
| 批量：非数组、空、超过 500、含 `null` 或非字符串 | 400 `invalid_input` |
| 保留规则：缺 `keep_days`/`keep_count` 键、`0`、`3651`、`1001`、`1.5`、`"3"`、`confirm` 非布尔 | 400 `invalid_input`；JSON 不合法 400 `invalid_json` |
| 请求已取消（读 Overview 失败） | 503 `database_unavailable` |

## 5. 正确、基准与错误案例

- 正确：规则“30 天 / 3 份”，保存时先回 `confirmation_required`，用户确认后带 `confirm: true` 重提，只剩最新 3 份。
- 基准：从未保存规则，行为与 0.1.0 相同（只按 `ROOMUSIC_BACKUP_KEEP_DAYS` 天清理）。
- 错误：规则文件损坏时退回环境变量——设过“不限”的管理员会被按 14 天删备份。

## 6. 必需测试

- 单元（`backend/internal/backup/deletion_test.go`、`retention_test.go`）：非法名与链接 / 目录 / FIFO 原物仍在；去重保序与三名单；`retentionPlan` 的天数边界、份数、不限、最新一份；规则文件损坏的各形态不回退且跳过清理；未确认时规则文件与备份都不变。
- 集成（`backend/internal/app/system_api_integration_test.go`）：三接口的授权 / 来源矩阵且文件不变；路径安全；校验矩阵；重启（新 Service 读同一目录）与真实数据库恢复后规则不变；非 root 时不可写 503（root 跳过）。
- 样本：`system/backup-deleted`、`backups-deleted`、`retention-confirmation-required`、`retention-saved`、`retention-saved-unlimited`，`errors/backup-deletions-invalid`、`backup-retention-invalid`。

## 7. 错误与正确写法

#### 错误

```go
// 先预览、再单独保存：两次请求之间来了新备份，预览说删 0 份，保存时却删了一份，用户没确认过。
plan := preview(rule); /* …返回前端… */ save(rule); applyRetention(currentPlan())
```

#### 正确

```go
s.retentionMu.Lock()
plan := retentionPlan(files, rule, s.now())
if len(plan) > 0 && !confirm {
	return RetentionResult{ConfirmationRequired: true, Deletions: len(plan)}, nil // 什么都不改
}
// 写规则文件，再按这一刻的 plan 清理
```
