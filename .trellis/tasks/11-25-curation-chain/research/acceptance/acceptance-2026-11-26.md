# 整理链 Z 跨子任务验收（2026-11-26 UTC）

同组多版保护与审查策略 v6 生效后，真实 MusicBrainz 配合同一 `gpt-6.2-sol` 整理、审查，**17/20 张达标，错连、错并、错改均为 0**。上一轮真实模型为 16/20，错连 1、错改 3。新版纠正了《菲主打》，《Ambient》的标题仍未纠正。两次真实模型试用均已排空。[两轮真实模型对照](#版本判定与审查策略-v6-的真实模型复验)

## V1–V18 最终状态

无目录前缀的 Go 集成测试均位于 `backend/internal/app/`。逐项核对了夹具、调用入口和断言。沿用 D 合入时已通过的 178 个整理集成用例、M 度量和 G 的界面覆盖，初验运行新增用例与两轮试用，本次复验只运行新增用例、受影响的既有用例与 oracle 轮。G 至首次试用前没有后端行为改动。V7 修复后单独复验名字核对与一次 oracle。

| 父任务 | 最终状态 | 覆盖文件、用例及断言 |
| --- | --- | --- |
| V1 | 通过 | `curation_match_c_integration_test.go` 的 `TestPostgreSQL整理发行组对应的封面归组和撤销` 使用两张各五首、无外部标识的真实文字标签合成专辑，未配置模型。逐张检查一次发行搜索、`unique_group`、只写组级对应、无识别待定、组封面落库。两张本地发行最终同组且存在程序归组操作。 |
| V2 | 通过 | 同文件的 `TestPostgreSQL整理发行组对应的封面归组和撤销`、`TestPostgreSQL整理两边专辑匹配后用别名合并艺人` 检查普通组级一次、普通版本级两次发行请求，以及新艺人至多一次详情读取。`curation_acceptance_match_integration_test.go` 的 **`TestPostgreSQL整理同名跨组用四个识别请求定案`** 让三个同名跨组候选只有一个时长吻合，断言正确版本落库、无识别待定，HTTP 记录恰好一次搜索和三次不同详情，另计艺人读取。`backend/internal/sources/coverartarchive/client_test.go` 的 `TestCoverArtLimiters` 核对默认封面清单与封面图片共用 CAA 的限速器，独立于 MB，并检查实际调用的限速许可。 |
| V3 | 通过 | `curation_authenticity_integration_test.go` 的 `TestPostgreSQLCurationAuthenticityFromScan` 检查每条外部请求均无两个占位词，推断标题实际参与搜索，时长冲突进待定；`curation_patrol_integration_test.go` 的 `TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序` 检查从数据库读取的模型本地整理值进入搜索证据并标为推断。`backend/internal/curation/program/match_model_inferred_test.go` 的 **`Test模型整理的标题必须通过曲数和时长核对`** 以五首歌、原占位标题、模型整理标题及完整单候选页调用实际匹配入口：结构吻合才定到正确版本；同曲数时长不符、同时长曲数不符分别产生相应冲突且均无对应。 |
| V4 | 通过 | `curation_match_c_integration_test.go` 的 `TestPostgreSQL整理零候选按来源和模型分流` 检查先按原题搜，再放宽，仍未命中时才查艺人；艺人不在库且能联网的直接持久化 `web` 路线，无模型的标为 `unlisted`。`curation_program_worker_integration_test.go` 的 `TestPostgreSQLCurationProgramRelink` 断言撤销导致 `no_usable_candidates` 且无待定。新增 `curation_acceptance_match_integration_test.go` 的 **`TestPostgreSQL整理唯一候选撤销后不调用模型`** 在已实际使用过的模型端点上比较撤销前后用量，两次均排空，所有模型调用与 token 增量均为零，外部对应和识别待定均为零。 |
| V5 | 通过（未配置模型时，待模型符合预期） | `curation_model_review_integration_test.go` 的 `TestPostgreSQL整理审查未采用即结项` 分拒绝、拿不准验证：目标操作为零、模型空闲、没有等待模型/进行中/等待人的项、建议保留原审查和理由。`curation_model_fallback_integration_test.go` 的 `TestPostgreSQL整理迁移旧待办为可采纳建议` 用 0091 下迁复原旧待办、上迁后保留建议并可采纳，等待人为零。`frontend/src/app/curation-settings.test.tsx` 的「状态与原因：开启者是自己时链接 Agent 设置；用量条按上限；最近的会话」精确匹配计数行“等模型 3 · 处理中 2 · 搁置 1”；「判断为不需要改的建议只能不采纳，没有采纳按钮」定位“未采用的建议”表并检查可用操作。 程序轮有 5 项等待模型，等待人工为零。 |
| V6 | 通过 | `curation_match_c_integration_test.go` 的 `TestPostgreSQL整理两边专辑匹配后用别名合并艺人` 从两个独立身份出发，检查两专辑对应、艺人共用 MBID、模型操作为零和程序合并；撤销后两个重定向清除，下一轮合并操作总数仍为一。`curation_patrol_integration_test.go` 的 `TestPostgreSQL整理规范名搜索并合并艺人` 让模型拒绝合并，断言仅程序产生合并操作；分别以两名搜索命中同一 ID，平票显示外部主名、库内票数变化后显示多数名、人工命名后显示人工名。 |
| V7 | 部分通过（受控回归通过，真实模型纠正 1/2 张） | `curation_name_retry_integration_test.go` 的 `TestPostgreSQL整理名字核对按新依据重排一次` 在提案入队后建立发行和逐轨对应，核对新会话的证据，检查获独立审查批准后才写标题、原值可见、撤销可恢复；另覆盖同一输入再次过时才结清。`curation_model_fallback_integration_test.go` 的 `TestPostgreSQL整理专辑名字只纠正有佐证的错字` 覆盖错字与繁简、大小写、标点、外部特殊写法。受控 oracle 曾纠正两张。v6 真实轮按完整外部标题与 14/14 曲结构证据批准了《菲主打》；《Ambient》只有组级对应，名字提案因缺少佐证保留为建议。[v6 字段与审查快照](live-real-responses-v6-evidence.json) |
| V8 | 通过 | `curation_model_fallback_integration_test.go` 的 `TestPostgreSQL整理模型记忆的事实只留建议`、`TestPostgreSQL整理无佐证主张先联网再留建议`、`TestPostgreSQL整理无佐证不合并艺人与发行` 分别对凭记忆的厂牌、无佐证的名字纠正、艺人合并核对建议与零写入，对缺依据的发行合并核对工具拒绝与零合并操作。`curation_release_merge_review_integration_test.go` 的 `TestPostgreSQL整理审查发行并入证据/缺外部对应即结项留建议` 另核对治理建议、人工待办与真实发行并入表。 |
| V9 | 通过（含第一档退避复验） | `curation_queue_recovery_integration_test.go` 的 `TestPostgreSQL整理重启复用响应及已提交写入` 在响应落盘、对应提交两处中断，重建客户端与工作者，最终只有一次 MB 请求、一次对应操作、一次字段操作且批次已关闭。`curation_queue_sources_integration_test.go` 的 `TestPostgreSQL整理队列限流时歌词和艺人资料继续` 在受控 MB 冷却已生效后才提供歌词时长与独立的 Wikidata Q 号，断言 MB 仍暂停且未发新请求，同窗内歌词完成、Wikipedia 简介及 Commons 图片落库、两队列速度为正并排空；随后断言 2 秒/4 秒退避及恢复。 新增 `curation_selection_backoff_integration_test.go` 的 `TestPostgreSQL整理第一档连续限流只推迟缺详情的项`，断言失败项保持路线与失败次数，下次时间等于来源恢复时刻，冷却结束可写入正确发行。 |
| V10 | 通过 | `curation_queue_scan_integration_test.go` 的 `TestPostgreSQL整理进行中新专辑优先` 挂住首张的 HTTP 请求，插入并确认新专辑后放行，断言真实首请求顺序为首张存量、新入库、次张存量，均只处理一次。同文件 `TestPostgreSQL整理安静窗口只发一次确认扫描` 用半截 FLAC 和保留原 mtime 的完整替换文件，断言拷贝中及成员变化后均 ready=0、来源请求为零，各窗口只发一次定时确认，稳定后出站并处理一次。 |
| V11 | 通过 | `curation_auto_layer_integration_test.go` 的 `TestPostgreSQL自动值随标签重扫作废且可撤销` 核对发行和曲目标题、两条作废操作及撤销，测试显式调用整理证据读取。新增 `curation_z_regression_integration_test.go` 的 `TestPostgreSQL整理重扫仅厂牌变化后由队列作废自动值` 只修改厂牌标签，推进真实后台队列，详情依次显示扫描值、自动值、新扫描值，旧自动行消失且产生一条扫描作废记录。按合同 §24.2，作废发生在重扫后的整理取证阶段。 |
| V12 | 通过 | `curation_metrics_integration_test.go` 的 `TestPostgreSQLCurationMetrics` 调用 `curation_metrics_report_integration_test.go` 的 `metricReport`，对程序可定样本进入发行/录音识别的数量断言为零；`curation_metrics_model_integration_test.go` 的 `checkItemBudgets` 要求第二档与联网均实际发生、逐项工具数与估算 token ≤ D16。D 的 92 样本在两种应答模式均为零项误入；oracle 第二档/联网工具峰值各 2，主模型 token 峰值分别 5,181/4,938。 `curation_patrol_integration_test.go` 的 `TestPostgreSQL整理增量巡检与管理员重巡` 对比原样重扫前后巡检调用、输入与输出 token 的整个结构体；`curation_model_fallback_integration_test.go` 的 `TestPostgreSQL整理联网额度与单项额度` 检查触及两档的 token 或工具次数门槛后的路由与结案。 |
| V13 | 通过 | 归档父任务 `11-22-auto-curation-sources` V1、V3–V5、V9、V11 在下表逐项列出。新增 `curation_z_regression_integration_test.go` 的 `TestPostgreSQL整理关闭总开关后双层停止出站`：已配置模型的隔离实例开启再关闭，扫描稳定专辑并推进调度，程序和模型均为 disabled，来源计数、模型用量和整理会话均为空；重新开启同一专辑后两端都有调用。 |
| V14 | 真实模型试用已完成，v6 轮 17/20 达标 | 两轮均以真实 MusicBrainz、同一 Responses 模型整理及审查，隔离库排空后逐曲评分。v6 轮正确对应 18 张，12 张为版本级、6 张为组级；其中 1 张仍有错字，另有 2 张未确定发行。三类错误均为零。[两轮对照](#版本判定与审查策略-v6-的真实模型复验) |
| V15 | 通过 | `curation_model_fallback_integration_test.go` 的 `TestPostgreSQL整理第二档查到候选外编号直接写入` 断言首档有完整五首候选但无正确编号；门槛关闭时没有第二档调用，开放后模型用原文搜索、读回曲目、提交正确编号，落库方法为 album_structure、独立审查数为零。`TestPostgreSQL整理相同曲数的时长反证不能写入` 断言独立审查收到 difference_ms，审前无对应、拒绝后结案且零写入。实际门槛由 `backend/internal/curation/program/queue_priority_integration_test.go` 的 `TestPostgreSQL整理队列五档取项与模型查询门槛` 覆盖：真实 `LookupTierReady` 在每次取项时核对，优先级高于本地完整时均为 false。 |
| V16 | 通过（含无标签编号目录复验） | `curation_patrol_integration_test.go` 的 `TestPostgreSQL整理无标签文件名直接识别` 断言扫描从文件名取得艺人与曲名，搜索使用推断的艺人和标题，完成录音对应与厂牌补全，模型本地整理和三档识别均零调用。`TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序` 同次回复提交两个真实片段和两个编造值，断言真实值按最低级写入、编造的艺人与曲名均无自动行，碟号与曲序维持扫描结果。 新增 `curation_local_directory_integration_test.go` 的 `TestPostgreSQL整理无标签编号目录中的专辑名与艺人`，实际扫描 `06 - Tori Amos - Boys for Pele`，断言调用一次本地整理、正确艺人和标题写入最低自动层并进入搜索证据。 |
| V17 | 通过 | `curation_z_regression_integration_test.go` 的 `TestPostgreSQL整理六类反例无错连错并错改` 运行 `testdata/curation_regression/samples.json` 内全部 12 个反例：同名不同专辑、未收录但有近似候选、相同曲数的不同合辑、曲数和时长都相符的错误标签编号、不该合并的艺人、28 个候选的截断搜索，各 2 个。额外先入库 picard-01/02 作为近名艺人的对照；显式检查 Aster Vale / Aster Valley、Birch Meridian / Birch Meridiem 各有两个原始身份且最终仍分别指向两个艺人。受控模型取首个候选；`metricScoreEntries` 读取数据库外部对应、组别、发行并入、艺人重定向和发行/曲目自动值，逐样本对照标注，12 个样本的错连/错并/错改分别均为 0/0/0。此指标允许正确放弃。 |
| V18 | 通过 | `backend/internal/curation/program/match_rules_c_test.go` 的 `Test散放单曲的录音和共同艺人` 用真实标题/艺人标记核对唯一匹配、多录音同艺人、同名不同艺人编号。`curation_match_c_integration_test.go` 的 `TestPostgreSQL整理散放单曲的写入粒度` 正常扫描两个散放样本；核对唯一者的录音编号，共同艺人时仅艺人对应、无录音对应，两者均无发行对应和 recording_match 待定项。 |

### V13 与旧验收的对应关系

| 归档条目 | 文件及测试 | 断言 |
| --- | --- | --- |
| V1 总开关 | `curation_sources_integration_test.go` 的 `TestPostgreSQLCurationSourcesGate`；`curation_program_worker_integration_test.go` 的 `TestPostgreSQLCurationProgramGates/disabled`；`curation_scan_acceptance_integration_test.go` 的 `TestPostgreSQLCurationDisabledBeforeFirstEnable` | 冷、热查询都被闸门拒绝且后台零出站；程序来源调用为空；首次开启前指纹零出站、受控模型无请求且无整理会话。 |
| V3 人工优先 | `curation_integration_test.go` 的 `TestPostgreSQLCurationFieldPriority` | 对八个字段核对三层、显式设空、清除人工与自动层后显示的下一层值。 |
| V4 按批与按来源撤销 | `curation_integration_test.go` 的 `TestPostgreSQLCurationBatchRevert`、`TestPostgreSQLCurationProviderCursor`；`curation_external_revert_integration_test.go` 的 `TestPostgreSQLExternalScopeRevert`；`curation_write_acceptance_integration_test.go` 的 `TestPostgreSQLCurationBatchRevertKeepsManualEdit` | 批内同字段逆序撤回、保留冲突；按来源 101 条跨页、其它来源保留；封面与歌词按批及按来源撤销，保留对照样本；后来的人工编辑仍为有效值。 |
| V5 撤销抑制与重扫 | `curation_integration_test.go` 的 `TestPostgreSQLCurationSuppression`；`curation_projection_integration_test.go` 的 `TestPostgreSQLCurationRescan`；`curation_external_reads_integration_test.go` 的 `TestPostgreSQLExternalArtworkRescan`；`curation_write_acceptance_integration_test.go` 的 `TestPostgreSQLCurationTrackAutoSurvivesRescan` | 相同字段、值、发行的重写得到 suppressed；扫描过程自身保留自动层及归属、外部封面文件和歌词行；有新扫描依据时后续整理作废按 V11 验收。 |
| V9 结构写入及撤销 | `curation_c_join_integration_test.go` 的 `TestPostgreSQLCurationCJoinGroup`；`curation_artist_merge_integration_test.go` 的 `TestPostgreSQLCurationAutoMergeRevertSuppresses`；`curation_release_merge_model_integration_test.go` 的 `TestPostgreSQLCurationMergeBlocked`、`TestPostgreSQLCurationMergeRevert`；`backend/internal/catalog/governance/artist_revert_integration_test.go` 的 `TestPostgreSQLArtistMergeRevertRestoresIntent`、同目录 `release_merge_contract_integration_test.go` 的 `TestPostgreSQLReleaseMergeRevert` | 无强证据的归组、发行并入被拒；撤销还原原组、艺人身份/署名/收藏、来源和目标碟轨布局，带抑制不重复操作。 |
| V11 文件只读 | `curation_scan_acceptance_integration_test.go` 的 `TestPostgreSQLCurationReadOnlyMusicTree` | 测试对目录树去掉写权限，在扫描、指纹、对应、封面、歌词、并入与重扫前后逐路径比对文件 SHA-256、mtime，检查无新增或缺失路径。 |

## 试用口径

`real-corpus` 按仓库 `backend/cmd/roomusic-curation-bench/corpus.json` 的 20 个公开发行编号逐个读取 MusicBrainz，使用项目固定的 FFmpeg 合成 288 首 16 kHz 单声道静音 FLAC。生成数据包括 5 张 Picard、5 张保留 MBID 去条码、6 张纯文字、2 张错字、2 张无标签专辑。验收逐曲核对了 FLAC 采样数和标签模式；无标签的本地文件名及目录名保留。评分使用生成当次的发行、组、逐曲录音和署名快照，单独存放，独立于各轮的应用响应和模型回答。[生成摘要](corpus-generation.json) · [逐专辑文件核对](corpus-check.json) · [公开参考明细](public-reference.json)

程序轮及三次 oracle 轮各从新的隔离 PostgreSQL 数据库、空的应用缓存和同一批音频开始。两次正常扫描确认文件稳定，中间将这一批文件的稳定等待推进 60 秒。计时从开启整理到最后一次排空确认，含真实来源的排队、请求、冷却和空闲确认。四次均使用生产 MusicBrainz 客户端、程序标识 `ROOMusic/dev` 和来源限速器；试用端的 HTTP 传输使用环境代理，只记录每次实际 GET 的端点类型、状态和时间。音频、绝对路径及私有库信息均未作为请求发送。该网络出口可能同时有其它程序访问 MusicBrainz。

四次均只开启 MusicBrainz 公网服务，CAA、LRCLIB 和声纹关闭。三次 oracle 轮均另设回环 oracle 及返回空结果的回环网页搜索。oracle 按清单选择候选、提交已知 ID 或返回已知标题，对没有身份标注的艺人放弃。程序层保留后台匹配与核对，模型主调用、审查、网页搜索都在测试实例内完成。对端点的重试也计入请求数，缓存命中不计；生成曲库的请求在试用窗口之外。

完成率的分母是 20 个目录。每个目录须扫为一张完整专辑，建立正确版本或合同允许的组级对应、显示正确标题、没有错误写入和影响该样本的未结束项。版本级还逐轨检查录音对应。错连按错误的外部对应行计数，错并核对跨发行组归组、发行并入及艺人重定向，错改核对落在发行和曲目自动层的字段。缺少对应或保留原有错字计为未完成。各档“解决”按建立正确发行或组级对应的本地发行数归属，模型巡检、放弃和保留建议单列。

## 程序轮与三次 oracle 对照

四次运行的隔离库均有 20 张、288 首，开启的队列均达到排空条件。空会话指 `steward_conversations` 中没有对应 `steward_messages` 的会话，修复前取排空前最后一次数据库快照，修复后在排空时按同一条件计数。来源传输统计包含重试；模型 token 按请求及 SSE 响应字节各自除以 4 向下取整。[程序轮](live-program.json) · [修复前 oracle](live-oracle.json) · [退避与本地整理修复后](live-oracle-after-fix.json) · [V7 修复后](live-oracle-after-v7.json)

| 度量 | 程序轮（沿用） | 修复前 oracle | 退避与本地整理修复后 oracle | V7 修复后 oracle |
| --- | --- | --- | --- | --- |
| 测试时间（UTC） | 00:53:04–00:54:10 | 00:54:40–01:07:05 | 01:20:20–01:22:57 | 01:34:59–01:37:43 |
| 从开启到排空（秒） | 65.97 | 744.93 | 157.14 | 164.63 |
| 会话总数 / 无消息会话数 | 未计数 | 1,963 / 1,876 | 95 / 0 | 97 / 0 |
| 正确对应：版本级 / 仅发行组 | 11 / 5 | 14 / 5 | 15 / 5 | 15 / 5 |
| 完整达到标注 | 14/20（70%） | 17/20（85%） | 19/20（95%） | 20/20（100%） |
| 要求精确版本且满足其他完成条件 | 9/20 | 12/20 | 14/20 | 15/20 |
| 错连 / 错并 / 错改 | 0 / 0 / 0 | 0 / 0 / 0 | 0 / 0 / 0 | 0 / 0 / 0 |
| 未结束项 / 等人工 | 5 / 0 | 0 / 0 | 0 / 0 | 0 / 0 |
| 按程序 / 第一档 / 第二档 / 联网建立的正确对应 | 16 / 0 / 0 / 0 | 17 / 1 / 1 / 0 | 17 / 3 / 0 / 0 | 17 / 3 / 0 / 0 |
| MB 请求 / HTTP 200 / HTTP 503 | 34 / 31 / 3 | 60 / 47 / 13 | 81 / 66 / 15 | 77 / 66 / 11 |
| 模型调用 / 估算输入 / 估算输出 | 0 / 0 / 0 | 101 / 145,188 / 11,300 | 110 / 183,208 / 12,077 | 114 / 187,706 / 12,466 |
| 估算 token 合计 | 0 | 156,488 | 195,285 | 200,172 |

| 端点 | 程序轮 | 修复前 oracle | 退避与本地整理修复后 oracle | V7 修复后 oracle |
| --- | --- | --- | --- | --- |
| musicbrainz/artist/lookup | 0 | 1 | 2 | 3 |
| musicbrainz/artist/search | 4 | 4 | 1 | 1 |
| musicbrainz/release/lookup | 14 | 36 | 63 | 58 |
| musicbrainz/release/search | 16 | 19 | 15 | 15 |
| CAA、LRCLIB 及其余公网来源 | 0 | 0 | 0 | 0 |
| 回环 SearXNG /search | 0 | 11 | 14 | 15 |
| 回环模型 /chat/completions | 0 | 101 | 110 | 114 |

程序轮的 MusicBrainz 503 比例为 8.8%，修复前后 oracle 分别为 21.7%、18.5%。以约 500 ms 周期采样队列的恢复时刻，程序轮为3 次，首次观察时距恢复还剩 1.55–1.72 秒；修复前为6 次，首次观察时距恢复还剩 1.62–15.73 秒；修复后为9 次，首次观察时距恢复还剩 0.47–3.90 秒。V7 修复后，11/77 次请求返回 503（14.3%），观察到 8 个恢复时刻，其中一个在取样时已过约 12 ms，其余最迟距恢复 7.78 秒。共用网络出口上的其它程序也可能发起 MB 请求。

| 模型类别 | 修复前（调用 / 输入 / 输出） | 退避与本地整理修复后（调用 / 输入 / 输出） | V7 修复后（调用 / 输入 / 输出） |
| --- | --- | --- | --- |
| 巡检 | 63 / 61,871 / 7,768 | 62 / 60,944 / 7,679 | 62 / 60,947 / 7,680 |
| 本地整理 | 0 / 0 / 0 | 2 / 3,474 / 199 | 2 / 3,474 / 199 |
| 第一档 | 1 / 17,758 / 74 | 3 / 67,926 / 222 | 3 / 67,926 / 222 |
| 第二档 | 3 / 20,077 / 309 | 0 / 0 / 0 | 0 / 0 / 0 |
| 字段与身份核对 | 11 / 9,589 / 1,140 | 14 / 12,615 / 1,452 | 15 / 13,417 / 1,555 |
| 联网 | 22 / 34,185 / 1,899 | 28 / 36,541 / 2,415 | 30 / 38,564 / 2,590 |
| 独立审查 | 1 / 1,708 / 110 | 1 / 1,708 / 110 | 2 / 3,378 / 220 |

V7 轮较上一轮多完成 1 张，少发 4 次 MB 请求、少收 4 次 503，模型多调用 4 次，估算 token 增加 4,887（输入 +4,498、输出 +389）。两轮的错连、错并、错改均为零。各轮模型项的结案数及每个发行的评分在原始 JSON 的 `score.model_item_outcomes` 和 `score.samples`。两个无标签目录的扫描值、有效值及本地整理去向另有数据库快照。[修复后四张重点专辑](live-oracle-after-fix-fields.json)

## 版本判定与审查策略 v6 的真实模型复验

2026-11-26 02:15:06–02:28:34 UTC，从全新隔离库扫描同一批 20 张、288 首，真实 MusicBrainz 与 Responses 端点沿用上一轮设置，主模型和独立审查模型同为 `gpt-6.2-sol`，密钥为 `<略>`。网页搜索仍使用返回空结果的回环 SearXNG。下表两列的 token 均取接口返回的 usage。[上一轮原始 JSON](live-real-responses.json) · [v6 原始 JSON](live-real-responses-v6.json)

| 指标 | 上一轮真实模型 | v6 真实模型 |
| --- | ---: | ---: |
| 完整达标 | 16/20 | **17/20** |
| 正确对应：版本级 / 仅发行组 | 13 / 5 | 12 / 6 |
| 按程序 / 第一档 / 第二档 / 联网建立的正确对应 | 17 / 1 / 0 / 0 | 17 / 1 / 0 / 0 |
| 错连 / 错并 / 错改 | 1 / 0 / 3 | **0 / 0 / 0** |
| 审查决定：批准 / 需人工确认 / 拒绝 | 0 / 7 / 0 | 1 / 3 / 0 |
| 模型直接写入 / 经审查写入（操作数） | 6 / 0 | 3 / 1 |
| 模型调用 / 输入 / 输出 token | 167 / 879,133 / 22,488 | 159 / 715,675 / 21,024 |
| 实报 token 合计 | 901,621 | 736,699 |
| MB GET / 200 / 503 | 92 / 82 / 10 | 97 / 80 / 17 |
| MB 艺人详情 / 艺人搜索 / 发行详情 / 发行搜索 | 4 / 4 / 64 / 20 | 3 / 4 / 64 / 26 |
| 回环搜索次数 | 62 | 66 |
| 排空耗时 / 会话数 / 空会话数 | 865.95 秒 / 94 / 0 | 807.39 秒 / 97 / 0 |

159 次模型请求全部返回 HTTP 200 和完整 usage，接口与应用日账对齐。直接写入为 1 次外部对应、2 次发行自动修正，经审查写入为 1 次发行标题修正。模型等待、运行中、等人工、搁置在结束时均为 0。程序队列的轮询观测到 12 个 MB 恢复时刻，首次采样距恢复约 1.32–3.86 秒；共用网络出口上的其他程序也可能发起请求。全阶段单项最高为 18 次工具调用、139,703 token，分属不同的艺人身份项。

| 重点样本 | 本轮实测与上一轮差异 |
| --- | --- |
| 王菲《菲主打》 | **本轮达标。** 上轮审查把两次纠正留作建议。本轮 v6 方案记录了后端重读的完整外部标题 `菲主打`、生效对应方法 `search`、本地和外部各 14 首、时长可比 14 首且吻合 14 首。审查批准，自动及有效标题为 `菲主打`，扫描值仍为 `菲主答`。 |
| Moby《Ambient》 | **标题未达标。** 第一档提交后，程序按 `album_structure` 正确认定发行组，版本对应 0 条、录音对应 0 条。巡检建议 `Ambinet → Ambient` 最终以 `no_evidence` 留作建议。当前结构取证从具体发行或录音对应读取；这次没有可提交 v6 审查的版本标题证据。上轮有版本对应，但标题被独立审查留作建议。 |
| Tori Amos《Boys for Pele》 | **缺发行或组级对应。** 上一轮第二档选错版本，连带写错条码、目录号和厂牌。本轮恢复了正确本地标题和艺人，最终 `unlisted`，这三个版本字段的自动值均为空，录音对应 0 条。工具记录显示模型本轮未提交发行编号。《Ambient》有实际的组级对应，同组两版在结构上均吻合时的写入边界由定向回归验证。 |
| Aphex Twin《…I Care Because You Do》 | **缺发行或组级对应。** 本地标题与艺人恢复正确。开放库工具读取 3 次、搜索 1 次返回 `source_unavailable`，联网后未取得可提交的发行编号，结清为 `unlisted`；与上一轮一样未达标。 |

[重点样本字段、核对项、审查证据与工具结果](live-real-responses-v6-evidence.json)

## 真实模型试用与 oracle 对照

以下保留 v6 之前的真实模型结果。2026-11-26 01:46:33–02:00:59 UTC，使用同一批 20 张、288 首静音文件、新隔离库、相同生产限速器和相同评分。真实端点为 `https://api.qlqq.de/v1`，协议为 OpenAI Responses，开启者的整理模型与独立审查模型均为 `gpt-6.2-sol`，密钥为 `<略>`。测试通过回环服务将应用生成的请求流转发至固定 HTTPS 目标，在进程内读取 Responses 终帧的 usage。每日上限设为 3,000,000 token，D16 使用生产设置。两轮均使用返回空结果的回环 SearXNG；真实轮还实际调用了生产的网页读取工具。MusicBrainz 请求数仅统计 MB API 客户端的实际 GET，含重试。该网络出口可能同时有其他程序访问 MB。[真实轮原始 JSON](live-real-responses.json) · [oracle 原始 JSON](live-oracle-after-v7.json)

| 度量 | V7 修复后 oracle | 真实模型 |
| --- | ---: | ---: |
| 排空耗时 | 164.63 秒 | 865.95 秒 |
| 达到全部标注 | 20/20 | 16/20 |
| 正确对应：发行版本 / 仅发行组 | 15 / 5 | 13 / 5 |
| 正确对应：程序 / 第一档 / 第二档 / 联网 | 17 / 3 / 0 / 0 | 17 / 1 / 0 / 0 |
| 错连 / 错并 / 错改 | 0 / 0 / 0 | 1 / 0 / 3 |
| 模型等待 / 运行中 / 等人工 / 搁置（结束时） | 0 / 0 / 0 / 0 | 0 / 0 / 0 / 0 |
| MB 请求 / 200 / 503 | 77 / 66 / 11 | 92 / 82 / 10 |
| MB 艺人详情 / 艺人搜索 / 发行详情 / 发行搜索 | 3 / 1 / 58 / 15 | 4 / 4 / 64 / 20 |
| 回环网页搜索次数 | 15 | 62 |
| 模型调用 | 114 | 167 |
| 输入 / 输出 token | 187,706 / 12,466（按字节估算） | 879,133 / 22,488（接口实报） |
| token 合计 | 200,172（估算） | 901,621（实报） |
| 独立审查 | 2 次，2 个标题完成核对写入 | 7 次完成，7 项均判需人工确认，0 项批准 |
| 直接写入的模型操作（日记账） | 原轮未单列 | 6 次，其中外部对应 2 次、发行自动字段操作 4 次 |

真实轮 167 次模型调用均返回 HTTP 200 和完整 usage，输入、输出分别与整理日账一致。共 94 个会话，空会话 0 个。7 项审查结论均记为未采用的建议，经审查的实际写入数为 0。6 次直接写入按模型批次中的操作 ID 计，一次字段操作可影响多个字段。MB 的 10 次 503 中，500 ms 间隔的进度轮询观察到 4 个不同的队列恢复时刻，首次观察时剩余约 1.57–1.88 秒。

| 模型阶段 | 调用次数 | 实报输入 token | 实报输出 token | 业务结果 |
| --- | ---: | ---: | ---: | --- |
| 巡检 | 65 | 63,516 | 8,556 | 62 项完成巡检 |
| 本地整理 | 2 | 3,658 | 113 | 两个无标签目录均写入正确专辑名与艺人 |
| 第一档 | 3 | 94,101 | 180 | 建立 1 个正确发行对应 |
| 第二档 | 6 | 57,779 | 1,385 | 写入 1 个版本级对应，评分判错 |
| 名字与身份核对 | 16 | 13,738 | 2,030 | 9 条名字建议、3 条署名建议 |
| 联网 | 68 | 636,619 | 9,036 | 1 张发行结清为未收录；艺人身份 6 项无结果、3 项留建议 |
| 独立审查 | 7 | 9,722 | 1,188 | 7 项均为 `needs_human_confirmation` |

**4 张未达到完整标注的样本**

| 专辑 | 结果与原因 |
| --- | --- |
| 王菲《菲主打》 | 发行版本正确。两条核对项中主模型都提交 `typo`。独立审查认为外部摘要展示了匹配编号，缺少可独立核对的正式标题，均以 `review_uncertain` 留建议。显示标题仍为 `菲主答`。 |
| Tori Amos《Boys for Pele》 | 本地标题和艺人均正确。第二档直写发行 `4cd43e6e-df96-3546-8343-870035e5eaf6`，清单期望 `4c31aef1-177e-4bba-9a56-180e21a0d043`。由此计 1 条版本错连及 3 个错改：条码写为 `075678069628`（参考为空），目录号为 `7567-80696-2`（期望 `7567 80696`），厂牌为 EastWest（期望 Atlantic）。 |
| Moby《Ambient》 | 第一档准确匹配发行，主模型两次将 `Ambinet` 判为错字。独立审查认为结构匹配足以支持发行对应，但外部摘要对标题本身的佐证不足。两次保留建议，显示名仍为 `Ambinet`。 |
| Aphex Twin《…I Care Because You Do》 | 本地整理恢复了正确标题和艺人。发行项第二档读取资料有 4 次成功、8 次 `source_unavailable`，联网阶段也未提交可写入的发行编号，最终结清为 `unlisted`，缺少清单中的发行或组级对应。 |

按项跨会话汇总，工具调用最高为 **23 次**（发行匹配项，共 104,401 token）；token 最高为 **123,589**（艺人身份项，15 次工具调用，含审查 1,108 token）。两个峰值属于不同项。该统计包含与每项关联的主模型及审查 usage；全轮没有会话摘要。该轮运行结束前从同一隔离库采集全阶段峰值，汇总正好为 901,621 token；同一查询已纳入后续驱动。[字段、审查与峰值快照](live-real-responses-evidence.json)

## 与 M 基线对照

M 初始 oracle 在 92 个受控样本上完成 78/92（84.8%），记录 243 次 MB 请求、445 次模型调用和 1,783,340 估算 token，错连/错并/错改为 12/0/2。after-d oracle 完成 90/92（97.8%），289 次 MB 请求、293 次模型调用、397,986 估算 token，三项错误均为零。Z 的 20 张公开专辑在上一轮平均每张实际请求 MB 4.05 次，V7 轮为 3.85 次。两套样本、来源开关和网络条件不同。M 公网速率基线为 60 次详情请求、45 次 200、15 次 503，耗时 69.59 秒。[M 初始](../../11-25-curation-chain/research/m-baseline/baseline-2026-11-25.md) · [after-d](../../11-25-curation-chain/research/m-baseline/after-d/metrics-2026-11-25.md) · [M 公网速率](../../11-25-curation-chain/research/m-baseline/live-measurements-2026-11-25.md)

after-z 使用与 after-d 相同的 92 个样本。oracle 完成 90/92，first_candidate 完成 88/92，两个模式逐张完成状态均与 after-d 相同，错连、错并、错改均保持 0/0/0。oracle 的 MB 请求由 289 增至 291，模型调用保持 293 次，估算 token 从 397,986 增至 399,578。first_candidate 的 MB 请求与模型调用均保持 283 次，估算 token 从 371,170 增至 373,773。[after-z 对照报告](../../11-25-curation-chain/research/m-baseline/after-z/metrics-2026-11-26.md)

## D16 单项上限

| 档位 | 修复前（项数 / 单项工具峰值 / 单项 token 峰值） | 修复后 | D16 上限 |
| --- | --- | --- | --- |
| 第二档 | 1 项 / 3 次 / 20,386 | 0 项，无实测峰值 | 20 次 / 150,000 token |
| 联网 | 11 项 / 2 次 / 7,805 | 14 项 / 2 次 / 2,927 | 30 次 / 300,000 token |

V7 轮第二档 0 项，联网 15 项，单项峰值为 2 次工具调用、2,927 token。建议维持现行上限。三次 oracle 轮均按字节估算 token；联网使用返回空结果的回环搜索，样本未覆盖真实模型阅读长网页的用量。after-d 在第二档 6 项、联网 32 项的主模型单项峰值分别为 2 次工具调用 / 5,181 token 和 2 次 / 4,938 token。

v6 之前真实模型的第二档 2 项，单项峰值 13/20 次工具、39,388/150,000 token；联网 10 项，峰值 21/30 次工具、123,589/300,000 token。按工具计分别用到上限的 65% 和 70%，按 token 计分别为 26% 和 41%。**建议维持两个档位的现行上限。** 本轮没有项碰到 D16；错版直写与标题佐证不足均已在额度内结案，调整额度不能直接解决这两类问题。网页搜索端点返回空结果，样本也没有覆盖长网页搜索结果下的最高开销。

| 真实单项峰值（工具调用 / 实报 token） | 上一轮 | v6 | D16 上限 |
| --- | ---: | ---: | ---: |
| 第二档 | 2 项，13 / 39,388 | 2 项，5 / 25,327 | 20 / 150,000 |
| 联网 | 10 项，21 / 123,589 | 14 项，18 / 139,703 | 30 / 300,000 |

**v6 复验建议维持 D16。** 两轮的第二档均未触顶，联网 token 峰值本轮增加 16,114，达到上限的 46.6%。这两轮的调用条件只覆盖空搜索结果服务与有限的公开网页读取。

## 缺陷复验与预期行为

1. **第一档取详情的会话与退避。** 修复前连续 503 曾导致约 10 分钟失败缓存，最终快照为 1,876 个空会话。复现输入为有缺失详情的候选；新增回归用例将该详情连续两次返回 503，再返回 200，检查失败项、可独立推进的项和会话创建。修复后 oracle 轮排空时共有 95 个会话，其中 0 个无消息。测试使用 250 ms 模型轮询，生产默认 1 分钟。[修复前现场](live-oracle-selection-backoff.json) · [修复后实测](live-oracle-after-fix.json)
2. **无标签目录提取出的数字艺人。** 复现目录为第 06 与第 13 张，扫描艺人分别为 `06`、`13`，两张的标题均含“艺人 - 专辑”。修复前两张均漏过本地整理，修复后两张均结案为 `local_cleanup`，有效艺人和标题分别为 Tori Amos / Boys for Pele、Aphex Twin / …I Care Because You Do。是否建立外部对应另外评分。[修复前字段](live-oracle-fields.json) · [修复后字段](live-oracle-after-fix-fields.json)
3. **无模型时等待名字核对，符合预期。** 程序轮准确识别了 Arise 和 Gettin’ In Over My Head，并为这两张专辑保留 5 条 `verify_name/waiting_model`，没有等待人工的项。程序轮生产路径沿用原结果，未重跑。[程序轮](live-program.json)

### 修复后未达到完整标注的样本

V7 修复后的 oracle 轮未达标样本为 **0 张**。首次真实轮有 4 张未达标，v6 轮有 3 张，见[真实模型复验](#版本判定与审查策略-v6-的真实模型复验)。20 张专辑中 15 张匹配到具体版本、5 张匹配到标注允许的发行组，288 首均参与评分。[V7 后逐张评分](live-oracle-after-v7.json)

上一轮唯一未达标的是 Moby《Ambient》（12 首）。发行匹配前的巡检建议缺少佐证，联网后保留为建议；匹配后的第二条名字核对项因依据过时结清，扫描标题和显示标题都是 `Ambinet`。[上一轮字段快照](live-oracle-after-fix-fields.json)

oracle 轮 01:37:37 UTC，该专辑已匹配到发行 `dd9e8a79-e72f-3b7c-8841-6e31fddca7ef`，结构佐证生成的第二条核对项待处理，原值 `Ambinet`、候选值 `Ambient`。01:37:38，同一项以 `model_verified` 结清，标题操作关联的独立审查已完成、决定为 `approved`；自动层与有效标题均为 `Ambient`，扫描原值仍为 `Ambinet`。该项从入队到结清的输入摘要相同。本轮验证了真实来源下的审查和写入，依据过时后的重排、同输入再次过时的结清以及撤销由定向回归用例验证。[本轮专辑快照](live-oracle-after-v7-ambient.json)

## 模型提交版本与审查策略 v6 的定向回归

新增 `curation_model_editions_integration_test.go` 的 `TestPostgreSQL整理模型提交同组多版须有版本标识`，让第二档模型向生产工具提交同组两个版本中的第一个。两版曲序、时长均吻合，条码、目录号、厂牌和录音编号不同。

| 本地输入 | 落库断言 |
| --- | --- |
| 没有版本标识 | 读过另一版详情，仅写入准确的 `release_group_match`。版本对应、五首歌的录音对应、三个版本专属字段的自动值均为零。 |
| 含匹配的 `mb_release` 标签 | 写入准确的发行版本、5 条正确的录音对应，以及该版条码、目录号和厂牌。 |

并入用例的操作审查记录和 API 返回值均与 `runtime.ReviewPolicyVersion` 比较。定向组合覆盖 11 个顶层用例、21 个叶用例，全部通过，app 包用时 48.620 秒。命令使用隔离项目，结束后按精确项目标签检查容器、网络和卷均为空。

```bash
mkdir -p temp/z-editions-regression
sg docker -c 'export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/z-editions-regression" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-editions-regression
ROOMUSIC_TEST_RUN="^Test(PostgreSQL整理(模型提交同组多版须有版本标识|审查发行并入证据|第二档查到候选外编号直接写入|相同曲数的时长反证不能写入|第一档禁用字段修改且对话可审查|第一档禁用曲目字段直写)|PostgreSQLCurationModel(Plan|Apply|Recording|ResolveMatchProgramChain|MergeReleases))$" mise exec -- ./scripts/test-integration.sh'
```

## 真实模型验证命令

v6 复验沿用以下环境变量与测试入口，临时目录改为 `temp/z-real-v6`，Compose 项目为 `roomusic-compose-test-z-real-v6`，原始结果为 `research/live-real-responses-v6.json`；用例 824.29 秒通过。项目标签下的容器、网络和卷均已清理。

在仓库根目录以独立 Compose 项目运行下列选择器。实跑外层设置 `GOFLAGS` 采集 Go 测试事件，并用临时启动包装把 app 测试进程超时设为 125 分钟，进度日志只保留时间、计数和状态。驱动最多观察 2 小时，本次用例 875.21 秒通过，结束后按项目标签查询容器、网络和卷均为空。按当前脚本的默认 25 分钟测试超时也足以覆盖此次实测用时。

```bash
mkdir -p temp/z-real-responses
sg docker -c 'export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/z-real-responses"
export ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-real-responses ROOMUSIC_TEST_RUN="^Test真实曲库队列试用$" ROOMUSIC_LIVE_MODE=real
export ROOMUSIC_LIVE_ENDPOINT=https://api.qlqq.de/v1 ROOMUSIC_LIVE_MODEL=gpt-6.2-sol
export ROOMUSIC_LIVE_KEY_FILE="$HOME/.codex/auth.json" ROOMUSIC_LIVE_KEY_FIELD=OPENAI_API_KEY
export ROOMUSIC_LIVE_REFERENCE="$PWD/temp/real-corpus-reference.json" ROOMUSIC_LIVE_OUT="$PWD/.trellis/tasks/11-25-curation-chain-z-acceptance/research/live-real-responses.json"
mise exec -- ./scripts/test-integration.sh'
```

计量单测于 `backend/` 使用 `mise exec -- go test ./internal/app -run "^Test真实模型计量保留分块事件并只取末帧用量$" -count=1 -v` 通过，覆盖 SSE 事件跨行、任意字节分块及末帧实报用量的归属。

## V7 回归验证与运行命令

| 用例 | 结果 |
| --- | --- |
| 新增 `TestPostgreSQL整理名字核对按新依据重排一次` | 5.14 秒通过。第一子例（3.25 秒）检查旧会话返回 `requeued`，同一项用新的输入摘要和身份摘要被另一会话领取，模型收到当前发行编号和对应操作号；审查批准前自动层为空，批准后可同时读回扫描值与有效值，撤销后恢复原值。第二子例（1.89 秒）让巡检原文在首次领取前变化，刷新后保持输入不变，再次提交时结清为 `stale`，无审查、无改名。 |
| 既有名字核对及巡检用例 | `TestPostgreSQL整理专辑名字只纠正有佐证的错字`（含 5 个子例，9.15 秒）、`TestPostgreSQL整理无佐证主张先联网再留建议`（2.85 秒）、`TestPostgreSQL整理观察建议的改动字段保持数组`（1.38 秒）、`TestPostgreSQL整理增量巡检与管理员重巡`（9.68 秒）均通过。 |
| `Test真实曲库队列试用` | 仅运行一次 oracle，173.49 秒通过；其中开启整理到排空为 164.63 秒。 |

组合首跑时，新增用例在项已结清后立即读取工具日志，只读到两次调用中的第一条。将断言改为等待异步日志落库，再单跑新增用例。三个 Compose 项目结束后按各自标签查询容器、卷和网络，均为零。

```bash
mkdir -p temp/{z-v7-regression,z-v7-regression-final,z-v7-oracle}
export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost
sg docker -c 'TMPDIR="$PWD/temp/z-v7-regression" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-v7-regression ROOMUSIC_TEST_RUN="^TestPostgreSQL整理(名字核对按新依据重排一次|专辑名字只纠正有佐证的错字|无佐证主张先联网再留建议|观察建议的改动字段保持数组|增量巡检与管理员重巡)$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'TMPDIR="$PWD/temp/z-v7-regression-final" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-v7-regression-final ROOMUSIC_TEST_RUN="^TestPostgreSQL整理名字核对按新依据重排一次$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'export TMPDIR="$PWD/temp/z-v7-oracle" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-v7-oracle ROOMUSIC_TEST_RUN="^Test真实曲库队列试用$" ROOMUSIC_LIVE_MODE=oracle ROOMUSIC_LIVE_REFERENCE="$PWD/temp/real-corpus-reference.json" ROOMUSIC_LIVE_OUT="$PWD/.trellis/tasks/11-25-curation-chain-z-acceptance/research/live-oracle-after-v7.json"
GOFLAGS=-json mise exec -- ./scripts/test-integration.sh'
```

## 退避与本地整理修复的回归验证与运行命令

| 验证 | 结果 |
| --- | --- |
| `TestPostgreSQL整理第一档连续限流只推迟缺详情的项` | 3.66 秒通过。计数触发器记录会话插入，覆盖先创建再删除的情况。两次 503 后按来源恢复时刻延后 2、4 秒，失败次数仍为零，路线保持原值。另一项在同一冷却窗内完成；第三次请求 200 后原项完成。两项合计 2 个会话、0 个空会话。 |
| 受影响的 10 个既有调度与第一档用例 | 全部通过，覆盖领取会话、搁置与重开、三次未处理、程序匹配链、审查不可用、日上限、停止条件、候选限制及两类字段直写限制。具体选择器见下方命令。 |
| `TestPostgreSQL整理无标签编号目录中的专辑名与艺人` | 5.42 秒通过。断言扫描得到艺人 `06` 及复合标题，两个字段均进入本地整理请求，更新后可从详情与搜索证据读回；曲目未被误改。 |
| `TestPostgreSQL整理无标签文件名直接识别`、`TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序` | 分别用时 2.71、3.37 秒通过。 |
| `Test真实曲库队列试用`（仅 oracle） | 集成用例 166.01 秒通过，开启整理到排空为 157.14 秒。 |

首次调度组合同时运行了新用例初版（1.94 秒通过）。加强同批已就绪项及会话插入断言后，仅单跑该新用例。以下是从仓库根目录执行的筛选条件和隔离项目，临时目录位于本工作区。

```bash
mkdir -p temp/{z-backoff-test,z-backoff-peer,z-local-directory,z-recheck-oracle}
export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost
sg docker -c 'TMPDIR="$PWD/temp/z-backoff-test" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-backoff ROOMUSIC_TEST_RUN="^Test(PostgreSQL整理第一档连续限流只推迟缺详情的项|PostgreSQLCurationModel(StartsCurationConversation|UnhandledThreeTimes|ShelveAndReopen|ResolveMatchProgramChain|ReviewUnavailableBackoff|DailyLimit|StopConditions)|PostgreSQL整理第一档禁用(字段修改且对话可审查|曲目字段直写)|整理第一档选择严格限定已发送的候选)$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'TMPDIR="$PWD/temp/z-backoff-peer" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-backoff-peer ROOMUSIC_TEST_RUN="^TestPostgreSQL整理第一档连续限流只推迟缺详情的项$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'TMPDIR="$PWD/temp/z-local-directory" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-local-directory ROOMUSIC_TEST_RUN="^TestPostgreSQL整理(无标签编号目录中的专辑名与艺人|无标签文件名直接识别|本地模型只写本地片段并保留碟号曲序)$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'export TMPDIR="$PWD/temp/z-recheck-oracle" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-recheck-oracle ROOMUSIC_TEST_RUN="^Test真实曲库队列试用$" ROOMUSIC_LIVE_MODE=oracle ROOMUSIC_LIVE_REFERENCE="$PWD/temp/real-corpus-reference.json" ROOMUSIC_LIVE_OUT="$PWD/.trellis/tasks/11-25-curation-chain-z-acceptance/research/live-oracle-after-fix.json"
GOFLAGS=-json mise exec -- ./scripts/test-integration.sh'
```

按以上四个项目的精确 Compose 标签检查容器、网络和卷，结果均为空。本次只修改测试和 Z 任务的研究记录，保留实现代理的生产代码修改；未提交 Git。

## 首次验收命令（沿用）


- 在仓库根目录以 `NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/z-match-test2" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-match2 ROOMUSIC_TEST_RUN='^TestPostgreSQL整理(同名跨组用四个识别请求定案|唯一候选撤销后不调用模型)$' sg docker -c 'mise exec -- ./scripts/test-integration.sh'` 验证，仅执行这两个测试，分别通过（4.75 秒、3.36 秒）。首跑使用独立目录 `temp/z-match-test` 和项目 `roomusic-compose-test-z-match`，测试夹具在发布艺人目录前查询，两个测试均在准备阶段报 `artist catalog preparing`；补上既有 `publishSearch` 步骤后重跑。两个项目均由脚本删除其容器和数据卷，按项目标签执行 `docker ps -aq` 返回空。
- 在 `backend/` 执行 `NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost mise exec -- go test ./internal/curation/program -run '^Test模型整理的标题必须通过曲数和时长核对$' -v -count=1`，三个子例全部通过。

于仓库根目录运行，端口由脚本随机分配。V13 为 2.70 秒，V11 为 4.36 秒。V17 初次仅用 12 个反例，35.06 秒通过；补上 2 张 Picard 的近名艺人对照后单独重跑该新用例，14 张入库，仅计 12 个反例，44.10 秒通过。第一次把 TMPDIR 置于 sg 外侧，脚本因默认锁占用提前退出；移入命令内后通过。第三项首次编译时并行开发的试用文件尚未加入 liveCorpusScore，完成后单独重试通过。两个项目的容器和卷均由脚本清理。

```bash
mkdir -p temp/z-regression temp/z-label
sg docker -c 'NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/z-regression" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-regression ROOMUSIC_TEST_RUN="^TestPostgreSQL整理(六类反例无错连错并错改|关闭总开关后双层停止出站)$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/z-label" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-label ROOMUSIC_TEST_RUN="^TestPostgreSQL整理重扫仅厂牌变化后由队列作废自动值$" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/z-regression" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-z-regression ROOMUSIC_TEST_RUN="^TestPostgreSQL整理六类反例无错连错并错改$" mise exec -- ./scripts/test-integration.sh'
```
