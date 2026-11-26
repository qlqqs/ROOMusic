# 扫描归组实施顺序

以下命令供后续实施和测试使用。本轮只交付设计。命令从仓库根运行，Go、Node 使用仓库的 mise 版本。集成脚本自行分配独立 Compose 项目；输入限于合成曲库、隔离测试库和模拟上游。实施代理负责生产改动，测试代理负责新增样本和修改经 PRD 推翻的旧预期。

## 实施步骤

1. **定下复用接口和迁移。** 和子任务 5 采用相同的 `backend/internal/catalog/textnorm/keys.go`、`MergeKey(string) string`。按设计增加 `backend/migrations/NNNN_scan_grouping.sql` 及对应 `.down.sql`（合入时按当时最大号顺延）。建版本词列、附件关系、内部曲序列、列表排序函数与艺人排序列；正反向使用已有艺人投影换代流程。为旧行保留可读默认值，down 在删列前恢复旧曲位。风险是改变既有搜索键、迁移号冲突或只在新库可运行。

   验证已有基础合同，用例覆盖新键及迁移往返后再验新语义。

   ```sh
   (cd backend && mise exec -- go test ./internal/catalog/textnorm -run '^Test(TextEquivalent|TextKeepsDistinctKeys|LooseSearchKeys)$')
   (cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/.*scan_grouping' mise exec -- ./scripts/test-integration.sh
   ```

2. **整理逻辑目录与混放候选。** 改 `backend/internal/catalog/ingest/{discmarker,scope,staging,organizer,continuity,filename_numbers,observation}.go` 及 `backend/internal/library/scanner.go` 中对应的暂存/Box 封面调用。先扩展 `OrganizationScope` 以识别附加碟，再在 CUE 对齐后将同父的直属轨统一到已有碟父范围，再将编号、带名、附加碟送入父目录 S4。四次连续性分别构造整批输入。整组失败后逐格式决定成功或回落，按名拆时显式处理缺专辑名、缺艺人和推断值。保持 CUE 去重、根散曲及格式版本规划。风险是只改保存一遍、在分碟时提前返回、或不同专辑的格式桶共用版本组。

   ```sh
   (cd backend && mise exec -- go test ./internal/catalog/ingest -run '^Test(ParseDiscMarker|ParseDiscMarkerRejectsNonDiscNames|ContinuityAlbum|OrganizeLooseAndUnknown|CueCandidateIncludesExplicitParentOutsideSheetDirectory|ResolveCuePhysicalCoexistenceUsesExplicitParent)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQL(ScanFormatSplitVersionGroup|LooseSinglesEmbeddedArtworkAndRescan)$' mise exec -- ./scripts/test-integration.sh
   ```

3. **分离编号、顺序和传给读者的证据。** 改 `backend/internal/catalog/ingest/{organizer,continuity,filename_numbers,observation,persist,persist_track}.go`。移除按序列推碟和缺号回写。先归一明确编号，再按碟生成 `SortPosition` 并用它实现 `orderedTracks`，INSERT、UPDATE 同时写曲号与次序；默认 1 和被放弃的冲突推断不能写进 `track_observations`。改 `backend/internal/catalog/{read,browse_tracks,artists_collections,curation_evidence}.go` 与 `backend/internal/catalog/governance/issue_read.go` 的有序轨表，按新列排、按真实曲号返回。修正 `frontend/src/features/catalog/components/{album-detail-view,track-info}.tsx` 和 `frontend/src/features/governance/components/release-issues-detail.tsx` 对未知曲号的既有文案。风险是 SQL 同号按 UUID 随机感排序、前端重新填号，或结构核对把内部次序当标签。子任务 2 拥有 `filename_numbers.go` 中的曲名/艺人提取，本步只合并涉及编号的代码段。

   ```sh
   (cd backend && mise exec -- go test ./internal/catalog/ingest -run '^Test(NaturalOrder|FilenameNumbersFiveLevelRules|OrganizeDiscNumberEvidencePriority|OrganizeSingleCueTrackNumberOutranksFilenameButNotPhysicalTag)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQL(RescanKeepsTrackIdentity|SaveObservationCreatesSeparateDiscMedia)$' mise exec -- ./scripts/test-integration.sh
   (cd frontend && mise exec -- npm test -- src/app/album-detail.test.tsx src/app/issues.test.tsx)
   ```

4. **产出字段决定、专辑艺人、版本词。** 改 `backend/internal/catalog/metadata/compilation.go`、`backend/internal/catalog/ingest/{compilation,organizer,persist}.go`，新增同目录 `version_terms.go`。由共用分布计数给出真实首位艺人、占比和稳定展示名，保留明确专辑署名及合辑证据。实现平票空决定的保留和 NULL 写入、现有证据列表及问题读取，补 `frontend/src/features/catalog/labels.ts` 的“有冲突”。把扫描版本词数组写入发行行并纳入候选摘要。先按主代理对设计末节括号副标题的裁定写一组正反例，再接入比较副本。风险是遗漏空选值的可选字段、覆盖人工值或把两个并列艺人投影成一个新名字。

   ```sh
   (cd backend && mise exec -- go test ./internal/catalog/metadata -run '^TestIsCompilationPlaceholder$')
   (cd backend && mise exec -- go test ./internal/catalog/ingest -run '^Test(OrganizeAlbumArtistKeepsAuthoritativeProvenance|OrganizeGenresUnionDoesNotFlagDifferentTrackGenres|FolderMetadataDoesNotChangeCandidateIdentity|CandidateMetadataSignatureIdentifiers)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQL(ReleaseIssuesDetail|ArtistGraphCompilationSkipsReleasePlaceholders)$' mise exec -- ./scripts/test-integration.sh
   (cd frontend && mise exec -- npm test -- src/features/catalog/labels.test.ts)
   ```

5. **发现、签名并原子绑定附属文件。** 新增 `backend/internal/library/attachments.go`，改同目录 `scanner.go`、`candidate_signature.go` 与 `backend/internal/catalog/ingest/persist.go`。同一逻辑目录共用一份按路径排列的文件清单，签名消费清单里的 stat，写入时再解图片头。用成功/不可读取区分空清单与失败，不可读取时保留旧绑定并使下轮可重试。只有获得 release ID 后才在候选事务中替换本发行附件。风险是过滤漏用、签名和写入枚举不同目录，或只变 PDF 时被跳过。

   ```sh
   (cd backend && mise exec -- go test ./internal/library -run '^Test(CandidateSignatureSidecarMetadata|CandidateSignatureAddedArtworkAndRipLog|CandidateSignatureIgnoresFilteredArtwork|FolderArtworkSearchesDirectoriesInOrder)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLArtworkPriority(格式版本各用内嵌封面|艺人目录封面不向下继承)$' mise exec -- ./scripts/test-integration.sh
   ```

6. **接入艺人和专辑列表。** 改 `backend/internal/catalog/internal/artistgraph/profiles.go` 和 `backend/internal/catalog/{read,recent_additions,artists_reads,artists_collections,browse_artists,browse_albums}.go`。艺人资料同事务写入有效名对应的列表键和首字母；主列表、组列表、艺人页与兼容浏览按有效值排序。保留 SQL 分页及 ID 判平，测试管理员改名、发行标题修正，以及搜索仍能按原显示名命中。风险是只给扫描层的名字排序、改动搜索使用的旧键，或组代表版本与列表采用不同次序。

   ```sh
   (cd backend && mise exec -- go test ./internal/catalog/internal/artistgraph -run '^TestInitialGroup$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQL(ArtistListCountsAndSearchDeepPages|ReleaseGroupListMirrorsReleaseList|RecentAdditionsProjectionAndSort)$' mise exec -- ./scripts/test-integration.sh
   ```

7. **统一升级版本、修订合同并核对依赖。** 改 `backend/internal/catalog/ingest/version.go`，归组版本 7→8；改 `.trellis/spec/backend/{library-scan-grouping,library-scan-incremental,artist-contracts,catalog-rest-contracts,database-guidelines}.md` 的相应章节。以代码为准更正规范里的版本号，写明缓存、签名新增行、缺号、首轮重组、附件发现和降级影响。将新旧图的身份验证与测试代理样本对照。风险是重复升版本，或独立升级本任务时把解析缓存也失效。

   ```sh
   ROOMUSIC_TEST_RUN='^TestPostgreSQL(ParseCacheGroupingVersionUpgrade|IncrementalCandidateStateFormatSplitSkipKeepsVersionGroupAndFavorites|ReleaseMergeRescanKeeps|RatingScanMovesFavoritesAndRatingsAtomically)$' mise exec -- ./scripts/test-integration.sh
   (cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
   git diff --check
   ```

8. **集成后对比子任务 1 基线。** 读取 `.trellis/tasks/11-26-curation-fixed-samples/research/baseline.md` 及该任务合入的用法，按其固定入口、样本集和时钟运行只用程序和带脚本模型两种模式。当前仓库的相关入口是 `ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh`；若子任务 1 发布新选择器，以其公布的同一条基线命令和参数取代，不用当前入口的默认 oracle 代替两种模式。对认错张数（须为 0）、认对张数、每张请求数、总用时逐项记录“基线 / 本次 / 差值”，认对不少、请求和用时不增加。风险是把旧版混放样本预期或不同计时口径拿来验收。此步只在子任务 1 合入后执行，结果进合入交付说明。

## 测试代理清单

测试代理维护既有测试的对照预期并补以下样本。实施步骤中的已有测试负责邻近行为；下面的反例需实际跑到相应断言后才算覆盖。每个阶段只跑该阶段涉及的选择器。

| 范围与建议位置 | 合成样本和断言 |
| --- | --- |
| `backend/internal/catalog/ingest/{continuity,organizer,filename_numbers}_test.go` | 分别构造只有四种读法中的对应一档能通过的样本；乱序输入相同结果；连续 01–03、标题为甲甲乙，改旧第 7 条预期为一张；缺号/重号不吸收少数。全组失败后两种格式都连续、仅一种连续、两种都失败分别覆盖；失败桶区分“全有名”和“有人缺名，同/不同艺人”；同 MBID 两目录不合并、格式版本组不串标题 |
| `ingest/organizer_test.go`、`backend/internal/library/scanner_format_split_integration_test.go` 及旧 Box 样本 | CD1+Disk II，父目录正轨+CD2，跨碟标签名不同但各碟连续，带碟标题，独立 Bonus/特典及多个附加碟排在最大正碟后；单 CD2 保留碟号；非碟目录及根散曲各自独立。检查两遍身份规划、稳定锚、`media.title/position`、CUE 跨目录、同目录拆分后封面不串图 |
| `backend/internal/catalog/textnorm`、`ingest/version_terms_test.go` | 预组/组合变音、大小写、标点、空白得到同键；简繁/假名、The 前后不被合并。目录与专辑名相同版本词只存一次，嵌套括号、大小写变体、Remastered 2009、纯年份及所有排除标记。普通副标题走主代理确认的判例。修改版本词而成员不变，两次扫描 release/track ID 相同，原显示标题与 edition 可读 |
| `ingest/compilation_test.go`、`organizer_test.go`，`backend/internal/catalog/governance/issue_read_integration_test.go` | 甲三票乙两票；各半二人、三人、四人并列、五人分散、五人中一人过半；重复客串、合辑标志、显式专辑艺人优先。字段相同、2:1:1、2:2 对照；必填和可选字段均测试平票。查 SQL NULL、候选值、原因、“有冲突”仅记一次、修正覆盖仍生效 |
| `ingest/{organizer,natural_order}_test.go`、`backend/internal/library` 集成 | 1,2,1,2 保留同碟，CD2、单个 1-03、黑胶的正反面、CUE 明确号均保留。无号的取 2 在 10 前的自然序，混有明确号时各字段独立；无号重扫仍无曲号观察、无 path_order_fallback。详情、Subsonic、整理证据及队列对照，旧缓存伪轨号 1 不能变成真实编号 |
| `backend/internal/library/{candidate_signature_test.go,attachments_test.go}` 及扫描集成 | 根内相对路径、直接子目录大小写、PDF 与图片头尺寸、WebP/TIFF、多个扫描图与封面共存、目录内多发行共享、CUE sheet 位置。深层、排除和符号链接不绑；增删改 PDF/非封面图能触发，二次未变可跳过；读取失败与候选事务失败后旧行保留，修复后重试成功 |
| `backend/internal/catalog` 列表集成及 `frontend/src/app/album-detail.test.tsx` | The Beatles 与 Beach Boys、Theatre、Les、带重音或标点的名字，跨页相同键按 ID 稳定。The 的分栏能从 B 找到，改名和撤销重排，原名仍可搜索；专辑按有效标题和代表版本排序，日期排序单独对照；无号弹层不显示虚构的“第几首” |
| `backend/internal/platform/postgres/migrations_rollback_integration_test.go`、`backend/internal/library/{scan_version_incremental,identity_rebinding}_integration_test.go` | 只选新增迁移往返，验证附属关系约束、老行兼容、up/down/up，再单独验证 7→8 解析缓存命中但候选重写，下一次跳过。拆分与合并的严格过半/平票、对应、手动修正、手动并入、曲目及旧组收藏，区分 ID 延续、旧记录保留与新图上的可见性 |
| 子任务 1 的固定样本 | 按步骤 8 交付两种模式的四项差值；两种模式的认错数分别为零 |

旧断言集中在 `TestOrganizeSameDirectoryConflictUsesMajorityOrStableSplit`、`TestOrganizeSameDirectoryAlbumTagVariants`、`TestOrganizeNonContiguousDiscDirectoriesStayLeafCandidates`、`TestOrganizeDiscDirectoryDecisionTable`、`TestApplyCompilation`、`TestOrganizerAssignsStableFallbackTrackPositions` 和 Box 封面相关用例。只替换被新规则推翻的预期，保留原输入做对照。合入时按测试代理实际交付的用例名追加精确的 `-run` 或 `ROOMUSIC_TEST_RUN`，测试输出须列出用例并执行断言。
