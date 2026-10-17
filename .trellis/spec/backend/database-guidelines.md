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

指纹必须包含路由中的目标身份（目录停用/恢复为小写 root ID），不能只含请求体。同一幂等键的
请求先在事务内取得该键的事务级 advisory lock，再查记录（READ COMMITTED，使加锁后的查询能看到
前一个持锁事务已提交的记录），因此并发重试只执行一次，其余请求回放已存结果。当前实现见
`backend/internal/library/httpapi/root_operations.go` 的 `beginRootOperation`。

M3 起，发行字段修正、人工归组与撤销（M4 起还有默认版本的设定与问题的核对）使用共同的 `operations.Run`（`backend/internal/operations`）：它自己开启 READ COMMITTED 事务，
取得“操作者 + 幂等键”的事务级 advisory lock（`pg_advisory_xact_lock(int, int)`，类别 `OPER`，与迁移锁、扫描锁、目录操作锁 `ROOT` 互不共用键空间），
查记录并重放或报同键异义，在同一事务里调用拥有业务表的包提供的执行器，再写操作记录，一起提交；业务行引用操作记录的外键是
`DEFERRABLE INITIALLY DEFERRED`（记录在执行器之后才插入）。被拒绝与失败的尝试不写记录。**READ COMMITTED 下带连接的 `SELECT … FOR UPDATE`
等锁之后只重检被锁的行，连接进来的其他行仍取自语句开始时的快照：需要“锁住之后读到别人刚提交的变化”时，锁行与读取分成两条语句。**
规则与测试见[治理合同](./governance-contracts.md)；目录操作与成员管理仍用各自的流程。

**覆盖层模式**：人工修正放在独立的表（`release_corrections`，没有行就是“没有修正”），扫描只写扫描列，读取经视图（`release_effective`）叠加修正，
所以重扫不会覆盖修正是结构保证而不是约定；人工修改版本号 `releases.edit_revision` 只由受控操作递增，扫描从不改它，扫描与人工修改因此互不制造冲突。

**迁移 0018（M4，默认版本）**：`release_groups` 增加 `default_release_id`（外键 `ON DELETE SET NULL`，**不约束属于这个组**）与组自己的 `edit_revision`，
并放宽 `operations` 的类型与目标类型约束（约束名不变，先 `DROP` 再 `ADD`）。默认版本不在别处随归组或扫描清除，而是由读取时的排序判断是否生效
（可见且仍在这个组里），所以扫描与归组都不用改组行。加锁顺序与归组一致：先发行行、后组行。规则见[默认版本合同](./default-version-contracts.md)。

**迁移 0019（M4，核对问题）**：新表 `release_issue_reviews`（每个版本每类问题一行，记住核对时的依据），放宽 `operations_type_ck`。核对与修正一样是覆盖层：
扫描从不读写它，依据由读取时比较，依据变了问题就重新计入。核对只锁发行行；撤销时若要写回的前态有核对行（例如撤销“取消核对”），写回的行引用一条更早的操作记录，**先给那条记录加 KEY SHARE 锁、再锁发行行**，
与撤销那条记录的顺序（先 FOR UPDATE 记录、再锁发行行）一致，否则两个撤销交叉时死锁。规则见[核对合同](./issue-review-contracts.md)。

**迁移 0020（M4，认领）**：不新增表，只放宽 `operations_type_ck`（增加 `release_claim`）并建一个部分表达式索引 `operations_claim_source_index`（`after_state #>> '{source,release_id}'`，只含认领与它们的撤销记录），让操作记录能按**来源**发行筛选。认领按“记录 → 发行行（UUID 升序）→ 组行（UUID 升序）”加锁，新版本归入旧版本原来的组，旧版本移进新建的空组（组从不删除）；扫描仍不读写这四类状态。规则见[认领合同](./claim-contracts.md)。

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
- 曲目 UUID 在同一 root 内随音频内容延续，不随路径改变（阶段 I）：曲目先按当前来源身份（root + 规范化相对路径，
  CUE 另含 sheet、父来源、轨号与起点）匹配；同一来源身份下音频内容指纹变化视为换了内容，新建曲目。改名、移动只在该 root
  遍历完整时，才按指纹（含 CUE 区间）把没有匹配到身份的观察与本轮消失的曲目唯一 1:1 配对、沿用 UUID；歧义、跨 root、
  相似度都不重绑定，遍历不完整或被取消时不重绑定。规则与测试见 [目录与扫描流程合同](./library-scan-contracts.md)。
- Release UUID 按候选中已有曲目的成员关系延续（严格过半沿用）；`releases.candidate_anchor` 只记录最近一次的位置，
  不参与查找，也不要求唯一。
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
- 所有权：`backend/internal/platform/postgres/migrate.go` 负责发现、计划、执行和记录；
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
