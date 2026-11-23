# H 设计：发行的整张并入

路径相对仓库根。调研出处记作“调研 §x”（`research/release-merge-split.md`）；Plan 审查的条目记作 M1–M9（必须改）、S1–S10（建议改），全部处理结果见文末。

## D1 路线：碟实际挂到目标，扫描按原发行认身份

被并入发行 B 的每个碟（`media` 行）改为 `release_id = A`，并记下它原来的发行与碟号。于是所有读取处（发行详情、摘要、搜索、统计、曲目位置、艺人投影、可见性、对比、问题……约 94 处直接连 `tracks → media → releases` 的查询）不用改就把 B 的曲目看成 A 的；B 没有碟，按现有可见性谓词（`releasesql.ReleaseVisiblePredicate`，`catalog/internal/releasesql/release.go:9-15`）自然不可见。全库没有清理无曲目发行的逻辑（只有 `persist.go:130` 删空碟），发行和组都不会被删除。

扫描侧“曲目当前属于哪个发行”改用**扫描归属** `COALESCE(m.origin_release_id, m.release_id)`。B 的候选重扫时仍认出 B（过半沿用），照常更新 B 的扫描字段，写碟时 B 的碟落到 A 上。A 与 B 不争同一个发行，调研 F-R1、F-R4 不出现。

比较过的另两条路：读取处叠加重定向（94 处都要改）；扫描读成员意图（改身份规划结构，风险最大）。

## D2 数据模型（迁移 0067）

- `release_merges(source_release_id uuid PRIMARY KEY REFERENCES releases ON DELETE RESTRICT, target_release_id uuid NOT NULL REFERENCES releases ON DELETE RESTRICT, operation_id uuid NOT NULL REFERENCES operations, merged_at timestamptz NOT NULL DEFAULT clock_timestamp(), CHECK (source_release_id <> target_release_id), UNIQUE (source_release_id, target_release_id))`，索引 `(target_release_id)`。一行表示 B 当前并入 A；撤销删行。
- `media` 加 `origin_release_id uuid NULL`、`origin_position int NULL`，CHECK 两者同空同非空；组合外键 `(origin_release_id, release_id) REFERENCES release_merges(source_release_id, target_release_id) ON DELETE RESTRICT`（S2）；部分唯一索引 `(origin_release_id, origin_position) WHERE origin_release_id IS NOT NULL`。
- `operations_release_targets(operation_id uuid REFERENCES operations ON DELETE CASCADE, release_id uuid, PRIMARY KEY(operation_id, release_id))`，索引 `(release_id)`，照 `operations_artist_targets`（迁移 0026:119–123）。合并与撤销各写 A、B 两行（M4）。
- `operations_type_ck` 加 `release_merge`（人工、对话、MCP、例外采纳）与 `release_auto_merge`（自动整理）；取值清单逐字照 0066 现有清单追加。
- `curation_pending_items.kind` 加 `release_merge`；`curation_model_exceptions.tool` 加 `merge_releases`；`curation_suppressions` 若有 subject/target 约束则放宽以容纳 `target_type=release, subject=release_merge`。
- down（S9）：断言每个来源 B 没有自有碟后，把并入碟还原（`release_id=origin_release_id, position=origin_position`）；照 0065 把 `steward_plan_items` 里引用这些操作的 `operation_id` 置空；删 `release_merge` 待定项、`merge_releases` 例外、`release_merge` 标记、两类操作及其撤销记录、`operations_release_targets`、`release_merges`、新列；`SET CONSTRAINTS ALL IMMEDIATE` 后按 0066 的清单重建约束。合并转移过的收藏回退时不还原，down 注释写明（与 0065 的回退说明同类）。

## D3 碟序

目标 A 的碟分两类：自有碟（`origin_release_id IS NULL`，碟号即扫描碟号，扫描决定）与并入碟。并入碟按（`release_merges.merged_at`、来源发行 ID、原碟号、碟 ID）依次排——同一来源的碟连在一起，先并入的在前（M5）。每碟取 `max(原碟号, 已占用的最大碟号 + 1)`：原碟号比已占用的都大时保留原碟号，否则接在后面。例：A{1}、B{2} → 1、2；A{1}、B{1,2}、C{1,2} 依次并入 → 1、2、3、4、5。

`layoutMergedMedia(tx, targetID)` 是唯一的排位函数，两步完成，避开不可延迟的 `media_release_position_uq`（迁移 0011:112–113）：

1. 一条 UPDATE 把 A 的全部并入碟设为 `(SELECT LEAST(MIN(position),0) FROM media WHERE release_id=A) - row_number()`，新值都小于当前最小值，不会撞。
2. 按上面的顺序与规则写正式碟号，只更新碟号真的变了的行。

扫描为来源 B 新建并入碟时用 `LEAST(MIN(position),0)-1`（每建一碟重新取，同一候选建两碟也不撞），候选写完后统一排位。合并、撤销、扫描写入 A 或任一并入 A 的发行之后都调用一次。`tracks.disc_number` 不改（仍是标签值）。

## D4 扫描的改动（只这几处）

| 位置 | 改动 |
| --- | --- |
| `catalog/ingest/identity_plan.go:123–125` `previous_release_id` | 记扫描归属 |
| `identity_plan.go:439–443` `allocateReleases` 的重叠 | 按扫描归属分组 |
| `ingest/candidate_state.go:43` 增量跳过 | `COALESCE(m.origin_release_id, m.release_id)=s.release_id`；A 与 B 的候选都必须能跳过 |
| `ingest/group_moves.go:9–26`（M7） | “是否移动”用 `COALESCE(m.origin_release_id, m.release_id) <> o.previous_release_id`；“原组是否被腾空”按扫描归属连 `releases`；接收方取 `m.release_id`（显示归属）所在的组。B 独占一组时重扫不会误判腾空 |
| `ingest/persist.go:81–87` 找碟、建碟 | 计划发行 R 是来源（在 `release_merges` 里）：按 `origin_release_id=R AND origin_position=disc` 找；没有则建在目标上（`release_id=target, origin_*=(R,disc)`，碟号按 D3 的临时值）。R 不是来源：按 `release_id=R AND position=disc AND origin_release_id IS NULL` 找；建碟前若碟号被并入碟占用，先做 D3 第 1 步 |
| `persist.go:130` 删空碟 | 按扫描归属删本候选发行的空碟（含挂在目标上的） |
| `persist.go` 收尾 | R 是来源或有并入碟时调 `layoutMergedMedia`；同事务刷新目标的 `searchindex.RefreshRelease` 与 `artistgraph.Refresh`（R 是来源时目标也算入） |

`scan_feedback_snapshot.go`（经 B 的曲目自动把 A 纳入加锁集合，`:27-39`）、`artist_projection.go` 读显示归属，不改。organizer 与 CUE 规则不动。

用户把两边文件真的搬到同一目录时（S5），按现有严格过半规则：A 多则沿用 A，B 的曲目成为 A 的自有曲目，B 的并入碟腾空删除，合并行仍在（撤销时 B 没有碟可还，见 D7）；B 多则沿用 B，A 的曲目写进 B 的并入碟；一样多则新建发行。只写成测试并在合同里说明，不另加规则。

## D5 执行器 `ReleaseMergeExecutor`（`catalog/governance/release_merge.go`）

- 输入：目标 A、来源 B、预览令牌、写入上下文（操作者、来源、批次、幂等键）。
- 锁（M1）：幂等锁 → CATP（`LockProjectionWrites`）→ A、B 发行行按 UUID 升序 `FOR UPDATE` → 受影响用户的 `personal_favorite_state` 行按用户 UUID 升序。**不锁组行**：组成员与可见性的变化都先拿 CATP，CATP 已足够；锁组行 `FOR UPDATE` 会和“收藏专辑”（先锁收藏状态、插入时外键对组行加 KEY SHARE，`personal/favorites.go:55`、`:118`）成环。撤销同一锁序。
- 校验顺序（M2、S3），失败不改任何数据：

| 顺序 | 条件 | 错误 |
| --- | --- | --- |
| 1 | A=B、ID 无效 | `400 invalid_input` |
| 2 | 任一不存在 | `404 not_found` |
| 3 | A 或 B 已作为来源在 `release_merges` 里 | `409 release_merged` |
| 4 | B 已作为目标（有发行并入 B） | `409 merge_chain` |
| 5 | 任一不可见 | `404 not_found` |
| 6 | 合起来超过发行详情的任一上限（碟 256、曲目 10000、曲目署名 10000，`catalog/read.go:19-23`） | `409 release_too_large` |
| 7 | 令牌与现在的计划不同 | `409 merge_changed` |

- 写入：插 `release_merges`；B 的碟写 `origin_*` 并挂到 A；`layoutMergedMedia(A)`；收藏（D6）；结束涉及 A 或 B 的未结束 `release_merge` 待定项（`resolved/merged`，M6）；刷新 A、B 的搜索投影与艺人投影；B 的 `edit_revision` 加一（S1），A 的不变。操作类型 `release_merge`/`release_auto_merge`，`target_id=A`，`operations_release_targets` 写 A、B。前态：B 的 `{id,title,group_id,edit_revision}`、每碟 `{medium_id, position}`、收藏逐行（D6）；后态：每碟在 A 上的碟号、B 的新版本号。
- 预览（只读，与执行共用纯函数 `buildMergePlan`）：`{target, source, media:[{medium_id, from_release, title, position_after, track_count}], hidden:{corrections, reviews, default_version, external_artwork, links, artist_overrides}, favorites_moved, token}`。令牌（S6）取 A、B 的 ID 与组 ID、“合并后 B 的组是否还有可见版本”、A 的全部碟（ID、原发行、碟号）、B 的全部碟 ID 与每碟在库曲目 ID 的 SHA-256。

## D6 发行级状态的去向

- A 的一切不动，对合并后的发行生效。B 的修正、自动层、外部封面、外部对应、核对、默认版本、组、整理工作状态与待定项原样留在 B：B 不可见，读取处自然不显示；撤销后恢复。不搬，所以撤销不用判断“仍是它写的”（调研 B5）。
- 发行作用域的艺人覆盖（S4）：艺人路由按 `m.release_id` 取发行级覆盖（`artistgraph/refresh.go:78-97`），合并后 B 的曲目用 A 的覆盖、B 的不生效，撤销后恢复。预览的 `hidden.artist_overrides` 列出。本地封面：A 没有本地封面时不用 B 的（已知限制）。
- 待认领：`releaseClaimablePredicate`（`governance/claims.go:28`）排除作为来源在 `release_merges` 里的发行；列表、执行器（`claim.go:289`，返回 `409 not_claimable`）与线索（`claim_hints_batch.go:44`）共用这一谓词。
- 默认版本指向 B 的组：按现有规则静默失效（`default-version-contracts.md:27`），撤销后恢复。
- 收藏（M3）：合并后 B 的组若不再有可见发行，按 `personal.MoveAlbumFavorites` 的语义把该组的专辑收藏转到 A 的组；前态逐行记 `{user_id, group_id, created_at, saved_title, saved_artist, target_existed}`。撤销时按原值插回 B 组；A 组那条只在 `target_existed=false` 且 `created_at`、`saved_*` 仍与复制值相同时删除；每个受影响用户的 `personal_favorite_state.revision` 只加一次。
- 整理：B 不可见，后台不处理它的待定项；A 的曲目集合变了，摘要变化后程序层照常重新处理 A。

## D7 撤销

- 共同条件：`release_merges` 里 B 的行仍存在且 `operation_id` 等于原操作；B 的 `edit_revision` 仍是合并后的值（合并期间撤销认领等能改 B 的组，S1），否则 `409 revert_conflict`。
- 自动合并（`release_auto_merge`）另加（M4，ADR 0006:141–142）：A 或 B 在这条合并之后（按 `(created_at,id)`，经 `operations_release_targets`）还有人工、未撤销的 `release_group_change`、`release_merge`、`release_claim`，返回 `revert_conflict`，按批、按来源撤销时列为 skipped。人工 `release_merge` 不加这条：合并之后对 A 的修正、曲目修正、归组、核对不阻碍撤销（链被 D5 禁止，A 上的结构操作不影响 B 的碟）。合同写明两者不同。
- 写入：B 的碟（含合并期间 B 新增的碟）还原 `release_id=B, position=origin_position`，清 `origin_*`；B 没有自有碟（合并期间不可能产生），有则报错（S2）；删 `release_merges` 行；`layoutMergedMedia(A)`；收藏按 D6 还原；B 的 `edit_revision` 加一；刷新投影；`operations_release_targets` 写 A、B。B 的碟已全部腾空（文件搬进 A 的目录，D4 末段）时照样撤销：删合并行，B 保持不可见。
- 不再提出：任何来源撤销任何合并，同一事务写 `curation_suppressions`：`target_type=release`、`target_id` 为较小 ID、`subject=release_merge`、值为另一方 `{release_id,title}`、哈希取另一方 ID 的 SHA-256。只挡自动提出，人工仍可再合并。标记写入照归组：`curation` 实现 `SuppressReleaseMerge` 并注入给 `governance`（`curation/join_group.go:96-98`、`governance/revert.go:28-37`）。
- 修改记录经 `operations_release_targets` 在 B 上也显示合并与撤销。

## D8 自动并入（强证据，模型提出）

- 发现：在程序匹配与模型匹配共用的 `writeMatch`（`curation/program/enrich.go:244`，模型匹配经 `model_matcher.go:250`）写入发行对应之后，以及存量回填（照 G 的结果键，`result.release_merge`）中，反查 `ObjectsLinkedTo("musicbrainz","release",X)`。另一方可见、未并入、满足下表前两条时，按确定的规则选目标（M6）：两边在库曲目的最小（`disc_number`、`position`）较小的一方；平局取 `created_at` 较早，再比 ID。待定项只建在目标上，`kind=release_merge`，冲突代码 `same_external_release`，候选固定为另一方；`input_digest` 取两边 `ReleaseDigest` 与两条 `external_link` 操作 ID 的 SHA-256。同一对只会建一条（目标唯一，沿用每发行每种至多一条未结束的约束）。三个及以上本地发行对上同一个 X 时，以选出的目标为中心，逐个对其余发行各建一条（不同来源、同一目标，在目标上排队，前一条结束后再建下一条）。
- 后台工具 `merge_releases`：输入 `{item_id, basis}`，目标与来源取自待定项；执行前核对摘要、候选仍可见且未并入。对话与 MCP：先 `preview_release_merge {target_release_id, source_release_id}` 返回预览与令牌，再 `merge_releases {target_release_id, source_release_id, token}`，走人工执行器并需要确认（照认领，`governance/stewardtools/tools.go:68`、`:75`，S7）。
- 后端核对强证据（后台与例外采纳）：

| 条件 | 结果 |
| --- | --- |
| 两边各有一条生效的 MusicBrainz 发行对应、指向同一个 X，各有一条未撤销的 `external_link` 操作后态佐证（照 G） | 必需，否则 `no_strong_evidence`（转人工） |
| 两边各至少一首在库曲目有录音对应，且录音集合不相交 | 必需，否则 `no_strong_evidence`（例如同一专辑的两种格式） |
| 这一对有 `release_merge` 标记 | `nothing_to_apply` |
| 两边分属不同的组，且其中一方最近一次归组操作是人做的 | `manual_structure` |
| D5 的结构条件 | D5 的错误码 |

- 审查策略第 4 版（S8）：证据只用库里已有的数据——两边的发行摘要、碟数与曲目数、MusicBrainz 对应与佐证操作、录音是否相交、外部发行的碟数与曲目数（取自对应操作的后态，没有时写“不可用”，不读外部源缓存）。规则补一条“强证据类结构操作（归组、发行合并）证据里没有生效的外部对应时，后端直接转人工”。`ReviewEvidenceFields` 补本地候选字段，与 `steward-review-contracts.md` 逐字同步。旧版批准的方案沿用执行守卫（拒绝）。
- 写入：来源 `curation`、`release_auto_merge`、模型批次；待定项 `resolved/model_merged`。审查没通过、转人工进入例外；调度器的例外匹配加 `merge_releases`（按 `item_id`，`curation/model/scheduler.go:222`）；采纳时管理员为操作者重新算令牌执行，前态变化 `exception_stale`；不采纳写 `release_merge` 标记。
- 白名单加 `merge_releases`（后台工具数加一）；对话与 MCP 的治理工具数加二（`preview_release_merge`、`merge_releases`），C6、MCP 的断言随之改。

## D9 接口

- `GET /api/v1/releases/{id}/merge-preview?source=<id>`、`POST /api/v1/releases/{id}/merge` `{source_release_id, token}` → `{operation, release}`。管理员、写接口同源检查，错误码见 D5。
- 发行详情：管理员加 `merged_releases: [{id, title, operation_id, revertible}]`；无合并时为空数组，非管理员不返回。现有样本里管理员发行详情若因此多出空字段，同步更新样本与前端解码器（A8 的“逐字不变”针对非管理员与其他接口）。
- 操作列表、修改记录、撤销对话框：两种新操作类型的 `changes` 含 `source {id,title}` 与碟数。

## D10 前端

- 专辑页“更多 → 管理”加“并入另一个发行…”：对话框先列同一专辑的其他版本（最常见情形），再提供专辑搜索（复用 `join-album-dialog` 的搜索）后选版本；选定后显示预览（并入后的碟序、合并期间隐藏的内容、收藏转移数），确认提交。`merge_changed` 时刷新预览。
- “管理”面板列出“已并入：《B》（撤销）”；撤销走现有撤销对话框，文案“撤销后《B》恢复为单独的发行”。
- 操作记录、修改记录、整理记录、例外列表认识两种新操作类型与 `merge_releases` 例外；不再提出列表显示 `release_merge` 标记并链到两个发行。

## D11 规范与文档

新增 `.trellis/spec/backend/release-merge-contracts.md`（数据、碟序、扫描改动、执行器、撤销、错误码、已知限制、必需测试）；`library-scan-pipeline.md`、`library-scan-incremental.md` 写扫描归属；`claim-contracts.md` 写排除；`curation-contracts.md` §20 写 D8；`steward-review-contracts.md` 写第 4 版；ADR 0006 加“H 说明”；路线图第 12 行完成。

## 已知限制

- B 被整张替换（新曲目 UUID）：新文件成为新发行单独出现，旧 B 留在合并里但没有曲目。与认领同类。
- 合并之后的曲目级操作记录目标是 A；撤销合并后按 A 筛选仍能看到它们（与 I 的已知限制同类）。
- `/releases/<B>` 返回 404，不跳转。
- 不允许链；三碟合一时都并到同一个目标。
- 合并后扫描再把 A 推过详情上限时，详情报错，需先撤销（与超大发行的现有行为相同）。
- 合并期间 B 的“缺失曲目”核对不生效，B 缺失的曲目作为 A 的问题出现；B 的问题核对与待定项若出现在例外里，采纳会失败（B 不可见）。
- A 没有本地封面时不使用 B 的本地封面。

## 规划审查（路线 b）

审查者：Plan 子代理（只读，读了规划、调研与相关代码、迁移、规范）。结论：D1 可行，改后可以实施，不需要大改。

| 条目 | 处理 |
| --- | --- |
| M1 合并锁组行与收藏专辑成环 | 采纳：不锁组行，锁序改为幂等 → CATP → 发行行 → 收藏状态行（D5），测试加交错 |
| M2 `release_merged` 被 404 遮住 | 采纳：校验顺序（D5） |
| M3 收藏前态不足 | 采纳：逐行前态、按原值恢复、只删合并带来的（D6） |
| M4 自动撤销须被之后的人工结构操作拦住；按 B 查不到操作 | 采纳：`operations_release_targets`，自动与人工撤销规则分开（D2、D7） |
| M5 临时碟号撞车、多来源交错 | 采纳：两步排位与新排序键（D3） |
| M6 待定项摘要、重复、目标选择、收尾、调度器匹配 | 采纳：确定选目标、只建一条、摘要、合并时结束待定项、调度器加工具（D5、D8） |
| M7 组腾空的 SQL | 采纳（D4） |
| M8 派发范围 | 采纳（implement.md） |
| M9 测试命名与缺口 | 采纳（implement.md） |
| S1 合并期间 B 可被撤销认领改动 | 采纳：B 版本号加一并在撤销时核对 |
| S2 数据库强制关系 | 采纳：组合外键、RESTRICT |
| S3 详情三个上限 | 采纳 |
| S4 艺人覆盖换主 | 采纳：写明、预览列出、测试 |
| S5 文件搬到一起 | 采纳为测试与合同说明，不另加规则 |
| S6 令牌覆盖面 | 采纳 |
| S7 对话与 MCP 预览工具 | 采纳 |
| S8 审查证据不读外部缓存 | 采纳 |
| S9 down 步骤 | 采纳；收藏回退不还原，写明 |
| S10 已知限制 | 采纳 |
