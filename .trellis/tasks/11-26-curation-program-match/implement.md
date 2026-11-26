# 程序整理实施顺序

从仓库根运行以下命令，Go 使用仓库固定的 mise 版本。集成命令只匹配列出的用例，由脚本新建一次性 PostgreSQL、独立临时目录及 Compose 项目。下面是实施后的验证计划。设计阶段不运行应用或容器。实现代理改生产代码与受影响合同，测试代理负责新增测试、更新已过时的断言与响应样本。

## 1. 确认共用输入与比较键

- 与子任务 3 统一 `backend/internal/catalog/textnorm/textnorm.go` 中的 `MergeKey(string) string`。按 design 的 Unicode 规则实现纯函数；3 先合入时直接复用。与扫描侧约定 CUE/log 文件观测和抓轨日志 TOC 的字段及交付顺序。
- 在 `backend/internal/catalog/curation_evidence.go` 读取物理文件输入，与 `backend/internal/curation/release_identity.go` 约定文件摘要。先将它与现有完整事务证据摘要并列，待第 7 步切换调度。迁移基线和运行时使用完全相同的成员、序和时间精度。
- 验证现有键的行为：`(cd backend && mise exec -- go test ./internal/catalog/textnorm -run '^Test(TextExact|TextKeepsDistinctKeys|LooseSearchKeys)$')`。新键的相等/不等样本由测试代理补，现有用例只验证旧键。
- 风险：直接调用 `Text/Loose` 会额外合并繁简、假名或符号；按版本回退到旧全文摘要会把算法变化误当改文件。

## 2. 先落库，建立可回退的旧数据转换

- 新增 `backend/migrations/NNNN_curation_program_match.sql` 和同名 `.down.sql`，按 design 建来源结论、署名明细和 `file_digest`，回填旧数据；同步 `backend/internal/curation/work_states.go` 的列、读写和到期索引谓词。序号与子任务 6 等并行迁移在集成时顺排。文件基线在重新解析前取旧扫描事实；旧时间戳只采信真正的查询字段。
- 在 `backend/internal/curation/match_results.go` 实现按来源读写的窄方法，调用方能传已有事务。把新状态定义为公共值供程序与 Writer 共用；6 的 `query_error` 只在升级和投影处引用，存储值由两方核对。
- 验证文件发现：`(cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')`。验证 up/down：`ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/.*curation_program_match' ./scripts/test-integration.sh`。测试代理在 `backend/internal/platform/postgres/migrations_rollback_integration_test.go` 补此版本夹具，核对新状态、拆分证据、旧表及索引恢复。
- 风险：两个迁移分别重建同一个 CHECK 时，up/down 需按实际前一版组合复原。down 在删表前重建受影响投影及清理派生对应，不能删人工对应及历史操作。

## 3. 接通准入和 S8 查询顺序

- 改 `backend/internal/catalog/curation_classification.go`、`backend/internal/curation/program/{queue_projection.go,queue_query.go,match_search.go,match_lookup.go,worker.go,release_route.go}`；按是否可进入实际查找步骤得出排队与执行的同一判断。只在识别路径微调 `backend/internal/curation/model_queue.go` 共用的优先级，保留资料和模型队列的调度。
- 分拆编号和文字的混合查询，沿用 MBID 详情、TOC、条码、目录号与厂牌、ISRC、声纹的既有 finder；将原始命中、已获候选、错误分开返回。改 `backend/internal/sources/musicbrainz/search_page.go`、`backend/internal/curation/search_pages.go`、`backend/internal/app/curation_musicbrainz.go` 的必要参数，支持最后一次按唯一艺人编号查询。删掉读取巡检规范名与重搜，保留本地辅助已经写入的有效值。
- 验证：`(cd backend && mise exec -- go test ./internal/catalog -run '^TestCurationClassification$' && mise exec -- go test ./internal/curation/program -run '^Test(LookupBounds|FirstHitStopsLookup|DiscTOCFromCue|B3FingerprintReachAndFallback)$' && mise exec -- go test ./internal/sources/musicbrainz -run '^Test(SearchQueries|DiscTOC|整理ISRC合并搜索)$')`。排队用 `ROOMUSIC_TEST_RUN='^TestPostgreSQL整理队列五档取项与模型查询门槛$' ./scripts/test-integration.sh`。
- 风险：`program.Match` 的简单 finder 也要覆盖；零结果条件必须基于当前步骤的原始结果。不能让后续名字搜索、旧巡检回调、声纹预查在强线索已经判定时再发请求。

## 4. 在当前线索上完成判定

- 改 `backend/internal/curation/program/{match.go,match_album.go,match_lookup.go,match_snapshot.go,match_partial.go,match_single.go,fingerprint.go}`。统一复用候选阻断、合并键、组 ID、`earlierEdition` 与结构比较。消除发行路径三处候选数量门槛和 `searchDetails.read` 三次上限对必须逐版核对的限制；单曲声纹的既有结构规则不一并删除。
- 单步产出已匹配、未匹配或冲突；仅完整候选集全受阻记撤销类未匹配。快照保留查询顺序、来源、方法和冲突，老快照可以解码；105 重查由当前规则执行，不给旧证据补造核对结果。用相同判定处理整页、详情、快照重放和模型调用的部分吻合验证。
- 验证：`(cd backend && mise exec -- go test ./internal/curation/program -run '^Test(发行组唯一的条件与结构判定|标签发行编号不能覆盖文字反证|AlbumContraryEvidence|AlbumCorroboration|AlbumMethodRequirements|SingleRules|B3FingerprintAlbumRules)$')`。共用部分吻合回归用 `ROOMUSIC_TEST_RUN='^TestPostgreSQL模型提交部分吻合补读异组$' ./scripts/test-integration.sh`。
- 风险：未知曲数、截断页、含有未排除异组的结果不足以认组。推断曲名读代表版后不可泄漏该版特有的录音对应、艺人、厂牌或条码。四首短专辑和四个候选版本是不同边界。

## 5. 把匹配证据写进自动层

- 组年份与代表曲名改 `backend/internal/curation/program/{match.go,enrich.go,track_fields.go}`。传递首发年份，必要时通过 `backend/internal/curation/source_finders.go`、`backend/internal/app/curation_musicbrainz.go`、`backend/internal/sources/musicbrainz/release_group.go` 读组实体的日期，避免该方法现有的逐页读取发行列表。核对原有字段写入、抑制和撤销。
- 确定性拆署名改 `backend/internal/curation/program/artist_links.go`、`backend/internal/curation/{field_artist_links.go,external_link.go,external_link_revert.go}`、`backend/internal/catalog/curation_artist_credits.go` 以及 `backend/internal/catalog/internal/artistgraph/{facts.go,refresh.go}`。先核对原始署名全文，再持久化有序明细、解析身份并建立各位置的对应；将拆分的撤销和自动值作废接到已有 writer/revert 和投影刷新路径。对受影响范围重建，显示仍读原文字。
- 两张同版改 `backend/internal/curation/{release_merge_discovery.go,release_merge_evidence.go,release_merge.go}` 及 `program/{enrich.go,track_fields.go}` 的发现入口。把版详情带给发现；在抑制与人工分组检查下，重合的走归组、完整互补的走程序治理并入，其余按现有模型准入。事务内验证预览、成员、双方对应和完整外部碟轨；复用现有并入执行器与幂等结果。
- 验证：`(cd backend && mise exec -- go test ./internal/curation/program -run '^TestAlignCredits(ConflictingArtist|TrackLayers)?$')`；`ROOMUSIC_TEST_RUN='^TestPostgreSQL(CurationProgramGroup|CurationProgramTrackFields|CurationReleaseMergeProposed|ReleaseMergeRevert)$' ./scripts/test-integration.sh`。
- 风险：把同一个本地合名对象连到两个 MBID 会相互覆盖。直接写派生署名重扫就会丢。自动并入不能仅凭碟数、总曲数或几个非重复录音；同一步发现、写入和模型领取竞争时只允许一个有效结果。

## 6. 保存来源结论、安排 90 天重查并交接 API

- 改 `backend/internal/curation/{match_results.go,unlisted.go,work_states.go,progress.go,httpapi/handlers.go}` 与 `backend/internal/curation/program/{pass.go,release_route.go,match_search.go,queue_query.go,model_matcher.go}`。在既有结论事务更新本来源；模型待定项只记录后续工作。对照 `patrol_submit.go` 中的旧状态谓词，保证 9 清理前程序不会依巡检名重新搜索。
- 每次未匹配按 `checked_at + 90 天` 预约；用触发原因控制仅程序，保留声纹、按本次结论更新时间。撤销类/冲突清空周期到期时间，复用手动入口的并发请求保护。与 6 合并 `pass.go` 时，只在成功提交一侧更新来源表，所有真实错误原样返回 6 的分支。显式验证到期重查仍会得到新的冲突，而非一律改回未匹配。
- 后端提供 design 里两种读取与原手动入口，交主代理改进度显示、decoder 和测试夹具；本子任务不改前端。新接口的样本登记到 `backend/internal/app/api_samples_integration_test.go` 和它的 curation 样本流程，由测试代理用合成库生成。并行任务引起的无关样本变化不可一并交付。
- 验证：`ROOMUSIC_TEST_RUN='^TestPostgreSQL整理未收录九十天与管理员重查$' ./scripts/test-integration.sh`（当前旧用例名）；`ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' ./scripts/test-integration.sh`。需要更新预期时用同一过滤器加 `ROOMUSIC_UPDATE_API_SAMPLES=1`，检查实际文件差异。
- 风险：旧 `unlisted_since` 常是首次结论时间；重查成功、工作状态更新和旧模型线程结项是三个时刻。聚合计数须按发行去重，查询中止不能发布缺席结论。

## 7. 切到规则 105、同步规范并合入验证

- 改 `backend/internal/curation/release_identity.go`、`backend/internal/curation/program/{worker.go,release_state.go,release_digests.go,queue_projection.go,pass.go}`。用第 2 步的基线识别文件变化，移除逐版本摘要和按冲突码升级的分支。普通升级只排未匹配/冲突/查询出错；可选的“已匹配也核对”标记走已存 ID，此次关闭。缓存艺人补建、过期自动值作废和陈旧待定项处理仍按原事务边界完成。仅升 `MatchRuleVersion`。
- 验证：`ROOMUSIC_TEST_RUN='^TestPostgreSQL(CurationExternalRuleUpgradePending|CurationProgramRealRescan|CurationProgramRescanAndChanges)$' ./scripts/test-integration.sh`。按 design 的规范表同步 `.trellis/spec/backend/{curation-contracts,sources-contracts,artist-contracts,release-merge-contracts,database-guidelines}.md` 中受影响段落；其余章节交 11。验证文档和依赖方向：`git diff --check`、`(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')`。
- 子任务 1 合入后，执行其交付在测试说明里的固定样本命令，包括纯程序和脚本模型两种模式。当前树尚无该任务承诺的最终入口名，不能把旧 `TestPostgreSQLCurationMetrics` 的一次输出当作新门禁。用同一份合成曲库、同一 mock 延迟和基线设置，记录每种模式认对数、认错数、每张实际请求数及用时的基线、当前值和差值；认错须为零，其余三项不退步。
- 风险：只因算法或来源记录变了就排全库查询会掩盖回归。升级夹具应同时包含已匹配、未匹配、旧 too_many、查询出错和已领取模型项，分别验证出站请求和版本标记。子任务 1 的入口、子任务 6 的状态合同和扫描取证未到时，将对应验证列为待验。

## 测试代理补充清单

| 规则 | 合成对照与关键断言 |
| --- | --- |
| A2.1 准入 | 单边名字且无编号/有编号；双占位声纹可用/不可用；合辑没有显式专辑艺人时用群星；散曲音轨名与艺人；本地辅助补全前后；无可用名字的专辑仍可补歌词。核对识别请求、平台行和队列优先级。 |
| S8/S9 | 七步各一例命中及前步查空的顺序；每个强线索拿到反证立即停止。TOC 多碟交集、条码与目录号不同来源、带厂牌、ISRC/声纹各抽四首；有编号全空不搜名字，声纹不可用/临时失败由 6 的既有合同区分。严格名字命中、只在零结果一次放宽、艺人零/一/多位、异写标题的 arid 查询，断言 HTTP 请求与顺序、名字阶段最多四次全空搜索、从未使用巡检名。 |
| A2.3 组与部分吻合 | 2、3、4 版和更多版的同组、同名异组、单版、合辑、古典、四首短专辑、截断页、全受阻/部分受阻。普通同组详情计数为零，需补推断曲名仅最早一版一次；不产生版本或录音对应。按同键、异键、不同艺人分别验证子集、末尾 bonus、同曲异名 EP，在线和重放一致。 |
| A2.5 自动层 | 组首发早于所有候选、首发缺失、已有年份、人工字段、撤销后抑制；拆/不拆 `Jay-Z & Kanye West`、连接词不同、已有另一佐证、曲目/专辑位置、人工修正、重扫与按来源撤销。两份互补整碟直接合并、重复录音保留两版进同组、总数相同但缺轨、人工分组/抑制、并发/重试均无错误并入。 |
| S7/A2.6 | 两个来源的合成记录互不覆盖，标签与 UTC 时间正确；先出错无行、重查出错不污染上一成功时间；撤销与占用同文案并排除 90 天。以可控时钟覆盖 89/90/180 天、手动触发与来源恢复，确认声纹实际调用且模型领取/建项均为零。 |
| A4/迁移/API | 104 升 105，matched 无识别出站、旧 too_many 进入新判定、查询错误按 6 最终状态值重开；模拟声明已匹配需核对时只请求已有 MBID。文件同尺寸改标签、增删成员、CUE 变化对照仅算法/自动字段变化；旧模型项并发结项不能覆盖新一代。up/down/up、空库、旧历史时间未知、重组后撤销、响应样本和进度聚合均按设计断言。 |
