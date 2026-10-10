# Classical M2b：ffmpeg 无损派生与 ALAC 播放

## 目标与用户价值

让 M2a 放不了的 ALAC 曲目（真实库 84 个，约占音频文件的 30%）在所有约定浏览器都能播放：服务端用 ffmpeg 生成经过校验的无损 FLAC 派生文件，
经过与直出相同的鉴权和来源核对后交给浏览器，界面如实说明“服务端做了无损转换”。
技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)。

## 依据

- `docs/architecture/core-1-roadmap.md` 阶段表 M2b 行：ffmpeg 无损派生缓存、CUE 精确分轨、歌词；前置为 M2a 与 ADR 0004 第 2–7 项（含许可与部署）获批；
  验收出口“ALAC 与 CUE 曲目在约定浏览器可播放；缓存有上限、可取消”。路线要求创建子任务时按最小可验收能力拆开。
- `docs/architecture/adr/0004-media-delivery.md`：推荐“直出优先，必要时生成完整派生缓存”（§2）；派生缓存合同（§6）；进程、部署与许可门禁（§7）；
  无损派生的验收对象是解码后的 PCM（§8）；M2b 须先批准质量策略、配额/撤销规则及部署/许可门禁（§9）；待批准决策第 2–7 项（§11）。
- M2a 的[播放合同](../../spec/backend/playback-contracts.md)与[播放器设计规范](../../spec/frontend/player-design-guidelines.md)：格式表、预检、原文件音频、
  10 秒复核、信号路径。前端解码器已为派生表示预留扩展点（`frontend/src/features/player/api/decoders.ts:22`）。
- 2026-10-09 用户裁决：下一阶段做 M2b；M2a 遗留的三组手工实播延后；M2b 范围为“ALAC 派生 + 歌词，CUE 精确分轨延后”（见“规划决策记录”）。

## 已核实事实

调研见 [research/library-evidence.md](research/library-evidence.md)（真实库只读汇总与运行环境）。

- 真实库 84 个 `.m4a` 全部是 ALAC（24 bit/48 kHz 73 个、16 bit/44.1 kHz 10 个、24 bit/44.1 kHz 1 个），合计 3127 MiB；
  M2a 收尾时 ALAC 移出直出格式表（`13a9cfe`），所有浏览器标注“这种格式目前不能直接播放”。
- 真实库扫描后没有 `cue_virtual` 曲目：8 份 CUE 都是逐轨抄本，相关专辑以物理 FLAC 入库，M2a 已能播放。
- 宿主机没有 ffmpeg；生产由 `scripts/prod.sh` 在宿主机运行 Go，项目不发布镜像或二进制。`scripts/gen-playback-fixtures.sh` 已按摘要固定
  `mwader/static-ffmpeg` 7.1.1（静态构建，可直接在宿主机运行），只用于生成素材。
- 用固定的 ffmpeg 7.1.1 实测（2026-10-09，本机 12 核虚拟机，单线程）：M2a 的 ALAC 素材转成 FLAC 后位深（16→16、24→24）、采样率、声道不变，
  解码后的 PCM（按 32 位整数取 MD5）与源一致；5 分钟的 24 bit/48 kHz 合成 ALAC（45.8 MiB）转换加源 PCM 摘要 0.78 秒，输出（32.0 MiB）
  解码复核 0.24 秒，内存约 35 MB。
- HTTP 服务只设置 `ReadHeaderTimeout`（`backend/cmd/roomusic/main.go:54`），没有写超时，预检可以长轮询。
- `playback` 只能依赖 `catalog`、`library`（`backend/internal/app/dependency_rules_test.go:36`）；`catalog.TrackSource` 不含位深、采样率与声道
  （`backend/internal/catalog/track_source.go:18`）；内容指纹按扩展名计算，现有入口只接受路径（`backend/internal/library/parser/fingerprint.go:37`）。

## 范围

做：

- ffmpeg 运行时适配：定位可执行文件、启动自检、固定参数的两遍转换（转换并摘要源 PCM、解码输出再摘要），进程组回收。
- 受管派生缓存：按内容版本建键、原子发布、配额与最近使用淘汰、启动清理与重建索引。
- 派生任务：同键合并、等待者登记、无人等待后取消、并发/排队/单用户上限、超时与输出上限、失败短期记忆。
- 接口：格式表增加 `delivery`；预检启动或加入转换并长轮询；音频接口提供已完成的派生文件；新增错误码。
- 前端：派生格式的能力判定、“正在准备”状态、预取下一首、信号路径与错误文案。
- 真实服务自动化覆盖 ALAC 两组参数；CI 与本地测试使用同一个固定的 ffmpeg。
- 规范、ADR 0004 决策记录、路线与部署说明。

不做：

- CUE 精确分轨（延后，见规划决策记录）、歌词（另立子任务 `classical-m2b-lyrics`）、服务端播放历史（P）。
- 有损转码、码率档位、实时流式转码、扫描后预生成全部派生、多实例共享缓存。
- Safari 上直出 ALAC 原文件（派生后所有浏览器都播放 FLAC）。
- 分发 ffmpeg 二进制或包含 ffmpeg 的镜像。

## 需求与验收

| 编号 | 需求 | 验收 |
| --- | --- | --- |
| B1 | 派生能力探测 | 有可用 ffmpeg 时，格式表列出 `m4a/alac`，`delivery` 为 `derived`、媒体类型 `audio/flac`；找不到 ffmpeg 或自检失败时不列出 ALAC，启动日志记录一条 `playback.derivation.unavailable` 及原因类别；已有直出格式不变，`delivery` 为 `original` |
| B2 | 无损派生正确 | ALAC 16 bit/44.1 kHz 与 24 bit/48 kHz 素材转成 FLAC 后位深、采样率、声道与扫描记录一致，解码后 PCM 摘要与源相同才发布；发布是原子的，失败、取消、超出上限时不留下可命中的文件 |
| B3 | 预检启动与等待 | 缓存未命中时预检启动或加入转换，最多等待 20 秒：完成返回 200 与派生表示，未完成返回 202 `preparing`；同键并发预检只运行一次 ffmpeg；HEAD/GET 音频从不启动转换，未命中返回 409 `derivation_not_ready` |
| B4 | 派生文件传输 | 检查顺序与 M2a 相同（鉴权 → 标识 → 曲目 → 目录 → 格式 → 源文件核对 → 缓存 → Range）；200/206/416、强 ETag（与原文件不同）、`audio/flac`、`private, no-store`；在途传输每 10 秒复核；来源缺失、变化或目录停用后即使缓存仍在也拒绝 |
| B5 | 内容版本绑定 | 缓存键包含内容指纹、大小、修改时间、ffmpeg 版本与转换策略版本；转换前用已打开的句柄重算内容指纹，与扫描记录不符返回 `source_changed` 且不发布 |
| B6 | 上限与取消 | 同时转换 2 个、排队 16 个、每用户同时等待 3 个，超出返回 503 `derivation_busy` 与 `Retry-After`；最后一个等待者离开 10 秒后取消转换，ffmpeg 进程组被回收、临时文件删除；单次转换 10 分钟、输出不超过源文件 2 倍加 16 MiB；剩余空间低于 1 GiB 时不启动新转换；关停时有限时间内结束全部转换 |
| B7 | 缓存管理 | 默认上限 10 GiB（`ROOMUSIC_MEDIA_CACHE_MAX_MIB` 可调），超出时按最近使用淘汰；启动时删除临时文件、重建索引；缓存目录位于任一允许的音乐根之内（或包含音乐根）时拒绝启动 |
| B8 | 前端播放体验 | Chromium 中 ALAC 曲目判定可播放（`canPlayType("audio/flac")`）；转换期间播放栏与正在播放页显示“正在准备”，切歌或停止即放弃等待；当前曲开始播放后预取下一首派生曲目（只预取 1 首）；信号路径“服务端”一栏写明无损转换与 PCM 核对；转换失败、服务器忙有明确文案 |
| B9 | 真实服务自动化 | `scripts/playback-e2e.sh` 用固定 ffmpeg 覆盖 ALAC 两组参数的派生播放、拖动与自然结束，原有 13 项仍通过；CI 的集成与真实播放作业使用同一个 ffmpeg |
| B10 | 文档与决策记录 | ADR 0004 记录第 2、4、6 项及第 5、7 项派生部分的裁决；新增派生合同规范，更新播放合同、前端规范、DESIGN.md、README、`.env.example`、路线图、父任务与后续验收任务 |
| B11 | 验证 | Go 单元与 `-race` 测试；隔离 PostgreSQL 与固定 ffmpeg 上的集成测试和接口样本；前端 lint、typecheck、Vitest、Playwright；重建的内嵌产物与源码一致 |

## 待批准决策（推荐方案）

| 编号 | 决策 | 推荐 | 不同选择的代价 |
| --- | --- | --- | --- |
| K1 | 质量策略（ADR 0004 第 2 项） | 只做无损派生：ALAC → FLAC，不重采样、不抖动、不改增益；PCM 核对一致才发布；不做有损回退、码率档位或静默降级 | 允许有损回退可省带宽，但局域网自用没有必要，还要多一套有损策略与标注 |
| K2 | 生成时机与缓存（第 4 项） | 第一次播放时按需生成，结果缓存在 `<数据目录>/media-cache`，默认上限 10 GiB，按最近使用淘汰；单实例；限额按 B6 | 扫描后预生成全部（真实库约 3 GiB、约 1.5 分钟 CPU）可免去首播等待，但每次扫描都要后台转换，大库代价高 |
| K3 | 内容版本证明（第 4 项） | 转换前用已打开的句柄重算内容指纹，与扫描记录一致才转换；命中缓存时只核对大小与修改时间（与 M2a 相同） | 只核对大小与修改时间更省一次读盘（单曲约 35 MB），但派生文件可能固化“保留大小与修改时间的原地改写” |
| K4 | 撤销（第 5 项派生部分） | 每个请求照常鉴权；最后一个等待者离开 10 秒后取消转换；派生文件的传输沿用 10 秒复核；目录停用、来源缺失或变化后不再提供缓存 | 不取消无人等待的转换实现更简单，但切歌频繁时会堆积无用转换 |
| K5 | ffmpeg 分发与许可（第 6 项） | 项目不分发 ffmpeg，由部署者安装；`ROOMUSIC_FFMPEG_PATH` 指定，未设置时在 `PATH` 中查找；启动时用内置的一小段 ALAC 跑完整转换与核对，失败就关闭派生。推荐的安装方式是新脚本取出按摘要固定的静态 ffmpeg 7.1.1，与测试、CI 一致；也可用发行版的包。ALAC（Apache 2.0）与 FLAC 都免专利费；以后若发布内置 ffmpeg 的镜像，另做许可审核 | 随项目分发 ffmpeg 要承担 GPL 构建的源码与声明义务 |
| K6 | 派生 API（第 7 项） | 沿用现有三个接口（B1、B3、B4），新增错误码 `derivation_not_ready`、`derivation_failed`、`derivation_busy`；不持久化任务状态、不加迁移 | 另设任务接口（创建/查询/取消）更显式，但要多一套资源与状态持久化 |
| K7 | 预取 | 当前曲开始播放后预取下一首派生曲目，只预取 1 首 | 不预取时每首 ALAC 开播前约等 1 秒；预取更多会浪费转换 |
| K8 | 后续验收任务 F2 | 改为“由 M2b 派生取代”，不再要求 Safari 直出 ALAC；F1（三组手工实播）与 F3（远端 CI）不变 | 保留 F2 只能让 Safari 省一次转换 |

## 风险

- 自检素材、合成样本不能代表所有真实 ALAC（例如非常规声道布局）：转换或核对失败时返回 `derivation_failed` 并短期记住，不会反复重试；真实库的验证由用户在装好 ffmpeg 后重扫并播放确认。
- 首播需要等待一次转换（5 分钟曲目约 1 秒）；极长曲目可能超过 20 秒长轮询，前端会继续等待直到完成或超过 10 分钟上限。
- 用户自己的实例要先装 ffmpeg（宿主机目前没有），否则 ALAC 仍标“不能直接播放”。

## 规划决策记录

| 日期 | 路线 | 选择者 | 结果 | 理由 |
| --- | --- | --- | --- | --- |
| 2026-10-09 | 人工选择 | qlqq | 下一阶段做 M2b，手工实播延后 | 急需推进开发；M2b 按 D1 顺序紧接 M2a |
| 2026-10-09 | 人工选择 | qlqq | M2b 范围：ALAC 派生 + 歌词两个子任务，CUE 精确分轨延后 | 真实库没有 CUE 虚拟轨，ALAC 全部不能播放，134 个文件内嵌歌词 |
| 2026-10-09 | 人工选择 | qlqq | 批准 K1–K8 与本规划，均按推荐方案执行；歌词子任务在本任务归档后启动 | 用户阅读最终规划摘要（目标、范围、决策、验收、风险与执行顺序）后回复“批准执行” |

## 实施结果（2026-10-10）

提交（均在 `main`，未推送）：`ef306f7` 无损派生缓存与 ffmpeg 适配；`5f184ef` ALAC 经无损派生播放（接口、配置、前端、内嵌产物）；
`5ac806b` 真实服务覆盖 ALAC 派生播放；`8e7657d` 派生合同、决策与部署说明。真实库核对见 [research/real-library-derivation-check.md](research/real-library-derivation-check.md)。

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| B1 | 达成 | `TestPostgreSQLPlaybackFormats…`（含与不含派生）、`TestOpenDerivationReportsAnUnusableFFmpeg…`（日志只含 `reason`）、接口样本 `playback/formats` |
| B2 | 达成 | `TestFFmpeg…`（两组素材的 PCM 核对、损坏输入、超限、不覆盖、句柄不受路径替换影响）；`TestPostgreSQLPlaybackDerivesALACLosslesslyWithFFmpeg`（STREAMINFO 与 PCM 逐位相同）；真实库 84/84 成功 |
| B3 | 达成 | `TestPostgreSQLPlaybackPreflightWaitsForTheDerivationAndAudioServesTheCachedFile`（202 → 200、音频未就绪 409、音频不启动转换）；`TestConcurrentWaitersShareOneConversion`；真实服务探针用例 |
| B4 | 达成 | 同上加 `TestPostgreSQLPlaybackChecksPrecedeAnyConversion`、`…DerivedStreamIsCutWhenTheRootIsDisabled`；派生 ETag 与原文件不同；Range、If-Range、416 与 304 针对派生文件 |
| B5 | 达成 | `TestDeriveKey…`、`…RejectsASourceRewrittenInPlaceBeforeConverting`、`TestVerifyHandle`、`TestContentIsVerifiedBeforeConversionAndSizeAfterwards` |
| B6 | 达成 | `TestRunningQueuedAndPerUserLimits`、`TestQueuedJobsStartInFirstInFirstOutOrder`、`TestJobIsCanceledWhenNoWaiterReturnsWithinTheGracePeriod`、`TestRunFFmpegKillsTheWholeProcessGroupOnCancel`、`TestJobTimeoutIsAFailure`、`TestFreeSpaceAndReservations`、`TestCloseCancelsRunningAndQueuedJobs`、`…PreflightWaitEndsWhenTheApplicationShutsDown` |
| B7 | 达成 | 缓存测试（索引重建、淘汰、不删刚发布的条目）、`config_test.go`、`TestOpenDerivationRefusesACacheDirectoryThatOverlapsALibraryRoot`（含符号链接，且拒绝发生在创建目录之前） |
| B8 | 达成 | Vitest（引擎正在准备、预取、忙与失败，能力判定，界面，provider 默认装配，解码器与样本契约）；Playwright 的 ALAC 用例与五种宽度；真实 Chromium 解码 `audio/flac` |
| B9 | 本机达成，远端未验证 | `./scripts/playback-e2e.sh` 16 项通过（原有 13 项加 ALAC 三项）；CI 的集成与真实播放作业都经 `scripts/ffmpeg-pinned.sh` 取得同一个 ffmpeg，待下次推送确认 |
| B10 | 达成 | 新增 `playback-derivation-contracts.md`；更新播放合同、后端索引与目录、运行合同、前端三份规范、DESIGN.md、ADR 0004、路线图、父任务、后续验收任务、README 与 `.env.example` |
| B11 | 达成 | 隔离 PostgreSQL 与固定 ffmpeg 上 `go test -race ./internal/playback/... ./internal/app/... ./internal/platform/... ./internal/catalog/... ./internal/library/...` 通过；Vitest 1011 项、Playwright 166 项、真实服务 16 项通过；无环境变量的重新构建与内嵌产物逐文件一致；提交前在导出的暂存树上重复构建与测试 |

### 与设计的差异

实施中有几处与 [design.md](design.md) 不同，已在其第 11 节记录原因：随机模式不预取；连续两次“正在准备”预检之间至少间隔 1 秒；
准备结束后状态回到加载；信号路径的交付方式取自能力判定表而不是引擎快照；缓存上限的默认值与下限只放在配置层；
预检因应用关停而结束时以 503 `derivation_busy` 应答。真实服务自动化还发现：播放 formats 专辑时会预取下一首 ALAC，
所以“音频请求从不启动转换”的用例改用独立目录的探针文件。

### 仍需用户完成

- 宿主机装好 ffmpeg（`./scripts/ffmpeg-pinned.sh`，或发行版的包），设置 `ROOMUSIC_FFMPEG_PATH`（或放进 `PATH`），重启服务，播放几首 ALAC 确认
  听感与进度（真实库 84 个 ALAC 的转换与 PCM 核对已通过，见调研）。没有 ffmpeg 时 ALAC 仍标注“这种格式目前不能直接播放”，应用照常运行。
- 三组手工实播（F1，ALAC 两行经派生播放）与下次推送后的远端 CI（F3），见后续验收任务。
