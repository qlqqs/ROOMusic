# Research: 艺人合并、改名、忽略候选的现状与可撤销改造（子任务 G）

- **Query**: 艺人合并/拆分/改名/忽略候选的现有实现、撤销需要恢复与拦截什么、外部对应与 C 层自动合并、D 层工具开放、前端、测试样本与风险
- **Scope**: internal（只读代码与文档，未运行测试）
- **Date**: 2026-11-23

先列事实（带 file:line），推断的部分单独标“推断”。

---

## 0. 决策与合同出处

| 出处 | 内容 |
| --- | --- |
| `docs/architecture/adr/0006-auto-curation-and-sources.md:54` | 证据表：艺人合并、拆分、改名、忽略候选没有撤销，所以“不可撤销的操作不能自动执行” |
| 同上 `:116` | 结构操作（含艺人合并改名）“不是字段值，没有层；靠操作记录的来源区分人工与自动” |
| 同上 `:140` | 结构操作撤销沿用各自规则；“之后被人工改过的结构，自动撤销被拒绝并在结果里列出” |
| 同上 `:180-182` | 艺人合并、改名：先做成可撤销。程序层：两个本地艺人的外部对应指向同一个外部艺人时合并；其余由模型提出并审查。忽略候选：先可撤销，之后可以自动。艺人拆分：不自动提出，纠正错误合并靠撤销合并 |
| 同上 `:185-186` | 强证据 = 已保存、状态为生效的外部对应；模型声称有依据而库里没有时转人工 |
| 同上 `:265-269` | 审查第 2 版：不可撤销项转人工（不变）；“强证据类操作（归组、发行合并拆分）证据里没有生效的外部对应时后端直接转人工”。这张表里艺人合并不在“强证据类操作”中 |
| 同上 `:312` | 取代 ADR 0003 2026-11-04（R4）：合并、改名、忽略候选改为可撤销，**恢复到操作前原样**（子任务 G）；R4 对其他新写操作仍适用 |
| 同上 `:344`、`:350` | C 的交付把“艺人对应与同一外部艺人的合并”留给 G |
| 同上 `:389` | 验收：自动的艺人合并只在强证据下发生，每一项都能撤销并回到操作前原样 |
| `docs/architecture/adr/0003-governed-operations-decisions.md:16` | 2026-11-04：四种艺人操作接 `operations.Run`，R4 不提供独立 Revert，用正常反向命令恢复旧 ID |
| 同上 `:18` | 2026-11-22 修订：合并、改名、忽略候选改为可撤销并恢复到操作前原样；艺人拆分不自动提出 |
| `.trellis/spec/backend/governance-contracts.md:211-212` | R4 轻量标准；“2026-11-22 决定把艺人合并、改名与忽略候选改为可撤销（尚未实现，实现前仍按本条）” |
| `.trellis/spec/backend/governance-contracts.md:121-122` | 列表 `revertible` = 未被撤销 ∧ 不是撤销 ∧ 目标可见 ∧ `edit_revision == result_revision`（自动层例外：看“仍是这条操作写的”） |
| `.trellis/spec/backend/artist-contracts.md:75` | 四 Kind 全部 R4 不可 Revert，target_type=artist；restore 只恢复当前 redirect 到本人的旧 ID |
| `.trellis/spec/backend/artist-contracts.md:45` | 写锁顺序：OPER 幂等/必要旧 operation 锁 → CATP → 发行 → 组 → 艺人/名字/派生 → 本人收藏 state；扫描不写 bindings、assignment_overrides、overrides、candidate_ignores、edit_revision |
| `.trellis/spec/backend/curation-contracts.md:474` | 15.7：艺人的外部对应与同一外部艺人的自动合并在 G |
| `.trellis/tasks/11-22-auto-curation-sources/design.md:223-225` | G 要点：合并记录已存前态（名字键原归属、作用域覆盖原归属、涉及旧身份）；撤销按前态逐项恢复，**包括链式重定向与绑定行原来的操作标识**，不用“拆分并恢复”近似；合并期间新产生的收藏等用户数据留在合并后的艺人上；改名撤销恢复原来的主名意图 |
| `.trellis/tasks/11-22-auto-curation-sources/implement.md:160-164` | G 范围与验证：逐项断言名字绑定、作用域归属、链式重定向、收藏解析；合并之后有新的人工修改时拒绝撤销 |
| 同上 `:140` | D 交付记录：后台白名单在 `app/curation_model.go`；结构类工具开放时在审查策略加“证据里没有生效的外部对应就转人工”，策略版本升到 3；例外表 `tool` 约束要随迁移放开 |
| `.trellis/tasks/archive/2026-11/11-22-auto-curation-c-program/prd.md:62` | C：本任务不建艺人对应；按外部 ID 反查艺人与重定向解析随 G 接入 |

---

## 1. 现有实现

### 1.1 表（`backend/migrations/0026_artist_catalog.sql` 等）

| 表 | 位置 | 作用 / 关键约束 |
| --- | --- | --- |
| `artists` | 0026:6-9 | `id, initial_name, edit_revision(默认 1), created_at`；艺人身份不物理删除 |
| `artist_name_keys` | 0026:10-15 | 规范化名字键；`initial_artist_id NOT NULL`（初始路由）；种子别名也只写这里（0026:125-131，注释“固定种子只建立初始路由，不写人工绑定”） |
| `artist_names` | 0026:17-21 | 写法；`origin` 后来加 `auto`（curation 合同 §2） |
| `artist_credits` | 0026:22-30 | 署名事实，`layer` 加了 `auto`；扫描/修正/自动值生成 |
| `artist_name_bindings` | 0026:33-37 | 人工意图：`key_id PK → artist_id`，`operation_id NOT NULL`（延迟外键）。**没有行 = 走 initial_artist_id** |
| `artist_assignment_overrides` | 0026:39-47 | 人工意图：按 track 或 release 作用域把名字键分给某艺人；`operation_id NOT NULL`；track/release 外键 `ON DELETE RESTRICT` |
| `artist_overrides` | 0026:49-56 | 人工意图：`display_name`、`redirect_artist_id`、`name_operation_id`、`redirect_operation_id` |
| `artist_effective_credits` | 0026:58-63 | 派生：每条署名解析到的艺人 |
| `artist_profiles`、`artist_search_names` | 0026:69-83 | 派生：主名、排序键、首字母、搜索名字（`source_artist_id` 用于被重定向身份的主名） |
| `artist_candidate_signals`、`artist_alias_signals`(0031) | 0026:95-101 | 派生候选信号 |
| `artist_candidate_ignores` | 0026:103-107 | 人工意图：名字键对 PK，`operation_id NOT NULL` |
| `operations_artist_targets` | 0026:119-123 | 操作 ↔ 艺人关联；`operations_artist_targets_artist_idx(artist_id, operation_id)` |
| `artist_favorites` | 0027 全文 | `PRIMARY KEY(user_id, artist_id)`；`artist_id` 是原锚点，`resolved_artist_id` 是可重建投影 |

解析顺序（`artistgraph/refresh.go:93-97`）：`COALESCE(track覆盖, release覆盖, binding, initial_artist_id)` 得 route，再 `COALESCE(o.redirect_artist_id, route)`——**只解一跳重定向**，因此合并必须把指向 source 的旧重定向扁平化（`artist_merge.go:47-77`）。

新名字键（扫描遇到没见过的规范化键）一律新建一个艺人并作为 `initial_artist_id`（`artistgraph/names.go:99-108`）；已有键只追加写法。

### 1.2 执行器、请求与 Kind

| 操作 | 执行器 | 请求/指纹 | HTTP |
| --- | --- | --- | --- |
| 合并 | `governance/artist_merge.go:10-144` | `ArtistMergeRequest`（`artist_requests.go:71-95`，双方 revision） | `POST /api/v1/artists/{id}/merge`（`catalog/httpapi/artist_governance.go:34`） |
| 拆分 | `artist_split.go:12-129`，范围 `artist_split_scope.go` | `ArtistSplitRequest`（`artist_requests.go:97-180`，basis 令牌，target=new/restore） | `:35-36` |
| 改名 | `artist_rename.go:10-62` | `ArtistRenameRequest`（`artist_requests.go:182-219`，mode=set/auto） | `PATCH .../name`（`:37`） |
| 忽略候选 | `artist_candidate_ignore.go:43-146` | `ArtistCandidateIgnoreRequest`（`artist_requests.go:221-278`，键对、ignored） | `POST .../candidate-ignore`（`:38`） |

- 共同前置：`lockArtistCatalog`（`artist_state.go:28-40`）= CATP 写锁 + `artistgraph.Ready`，未就绪 `409 artist_catalog_not_ready`；`lockArtists` 按 UUID 序 `FOR UPDATE`（`:71-99`）；`checkArtist` 核对 revision 与“未被重定向”（`:100-108`）。
- 派生刷新 `ArtistGovernance.refresh`（`artist_state.go:232-249`）：`artistgraph.Refresh(releases)` → `RefreshProfiles(artistIDs)` → 有 mapping 时调 app 注入的 `RefreshArtistResolution`（收藏解析）。注释：艺人治理不改发行文本，别名搜索由 profiles 搜索投影与有效署名联结传播（不调 `searchindex.RefreshRelease`）。
- 上限（`artist_requests.go:14-21`）：名字键 1000、显式覆盖 10000、轨 100000；记录状态 JSON 8 MiB（`artist_state.go:138-147`）。
- Kind 注册：`app/app.go:226`（`ArtistMergeKind{}, ArtistSplitKind{}, ArtistRenameKind{}, ArtistCandidateIgnoreKind{}`，均为无字段结构体，不接收 suppression 写入器）。
- 四个 Kind 的 `Revert` 都返回 `artistNotRevertible()` = `409 not_revertible`“请通过拆开、合并或再次改名恢复”（`artist_describe.go:159-205`）；`describeArtistRecords`（`:96-158`）从不设置 `Description.Revertible`（恒 false）。HTTP 列表 `revertible = description.Revertible && 不是撤销 && 未被撤销`（`operations/httpapi/handlers.go:177`）。
- `RevertExecutor`（`operations/kind.go:66-98`）：先 `LoadForUpdate` 原记录（OPER 侧锁），拒绝撤销记录/已撤销，再交 `kind.Revert`；`Outcome.ResultRevision` 必须等于 `ExpectedRevision+1`（`operations/operations.go:167`）。`Run` 写记录后 `insertArtistTargets` 把主目标与 `Targets` 中的艺人写入 `operations_artist_targets`（`operations.go:338-362`）。

### 1.3 各操作改了什么、记录里存了什么

记录状态类型 `artistRecordState{schema_version:1, artists[], bindings[], assignments[], ignores?, changes?}`（`artist_state.go:126-137`）；`artistIdentity` 含 `id,name,revision,display_name,redirect_artist_id,name_operation_id,redirect_operation_id`（`:42-50`）。

**合并（source 并入 target）** `artist_merge.go`
- before（`:32-77`）：`loadArtistMappings(source)` 读出所有 canonical 为 source 的名字键路由（`key_id`、原 `artist_id`=binding 或 initial、`operation_id`=binding 行的操作，**null 表示原来没有 binding 行**）与作用域覆盖（`release_id/track_id/key_id/artist_id/operation_id`）（`artist_state.go:150-198`）；`Artists=[target, source, 所有 redirect→source 的旧身份]`，各含当时 revision 与 overrides 四列。
- 改动：
  1. 上述键全部 upsert `artist_name_bindings(key→target, operation_id=本操作)`（`:93-99`）；
  2. `artist_assignment_overrides` 中 `artist_id ∈ oldIDs` 的行改成 target、`operation_id=本操作`（`:100-103`）；
  3. `artist_overrides` 对 oldIDs（source + 扁平化旧身份）upsert `redirect_artist_id=target, redirect_operation_id=本操作`，显示名保留（`:104-108`）；
  4. 只给 source 与 target 递增 `edit_revision`（`:109-111`），扁平化的旧身份不递增；
  5. refresh（受影响发行 = target∪source 有效署名的发行；profiles = target+oldIDs；收藏 mapping oldID→target）（`:112-118`）。
- after：`Artists` 为更新后的身份；`Bindings/Assignments` 是 before 的复制并改成 target+本操作；`Changes={kind,source{id,name},target{id,name},name_count,release_count,track_count}`（`:119-140`）。
- Outcome：目标 target，`Targets=oldIDs`，`ExpectedRevision=target.Revision`，`ResultRevision=+1`（`:141-142`）。

**改名** `artist_rename.go`
- 改动：upsert `artist_overrides(display_name=新名或 NULL(auto), name_operation_id=本操作)`（`:33-36`）；递增该艺人 revision；refresh（无 mapping）。
- before/after：各一个 `artistIdentity`（含 `display_name`、`name_operation_id`）；`Changes={kind,from{name,source},to{name,source}}`（`:48-58`）。

**忽略候选（或恢复提示）** `artist_candidate_ignore.go`
- 校验键存在、键对在当前解析下属于这两位艺人（`:69-86`）。
- before：两位身份 + 每个键对 `{left_key_id,right_key_id,operation_id}`，`operation_id` 为 null 表示当时没被忽略（`:87-111`）。
- 改动：ignored=true 时 `INSERT ... ON CONFLICT DO NOTHING`（**已被更早操作忽略的键对保留原 operation_id**）；ignored=false 时 DELETE（`:115-125`）；递增两位艺人 revision；**不调 refresh**（候选列表是读时计算，`artist_candidates.go`）。
- after：键对逐项更新 operation_id（新忽略的写本操作、恢复的写 null）；`Changes={kind,other_artist,ignored}`（`:129-142`）。

**拆分** `artist_split.go`
- target=new：新建艺人（可带显示名）；target=restore：要求 target 当前 redirect 到 source，把它的 `redirect_artist_id=NULL, redirect_operation_id=本拆分操作`（`:78-86`）。
- 把计划内的键 upsert binding → target、作用域覆盖写成 target（`:87-99`）；递增 source（restore 时也递增 target）。
- `ArtistSplitKind` 同样不可撤销（`artist_describe.go:174-183`）。

### 1.4 为什么标为不可撤销

- R4（2026-11-03，单管理员）：“有反向操作可以恢复的类型不提供撤销”（`governance-contracts.md:211-212`、ADR 0003 decisions:15-16）。反向命令：合并↔拆分（restore 旧 ID）、改名↔再改名、忽略↔`ignored:false`。
- 代码注释同此（`artist_describe.go:163,174,185,196`）；前端文案同此（见第 6 节）。

### 1.5 拆分恢复来源读取（`merged_sources.go`）

- `GET /artists/{id}/merged-sources` 只列 `redirect_artist_id = 本人` 的旧身份，并**沿 `redirect_operation_id` 回溯类型化的 merge 记录**：要求记录 `Type==artist_merge`、before/after 都含该身份、`after.redirect_operation_id == 该操作`；扁平化重定向（before 已有 redirect）要求 `after.Revision == before.Revision`，再按 before 的 redirect 继续回溯（`merged_sources.go:193-224`、`:226-269`）。
- 证据不满足返回 `errMergedSourceEvidence`（`:161`）→ 读取失败。**推断**：撤销合并若把 `redirect_operation_id` 写成撤销操作 ID 或不还原，会让之后的 merged-sources 读取在链上报错；必须按前态原样还原这一列。

---

## 2. 撤销各操作需要恢复什么、什么应阻止撤销

### 2.1 合并

需要恢复（全部来自现有 before/after）：

| 对象 | 恢复方式（推断） |
| --- | --- |
| 名字绑定 | 对 before.Bindings 每项：`operation_id` 为 null → 删除 binding 行；否则恢复 `(artist_id, operation_id)` 原值（原值可能是被扁平化的旧身份 A，不是 source） |
| 作用域覆盖 | 对 before.Assignments 每项按 `(release_id/track_id, key_id)` 恢复 `artist_id, operation_id` |
| 重定向与链 | 对 before.Artists 中 target 以外的每个身份恢复 `redirect_artist_id`、`redirect_operation_id`（source → 原值，通常 NULL 或某拆分操作 ID；扁平化的 A → 回到 source，操作 ID 回到 A 原来那条 merge）；显示名两列未被合并改，无需动 |
| revision | 撤销是新操作，source、target 再递增（ResultRevision=当前+1）；不能把数字还原 |
| 派生 | `artistgraph.Refresh(受影响发行)`：发行集取撤销前 target 的有效署名所在发行（合并后包含 source 原发行）；`RefreshProfiles(target+oldIDs)`；收藏 mapping：source→source、A→source（按恢复后的一跳 canonical） |
| 候选/忽略 | 候选是读时计算（两位解析到同一位时不列出，`artist-contracts.md:71`），无需写；忽略表合并未动 |
| 搜索 | 合并本身只经 profiles/search names 传播，不刷新发行搜索（`artist_state.go:239`），撤销同理 |
| 收藏 | `personal.RefreshArtistResolution` 只更新 `resolved_artist_id` 并递增受影响用户 state（`personal/artist_resolution.go:49-82`）；原锚点保留，所以锚在 source 上的收藏自动分回 |

现有记录是否够用（事实 + 推断）：
- 事实：before.Bindings 有“原来是否有 binding 行”（operation_id 可空）、原 artist_id 与原操作；before.Assignments 有原 artist_id 与原操作；before.Artists 有每个受影响身份的 overrides 四列与 revision；after 有合并留下的状态（`artist_merge.go:119-136`）。schema_version 一直是 1，`merged_sources` 已依赖这份快照。
- 推断：恢复意图所需的信息在**现有记录里是完整的**，不需要补抓前态。记录没有区分 `artist_overrides` 行“不存在”与“四列都是 NULL”，但所有读取都是 LEFT JOIN 取列（`artist_state.go:53-56`、`refresh.go:97`），两者等价；缺的只有受影响发行列表，可在撤销时重算。
- 推断：撤销记录的 before/after 需要带同一 `changes`（kind=artist_merge），因为 `decodeArtistChanges` 要求 `changes.kind == record.ChangeType()`，撤销记录的 ChangeType 是原类型（`artist_describe.go:53-66`、`operations/records.go:48-53`），否则列表里 changes=null。

应阻止撤销的情况：
- 事实：现有通用规则是“目标 `edit_revision == result_revision`”（`governance-contracts.md:109`）。艺人的每种操作都会递增所涉艺人的 revision（合并递增 source/target，拆分递增 source 与 restore 的 target，改名递增本人，忽略递增两位）。
- 推断：**只用 revision 相等会让链式合并无法按倒序撤销**。例：merge1(A→B)、merge2(B→C)。先撤 merge2 会再递增 B（以及 C），此时 merge1 的 target B 的 revision 已经不等于 merge1.result_revision，按批倒序撤销（`curation/revert.go` 按时间倒序逐条）会跳过 merge1。改名、忽略同样：同一艺人上的两条自动操作，撤后一条后前一条撤不了。
- 推断的可选判定（与“自动层看是否仍是这条操作写的”一致）：
  1. 归属核对：after.Bindings 的每个键当前 binding 仍是 `(target, 本操作)`；after.Assignments 每行仍是 `(target, 本操作)`；每个 oldID 当前 `redirect_artist_id=target` 且 `redirect_operation_id=本操作`；target 当前未被重定向。
  2. 之后没有新决定：经 `operations_artist_targets(artist_id, operation_id)` 查 target 与 oldIDs 上 `created_at` 晚于本操作、不是撤销、未被撤销的操作；有则拒绝（“合并之后有新的人工修改时拒绝撤销”；这条同时拦住之后的改名、忽略、拆分、再合并）。撤销记录本身不算新决定，所以倒序撤销成立。
  3. 不满足返回 `409 revert_conflict`（沿用现有码），`Describe` 用同一判定批量计算 `Revertible`。
- 推断：扫描之后新增的事实（新曲目、新写法、新署名）不阻止撤销：扫描不写意图表（`artist-contracts.md:45`），撤销恢复意图后按当时事实重算派生即可。

### 2.2 改名

- 恢复：`artist_overrides.display_name`、`name_operation_id` 回到 before.Artists[0] 的值；递增 revision；refresh（发行集 = 该艺人有效署名发行，profiles = 本人，无 mapping）。
- 现有记录：before/after 都有 `display_name` 与 `name_operation_id`（`artist_rename.go:43-50`）。推断：够用。
- 拦截（推断）：当前 `name_operation_id == 本操作` 且显示名等于 after；本人之后没有未撤销的操作（同 2.1 第 2 条）；艺人已被重定向时也会被第 2 条拦住（合并递增并关联 source）。
- 来源为 curation 的改名被撤销时写“不再提出”（推断：`target_type='artist'`、subject 如 `artist_name`、值为规范化名或 auto）。

### 2.3 忽略候选

- 恢复：对 before.Ignores 每个键对——before 有 operation_id 而当前没有 → 以原 operation_id 重新插入（恢复“恢复提示”这条操作删掉的行）；before 为 null 且当前行 operation_id == 本操作 → 删除；before 与 after 相同的键对（本次 ON CONFLICT 没动的）不碰。递增两位 revision；不需 refresh。
- 现有记录：before/after 的 `ignores[]` 区分了“已被更早操作忽略”和“本次新忽略”（`artist_candidate_ignore.go:133-141`）。推断：够用。
- 拦截（推断）：每个本次改动的键对当前状态仍等于 after（行在且 operation_id=本操作，或行不在）；两位艺人之后没有未撤销的操作（或只看键对归属，二选一，需定）。
- 已忽略列表按忽略操作的 `operations.created_at` 排序（`artist-contracts.md:71`），用原 operation_id 恢复能保持原顺序。

### 2.4 拆分保持不可撤销（确认）

- ADR 0006:182：“艺人拆分不自动提出；它是纠正错误合并的手段，由撤销合并覆盖”；ADR 0003 decisions:18 同；`ArtistSplitKind.Revert` 现为 `not_revertible`（`artist_describe.go:178-180`）。G 的范围（implement.md:162）只列合并、改名、忽略候选。
- 推断：拆分保留 `not_revertible`，并作为“之后有新的人工决定”阻止更早合并的撤销（拆分 restore 会把旧身份的 `redirect_operation_id` 改成拆分操作，`artist_split.go:79`）。

---

## 3. 外部对应、MusicBrainz 艺人数据与 C 层自动合并

### 3.1 `external_links` 对艺人的现状

- 表允许 `object_type='artist'`（`0060_curation_sources.sql:11`），唯一 `(source, object_type, object_id)`、反查索引 `(source, object_type, external_id)`（`:18-20`）；**external_id 不唯一**，多个本地艺人可以对应同一个外部艺人。
- 合同：对 MusicBrainz，艺人 → artist；`object_id` 无外键；“艺人合并靠重定向，被并入的艺人身上的对应仍在——按外部 ID 反查艺人时要解析重定向”（`curation-contracts.md:206-212`）。
- 生产写入口只有 `Writer.ApplyLinks`，它**只接受 `release | track | release_group`**（`curation/external_link.go:77`），成员校验也只有这三种（`:88-103`）。`ObjectsLinkedTo` 只做原样反查、不解重定向（`curation/links.go:71-85`）。
- 结论（事实）：目前没有任何代码写艺人对应；C 程序层不建艺人对应（C PRD:62）。

### 3.2 可用的 MusicBrainz 艺人数据

| 来源 | 位置 | 内容 |
| --- | --- | --- |
| 能力接口 `ArtistFinder` | `sources/sources.go:44-46`、`:193-200` | `ArtistByID`、`SearchArtists` → `Artist{ID,Name,SortName,Disambiguation,Type,Country,Aliases[]}` |
| MB 适配器 | `sources/musicbrainz/finders.go:132-145` | `ArtistByID` 用 `inc=aliases`；搜索取 10 条 |
| 闸门 | `curation/source_finders.go:110-131` | `Sources.ArtistFinder("musicbrainz")`；目前**没有生产调用方** |
| 发行/曲目/录音的署名 | `sources/sources.go:156-191`、`musicbrainz/project.go:5-8,14,28,50` | `Release.Artists`、`Track.Artists`、`Recording.Artists` 都是 `[]Credit{ArtistID, Name, JoinPhrase}` |
| 标签里的 MBID | `0059_track_identifiers.sql`；`catalog/curation_evidence.go:236-239` | `track_identifiers.kind` 含 `mb_artist`、`mb_album_artist`（带 `ordinal`），证据按 kind 聚合 |
| 已有用法 | `curation/program/match_album.go:99-105` | 只把 `mb_album_artist`/`mb_artist` 当作相反证据 `artist_tag_mismatch`；`enrich.go:85,211`、`track_fields.go:24,32` 只用署名的拼接文字 |

- `catalog.CurationEvidence` 不含本地艺人 ID 或署名位置（`catalog/curation_evidence.go:46-76`）；`program` 不导入 `catalog/internal`（`curation-contracts.md:392`），读本地有效署名需要 catalog 新增公开读取。

### 3.3 程序层可以接入的位置

- 匹配“把握足够”后的写入链 `writeMatch`（`curation/program/enrich.go:234-302`）：步骤依次 `links` → `fields` → `track_fields/<id>` → `group`，每步经 `u.write(step, ...)` 派生幂等键（`unit.go:25-28,59-91`）。`planMatch` 已持有外部发行（含 `Artists` 与各曲目 `Artists`）和曲目配对 `m.Pairs`（`enrich.go:199-230`）。
- 强证据样板：`Writer.JoinGroup`（`curation/join_group.go`）= `sourceSpec`（来源 `curation`、provider、batch、幂等键；`external_artwork.go:44-59`）+ `checkWrite`（开启者、批次、模型领取；`model_run.go:70-`）+ 锁内核对强证据 `checkGroupEvidence`（读 `external_links` 与写下它的 `external_link` 记录后态，`FOR SHARE`）+ 最近一次结构决定是人做的 → `409 manual_structure` + 不再提出标记 → `nothing_to_apply`，然后调用治理执行器。
- 撤销时写标记的样板：`GroupChangeOperations(suppressions)`，原操作 `origin.source=='curation'` 时在撤销事务里写 `group_join` 标记（`governance/revert.go:28-37,264-274`；app 注入 `app.go:225`）。
- 标记表 `curation_suppressions.target_type` 无 CHECK（`0058_auto_curation.sql:91-102`）；按发行列出时只覆盖 release/release_group/track（`curation/suppressions.go:50-52`）。
- 按批/按来源撤销不按类型取候选（`curation-contracts.md:118`），按倒序逐条 `RevertExecutor`，未注册的类型记 `not_revertible`（`curation/revert.go:77-98`）；艺人 Kind 已注册，变成可撤销后自动纳入。

### 3.4 C 自动合并（推断）

- 建对应：在专辑方式把握足够时，若本地发行级有效署名（仅取 scan/correction 层，不取 `auto` 层，遵守“自动层不能证明自己”）与外部发行署名数量相同且逐位对应（最简单：两边都只有一位），把本地艺人对应到该 `Credit.ArtistID`；曲目艺人可同理按已配对的录音/曲目署名。标签 `mb_album_artist`/`mb_artist` 只有一个值且本地只有一位署名时也可作为 `tag_id` 依据。名字是否必须松比较相等需要定：要合并“周杰倫/Jay Chou”这类别名，就不能要求名字相等，只能要求位置与数量一一对应。
- 写入：给 `ApplyLinks` 增加 `artist` 类型与成员校验（艺人在该发行上有有效署名），或另设入口；对应已存在且是别的外部艺人 → `occupied`，可视为“这个本地艺人可能混了两个人”的信号，不合并。
- 判定合并：反查同一 MB 艺人 X 的所有本地艺人并解一跳重定向，得到两个及以上不同 canonical → 强证据。锁内复核：两位都未被重定向、两条对应仍在且由 `external_link` 记录写下（同 `checkGroupEvidence` 的做法）、没有 `artist_merge` 标记、两位之间没有人工的拆分/人工的忽略候选（类比 `manual_structure`）。
- 新入口 `Writer.MergeArtists`：Source `{Kind:"source", Provider:"musicbrainz"}`（模型层为 `model`），批次与 UUIDv5 幂等键（艺人对、X、规则版本），调用 `ArtistGovernance.MergeExecutor`（需要 `curation` 能拿到注入了收藏解析的 `ArtistGovernance`）。target 的选取需要确定性规则（例如 ID 较小或参与更多）。
- 撤销：`ArtistMergeKind` 需要像 `GroupChangeOperations` 一样注入标记写入器；原操作来源为 curation 时写 `target_type='artist'`、subject `artist_merge`、值为另一方 ID（两个方向各一条或无序对的哈希）。

---

## 4. 模型层 D 的现状与开放所需

| 项 | 现状（事实） |
| --- | --- |
| 工具定义 | `catalog/governance/stewardtools/tools.go:69-72`：`merge_artists`、`split_artist`、`rename_artist`、`ignore_artist_candidate`，`Revertible=false`，描述写“不可撤销” |
| 执行 | `adapter.write`（`tools.go:83-104`）直接 `operations.Run`，`Origin{Source: caller.Source, ConversationID, PlanID, Mode, TokenID}`，**没有 provider_kind/provider/batch**；命令映射 `commands.go:84-129` |
| 预览 | `preview.go:154-221`（主名前后、并入谁、键对忽略前后） |
| 后台白名单 | `app/curation_model.go:103-111` 不含任何艺人写工具；`withBackgroundPolicy`（`:114-164`）只给 `correct_release/correct_track` 改道到 `curationtools.AutoCorrect*`，并在预览前核对批内（`:167-216`） |
| 审查证据登记 | `catalog/stewardtools/review.go:41-48` `SupportsReviewEvidence` 只含 `correct_track, correct_release, change_release_group, set_default_release, review_release_issue, claim_release`；`ReadReviewTargets`（`:51-`）只读 release/track/group 目标；外部证据 `reviewExternal` 只按 release 目标取对应（`app/steward.go:296-320`）；未登记 → `ErrReviewEvidenceUnsupported` → 转人工（`app/steward.go:275-278`） |
| 策略 | `steward/runtime/review.go:20` 版本 2；`ReviewPolicyRules`（`:27-38`）写明“后端工具登记为不可撤销的项转人工确认，不送审；艺人合并、拆分、改名、忽略候选以及撤销操作属于此类”；**没有强证据规则**；执行判定 `reviewPolicy`（`:161-172`）只看项数与 `Revertible` |
| 规范同步 | `.trellis/spec/backend/steward-review-contracts.md:132-144` 要求与 `ReviewPolicyRules` 逐字一致（第 140 行即上面那句）；未找到逐字比对的测试 |
| 其他依赖 Revertible 的行为 | `steward/runtime/run.go:512`：assistant 模式下 Revertible 且在免确认名单里的工具可直接执行；`plan_store.go:205`：接受的 Revertible 项可加入免确认名单。翻成 true 会改变对话里艺人工具的确认行为 |
| 待定项 | `curation_pending_items.target_type CHECK (='release')`（`0062_curation_program.sql:20`） |
| 例外 | `curation_model_exceptions.tool` CHECK 现为 `('resolve_release_match','correct_release','correct_track')`（`0065_track_corrections.sql:50-52`，0064:24 原为前两项）；`pending_item_id NOT NULL`（0064:22）；`CreateException` 要求待定项属于该会话（`curation/model_exceptions.go:50-79`）；采纳/拒绝按工具分支处理（`curation-contracts.md:684-686`） |

开放给后台所需（推断）：
1. 三个工具 `Revertible=true`、改描述；`split_artist` 保持 false。
2. 加入 `backgroundToolAllowed`；`Source==curation` 时改道到 `curation.Writer` 的艺人入口，带模型来源、模型批次与开启者身份；批内范围需要定义（待定项是发行级的：限“本批发行上有有效署名的艺人”，或让待定项支持 `target_type='artist'`，后者需迁移）。
3. `SupportsReviewEvidence` 登记三种工具，并给 `ReadReviewTargets` 增加艺人目标（id、revision、主名、名字、参与数、是否重定向）及艺人的外部对应；`reviewExternal` 目前只看 release 目标。
4. 审查策略升到 3：从“不可撤销”句中去掉合并、改名、忽略候选（拆分与撤销操作仍在）；ADR 第 9 节的强证据规则写入（归组、发行合并拆分）；是否把艺人合并也列为强证据类操作需定（ADR 0006:180 的“其余由模型提出并审查”与 :269 的表不含艺人合并）。规范第 132-144 行同步。
5. 迁移放宽 `curation_model_exceptions.tool` CHECK；实现三种工具的例外采纳（写人工、来源 `review_decision=human`）与不采纳（写不再提出标记）。

---

## 5. 前端

| 位置 | 现状 |
| --- | --- |
| `frontend/src/features/governance/change-summary.ts:51-58` | 类型标签：合并艺人 / 拆开艺人 / 修改艺人主名 / 忽略（恢复）重复艺人提示 |
| 同上 `:59-71` | 撤销记录标签只覆盖发行类 kind，艺人类落到默认“撤销” |
| 同上 `:134-150` | `linesOf` 艺人分支注释“不能撤销（reverse 用不到）”，没有反向文案 |
| 同上 `:243-251` | `notRevertibleReason`：`artist_merge`→“需要时可以拆开”、`artist_split`→“需要时可以再合并”、`artist_rename`→“需要时可以再改名”、`artist_candidate_ignore`→“不能撤销”；这几行在 `reverted_by` 判断之前 |
| `components/revert-operation-dialog.tsx:27-41` | `revertIntentOf`：`release===null && album===null` 时返回 null（艺人记录无法发起撤销）；命令类型是发行意图 |
| 同上 `:43-55` | 标题表没有艺人类型；`:102` 正文写“《发行或专辑标题》会改回” |
| `components/operation-list.tsx:24-33,119-123` | 艺人目标显示为链接；按钮位置写 `notRevertibleReason` |
| `components/merge-artist-dialog.tsx:10-11` | `MERGE_IRREVERSIBLE_TEXT = "合并不能撤销，需要时可以再拆开。"` |
| `components/split-artist-dialog.tsx:18-19` | `SPLIT_IRREVERSIBLE_TEXT`（拆分保持） |
| `components/rename-artist-dialog.tsx:16,81` | “改名不能撤销，需要时可以再改。” |
| `components/artist-admin-actions.tsx:25`、`hooks/use-artist-commands.ts:70` | 注释：艺人操作都不能撤销 |
| `api/decoders.ts:242-243,357-370` | 记录的 `artist` 摘要与四种 changes 解码；注释“都不能撤销” |
| `features/curation/curation-text.ts:72-90` | 范围撤销的跳过原因（`not_revertible`→“不能撤销”）；标记主题只为曲目字段写了标签（`:118`） |
| `features/steward/tool-labels.ts:264-268` | 管家工具标签 |
| `app/routes/artist-detail-page.tsx:39` | 艺人页用 `useArtistAdminActions` |
| `components/operations-view.tsx:24-56,117` | 支持 `artist` 筛选（“这位艺人的记录”） |

---

## 6. 需要保留或更新的测试与样本

后端：
- `backend/internal/catalog/governance/artist_requests_test.go:57-65` `TestArtistKindsNeverRevert`（四个 Kind 都 `not_revertible`）——需改为只断言拆分。
- `backend/internal/app/artist_samples_integration_test.go:565-571`（`operations/artists` 每项 `revertible==false`）、`:581`（`artists/errors/not-revertible` 用合并记录）、`:583-596`（损坏状态 `revertible==false`）。样本文件 `backend/internal/app/testdata/api-samples/{operations/artists.json, operations/artist-*.json, artists/errors/not-revertible.json, governance/artist-*.json}`；`TestArtistSampleRecorderOwnership`（`:813-`）。
- `backend/internal/catalog/governance/artist_integration_test.go`：`TestPostgreSQLArtistMergeRestoreAndDurableIntent`(143)、`...GuardRollbackAndConcurrentRevisions`(322)、`...OperationTargetsUnionAndDescribeCorruption`(417)、`...CandidateIgnoreRescanAndRestore`(513)、`...IgnoredCandidates*`(616/666/695)、`...ScopedRestoreUsesOldIdentity`(844) 等——保留，撤销断言新增。
- `merged_sources_integration_test.go`：`ChainRestoreAndRemerge`(77)、`BindingChangesAndEmptyKeys`(116)、`ScopedMerge`(141)；`merged_sources_test.go:22` 类型化链回溯——撤销后链证据必须仍成立。
- `backend/internal/personal/artist_favorites_integration_test.go:56` 收藏锚点随合并/拆分——撤销合并后的收藏解析可参照。
- `backend/internal/catalog/governance/stewardtools/c6_integration_test.go:304-` 艺人工具成功路径与版本号；`:471-475` 输入错误。
- `backend/internal/steward/httpapi/plans_c6_integration_test.go:241` 用 `rename_artist` 作“其他 shared 工具”。
- 前端样本契约 `frontend/src/test/api-contract.test.ts:742`（`artists/errors/not-revertible`）、`:822`。

前端：
- `features/governance/artist-operations-step5.test.ts:19-22`（三种原因文案）、`:35-63`。
- `app/artist-governance-step5.test.tsx:110,132,146,358`（对话框“不能撤销”文案）。
- `e2e/artists-governance.spec.ts:21,37`。
- `test/governance-data.ts:69,562`（记录夹具）。

---

## 7. 风险

1. **链式合并**（A→B 再 B→C）：第二次合并把 A 扁平化到 C 且不递增 A 的 revision（`artist_merge.go:47-77,109`）。撤销 merge2 必须把 A 还原为 `redirect=B, redirect_operation_id=merge1`，否则一跳解析与 merged-sources 链回溯都会错（`merged_sources.go:209-222`）。merge1 只能在 merge2 撤销之后撤（A 当前 redirect_operation_id 不是 merge1）。
2. **撤销顺序与 revision**：撤销会递增 revision（`kind.go` 版本契约），若沿用“revision 相等”判定，倒序撤销链会被卡住（见 2.1）；按批撤销尤其明显。
3. **收藏**：取消收藏按 `resolved_artist_id` 删除全部锚点（`personal/artist_favorites.go:73`），合并期间在 target 上取消收藏会连同 source 锚点一起删掉，撤销无法恢复——与 design.md:225“合并期间新产生的用户数据留在合并后的艺人上”一致，属于已接受的行为。新增收藏总是写 canonical（`:44-53,71`），撤销后留在 target。
4. **扫描在合并后入库**：新键 → 新艺人，与合并无关（`names.go:99-108`）；旧键的新署名按合并后的 binding 归 target，撤销后按恢复的 binding 归回 source（推断：符合“原样”）。作用域覆盖的 track/release 外键是 RESTRICT，覆盖不会因删除而丢失。
5. **并发**：撤销持 OPER（原记录 `LoadForUpdate`）→ CATP → 艺人行，与现有锁序一致（`artist-contracts.md:45`）；需在 CATP 后重读状态再判定。与扫描、程序层写入通过 CATP 串行。
6. **回退迁移**：若新增标记主题、操作来源或例外工具，`.down.sql` 需按现有做法有损删除（例如 0065 down 删除 `tool='correct_track'` 的例外后再收紧 CHECK）。
7. **对话行为变化**：三个工具改为可撤销后，`steward` 模式会把它们送审而不是一律转人工，`assistant` 模式可加入免确认名单（`run.go:512`、`plan_store.go:205`）。
8. **同名不同人**：外部对应按位置对上的规则若放宽到名字相似，会把同名的不同艺人连到一起；ADR 3 节“弱相似不能作为合并依据”。

---

## G 的最小方案建议（推断）

**数据与记录**
- 不改既有记录形状：合并、改名、忽略的 schema_version 1 前后态已含恢复所需的意图行，历史记录也可按新规则判定是否可撤。撤销记录沿用同一状态类型，before/after 对调并带原 `changes`（kind 为原类型），`Targets` 与原操作相同，以便按艺人筛选与 Describe。
- 迁移只放宽约束：`curation_model_exceptions.tool` 加三种艺人工具；如需 artist 级待定项再放宽 `curation_pending_items.target_type`。`curation_suppressions` 无需迁移（target_type 无 CHECK）。
- 新增艺人对应：`external_links` 已支持 `artist`；写入口与成员校验扩展到 `artist`。

**撤销算法**
- 共同：`lockArtistCatalog` → 锁 `before.Artists` 全部身份 → 判定 → 按前态写意图表 → 递增 source/target（忽略为两位、改名为本人）→ `refresh`（发行集在写之前按 target∪oldIDs 计算；profiles 全部涉及身份；合并时传收藏 mapping）→ 来源为 curation 时写不再提出标记。
- 合并：binding 有原行则恢复原 `(artist_id, operation_id)`、无原行则删除；作用域覆盖逐行恢复；source 与扁平化旧身份的 `redirect_artist_id/redirect_operation_id` 恢复原值。
- 改名：恢复 `display_name/name_operation_id`。
- 忽略：仅对本次改动的键对恢复（删本操作写的行，或以原 operation_id 重插被删的行）。
- 拆分：保持 `not_revertible`。

**拦截条件**（`409 revert_conflict`，Describe 同判定）
- 归属：after 中每一项意图当前仍是“本操作写的那样”。
- 之后无新决定：所涉艺人在 `operations_artist_targets` 上没有晚于本操作、非撤销、未被撤销的操作。
- 艺人目录未就绪：`409 artist_catalog_not_ready`（沿用）。

**C 自动合并**
- 在 `writeMatch` 的 `links` 之后增加艺人对应（发行署名与外部署名逐位一一对应、只取 scan/correction 层），之后增加 `artist_merge` 步：同一 MB 艺人反查并解重定向得到两位 canonical，锁内复核对应与记录、标记、人工拆分/忽略，再经新 `Writer.MergeArtists` 调用 `MergeExecutor`（来源 curation/musicbrainz、批次、UUIDv5 键）。撤销 curation 来源的合并写 `artist_merge` 标记。

**D 工具开放**
- 三工具 `Revertible=true`；白名单加入并按 `Source==curation` 改道到 Writer（模型来源、模型批次、批内艺人范围）；登记审查证据（艺人目标 + 艺人外部对应）；策略 v3（文案改、强证据规则加入）；例外工具 CHECK 与采纳/不采纳实现。

**顺序**
1. 后端撤销：三个 Kind 的 Revert、Describe.Revertible、撤销后标记接口（先不接 curation 也可，注入点留好）；测试覆盖逐项恢复、链式倒序、收藏解析、merged-sources 链、拦截。
2. 前端：撤销入口支持艺人目标、标题与文案、`notRevertibleReason` 与对话框提示；样本与测试更新。
3. C：艺人对应写入与强证据合并、标记。
4. D：工具改道、审查证据、策略 v3、例外。

## Caveats / Not Found

- 未找到 `ReviewPolicyRules` 与 `steward-review-contracts.md` 逐字比对的测试（只在规范中声明一致）。
- 未核对扫描对 `mb_artist` 多值的 `ordinal` 与本地署名拆分顺序是否一一对应（`library-scan-tags.md` 未读）；按位置建艺人对应前需确认。
- 未运行任何测试；行号以 2026-11-23 工作区（`main`，`967061c5`）为准。
