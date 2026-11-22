# Research: 覆盖层与受治理操作的现状

- **Query**: 现有数据模型和写入流程离“外部源与模型的修改默认自动生效、事后可撤销、人工修正永远优先”差多少
- **Scope**: internal（只读代码、迁移、ADR 与合同；未运行测试）
- **Date**: 2026-11-22

路径均相对仓库根目录。迁移 SQL 实际在 `backend/migrations/`，`backend/internal/platform/postgres/` 里只有迁移执行器（`migrate.go` 等），没有表定义。

## 结论

1. 元数据只有两层：扫描值（`releases` 等表的列）和“修正意图”（`release_corrections` 等表）；有效值由视图 `release_effective` 在读取时叠加，没有第三层留给外部源或模型。
2. 可修正的字段只有发行级八个；曲目级没有任何字段修正；艺人级是改主名、名字路由、作用域归属、忽略候选四张意图表。
3. 修正行没有“来源/作者类型”列，只有 `operation_id`。谁写的要回查 `operations.actor_id`（必须是用户）和可空的 `operations.origin`（现有取值只区分聊天与 MCP 两个管家入口）。
4. “人工修正优先”是结构保证：扫描只写扫描列，不碰修正表、版本号和既有发行的组。这个保证只存在于“扫描 vs 修正”之间，修正层内部不分作者，后写覆盖先写。
5. `operations.Run` 提供幂等、同事务记录和单条撤销。十种操作类型里，五种发行/专辑类可撤销，四种艺人类和撤销本身返回 `not_revertible`。
6. 没有“批”：表里没有批次列，列表不能按来源或方案筛选，撤销一次只针对一条记录，且只能撤销目标上最近一次修改。
7. 操作者只能是 `users` 表里的用户；管家写入时操作者仍是当前用户，另在 `origin` 里记入口、会话、方案、模式、令牌与审查结论。
8. 艺人合并不可撤销是 2026-11-03 的轻量标准（R4）决定的；反向操作是“拆分并恢复旧身份”，它恢复重定向和按原锚点的收藏，但名字绑定行、作用域归属和链式重定向不会回到合并前的原样。
9. “需要关注”在读取时由 SQL 实时算出，三类：拿不准的字段、归组冲突、缺失曲目；任何修正行都会让对应字段不再计数。
10. 仓库里已有 MusicBrainz 只读客户端，但只作为管家对话工具；没有任何不经会话的后台代码调用 `operations.Run`。

## 一、元数据的分层

### 1.1 扫描观察值

| 内容 | 表与字段 | 依据 |
|---|---|---|
| 发行的扫描值 | `releases`：`title`、`artist`、`album_artist`、`year`、`source_type`、`media_type`、`genres`、`catalog_number`、`edition`、`label`、`barcode`，另有位置列 `relative_directory`、`candidate_anchor`、`candidate_kind` | `backend/migrations/0002_core_slice.sql:34`、`backend/migrations/0010_album_organization.sql:2-5`、`backend/internal/catalog/ingest/persist.go:195-199` |
| 字段决定（扫描为什么选这个值） | `release_field_decisions`：`field_key`、`selected_value`、`selected_source`、`confidence`（high/medium/low）、`action`（auto_apply/uncertain_apply）、`rule_id`、`candidates`、`reason` | `backend/migrations/0010_album_organization.sql:17-22`、`:41` |
| 归组证据 | `release_grouping_evidence`：`candidate_kind`、`rule_id`、`source_refs`、`reason` | `backend/migrations/0010_album_organization.sql:45-49` |
| 曲目的扫描值 | `tracks`：`title`、`artist`、`position`、`disc_number`、`source_status`（present/missing）、`observed_at` 等 | `backend/migrations/0002_core_slice.sql:49` |
| 逐轨观察 | `track_observations`：`source_kind`、`inferred`、`field_name`、`value` | `backend/migrations/0003_catalog_observations.sql:25-32`、`backend/migrations/0004_field_provenance.sql:1-3` |
| 艺人事实 | `artist_credits`（`layer` 为 scan 或 correction）、`artist_names`（`origin` 为 scan/correction/seed）、`artist_name_keys`、`artist_tag_evidence` | `backend/migrations/0026_artist_catalog.sql:10-30`、`:88-94` |

重扫时字段决定整批删除后重写，归组证据按发行 upsert：`backend/internal/catalog/ingest/persist.go:132`、`:136`、`:158`。

### 1.2 人工修正（覆盖层）

| 级别 | 表 | 内容 | 依据 |
|---|---|---|---|
| 发行 | `release_corrections` | 主键 `(release_id, field)`；`mode` 为 set 或 null；值在 `text_value`、`year_value`、`list_value`；`operation_id` 指向设下它的操作 | `backend/migrations/0017_governed_operations.sql:44-61`、`backend/migrations/0032_release_genres.sql:21-31` |
| 发行 | `releases.group_id`、`releases.edit_revision` | 人工归组直接改 `group_id`；`edit_revision` 只有受控操作递增 | `backend/migrations/0017_governed_operations.sql:37-39`、`backend/internal/catalog/governance/group_change.go:160` |
| 专辑（组） | `release_groups.default_release_id`、`release_groups.edit_revision` | 默认版本与组自己的版本号 | `backend/migrations/0018_release_group_default.sql:8-10` |
| 发行 | `release_issue_reviews` | 主键 `(release_id, issue)`；`basis` 为核对时的依据；`operation_id` | `backend/migrations/0019_release_issue_reviews.sql:8-16` |
| 艺人 | `artist_overrides` | `display_name`（人工主名）、`redirect_artist_id`（合并后的重定向），各带一个 operation_id | `backend/migrations/0026_artist_catalog.sql:49-56` |
| 艺人 | `artist_name_bindings` | 名字键的全局归属 `key_id → artist_id` | `backend/migrations/0026_artist_catalog.sql:33-37` |
| 艺人（曲目或发行作用域） | `artist_assignment_overrides` | 某曲目或某发行上某个名字键归谁 | `backend/migrations/0026_artist_catalog.sql:39-45` |
| 艺人 | `artist_candidate_ignores` | 被忽略的名字键对 | `backend/migrations/0026_artist_catalog.sql:103-107` |
| 艺人 | `artists.edit_revision` | 艺人的人工修改版本号 | `backend/migrations/0026_artist_catalog.sql:6-9` |

### 1.3 有效值

- 发行：视图 `release_effective`。`title`、`album_artist`（及跟随它的 `artist`）用 `COALESCE(修正值, 扫描值)`；`year`、`genres`、`label`、`catalog_number`、`edition`、`barcode` 用 `CASE`（有修正行取修正值，null 修正为空）。定义见 `backend/migrations/0032_release_genres.sql:33-64`（初版在 `0017_governed_operations.sql:67-96`）。
- 所有读取的发行来源是常量 `ReleaseSource = "release_effective releases"`：`backend/internal/catalog/internal/releasesql/release.go:7`。视图不物化（`docs/architecture/adr/0002-metadata-overlays.md:261-262`）。
- 艺人：`artist_effective_credits`（有效署名）与 `artist_profiles`（`auto_display_name`、`effective_display_name`）是可重建的派生表：`backend/migrations/0026_artist_catalog.sql:58-74`。专辑艺人的修正会生成 `layer='correction'` 的署名事实：`backend/internal/catalog/internal/artistgraph/facts.go:60`、`:94-96`。
- 专辑的代表版本由排序规则在读取时决定，默认版本只在“可见且仍属于这个组”时生效：`backend/internal/catalog/internal/releasesql/release.go:51-56`。

### 1.4 哪些字段可以修正

- **发行级（八个，固定顺序）**：`title`、`album_artist`、`year`、`genre`、`label`、`catalog_number`、`edition`、`barcode`。标题与专辑艺人不能设空。依据 `backend/internal/catalog/fields.go:25-34`，表约束 `backend/migrations/0017_governed_operations.sql:52-54`。
- `source_type`、`media_type` 不在可修正字段里，只能“核对”（见第五节）。
- **曲目级**：没有字段修正。没有找到曲目修正表或曲目有效值视图（全库视图只有 `release_effective` 与 `track_display_durations`，后者在 `backend/migrations/0036_track_analysis_loudness_range.sql:5`）；`backend/internal/catalog/governance/` 下没有更新 `tracks` 的语句。曲目级唯一的人工意图是艺人归属的作用域覆盖（`artist_assignment_overrides.track_id`）。
- **艺人级**：主名（`rename`，set 或恢复 auto）、合并、拆分（按别名或按专辑）、忽略候选。请求结构见 `backend/internal/catalog/governance/artist_requests.go:71-76`、`:97-135`、`:182-187`、`:224-231`。
- ADR 记录“署名等集合字段不在首批”：`docs/architecture/adr/0002-metadata-overlays.md:249-250`。

### 1.5 修正记录能否区分是谁写的

- `release_corrections` 的列只有 `release_id`、`field`、`mode`、三个值列和 `operation_id`，**没有来源或作者类型列**（`backend/migrations/0017_governed_operations.sql:44-50`、`backend/migrations/0032_release_genres.sql:21`）。四张艺人意图表与核对表同样只有 `operation_id`。
- 要知道谁写的，需回查操作记录：
  - `operations.actor_id`：`UUID NOT NULL REFERENCES users(id)`（`backend/migrations/0017_governed_operations.sql:9`）。
  - `operations.origin`：可空 JSONB（`backend/migrations/0054_steward_plans.sql:1`），结构是 `Origin{review_id, review_decision, source, conversation_id, plan_id, mode, token_id}`（`backend/internal/operations/operations.go:130-139`）。
- `origin.source` 在代码里只有两个取值：`chat` 与 `mcp`（`backend/internal/steward/tool/definition.go:46`、`:49`）。HTTP 入口不传 `Origin`，列为 NULL（`backend/internal/catalog/httpapi/corrections.go:94-97`、`backend/internal/catalog/httpapi/artist_governance.go:145`）。
- 所以现在能区分的是：网页直接操作（origin 为 NULL）、管家聊天（`source=chat`，带 `mode`、`conversation_id`，经方案确认的带 `plan_id`）、MCP（`source=mcp`，带 `token_id`）。**没有“外部源”这一取值。**
- 管家写工具的可选参数 `basis={source: local|external|mixed, note}` 只用于展示，不进执行器、不进指纹，也不在 `operations` 里（`backend/internal/catalog/governance/stewardtools/tools.go:20`、`backend/internal/catalog/governance/stewardtools/commands.go:64-67`、`.trellis/spec/backend/steward-runtime-contracts.md:245`）；它只随工具输入存在 `steward_plan_items.input` 里（`backend/migrations/0054_steward_plans.sql:25`）。
- 普通成员只看得到 `corrected_fields`（有修正的字段名），看不到来源与操作者：`.trellis/spec/backend/governance-contracts.md:125`。

## 二、“人工修正优先”现在怎么保证

- **扫描不写覆盖层。** 既有发行重扫时的 `UPDATE releases` 只含扫描列，不含 `group_id` 与 `edit_revision`：`backend/internal/catalog/ingest/persist.go:199`。迁移注释写明“扫描只写 releases，从不碰这张表”：`backend/migrations/0017_governed_operations.sql:41-43`。合同称之为结构保证：`.trellis/spec/backend/governance-contracts.md:137-138`。
- **读取经视图叠加。** 有修正行就取修正值，重扫只更新底下的扫描值；清除修正后显示的是当前扫描值（`.trellis/spec/backend/governance-contracts.md:130-131`）。
- **归组。** 扫描只给新建的发行选组（`JoinGroupID` 仅用于新建：`backend/internal/catalog/ingest/organizer.go:68-69`、`backend/internal/catalog/ingest/persist.go:188-195`）；既有发行的组不动。
- **默认版本与核对。** 扫描不更新组行（`backend/migrations/0018_release_group_default.sql:6-7`），不读写核对行（`backend/migrations/0019_release_issue_reviews.sql:5-6`）。
- **艺人。** 扫描不能写四张意图表或 `edit_revision`（`.trellis/spec/backend/artist-contracts.md:41`）。归属解析顺序是 曲目+名字键 → 发行+名字键 → 全局绑定 → 初始归属，再解析重定向；人工主名始终优先（`.trellis/spec/backend/artist-contracts.md:22-24`）。
- **新建发行不继承。** 成员关系不连续而新建发行时，旧发行的修正与归组留在旧发行上，要靠“认领”迁移：`.trellis/spec/backend/governance-contracts.md:140-141`。

### “确认扫描值”是什么

- 就是把字段 `set` 为当前的扫描值。迁移注释：“mode = 'set' 表示改成某个值（等于扫描值也是有效修正，用来确认扫描值）”（`backend/migrations/0017_governed_operations.sql:41-42`）；合同 `.trellis/spec/backend/governance-contracts.md:86`。
- 效果有两个：值被钉住，以后扫描值变了也不跟随；该字段不再计入“需要关注”，因为待处理字段的定义排除了有修正行的字段（`backend/internal/catalog/internal/releasesql/issues.go:21-28`）。
- 执行器不把它当作“无变化”：之前没有修正时，任何 set 都算改变（`backend/internal/catalog/governance/corrections.go:461-464`）。

## 三、受治理操作

### 3.1 `operations.Run` 的签名与流程

```go
func Run(ctx context.Context, database *sql.DB, spec Spec, execute Executor) (Result, error)
type Executor func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error)
```

- 定义：`backend/internal/operations/operations.go:195`、`:167`。`Spec{Origin, ActorID, Type, Key, Fingerprint, RequestID}` 在 `:142-150`；`Outcome{TargetType, TargetID, Targets, ExpectedRevision, ResultRevision, Before, After, RevertsID, Response}` 在 `:153-162`。
- 流程（`backend/internal/operations/operations.go:196-273`）：
  1. 开 READ COMMITTED 事务，按“操作者 + 幂等键”取事务级 advisory lock（`:198-205`）。
  2. 查同键记录：指纹相同重放已存响应，不同返回 409 `idempotency_conflict`（`:207-219`）。
  3. 预先生成操作 ID，调用执行器完成业务变化（`:221-225`）。
  4. 写入操作记录（含 origin）与艺人目标关联，提交（`:254-272`）。
- 只记录成功提交的操作，被拒绝或失败的尝试不写记录（`backend/internal/operations/operations.go:13`）。
- 调用点共九处：五个发行/专辑 HTTP handler、艺人 HTTP handler、撤销 HTTP handler，以及两个管家工具包各一处（`backend/internal/catalog/httpapi/corrections.go:94`、`group_change.go:57`、`group_default.go:56`、`issue_review.go:63`、`claim.go:64`、`artist_governance.go:145`，`backend/internal/operations/httpapi/handlers.go:219`，`backend/internal/catalog/governance/stewardtools/tools.go:94`，`backend/internal/operations/stewardtools/revert.go:99`）。

### 3.2 操作记录表的字段

`operations`（`backend/migrations/0017_governed_operations.sql:7-32`，`origin` 由 `0054_steward_plans.sql:1` 加入）：

| 关注点 | 字段与约束 |
|---|---|
| 操作者 | `actor_id UUID NOT NULL REFERENCES users(id)` |
| 类型 | `operation_type`，CHECK 允许十种（`backend/migrations/0026_artist_catalog.sql:113-116`） |
| 目标 | `target_type`（release / release_group / artist，`0026:117-118`）、`target_id`（不设外键） |
| 幂等键 | `idempotency_key`、`fingerprint`；唯一约束 `operations_actor_key_uq (actor_id, idempotency_key)` |
| 版本 | `expected_revision`、`result_revision`，CHECK `result = expected + 1` |
| 撤销依据 | `before_state`、`after_state`（执行器定义的类型化 JSON） |
| 撤销关系 | `reverts_operation_id`；CHECK 只有 revert 类型可以非空；唯一约束 `operations_reverts_uq`（一条操作至多被撤销一次） |
| 其他 | `response`、`request_id`、`created_at`、`origin` |

- **没有“可否撤销”这一列。** 可否撤销是读取时算的：`Kind.Describe` 给出 `Revertible`（`backend/internal/operations/kind.go:53`），列表再加上“本身不是撤销、未被撤销过”（`backend/internal/operations/httpapi/handlers.go:160`）。
- 艺人操作的主、次目标另存在 `operations_artist_targets`（`backend/migrations/0026_artist_catalog.sql:119-123`）。
- 记录永久保留，不清理，不提供“回滚到某点”：`.trellis/spec/backend/governance-contracts.md:115`。

### 3.3 全部操作类型与可撤销性

类型常量在 `backend/internal/operations/operations.go:37-46`；Kind 注册在 `backend/internal/app/app.go:144-145`。

| 类型 | 目标 | 可撤销 | 依据 |
|---|---|---|---|
| `release_correction` | 发行 | 是 | `backend/internal/catalog/governance/revert.go:82-145` |
| `release_group_change` | 发行 | 是 | `backend/internal/catalog/governance/revert.go:210-260` |
| `release_group_default` | 专辑 | 是 | `backend/internal/catalog/governance/group_default.go:220-266` |
| `release_issue_review` | 发行 | 是 | `backend/internal/catalog/governance/issue_review.go:246-281` |
| `release_claim` | 发行（新版本） | 是 | `backend/internal/catalog/governance/claim_revert.go:87` |
| `revert` | 同原操作 | 否，`not_revertible` | `backend/internal/operations/kind.go:77-78` |
| `artist_merge` | 艺人 | 否，`not_revertible` | `backend/internal/catalog/governance/artist_describe.go:163-169` |
| `artist_split` | 艺人 | 否，`not_revertible` | `backend/internal/catalog/governance/artist_describe.go:174-180` |
| `artist_rename` | 艺人 | 否，`not_revertible` | `backend/internal/catalog/governance/artist_describe.go:185-191` |
| `artist_candidate_ignore` | 艺人 | 否，`not_revertible` | `backend/internal/catalog/governance/artist_describe.go:196-202` |

四种艺人类型共用 `artistNotRevertible()`，说明文字是“请通过拆开、合并或再次改名恢复”（`backend/internal/catalog/governance/artist_describe.go:159-161`）；它们的 `Describe` 从不设置 `Revertible`，所以列表里恒为假。

撤销的通用守卫在 `RevertExecutor`（`backend/internal/operations/kind.go:67-95`）：先锁原记录；本身是撤销或类型不符返回 `not_revertible`，已被撤销返回 `already_reverted`。各 Kind 再核对“目标当前的 `edit_revision` 等于记录的 `result_revision`”，否则 `revert_conflict`（例：`backend/internal/catalog/governance/revert.go:87-89`）。

### 3.4 有没有按批归组、按批撤销的能力

**没有。**

- `operations` 没有批次列。
- 列表只能按目标（发行、专辑、艺人）和认领来源筛选，不能按操作者、类型、来源、方案或时间段筛选：`backend/internal/operations/records.go:113-121`、`:131-150`。
- 撤销接口与工具一次只接受一个操作 ID：`backend/internal/operations/httpapi/handlers.go:50`、`backend/internal/operations/stewardtools/revert.go:41-42`。
- ADR 明确不支持链式恢复：`docs/architecture/adr/0003-governed-operations.md:165`；合同 `.trellis/spec/backend/governance-contracts.md:132`。
- 最接近“批”的是管家方案：`steward_plans` 与 `steward_plan_items`（`backend/migrations/0054_steward_plans.sql:7-34`），方案项记有 `operation_id`，操作的 `origin.plan_id` 也指回方案。但每个方案项独立进入 `Run`，一项失败不阻止后续项（`.trellis/spec/backend/steward-runtime-contracts.md:286`），也没有按方案撤销的代码。方案随会话删除（`0054:9`）。

### 3.5 操作者是否只能是用户

- 是。`actor_id` 有指向 `users(id)` 的非空外键；`users.role` 只有 admin 与 user（`backend/migrations/0006_multi_user.sql:1`）。`Spec.ActorID` 的注释是“操作者（会话中的用户）”（`backend/internal/operations/operations.go:145`）。
- 管家写入时 `ActorID` 取 `caller.User.ID`，同时填 `Origin`：`backend/internal/catalog/governance/stewardtools/tools.go:94-95`。所以“管家代表某用户”的标记已经存在，就是 `origin`：
  - `source`：chat 或 mcp；`conversation_id`、`plan_id`、`token_id`。
  - `mode`：会话模式 assistant / operator / steward（`backend/internal/steward/runtime/conversations.go:65`）。
  - `review_id`、`review_decision`：由装配层经 context 补入（`backend/internal/operations/review_origin.go:8-11`、`backend/internal/operations/operations.go:241-243`、`backend/internal/app/steward.go:116-118`）。
- `origin` 不参与指纹（`backend/internal/operations/operations.go:130`），同键重放不覆盖原来源（`.trellis/spec/backend/governance-contracts.md:65`）。
- 管家的三种模式决定是否需要确认（`backend/internal/steward/runtime/run.go:473-475`、`backend/internal/steward/runtime/run_tools.go:120-122`）：operator 直接执行；assistant 下可撤销工具命中会话的免确认名单时直接执行，其余合成方案等人确认；steward 模式的方案交给审查流程。

## 四、艺人的合并与拆分

### 4.1 合并为什么不可撤销

- 是决策而不是技术限制的记录：2026-11-03 的轻量标准（R4）规定“有反向操作可以恢复的不提供撤销（`Revert` 返回 `not_revertible`）”，2026-11-04 明确艺人四种操作按此执行，“用正常反向命令恢复旧 ID 而不是新同名 UUID”。依据 `docs/architecture/adr/0003-governed-operations-decisions.md:15-16`、`.trellis/spec/backend/governance-contracts.md:196-197`、`docs/architecture/core-1-roadmap.md:67`。
- 合并记录里其实存了前态：涉及的艺人身份、每个名字键原来的归属与原 operation_id、每条作用域覆盖原来的归属（`backend/internal/catalog/governance/artist_merge.go:32-46`、`:141`；结构 `backend/internal/catalog/governance/artist_state.go:110-133`）。现有代码只把它用于“可恢复来源”的读取（`backend/internal/catalog/governance/merged_sources.go:226-269`），没有用它做撤销。

### 4.2 合并做了什么

`backend/internal/catalog/governance/artist_merge.go`：

1. 把当前解析到来源艺人的全部名字键 upsert 到 `artist_name_bindings`，归属改为目标，`operation_id` 改为本次合并（`:89-99`）。
2. 把来源艺人（以及更早重定向到来源的旧身份）名下的作用域覆盖改归目标（`:100-103`）。
3. 给来源艺人和那些更早的旧身份写重定向，全部直接指向目标（“扁平化”，`:48-77`、`:104-108`）。只改重定向两列，不动 `display_name`。
4. 双方 `edit_revision` 加一（`:109`），刷新派生署名、主名和收藏解析（`:112-118`）。

### 4.3 拆分能否作为反向操作恢复合并前状态

拆分的 `target.kind="restore"` 就是为此设计的（`backend/internal/catalog/governance/artist_split.go:21-41`、`:78-86`）：清除旧身份的重定向，把选中的名字键和作用域覆盖移回旧身份。可恢复的旧身份由 `GET /api/v1/artists/{id}/merged-sources` 列出（`backend/internal/catalog/httpapi/handlers.go:59`、`backend/internal/catalog/governance/merged_sources.go:32-125`）。

从代码能确认的、与“合并前原样”的差别：

| 方面 | 恢复后的情况 | 依据 |
|---|---|---|
| 别名（名字绑定） | 只移回调用方选中的名字键（每次最多 100 个，必填非空），且该键当前全局归属必须仍是合并后的艺人；已被分给第三人的键不会被抢回 | `backend/internal/catalog/governance/artist_requests.go:15`、`:105`，`artist_split_scope.go:130-137`，`merged_sources.go:127-140` |
| 别名（绑定行本身） | 合并前靠初始归属路由、没有绑定行的键，恢复后变成一条显式绑定行；合并前绑定行的原 `operation_id` 被合并覆盖，恢复时写的是新的 operation_id，不回到原值 | `artist_merge.go:94-95`，`artist_split.go:87-96`，`artist_state.go:152-155` |
| 作用域归属 | 按别名恢复时，选中名字键下所有当前解析到合并后艺人的作用域覆盖都移给旧身份，代码不查合并记录来区分哪些原本属于来源、哪些原本就属于目标；未选中名字键的覆盖留在合并后的艺人上 | `artist_split_scope.go:233-267`，`artist_split.go:97` |
| 链式重定向 | 合并把更早的旧身份直接改指目标；恢复只清除被恢复那一个 ID 的重定向，其余仍指向合并后的艺人（可各自再恢复），原来的链不会重建 | `artist_merge.go:48`、`:100-106`，`artist_split.go:79` |
| 收藏 | 收藏保留原锚点 `artist_id`，合并只改解析列 `resolved_artist_id`；恢复时锚在旧 ID 上的收藏重新解析回旧 ID。但合并期间取消收藏会删除所有解析到合并后艺人的锚点行，恢复后不会回来；合并期间新增的收藏锚在合并后的艺人上，恢复后留在那边 | `backend/migrations/0027_artist_favorites.sql:1-8`，`backend/internal/personal/artist_resolution.go:69-71`，`artist_split.go:85`，`backend/internal/personal/artist_favorites.go:71`、`:73` |
| 署名 | 署名事实（`artist_credits`）合并与拆分都不改；有效署名是派生表，随路由重新计算。所以署名是否回到原样取决于上面的绑定与作用域归属是否移回 | `artist_state.go:232-249` |
| 主名 | 来源艺人的人工主名在合并和恢复中都不被修改，恢复后仍在 | `artist_merge.go:104-106`，`artist_split.go:79` |
| 版本号 | 只增不减 | `artist_state.go:250-253` |
| 忽略的候选 | 按名字键对存储，合并与拆分都不写这张表 | `backend/migrations/0026_artist_catalog.sql:103-107` |

另外两点：

- 恢复本身也是一条 `artist_split` 记录，同样不可撤销。
- `target.kind="new"` 会新建一个艺人 UUID（`artist_split.go:60-77`），不是旧身份；锚在旧 ID 上的收藏不会跟过去。

## 五、专辑归组、默认版本、问题核对、认领

| 操作 | 可撤销 | 并发与令牌 | 依据 |
|---|---|---|---|
| 字段修正 | 是，恢复被改字段的前一个意图 | 发行的 `edit_revision`，无令牌 | `backend/internal/catalog/governance/corrections.go:372-441`，`revert.go:82-145` |
| 归入其他专辑 / 移出为独立专辑 | 是，移回原组（组从不删除）；要求当前组仍是记录的后态 | 发行的 `edit_revision`，无令牌 | `group_change.go:99-177`，`revert.go:210-260` |
| 默认版本 | 是，恢复前态的默认版本或自动选择 | 专辑自己的 `edit_revision`，无令牌 | `group_default.go:154-209`、`:220-266` |
| 问题核对 | 是，恢复前态的核对行（含原 operation_id，核对人仍是原来的人） | 发行的 `edit_revision` 加依据令牌（依据 JSONB 文本的 SHA-256）；依据变了返回 `issue_changed` | `issue_review.go:84-89`、`:180-232`、`:246-281` |
| 认领 | 是，一次恢复两个发行的修正、核对、所在组与各组的默认版本；新版本、旧版本和涉及的组任一方之后被改过就整体拒绝，不做部分撤销；不要求新版本可见 | 新旧两个发行的 `edit_revision` 加预览令牌（计划内容的指纹）；令牌不符返回 `claim_changed` | `claim.go:18-24`、`:207`、`:468`、`:506`，`claim_revert.go:16-19`、`:133-150` |
| 艺人拆分（对照） | 否 | 艺人 `edit_revision` 加范围依据 `basis`；不符返回 `artist_scope_changed` | `artist_split.go:29-48` |
| 艺人合并（对照） | 否 | 双方 `edit_revision`，无令牌 | `artist_merge.go:26-31` |

发行的修正、归组、核对共用同一个 `edit_revision`（`backend/migrations/0017_governed_operations.sql:37-38`），所以“只能撤销最近一次修改”是按发行算的，不是按字段算的。

## 六、“需要关注”的问题怎么算

### 6.1 问题类型与数据来源

全部定义在 `backend/internal/catalog/internal/releasesql/issues.go`，读取时用 SQL 实时计算，不物化。

| 类别 | 判定 | 数据来源 | 依据 |
|---|---|---|---|
| 拿不准的字段（fields） | `release_field_decisions.action = 'uncertain_apply'`，且该字段没有修正行（`artist` 看 `album_artist` 的修正）；按字段去重计数 | 扫描的字段决定 | `issues.go:21-28`、`:36` |
| 归组冲突（grouping） | `release_grouping_evidence.reason` 非空；每个发行计 0 或 1 | 扫描的归组证据 | `issues.go:39-41`、`:48` |
| 缺失曲目（missing_tracks） | `tracks.source_status = 'missing'`；按来源身份去重计数 | 完整扫描后的缺失对账 | `issues.go:51-54`、`:63-64` |

- “需要关注”的数字 = 三类待处理数目之和：`backend/internal/catalog/internal/releasesql/release.go:17-18`，随发行摘要一起读出（`release.go:45`）。
- 字段拿不准的原因有三种：`field_missing`、`inconsistent_candidates`、`inferred_value`（`backend/internal/catalog/ingest/organizer.go:111`、`:124-130`、`:848`）。流派恒为 `auto_apply`，不产生问题（`organizer.go:176`、`backend/migrations/0032_release_genres.sql:16-19`）。
- 每类再分“待处理”与“已核对”。核对存在 `release_issue_reviews`，读取时拿存下的依据与现在的依据比较，相同才算有效；依据变了问题重新计入（`issues.go:7-8`、`:30-35`、`:42-47`、`:56-61`）。缺失曲目逐首比较“最后一次被看到的时间”。
- 可以核对的只有四种：`grouping`、`missing_tracks`、`source_type`、`media_type`（`backend/internal/catalog/governance/issue_review.go:22-27`、`backend/migrations/0019_release_issue_reviews.sql:14`）。其余拿不准的字段只能靠修正（含“确认扫描值”）消除。

### 6.2 接口

| 接口 | 作用 | 依据 |
|---|---|---|
| `GET /api/v1/release-issues?status=open\|reviewed&category=fields\|grouping\|missing_tracks` | 有问题的可见版本分页列表，带两种状态下的筛选项计数 | `backend/internal/catalog/httpapi/handlers.go:79`，`backend/internal/catalog/httpapi/issue_review.go:76`，`backend/internal/catalog/governance/issue_read.go:108` |
| `GET /api/v1/releases/{id}/issues` | 一个版本的全部问题明细、依据令牌与核对状态 | `handlers.go:71`，`issue_review.go:110`，`issue_read.go:292` |
| `POST /api/v1/releases/{id}/issue-review` | 核对或取消核对 | `handlers.go:72`，`issue_review.go:16` |
| 管家工具 `list_release_issues`、`get_release_issues` | 同上两个读取 | `backend/internal/catalog/stewardtools/tools.go:119`、`:128` |
| 管家工具 `list_albums` 的 `attention=required` | 只列待关注专辑 | `backend/internal/catalog/stewardtools/tools.go:84-89` |

三个 HTTP 接口都只有管理员可用（`backend/internal/catalog/httpapi/issue_review.go:20`、`:77`、`:111` 处的 `RequireAdmin`；写接口另先过同源检查，`:17`）。

### 6.3 不计入“需要关注”的相邻机制

- 重复艺人候选是另一套：信号表 `artist_candidate_signals`、`artist_alias_signals`（`backend/migrations/0026_artist_catalog.sql:95-101`、`backend/migrations/0031_artist_alias_signals.sql:7-19`），接口 `GET /api/v1/artist-candidates`（`backend/internal/catalog/httpapi/artist_governance.go:39`）。
- 待认领的旧发行由谓词实时算出，接口 `GET /api/v1/release-claims`（`backend/internal/catalog/httpapi/handlers.go:76`）。

## 七、相关的既有外部源代码

- `backend/internal/steward/web/musicbrainz.go:63`（`SearchMusic`）、`:110`（`GetMusic`）是只读的 MusicBrainz 客户端，带封面清单读取（`:177`）。
- 它只以管家工具 `search_music_metadata`、`get_music_metadata` 的形式暴露（`backend/internal/steward/web/tools.go:51`、`:73`），输出声明为外部不可信资料（`tools.go:10`）。合同在 `.trellis/spec/backend/steward-web-contracts.md`。
- ADR 0002 的现有立场：“未来 Steward 只能提交同一种提案，不能成为独立的字段权威”（`docs/architecture/adr/0002-metadata-overlays.md:72`）；“本 ADR 不批准任何外部提供方或标签字段，Core 1 也不引入强证据自动归组”（`:179-180`）。

## 八、离“自动生效、按来源或按批撤销、人工修正优先”还缺什么

只列从代码能确认的缺口。

### 来源与作者

1. **覆盖层的行没有来源或作者类型。** `release_corrections`、四张艺人意图表、`release_issue_reviews` 都只有 `operation_id`（1.5 节）。
2. **`origin` 没有“外部源”的取值，也不受数据库约束。** 代码里 `source` 只有 chat 与 mcp；列是可空 JSONB，没有 CHECK（`backend/migrations/0054_steward_plans.sql:1`）。
3. **操作者必须是用户。** `actor_id` 是指向 `users` 的非空外键，角色只有 admin 与 user，没有系统或服务身份（3.5 节）。
4. **没有不经会话的写入口。** `operations.Run` 的全部调用点都在 HTTP handler 和管家工具里（3.1 节）；没有后台任务调用它。

### 人工优先

5. **修正层内部不分作者，一个字段只有一行。** 主键是 `(release_id, field)`，写入是 upsert（`backend/internal/catalog/governance/corrections.go:492-496`）。执行器只核对版本号，不看现有意图是谁写的（`corrections.go:379-415`）；视图也只判断有没有修正行。所以现在的结构保证只覆盖“扫描不盖修正”，不覆盖“自动写入不盖人工修正”。
6. **被覆盖的旧意图只留在操作记录里。** 前一个意图存在 `operations.before_state`，不在覆盖层表里（`corrections.go:335-340`）。
7. **任何修正行都会消掉字段问题。** 待处理字段的定义不区分修正的作者（`backend/internal/catalog/internal/releasesql/issues.go:24-28`）。

### 撤销

8. **没有批次。** 没有批次列，列表不能按来源、方案、操作者、类型或时间筛选，撤销只接受单个操作 ID（3.4 节）。
9. **只能撤销目标上最近一次修改。** 撤销要求目标的 `edit_revision` 等于记录的 `result_revision`。同一发行上连续两次操作，前一次就撤不了；发行的修正、归组、核对共用一个版本号，所以之后任何一次人工改动（哪怕改的是别的字段）都会让更早的自动修改无法撤销（`backend/internal/catalog/governance/revert.go:87-89`、`backend/migrations/0017_governed_operations.sql:37-38`）。
10. **一条操作至多被撤销一次，撤销不能再撤销，没有链式恢复。**（`backend/migrations/0017_governed_operations.sql:30`、`backend/internal/operations/kind.go:77-78`、`docs/architecture/adr/0003-governed-operations.md:165`）
11. **四种艺人操作没有撤销。** 反向操作“拆分并恢复”不保证回到合并前原样，差别见 4.3 节的表。

### 可修改的范围

12. **可修正字段只有发行级八个。** 没有曲目级字段修正（曲目标题、曲目艺人、曲序等）；署名等集合字段不可编辑；`source_type`、`media_type` 只能核对不能修正（1.4 节）。
13. **自动归组没有写路径。** 归组只有逐个版本的人工操作（join / detach），扫描只给新建发行选组；发行的合并与拆分没有实现（`docs/architecture/adr/0002-metadata-overlays.md:179-181`、`.trellis/spec/backend/governance-contracts.md:195`）。

### 自动生效

14. **“自动执行”现在只存在于管家会话内。** operator 模式直接执行；assistant 模式只有可撤销工具可以进免确认名单，四种艺人工具每次都要确认（`backend/internal/steward/runtime/run.go:473-475`、`backend/internal/catalog/governance/stewardtools/tools.go:65-68`、`.trellis/spec/backend/steward-runtime-contracts.md:273`）。
15. **所有写入口都要求管理员。** HTTP 写接口先过同源与管理员检查；管家写工具的 `MinRole` 是管理员（`backend/internal/catalog/governance/stewardtools/tools.go:80`）。

## Caveats / Not Found

- 未运行任何测试，结论全部来自阅读代码、迁移、ADR 与合同。
- `backend/internal/catalog/governance/claim.go` 只读了文件头、函数清单与关键判断行，`claim_read.go` 只看了注释；认领的逐项去向规则没有逐行核对，细节以 `.trellis/spec/backend/claim-contracts.md` 为准（本次未通读）。
- `.trellis/spec/backend/default-version-contracts.md`、`issue-review-contracts.md`、`steward-review-contracts.md` 未通读，相关结论取自代码。
- 4.3 节“作用域归属”一行是根据拆分计划的查询条件得出的：代码里没有读取合并记录来区分覆盖的原归属。没有找到专门断言这一行为的测试，也没有去找。
- 管家 steward 模式的审查流程（`reviewPlan`）如何决定自动执行或转人工，本次没有展开。
- 前端如何展示来源与撤销入口未调研。
- 工作区里 `docs/architecture/adr/0005-steward-data-and-egress.md` 与 `docs/architecture/core-1-roadmap.md` 有未提交改动，本次没有核对改动内容。
