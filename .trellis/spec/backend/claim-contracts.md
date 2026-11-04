# 认领合同（阶段 M4 第三项）

## 1. 范围与触发

M4 的第三项（2026-10）让管理员把**已不可见的旧版本**上的人工修改一次迁给扫描新建的**新版本**：字段修正、所在的专辑、默认版本与问题核对四类。
新增或修改以下内容时必须阅读并更新本文件：迁移 0020、“待认领”的判定谓词（`backend/internal/catalog/governance/claims.go`）、推荐、认领的预览与执行器
（`governance/claim.go`）、认领的撤销与展示（`governance/claim_revert.go`，`governance.ClaimOperations`）、待认领的读取（`governance/claim_read.go`）、操作记录按来源筛选。

- 共同写入流程（幂等、记录、撤销框架、操作记录列表）仍在[受治理操作合同](./governance-contracts.md)；本文件只写认领自己的规则。
- 四类人工状态各自的规则在[受治理操作合同](./governance-contracts.md)（修正、归组）、[默认版本合同](./default-version-contracts.md)与[核对合同](./issue-review-contracts.md)。
- 扫描**必须保持不读写这四类状态与操作记录**（第 9 节）。认领只在管理员提交时发生，没有自动认领。

决策依据：[ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 与 [ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md) 的
2026-10-17 决策记录，任务 [M4 认领](../../tasks/archive/2026-10/10-17-classical-m4-claim/prd.md)（裁决 K1–K9）。

## 2. 数据模型（迁移 0020）

不新增表：旧版本认领之后一无所有，前后状态完整记在操作记录里，撤销以它为依据；“待认领”由谓词实时算出。

- 放宽 `operations_type_ck`（名字不变）：操作类型增加 `release_claim`；目标类型仍是 `release`，目标是**新版本**，旧版本与各组的状态写在记录的前后状态里。
- 部分表达式索引 `operations_claim_source_index ON operations ((after_state #>> '{source,release_id}'), created_at DESC, id DESC) WHERE (after_state #>> '{source,release_id}') IS NOT NULL`：
  按**来源**找认领记录与它们的撤销记录（两者的状态里都有 `source`，其他类型没有这个键，不进索引）。`operations.ListParams.Sources` 的表达式必须与索引逐字相同，
  `EXPLAIN` 用例守住。
- 不写 down 迁移。回退是关闭认领的写入路由与页面入口，数据都在既有的表里。

## 3. 待认领的判定

**旧版本**：发行行存在、**不可见**（`releasesql.ReleaseVisiblePredicate` 为假），并且留有下列至少一项人工状态：有 `release_corrections` 行；所在的组里有**别的发行**（不论可见与否）；
是某个组的 `default_release_id`（失效中的也算）；有 `release_issue_reviews` 行（过期的也算）。
扫描从不把新发行放进已有的组，所以组里有别的发行只可能来自人工归组；看“有别的发行”而不是“有别的可见发行”，同一张专辑的两个版本都被替换之后仍能逐个认领回这张专辑。
**新版本**：任意可见的发行，不是旧版本自己。

判定是一个 SQL 谓词常量 `releaseClaimablePredicate`，列表、详情、预览、执行器与提示共用。认领之后旧版本的四类状态都已移走或清除，不再满足判定；文件回来、旧版本又可见时也不再满足。

## 4. 推荐

推荐只决定排序与提示，不自动执行。旧版本 O 的**路径集合** = O 名下全部曲目的 `(source_root_id, relative_path)`（原地替换后缺失的旧曲目仍保留路径）
加上 O 的归组依据 `release_grouping_evidence.source_refs` 里同一 root 的路径（最多 100 条；新增文件使原有曲目整体改挂走之后，只有这一行还记得原来的文件）。
候选是可见版本 N（N ≠ O），满足任一条件，理由码写进响应：

| 理由码 | 条件 |
| --- | --- |
| `same_files` | 路径集合里有路径与 N 的某首 present 曲目相同（同一 root；比较走 `tracks_bound_source_path_uq`，每条路径一次索引查找） |
| `same_directory` | `O.source_root_id = N.source_root_id` 且 `O.relative_directory = N.relative_directory`（没有可用索引，只在打开一个旧版本的详情时算一次） |

排序：两个理由都命中的在前；然后 `same_files` 命中的路径数多的在前；再按 N 的有效标题（小写）和 ID。最多 5 个。不用音频指纹，不按标题相似度；
目录与文件名都改了（含跨 root 移动）的版本推荐不出来，只能手动搜索（复用“归入其他专辑”的专辑搜索，可以选到别的目录、别的 root 的版本）。

**真实扫描核对过的情形**（`TestPostgreSQLClaimCluesSurviveARealRescan`，合成 FLAC）：原地替换音频（新版本同路径，旧曲目标为缺失）、新增文件使旧发行名下一首曲目都不剩（2 首加 3 首）、
目录不变而文件名与内容都换了（只命中 `same_directory`）。**单纯的文件改名不新建发行**（扫描按内容指纹把曲目重绑定到新路径），不属于认领。

**专辑详情的提示**用同一组条件反过来查：`GET /api/v1/releases/{id}/claim-hints` 返回把这个可见版本列为推荐的待认领旧版本，最多 3 个，最近看到的在前，不受推荐 5 个的上限限制。
做法是先按待认领过滤（这样的版本很少），再逐个检查，不从 N 出发反查整个库。

## 5. 预览

`GET /api/v1/releases/{id}/claim-preview?source=<旧版本 ID>`（只有管理员，不加锁）：检查顺序 401、403、400（`source` 缺失、重复或非 UUID 是 `invalid_id`；两者相同是 `invalid_input`）、
404（新版本不存在或不可见，旧版本不存在）、409 `not_claimable`、200。

预览与执行器共用纯函数 `buildClaimPlan`，按同一份状态快照得出每一项的去向，所以“预览写的就是执行的”是结构保证。响应
`{release, source, revision, source_revision, items, token}`；`release` 与 `source` 是 `{id, title, group_id, visible}`。`items` 的每项
`{kind, field?, issue?, group?, action, reason?, effect?, from?, to?}`：

| kind | 迁移（`move`） | 跳过（`skip`）与原因 | 其他 |
| --- | --- | --- | --- |
| `correction`（`field`） | 新版本没有同一字段的修正：复制过去 | `target_has_correction`：新版本已有，保留新版本的 | `from` 是旧意图，`to` 是新版本现在的 |
| `album`（`group`） | 旧版本所在的组有别的发行，新版本所在的专辑里没有别的**可见**版本：新版本归入那个组 | `already_in_album`（新版本已在那个组里）、`target_in_album`（新版本在另一张多版本专辑里） | 没有别的发行就没有这一项 |
| `default`（`group`） | 新版本认领之后属于那张专辑：接任默认版本 | — | `reset`：新版本没有进入那张专辑，那张专辑改回自动选择（旧版本不可见，原来的设定本来就失效，显示不变） |
| `review`（`issue`） | 新版本没有同一类核对：依据与原 `operation_id` 原样复制（核对人与时间不变） | `target_has_review`；`not_transferable`（缺失曲目的核对针对旧版本自己的曲目，不迁移） | `effect`：`valid`（新版本现在有同样的问题且依据相同，立即生效）、`stale`（有同类问题但依据不同，会重新提醒）、`no_issue`（现在没有这类问题，核对暂时无事可压） |

**令牌**是计划的指纹：来源与目标的 ID，加上每一项的 `kind、field、issue、group、action、reason`，取 SHA-256；不含标题、`from`、`to`、`effect` 等展示字段。
两个发行的修正与核对只能被受治理写入改变，每次改变都让发行的 `edit_revision` 加一，版本号另行核对，所以不必进令牌。令牌要拦住的是不改变这两个版本号的变化：
扫描改变了成员关系（来源的组里是否还有别的发行、新版本所在的专辑有几个别的可见版本），以及别人改了以来源为默认版本的专辑（只让专辑的版本号加一）。
令牌绑定具体的一对版本：拿别的版本对的令牌来提交得到 `claim_changed`。

## 6. 认领的写入

`POST /api/v1/releases/{id}/claim`，`{id}` 是新版本；请求体 `{expected_revision, source_release_id, source_revision, preview}`。

- **检查顺序**：同源（403）→ 管理员（401/403）→ 路由 ID（`400 invalid_id`）→ `Idempotency-Key`（`400 invalid_input`）→ 请求体（`invalid_json`；版本号小于 1、
  `source_release_id` 非 UUID、与新版本相同、缺 `preview` 都是 `400 invalid_input`）→ `operations.Run`。
- **指纹**：`{type: release_claim, release_id, expected_revision, source_release_id, source_revision, preview}`（`governance.ClaimRequest.Fingerprint`）。
- **执行器 `governance.ClaimExecutor`**，在同一个事务里：
  1. **加锁，顺序固定**：先对旧版本核对行引用的操作记录加 `FOR KEY SHARE`；再按 UUID 升序 `FOR UPDATE` 两个发行行（`lockReleaseRow`，不要求可见）；最后按 UUID 升序锁涉及的组行
     （新版本的组、旧版本的组、以旧版本为默认版本的组）。这与归组、默认版本的顺序一致（先发行、后组）；扫描记录反馈时也是先按 UUID 升序 `FOR UPDATE` 发行行、再按 UUID 升序 `FOR SHARE` 组行（`ingest/scan_feedback_snapshot.go` 的 `lockFeedbackGraph`），所以不会形成环形等待。
     先锁记录的原因同核对合同第 5 节：核对行带着原 `operation_id` 迁到新版本，提交时的外键检查会给那条记录加 KEY SHARE 锁，而撤销那条更早的记录会先 `FOR UPDATE` 锁它、再锁发行行。
  2. 新版本不存在或不可见、旧版本不存在：`404 not_found`；旧版本可见或不满足判定：`409 not_claimable`（先于版本号核对：已经认领过的旧版本不论带什么版本号都是它）。
  3. 任一版本号不等：`409 revision_conflict`。
  4. 重新计算计划，令牌不等：`409 claim_changed`。
  5. 按计划写入：迁移的修正写本次操作的 ID，迁移的核对保留原操作 ID；然后删除旧版本的全部修正行与核对行；新版本归入旧版本原来的组；
     旧版本只要所在的组里有别的发行就移进一个**新建的空组**（这样它才离开待认领列表；不可见的空组不出现在任何地方，组从不删除）；默认版本按计划写入，被写的组版本号加一；两个发行的 `edit_revision` 各加一。
     **四类全部被跳过时也照样执行**：旧版本的修改被清走、留在记录里，表示新版本已经接替了它。
- **记录**：`expected_revision` 与 `result_revision` 是**新版本**的（`Outcome` 只有一对版本号，撤销规则以目标为准）；前后状态类型化且写全
  `{source: {release_id, revision, title, group_id, corrections, reviews}, target: {…同形}, groups: {组 ID: {revision, default_release_id}}}`，
  后态的 `groups` 还含旧版本新建的空组。
- **响应**：`{release_id, source_release_id, group_id, previous_group_id, revision, source_revision, operation_id, moved: {corrections, album, reviews}, skipped: {corrections, album, reviews}, defaults: {moved: [组 ID], reset: [组 ID]}}`。
  `group_id` 与 `previous_group_id` 是新版本认领之后与之前所在的组；同键同义重放返回同一份响应。

## 7. 撤销与展示

`governance.ClaimOperations()` 实现 `operations.Kind`，由 `app` 注册：

- **Revert**：先对前态引用到的更早的操作记录加 `FOR KEY SHARE`（写回的修正行与核对行带原 `operation_id`），再按 UUID 顺序锁两个发行行，最后锁前态里的各个组
  （认领时新建的空组不在前态里，不锁、不核对，撤销之后留着无害）。**以下任一不等就是 `409 revert_conflict`，不做部分撤销**：新版本的 `edit_revision` 与记录的 `result_revision`；
  旧版本的版本号与后态里的值；前态里各组的版本号与后态里的值。然后写回**前态**：两个发行的修正与核对（先清掉现有的，再连同原 `operation_id` 写回，核对人仍是原来的人）、
  两个发行的 `group_id`、各组的默认版本（只写与现在不同的）；两个发行的版本号加一，默认版本被写回的组版本号也加一。撤销记录的前后状态与认领同形状，所以按来源筛选也能找到它。
- **撤销不要求新版本可见**：认领之后新版本又被整张替换、变得不可见时，撤销仍能把人工状态还给旧版本。这与其他类型（目标不可见就 `404`）不同，因为这种撤销的目的就是把修改还给另一个版本。
- **Describe**：改动摘要 `{kind: "release_claim", source: {id, title, visible}, previous_group_id, moved: {corrections, album, defaults, reviews}, skipped: {…}}`；
  `defaults` 是接任默认版本的专辑**数量**（改回自动选择的不算）。`moved` 与 `skipped` 由前后状态推导，不另存：新版本上操作号是这条记录的修正是迁来的，其余被跳过；
  核对看依据与操作号是否原样出现在新版本上（缺失曲目的核对因此总在 `skipped`）；专辑看新版本是否进了来源原来的组。**撤销记录只带 `kind` 与 `source`。**
  **`source.title` 取记录里的名字**（认领用认领之前的有效标题，撤销用恢复之后的），`visible` 才取现在的状态：认领会把旧版本的修正清走，现在的有效标题已是扫描值，
  取现在的就会让操作记录里的名字和用户认领时看到的不同（真实服务用例发现，`TestPostgreSQLClaimDescribe` 守住）。
  `revertible` 只看版本号：新版本停在 `result_revision`，旧版本与前态里各组停在后态里的版本；不要求可见。
- **操作记录列表**：`GET /api/v1/operations?release_id=` 除了以这个版本为目标的记录，还列出以它为**来源**的认领与撤销记录（`ListParams.Sources`，与 `Targets` 取并集）。
- **与其他记录的关系**：认领之后，被移走的修正与核对的原记录目标仍是旧版本，而旧版本的版本号已变，所以它们不能再单独撤销（`revert_conflict`），与“只撤销最近一次修改”一致；
  要恢复就撤销这次认领。认领之后新版本又被修改，认领就不能再撤销。

## 8. 读取接口（只有管理员）

- **`GET /api/v1/release-claims?page=&page_size=`**：待认领列表，按最后一次被看到的时间倒序（名下曲目 `observed_at` 的最大值；没有曲目取归组依据的 `observed_at`；都没有取发行的创建时间），再按 ID；
  分页默认 50、最大 100（`400 invalid_pagination`）。每项 `{release: {id, title, artist, year, edition}, album: {id, title, other_release_count} | null, location: {root_name, directory}, last_seen_at, holds: {corrections, album, default, reviews}}`。
  标题是有效值（有修正时就是修正后的名字，管理员认得出）；`album.title` 取**不含旧版本自己**的成员里按代表版本规则排第一的有效标题（可见成员优先）；
  `root_name` 是扫描目录路径的末段，`directory` 经 `SafeRelativeSourceRef`，不返回绝对路径，不安全的是空串。推荐只在详情里算。
- **`GET /api/v1/release-claims/{id}`**：列表项的全部字段在顶层，再加 `revision`、`corrections: [{field, correction: {mode, value?}, scan_value}]`（字段表的固定顺序）、
  `siblings: [{id, title, visible}]`（最多 20）、`default_of: [{id, title}]`、`reviews: [{issue, reviewed_at, reviewer: {id, username}}]`（用户名由传输层补上）、
  `suggestions: [{release, group_id, edition, reasons, same_files}]`（第 4 节）。不满足判定是 `404 not_found`。
- **`GET /api/v1/releases/{id}/claim-hints`**：`{items: [{id, title}]}`，没有时是空数组；版本不存在或不可见 `404 not_found`。
- 检查顺序都是 401、403、400、404、200。

## 9. 与扫描和其他操作的关系

- 扫描不读写认领涉及的四类状态与操作记录：认领之后重扫，修正按视图继续生效，归组与默认版本保持，核对在依据相同时仍有效；不变的重扫不改认领的结果。这是结构保证，真实重扫用例守住。
- 旧版本的文件回来（又可见）时，它身上已经没有人工状态，自己是一张单独的专辑，与新版本是两个可见版本；撤销认领可以把修改还给它。
- 认领后的新版本可以照常修正、归组、核对、设为默认版本。
- 并发：认领、归组、默认版本、核对与撤销都按“记录 → 发行行（UUID 升序）→ 组行（UUID 升序）”的顺序加锁。扫描记录反馈时同样先锁发行行（UUID 升序 `FOR UPDATE`）、后锁组行（UUID 升序 `FOR SHARE`），不锁操作记录。

## 10. 错误码（新增）

| 条件 | 响应 |
| --- | --- |
| 旧版本现在可见，或已不再留有人工修改 | `409 not_claimable` |
| 预览令牌与现在的不同（页面打开之后有扫描或修改） | `409 claim_changed` |
| 新版本与旧版本相同 | `400 invalid_input` |

其余与[受治理操作合同](./governance-contracts.md)第 8 节相同：401、403、400、`404 not_found`、`409 revision_conflict`、`idempotency_conflict`、撤销的各种 409、`503`、`500`。

## 11. 必需测试与断言点

- `internal/catalog/governance`：`claims_integration_test.go`——四类人工状态各自让不可见的版本待认领，可见的与没有人工状态的不是；推荐的两个理由、排序、上限与有界性。
  `claim_integration_test.go`——四类迁移、新版本已有的保留、专辑的各种情形、没有专辑、缺失曲目的核对不迁移与全部跳过、迁移的核对仅在依据相同时有效、各种拒绝都没有副作用、
  预览之后的变化被令牌拦下、同键重放、预览只读、与修正、归组、默认版本、核对、扫描随机交错不死锁，以及认领与撤销那条核对的原记录交叉的死锁回归（去掉 KEY SHARE 锁时报 `40P01`）。
  `claim_revert_integration_test.go`——撤销恢复全部前态与原核对人、跳过的也恢复、之后又有修改被拒绝、新版本不可见仍可撤销、`Describe`（含记录里的来源名字）、撤销与原核对交叉不死锁。
  `claim_read_integration_test.go`——列表、专辑与默认版本、位置不泄漏绝对路径、详情、提示。
- `internal/operations`：`TestPostgreSQLListFiltersBySourceRelease`——迁移后的类型约束、按来源筛选、与目标取并集、走部分索引。
- `internal/app`：`claim_api_integration_test.go`——401、跨源与非管理员 403、各种 400 都没有副作用；预览、认领与重放、并发请求、记录与撤销、之后又有修改时拒绝、读取接口；
  `claims_rescan_integration_test.go`——三种扫描情形的线索与认领之后的多次真实重扫。接口样本：`release-claims/{list,get,hints,preview,claimed}.json`、
  `operations/{list-claims,list-claims-reverted,reverted-claim}.json`、`releases/get-claimed.json`、`errors/{claim-changed,not-claimable,not-claimable-release,revision-conflict-claim}.json`。
- 真实服务：`frontend/e2e/real/claims.real.ts`（见[前端验证分层](../frontend/quality-verification-layers.md)）。
- 运行 `go test -race -count=1 ./internal/operations/...` 与 `go test -count=1 ./internal/catalog/... ./internal/app/...`（PostgreSQL 集成需隔离 PostgreSQL 18，见[质量规范](./quality-guidelines.md)）。

## 12. 边界与已知限制

- 不做自动认领、批量认领、“放弃这些修改”（没有接替者的旧版本一直留在列表里，按最后一次看到的时间沉到后面）、一个旧版本拆给多个新版本，或多个旧版本合到一个新版本时逐项挑选；
  不迁移可见版本之间的修改；不合并或拆分 Release（M4 第五项）；不改音乐文件。
- 推荐只看文件路径与目录；归组依据的路径最多 100 条，超出部分不参与推荐。
- 认领之后被移走的修正与核对的原记录不能再单独撤销。
- 同一张专辑的几个版本先后被替换，而前一个的新版本没有进入这张专辑（它已在别的多版本专辑里）时，后面的旧版本看不出原来的专辑关系，需要认领之后手动归组。
- 普通成员看不到任何认领入口与状态，只看到修改生效后的结果。
- 回退：关闭认领的写入路由与页面入口，数据保留；不写 down 迁移。
