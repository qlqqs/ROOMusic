# 整理合同重写实施记录

## 步骤与核对

1. 阅读 ADR 0009、父任务 PRD、子任务 2–10 的设计和现有后端实现，按扫描、查找、写入、错误与限流、模型、艺人、接口顺序重排合同。
2. 保留当前代码使用的表、字段、状态、接口和错误码，删除历史章节及归档任务链接。
3. 将 ADR 0009 第 10 节子任务 1–11 的状态更新为“已完成”。
4. 用 `rg` 检查合同没有“以本节为准”或 `tasks/archive`，检查失效的合同锚点，并运行 `git diff --check`。
5. 从合同抽查接口路径、状态值和错误码，在后端路由、状态写入和错误构造处核对。

## 代码核对出入

- `.trellis/spec/backend/curation-contracts.md` 原合同把 `MatchRuleVersion` 写成 105，代码 `backend/internal/curation/program/worker.go:17` 为 106，按代码改为 106。
- 原合同把识别状态写成 `unmatched`、`conflict`、`query_error` 的简化集合，代码 `backend/internal/curation/program/release_state.go:156` 还读取 `uncertain`、`no_match`、`unlisted`、`no_usable_candidates`，合同保留这些兼容读取值，并把新结论写成代码使用的 `matched`、`unmatched`、`conflict`、`try_later`、`query_error`。
- 代码 `backend/internal/curation/httpapi/handlers.go:78` 暴露 `POST /api/v1/admin/curation/model/retry`，旧合同遗漏，已补入接口表。
- 代码 `backend/internal/curation/httpapi/artist_media.go:16-20` 暴露艺人单项和全量联网接口，旧合同仅描述后台身份项，已补入手动艺人章节。

## 验证结果

合同扩充后重新执行了下方两轮 `rg` 抽查、样本读取、归档引用检查和 `git diff --check`，全部通过。没有运行测试，符合本任务“不需要跑测试”的要求。

## 抽查命令与结果

- `rg -n 'admin/curation/(settings|progress|sources|batches|reverts|suppressions|classifications|conflicts|releases/.+match-results|model/retry)|artists/\\{id\\}/web-search|artist-web-search' backend/internal/curation/httpapi` 命中 `handlers.go:43-88`、`artist_media.go:16-20`，合同接口路径与路由一致。
- `rg -n 'status[ =].*"(matched|unmatched|conflict|query_error|waiting_model|waiting_route|in_progress|waiting_human|shelved)"' backend/internal/curation` 命中 `program/release_route.go:137-154`、`model_queue.go` 和 `artist_web_search.go:56`。重试状态在代码中为 `try_later`，合同已按代码记录。
- `rg -n 'Conflict\\("(curation_disabled|curation_paused|scan_running|source_disabled|batch_closed|nothing_to_apply|revert_conflict|exception_stale|artist_already_linked|artist_redirected)"' backend/internal/curation` 命中 `writer.go`、`sources.go`、`worker.go`、`artist_web_queue.go` 等实现。
- `rg -n '以本节为准|tasks/archive' .trellis/spec/backend/curation-contracts.md` 无输出。
- `rg -n 'curation-contracts\\.md#' .trellis docs` 仅命中更新后的第 5 节链接，无失效旧锚点。
- `git diff --check` 通过。

## 第二轮核对命令与结果

- `rg -n 'track_auto_values|track_effective|track_auto_correction|ApplyTrack' backend/migrations backend/internal/curation backend/internal/catalog` 命中 `0065_track_corrections.sql`、`catalog/governance/track_auto_values.go` 和 `curation/writer.go`，已补写曲目自动层、锁顺序、`OnlyEmpty`、撤销和回填。
- `rg -n 'release_match_credit_parts|curation_match_names|curation_artist_negative_pairs|local_credit_parts|local_model_groupings|file_digest' backend/migrations backend/internal/curation backend/internal/catalog` 命中 0102、0104、0106 迁移及对应读取写入代码，已补写识别结论存储、拆名否定对、本地归组和摘要字段。
- `cat backend/internal/app/testdata/api-samples/curation/{conflicts,match-results,match-selection,progress,settings-model,source-updated}.json` 核对了 conflicts、results、candidates、snapshot_id、selection、进度模型段、设置字段和源字段，合同已补入。
- `rg -n 'Reasons.*append|"(compound_title|missing_artist|filename_only|missing_position|missing_disc|artist_split|legacy_encoding|unknown_disc_directory|mixed_directory)"' backend/internal/curation/local_items.go` 命中本地辅助原因码，合同已列出七类输入和代码原因码。
- `rg -n 'localDependencySQL|curation_stable_releases|artist_web_generation' backend/internal/curation/model_queue.go backend/internal/curation/program/queue_query.go` 命中目录本地辅助与识别队列的稳定性互斥条件，合同已写成全目录稳定后一起放行。
- `rg -n 'ArtistWebCallLimit|ArtistWebTokenLimit|MatchRuleVersion|max_request_tokens|model_concurrency' backend/internal/curation backend/internal/app/testdata/api-samples/curation` 命中 10 次、50000 token、规则 106、单次预算和并发范围，合同已统一写成代码现状。
- `grep -n "以本节为准\\|tasks/archive" .trellis/spec/backend/curation-contracts.md` 无输出。
- `rg -n 'curation-contracts\\.md#' .trellis docs` 仅命中已更新的第 5 节锚点。
- `git diff --check` 通过。

## 第三轮核对

逐条复核 `temp/factcheck-11.md` 后，采纳并改写合同的项目包括：扫描原因、文件大小与原始修改时间、标签媒体标记、拆名与归组；编号查询准入、方法佐证、`partial_subset`/`partial_superset`、发行组直接认定条件和散放单曲路径；`insufficient_evidence`、`candidates_revoked`、`ambiguous_editions`、`request_limit`、历史状态和文件摘要函数；待定项状态 CHECK、列、`model_stop_code`、`empty_count`、`reset_item_usage_on_claim`、模型状态表、稳定视图和排期字段；本地辅助七种原因、无模型建项、目录稳定放行；模型暂停映射、空返回、批次 400 拆分、共享日账和请求预算；AcoustID 334 毫秒间隔、`acoustid_sparse`/`acoustid_spread`、0.9 阈值和缓存限制；署名、自动合并、发行并入、艺人资料表、设置字段、投影字段、错误码和接口行为。

测试锚点逐个用 `rg -n 'func <用例名>' backend` 确认后，合同改为代码中存在的用例名，包括 `TestPostgreSQLProgramMatchResultsAPI`、`TestPostgreSQLJudgeConflictSelectionRevalidatesSnapshot`、`TestPostgreSQLProgramModelMatchSameKeyKeepsNewerConclusion`、`TestPostgreSQLCurationCJoinGroup`、`TestPostgreSQLLocalAssistSevenReasons`、`TestPostgreSQLLocalAssistGroupingRevert`、`TestPostgreSQLLocalAssistBeforeMatch`、`TestPostgreSQLCurationModelRulesDirectoryAndPriority`、`TestPostgreSQLCurationModelRulesBatch400`、`TestPostgreSQLJudgeHTTPBudget`、`TestPostgreSQLJudgeNameKeepUncertainAndLateCorrection`、`TestPostgreSQLArtistWebSearchOrderedQueue` 和 `TestPostgreSQLCurationCProgress`。

不采纳项目及依据如下：

- “移动有歧义时保留旧数据”在扫描实现中没有对应分支，合同不写。
- `CD2`、`1-03`、`A1` 的显式保留在当前解析测试中找不到可执行断言，合同保留通用编号规则但不列这三个示例。
- `revertible` 不是代码返回字段，合同按撤销冲突规则描述，不单列该标记。
- 补齐曲目艺人后重算歌词摘要、共享本地优先读取、启动清理无效领取和旧会话、重试前重查共享额度均无独立可引用实现或属于实现细节，合同不新增断言。
- 合辑占位艺人有效署名为空、管家 MusicBrainz 工具绕过闸门、无编号项先排声纹、资料建立后排队、Commons 的 P18、模型设置不影响程序摘要在当前代码或样本中没有可独立核对的合同断言，合同不新增表述。
- 第三轮清单中目录本地辅助“结案后仅一张继续识别”的缺陷不成立，`model_queue.go` 的目录依赖会等待 `release_ids` 全部稳定并在结案后整体放行，未记录为实现出入。
- 清单把 `waiting_retry` 与 `try_later` 混用，代码未定义 `waiting_retry`，合同和记录统一为 `try_later`。

## 第三轮核对命令与结果

- `rg -n 'partial_subset|partial_superset|insufficient_evidence|candidates_revoked|ambiguous_editions|request_limit|curation_release_file_digest|curation_eligible_releases|curation_attempted_change_at' backend/internal backend/migrations` 命中对应匹配、迁移、队列和排期实现，合同已补齐。
- `rg -n 'model_stop_code|empty_count|reset_item_usage_on_claim|curation_model_state' backend/internal backend/migrations` 命中 `0101_curation_model_rules.sql`、`pending_items.go` 和模型调度器，合同已补齐。
- `rg -n 'acoustid_sparse|acoustid_spread|MinInterval|artist_external_artworks|artist_external_bios|judged_by_model|musicbrainz_names' backend/internal backend/migrations` 命中声纹、艺人资料和投影实现，合同已按代码修订。
- `for t in TestPostgreSQLProgramMatchResultsAPI TestPostgreSQLJudgeConflictSelectionRevalidatesSnapshot TestPostgreSQLProgramModelMatchSameKeyKeepsNewerConclusion TestPostgreSQLCurationCJoinGroup TestPostgreSQLLocalAssistSevenReasons TestPostgreSQLLocalAssistGroupingRevert TestPostgreSQLLocalAssistBeforeMatch TestPostgreSQLCurationModelRulesDirectoryAndPriority TestPostgreSQLCurationModelRulesBatch400 TestPostgreSQLJudgeHTTPBudget TestPostgreSQLJudgeNameKeepUncertainAndLateCorrection TestPostgreSQLArtistWebSearchOrderedQueue TestPostgreSQLCurationCProgress; do rg -n "func $t\\b" backend; done` 每个用例均命中一个定义。
- `grep -n "以本节为准\\|tasks/archive" .trellis/spec/backend/curation-contracts.md` 无输出。
- `rg -n 'curation-contracts\\.md#' .trellis docs` 仅命中两个更新后的第 5 节链接。
- `git diff --check` 通过。未运行测试，符合任务要求。
