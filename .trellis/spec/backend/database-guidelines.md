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

**迁移 0021/0022（M5 P 线，本分支编号）**：独立typed收藏与歌单能力，不借用 `operations.Run`。收藏共用本人集合版本；歌单使用PKEY/PQUO事务锁、间隔键、同事务成功收据与适应撤销。成功收据不随歌单删除，撤销可暂时超过普通10,000首配额；强owner复合外键与引用RESTRICT规则见[本人收藏与歌单合同](./personal-contracts.md)。新增表属于additive迁移，fresh/rerun仍由现有执行器校验，不改既有迁移。

Do not hold a database transaction open while walking a library tree or reading
audio tags. Persist scan progress in bounded batches and finalize the scan with
a transaction that applies negative reconciliation only when the outcome is
complete and successful.

## 延迟敏感只读事务与 JIT

延迟敏感的只读请求可在 `BeginTx` 成功后、业务查询前，用该 `*sql.Tx.ExecContext(ctx, "SET LOCAL jit = off")` 去除 JIT 编译开销；设置失败按查询错误返回并回滚，不能继续读取。只影响当前事务，禁止会话级 `SET`、连接池初始化参数或数据库全局修改。采用者必须有集成测试验证事务内 `current_setting('jit')='off'`，提交/回滚后在同一连接（核对 backend PID）恢复调用前默认值，并测试设置失败后不执行业务查询、不泄漏事务。应保留实际规模的执行计划与延迟证据，尽量从查询形状修复空表误估；禁止更改 `enable_*` 等 planner 开关、其他优化器参数、在业务事务执行 `ANALYZE` 或缩小性能夹具。该许可不扩展到写事务。

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

- `postgres.Open(ctx, url, options...)` 调用 `applyMigrations` 作为启动入口；选项 `WithMigrationBackups`（迁移前备份）、
  `WithLogger`、`WithMigrationFS`（测试注入迁移集合）；测试也可直接调用 `applyMigrationsFromFS`。
- `schema_migrations` 至少包含 `version BIGINT PRIMARY KEY`、
  `applied_at TIMESTAMPTZ`、`name TEXT` 和 `checksum TEXT`；迁移 0008 负责追加
  后两列。`down_sql TEXT`、`down_checksum TEXT` 由执行器在迁移前备份成功后 `ADD COLUMN IF NOT EXISTS`，不对应迁移文件。
- 事务必须在执行 SQL、读取 tracking 表和写入元数据时使用同一个
  `*sql.Tx`，锁 key 固定为 `0x524f4f4d55534943`。

#### 3. 合同（输入 / 输出 / 边界）

- 输入是 `embed.FS` 中原始迁移字节；文件名为 `NNNN_name.sql`，版本从 1 连续；
  校验和是原始字节的 lowercase SHA-256。版本 ≥ 31 必须有同名 `NNNN_name.down.sql`，0001–0030 不得有；
  回退文件首行为 `-- roomusic:irreversible <理由>` 时表示不可回退（理由必填，其后不得有 SQL）。
- 启动顺序：取锁 → 只读探测与读取历史 → 计划（升级 / 降级 / 不变）→ 有变化且库已存在时迁移前备份 → 补回退列 →
  降级（倒序执行库里保存的回退 SQL 并删行）或升级 → 记录元数据 → 提交。备份前事务只允许持有迁移 advisory lock
  和不高于 `AccessShareLock` 的表锁，任何 DDL（含加回退列、执行 0001、执行 0008）都在备份成功之后。
- 降级执行的是**库里保存的**回退 SQL；当前程序文件中的回退副本与库不同时只记 `platform.migration.rollback_differs` 警告。
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
| 版本 ≥ 31 缺回退文件、0001–0030 有回退文件、回退文件无对应正向迁移、不可回退标记缺理由 | 在开启事务前返回错误 |
| 迁移前备份失败 | `ErrPreMigrationBackup`，不执行任何 DDL |
| 库里有未知的更新版本：不连续、缺回退 SQL 或标记不可回退 | `SchemaAheadError`（错误文本保留 `unknown version N`），不改库，日志 `downgrade_refused` |
| 回退 SQL 执行失败 | `RollbackFailedError`，整体回滚，日志 `downgrade_failed` 附迁移前备份 |

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

### 回退 SQL 规则（迁移作者，2026-11-06 起）

1. 每个新迁移同时提交 `.down.sql`；确实无法回退时写 `-- roomusic:irreversible <理由>`，并在任务设计里说明降级时用哪份备份恢复。
2. 回退要把结构和旧版本依赖的数据合同恢复到升级前：删掉新建的表、列、索引、约束、视图、函数、触发器、类型和种子行，恢复被删除或改窄前的约束。
   新表、新列里的数据随回退丢弃，这是降级的预期代价。
3. 新增取值的回退先清理新取值：放宽 CHECK、新增枚举值、新增 `operations.type` 等，回退先 DELETE 或 UPDATE 使用新取值的行，再恢复更窄的约束；
   往返测试缺少对应的 `rollbackFixtures[v]` 会失败。
4. 回退 SQL 与正向 SQL 同在一个事务：不得包含 `COMMIT`、`ROLLBACK`、`CONCURRENTLY`、`ALTER TYPE … ADD VALUE`、`VACUUM` 等事务外语句；不得删除扩展等数据库级共享对象。
5. 恢复唯一性约束前，若新版本允许的重复数据会让重建失败，回退先合并或删除重复行；做不到就标记不可回退。
6. `.sql` 与 `.down.sql` 一旦进入任何 `vX.Y.Z` 标签都不得修改；回退写错时在之后的版本加修正迁移，并在发布说明写明从受影响版本降级要用备份。
7. `/data` 目录格式变化要版本化：封面（`<DATA_DIR>/artwork/`）与派生缓存（`<DATA_DIR>/media-cache/v1/`）换新的版本子目录或新文件名，
   不原地改写旧版本会读的文件。

## Testing

Repository integration tests use a disposable PostgreSQL database at the
supported version and cover constraints, rollback, conflict, idempotency, and
representative query shape. Scanner integration tests must prove incomplete
scans cannot mark sources missing.

迁移治理测试还必须覆盖全新库、重复启动时间戳稳定、旧 1/6/7 历史升级、名称/
校验和漂移、未知版本、事务回滚、并发 advisory lock 和锁等待上下文取消；集成
入口为 `./scripts/test-integration.sh`，使用隔离 schema 和 PostgreSQL 18。
回退相关：每个带回退的真实迁移由 `migrations_rollback_integration_test.go` 做升级 → 回退 → 再升级往返并比对 schema 快照；
执行器测试覆盖升降级往返、拒绝不改库、回退 SQL 失败原子回滚、迁移前备份失败、回退副本不同只告警、旧库补列在事务内；
`internal/backup/migration_backup_integration_test.go` 覆盖库缺回退列与降级前的迁移前备份不被锁住。

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

### 并行测试的统计隔离

同库独立 schema 的测试不得执行不带表名的 `ANALYZE`：它不受 `search_path` 限制，会更新其他测试 schema 的统计。需要夹具统计时使用 `postgrestest.AnalyzeSchema(t, ctx, db, onlyTables...)`：无表名时分析完整当前schema；可选allowlist仅接受当前schema存在的普通表/分区表原始名，全部验证通过后再执行，未知表拒绝且不更新任何统计。表标识符只来自PostgreSQL `format('%I.%I')`，重复表去重。分阶段统计测试用allowlist保留辅助空表reltuples=-1→0断言，不得为规避污染放松断言或改为串行运行。

### M5 ⑤迁移与解析缓存

0026集中创建艺人身份/名字/事实/意图/派生/证据/候选及operations artist目标关联，0027创建personal拥有的typed艺人收藏。沿用catalog_projection_state新增artists能力，发行代次标记用于短批续跑；发布前同事务调用个人owner刷新解析。迁移不重写已发布0001–0025，艺人不物理删除；人工意图操作FK延迟检查，扫描不得写它们。接口与验证见[艺人合同](./artist-contracts.md)。

0028增加artist_profiles.applied_generation（默认0）及后台清单artist类型；0不伪造完成，新代次必须由真实派生事务写marker。已有完整ready投影内容不变，无需因marker增加而重新规范化；进行中build补齐身份清单，发布只做有索引的缺口判断。在线写者不能以“补缺”为由把无关全库工作放进CATP临界区。

0029仅新增`artist_effective_backfill_idx(artist_id,name_id,track_id,release_id)`。依据完整规模同SQL有无索引计划，支持热门身份计票及逐个不同名字遍历；不改旧迁移、Version或ready，不在DDL预填派生。迁移fresh/rerun仍由统一执行器检验。索引磁盘/WAL/写放大及完整批提交耗时必须一起记录；不能因cost下降不再触发JIT就把写事务关闭JIT当等价实现。

0030仅新增有效署名的`(artist_id,track_id) INCLUDE(release_id,role)`与`(artist_id,release_id) INCLUDE(track_id,role)`部分覆盖索引（对应来源非NULL），服务前台热门艺人命中说明的分支定位。不改既有迁移、事实、代次或发布状态；I9已接受回填性能，不为本轮新索引重启回填验收。

### I8：纯派生回填批的局部异步提交许可

仅单worker回填**应用批**可选择在该`*sql.Tx`内执行`SET LOCAL synchronous_commit = off`；选择前必须逐项审计写集：只含可从既存权威重建的派生投影及其自身进度/完成代次，二者仍原子提交。崩溃恢复可重做未持久化批，不允许投影和进度分开提交。该许可不是全局默认，也不是必须采用。

禁止用于M3、扫描、任何用户写入、治理意图、个人数据、扫描事实或稳定身份权威；发布事务若刷新收藏也不符合条件。不能仅因函数叫backfill就放行，例如艺人Refresh可能建立身份/名字键并恢复tracks.artist，不可将整条事务视作纯投影。只读JIT许可与此独立，不允许写事务关JIT、planner开关或业务ANALYZE。

采用者必须集成测试：同一连接/PID原设置在批事务内变为off，COMMIT后恢复原值；ROLLBACK/取消也恢复，设置失败短路且不提交进度；M3/扫描/用户写者没有此设置。必须记录实际采用范围和未采用理由，不得仅靠mock断言SQL字符串。错误示例：连接池初始化关闭同步提交，或对包含个人解析刷新/扫描事实的事务一概关闭；正确示例：明确纯搜索派生应用批中局部设置并与进度原子提交，用户业务事务仍用默认持久性。

### 串行回填的计划取证

复用连接的SQL不能只凭新连接一次EXPLAIN判定性能。M5艺人回填曾在第6次从custom转generic后，对热门署名逐条查询tracks，JIT仍为0；必须分清计划切换与JIT成本。诊断先完成未受EXPLAIN扰动的真实串行批，再读取同连接`pg_prepared_statements`计数并对实际prepared语句和实际参数取计划；独立连接回放作对照，保留SQL/SHA/参数及完整提交耗时。原始参数仅允许合成隔离夹具的显式诊断落仓外，不能新增业务原文日志。

按数据密度选等价SQL可以优化，但策略探测上限不能变成业务输出上限。至少覆盖阈值末项改变结果及跨阈值回落，并同时测稀疏/热门/missing：减少热门随机读取可能给稀疏批新增固定扫描成本。计划节点、COMMIT、选批和批间等待分别计时，不拿进程总墙钟误当某阶段时间；所有正式分位数仍取完整提交分布。
