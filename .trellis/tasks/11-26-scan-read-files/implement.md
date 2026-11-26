# 扫描读文件实施顺序

从仓库根运行以下命令。Go 用项目 mise 版本，单测在 backend 模块下执行；集成命令由脚本创建独立项目和临时 PostgreSQL。以下是实施时的命令，本次设计未运行。实现代理改生产代码和规范；现有断言的预期调整、合成文件和新增用例均交测试代理。每步列出的已有用例可先跑，不用尚未存在的测试名称取得绿色结果。

## 1. 建立存储与观察合同

- 新增 `backend/internal/catalog/metadata/scan_tags.go`，在 `backend/internal/library/parser/parser.go`、`backend/internal/catalog/ingest/observation.go` 加文件事实快照和实际格式。在 `backend/internal/library/scanner.go` 的两个构造函数透传。改 `backend/internal/catalog/ingest/persist_track.go` 的 INSERT 和 UPDATE，同一候选事务整体替换快照。
- 新增 `backend/migrations/NNNN_scan_file_tags.sql`、`backend/migrations/NNNN_scan_file_tags.down.sql`，结构按 design.md。当前最大号 0097，**合入时按当时最大号顺延**，上下行同步改名。资格视图的排除条件只匹配现存成员，普通音乐仍可见。复用现有按曲目归属 media 查询，不增加按标签搜索的索引。
- 风险在于写入占位参数错位、SQL NULL 与显式 false/0 混同、虚拟轨优先级和候选签名遗漏。首先对照迁移和旧样本。

```sh
(cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')
(cd backend && mise exec -- go test ./internal/catalog/ingest ./internal/library -run '^(TestCandidateMetadataSignatureIdentifiers|TestArtistEvidencePhysicalObservationAndStagingJSONRoundTrip|TestBuildCueVirtualObservationClonesMutableProvenancePerTrack)$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/.*scan_file_tags' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLCandidatePersistenceIsIdempotentAndReplacesCurrentEvidence$' mise exec -- ./scripts/test-integration.sh
```

## 2. 将探测、缓存与文件读取接到真实格式

- 新增 `backend/internal/library/parser/format.go`，改同包 `parser.go`、`fingerprint.go`、`version.go`。先落实 design.md 末尾的独立 ID3 决定，再复用容器头判断与各格式解析器。用已打开对象探测指纹类型。保留既有格式版本表，在此步暂不升 CommonVersion。
- 改 `backend/internal/library/{scan_filter,scan_progress,scan_pipeline,scanner,parse_cache}.go`。共享预筛，处理无后缀、错后缀和仅诊断的音频后缀，CUE 父音频走同一过滤。区分未知格式和实际读错。物理、CUE 及延迟封面的版本均来自解析结果；缓存命中用记录内的真实格式，不为命中另开文件。风险是 M4A 与 FLAC 同路径改名后用错版本、Ogg/Opus 误派、带 ID3 的 APE 误判、文件名清理错误地剥掉不存在的后缀。
- 现有把任意坏内容都当 parse_failure 的样本由测试代理拆成“完全不认识”和“已识别后损坏”两类，保住和 missing 断言保留。

```sh
(cd backend && mise exec -- go test ./internal/library/parser -run '^(TestAudioParsersReturnMetadataAndRejectInvalidInput|TestAPEID3Prefix|TestContentFingerprintOfMatchesTheFileVariant|TestContentFingerprintRejectsUnsupportedAndDamagedFiles|TestDSDFingerprintIgnoresTagsAndDetectsAudioChanges)$')
(cd backend && mise exec -- go test ./internal/library -run '^TestScannerOnlyDiagnosesKnownUnsupportedAudioExtensions$')
ROOMUSIC_TEST_RUN='^(TestPostgreSQLIncrementalParseCacheCueWithoutArtworkDoesNotOpenParents|TestPostgreSQLMovedFileKeepsTrackIdentity)$' mise exec -- ./scripts/test-integration.sh
```

## 3. 在文件读完时判定是否接收结果

- 改 `backend/internal/library/{scan_pipeline,scanner,parse_cache,identity_plan}.go` 与 `backend/internal/catalog/ingest/{identity_plan,held_sources,staging}.go`。用设计中的读前/读后 Stat 包住未命中时的解析、CUE sheet 和按需再次读盘。将“读中变化”作为可跳过的独立结果传给按序 apply，先登记保住、再做 missing 对账。若同根稍早的物理或 CUE 槽位已暂存了同一变化来源，在身份规划前使这些记录失效并保住对应曲目。晚发现的指纹变化也走同一处理；不可签名或绑定新封面。
- 风险是同一 CUE 父文件被不同槽位重复消费、缓存先于观察写入，以及新文件读一半时走到创建曲目。保住来源的计数按物理文件或 sheet 去重，普通失败、取消与完整性沿用现有语义。生产 Fingerprinter 与测试替身同步适配状态核对。
- 在已有保住、缓存与并发门禁之外，测试代理的“同大小改 mtime”用例是此步的实际验收条件。

```sh
ROOMUSIC_TEST_RUN='^(TestPostgreSQLScanLifecycleHeldFilesStillReconcilesDeletedSources|TestPostgreSQLScanLifecycleHeldUnsafeCueKeepsVirtualTracks|TestPostgreSQLConcurrentScanMatchesSerialStagingDiagnosticsAndCache|TestPostgreSQLFingerprintFailureDefersScope)$' mise exec -- ./scripts/test-integration.sh
```

## 4. 解析新增标签、流派编号与媒体标志

- 新增 `backend/internal/library/parser/scan_tags.go`，改同包 `tags.go`、`tag_finalization.go`、`mp3.go`、`m4a.go`、`lyrics.go`、`id3_legacy.go`。按 design.md 字段表实现公共键规则；格式层只做 ID3 帧、iTunes atom/freeform、Vorbis/APE 键到公共字段的转换。复用 ID3v1 流派表、ID3 文本编码和现有歌词上限。完整日期与现有年份分别解析。
- 风险是解析器在收集事实前就对空值或长文本提前返回，或用 map 顺序决定字段优先级。尤其要覆盖等于 75%、首个 TCON 精确大小写、MP4 原子不是字符串、COMM 有描述、格式专用回调没有进入通用 setter。

```sh
(cd backend && mise exec -- go test ./internal/library/parser -run '^(TestID3GenresReadNullSeparatedTCONValues|TestFLACParserReadsAllCommentsAfterLongValue|TestAPEv2Tags|TestOggAndOpusParsersReadVorbisLyrics|TestM4AParserReadsLyrics|TestMP3ParserPicksTheUSLTFrameWithAnEmptyDescriptor)$')
ROOMUSIC_TEST_RUN='^(TestPostgreSQLScanStoresUpdatesAndClearsEmbeddedLyrics|TestPostgreSQLCueVirtualTracksHaveNoLyricsButTranscriptTracksKeepThem)$' mise exec -- ./scripts/test-integration.sh
```

## 5. 收紧拆名、古典及文件名规则

- 改 `backend/internal/catalog/metadata/{artist,genre}.go`、`backend/internal/catalog/ingest/filename_numbers.go`。复用显式分隔器，删除紧凑标点和名单特判，输出仍做现有清理和去重。文件名只删猜艺人的尾段；曲号模式的目录证据、格式词过滤及固定目录名识别继续使用原实现。
- 风险是把原始署名证据也拆掉，或把真实标签和歌词署名截断。先与测试代理按 PRD 更新含旧期望的表项（紧凑斜杠、逗号、多人 &、文件名猜艺人），保留其他对照。

```sh
(cd backend && mise exec -- go test ./internal/catalog/metadata -run '^(TestSplitArtistNamesFlattensConservativeSeparators|TestSplitArtistNamesHandlesMultivalueAndSurnameRules|TestCanonicalTrackArtistUsesStableDisplayOrder|TestJoinArtistValues|TestGenreKey)$')
(cd backend && mise exec -- go test ./internal/catalog/ingest -run '^(TestFilenameTitleAndArtistCleaningTable|TestFilenameTitleCleaningDoesNotReplaceTaggedArtist|TestFilenameEvidenceUsesPhysicalDirectoryAndSkipsCue)$')
```

## 6. 迁移旧编码写入到扫描候选

- 改 `backend/internal/catalog/metadata/legacy_encoding.go`、`backend/internal/library/parser/{id3_legacy,mp3}.go`，补齐原字节与编码身份。由 `backend/internal/library/scanner.go` 清除被 CUE 覆盖字段的旧编码来源。新增 `backend/internal/catalog/ingest/legacy_encoding.go`，在 `organizer.go` 已定锚点和成员、尚未生成字段决定处接一个调用。与子任务 3 合并时只对齐该调用位置；其桶、锚点和排序逻辑由 3 维护。更新 `observation.go` 的字段来源及证据写入。
- 从 `backend/internal/curation/program/queue_projection.go` 删唯一解码写入循环，删 `backend/internal/curation/local_legacy.go` 及 `writer.go` 的 legacy 专用分支。核对 `backend/internal/catalog/curation_{evidence,legacy}.go` 与 `backend/internal/curation/local_search.go` 对 garbled 的投影：只对已有有效的人工/自动值使用相应来源，未决字段不作为查询文字。保留旧自动结果的正常失效和撤销；不在本步实施模型选择编码。
- 风险是用文件夹代替候选导致同目录串专辑，或拿“解出的文字个数”代替“共同可用的编码个数”。专辑取交集应在每次归组观察上运行，含命中缓存的老成员。

```sh
(cd backend && mise exec -- go test ./internal/catalog/metadata -run '^(TestLegacyDecodingsPreservesWesternText|TestLegacyDecodingsKeepsGBKCandidate|TestFieldAuthenticity)$')
(cd backend && mise exec -- go test ./internal/catalog/ingest ./internal/library -run '^(TestOrganizeAlbumArtistKeepsAuthoritativeProvenance|TestCueArtistEvidenceUsesEffectiveSourcesAndIsolatesTracks)$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationLegacyReleaseBaseline$' mise exec -- ./scripts/test-integration.sh
```

## 7. 统一过滤非音乐发行的队列入口

- `backend/internal/catalog/curation_evidence.go` 提供整理专用资格集合；`backend/internal/curation/program/{queue_projection,queue_query,queue_artists,queue_recovery,model_queue,worker}.go` 在投影、点刷、声纹恢复、优先级、在途复核与计数共用。模型候选/领取在 `backend/internal/curation/{model_queue,pending_items}.go` 复用同一发行集合。核对 `backend/internal/curation/{progress,unlisted}.go` 的整理专用可见 CTE，避免旧 work_state 计入待办。普通曲库浏览继续用原有 VisibleReleaseIDsSQL。
- 风险是仅过滤新投影，已有 queued、到期 retry 或模型项仍会出队，或把共享艺人的其他音乐发行一起排除。与子任务 5、7 交接资格视图的含义和调用点。

```sh
ROOMUSIC_TEST_RUN='^TestPostgreSQL整理队列五档取项与模型查询门槛$' mise exec -- ./scripts/test-integration.sh
```

## 8. 统一升版、修正规范并检查交付

- 在 `backend/internal/library/parser/version.go` 把 CommonVersion 从 5 升至 6；`parser/version_test.go` 的现有 M4A 精确断言由测试代理同步调整。改 `.trellis/spec/backend/{library-scan-pipeline,library-scan-incremental,library-scan-tags,library-scan-contracts}.md` 中 design.md 点明的段落。避免覆盖子任务 3 的 GroupingVersion 条款及子任务 4 的调度条款。
- 对照测试代理交付的下表运行专项门禁，复用各步已通过结果。确认数据库真实版本和迁移上下行文件名连续。合入前单独跑依赖方向门禁。

```sh
(cd backend && mise exec -- go test ./internal/library/parser -run '^(TestCacheVersionKnownExtensions|TestM4ACacheVersion|TestCacheVersionUnknownExtensions)$')
ROOMUSIC_TEST_RUN='^(TestPostgreSQLParseCacheFormatVersionUpgrade|TestPostgreSQLIncrementalParseCacheSecondScanPreservesIdentityAndReport)$' mise exec -- ./scripts/test-integration.sh
(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
git diff --check
```

子任务 1 合入后，按其交付的固定样本命令分别跑“程序”和“程序＋脚本模型”，复用对应的 `.trellis/tasks/11-26-curation-fixed-samples/research/baseline.md` 作基线。记录每种模式的认对数、认错数、逐张请求数和用时（基线、本次、差值），认错须为 0、认对不得下降，请求数与用时不得增加。设计时子任务 1 尚未交付其最终入口，此项在其合入后验收。

## 测试代理补充清单

均用临时合成文件或一次性 PostgreSQL。下列为拟新增的测试名，每个名字下用子用例覆盖表中情形。实现代理不新增测试。

| 所在包与拟新增测试 | 合成样本和断言 |
| --- | --- |
| library，TestPostgreSQLScanReadFileChanged | 在读前和读后之间用同步点改同大小文件的 mtime，不依赖 sleep；旧曲目、编号、标签、封面和来源状态保留，新文件不建轨。覆盖 CUE 文本、物理/父文件两种遍历次序、规划补指纹时变化；正常文件同轮仍落库，下次稳定读仅用新数据。 |
| library/parser，TestScanReadFormat | 各支持格式按真实头；伪 .mp3 的 FLAC、无后缀 M4A、旧仅诊断扩展名装有效音频、Ogg/Opus、带 ID3 的 APE、裸 AAC 对照；空文件、随机字节、识别后的截断和 I/O 失败。按主代理裁决处理独立 ID3 样本。 |
| library，TestPostgreSQLScanReadFormatCache | 路径预筛跳过图片、文本和 CUE 的音频头读取；预计与已处理计数一致；无后缀、错后缀音频二次扫描命中对应真实格式，重命名仍用原指纹延续 UUID；CUE 和延迟内嵌封面同样可读。 |
| library，TestPostgreSQLScanReadFormatCache（未知格式） | 认不出格式命中缓存时不开文件，仍报 unsupported_format、保住来源并登记 seen；文件大小或微秒 mtime 改变后重新判型。 |
| library/parser，TestScanReadTagForms | 依 design.md 字段表逐键成对测试。覆盖 m/n 和单独总数、0 与小于碟号、二进制 trkn/disk、日期精度/坏值/别名顺序、私录兜底被显式假覆盖、主版本首键、四种增益/峰值含显式 0。用两种候选键倒置验证优先级。 |
| library/parser，TestScanReadCommentsLyricsAndMedia | 四类注释容器、同名多值、带描述的 UTF-16 COMM、MP4 自由键。时间戳正好 3/4 与 4/5、空行、混有普通歌词、原生 SYLT 时间单位。TCON 首值、次值、大小写、(17)、越界编号、两个存在性标记同现和“0”值；普通 Rock、©lyr 与旧 USLT 作对照。 |
| library，TestPostgreSQLScanReadTagsSnapshot | 缓存、暂存、曲目 JSON 往返；重扫移除标签、false 与缺键区分；专辑 Year、老歌词显示、整理编号、播放参数均不被旁存标签改变；覆盖 CUE 快照、事务回滚、超缓存预算后仍入库。 |
| catalog/metadata 与 catalog/ingest，TestScanReadNamesAndFallback | 每个显式分隔符含无空格半角分号；姓名内部斜杠、半角逗号、& 两侧多词、姓在前、括号、换行/NUL。古典三中文正例与新古典/古典跨界/印度古典反例，作品标签正例。无标签“01 甲 - 乙 [FLAC]”与有真实艺人、正常年份目录对照，核对字段来源。 |
| library，TestPostgreSQLScanReadAlbumEncoding | 关整理扫描 ID3v1/Latin-1 ID3v2：单字段两解、同张另一轨使交集唯一、全张多解、无交集、西文重音、正常 UTF-8、CUE 覆盖、混放的另一候选；变化一轨时已缓存成员一起重算。验证扫描值、字段来源、未决原字节、整理搜索不发乱码，以及无新的 legacy_encoding 整理操作。 |
| curation/program，TestPostgreSQLScanReadMediaQueue | 音乐、播客、有声书、混合成员、missing 成员、CUE；先入队后补标、去标后恢复，核对分页/点刷/计数/领取、到期重试、模型领取及模拟上游请求为零；普通音乐同批照常处理。 |
| library，TestPostgreSQLScanReadCommonVersionUpgrade | 预置 c5 缓存和现有专辑/曲目、人工修正、收藏及对应，第一次增量扫描全部重读，第二次命中；普通完整专辑结构/编号稳定，允许 PRD 点明的 S5 与古典差异。降级与再升级覆盖新列及视图，旧版本读不到的新文件按旧规则处理。 |

测试代理按表中名字分步运行；数据库用例可按本步名称单选，全部已交付时可一次收齐。

```sh
(cd backend && mise exec -- go test ./internal/library/parser -run '^TestScanRead(Format|TagForms|CommentsLyricsAndMedia)$')
(cd backend && mise exec -- go test ./internal/catalog/metadata ./internal/catalog/ingest -run '^TestScanReadNamesAndFallback$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLScanRead(FileChanged|FormatCache|TagsSnapshot|AlbumEncoding|MediaQueue|CommonVersionUpgrade)$' mise exec -- ./scripts/test-integration.sh
```
