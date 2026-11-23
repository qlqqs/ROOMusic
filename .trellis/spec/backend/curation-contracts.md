# 自动整理合同

## 1. 范围与触发

M7 子任务 A（2026-11）给后台自动整理准备好写入、记录与撤销的地基：发行字段的**自动层**、来源与批次、按“这个值是否仍是这条操作写的”
判断的撤销、“不再提出”标记与默认关闭的总开关。本任务**没有任何自动写入的调用方**（外部源、匹配与模型后台运行在后面的子任务），
也没有界面；没打开开关的实例行为与之前相同。

新增或修改以下内容时必须阅读并更新本文件：`release_auto_values`、`releases.auto_revision`、视图 `release_effective` 的三层取值、
操作类型 `release_auto_correction`、`operations.batch_id` 与来源 `curation`、表 `curation_batches`、`curation_suppressions`、
`curation_settings`、包 `backend/internal/curation` 与它的管理员接口。共同写入流程、人工修正与操作记录列表的通用规则仍在
[受治理操作合同](./governance-contracts.md)。

子任务 B1 在这个包里加了三样（第 11、12 节）：各外部源的开关、后台取得外部源能力的闸门、外部对应的存储。
子任务 B2 加了外部封面与外部歌词的存放、写入与撤销（第 14 节），闸门多了封面与歌词两种能力。
子任务 C 加了第一个生产调用方：程序层的后台循环、外部对应与强证据归组两个受治理写入口、暂停与进度（第 15 节）。
外部源本身（怎样发请求）见[外部源合同](./sources-contracts.md)。

决策依据：[ADR 0006](../../../docs/architecture/adr/0006-auto-curation-and-sources.md) 第 4、5、8、10 节。

## 2. 数据模型（迁移 0058）

- **`release_auto_values`**：自动层的当前意图，主键 `(release_id, field)`，形状与约束逐条照搬 `release_corrections`
  （八个字段、`set` 或 `null`、标题与专辑艺人不能设空、取值的四种合法组合）。`operation_id` 是写下这个值的操作，延迟外键。
- **`releases.auto_revision`**（`BIGINT NOT NULL DEFAULT 1`）：自动层的版本号，每次自动写入与它的撤销加一。它只用来满足
  `operations_revision_ck` 并给记录排序，**不参与任何判断**；扫描不写它，视图不输出它。
- **`release_effective`**：在八个修正连接之后再连接八次 `release_auto_values`。每个字段取 人工修正 → 自动层 → 扫描值 里第一个存在的；
  修正或自动层明确设空时结果为空，不再往下取。展示艺人 `artist` 跟随专辑艺人的修正与自动值。列的名字、顺序与类型不变。
- **`operations`**：`operations_type_ck` 增加 `release_auto_correction`；新列 `batch_id UUID`（可空，引用 `curation_batches`），
  部分索引 `operations_batch_idx (batch_id, created_at DESC, id DESC) WHERE batch_id IS NOT NULL`；按来源筛选用表达式部分索引
  `operations_provider_idx ((origin->>'provider'), created_at DESC, id DESC) WHERE origin->>'source' = 'curation'`。
- **`artist_credits.layer`** 与 **`artist_names.origin`** 增加 `auto`。
- **`curation_batches`**：`id`、`tier`（`program | model`）、`opened_at`、`closed_at`（可空）、`actor_id`（开批次时的开启者）。
  改动数量不存列，读取时按 `operations.batch_id` 统计。
- **`curation_suppressions`**：`id`、`target_type`、`target_id`、`subject`、`value_hash`、`value`（JSONB，展示用）、`operation_id`
  （产生它的撤销操作，可空，延迟外键）、`created_by`、`created_at`；唯一 `(target_type, target_id, subject, value_hash)`。
  目前只有 `target_type='release'`、`subject=字段名`。
- **`curation_settings`**：单行表（`id` 恒为 1），`enabled`（默认 false）、`enabled_by`（可空）、`enabled_at`、`updated_at`。
- **回退是有损的**：回退脚本丢弃自动值、不再提出标记、设置、批次、`release_auto_correction` 类型的操作及指向它们的撤销记录，
  删除 `layer='auto'` 的署名事实，把 `origin='auto'` 的名字改回 `scan`。原来专辑艺人取自动层的发行，其有效署名与搜索值不会因为
  重启而刷新（投影的代次与输入版本没有变）：**回退之后做一次完整重扫**；对该发行的任何一次人工修改也会刷新它。

## 3. 结构隔离

三层互不读写对方的表与版本号，这是结构保证，测试用断言固定：

- 自动层的执行器与撤销只写 `release_auto_values` 与 `releases.auto_revision`，不写 `release_corrections` 与 `edit_revision`；
  它们读视图只为在记录里保存“当时的有效值”，撤销响应里的 `revision` 读的是没有变化的 `edit_revision`。
- 人工修正的执行器与撤销只写 `release_corrections` 与 `edit_revision`；之后的自动写入不妨碍撤销更早的人工修改。
- 扫描的写入语句不涉及新表与新列；重扫之后自动值保持。
- 认领不搬自动层：旧发行上的自动值留在旧发行上。

## 4. 写入 `curation.Writer.Apply`

只供进程内调用，**没有 HTTP 写入口**。

```go
type Source struct {
    Kind     string // "source" 或 "model"
    Provider string // ^[a-z0-9._:-]{1,64}$
    Basis    string // 依据的摘要，至多 500 字节；展示时按不可信文本处理
}
type Request struct {
    ReleaseID string
    Fields    map[string]catalog.CorrectionIntent // 至少一项；只能 set 或 null，校验与人工修正相同
    Source    Source
    BatchID   string // 可空
    Key       string // 幂等键（UUID），由调用方按自己的工作单元确定
    RequestID string
}
type Applied struct {
    OperationID string
    Written     []string          // 实际写入的字段
    Skipped     map[string]string // 字段 → suppressed | unchanged
}
```

一次 `Apply` 是一次 `operations.Run`，事务内的顺序也是加锁顺序：

1. `Run` 的幂等锁（操作者加键）。操作者是 `curation_settings.enabled_by`，在事务外先读一次作为 `Spec.ActorID`。
2. 目录写锁，再锁发行行（`governance.LockAutoRelease`）。发行不存在或不可见 `not_found`。
3. 核对设置（`FOR SHARE`）：`enabled` 为真，且 `enabled_by` 是启用状态的管理员（`identity.IsEnabledAdmin`），并且与第 1 步读到的相同；
   否则 `409 curation_disabled`。
4. 批次（如有）必须存在且未结束，否则 `409 batch_closed`。
5. 逐字段过滤：命中“不再提出”的记 `suppressed`；与现有自动值相同的记 `unchanged`。没有剩下的字段 `409 nothing_to_apply`，
   不产生记录（返回的 `Applied.Skipped` 仍说明每个字段的原因）。
6. 写自动层（`operation_id` 为本次操作）、`auto_revision` 加一，调用与人工修正相同的派生刷新（艺人投影与搜索值）。
7. 记录：类型 `release_auto_correction`，目标是发行，版本号用 `auto_revision`；前态记每个被写字段之前的自动值**及写入它的操作**，
   后态记新的自动值与写入者，两边都带当时的有效值；`origin = {source:"curation", provider_kind, provider, basis}`；`batch_id` 写入列。

- 指纹含操作类型、发行、各字段的规范化意图与 `provider`，不含 `basis`。同键同内容重放返回原结果。
- “同一个值”由 `governance.AutoValueKey` 判定：规范化意图的 JSON 及其 SHA-256；`unchanged` 的比较与 `value_hash` 用同一个函数。
- 人工修正存在时照样可以写自动层（值不生效，清除人工修正后才显示）。自动值等于当前扫描值也算有效写入。

## 5. 撤销

### 5.1 单条

`release_auto_correction` 注册自己的 `Kind`，走 `POST /api/v1/operations/{id}/revert`，通用守卫不变（已撤销、撤销不能再撤销）。

- 取目录写锁并锁发行行，**不要求发行可见**。
- 对原操作写过的每个字段，核对自动层这一格的 `operation_id` 仍是原操作。全部是：逐格恢复成前态记的自动值与**原来的写入者**
  （前态没有值就删除这一格），`auto_revision` 加一，刷新派生，并为每个被撤的值写“不再提出”（重复的忽略）。
  任何一格不是：整条拒绝 `409 revert_conflict`，说明里带改写它的操作 ID；不做部分撤销。
- 不读 `edit_revision` 作判断，所以之后的人工修改不妨碍撤销；恢复写入者使得“先撤后写的、再撤先写的”成立。
- 撤销响应的 `revision` 是发行当前的 `edit_revision`（`operations.Outcome.ResponseRevision`；其余类型为 0，沿用记录的后一版本）。
- `Describe`：`changes = {kind:"release_auto_correction", fields:[{field, before:{auto, effective}, after:{auto, effective}}]}`，
  `auto` 是 `{mode, value?, operation_id}` 或 null；`revertible` = 未被撤销 ∧ 本身不是撤销 ∧ 每一格仍是这条操作写的（与发行是否可见无关）。
- “不再提出”的写入经 `governance.AutoSuppressionWriter`：`governance` 定义接口、`curation` 实现、`app` 注入
  （`governance.AutoCorrectionOperations(writer)`，缺了在装配时失败）。标记的 `created_by` 是执行撤销的人（`operations.ActorID(ctx)`）。

### 5.2 按批、按来源

`POST /api/v1/admin/curation/reverts`，见第 7 节。每次调用处理至多 100 条：

- 候选是范围内不是撤销、还没被撤销的操作，按 `(created_at, id)` 倒序，从游标之后开始；倒序保证同一格上后写的先撤。
  按批次的查询只用 `batch_id`（只有自动整理的操作才有批次），走 `operations_batch_idx`；按来源的查询用
  `origin->>'source' = 'curation'` 与 `(origin->>'provider')`，走 `operations_provider_idx`。候选不按操作类型取，以后的结构类自动操作不用改这里。
- 逐条 `operations.Run(撤销)`，每条一个事务、独立成败；操作者是发起请求的管理员。每条的幂等键由请求的键与操作 ID 派生（UUIDv5），
  同时到达的同键请求由 `Run` 的幂等锁串行并重放。
- **同一请求重试不产生新的撤销记录**：已经撤销的操作不再是候选。返回的 `reverted` 只统计这次调用实际撤销的条数，
  总数看批次详情的 `operations` 与 `reverted`。
- 它没有自己的状态表：每条撤销本身是一条受治理的操作记录。

## 6. 不再提出

- 写入：自动修改被撤销时（单条或成批）。键是（发行、字段、值的哈希）。
- 读取：`Apply` 第 5 步。只挡同一个值；不同的值照常写入。
- 管理员可以列出与逐条清除；清除后同一个值可以再次自动写入。

## 7. 接口

全部要求管理员；写接口先做同源检查。

| 接口 | 说明 |
| --- | --- |
| `GET /api/v1/admin/curation/settings` | `{enabled, enabled_by:{id, username}\|null, enabled_at, active, inactive_reason}`；`active` 是“现在自动写入会不会被接受”，`inactive_reason` 为 `disabled`、`enabler_unavailable` 或 null |
| `PUT /api/v1/admin/curation/settings` | `{enabled}`。打开时 `enabled_by` 记为调用者；关闭时保留 `enabled_by` 作为记录。关闭不动已有的自动层。返回同上 |
| `GET /api/v1/admin/curation/batches?page=&page_size=` | 倒序分页（默认 50、最大 100）；每项 `{id, tier, opened_at, closed_at, operations, reverted}` |
| `GET /api/v1/admin/curation/batches/{id}` | 单个批次；改动明细用操作记录列表按 `batch_id` 取 |
| `POST /api/v1/admin/curation/reverts` | 需要 `Idempotency-Key`。`{scope:{kind:"batch", id} \| {kind:"provider", provider}, cursor}` → `{reverted, skipped:[{operation_id, code}], next_cursor}`；`next_cursor` 为 null 表示做完，客户端带着它和同一个键继续调用 |
| `GET /api/v1/admin/curation/suppressions?release_id=&page=&page_size=` | 倒序分页；每项 `{id, target_type, target_id, subject, value_hash, value, operation_id, created_by, created_at}`；`value` 按不可信文本显示 |
| `DELETE /api/v1/admin/curation/suppressions/{id}` | 成功返回 `204`，标记不存在返回 `404 not_found`。清除封面或歌词的任一标记时，同一事务内清除同一目标且 `operation_id` 相同、非空的对应标记（见第 14.5 节）。其余主题仅清除指定标记 |
| `GET /api/v1/admin/curation/sources` | `{items:[{id, name, capabilities, enabled, active, inactive_reason}]}`，按注册顺序；见第 11 节 |
| `PUT /api/v1/admin/curation/sources/{id}` | `{enabled}`，返回同上的一项；未知的源 `404 not_found` |
| `GET /api/v1/operations` | 增加 `source`（只接受 `curation`）、`provider`、`batch_id`，与现有目标筛选之间是“并且”；有批次的记录带 `batch_id`（没有批次时省略）；`origin` 多出 `provider_kind`、`provider`、`basis` |
| `GET /api/v1/releases/{id}/corrections` 与修正成功的响应 | 每个字段增加 `auto`：`{mode, value?, operation_id, provider_kind, provider}` 或 null；`effective_value` 是三层结果 |

发行详情 `GET /api/v1/releases/{id}` 和版本对比 `GET /api/v1/release-groups/{id}/comparison` 的每个发行可带
`auto_fields`（字符串数组）。它只列有自动值且没有人工修正行的字段名，包含自动设空的字段，排序与 `corrected_fields` 相同。
字段名按实际存储列出，例如专辑艺人用 `album_artist`。没有符合条件的字段时省略 `auto_fields`。
`corrected_fields` 仍只列人工修正的字段。

“清除人工修正之后显示什么”的三处预览（修正的撤销预览、管家 `correct_release` 的清除预览、修正状态）取自动值，没有自动值才是扫描值
（`CorrectionField.ValueAfterClear`）。

## 8. 其他读取处

- **需要关注**（`releasesql.FieldIssueRows`）：扫描拿不准的字段有修正行**或有自动行**时不计入；撤销自动值后重新计入。
- **专辑级署名**（`catalog.LoadReleaseCredits`）：专辑艺人一行按有效的专辑艺人（修正优先，其次自动值）替换。
- **署名事实与有效署名**：自动层的专辑艺人生成 `layer='auto'` 的事实；有效署名的层三选一——有专辑艺人的修正行用 `correction`，
  否则有自动行用 `auto`，否则 `scan`。自动值是合辑占位名时发行级有效署名为空，与修正层的行为一致。
- 管家的读取工具与审查证据经视图读有效值；管家的写工具仍只写人工修正。

## 9. 开启者

操作记在打开开关的管理员名下，不新造系统用户。现在管理员只有初始化时创建的那一位，没有修改角色的入口，最后一位管理员也不能被停用，
所以“开启者失效”目前到不了；核对仍按 ADR 实现，没有为它做界面或接手流程。

## 10. 错误码

| 条件 | 响应 |
| --- | --- |
| 开关关着、开启者不是启用状态的管理员、开启者在事务外读取之后变了 | `409 curation_disabled` |
| 批次不存在或已经结束 | `409 batch_closed` |
| 所有字段都相同或已标记为不再提出 | `409 nothing_to_apply` |
| 自动值已被更晚的自动整理改写；外部封面或歌词的行已不在或属于别的操作；它所属的发行已不存在 | `409 revert_conflict` |
| 外部封面或歌词：本地已有、外部已有、已标记不再提出（`ExternalApplied.Skipped` 给出 `local_present`、`occupied`、`suppressed`） | `409 nothing_to_apply` |
| 图片不合格（超过 16 MiB、不是四种格式、宽高读不出）；歌词规范化后为空、超过 64 KiB 或没有可显示的行；来源标识含冒号等不合形状的字符 | `400 invalid_input` |
| 歌词的曲目不存在或不是 `present` | `404 not_found` |
| 经闸门调用外部源时这个源的开关关着（进程内的错误，没有对应的 HTTP 写入口） | `409 source_disabled` |
| 来源、依据、范围或游标无效；缺少或非 UUID 的 `Idempotency-Key` | `400 invalid_input` |
| 批次、标记或发行的 ID 不是 UUID | `400 invalid_id` |
| 批次或标记不存在 | `404 not_found` |

其余（未登录、跨源、非管理员、分页、数据库不可用）与[受治理操作合同](./governance-contracts.md)第 8 节相同。

## 11. 各源开关与闸门（子任务 B1）

- **`curation_source_settings`**（迁移 0060）：`source`（主键，外部源的标识）、`enabled`、`updated_by`、`updated_at`。
  没有行时取声明的 `DefaultEnabled`（MusicBrainz 为开）。源的清单来自 `app` 装配时注册的声明，不存库。
- 接口返回的每一项：`enabled` 是这个源自己的开关；`active` 是“后台现在能不能用它”；`inactive_reason` 依次判断
  `curation_disabled`（总开关关着）、`enabler_unavailable`（开启者不是启用状态的管理员）、`source_disabled`，都不是时为 null。
- **闸门 `curation.Sources`**：后台代码只从它取能力（`ReleaseFinder`、`RecordingFinder`、`ArtistFinder`、`CoverFinder`、`LyricsFinder`，参数是源的标识）。
  注册的源按顺序是 MusicBrainz、Cover Art Archive、LRCLIB、AcoustID（B3），都默认开；AcoustID 还要指纹工具可用、声明的密钥都填了才生效（发布镜像只有用户密钥一把，第 16 节）。
  取到的是包了一层的实现，每个方法先 `Check`，再用 `sources.WithBackground(ctx, permit)` 调用适配器，`permit` 就是对这个源的 `Check`。
  所以核对发生在调用开始时、读缓存之前、每次出站之前（排队之后与每次重试之前）：调用排队或等待重试期间开关被关掉，之后不再出站。
- `Check` 在一个事务里读设置（与 `Writer.Apply` 相同的 `loadSettings`，开启者资格由 `identity.IsEnabledAdmin` 判断）：
  总开关关着或开启者失效返回 `curation_disabled`，源的开关关着返回 `source_disabled`；都不调用适配器，缓存里有结果也不返回。
- **开关只管后台。** 管家对话里的两个 MusicBrainz 工具持有适配器本身，不经过闸门：对话里的查询是用户本人发起的请求，
  未开启自动整理的实例行为不变（见[管家联网合同](./steward-web-contracts.md)的音乐资料一节）。
- 本任务里 `Sources` 没有生产调用方，由测试里的调用验证；子任务 C 的匹配循环是第一个调用方。

## 12. 外部对应的存储（子任务 B1）

**`external_links`**（迁移 0060）：`id`、`source`、`object_type`（`release | release_group | track | artist`）、`object_id`、`external_id`、
`method`、`confidence`（两者只约束形状 `^[a-z0-9_]{1,32}$`；程序层写 `method` 为 `tag_id | barcode | catalog_number | isrc | disc_id`、`confidence` 为 `certain`，见第 15 节）、`operation_id`（可空，延迟外键）、`created_at`；
唯一 `(source, object_type, object_id)`，反查索引 `(source, object_type, external_id)`。

- 表里的每一行都是生效的对应；ADR 0006 所说的“状态”在这里是“行在不在”。一个本地对象在一个源里至多一个。
- 对 MusicBrainz：发行 → release，专辑（本地的 release group）→ release group，曲目 → recording，艺人 → artist。
- `object_id` 不设外键：非测试代码不删除这四类对象。艺人合并靠重定向，被并入的艺人身上的对应仍在——按外部 ID 反查艺人时要解析重定向。
- 扫描、认领、人工修正的语句都不涉及这张表：重扫、认领、改名移动之后对应保持。
- 事务内的函数：`PutLink`（写入或替换，返回被替换的行）、`RemoveLink`、`LinksOf`（按对象）、`ObjectsLinkedTo`（按外部 ID 反查）。
  **它们不是受治理的写入口**：生产代码只经 `Writer.ApplyLinks`（类型 `external_link`，第 15.3 节）建立对应，它在执行器里调用这些函数。

## 13. 必需测试与断言点

集成测试在隔离的 PostgreSQL 上用合成数据运行；自动写入没有 HTTP 入口，测试直接调用 `curation.Writer.Apply`。

| 断言点 | 用例 |
| --- | --- |
| 三层优先顺序，八个字段逐个覆盖，含两层各自设空 | `TestPostgreSQLCurationFieldPriority` |
| 重扫（原样与扫描值变化）不动自动层 | `TestPostgreSQLCurationRescan` |
| 自动写入不动 `edit_revision` 与修正行；人工修改与自动修改互不妨碍对方的撤销 | `TestPostgreSQLCurationIndependentRevisions` |
| 同一字段两条自动修改的撤销顺序与写入者的恢复 | `TestPostgreSQLCurationRevertOrder` |
| 专辑艺人的自动值对署名、艺人页、排序与搜索的影响与恢复 | `TestPostgreSQLCurationArtistProjection` |
| 批次、按批撤销、部分跳过、同键重试不产生新的撤销记录 | `TestPostgreSQLCurationBatchRevert` |
| 按来源撤销，超过 100 条时续做 | `TestPostgreSQLCurationProviderCursor` |
| 不再提出：同值被挡、不同值通过、清除后恢复 | `TestPostgreSQLCurationSuppression` |
| 开关与开启者 | `TestPostgreSQLCurationEnabler` |
| 普通成员被拒绝 | `TestPostgreSQLCurationMemberDenied` |
| “需要关注”随自动值与撤销变化 | `TestPostgreSQLCurationAttention` |
| 并发：两次自动写入、撤销与自动写入（用事务锁制造交叠，不靠时间） | `TestPostgreSQLCurationConcurrent` |
| 筛选与候选查询走 `operations_provider_idx`、`operations_batch_idx`；候选查询没有排序与顺序扫描 | `TestPostgreSQLCurationFilterPlans` |
| 迁移升级与回退往返 | `TestPostgreSQLMigrationRollbackRoundTrip/0058_auto_curation.sql` |
| 接口样本：`corrections/get-auto`、`operations/auto-list`、`operations/auto-reverted`、`curation/*` | `TestPostgreSQLAPIResponseSamples` 与前端 `api-contract.test.ts` |

子任务 B1 的断言点：

| 断言点 | 用例 |
| --- | --- |
| 闸门：总开关关闭、源关闭、开启者失效时拒绝，零出站，缓存里有结果也不返回；管家工具照常可用 | `TestPostgreSQLCurationSourcesGate` |
| 调用在排队或等待重试时关掉开关，之后不再出站 | `TestPostgreSQLCurationSourcesGateDuringWait` |
| 源开关接口：默认开、管理员读写、成员 403、未知源 404、`active` 与 `inactive_reason` | `TestPostgreSQLCurationSourcesSettings` |
| 外部对应：一对象一源一条、替换返回旧行、按对象读、按外部 ID 反查；移动、重扫与认领之后保持 | `TestPostgreSQLCurationSourcesLinks` |
| 迁移升级与回退往返 | `TestPostgreSQLMigrationRollbackRoundTrip/0060_curation_sources.sql` |
| 接口样本：`curation/sources`、`curation/source-updated` | `TestPostgreSQLAPIResponseSamples` 与前端 `api-contract.test.ts` |

子任务 B2 的断言点见第 14.7 节。

视图多了八个连接：目录的四个 EXPLAIN 用例与 `TestPostgreSQLCatalogHTTPPerformance` 是门禁，要设启用参数才会运行
（`ROOMUSIC_CATALOG_EXPLAIN=1`、`ROOMUSIC_CATALOG_EXPLAIN_OUTPUT`、`ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT`），不设时是跳过而不是通过。

## 14. 外部封面与外部歌词（子任务 B2）

自动整理补的封面与歌词不占本地的位置：各有一张表，读取时本地优先、外部只补空缺，扫描不读写它们。
写入与撤销是受治理操作；本任务**没有生产调用方**（什么时候取、取哪一个由子任务 C 决定），测试直接调用 `curation.Writer`。

### 14.1 数据模型（迁移 0061）

- **`release_external_artworks`**：主键 `release_id`（一个发行至多一张）；`content_hash`、`mime_type`、`width`、`height`、`storage_key` 与 `release_artworks` 同形；
  `source`（源的标识，`^[a-z0-9._-]{1,64}$`）、`external_id`（1–128 字节；Cover Art Archive 是 `<外部发行 ID>/<图片编号>`）、
  `operation_id`（写下它的操作，非空，延迟外键）、`created_at`；`storage_key` 上有索引。图片文件与本地封面在同一个内容寻址目录 `<数据目录>/artwork/`。
- **`track_external_lyrics`**：主键 `track_id`（一首曲目至多一份）；`lyrics`（规范化后的文本，非空、不超过 65,536 字节）、`content_hash`、`source`、
  `external_id`（LRCLIB 的记录编号）、`operation_id`、`created_at`。
- `operations_type_ck` 增加 `release_external_artwork`、`track_external_lyrics`。两类操作的目标都是**发行**
  （歌词操作的目标是曲目当时所在的发行，曲目记在状态里）；版本号用 `releases.auto_revision`。
- 不再提出标记（`curation_suppressions`，结构不变）每次撤销写两条——同一张图的另一档缩略图内容不同，只按内容挡不住：

  | 对象 | `target_type` | `subject` | `value_hash` |
  | --- | --- | --- | --- |
  | 封面的内容 | `release` | `artwork` | 图片字节的 SHA-256 |
  | 封面的外部身份 | `release` | `artwork_source` | `source + "\x00" + external_id` 的 SHA-256 |
  | 歌词的内容 | `track` | `lyrics` | 规范化后文本的 SHA-256 |
  | 歌词的外部身份 | `track` | `lyrics_source` | `source + "\x00" + external_id` 的 SHA-256 |

- **回退是有损的**：脚本先把 `steward_plan_items.operation_id` 里指向这两类操作及其撤销的引用置空（这个外键立即检查、不级联），
  再删四种标记与两张表，删撤销记录再删操作，`SET CONSTRAINTS ALL IMMEDIATE` 之后恢复类型约束。已下载的图片文件留在 `artwork/` 里无人引用。

### 14.2 读取

- **封面**：`releasesql` 的连接片段是两个左连接（外部的只在没有本地行时连上），四列各取 `COALESCE(本地, 外部)`；所有摘要（发行、专辑的代表版本、
  曲目摘要、搜索、最近添加、播放历史、待处理问题、认领建议）因此一致。`artwork.Store.Lookup` 按存储键在两张表里找，图片接口不区分来源。
  摘要里的 `artwork` 对象**没有来源字段**；哪些是自动补的从操作记录看。扫描反馈的封面指纹只看本地的。
- **歌词**：`LoadTrackLyrics` 先用本地的；本地没有，或有但解析不出可显示的行时，用外部的，`source` 为 `online`
  （见[歌词合同](./lyrics-contracts.md)）。“本地有歌词”在读取与写入两处是同一个判断：`catalog.ParseLyrics` 成功。
- 本地封面或歌词出现后自动改用本地的，消失后回到外部的；外部的行一直留着。

### 14.3 扫描与文件

- 扫描的语句不涉及两张新表：全量重扫（含扫描删除本地封面关联的情形）、改名移动、认领之后外部封面与外部歌词保持。认领不搬它们。
- `artwork/` 目录现在有两个写者（扫描与自动整理）。“数引用再删文件”与“落盘再写行”靠**存储键的事务级 advisory lock** 互斥
  （两参数形式，类别是 ASCII 的 `ARTW`，第二个参数是存储键的哈希；永远最后取，在目录写锁与发行行锁之后）：
  扫描与自动整理在写关联之前取锁并确认文件在；`Store.Discard` 开事务、取锁、数**两张表**的引用，为 0 才删文件。
- `artwork.Store` 的导出入口：`Describe`（校验并算出键，不落盘）、`Write`／`WriteIfChanged`（在调用方的事务里取锁并落盘）、`Exists`、`Discard`。

### 14.4 写入 `ApplyArtwork`、`ApplyLyrics`

```go
type ArtworkRequest struct { ReleaseID string; Data []byte; Source Source; ExternalID string; BatchID, Key, RequestID string }
type LyricsRequest  struct { TrackID string; Text string; Source Source; ExternalID string; BatchID, Key, RequestID string }
type ExternalApplied struct { OperationID string; Skipped string } // Skipped: local_present | occupied | suppressed
```

`Source.Kind` 必须是 `source`，`Provider` 按 `^[a-z0-9._-]{1,64}$` 校验（比 `Apply` 严：不允许冒号）。流程与 `Apply`（第 4 节）相同的部分不重复：

1. 事务外只校验，不落盘：封面 `Store.Describe`；歌词 `metadata.NormalizeLyrics` 后 `catalog.ParseLyrics` 必须有可显示的行。
2. 加锁：封面 `governance.LockAutoRelease`（要求发行可见）；歌词 `governance.LockAutoReleaseOfTrack`——先取目录写锁，再按曲目解析所在的发行
   （扫描会把曲目改挂到别的发行），再锁发行行。然后核对设置与批次。
3. 依次判断，命中即以 `409 nothing_to_apply` 结束，不产生记录、不落盘：
   - `local_present`：发行有本地封面／曲目的本地歌词能解析出可显示的行；
   - `occupied`：外部表里已有这一行，不论内容是否相同——**存放层面就是只补空缺，要换先撤销**。
     封面的例外：这一行指向的文件已经不在磁盘上（恢复了数据库备份而没有数据目录）时视同没有，旧行被这次写入替换；旧行的操作此后撤不了，记录保留；
   - `suppressed`：内容或外部身份的不再提出标记命中。
4. 封面在事务里取存储键的锁并落盘；插入外部表的行；`auto_revision` 加一。
5. 记录：前态是 `{"artwork": null}` 或 `{"track_id", "lyrics": null}`（替换了文件丢失的旧行时，前态记旧行的键、哈希、来源与原操作，只作记录）；
   后态封面记 `{storage_key, content_hash, mime_type, width, height, source, external_id}`，歌词记
   `{track_id, track_title, lyrics: {content_hash, kind, lines, source, external_id}}`，**不存歌词全文**。
6. 封面：执行器写了文件而事务没有提交时，返回前 `Discard`。同键重放不进执行器，不落盘。

指纹含类型、发行或曲目、内容哈希、`provider`、`external_id`，不含 `basis`。

### 14.5 撤销与展示

两个 `Kind` 在 `curation` 包（`ExternalArtworkOperations`、`ExternalLyricsOperations`），由 `app` 注册；单条撤销、按批与按来源的 `Reverter`、
管家的 `revert_operation` 都自动覆盖它们。

- 按**记录的目标发行**加锁，不要求可见；发行行已不存在时 `409 revert_conflict`。
- 外部表这一行存在且 `operation_id` 是原操作：删除，`auto_revision` 加一，写两条不再提出（`created_by` 是执行撤销的人）；否则 `409 revert_conflict`。
  不读 `edit_revision` 作判断，之后的人工修改不妨碍撤销；响应的 `revision` 是发行当前的 `edit_revision`。
- 清除 `artwork` 或 `artwork_source`、`lyrics` 或 `lyrics_source` 中任一标记时，按 `target_type`、`target_id` 和非空的 `operation_id`
  匹配对应主题，两条在同一事务中删除。对应标记不存在或指定标记的 `operation_id` 为空时，仅删除指定标记。
- 封面的文件在提交之后清理：`operations.Outcome.AfterCommit`（`Run` 在提交成功后调用一次，重放与失败时不调用）里 `Store.Discard`。清理失败只记日志
  `curation.artwork.discard_failed`。
- `Describe` 的 `changes`：

  ```jsonc
  {"kind": "release_external_artwork", "source": "coverartarchive", "external_id": "…",
   "artwork": {"resource_id": "<storage_key>", "mime": "image/jpeg", "width": 1200, "height": 1200}}
  {"kind": "track_external_lyrics", "source": "lrclib", "external_id": "…",
   "track": {"id": "…", "title": "…"}, "lyrics_kind": "synced", "lines": 42}
  ```

  `revertible` = 外部表这一行仍属于这条操作。撤销记录用被撤销操作的后态，内容不分前后。`track.title` 是记录里保存的当时的标题。
  读不懂的状态不报错，`changes` 为 null（一条坏记录不能让整页列表失败）。撤销之后 `artwork.resource_id` 可能已经读不到图片。
- 撤销预览（管家工具用）：一行，字段名 `artwork` 或 `lyrics`，前是 `{source, external_id}`（歌词另带 `track_title`），后是 null。

### 14.6 已知限制

- 歌词操作记在曲目当时所在的发行上：曲目之后被改挂到别的发行时，按新发行筛选操作记录看不到它。
- 歌词的不再提出标记按曲目存；子任务 C 起按发行筛选的标记列表包含它的曲目与专辑上的标记（第 15.6 节）。
- 两者都留给整理记录页（子任务 E1）按批次展示。
- 恢复数据库备份而没有数据目录时，外部封面的行在、文件不在：摘要仍给出 `artwork`，图片接口 404。子任务 C 的缺口查询把它算作缺口重新获取
  （第 15.4 节；新的写入替换旧行）。

### 14.7 必需测试与断言点

| 断言点 | 用例 |
| --- | --- |
| 读取优先：只有外部、本地出现、本地被全量扫描删除；各摘要与图片接口 | `TestPostgreSQLExternalArtworkPriority` |
| 重扫、改名移动、认领之后保持；孤儿清理不删被外部引用的文件 | `TestPostgreSQLExternalArtworkRescan` |
| 写入的记录、三种跳过、开关与开启者、无效图片与重放不落盘、回滚后的文件清理、文件丢失的旧行被替换 | `TestPostgreSQLExternalArtworkApply` |
| 单条、按批、按来源撤销；两条不再提出；共享文件不删；人工修改版本号不变 | `TestPostgreSQLExternalArtworkRevert` |
| 歌词的读取、写入、撤销；`online`；只有元标签的本地歌词 | `TestPostgreSQLExternalLyricsPriority`、`…Apply`、`…Revert` |
| 并发：同一发行两次写入、写入与撤销、两个发行同一内容一写一撤（用锁制造交叠） | `TestPostgreSQLExternalConcurrent` |
| 操作记录列表的 `changes` 与 `revertible`、坏记录不让列表失败、管家撤销工具 | 同名前缀的用例 |
| `AfterCommit` 只在提交成功后调用一次 | `backend/internal/operations` 的集成用例 |
| 闸门对封面与歌词两种能力 | `TestPostgreSQLCurationSourcesGate` |
| 迁移升级与回退往返（含方案项的外键） | `TestPostgreSQLMigrationRollbackRoundTrip/0061_external_artwork_lyrics.sql` |
| 接口样本：`tracks/lyrics-online`、`operations/external-*`、`curation/sources` | `TestPostgreSQLAPIResponseSamples` 与前端 `api-contract.test.ts` |


## 15. 程序层：后台匹配与补全（子任务 C）

子任务 C 加了自动整理的第一个生产调用方：后台循环 `backend/internal/curation/program`。它按缺口把发行与 MusicBrainz 对上，有把握的直接建立外部对应、
补空缺字段、取封面与歌词、在强证据下归组；拿不准的形成待定项留给模型层（子任务 D）。本节记合同、判定规则与不变量；
推导过程与取舍在本机归档的任务设计 `11-22-auto-curation-c-program/design.md`。

### 15.1 数据模型（迁移 0062）

- **`curation_work_states`**：主键 `(subject, target_id)`，`subject` 为 `release`、`artwork`（目标是发行）或 `lyrics`（目标是曲目）；`rule_version`、`input_digest`、
  `status`（`matched | uncertain | no_match | try_later | done`）、`attempts`（得出结论的次数）、`generation`（代次，每次得出结论加一）、`retry_count`、`next_attempt_at`、`result`。
  存进去的规则版本是 `program.RuleVersion × 100 + textnorm.Version`。
- **`curation_pending_items`**：每个发行每一种（`release_match | recording_match`）至多一条未结束的；状态 `waiting_model | in_progress | resolved | waiting_human | shelved`，
  本任务只写 `waiting_model` 与 `resolved`（`resolution='program_matched'`）。`evidence`、`candidates`、`conflicts` 是 JSON，外部来的文本按不可信文本处理。
- `curation_settings.paused`；`operations_type_ck` 增加 `external_link`。
- 回退有损：删两张表、`paused` 列、`external_link` 操作与它的撤销、它建的对应、`external_link` 与 `group_join` 标记；自动整理做的归组（已有类型）保留。

### 15.2 判定的不变量

- **输入只取人工修正与扫描值**，不读自动层：自动层是匹配的产出，不能拿来证明自己。证据经 `catalog.LoadCurationEvidence` 读取（`program` 不导入 `catalog/internal`）。
- 依据顺序：标签 ID → 条码（含 CUE 的 MCN）→ 厂牌编号 → ISRC → 光盘目录（只用 CUE 整轨）→ 搜索；**命中即定**，命中的一步拿不准就不往后走。搜索与模糊目录从不判定。
- **把握足够** = 核对后恰好剩一个候选 + 这一步要求的佐证 + 没有任何相反证据（冲突代码表见设计 3.3）。列表可能被截断（25 条）、候选多于 3 个、曲号重复都直接拿不准。
- 已经有外部对应的发行判定为别的外部发行时是 `existing_link`，拿不准；程序从不替换已有的对应。

专辑方式的依据（每一步至多按 ID 读取 3 个完整发行再核对；曲目数只在读取之后核对）：

| 步 | `method` | 候选 | 这一步要求的佐证 |
| --- | --- | --- | --- |
| 1 | `tag_id` | 在库曲目的 `mb_release` 唯一（多于一个即 `mixed_release_tags`） | 位置对得上（曲号不是推断时允许本地是子集），并且 `count_equal` 或 `durations_ok` |
| 2 | `barcode` | `ReleasesByBarcode`（含 MCN） | `count_equal`，并且 `durations_ok`、`title_match`、`artist_match` 至少一项 |
| 3 | `catalog_number` | `ReleasesByCatalog` | `count_equal`，并且 `label_match` 或 `durations_ok` |
| 4 | `isrc` | 至多 4 首的 `RecordingsByISRC` 结果里发行集合的交集 | `count_equal`，并且至少一对 ISRC 相符 |
| 5 | `disc_id` | CUE 整轨逐碟的精确 disc ID 交集（任一碟模糊即 `disc_fuzzy`） | `count_equal`，并且 `title_match` 或 `artist_match` |
| 6 | `search` | `SearchReleases` | 从不判定（`search_only`） |

`durations_ok` = 可比较的对数 ≥ ⌈配对数/2⌉ 且全部在容差内。本地有推断的曲号时，每一对都必须可比较且在容差内（否则 `inferred_positions`）；(碟号、曲号)重复即 `position_conflict`。
其他直接拿不准的：`too_many`（候选多于 3 个）、`list_truncated`（列表 25 条或录音的发行数 25）、`isrc_spread`（交集为空）、`ambiguous`（多个候选都通过）。

相反证据（任何一条就拿不准）：`duration_mismatch`、`barcode_mismatch`、`catalog_mismatch`、`title_artist_mismatch`（第 2–4 步，标题与艺人都对不上）、
`release_tag_mismatch`、`group_tag_mismatch`、`artist_tag_mismatch`、`disc_tag_mismatch`、`recording_tag_mismatch`、`track_tag_mismatch`、`isrc_mismatch`、
`media_count_mismatch`、`media_format_mismatch`（本地是 CD 而外部没有 CD 介质）、`pseudo_release`、`suppressed`、`existing_link`。

单曲方式：`tag_id`（`mb_recording` 唯一，外部时长已知且在容差内）→ `isrc`（按时长筛选后恰好一个且标题相等）→ `search`（从不判定）。
信息不足（标题与艺人都是占位、没有任何标识）记 `no_match` + `insufficient_evidence`，不查询、不建待定项。

- 零散单曲（`candidate_kind='loose_unknown'`）只对录音，不补发行字段、不取封面。
- 时长容差 max(3 秒, 外部时长的 1%)；松比较是 `textnorm.Loose(textnorm.Text(x))`，空键不相等。

### 15.3 写入

| 步骤 | 写入口 | 规则 |
| --- | --- | --- |
| 外部对应 | `Writer.ApplyLinks`（类型 `external_link`） | 一次匹配一条记录（发行、配对的曲目、必要时本地专辑）；标记过的 `suppressed`、相同的 `unchanged`、已对应别处的 `occupied`；后态里发行那一项带 `external_group_id` |
| 补空缺 | `Writer.Apply` 带 `OnlyEmpty` | 锁内跳过有修正行或自动值的字段（`occupied`）；只补标题（占位时）、专辑艺人、年份、厂牌、编号、条码 |
| 归组 | `Writer.JoinGroup`（类型 `release_group_change`） | 见 15.5 |
| 封面 | `Writer.ApplyArtwork` | 见 15.4 |
| 歌词 | `Writer.ApplyLyrics` | 见 15.4 |

- 每一步的幂等键是 UUIDv5（目标、主题、规则版本、代次、步骤）。中断后重做回放原结果；清除标记之后代次已经前进，不会回放一条已撤销的操作。
  `nothing_to_apply` 当作已完成；`idempotency_conflict` 时代次加一重试一次。
- **外部对应的撤销**：每一项仍是这条操作写的才整条撤（否则 `409 revert_conflict`）；逐项删除，为每个外部 ID 写 `external_link` 标记（目标是那个对象，哈希为 `源 NUL 外部 ID`），
  `auto_revision` 加一。撤销后摘要变化，发行被重新判定为 `suppressed` 拿不准并建待定项。
- `Describe`：`changes = {kind:"external_link", source, method, confidence, external:{title, artist, date, country}, links:[{object_type, object_id, external_id, external_group_id?}]}`；
  撤销记录的 `changes` 与被撤销的那条相同。

### 15.4 封面与歌词

- 封面只在专辑方式：发行已对应 + 本地没有封面 + 外部封面行不在**或文件不在**。先取外部发行的主封面（`sources.FrontImage`），没有再取外部专辑的；
  下载之前用 `curation.ExternalIdentitySuppressed` 查按图片编号的标记，命中就不下载。
- 歌词不依赖匹配：曲目在库、本地与外部都没有歌词、标题不空、艺人不是占位、时长 > 0 时，先 `LyricsFor` 精确查询，不接受再 `SearchLyrics`。
  接受 = 不是纯音乐、有歌词、时长差 ≤ 2 秒、标题松比较相等、艺人相合（相等，或按 feat./&/、等切开后第一个名字相等）；搜索结果按内容去重后恰好一份才写，同步的优先。
- 歌词单独一轮，排在发行之后。

### 15.5 强证据归组

- `JoinGroup` 在锁之后核对：发行有 MusicBrainz 的发行对应，且写下它的 `external_link` 记录后态里有外部专辑 ID X；目标专辑有对 X 的专辑对应。任一不满足 `409 no_strong_evidence`。
- 发行最近一条未被撤销的 `release_group_change` 来源不是 `curation`（页面、对话、管家）→ `409 manual_structure`：人做过的结构决定优先。
- 标记过不再归入（`group_join`，哈希为目标专辑 ID）→ `409 nothing_to_apply`。
- `governance.GroupChangeOperations(suppressions)`：撤销来源为 `curation` 的归入时写 `group_join` 标记；人工的归组撤销不写。归组记录的后态多了 `action`。
- 程序只做“归入”，不做“移出”。

### 15.6 后台循环、暂停与进度

- 单线程；总开关关、开启者失效、暂停时不读证据、不出站。启动时收掉遗留的 program 批次。
- 每轮按（加入时间倒序、ID 倒序）键集分页，每页 100 个发行；重算摘要与状态比较，不同才处理。变化标记（扫描结束、设置、源设置、标记、操作记录、到期的重试）变了或 6 小时后再扫。
- **闸门也挡暂停与扫描**：`curation.Sources` 的放行检查（每次出站之前）在暂停时返回 `409 curation_paused`，扫描进行中返回 `409 scan_running`（`WithScanCheck` 注入）。
- `ErrTryLater` → `try_later`，退避 min(24 小时, 10 分钟 × 2^n)，不计尝试次数；源的开关关着 → 跳过这个主题，不写状态。
- 批次：第一次写入时开，满 500 条、1 小时、一轮结束、关闭或暂停时关；全被挡下的空批被移除。
- `app.WithCurationProgram(false, …)` 关掉循环；`app` 包里共用的测试夹具默认关掉它（现有测试会打开总开关，循环会向真实外部服务发请求）。

| 接口 | 说明 |
| --- | --- |
| `GET /api/v1/admin/curation/settings` | 增加 `paused` |
| `PUT /api/v1/admin/curation/settings` | `{enabled?, paused?}`，至少一项，否则 `400 invalid_input`；`enabled` 从关到开时清除暂停 |
| `GET /api/v1/admin/curation/progress` | `{state, rule_version, releases:{total, processed, matched, uncertain, no_match, try_later}, pending:{waiting_model}, batch:{id, opened_at, operations}\|null, last_pass_finished_at}`；只算可见发行；没装配循环时 `state="unavailable"` |
| `GET /api/v1/admin/curation/suppressions?release_id=` | 同时列出这个发行的曲目与它所在专辑上的标记 |

新错误码：`409 no_strong_evidence`、`409 manual_structure`、`409 curation_paused`、`409 scan_running`。

### 15.7 已知限制

- 艺人的外部对应与同一外部艺人的自动合并在子任务 G；曲目标题与艺人的补全在子任务 I；声学指纹见第 16 节（子任务 B3）；待定项的处理在 D，界面在 E1、E2。
- 撤销外部对应不连带撤销它带来的字段、封面与归组（各自独立可撤；按批撤销能一次撤完）。
- 分轨抓取不重建光盘目录；音译或翻译了的专辑因 `title_artist_mismatch` 交给模型。

### 15.8 必需测试与断言点

集成测试在隔离的 PostgreSQL 上用合成数据运行；外部源用测试里实现能力接口的假对象经 `curation.NewSources` 注册（走真实闸门），端到端用本地 HTTP 服务代替三个外部源。
集成用例省略共同前缀 `TestPostgreSQLCurationProgram`。

| 断言点 | 用例 |
| --- | --- |
| 专辑六步、单曲三步与各步要求的佐证；时长容差的内外边界 | `program` 包的 `TestAlbumCorroboration`、`TestAlbumMethodRequirements`、`TestSingleRules` |
| 每一条相反证据各一个反例（冲突代码逐个断言） | `TestAlbumContraryEvidence/<冲突代码>` |
| 候选多于 3 个、列表截断、ISRC 分散、模糊目录、信息不足、空键、命中即定 | `TestLookupBounds`、`TestFirstHitStopsLookup`、`TestAlbumCorroboration/barcode_second` |
| CUE 的 MCN；由 CUE 重建光盘目录（B1 的已知向量）与不建目录的条件 | `TestCurationEvidenceMCN`、`TestDiscTOCFromCue` |
| 歌词接受规则（时长、标题、艺人写法与分隔符、繁简与全半角、纯音乐、多份不同） | `TestLyricsAcceptance` |
| 三个主题的摘要：稳定、按输入变化、时长取整 | `TestSubjectDigests`、`TestPostgreSQLCurationEvidenceProjection` |
| U7 合成曲库各形态的结果与写入 | `Library/{picard,barcode,catalog,isrc_album,isrc_single,cue,multi_disc,chinese_search,untagged}` |
| 补空缺只补真正空着的字段，人工优先 | `OnlyEmpty` |
| 强证据归组、撤销后不再归入、人工移出之后不归回 | `Group/{revert,manual_detach}` |
| 撤销对应后拿不准并建待定项；清除标记后是新的记录；待定项结束；代次 | `Relink` |
| 总开关、开启者、暂停、扫描、单元进行中暂停或开始扫描；各源开关 | `Gates/*`、`SourceSwitches` |
| 重扫不重复请求、规则版本、人工修正经变化标记触发重判 | `RealRescan`、`RescanAndChanges` |
| `ErrTryLater` 的退避与尝试次数 | `Retry` |
| 中断后重做回放、遗留批次；封面文件丢失后的恢复 | `Restart`、`RestartArtworkReplay` |
| 经 `app` 装配的端到端（三个真实适配器 + 本地服务） | `EndToEnd` |
| 外部对应的写入、撤销、展示；强证据归组；`OnlyEmpty`；暂停与闸门；进度与执行计划；标记列表 | `TestPostgreSQLCurationC{LinksApply,LinksRevert,LinksOperations,JoinGroup,OnlyEmpty,Paused,Progress,Suppressions}` |
| 迁移升级与回退往返 | `TestPostgreSQLMigrationRollbackRoundTrip/0062_curation_program.sql` |
| 接口样本：`curation/settings-paused`、`curation/progress`、`operations/link-list`、`operations/link-reverted` | `TestPostgreSQLAPIResponseSamples` 与前端 `api-contract.test.ts` |

判定规则、代次与“仍是这条操作写的”等关键逻辑做了变异检查（每条相反证据、数量与时长佐证、条码的第二项佐证、歌词容差、命中即定、代次、强证据三项、人工结构、`OnlyEmpty`）。

## 16. 声学指纹与 AcoustID（子任务 B3）

子任务 B3 把 AcoustID 接进第 15 节的匹配：没有条码、编号、ISRC，标签又差的单曲与专辑可以靠音频本身对上 MusicBrainz。
推导与取舍在本机归档的任务设计 `11-23-auto-curation-b3-acoustid/design.md`（第八节优先）；外部源一侧见[外部源合同](./sources-contracts.md) 8a 节，部署见[部署合同](./deployment-environment-contracts.md)。

### 16.1 数据模型（迁移 0063）

- **`track_acoustic_fingerprints`**：主键 `track_id`（随曲目删除）；`pipeline_version`；有效性键 `content_fingerprint`、`source_size`、`source_modified_at`、`cue_index_frames`、`cue_end_frames`；
  `status`（`done | failed`）、`fingerprint`、`duration_seconds`、`failure_category`（`too_short | decode_failed | timeout`）、`attempts`、`computed_at`。
  放在数据库而不是数据目录（ADR 0006 决策记录“B3 说明”）：只为走到指纹这一步的曲目按需计算。
- **`curation_source_keys`**：主键 `(source, name)`；`ciphertext`、`last_four`、`status`（`unchecked | valid | invalid`）、`updated_by`、`updated_at`。清除即删行。
- 回退删除两张表。

### 16.2 指纹的计算与缓存

- 只在匹配走到指纹这一步时按需计算，按曲目缓存。缓存行的 `pipeline_version` 与有效性键和曲目当前值逐项相同才有效；音频不变不重算。
- 取源与音频分析相同（允许的根、大小与修改时间核对、CUE 区间、DSD 转 PCM）；ffmpeg 以 `file:/dev/fd/3` 读打开的句柄（可定位），
  解码前 120 秒为 11025 Hz 单声道 s16le，经管道交给 `fpcalc -format s16le -rate 11025 -channels 1 -length 120 -json -`。单首时限 60 秒。
- 发给 AcoustID 的时长是整首时长（CUE 虚拟轨是区间长度；最后一首没有结束帧时取曲目时长），不是解码的 120 秒。
- 三种结果：
  - 算出；
  - 永久算不出：`too_short`（精确时长 < 10 秒，比较时不取整），或缓存里的失败还没到重试时间；
  - 暂时算不出：取源失败、文件变了、时长未知。暂时的结果不写缓存。
- `decode_failed`、`timeout` 照音频分析的规则重试：24 小时后，至多 3 次。
- `catalog.CurationEvidence`、`catalog.CurationTrack` 与曲目查询的标识种类**没有新增任何字段**：发行摘要是整个证据结构的哈希，加字段会让全库重做。
  指纹按曲目 ID 经 `program.Fingerprints` 取，由 `app` 把 `playback.Fingerprints` 适配进来。

### 16.3 密钥与源的状态

- `SourceSettings.keys`：只有声明了密钥的源才有（其余省略，现有输出不变），形状为 `[{name, configured, last_four, status}]`，按声明顺序每把一项。
- `inactive_reason` 依次判断：
  1. `curation_disabled`
  2. `enabler_unavailable`
  3. `source_disabled`
  4. `fingerprint_unavailable`：指纹工具自检没通过
  5. `bundled_key_rejected`：内置的应用密钥本进程内被 AcoustID 拒过（B3 补充）
  6. `key_missing`：有一把没填
  7. `key_invalid`：都填了，有一把无效
- 第 4、5 条由 `SourceRegistration.Unavailable func() string` 给出（空串表示可用；`app` 装配 AcoustID 时按指纹工具与 `BundledKeyRejected()` 返回）。
- 加密用唯一的实例密钥存储（`app` 里只建一个，交给管家与闸门），绑定 `instance` 与 `curation.source_key.<源>.<名>`；解密失败当作无效。
  `curation` 只依赖一个 `Seal`/`Open` 小接口，不导入 `steward/secret`。
- **回写**：
  - 真正出站成功时，把这次用到的、仍是同一密文的 `unchecked` 改成 `valid`；缓存命中不算。
  - `InvalidKeyError{Name}` 只把那一把、并且仍是这次密文的标为 `invalid`（compare-and-set，换上的新密钥不会被旧请求标坏）。
  - 回写只改 `status`，**不改 `updated_at`**；`updated_at` 进入变化标记，改了会让进行中的一轮重来。
  - 填写与清除会推进变化标记；清除只删行时，同一事务里更新源设置的时间。
- 闸门 `Check("acoustid")` 在这几种原因时返回 `409 source_disabled`；暂停、扫描与数据库错误照常返回各自的错误。

| 接口 | 说明 |
| --- | --- |
| `PUT /api/v1/admin/curation/sources/{id}/keys/{name}` | `{key}`，去掉首尾空白后 1–128 个可打印 ASCII，否则 `400 invalid_input` → 这个源的 `SourceSettings` |
| `DELETE /api/v1/admin/curation/sources/{id}/keys/{name}` | → 这个源的 `SourceSettings` |

管理员与同源检查同其他源接口。新错误码 `400 key_not_supported`（源没有密钥）；未知源或未声明的密钥名 `404 not_found`。密钥的变更不进操作记录。

### 16.4 匹配的新一步

**可用性**：工作者用 `Check("acoustid")` 判断。只有 `not_found`（没注册）与 `source_disabled` 算不可用，其余错误照常往上抛。可用时向 `MatchWithFingerprint` 传入指纹步骤，
`Match` 的签名不变（等于不带指纹）。结果多一个 `fingerprint` 字段，写进工作状态的 `result`：

| 值 | 含义 |
| --- | --- |
| `used` | 走到了这一步，并且 AcoustID 可用 |
| `unavailable` | 走到了这一步，但 AcoustID 不可用；途中遇到 `ErrInvalidKey` 或 `source_disabled` 也是这个值，并且照常往下走到搜索 |
| `not_reached` | 前面某一步命中，或者因为别的原因提前返回 |

**专辑方式**（第 6 步 `acoustid`，在 `disc_id` 与 `search` 之间）：

1. 抽样：与 ISRC 相同的位置，在全部曲目里取至多 4 首。算出的不到一半：如果有暂时原因，整个发行记 `try_later`；否则这一步不算命中。
2. 逐首查询，只要分数 ≥ 0.9 的录音，得到每首的录音集合 R_t。
3. 去掉曲目数或碟数与本地不符的发行（两个值都给了才比较），再在有结果的曲目之间求发行交集。
   - 抽样 ≥ 2 首却只有 1 首有结果 → `acoustid_sparse`；
   - 交集为空 → `acoustid_spread`；
   - 交集多于 3 个 → `too_many`。
4. 按 ID 读回完整发行后核对，通过 = `count_equal` ∧ `durations_ok` ∧ 每首有结果的抽样曲目配对到的录音 ∈ R_t。
   - 配对到的录音不在 R_t 里 → `acoustid_mismatch`；
   - 其余相反证据同第 2–4 步，`title_artist_mismatch` 也适用。
5. 候选用 AcoustID 返回的发行信息生成，并带 `score`（`omitempty`；其他方法的候选不带）：各有结果曲目里出现在这个发行上的录音最高分，再取这些曲目之间的最小值。

**单曲方式**（在 `isrc` 与 `search` 之间）：

- 分数 ≥ 0.9 的录音按时长筛选。剩下多于 3 个 → `too_many`；不多于 3 个时，逐个用 MusicBrainz 的 `RecordingByID` 读回正式数据，再交给 `DecideSingle`。
- 标题不是占位，并且与录音标题松比较不相等 → `title_mismatch`。

**信息不足**：指纹可用时不再提前返回；但指纹这一步没命中时，仍记 `no_match` + `insufficient_evidence`，不建待定项。

依据文案 `acoustid` 是“声学指纹”。

### 16.5 重新处理（规则版本不变，仍是 1）

`needsWork` 对 `release` 主题多一个条件，必须同时满足：

- 本轮 AcoustID 可用（一轮开始时判断一次）；
- 状态是 `no_match` 或 `uncertain`；
- 满足下面两条之一：
  - `result.fingerprint == "unavailable"`；
  - 没有这个字段（B3 之前写的），`method` 是空或 `search`，并且 `conflicts` 里没有 `track_count`。

**只因为这一条才被重开的发行**，在处理前再判断一次可用性：不可用就跳过，不写状态。

结果：

- 没填密钥的实例升级后，一个发行也不重做。
- 填齐密钥后，只重做那些“走到了指纹这一步却因为它不可用而跳过”的发行，每个一次。
- 已对上的发行、在前面各步就拿不准的发行，以及封面与歌词，都不受影响。

### 16.6 已知限制

- 专辑方式里，外部已合并掉的旧录音 ID 可能误报 `acoustid_mismatch`，结果只是拿不准，交给模型。
- 同一首歌在 MusicBrainz 里常有重复录音，单曲方式会出现较多 `ambiguous`。
- 同一张专辑的多个版本，在曲目数与碟数都相同时会是 `too_many` 或 `ambiguous`。
- 真实的匹配比例要到 V12 的真实试用才知道。
- arm64 的 fpcalc 只在发布流水线的构建阶段验证（构建时核对版本）。

### 16.7 必需测试与断言点

| 断言点 | 用例 |
| --- | --- |
| 适配器：POST、gzip、六项表单、映射、去重、发行的曲目数与碟数、缓存命中、换任一把密钥仍命中 | `sources/acoustid` 的 `TestLookupFingerprintRequestMappingAndCache` |
| 全实例间隔 ≥ 334 ms；429/14、503/13 的退避与用尽；码 4、17、6 各自命名、不缓存；码 3、8 是空结果 | `TestLookupFingerprintGlobalInterval`、`TestLookupFingerprintBackoff`、`TestLookupFingerprintErrors` |
| 专辑方式每条通过与不通过（抽样不足、暂时失败、sparse、spread、too_many、曲目数与碟数筛选、mismatch、title_artist_mismatch、ambiguous） | `program` 的 `TestB3FingerprintAlbumRules` |
| 单曲方式（读回正式录音、分数、too_many、ambiguous、时长、title_mismatch、占位标题） | `TestB3FingerprintSingleRules` |
| 信息不足、`fingerprint` 三种取值、途中密钥失效或源关闭时回退到搜索 | `TestB3FingerprintReachAndFallback` |
| 不带指纹时与 B3 之前逐字段相同 | `TestB3MatchWithoutFingerprint`（同一组期望在 `main` 上也通过） |
| 三个摘要的基准哈希与 `main` 相同 | `TestB3DigestBaseline` |
| `needsWork` 的 32 种组合与旧状态的推断 | `TestB3FingerprintNeedsWork` |
| 密钥接口（403、两种 400、两种 404、顺序、原因顺序、脱敏、密文用途、清除删行、变化标记） | `TestPostgreSQLB3AcoustIDKeyAPI` |
| 内置应用密钥（B3 补充）：声明只有 user、表单用内置值、被拒只记一次日志并置标记；闸门只列 user、旧 application 行隐藏且清除 404、被拒不标 user、原因 `bundled_key_rejected` | `sources/acoustid` 的 `TestLookupFingerprintBundledKeyForm`、`TestLookupFingerprintBundledKeyRejection`；`TestPostgreSQLB3AcoustIDBundledKeyGate` |
| 回写：出站成功才 `valid`、时间不变、码 6 只标用户密钥、缓存命中不验证、compare-and-set | `TestPostgreSQLB3AcoustIDKeyWriteback` |
| 真实 ffmpeg 与 fpcalc：整轨与 CUE、缓存、内容指纹与 CUE 帧与版本变化后重算、短轨边界、解码失败的冷却与上限 | `TestPostgreSQLB3AcousticFingerprintCache` |
| 经 `app` 装配的一轮：占位专辑对上、补字段、待定候选带分数、两种无效密钥各自标记并回退搜索 | `TestPostgreSQLB3AcoustIDProgram` |
| 指纹工具缺失或自检失败时其余三源、匹配、封面、歌词不受影响 | `TestPostgreSQLB3FingerprintToolUnavailable` |
| 升级：没有密钥或不全时零重做；齐备后只重开该重开的、一次；其余代次与媒体状态不变 | `TestPostgreSQLB3LegacyWorkReopenedOnce` |
| 暂停与扫描时闸门错误照常抛出、不写状态 | `TestPostgreSQLB3FingerprintGatePauseAndScan` |
| 抽取取源函数后音频分析行为不变 | 现有 `TestFFmpegAnalysis*`、`TestPostgreSQLAnalysis*`、`TestAnalysisWorker*` |
| 接口样本：`curation/sources`、`curation/source-key-set`、`curation/source-key-cleared`、`errors/curation-key-not-supported` | `TestPostgreSQLAPIResponseSamples` 与前端 `api-contract.test.ts` |

## 17. D：模型后台判断

### 17.1 数据模型与来源

迁移 0064 给自动整理设置增加 `model`、`model_daily_token_limit` 和独立的 `model_updated_at`。只改模型设置时，程序层的变化标记 `updated_at` 保持原值。按服务器本地日期在 `curation_model_usage` 原子累加输入与输出 token。主模型、独立审查与会话摘要均计入，缺少的计数字段作零。

待定项保存会话、领取时间、未处理次数、重审次数、下次尝试时间和搁置原因。证据摘要变化时，程序可将搁置项重新放回 `waiting_model` 并清零未处理次数。`curation_model_exceptions` 以方案项 ID 唯一，保存待定项、工具输入、预览、模型写入者、审查决定与理由、会话和人工决定。删除会话会清空例外的会话引用，例外仍可读取。0064 回退会删除整理会话、例外和按日用量。

### 17.2 调度、停止与收尾

应用在管家恢复完成后启动 `model.Scheduler.Run(ctx)`。调度器先关闭遗留 model 批次，作废遗留后台方案，将处理中的项放回队列并清除领取与重审信息，保留未处理次数。默认每分钟轮询。启动与每步开始前检查开关、开启者仍是有效管理员、暂停、扫描、MusicBrainz 开关、开启者的管家配置和每日额度。每一步还核对开启者身份与启动时相同。

先处理已到期的重审会话，使用其待审后台方案。方案已作废或归属已变更则释放待定项。新会话归开启者，按创建时间和 ID 领取最多五个到期且发行可见的项，行锁使用 `SKIP LOCKED`。第一条消息只携带程序产生的项 ID、种类与冲突代码。候选和证据经工具的不可信输出返回。模型批次在首次写入时打开，运行收尾时关闭。

| 运行结束原因 | 遗留的处理中项 |
| --- | --- |
| `completed`、`step_limit`、`token_limit` | 未处理次数加一，第三次转为 `shelved/unhandled` |
| `review_unavailable` | 保留方案，依次延迟 5 分钟、30 分钟和 2 小时重审。第三次重审仍失败则作废方案并延迟 24 小时回队 |
| `curation_stopped`、`cancelled` | 作废方案、释放领取，保留未处理次数 |
| `provider_error` | 作废方案、释放领取。调度器从 10 分钟起倍增退避，最长 6 小时 |
| `internal_error`、意外的 `awaiting_confirmation` | 记录异常、作废方案、释放领取 |

### 17.3 工具、审查与写入

后台来源为 `curation`。注册表在列出与调用时检查白名单。可用的目录工具包括 `get_release`、`get_release_evidence`、`get_album`、`get_track`、`search_library` 以及目录的专辑、艺人、艺人专辑与曲目、发行问题列表。音乐资料查询 `search_music_metadata`、`get_music_metadata` 经 MusicBrainz 后台闸门。其余四种工具如下。

| 工具 | 合同 |
| --- | --- |
| `read_pending_items` | 仅读取当前会话领取的项。可传 `item_ids`，结果包含标识、种类、发行、冲突、证据和候选，按不可信数据处理 |
| `resolve_release_match` | 输入 `item_id`、`external_id` 和 `basis.note`。按 ID 读回发行或录音，核对摘要、曲目配对、已有对应与标记。独立审查批准后经程序写入链建立对应、补缺、按强证据归组，记录匹配结论与 `model_matched` |
| `correct_release` | 后台只接受本批发行的设值或留空，锁内核对人工版本，已有人工修正返回 `manual_override`，批外返回 `out_of_batch`。后台写自动层。对话与 MCP 沿用人工修正 |
| `shelve_pending_item` | 输入 `item_id` 与 `reason`，只改变本批待定项状态，记录搁置原因 |

对应的外部源始终是 `musicbrainz`。模型操作的来源为 `curation`，记录会话、审查、`provider_kind=model` 和规范化的模型名（小写、非法字符替换成连字符、最多 64 字节）。依据最多 500 字节，按 UTF-8 边界截断。建立对应使用工具调用的幂等键，补缺与归组按步骤派生键。撤销对应后，以该对应支持的归组按已有归组撤销合同单独处理。

审查策略第 2 版在证据中携带标记为 `program_untrusted_external` 的外部对应与未结束待定项的候选摘要。后台一步第 21 项 shared 提案收到 `too_many_proposals`。没通过与转人工的提案进入持久例外，取舍分别为 `rejected_by_review` 和 `escalated`，模型可继续处理本批其余项。

### 17.4 人工决策

采纳者认领 `open → accepting`，用当前状态重新计算 `preview.changes`。前后值变化返回 `exception_stale` 并重新开放。字段修正经 `CorrectionExecutor` 写人工层，使用当前人工版本，来源包含原审查、会话及 `review_decision=human`。匹配经 `ModelMatcher.Apply` 写对应与自动层，保留模型来源，操作者为采纳者，批次为空。幂等键由例外 ID 和步骤派生。保存 `accepted` 及实际操作 ID。多步匹配在首笔写入前将恢复快照暂存在例外预览的内部字段，接口仅返回公开变化。进程中断后，原采纳者按首笔操作键对账，核对证据摘要与已建立的对应，再以原键续做余下步骤。结论与操作编号在同一事务保存并清除快照。

不采纳字段时按规范化值写不再提出标记。不采纳匹配时按发行或单曲的曲目 ID 写 MusicBrainz 对应标记。标记、`declined` 和待定项收尾在同一事务提交。该项所有例外均有决定后，采纳匹配记 `resolved/human_accepted`。仅拒绝匹配记 `resolved/human_declined`。只有字段例外则回到 `waiting_model`。程序已先解决的项保留程序结论。

### 17.5 管理员 REST 接口与错误码

路径前缀为 `/api/v1/admin/curation`。读写均要求管理员，写接口检查同源。未授权、非管理员、数据库故障分别沿用 401、403、503 合同。

| 方法与路径 | 请求与响应 |
| --- | --- |
| `PUT /settings` | `model` 最多 200 字节，空串使用开启者对话模型。`daily_token_limit` 为 1 至 10¹² 的整数或 null，默认 10000000。省略保留原值。GET、PUT 都返回这两项。无效值返回 400 `invalid_input` |
| `GET /progress` | `model` 段含 `state`、可空 `unavailable_reason`、有效模型、`today.{date,input_tokens,output_tokens}`、`daily_token_limit`、仅统计可见发行的 `items.{waiting_model,in_progress,waiting_human,shelved}` |
| `GET /exceptions` | `status=open\|all`（默认 open）、`page`、`page_size`（最多 100）。返回 `items,total,page,page_size`。包含发行摘要、输入、变化、审查与决定。参数错误返回 400 `invalid_input` |
| `POST /exceptions/{id}/accept` | 返回 `{exception,operation_ids}`。前值变化为 409 `exception_stale`，匹配受开关、资料源或当前可写内容限制时为 409 `curation_disabled`、`source_disabled`、`nothing_to_apply`。外部源暂时失败为 503 `source_unavailable` |
| `POST /exceptions/{id}/decline` | 返回 `{exception}`。两个决定接口均校验 ID 与例外状态，分别可返回 400 `invalid_id`、404 `not_found`、409 `exception_decided` |
| `GET /conversations` | 本管理员的整理会话，返回 `items,pagination`，每项含 `kind=curation` 与 `item_counts`。详情、事件及续聊使用管家会话接口 |

模型状态为 `idle`、`running`、`waiting_review`、`paused`、`disabled`、`waiting_for_scan`、`limit_reached` 或 `unavailable`。不可用原因区分 `not_configured`、`key_needs_reentry`、`tool_unsupported`、`enabler_unavailable`、`source_disabled` 与 `provider_error`。例如开启者未配置管家时，程序仍可继续匹配，模型状态为 `unavailable/not_configured`。达到每日上限后，新的服务器本地日期使用该日累计量重新判断。

样例登记在 `recordCurationSamples`：`curation/settings-model`、`curation/progress`、`curation/conversations`、`curation/exceptions`、`curation/exception-accepted`、`curation/exception-declined` 以及 `errors/{exception-stale,exception-decided,curation-conversation,curation-plan}`。

### 17.6 必需测试与断言点

| 断言点 | 用例 |
| --- | --- |
