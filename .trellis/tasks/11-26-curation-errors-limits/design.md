# 子任务 6 技术设计

适用父任务 A2.4、A2.7 最后一行、A3 与“运行时哪些是并行的”，规则见 `docs/architecture/adr/0009-curation-chain-review.md` 第 4.4、4.7、5 节。一次仍只识别一张专辑。把失败留在发生的步骤，把下一张专辑的 AcoustID 查询交给已有声纹队列。

## 一、改动边界

下列 Go 文件按所在目录列出。子任务 5 拥有 S8 的步骤顺序、准入、候选判定、结论写入和版本升级。本任务在它的步骤入口接入断点，在错误出口保存等待或查询出错。模型报错归子任务 7，界面归主代理。

| 位置 | 涉及文件及职责 |
| --- | --- |
| `backend/internal/sources/` | `queue.go` 管来源暂停与恢复通知，`limiter.go` 管固定发送间隔和竞争许可，`request.go`、`sources.go`、`acoustid/client.go` 区分上游故障、请求响应无效和密钥错误。复用 `cache.go` 的键、有效期和日志 |
| `backend/internal/curation/program/` | `match_search.go`、`match_lookup.go`、`release_route.go` 接入 S8 步骤和错误，`match_snapshot.go` 只接入名字查询详情的错误出口；`pass.go`、`release_state.go` 接入发行等待、终止和失效；`queue_projection.go`、`queue_query.go`、`queue_worker.go`、`worker.go`、`queue_progress.go` 管领取、按来源唤醒与进度；`queue_fingerprint.go`、`fingerprint.go` 管抽样、预查和消费。优先级沿用 `model_queue.go` 已有调用入口 |
| `backend/internal/curation/` | `work_states.go` 读写工作状态，`progress.go` 汇总等待和查询出错。沿用 `model_queue.go` 的 `QueueRankSQL`，`source_finders.go` 的来源闸门及密钥状态回写 |
| 本地声纹 | `backend/internal/playback/fingerprints.go`、`backend/internal/catalog/track_fingerprint.go`、`backend/internal/app/fingerprint.go` 移交技术失败的重试期限；保留文件快照校验、CUE 区间和成功指纹 |
| 表、接口样本、规范 | `backend/migrations/` 一对正反迁移；`backend/internal/platform/postgres/migrations_rollback_integration_test.go` 为既有回退用例登记新约束取值；`backend/internal/app/testdata/api-samples/curation/progress.json`；`backend/internal/app/testdata/api-samples/diagnostics/admin.json` 的迁移号；`.trellis/spec/backend/sources-contracts.md`、`.trellis/spec/backend/curation-contracts.md`。具体测试入口见同目录 `implement.md` |

## 二、查询错误及来源暂停

| 本次 S8 调用的结果 | 去向 |
| --- | --- |
| 已验证的有效空列表、404 | 给子任务 5 的步骤返回“无结果”。前置条件不足的步骤由其准入函数跳过 |
| 内容与请求不相容，包括 200 但 JSON 或必需字段无法解析、实际查询参数被拒、本地解码或计算失败 | 返回携带步骤与来源的专辑错误。本地计算以 `local_fingerprint` 标识，和远端 `acoustid` 分开 |
| 网络暂时失败、超时、429、503、AcoustID 明示服务繁忙 | 返回共享 `QueuePause{Source, Reason, RetryAt}`；记录真实失败源。声纹候选的 MusicBrainz 详情失败仍归 `musicbrainz` |
| AcoustID 被关闭、未注册、密钥被拒 | 复用 `fingerprintStep`、`ErrInvalidKey` 与密钥闸门，声纹步骤返回 `unavailable`，释放其依赖，继续 S8 |
| 整理暂停、扫描开始、取消、数据库失败 | 原样返回相应控制流；不更新本轮计数 |

在发行识别和声纹工作的队列上下文里携带查询用途，`Requester` 在仍能区分 HTTP 状态和解码结果时标注“响应无效”。已建立连接但读取中途断开按网络故障处理；收到完整响应后解析失败才归该发行。该标记要穿过 `decodeResponse`、`sourceFailure`、`found`，优先于 `IsSourceUnavailable` 判断；对话、封面、歌词和模型调用沿用各自错误映射。现有 `found` 将 `ErrInvalidQuery` 也变为空，识别链只折算真正的查无。AcoustID 的 200 业务错误 5、13、14 仍按临时来源错误处理，4、17、6 仍走密钥分支。任一步出错直接返回，跳过弱线索、匹配写入、未匹配标记和模型分流。短重试中的多个 HTTP 尝试只产生一次步骤结果。

沿用请求内短重试、取消和放行检查；429/503 含首次最多 5 次、总预算 10 秒，超出预算的有效 Retry-After 交给来源。队列原有首次网络错误放行一次的处理保留。耗尽后 `QueuePolicy.failed` 以来源 ID 计连续级别，按 5、10、20、40、80、160、180 秒递增，此后保持 180 秒。最终恢复时刻取本地暂停期限和上游指定时刻的较晚者，支持秒数与 HTTP 日期；已在暂停时读取该状态不能再次升级。真正出站得到有效响应（包括正常空结果、404）后清零；磁盘命中不能证明来源恢复。去掉 `Limiter.level`、`setLevel`、`effectiveInterval` 及请求层两处等级联动。沿用按实际发送时间算的 `MinInterval`、串行许可及每次重试重新领取许可。下调队列上限时注意 `SourceRetryDelay` 也供模型项级 `NextSourceRetry` 使用，在来源策略处封顶，避免顺带改变模型重试。

## 三、保存断点、重新领取与展示

在已有 `curation_work_states` 的 `subject='release'` 行使用 `status='try_later'` 表示等待，新增允许的终态 `query_error`。继续使用 `retry_count`、`next_attempt_at`、`generation`、`match_rule_version`。在已有 JSONB `result` 中合并一个 `retry` 对象，其余键保留，特别是 90 天程序重查的上下文。字段约定如下。

| 字段 | 用途 |
| --- | --- |
| `step` | 出错的 S8 步骤，使用稳定名字 `tag_id`、`disc_id`、`barcode`、`catalog_number`、`isrc`、`acoustid`、`name`。名字步骤内按子任务 5 的同一顺序重入 |
| `kind`、`source` | `source` 表示来源暂停；`album` 表示只与本张有关。来源取 `musicbrainz`、`acoustid` 或 `local_fingerprint` |
| `reason`、`pages` | 固定原因码供进度/诊断；如后续判定需要前面步骤的查询依据，沿用 `SearchPage` 保存已完成步骤的页摘要。完整 HTTP 正文只存在来源缓存 |

领取时读发行行，同一文件输入、代次、识别版本才可使用断点；用子任务 5 的本地文件变化判据。有变化或升级则从首步开始，清断点和计数。只有先前步骤确实查空或无适用线索才能抵达后续步骤；恢复直接进入记录的步骤。步骤内多次读取（例如名字搜索、随后读候选详情）重新用同一个 finder 和相同参数构造请求。GET 缓存键是方法及规范化 URL 的哈希，AcoustID POST 还包含时长与声纹。有效返回 30 天、404 和有效空结果 24 小时，先检查来源开关再读缓存，命中不占许可。中断的请求没有有效缓存，重新出站；同一步已取回的详情或搜索页在有效期内直接命中。即使前面整步的缓存已过期，断点以前的步骤仍不重发。结项清理 `retry`，沿用原写入路径保存最终证据。

例如：条码查空，ISRC 找到候选，读候选详情时 MusicBrainz 返回 503。事务提交 `try_later / isrc / source / musicbrainz`。到期后重入 ISRC：条码调用为零，ISRC 搜索页命中缓存，只重发详情。步骤错误上报及该条 `try_later` 写入要在 `pass.go` 当前提前返回 `sourceFailure` 之前接住；检查当前代次、文件输入、规则版本及手动请求标记，避免写回过期快照。

- **来源等待。** 写 `next_attempt_at=QueuePause.RetryAt` 并保留原专辑错误次数，`attempts`、`generation` 不递增。调度器以最近到期项/来源期限设定唤醒时间，排入已有队列试一次实际工作；持续失败只延长该来源的门槛。除明确更久的 Retry-After，专辑没有额外超过 3 分钟的等待。改掉按整个主题 `cooling(subject)` 跳过所有专辑的条件，暂停只挡要访问该源的工作，其他专辑、其他来源可继续。长 Retry-After 到期以前对该来源的队列出站均受闸门限制。
- **恢复事件。** 让 `QueuePolicy.recovered` 在原先有暂停且真实请求成功时通知调度循环，和“成功请求数”回调分开，因而 404 也能唤醒。只对 `try_later + retry.kind=source + retry.source` 对应的行提前到期，保持步骤、输入和代次，唤醒外层空闲等待和内层领取循环。领取仍核对当前冷却；处理恢复事件时若已有更新的暂停，以新期限为准。过期没有其他成功流量时，由到期的正常请求探测，禁止等待恢复通知才出站。重启从发行等待行的未来期限按来源恢复闸门，尊重已记录的长 Retry-After；短退避级别沿用进程内策略。重启后、首次后台探测前，界面请求成功仍等持久化的原期限；到期由正常后台请求探测并恢复。
- **专辑错误。** 每轮真实执行失败，按同一发行和输入记一次，建议前两次复用 5、10 秒，到期只重试出错步骤。来源故障、纯缓存读失败、取消、重复盘点不计次。步骤恢复并取得有效结果后清此前连续次数，即使随后另一来源等待也不能继承旧步骤的失败。第三次写 `query_error`，只累计专辑错误次数，沿用原识别代次与结论次数，保留失败源、步骤、原因，清 `next_attempt_at`、`needs_work`，停止识别。实际文件变化或识别规则升级才重启；缺封面、歌词更新、重新盘点、来源恢复、90 天到期都不能激活。保存终态时确认同一领取快照，只完成一次。已有 `try_later` 的封面等其他主题维持各自语义。
- **展示数据。** 现有管理员整理进度接口保留 `releases.try_later` 并增 `releases.query_error`，分别统计等待和已停下的发行，等待不计入 `processed`。队列继续输出 `source`、`pause_reason`、`retry_at`；持久等待项应计入 `remaining`，只有可领取的计入 `ready`，同一主题仍有工作时不能把全部就绪数清零。当前来源窗口由进度返回，重复 180 秒窗口仍可显示“来源不可用”。交给子任务 5/主代理的单张状态投影为 `status`、`retry_source`、`retry_at`（来自本行），界面据此显示“等待重试 · MusicBrainz”等。避免把某条队列最后失败的来源套用给每一张专辑。

## 四、同源竞争与声纹提前量

优先级仅在多个调用已经等同一来源的下一次许可时比较。按用户确认的四档执行：界面交互 → 普通识别 → 本地齐全的低优先识别 → 艺人资料。以 `QueueRankSQL` 已计算的 rank 为准，手动/新入库等提前的专辑遵从实际 rank，rank 4 属于低优先识别。在 `processQueueItem` 给 `artist_media` 设置单独的来源优先级；识别和为其预查的声纹沿用所属发行档次。不同来源照常并行，已经发出的请求完成后再决定下一许可。模型自查由其拥有任务移除；过渡期可让旧 `QueueModelLookup` 与中间档使用相同许可等级以兼容调用方，不改模型调度。

1. 在 `projectReleasePage` 使用与 S8 共用的本地准入和前五步取参（发行编号、可用 TOC、条码、目录号与厂牌、抽样 ISRC）。只对稳定、可见、本轮需要识别、可用 AcoustID、且这五步都没有可查输入的专辑提前登记 `sampleFour(e.SortedTracks())`。已对应且无须识别、名字不全而 S8 不准入、播客等按上游准入结果排除；保留允许用声纹的双占位特例。不能拿较宽的 `HasSearchID()` 或“标题差”替代上述条件。前面有可查询线索的专辑，由识别真正查空到第六步时才登记。90 天重查按子任务 5 的 S8 资格同样处理。
2. 在事务先写发行投影，再登记现有 `subject='fingerprint'` 曲目行。复用 `queueFingerprints` 的代次、`queue_identity`、可见曲目检查，改成可用该事务且同次请求幂等；重复盘点不能将已完成曲目再置 pending。未来到期的重查在首次变为可领时也走同一登记入口。识别选取继续用 `dependencies_ready` 略过等待声纹的一张，先取其他就绪发行。
3. 声纹队列串行计算本地指纹，用受闸门保护的 `FingerprintFinder.LookupFingerprint` 查 AcoustID（限额每秒 3 次），预查只访问 AcoustID。扩展现有 `computedPrint`，保存该样本的有效命中列表、有效空或不可用标志，按发行、代次、输入和曲目核对。释放数据库事务后才做外部调用，写完成时再比对代次/摘要。识别消费这些结果，沿用原有抽样比例、分数和交集规则，仍由识别队列按需读取 MusicBrainz 详情、判定和写入。进程重启丢失内存结果时，由声纹队列重新装载本地指纹及 AcoustID 的响应缓存。结束本轮/输入变化时清除内存结果；下一轮不能因存在旧 done 行而直接认为新一轮已备好。
4. 对声纹错误按第二、三节处理。提前登记的发行已确定会走第六步；按需登记的发行已经走到第六步。失败时将来源等待或本地失败结果归到父发行的一次声纹步骤。以发行锁和领取时的代次、输入、状态原子暂停该轮剩余样本，收回未完成依赖；一次出错算一张一次，重排该步时只补缺少的工作。已有有效样本走缓存。密钥中途失效或来源关闭，释放该发行依赖并跳过声纹；读预查结果前同样检查开关。查询有误的空值不能当作已完成的空列表。
5. `track_acoustic_fingerprints` 现有读取、更新 SQL 都硬编码 24 小时/最多三次，播放侧还会返回 24 小时建议并将旧失败读为永久。真实计算错误由发行控制重试：读取缓存只复用有效指纹和确定性 `too_short`，对 `decode_failed`、`timeout` 允许在发行下次实际领取时重算并覆盖旧失败。去掉对应更新谓词和重复的曲目级停查判断，保留文件与 CUE 快照比较。`too_short` 仍是确定性排除；输入未变且实际解码失败要向上返回本地错误，不能借用旧失败凑足三次。扫描使文件版本变化时走输入失效再投影。无需修改 fpcalc 或声纹算法版本。

## 五、升级、兼容与回滚

- 使用已有表及 `result.retry`，只需在 `backend/migrations/` 新建 `NNNN_curation_query_retry.sql` 及同名 `.down.sql`（NNNN 取集成顺序中的下一号，当前基线末号 0097）。正向扩展 `curation_work_states_status_check` 允许 `query_error`，本分支只增加 query_error，主代理在合入时与子任务 5 合并最终允许的状态集合。
- 旧发行 `try_later` 无断点且次数混用。正向将这些行的 `retry_count` 归零、旧期限截到升级后最多 3 分钟，第一次到期从 S8 首步开始。保留原查询时间、重查上下文和其他主题。旧指纹行按既有快照键读。规则版本沿用子任务 5 的一次识别升级（基线 `MatchRuleVersion=104`，合并目标 105）；子任务 6 不另升识别、本地或资源版本。已匹配的升级路径归子任务 5。
- 回退时用配套 down 先把新终态映射到旧版可读的 `try_later`、到期可领；对本任务断点行清 `retry`，重置项级错误次数，保留结果其他键。已记录的未来来源期限应遵守到时，旧版随后从头读缓存。旧版声纹工作者会按原流程取用本地指纹，已提前取得的 AcoustID 响应仍按其原键命中。恢复上一版状态约束，使用与数据库版本配套的程序。回退丢失断点和新的终止语义；外部对应、人工层、曲库文件和有效响应缓存无需回滚。只在迁移隔离库验证。

## 六、范围核对

| PRD 事项 | 设计落点 |
| --- | --- |
| 出错不算结论 | 二、三：错误出口先于弱线索和结论 |
| 来源暂停 | 二、三：来源级别、Retry-After、到期试查和进度 |
| 专辑等多久 | 三：同输入断点、缓存、期限和恢复通知 |
| 只和这张有关的错误 | 三、四：真实失败最多三轮，文件或规则变化重开 |
| 声纹来源关掉、密钥失效 | 二、四：跳过、解除依赖，临时错误等待 |
| 去掉放慢发送 | 二：固定来源声明的间隔 |
| 抢来源的先后 | 四：按同源等待者授予许可 |
| 提前查 AcoustID | 四：前五步无本地线索才预查，按需登记与预查共用队列 |
| 空等 | 子任务 1 的固定样本中，确认扫描耗时 91 毫秒，扫描结束后首请求又等约 29.9 秒。程序遇到 `scan_running` 按独立的 `ScanInterval` 复查。生产默认每 2 秒复查一次，显式配置可覆盖；发现扫描结束的这一轮即可领取 |
| 规范 | 实施时同步来源合同第 4、5、8a 节，整理合同第 15.1、15.6、16.2、16.4、23.5、25.1、25.3、25.5 节相关条款 |
