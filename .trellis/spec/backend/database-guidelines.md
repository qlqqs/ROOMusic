# Database Guidelines

## Core 0 Database Contract

PostgreSQL 18 is the only required business authority for Core 0. The current
[Compose file](../../../compose.yaml) provides it locally. Redis and Meilisearch
may be running for development, but application startup, sessions, scanning,
search, and the user-visible Core 0 loop must work without them.

当前实现使用 `database/sql` + `pgx/v5`，迁移通过 `embed.FS` 发现并按版本排序；
未引入 ORM、query generator 或外部迁移工具。启动时执行器在单个事务中取得固定
的 `pg_advisory_xact_lock`（key 为 `0x524f4f4d55534943`，即字符串
`ROOMUSIC` 的稳定命名空间），按 `schema_migrations` 跳过已完成版本，并在
`name`/`checksum` 列保存文件名和原始字节的 lowercase SHA-256。锁是事务级的，
提交、回滚、连接断开或上下文取消都会释放。

## Ownership And Access

Each capability owns its tables, queries, row mappings, and migrations. Another
capability accesses that data through a published application contract or
consumer-owned port, never by importing SQL or querying private tables.

Planned table families should make ownership visible, for example identity
users/sessions, library roots/scan runs/source observations, catalog release
graph entities, and operations change sets/journal events. Exact names are fixed
by the first reviewed migration, not by this documentation example.

Repository methods return domain or application values, not database row structs.
SQL nullability, driver types, and JSON representation stop at the adapter.

## Query Rules

- Use explicit columns. Avoid `SELECT *` in application queries and scans.
- Bind every value as a parameter. Dynamic sort/filter choices come from a
  server-owned allowlist, never string concatenation.
- Pass `context.Context` through queries and honor cancellation.
- Bound list and search queries with stable ordering and explicit pagination.
- Avoid N+1 access for Release -> Medium -> Track views; use a bounded query plan
  or batch by IDs, then assemble the typed projection at one owner.
- PostgreSQL basic search is the Core 0 implementation. A future Meilisearch
  adapter is a rebuildable projection and cannot become the write authority.
- Keep query performance observable and add indexes from measured access
  patterns. Do not index every candidate field preemptively.

## Transactions, Revision, And Idempotency

Application use cases define transaction boundaries; individual repositories do
not silently begin and commit independent transactions inside one business
operation.

Directory add, disable, and restore must atomically persist:

1. the resource state and incremented revision;
2. the Change Set state;
3. the Operation Journal event; and
4. the idempotency result or conflict evidence.

Use compare-and-update semantics such as:

```sql
UPDATE library_roots
SET status = $1, revision = revision + 1, updated_at = $2
WHERE id = $3 AND revision = $4;
```

Zero affected rows maps to a classified revision conflict, not an unconditional
retry. An idempotency key is scoped to its operation/actor context and tied to a
canonical request fingerprint. Repeating the same request returns the recorded
result; reusing the key for different input returns a conflict.

Do not hold a database transaction open while walking a library tree or reading
audio tags. Persist scan progress in bounded batches and finalize the scan with
a transaction that applies negative reconciliation only when the outcome is
complete and successful.

## Domain Integrity

- Encode required uniqueness, foreign keys, and lifecycle constraints in
  PostgreSQL as well as domain code where the database can enforce them.
- Use `timestamptz` and UTC instants for persisted time. Presentation timezone
  belongs at the client boundary.
- Store session token hashes only; never store or log the bearer token. Session
  rows include owner, expiry, revocation, and timestamps needed for immediate
  invalidation.
- Keep Track identity stable for the same root and normalized relative source
  path. Rename/move appears as an old missing source and a new source in Core 0;
  weak similarity cannot update the old identity.
- Preserve missing sources and derived entities for diagnosis. Core 0 performs
  no automatic physical purge.
- Store provenance for key metadata: current value, source kind, inferred flag,
  scan run, and observation time.
- Store artwork bytes in ROOMusic-managed data storage, not PostgreSQL; persist
  source/hash/MIME/dimensions/storage references.
- Use JSONB only for genuinely variable, versioned evidence or diagnostics.
  Do not hide core identities, relations, revisions, or query-critical fields in
  an untyped JSON document.

## Migrations

- Put ordered migration files in `backend/migrations/`.
- Every schema change is an explicit, reviewable migration; application startup
  must not infer schema from structs.
- Never edit a migration after it has been shared or applied outside a disposable
  local database. Add a corrective migration.
- Make additions backward-compatible across the intended deployment transition
  when feasible. Split destructive changes into expand, backfill, switch, and
  contract steps.
- A migration claiming to be reversible must actually restore the prior schema
  and data contract. Otherwise document restore/forward-fix requirements instead
  of writing a destructive fake down migration.
- Select and document the migration command/tool with the first migration. V0's
  historical tool choice is not automatically inherited.

### ROOMusic 执行器合同

#### 1. 范围 / 触发

- 触发条件：启动边界新增或改变 PostgreSQL schema 迁移、迁移追踪字段、并发锁或
  回滚语义时，必须同时更新本合同和对应集成测试。
- 所有权：`backend/cmd/roomusic/database.go` 负责发现、计划、执行和记录；
  `backend/migrations/` 是唯一 SQL 来源。

#### 2. 签名（命令 / 数据库）

- `applyMigrations(ctx context.Context, connection *sql.DB) error` 是启动入口；
  测试可通过 `applyMigrationsFromFS` 注入 `fs.FS`。
- `schema_migrations` 至少包含 `version BIGINT PRIMARY KEY`、
  `applied_at TIMESTAMPTZ`、`name TEXT` 和 `checksum TEXT`；迁移 0008 负责追加
  后两列。
- 事务必须在执行 SQL、读取 tracking 表和写入元数据时使用同一个
  `*sql.Tx`，锁 key 固定为 `0x524f4f4d55534943`。

#### 3. 合同（输入 / 输出 / 边界）

- 输入是 `embed.FS` 中原始迁移字节；文件名为 `NNNN_name.sql`，版本从 1 连续；
  校验和是原始字节的 lowercase SHA-256。
- 成功输出是每个发布版本恰好一行 `version/name/checksum/applied_at`；重复启动
  不重放 SQL，也不改写既有 `applied_at`。
- tracking 表探测故意使用未限定的 `to_regclass('schema_migrations')`，使探测和
  迁移 SQL 遵循同一个 PostgreSQL `search_path`（不能拼接未转义的 schema 名）。
- 事务提交前必须重新读取并校验 tracking 表；迁移 SQL 写入的未知版本、重复行或
  名称/校验和漂移一律失败。迁移文件不得包含 `COMMIT`、`ROLLBACK` 或依赖事务外
  执行的语句。

#### 4. 校验与错误矩阵

| 条件 | 执行器行为 |
| --- | --- |
| 文件名非法、版本重复或有缺口 | 在开启事务前返回错误 |
| 已记录名称/校验和与当前文件不符 | 带版本上下文返回错误并回滚 |
| tracking 表含未发布版本（包括迁移期间新增） | fail closed 并回滚 |
| 旧库只有可信的 1/6/7 记录 | 将 2--5 一次性基线，不重放旧 SQL |
| advisory lock 等待被取消、SQL/记录/提交失败 | 回滚全部 DDL 和记录，启动不 ready |

#### 5. 良好 / 基线 / 错误案例

- 良好：全新库一次事务执行 0001--0008，记录 1--8 的文件名和哈希；第二次启动
  只校验记录。
- 基线：旧执行器留下 1、6、7，且业务表已存在；只补 2--5 元数据并执行新增迁移。
- 错误：手工插入版本 99、篡改哈希，或在迁移 SQL 中插入版本 99；执行器拒绝提交，
  不尝试猜测或修复损坏 schema。

#### 6. 必需测试（断言点）

- 无数据库单元测试：版本发现、连续性、重复/非法文件名和原始字节哈希。
- PostgreSQL 18 集成测试：fresh、rerun 时间戳稳定、旧 1/6/7 基线、0008 已提交
  但缺行、漂移/未知版本、迁移期间新增未知版本、事务回滚、并发锁和锁等待取消；
  断言失败后 schema/记录不存在且同一连接可重试。

#### 7. 错误 vs 正确

错误：迁移 SQL 执行后只遍历当前发布版本并 upsert tracking 行，忽略表中额外的
  版本。

正确：在提交前重新读取完整 tracking 表，先验证每一行属于当前发布集合且元数据
  一致，再为缺失的发布版本补记记录。

## Testing

Repository integration tests use a disposable PostgreSQL database at the
supported version and cover constraints, rollback, conflict, idempotency, and
representative query shape. Scanner integration tests must prove incomplete
scans cannot mark sources missing.

迁移治理测试还必须覆盖全新库、重复启动时间戳稳定、旧 1/6/7 历史升级、名称/
校验和漂移、未知版本、事务回滚、并发 advisory lock 和锁等待上下文取消；集成
入口为 `./scripts/test-integration.sh`，使用隔离 schema 和 PostgreSQL 18。

## Anti-Patterns

- Cross-module joins from arbitrary repositories because all tables share one
  database.
- Long filesystem or network work inside a transaction.
- Last-write-wins updates without an expected revision.
- Redis-backed sessions or Meilisearch-only search in the Core 0 acceptance path.
- Returning raw SQL errors or constraint names to REST clients.
- Treating Operation Journal, scan history, and runtime logs as one table.

These rules implement the transaction and authority requirements in the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
