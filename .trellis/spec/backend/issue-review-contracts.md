# 元数据问题与核对合同（阶段 M4 第二项）

## 1. 范围与触发

M4 的第二项（2026-10）把“需要关注”拆成可以逐条说明与处理的问题：管理员在“元数据”页按版本看到扫描拿不准的字段、同一目录的归组冲突与缺失的曲目，
字段沿用 M3 的修正，来源类型、介质类型、归组冲突与缺失曲目可以**标记为已核对**。新增或修改以下内容时必须阅读并更新本文件：
核对表 `release_issue_reviews`、“需要关注”的三类拆分（`backend/internal/catalog/internal/releasesql/issues.go`）、依据与依据令牌、核对接口与执行器、
它的撤销与展示（`governance.IssueReviewOperations`）、问题列表与版本的问题两个读取接口。

- 共同写入流程（幂等、记录、撤销框架、操作记录列表）仍在[受治理操作合同](./governance-contracts.md)；本文件只写核对这种操作自己的规则。
- “需要关注”的读取与按组之和仍由 [Catalog REST 合同](./catalog-rest-contracts.md)描述；算法以本文件第 3 节为准。
- 扫描**必须保持不读写核对表**（第 7 节）。

决策依据：[ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 与 [ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md) 的
2026-10-17 决策记录，任务 [M4 问题处理](../../tasks/archive/2026-10/10-16-classical-m4-issue-handling/prd.md)（裁决 K1–K8）。

## 2. 数据模型（迁移 0019）

- **`release_issue_reviews`**：`PRIMARY KEY (release_id, issue)`，每个版本每类问题至多一行；`issue` 是 `grouping`、`missing_tracks`、`source_type`、`media_type`
  （约束 `release_issue_reviews_issue_ck`）；`basis` 是核对时的**依据**（JSONB 对象，`release_issue_reviews_basis_ck`）；`operation_id` 指向设下它的操作
  （外键 `DEFERRABLE INITIALLY DEFERRED`，核对人与时间取自那条记录）。`release_id` 外键 `ON DELETE CASCADE` 只是保险（发行从不删除）。
- 过期的行（依据不再相等）不删除：详情据此说明“核对之后情况变了”；再次核对时覆盖。
- 迁移 0019 同时放宽 `operations_type_ck`（名字不变）：操作类型增加 `release_issue_review`；目标类型仍是 `release`。

## 3. 问题、依据与“需要关注”

三类问题只看可见版本，都在 `internal/releasesql/issues.go` 的常量里定义一次，“需要关注”、问题列表与详情共用：

| 问题 | 什么时候存在 | 依据（JSONB） | 核对仍有效的条件 |
| --- | --- | --- | --- |
| 字段（`source_type`、`media_type` 可以核对） | 字段决定是 `uncertain_apply` 且没有人工修正（`artist` 由专辑艺人的修正覆盖） | `{reason, value, candidates}` | 核对时的依据与现在的相等 |
| `grouping` | `release_grouping_evidence.reason` 非空（目前只有 `same_directory_conflict`） | `{reason, source_refs}` | 同上 |
| `missing_tracks` | 有 `source_status = 'missing'` 的曲目 | `{tracks: {曲目 ID: 最后一次看到的时间（Unix 微秒）}}` | **逐首**：这首在依据里，且时间没变 |

- 时间统一用 `(EXTRACT(EPOCH FROM observed_at) * 1000000)::bigint`，写入与比较是同一个表达式（`releasesql.MissingTrackSeenAt`）。曲目回来时扫描更新 `observed_at`，
  再缺失时时间已经不同；核对之后新缺失的曲目不在依据里，照常计入。
- **“需要关注”= 三类待处理数目之和**（`releasesql.ReleaseOpenFieldIssues + releasesql.ReleaseOpenGroupingIssue + releasesql.ReleaseOpenMissingTracks`），另有三个“已核对”常量只给问题页用。
  **没有核对行时与只看扫描结论的算法逐项相同**（迁移门禁：既有集成测试与接口样本逐字不变）。组的计数是可见成员之和，`attention=required` 与概览随之变化，不另写规则。
- **依据令牌**：依据的 JSONB 文本输出（PostgreSQL 对同一个值的输出唯一：键排序、去重）取 SHA-256，读取接口作为 `basis` 返回；核对请求带回它，
  执行器锁住发行之后重新计算，不同就 `409 issue_changed`。扫描不改发行的版本号，没有令牌的话，页面打开与提交之间的一次扫描会让管理员核对一个自己没看到的情况。
- 已知的多余提醒：候选值按出现次数排序，次数变化导致顺序变化也算“情况变了”；文件改名会让归组冲突重新提醒一次；来源路径最多 100 条，超出部分的变化发现不了。

## 4. 核对与取消核对

`POST /api/v1/releases/{id}/issue-review`，请求体 `{expected_revision, issue, action: "review", basis}` 或 `{expected_revision, issue, action: "unreview"}`。

- **检查顺序**：同源（403）→ 管理员（401/403）→ 路由 ID（`400 invalid_id`）→ `Idempotency-Key`（`400 invalid_input`）→ 请求体（`invalid_json`；
  版本号小于 1、`issue` 不是四种之一、`action` 不对、`review` 缺 `basis` 或为空、`unreview` 带 `basis` 都是 `400 invalid_input`）→ `operations.Run`。
- **指纹**：`{type: release_issue_review, release_id, expected_revision, issue, action, basis}`（`governance.IssueReviewRequest.Fingerprint`）。
- **执行器**（`governance.IssueReviewExecutor`）：`lockVisibleRelease`（只锁发行行；不存在或不可见 `404 not_found`）→ 版本号不等 `409 revision_conflict` →
  - `review`：问题现在不存在或依据令牌不等于请求的 `basis` 是 `409 issue_changed`；这一类现在没有待处理的部分（已核对且仍有效）是 `400 no_changes`；否则写入现在的依据（覆盖过期的行）。
  - `unreview`：没有核对行 `400 no_changes`（过期的行也可以取消）；否则删除。
  - 发行的 `edit_revision` 加一；前后状态是 `{issue, review: {basis, operation_id} | null}`。
- **响应**：`{release_id, issue, reviewed, revision, operation_id}`；同键同义重放返回同一份响应。
- 与修正、归组共用发行的版本号，所以规则仍是“只能撤销最近一次修改”。取消核对是普通的新操作（预期版本是当前版本号），任何时候都能做。
- 并发：只锁发行行，不锁组行，不会与归组、默认版本形成环形等待。扫描的 `ingest.PersistCandidate` 先更新发行行再重写证据，所以执行器锁住发行之后读到的依据
  不会是同一版本扫描写到一半的样子；`ingest.MarkMissingSources` 不锁发行行，它提交后新标为缺失的曲目不在依据里，照常计入。

## 5. 撤销与展示

`governance.IssueReviewOperations()` 实现 `operations.Kind`，由 `app` 注册：

- **Revert**：把核对行恢复成前态（有前态就写回当时的依据与操作，核对人仍是原来的人；没有就删除）；恢复的核对现在过期也照样恢复（有效性在读取时判断）。
  版本号不等于原记录的 `result_revision` 是 `409 revert_conflict`，版本不可见 `404 not_found`。
  **加锁顺序**：前态有核对行时先 `SELECT … FOR KEY SHARE` 锁住它引用的那条更早的操作记录，再锁发行行。写回的行在提交时的外键检查会给那条记录加 KEY SHARE 锁，
  而撤销那条更早的记录会先 `FOR UPDATE` 锁它、再锁发行行；不按这个顺序会死锁（`TestPostgreSQLRevertingAnUnreviewAndTheOriginalReviewDoNotDeadlock`，去掉这把锁时
  PostgreSQL 报 `40P01`）。操作记录从不更新或删除，KEY SHARE 不挡任何其他写入。
- **Describe**：复用 `loadReleaseInfos`；改动摘要 `{kind: "release_issue_review", issue, from: {reviewed, track_count}, to: {reviewed, track_count}}`
  （`track_count` 只在缺失曲目的核对上有，取依据里的曲目数，其余为 `null`）；`revertible` 与修正相同。状态损坏时 `changes` 为 `null`。

## 6. 读取接口（只有管理员）

- **`GET /api/v1/release-issues?status=open|reviewed&category=fields|grouping|missing_tracks&page=&page_size=`**：`status` 缺省 `open`（有待处理部分的版本），
  `reviewed` 是有被仍然有效的核对压下部分的版本（缺失曲目部分核对时同一个版本出现在两边）；`category` 缺省全部；非法值（含重复给出、空值）`400 invalid_filter`，
  分页同其他列表（默认 50、最大 100，`400 invalid_pagination`）。按有效标题、艺人与 ID 排序。每项 `{release: 版本摘要, group_id, edition, open: {fields, grouping, missing_tracks},
  reviewed: {…}}`，`open` 三项之和等于 `release.attention_count`；`summary: {open: {all, fields, grouping, missing_tracks}, reviewed: {…}}` 是筛选项上的数字
  （一次 `COUNT(*) FILTER` 查询，不随类别与分页变化），`pagination.total` 取自其中对应的一项。
- **`GET /api/v1/releases/{id}/issues`**：不存在或不可见 `404 not_found`。`{release_id, group_id, revision, release: 版本摘要, edition, fields: [...], grouping: … | null,
  missing_tracks: … | null}`：
  - `fields` 是现在仍算问题或已核对的字段（已修正的不列），每项 `{field, value, reason_code, candidates, reviewable, basis, status, review}`；只有来源类型与介质类型 `reviewable` 为真并带 `basis`。
  - `grouping`：`{reason_codes, source_refs, truncated, siblings: [{release_id, group_id, title}], basis, status, review}`；`siblings` 是同一扫描目录
    （`source_root_id` 与 `relative_directory` 相同）的其他可见版本，最多 20 个。
  - `missing_tracks`：`{count, tracks: [{id, title, medium_position, position, source, last_seen_at, reviewed}], truncated, basis, status, review}`，最多列 1000 首，
    依据始终覆盖全部。路径经 `SafeRelativeSourceRef` 处理（与证据的来源路径一致，不返回绝对路径）。
  - `status` 是 `open` 或 `reviewed`；`review` 是 `{operation_id, reviewed_at, reviewer: {id, username}}`，有核对行就给（时间与操作者经 `operations.Load` 读取，
    用户名由 HTTP 层经 `identity.Guard.Usernames` 批量补上）。有核对行而 `status` 是 `open`，就是“核对之后情况变了”。
  - 字段、归组与缺失曲目的明细都在 `internal/releasesql/issues.go` 定义问题的同一批行上读取，待处理的条目与 `release.attention_count` 一致（缺失曲目在“需要关注”里按来源去重计数，
    `count` 与 `tracks` 按曲目行；扫描维护来源身份唯一，正常情况下两者相同）。

## 7. 与扫描、认领的关系

- 扫描不读写核对表、不改版本号与操作记录：依据不变的重扫之后核对保持，依据变了就重新计入（结构保证，`TestPostgreSQLIssueReviewsSurviveARealRescan` 与真实服务用例守住）。
- 成员关系不连续而新建的发行（M3 的 G8，见[受治理操作合同](./governance-contracts.md)第 7 节）是新版本，没有核对，问题重新出现；旧版本的核对随旧版本留存（不可见）。M4 第三项起管理员可以把它们认领给新版本（依据与原核对人、时间原样迁移，缺失曲目的核对除外，见[认领合同](./claim-contracts.md)）。
- 全部文件都找不到的版本不可见，不在问题页，也不计入“需要关注”（现状不变）。

## 8. 错误码（新增）

| 条件 | 响应 |
| --- | --- |
| 核对时问题已不存在，或依据令牌与现在的不同（页面打开之后扫描改了问题） | `409 issue_changed` |
| 问题列表的状态或类别非法 | `400 invalid_filter` |
| 这一类已经核对过且仍然有效 / 取消时没有核对 | `400 no_changes` |

其余错误（401、403、400、`404 not_found`、`409 revision_conflict`、`idempotency_conflict`、撤销的各种 409、`503`、`500`）与[受治理操作合同](./governance-contracts.md)第 8 节相同。

## 9. 必需测试与断言点

- `internal/catalog`：`issues_integration_test.go`——核对表的约束；各类核对只压下自己那一类，原样重写依据不算变化，依据变了重新计入，缺失曲目逐首判断，
  组的计数与“只看需要关注”。`governance/issue_review_integration_test.go`——四类核对与取消（含过期的行）、各种拒绝都没有副作用、令牌稳定（键顺序与空白无关，同一时间相等、
  再次出现后不等）、撤销恢复前态与原核对人（过期也恢复）、`revert_conflict`、`Describe`、同键重放、与修正、归组随机交错不死锁、两个撤销交叉的死锁回归。
  `governance/issue_read_integration_test.go`——列表的数目之和、筛选、分页与 `summary`、与 `attention=required` 一致，详情的内容、上限与核对状态。`governance/issue_review_test.go`：指纹。
- `internal/operations`：`group_target_integration_test.go`——迁移后的类型约束接受核对。
- `internal/app`：`issue_review_api_integration_test.go`——401、跨源与非管理员 403、各种 400 都没有副作用；核对、取消、撤销与记录列表；同键重放与同键异义；
  并发请求；读取接口的权限与 400；**合成素材真实重扫后核对保持、依据变了重新计入**。接口样本：`issue-reviews/{reviewed,unreviewed}.json`、
  `release-issues/{list,list-reviewed,list-missing-tracks,get,get-reviewed}.json`、`operations/{list-issue-reviews,reverted-issue-review}.json`、
  `errors/{issue-changed,invalid-filter,revision-conflict-issue}.json`（依据令牌规范化为顺序编号）。
- 真实服务：`frontend/e2e/real/issues.real.ts`（见[前端验证分层](../frontend/quality-verification-layers.md)）。
- 运行 `go test -race -count=1 ./internal/operations/...` 与 `go test -count=1 ./internal/catalog/... ./internal/app/...`（PostgreSQL 集成需隔离 PostgreSQL 18，
  见[质量规范](./quality-guidelines.md)）。

## 10. 边界与已知限制

- 不做新的检查规则（缺封面、缺年份等）、严重程度与置信度、建议值、批量核对、问题页内搜索与导航上的数量；不接外部参照与 Music Steward（M6）。
- 来源类型与介质类型只能核对，不能修改（M3 的 G2 不变）；不把曲目在版本之间移动，不合并或拆分 Release（M4 第五项）。
- 普通成员只看到“需要关注”的数字变化；看不到问题页、核对人或“已核对”标记；“来源与证据”照常写扫描结论。
- 回退：关闭写入路由，核对行保留；要让“需要关注”回到原来的算法，回退 `internal/releasesql/issues.go` 与 `read.go` 的查询即可，表可以留着。不写 down 迁移。
