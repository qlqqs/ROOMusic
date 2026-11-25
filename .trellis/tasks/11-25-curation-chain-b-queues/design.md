# 整理链 B 设计：队列、多来源并行与中断恢复

本设计已按审阅意见实施。路径以仓库根为基准。依据为父任务 `design.md` 第 4 节、R3/R4/R16 及后端整理合同第 15.6、22–24 节。

## 1. 扫描能证明什么

| 情形 | 当前行为及依据 |
| --- | --- |
| 手动 | 管理员 `POST /api/v1/scans` 默认增量，可显式选 full。管家的手动入口也调用同一个 `StartScan`。见 `backend/internal/library/httpapi/scans.go:27`、`backend/internal/library/stewardtools/config.go:202`、`backend/internal/library/scans.go:347`。 |
| 定时 | 应用启动 `RunScanScheduler`，按配置时区的每日整点对 active 根发起增量扫描。初值开启、4 点，可关闭。10 分钟是重新读取调度状态的最长间隔。整点遇到已有扫描、无活动根、协调失败会跳过。见 `backend/internal/app/app.go:216`、`backend/migrations/0042_scheduled_scan.sql:7`、`backend/internal/library/scan_schedule.go:39`、`backend/internal/library/scan_scheduler.go:42`、同文件第 94 行。 |
| 文件监视 | 基线中 `StartScan` 由上述 HTTP、管家和定时器调用。没有文件监视触发器。B 在同一入口增加静默窗口后的整理确认扫描。见 `backend/internal/curation/work_states.go:32`、`backend/internal/library/curation_observations.go` 的 StartCurationConfirmation。 |
| 根路径不存在、变为链接、根无法遍历 | 检查或遍历记 `root_unavailable`，根错误使整次扫描 incomplete。只有 succeeded 才执行所有根的缺失对账。原有发行、曲目及上次的 present 状态均可保留。见 `backend/internal/library/scanner.go:98`、第 255、328、366、458 行。 |
| 挂载消失但挂载点仍是可遍历的空目录 | 目录检查可以通过。原有 present 曲目、本轮完全为空且没有排除或遍历错误时，扫描记 root_empty，终态 incomplete，保留 present（backend/internal/library/scanner.go:466）。其余正常成功对账时，才把未再次见到的曲目标为 missing 并刷新搜索可见性。曲目和发行行继续存在。这条路径和上述根错误路径都没有清理 `curation_work_states`、`external_links`、发行/曲目自动值、媒体绑定或操作历史。不可见与删除要分别核验。见 `backend/internal/catalog/ingest/scan_feedback_missing.go:12`、`backend/internal/curation/progress.go` 的 visible 联接。 |

一次扫描先按根统计候选文件数，再正式遍历。计数阶段不保存逐专辑文件大小。正式读取在物理音轨和 CUE 父音频上记录大小，解析失败的文件进 held 路径；随后按 scope 组织候选，增量命中会 touch 曲目并跳过候选写入。原有候选签名还含标签、mtime、解析器版本、附属封面与日志，用它的变化推断拷贝会产生误判。基线未记录专辑稳定状态。一次正式遍历能跟上次观测比较，不能仅凭一次读到的快照证明拷贝已结束。见 `backend/internal/library/scan_progress.go:210`、`backend/internal/library/scan_pipeline.go:144`、第 251 行、`backend/internal/library/scanner.go:769`、`backend/internal/library/candidate_signature.go:20`。

## 2. D7 的观测与放行

- 候选写入时将新建或成员已变化的发行置为待确认。扫描终态事务以本轮已观测曲目定位发行，增量沿用与重写走相同的成员归集。同一发行的多碟、多候选先合并。签名对去重后的根 ID、规范相对路径、字节数排序取摘要。CUE 纳入实际 sheet 和父音频，虚拟轨不会把一份父文件重复计数。拷贝保留原 mtime 也能检测到新增成员及文件增长。
- 以完整观测比较。目录枚举出正在出现的支持格式文件，即使它还无法解析，也不能拿“已解析的几首”确认整张稳定。按 organizer 的目录范围检查 held/读目录失败及成员集合，在散曲根按路径限定，在多碟专辑覆盖所有碟目录。首次、新旧发行 ID 变化、成员或大小变化都记 `releases.curation_changed_at`、清除确认。失败、取消、部分可读的扫描可以使旧确认失效，不能授予新确认。成功但所有成员 missing 的发行退出可领集合，保留结论。不能用全局最后一次成功时间代替目标被成功观测。
- 稳定要求最近一次成功扫描的成员签名与上次一致，且最后观测到变化已满 60 秒。扫描内发现文件又长大或支持格式文件解析失败时，登记新的变化窗口。升级前已有数据默认稳定。
- 对待确认项，静默期满由整理协调器通过 app 调用现有 StartScan 进行普通增量扫描，有扫描在跑时让路，同一窗口只发一次。确认与常规扫描共用同一条读取、入库与对账路径。

## 3. 排队投影与领取

沿用 `curation_work_states(subject,target_id)` 保存每项最后的结果。为排队新增独立于结论的 `needs_work` 和可空 `manual_requested_at`。从未处理的目标补行，使用新状态 `pending`、代次和尝试数 0。重排既有行时只改投影/待做位，保留上次结论、摘要、重查期限与代次。`pending` 不计入旧进度的 processed。无需按来源建五份作业表，也无需保存页号。

发行工作状态物化 `queue_class`、`queue_has_id`、四元数组 `queue_gap`、`queue_artist_unlinked`、`queue_added_at` 和 `queue_identity`。主题用 `queue_release_id` 联接发行。扫描单独在 releases 保存 D7 签名、变化时间、确认位。媒体行继承所属发行的排序，歌词和声纹按曲目联接所属发行，艺人取有效主署名中**可见且已稳定**的最高优先发行，无须复制多个艺人任务。艺人待定项的专辑及曲目样本只取已稳定发行。通过艺人家族、有效主署名和有效外部对应求 `queue_artist_unlinked`；艺人认定、合并后重新计算相关发行，避免补出身份后仍抢占队首。

`catalog.CurationEvidence.Classification()` 和 `GapOrder()` 是分类的唯一实现。A 的 tagged_id 仅表示标签中的发行 MBID（散曲为录音 MBID）。排队的 `queue_has_id` 单独投影实际可检索的外部编号、有效条码与曲目标签/CUE ISRC，不把占位值或单纯“艺人已有对应”当作发行强编号。insufficient 走 C 的本地结论及既有路由，不发空词搜索。见 `backend/internal/catalog/curation_classification.go:44`、第 66 行及 `backend/internal/curation/program/release_state.go:35`。

每队列领取一个目标，先限定稳定、可见、存在该主题缺口/工作、重试已到、所需来源可用，然后排序：

1. rank 0 手动触发。沿用 `curation/unlisted.go:14` 的可见性与状态验证，同一事务写到期和手动标记。完成时按领取时读到的请求时间条件清除，处理中到达的新请求保留。
2. rank 1 新入库。首次启用后，等到第一个非空、已结束扫描的目录快照，以条件更新将首页的最大发行 created_at 写入 curation_settings.queue_baseline_at；空库先等待首次入库。此快照是存量，后续扫描创建且 created_at 晚于该边界的发行才享受新入库优先，重启与重新开启不挪动边界。每个主题用发行入库时间与基线比较，结合该主题 attempts=0 计算此档，识别先完成不能让尚待处理的歌词降档。一次扫描新增多张时组内按入库时间倒序。恢复可见、改名、重算摘要均不刷新入库时间。
3. rank 2 有可检索编号，rank 3 有缺口，rank 4 本地完整。只在 rank 3 按 `gap_order DESC`（标题/艺人非真实数、非真实曲名数、缺本地封面、缺年份，逐列比较），只在 rank 4 按 `artist_unlinked DESC`。各档最后均按 `added_at DESC, target_id` 唯一排序。SQL 用 CASE 和 ORDER BY ... LIMIT 1，手动优先各档，不靠 SELECT 后在一页内重排。
4. 独立的程序重查按 `next_attempt_at` 到期取得资格。沿用 C 对 unlisted 的 **90 天后一次、仅程序**重查，沿用 rechecked_at，手动也可重开。no_usable_candidates 等待阻止依据改变。模型待定项保持自己的路由和认领状态。

首次启用、进程重启、扫描结束、外部操作、抑制或来源设置变化，以及定期盘点时，分块读取可见目标，只更新发生变化的排队列。协调器在内存记录已发布的 `LoadProgramChanges` 标记；盘点期间出现新的标记会先完成新快照，再领取。程序自身每次处理完按目标和关联艺人刷新；下一项直接从当前投影取。暂停期间继续保留扫描观测，恢复时先补投影。复用 A 的摘要、C 的离线判定和 F 的自动值失效处理。

## 4. 五条程序队列及依赖

| 队列与单项 | 进入和完成条件 |
| --- | --- |
| 识别：发行或散放单曲的发行行 | 复用 C 的查询和定案。仅到声纹分支才为原有 sampleFour 抽样曲目登记计算需求，交回识别队列处理下一张；所需声纹有结果后再就绪。已取得的查询从缓存读，沿用候选快照、现有对应、署名提案和并入发现。确定身份后立即使封面和相关艺人更新资格。 |
| 封面：发行 | 沿用 artworkGap 与 subjectEvidence。已有可信版本或发行组对应，缺本地封面且外部绑定/文件有缺口；既有对应可直接入队。CAA 及已启用封面插件保留目标顺序、抑制和实际文件检查。散放单曲排除。 |
| 歌词：曲目 | 沿用 lyricsGap，用有效曲名、非占位艺人、有效时长及本地/外部歌词缺口。可用专辑名用于精确查询，缺 MB 对应也可领取。保留精确/搜索回退、候选核对和抑制。见 `program/enrich_media.go:116`、第 202 行（program 指 `backend/internal/curation/program`）。 |
| 艺人资料：规范艺人 | 已有 MB/Wikidata 对应且图片或简介可补/升级时领取。沿用 `program/artist_media_pass.go:47`、第 320、382 行的等级、按名字的插件、无对应时建身份待定项以及重定向/失去署名后的收尾。无需等待整轮发行识别结束。已存艺人链接优先复用，有 MB 辅助读取时和识别共用同一 MB 限速。 |
| 声纹：抽样曲目 | 工作环仅计算本机声纹，复用 app 的 Fingerprints 适配及 `track_acoustic_fingerprints` 的文件快照、算法版本、成功/永久/暂时失败判定。缓存本身就是计算结果和状态，无须再存声纹正文。领取时检查需求的发行识别摘要和代次。已产生的声纹保留在现有文件快照缓存中，重启后由声纹队列取回，再交给识别。AcoustID 网络查询仍由识别按原分支执行，使用 AcoustID 自己的限速器。 |

每队一个 goroutine。保留现有发行并入队列的推进及艺人身份待定项收尾，挂在对应结论提交/队列排空的本地维护中。沿用 WithCurationProgram(false) 的装配开关。每次领取、缓存读取、真正出站（含重定向）、开始计算与写回前都检查相关闸门：总开关、开启者仍为有效管理员、暂停、活动扫描；涉及外部服务时再查源和密钥；各队等待来源、声纹或稳定性时释放执行机会。

## 5. 所有权、事务与恢复

- 一个实例一个进程，每队一个取项循环。并发取证后使用现有 Writer 的行锁、写入前证据复核和步骤派生键。每个步骤独立提交，重启重放相同输入的原步骤键。最终状态、待定项与清待做标记同事务提交。暂停和扫描让路时保留已提交的步骤。
- 封面、歌词或艺人资料的成品已经可见、步骤已提交而结论尚未写入时，盘点核对成品的当前拥有操作、程序批次和步骤派生键，在同一投影事务补写终态。人工覆盖与旧代次成品保留原归属。
- program 批次由进程内互斥保护，保留批次轮换、启动清理与空批清理。

## 6. 来源退避、恢复与响应缓存

`sources/limiter.go` 已有按 Declaration.ID 共用的进程内限速器和对话优先权。基线的清单复用 MusicBrainz 许可。现在清单使用 CAA 自己声明的 1 秒限速器，清单和 CAA 图片共享本来源配额，保留显式注入。MB 识别、艺人辅助读取、第二档模型工具共享 MB 同一个调度入口；LRCLIB、AcoustID、各资料源和各插件沿用各自来源声明。正式标识 M 实测 60 次有 45 次成功、15 次 503，69.589 秒内成功 **0.647 次/秒**，见父任务 `research/m-baseline/live-measurements-2026-11-25.md`。

- 队列调用在 `sources.WithBackground` 的闸门基础上带队列上下文。Requester 将真实出站的来源、HTTP 状态/网络错误类别、Retry-After 和完成时间交给来源调度。遇 429/503 立即结束本次尝试；连续两次暂时网络错误同样进入退避。第二次之前只允许一次有间隔的重试。原先的“后台同 URL 连试 5 次再记住 10 分钟”不与队列退避叠加。无队列上下文的既有调用维持自己的调用合同。
- 每个来源在内存维护 `source, pause_reason, retry_at, penalty_level`。进程启动时恢复声明下限。级别范围 0–8，初值为 0，拥塞升一级，后台冷却取 max(Retry-After, min(10 分钟, 2 秒 × 2^(级别−1)))。实际发出间隔取 max(声明下限, 声明下限 × 2^级别)，本地放慢上限 60 秒；等待结束只放行一个试探请求。真实有效响应后清本次暂停、降一级，逐次回到声明下限；缓存命中不解除拥塞。所有共享同一上游的出站服从同一配额。其它来源继续，程序只有依赖该来源且无法用其它启用源完成的队列暂停。暂停是整条队列暂停领新目标，换一个目标也不能跳过上游冷却。多提供方的媒体沿用当前项的回退：可用的插件或资料源可以先补；若余下来源还在冷却，不得把目标记为“查无结果”。每次等待或重试后重新查开关。
- 正常完成的 404、有效空列表按业务路径记无匹配或 C 的下一档；来源不可用只更新内存中的来源状态，不改该项 attempts/retry_count/generation、已有结果及 90 天重查语义。无效密钥、开关关闭、闸门撤销、请求被取消和本地声纹暂时失败各保留原语义。
- `sources/request.go` 在限速前读缓存，`sources/cache.go` 临时文件加 rename。沿用 JSON 成功 30 天、404/空结果 24 小时，POST 复用只含查询语义的键。重启/暂停发生在响应完整且验证通过后、结果提交前，先完成该响应的缓存落盘，再处理取消，恢复从缓存继续。图片沿用原有下载和成品文件复用。

## 7. 供 D、E 消费的调度接口

B 提供以下三个接口。D/E 接入名字巡检与后续模型路由。

| 接口 | 输入与约定 |
| --- | --- |
| `NextModelKind(ctx, now)` | 维持全实例单个模型运行，先收尾已有会话，再在有可领任务的类别中按“巡检与本地整理 → 第一档候选选择 → 核对判断 → 第二档 MB 查询 → 联网兜底”选取。相同类别按当前优先级、创建时间、ID；类别无可领项就继续看下一类。D 提供名字巡检完成状态，改模型不使巡检重新排队。 |
| `LookupTierReady(ctx, releaseID, now)` | 必须已经完成该发行涉及名字的巡检；同一识别投影中无 rank 0–3 的稳定、可见、已到期待办（含在途及暂等依赖的高优项），且 MB 可用。退避不可伪装成排空。未稳定和将来到期的 90 天重查不占门槛。第二档不满足条件时不建会话、不占运行名额；启动后同一 MB 许可优先级为对话、程序高优、第二档、本地完整。新高优项在下一次许可/项边界重新取得优先权。 |
| `BeforeModelItem(ctx, actor, now)` | 第一项前及上一项提交、用量入账之后，用相同日期口径读取 `TodayUsage` 和 daily_token_limit，达到上限即保留未开始项，返回原因及次日可重检时刻。已开始项允许完成，按项提交，中断只释放未完成项。每步/每次出站继续复查总开关、开启者、暂停、扫描和来源。现有模型运行器在已配置日额度时一次领一项，步骤中继续检查运行闸门。在无限额多项会话内临时设置额度时，已领取的会话可以完成，下一次领取应用新额度。 |

## 8. 进度与验证交接

在管理员 `GET /api/v1/admin/curation/progress` 的旧结构上增加 `queues`。固定五项由识别、封面、歌词、艺人资料和声纹组成。维持现有 state、rule_version、releases（含 try_later、unlisted、no_usable_candidates）、pending、batch、last_pass_finished_at。最后一项时间定义为所有已启用队列在一次投影刷新后完成本轮可领工作时刻；没有装配 worker 时仍给 unavailable。模型详情继续使用既有模型进度。

| 每队字段 | 口径 |
| --- | --- |
| `id, state, remaining, ready` | remaining 数当前可见且有该主题需求的未完成目标，含在途、等稳定、等声纹或等来源，每个目标一次；尚未到期的 90 天重查单列于原结果，不作积压。ready 仅数现在可领的项，使用领取查询的同一资格谓词。 |
| `successful_requests_per_second, completed_items_per_second` | 分别计真实有效成功出站（2xx、通过来源校验，缓存命中和 404 不计）与成功提交项（含完成的无结果项）。按队列在进程内保留最近 5 分钟的分桶计数，以实际观察时长为分母，包含窗口内的来源等待。声纹没有外部请求，前一字段为 null。重启重新采样。 |
| `eta_seconds, pause_reason, source, retry_at` | 排空估时用剩余项数除以实测项/秒，避免用请求/秒代替项/秒。无样本、没有可用速度或未能确定何时解除阻塞时为 null，已排空为 0。暂停可报告 disabled、paused、waiting_for_scan、waiting_for_stability、waiting_for_fingerprint、source_disabled、rate_limited 或 source_unavailable；来源暂停附具体来源和到期时刻。全局状态先反映总开关/暂停/扫描，单源阻塞期间有其它队工作时仍为 running。 |

**迁移和代码位置**

| 文件或目录 | 内容 |
| --- | --- |
| `backend/migrations/0089_curation_queues.sql` | 设置增加首次存量边界，已有工作表增加待做、手动请求、发行排序和关联列。待做部分索引供队列按主题过滤。down 删除新增占位后恢复旧约束。 |
| `backend/migrations/0090_curation_observations.sql` | 发行成员签名、变化时间、确认位；存量按原有成员大小建立基线。视图供领取与模型共用稳定判据。 |
| `backend/internal/library/curation_observations.go`、同目录的扫描流水线、`backend/internal/catalog/ingest` | 复核文件大小、归集 held、在入库与终态事务中维护 D7，调用普通增量扫描。 |
| `backend/internal/curation/program/queue_*.go`、`unit.go`、`pass.go`、`artist_media*.go`、`track_fields.go` | 排队、领取、声纹交接、进度、现有步骤的提交与重放。 |
| `backend/internal/catalog/curation_classification.go`、`backend/internal/curation/work_states.go`、`unlisted.go`、`progress.go` | 编号优先级、结果状态、手动重查和进度接口。 |
| `backend/internal/sources/{queue,limiter,request}.go`、`coverartarchive/client.go` | 来源级退避、后台许可顺序、成功速度、CAA 清单。 |
| `backend/internal/curation/model_queue.go`、`model/queue.go`、`program/model_queue.go`、`pending_items.go`、`model/{scheduler,status}.go`、`backend/internal/app/app.go` | 模型领取类别、额度检查点、第二档闸门、服务装配。 |

**既有测试与合同变化**

- `TestCoverArtLimiters` 要求清单共用 MB 限速器。新的默认值为 CAA 自己的图片与清单共用。
- `TestPostgreSQLCurationProgramGates/{pause_during_lookup,scan_during_lookup}`、`TestPostgreSQLCurationProgramSourceSwitches`、`TestPostgreSQLCurationProgramRestart`、`TestPostgreSQLCurationProgramRestartArtworkReplay` 用没有工作行或空状态判断尚未提交结论；新占位为 pending，尝试数及代次为零。
- `TestPostgreSQLCurationProgramRetry` 和 `TestPostgreSQLArtistMediaProgramPriority/temporary_failure_with_unfilled_gap` 期望来源故障写入项级 try_later。新实现保留结果并暂停来源。
- `TestPostgreSQLCurationProgramRealRescan` 期望单扫后即有识别结论。D7 要求成功确认及 60 秒静默。
- `TestPostgreSQLCurationCProgress` 做整结构比对，新增固定五项 queues。其规则版本 101 与基线 102 的分歧、`TestPostgreSQLB3AcoustIDProgram` 两个无效密钥子例的 search_only/list_truncated 差异，已在未修改的基线上复现。
- `TestPostgreSQLCurationProgramEndToEnd` 也把进度规则版本与资料主题的 StoredRuleVersion=101 比较。运行时识别规则为 102；失败点早于该用例末尾的 HTTP 次数断言。
- 预算由项间检查，原先要求在某一项的中间停止的断言也需由测试代理按此合同复核。M 两种模式的 after-f 对比归后续测试阶段。

**实施验证记录**

- 在 `backend` 下运行 `mise exec -- go build ./...`、`mise exec -- go vet ./...` 及 `mise exec -- go test ./internal/curation/... ./internal/sources/... ./internal/library/... ./internal/catalog/...`。构建和静态检查通过，指定包测试仅 CAA 旧限速器断言失败。
- 独立 PostgreSQL 使用 `mise exec -- env ROOMUSIC_TEST_RUN='…' ./scripts/test-integration.sh` 精选既有用例。通过的路径包括扫描让路后恢复、扫描取消、held 与 missing 对账、声纹缓存与暂停、曲目字段补全、模型领取刷新与重启回收。上列旧合同断言保留。
- 1000 张合成可见发行均待识别时，默认 PostgreSQL 配置下，单项领取查询执行 3.702 ms，高优先级存在性检查执行 0.155 ms。数据、查询计划与上游 HTTP 实测是不同口径。
- 在隔离库保留已提交的程序操作并延后首次终态写入，封面、歌词、艺人资料分别恢复为 done，均为第 1 代、尝试 1 次。操作条数分别保持 1、1、2。
