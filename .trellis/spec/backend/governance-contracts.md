# 受治理操作合同

## 1. 范围与触发

M3（2026-10）让管理员能修正扫描得到的发行信息、调整版本归属的专辑，并撤销自己的修改。新增或修改以下内容时必须阅读并更新本文件：
操作记录表 `operations`、共同写入流程 `operations.Run`、可修正字段与修正表 `release_corrections`、发行的人工修改版本号
`edit_revision`、有效值视图 `release_effective`、人工归组、撤销、操作记录列表。读取投影（列表、详情、按组读取）的通用规则仍在
[Catalog REST 合同](./catalog-rest-contracts.md)；扫描的写入不受本文件约束，但**必须保持不碰本文件的表和列**（第 7 节）。
M4 的第一项（2026-10）在同一流程上增加以专辑为目标的“设定默认版本”，它自己的规则（组的版本号、失效规则、执行器、撤销与对比接口）在
[默认版本与版本对比合同](./default-version-contracts.md)；本文件只记它对共同流程的扩展（目标类型、撤销响应、列表筛选）。
M4 的第二项（2026-10）增加以发行为目标的“核对问题”（与修正、归组共用发行的版本号），规则在[元数据问题与核对合同](./issue-review-contracts.md)。
M4 的第三项（2026-10）增加“认领”：把不可见旧发行上的修正、归组、默认版本与核对迁给新发行，规则在[认领合同](./claim-contracts.md)。
M7 子任务 A（2026-11）增加**自动层**：后台自动整理写的发行字段值排在人工修正之下、扫描值之上，有自己的操作类型 `release_auto_correction`、
来源 `curation`、批次与撤销规则，全部在[自动整理合同](./curation-contracts.md)；本文件只记它对共同流程、有效值与列表的扩展。

决策依据：[ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md)、[ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md)。

## 2. 数据模型（迁移 0017）

- **`operations`**：每次**成功提交**的受控操作一行（修正、归组、默认版本、核对、认领、撤销）。列：`id`、`actor_id`、`operation_type`
  （`release_correction | release_group_change | release_group_default | release_issue_review | release_claim | revert`，`release_group_default` 由迁移 0018、`release_issue_review` 由 0019、`release_claim` 由 0020 加入）、`target_type`（`release`；0018 加入 `release_group`，即专辑）、
  `target_id`（不设外键：记录可能比目标活得久）、
  `idempotency_key`、`fingerprint`、`expected_revision`、`result_revision`、`before_state`/`after_state`（执行器定义的类型化前后状态，撤销的依据）、
  `response`（原样返回客户端，也是同键重放的响应）、`reverts_operation_id`、`request_id`、`created_at`、`origin`（0054，可空 JSONB）。
  0058 加入类型 `release_auto_correction` 与可空的 `batch_id`（自动整理的批次，见[自动整理合同](./curation-contracts.md)）；0061 加入 `release_external_artwork`、`track_external_lyrics`；
  0062 加入 `external_link`（自动整理建立的外部对应，同一合同第 15 节）。
  约束都有名字：`operations_revision_ck`（`result = expected + 1`）、`operations_reverts_ck`（只有撤销记录指向原操作）、
  `operations_actor_key_uq`（`(actor_id, idempotency_key)`）、`operations_reverts_uq`（一条操作至多被撤销一次）。
- **`releases.edit_revision`**（`BIGINT NOT NULL DEFAULT 1`，`> 0`）：发行的人工修改版本号。**只有受控操作（修正、归组、核对、撤销）递增，扫描从不改它。**
  字段修正、归组与核对共用同一个号，所以撤销规则只有一句话：只能撤销这个发行**最近一次**修改。
- **`release_corrections`**：当前意图，主键 `(release_id, field)`；无行即无修正。`set` 钉住值（即使等于扫描值），`null` 设空（标题、专辑艺人不可）。
  年份 1–9999，其余文本非空且 ≤4096 字节；0032 起流派用 `list_value TEXT[]`，见[多流派合同](./genre-contracts.md)。
  `operation_id` 引用设下意图的操作，外键 `DEFERRABLE INITIALLY DEFERRED`（执行器后插入记录）。
- **`release_effective`**（视图）：列与 `releases` 同名，可修正字段换成有效值：`title`、`album_artist`、`year`、`genres`、`label`、`catalog_number`、
  `edition`、`barcode` 取修正值（`null` 为空）；没有修正时取自动层的值（0058，同样 `null` 为空），两者都没有才取扫描值；
  展示艺人 `artist` 跟随专辑艺人的修正与自动值；另带 `edit_revision`；其余列原样。
  每个字段在修正表与自动层的主键上各一次 `LEFT JOIN`，不被引用的列对应的连接会被优化器去掉。
- 可修正字段固定为八个，顺序固定：`title`、`album_artist`、`year`、`genre`、`label`、`catalog_number`、`edition`、`barcode`
  （后端 `catalog.CorrectableFields` 与前端 `CORRECTABLE_FIELDS` 一致，样本契约测试守住）。
- 组（`release_groups`）在 M3 没有自己的字段（M4 起有默认版本与专辑的版本号，见[默认版本合同](./default-version-contracts.md)），**从不删除**：
  撤销归组要回到原来的组 ID，空组的地址随之恢复。

## 3. 共同写入流程 `operations.Run`

`operations` 包只含流程与记录，不含业务表、规则与 HTTP；业务由拥有表的包提供的执行器完成。依赖方向：`operations` 只依赖 `platform/uuid`；
`catalog/governance` 与 `catalog/httpapi` 依赖 `operations`；`operations/httpapi` 不导入 catalog/governance（catalog/governance 的能力经 `operations.Kind` 由 `app` 注入）。

```text
handler：同源 → 管理员 → 校验 ID、Idempotency-Key（UUID）、请求体 → 计算指纹
  → operations.Run(ctx, db, Spec{ActorID, Type, Key, Fingerprint, RequestID}, executor)
       BEGIN（READ COMMITTED）
       pg_advisory_xact_lock(类别 'OPER', fnv32(actor:key))          同一操作者同一键的请求串行
       有记录且指纹相同 → 重放已存响应；指纹不同 → 409 idempotency_conflict
       executor(tx, 操作 ID) → Outcome{目标、预期/结果版本、前后状态、响应} 或分类错误    业务行写入时就带上操作 ID
       INSERT INTO operations …；COMMIT                              业务变化与记录一起提交或一起回滚
```

- 幂等键作用域是**同一操作者**（跨操作类型）。指纹是规范化 JSON 的 SHA-256，含操作类型、目标 ID（小写）、预期版本与完整语义
  （`set`/`null`/`clear` 不同则指纹不同）。同键同义重放得到同样的状态码与响应，不重新执行。
- 只记录成功提交的操作；被拒绝（403、404、409）与失败的尝试不写记录（与目录操作一致）。
- 错误经 `operations.Problem(err)` 映射：`*Failure` 按类别 400/404/409；连接类错误（上下文取消、网络、PostgreSQL `08`/`53`/`57`/`58`）为
  `ErrUnavailable` → `503 database_unavailable`（结果不确定，用**同一个键**重试即可对账）；其余（约束、触发器）为 `ErrOperationFailed` → `500 operation_failed`。
  500 与 503 在 handler 记日志，响应不含内部细节。
- `Kind` 接口（每种操作类型一个，由拥有业务表的包实现并经 `app` 注册；目前是修正、归组、默认版本、核对与认领五种）：`Type()`、`Revert(ctx, tx, operationID, original)`、
  `Describe(ctx, db, records)`（批量补充列表展示：目标当前状态——发行目标给 `Release`、专辑目标给 `Album`——、改动摘要、`Revertible`）。`RevertExecutor(kind, originalID)` 负责通用部分：
  先**单独一条语句** `SELECT … FOR UPDATE` 锁住原记录，再读取它（READ COMMITTED 下带连接的 `FOR UPDATE` 只重检被锁的行，连接来的行仍取自语句快照，
  读不到刚提交的撤销记录），所以对同一条记录的并发撤销串行，后到的得到 `already_reverted`。
- C6 的 `Spec.Origin *Origin` 可选，形状为 `{source:chat|mcp,conversation_id?,plan_id?,mode?,token_id?}`；
  M7 A 增加 `{source:curation,provider_kind,provider,basis}`，同时 `Spec.BatchID` 写入 `operations.batch_id`。
  执行器可以用 `operations.ActorID(ctx)` 取本次 `Run` 的操作者；`Outcome.ResponseRevision` 非零时作为撤销响应的 `revision`；
  `Outcome.AfterCommit` 非空时，`Run` 在事务成功提交之后调用它一次（同键重放与失败时不调用），用于清理数据库之外的东西（外部封面的文件）。
  只由后端入口构造，不参与指纹；旧 HTTP 调用不传时列为 SQL NULL，行为不变。对话确认写含 plan_id；直接执行不含 plan_id；MCP 含 token_id。
  `operations.Record.Origin`、列表接口和 list_operations 输出 origin（旧记录为 null）。同键重放仍返回原操作，不覆盖原来源。
- 方案项独立进入 Run；幂等键由后端生成并保存，同项恢复执行复用原键。展示依据 basis 不进入指纹。
  方案、审批、模式与新增错误码见[管家运行时合同](./steward-runtime-contracts.md#c6-共享写入与确认合同)。

## 4. 接口

所有写接口：同源（403）→ 管理员（401/403）→ 路由 ID 与 `Idempotency-Key` 校验（`400 invalid_id` / `invalid_input`）→ 请求体（`400`）→ `Run`。
读接口：管理员（401/403）→ 参数 → 读取。非管理员读与写都是 `403 forbidden`。

### 4.1 修正

- `GET /api/v1/releases/{id}/corrections`：`{release_id, revision, fields:[{field, scan_value, correction, effective_value, uncertain}]}`，八个字段固定顺序；
  `correction` 为 `null`、`{mode:"set", value}` 或 `{mode:"null"}`；年份为整数，流派为非空数组或 null，其余为字符串。流派 `uncertain` 恒为 false；其他字段表示扫描处理是
  `uncertain_apply`（专辑艺人还看 `artist` 字段）。专辑艺人的 `scan_value` 是扫描的专辑艺人，没有时是扫描的展示艺人（即页面显示的值）。
  发行不存在或没有可见曲目 `404 not_found`。
- `PATCH /api/v1/releases/{id}/corrections`：`{expected_revision, changes:{<field>:{mode:"set",value}|{mode:"null"}|{mode:"clear"}}}`。
  没写的字段不变；`clear` 删除修正、回到**当前**扫描值；不改变状态的条目（没有修正时 `clear`、与现有修正相同的 `set`）忽略，全部被忽略 `400 no_changes`。
  文本规范化（去首尾空白、内部连续空白折叠）后不能为空（清空用 `null`）、无控制字符、不超过 4096 字节；年份 1–9999；
  未知字段、非法修改方式、标题与专辑艺人设空都是 `400 invalid_correction`（带可直接展示的说明）。
  执行：`SELECT … FROM releases WHERE id = ? FOR UPDATE`，发行须存在且可见（至少一条 `present` 曲目），`edit_revision = expected_revision`
  （否则 `409 revision_conflict`），写或删修正行，`edit_revision + 1`。成功返回与 `GET` 相同的形状加 `operation_id`。
  “确认扫描值”就是 `set` 为当前扫描值：钉住它，并让该字段不再计入“需要关注”。

### 4.2 人工归组

- `POST /api/v1/releases/{id}/group`：`{expected_revision, action:"join", group_id}` 或 `{expected_revision, action:"detach"}`
  （`detach` 带 `group_id`、非法动作、非 UUID 的目标都是 `400 invalid_input`）。
- 先锁发行行（存在、可见、版本匹配），再按 UUID 顺序锁涉及的组行（避免死锁）。`join`：目标组存在且至少有一个可见版本（不含自己），
  否则 `404 group_not_found`；目标就是当前组 `409 same_group`。`detach`：当前组除自己外没有可见版本 `409 already_alone`，否则新建组。
  更新 `releases.group_id`，`edit_revision + 1`。
- 返回 `{release_id, group_id, previous_group_id, revision, operation_id}`。前后状态是组 ID，另存当时两边代表版本的标题快照供记录显示。

### 4.3 撤销与操作记录

- `POST /api/v1/operations/{id}/revert`（没有请求体）：先在事务外读一次记录只为按类型找到 `Kind`（不存在 `404 not_found`；类型未注册或本身是撤销
  `409 not_revertible`），其余判断都在 `Run` 里锁住原记录之后做：本身是撤销 `409 not_revertible`，已被撤销 `409 already_reverted`，
  目标的人工修改版本号（发行或专辑各自的 `edit_revision`）不等于记录的 `result_revision` `409 revert_conflict`，目标当前不可见 `404 not_found`。
  成功写一条 `revert` 记录（`reverts_operation_id` 指向原记录），返回 `{operation_id, reverts_operation_id, target_type, target_id, revision}`
  （M4 起；原来的 `release_id` 只适用于发行，开发阶段不做兼容）。指纹是 `{type: revert, operation_id}`。
- `GET /api/v1/operations?page=&page_size=&release_id=&group_id=`：按时间倒序（再按 ID 倒序）分页，默认 50、最大 100（`400 invalid_pagination`），
  可按发行（`release_id`）或专辑（`group_id`，M4）筛选，非 UUID 都是 `400 invalid_id`；**两个都给时取并集**（这个版本的记录加上这张专辑的记录，
  分页与 `total` 针对并集），`operations.List` 的参数相应是目标列表 `Targets []Target`。每条：`{id, type, created_at, actor:{id, username},
  release:{id, title, group_id, visible}|null, album:{id, title, visible}|null, changes:{kind, …}|null, reverts, reverted_by, revertible, origin}`。
  发行目标的记录 `release` 是目标**当前**的有效标题、所在组与可见性、`album` 为 `null`；专辑目标的记录反过来（目标已不存在时两者都是 `null`）。
  `changes` 是改动摘要（`release_correction`：每个字段的前后修正意图与当时的有效值；`release_group_change`：前后组 ID 与当时的组标题；
  `release_group_default`：前后的默认版本快照，见[默认版本合同](./default-version-contracts.md)；`release_issue_review`：问题与前后是否有核对，
  见[核对合同](./issue-review-contracts.md)；`release_claim`：来源版本与迁移、保留的各类，见[认领合同](./claim-contracts.md)），
  撤销记录的前后状态与它所撤销的操作相反；记录里的状态损坏时 `changes` 为 `null`，不让列表失败。
  `revertible` = 未被撤销 ∧ 本身不是撤销 ∧ 目标可见 ∧ 目标的 `edit_revision` 等于记录的 `result_revision`
  （`release_auto_correction` 看每个字段的当前写入者；艺人合并、改名、忽略候选按第 6 节判断）。展示信息由 `Kind.Describe` 按类型批量查询，不逐条查询；
  操作者用户名经 `identity.Guard.Usernames` 批量取。响应不含请求体、前后状态原文、路径与密钥。
- M7 A：列表另接受 `source=curation`、`provider`、`batch_id`，与目标筛选之间是“并且”（无效值 `400 invalid_input`，`batch_id` 非 UUID `400 invalid_id`）；
  属于批次的记录带 `batch_id`，不属于的省略这个键，所以既有样本不变。
- 记录永久保留，没有清理，不提供“回滚到某点”。

## 5. 有效值投影

- 所有 catalog 读取（列表、详情、搜索、排序、分页、按组读取、代表版本规则、最近播放的曲目位置）的发行来源是常量
  `releasesql.ReleaseSource = "release_effective releases"`（别名仍叫 `releases`），所以展示、排序与搜索用的都是有效值。扫描与身份规划读写 `releases` 本表。
- **无修正、无自动值时与旧行为逐项一致**（既有视图与列表样本测试守住）。
- **需要关注**：`uncertain_apply` 的字段决定如果该字段有修正（`set` 或 `null`）或有自动值就不计数，`artist` 由专辑艺人的修正或自动值覆盖；分组原因与缺失曲目照旧计数；
  清除修正、撤销自动值后扫描的不确定重新计入。M4 第二项起被仍然有效的核对压下的问题也不计数（[核对合同](./issue-review-contracts.md)第 3 节）。
- **制作人员**：详情的专辑级署名里，专辑艺人有修正或自动值时用有效的专辑艺人替换扫描写入的 `album_artist` 署名。
- **证据**：发行详情增加 `revision` 与 `corrected_fields`（有修正的字段名，固定顺序，没有时是空数组，所有登录成员可见，不含修正值的来源与操作者）；
  证据摘要与管理员完整证据照旧写扫描结论，不把人工选择伪装成高置信标签。

## 6. 撤销规则

- 撤销是一次新操作，恢复的是前态的**意图**（没有修正、设值或设空；归组回到原组），不把历史有效值写回扫描列：因此撤销“从没有修正到设值”之后，
  显示的是**现在**的自动值，没有自动值时是现在的扫描值（重扫之后可能已不是修改前那一刻的值）。
- 只能撤销发行最近一次修改（`edit_revision` 相等）；撤销本身也递增版本号、写一条记录，**不能再被撤销**，需要时重新修改。没有链式恢复。
- 撤销只恢复这条记录改动过的字段，不碰其他字段。
- 自动层的写入与撤销不递增 `edit_revision`，所以不占用“最近一次修改”：自动写入之后，之前的人工修改仍可撤销。自动修改自己的撤销规则
  （按写入者判断、按批与按来源、撤销后不再提出）见[自动整理合同](./curation-contracts.md)第 5 节。
- 归组的撤销仍按版本号规则；M7 C 起 `GroupChangeOperations(suppressions)` 必须传入“不再提出”的写入器：撤销来源为 `curation` 的归入时，
  同一事务里写 `group_join` 标记，后台不再把这个发行归入同一个专辑；人工归组的撤销不写。归组记录的后态多了 `action`（`join | detach`），之前的记录没有它。

- 艺人合并、改名及忽略候选按意图和涉及艺人的后续操作判定，支持倒序撤销。列表批量判断与事务内的检查共用同一规则。撤销时先锁原记录，为将重新引用的历史操作取 `FOR KEY SHARE`，再取 CATP 和 UUID 升序的艺人行锁。迁移 0066 将 `operations.created_at` 默认值改为插入时的 `clock_timestamp()`，使持 CATP 写入的操作按实际写入顺序比较。回退恢复 `now()`。恢复细目与自动合并标记见[艺人合同](./artist-contracts.md)。

## 7. 与扫描的关系

- 扫描只更新 `releases` 的扫描值与证据，不碰 `release_corrections`、`release_auto_values`、`edit_revision`、`auto_revision`、`operations`、`releases.group_id`（既有发行），也不更新组行（M4 的默认版本列）：
  修正按视图继续生效、人工归组保持，这是结构保证而不是约定（真实重扫用例守住，见第 9 节）。
- 扫描值在打开编辑框与提交之间变化不算冲突：`set` 与 `null` 的结果与扫描值无关；`clear` 的结果是提交时的自动值或扫描值，响应返回实际有效值。
- **已知限制（G8）**：成员关系不连续而新建发行时（整张替换等），新发行按扫描值显示、单独成组；旧发行的修正与归组留在旧发行上（不可见但保留，记录里 `visible: false`），
  不自动继承；M4 第三项起管理员可以把这些修改认领给新发行（预览、撤销，见[认领合同](./claim-contracts.md)），认领之前它们一直留在旧发行上。
- 并发：修正事务只锁发行行与自己的修正行，归组事务锁发行行与组行（按 UUID 顺序），设定默认版本同样先锁发行行再锁组行，核对只锁发行行
  （撤销时若要写回的前态有核对行，先给它引用的更早记录加 KEY SHARE 锁，见[核对合同](./issue-review-contracts.md)第 5 节），认领先锁记录、再锁两个发行行、最后锁组行（见[认领合同](./claim-contracts.md)第 6 节），
  M5 扫描反馈会先锁全部既有发行（UUID 升序），用独立语句重读归属，再对涉及组升序取 `FOR SHARE`；仍不锁修正或 operations。持组后不得补发行锁。反馈专用锁在savepoint内限定500ms，超时或集合失效回滚并释放新增锁，扫描报告降级而核心继续；集合失效不重试。封面与missing遵循同序，终态额外先持scan_runs行锁。并发矩阵与故障边界见 [扫描反馈合同](./scan-feedback-contracts.md)。

## 8. 错误码

| 条件 | 响应 |
| --- | --- |
| 未登录 | `401 unauthorized` |
| 跨源写入 / 非管理员 | `403`（同源检查在鉴权之前） |
| ID 不是 UUID / 缺少或非 UUID 的 `Idempotency-Key` / 请求体非法 | `400 invalid_id` / `invalid_input` / `invalid_json` |
| 修正值非法、标题或专辑艺人设空、未知字段 | `400 invalid_correction` |
| 全部条目不改变状态 | `400 no_changes` |
| 分页参数无效 | `400 invalid_pagination` |
| 发行不存在或不可见；记录不存在 | `404 not_found` |
| 归入的目标组不存在或没有可见版本 | `404 group_not_found` |
| 归入自己所在的组 / 移出已经独立的版本 | `409 same_group` / `409 already_alone` |
| 预期版本过期 | `409 revision_conflict` |
| 同键不同请求 | `409 idempotency_conflict` |
| 撤销的是撤销记录或不支持撤销的类型 / 已被撤销 / 之后又有修改 | `409 not_revertible` / `already_reverted` / `revert_conflict` |
| 数据库不可用（结果不确定） | `503 database_unavailable` |
| 业务变化或记录没能写入，已整体回滚 | `500 operation_failed` |

默认版本错误见[默认版本合同](./default-version-contracts.md)。

## 9. 必需测试与断言点

- `internal/operations`：指纹与错误映射单元测试；PostgreSQL 集成——记录/重放/同键异义、并发同键只执行一次、执行失败与记录写入失败整体回滚、
  版本契约（`result = expected + 1`）、延迟外键、撤销记录互相指向、列表排序分页筛选（含发行与专辑的并集）、撤销执行器的守卫与对同一条记录的并发撤销、
  撤销响应的目标类型。
- `internal/catalog`、`internal/catalog/governance`：视图无修正时与表逐列相同、逐字段叠加与清除、修正行的约束拒绝非法状态、读取返回有效值（含署名与“需要关注”）；
  修正状态迁移（改值/设空/清除、清除后回到重扫值）、拒绝过期/不可见/不存在、确认扫描值消除关注、各处读取一致；
  归组的归入/移出/非法请求无副作用/并发不死锁；撤销恢复每种前态、只动改过的字段、清除后显示现在的扫描值、之后又有修改或目标不可见时被拒绝、
  `Describe` 的目标/改动/可撤销。
- `internal/app`（经路由与鉴权的 HTTP 层）：401/403/400 无副作用、成功路径、幂等重放与冲突、并发请求；**修正在真实重扫之后仍生效并显示在各处**；
  **人工归组在真实重扫之后保持**；操作记录只有管理员可读；撤销接口的各种 409/404；依赖规则测试覆盖两个新包。
- 前端：解码器对后端样本的往返契约；`field-draft`、`change-summary`、`operations-search` 单元测试；各对话框与页面的 Vitest；
  Playwright 的五种宽度、axe 与纯键盘；真实服务用例见第 10 节。
- 运行 `go test -race ./internal/operations/...`、`go test ./internal/catalog/... ./internal/app/... -count=1`（PostgreSQL 集成需隔离 PostgreSQL 18，
  见[质量规范](./quality-guidelines.md)）。

## 10. 真实服务用例

`frontend/e2e/real/governance.real.ts` 经 `scripts/playback-e2e.sh` 使用隔离服务、数据库与合成 WAV。
断言：重扫不覆盖人工修正；撤销后回到当前扫描值；归组重扫后保留且可撤销；成员读写均 403。
不依赖其他用例素材，结束只清理本用例文件。来源与确认另由 C6 的隔离测试覆盖；未执行的用例不得计为通过。

## 11. 边界与后续

- M3 **没有**审批队列、editor 角色、“回滚到某点”、扫描基值版本（ADR 0002 的证据绑定针对待审批提案）、物化有效值表（修正行很少，透视代价是主键上的小查询）。
- 成员写操作（创建、停用/启用、撤销会话）**不在 M3**：暂不要求幂等键、不写操作记录；原计划的接入任务已于 2026-11-03 撤销（R4），
  见[身份与成员合同](./identity-contracts.md)。
- M4：默认版本已作为第一项实施（[默认版本合同](./default-version-contracts.md)），问题处理作为第二项实施（[核对合同](./issue-review-contracts.md)），认领作为第三项实施（[认领合同](./claim-contracts.md)）；
  2026-11-03 起整组合并/拆分搁置（2026-11-22 恢复立项，尚未实现，见 [ADR 0006](../../../docs/architecture/adr/0006-auto-curation-and-sources.md)）、扫描反馈移到 M5 之后；M6 C6：来源与执行模式已在可空 origin 中记录。
- **新写操作的轻量标准**（2026-11-03，R4，只有一个管理员）：接入 `Run`（记录与幂等是现成的）；有反向操作可以恢复的类型不提供撤销——`Kind.Revert` 返回
  `409 not_revertible`，`Describe` 的 `Revertible` 恒为假；预览令牌只在预览依赖版本号覆盖不到的状态时才加。已实施的五种类型不变。艺人合并、改名与忽略候选按 ADR 0006 第 7 节提供撤销。见[路线调整](../../../docs/architecture/core-1-roadmap.md#2026-11-03-路线调整)。
- 前端合同见[状态规范](../frontend/state-management.md)、[组件规范](../frontend/component-guidelines.md)与[前端目录](../frontend/directory-structure.md)。

## M5 ④搜索投影挂接

扫描候选及修正/清除/撤销/claim 双方在同事务刷新目标代次搜索投影；CATP 先于业务行锁，失败回滚，后台持锁重读避免覆盖新值。扫描不写治理表，missing 可见性读取实时过滤。详见[搜索合同](./search-contracts.md)。

## M5 ⑤艺人治理增量

新增 `target_type=artist` 与 `artist_merge/artist_split/artist_rename/artist_candidate_ignore`，全部经 `Run`，拆分不可撤销；双资源revision、名字/发行快照basis、旧ID恢复、候选忽略按名字键对，详见[艺人合同](./artist-contracts.md)。`operations_artist_targets`同事务记录主/次目标，artist_id与release_id/group_id筛选取并集。Describe按页批量补当前艺人，整理期保留历史摘要，坏state仅changes=null。

app注入 `RefreshArtistResolution(ctx,tx,mapping)`；治理不得直接访问personal表。艺人新写未ready为409 artist_catalog_not_ready，既有成功幂等重放仍返回原结果。修正/清除/撤销、claim双方、归组在原CATP事务中刷新艺人有效署名与搜索，扫描不改四张人工意图表或edit_revision。新HTTP与schema验证必须覆盖准备态、revision/幂等、scope冲突、部分写失败回滚和拆分不可撤销。

I1 来源读取仍遵循[艺人合同](./artist-contracts.md)，不改治理记录或 revision。

## 曲目修正（人工层）

管理员通过 `GET /api/v1/tracks/{id}/corrections` 读取一首在库曲目的 `track_id`、当前 `release_id`、曲目人工版本 `revision` 和 `fields`。字段按 `title`、`artist` 排列，每项含 `field`、`scan_value`、`correction`、`auto`、`effective_value`、`uncertain`。后者取扫描观察的推断标记。没有意图时对应层返回 JSON null。管理员校验先于 UUID 校验，曲目缺失或不存在返回 404。

同一路径的 PATCH 沿用发行修正的同源、管理员、UUID、幂等键和请求体检查顺序。请求提供正整数 `expected_revision` 与 `changes`。两个字段都接受 `set` 和 `clear`。文本经发行修正的规范化后必须非空，限 4096 字节。`null` 或未知字段返回 `400 invalid_correction`，所有意图都与原来相同返回 `400 no_changes`。例如清除已有标题修正会立即显示当前自动值，缺少自动值时显示扫描值。

`operations.Run` 在幂等锁内取得 CATP，再锁曲目当前所属发行与曲目。版本过期返回 `409 revision_conflict`。成功时单独递增 `tracks.edit_revision`，返回最新状态与 `operation_id`。同键同义重放原响应。标题与艺人修正都在事务内刷新曲目搜索，艺人修正同时刷新所属发行的署名。发行详情的曲目用可选 `corrected_fields`、`auto_fields` 标明各字段当前生效的层。

操作类型 `track_correction` 以当时所属发行为目标，前后快照含 `track_id`、`track_title`、改动字段的意图与当时的有效值。列表按曲目的在库状态与当前曲目版本判断能否撤销。撤销恢复之前的人工意图，并递增曲目版本。曲目已缺失或后续人工版本变化时返回 `409 revert_conflict`。同发行另一首曲目的编辑可以独立撤销。曲目移到另一发行后，撤销记录属于它当前的发行，原记录保留原目标。同路径替换文件产生新曲目 ID，原修正随原曲目保留。

验证应覆盖设值、清除、同键重放、过期写入、跨曲目版本独立、撤销后读取当前下层值，以及重扫后保留人工意图。未修正时，已有目录与操作记录接口样本应逐字相同。
