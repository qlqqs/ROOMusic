# 自动整理 A：设计

依据：[ADR 0006](../../../docs/architecture/adr/0006-auto-curation-and-sources.md) 第 4、5、10 节；父任务 `design.md` 第二、三节；
现状见 `research/auto-layer-coupling.md`（下文写作“调研”）与父任务的 `research/overlays-and-operations.md`。
表名、列名与接口路径是本任务的设计；实现时发现与现有代码冲突的，回到本文修订后再改。

## 一、边界

```text
调用方（本任务只有测试；以后是 C 的匹配与 D 的后台运行）
   │  curation.Writer.Apply(发行, 字段们, 来源, 批次, 幂等键)
   ▼
curation（新包）：开关与开启者的核对、不再提出的过滤、批次、按批与按来源撤销
   │  operations.Run(Spec{操作者=开启者, 来源=自动整理, 批次}, 执行器)
   ▼
catalog/governance：自动层的读写、撤销（Kind）、派生刷新      ← 只有它够得到 catalog/internal 下的刷新函数
   ▼
release_auto_values ──► 视图 release_effective（人工修正 → 自动层 → 扫描值）──► 所有读取
```

| 包 | 新增或改动 | 依赖 |
| --- | --- | --- |
| `backend/internal/curation`（新） | 设置、核对、批次、不再提出、写入入口、按批与按来源撤销 | `operations`、`identity`、`catalog/governance`、`platform/*` |
| `backend/internal/curation/httpapi`（新） | 管理员接口 | `curation`、`identity`、`operations`、`platform/*` |
| `backend/internal/catalog/governance` | 自动层的执行器、`Kind`（撤销、列表展示、撤销预览）；修正状态带上自动层 | 不依赖 `curation`；撤销时要写“不再提出”，经 app 注入的接口完成 |
| `backend/internal/catalog` 及其 `internal/` | 自动层的读取；“需要关注”、专辑级署名、署名事实与有效署名、回填成本估算同时看自动层 | 不变 |
| `backend/internal/operations` | 新操作类型；`Origin` 增加字段；`Spec` 带批次；列表按来源与批次筛选；撤销响应的版本号可由类型指定 | 不变 |
| `backend/internal/identity` | 导出“这个 ID 是不是启用状态的管理员”的事务内核对 | 不变 |
| `backend/internal/app` | 装配、注册 `Kind` 与注入的接口、依赖规则表 | — |
| `frontend/src/features/governance`、`features/artists` | 解码与标签接受新的操作类型、来源与名字来源（最小适配，见第九节） | — |

`governance` 不能依赖 `curation`（`curation` 要调用它），而撤销自动修改时要在同一事务里写“不再提出”。做法沿用艺人治理注入
收藏解析刷新的先例（`backend/internal/catalog/governance/artist_state.go:18`）：`governance` 定义一个窄接口，`curation` 实现，`app` 注入。

## 二、数据（迁移 0058，带回退脚本）

### 自动层

`release_auto_values`：形状与 `release_corrections` 相同，约束逐条照搬（字段取值、`set` 或 `null`、标题与专辑艺人不能设空、取值的四种合法组合）。

| 列 | 说明 |
| --- | --- |
| `release_id` | 引用 `releases(id)`，`ON DELETE CASCADE`（与修正表一致；发行从不物理删除） |
| `field` | 八个字段之一；主键 `(release_id, field)` |
| `mode`、`text_value`、`year_value`、`list_value` | 同修正表 |
| `operation_id` | 写入这个值的操作，引用 `operations(id)`，延迟检查 |

`releases` 增加 `auto_revision BIGINT NOT NULL DEFAULT 1`：自动层的版本号，每次自动写入与它的撤销加一。
它只用来满足 `operations` 表“后一版本 = 前一版本 + 1”的约束并给记录排序，**不参与撤销的判断**。
扫描不写它；视图不输出它。自动写入不动 `edit_revision`。

### 视图

重建 `release_effective`：在现有八个修正连接之后再连接八次 `release_auto_values`。

- 标题、专辑艺人、展示艺人：`COALESCE(修正值, 自动值, 扫描值)`；展示艺人跟随专辑艺人的修正与自动值。
- 其余字段：有修正行取修正（设空为空）；否则有自动行取自动（设空为空）；否则取扫描值。
- 列的名字、顺序与类型不变，所以所有读取（`ReleaseSource`）不用改。

连接数从 9 张表变成 17 张。现有的 EXPLAIN 与读取性能测试（`backend/internal/catalog/*_explain_integration_test.go`、
`backend/internal/app/catalog_read_performance_integration_test.go`）是这项改动的门禁；若出现退化，退路是把自动层改成每个发行一行的宽表（一次连接），接口与行为不变。

### 署名与名字

- `artist_credits.layer` 增加 `auto`；`artist_names.origin` 增加 `auto`。
- 署名事实（`artistgraph/facts.go`）：读入自动层的专辑艺人值，生成 `layer='auto'` 的事实，规则与修正层相同。
- 有效署名（`artistgraph/refresh.go`）的层选择改为三选一：有专辑艺人的修正行用 `correction`，否则有自动行用 `auto`，否则 `scan`。

### 操作记录

- `operations_type_ck` 增加 `release_auto_correction`。
- `operations` 增加 `batch_id UUID`（可空，引用 `curation_batches(id)`），加部分索引 `(batch_id, created_at DESC, id DESC) WHERE batch_id IS NOT NULL`。
- 来源仍在 `origin`（JSONB）。按来源筛选用表达式部分索引：`((origin->>'provider'), created_at DESC, id DESC) WHERE origin->>'source' = 'curation'`；
  查询里的表达式与索引逐字一致，测试用同一个函数拼 EXPLAIN（沿用 `records.go` 对认领来源的做法）。

### 批次、不再提出、设置

| 表 | 列 | 说明 |
| --- | --- | --- |
| `curation_batches` | `id`、`tier`（`program` 或 `model`）、`opened_at`、`closed_at`（可空）、`actor_id`（开批次时的开启者） | 改动数量不存列，读取时按 `operations.batch_id` 统计。用量等列由子任务 D 增加 |
| `curation_suppressions` | `id`、`target_type`、`target_id`、`subject`、`value_hash`、`value`（JSONB，展示用）、`operation_id`（产生它的撤销操作，可空）、`created_by`、`created_at`；唯一 `(target_type, target_id, subject, value_hash)` | 本任务只用 `target_type='release'`、`subject=字段名`；结构操作与例外的“不采纳”以后沿用 |
| `curation_settings` | `id`（恒为 1）、`enabled`、`enabled_by`（可空，引用 `users`）、`enabled_at`、`updated_at` | 单行表，迁移里插入初始行（关）。沿用 `scan_schedule` 的做法 |

`value_hash` 是规范化之后的意图（模式加取值）的 SHA-256；规范化用与修正相同的函数，所以“同一个值”的判断与写入一致。

### 回退

回退脚本先删除 `release_auto_correction` 类型的操作及指向它们的撤销记录、`layer='auto'` 的署名事实，把 `origin='auto'` 的名字改回 `scan`，
再恢复约束、删除新表与新列、恢复旧视图。`rollbackFixtures` 补上 58 号的数据（规则见 `.trellis/spec/backend/database-guidelines.md:223-234`）。

## 三、写入

```go
// curation
type Source struct {
    Kind     string // "source" 或 "model"
    Provider string // 外部源的标识或模型名：小写字母、数字与 . _ : -，至多 64 字节
    Basis    string // 依据的摘要，至多 500 字节；展示时按不可信文本处理
}
type Request struct {
    ReleaseID string
    Fields    map[string]catalog.CorrectionIntent // 至少一项；校验规则与人工修正相同
    Source    Source
    BatchID   string // 可空
    Key       string // 幂等键（UUID），由调用方按自己的工作单元确定
    RequestID string
}
type Applied struct {
    OperationID string
    Written     []string          // 实际写入的字段
    Skipped     map[string]string // 字段 → 原因：suppressed、unchanged
}
func (w *Writer) Apply(ctx context.Context, request Request) (Applied, error)
```

一次 `Apply` 是一次 `operations.Run`，事务内的顺序（也是加锁顺序）：

1. `operations.Run` 的幂等锁（操作者加键）。
2. 目录写锁（`writegate`），再锁发行行。发行不存在或不可见返回 `not_found`。
3. 核对设置：读 `curation_settings`（`FOR SHARE`），要求 `enabled`，并且 `enabled_by` 是启用状态的管理员；否则返回 `curation_disabled`（409），
   原因写明是开关关着还是开启者失效。操作者取这里读到的 `enabled_by`。
4. 批次（如有）必须存在、未结束；否则 `batch_closed`（409）。
5. 逐字段过滤：命中“不再提出”的跳过（`suppressed`）；与现有自动值相同的跳过（`unchanged`）。没有剩下的字段时返回 `nothing_to_apply`（409），不产生记录。
6. 写入自动层（`operation_id` 为本次操作），`auto_revision` 加一，调用与人工修正相同的派生刷新（艺人投影与搜索值）。
7. 记录：类型 `release_auto_correction`，目标是发行，版本号用 `auto_revision`；前态记每个被写字段之前的自动值**及写入它的操作**，
   后态记新的自动值，两边都带当时的有效值；`origin = {source:"curation", provider_kind, provider, basis}`；`batch_id` 写入列。

由于操作者要在事务里才确定，`operations.Run` 的幂等锁需要在进入之前知道操作者：`Apply` 先用一次只读查询取 `enabled_by`
作为 `Spec.ActorID`，事务里第 3 步再核对它没有变；变了返回 `curation_disabled`，调用方重试。

指纹含操作类型、发行、各字段的规范化意图与来源的 `provider`（不含 `basis`）。同键同内容重放返回原结果。

**结构隔离**：这个执行器只读写 `release_auto_values` 与 `releases.auto_revision`，不读写 `release_corrections` 与 `edit_revision`。
人工修正的执行器与撤销只读写修正表与 `edit_revision`。扫描的写入语句不涉及新表与新列。测试用断言固定这三点。

人工修正存在时照样可以写自动层（值不生效，清除人工修正后才显示）。自动值等于当前扫描值也算有效写入。

## 四、撤销

### 单条

`release_auto_correction` 注册自己的 `Kind`，走现有的 `POST /api/v1/operations/{id}/revert`，通用守卫不变（已撤销、撤销不能再撤销）。

`Revert`：取目录写锁并锁发行行（**不要求发行可见**，这样已经不可见的发行上的自动值也撤得掉）；对原操作写过的每个字段，
核对自动层这一格的 `operation_id` 仍是原操作：

- 全部是：逐格恢复成前态记的自动值与**原来的写入者**（前态没有值就删除这一格）；`auto_revision` 加一；刷新派生；
  经注入的接口为每个被撤的值写“不再提出”（重复的忽略）。
- 任何一格不是：整条拒绝，返回 `revert_conflict`，说明里带改写它的操作 ID。不做部分撤销。

不读 `edit_revision`，所以之后的人工修改不妨碍撤销；恢复写入者使得“先撤后写的、再撤先写的”成立。

`Describe`：改动摘要 `{kind:"release_auto_correction", fields:[{field, before:{auto, effective}, after:{auto, effective}}]}`；
`Revertible` 是“每一格仍是这条操作写的”。撤销预览（`RevertPreviewer`）给出撤销后各字段的有效值。

撤销响应里的 `revision` 现在由通用代码填成记录的后一版本。自动类型的版本号是 `auto_revision`，而前端把发行类撤销的 `revision`
当作发行的人工修改版本号；`Outcome` 增加一个可选的“响应里的版本号”，自动类型填发行当前的 `edit_revision`（没有变）。

### 按批、按来源

`curation.RevertScope(ctx, actor, scope, cursor, key)`，每次调用处理至多 100 条：

- 范围：`batch`（批次 ID）或 `provider`（来源标识）。候选是范围内 `origin.source='curation'`、不是撤销、还没被撤销的操作，
  按 `(created_at, id)` 倒序，从 `cursor` 之后开始。
- 逐条调用 `operations.Run(撤销)`，每条一个事务、独立成败。操作者是发起请求的管理员。每条的幂等键由请求的键与操作 ID 派生（UUIDv5），
  同一请求重试不会重复撤销，已成功的重放原结果。
- 返回 `{reverted, skipped:[{operation_id, code}], next_cursor}`；`next_cursor` 为空表示做完。客户端带着它继续调用。

它没有自己的状态表：每条撤销本身是一条受治理的操作记录。倒序保证同一格上后写的先撤，批内与跨批都成立。
范围按来源而不是按操作类型取候选，以后 C、G、H 加入的结构类自动操作（有自己的 `Kind`）不用改这里。

## 五、不再提出

- 写入：自动修改被撤销时（单条或成批）。键是（发行、字段、值的哈希）。
- 读取：`Apply` 第 5 步。只挡同一个值。
- 管理员可以列出（可按发行筛选，分页）与逐条清除。清除后同一个值可以再次自动写入。
- 子任务 D 的“不采纳”调用 `curation` 导出的同一个写入函数。

## 六、开关

- `GET /api/v1/admin/curation/settings`：`{enabled, enabled_by:{id, username}|null, enabled_at, active, inactive_reason}`。
  `active` 是“现在自动写入会不会被接受”；`inactive_reason` 为 `disabled`、`enabler_unavailable` 或 null。
- `PUT` 同一路径 `{enabled}`：同源加管理员。打开时 `enabled_by` 记为调用者；关闭时保留 `enabled_by` 作为记录。
- 关闭不动已有的自动层。

现在代码里管理员只有初始化时创建的那一位，也没有修改角色的入口，并且最后一位管理员不能被停用（调研第 8 节）。
“开启者失效”因此目前到不了；核对仍然按 ADR 写（一条查询的事），测试里直接改用户行来验证，不为它另做界面或流程。

## 七、读取

| 接口 | 权限 | 说明 |
| --- | --- | --- |
| `GET /api/v1/operations` | 管理员 | 增加查询参数 `source`（目前只接受 `curation`）、`provider`、`batch_id`，与现有目标筛选之间是“并且”；列表项增加 `batch_id`（可空）；`origin` 多出 `provider_kind`、`provider`、`basis` |
| `GET /api/v1/admin/curation/batches` | 管理员 | 分页，倒序；每项 `{id, tier, opened_at, closed_at, operations, reverted}`，数量按记录统计 |
| `GET /api/v1/admin/curation/batches/{id}` | 管理员 | 同上一项的单个对象；改动明细用操作记录列表按 `batch_id` 取 |
| `POST /api/v1/admin/curation/reverts` | 管理员、同源、幂等键 | `{scope:{kind, id|provider}, cursor}`，见第四节 |
| `GET /api/v1/admin/curation/suppressions`、`DELETE …/{id}` | 管理员 | 见第五节 |
| `GET /api/v1/releases/{id}/corrections` | 管理员 | 每个字段增加 `auto`：`{mode, value, operation_id, provider_kind, provider}` 或 null；`effective_value` 已是三层结果 |

`GET /api/v1/releases/{id}/corrections` 是唯一改动返回内容的现有接口（每个字段多一个键）。面向成员的接口不变：
`corrected_fields` 仍只列人工修正的字段。

把“没有修正”当成“显示扫描值”的三处预览（修正撤销预览、管家 `correct_release` 的清除预览、修正状态）改为“显示自动值，没有才是扫描值”。
认领详情里修正旁边的 `scan_value` 不改（它说明的是旧发行的扫描值）。

## 八、“需要关注”与其他读取处

- `releasesql.FieldIssueRows`：扫描拿不准的字段，有修正行**或有自动行**时不计入；展示艺人仍映射到专辑艺人。
- `catalog.LoadReleaseCredits`：专辑级署名里专辑艺人一行的替换，按有效的专辑艺人（修正或自动）。
- `maintenance` 的回填成本估算：专辑艺人的自动值与修正值同样计入。
- 认领不搬自动层：旧发行上的自动值留在旧发行（不可见）；新发行由以后的匹配自己得到。认领的判定、令牌与撤销不变。
- 审查证据与管家的读取工具经视图读有效值，自动得到三层结果，不改。管家的写工具仍只写人工修正。

## 九、前端的最小适配

前端的解码是严格的：未知的操作类型、`changes.kind`、`origin.source` 会让操作记录页整页解码失败
（`frontend/src/features/governance/api/decoders.ts:208-291`、`:296-302`）。虽然本任务之后生产里还没有自动操作，
解码与标签随后端合同一起改，避免子任务 C 一合入页面就坏：

- 操作类型增加 `release_auto_correction`，标签“自动整理”；改动摘要按字段列出前后值（复用修正的展示）。
- 来源增加 `curation`，标签为“自动整理 · <来源标识>”。
- 艺人别名的来源增加 `auto`。
- 修正状态的解码接受每个字段的 `auto`；编辑界面暂不展示（E1 做）。

不新增页面与入口。

## 十、取舍

| 取舍 | 选择 | 放弃的做法与原因 |
| --- | --- | --- |
| 自动层的形状 | 与修正表相同的“每格一行” | 每发行一行的宽表：连接少，但校验、读写与撤销都要另写一套；留作性能退路 |
| 自动操作的版本号 | `releases.auto_revision` | 放宽 `operations` 的版本约束：约束对所有类型成立更简单，新列也不参与任何判断 |
| 批次与来源放哪 | 批次是列，来源在 `origin` 加表达式索引 | 都做成列：`origin` 已经是来源的唯一位置，重复存放会不一致 |
| 按批撤销的形态 | 无状态、分段、逐条独立的撤销 | 后台作业加进度表：现在没有通用作业机制，批次有上限时分段调用足够 |
| 撤销遇到被改写的值 | 整条拒绝 | 部分撤销：一条操作至多撤销一次，部分撤销之后剩下的再也撤不了 |
| 三层值从哪读 | 扩展修正状态接口 | 另开一个接口：同一个编辑界面要同时看三层，分两处读容易不一致 |
| 开启者失效的界面 | 不做 | 目前到不了这个状态 |

## 十一、风险

- 视图连接数翻倍可能让某些列表查询的计划变差；门禁与退路见第二节。
- 有效署名的层选择是按“行是否存在”整层切换的：自动值是合辑占位名时发行级有效署名为空，与修正层现有行为一致，测试里固定。
- `operations.Run` 需要在事务外先知道操作者（幂等锁的键），与事务内核对之间有一个很短的窗口；第三节用“事务内再核对一次”关上。
- 迁移的回退要删除自动类型的操作记录，属于有损回退；只在版本回退时发生，回退说明里写明。
