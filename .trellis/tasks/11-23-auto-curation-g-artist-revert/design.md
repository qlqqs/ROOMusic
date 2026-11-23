# 自动整理 G：技术设计

初稿由规划子代理（Plan，2026-11-23）对照代码写出并审阅 `prd.md` 的 G1–G8；主代理全部采纳（M1 选迁移方案）。路径省略 `backend/internal/`。

## 1. 对决定的修订

### 必须改

| # | 问题（file:line） | 改法 |
| --- | --- | --- |
| M1 | G3② 的“之后”不能比 `operations.created_at`：默认 `NOW()` 是事务开始时间（`0017_governed_operations.sql:22`），`Run` 先 BeginTx 再取锁，两条并发艺人操作的提交顺序与 created_at 可能相反 | 迁移 0066：`ALTER TABLE operations ALTER COLUMN created_at SET DEFAULT clock_timestamp()`（记录在执行器之后插入、仍持 CATP，插入顺序即提交顺序）；down 改回 `now()`；判定用 `(created_at,id) > (原记录)` |
| M2 | 四个艺人 Kind 是空结构体（`artist_describe.go:159-205`、`app/app.go:226`），撤销要刷新收藏解析（`ArtistGovernance.resolution`）、撤销自动合并要写标记 | `governance.ArtistOperations(g *ArtistGovernance, s ArtistSuppressionWriter) []operations.Kind`；app 只建一个 `ArtistGovernance`，同时注入 `curationWriter.WithArtists(g)` |
| M3 | 标记与“分开决定”只看两个 ID 会失效（A、B 自动合并撤销后人工把 B 并入 C，下一轮会合并 A 和 C） | 按身份闭包判定：`closure(P) = {P} ∪ {redirect_artist_id = P 的身份}`；标记一行（`target_type='artist'`、`target_id` 取较小 ID、`subject='artist_merge'`、`value={artist_id,name}`），查时两个方向都查 |
| M4 | 忽略存的是名字键对（`0026:103-107`），没有“艺人对” | “分开决定”= (a) 两闭包之间有标记；(b) 有 `artist_split` 同时触及两闭包（经 `operations_artist_targets`）；(c) 有撤销 `artist_merge` 的记录同时触及两闭包（不论来源）；(d) 有忽略行两端名字键按全局路由解析后分属 P 与 Q |
| M5 | 没排除 MusicBrainz 特殊用途艺人（Various Artists 会把不相干的本地艺人合并） | 常量表排除，至少 Various Artists `89ad4ac3-39f7-470e-963a-56509c546377` 与 [unknown] `125ec42a-7229-4250-afc5-e057484327fe`；`closure(P) ∪ closure(Q)` 的对应指向两个以上不同 MBID 时不合并 |
| M6 | `artist_effective_credits` 只存生效层（修正 > auto > 扫描，`artistgraph/refresh.go:79-88`），auto 层会让自己证明自己 | catalog 新增读取：有修正用修正层、否则扫描层（跳过 auto），按 `refresh.go:93-97` 的规则自行解析到 canonical |
| M7 | 标签对位：MBID 在同种类内去重保序（`library-scan-tags.md:100,109`），本地署名拆分再去重（`facts.go:31-45`、`metadata/artist.go:115`），序号不一定对得上 | 只有本地、外部、标签三者数量相等时按位比较；标签不能单独成立，必须与外部 credit 的 ArtistID 相同 |
| M8 | 前端解码器：`governance/api/decoders.ts:309` external_link `object_type` 只认三种；`curation/api/decoders.ts:138` 标记 `target_type` 只认三种 | 两处加 `"artist"` 与文案 |
| M9 | 只改 `Revertible` 不够：`SupportsReviewEvidence` 没登记艺人工具（`catalog/stewardtools/review.go:41-48`），会按“没有证据读取方式”转人工（`app/steward.go:275`） | 登记三个工具；`ReadReviewTargets` 读艺人目标；`reviewExternal` 加艺人对应；同步 `ReviewEvidenceFields` 与规范 |
| M10 | `notRevertibleReason` 的艺人分支在 `reverted_by` 之前（`change-summary.ts:243-251`），撤销后仍写“需要时可以拆开” | 艺人分支挪到 `reverted_by` 之后，改为“之后又有修改” |

### 应改（采纳）

- S1 艺人撤销不要求目标可见；目录未就绪 `409 artist_catalog_not_ready`；记录状态损坏 `409 revert_conflict`、Describe false。
- S2 撤销记录 before 用撤销前当前快照、after 用恢复后重读的快照，两边都带原 `changes`。
- S3 撤销合并后 `artist_overrides` 四列全空的行可以删。
- S4 `MergeArtists` 不走 `checkWrite`，直接 `checkExternalWriter`；`Source.Kind=model` 一律拒绝。
- S5 只让程序（source）写艺人对应；模型解决的匹配由回填步骤以 source 身份补写。
- S6 判定②也会挡住无关的后续合并（D→C 挡住 B→C 的撤销），倒序撤即可，原因写“之后又有修改”。
- S7 给 `ReviewPolicyRules`、`ReviewEvidenceFields` 加与规范逐字比对的测试。

### 已确认

前态够用（绑定的 `operation_id` 为 null 表示原来没有行，`artist_state.go:152-156`；作用域有原归属与原操作；扁平化旧身份及其 overrides 都在）；扫描不用处理（扫描不写意图表，旧键上的新署名随恢复的绑定回到 source）；CATP 全局可重入、各步各自一个 `Run`，不会与发行锁成环。

## 2. 撤销算法（`governance/artist_revert.go`）

共同：`RevertExecutor` 已锁原记录 → `lockArtistCatalog`（CATP）→ 解码 schema_version 1，失败 `revert_conflict` → `lockArtists`（UUID 序锁全部涉及身份）→ `artistRevertable(tx, record)`，不过 `409 revert_conflict`（“这位艺人之后又有修改，不能撤销这一条”）→ 写回 → 递增版本 → 刷新 → 标记（如需）→ Outcome。

- 合并（old = after.Artists 去掉 target）：先算 `releases = affectedArtistReleases([target]∪old)`；绑定：before 的 `operation_id` 为 null 的删 `key_id=k AND operation_id=O`，否则改回原 `(artist_id, operation_id)`；作用域按 `(release_id, track_id, key_id)` + `operation_id=O` 定位改回；重定向把每个 old 的 `redirect_artist_id/redirect_operation_id` 改回 before 值，显示名不动；递增 target 与 source；刷新 `g.refresh(releases, [target]∪old, mapping{old → before.redirect ?? old})`；原记录 `origin.source=='curation'` 时 `SuppressArtistMerge(target, source, revertID, actor)`；Outcome 目标 artist target、`Targets=old`。
- 改名：先算 releases；`display_name, name_operation_id` 写回 before；递增、刷新（无 mapping）。
- 忽略：只处理 `before.op ≠ after.op` 的键对：after 为 O → 删；after 为 null → 用 before 原 `operation_id` 重插。递增两位，不刷新。
- 拆分：保持 `not_revertible`。
- Describe：批量调用同一判定填 `Revertible`；撤销记录与损坏记录 false。

## 3. 拦截判定

```sql
-- ① 合并写下的每一项仍是它写的
NOT EXISTS (bindings: after 每项 LEFT JOIN artist_name_bindings，缺行或 artist_id/operation_id 不符)
AND NOT EXISTS (assignments: after 每项 LEFT JOIN artist_assignment_overrides，缺行或不符)
AND NOT EXISTS (old 每个 LEFT JOIN artist_overrides，redirect_artist_id 或 redirect_operation_id 不符)
AND NOT EXISTS (SELECT 1 FROM artist_overrides WHERE artist_id=$target AND redirect_artist_id IS NOT NULL)
-- 改名：display_name IS NOT DISTINCT FROM after 值 AND name_operation_id=$op
-- 忽略：changed 键对 after=op → 行存在且 op=$op；after=null → 行不存在
-- ② 之后没有别的生效中的操作
AND NOT EXISTS (SELECT 1 FROM operations_artist_targets mine
  JOIN operations_artist_targets t ON t.artist_id=mine.artist_id AND t.operation_id<>mine.operation_id
  JOIN operations l ON l.id=t.operation_id
  WHERE mine.operation_id=$op AND l.operation_type<>'revert' AND (l.created_at,l.id)>($created,$op)
    AND NOT EXISTS (SELECT 1 FROM operations r WHERE r.reverts_operation_id=l.id))
```

撤销记录也写进 `operations_artist_targets`，② 排除撤销记录，所以链式合并可以倒序撤销。

## 4. 艺人对应（G5）

- 读取：`catalog.LoadCurationArtistCredits(ctx, q, releaseID)`（目录未就绪 `ErrArtistCatalogPreparing`）：发行的 `release_artist`/`album_artist`（有修正用修正层，否则扫描层）、每首曲目的 `track_artist`（同上），每项 `{position, name, artist_id(canonical)}`。
- 外部：发行用 `Release.Artists`；曲目经 `m.Pairs` 找外部 Track 的 `Artists`（同一录音多次就跳过）；单曲用 `Recording.Artists`。标签：发行用所有曲目一致的 `mb_album_artist`，曲目用自己的 `mb_artist`。
- 对位（纯函数 `alignCredits`）：本地与外部数量相等且 ≥1；外部每项有 ArtistID 且互不相同；本地 canonical 互不相同；不含特殊用途艺人；每位 `textnorm.Text(本地)==textnorm.Text(外部)`，或标签数量与外部相等且 `tag[i]==ext[i].ArtistID`；一位不满足整组不写；一位本地艺人在本发行对上两个不同 MBID 时丢弃。
- 写入：`writeMatch` 的 `links` 之后、`fields` 之前加 `artist_links` 步（只在 `Source.Kind=="source"`），单独一次 `ApplyLinks`（`ObjectType:"artist"`、method `artist_credit`、confidence `certain`）；`external_link.go:77` 放开 `artist`；`validateLinkMembers` 对 artist 在事务内重读，要求对象是未重定向的 canonical 且出现在本发行的非 auto 署名里。unchanged/occupied/suppressed 同现有；全跳过 `nothing_to_apply`。
- 回填：仿 `needsTrackFields`/`backfillTrackFields`：result 加 `artist_links` 标记；matched 且没有标记时按已有对应取外部发行写入；不升规则版本。
- 迁移：G5 不需要（`0060:11` 已允许 artist）；只有 M1 的 0066。

## 5. 自动合并（G6）

- 时机：`artist_links` 之后（新匹配与回填两条路径），对本发行涉及的每个 MBID X：`ObjectsLinkedTo(artist, X)` 取对应它的本地艺人，解开一跳重定向得 canonical 集合，少于 2 个不做；对其余每一位 `u.write("artist_merge/"+X+"/"+min+"/"+max, …)`，进程序批次。
- `Writer.MergeArtists(ctx, ArtistAutoMerge{A, B, ExternalID, Source, BatchID, Key})`：`sourceSpec`，类型 `TypeArtistMerge`，指纹 `{type, pair 排序, external_id, provider, rule:"same_mb_artist/1"}`（不含版本）；执行器 `g.CurationMergeExecutor(a, b, check)`：把 `MergeExecutor` 检查之后的部分抽成 `mergeLocked`；CATP 与艺人行锁之后要求两位都未重定向，按参与数降序、ID 升序选 target，调 `check` 再 `mergeLocked`。`check`：`checkExternalWriter`；强证据（`external_links ... FOR SHARE`，对应行由一条未撤销的 external_link 记录写下且 after_state 有这项，同 `checkGroupEvidence`）；闭包内只有一个 MBID；标记命中 `nothing_to_apply`；“分开决定”命中 `manual_structure`。
- 锁序：OPER 幂等锁 → CATP → 艺人行（UUID 序）→ external_links 共享锁 → curation 设置与批次 → 派生数据 → 收藏 state；不能在别的 `Run` 事务里调用。
- 程序侧：`nothing_to_apply`、`manual_structure`、`no_strong_evidence`、`artist_redirected`、`revision_conflict`、`artist_catalog_not_ready`、`artist_scope_too_large` 只跳过这一对。
- 撤销写标记：`ArtistMergeKind.Revert` 发现原记录来源 curation 时同一事务写标记。

## 6. 管家工具与审查策略第 3 版（G7）

- `governance/stewardtools/tools.go:69-72`：merge、rename、ignore 改 `true`，描述“可以从操作记录撤销”；split 不变。
- `catalog/stewardtools/review.go`：`SupportsReviewEvidence` 加三个工具；`ReadReviewTargets` 解析 `artist_id`、`source_artist_id`、`other_artist_id`，经 `Store.LoadArtist` 产出 `{type:"artist", id, revision, source:"catalog_read_summary", data:{name, redirected, release_count, track_count}}`（名字标 `catalog_effective_untrusted`）。
- `app/steward.go`：`reviewExternal` 加 `artists:[{artist_id, links:[…]}]`。
- `steward/runtime/review.go`：`ReviewPolicyVersion = 3`；规则首行“策略版本：3。”；那一句改为“后端工具登记为不可撤销的项转人工确认，不送审；艺人拆分以及撤销操作属于此类。”；`ReviewEvidenceFields` 加艺人 data 与 external.artists；`steward-review-contracts.md` 逐字同步。
- 测试：`steward/httpapi/review_c7_integration_test.go:299`、`app/steward_review_c7_integration_test.go:149-150` 改用 `runtime.ReviewPolicyVersion`。后台白名单不变。

## 7. 前端（G8）

- `change-summary.ts`：撤销记录标签（撤销合并艺人、撤销修改主名、撤销忽略/恢复提示）；`linesOf` 反向文案；`notRevertibleReason` 按 M10；拆分仍“需要时可以再合并”；external_link 摘要“N 位艺人”。
- `revert-operation-dialog.tsx`：`item.artist` 不为空时 `subject:"artist"` 的撤销意图；标题表加三种；正文“‘{艺人名}’会改回”。
- `use-release-commands.ts`：artist 撤销不跳转；`operations-page.tsx` 刷新合并艺人数据的刷新。
- `merge-artist-dialog.tsx:10`、`rename-artist-dialog.tsx:16,81`、`artist-admin-actions.tsx:25`、`use-artist-commands.ts:70`：文案改“可以从操作记录撤销”。
- 解码器加 `artist`（M8）；`curation-text.ts` target 加“艺人”、subject 加 `artist_merge`（“自动合并艺人”），值显示 `value.name`；`suppressions-section.tsx` 艺人链接 `/artists/{id}`。
- 测试：`artist-operations-step5.test.ts`、`artist-governance-step5.test.tsx`、`e2e/artists-governance.spec.ts`、`test/governance-data.ts`、`api-contract.test.ts`。

## 8. 测试与变异

| 阶段 | 用例 |
| --- | --- |
| 撤销（R1、R2） | `TestPostgreSQLArtistMergeRevertRestoresIntent`（有/无原行绑定、两种作用域、重定向、有效署名、主名与搜索名、收藏 `resolved_artist_id`、merged-sources）、`…MergeRevertChainReverseOrder`、`…MergeRevertBlockedByLaterDecision`（改名、忽略、拆分、D→C）、`…MergeRevertAfterRescan`、`…RenameRevertChain`、`…CandidateIgnoreRevertMixedPairs`、`…ArtistRevertDescribeMatchesExecutor`、`…ArtistRevertConcurrent`、`…ArtistRevertCatalogNotReady`、`TestArtistSplitNeverReverts`、0066 往返 |
| 对应（R3） | `TestAlignCredits*`（数量不等、名字不同标签相同、只有标签、auto 层、修正层、VA）、`TestPostgreSQLCurationArtistLinks`、`…ArtistLinksBackfill`、`…ExternalLinkArtistRevertSuppresses` |
| 合并（R4） | `TestPostgreSQLCurationAutoMergeSameMusicBrainzArtist`（来源 curation、批次、方向）、`…AutoMergeBlocked`（闭包标记、拆分、忽略行、人工撤销、多个 MBID、重定向）、`…AutoMergeRevertSuppresses`、`…AutoMergeReplay` |
| 管家（R5） | `TestPostgreSQLStewardC6ArtistTools` 更新、`TestPostgreSQLStewardReviewArtistTools`、`TestReviewPolicyRulesMatchSpec`、对话合并后能撤销 |
| 界面（R6） | vitest 与 e2e |

变异：不恢复扁平化旧身份的 `redirect_operation_id`；绑定一律删除；去掉判定②；改用版本号相等判定；Describe 恒 true；撤销不写标记；标记不按闭包；对位允许 auto 层；不传收藏映射；0066 不改默认值（两事务受控锁顺序的时间倒置模拟）。

## 9. 风险与回退

- 对话行为改变：Assistant 模式可把三个工具加入免确认名单；旧的待定方案项存的是 `revertible=false`，仍走人工；版本 2 的审查失效后重审。
- 外部对应被撤销时已发生的自动合并保留（与归组一致），需要时单独撤销。
- 大艺人合并的撤销刷新范围大，受 10 万轨上限（`artist_scope_too_large`）。
- 0066 改全部操作记录的时间语义为插入时刻；历史不变，排序仍单调；down 改回 `now()`。
- 回退：旧前端遇到 `target_type='artist'` 标记与艺人对应会解码失败，前后端一起回退；已发生的自动合并只能用拆分恢复。
- 同名不同人：靠位置、名字、标签一致，加“闭包内多个 MBID 不合并”与“人工分开决定优先”。
