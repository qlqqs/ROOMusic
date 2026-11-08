# 默认版本与版本对比合同（阶段 M4 第一项）

## 1. 范围与触发

M4 的第一项（2026-10）让管理员为多版本专辑指定**默认版本**，也让所有登录成员**对比**一张专辑的各个版本。新增或修改以下内容时必须阅读并更新本文件：
`release_groups.default_release_id` 与 `release_groups.edit_revision`、代表版本规则里的默认项、组详情的 `revision` 与 `default_release_id`、
设定默认版本的接口与执行器、它的撤销与展示（`governance.GroupDefaultOperations`）、版本对比接口。

- 共同写入流程（幂等、记录、撤销框架、操作记录列表）仍在[受治理操作合同](./governance-contracts.md)；本文件只写默认版本这种操作自己的规则。
- 按组读取的通用规则（可见性、搜索、排序、分页）仍在 [Catalog REST 合同](./catalog-rest-contracts.md)。
- 扫描**必须保持不碰本文件的两列**（第 7 节）。

决策依据：[ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md) 与 [ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md) 的
2026-10-14 决策记录，任务 [M4 默认版本与版本对比](../../tasks/archive/2026-10/10-14-classical-m4-default-version/prd.md)（裁决 K1–K9）。

## 2. 数据模型（迁移 0018）

- **`release_groups.default_release_id`**（`UUID`，可空，外键 `release_groups_default_release_fk` → `releases(id)`，`ON DELETE SET NULL`）：管理员指定的默认版本，
  NULL 表示自动选择。**不约束它属于这个组**：归组会移动版本，而扫描和归组都不改这一列。发行从不删除，`ON DELETE SET NULL` 只是保险。不加索引：都按组的主键读取。
- **`release_groups.edit_revision`**（`BIGINT NOT NULL DEFAULT 1`，约束 `release_groups_edit_revision_ck`：`> 0`）：专辑的人工修改版本号。
  **只有默认版本的设定、清除与撤销递增它**；归组递增的是发行的 `edit_revision`，扫描从不碰组行。默认版本是组的状态，所以不借用发行的版本号：
  否则改了某个版本的标题，就会挡住默认版本的撤销。
- 迁移 0018 同时放宽 `operations` 的两个检查约束（名字不变）：操作类型增加 `release_group_default`，目标类型增加 `release_group`。

## 3. 生效规则与代表版本

- **生效条件**：已存的默认版本可见（至少一条 `present` 曲目）**并且**现在属于这个组时生效。否则**静默失效**：专辑回到自动规则（年份最早），
  不清除这一列；版本回来（文件恢复、撤销移出、被归回）时重新生效。生效条件不另写代码，由排序自然得出：参与排序的只有这个组的可见成员。
- **代表版本规则**：`releasesql.ReleaseGroupMemberOrder`（`backend/internal/catalog/internal/releasesql/release.go`）的第一项是
  `releases.id IS DISTINCT FROM (SELECT default_release_id FROM release_groups WHERE id = releases.group_id)`。默认版本这一项为 false，排在最前；
  其余照旧：年份最早（没有年份排最后）、创建时间、ID。列表排名、组详情的成员顺序、版本对比、`representativeTitle`（归组与默认版本记录的标题快照）
  与 `loadAlbumInfos`（操作记录的专辑标题）共用这一个常量，所以卡片、列表排序、打开专辑时显示的版本、版本导航的顺序一起跟着变，不另写规则。
- **组详情**增加 `revision`（专辑的人工修改版本号，写操作的预期版本）与 `default_release_id`（**生效中的**默认版本，失效或没有设定时为 `null`）。
  判定与成员顺序是同一条规则：已存的值等于排在最前的可见成员时才给出，所以同一份响应里 `default_release_id` 非空时总等于 `releases[0].id`。
  组摘要（列表）不加这两个字段。
- **门禁**：没有设定默认版本时，按组读取的代表版本、顺序与既有接口样本逐项不变（只多出组详情的两个字段）。

## 4. 设定与清除

`POST /api/v1/release-groups/{id}/default`，请求体 `{expected_revision, release_id}`：`release_id` 是 UUID 表示设为默认版本，`null` 表示改回自动选择。

- **检查顺序**：同源（403）→ 管理员（401/403）→ 路由 ID（`400 invalid_id`）→ `Idempotency-Key`（`400 invalid_input`）→ 请求体
  （JSON 非法 `invalid_json`；`expected_revision < 1`，或 `release_id` 缺失、不是字符串、不是 UUID，都是 `400 invalid_input`：漏写不能当成“改回自动选择”）→ `operations.Run`。
- **指纹**：`{type: release_group_default, group_id, expected_revision, release_id | null}`，ID 都转小写（`governance.GroupDefaultRequest.Fingerprint`）。
- **执行器**（`governance.GroupDefaultExecutor`，在 `Run` 的事务里）：
  1. 设为默认时先 `FOR UPDATE` 锁目标发行行（不要求可见），再锁组行；清除时只锁组行。顺序与归组一致（先发行、后组），同时进行不会成环。
  2. 组不存在或没有可见成员：`404 not_found`。
  3. 组的 `edit_revision` 不等于 `expected_revision`：`409 revision_conflict`。
  4. 设为默认时，目标发行必须存在、可见并且 `group_id` 是这个组，否则 `409 not_in_group`。
  5. 请求的值等于**已存的值**：`400 no_changes`。比较的是已存的值而不是生效中的值：已存的值失效时，清除它是有效变化（清掉之后它回来也不会再生效）；
     再把失效中的那个版本设为默认，在第 4 步就是 `409 not_in_group`，到不了这一步。
  6. `UPDATE release_groups SET default_release_id = ?, edit_revision = edit_revision + 1`。
- **记录**：目标是 `release_group` 与组 ID，预期与结果版本是组的版本号。前后状态 `{group_title, default: {release_id, title, year, edition} | null}`：
  `group_title` 是那一刻专辑的标题（代表版本的有效标题）；`default` 是已存默认版本的有效标题、年份与版本说明的**快照**（包括失效中的），
  版本之后改名或变得不可见，记录仍然可读。
- **响应**：`{group_id, default_release_id, revision, operation_id}`，`default_release_id` 是现在**已存**的值；同键同义重放返回同一份响应。

## 5. 撤销与展示

`governance.GroupDefaultOperations()` 实现 `operations.Kind`，由 `app` 注册：

- **Revert**：前态的默认版本不是 null 时先锁那个发行行（它已不存在时 `409 revert_conflict`），再锁组行。组没有可见成员 `404 not_found`；
  组的 `edit_revision` 不等于原记录的 `result_revision` 是 `409 revert_conflict`（只能撤销专辑**最近一次**默认版本调整）。然后把 `default_release_id`
  恢复成前态：那个版本现在失效也照样恢复（恢复的是意图，失效规则照常生效）。版本号加一，写前后状态。撤销记录不能再撤销（通用规则）。
- **Describe**：一条查询读出这些组现在的标题、是否可见（是否有可见成员）与版本号。标题取排在最前的成员（可见的优先，再按代表版本规则），
  专辑已经没有任何成员时用记录里的 `group_title`，记录始终有名字。列表项的 `album` 是 `{id, title, visible}`、`release` 为 `null`；
  `revertible` = 专辑可见 ∧ 版本号等于记录的 `result_revision`（再加通用的“未被撤销、本身不是撤销”）。改动摘要是
  `{kind: "release_group_default", from, to}`，即前后状态；状态损坏时为 `null`。
- 专辑详情的“修改记录”用 `GET /api/v1/operations?release_id=<版本>&group_id=<专辑>`：这个版本的记录加上这张专辑的默认版本记录（并集，合同见
  [受治理操作合同](./governance-contracts.md)第 4.3 节）。

## 6. 版本对比

`GET /api/v1/release-groups/{id}/comparison`：所有登录成员可读；未登录 `401`；ID 不是 UUID `400 invalid_id`；组不存在或没有可见成员 `404 not_found`；
读取失败 `503 database_unavailable`。

- **响应**：`{group_id, default_release_id, release_count, releases: [...]}`。成员是可见版本，顺序与上限和组详情一致（代表版本规则，最多 100 个），
  `release_count` 是完整数量，`default_release_id` 的判定与组详情相同。每个成员：`id, title, artist, album_artist, year, edition, label, catalog_number,
  barcode, genres, source_type, media_type, medium_count, track_count, duration_seconds, formats, corrected_fields`。
  - `genres` 是完整有序列表（无值为 `[]`），顺序或内容不同均算不同，见[多流派合同](./genre-contracts.md)。
  - 文本字段是有效值（经 `release_effective`），空白规范为 `null`；`corrected_fields` 与发行详情相同（固定顺序，没有时是空数组）。
  - `medium_count`、`track_count`、`duration_seconds` 只计 `present` 曲目。`duration_seconds` 是总时长：**只要有一首曲目的时长未知**（零、负数与非有限值都算未知）
    **就是 `null`**，不拿已知的部分冒充总数，与专辑详情头部的规则一致（部分之和会让两个版本的差异看起来是真的）。
  - `formats` 是 `present` 曲目按 `(codec, bit_depth, sample_rate)` 分组的 `[{codec, bit_depth, sample_rate, track_count}]`（未知参数为 `null`），
    按曲目数降序、再按编码、位深、采样率排序；一个版本最多 32 组，超过时整个读取失败，不静默截断。**不做音质判断，也不排名。**
- **查询数量固定**：成员与字段一条（含介质、曲目、时长汇总与成员总数）、格式汇总一条、修正字段一条，后两条按成员 ID 批量，不随版本数增长。
- 不并入组详情：组详情每次打开专辑页都要读，对比只在打开对话框时读。

## 7. 与扫描、归组的关系

- 扫描不碰 `release_groups` 的这两列（新建组时取默认值，之后从不更新组行），所以重扫后默认版本保持，这是结构保证
  （`TestPostgreSQLDefaultVersionSurvivesARealRescan` 与真实服务用例守住）。
- 同目录按格式切分时，扫描新建的发行可以加入同一 `VersionGroup` 的已有组：优先使用已规划沿用发行中候选键最小者的组，否则使用本批第一个成功写入发行的组。仅新建发行设置 `group_id`，不移动已有发行；即使两个沿用版本已被人工分到不同组也保持原样。组的两列不变，新增成员仍按现有代表版本规则参与排序，不按格式挑选默认版本。
- 归组不改组的默认版本与版本号：把默认版本移出后原专辑回到自动规则；撤销这次移出，它回到原专辑并重新成为默认版本。
- 整张替换后新建的发行不继承默认版本（与 M3 的 G8 一致）：旧的默认版本失效，专辑回到自动规则。M4 第三项起可以认领：新版本进入那张专辑时接任默认版本，否则那张专辑改回自动选择（[认领合同](./claim-contracts.md)）。
- 并发：设定先锁发行行再锁组行，归组也是先发行后组（组行按 UUID 顺序），扫描反馈按既有发行升序锁后取组升序共享锁；扫描新建版本引用已有组只取外键共享锁，不更新组行，不持组补既有发行锁（详见[扫描反馈合同](./scan-feedback-contracts.md)）。

## 8. 错误码（新增）

| 条件 | 响应 |
| --- | --- |
| 设为默认的版本不在这张专辑里、不可见或不存在 | `409 not_in_group` |
| 组不存在或没有可见成员（设定、撤销、对比） | `404 not_found` |
| 请求的值等于已存的值（比较已存的值，不是生效中的值） | `400 no_changes` |
| 专辑的版本号过期 | `409 revision_conflict` |
| 撤销时专辑之后又有调整，或前态的版本已不存在 | `409 revert_conflict` |
| 操作记录列表的 `group_id` 不是 UUID | `400 invalid_id` |

其余错误（401、403、400、`409 idempotency_conflict`、`503 database_unavailable`、`500 operation_failed`、撤销的其他 409）与[受治理操作合同](./governance-contracts.md)第 8 节相同。

## 9. 必需测试与断言点

- `internal/catalog`：`governance/group_default_integration_test.go`——设定后代表版本、列表排序、组详情的成员顺序与标题快照都跟着变，没有设定时逐项不变；
  失效（不可见、被移出）与回来重新生效；钉住自动选出的版本、清除失效中的默认版本；`not_in_group`、`no_changes`、过期版本与 404 都没有副作用；
  撤销恢复每种前态（含失效中的前态）、之后又有调整时 `revert_conflict`、同键重放；`Describe` 的专辑、改动与可撤销；
  设定与归组同时进行不死锁、对同一个版本的设定与移出串行。`comparison_integration_test.go`——有效值与 `corrected_fields`、只计 present 曲目、
  未知时长、格式分组与排序与 32 组上限、成员顺序与默认版本、100 个上限与固定查询数、404。`governance/group_default_test.go`：指纹。
- `internal/operations`：`group_target_integration_test.go`——迁移后的约束拒绝未知的类型与目标类型、并集筛选（分页与总数）、撤销响应的目标。
- `internal/app`：`group_default_api_integration_test.go`——401、跨源与非管理员 403、各种 400 都没有副作用；设定、清除、撤销与读取；同键重放与同键异义；
  并发请求；**合成素材真实重扫后默认版本保持**。`comparison_api_integration_test.go`——权限（普通成员可读、未登录 401）、错误与读取。
  接口样本：`group-defaults/set.json`、`release-groups/get-default.json`、`release-groups/comparison.json`、`operations/list-by-group.json`、
  `operations/reverted-default.json`、`errors/not-in-group.json`、`errors/revision-conflict-group.json`。
- 真实服务：`frontend/e2e/real/default-version.real.ts`（见[前端验证分层](../frontend/quality-verification-layers.md)）。
- 运行 `go test -race -count=1 ./internal/operations/...` 与 `go test -count=1 ./internal/catalog/... ./internal/app -run 'Default|Comparison|Group'`
  （PostgreSQL 集成需隔离 PostgreSQL 18，见[质量规范](./quality-guidelines.md)；用例名不一定含 `Group`，过滤条件不能只写它）。

## 10. 边界与已知限制

- 失效中的默认版本在页面上看不出来（专辑显示为自动选择），也不能单独清除（只能“设为默认版本”换成别的版本，或撤销）；它回来时会重新生效。
- 版本对比只比较发行级字段与格式汇总，不做曲目逐首对应，也不提供“切换播放版本”（需要另外的数据合同，见 [DESIGN-components.md](../../../frontend/DESIGN-components.md) 第 6 节：不按数组下标认定为同一首录音）。
- 默认版本对所有成员都一样；个人默认版本或偏好属于 M5。不按格式参数自动挑选“最高音质”。
- 回退：关闭两个新路由（设定默认版本、版本对比），保留数据与记录；代表版本规则里的默认项可以保留，没有新的设定就不起作用。不写 down 迁移。
- M4 的其余各项分别立项：问题处理已作为第二项实施（[核对合同](./issue-review-contracts.md)），认领已作为第三项实施（[认领合同](./claim-contracts.md)），扫描反馈、Release 合并与拆分未立项。
