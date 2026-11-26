# 整张发行并入

管理员可以把扫描拆开的两个可见发行重新合为多碟发行。本文用 A 表示接收发行，B 表示并入来源。曲目保留 ID 和 medium_id，碟实际挂到 A。播放、搜索、艺人、收藏曲目和歌单按现有显示归属找到 A。

## 数据与碟序

迁移 0067 的 `release_merges` 以 B 为主键，记录 A、操作 ID 和插入时刻。来源与目标必须不同，引用的发行受 RESTRICT 保护。并入碟的 `media.origin_release_id` 和 `origin_position` 成对设置，组合外键确保其显示发行与合并行一致，每一来源的原碟号唯一。自有碟两列都为 NULL。合并与撤销各自通过 `operations_release_targets(operation_id,release_id)` 关联 A、B。

自有碟保留扫描碟号。并入碟按合并时刻、来源 UUID、原碟号、碟 UUID 升序，每碟取原碟号与当前最大碟号加一中的较大值。例：A 有第 1 碟、B 只有第 3 碟，结果是 1、3。A 有第 1 碟，B、C 各有第 1、2 碟，B 先并入，结果是 1、2、3、4、5。曲目的标签碟号保持原值。

`ingest.LayoutMergedMedia` 先用一条 UPDATE 将并入碟移至当前最小碟号与零的较小值减去各行序号，再写正式位置，满足立即检查的唯一索引。来源候选新建的每张碟都重新取该目标的最小值减一。目标新建自有碟遇并入碟占位时先腾位，随后统一排位。

down 拒绝仍有自有碟的来源，将现存并入碟还原到来源及原碟号，再清除对应操作、撤销、方案引用、例外、待定项与标记。收藏保留降级时的状态。

## 扫描归属

扫描用 `COALESCE(m.origin_release_id,m.release_id)` 计算候选重叠及之前的发行，决定增量跳过和是否腾空旧组。若此处只读取显示归属，B 的候选会误认 A。接收收藏的组仍取实际移入曲目的显示发行。B 候选照常写 B 的扫描值，按 B 与原碟号找碟，显示在 A；A 候选只复用自己的碟。候选保存清理腾空的碟，合并目标重排，搜索和艺人投影在同一事务刷新。[扫描流程](./library-scan-pipeline.md)与[增量扫描](./library-scan-incremental.md)

双方的文件实际搬进同一目录时，沿用严格过半规则。A 占多数时，原 B 的曲目可写入 A 的自有碟；B 占多数时写入 B 的并入碟。平分时建立新发行。撤销还原当时仍属 B 的碟。

## 接口、预览与事务

- 管理员用 `GET /api/v1/releases/{A}/merge-preview?source={B}` 读取 `target`、`source`、`media`、`hidden`、`favorites_moved`、`token`。碟项给出 ID、原发行、标题、合并后位置和曲目数。隐藏项涵盖修正、核对、默认版本、外部封面、对应和发行作用域的艺人覆盖。
- `POST /api/v1/releases/{A}/merge` 要求同源、管理员与 UUID 幂等键。请求为 `{source_release_id,token}`，成功返回 `{operation,release}`。管理员发行详情的 `merged_releases` 为数组，每项含 `id,title,operation_id,revertible`。普通成员响应省略该字段。
- 对话和 MCP 使用 `preview_release_merge(target_release_id,source_release_id)` 与 `merge_releases(target_release_id,source_release_id,token)`，沿用认领工具的确认与授权。
- 预览和执行共用计划。令牌使用 SHA-256，绑定 A、B 的身份与组、B 组有无其他可见发行、双方碟身份与碟序以及在库曲目成员。执行取得幂等锁、CATP、按 UUID 升序的发行行锁，最后按用户 UUID 升序锁受影响的收藏状态行。组成员变化由 CATP 串行化，再取组行锁会与专辑收藏的外键检查形成环。
- 合并移动 B 的全部碟，B 的 edit_revision 加一，A 的版本号保持原值。两方搜索与艺人投影随事务刷新，涉及 A 或 B 的开放合并待定项以 resolved/merged 结束。操作保存 B 的身份、标题、组与版本，碟的原位置与新位置，以及逐行收藏前态。

### 错误优先级

| 顺序 | 条件 | HTTP 与错误码 |
| --- | --- | --- |
| 1 | UUID 无效、A=B | 400 invalid_input |
| 2 | 任一发行不存在 | 404 not_found |
| 3 | 任一方已是并入来源 | 409 release_merged |
| 4 | B 已有其他来源并入 | 409 merge_chain |
| 5 | 任一方不可见 | 404 not_found |
| 6 | 合并后超过 256 碟、10000 曲目或 10000 条曲目署名 | 409 release_too_large |
| 7 | 写入携带的令牌与当前计划不同 | 409 merge_changed |

## 发行级状态与撤销

A 的发行级状态对合并后的曲目生效。B 的修正、自动层、封面、外部对应、核对、默认版本、艺人覆盖、组与整理工作状态保留在 B。B 不可见时默认版本指向按既有规则失效，待认领谓词排除 B。

B 组失去最后一个可见发行时转移其专辑收藏。每行前态记录 user_id、group_id、created_at、saved_title、saved_artist、target_existed。撤销按原值恢复来源收藏，只有目标收藏由本次合并创建且时间与快照仍匹配时才删除。每个受影响用户的收藏版本在一次操作中只递增一次。

两种操作均核对 B 的合并关系仍由原操作写入，且 B 的版本等于合并后版本，否则返回 409 revert_conflict。人工合并允许 A 之后发生归组、核对、字段或曲目修正。自动合并另外按操作的 (created_at,id) 检查双方后来的、仍生效的人工归组、合并或认领；存在时单项撤销冲突，批量撤销计为 skipped。

撤销把所有仍携带来源 B 的碟还给 B，包括重扫新增的碟，清除来源列与合并行，重排 A、还原收藏、递增 B 的版本并刷新投影。B 已无碟时仍可撤销。来源有自有碟属于状态冲突。两种撤销都调用注入的 SuppressReleaseMerge，在同一事务写入 target_type=release、subject=release_merge 的标记。目标取较小 UUID，值保存另一方的 release_id、title，哈希为该 UUID 文本的 SHA-256。缺少写入器或写入失败则整个撤销回滚。标记只影响自动再次提出，人工可重新并入。

## 已知限制

B 的内容被整张替换后，新曲目可形成单独发行。访问已并入的 B 返回 404。多方合并必须共用同一目标。扫描在合并后继续增碟、增轨仍可能超过详情上限，此时需先撤销。合并期间的曲目操作以当时的 A 为记录目标；撤销合并后仍可在 A 上查询。B 的缺失曲目在 A 的问题中显示，B 的问题核对与现有待定项可能在例外采纳时因不可见而失效。A 缺本地封面时不会继承 B 的本地封面。

## 必需测试

| 断言点 | 用例名 |
| --- | --- |
| 合并前后 A 的标题与曲目身份，B 的详情、列表、搜索、艺人、认领与曲目位置 | `TestPostgreSQLReleaseMergeManual`、`TestPostgreSQLReleaseMergeDetailAPI` |
| 双方全量、增量、来源增删曲目、一次新增两碟、同根目录改名均保留身份 | `TestPostgreSQLReleaseMergeRescanKeeps` |
| 目标增碟与并入碟号冲突，两多碟来源的先后顺序与原碟号跳号 | `TestPostgreSQLReleaseMergeTargetGainsDisc`、`TestPostgreSQLLayoutMergedMedia` |
| 按扫描归属判旧组腾空，实际接收收藏的组按显示归属 | `TestPostgreSQLReleaseMergeGroupMoves` |
| 撤销还原碟、精确收藏快照、修正、核对、默认版本与发行艺人覆盖 | `TestPostgreSQLReleaseMergeRevert`、`TestPostgreSQLReleaseMergeRevertFavorites`、`TestPostgreSQLReleaseMergeArtistOverrides` |
| 来源文件搬进目标目录，空来源仍能撤销；整张换内容则保留新发行 | `TestPostgreSQLReleaseMergeFilesMovedTogether`、`TestPostgreSQLReleaseMergeFilesMovedTogetherOtherMajorities`、`TestPostgreSQLReleaseMergeSourceReplaced` |
| 按顺序命中七项校验、三个上限，失败无副作用；幂等重放 | `TestPostgreSQLReleaseMergeGuards`、`TestPostgreSQLReleaseMergeGuardsAfterScan`、`TestPostgreSQLReleaseMergeManual` |
| B 的版本变化，自动合并之后的有效人工结构操作与已撤销操作，两种撤销规则与标记失败回滚 | `TestPostgreSQLReleaseMergeRevertGuards`、`TestPostgreSQLReleaseMergeRescanKeeps` |
| B 的修改记录可见合并与撤销，接口样本与工具数 | `TestPostgreSQLReleaseMergeRevert`、`TestPostgreSQLAPIResponseSamples`、`TestC6RealRegistryToolCounts`、`TestPostgreSQLMCPRealRegistryProtocolsAndIsolation` |
| 并发扫描、认领、归组和收藏下按共同锁序写入 | `TestPostgreSQLReleaseMergeConcurrent`、`TestPostgreSQLReleaseMergeConcurrentFavorites` |
| 0067 带并入碟、操作和收藏回退再升级，碟号复原 | `TestPostgreSQLMigrationRollbackRoundTrip/0067_release_merges.sql` |

## 程序识别规则 105

程序取得同版的两个可见发行后，在活动对应、抑制及人工结构检查通过时，只有合并预览覆盖该外部版全部碟、各轨位置、各录音且录音无重复时才能直接并入。执行器锁内重验相同的预览。重复录音保留两份，按同一外部组已有的归组治理路径处理。其余符合并入候选条件的仅在配置模型时建项。撤销程序并入沿用 release_auto_merge。
