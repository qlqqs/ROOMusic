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
| `DELETE /api/v1/admin/curation/suppressions/{id}` | `204`；不存在 `404 not_found` |
| `GET /api/v1/admin/curation/sources` | `{items:[{id, name, capabilities, enabled, active, inactive_reason}]}`，按注册顺序；见第 11 节 |
| `PUT /api/v1/admin/curation/sources/{id}` | `{enabled}`，返回同上的一项；未知的源 `404 not_found` |
| `GET /api/v1/operations` | 增加 `source`（只接受 `curation`）、`provider`、`batch_id`，与现有目标筛选之间是“并且”；有批次的记录带 `batch_id`（没有批次时省略）；`origin` 多出 `provider_kind`、`provider`、`basis` |
| `GET /api/v1/releases/{id}/corrections` 与修正成功的响应 | 每个字段增加 `auto`：`{mode, value?, operation_id, provider_kind, provider}` 或 null；`effective_value` 是三层结果 |

面向成员的接口不变：`corrected_fields` 仍只列人工修正的字段。

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
| 自动值已被更晚的自动整理改写 | `409 revert_conflict` |
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
- **闸门 `curation.Sources`**：后台代码只从它取能力（`ReleaseFinder`、`RecordingFinder`、`ArtistFinder`，参数是源的标识）。
  取到的是包了一层的实现，每个方法先 `Check`，再用 `sources.WithBackground(ctx, permit)` 调用适配器，`permit` 就是对这个源的 `Check`。
  所以核对发生在调用开始时、读缓存之前、每次出站之前（排队之后与每次重试之前）：调用排队或等待重试期间开关被关掉，之后不再出站。
- `Check` 在一个事务里读设置（与 `Writer.Apply` 相同的 `loadSettings`，开启者资格由 `identity.IsEnabledAdmin` 判断）：
  总开关关着或开启者失效返回 `curation_disabled`，源的开关关着返回 `source_disabled`；都不调用适配器，缓存里有结果也不返回。
- **开关只管后台。** 管家对话里的两个 MusicBrainz 工具持有适配器本身，不经过闸门：对话里的查询是用户本人发起的请求，
  未开启自动整理的实例行为不变（见[管家联网合同](./steward-web-contracts.md)的音乐资料一节）。
- 本任务里 `Sources` 没有生产调用方，由测试里的调用验证；子任务 C 的匹配循环是第一个调用方。

## 12. 外部对应的存储（子任务 B1）

**`external_links`**（迁移 0060）：`id`、`source`、`object_type`（`release | release_group | track | artist`）、`object_id`、`external_id`、
`method`、`confidence`（两者只约束形状 `^[a-z0-9_]{1,32}$`，取值表由子任务 C 的把握规则定义）、`operation_id`（可空，延迟外键）、`created_at`；
唯一 `(source, object_type, object_id)`，反查索引 `(source, object_type, external_id)`。

- 表里的每一行都是生效的对应；ADR 0006 所说的“状态”在这里是“行在不在”。一个本地对象在一个源里至多一个。
- 对 MusicBrainz：发行 → release，专辑（本地的 release group）→ release group，曲目 → recording，艺人 → artist。
- `object_id` 不设外键：非测试代码不删除这四类对象。艺人合并靠重定向，被并入的艺人身上的对应仍在——按外部 ID 反查艺人时要解析重定向。
- 扫描、认领、人工修正的语句都不涉及这张表：重扫、认领、改名移动之后对应保持。
- 事务内的函数：`PutLink`（写入或替换，返回被替换的行）、`RemoveLink`、`LinksOf`（按对象）、`ObjectsLinkedTo`（按外部 ID 反查）。
  **它们不是受治理的写入口**：没有生产调用方，子任务 C 把建立对应做成 `operations.Run` 里的操作（记录、撤销、不再提出）时在执行器里调用。

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

视图多了八个连接：目录的四个 EXPLAIN 用例与 `TestPostgreSQLCatalogHTTPPerformance` 是门禁，要设启用参数才会运行
（`ROOMUSIC_CATALOG_EXPLAIN=1`、`ROOMUSIC_CATALOG_EXPLAIN_OUTPUT`、`ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT`），不设时是跳过而不是通过。
