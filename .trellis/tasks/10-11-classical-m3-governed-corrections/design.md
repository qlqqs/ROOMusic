# M3 技术设计

需求与裁决见 [prd.md](prd.md)，执行顺序见 [implement.md](implement.md)。本文把 ADR 0002、0003 的推荐方向落到本仓库的表、包与接口上；
SQL 与 JSON 只写到确定行为所需的程度，字段名以实施时的样本测试为准。

## 1. 边界与能力归属

| 归属 | 负责 | 不负责 |
| --- | --- | --- |
| `internal/operations`（新包，无 HTTP） | 操作记录表 `operations`；共同写入流程 `Run`（幂等键加锁、重放、冲突、调用执行器、写记录、提交）；读取与分页；撤销的分派接口 `Kind` | 任何业务表；字段或归组规则 |
| `internal/catalog` | 修正表 `release_corrections`、发行的人工修改版本号、有效值视图与全部读取投影；字段修正与归组的执行器及逆执行器（在调用方给的事务里执行） | 鉴权、幂等、操作记录表 |
| `catalog/httpapi` | `GET/PATCH /releases/{id}/corrections`、`POST /releases/{id}/group`；详情增加 `revision` 与 `corrected_fields` | 撤销与操作记录列表 |
| `operations/httpapi`（新包） | `GET /operations`、`POST /operations/{id}/revert`；按操作类型分派给注册的 `Kind` | 读写 catalog 的表 |
| `internal/app` | 组装：把 catalog 实现的 `Kind` 注册给 `operations/httpapi`；依赖规则测试加入两个新包 | — |
| 前端 `features/governance`（新） | 修正对话框、归组对话框、操作记录表与页面、撤销；接口解码器 | 决定有效值或权限 |

依赖方向：`operations` 只依赖 `platform/uuid`；`catalog` 与 `catalog/httpapi` 依赖 `operations`；`operations/httpapi` 依赖
`operations`、`identity`、`platform/httpserver`、`platform/uuid`，不导入 catalog（catalog 的能力经 `Kind` 接口由 app 注入）。
扫描器（`library`、`catalog.PersistCandidate`）不改。

## 2. 数据模型（迁移 0017）

```sql
-- 操作记录：只记成功提交的操作；业务变化、记录与幂等结果同一事务。
CREATE TABLE operations (
  id UUID PRIMARY KEY,
  actor_id UUID NOT NULL REFERENCES users(id),
  operation_type TEXT NOT NULL CHECK (operation_type IN ('release_correction','release_group_change','revert')),
  target_type TEXT NOT NULL CHECK (target_type IN ('release')),
  target_id UUID NOT NULL,                       -- 不设外键：记录可能比目标活得久，target_type 以后会扩展
  idempotency_key UUID NOT NULL,
  fingerprint TEXT NOT NULL,
  expected_revision BIGINT NOT NULL CHECK (expected_revision > 0),
  result_revision BIGINT NOT NULL CHECK (result_revision = expected_revision + 1),
  before_state JSONB NOT NULL,                   -- 执行器定义的类型化前态（逆操作的依据）
  after_state JSONB NOT NULL,
  response JSONB NOT NULL,                       -- 重放时原样返回
  reverts_operation_id UUID UNIQUE REFERENCES operations(id),  -- 撤销记录指向被撤销的操作；一条操作最多被撤销一次
  request_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (actor_id, idempotency_key),
  CHECK ((operation_type = 'revert') = (reverts_operation_id IS NOT NULL))
);
CREATE INDEX operations_created_index ON operations (created_at DESC, id DESC);
CREATE INDEX operations_target_index ON operations (target_type, target_id, created_at DESC);

-- 发行的人工修改版本号：只有受控操作（修正、归组、撤销）递增，扫描从不改它。
ALTER TABLE releases ADD COLUMN edit_revision BIGINT NOT NULL DEFAULT 1 CHECK (edit_revision > 0);

-- 当前的修正意图：每个字段至多一行；没有行就是“没有修正”。
CREATE TABLE release_corrections (
  release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
  field TEXT NOT NULL CHECK (field IN ('title','album_artist','year','genre','label','catalog_number','edition','barcode')),
  mode TEXT NOT NULL CHECK (mode IN ('set','null')),
  text_value TEXT,
  year_value INTEGER,
  operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,  -- 设下这个意图的操作；记录在执行器之后插入，所以提交时才检查
  PRIMARY KEY (release_id, field),
  CHECK (NOT (field IN ('title','album_artist') AND mode = 'null')),
  CHECK (mode = 'null' AND text_value IS NULL AND year_value IS NULL
      OR mode = 'set' AND field = 'year' AND text_value IS NULL AND year_value BETWEEN 1 AND 9999
      OR mode = 'set' AND field <> 'year' AND year_value IS NULL AND text_value IS NOT NULL
         AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
);
```

- 一个发行一个版本号（`edit_revision`），字段修正与归组共用：撤销规则因此只有一句话——“只能撤销这个版本最近一次修改”。
  ADR 0002 建议区分修正、结构与扫描基值版本；这里不区分扫描基值版本（见 §8），修正与结构合并为一个，代价是同时打开编辑框与归组对话框时
  后提交的一方会得到 409（单管理员场景极少），换来规则简单、撤销资格一目了然。
- ReleaseGroup 仍只有 `id`、`created_at`：组没有自己的字段，空组不删除（撤销归组时要回到原来的组 ID，原地址随之恢复）。
- 修正行引用设下它的操作，所以“谁在何时设成了这个值”可以从记录里查到；撤销后行被恢复成前一个意图（可能是删除行）。

## 3. 有效值投影

在同一迁移里建视图 `release_effective`：列与 `releases` 读取所需的列同名，可修正字段换成有效值：

| 有效列 | 规则 |
| --- | --- |
| `title` | 有 `set` 修正取修正值，否则取扫描值 |
| `album_artist` | 有 `set` 修正取修正值，否则取扫描值（专辑艺人不能设空） |
| `artist`（展示艺人） | 专辑艺人有 `set` 修正时取修正值，否则取扫描值：扫描的 `artist` 本来就是“有专辑艺人取专辑艺人” |
| `year`、`genre`、`label`、`catalog_number`、`edition`、`barcode` | `set` 取修正值，`null` 为空，没有修正取扫描值 |
| 其余列（`id`、`group_id`、`created_at`、`candidate_kind`、`source_type`、`media_type` 等） | 原样 |

视图用一次 `LEFT JOIN`（对 `release_corrections` 按 `release_id` 透视，`BOOL_OR(field = …)` 区分“设空”与“没有修正”）。
catalog 的读取 SQL 把 `FROM releases`、`JOIN releases` 换成 `release_effective releases`（别名不变，`read.go`、`groups.go`、
`track_placement.go` 中的常量与查询都继续成立），于是列表、详情、搜索、排序、分页、按组读取、代表版本规则与最近播放一起换成有效值。
扫描与身份规划仍读写 `releases` 本表。不建物化表：修正行很少，透视的代价是一次小表聚合；以后真有性能问题再物化（ADR 0002 第十节第 6 项）。

另外三处跟着有效值走：

- **需要关注**：`uncertain_apply` 的字段决定，如果该字段有修正（`set` 或 `null`）就不计数；`artist` 字段由专辑艺人的修正覆盖。
  分组原因与缺失曲目照旧计数。清除修正后扫描的不确定重新计入。
- **制作人员**：详情的专辑级署名里，专辑艺人有修正时用修正值替换扫描写入的 `album_artist` 署名。
- **证据**：详情增加 `corrected_fields`（有修正的字段名数组，所有登录成员可见，不含修正值来源与操作者），前端在“来源与证据”里把
  这些字段标为“人工修正”；扫描结论（来源、可信度、处理方式）照旧显示，不把人工选择伪装成高置信标签。管理员证据接口不变。

**无修正时与旧行为逐项一致**是这一步的门禁：先只建视图、切换读取，不开放写入，用现有的 catalog 与按组接口测试（含样本）证明输出不变。

## 4. 共同写入流程 `operations.Run`

```text
handler：同源 → 管理员 → 校验路由 ID、Idempotency-Key（UUID）与请求体 → 计算指纹
  → operations.Run(ctx, db, Spec{actor, type, key, fingerprint}, executor)
       BEGIN（READ COMMITTED）
       pg_advisory_xact_lock(类别 'OPER', hash(actor, key))      -- 同一 actor 同一键的请求串行
       SELECT … FROM operations WHERE actor_id = ? AND idempotency_key = ?
         有记录且指纹相同 → 回放 response（同样的状态码），不执行
         有记录但指纹不同 → 409 idempotency_conflict
       生成本次操作 ID
       executor(tx, 操作 ID) → Outcome{target, expected/result revision, before, after, response} 或分类错误
                                                                  -- 修正行写入时就带上这个操作 ID
       INSERT INTO operations …
       COMMIT                                                     -- 业务变化与记录一起提交；延迟外键在此检查
  → 写出响应
```

- 幂等键的作用域是“同一 actor”（跨操作类型）：同一个键用于另一种操作、另一个目标或不同的修改内容，指纹不同，返回 409。
  指纹是规范化 JSON 的 SHA-256，含操作类型、目标 ID（小写）、预期版本与完整语义（字段按名排序，`set`/`null`/`clear` 不同则指纹不同）。
- 重放在当前授权下进行：失去管理员身份后用旧键重放得到 403，不会读到已存结果。
- 执行器返回分类错误（未找到、版本冲突、无效输入等），`Run` 回滚并映射成稳定错误码（§10）；记录写入失败（约束、触发器）同样整体回滚，返回 500 `operation_failed`。
- 只记成功提交的操作，被拒绝的尝试（403、404、409）不写记录，与目录操作一致；数据库不可用返回 503，前端按“结果不确定”处理，
  用**同一个键**重试即可对账（已提交则重放结果，未提交则执行一次）。
- 不记录执行模式（`assistant`/`steward`/`operator`）：人类管理员直接提交不是 Agent 的 operator 模式，模式字段在 M6 引入。

## 5. 字段修正

- `GET /api/v1/releases/{id}/corrections`（管理员）：`{ release_id, revision, fields: [ { field, scan_value, correction, effective_value, uncertain } ] }`，
  八个字段固定顺序；`correction` 为 `null`、`{mode:"set", value}` 或 `{mode:"null"}`；年份的值是整数，其余是字符串。`uncertain` 表示扫描对该字段的处理是
  `uncertain_apply`（专辑艺人还看 `artist` 字段）。专辑艺人的 `scan_value` 是扫描的专辑艺人，没有时是扫描的展示艺人（即页面当前显示的值）。
- `PATCH /api/v1/releases/{id}/corrections`（管理员、同源、`Idempotency-Key`）：`{ expected_revision, changes: { <field>: {mode:"set", value} | {mode:"null"} | {mode:"clear"} } }`。
  - 没写的字段不变；`clear` 删除修正，回到**当前**扫描值；`set` 等于扫描值也是有效修正（“确认扫描值”，钉住当前值并消除该字段的“需要关注”）。
  - 文本去掉首尾空白后不能为空（要清空用 `null`）、不含控制字符、不超过 4096 字节；年份是 1–9999 的整数；标题与专辑艺人不接受 `null`。
  - 不改变状态的条目（没有修正时 `clear`、与现有修正相同的 `set`）忽略；全部被忽略返回 `400 no_changes`。
  - 执行：`SELECT … FROM releases WHERE id = ? FOR UPDATE`，要求存在且可见（至少一条 present 曲目），`edit_revision = expected_revision`；
    写或删修正行，`edit_revision + 1`。前态与后态是被改字段的修正意图（`null` 表示没有修正），另存当时的有效值快照供记录显示。
  - 成功返回与 `GET` 相同的形状加 `operation_id`。
- 逆操作：要求发行当前 `edit_revision = result_revision`，把被改字段恢复成前态的**意图**（没有修正、设值或设空），不把历史有效值写回扫描列。

## 6. 人工归组

- `POST /api/v1/releases/{id}/group`（管理员、同源、`Idempotency-Key`）：
  `{ expected_revision, action: "join", group_id }`（归入另一张专辑）或 `{ expected_revision, action: "detach" }`（移出为独立专辑）。
- 执行：先锁发行行（存在、可见、版本匹配），再按 UUID 顺序锁涉及的组行（`release_groups … FOR UPDATE`，避免死锁）。
  - `join`：目标组存在且至少有一个可见版本（不含自己），否则 `404 group_not_found`；目标就是当前组时 `409 same_group`。
  - `detach`：当前组除自己外没有可见版本时 `409 already_alone`；否则插入新组。
  - 更新 `releases.group_id`，`edit_revision + 1`。前态/后态是组 ID，另存当时两边代表版本的标题快照，供记录显示（组可能之后变空）。
- 返回 `{ release_id, group_id, previous_group_id, revision, operation_id }`；前端随后跳到新组并选中该版本。
- 逆操作：发行当前 `edit_revision = result_revision` 且仍在后态的组里，就移回前态的组（组从不删除，所以总在）；原组的地址随之恢复可见。
- 代表版本规则不变（年份最早，没有年份排后，再按创建时间与 ID）；整组合并、默认版本、Release 合并与拆分属于 M4。
- 扫描对已有发行不改 `group_id`（`persist.go:149`），新发行仍各自新建组；为此加回归测试：归组后重扫，归组保持。

## 7. 撤销与操作记录

- `POST /api/v1/operations/{id}/revert`（管理员、同源、`Idempotency-Key`，空请求体）：按记录的 `operation_type` 找到注册的 `Kind`，在 `Run` 里执行逆操作，
  写一条 `revert` 记录（目标相同，`reverts_operation_id` 指向原记录）。
  - 原记录本身是撤销 → `409 not_revertible`（撤销不能再撤销，需要时重新修改）；已被撤销 → `409 already_reverted`；
    发行之后又有修改（版本号不等）→ `409 revert_conflict`；目标发行当前不可见 → `404 not_found`。
  - 指纹是操作类型 `revert` 加原记录 ID。
- `GET /api/v1/operations?page=&page_size=&release_id=`（管理员）：按时间倒序分页（默认 50，最大 100），可按发行筛选。每条含：类型、操作者（ID 与用户名）、时间、
  发行（ID、当前有效标题、当前组 ID、是否可见）、改动摘要（字段的前后意图与当时的有效值快照，或归组的前后组 ID 与标题快照）、`reverts`、`reverted_by`、
  `revertible`（未被撤销、本身不是撤销、发行可见且版本号等于记录的结果版本）。展示所需的发行信息由 catalog 的 `Kind.Describe` 批量提供，不逐条查询。
- 记录永久保留，没有清理；不提供“回滚到某点”。

## 8. 与扫描的关系

- 扫描不碰 `release_corrections`、`edit_revision`、`operations`：重扫只更新扫描值与证据，修正按视图继续生效，这是结构保证而不是约定。
- 扫描值在打开编辑框与提交之间变化不算冲突：`set` 与 `null` 的结果与扫描值无关；`clear` 的结果是提交时的扫描值，响应返回实际有效值。
  没有审批等待，所以不把扫描基值版本绑进预期版本（ADR 0002 §5 的证据绑定针对待审批提案）。
- 成员关系不连续而新建发行时（整张替换、新增多于原有等），新发行按扫描值显示、单独成组；旧发行的修正与归组留在旧发行上，
  旧发行不可见，记录仍可查（`visible: false`），不自动继承、也不提供认领（M4）。
- 并发：修正事务只锁发行行与自己的修正行，归组事务锁发行行与组行；扫描事务更新发行行但从不锁组行或修正行，事务都短，不形成环形等待。

## 9. 前端

- `features/governance`：解码器与接口、`useReleaseCorrections`、`useCorrectRelease`、`useChangeReleaseGroup`、`useRevertOperation`、`useOperations`；
  沿用目录命令的模式：幂等键在发起时生成，结果不确定时用同一个键重试，悲观更新（后端确认并刷新之后才算完成），写操作不自动重试。
- **编辑发行信息**（专辑详情头部，管理员）：对话框列出八个字段，每个字段三选一——“使用扫描值”“自定义”（带输入框）“留空”（标题与专辑艺人没有此项），
  下方写明扫描值与“扫描结果不确定”提示；不确定的字段有“确认扫描值”快捷按钮。只提交改动的字段；409 时说明“这张专辑刚被修改过”并重新载入。
- **调整归组**（专辑详情，管理员）：“归入其他专辑…”打开对话框，按标题搜索专辑（复用 `/release-groups?q=`，排除当前组），选中后显示目标的封面、标题、艺人与版本数，确认后提交；
  “移出为独立专辑”只在组内多于一个版本时出现，确认对话框说明结果。成功后跳到新的组并选中该版本。
- **撤销**：成功提示里有“撤销”；专辑详情（管理员）有“修改记录”（这个版本最近 5 条，链接到全部记录）；导航“资料库”下新增“操作记录”页（管理员），
  表格列出时间、操作者、专辑、改动与“撤销”（只在 `revertible` 为真时出现），按页浏览。
- 成功后失效相关查询：发行与组的列表和详情、修正状态、操作记录、最近播放。播放队列里已有条目的标题不追更（下次加入队列时用新值）。
- 普通成员：没有任何编辑入口；证据区标出“人工修正”的字段。所有对话框沿用现有对话框组件（焦点约束、取消、焦点恢复），五种宽度不溢出、axe 无违规、纯键盘可完成。

## 10. 错误码

| 条件 | 响应 |
| --- | --- |
| 未登录 | `401 unauthorized` |
| 跨源写入 / 非管理员 | `403`（同源检查在鉴权之前，与现有写接口一致） |
| ID 不是 UUID、缺少或非 UUID 的 `Idempotency-Key`、请求体字段非法 | `400 invalid_id` / `400 invalid_input` / `400 invalid_json` |
| 修正值非法、标题或专辑艺人设空、未知字段 | `400 invalid_correction` |
| 全部条目不改变状态 | `400 no_changes` |
| 发行不存在或不可见；记录不存在 | `404 not_found` |
| 归入的目标组不存在或没有可见版本 | `404 group_not_found` |
| 归入自己所在的组 / 移出已经独立的版本 | `409 same_group` / `409 already_alone` |
| 预期版本过期 | `409 revision_conflict` |
| 同键不同请求 | `409 idempotency_conflict` |
| 撤销：本身是撤销 / 已被撤销 / 之后有修改 | `409 not_revertible` / `409 already_reverted` / `409 revert_conflict` |
| 记录写入失败 | `500 operation_failed`（整体回滚） |
| 数据库不可用 | `503 database_unavailable` |

## 11. 迁移、兼容与回退

- 只新增迁移 0017；已有数据没有修正、版本号为 1，读取结果与升级前相同，不需要重扫。开发阶段不做兼容。
- 回退：关闭三个写入路由（或整体回退版本前先停写）；修正与记录保留，旧版本读不到视图会显示扫描值，但不会删除修正，重新部署即恢复。不写 down 迁移。
- 目录操作仍用 `library_root_operations` 与扫描目录页的记录，不迁移；成员写操作不接入（G6）。二者以后接入时复用 `operations.Run`。

## 12. 取舍

| 方案 | 结论 |
| --- | --- |
| 修正存独立表 + 读取视图（选用） | 扫描不用改，重扫不覆盖由结构保证；读取一处换别名 |
| 有效值物化回 `releases` 列、扫描值另存 | 扫描写入路径要感知修正，两处写同一列需要锁协调，风险集中在最复杂的扫描代码；不选 |
| 字段级版本号 / 修正与归组分开的版本号 | 冲突更少，但撤销资格规则变复杂；单管理员场景收益小，不选 |
| editor、审批队列、回滚到某点 | G1 裁决不做 |
| 存失败尝试的记录 | 现有目录操作也不存；需要独立受控写入流程，留到有审计需求时 |

## 13. 已知限制

- 被替换的发行不继承修正与归组，需要重新操作（M4 认领）。
- 播放队列里已有条目的标题与艺人不随修正更新。
- 清除修正时不核对扫描值是否在打开编辑框后变化，以提交时的扫描值为准。
- 不记录被拒绝的尝试；撤销只针对每个发行的最近一次修改。
