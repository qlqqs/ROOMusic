# 子任务 6 实施顺序

以下命令供实施、测试和合入阶段使用，从仓库根目录运行。Go 使用仓库的 mise 版本；数据库用例使用独立合成库。实现代理修改生产代码、合同及与新行为冲突的既有断言，新增用例由测试代理负责。这里只列当前存在的测试名作为实施锚点；新行为的断言见末节。

## 1. 来源分类、暂停与固定间隔

修改 `backend/internal/sources/{sources,queue,request,limiter}.go` 和 `backend/internal/sources/acoustid/client.go`。先在识别使用的 GET/POST 解码出口区分无效响应和正常空结果，再让队列按来源维护 5 秒起、最长 180 秒的退避。保留 Retry-After 更久的绝对时刻。移除失败等级对发送间隔的作用，让真实成功（含有效 404）既可清零、也可通知等候者。保持统计成功请求与恢复事件两个语义。封顶限于来源暂停，保留模型项级计时。

验证：

```sh
(cd backend && mise exec -- go test ./internal/sources -run '^Test(LimiterPriority|LimiterWaitDeadlineAndCancellation|RequesterOutboundProxyBackoff)$')
(cd backend && mise exec -- go test ./internal/sources/musicbrainz -run '^Test(RetrySchedule|RetryNetwork|CacheDirectoryAndInvalidResponse|SharedOutboundInterval)$')
(cd backend && mise exec -- go test ./internal/sources/acoustid -run '^TestLookupFingerprint(Errors|Backoff|RequestMappingAndCache)$')
```

风险在于 `decodeResponse` 会把无效响应转成通用失败，`pauseQueue` 可能接着把它变为全来源暂停。短重试之前必须先识别 HTTP 429/503，随后才解释业务响应。

## 2. 发行错误状态与 S8 续查

在 `backend/migrations/` 增加设计中的 `0103_curation_query_retry.sql`、`0103_curation_query_retry.down.sql`，承接 0102 的状态集合。改 `backend/internal/curation/work_states.go` 和 `backend/internal/curation/program/{match_search,match_lookup,release_route,pass,release_state}.go`。将步骤标识、错误种类、真实来源和已完成页摘要带回发行边界，保留已有 `result` 中的重查字段。同一次领取只保存一次等待或查询出错；保存时检查代次、输入和原状态。失效/成功时清除断点，重入步骤时用原查询键读缓存。普通结论和规则版本的变更接子任务 5 的实现。

验证：

```sh
(cd backend && mise exec -- go test ./internal/curation/program -run '^Test(FirstHitStopsLookup|LookupBounds|B3MatchWithoutFingerprint)$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/^[0-9]+_curation_query_retry[.]sql$' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_TEST_RUN='^TestPostgreSQL整理重启复用响应及已提交写入$' mise exec -- ./scripts/test-integration.sh
```

核对实际执行了新迁移子用例。已有 `try_later` 不含失败步骤，也无法从旧 `retry_count` 区分来源/专辑错误，升级时按设计重置。不要用空 `MatchResult` 代替有错误的返回值；否则 90 天程序重查分支也可能把它保存为未匹配。

## 3. 领取、恢复与进度

修改 `backend/internal/curation/program/{queue_projection,queue_query,queue_worker,queue_progress,worker}.go`、`backend/internal/curation/progress.go` 和 `backend/internal/app/testdata/api-samples/curation/progress.json`。把未来到期、来源等待、声纹依赖和可领取分开计算；`query_error` 同文件同规则跳过。到来源期限后允许首次正常出站；来源实际恢复后使本来源等待行立即可领并唤醒循环。重启读取等待行和长 Retry-After。来源停用也要唤起投影，以便 AcoustID 可跳过。接好子任务 5 的单张结论数据和主代理的进度 decoder。

验证：

```sh
ROOMUSIC_TEST_RUN='^TestPostgreSQL(整理队列限流时歌词和艺人资料继续|CurationGapAndIdentityChanges)$' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' mise exec -- ./scripts/test-integration.sh
```

旧限流用例断言暂停时没有发行状态、`try_later=0`，按新合同改成发行等待、常规尝试次数为零。计数 SQL 不能因到期时间在未来而把暂停队列显示为空；也要防止盘点把 `needs_work` 重新置真导致本地错误立即再试。恢复通知与等待事务先后均可能发生，写完等待后复核来源窗口，接收通知时复核当前窗口；仅解除同来源的等待。

## 4. 同一来源的优先级

修改 `backend/internal/sources/{queue,limiter}.go` 与 `backend/internal/curation/program/queue_worker.go`。按界面、普通识别、本地齐全识别、艺人资料的顺序给现有调用分档，艺人资料在创建来源上下文时单独标记；rank 4 复用实际队列排名。保留 `backend/internal/curation/program/model_queue.go` 在子任务 7/9 移除旧调用前的可编译入口。已有 `backend/internal/curation/model_queue.go` 的 rank SQL 仍管专辑内部排序。

验证（本步修改了许可选择，再跑对应的优先级用例）：

```sh
(cd backend && mise exec -- go test ./internal/sources -run '^TestLimiterPriority$')
ROOMUSIC_TEST_RUN='^TestPostgreSQL整理队列五档取项与模型查询门槛$' mise exec -- ./scripts/test-integration.sh
```

只在竞争相同来源时比较。把艺人工作者整体停到识别队列清空，会误挡仅需 Wikipedia/Commons 的工作。

## 5. 把 AcoustID 查询移到声纹队列

修改 `backend/internal/curation/program/{queue_projection,queue_query,queue_worker,queue_fingerprint,fingerprint,worker}.go`，以及与子任务 5 共用的 `match_search.go`、`release_route.go` 声纹入口。用 S8 同一套步前取参选择可预查的发行；同一事务登记样本，查询前后复核代次和来源可用性。声纹队列以所属发行优先级做 fpcalc 和 AcoustID，识别只消费该轮结果并按原规则核对；未预查但真实走到声纹的发行按需登记。改 `backend/internal/catalog/track_fingerprint.go`、`backend/internal/playback/fingerprints.go`、`backend/internal/app/fingerprint.go`，移除另一套技术失败 24 小时读取/写入门槛，保留确定性短轨和成功缓存。

验证：

```sh
(cd backend && mise exec -- go test ./internal/curation/program -run '^TestB3Fingerprint(AlbumRules|SingleRules|ReachAndFallback|NeedsWork)$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLB3AcousticFingerprintCache$' mise exec -- ./scripts/test-integration.sh
```

现有“解码失败冷却与重试上限”子用例同步改断言。检查重复盘点不会把已完成曲目重新置 pending，失败行也不会以 done/空列表放行；同一张四条样本只能触发一轮专辑失败。进程重启、同一版 90 天重查和文件代次变更要区分，避免旧的内存命中或旧指纹排队行挡住新识别。

## 6. 合同、合入门禁与基线对照

修改 `.trellis/spec/backend/sources-contracts.md` 第 4、5、8a 节及 `.trellis/spec/backend/curation-contracts.md` 的工作状态、声纹缓存与匹配、查询终止、队列/退避与进度段落，逐项对照 `design.md` 第六节。只调整本次规则，与子任务 5 共编段落按各自职责合并。按子任务 1 的 `.trellis/tasks/11-26-curation-fixed-samples/research/idle-gaps.md`，在 `backend/internal/curation/program/worker.go` 把 `ScanInterval` 默认值从 30 秒改为 2 秒。`scan_running` 按该间隔复查，其余休息状态使用 `PollInterval`。

验证：

```sh
ROOMUSIC_TEST_RUN='^TestPostgreSQL(CurationProgramGates|B3FingerprintGatePauseAndScan)$/^scan(_during_lookup|_running)?$' mise exec -- ./scripts/test-integration.sh
git diff --check
(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
```

固定样本回归依赖子任务 1 的真实入口。合入后用它公布的单条命令分别取得纯程序、程序加脚本模型的结果，与 `.trellis/tasks/11-26-curation-fixed-samples/research/baseline.md` 同样本、同延迟比较。交付列出认对、认错、按接口拆分的请求数、总用时和逐项差值；要求认错为 0、认对不减、请求数和用时不增。未取得基线时记录尚待验证。

## 测试代理补充清单

优先扩展已有用例，使用 `backend/internal/upstreammock/scenario.go`、既有假时钟、固定合成曲目和请求记录。以下每行包括正反样本或对照。

| 行为 | 检查点 |
| --- | --- |
| 确认扫描 | 扫描进行中按 ScanInterval（默认 2 秒）复查，扫描结束后下一轮即领取。用假时钟分别核对默认间隔与显式覆盖值 |
| 来源等待 | 分别注入 503 和 429，短重试、5/10/20/40/80/160/180/180 秒、无 Retry-After、较短值和超过 180 秒的秒数/HTTP 日期；暂停内零出站。恢复后第一次失败回 5 秒。正常 404 可清零，缓存命中不可。长故障中专辑计次保持不变，其他来源可工作 |
| 断点及缓存 | 强步骤查空，下一步骤命中、读详情时报错；期间无弱步骤、无未匹配、无模型项。进程重启后仍在同一步；检查上游请求序列和 `sources.cache.hit`，完整步骤不重跑，步骤内成功的请求命中，只补失败请求。另测期限到达无人先探测、同一进程中同源其他工作先恢复、异源成功、超过 3 分钟的 Retry-After 重启后仍生效（首次后台探测前界面成功也等原期限） |
| 专辑错误 | 某一张的 HTTP 200 响应结构损坏，另外一张同源正常。前两次等待，第三次 `query_error`，到期、手动重跑盘点及 90 天都不再发；修改文件或升识别版本重新计数。穿插来源暂停不增加次数，恢复后的有效步骤打断连续错误；首次样本计算失败与多首同轮失败各只记一轮 |
| 指纹本地计算 | 合成损坏音频、正常音频、精确时长短于 10 秒的对照；每一轮核对真实计算次数和文件快照。旧 24 小时错误缓存不会伪造第二次尝试；可重算成功的专辑继续识别。取消、扫描期间及数据库失败不计次 |
| 开关、密钥 | 启用、关闭、途中禁用、无效用户/应用密钥和 AcoustID 暂时不可用；关闭或密钥无效时解除声纹依赖，暂时故障保留断点，恢复前零名字查询；同一候选读 MusicBrainz 失败要显示真正来源 |
| 同源许可 | 令四个档位同时等待同一把限速器，检查顺序，包括 rank 4 识别先于艺人资料；在不同来源制造同时等待，验证可并行。用假钟比较连续失败前后同一来源相邻出站的间隔；另测 UI 等待时长和单次短重试仍遵从原合同 |
| 提前查询与耗时 | A 用 MusicBrainz 读详情期间，让前五步无可用输入的 B 在声纹队列查 AcoustID。用固定响应延迟记录区间交叠、B 被识别前样本已完成、B 的识别阶段 AcoustID 出站为零，比较同数据同步执行的 MusicBrainz 空档。各给一张有 MBID、TOC、条码、目录号或抽样 ISRC 的反例，另测已筛除项、前五步实际查空后按需查、90 天重查、有效空及重启从两层缓存恢复 |
| 投影并发与进度 | 同代多次盘点、等候中换文件、旧代晚到的声纹、途中更换来源状态及处理结果与恢复事件同时到来。验证无重复登记、过期结论或错误次数；同一主题中有等待项和就绪项时，进度分别计数，能读到专辑等待来源和期限 |
| 升降级 | 迁移前放入旧发行等待行和其他主题行；升、降、再升后查约束、原结果其他键、兼容状态及新终态回退。与子任务 5 的结论迁移叠加，检验其约束允许值和匹配结果仍可读 |

## 实施记录

采用现有队列与来源缓存。断点已接到文件编号、光盘、条码、目录号、ISRC、声纹、名字七个入口。每步重入时沿用来源缓存和步前页摘要。声纹队列把一张发行的多个样本错误合成一次，识别消费时在发行事务持久化。两个工作者交接前若中断，该步下轮通过已有的本地指纹和 HTTP 响应缓存重做。

已按授权修改的既有用例如下。

- `backend/internal/curation/program/b3_fingerprint_test.go`：无效指纹查询返回错误，断言没有结论且没有发名字请求。
- `backend/internal/app/curation_queue_sources_integration_test.go`：限流时有一张发行等待，重试次数、结论次数、代次均为零。
- `backend/internal/app/curation_b3_fingerprint_integration_test.go`：连续三次实际解码，每次都能从曲目记录看到尝试次数增加；移除原先 24 小时冷却与曲目永久失败的预期。
- `backend/internal/platform/postgres/migrations_rollback_integration_test.go`：为 0103 登记旧版等待行和新版 `query_error` 夹具，沿用既有结构、整行快照与再升级门禁。
接口进度样本增加 `query_error` ，与 matched、unmatched、conflict、try_later 一同输出。规则版本为 105，诊断迁移号为 103。

生产整理器默认每 2 秒复查进行中的扫描。`backend/internal/app/curation_program_fixture_integration_test.go` 的假时钟使用原有的 30 秒显式配置。默认间隔及覆盖值的断言、重启后仍等待原来源期限的例外均已交接给测试代理。除本次确认的扫描复查样本及该例外，测试代理的清单沿用上表。合并后的识别准入与七步沿用子任务 5，迁移约束接续 0102。固定样本数量及耗时差值留到合入后实测。

### 合并前已运行的验证

下列 Go 筛选从 `backend` 运行，使用 `mise exec -- go test <包> -run '<筛选>'`。本次结果均通过。

| 包 | 筛选 |
| --- | --- |
| `./internal/sources` | `^Test(LimiterPriority\|LimiterWaitDeadlineAndCancellation\|RequesterOutboundProxyBackoff)$` |
| `./internal/sources/musicbrainz` | `^Test(RetrySchedule\|RetryNetwork\|CacheDirectoryAndInvalidResponse\|SharedOutboundInterval)$` |
| `./internal/sources/acoustid` | `^TestLookupFingerprint(Errors\|Backoff\|RequestMappingAndCache)$` |
| `./internal/curation/program` | `^Test(FirstHitStopsLookup\|LookupBounds\|B3MatchWithoutFingerprint)$` |
| `./internal/curation/program` | `^TestB3Fingerprint(AlbumRules\|SingleRules\|ReachAndFallback\|NeedsWork)$` |
| `./internal/app` | `^TestPackageDependencyDirections$` |

数据库使用测试脚本的新项目与独立库；命令为 `ROOMUSIC_TEST_RUN='<筛选>' mise exec -- ./scripts/test-integration.sh`，通过 `sg docker` 运行。下列筛选都执行到了目标用例并通过。

- `^TestPostgreSQLMigrationRollbackRoundTrip$/^[0-9]+_curation_query_retry[.]sql$`（实际执行 0098 的子用例）
- `^TestPostgreSQL整理重启复用响应及已提交写入$`
- `^TestPostgreSQL(整理队列限流时歌词和艺人资料继续|CurationGapAndIdentityChanges)$`
- `^TestPostgreSQLAPIResponseSamples$`
- `^TestPostgreSQL整理队列五档取项与模型查询门槛$`
- `^TestPostgreSQLB3AcousticFingerprintCache$`
- `^TestPostgreSQL(CurationProgramGates|B3FingerprintGatePauseAndScan)$/^scan(_during_lookup|_running)?$`（三个扫描子用例）

`git diff --check` 通过。单独的 `^TestLimiterPriority$` 由上面的来源测试组覆盖。

曾失败：API 样本用例发现诊断样本迁移版本和进度样本规则版本落后；更新两处后通过。声纹单曲用例发现 AcoustID 旧录音编号与 MusicBrainz 正式编号可以不同；保留正式编号处理后通过。

未运行：上表“测试代理补充清单”的新增场景，以及合入后子任务 1 的固定样本基线对照，当前无认对、认错、请求数和用时差值。


## 与规则 105 的合并记录

| 冲突文件 | 合入结果 |
| --- | --- |
| `.trellis/spec/backend/curation-contracts.md` | 第 15、23、25 节使用 0102 的结论、0103 的失败状态及同源竞争顺序。 |
| `backend/internal/app/testdata/api-samples/curation/progress.json` | 进度的发行计数合并 matched、unmatched、conflict、try_later、query_error，rule_version 为 105。 |
| `backend/internal/app/testdata/api-samples/diagnostics/admin.json` | migration_version 为 103。 |
| `backend/internal/curation/program/match_lookup.go` | 编号命中即判、散曲多录音标签判冲突。适用步骤独立标记，查无记录才算空。 |
| `backend/internal/curation/program/match_search.go` | 按七步分别记录步前页摘要与断点。第六步暂停时等待，重新入步时检查开关，只有到达第七步才搜索名字。 |
| `backend/internal/curation/program/pass.go` | 按当前识别步骤保存等待与专辑错误，提交时核对物理文件摘要与最新来源期限，保留手动重查的范围。有效结论、结论次数和代次沿用规则 105。 |
| `backend/internal/curation/program/queue_projection.go` | 准入与识别共用。前五步没有标识才提前登记声纹，文件或规则变化清断点，按可见和稳定条件领取。 |
| `backend/internal/curation/program/queue_query.go` | 领取沿用 0102 的可识别发行视图及文件摘要函数。发行和它的声纹共享排名，未来等待只进剩余数，到期的普通未匹配可重新领取。 |
| `backend/internal/curation/program/release_route.go` | 保留已对应发行或单曲的定向核对，重试沿用同一编号。第七步的普通核对失败记未匹配，已知不同组保留冲突。 |
| `backend/internal/curation/progress.go` | 从同一份可见发行统计读取三个结论、等待和查询出错。 |
| `backend/internal/curation/work_states.go` | 同时读取 file_digest、断点来源和未匹配到期时间。 |
| `backend/internal/platform/postgres/migrations_rollback_integration_test.go` | 保留 102 的夹具，103 验证等待和查询出错的整行回退。 |

`backend/migrations/0103_curation_query_retry.sql` 在 0102 的取值集合上增加 query_error。反向迁移保留来源结论，对发行的等待或查询出错行删除断点、恢复到 0102 可读的等待状态。配套调整包括 `backend/internal/curation/program/{fingerprint,queue_fingerprint}.go` 的声纹查询标记、`queue_progress.go` 的 90 天到期时间和 `unit.go` 的当前步骤。`backend/internal/app/curation_program_worker_integration_test.go` 的既有 TestPostgreSQLCurationProgramRetry 调整等待断言，检查 try_later、MusicBrainz 和期限，同时检查结论次数、失败次数与代次为零。

### 合并后的验证结果

以下 Go 命令从 `backend` 运行。编译、静态检查和以下 13 个单元用例通过。

```sh
mise exec -- go build ./internal/app ./internal/platform/postgres ./internal/curation/... ./internal/sources/...
mise exec -- go vet ./internal/app ./internal/platform/postgres ./internal/curation/... ./internal/sources/...
mise exec -- go test ./internal/app ./internal/platform/postgres ./internal/curation/... ./internal/sources/... -run '^(TestPackageDependencyDirections|TestEmbeddedMigrationRollbackFiles|TestProgramRuleUpgradeStatePolicy|TestProgramUnmatchedRecheckSchedule|TestB3FingerprintAlbumRules|TestB3FingerprintReachAndFallback|TestProgramIdentifierSearchOrder|TestProgramNameSearchOrder|TestProgramSimpleFinderBoundary|TestProgramReleaseAdmission|TestLookupBounds|TestFirstHitStopsLookup|TestProgramPartialAlbumNamesOnlineAndReplay)$'
```

初次 vet 和测试编译发现规则升级用例仍调用原签名的 `searchRelease`。把本轮断点放在工作单元中后，原调用和生产入口均可编译。初次迁移命令的两个数据库子用例已通过，但脚本因上述编译错误退出失败。初次功能批次在发现同一编译错误后中止，采用修正后的二进制重新执行。

单元筛选最初另含 `TestB3FingerprintSingleRules`、`TestB3MatchWithoutFingerprint`、`TestB3FingerprintNeedsWork`，整次返回失败。对合并结果和导出的 `main` 基线 `7af059de` 各运行以下命令，两边的失败子用例名单完全相同。第一处的单曲夹具只有曲名、无曲目艺人，第二处的期望未设置放宽快照标记，第三处的旧工作行只设置 input_digest，规则 105 判断文件变化使用 file_digest。保留基线断言供主代理与子任务 5 核对。

```sh
mise exec -- go test ./internal/curation/program -run '^(TestB3FingerprintSingleRules|TestB3MatchWithoutFingerprint|TestB3FingerprintNeedsWork)$'
```

集成测试均在独占的合成 PostgreSQL 实例运行。迁移 0102、0103 的升降再升均通过。功能批次运行 21 个顶层用例，首次完整批次 17 个通过、4 个失败。单独更新早期 `TestPostgreSQLCurationProgramRetry` 的等待期望后复跑通过。最终结果为 18 个通过，3 个仍包含旧状态断言。

```sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/^010[23]_' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_TEST_RUN='^TestPostgreSQL(APIResponseSamples|B3FingerprintSourceWaitAndSwitch|B3AcoustIDProgram|B3LocalFingerprintRetriesRealAudio|QueueRecoveryRacesFailureCommit|FingerprintProjectionRejectsLateGeneration|FingerprintControlsDoNotCountAlbumFailure|QueueProgressMixesReadyWaitingAndQueryError|整理队列五档取项与模型查询门槛|整理队列限流时歌词和艺人资料继续|整理重启复用响应及已提交写入|整理重启后界面成功仍按原期限探测|ProgramMatchResultsAPI|ProgramMatchFailedLookupKeepsLastSuccess|ProgramRuleUpgradeUsesExistingID|ProgramFileDigestChanges|ProgramPeriodicRecheckCallsAcoustID|ProgramAdmissionPreservesLyrics|整理同名跨组首查停止|CurationProgramRetry|整理名字核对按新依据重排一次)$' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationProgramRetry$' mise exec -- ./scripts/test-integration.sh
```

- `TestPostgreSQLB3AcoustIDProgram` 的两种无效密钥均通过，各一次 AcoustID 查询、一次名字查询；`TestPostgreSQLB3FingerprintSourceWaitAndSwitch` 的 AcoustID 恢复和 MusicBrainz 详情恢复两个子用例均通过。AcoustID 等待前名字查询为零，等待期关闭后识别记录 unavailable。
- 测试代理第一阶段共有四个旧结论断言仍失败。`backend/internal/app/curation_b3_query_wait_integration_test.go` 的“已关闭”和“等待期间关闭”，以及 `backend/internal/curation/program/queue_fingerprint_integration_test.go` 的旧代结果晚到和“计算途中停用来源”，均期望 unlisted，实际为 unmatched。旧代晚到样本实测共四次计算，新一代完成一次识别。测试代理的用例保留原状。
- 进度样本、混合就绪与等待、排队优先级、重启续查、已有来源结论保留、90 天重查、文件摘要变化和等待提交竞态均通过。`git ls-files -u` 为空，`git diff --check` 和 `git diff --cached --check` 通过。

第二阶段的测试清单增加以上四处旧状态断言对齐，保留禁用时 unavailable、等待期间零名字请求和密钥无效时一次名字请求的断言。按最终七步验证跨步骤跳过、步骤内页或详情缓存、同源请求优先级，以及提前查声纹与 MusicBrainz 出站的实际重叠。固定样本耗时差值和第二阶段新增用例本次未运行。
