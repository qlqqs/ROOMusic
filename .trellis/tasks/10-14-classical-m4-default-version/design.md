# M4 默认版本与版本对比：技术设计

需求与裁决见 [prd.md](prd.md)，执行顺序见 [implement.md](implement.md)。

## 1. 边界与能力归属

- **catalog** 拥有默认版本（`release_groups` 的新列）与版本对比的读取。默认版本的写入走 M3 的共同写入流程 `operations.Run`：
  新增操作类型 `release_group_default` 与目标类型 `release_group`，撤销与展示由 catalog 实现的 `operations.Kind` 提供，由 `app` 注册。
- **operations** 扩展为支持“组”这种目标：目标类型常量、记录展示里的专辑信息、撤销响应的目标、列表按组筛选。它仍不导入任何业务包。
- **扫描不变**：`persist.go` 不改。扫描新建组时新列取默认值，之后从不更新组行（第 8 节）。
- **前端**：catalog 只读（默认版本标记、版本对比对话框、地址规范化暂缓）；governance 负责写（设为默认版本、改回自动选择、记录与撤销）；
  app 负责组合（把组的版本号与默认版本交给操作区，命令进行中时让详情页暂缓地址规范化）。catalog 仍不导入 governance。

## 2. 数据模型（迁移 0018）

```sql
ALTER TABLE release_groups
    ADD COLUMN default_release_id UUID CONSTRAINT release_groups_default_release_fk REFERENCES releases(id) ON DELETE SET NULL,
    ADD COLUMN edit_revision BIGINT NOT NULL DEFAULT 1 CONSTRAINT release_groups_edit_revision_ck CHECK (edit_revision > 0);
ALTER TABLE operations DROP CONSTRAINT operations_type_ck, ADD CONSTRAINT operations_type_ck
    CHECK (operation_type IN ('release_correction', 'release_group_change', 'release_group_default', 'revert'));
ALTER TABLE operations DROP CONSTRAINT operations_target_type_ck, ADD CONSTRAINT operations_target_type_ck
    CHECK (target_type IN ('release', 'release_group'));
```

- `default_release_id`：管理员指定的默认版本，NULL 表示自动选择。**不约束它属于这个组**：归组会移动版本，而扫描和归组都不改这一列。
  它只在“这个发行可见并且现在属于这个组”时生效（第 3 节），否则静默失效、回到自动规则；版本回来（文件恢复、撤销移出、被归回）就重新生效。
- `edit_revision`：组的人工修改版本号。只有默认版本的设定、清除和撤销会递增它；归组改的是发行的版本号，不动它；扫描也不碰。
- 发行从不删除（非测试代码里没有 `DELETE FROM releases`），`ON DELETE SET NULL` 只是保险。不加索引：都按组的主键读取。

## 3. 代表版本规则

- `releaseGroupMemberOrder`（`backend/internal/catalog/groups.go:21-23`）前面加一项：
  `(releases.id IS DISTINCT FROM (SELECT g.default_release_id FROM release_groups g WHERE g.id = releases.group_id))`。
  默认版本这一项为 false，排在最前；其余仍按年份最早（没有年份排最后）、创建时间、ID。
- 列表排名、组详情的成员顺序与 `representativeTitle` 已经共用这一个常量。所以专辑卡片、列表排序、详情页默认显示的版本、版本导航的顺序，
  以及归组对话框和归组记录里的标题快照都会一起跟着变，不另写规则。
- 生效条件不需要另写：参与排序的只有这个组的可见成员。默认版本不可见或不在组里时没有行能匹配，结果就是自动规则。
- 组详情增加 `revision`（组的人工修改版本号）和 `default_release_id`（**生效中的**默认版本；失效或没有设定时为 null）。组摘要（列表）不加这两个字段。
- 门禁：没有设定默认版本时，输出与现在逐项相同。现有的按组读取测试和接口样本不用改就能通过（新增的两个字段除外）。

## 4. 设定与清除默认版本

- `POST /api/v1/release-groups/{id}/default`，请求体 `{expected_revision, release_id}`：`release_id` 是 UUID 表示设为默认版本，null 表示改回自动选择。
  检查顺序与 M3 相同：同源（403）→ 管理员（401/403）→ 路由 ID（`400 invalid_id`）→ `Idempotency-Key`（`400 invalid_input`）
  → 请求体（`expected_revision < 1`、`release_id` 既不是 UUID 也不是 null，都是 `400 invalid_input`；JSON 非法是 `invalid_json`）→ `Run`。
- 指纹：`{type: release_group_default, group_id, expected_revision, release_id | null}`（ID 都转小写）。
- 执行器（在 `Run` 的事务里）：
  1. 设为默认时先锁目标发行行，再锁组行；清除时只锁组行。锁的顺序与归组一致（先发行、后组），所以不会死锁。
  2. 组不存在或没有可见成员：`404 not_found`。
  3. 组的 `edit_revision` 不等于 `expected_revision`：`409 revision_conflict`。
  4. 设为默认时，目标发行必须可见，并且 `group_id` 是这个组，否则 `409 not_in_group`。
  5. 请求的值等于**已存的值**时返回 `400 no_changes`；已存的值包括失效中的那个。清除一个失效中的默认版本算作有效变化：清掉之后它回来也不会再生效。
  6. 执行 `UPDATE release_groups SET default_release_id = ?, edit_revision = edit_revision + 1`。
- 记录：目标是 `release_group` 和组 ID，预期版本与结果版本都用组的版本号。前后状态是 `{group_title, default: {release_id, title, year, edition} | null}`：
  - `group_title` 是改动之前这张专辑的标题，即代表版本的有效标题；
  - `default` 是当时默认版本的有效标题、年份与版本说明的快照。版本之后可能改名或变得不可见，记录仍要能读。
- 响应：`{group_id, default_release_id, revision, operation_id}`；同键同义重放返回同一份响应。

## 5. 撤销与操作记录

- catalog 新增 `GroupDefaultOperations()`，实现 `operations.Kind`：
  - **Revert**：
    - 加锁：前态的默认版本不是 null 时，先锁那个发行行，再锁组行。
    - 拒绝：组没有可见成员返回 `404 not_found`；组的 `edit_revision` 不等于原记录的 `result_revision` 返回 `409 revert_conflict`。
    - 恢复：把 `default_release_id` 恢复成前态。那个版本现在失效也照样恢复，恢复的是意图，失效规则照常生效。之后版本号加一，记下前后状态。
  - **Describe**：用一条查询读出这些组现在的标题（代表版本的有效标题）、是否可见（是否有可见成员）和版本号。
    `Revertible` 等于“可见并且版本号等于记录的 `result_revision`”。改动摘要是 `{kind: "release_group_default", from, to}`，即前后状态。
- operations 包：
  - 新增常量 `TypeReleaseGroupDefault`、`TargetReleaseGroup`。
  - `Description` 增加 `Album *AlbumInfo`（`{id, title, visible}`，组目标的记录用它）。列表项相应增加 `album`：
    发行目标的记录 `release` 非 null、`album` 为 null；组目标的记录反过来。
  - `RevertResult` 改成 `{operation_id, reverts_operation_id, target_type, target_id, revision}`。原来的 `release_id` 只适用于发行，前端也没有读它；开发阶段不做兼容。
  - `List` 支持按组筛选。`release_id` 和 `group_id` 都给时取并集，即这个版本的记录加上这张专辑的默认版本记录；只给一个时就只取那一种。
    `group_id` 不是 UUID 返回 `400 invalid_id`。
- 列表里“专辑已不可见”的判断，对组目标的记录看 `album.visible`。

## 6. 版本对比

- `GET /api/v1/release-groups/{id}/comparison`。所有登录成员都能读；未登录 `401`；ID 不是 UUID `400 invalid_id`；
  组不存在或没有可见成员 `404 not_found`；查询失败按组读取的现有约定返回 `503`。
- 响应 `{group_id, default_release_id, release_count, releases: [...]}`。成员按代表版本规则排序（默认版本在前），最多 100 个，与组详情一致。每个成员包括：
  `id, title, artist, album_artist, year, edition, label, catalog_number, barcode, genre, source_type, media_type,
  medium_count, track_count, duration_seconds, formats, corrected_fields`。
  - 文本字段都是有效值（经 `release_effective`）；`corrected_fields` 与发行详情相同。
  - `medium_count`、`track_count`、`duration_seconds` 只计 present 曲目。`duration_seconds` 是已知时长之和，全部未知时为 null。
  - `formats` 是 present 曲目按 `(codec, bit_depth, sample_rate)` 分组的 `[{codec, bit_depth, sample_rate, track_count}]`，
    按曲目数降序、再按编码排序。不做“音质”判断，也不给出排名。
- 查询数量固定，不逐个成员查询：成员与字段一条、格式汇总一条、修正字段一条，后两条按成员 ID 批量。
- 不并入组详情：组详情每次打开专辑页都要读，对比只在打开对话框时读。

## 7. 前端

### catalog（只读）

- 解码组详情的 `revision`、`default_release_id` 和对比响应；查询键 `catalogKeys.comparison(generation, groupId)` 放在 catalog 的 `all` 前缀下，写操作之后随目录一起失效。
- 版本导航：生效中的默认版本带“默认版本”标签，所有成员都看得到；自动选择时不加标签，与现在一致。
  导航标题旁有“对比版本”按钮，多于一个版本时才出现，所有成员可用。
- 版本对比对话框用 `ModalDialog`。尺寸按 DESIGN：`min(980px,100%)`，最大 `90vh`；窄屏时内部滚动，关闭按钮始终可见，关闭后焦点回到按钮。
  - **表格**：行标题是字段，列是各个版本。列头写“版本 N”，与版本导航一致，默认版本带标签；列头还有“查看这个版本”链接，点击后关闭对话框并切换到该版本。
  - **行**：标题、专辑艺人只在各版本不同时出现；其余各行是年份、版本说明、厂牌、编号、条码、流派、来源类型、介质类型、
    介质与曲目（如“2 张介质 · 24 首曲目”）、总时长、音频格式。所有版本都没有值的行不出现，个别版本没有值的格写“—”。
  - **差异**：以第一列（默认版本，或自动选出的代表版本）为基准，与它不同的格加金色下划线，并带读屏文字“（与版本 1 不同）”，不只靠颜色区分。
  - **人工修正**：有人工修正的值带“人工修正”标记，与侧栏“此版本”一致。
  - **音频格式**：写具体参数，如“FLAC · 24 bit / 96 kHz”；有多种格式时逐项列出，并注明各自的曲目数。不写“最高音质”之类的结论。
  - **数量上限**：版本超过 100 个时说明只比较前 100 个，与版本导航一致。
- **地址规范化暂缓**：`AlbumDetailView` 新增一个由页面传入的开关，命令进行中时为真，包括命令之后的刷新。
  - 为什么要暂缓：设定或清除默认版本会换掉代表版本。命令会先把地址带到 `?release=<当前版本>`，再刷新数据。刷新完成之前，
    缓存里的旧代表版本会让规范化把这个参数去掉，刷新后页面就跳到了别的版本。所以等刷新完成，再按新数据规范化。
  - M3 归组后的跳转可能有同样的问题（目标专辑已有旧缓存时），实施时先用测试确认现状。

### governance（写）

- **命令**：`ReleaseIntent` 增加 `{kind: "default"; release: ReleaseTarget; group: {id, revision}; target: string | null}`。
  `release` 是页面上正在看的版本；`target` 等于它的 ID 表示“设为默认版本”，为 null 表示“改回自动选择”。结果是 `{kind: "default"; result}`。
  成功之后、刷新之前调用 `onMoved(group.id, release.id)`，让页面继续显示这个版本；它不再是代表版本时，地址会带上 `release`。
- **头部操作区**（组里多于一个版本时才有这一组按钮）：
  - 这个版本不是生效中的默认版本时显示“设为默认版本”。自动选出的代表版本也显示：设定之后，以后归入更早的版本也不会把它挤下去。
  - 这个版本是默认版本时显示“改回自动选择”。
  - 两者都不弹确认对话框（随时可以撤销，也不丢数据）；结果提示就近显示，带“撤销”。
- **提示文案**：
  - 设为默认版本：“已把这个版本设为默认版本。专辑列表和打开这张专辑时会先显示它。”
  - 改回自动选择：“已改回自动选择：这张专辑默认显示年份最早的版本。”
  - 撤销之后：“已撤销默认版本的调整。”
- **移出为独立专辑**：要移出的是生效中的默认版本时，确认对话框多写一句：
  “它是这张专辑的默认版本；移出后专辑改按自动规则显示。它以后回到这张专辑时，会重新成为默认版本。”
- **操作记录**：
  - 类型名“设定默认版本”，撤销记录叫“撤销默认版本调整”。
  - 改动写成“默认版本：自动选择 → 《标题》（2015 · Remaster）”。版本标签取快照里的标题、年份和版本说明。
  - “专辑”列：组目标的记录链接到 `/albums/{组 ID}`；专辑已不可见时只写标题，加“（已不可见）”。
  - 撤销前的确认对话框同样前后互换，写出“会改回什么”。
- **“修改记录”（专辑详情）**：请求 `release_id=<版本>&group_id=<专辑>`，同时列出这个版本的记录和这张专辑的默认版本记录。
  “全部记录”链接带同样两个参数；操作记录页的地址参数相应增加 `group`。
- **撤销专辑级记录**：在专辑详情撤销时，命令里的版本取页面当前显示的版本，`returnGroupId` 是这张专辑，所以页面继续显示当前版本。
  操作记录页没有“当前版本”：提示不按版本过滤，也不跟随导航。

### app

- 页面把组详情的 `revision`、`default_release_id` 经 `renderAdminActions` 的上下文交给操作区。
- 两个命令状态（头部操作区、“修改记录”）任一处于进行中时，打开详情页的规范化暂缓开关。

## 8. 与扫描、归组的关系

- 扫描不碰 `release_groups` 的新列和 `edit_revision`（新建组时取默认值），所以重扫后默认版本保持不变。这一点由真实重扫用例守住。
- 归组不改组的默认版本和版本号：
  - 把默认版本移出后，原专辑回到自动规则；
  - 撤销这次移出，版本回到原专辑，重新成为默认版本。
- 整张替换后新建的发行不继承默认版本，与 M3 的 G8 一致：旧的默认版本失效，专辑回到自动规则。

## 9. 错误码（新增与沿用）

| 条件 | 响应 |
| --- | --- |
| 设为默认的版本不在这张专辑里，或不可见 | `409 not_in_group` |
| 组不存在或没有可见成员（设定、撤销、对比） | `404 not_found` |
| 请求的值等于已存的值 | `400 no_changes` |
| 组的版本号过期 | `409 revision_conflict` |
| 组筛选的 ID 不是 UUID | `400 invalid_id` |

其余错误（401、403、400、`409 idempotency_conflict`、`503 database_unavailable`、`500 operation_failed`，以及撤销的各种 409）与 M3 相同。

## 10. 迁移、兼容与回退

- 迁移 0018 只向前，不改 0017。开发阶段不做兼容：撤销响应和操作记录列表项的形状改变，前端解码器与接口样本同步更新。
- 回退：关闭两个新路由（默认版本写入、对比读取），保留数据和记录。代表版本规则里的默认项可以保留，没有新的设定就不起作用。不写 down 迁移。

## 11. 取舍

- **默认版本存在组上，而不是在发行上加标记**：一个组至多一个默认版本由结构保证；版本被移走时也不用改 M3 的归组执行器。代价是需要第 2 节的失效规则。
- **失效而不清除**：要清除，就得让扫描或归组去改组行。前者违反“扫描不碰受治理状态”，后者让归组的撤销变复杂。失效规则可以直接从读取得出。
  代价是被移走的默认版本回来时会重新生效，所以在移出确认对话框里写明。
- **组有自己的版本号**：默认版本是组的状态，与某个发行的修改无关。如果借用发行的版本号，改了这个版本的标题就会挡住默认版本的撤销。
- **对比用独立接口**：一次取回格式汇总，不用为每个版本各读一次详情；也不加重每次打开专辑页都要读的组详情。
- **设定默认版本不弹确认对话框**：改动随时可以撤销，也不丢数据。“移出”不同，它需要先确认影响范围。

## 12. 已知限制

- 失效中的默认版本在页面上看不出来（专辑显示为自动选择），也不能单独清除；它回来时会重新生效。
- 版本对比只比较发行级字段和格式汇总，不做曲目的逐首对应（DESIGN 第 291 行）。
- 默认版本对所有成员都一样；个人偏好属于 M5。
