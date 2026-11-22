# Research: 自动层与现有代码的耦合点

- **Query**: 给发行加第三层“自动层”之前，现有代码里哪些地方直接依赖 `release_corrections`、派生刷新、署名事实、搜索、认领、接口样本、实例设置、管理员判断、操作记录读取与工程约定
- **Scope**: internal（只读代码、迁移、规范与任务文档；未运行测试，未读 `temp/` 与任何实例数据）
- **Date**: 2026-11-22

路径均相对仓库根目录。已有调研 `.trellis/tasks/11-22-auto-curation-sources/research/overlays-and-operations.md` 覆盖的内容（分层总览、`operations.Run` 流程、操作类型与可撤销性、问题的三类定义）不再重复，这里只补十个问题。

## 1. 谁读写 `release_corrections`

**结论**：非测试代码里直接读写这张表的位置共 13 处，分布在视图、字段问题判定、`corrected_fields`、修正状态、专辑级署名、艺人署名事实与有效署名、认领、维护任务的成本估算；另有若干位置不直接读表，但把“没有修正行”等同于“显示扫描值”。

### 1.1 迁移（SQL）

| 位置 | 用途 |
|---|---|
| `backend/migrations/0017_governed_operations.sql:44-61` | 建表：主键 `(release_id, field)`，`release_id` 外键 `ON DELETE CASCADE`（`:45`），`operation_id` 外键 `DEFERRABLE INITIALLY DEFERRED`（`:50`），字段、模式、必填、取值四个 CHECK（`:52-60`） |
| `backend/migrations/0032_release_genres.sql:21-31` | 加 `list_value TEXT[]`，重建 `release_corrections_value_ck`（流派用列表） |
| `backend/migrations/0032_release_genres.sql:33-64` | 当前生效的视图 `release_effective`：八次按主键的 `LEFT JOIN`；`title`、`artist`、`album_artist` 用 `COALESCE(修正, 扫描)`（`:37-38`、`:44`），其余用 `CASE WHEN c.release_id IS NULL`（`:45`、`:48-54`）；`artist` 跟随 `album_artist` 的修正（`:38`） |
| `backend/migrations/0032_release_genres.down.sql:4-47` | 回退：恢复旧约束与旧视图 |

### 1.2 Go 代码（直接读写）

| 位置 | 类别 | 用途 |
|---|---|---|
| `backend/internal/catalog/fields.go:52-83` `LoadCorrectionIntents` | 修正意图 | 读一个发行的全部修正行，返回 `map[字段]CorrectionIntent`；调用方：`fields.go:87`、`governance/corrections.go:287`、`:382`、`governance/revert.go:94`、`governance/claim_read.go:262` |
| `backend/internal/catalog/fields.go:86-98` `loadCorrectedFieldNames` | `corrected_fields` | 由上面的意图得出有修正的字段名；发行详情头部用（`backend/internal/catalog/read.go:146`、`:158`） |
| `backend/internal/catalog/comparison.go:179-213` `loadComparedCorrections` | `corrected_fields` | 版本对比接口一条查询取多个发行的修正字段名（`:180`） |
| `backend/internal/catalog/read.go:333-345` `LoadReleaseCredits` | 专辑级署名 | 有 `album_artist` 修正时，去掉扫描写入 `release_credits` 的 `album_artist` 行（`:338-341`），再并上修正值（`:343-345`） |
| `backend/internal/catalog/internal/releasesql/issues.go:21-28` `FieldIssueRows` | 待关注字段判定 | `uncertain_apply` 的字段决定里排除有修正行的字段；`artist` 映射到 `album_artist` 的修正（`:27`）。使用它的地方：`issues.go:36-37`、`internal/releasesql/summary_batch.go:21`、`internal/releasesql/release.go:18`、`governance/issue_read.go:81`、`:84`、`:364`、`governance/issue_review.go:98` |
| `backend/internal/catalog/governance/corrections.go:475-498` `writeIntent` | 写入 | `DELETE`（`:477`）或 `INSERT … ON CONFLICT DO UPDATE`（`:492-496`），`operation_id` 写本次操作。调用方：修正执行器 `corrections.go:412`、修正撤销 `revert.go:121`、认领 `claim.go:560`、认领撤销 `claim_revert.go:48` |
| `backend/internal/catalog/governance/claims.go:31-37` `releaseClaimablePredicate` | 认领判定 | 旧发行“留有人工状态”的四个条件之一是存在修正行（`:33`） |
| `backend/internal/catalog/governance/claim_read.go:80-86` `claimEntryProjection` | 认领列表 | `string_agg` 出旧发行有修正的字段名（`:83`），对应 `ClaimHolds.Corrections`（`claim_read.go:55-60`） |
| `backend/internal/catalog/governance/claim.go:219-256` `loadClaimSide` | 认领快照 | 读修正行连同 `operation_id`（`:225`），存入 `claimSide.Corrections`，进操作记录的前后态 |
| `backend/internal/catalog/governance/claim.go:592` | 认领写入 | 清空旧发行的全部修正行 |
| `backend/internal/catalog/governance/claim_revert.go:31` `restoreClaimSide` | 认领撤销 | 先清空一侧的修正行，再按前态连同原 `operation_id` 写回（`:38-50`） |
| `backend/internal/catalog/internal/artistgraph/facts.go:58-60` | 署名事实 | `LEFT JOIN` 取 `album_artist` 修正值，生成 `layer='correction'` 的事实（见第 3 节） |
| `backend/internal/catalog/internal/artistgraph/refresh.go:78-81` | 有效署名 | 发行级署名按“是否存在 `album_artist` 修正行”选 `correction` 或 `scan` 层（见第 3 节） |
| `backend/internal/catalog/maintenance/artist_batch.go:268-269` `artistReleaseNameInputsSQL` | 维护任务 | 艺人投影回填估算一个发行的名字输入成本时，把 `album_artist` 修正值计两次（对应两个角色） |

### 1.3 经 `release_effective` 间接依赖

- 全部读取投影的发行来源是常量 `ReleaseSource = "release_effective releases"`：`backend/internal/catalog/internal/releasesql/release.go:7`。
- 直接写视图名的位置：`backend/internal/catalog/read.go:139`（详情头部）、`backend/internal/catalog/artists_collections.go:106`（艺人曲目排序的连接）、`backend/internal/catalog/internal/searchindex/write.go:122-123`（搜索派生值的输入）、`backend/internal/catalog/internal/releasesql/artist_refs.go:76-77`（专辑艺人署名的艺人引用）。
- 视图在迁移里被 `DROP` 再 `CREATE` 过一次（`0032_release_genres.sql:2`、`:33`）。

### 1.4 把“没有修正”等同于“显示扫描值”的位置

| 位置 | 做法 |
|---|---|
| `backend/internal/catalog/governance/corrections.go:230-234`、`:279-286` | 修正状态里 `scan_value` 读 `releases`，`effective_value` 读 `release_effective` |
| `backend/internal/catalog/governance/revert_preview.go:28` | 撤销预览：前态没有修正时，预览的“之后”取 `field.ScanValue` |
| `backend/internal/catalog/governance/stewardtools/preview.go:43-45` | 管家 `correct_release` 的预览：`clear` 的“之后”取 `field.ScanValue` |
| `backend/internal/catalog/governance/claim_read.go:266-273` | 认领详情：每个修正旁边给 `scan_value`（读 `releases`） |
| `.trellis/spec/backend/governance-contracts.md:130-131` | 合同写明清除修正后显示当前扫描值 |

### 1.5 管家工具与审查证据

- 审查证据 `backend/internal/catalog/stewardtools/review.go:85-96`：对目标发行调用 `LoadCorrectionState`，把八个字段的 `EffectiveValue` 以来源标签 `catalog_effective_untrusted` 交给审查；认领来源一侧用 `claim.Corrections`，标签 `catalog_correction_untrusted`（`review.go:68-76`）。
- 管家的发行详情工具返回 `CorrectedFields`：`backend/internal/catalog/stewardtools/details.go:61`。
- 写工具预览：`backend/internal/catalog/governance/stewardtools/preview.go:24-55`。

### 1.6 引用这张表的测试文件（23 个）

`backend/internal/platform/postgres/migrations_genres_integration_test.go`、`backend/internal/operations/run_integration_test.go`、`backend/internal/catalog/` 下的 `effective_integration_test.go`、`artist_refs_integration_test.go`、`personal_reads_explain_test.go`、`recent_additions_explain_integration_test.go`、`track_collection_stats_explain_integration_test.go`、`governance/` 下的 `corrections_integration_test.go`、`claim_integration_test.go`、`claim_revert_integration_test.go`、`fixture_integration_test.go`、`internal/artistgraph/` 下的 `compilation_integration_test.go`、`masked_scan_integration_test.go`、`refresh_review_integration_test.go`、`maintenance/artist_workload_test.go`，以及 `backend/internal/app/` 下的 `api_samples_integration_test.go`、`corrections_api_integration_test.go`、`claim_api_integration_test.go`、`claims_rescan_integration_test.go`、`scan_feedback_governance_integration_test.go`、`scan_feedback_artwork_integration_test.go`、`catalog_read_performance_integration_test.go`、`personal_realistic_performance_integration_test.go`。

## 2. 字段修正执行器与撤销在同一事务里刷新了什么

**结论**：两者都只调用一个函数 `refreshGovernedRelease`，它无条件做两件事：重建这个发行的艺人投影、刷新这个发行的搜索派生值。没有按“是否改了 album_artist”分支，也不刷新收藏解析。

- 调用点：修正执行器 `backend/internal/catalog/governance/corrections.go:422`；修正撤销 `backend/internal/catalog/governance/revert.go:128`。两处都在 `UPDATE releases SET edit_revision = edit_revision + 1` 之后（`corrections.go:419`、`revert.go:125`）。
- 函数本身（未导出）：`backend/internal/catalog/governance/artist_state.go:226-231`

  ```go
  func refreshGovernedRelease(ctx context.Context, tx *sql.Tx, id string) error
  ```

  依次调用 `artistgraph.Refresh(ctx, tx, []string{id})` 与 `searchindex.RefreshRelease(ctx, tx, id)`。
- 同一函数的其他调用点：归组 `group_change.go:163`、归组撤销 `revert.go:251`、认领 `claim.go:635`、认领撤销 `claim_revert.go:169`。

| 刷新 | 签名 | 包 | 做什么 | 触发条件 |
|---|---|---|---|---|
| 艺人投影 | `func Refresh(ctx context.Context, tx *sql.Tx, releaseIDs []string) error`（`backend/internal/catalog/internal/artistgraph/refresh.go:11`） | `catalog/internal/artistgraph` | 依次 `refreshFacts`（署名事实，`:27`）、`refreshEffective`（有效署名，`:31`）、`refreshProfiles`（受影响艺人的主名与搜索名，`:39`）、`refreshCandidates`（重复候选，`:42`），最后写 `artist_projection_releases` 的代次标记（`:45-46`） | 每次修正与撤销都执行，不看改了哪个字段 |
| 搜索派生值 | `func RefreshRelease(ctx context.Context, tx *sql.Tx, id string) error`（`backend/internal/catalog/internal/searchindex/write.go:13`） | `catalog/internal/searchindex` | `input_revision` 加一（`write.go:30-32`），调用 `Apply` 重读 `release_effective` 并重写 `catalog_search_values` 与 `search_title`（`write.go:83-100`、`:122-134`） | 同上 |
| 曲目搜索值 | `func RefreshTracks(ctx context.Context, tx *sql.Tx, ids []string) error`（`backend/internal/catalog/internal/searchindex/batch.go:17`） | 同上 | `refreshFacts` 内部调用（`facts.go:181`），只针对显示署名被改写的曲目 | 只有曲目的展示艺人串与规范化结果不同时才有 ID（`facts.go:154-157`、`:166-180`） |
| 收藏解析 | `RefreshArtistResolution(context.Context, *sql.Tx, map[string]string) error`（接口在 `backend/internal/catalog/governance/artist_state.go:18`，实现在 `backend/internal/personal/artist_resolution.go:46`） | `personal` | 艺人收藏的解析列 | **修正与撤销不调用**；只有艺人治理的 `(*ArtistGovernance).refresh` 在有重定向映射时调用（`artist_state.go:232-249`） |

前置条件与可见性：

- `artistgraph.Refresh` 与 `searchindex.RefreshRelease` 都要求调用方已持有目录写锁 CATP（`refresh.go:10`、`write.go:12`）。修正与撤销通过 `lockVisibleRelease` 先取这把锁再锁发行行：`backend/internal/catalog/governance/corrections.go:350-362`；锁本身是 `pg_advisory_xact_lock(0x43415450, hashtext(current_schema()))`，且“必须在发行、曲目等业务行锁之前取得”（`backend/internal/catalog/internal/writegate/gate.go:14-19`）。
- `searchindex.RefreshRelease` 在搜索投影版本与进程不一致时返回 `ErrVersionMismatch`（`write.go:27-29`、`backend/internal/catalog/internal/searchindex/state.go:15`、`:51-53`）。
- `refreshGovernedRelease`、`writeIntent`、`lockVisibleRelease`、`loadFieldValues` 都是 `catalog/governance` 包内未导出函数；`artistgraph`、`searchindex`、`writegate`、`releasesql` 位于 `backend/internal/catalog/internal/` 之下，按 Go 的 internal 规则只有 `backend/internal/catalog/` 子树里的包可以导入。依赖规则表里导入它们的包也只有 `catalog`、`catalog/governance`、`catalog/ingest`、`catalog/maintenance`、`catalog/testsupport`（`backend/internal/app/dependency_rules_test.go:63-71`）。
- 执行器在刷新之后重读修正状态，把被改字段的有效值写入操作记录的后态（`corrections.go:425-433`）；有效值来自 `release_effective`（`corrections.go:283`、`:386`）。

## 3. 专辑艺人修正如何生成署名事实

**结论**：`refreshFacts` 每次都从当前的扫描列和 `album_artist` 修正值重建事实，修正值同时生成 `release_artist` 与 `album_artist` 两个角色的 `layer='correction'` 事实；有效署名按“这个发行有没有 `album_artist` 修正行”整层二选一，不逐条合并。

### 3.1 事实的生成

- 输入查询 `backend/internal/catalog/internal/artistgraph/facts.go:58-60`：读 `releases.artist`、`releases.album_artist` 与 `LEFT JOIN release_corrections … field='album_artist'` 的 `text_value`。
- 扫描层：`release_artist`（非推断且不是合辑占位名时，`facts.go:83-87`）、`album_artist`（不是合辑占位名时，`facts.go:89-93`）。
- 修正层 `facts.go:94-100`：修正值非空且不是合辑占位名时，对 `release_artist`、`album_artist` 两个角色各调用一次 `add(…, "correction")`；修正值按 `metadata.CanonicalArtistNames` 拆成多个名字（`facts.go:34`）。
- 名字入库时的来源：修正层的名字以 `origin='correction'` 新建（`facts.go:193-197`，插入在 `backend/internal/catalog/internal/artistgraph/names.go:120`）；已存在的名字不改 `origin`（`names.go:86-89` 先查缓存）。
- 落库 `facts.go:207-219`：先删除本批发行范围内不在新事实集合里的旧行（比较键含 `layer`），再按 `(release_id,track_id,role,name_id,layer)` upsert。
- 函数注释写明“重建当前扫描事实与专辑艺人修正层……不触碰任何艺人治理表”（`facts.go:24`）。

### 3.2 约束所在的迁移

- `artist_credits.layer`：`CHECK(layer IN ('scan','correction'))`，唯一约束含 `layer`：`backend/migrations/0026_artist_catalog.sql:25`、`:29`。之后的迁移没有再改这个约束（全部迁移里只有这两行出现 `layer`）。
- `artist_names.origin`：`CHECK (origin IN ('scan','correction','seed'))`：`backend/migrations/0026_artist_catalog.sql:20`。艺人别名接口把 `origin` 返回给前端：`backend/internal/catalog/artists_reads.go:42`、`:54`，`backend/internal/catalog/artists_types.go:43`。

### 3.3 有效署名在两层之间的取舍

- `refreshEffective` `backend/internal/catalog/internal/artistgraph/refresh.go:67-94`：先删本批发行的发行级与曲目级有效署名（`:69-74`），再插入。
- 发行级事实的层选择 `refresh.go:80-81`：

  ```sql
  c.layer = CASE WHEN EXISTS(SELECT 1 FROM release_corrections rc
    WHERE rc.release_id=c.release_id AND rc.field='album_artist') THEN 'correction' ELSE 'scan' END
  ```

  判定依据是修正行是否存在，不是修正层事实是否存在；修正值是合辑占位名时没有修正层事实（`facts.go:94`），这时发行级有效署名为空。
- 曲目级事实不分层，全部进入有效署名（`refresh.go:83-84`）。
- 艺人归属按 曲目覆盖 → 发行覆盖 → 全局绑定 → 初始归属，再解析重定向（`refresh.go:86-92`）。

### 3.4 其他按 `album_artist` 修正取值的位置

| 位置 | 内容 |
|---|---|
| `backend/migrations/0032_release_genres.sql:38`、`:44` | 视图的 `artist` 与 `album_artist` |
| `backend/internal/catalog/read.go:333-345` | 专辑级署名里 `album_artist` 行的替换 |
| `backend/internal/catalog/internal/releasesql/issues.go:27` | `artist` 字段问题由 `album_artist` 修正消除 |
| `backend/internal/catalog/governance/corrections.go:298-299` | 修正状态里 `artist` 的不确定算到 `album_artist` 上 |
| `backend/internal/catalog/internal/releasesql/artist_refs.go:76-77` | 专辑艺人署名匹配艺人引用时与 `release_effective.album_artist` 比较 |
| `backend/internal/catalog/internal/searchindex/write.go:122-132` | 搜索值 `release_artist`、`album_artist` 读 `release_effective` |
| `backend/internal/catalog/maintenance/artist_batch.go:268-269` | 回填成本估算 |
| `backend/internal/catalog/ingest/persist.go:141-145` | 扫描重写 `release_credits` 的 `album_artist` 行（扫描值） |

相关测试：`backend/internal/catalog/internal/artistgraph/masked_scan_integration_test.go`、`compilation_integration_test.go`、`refresh_review_integration_test.go`，`backend/internal/catalog/artist_refs_integration_test.go`。

## 4. 搜索与排序

**结论**：搜索有两种模式。规范化模式读物化表 `catalog_search_values` 与 `catalog_projection_subject_state.search_title`，这两处在修正的同一事务里同步刷新；旧模式（投影未就绪时）与专辑、发行列表的排序直接读 `release_effective`，读取时现算。

- 模式选择 `backend/internal/catalog/search.go:39-66`：搜索投影 `Ready()` 且字表可加载时是 `normalized`，否则 `legacy`。
- 规范化模式：命中查询读 `catalog_search_values`（`backend/internal/catalog/search_sql.go:36-38`）；结果排序用 `catalog_projection_subject_state.search_title`（`backend/internal/catalog/search_ranked_sql.go:22`、`backend/internal/catalog/search_tiers_sql.go:70`、`:81`、`backend/internal/catalog/search_probe_sql.go:50-65`）。表定义在 `backend/migrations/0025_catalog_search.sql:27-35`、`:64-76`。
- 发行的搜索值只有五个字段，全部取自 `release_effective`：标题、展示艺人、专辑艺人、厂牌、编号（`backend/internal/catalog/internal/searchindex/write.go:47`、`:120-135`）。年份、流派、版本说明、条码不进搜索值。
- 旧模式：`ILIKE` 直接对 `release_effective` 的 `title`、`artist`、`album_artist`（`backend/internal/catalog/search_sql.go:45-50`）；发行列表的搜索条件同样（`backend/internal/catalog/read.go:29-32`）。
- 列表排序：发行列表 `ORDER BY LOWER(releases.title),LOWER(releases.artist),releases.id`（`backend/internal/catalog/read.go:103`，来源是 `ReleaseSource`）；专辑内代表版本顺序用有效年份（`backend/internal/catalog/internal/releasesql/release.go:55-56`）；艺人曲目排序连接 `release_effective`（`backend/internal/catalog/artists_collections.go:106`）。
- 修正后的更新方式：
  - 同步：`refreshGovernedRelease` → `searchindex.RefreshRelease`（第 2 节）。扫描落库同样同步调用（`backend/internal/catalog/ingest/persist.go:166`）。
  - 后台回填：维护任务按工作清单逐个调用 `searchindex.Apply`（`backend/internal/catalog/maintenance/batch.go:56`），用于整代重建；`Apply` 对“同代且 `input_revision` 已应用”的对象跳过（`write.go:68-70`）。同步刷新在清单已封存时累加 `live_updated`（`write.go:39-42`）。
- 艺人名搜索走另一张表 `artist_search_names`，由 `refreshProfiles` 写（`backend/internal/catalog/internal/artistgraph/profiles.go:190-192`）。

## 5. 认领

**结论**：认领只搬 `release_corrections` 与 `release_issue_reviews` 两张表的行，并只把这两类写进操作记录的前后态；它不知道任何别的按发行存放的覆盖层。发行行从不物理删除，所以 `ON DELETE CASCADE` 在现有代码路径里不会被触发。

### 5.1 迁移修正的做法

- 计划 `backend/internal/catalog/governance/claim.go:137-149`：旧发行有修正的字段，新发行没有同字段修正则 `move`，已有则 `skip`（原因 `target_has_correction`）。
- 写入 `claim.go:553-597`：`move` 的字段用 `writeIntent` 写到新发行，`operation_id` 写本次认领（`:560`）；随后 `DELETE FROM release_corrections WHERE release_id=旧发行`（`:592`）与核对行的删除（`:595`）。
- 两个发行的 `edit_revision` 各加一（`claim.go:628-632`），再各刷新一次派生（`claim.go:634-638`）。
- 操作记录前后态 `claimRecordState{Source, Target, Groups}`（`claim.go:459-463`），其中 `claimSide` 只含 `Corrections` 与 `Reviews`（`claim.go:91-98`）；`Title` 取自 `release_effective`（`claim.go:221`）。
- 撤销 `backend/internal/catalog/governance/claim_revert.go:30-60`：两侧各自清空修正与核对行后按前态写回；要求新旧发行与涉及的组都停在认领之后的版本上（`claim_revert.go:133`、`:149`）。
- 合同对应条目：`.trellis/spec/backend/claim-contracts.md:28`（待认领判定）、`:66`（修正项去向）、`:90-91`（写入步骤）、`:103-105`（撤销）、`:115`（被移走修正的原记录不能再单独撤销）、`:132`（扫描不读写这四类状态）。

### 5.2 待认领的判定与令牌

- 判定只看四类人工状态：修正行、组里有别的发行、是某个组的默认版本、核对行（`backend/internal/catalog/governance/claims.go:31-37`）。
- 预览令牌只含两个发行 ID 与各项去向（`claim.go:207-216`）；注释写明依赖“两个发行的修正与核对只能被受治理写入改变，每次改变都让版本号加一”（`claim.go:203`，合同 `.trellis/spec/backend/claim-contracts.md:72`）。

### 5.3 外键与物理删除

- `release_corrections.release_id`：`REFERENCES releases(id) ON DELETE CASCADE`（`backend/migrations/0017_governed_operations.sql:45`）。`operation_id`：`REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED`，没有 `ON DELETE` 子句（`0017:50`）。
- 其他引用 `releases(id)` 的外键多数是 `CASCADE`；例外：`artist_assignment_overrides.release_id` 是 `ON DELETE RESTRICT`（`backend/migrations/0026_artist_catalog.sql:41`），`release_groups.default_release_id` 是 `ON DELETE SET NULL`（`backend/migrations/0018_release_group_default.sql:9`）。
- 发行的物理删除：`backend/` 非测试 Go 代码与迁移里没有 `DELETE FROM releases`；出现的三处都在测试里（`backend/internal/operations/group_target_integration_test.go:97`、`backend/internal/catalog/maintenance/artist_integration_test.go:293`、`backend/internal/catalog/maintenance/artist_profiles_integration_test.go:31`）。规范写明“发行从不删除”，级联“只是保险”（`.trellis/spec/backend/issue-review-contracts.md:21`、`.trellis/spec/backend/default-version-contracts.md:19`）。组也从不删除（`.trellis/spec/backend/governance-contracts.md:35`）。
- 非测试代码里也没有 `DELETE FROM operations`、`DELETE FROM users`。

### 5.4 对一张新的按发行存放的表意味着什么（仅事实）

- 现有认领代码按表名操作，不会读、搬或删别的表的行；旧发行在认领后仍然存在（只是不可见），其上其他表的行保持原样。
- 认领与认领撤销对两个发行都调用 `refreshGovernedRelease`，会按当时的 `release_effective` 重算搜索值与署名（`claim.go:634-638`、`claim_revert.go:168-172`）。

## 6. 接口与样本

**结论**：与修正有关的返回字段集中在五个接口；样本统一放在 `backend/internal/app/testdata/api-samples/`，由后端集成测试生成并逐字比对，前端的 decoder 测试读同一批文件。

### 6.1 接口与字段

| 接口 | 权限 | 与修正有关的字段 | 依据 |
|---|---|---|---|
| `GET /api/v1/releases/{id}` | 登录成员 | `revision`、`corrected_fields`（有修正的字段名，固定顺序）；`title`、`artist`、`album_artist`、`year`、`genres`、`label`、`catalog_number`、`edition`、`barcode` 都是有效值 | `backend/internal/catalog/httpapi/handlers.go:162-213`，`backend/internal/catalog/projection.go:85-100`，`backend/internal/catalog/read.go:130-160`，`.trellis/spec/backend/catalog-rest-contracts.md:38` |
| `GET /api/v1/release-groups/{id}` | 登录成员 | 没有修正专属字段；成员是 `ReleaseSummary`（有效值） | `backend/internal/catalog/projection.go:136-141` |
| `GET /api/v1/release-groups/{id}/comparison` | — | 每个版本带 `corrected_fields` | `backend/internal/catalog/comparison.go:51`、`:179-213`，`.trellis/spec/backend/default-version-contracts.md:79-81` |
| `GET /api/v1/releases/{id}/evidence` | 管理员 | 只有扫描的字段决定（`field`、`source`、`confidence`、`action`、`rule_id`、`value`、`candidates`、`reason_code`）与归组证据；**不含修正值** | `backend/internal/catalog/httpapi/handlers.go:223-249`，`backend/internal/catalog/projection.go:102-121`，`backend/internal/catalog/read.go:395-417` |
| `GET /api/v1/releases/{id}/corrections` | 管理员 | `release_id`、`revision`、`fields[]`：`field`、`scan_value`、`correction`（`{mode, value}` 或 null）、`effective_value`、`uncertain` | `backend/internal/catalog/httpapi/corrections.go:35-55`，`backend/internal/catalog/governance/corrections.go:205-220` |
| `PATCH /api/v1/releases/{id}/corrections` | 管理员 | 同上，加 `operation_id` | `backend/internal/catalog/governance/corrections.go:222-226` |
| `GET /api/v1/release-claims/{id}` | 管理员 | `corrections[]`：`field`、`correction`、`scan_value`；列表项 `holds.corrections` | `backend/internal/catalog/governance/claim_read.go:55-60`、`:189-194` |
| `GET /api/v1/operations` | 管理员 | 修正记录的 `changes = {kind:"release_correction", fields:[{field, before:{correction, effective}, after:{correction, effective}}]}` | `backend/internal/catalog/governance/revert.go:147-157`、`:178-203` |

发行详情里专辑级署名的 `album_artist` 行在有修正时是修正值（第 1.2 节 `read.go:333-345`）。

### 6.2 样本

- 目录：`backend/internal/app/testdata/api-samples/`（常量 `apiSamplesDirectory`，`backend/internal/app/api_samples_integration_test.go:35`）。`backend/testdata/` 只有播放用的音频素材。
- 比对测试：`TestPostgreSQLAPIResponseSamples`（`api_samples_integration_test.go:44`）；比对与重写逻辑在 `(*apiSampleRecorder).finish`（`:1167-1247`），样本归属按前缀划分（`ownsSample`，`:1250` 起）。其他记录器：`artist_samples_integration_test.go:89`、`search_samples_integration_test.go:141`、`personal_samples_integration_test.go:19`、`scan_feedback_samples_integration_test.go:16`、`system_samples_integration_test.go:19`、`dsp_api_samples_integration_test.go:29`、`zones_api_samples_integration_test.go:127`。
- 重写方式：`ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh`，之后同步前端 decoder（`api_samples_integration_test.go:31-33`）。
- 与本任务相关的样本及其记录行：

  | 样本 | 记录行 |
  |---|---|
  | `releases/get`、`releases/evidence`、`releases/get-single`、`releases/get-cue`、`releases/get-work` | `api_samples_integration_test.go:609`、`:694-697`、`:713` |
  | `corrections/get`、`corrections/patched`、`corrections/get-corrected`、`releases/get-corrected` | `:766-774` |
  | `operations/reverted`、`operations/list`、`operations/list-by-release` | `:827-834` |
  | `release-groups/get`、`get-multiple`、`get-default`、`comparison`，`operations/list-by-group`、`reverted-default` | `:693`、`:722`、`:852-861` |
  | `operations/reverted-issue-review`、`list-issue-reviews` | `:906-907` |
  | `release-claims/*`、`releases/get-claimed`、`operations/list-claims`、`reverted-claim`、`list-claims-reverted` | `:944-964` |
  | `operations/artists`、`artist-source-filter`、`artist-union-filter`、`artist-damaged-state` | 由 `artist_samples_integration_test.go` 的记录器写（文件在 `testdata/api-samples/operations/`） |
  | `search/artist-correction-legacy`、`search/artist-correction-normalized` | 由 `search_samples_integration_test.go` 的记录器写 |

- 前端读取位置：`frontend/src/test/api-samples.ts:6`（唯一记录样本目录的地方），契约测试 `frontend/src/test/api-contract.test.ts`。

## 7. 实例级设置的现有存放方式

**结论**：四类设置用了四种不同做法。数据库里的实例级设置有两张“单行表”（`id` 固定为 1）和一张带添加者的名单表；备份保留规则不在数据库里；DSP 没有实例级设置。

| 设置 | 存放 | 形状 | 读写的包 | 接口 | 记操作者 |
|---|---|---|---|---|---|
| 定时扫描 | 表 `scan_schedule`（`backend/migrations/0042_scheduled_scan.sql:1-7`） | 单行表：`id SMALLINT PRIMARY KEY CHECK (id = 1)`，列 `enabled`、`hour`、`updated_at`；迁移里插入初始行（`:7`） | `library`：`LoadScanSchedule`、`SaveScanSchedule`（`backend/internal/library/scan_schedule.go:61-69`、`:72` 起） | `GET`、`PUT /api/v1/scan-schedule`（`backend/internal/library/httpapi/handlers.go:42-43`，handler 在 `backend/internal/library/httpapi/scan_schedule.go:13-31`；写要求同源与管理员） | 否 |
| 管家成员开关 | 表 `steward_instance_settings`（`backend/migrations/0051_steward_runtime.sql:18-22`） | 单行表：`id integer PRIMARY KEY CHECK (id = 1)`，列 `members_enabled`；迁移里插入初始行 | `steward/runtime`：`GetMembersEnabled`、`SetMembersEnabled`（`backend/internal/steward/runtime/settings.go:193-203`） | `GET`、`PUT /api/v1/admin/steward/settings`（`backend/internal/steward/httpapi/handlers.go:74-75`，handler 在 `backend/internal/steward/httpapi/admin.go:35`、`:44`） | 否 |
| 管家出站放行名单 | 表 `steward_egress_allowlist`（`backend/migrations/0051_steward_runtime.sql:24-30`） | 多行表：主键 `(host, port)`，`added_by uuid REFERENCES users(id) ON DELETE SET NULL`，`created_at` | `steward/runtime`：`ListAllowlist`、`AddAllowlistEntry(ctx, actorID, host, port)`、`DeleteAllowlistEntry`、`IsEgressAllowed`（`backend/internal/steward/runtime/allowlist.go:22-66`） | `GET`、`POST`、`DELETE /api/v1/admin/steward/egress-allowlist`（`backend/internal/steward/httpapi/handlers.go:76-78`，`admin.go:66`、`:93`） | 是（`added_by`）；重复添加保留原添加者与时间（`allowlist.go:44-49`） |
| 备份保留规则 | 备份目录里的文件 `.roomusic-backup-retention.json`（`backend/internal/backup/retention.go:15`） | JSON 文档 `{version, keep_days, keep_count, saved_at}`（`retention.go:18-27`）；文件不存在时回退到启动配置的保留天数（`retention.go:34-38`） | `backup`：`effectiveRetention`、`SaveRetention(ctx, rule, confirm, actorID)`（`retention.go:34`、`:124-154`） | `PUT /api/v1/system/backups/retention`（`backend/internal/backup/httpapi/handlers.go:37`、`:164-168`）；读取随 `GET /api/v1/system/backups` 的概览返回（`backend/internal/backup/service.go:131-135`） | 只写进日志（`retention.go:150-151`） |
| DSP 各项设置 | 表 `playback_zones` 的列（每个播放区一行，属于用户）：`leveling`、`crossfade`、`crossfade_seconds`、`gapless`、`quality`（`backend/migrations/0043_playback_zones.sql:26-30`），以及 JSONB 文档列 `headroom`（`0044`）、`sample_rate`（`0045`）、`downmix`（`0046`）、`peq`（`0047_playback_zone_peq.sql:1-2`）、`convolution`（`0049`） | 每个播放区各自的列；JSONB 列经 `zones.SettingsDocument` 规范化（`backend/internal/zones/settings_documents.go:8-12`） | `zones` | `POST /api/v1/me/playback/zones/{zone_id}/commands`（`backend/internal/zones/httpapi/handlers.go:38`） | — |
| DSP 预设与冲激响应 | 表 `dsp_peq_presets`（`backend/migrations/0047_playback_zone_peq.sql:4-12`）、`dsp_ir_files`（`backend/migrations/0048_dsp_ir_files.sql:1-5`） | 每用户多行，`user_id … ON DELETE CASCADE` | `dspsettings`（`backend/internal/dspsettings/preset_store.go:33`、`ir_store.go:25`） | `/api/v1/me/dsp/*`（`backend/internal/dspsettings/httpapi/handlers.go:31-42`） | — |
| 管家个人设置（对照） | 表 `steward_user_settings`（`backend/migrations/0051_steward_runtime.sql:1-16`） | 每用户一行，含 JSONB 列 `overrides`、`probe_result` | `steward/runtime` | `/api/v1/me/steward/settings`（`backend/internal/steward/httpapi/handlers.go:58-59`） | — |

没有通用的键值设置表。回退测试给 0042 配了改写单行设置的数据（`backend/internal/platform/postgres/migrations_rollback_integration_test.go` 中 `rollbackFixtures[42]`）。

## 8. 用户停用与管理员判断

**结论**：停用列是 `users.disabled_at`，角色列是 `users.role`（`admin` 或 `user`）。只凭用户 ID 核对的现成函数只有 `(*identity.Guard).EnabledUser`，它返回启用用户及其角色、不判断是否管理员，并且用 Guard 自己的连接池查询，不接受事务。

- 列定义：`role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','user'))`、`disabled_at TIMESTAMPTZ`：`backend/migrations/0006_multi_user.sql:1-2`。
- 会话路径：
  - `(*Guard).Authenticate(request)`：会话未撤销、未过期且 `u.disabled_at IS NULL`（`backend/internal/identity/guard.go:85-97`）。
  - `(*Guard).RequireAdmin(w, r)`：在上面基础上要求 `Role == "admin"`，否则 403（`guard.go:100-111`）。
- 只凭用户 ID：

  ```go
  // backend/internal/identity/access.go:9-14
  func (guard *Guard) EnabledUser(ctx context.Context, id string) (User, error)
  ```

  查询 `SELECT id::text,username,role FROM users WHERE id=$1::uuid AND disabled_at IS NULL`，用 `guard.database`（`*sql.DB`）；不存在或已停用返回 `sql.ErrNoRows`。调用方：管家工具执行前重读身份（`backend/internal/app/steward.go:171-185`，随后用 `definition.MinRole.Allows(user)` 判断角色）、访问令牌换身份（`backend/internal/steward/token/store.go:125`，接口声明在 `backend/internal/steward/token/token.go:59`）。
- 角色判断的另一处：`tool.Role.Allows(user)`（`backend/internal/steward/tool/definition.go:22-30`）。
- 在事务里直接写 SQL 核对的现有例子（都不是可复用的导出函数）：
  - `SELECT id FROM users WHERE role='admin' AND disabled_at IS NULL FOR UPDATE`（`backend/internal/identity/users.go:61`，`SetUserDisabled` 里保护最后一个管理员）。
  - `SELECT 1 FROM users WHERE id=$1::uuid AND disabled_at IS NULL FOR UPDATE`（`backend/internal/identity/passwords.go:99`）。
  - 长连接复查会话：`… u.disabled_at IS NULL AND u.id=$2::uuid AND u.role=$3`（`backend/internal/app/steward.go:160-163`）。
- 停用入口：`identity.SetUserDisabled(ctx, db, id, disabled, revoked...)`（`backend/internal/identity/users.go:54` 起）；停用最后一个启用的管理员返回 `last_admin`（`users.go:84-88`）。HTTP 入口 `PATCH /api/v1/users/{id}` 只接受 `disabled`（`backend/internal/identity/httpapi/handlers.go:288-314`）。
- 角色的来源：`admin` 只在首次初始化时插入（`backend/internal/identity/httpapi/handlers.go:114-125`，`setup_state` 已存在时返回 `setup_closed`）；创建成员固定是 `user`（`handlers.go:276`）。非测试代码里没有 `UPDATE users SET role`。
- `operations.actor_id` 是指向 `users(id)` 的非空外键，没有 `ON DELETE` 子句（`backend/migrations/0017_governed_operations.sql:9`）。

## 9. 操作记录的读取

**结论**：列表只支持按目标（发行、专辑、艺人）和认领来源筛选，取并集；页码加页大小分页；`origin` 以库里的 JSON 原样返回，没有来源时是 `null`。

- 参数 `backend/internal/operations/records.go:116-121`：`ListParams{Targets []Target, Sources []string, Page, PageSize}`。
- 筛选 `listFilter`（`records.go:131-150`）：每个发行或专辑目标是 `(o.target_type = $n AND o.target_id = $m)`；艺人目标走 `operations_artist_targets`（`:137-140`）；来源是 `(o.after_state #>> '{source,release_id}') = $n`，表达式须与迁移 0020 的部分索引逐字一致（`records.go:129-130`，索引在 `backend/migrations/0020_release_claims.sql:13`）；各条件之间是 `OR`。
- 查询 `List`（`records.go:153-180`）：先 `COUNT(*)`，再 `ORDER BY o.created_at DESC, o.id DESC LIMIT … OFFSET …`。现有索引：`operations_created_index (created_at DESC, id DESC)`、`operations_target_index (target_type, target_id, created_at DESC, id DESC)`（`backend/migrations/0017_governed_operations.sql:34-35`）。
- 行结构 `Record`（`records.go:30-44`）：`Origin json.RawMessage`、`ID`、`ActorID`、`Type`、`TargetType`、`TargetID`、`ExpectedRevision`、`ResultRevision`、`Before`、`After`、`RevertsID`、`OriginalType`、`RevertedByID`、`CreatedAt`；投影里 `o.origin` 是最后一列（`records.go:56-58`、`:66-67`）。
- HTTP：路由 `GET /api/v1/operations` 与 `POST /api/v1/operations/{id}/revert`（`backend/internal/operations/httpapi/handlers.go:48-51`）；handler `listOperations`（`:84-167`），管理员；查询参数 `release_id`（同时加入 `Targets` 与 `Sources`，`:94-101`）、`group_id`（`:102-108`）、`artist_id`（`:110-116`）、`page`、`page_size`（默认 50、上限 100，`:21-25`；解析在 `backend/internal/platform/httpserver/pagination.go:12-31`）。
- 列表 DTO `operationItem`（`handlers.go:60-73`）：`origin`、`id`、`type`、`created_at`、`actor{id, username}`、`release`、`album`、`artist`、`changes`、`reverts`、`reverted_by`、`revertible`；响应外层 `{items, pagination{page, page_size, total}}`（`:163-166`）。
- `origin`：`Origin: record.Origin` 原样赋值（`handlers.go:155`）；类型是 `json.RawMessage`，库里为 NULL 时序列化为 `null`（样本 `backend/internal/app/testdata/api-samples/operations/list.json` 里是 `"origin": null`）。结构体定义见 `backend/internal/operations/operations.go:130-139`。
- `revertible` 的计算：`description.Revertible && record.Type != TypeRevert && record.RevertedByID == ""`（`handlers.go:160`）；发行类记录的 `Revertible` 是“目标可见且 `edit_revision` 等于记录的 `result_revision`”（`backend/internal/catalog/governance/revert.go:315-322`）。
- 写入侧与版本号有关的两处约束：表约束 `operations_revision_ck CHECK (expected_revision > 0 AND result_revision = expected_revision + 1)` 自 0017 起未被后续迁移修改（`backend/migrations/0017_governed_operations.sql:26`；之后只有 `operations_type_ck` 与 `operations_target_type_ck` 被重建，见 `0018:14-18`、`0019:19-20`、`0020:6-7`、`0026:113-118`）；`Outcome.ResultRevision` 的注释是“必须等于 ExpectedRevision + 1”（`backend/internal/operations/operations.go:158`）。
- 管家工具 `list_operations` 用同一个 `operations.List`，页大小默认 20、上限 50，返回同样带 `origin` 但不含操作者（`backend/internal/operations/stewardtools/tools.go:43-94`）。审查证据取最近 20 条（`backend/internal/app/steward.go:143`）。
- 前端用到操作记录列表的文件：
  - `frontend/src/features/governance/api/endpoints.ts`（`:123` 请求列表，`:128` 撤销）
  - `frontend/src/features/governance/api/decoders.ts`（`:306` `origin` 可缺省可为 null，`:587`、`:613`）
  - `frontend/src/features/governance/change-summary.ts`（`:8-13` 来源标签）
  - `frontend/src/features/governance/components/operation-list.tsx`（`:74-80`）
  - `frontend/src/features/governance/components/operations-view.tsx`
  - `frontend/src/features/governance/components/release-history.tsx`
  - `frontend/src/features/governance/components/artist-admin-actions.tsx`
  - `frontend/src/features/governance/operations-search.ts`、`query-keys.ts`、`index.ts`
  - 测试：`frontend/src/test/api-contract.test.ts`、`frontend/src/features/governance/change-summary.test.ts`、`artist-operations-step5.test.ts`、`operations-search.test.ts`、`frontend/src/app/claims.test.tsx`、`default-version.test.tsx`、`release-corrections.test.tsx`

## 10. 工程约定

### 10.1 迁移

- 最新编号 0057：`backend/migrations/0057_steward_memory.sql` 与 `0057_steward_memory.down.sql`。
- 命名 `NNNN_name.sql`，正则 `^([0-9]+)_[A-Za-z0-9][A-Za-z0-9._-]*\.sql$`（`backend/internal/platform/postgres/migrate.go:30`）；版本从 1 连续，重复或跳号报错（`migrate.go:134-135`、`:186`）；经 `embed.FS` 发现（`backend/migrations/embed.go`）。
- 回退文件与正向文件同名加 `.down.sql`（`migrate.go:116-119`、`:153`）；版本 31 起必须有回退文件（`backend/internal/platform/postgres/migrate_rollback.go:15`），无法回退时首行写 `-- roomusic:irreversible <理由>`（`migrate_rollback.go:16`、`:35-44`）。
- 作者规则七条：`.trellis/spec/backend/database-guidelines.md:223-234`（其中第 3 条：放宽 CHECK 或新增 `operations.type` 取值的回退要先清理使用新取值的行；第 4 条：不得含事务外语句）。
- 往返测试 `TestPostgreSQLMigrationRollbackRoundTrip`（`backend/internal/platform/postgres/migrations_rollback_integration_test.go:113`）：升级、回退、再升级并比对 schema 快照；迁移改变了旧约束或枚举而 `rollbackFixtures` 里没有对应版本时失败（`:16`、`:230-233`）。

### 10.2 包依赖规则

- 测试 `TestPackageDependencyDirections`，规则表 `packageRules`：`backend/internal/app/dependency_rules_test.go:23-126`、`:128-183`。
- 登记方式：键是相对 `backend/internal/` 的包路径，值是允许导入的内部包列表与是否可用 `net/http`（`:17-21`）。没有登记的包报 `has no dependency rule`（`:143-146`）；规则表里有而目录里没有的包也报错（`:171-182`）；`app` 的规则里要列出它装配的每个包（`:117-125`）。
- 表头注释的方向约定：平台包是叶子；能力核心不含 HTTP；各 `httpapi` 只依赖本能力、`identity` 与 `platform`；只有 `app` 装配全部（`:23-26`）。

### 10.3 集成测试的隔离数据库

- 辅助包 `backend/internal/platform/postgres/postgrestest/postgrestest.go`：环境变量 `ROOMUSIC_TEST_DATABASE_URL`（`:20`）；未设置时 `t.Skipf`（`:23-30`）；`NewSchema(t, prefix)` 建随机 schema 并返回固定 `search_path` 的 URL，测试结束删除（`:46-77`）；`NewDatabase(t, prefix)` 建独立数据库（`:80-108`）。
- 常见用法：`postgrestest.NewSchema(t, "roomusic_test_")` 后 `postgres.Open(ctx, scopedURL)`（会执行全部迁移），例 `backend/internal/catalog/governance/fixture_integration_test.go:31-34`、`backend/internal/app/auth_root_operations_integration_test.go:54-57`（`newIntegrationTestApplication`）。
- 运行入口 `./scripts/test-integration.sh`（也可 `mise run test-integration`，`.mise.toml` 的 `tasks.test-integration`）：独占 Compose 项目 `roomusic-compose-test-<随机>`（`scripts/test-integration.sh:6-9`），文件锁防并发（`:11-12`），随机端口与库名并导出 `ROOMUSIC_TEST_DATABASE_URL`（`:19-29`），结束时 `down -v`（`:40-49`）；实际命令 `go test -v ./... -run "${ROOMUSIC_TEST_RUN:-TestPostgreSQL|TestFFmpeg}" -count=1 -timeout 25m`（`:57`）。
- 可用环境变量：`ROOMUSIC_TEST_RUN`（收窄用例）、`ROOMUSIC_TEST_KEEP_DB`、`ROOMUSIC_TEST_COMPOSE_PROJECT`、`ROOMUSIC_TEST_PG_PORT`、`ROOMUSIC_UPDATE_API_SAMPLES`。脚本注释：app 包串行约 8 分钟（`:55-56`）。
- 默认筛选只匹配名字含 `TestPostgreSQL` 或 `TestFFmpeg` 的用例。
- 文档：`docs/development-environment.md:25-33`（项目名、端口绑定、串行、只清理本次测试资源、只用合成数据），`.trellis/spec/backend/database-verification.md`。

### 10.4 变异检查

- 没有工具，是手工做法：对新增逻辑逐项临时改坏实现，确认相关用例失败后还原；存活的变异逐个判断是否等价。说明在 `.trellis/spec/frontend/quality-verification-layers.md:49-50`。
- 适用范围：只用于并发、权限与数据迁移等关键逻辑（`docs/architecture/core-1-roadmap.md:69`，R5；`:211`）。
- 最近一份有完整记录的任务文档（本机归档）：`.trellis/tasks/archive/2026-11/11-05-deploy-login-throttle/research/test-agent-validation.md`——只改实现不改断言，每次只跑相关测试并要求因目标断言失败，随后恢复原始字节（`:126`）；列出“变异 → 检出位置”表（`:128` 起）、实际命令（`:142` 起）与还原后的文件 SHA-256（`:160` 起）。
- 更早的写法：`.trellis/tasks/archive/2026-10/10-17-classical-m4-claim/implement.md:15`（每个修正配回归测试，临时去掉修正确认新测试失败再还原）。
- 本任务已写入的要求：`.trellis/tasks/11-22-auto-curation-a-foundation/prd.md:83`、`.trellis/tasks/11-22-auto-curation-sources/implement.md:66`。

## 未确认

- 未运行任何测试；全部结论来自阅读代码、迁移、规范与任务文档。
- `artist_names.origin='correction'` 只在名字首次创建时写入这一点，取自 `names.go:86-120` 的前半段；没有通读该函数后半段对已存在名字键的处理。
- `refreshCandidates`（`backend/internal/catalog/internal/artistgraph/candidates.go:13`）与 `refreshProfiles`（`profiles.go:34`）内部是否还有区分署名层或名字来源的逻辑没有逐行核对；全库搜索 `layer` 只命中 `facts.go` 与 `refresh.go`。
- `catalog` 包里其余艺人读取（`artists_reads.go`、`artists_summary_sql.go` 等）是否有直接读 `releases.album_artist` 而不经视图的位置，没有逐个核对；只确认了 `artists_collections.go:106` 用的是视图。
- 认领撤销的锁顺序与 `claim_revert.go:60-120` 之间的细节没有逐行读，相关结论取自文件头注释与合同。
- 前端对 `corrected_fields`、`scan_value`、`effective_value` 的使用只列出了命中文件（`frontend/src/features/catalog/` 与 `frontend/src/features/governance/` 下共 22 个文件），没有分析各自的展示逻辑。
- 搜索样本里两份 `artist-correction-*` 与 `operations/artist-*` 样本的具体记录行号没有查。
- “开启者降为普通成员”这一情形：代码里没有找到修改 `users.role` 的路径，也没有找到创建第二个管理员的路径（管理员只在首次初始化时创建）；是否有脚本层面的途径（如 `scripts/reset-password.sh`）改角色没有查。
- 备份保留规则文件的写入细节（`writeRetention`）与并发保护没有展开。
