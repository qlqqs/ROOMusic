# 受治理操作与修正覆盖层合同（阶段 M3）

## 1. 范围与触发

M3（2026-10）让管理员能修正扫描得到的发行信息、调整版本归属的专辑，并撤销自己的修改。新增或修改以下内容时必须阅读并更新本文件：
操作记录表 `operations`、共同写入流程 `operations.Run`、可修正字段与修正表 `release_corrections`、发行的人工修改版本号
`edit_revision`、有效值视图 `release_effective`、人工归组、撤销、操作记录列表。读取投影（列表、详情、按组读取）的通用规则仍在
[Catalog REST 合同](./catalog-rest-contracts.md)；扫描的写入不受本文件约束，但**必须保持不碰本文件的表和列**（第 7 节）。
M4 的第一项（2026-10）在同一流程上增加以专辑为目标的“设定默认版本”，它自己的规则（组的版本号、失效规则、执行器、撤销与对比接口）在
[默认版本与版本对比合同](./default-version-contracts.md)；本文件只记它对共同流程的扩展（目标类型、撤销响应、列表筛选）。
M4 的第二项（2026-10）增加以发行为目标的“核对问题”（与修正、归组共用发行的版本号），规则在[元数据问题与核对合同](./issue-review-contracts.md)。
M4 的第三项（2026-10）增加“认领”：把不可见旧发行上的修正、归组、默认版本与核对迁给新发行，规则在[认领合同](./claim-contracts.md)。

决策依据：[ADR 0002](../../../docs/architecture/adr/0002-metadata-overlays.md)、
[ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md) 与任务
[M3 修正覆盖层与受治理操作](../../tasks/archive/2026-10/10-11-classical-m3-governed-corrections/prd.md)。

## 2. 数据模型（迁移 0017）

- **`operations`**：每次**成功提交**的受控操作一行（修正、归组、默认版本、核对、认领、撤销）。列：`id`、`actor_id`、`operation_type`
  （`release_correction | release_group_change | release_group_default | release_issue_review | release_claim | revert`，`release_group_default` 由迁移 0018、`release_issue_review` 由 0019、`release_claim` 由 0020 加入）、`target_type`（`release`；0018 加入 `release_group`，即专辑）、
  `target_id`（不设外键：记录可能比目标活得久）、
  `idempotency_key`、`fingerprint`、`expected_revision`、`result_revision`、`before_state`/`after_state`（执行器定义的类型化前后状态，撤销的依据）、
  `response`（原样返回客户端，也是同键重放的响应）、`reverts_operation_id`、`request_id`、`created_at`。
  约束都有名字：`operations_revision_ck`（`result = expected + 1`）、`operations_reverts_ck`（只有撤销记录指向原操作）、
  `operations_actor_key_uq`（`(actor_id, idempotency_key)`）、`operations_reverts_uq`（一条操作至多被撤销一次）。
- **`releases.edit_revision`**（`BIGINT NOT NULL DEFAULT 1`，`> 0`）：发行的人工修改版本号。**只有受控操作（修正、归组、核对、撤销）递增，扫描从不改它。**
  字段修正、归组与核对共用同一个号，所以撤销规则只有一句话：只能撤销这个发行**最近一次**修改。
- **`release_corrections`**：当前的修正意图，`PRIMARY KEY (release_id, field)`，没有行就是“没有修正”。`mode` 为 `set`（改成某个值，
  等于扫描值也是有效修正）或 `null`（明确设为空）；标题与专辑艺人不能是 `null`；`set` 的值非空、不超过 4096 字节，年份是 1–9999 的整数。
  `operation_id` 指向设下这个意图的操作，外键 `DEFERRABLE INITIALLY DEFERRED`（记录在执行器之后才插入）。
- **`release_effective`**（视图）：列与 `releases` 同名，可修正字段换成有效值：`title`、`album_artist`、`year`、`genre`、`label`、`catalog_number`、
  `edition`、`barcode` 取修正值（`null` 为空，没有修正取扫描值）；展示艺人 `artist` 跟随专辑艺人的修正；另带 `edit_revision`；其余列原样。
  每个字段一次主键上的 `LEFT JOIN`，不被引用的列对应的连接会被优化器去掉。
- 可修正字段固定为八个，顺序固定：`title`、`album_artist`、`year`、`genre`、`label`、`catalog_number`、`edition`、`barcode`
  （后端 `correctableFields` 与前端 `CORRECTABLE_FIELDS` 一致，样本契约测试守住）。
- 组（`release_groups`）在 M3 没有自己的字段（M4 起有默认版本与专辑的版本号，见[默认版本合同](./default-version-contracts.md)），**从不删除**：
  撤销归组要回到原来的组 ID，空组的地址随之恢复。

## 3. 共同写入流程 `operations.Run`

`operations` 包只含流程与记录，不含业务表、规则与 HTTP；业务由拥有表的包提供的执行器完成。依赖方向：`operations` 只依赖 `platform/uuid`；
`catalog` 与 `catalog/httpapi` 依赖 `operations`；`operations/httpapi` 不导入 catalog（catalog 的能力经 `operations.Kind` 由 `app` 注入）。

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
- 不记录执行模式（`assistant`/`steward`/`operator`）：人类管理员直接提交不是 Agent 的 operator 模式，模式字段在 M6 引入。

## 4. 接口

所有写接口：同源（403）→ 管理员（401/403）→ 路由 ID 与 `Idempotency-Key` 校验（`400 invalid_id` / `invalid_input`）→ 请求体（`400`）→ `Run`。
读接口：管理员（401/403）→ 参数 → 读取。非管理员读与写都是 `403 forbidden`。

### 4.1 修正

- `GET /api/v1/releases/{id}/corrections`：`{release_id, revision, fields:[{field, scan_value, correction, effective_value, uncertain}]}`，八个字段固定顺序；
  `correction` 为 `null`、`{mode:"set", value}` 或 `{mode:"null"}`；年份的值是整数，其余是字符串。`uncertain` 表示扫描对该字段的处理是
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
  release:{id, title, group_id, visible}|null, album:{id, title, visible}|null, changes:{kind, …}|null, reverts, reverted_by, revertible}`。
  发行目标的记录 `release` 是目标**当前**的有效标题、所在组与可见性、`album` 为 `null`；专辑目标的记录反过来（目标已不存在时两者都是 `null`）。
  `changes` 是改动摘要（`release_correction`：每个字段的前后修正意图与当时的有效值；`release_group_change`：前后组 ID 与当时的组标题；
  `release_group_default`：前后的默认版本快照，见[默认版本合同](./default-version-contracts.md)；`release_issue_review`：问题与前后是否有核对，
  见[核对合同](./issue-review-contracts.md)；`release_claim`：来源版本与迁移、保留的各类，见[认领合同](./claim-contracts.md)），
  撤销记录的前后状态与它所撤销的操作相反；记录里的状态损坏时 `changes` 为 `null`，不让列表失败。
  `revertible` = 未被撤销 ∧ 本身不是撤销 ∧ 目标可见 ∧ 目标的 `edit_revision` 等于记录的 `result_revision`。展示信息由 `Kind.Describe` 按类型批量查询，不逐条查询；
  操作者用户名经 `identity.Guard.Usernames` 批量取。响应不含请求体、前后状态原文、路径与密钥。
- 记录永久保留，没有清理，不提供“回滚到某点”。

## 5. 有效值投影

- 所有 catalog 读取（列表、详情、搜索、排序、分页、按组读取、代表版本规则、最近播放的曲目位置）的发行来源是常量
  `releaseSource = "release_effective releases"`（别名仍叫 `releases`），所以展示、排序与搜索用的都是有效值。扫描与身份规划读写 `releases` 本表。
- **无修正时与旧行为逐项一致**（`TestPostgreSQLEffectiveViewEqualsTheTableWithoutCorrections` 与既有按组/列表样本测试守住）。
- **需要关注**：`uncertain_apply` 的字段决定如果该字段有修正（`set` 或 `null`）就不计数，`artist` 由专辑艺人的修正覆盖；分组原因与缺失曲目照旧计数；
  清除修正后扫描的不确定重新计入。M4 第二项起被仍然有效的核对压下的问题也不计数（[核对合同](./issue-review-contracts.md)第 3 节）。
- **制作人员**：详情的专辑级署名里，专辑艺人有修正时用修正值替换扫描写入的 `album_artist` 署名。
- **证据**：发行详情增加 `revision` 与 `corrected_fields`（有修正的字段名，固定顺序，没有时是空数组，所有登录成员可见，不含修正值的来源与操作者）；
  证据摘要与管理员完整证据照旧写扫描结论，不把人工选择伪装成高置信标签。

## 6. 撤销规则

- 撤销是一次新操作，恢复的是前态的**意图**（没有修正、设值或设空；归组回到原组），不把历史有效值写回扫描列：因此撤销“从没有修正到设值”之后，
  显示的是**现在**的扫描值（重扫之后可能已不是修改前那一刻的值）。
- 只能撤销发行最近一次修改（`edit_revision` 相等）；撤销本身也递增版本号、写一条记录，**不能再被撤销**，需要时重新修改。没有链式恢复。
- 撤销只恢复这条记录改动过的字段，不碰其他字段。

## 7. 与扫描的关系

- 扫描只更新 `releases` 的扫描值与证据，不碰 `release_corrections`、`edit_revision`、`operations`、`releases.group_id`（既有发行），也不更新组行（M4 的默认版本列）：
  修正按视图继续生效、人工归组保持，这是结构保证而不是约定（真实重扫用例守住，见第 9 节）。
- 扫描值在打开编辑框与提交之间变化不算冲突：`set` 与 `null` 的结果与扫描值无关；`clear` 的结果是提交时的扫描值，响应返回实际有效值。
- **已知限制（G8）**：成员关系不连续而新建发行时（整张替换等），新发行按扫描值显示、单独成组；旧发行的修正与归组留在旧发行上（不可见但保留，记录里 `visible: false`），
  不自动继承；M4 第三项起管理员可以把这些修改认领给新发行（预览、撤销，见[认领合同](./claim-contracts.md)），认领之前它们一直留在旧发行上。
- 并发：修正事务只锁发行行与自己的修正行，归组事务锁发行行与组行（按 UUID 顺序），设定默认版本同样先锁发行行再锁组行，核对只锁发行行
  （撤销时若要写回的前态有核对行，先给它引用的更早记录加 KEY SHARE 锁，见[核对合同](./issue-review-contracts.md)第 5 节），认领先锁记录、再锁两个发行行、最后锁组行（见[认领合同](./claim-contracts.md)第 6 节），
  扫描事务更新发行行但从不锁组行或修正行，事务都短，不形成环形等待。

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

默认版本新增的 `409 not_in_group` 等见[默认版本合同](./default-version-contracts.md)第 8 节。

## 9. 必需测试与断言点

- `internal/operations`：指纹与错误映射单元测试；PostgreSQL 集成——记录/重放/同键异义、并发同键只执行一次、执行失败与记录写入失败整体回滚、
  版本契约（`result = expected + 1`）、延迟外键、撤销记录互相指向、列表排序分页筛选（含发行与专辑的并集）、撤销执行器的守卫与对同一条记录的并发撤销、
  撤销响应的目标类型。
- `internal/catalog`：视图无修正时与表逐列相同、逐字段叠加与清除、修正行的约束拒绝非法状态、读取返回有效值（含署名与“需要关注”）；
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

`frontend/e2e/real/governance.real.ts`（隔离服务、隔离 PostgreSQL、真实 Chromium，经 `scripts/playback-e2e.sh` 运行，与播放用例同一服务）：
素材是用例自己生成的合成 WAV（INFO 标签带专辑），放在 `library/governance` 下，结束后删除并重扫，不依赖其他用例文件的先后顺序。覆盖：页面修改标题 →
改文件标签并重扫 → 扫描值更新而修正不动且仍是同一个发行 → 操作记录页撤销后回到现在的扫描值；把两张专辑归到一起（一个组两个版本、重扫后保持）→ 在“修改记录”里撤销后分开；
普通成员没有任何入口且读写接口都是 `403`。

## 11. 边界与后续

- M3 **没有**审批队列、editor 角色、“回滚到某点”、扫描基值版本（ADR 0002 的证据绑定针对待审批提案）、物化有效值表（修正行很少，透视代价是主键上的小查询）。
- 成员写操作（创建、停用/启用、撤销会话）**不在 M3**：暂不要求幂等键、不写操作记录；作为独立小任务接入（补幂等键并写记录，不提供撤销），
  见[身份与成员合同](./identity-contracts.md)。
- M4：默认版本已作为第一项实施（[默认版本合同](./default-version-contracts.md)），问题处理作为第二项实施（[核对合同](./issue-review-contracts.md)），认领作为第三项实施（[认领合同](./claim-contracts.md)）；
  整组合并/拆分、扫描反馈仍分别立项；M6：执行模式字段。
- 前端合同见[状态规范](../frontend/state-management.md)、[组件规范](../frontend/component-guidelines.md)与[前端目录](../frontend/directory-structure.md)。
