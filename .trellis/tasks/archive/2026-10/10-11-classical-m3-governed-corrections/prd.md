# M3 修正覆盖层与受治理操作

## 目标与用户价值

Core 1 阶段 M3：让管理员能修正扫描得到的发行信息、把同一作品的不同版本归到一起；这些人工结果不会被下一次扫描冲掉，
谁改了什么有记录，改错了能撤销，音乐文件始终不写。它是 M4（问题处理、版本对比与默认版本、Release 合并拆分）与 M6（Steward 提案）
复用的写路径基础（[路线图](../../../../../docs/architecture/core-1-roadmap.md) M3 行，父任务 [R5](../../2026-11/09-27-classical-core-1/prd.md)）。
技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)。

## 背景与已确认的事实

### 已批准的范围与验收出口

- 2026-09-28 D1（ADR 0003 决策记录）：M3 以“字段修正”和“ReleaseGroup 成员调整”两种操作试点共同合同；后者只改 Release 所属组，
  扫描器更新已有 Release 时不改该字段。艺人合并不是试点（还没有艺人实体）。
- 2026-09-28 D6（ADR 0002 决策记录）：Core 1 只允许人工确认归组，不做任何自动跨版本归组。
- 路线图 M3 行的验收出口：重扫不覆盖修正、越权拒绝、并发冲突、幂等与支持的撤销有证据。
- F3 裁决（2026-10-02）：成员写操作的 `Idempotency-Key` 延到 M3，随“成员管理是否纳入受治理操作”一起决定
  （[身份与成员合同](../../../../spec/backend/identity-contracts.md)）；本任务的结论见 G6。
- 开发阶段不做兼容（用户 2026-09-29 裁决，见 ADR 0003 决策记录中的 F2 一行）：旧记录与旧格式不写兼容层；
  表结构只通过新增迁移改变（[数据库规范](../../../../spec/backend/database-guidelines.md)“Migrations”）。
- 本阶段裁决的 ADR 项：[ADR 0002](../../../../../docs/architecture/adr/0002-metadata-overlays.md) 第十节第 1、3、5、6、7 项；
  [ADR 0003](../../../../../docs/architecture/adr/0003-governed-operations.md) 第九节第 1 项其余部分与第 2–6 项（第 7 项属 M4/M6），处理结论见下文“ADR 待批项的处理”。

### 现有代码与数据

- 扫描每次都改写发行的描述列：已有发行走 `UPDATE releases SET title,artist,album_artist,year,…,barcode`
  （`backend/internal/catalog/persist.go:149`），所以直接改这些列的修正会在下次扫描时丢失。该语句不改 `group_id`；
  新发行同时新建一个只有 `id`、`created_at` 的 ReleaseGroup（`persist.go:142`）。
- 发行的描述列：`title`、`artist`（展示用艺人，有专辑艺人时取专辑艺人）、`album_artist`、`year`、`genre`、`catalog_number`、
  `edition`、`label`、`barcode`，另有来自抓轨日志或目录名的 `source_type`、`media_type`（迁移 0002、0010、0011、0012）。
  扫描为这些字段写字段决定 `release_field_decisions`（每个字段一行，`action` 为 `auto_apply` 或 `uncertain_apply`，迁移 0010），
  `uncertain_apply` 的字段计入“需要关注”（`backend/internal/catalog/read.go:35`）。专辑级“制作人员”读扫描写入的 `release_credits`
  （只有 `album_artist` 一种角色，`persist.go:111`）。
- 列表、详情、搜索、排序、按组读取、最近播放与播放器标签都直接读发行的列：摘要投影 `read.go:68`、搜索谓词 `read.go:56`、
  代表版本规则与组排序 `backend/internal/catalog/groups.go`，播放历史经 `Store.LoadTrackPlacements`（`track_placement.go:29`）复用同一投影。
- 曲目的标题、艺人与署名同样每次扫描改写（`persist_track.go:80`、`persist.go` 中的 `track_credits`）。
- 角色只有 `admin`、`user`（迁移 0006；`backend/internal/identity/guard.go:70` 的 `RequireAdmin`）。
- 唯一的受控写入先例是目录停用/恢复：锁目录行、核对 `expected_revision`、同一事务写 `library_root_operations`（前后状态、响应、
  幂等键与指纹），同键请求用 advisory lock 串行（`backend/internal/library/httpapi/root_operations.go:65`）；
  恢复只认相邻的成功停用记录（`root_operations.go:218`）。这些 handler 里直接写 SQL，是已登记的技术债
  （[目录结构规范](../../../../spec/backend/directory-structure.md)“已知技术债”）。
- 前端专辑详情由头部（标题、专辑艺人、年份等）、版本导航、介质与曲目、侧栏“发行信息/此版本”与“制作人员”、底部“来源与证据”组成
  （`frontend/src/features/catalog/components/`）；目录操作记录在扫描目录页（`root-operations-section.tsx`）。前端对象解码器忽略未知字段，
  后端新增响应字段不会让旧解码器失败（`frontend/src/shared/api/decode.ts` 的 `object`）。
- 原型的“审批与日志”页有待办、账簿与“恢复点”回滚，角色表为管理员/编辑者/听众，风险分低中高；[DESIGN.md](../../../../../frontend/DESIGN.md) §8
  已把“恢复点回滚”“编辑者”“问题可应用/忽略/撤销”列为原型的不实承诺，要求 M3 另行评审，不能照搬。

## 需求

- **R1 修正不被扫描覆盖**：发行字段的人工修正单独保存；重扫只更新扫描值与证据，有修正的字段继续显示修正值；
  “没有修正”“改成某个值”“明确设为空”三种状态互不混淆，清除修正后显示**当前**扫描值。可修正的字段见 G2。
- **R2 处处一致**：列表、详情、搜索、排序、分页、按组读取（代表版本与组排序）、最近播放与“制作人员”使用同一个有效值，由后端统一投影；
  没有修正时所有读取结果与升级前相同。
- **R3 人工归组**：可以把一个版本归入另一张专辑（ReleaseGroup），也可以把它移出为独立专辑（G3）；重扫不改变已有发行的归组。
- **R4 受控写入**：后端鉴权、同源校验、幂等键（同键同义只执行一次，同键异义冲突）、预期版本（过期则冲突，不做最后写入获胜）；
  业务变化与操作记录在同一事务里提交或一起回滚。
- **R5 可追溯**：每次成功的修正、归组与撤销都有一条记录：谁、何时、哪个版本、改动前后（G5）。
- **R6 撤销**：一条操作之后这个版本没有新的修改，就可以撤销它；撤销是一次新的操作，恢复之前的修正**意图**或原来的组，不把历史值写回扫描列；
  之后已有修改、已经撤销过或要撤销的本身是撤销时拒绝，不覆盖新状态（G1）。
- **R7 权限**：修正、归组、撤销与查看操作记录只有管理员能做；普通成员只读，看到的是有效值，证据区能看出哪些字段被人工修正过（G1、G5）。
- **R8 界面**：管理员在专辑详情里编辑发行信息、调整归组、查看这个版本的修改记录并撤销；另有“操作记录”页；
  冲突与结果不确定有明确提示和下一步。
- **R9 音乐文件只读**：不写标签、不改名、不移动或删除文件。

## 决策

| 编号 | 决策 | 结论 | 状态 |
| --- | --- | --- | --- |
| G1 | 治理深度：谁能改、是否审批、撤销到什么程度 | 只有管理员，提交即生效并写入操作记录；一条操作之后没有新改动时可以单独撤销（撤销也记一条）；不加 editor、不做审批队列与“回滚到某点” | 2026-10-11 用户选择（AskUserQuestion：“管理员直接生效 + 单条撤销（推荐）”） |
| G2 | 首批可修正字段与设空规则 | 只改发行字段：标题、专辑艺人（这两个不能设空）、年份、流派、厂牌、编号、版本说明、条码（可以明确设为空）；改专辑艺人同时改变展示艺人；来源类型、介质类型（抓轨日志等得到的事实）、曲目级字段与署名不在首批 | 2026-10-12 用户选择（AskUserQuestion：“只改发行字段（推荐）”） |
| G3 | 归组的操作集合与界面 | 两个操作，都只动一个版本：“归入其他专辑”（按标题搜索目标，确认前显示目标的封面、标题、艺人与版本数；目标至少有一个可见版本）与“移出为独立专辑”（组内多于一个版本时）；入口在专辑详情，成功后跳到新组并选中该版本；原组没有可见版本时不再出现在列表里，但不删除（撤销后原地址恢复）；代表版本规则不变；整组合并与默认版本属 M4 | 2026-10-13 用户批准 |
| G4 | 修正过的字段是否还计入“需要关注” | 有修正（改值或设空）的字段不再计入，清除修正后重新计入；改专辑艺人同时覆盖“艺人”字段的不确定；编辑框为扫描不确定的字段提供“确认扫描值”（把当前扫描值存为修正）；证据区照旧显示扫描结论，并标出“人工修正” | 2026-10-13 用户批准 |
| G5 | 操作记录放在哪里、谁能看 | 只有管理员能看：“资料库”下新增“操作记录”页（倒序分页，写明谁、何时、哪张专辑、改了什么，可撤销的有“撤销”）；专辑详情显示这个版本最近 5 条修改；成功提示里也有“撤销”。普通成员只看到有效值与“人工修正”标记，看不到是谁改的。目录操作记录仍在扫描目录页，不并入 | 2026-10-13 用户批准 |
| G6 | 成员写操作是否纳入（幂等键与记录） | 不纳入 M3：M3 建好操作记录后，作为独立小任务接入（创建、停用/启用、撤销会话补幂等键并写操作记录，不提供撤销），可在 M4 之前任何时候做。理由：重复创建已被用户名唯一约束挡住，家庭场景价值低；接入会牵动身份接口里 SQL 的抽取，单独做更好审查 | 2026-10-13 用户批准 |
| G7 | 交付拆分与顺序 | 一个任务，十步分别提交：后端（迁移与操作记录 → 字段修正 → 归组 → 撤销与记录接口）→ 前端（编辑 → 归组 → 操作记录）→ 真实服务用例 → 文档 → 收尾 | 2026-10-13 用户批准 |
| G8 | 被扫描判定为新发行时的修正与归组 | 成员关系不连续而新建发行时（整张替换等），新发行按扫描值显示、单独成组；旧发行的修正与归组留在旧发行上（不可见但保留，操作记录可查），不自动继承、不提供认领（M4） | 2026-10-13 用户批准（已知限制） |
| G9 | 布局确认 | 收尾时用合成数据截取编辑对话框、归组对话框、操作记录页与专辑详情“修改记录”的宽屏与窄屏，请用户确认后再归档 | 2026-10-13 用户批准 |

### ADR 待批项的处理（2026-10-13 随最终摘要获批）

| ADR 项 | 结论 | 设计出处 |
| --- | --- | --- |
| 0002 第 1 项 | 采纳“独立修正意图 + 后端有效值投影”；首批字段与设空规则见 G2；`set` 等于扫描值也是有效修正；署名等集合字段不在首批 | design §2、§3、§5 |
| 0002 第 3 项 | 每个发行一个人工修改版本号 `edit_revision`，修正、归组、撤销共用，扫描从不改它；不把扫描基值版本绑进预期版本（没有审批等待）；扫描不碰修正表与组 | design §2、§8 |
| 0002 第 5 项 | 只有管理员（G1）；不引入 `editor` | design §1 |
| 0002 第 6 项 | 悬挂修正不做管理入口（G8）；有效值用视图、不物化；普通成员只见有效值与“人工修正”标记 | design §3、§8 |
| 0002 第 7 项 | M3 = 字段修正、归组、撤销与操作记录；M4 = 认领、合并拆分、默认版本、问题处理；回退 = 关闭写入路由，保留修正与记录，不写 down 迁移 | design §11 |
| 0003 第 1 项其余 | 最小共同合同：新包 `internal/operations`（操作记录、共同写入流程、按类型注册的逆操作），两种试点共用；不做审批队列、链式恢复；成员管理不纳入 M3（G6） | design §1、§4 |
| 0003 第 2 项 | 两种试点都只有管理员可执行，人类管理员的一次明确提交即生效，不需要独立审批；普通成员只读 | design §4 |
| 0003 第 3 项 | 操作者取自会话；M3 不记录执行模式（M6 引入）；没有批准对象，因此没有批准期限与失效规则 | design §4 |
| 0003 第 4 项 | 资源版本 = 发行的 `edit_revision`；幂等作用域 = 操作者 + 键（跨操作类型）；指纹含类型、目标、预期版本与完整语义；同键并发由 advisory lock 串行；结果不确定时用同一个键重试对账 | design §4 |
| 0003 第 5 项 | 检查点 = 记录里的类型化前后态；永久保留；逆操作按操作类型注册；只支持单条撤销，不支持链式恢复 | design §2、§7 |
| 0003 第 6 项 | 业务变化与记录同一事务；被拒绝与失败的尝试不记录（与目录操作一致）；目录操作记录不迁移 | design §4、§11 |

## 验收标准

- [x] A1（R1）：合成素材真实重扫后修正仍生效，扫描值与证据照常更新；清除修正后显示最新的扫描值。
- [x] A2（R1、G2）：无修正、设值、设空、清除之间的转换正确；标题与专辑艺人设空、非法值被拒（`400 invalid_correction`）；不改变状态的提交 `400 no_changes`。
- [x] A3（R2）：列表、详情、搜索、排序、分页、按组读取、最近播放与“制作人员”显示同一有效值（含专辑艺人带动展示艺人与排序）；
  没有修正时现有 catalog、按组、播放历史测试与接口样本不改即通过。
- [x] A4（R3、G3）：归入与移出后组的成员数、代表版本与 attention 之和正确，原组没有可见版本时不出现、详情 404；
  `same_group`、`already_alone`、`group_not_found` 各有用例；归组后真实重扫，归组保持。
- [x] A5（R4、R7）：非管理员与跨源写入被拒且没有任何变化，未登录 401；同键同义只执行一次（含并发同键），同键异义 `409 idempotency_conflict`；
  过期版本 `409 revision_conflict`；业务或记录写入失败整体回滚。
- [x] A6（R5、G5）：每次成功的修正、归组与撤销各有一条记录，含操作者、时间、版本与前后状态；操作记录只有管理员可读，倒序分页，可按发行筛选，不含路径与密钥。
- [x] A7（R6）：撤销恢复之前的修正意图（无修正、设值、设空分别验证）或原来的组；之后有修改 `409 revert_conflict`、重复撤销 `409 already_reverted`、
  撤销撤销 `409 not_revertible`；撤销本身也记一条，版本号递增。
- [x] A8（G4）：修正（含确认扫描值）后该字段不再计入“需要关注”，清除后重新计入；证据区标出人工修正的字段。
- [x] A9（R8）：管理员能在专辑详情编辑字段、调整归组、查看修改记录并撤销，“操作记录”页可浏览与撤销；普通成员没有任何编辑入口；
  冲突与结果不确定有明确提示，结果不确定后用同一个键重试；五种宽度不溢出、axe 无违规、纯键盘可完成。
- [x] A10：真实服务（Go、隔离 PostgreSQL、Chromium、合成素材）中，页面上修改标题后重扫仍为修正值；两张合成专辑归组后是一个组两个版本，撤销后分开。
- [x] A11：接口样本与前端解码器一致；规范、ADR 决策记录与路线文档同步；布局经用户确认（G9）。

## 范围外

- 写回音乐文件标签、改名、移动、删除文件（永久范围外，需另立能力）。
- 自动跨版本归组、按标题相似度推荐归组（D6）。
- `editor` 角色、审批队列、“回滚到某点”（G1）。
- 曲目级字段、署名、来源类型与介质类型的修正（G2）；修正说明（备注）字段。
- Release 合并与拆分、曲目改挂到别的发行、整组合并、默认版本与版本对比、问题收件箱、被替换发行的修正认领（M4）。
- 成员写操作的幂等与记录（G6，后续独立任务）；目录操作记录并入“操作记录”页；被拒绝尝试的记录。
- 艺人实体与别名、搜索规范化（M5）；Steward 提案与独立审查（M6）。

## 规划决策记录

- 路线：人工选择。选择者：用户（qlqq）。
- 2026-10-11：用户回复“开始M3的规划”，同意创建任务并进入规划。
- 2026-10-11：G1 用户选择“管理员直接生效 + 单条撤销（推荐）”。
- 2026-10-12：G2 用户选择“只改发行字段（推荐）”。
- 2026-10-13：用户回复“批准执行”，批准最终规划摘要（G3–G9、ADR 待批项对照表与十步计划），授权提交规划文档并启动任务。
- 2026-10-14：G9 用户看过合成数据的宽屏与窄屏布局图（操作记录页、专辑详情的“修改记录”、编辑与归入对话框、撤销确认）后选择“布局可以，继续收尾（推荐）”。

## 实施结果（2026-10-14）

提交（均在 `main`，未推送）：`f182760` 规划；`c8c7e7c` 迁移 0017、操作记录包与发行有效值视图（无行为变化）；`1a36869` 字段修正接口与有效值读取；`b054ec3` 人工归组接口；
`bf62b00` 撤销与操作记录接口；`0fcb636` 前端编辑发行信息；`9c28da3` 前端调整归组；`b2e17ce` 前端操作记录与修改记录；`53c886e` 真实服务用例；`20c7913` 文档同步；
`084c1d0` 收尾截图时发现的对齐问题。收尾（本结果、归档、链接修正与 journal）随后提交。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| A1 | 达成 | `TestPostgreSQLCorrectionsSurviveARealRescanAndShowEverywhere`（真实扫描接口重扫合成 FLAC，扫描值与证据更新，修正仍生效并显示在各处）、`TestPostgreSQLCorrectionStateTransitions`（清除后显示重扫之后的值）、`TestPostgreSQLRevertToNoCorrectionShowsTheCurrentScannedValue`；真实服务用例 `governance.real.ts`：页面改标题 → 改文件的专辑标签并重扫 → 同一个发行、扫描值更新而修正不动，撤销后回到现在的扫描值；用“扫描写入时顺手清掉修正”的临时改动验证过该用例会失败，随即还原 |
| A2 | 达成 | `TestParseCorrectionChanges`、`TestCorrectionFingerprintCoversTheCompleteSemantics`、`TestPostgreSQLCorrectionStateTransitions`（改值、设空、清除、确认扫描值之间的转换与前后意图记录）、`TestPostgreSQLCorrectionsAPIStateAndSuccess`、`…APIRejectsUnauthorizedAndInvalidRequestsWithoutSideEffects`（非法值 `invalid_correction`、`no_changes`）；前端 `field-draft.test.ts`（18 项：规范化、字节上限、年份范围、只提交动过且有变化的字段） |
| A3 | 达成 | `TestPostgreSQLEffectiveViewEqualsTheTableWithoutCorrections`（无修正时视图逐列等于表）、`…EffectiveViewAppliesAndRemovesCorrectionsPerField`、`…ReadsReturnEffectiveValues`（列表、详情、搜索、排序、按组读取）、`…ReleaseCreditsFollowTheAlbumArtistCorrection`、`TestPostgreSQLCorrectedValuesAreConsistentAcrossReads`；既有 catalog、按组、播放历史测试不改即通过，既有接口样本只有三份发行详情样本各增加 `revision` 与 `corrected_fields` 两行 |
| A4 | 达成 | `TestPostgreSQLJoinMovesAVersionIntoAnotherAlbum`、`…DetachMakesAVersionItsOwnAlbum`、`…GroupChangeRejectsInvalidRequestsWithoutSideEffects`（`same_group`、`already_alone`、`group_not_found`）、`…ConcurrentGroupChangesDoNotDeadlock`、`TestPostgreSQLGroupChangeAPIJoinDetachAndReads`、`TestPostgreSQLManualGroupingSurvivesARealRescan`；真实服务用例：归组后一个组两个版本、重扫后保持、撤销后分开 |
| A5 | 达成 | 两个接口的 `…APIRejectsUnauthorized…`（跨源与非管理员无任何变化、未登录 401）、`…IdempotencyAndConflicts`（重放与 `idempotency_conflict`、过期版本 `revision_conflict`）、`…ConcurrentRequests`（修正、归组、撤销各一个）；`TestPostgreSQLRunExecutesOnceForConcurrentRequestsWithTheSameKey`、`…RunRollsBackWhenExecutionFails`、`…RunRollsBackWhenTheRecordCannotBeWritten`、`…RunRefusesRecordsThatBreakTheRevisionContract` |
| A6 | 达成 | `TestPostgreSQLOperationsAPIListsRecordsForAdministratorsOnly`（只有管理员可读、倒序分页、按发行筛选）、`TestPostgreSQLListOrdersPagesAndFiltersByTarget`、`TestPostgreSQLDescribeReportsTargetsChangesAndRevertibility`；接口样本经既有的脱敏断言（不含路径、令牌与密码） |
| A7 | 达成 | `TestPostgreSQLRevertRestoresEveryPreviousCorrectionIntent`（无修正、设值、设空）、`…OnlyTouchesTheFieldsTheOperationChanged`、`…RevertOfAGroupChangeMovesTheVersionBack`、`…RevertIsRefusedAfterLaterEditsOrWhenTheTargetIsHidden`、`TestPostgreSQLRevertExecutorRecordsAndGuardsTheOriginal`（`not_revertible`、`already_reverted`）、`TestPostgreSQLConcurrentRevertsOfTheSameOperation`、`TestPostgreSQLRevertAPIUndoesCorrectionsAndGroupChangesAndReplays` |
| A8 | 达成 | `TestPostgreSQLAttentionIgnoresCorrectedFields`、`TestPostgreSQLConfirmingTheScannedValueClearsAttention`（确认扫描值后不再计入，清除后重新计入）；前端证据表与详情头部的“人工修正”标记（`release-corrections.test.tsx`、Playwright） |
| A9 | 达成 | Vitest：`release-corrections.test.tsx`（13 项）、`release-grouping.test.tsx`（14 项）、`operations.test.tsx`（22 项：入口与权限、列表、分页与越界规范化、筛选、撤销的各种结果、结果不确定后同键重试、焦点、专辑详情的“修改记录”、成员无入口）、`change-summary.test.ts`、`operations-search.test.ts`、`disclosure.test.tsx`；Playwright `governance.spec.ts`（20 项：编辑、归组、撤销与操作记录的流程，五种宽度、axe、纯键盘、Esc 与焦点恢复，含超长改动内容）与 `responsive.spec.ts` 新增的三个页面状态；对新增前端逻辑逐项做过变异检查（唯一存活的是页面与视图两道 `isAdmin` 把关里去掉一道，行为不变的等价变异） |
| A10 | 达成 | `./scripts/playback-e2e.sh` 27 项通过（新增 `governance.real.ts` 4 项加原有播放 23 项；治理用例在播放用例之前与之后两种顺序都验证过）：页面改标题后重扫仍为修正值；两张合成专辑归组后是一个组两个版本、重扫保持、在“修改记录”里撤销后分开；普通成员没有入口且读写接口都是 403 |
| A11 | 达成 | 19 份新样本与解码器经 `api-contract.test.ts` 往返核对；新增 `governance-contracts.md` 并同步 Catalog REST、数据库、目录结构、运行合同、身份合同、前端规范、DESIGN、ADR 0002/0003 决策记录、路线图、架构决策摘要、产品目标与父任务（`20c7913`）；布局经用户确认（见规划决策记录，G9） |

验证：隔离 PostgreSQL（`roomusic-test`，空闲端口、随机库名，用后 `down -v`）上，收尾时在最终树上重跑 `gofmt -l .`、`go vet ./...`、`go test -race -count=1 ./internal/operations/...`、
`go test -count=1 ./internal/catalog/... ./internal/identity/... ./internal/app/... ./cmd/...` 全部通过（含接口样本逐字比较）；步骤 1–4 期间另跑过 `internal/library`、`internal/listening`、`internal/playback`（本任务没有改动它们）与逐项变异检查。
前端：`npm run lint`、`npx tsc --noEmit`、全量 Vitest 67 个文件 1358 项、`npm run build` 后内嵌产物与提交的逐文件一致（每个涉及前端的提交都在导出的暂存树上重复类型检查、全量 Vitest 与构建比对）、
Playwright 路由模拟 220 项通过；真实服务 27 项通过。没有运行 Go 全量测试：本任务只改了 `operations`、`catalog`、`identity` 与 `app` 的装配，其余包由各步骤的相关测试覆盖。
既有的同步歌词用例（`player.spec.ts` 同步歌词）偶发失败（M3 之前的基线上也出现过，单独运行通过），与 M3 无关；步骤 7 的全量运行中通过。

### 与设计的差异

已补记在 [design.md](design.md) 第 14 节：`Kind` 与逆执行器随撤销接口放在第 4 步；`RevertExecutor` 锁记录与读取分成两条语句（并发测试发现 READ COMMITTED 下带连接的 `FOR UPDATE` 读不到刚提交的撤销记录）；
视图是每个字段一次主键上的 `LEFT JOIN` 而不是一次透视；前端的命令 hook 合并成 `useReleaseCommands`，命令状态由页面持有，版本换组先导航再刷新；折叠面板抽成 `Disclosure`；
操作记录的查询 `staleTime` 为 0；撤销成功后焦点带到区块标题；收尾截图后修正了“撤销”按钮的对齐。

### 已接受的限制与后续

- 被扫描判定为新发行时（整张替换等）不继承旧发行的修正与归组（G8），认领属于 M4；播放队列里已有条目的专辑名与专辑艺人不随修正即时更新（刷新页面或重新加入队列后用新值）；清除修正时以提交时的扫描值为准；不记录被拒绝的尝试。
- 浏览器验证只有 Linux Chromium 的自动化证据（五种宽度、axe、键盘），没有 Safari、Firefox 与 Windows 的实际渲染，也没有人工读屏验证。
- 部署后无需额外操作：迁移 0017 只新增表、列与视图，已有发行没有修正、版本号为 1，读取结果与升级前相同，不需要重扫。
- 后续（均未启动）：成员写操作接入操作记录（G6，独立小任务）；M4 的规划（认领、整组合并拆分、默认版本、问题处理）。F1 手工实播与 F3 远端 CI 仍按用户裁决延后。
