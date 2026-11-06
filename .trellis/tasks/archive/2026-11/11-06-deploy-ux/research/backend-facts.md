# 调研：部署体验改进的后端现状事实

- **问题**：为“备份内置进 Go 应用、精简生产必填配置、GHCR 语义化版本镜像与可回退迁移”收集后端现状
- **范围**：仓库内部（只读）；第 8 项中 Alpine 软件包的版本来自记忆，未联网核实
- **日期**：2026-11-06
- **仓库**：`/home/qlqq/workspace/ROOMusic`（下文路径都相对于仓库根目录）

---

## 1. 迁移执行器 `backend/internal/platform/postgres/migrate.go`

### 1.1 入口与调用链

- `postgres.Open(ctx, url)`（migrate.go:53）先调用 `Connect`（:66，`sql.Open("pgx")` 加 `PingContext`），再调用 `applyMigrations`（:78），后者调用 `applyMigrationsFromFS(ctx, conn, migrations.Files)`（:141）。
- `migrations.Files` 来自 `backend/migrations/embed.go`，即 `//go:embed *.sql`。
- `postgres.Connect` 只连接、不迁移，`reset-password` 子命令用的就是它（见第 3 节）。

### 1.2 加载与校验（`loadMigrations`，:84-139）——在任何数据库副作用之前完成

- 文件名正则 `^([0-9]+)_[A-Za-z0-9][A-Za-z0-9._-]*\.sql$`（:28），版本必须是正整数（:107-113），不允许重复（:114）。
- **校验和是整个 .sql 文件原始字节的 SHA-256**，含注释和末尾换行（:121，`hex.EncodeToString`）。所以只要往已发布的迁移文件里追加任何内容（比如回退 SQL），校验和就会变。
- 版本必须从 1 起连续，否则报 `missing migration version`（:132-137）。
- `applyMigrationsFromFS` 还要求第一个是版本 1（:149），并且必须有元数据迁移 0008（:152-155，常量 `migrationMetadataVersion = 8`，:26）。

### 1.3 事务边界：**所有迁移在同一个事务里执行**

- 一次 `connection.BeginTx`（:157）覆盖：取锁、探测表、执行所有待执行迁移、写元数据、`Commit`（:227）。
- 任何一步出错都会触发 defer 回滚（:162-173），回滚失败会附加进返回的错误。
- 每个迁移用 `transaction.ExecContext(ctx, string(candidate.SQL))` 一次执行整份文件（`executeMigration`，:243-248），依赖 pgx 简单协议支持多语句。
- 测试 `TestPostgreSQLMigrationRollsBackOnFailure`（migrate_test.go:131）验证失败后什么都不留下。

### 1.4 Advisory lock

- 在事务内第一条语句执行 `SELECT pg_advisory_xact_lock($1::bigint)`（:175），键为 `0x524f4f4d55534943`（即 ASCII `ROOMUSIC`，:24）。
- 这是事务级锁：提交、回滚、断连或 ctx 取消时自动释放。
- 测试：`TestPostgreSQLMigrationConcurrentExecutorsSerialize`（migrate_test.go:305）、`TestPostgreSQLMigrationLockCancellation`（:332）。

### 1.5 表探测与 0008 元数据基线

- `schemaMigrationsTableExists` 使用未限定的 `to_regclass('schema_migrations')`（:250-256）。表不存在时先执行 0001（:183-187）。
- `schemaMigrationsMetadataColumns` 用 `pg_attribute` 探测 `name`、`checksum` 两列是否存在（:258-277）。`readAppliedMigrations` 在列缺失时读成 `NULL::text`（:279-312），发现重复版本就拒绝（:302）。
- `buildMigrationPlan`（:319-387）：
  - 每条历史行都要经过 `validateAppliedMigration`（:339，函数在 :440-452）：**版本不在内嵌集合里就报 `schema_migrations contains unknown version N`（:443）**；name 或 checksum 非空且不一致时报漂移（:445-450）。
  - 必须有版本 1（:344）。
  - `maximumRecorded > maximumAvailable` 时报 `schema_migrations contains future version %d (latest supported %d)`（:354-356）。
  - 两个元数据列都已存在、但没有 8 号行时，把 8 记为一次性基线，不再执行 ALTER（:359-366）。
  - 最大已记录版本 ≥ 6 时，要求 6 到最大版本连续（:367-372），2 到 5 缺失的作为基线、不重放（:373-377）。这对应旧版只记录了 1、6、7 的稀疏历史。否则要求 1 到最大版本全部连续（:379-383）。
- 执行循环（:202-218）：跳过已应用和基线版本。8 号在列已齐全时只标记、不执行（:206-208）。执行完 8 号后把列状态置为齐全（:214-217）。
- 执行后列仍不齐全就报错（:220-223）。
- `recordMigrationMetadata`（:389-438）：提交前**重新读取并校验整张 tracking 表**（:399-407，迁移 SQL 自己写进去的未知版本也会被拒绝），然后对每个内嵌版本执行 `INSERT … ON CONFLICT (version) DO UPDATE SET name=COALESCE(old,new), checksum=COALESCE(old,new) RETURNING`（:413-419），再次比对 name 和 checksum（:422-435）。

### 1.6 “future version”拒绝点（与回退设计直接相关）

- **实际先触发的是 :443 的 `unknown version`**，而不是 :355 的 `future version`：`buildMigrationPlan` 在 :339 逐行调用 `validateAppliedMigration`，内嵌集合里没有的版本会立即报 `unknown version`。所以 :354 那个分支只有在“历史行版本已知、但超过最大可用版本”时才可能命中，而当前数据结构下这种情况不会出现，等于不可达。
- 测试依据：`TestPostgreSQLMigrationRejectsDriftAndFutureVersion`（migrate_test.go:283-303）插入 99 号行后，断言错误包含 `"unknown version 99"`（:299）。`TestPostgreSQLMigrationRejectsFutureVersionCreatedDuringMigration`（:162-185）断言迁移 SQL 自己写入的 99 号行同样报 `unknown version 99`，并且整体回滚。
- 单元测试 `TestBuildMigrationPlanRejectsUnknownAndDrift`（:100）。
- 拒绝后的启动行为：`openDatabase` 返回错误，`main` **不退出**，而是以降级模式启动（main.go:44-47、:49）。此时 `/readyz` 返回 503，所有 `/api` 返回 503 `database_unavailable`（app.go:148-158）。Docker 的 HEALTHCHECK 访问 `/readyz`（Dockerfile:27-28），因此容器会变成 unhealthy。

### 1.7 不能放在事务里执行的语句

- 检查了 `CONCURRENTLY`、`VACUUM`、`ALTER TYPE … ADD VALUE`、`CREATE DATABASE` 和顶层 `BEGIN/COMMIT`：**30 个迁移里都没有**。`grep` 命中的 `BEGIN` 都在 `DO $$ … $$` 块内（0003:7、0010:24、0011:140、0012:8、0013:8、0015:8）。
- 0025:2 的 `CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public` 可以在事务里执行。
- 0030 的注释明确写着“沿用统一事务迁移”（0030:2）。所有 `CREATE INDEX` 都是普通（非并发）建索引，持有表锁。
- **0001、0006、0007 会在自己的 SQL 里写 schema_migrations**（0001:7-9、0006:4、0007:24），用的是 `INSERT … ON CONFLICT DO NOTHING`。

### 1.8 测试文件

| 文件 | 内容 |
|---|---|
| `backend/internal/platform/postgres/migrate_test.go` | 单元测试：加载与校验（:38）、稀疏历史（:78）、未知版本与漂移（:100）、0008 基线（:120）。集成测试（`TestPostgreSQL*`）：失败回滚（:131）、迁移内写入未来版本（:162）、全新库与重复执行（:187）、旧版稀疏升级（:225）、0008 列已提交但缺行（:255）、漂移与未来版本（:283）、并发串行（:305）、锁等待取消（:332）、`Connect` 不迁移（:485）。辅助函数 `testMigrationFS`（:391）用 `fstest.MapFS` 注入迁移 |
| `backend/internal/platform/postgres/migrate_catalog_search_test.go` | 断言 0025 排在扫描反馈迁移之后（:9） |
| `backend/internal/platform/postgres/postgrestest/postgrestest.go` | 通过 `ROOMUSIC_TEST_DATABASE_URL`（:20）为每个测试建立隔离 schema（`NewSchema`）。未配置该变量时 `t.Skip` |
| `scripts/test-integration.sh:29` | 只运行 `-run 'TestPostgreSQL|TestFFmpeg'` 的集成测试 |

### 1.9 相关规范

- `.trellis/spec/backend/core0-runtime-contracts.md:114-126`：写明“**不提供 down migration，生产回退使用备份或 forward-fix**”（:124）。
- `.trellis/spec/backend/database-guidelines.md:132-175`：其中 :142-144 写明“声称可逆的迁移必须真的恢复之前的 schema 和数据合同，否则记录恢复或 forward-fix 要求，而不是写一个破坏性的假回退迁移”。:150 起是“ROOMusic 执行器合同”，触发条件包括改变回滚语义时必须同时更新合同和集成测试。

---

## 2. 30 个迁移的分类

类型说明：**结构新增**指只建表、加列、加索引或约束；**放宽约束**指 DROP 后重新 ADD 一个范围更宽的 CHECK，或删除唯一索引/约束；**数据变换**指 UPDATE、DELETE 或合并已有行；**种子**指 INSERT 固定行。所有迁移都没有删除表或删除列。

| 版本 | 文件 | 类型 | 回退难度 | 备注 |
|---|---|---|---|---|
| 0001 | `0001_core_scaffold.sql` | 结构新增 | 不应回退 | 建 `schema_migrations` 并自己写入版本 1 |
| 0002 | `0002_core_slice.sql` | 结构新增 | 易（形式上） | users、setup_state、sessions、library_roots、scan_runs、releases、media、tracks、scan_diagnostics。回退等于清空整个库，属于基线 |
| 0003 | `0003_catalog_observations.sql` | 结构新增 | 易 | 建 release_groups；为 releases.group_id 加外键（DO 块）；releases 加 source_root_id、relative_directory；建唯一索引 `releases_source_directory`；建 track_observations |
| 0004 | `0004_field_provenance.sql` | 结构新增 | 易 | track_observations 加 field_name、value 两列和一个索引 |
| 0005 | `0005_release_artwork.sql` | 结构新增 | 易 | 建 release_artworks（storage_key 带 UNIQUE） |
| 0006 | `0006_multi_user.sql` | 结构新增 | 易（会丢信息） | users 加 role（默认 admin）和 disabled_at；加索引；自己写入版本 6 |
| 0007 | `0007_root_operations.sql` | 结构新增 | 易 | library_roots 加 status、revision、updated_at；建 library_root_operations 和两个索引；自己写入版本 7 |
| 0008 | `0008_migration_metadata.sql` | 结构新增（执行器元数据） | 不应回退 | schema_migrations 加 name、checksum。执行器硬编码依赖它（migrate.go:26、:152、:220） |
| 0009 | `0009_scan_cancellation.sql` | 结构新增 | 易 | scan_runs 加 cancel_requested_at；建部分索引 |
| 0010 | `0010_album_organization.sql` | 结构新增 + 删除唯一索引 | 中 | releases 加 4 列，tracks 加 7 列；建两个部分唯一索引；**DROP INDEX releases_source_directory**（:16）；建 release_field_decisions（带两个 CHECK）和 release_grouping_evidence。重建 0003 的唯一索引可能因为数据已经重复而失败 |
| 0011 | `0011_scan_staging_and_identity.sql` | **数据变换** + 结构新增 + 放宽约束 | **不可逆** | 建 scan_observations；再次 DROP releases_source_directory；tracks 加 7 列，releases 加 4 列；**删除 release_artworks 的 storage_key 唯一约束**（:33）；建 release_credits。**数据变换**：candidate_anchor 从 v1 改写为 v2（:49-89）；source_identity 改写（:91-95）；**合并重复的 media，并删除多余 media 行**（:97-110）；**删除重复的 track_observations**（:114-123）；**删除重复的 release_grouping_evidence**（:127-135）；随后建唯一索引和三个 CHECK |
| 0012 | `0012_track_credits_and_release_metadata.sql` | 结构新增 | 易 | releases 加 edition、label、barcode 和三个 CHECK；建 track_credits 和一个索引 |
| 0013 | `0013_track_content_fingerprints.sql` | 结构新增 + 删除唯一约束 | 中 | tracks 加 3 列和两个 CHECK；建部分唯一索引 `tracks_bound_source_path_uq`；**DROP CONSTRAINT tracks_source_root_id_relative_path_key**（:31）；scan_observations 加 13 列和两个索引。之后同一路径上可以并存新旧曲目，重建原唯一约束可能失败 |
| 0014 | `0014_release_anchor_location.sql` | 删除唯一索引 + 加列 | 中 | **DROP INDEX releases_candidate_anchor_uq**（:4）；scan_observations 加 planned_release_id。之后锚点允许重复，重建唯一索引可能失败 |
| 0015 | `0015_track_lyrics.sql` | 结构新增 | 易（会丢歌词） | tracks 加 lyrics、lyrics_source 和一个 CHECK |
| 0016 | `0016_play_history.sql` | 结构新增 | 易（会丢播放历史） | 建 play_history 和一个索引 |
| 0017 | `0017_governed_operations.sql` | 结构新增（含视图） | 易（会丢治理数据） | 建 operations；releases 加 edit_revision；建 release_corrections；**建视图 release_effective**（:67-96，依赖 releases 的多列）。回退时要先 DROP VIEW。注意这里用的是不带 IF NOT EXISTS 的 CREATE |
| 0018 | `0018_release_group_default.sql` | 结构新增 + 放宽 CHECK | 中 | release_groups 加 default_release_id、edit_revision；放宽 operations_type_ck 和 operations_target_type_ck。恢复更窄的 CHECK 前必须删除使用新取值的 operations 行 |
| 0019 | `0019_release_issue_reviews.sql` | 结构新增 + 放宽 CHECK | 中 | 建 release_issue_reviews；放宽 operations_type_ck |
| 0020 | `0020_release_claims.sql` | 放宽 CHECK + 表达式索引 | 中 | 放宽 operations_type_ck（加入 release_claim）；建表达式部分索引 `operations_claim_source_index` |
| 0021 | `0021_favorites.sql` | 结构新增 | 易（会丢收藏） | 建 personal_favorite_state、favorite_tracks、favorite_albums 和索引 |
| 0022 | `0022_playlists.sql` | 结构新增 | 易（会丢歌单） | 建 personal_playlists、personal_playlist_items、personal_playlist_requests、personal_playlist_undo，有复合外键和可延迟约束 |
| 0023 | `0023_scan_feedback.sql` | 结构新增 | 易（会丢扫描反馈） | scan_runs 加 17 列和一个表级 CHECK；建 scan_reports、scan_catalog_changes、scan_added_releases 和一个索引 |
| 0024 | `0024_catalog_recent_additions.sql` | 加索引 | 易 | 只有一个索引 |
| 0025 | `0025_catalog_search.sql` | 结构新增 + 扩展 + 种子 | 易（投影可重建） | `CREATE EXTENSION pg_trgm`；建 catalog_projection_state 并 **INSERT 'search' 行**；建 subject_state、phases、work、catalog_search_values 和若干 GIN、表达式索引；在 releases 上建一个索引。回退是否 DROP EXTENSION 需要单独决定 |
| 0026 | `0026_artist_catalog.sql` | 结构新增 + 放宽 CHECK + 种子 | 中（会丢艺人治理） | 放宽 capability CHECK 并 **INSERT 'artists' 行**；建 artists 等十余张表；放宽 operations 的 type 和 target_type CHECK（加入 artist_*）；建 operations_artist_targets；**写入种子艺人“周杰伦”等**（:126-131）。回退需要先删除 artist 类 operations 行和 'artists' 状态行，再恢复 CHECK |
| 0027 | `0027_artist_favorites.sql` | 结构新增 | 易（会丢艺人收藏） | 建 artist_favorites 和三个索引 |
| 0028 | `0028_artist_profile_backfill.sql` | 加列 + 放宽 CHECK | 中 | artist_profiles 加 applied_generation；把 catalog_projection_phases 和 catalog_projection_work 的 subject_kind CHECK 放宽为包含 'artist'。恢复前要删除 'artist' 行（这些是可重建的投影工作行） |
| 0029 | `0029_artist_backfill_indexes.sql` | 加索引 | 易 | 一个索引 |
| 0030 | `0030_artist_search_subject_indexes.sql` | 加索引 | 易 | 两个 INCLUDE 部分索引 |

汇总：
- **不可逆**：0011（合并和删除数据、改写身份）。
- **不应回退**：0001、0008（执行器基础）。
- **中等**（恢复唯一性或更窄的 CHECK 前必须先删除或清理数据）：0010、0013、0014、0018、0019、0020、0026、0028。
- 其余是结构新增，只需 DROP 对应表、列和索引，但会丢掉新功能写入的数据。
- 当前最新版本是 0030。

---

## 3. 应用启动、后台任务、关闭与子命令

### 3.1 `backend/cmd/roomusic/main.go` 的启动顺序

1. 只要带了参数，就走 `runCLICommand(os.Args[1:])`，返回后退出（:33-35）。
2. 创建 JSON slog 日志器（:36），并用 `signal.NotifyContext(SIGINT, SIGTERM)` 建立应用生命周期 ctx（:37）。
3. `config.Load()`（:39）失败时记录 `platform.config.invalid` 并 `os.Exit(1)`（:40-43）。
4. `openDatabase`（:44，函数在 :118-132）依次执行 `postgres.Open`（连接加全部迁移）、`app.InitializeCatalog`（即 `maintenance.Initialize`，用短事务登记投影目标版本，app.go:126-128）、`library.RecoverInterruptedScans`。**任何一步失败都只记录 `platform.database.unavailable`，然后以降级模式继续启动**（:45-47、:49）。
5. 数据库可用时：`app.OpenDerivation`（:56，打开 `<DATA_DIR>/media-cache` 并自检 ffmpeg）。缓存目录与音乐根重叠时 `os.Exit(1)`；找不到 ffmpeg 只告警。然后 `app.New(...)`（:65）。
6. 创建 `http.Server{Addr, ReadHeaderTimeout: 5s}`（:68-72），**没有设置 WriteTimeout 或 ReadTimeout**。在 goroutine 里 `ListenAndServe`（:76-78）。
7. 等待 ctx 取消或服务出错（:80-84）。

### 3.2 关闭顺序（共用一个 10 秒的 shutdownContext，:85）

`server.Shutdown`（:87）→ `application.WaitForScans`（:91）→ `application.WaitForCatalogMaintenance`（:96）→ `derivations.Close(5s)`（:102）→ `database.Close`（:107）。

### 3.3 现有后台 worker 和定时器模式

- **扫描**：没有定时扫描或调度，只有管理员手动触发 `POST /api/v1/scans`。`library.Service`（`internal/library/service.go:14-24`）持有 `scanContext`（即应用生命周期 ctx）和 `scanWorkers sync.WaitGroup`。`StartScan`（`internal/library/scans.go:309-363`）持有 `scanMutex`，取得跨进程的扫描执行锁（`acquireScanExecution`），提交 scan_runs 行，然后 `scanWorkers.Add(1); go func(){ defer Done(); runScan(...) }()`（:353-357）。`WaitForScans`（service.go:35-47）以“WaitGroup 加 ctx”的方式有界等待。
- **目录后台维护**：`app.New` 里启动 `go maintenance.Run(lifecycle, db, logger, ...)`，并用 `catalogDone` channel 通知退出（app.go:110-120）。`Run`（`internal/catalog/maintenance/worker.go:27-68`）是一个循环：在专用 `db.Conn` 上用 `pg_try_advisory_lock(0x4341544d, hashtext(current_schema()))` 选主（:71-77）；出错时记录日志，用 `wait(ctx, 1s)` 退避（:184-193，基于 `time.NewTimer` 和 `select ctx.Done`）。`WaitForCatalogMaintenance`（app.go:131-138）用 select 等待。
- **其他计时器**：扫描进度每 500ms 一次的 ticker（`internal/library/scanner.go:170`）；派生管理器的 `jobsDone sync.WaitGroup`（`internal/playback/derivation/manager.go:139`）和 `time.NewTimer`（:556）；`derivation/clock.go:17` 用了 `time.AfterFunc`。
- 仓库里**没有**通用的 cron 或调度器组件。

### 3.4 子命令

- `backend/cmd/roomusic/reset_password.go`：`runCLICommand`（:50-61）在终端模式下保存并恢复终端状态。`runCommand`（:63-80）用 `switch args[0]` 分发 `help`、`-h`、`--help` 和 `reset-password`，未知命令返回 2。
- `runResetPassword`（:82-120）：读取密码 → `env.databaseURL()`（即 `config.DatabaseURL`，只要求 `ROOMUSIC_DATABASE_URL`，config.go:166-172）→ 用 30 秒超时调用 `env.connect`（即 `postgres.Connect`，**不迁移**）→ `identity.ResetPasswordByUsername`。
- 依赖通过 `resetPasswordEnv` 结构体注入（:24-32），测试在 `reset_password_test.go`（645 行）。
- 生产调用方式：`make prod-reset-password U=…`，实际执行 `compose exec app /app/roomusic reset-password`（scripts/production.sh:182）。

---

## 4. 配置

### 4.1 应用实际读取的环境变量（唯一位置：`backend/internal/platform/config/config.go`）

| 变量 | 必填 | 默认值 | 校验位置 / 规则 |
|---|---|---|---|
| `ROOMUSIC_DATABASE_URL` | **必填** | 无 | config.go:166-172，去掉首尾空白后不能为空 |
| `ROOMUSIC_ALLOWED_LIBRARY_ROOTS` | **必填** | 无 | config.go:76-83，逗号分隔，至少一个 |
| `ROOMUSIC_DATA_DIR` | 可选 | `./data` | config.go:52-55 |
| `ROOMUSIC_HTTP_ADDR` | 可选 | `:8080` | config.go:158-163 |
| `ROOMUSIC_ENV` | 可选 | `development` | config.go:151-156；值为 `production` 时影响来源模式 |
| `ROOMUSIC_ALLOWED_ORIGINS` | 可选 | 空 | config.go:106-136，逐项用 `weborigin.ParseConfigured` 校验并去重；不能和 `ROOMUSIC_SECURE_COOKIES` 同时使用（:126）；`ROOMUSIC_PUBLIC_URL` 必须在清单内（:129-134） |
| `ROOMUSIC_PUBLIC_URL` | 可选 | 空 | config.go:56-62 要求是绝对 origin；生产环境且清单为空时不允许设置（:95-97） |
| `ROOMUSIC_SECURE_COOKIES` | 可选 | false（只有 `"true"` 才生效） | config.go:75；生产环境且清单为空时不允许设置（:92-94） |
| `ROOMUSIC_FFMPEG_PATH` | 可选 | 空，表示在 PATH 中查找 | config.go:63-66，必须是绝对路径 |
| `ROOMUSIC_MEDIA_CACHE_MAX_MIB` | 可选 | 10240 | config.go:139-149，范围 256 到 2^30 |

- 来源模式（config.go:88-101）：清单非空时为 `allow-list`；生产环境且清单为空时为 `same-host`；其余为 `legacy`。
- 配置测试在 `backend/internal/platform/config/config_test.go`（`setRequired` 辅助函数在 :10）。

### 4.2 生产 Compose 和 `.env.prod` 层面的变量（不由 Go 读取）

- `deploy/production/compose.yaml` 中 app 服务固定设置：`ROOMUSIC_ENV=production`、`HTTP_ADDR=:8080`、`ALLOWED_LIBRARY_ROOTS=/music`、`DATA_DIR=/data`、`FFMPEG_PATH=/usr/local/bin/ffmpeg`（:14-21）。`DATABASE_URL` 由 `ROOMUSIC_DB_PASSWORD` 拼出（:16）。
- `.env.prod.example` 中**必填**的有 `ROOMUSIC_DB_PASSWORD`、`ROOMUSIC_HTTP_PORT`、`ROOMUSIC_MUSIC_DIR`、`ROOMUSIC_BACKUP_DIR`、`ROOMUSIC_SUBNET`（compose 里用 `:?` 强制：:16、:24、:27、:47、:70、:76、:105）。可选的有 `ROOMUSIC_ALLOWED_ORIGINS`、`ROOMUSIC_TIMEZONE`（默认 Asia/Shanghai）、`ROOMUSIC_BACKUP_HOUR`（默认 3）、`ROOMUSIC_BACKUP_KEEP_DAYS`（默认 14）、`ROOMUSIC_MEDIA_CACHE_MAX_MIB`（默认 10240）。
- 还有两个镜像选择变量：`ROOMUSIC_IMAGE`（默认 `roomusic-prod-app:current`，:11）和 `ROOMUSIC_TOOLS_IMAGE`（默认 `roomusic-prod-tools:current`，:61、:93）。两者都设置了 `pull_policy: never`。
- `scripts/production.sh`：`preflight_directories` 要求 `MUSIC_DIR` 和 `BACKUP_DIR` 是已存在的绝对目录（:85-91）；校验 `HTTP_PORT` 在 1 到 65535（:82-83）。
- 仓库里其他 `ROOMUSIC_*` 变量（`TEST_*`、`SMOKE_*`、`CATALOG_EXPLAIN*`、`REDIS_URL`、`MEILISEARCH_*`、`AUTH_*` 等）只出现在测试、冒烟脚本、开发 compose 或旧文档里，生产代码不读取。

### 4.3 `ROOMUSIC_DATA_DIR` 下的子目录

| 子目录 | 来源 | 内容 |
|---|---|---|
| `artwork/` | `internal/catalog/artwork/artwork.go:35`（`NewStore`），写入时 `MkdirAll(0750)`（:154） | 内容寻址的封面文件，文件名为 storage_key（哈希加扩展名），由 release_artworks.storage_key 关联 |
| `media-cache/` | `internal/app/derivation.go:17,24` | ALAC 派生缓存：`v1/<键>.flac` 是成品，`tmp/*.partial` 是生成中的文件（`internal/playback/derivation/cache.go:30-33`，目录权限 0700，启动时清空 tmp 并重建索引） |

- 没有其他子目录。扫描服务把 `dataDirectory` 传给 `library.NewService`（app.go:90），只用于受管封面。
- 生产环境中 `/data` 是命名卷 `app-data`（compose.yaml:31、:108），而容器根文件系统是 `read_only: true`（:32），`/tmp` 是 64MB 的 tmpfs（:33）。所以**app 容器里目前只有 `/data` 和 `/tmp` 可写**。
- README:167 写明：备份只覆盖 PostgreSQL，`/data` 里的派生缓存和封面不在备份范围内。

---

## 5. 版本信息

- **二进制里没有版本号或提交号**：没有 `-X main.version` 之类的 ldflags（Dockerfile:9 只有 `-trimpath -ldflags="-s -w"`）；也没有用 `debug.ReadBuildInfo`。镜像由 `git archive` 导出的树构建、不含 `.git`，因此 Go 的 vcs 构建信息也不会被嵌入。
- `/healthz` 返回 `{"status":"ok"}`；`/readyz` 返回 `{"status":"ready"}` 或 503 `{"status":"not_ready"}`（`internal/platform/httpserver/health.go:6-19`）。两者都不带版本信息。
- 所有 `/api/v1` 接口里都没有版本字段。前端 `frontend/src` 里也没有找到版本显示、`__APP_VERSION__` 或 `VITE_*` 注入。
- 现在唯一的版本来源是**镜像标签和镜像 label**：`scripts/production.sh` 构建时加上 `--label org.opencontainers.image.revision=$sha`，tag 为 `<prefix>-app:<12 位 sha>` 再加 `:current`（:115-116、:145-149）。`show_version` 读取运行中容器的这个 label，打印“当前运行：<sha12>”（:94-100）。

---

## 6. 管理员相关后端 API

- **路由注册**：组合根 `internal/app/app.go:92-109` 使用标准库 `http.ServeMux`，路由带方法前缀（Go 1.22 写法）。每个能力包的 `httpapi.New(...).Register(mux)` 自己注册路由。外层包一层 `httpserver.Middleware(logger, mux)`（:121），负责请求 ID 和完成日志。最后是 `RegisterAPIFallback` 和前端兜底（:108-109）。
- **鉴权方式**：没有中间件链，而是在每个 handler 里显式调用：
  - `guard.Authenticate(r)`（`internal/identity/guard.go:85-97`，按会话 cookie 查 sessions 和 users，要求用户未停用）
  - `guard.RequireAdmin(w, r)`（:100-111，失败时已写出 401 或 403 `forbidden`，“需要管理员权限”）
  - 写请求先调用 `guard.RequireSameOrigin(w, r)`（:120 起）
  - 典型写法见 `internal/library/httpapi/scans.go:12-36`（startScan）：先 `RequireSameOrigin`，再 `RequireAdmin`，然后调用 service，按错误类型映射成 `httpserver.WriteError(w, r, 503, "<code>", "<中文消息>")`，成功时调用 `httpserver.WriteJSON`。
- **现有的管理类接口**：
  - 扫描（`internal/library/httpapi/handlers.go:38-44`）：`GET/POST /api/v1/scans`、`GET /scans/active`、`GET /scans/{id}`、`POST /scans/{id}/cancel`、`GET /scans/{id}/diagnostics`、`GET /scans/{id}/report`
  - 音乐根（:33-37）：`/api/v1/library-roots` 及其 PATCH/restore、`/library-root-operations`
  - 成员（`internal/identity/httpapi/handlers.go:71-75`）：`/api/v1/users` 的增删改、重置密码、撤销会话
  - 操作记录与撤销（`internal/operations/httpapi/handlers.go:48-49`）
  - 艺人治理（`internal/catalog/httpapi/artist_governance.go:34-39`）
  - `GET /api/v1/catalog/status`（`internal/catalog/httpapi/search.go:98-110`）只要求登录、不要求管理员，返回搜索和艺人投影状态，并设置 `Cache-Control: no-store`
  - **没有**“系统状态”“版本”“备份”之类的接口
- **文件下载类写法**：
  - 封面 `GET /api/v1/artworks/{id}`（`internal/catalog/httpapi/handlers.go:260-287`）：先鉴权，再 Lookup 查库，然后用 ETag 和 `If-None-Match` 判断 304，否则整份读入内存后 `w.Write`。
  - 音频 `GET /api/v1/tracks/{id}/audio`（`internal/playback/httpapi/handlers.go:145-177`）：先自己预判 Range（保证 416 也是 JSON 信封），设置 `Content-Type`、`Cache-Control: private, no-store`、强 ETag 和 `X-Content-Type-Options: nosniff`，再把文件包成 `playback.NewLifecycleReader(file, lifecycle)`（停机时中止传输），交给 `http.ServeContent`。
  - 仓库里**没有**设置 `Content-Disposition` 的地方。
  - 中间件的 `recordingResponseWriter` 实现了 `ReadFrom`、`Flush` 和 `Unwrap`（`internal/platform/httpserver/middleware.go:138-180`），不影响流式输出。

---

## 7. `.github/workflows/ci.yml`

- 触发条件：pull_request、推送到 main、workflow_dispatch。权限为 `contents: read`（:3-10）。所有 job 运行在 `ubuntu-24.04` 上。
- **backend** job（:18-44）：Go `1.25.10`（setup-go@v7），依次 gofmt 检查、`go test ./...`、`go test -race ./...`、`go vet`、`go build ./...`。
- **frontend** job（:46-93）：Node `24.22.1`（setup-node@v7），依次 `npm ci`、lint、typecheck、test、build；**检查内嵌产物 `backend/cmd/roomusic/web` 是否漂移**（:74-77）；Playwright e2e。
- **integration** job（:95-127）：shell 语法检查；对三份 compose 运行 `docker compose config --quiet`（生产那份配 `.env.prod.example`）；运行三条 grep，分别检查 Dockerfile 里的 ffmpeg 镜像、golang 版本、postgres tools 镜像与 compose、`.mise.toml` 是否一致（:119-121）；`./scripts/test-integration.sh`（PostgreSQL 18 加固定版本 ffmpeg）；`git diff --check`。
- **playback** job（:129-164）：`./scripts/playback-e2e.sh`，在 Chromium 中真实播放。
- **没有 docker build、buildx、GHCR 登录或推送，也没有 tag 或 release 触发**。`permissions` 里没有 `packages: write`。
- 版本来源：`.mise.toml` 写 `go = "1.25.10"`、`node = "24.22.1"`；`backend/go.mod:3` 是 `go 1.25.0`。

---

## 8. 生产镜像中的 `postgresql17-client`，以及 pg_dump 的版本约束

- `deploy/production/Dockerfile:17-18`：app 阶段基于 `alpine:3.22`，执行 `apk add ca-certificates postgresql17-client`。
- **它的唯一用途是 `entrypoint.sh` 里的 `pg_isready`**（`deploy/production/entrypoint.sh:5`，`pg_isready -q -d "$ROOMUSIC_DATABASE_URL" -t 3`，每 2 秒重试一次，每 30 秒打印一条 `platform.database.waiting`），原因是“应用只在启动时连接一次数据库”（entrypoint.sh:2）。Go 代码里没有调用任何 PostgreSQL 客户端二进制。
- 数据库镜像是 `postgres:18.1-alpine3.23@sha256:aa6e…`（compose.yaml:3）。tools 阶段直接使用同一个镜像（Dockerfile:14-15），所以 backup.sh 和 restore.sh 用的是 pg_dump 18。CI 用 grep 保证两者一致（ci.yml:121）。
- pg_dump 的版本约束（PostgreSQL 官方文档的已知规则）：pg_dump **只能导出主版本不高于它自己的服务器**，遇到更新的服务器会报 server version mismatch 并拒绝；pg_restore 读不了比它新的 pg_dump 生成的归档格式。所以要在应用镜像里对 PG 18 执行 `pg_dump`，客户端必须是 18 或更高，现在的 `postgresql17-client` 不满足。
- Alpine 3.22 的仓库里大概率没有 `postgresql18-client`（3.22 发布于 2025 年 5 月，早于 PG 18；PG 18 包预计从 3.23 开始提供）。这一点没有联网核实。tools 阶段的 postgres:18.1-alpine3.23 镜像本身带有 18 版的 pg_dump、pg_restore、pg_isready 等。

---

## 9. `deploy/production/restore.sh` 步骤摘要

### 9.1 通用前置（:37-63）

- 参数只接受 `check|production <备份文件>`；备份文件必须存在且可读，然后 `realpath`。
- 项目名只允许 `roomusic-prod` 或 `roomusic-drill`（:49），env 文件默认 `<repo>/.env.prod`。
- 清掉继承下来的 `ROOMUSIC_*` 和 `COMPOSE_*` 变量（:53-55），再显式导出 `COMPOSE_PROJECT_NAME` 和 `COMPOSE_ENV_FILES`。
- 在仓库根目录运行 `docker compose -f deploy/production/compose.yaml`。
- 创建临时目录 `work`，设置 trap 在退出时清理。

### 9.2 `check`（:65-80）

1. 把备份复制到 `work`，临时改为 chmod 644。
2. `compose --profile tools run --rm --no-deps restore-check`：无网络（`network_mode: none`），以 postgres 用户运行，4GiB tmpfs（compose.yaml:91-98），备份只读挂载为 `/backup.dump`。
3. 容器内执行 `_inside`（:20-34）：`initdb`（本地 trust、拒绝 host）→ 只监听 unix socket 的 `pg_ctl start` → `createdb roomusic` → `pg_restore --no-owner --exit-on-error` → 运行 `counts_sql` → 停库并删除数据目录。
4. `counts_sql`（:6-18）输出 `schema_migrations.max`、`schema_migrations.count`，以及 users、sessions、library_roots、releases、tracks、release_artworks 的行数（表不存在时写“缺失”）。
5. 如果生产 postgres 在运行，就对线上库跑同一份 counts，用 awk 并排打印“项目 / 备份 / 当前数据库”；否则只打印备份里的数。

### 9.3 `production`（:82-140）

1. postgres 必须在运行。打印备份文件路径、修改时间和大小，**要求输入项目名确认**（:85-87）。
2. 用一次性的 backup 容器执行 `backup now`，做**恢复前的安全备份**（:90），失败就中止。从输出的 JSON 日志里解析出 `roomusic-…-manual.dump` 文件名，再从 env 文件读 `ROOMUSIC_BACKUP_DIR` 拼出宿主机路径，确认文件存在（:96-102）。
3. `compose stop app backup`（:115）。
4. 在 postgres 容器里用 psql 执行 `DROP DATABASE roomusic WITH (FORCE)` 和 `CREATE DATABASE roomusic OWNER roomusic`（:116-119）。
5. 把宿主机上的备份经 stdin 传给 `compose exec -T postgres pg_restore -U roomusic -d roomusic --no-owner --exit-on-error`（:120-122）。第 4 或第 5 步失败时调用 `restore_failed`：应用和计划备份保持停止，并打印一条可以直接执行的“用安全备份再恢复一次”的完整命令（:106-114）。
6. `compose start backup app`，最多等待 300 秒，直到 app 的 Docker health 变成 healthy（:124-139）。失败时提示“若备份包含较新的迁移，先 make prod REF=<对应提交>”。
7. 最后打印恢复后的 counts（:140）。

### 9.4 相关：`backup.sh` 的要点（供搬进 Go 时对照）

- `pg_dump -Fc` 写到 `.roomusic-<时间>-<mode>-<pid>.partial.XXXXXX`，stderr 丢弃（backup.sh:29-31）→ 用 `pg_restore --list` 校验（:33）→ **用硬链接原子发布为 `roomusic-YYYYmmdd-HHMMSS[-manual].dump`，不覆盖已有文件，同一秒内并发时顺延**（:38-43）→ 更新 `.last-success` 的时间戳（:46）→ 输出 JSON 日志 `backup.completed`（file、bytes）→ 用 `find -mmin +keep_days*1440` 清理过期备份（:51）。umask 077。
- 循环模式（:57-86）：每 600 秒检查一次。首次启动时如果 24 小时内没有成功备份就立即补一次；当地时间到达 `BACKUP_HOUR` 且当天还没有成功备份时执行；失败后 1 小时重试。
- `check` 模式：`.last-success` 在 93600 秒（26 小时）内才算健康（:56），供 compose 的 healthcheck 使用（compose.yaml:79-84）。
- `scripts/production.sh up` 在换版本前执行 `compose run --rm --no-deps -T backup now`（:175），失败就不换版本。`make prod-backup` 执行 `compose exec backup … now`（:181）。

---

## 注意事项 / 未找到

- **future version 的报错文案**：数据库里有未知版本时，实际报的是 `unknown version N`（migrate.go:443），不是 `future version`。`future version` 分支（:354）在当前逻辑下不可达。
- **校验和覆盖整个文件**（migrate.go:121）：往已发布迁移文件里追加回退 SQL 会被判为 checksum drift。
- **迁移失败不会让进程退出**，而是进入降级模式（main.go:44-49）。
- 没有找到任何定时调度组件，也没有版本号注入、系统状态接口或 `Content-Disposition` 下载接口。
- 第 8 项中 Alpine 3.22 和 3.23 是否提供 `postgresql18-client` 没有联网核实。
