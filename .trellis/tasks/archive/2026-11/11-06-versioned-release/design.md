# 版本化镜像发布与双向迁移：技术设计

- 父任务：[部署体验改进](../11-06-deploy-ux/prd.md)；需求：[prd.md](./prd.md)（R1–R9、A1–A6）
- 依据：[后端调研](../11-06-deploy-ux/research/backend-facts.md) §1、§2、§5、§7
- 衔接：[builtin-backup 设计](../11-06-builtin-backup/design.md)（迁移前备份端口、版本接口）、[config-trim 设计](../11-06-config-trim/design.md)（发布附带的 compose 与示例配置）
- 日期：2026-11-06；起草：设计子 agent（未改任何产品代码）

## 0. 三个子任务共用的名字

与 [builtin-backup 设计 §0](../11-06-builtin-backup/design.md) 的表相同。本任务使用：`postgres.Open(ctx, url, ...OpenOption)`、`postgres.WithMigrationBackups`、
`postgres.WithLogger`、`postgres.MigrationBackups`（`BackupBeforeMigration`、`LatestPreMigrationBackup`）、`postgres.MigrationChange` / `MigrationDowngrade`、
迁移前备份文件 `roomusic-YYYYmmdd-HHMMSS-pre-migration-<N>.dump`、`internal/platform/buildinfo`、`GET /api/v1/system/version`、
`deploy/production/compose.yaml` 与 `deploy/production/example.env` 中的占位符 `__RELEASE_VERSION__`、演练项目 `roomusic-drill` 与子网 `10.203.51.0/24`。

## 1. 边界

| 部分 | 位置 |
| --- | --- |
| 版本注入 | `backend/internal/platform/buildinfo/`（新包）、`deploy/production/Dockerfile`、`backend/cmd/roomusic/cli.go`（`--version`）、`main.go`（启动日志） |
| 双向迁移 | `backend/internal/platform/postgres/migrate.go`（加载、计划、降级执行、记录）、`backend/migrations/`（`NNNN_name.down.sql`） |
| 回退测试 | `backend/internal/platform/postgres/migrations_rollback_integration_test.go`、`postgrestest/snapshot.go` |
| 发布 | `.github/workflows/release.yml`（新）、`.github/workflows/ci.yml`（加 `workflow_call` 与 actionlint）、`scripts/release-check.sh`（新，主会话打标签前的只读检查） |
| 规范 | `.trellis/spec/backend/core0-runtime-contracts.md:124`、`database-guidelines.md:132-175` |

## 2. 版本注入（R1）

```go
// Package buildinfo 保存构建时写入的版本号与提交号；未写入或写入值不合法时视为开发构建。
package buildinfo

var (
    version = "" // -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.version=0.1.0
    commit  = "" // -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.commit=<40 位提交号>
)

type Info struct {
    Version string // 语义化版本，不带 v；"" 表示开发构建
    Commit  string // 40 位小写十六进制；"" 表示未知
}

func Current() Info
```

- 校验：`version` 必须匹配 `^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`（允许预发布后缀，供演练镜像 `0.1.0-drill-a` 使用；
  发布工作流只接受 `vX.Y.Z` 标签），`commit` 必须是 40 位小写十六进制，否则当作空。
- Docker 构建参数 `ROOMUSIC_VERSION`、`ROOMUSIC_COMMIT` → ldflags（§6）。本地 `go build` / `go test` 不带 ldflags → 开发构建。
- `roomusic --version`（也接受 `roomusic version`），stdout 一行，退出 0：

| 情况 | 输出 |
| --- | --- |
| 正式构建 | `roomusic 0.1.0 (0123456789ab)` |
| 开发构建、有提交号 | `roomusic 开发版 (0123456789ab)` |
| 开发构建、无提交号 | `roomusic 开发版` |

- 启动日志：`main` 创建 logger 后第一条 `logger.Info("roomusic build", "event", "platform.build.info", "module", "platform", "version", <"0.1.0" 或 "dev">, "commit", <40 位或 "">)`；
  现有 `roomusic server starting`（`main.go:74`）补 `event=platform.server.starting` 与同样两个字段。
- 管理接口：`GET /api/v1/system/version` → `{"version": "0.1.0" | null, "commit": "<40 位>" | null}`（开发构建为 null，页面显示“开发版”），合同与样本见 builtin-backup §3.1、§3.2。

## 3. 迁移执行器改造（R4–R7）

### 3.1 文件命名与“不可回退”标记

- 正向：`NNNN_name.sql`（不变）。回退：同名的 `NNNN_name.down.sql`，两者放在 `backend/migrations/`，都被 `//go:embed *.sql` 收进去。
- 现有正则（`migrate.go:28`）会把 `0031_x.down.sql` 也当作正向迁移（`.` 是允许字符）而报版本重复，所以加载时**先按后缀分类**：以 `.down.sql` 结尾的是回退文件，其余按原正则。
- 不可回退的迁移仍要有 `.down.sql`，内容只有一行标记（可再跟注释）：

```sql
-- roomusic:irreversible 合并重复的媒体记录后无法拆回原样
```

  标记正则 `^-- roomusic:irreversible\s+(\S.*)$`，理由必填；标记之后只允许空行与 `--` 注释。

### 3.2 加载校验（`loadMigrations`，在任何数据库副作用之前）

常量 `rollbackRequiredFromVersion int64 = 31`：从下一个新迁移开始必须带回退文件。

| 条件 | 结果 |
| --- | --- |
| 版本 ≥ 31 的正向迁移没有同名 `.down.sql` | 错误 `migration 31 (0031_x.sql) is missing rollback file 0031_x.down.sql` |
| 版本 < 31 的迁移带了 `.down.sql` | 错误（0001–0030 永远不会被回退，带上只会误导） |
| `.down.sql` 没有对应的正向文件，或名字不完全一致 | 错误 |
| 回退文件不是合法 UTF-8 或含 NUL（存进 `text` 列会失败） | 错误 |
| 可回退文件去掉注释与空白后为空 | 错误（防止空文件冒充回退） |
| 标记文件理由为空，或标记后还有 SQL | 错误 |

`migration` 结构增加 `Down *migrationDown`（`Name`、`SQL`、`Checksum`、`Irreversible`、`Reason`），`Checksum` 是回退文件原始字节的 lowercase SHA-256。
正向文件的校验和规则（`migrate.go:121`）不变，所以已发布迁移的校验和不受影响。`go test ./...`（CI backend job）里的加载单元测试会对真实嵌入集合跑一遍，缺回退文件立刻失败。

### 3.3 `schema_migrations` 新增列（执行器负责，不新增迁移）

```sql
ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS down_sql TEXT, ADD COLUMN IF NOT EXISTS down_checksum TEXT
```

- 时机：迁移事务里用 `pg_attribute` 探测（扩展 `schemaMigrationsMetadataColumns`，`migrate.go:258-277`）到缺列时，**在迁移前备份成功之后**（没有待执行变更时则在提交前）执行；
  已有列时不发 ALTER（避免每次启动都拿 AccessExclusiveLock）。在此之前读历史时缺的列按 `NULL::text` 读，计划判定不依赖这两列是否存在（见 §3.4 的锁规则）。
- 两列都可空：0001–0030 的行永远是 NULL；0001/0006/0007 在自己的 SQL 里 `INSERT INTO schema_migrations(version) … ON CONFLICT DO NOTHING`（`0001:7-9`、`0006:4`、`0007:24`）不受影响。
- `down_sql` 存回退文件原文（不可回退时存标记原文），`down_checksum` 存其 SHA-256。

**为什么不用新迁移 0031 加列**（PRD 提示的另一条路）：

| | 执行器加列（采用） | 迁移 0031 加列 |
| --- | --- | --- |
| 0031 自己的回退 | 不存在这个问题 | 只能标记不可回退：能回退它的程序必须认识 0031，而认识 0031 的程序本身就不需要回退它 |
| 降级底线 | 首个带机制的版本 0.1.0，库停在迁移 30 | 库变成 31，旧的 `make prod` 构建（最高 30）一见到 31 就报 `unknown version` |
| 本机生产过渡的回退 | 旧程序只读 `version,name,checksum`（`migrate.go:288`）、只写这三列（`:413-419`），多出两个可空列不影响，**直接换回旧镜像即可** | 必须先恢复备份 |
| 与“结构变化都走迁移文件”规则 | 对执行器自己的追踪表开一个例外，写进执行器合同 | 符合 |

### 3.4 启动判定流程（替换 `applyMigrationsFromFS`，`migrate.go:141-232`）

**执行器合同·备份前锁规则**（与 builtin-backup §2.2 同一条，写进 `database-guidelines.md` 的执行器合同）：

> 调用迁移前备份之前，迁移事务只允许持有迁移 advisory lock 和不高于 AccessShareLock 的表锁（即只做 `SELECT` 与系统目录查询）；
> 任何 DDL——包括加回退列 `down_sql` / `down_checksum`、执行 0001、执行 0008——都放在备份成功之后。读历史时缺列按 NULL 处理，计划判定不依赖这些列是否存在。
> 全新空库要执行 0001，本来就不备份，不受此规则影响。

理由：pg_dump 在另一个连接上要拿每张表的 AccessShareLock。迁移事务若先做 DDL（AccessExclusiveLock 持有到事务结束），pg_dump 等事务、事务等 pg_dump 返回，
形成 PostgreSQL 死锁检测看不到的跨连接死锁（例如停在迁移 20、还没有回退列的旧库直接升级到 0.1.0）。pg_dump 另带 `--lock-wait-timeout=60s`（builtin-backup §3.5），
意外冲突时以 `stage=pg_dump, reason=lock_timeout` 失败而不是挂起。

```text
available := loadMigrations(source)                      // §3.2，失败即返回，无副作用
tx := BEGIN；SELECT pg_advisory_xact_lock(0x524f4f4d55534943)
existed := to_regclass('schema_migrations') IS NOT NULL
if !existed { 执行 0001 }                                  // 全新空库：之后不会备份，DDL 不受锁规则限制
cols := 探测 name、checksum、down_sql、down_checksum（只查 pg_attribute）
history := 读 version,name,checksum,down_sql,down_checksum（缺列读 NULL::text）
// ——到这里为止，existed 为真时事务只持有迁移 advisory lock 与 schema_migrations 的 AccessShareLock——

known, unknown := 按 version 是否在 available 里拆分 history
对 known 做原有校验：name / checksum 漂移 → 错误（不变）
                    down_checksum 非空且与嵌入文件不同 → 只记 WARN platform.migration.rollback_differs（库里的副本照用）
maxAvailable := available 最高版本；maxRecorded := history 最高版本

if len(unknown) == 0:
    plan := 原有 buildMigrationPlan 逻辑（稀疏 1/6/7 基线、0008 基线、连续性，migrate.go:319-387）
    change := upgrade(FromVersion=maxRecorded, ToVersion=maxAvailable, Versions=待执行版本升序)
else:                                                     // 库来自更新的版本
    要求：available 的每个版本都已应用（否则 reason=pending_upgrade）
    要求：unknown 恰好是 maxAvailable+1 … maxRecorded，无缺口（否则 reason=not_contiguous，blocking=第一个缺口后的版本）
    对 unknown 每一行（从高到低）：
        down_sql 为 NULL（含库里根本没有回退列）      → reason=missing_rollback
        sha256(down_sql) ≠ down_checksum              → reason=rollback_checksum_mismatch
        down_sql 以不可回退标记开头                    → reason=irreversible（detail=理由）
    有任何一条不满足 → 返回 *SchemaAheadError；事务回滚；库不变
    change := downgrade(FromVersion=maxRecorded, ToVersion=maxAvailable, Versions=unknown 降序)

if change.Versions 为空:
    if !cols.rollback { ALTER TABLE … ADD COLUMN …（§3.3） }    // 无备份，直接加列
    recordMigrationMetadata(...)；COMMIT；return

if existed && backups != nil:                               // 全新空库不备份
    name, err := backups.BackupBeforeMigration(ctx, db, change)
    if err != nil: 记 platform.migration.pre_backup_failed；return fmt.Errorf("%w: %v", ErrPreMigrationBackup, err)（回滚）

// ——备份之后才允许 DDL——
if !cols.rollback { ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS down_sql TEXT, ADD COLUMN IF NOT EXISTS down_checksum TEXT；cols.rollback = true }

if change.Direction == downgrade:
    for v in change.Versions（降序）:
        EXEC row[v].down_sql                               // 同一事务，pgx 简单协议多语句
        DELETE FROM schema_migrations WHERE version = v
        出错 → return *RollbackFailedError{…, Backup: name}（回滚：所有回退与删除一起撤销）
else:
    按原逻辑执行待执行的正向迁移（含 0008 特殊处理）

recordMigrationMetadata(...)  // 重新读取整张表并严格校验：此时不允许任何未知版本（迁移或回退 SQL 自己写进去的也拒绝，保持 migrate.go:397-407 的语义）；
                              // 对每个嵌入版本 upsert name、checksum，版本 ≥ 31 时一并写 down_sql、down_checksum（ON CONFLICT 用 COALESCE，不覆盖已有值）
COMMIT
记 platform.migration.upgraded 或 platform.migration.downgraded
```

- **R7 回退后再升级**：回退删除了记录，再次升级时这些版本就是普通的待执行迁移，照常执行并重新写入回退 SQL。
- **R5 写入时机**：正向迁移的回退 SQL 在同一事务的 `recordMigrationMetadata` 里写入，与迁移一起提交或一起回滚。
- **升级与降级不会同时发生**：降级要求本二进制的所有版本都已应用。
- **回退 SQL 来自库里**：降级用的是新版本写进库的副本，旧二进制不需要认识新迁移的文件。回退文件发布后与正向文件一样不得修改；
  写错了只能在之后的版本里加修正迁移（详见 §5 规则 6），已经写进库的旧副本不会被替换。

### 3.5 错误类型与兼容

```go
// SchemaAheadError：库里有本程序不认识的更新迁移，且不能自动回退。库没有被修改。
type SchemaAheadError struct {
    DatabaseVersion  int64  // 库里最高版本
    SupportedVersion int64  // 本程序最高版本
    Version          int64  // 阻止回退的迁移
    Name             string // 库里记录的文件名（可能为空）
    Reason           string // missing_rollback | irreversible | rollback_checksum_mismatch | not_contiguous | pending_upgrade
    Detail           string // irreversible 时的理由
}
// Error() 形如 "schema_migrations contains unknown version 33 (latest supported 30): cannot roll back: irreversible"
// RollbackFailedError：回退 SQL 执行失败，事务已整体回滚。
type RollbackFailedError struct {
    DatabaseVersion, SupportedVersion, Version int64
    Name, Backup string // Backup = 本次迁移前备份的文件名
    Err          error
}
```

- `Error()` 保留子串 `unknown version N`：现有 `TestPostgreSQLMigrationRejectsDriftAndFutureVersion`（`migrate_test.go:283-303`，插入 99 断言 `"unknown version 99"`）与
  `TestBuildMigrationPlanRejectsUnknownAndDrift`（`:100`）的断言继续成立（99 不连续 → `not_contiguous`）；`TestPostgreSQLMigrationRejectsFutureVersionCreatedDuringMigration`（`:162-185`）
  由提交前的严格复查继续拒绝。`migrate.go:354` 那个不可达的 `future version` 分支删除。
- `main`：`openDatabase` 返回错误时照旧降级启动（`main.go:44-49`），`/readyz` 503、`/api` 503。

### 3.6 日志（`postgres.WithLogger`，`module=platform`）

| 事件 | 级别 | 字段 |
| --- | --- | --- |
| `platform.migration.upgraded` | INFO | `from_version`、`to_version`、`versions`、`backup` |
| `platform.migration.downgraded` | INFO | 同上 |
| `platform.migration.downgrade_refused` | ERROR | `database_version`、`supported_version`、`blocking_version`、`blocking_name`、`reason`、`detail`、`restore_backup`、`message` |
| `platform.migration.downgrade_failed` | ERROR | 同上 + `backup` |
| `platform.migration.pre_backup_failed` | ERROR | `direction`、`from_version`、`to_version` |
| `platform.migration.rollback_differs` | WARN | `version` |

- `restore_backup` = `backups.LatestPreMigrationBackup(SupportedVersion)`：“从本版本升级出去之前”那份迁移前备份（文件名后缀 `-pre-migration-<本版本最高迁移>`），找不到为空。
- `message` 是给人看的一句中文，例如：
  `数据库来自更新的版本（迁移 33），迁移 32 不能自动回退。用 roomusic-20261101-030000-pre-migration-30.dump 恢复（docker compose run --rm app restore <文件>），或改回能识别迁移 33 的版本。`
  找不到备份时把文件名换成“升级前的那份迁移前备份”。字段里不出现路径、连接串或 SQL 原文。

## 4. 回退自动测试（A2、A3）

### 4.1 执行器测试（A2）

| 测试 | 文件 |
| --- | --- |
| 加载：缺回退文件、多余回退文件、孤儿回退文件、标记解析、空回退、`.down.sql` 不被当成正向迁移 | `migrate_test.go`（无数据库） |
| 计划：未知连续全可回退 → 降级计划；缺口；不可回退；校验和不符；本版本有未应用迁移；已知版本回退副本不同只告警 | `migrate_test.go` |
| PG 18：升级写入 `down_sql` / `down_checksum`；降级按倒序执行、删记录、**先调用迁移前备份**（假 `MigrationBackups` 记录调用顺序与 `change`）；备份失败时库不变；回退 SQL 报错时整体回滚、行数与结构不变、返回 `RollbackFailedError` 带备份名；不可回退 / 不连续时不调用备份、库不变；回退后再升级成功；旧 1/6/7 稀疏库加列后照常；缺列的旧库（只到 0007）加列与 0008 同一事务 | `migrate_rollback_test.go`（`TestPostgreSQLMigrationRollback*`），用 `testMigrationFS`（`migrate_test.go:391`）注入 |
| 降级被拒时 `openDatabase` 返回错误、降级 handler 的 `/readyz` 为 503 | `backend/cmd/roomusic/open_database_integration_test.go` + 现有降级 handler 测试 |
| **缺回退列 + 有待执行迁移时不死锁**（`TestPostgreSQLPreMigrationBackupWithoutRollbackColumns`）：用独立数据库（`postgrestest.NewDatabase`，pg_dump 备份的是整个库）以真实嵌入迁移升到 20，再 `ALTER TABLE schema_migrations DROP COLUMN down_sql, DROP COLUMN down_checksum` 模拟机制之前的旧库；用真实 `backup.Service`（`ROOMUSIC_TEST_PG_BIN_DIR` 的 pg 工具）调用 `postgres.Open(ctx, url, postgres.WithMigrationBackups(service))`，`ctx` 限时 2 分钟。断言：`Open` 在时限内成功返回；备份目录出现 `-pre-migration-20.dump` 且 `pg_restore --list` 可读；迁移升到最新；两列存在且版本 ≥ 31 的行（若有）已写入回退 SQL。归属：**本任务实现**（加列逻辑在本任务），放在 `backend/internal/backup/migration_backup_integration_test.go`（`backup` 包本来就导入 `postgres`；反过来在 `postgres` 包的测试里导入 `backup` 会成环）。builtin-backup 的锁规则探针（`migrate_backup_test.go`，假备份在另一连接上限时 5 秒拿每张表的 AccessShareLock）是同一规则的快速防线 | `./scripts/test-integration.sh` |

### 4.2 每个带回退的真实迁移都有往返测试（A3）

`TestPostgreSQLMigrationRollbackRoundTrip`（`backend/internal/platform/postgres/migrations_rollback_integration_test.go`）自动枚举 `migrations.Files` 里版本 ≥ 31 且可回退的迁移，
**不需要为每个迁移手写测试**：

```text
一个隔离 schema：applyMigrationsFromFS(前 30 个)
for v in 31..max（可回退）:
    S0 := Snapshot()
    apply(1..v)                 → S1
    执行 rollbackFixtures[v]（如有）：用新版本才有的取值写入旧表
    apply(1..v-1)               → 走真实的降级路径（库里的 down_sql）→ S2；断言 S2 == S0（结构 + 数据）
    apply(1..v)                 → S3；断言 S3 与 S1 结构相同、各表行数相同
不可回退的迁移：只断言加载标记正确，并从这里开始后面的版本在新 schema 里起步
```

- `postgrestest.Snapshot(ctx, db) (Snapshot, error)`（新文件 `postgrestest/snapshot.go`，只查当前 schema，`current_schema()`）：
  - 表、视图、序列：`pg_class`（relkind）；列：按 `attnum` 排名（不比较原始 attnum，重新加列后 attnum 会变）的 `attname`、`format_type(atttypid, atttypmod)`、`attnotnull`、`pg_get_expr(adbin, adrelid)`、
    `attidentity`、`attgenerated`、排序规则；约束 `pg_get_constraintdef`；索引 `pg_get_indexdef`；视图 `pg_get_viewdef`；函数 `pg_get_functiondef`；
    触发器 `pg_get_triggerdef`（排除内部触发器）；枚举 `pg_enum` 标签与顺序；域；序列参数。
  - 定义文本里的 schema 名统一替换为 `$SCHEMA`。扩展（如 0025 的 `pg_trgm`，属于整个数据库）不进快照。
  - 数据：每张表行数；不超过 1000 行的表再加 `md5(string_agg(row_to_json(t)::text, E'\n' ORDER BY row_to_json(t)::text))`；`schema_migrations` 的数据比较不含 `applied_at`。
  - `Diff(a, b) []string` 输出可读差异。
- **CHECK 放宽与枚举新增要有数据夹具**：比较 S0 与 S1，若已有约束同名但定义变了、或已有枚举类型多了标签，而 `rollbackFixtures[v]` 不存在，测试失败并提示补夹具。
- 自检：`TestPostgreSQLSchemaSnapshotDetectsChanges` 用合成迁移（`testMigrationFS`）证明快照能发现漏删的列、索引、约束与残留数据；在真实迁移只有 ≤ 30 时这个测试保证框架本身有效。
- CI：名字以 `TestPostgreSQL` 开头，由 integration job 的 `./scripts/test-integration.sh`（`-run 'TestPostgreSQL|TestFFmpeg'`）运行。

## 5. 迁移作者规则（写进 `database-guidelines.md` 与 `core0-runtime-contracts.md` 的草稿）

`core0-runtime-contracts.md:124` 的“不提供 down migration，生产回退使用备份或 forward-fix”改为：

> 从迁移 0031 起，每个迁移都附带 `NNNN_name.down.sql`；执行正向迁移时回退 SQL 与其校验和写进 `schema_migrations.down_sql / down_checksum`。
> 启动时库里有本程序不认识的、连续的更新迁移且都可回退，就先做迁移前备份，再在同一事务里按版本倒序执行库里的回退 SQL 并删除记录；
> 任何一条不可回退、不连续或执行失败都整体回滚，不改库，不进入就绪。降级底线是首个带此机制的版本 0.1.0（迁移 30）。

`database-guidelines.md` 的“ROOMusic 执行器合同”增补（签名、错误矩阵、必需测试随 §3、§4 更新；§3.4 的“备份前锁规则”原文写进合同第 3 节），并新增“回退 SQL 规则”：

1. 每个新迁移必须同时提交 `.down.sql`；确实无法回退时写 `-- roomusic:irreversible <理由>`，并在任务设计里说明降级时用哪份备份恢复。
2. 回退必须把**结构和旧版本依赖的数据合同**恢复到升级前：删掉新建的表、列、索引、约束、视图、函数、触发器、类型和种子行，恢复被删除或改窄前的约束。
   回退会丢掉新表、新列里的数据，这是降级的预期代价。
3. **新增取值的回退必须先清理新取值**：放宽 CHECK、新增枚举值、新增 `operations.type` 之类时，回退先 DELETE 或 UPDATE 使用新取值的行，再恢复更窄的约束；
   旧版本能读写的状态才算合格。往返测试会因为缺少 `rollbackFixtures[v]` 而失败。
4. 回退 SQL 与正向 SQL 一样在同一事务执行：不得包含 `COMMIT`、`ROLLBACK`、`CONCURRENTLY`、`ALTER TYPE … ADD VALUE`、`VACUUM` 等事务外语句；
   不得删除数据库级共享对象（扩展）。
5. 恢复唯一性约束前，若新版本允许的重复数据会让重建失败，回退要先合并或删除重复行；做不到就标记不可回退。
6. `.sql` 与 `.down.sql` 一旦发布（进入任何 `vX.Y.Z` 标签）都不得修改。回退写错了，在之后的版本里加修正迁移，并在发布说明里写明从受影响版本降级要用备份。
7. **`/data` 目录格式变化要版本化**：封面（`<DATA_DIR>/artwork/`）与派生缓存（`<DATA_DIR>/media-cache/v1/`）的格式变化必须换新的版本子目录或新文件名，
   不得原地改写旧版本会读的文件；旧版本遇到新目录或不认识的文件时忽略即可，必要时由重新扫描或再次播放重建。
8. 测试要求：往返测试自动覆盖；需要夹具时补 `rollbackFixtures`；不可回退的迁移在任务设计里写明恢复办法。

## 6. Dockerfile（在 builtin-backup 的改动之上）

```dockerfile
# syntax=docker/dockerfile:1
# 上下文是仓库根（发布工作流的干净检出，或 git archive 导出树）；前端使用已提交的内嵌产物，不安装 Node。
FROM --platform=$BUILDPLATFORM golang:1.25.10-alpine3.22@sha256:26b4d7113039cd51356bd7930ecafd1031d2975dc3b6940ec8ed09457e17cf95 AS build
ARG TARGETOS TARGETARCH
ARG ROOMUSIC_VERSION=""
ARG ROOMUSIC_COMMIT=""
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN test -f cmd/roomusic/web/index.html \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.version=${ROOMUSIC_VERSION} -X github.com/qlqq/roomusic/backend/internal/platform/buildinfo.commit=${ROOMUSIC_COMMIT}" \
    -o /out/roomusic ./cmd/roomusic

FROM mwader/static-ffmpeg@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c AS ffmpeg

# PostgreSQL 客户端主版本必须与 compose.yaml 的数据库镜像一致（CI 检查）。
FROM alpine:3.23@sha256:<实现时取 alpine:3.23 的索引摘要> AS app
RUN apk add --no-cache ca-certificates postgresql18-client \
 && pg_dump --version | grep -q '^pg_dump (PostgreSQL) 18\.' \
 && addgroup -S -g 1000 roomusic && adduser -S -D -H -u 1000 -G roomusic roomusic \
 && mkdir -p /app /data /backups && chown roomusic:roomusic /data /backups && chmod 0700 /backups
COPY --from=build /out/roomusic /app/roomusic
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --chmod=0755 deploy/production/entrypoint.sh /app/entrypoint.sh
USER 1000:1000
WORKDIR /app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --start-interval=5s --retries=3 \
 CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/entrypoint.sh"]
```

- 多架构：Go 阶段固定在构建机平台（`--platform=$BUILDPLATFORM`）交叉编译（`CGO_ENABLED=0`，纯 Go）；app 阶段按目标平台。发布时两个平台分别在原生 runner 上构建，交叉编译不增加成本；
  按裁决，本机不安装 QEMU/binfmt，arm64 只在原生 runner 上构建（`workflow_dispatch` 验证，见 §7.2）。
- 固定摘要都是**多架构索引摘要**（2026-11-06 用 `docker buildx imagetools inspect` 核实）：

| 镜像 | 摘要类型 | 平台 |
| --- | --- | --- |
| `golang:1.25.10-alpine3.22@sha256:26b4d711…` | OCI image index | 含 linux/amd64、linux/arm64/v8 |
| `mwader/static-ffmpeg@sha256:11a44711…` | Docker manifest list | linux/amd64、linux/arm64 |
| `postgres:18.1-alpine3.23@sha256:aa6eb304…` | OCI image index | 含 linux/amd64、linux/arm64/v8 |
| `alpine:3.23`（当前 `sha256:f3d55900…`） | OCI image index | 含 linux/amd64、linux/arm64/v8 |

  核实命令：`docker buildx imagetools inspect <引用> | grep -E 'MediaType|Platform'`。
- OCI 标签由发布工作流的 `docker/metadata-action` 写入（`org.opencontainers.image.version / revision / source / created / title`），Dockerfile 不硬编码。

## 7. 发布工作流（R2、A4）

### 7.1 `ci.yml` 的改动

- `on:` 增加 `workflow_call:`，让发布工作流复用同一套门禁（backend、frontend、integration、playback）。
- 被调用时 `${{ github.workflow }}` 是调用方的名字（“Release”），所以 `ci.yml` 现有的 `group: roomusic-ci-${{ github.workflow }}-${{ github.ref }}` 会变成
  `roomusic-ci-Release-refs/tags/vX.Y.Z`，与发布工作流自己的 `roomusic-release-refs/tags/vX.Y.Z` 不同，不会互相取消或死锁。
- integration job 增加 actionlint：`docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.12 -color`。

### 7.2 `.github/workflows/release.yml`（完整草稿）

```yaml
name: Release

on:
  push:
    tags: ['v*.*.*']
  # 手动运行只在两个平台各构建一次，不推送、不发布；用于验证多架构构建。
  workflow_dispatch:

permissions: {}

concurrency:
  group: roomusic-release-${{ github.ref }}
  cancel-in-progress: false

env:
  IMAGE: ghcr.io/qlqqs/roomusic

jobs:
  ci:
    name: CI
    permissions:
      contents: read
    uses: ./.github/workflows/ci.yml

  prepare:
    name: Validate tag
    runs-on: ubuntu-24.04
    timeout-minutes: 5
    permissions:
      contents: read
    outputs:
      version: ${{ steps.version.outputs.version }}
      publish: ${{ steps.version.outputs.publish }}
    steps:
      - name: Checkout
        uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - name: Resolve version
        id: version
        env:
          REF_TYPE: ${{ github.ref_type }}
          REF_NAME: ${{ github.ref_name }}
        run: |
          set -euo pipefail
          if [[ "$REF_TYPE" != tag ]]; then
            echo "version=0.0.0-dryrun" >> "$GITHUB_OUTPUT"
            echo "publish=false" >> "$GITHUB_OUTPUT"
            exit 0
          fi
          if [[ ! "$REF_NAME" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
            echo "::error::标签必须是 vX.Y.Z：$REF_NAME"
            exit 1
          fi
          git fetch --no-tags origin main
          if ! git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then
            echo "::error::标签指向的提交不在 main 上"
            exit 1
          fi
          grep -q '__RELEASE_VERSION__' deploy/production/compose.yaml
          grep -q '__RELEASE_VERSION__' deploy/production/example.env
          echo "version=${REF_NAME#v}" >> "$GITHUB_OUTPUT"
          echo "publish=true" >> "$GITHUB_OUTPUT"

  build:
    name: Build ${{ matrix.arch }}
    needs: [ci, prepare]
    strategy:
      fail-fast: true
      matrix:
        include:
          - arch: amd64
            platform: linux/amd64
            runner: ubuntu-24.04
          - arch: arm64
            platform: linux/arm64
            runner: ubuntu-24.04-arm
    runs-on: ${{ matrix.runner }}
    timeout-minutes: 30
    permissions:
      contents: read
      packages: write
    steps:
      - name: Checkout
        uses: actions/checkout@v7
      - name: Set up Buildx
        uses: docker/setup-buildx-action@v4
      - name: Log in to GHCR
        if: needs.prepare.outputs.publish == 'true'
        uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Image metadata
        id: meta
        uses: docker/metadata-action@v6
        with:
          images: ${{ env.IMAGE }}
          labels: |
            org.opencontainers.image.title=ROOMusic
            org.opencontainers.image.version=${{ needs.prepare.outputs.version }}
      - name: Build and push by digest
        id: build
        uses: docker/build-push-action@v7
        with:
          context: .
          file: deploy/production/Dockerfile
          platforms: ${{ matrix.platform }}
          build-args: |
            ROOMUSIC_VERSION=${{ needs.prepare.outputs.version }}
            ROOMUSIC_COMMIT=${{ github.sha }}
          labels: ${{ steps.meta.outputs.labels }}
          outputs: type=image,name=${{ env.IMAGE }},push-by-digest=true,name-canonical=true,push=${{ needs.prepare.outputs.publish }}
      - name: Export digest
        if: needs.prepare.outputs.publish == 'true'
        env:
          DIGEST: ${{ steps.build.outputs.digest }}
        run: |
          set -euo pipefail
          mkdir -p "$RUNNER_TEMP/digests"
          touch "$RUNNER_TEMP/digests/${DIGEST#sha256:}"
      - name: Upload digest
        if: needs.prepare.outputs.publish == 'true'
        uses: actions/upload-artifact@v7
        with:
          name: digests-${{ matrix.arch }}
          path: ${{ runner.temp }}/digests/*
          if-no-files-found: error
          retention-days: 1

  merge:
    name: Publish multi-arch image
    needs: [prepare, build]
    if: needs.prepare.outputs.publish == 'true'
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    permissions:
      contents: read
      packages: write
    steps:
      - name: Download digests
        uses: actions/download-artifact@v8
        with:
          path: ${{ runner.temp }}/digests
          pattern: digests-*
          merge-multiple: true
      - name: Set up Buildx
        uses: docker/setup-buildx-action@v4
      - name: Log in to GHCR
        uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Image metadata
        id: meta
        uses: docker/metadata-action@v6
        env:
          DOCKER_METADATA_ANNOTATIONS_LEVELS: index
        with:
          images: ${{ env.IMAGE }}
          flavor: latest=true
          tags: |
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
          annotations: |
            org.opencontainers.image.title=ROOMusic
      - name: Create manifest list
        working-directory: ${{ runner.temp }}/digests
        run: |
          set -euo pipefail
          args=()
          while IFS= read -r tag; do args+=(--tag "$tag"); done < <(jq -r '.tags[]' <<<"$DOCKER_METADATA_OUTPUT_JSON")
          while IFS= read -r annotation; do args+=(--annotation "$annotation"); done < <(jq -r '.annotations[]' <<<"$DOCKER_METADATA_OUTPUT_JSON")
          for digest in *; do args+=("${IMAGE}@sha256:${digest}"); done
          docker buildx imagetools create "${args[@]}"
      - name: Verify platforms
        env:
          VERSION: ${{ needs.prepare.outputs.version }}
        run: |
          set -euo pipefail
          docker buildx imagetools inspect "${IMAGE}:${VERSION}" --format '{{json .Manifest}}' \
            | jq -e '[.manifests[] | select(.platform != null) | "\(.platform.os)/\(.platform.architecture)"] as $p
                     | ($p | index("linux/amd64")) != null and ($p | index("linux/arm64")) != null'
      - name: Check anonymous pull
        env:
          VERSION: ${{ needs.prepare.outputs.version }}
        run: |
          docker logout ghcr.io
          if ! docker buildx imagetools inspect "${IMAGE}:${VERSION}" >/dev/null 2>&1; then
            echo "::warning::镜像还不能匿名拉取：首次发布后请在 GitHub 网页上把 roomusic 包设为公开"
          fi

  release:
    name: GitHub Release
    needs: [prepare, merge]
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    permissions:
      contents: write
    steps:
      - name: Checkout
        uses: actions/checkout@v7
      - name: Write versioned deployment files
        env:
          VERSION: ${{ needs.prepare.outputs.version }}
        run: |
          set -euo pipefail
          out="$RUNNER_TEMP/release"
          mkdir -p "$out"
          sed "s/__RELEASE_VERSION__/${VERSION}/g" deploy/production/compose.yaml > "$out/compose.yaml"
          sed "s/__RELEASE_VERSION__/${VERSION}/g" deploy/production/example.env > "$out/example.env"
          if grep -q '__RELEASE_VERSION__' "$out/compose.yaml" "$out/example.env"; then exit 1; fi
          ROOMUSIC_DB_PASSWORD=ci-placeholder ROOMUSIC_MUSIC_DIR=/srv/music \
            docker compose -f "$out/compose.yaml" --env-file "$out/example.env" config --quiet
      - name: Create release
        env:
          GH_TOKEN: ${{ github.token }}
          TAG: ${{ github.ref_name }}
        run: |
          gh release create "$TAG" "$RUNNER_TEMP/release/compose.yaml" "$RUNNER_TEMP/release/example.env" \
            --verify-tag --title "ROOMusic ${TAG}" --generate-notes
```

- 权限最小化：工作流默认 `{}`；只有 `build`、`merge` 有 `packages: write`，只有 `release` 有 `contents: write`。`GITHUB_TOKEN` 推送 GHCR 新包时，包按 `org.opencontainers.image.source`
  链接到仓库（metadata-action 默认写入该标签）。
- 标签：`0.1.0`、`0.1`、`latest`（`flavor: latest=true`；`{{major}}` 单独的标签不发）。
- 版本号写进附件：`example.env` 的 `ROOMUSIC_VERSION=0.1.0` 与 compose 默认值都被替换；部署者只改 `.env` 的一行即可升降级。
- **首个包设为公开需要用户在网页上手动操作一次**：GitHub 文档写明个人账号下的新包默认私有，且包只继承仓库的访问权限、不继承可见性；文档只描述了网页路径
  （包设置 → Danger Zone → Change visibility，输入包名确认），并提醒**设为公开后不能改回私有**。首次发布后由用户操作，之后的版本自动沿用公开。
- arm64 runner：`ubuntu-24.04-arm` 自 2025-08-07 起对公开仓库正式提供且免费；仓库 `qlqqs/ROOMusic` 当前为 PUBLIC（`gh repo view` 已核实）。
- action 版本取 2026-11-06 的最新主版本：`actions/checkout@v7`、`actions/upload-artifact@v7`、`actions/download-artifact@v8`、`docker/setup-buildx-action@v4`、
  `docker/login-action@v4`、`docker/metadata-action@v6`、`docker/build-push-action@v7`（`gh api repos/<owner>/<repo>/releases/latest` 核实）。

### 7.3 打标签前的只读检查（R3）：`scripts/release-check.sh vX.Y.Z`

主会话在用户说“发布”后运行，全部通过才执行 `git tag -a vX.Y.Z -m "ROOMusic X.Y.Z" && git push origin main vX.Y.Z`（先推 main，工作流会检查标签提交在 main 上）：

1. 参数匹配 `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`。
2. 工作区干净、当前分支是 `main`；`git fetch origin main --tags` 后 `git merge-base --is-ancestor origin/main HEAD`（远端 main 是本地 HEAD 的祖先）。
3. 新版本大于所有已有 `v*` 标签（`sort -V`），且标签不存在。
4. 从上一个标签（没有则从第一个提交）到 HEAD 的新增与修改文件中：没有 `.env`、`.env.*`（`example` 文件除外）、`*.pem`、`*.key`、`*.p12`、`id_rsa*`、`*.dump`；
   新增行里没有 `ghp_`、`github_pat_`、`gho_`、`glpat-`、`AKIA[0-9A-Z]{16}`、`-----BEGIN [A-Z ]*PRIVATE KEY-----`、`xox[baprs]-`、`sk-[A-Za-z0-9]{20,}`。命中时只打印文件名与行号，不打印内容。
5. `deploy/production/compose.yaml`、`example.env` 仍含占位符；`backend/cmd/roomusic/web` 与前端源码一致（`git diff --exit-code` 已由 CI 保证，这里只检查工作区）。
6. 打印将要发布的提交与版本，退出 0。脚本不推送、不打标签。

## 8. 升降级演练 A → B → A → B（A5，隔离，不碰生产）

- **本机网络约束**：本机任何演练与测试都不得创建未固定子网的 Docker 网络（自动地址池的下一个网段是 `192.168.48.0/20`，会覆盖局域网 `192.168.50.0/24`）。
  演练项目在第一次 `up` 之前就必须放好 `compose.override.yaml`（子网 `10.203.51.0/24`），并先用 `docker compose config --format json | jq '.networks.roomusic.ipam'` 确认；
  任何 `docker run` 只用默认 bridge 或 `--network host`，不加 `--network <新名字>`。
- 目录 `/tmp/roomusic-drill/`，项目 `roomusic-drill`，端口 `18090`，`compose.override.yaml` 子网 `10.203.51.0/24`，备份目录 `/tmp/roomusic-drill/backups`（`chown 1000:1000`），
  音乐为合成曲库副本。compose 用当前提交的 `deploy/production/compose.yaml`。
- **镜像 A**：`git archive HEAD | tar -x -C /tmp/roomusic-drill/src-a`，
  `docker build --build-arg ROOMUSIC_VERSION=0.1.0-drill-a --build-arg ROOMUSIC_COMMIT=$(git rev-parse HEAD) -f /tmp/roomusic-drill/src-a/deploy/production/Dockerfile -t ghcr.io/qlqqs/roomusic:0.1.0-drill-a /tmp/roomusic-drill/src-a`。
- **镜像 B**：同样导出到 `src-b`，**只在导出目录里**加两份文件（不进仓库）：下一个可用版本号 `N`（当前为 31）的 `000N_drill_probe.sql` 与 `000N_drill_probe.down.sql`：

```sql
-- 000N_drill_probe.sql（只存在于演练镜像）
CREATE TABLE drill_probe (id bigint PRIMARY KEY, note text NOT NULL);
ALTER TABLE users ADD COLUMN drill_note text;
-- 000N_drill_probe.down.sql
ALTER TABLE users DROP COLUMN drill_note;
DROP TABLE drill_probe;
```

  构建标签 `ghcr.io/qlqqs/roomusic:0.1.0-drill-b`。另建 **B′**：回退文件换成 `-- roomusic:irreversible 演练：不可回退`，标签 `0.1.0-drill-b-irrev`。
- 每一步：改 `.env` 的 `ROOMUSIC_VERSION` → `docker compose up -d --wait`（本地已有镜像，`pull_policy` 默认 missing，不会去 GHCR 拉）→ 记录：
  - `docker compose ps` 两个服务 healthy；`docker compose exec app /app/roomusic --version`；
  - 计数：`users`、`release_groups`、`tracks`、`favorite_tracks`、`favorite_albums` 与 `max(schema_migrations.version)`（`docker compose exec -T postgres psql -X -qAt …`）；
  - `ls -l /tmp/roomusic-drill/backups/*-pre-migration-*`；`docker compose logs app | grep -E 'platform\.migration\.|backup\.'`。

| 步骤 | 期望 |
| --- | --- |
| A（首次） | 空库，不做迁移前备份；建管理员、扫描、收藏若干，得到计数 C0；`max=30` |
| A → B | 新增 `-pre-migration-30.dump`；`platform.migration.upgraded`；计数 = C0；`max=31`；随后 `UPDATE users SET drill_note='x'`、`INSERT INTO drill_probe …` |
| B → A | 新增 `-pre-migration-31.dump`；`platform.migration.downgraded`；计数 = C0；`max=31` 的行已删除，`drill_probe` 表与 `users.drill_note` 列不存在 |
| A → B | 新增 `-pre-migration-30.dump`；再次升级成功；计数 = C0 |
| B → B′ → A（附加） | 先回 A，再切到 B′（升级，写入不可回退标记），再切回 A：A 不就绪（`/readyz` 503），`platform.migration.downgrade_refused`、`reason=irreversible`、`restore_backup` 指向 `-pre-migration-30` 文件；库不变。按日志执行 `docker compose stop app && docker compose run --rm app restore <该文件> && docker compose up -d --wait` 后 A healthy，计数 = C0 |

- 清理：`docker compose -p roomusic-drill down -v --remove-orphans`；`docker image rm ghcr.io/qlqqs/roomusic:0.1.0-drill-a ghcr.io/qlqqs/roomusic:0.1.0-drill-b ghcr.io/qlqqs/roomusic:0.1.0-drill-b-irrev`；
  `rm -rf /tmp/roomusic-drill`。演练镜像与迁移文件都不推送、不提交。

## 9. 取舍

| 问题 | 选择 | 放弃的方案与理由 |
| --- | --- | --- |
| 回退 SQL 存放 | 独立 `.down.sql`，执行时存进库 | 追加到正向文件：改变已发布迁移的校验和；只存在文件里：旧二进制没有新迁移的文件 |
| 不可回退的表达 | 回退文件里的标记行 | 单独清单文件：两处维护；旧二进制无法从库里得知 |
| 新增列 | 执行器加列 | 迁移 0031（§3.3） |
| 降级时机 | 启动时自动 | 单独的 `roomusic downgrade` 命令：违背“改版本号再 up 就行” |
| 回退文件发布后能否修正 | 不能（与正向一致），库里保留首次写入的副本 | 允许修正并在启动时刷新库里副本：新旧版本交替启动会来回覆盖 |
| 多架构构建 | 两个原生 runner + 按摘要合并 | 单 runner QEMU：arm64 的 `apk add` 与测试慢且偶发失败；`docker/github-builder` 可复用工作流：少写 YAML，但多一层外部依赖，门禁与权限更难看清 |
| 复用 CI | `workflow_call` | 复制 job：两份门禁会漂移；`workflow_run`：拿不到标签上下文，且不能精确对应一次运行 |
| 发布 main 尝鲜镜像 | 不发布（D5） | |

## 10. 运维与回滚

- 发布失败（任一 job 失败）：没有推送任何标签的镜像（按摘要推送的中间产物不带标签，GHCR 里显示为未标记版本，可以在包设置里删除）；
  修复后删除远端标签再重新打（`git push origin :refs/tags/vX.Y.Z`，须用户同意）或直接发布下一个补丁版本。推荐后者。
- 已发布版本有问题：发布新补丁版本；用户把 `ROOMUSIC_VERSION` 改回上一个版本即可降级（迁移自动回退，前提是期间新增的迁移都可回退）。
- 降级被拒：按日志 `restore_backup` 用 `roomusic restore` 恢复，或换回能识别该迁移的版本。

## 11. 测试方案

| 验收 | 测试 | 命令 |
| --- | --- | --- |
| A1 | `buildinfo` 单元测试（合法 / 非法版本与提交、开发构建）；`cli` 的 `--version` 三种输出；`GET /api/v1/system/version` 样本 `system/version-dev.json`；演练里 `--version` 为 `0.1.0-drill-a` | `cd backend && go test ./internal/platform/buildinfo/ ./cmd/roomusic/` |
| A2 | §4.1 | `cd backend && go test ./internal/platform/postgres/`；`./scripts/test-integration.sh`（含 `TestPostgreSQLMigrationRollback*`） |
| A3 | §4.2 | 同上（`TestPostgreSQLMigrationRollbackRoundTrip`、`TestPostgreSQLSchemaSnapshotDetectsChanges`） |
| A4 | actionlint 通过；`workflow_dispatch` 在两个原生 runner 上各构建一次（不推送）——按裁决 3 这取代 PRD 里“本机 buildx 对两个平台各构建一次”中的 arm64 部分；本地只 `docker buildx build --platform linux/amd64` 一次；首次真实发布后 `docker manifest inspect ghcr.io/qlqqs/roomusic:0.1.0` 显示 amd64 与 arm64、`docker logout ghcr.io` 后可拉取 | `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.12`；`gh workflow run release.yml --ref main` |
| A5 | §8 演练 | 见 §8 |
| A6 | 规范按 §5 更新；`git grep -n "不提供 down migration"` 无结果 | |

## 12. 裁决结果（主会话 2026-11-06）

1. **采纳**：回退所需的两列由执行器加在 `schema_migrations` 上，不新增迁移 0031；降级底线保持迁移 30。加列属于 DDL，按 §3.4 锁规则放在迁移前备份之后（审阅修正）。
2. **采纳**：回退文件与正向文件一样，发布后不得修改；写错的回退靠后续修正迁移与发布说明。
3. **arm64 构建验证改为 `release.yml` 的 `workflow_dispatch` 在原生 `ubuntu-24.04-arm` runner 上进行**，本机不安装 binfmt / QEMU；本机只构建 amd64。
4. **采纳**：版本号的预发布后缀只用于本地演练镜像（`0.1.0-drill-a` 等）；发布工作流只接受 `vX.Y.Z`。
5. **采纳**：发布时经 `workflow_call` 重跑整套 CI 门禁。
6. **用户已同意镜像公开**：首次发布后由用户在 GitHub 网页上把 `roomusic` 包设为公开（不可撤销）。
7. （审阅补充）迁移前备份的锁规则见 §3.4，pg_dump `--lock-wait-timeout=60s` 见 builtin-backup §3.5；本机演练网络约束见 §8。

## 13. 待核实

| 事项 | 核实方法 |
| --- | --- |
| `docker buildx imagetools create --annotation "index:…"` 接受 metadata-action 输出的带层级前缀的注解 | `workflow_dispatch` 无法覆盖（不推送）；首次发布前在个人测试仓库或用 `--dry-run`：`docker buildx imagetools create --dry-run --annotation 'index:org.opencontainers.image.title=x' -t x alpine:3.23` |
| metadata-action 对 `v0.1.0` 生成 `0.1`（`{{major}}.{{minor}}`）与 `latest` | README 的 `v1.2.3 → 1.2.3, 1.2, latest` 示例；0.x 的行为在首次发布的 merge 日志里确认 |
| `GITHUB_TOKEN` 能在个人账号下创建新包 `ghcr.io/qlqqs/roomusic` | 首次发布；失败时改用带 `write:packages` 的细粒度令牌（需用户创建 secret） |
| 被调用的 `ci.yml` 顶层 `concurrency` 作用于调用 job 且组名不与发布工作流冲突 | 首次 `workflow_dispatch` 运行时观察；社区资料显示被调用工作流的顶层 concurrency 会生效且 `github.workflow` 为调用方名字 |
| `ubuntu-24.04-arm` 上 `apk add postgresql18-client` 正常 | `workflow_dispatch` 运行 |
| `pg_restore` 18 对旧 PG 18.1 服务端导出的备份没有兼容问题 | builtin-backup 集成测试（客户端 18.x 与服务端 18.1） |
