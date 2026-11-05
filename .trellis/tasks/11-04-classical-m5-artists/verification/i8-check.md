# I8 增量检查报告

## 一、范围、合同与结论（2026-11-05）

本次为 trellis-check 子代理只读审查，不再派生代理。范围仅为稳定的 N+1 切片：`artistgraph/facts.go`、`searchindex/batch.go`、`values.go`、`write.go`，以及 `searchindex/batch_integration_test.go`、`values_test.go`、`artistgraph/search_batch_integration_test.go`。参考现有单轨 Apply/RefreshTrack、状态与数据库约束、metadata拆分实现及search/database spec；**未审正在修改的I8 harness、maintenance诊断/提交策略，不改owner文件**。旧 `backfill-b-check.md` 及失败证据保留。

已读取联合设计 §15.2b 末尾I8修订：完整分布CATP p95≤200ms、p99≤500ms；最大值记录归因，不再作为中途停止条件。旧max≤1秒是主会话设定而非用户要求。另须联合回填≤36分钟、回填期M3全部成功且p95≤1秒、扫描全部成功、播放/搜索无503，以及原5warm30/搜索/完整队列验收。局部异步提交仅获有条件许可，不能扩大至M3/扫描/权威事实写入；本切片没有设置 synchronous_commit。

**严重性结论：未发现本切片内可证实的新生产bug或合同违背。静态审查可继续进入回归/诊断，不等于测试或I8性能PASS。** 本人未执行DB、测试、race、编译、vet或benchmark；当前前后诊断仍由maintenance owner独占。

## 二、语义、锁与事务覆盖矩阵

| 项目 | 文件证据与判断 |
| --- | --- |
| 精确变更集合 | facts仍沿原可信observation/evidence/inferred条件计算CanonicalTrackArtist，只有display与当前artist不同才收集changes。JSON UPDATE只包含这些轨；后续RefreshTracks不包含未变化轨。无变化重刷不会额外推进搜索revision或重建搜索行 |
| 完整分块 | facts的UPDATE和RefreshTracks均以128为单批、遍历全部集合，不是总数上限。没有截掉第129项之后的署名、轨或credit；仍可能在同一外层事务累计很多块，128不能证明CATP耗时上限 |
| 锁与原子性 | 全部SQL使用调用者同一`*sql.Tx`和ctx，不另开事务/提交，不新增OPER或advisory锁。仍要求调用者预先持CATP；批量UPDATE的行锁物理顺序不能声称与旧逐轨相同，但没有增加跨CATP的锁序反转。错误向上传播，由调用方整体回滚，不能在错误后继续提交 |
| 版本与代次 | 非空RefreshTracks加载一次State并先检查CurrentVersion，不在未知版本下推进revision。CATP覆盖期间目标状态稳定，按同一TextnormVersion/TargetGeneration写全部chunk。未更改ProjectionVersion、发布状态或epoch；空集合直接无操作 |
| input_revision | UUID先验证、转小写去重再排序；每个显式ID仅执行一次insert=1或既有revision+1，大小写/重复输入不会重复Touch。graph唯一生产调用点传库中真实UUID及实际变化集合 |
| 完成marker | INSERT RETURNING获得新revision及旧applied字段；与Apply相同，同代且applied_revision已等于新revision时跳过投影重写。其余ID完成值写入后才一起更新search_applied_generation/input_revision/search_title；marker、值和事实同事务 |
| live_updated | sealed时只计当前build清单之外的去重ID，已在清单的不计；未sealed不增加。沿用逐轨RefreshTrack每次显式变化的计数语义，不是全生命周期唯一对象数。即使Apply式同代marker跳过，live规则仍和旧路径一致 |
| 多名与credit | 单对象/批量共用appendValue、appendCredit、normalizeValues。release_artist/album_artist/track_artist按既有CanonicalArtistNames保守拆分、精确去重并保留输入顺序position；credit仍保持整条原文和原position，不因含斜线被当作艺人展示拆开。四角色验证、标题/CatalogNumber规范化、ShortGrams及非空normalized写入规则一致 |
| unknown / missing | unknown UUID在两条路径都生成空完成marker、空search_title，没有伪造track或值；有效轨FK防止不存在轨存在合法credit。missing不被批量加载过滤，仍保留值和marker，由读取层按状态排除。空字段且无credit可完成空投影 |
| 原文恢复及后续graph | 原可信原文选择、placeholder条件、角色事实、ensure和候选路径未因批量更新被替换。facts→effective→profiles→候选→release marker仍在同一外层事务。早先逐轨更新改为先收集后统一更新，不允许在后续名字校验失败时保留已更新搜索值 |

共用造值提取不是从HEAD首次引入多名规则的产品变更：本轮基于已实现的⑤多名搜索合同审查，不能将整个未提交工作区相对HEAD的差异都归因于I8。批量与单对象共用实现可以防漂移，但两者一致本身不足以证明共同实现正确，因此同时检查了独立的原文/角色/规范化断言及现行spec。

## 三、新增测试断言审查

1. **共用造值纯测试**：显式检查Zulu/Alpha顺序与重复去除、位置重置；credit原文两侧空格/斜线及position=7保留；繁简/全角标题、目录号规范化；4097字节/非法UTF-8报错及未知credit角色拒绝。没有仅以调用无错误代替语义断言。
2. **批量/逐轨对照**：260个真实轨加1个unknown，覆盖3个chunk、missing、空字段、四credit、封闭清单内外对象；单轨执行后保存完整业务快照，回滚保存点再批量执行，比较去除随机搜索行ID后的values、全部markers和live。额外输入重复与大写UUID；断言261个marker目标代次/revision完成，后续Apply明确不重写。
3. **后chunk失败与回滚**：129轨的最后一轨标题超预算，预期原文错误；首块虽已执行，回滚保存点后markers/values/live必须恢复。另检查未知版本不推进revision，以及成功批量调用后仍可由调用者回滚，防止内部提交。
4. **取消回滚**：真实搜索INSERT触发器延迟，200ms context到期要求错误且ctx已取消；外层rollback后查询原revision/applied marker及空投影，证明不是只检验函数返回。该测试在同一chunk取消，没有宣称覆盖所有chunk/COMMIT时刻。
5. **graph真实调用**：130个实际需恢复显示署名的轨（含missing），断言130个目标代次marker、每轨标题+两名字+四credit共7值及全部恢复后的显示名。另建未变化轨，检查revision和搜索行ID都不变；再次Refresh检查恢复轨revision不再增加。因此并非直接调用batch而绕过graph调用点。

上述为**代码中存在的断言**，本检查未运行，未新增PASS数量或耗时声明。

## 四、仍未直接覆盖的边界与后续验证

- 新增批量对照主要在manifest_sealed=true下验证；未sealed的live不增分支、异常“applied_revision预先等于递增后revision”的跳过分支、非法UUID拒绝未见本组三文件的专门断言。当前静态上与单轨路径/输入前置校验一致，列为覆盖边界，不虚报成已执行测试。
- 新graph测试覆盖可信observation恢复，没有专门组合多条raw evidence、inferred未知艺人占位及不同治理路由；相关既有graph回归仍应保留。未知UUID测试与“未知艺术家”占位不是同一项，不能相互替代。
- searchindex取消/后块失败验证了该端口的事务回滚，但新增graph测试未制造“显示名UPDATE之后、后续graph错误”的完整外层故障。静态确认沿用共同事务及错误传播，不声称本组新增集成测试已覆盖该故障点。
- 新造值测试对超长/非法原文的直接验证使用normalizeValues；艺人展示字段先走既有CanonicalArtistNames，对其单值输入预算仍依赖原写入边界。本轮未新增或放宽该规则，不把这一个纯测试说成所有调用层的输入校验证明。
- 批量减少数据库往返，不等于所有输入规模的查询/持锁成本已达标。真实前后计划、全部提交分布、正式在线请求指标与回填墙钟均待owner/主控；未读改动中的harness，不对其I8口径正确性提前作结论。

实际执行仅文件读取/检索（cat、sed、rg、find、git diff/status、ls）及新建本报告；未修改生产、测试、spec、前端或旧B报告。收到稳定harness/诊断证据后再另行追加，不占用当前独占DB。

## 五、UUID依赖最小修复复核（2026-11-05）

主控指出本报告初版读取的 `batch.go` 新增platform/uuid依赖不符合searchindex allowlist；初版未抓到该包边界问题，不能将第二节“UUID先验证”继续当作当前实现。

最新文件已移除platform/uuid、errors及uuid.Valid分支，仍按小写去重/排序、每128项完整处理，参数经`$1::uuid[]`交给PostgreSQL校验，不拼SQL、不忽略转换错误。未知**合法**UUID仍走原来的空marker语义；非法文本不再在Go预检，可能在后一个chunk报数据库错误，必须由调用者回滚整个事务。唯一生产调用点输入来自数据库UUID，未新增对外宽松接口或自制UUID算法。

`dependency_rules_test.go` 的searchindex allowlist仍为textnorm/metadata/releasesql，未加入platform/uuid或豁免分支。**依赖问题静态已关闭**；主控报告无DB的TestPackageDependencyDirections PASS 0.028s，本检查未亲自运行。第二、四节关于Go前置UUID校验的旧描述由本节取代。

## 六、稳定I8 harness审查：一项P2待修

本节新增范围为 `catalog_scale_benchmark_test.go`、`catalog_scale_acceptance_test.go`、`catalog_scale_acceptance_unit_test.go`、`catalog_scale_diagnostics_test.go`、`catalog_scale_load_test.go`；没有运行它们。主控转述owner的15项纯测试PASS不作为本检查执行结果。

### P2：最终summary早于部分资源清理，可能与真实失败状态不一致

**证据**：benchmark中的diagnostics.Close defer约在260行注册，应用WaitForScans/worker清理约在324行注册，最终FirstError补记/writeReport defer约在417行才注册。Go逆序执行defer，因此最终summary已写出后，前两项清理才可能通过`b.Error`报告失败。成功测量路径下，可能留下`summary.Pass=true / FirstError=""`，而最终benchmark实际FAIL。即便将报告移到最后，现有补记条件 `!report.Complete || len(report.AcceptanceFailures)==0` 还会在已有性能失败且采集完整时跳过清理错误，不能仅靠总b.Failed区别“纯验收失败”和额外清理失败。

建议owner统一清理与最终报告顺序，清理错误显式写FirstError，在所有应纳入结果的清理之后封口summary；保留此前性能失败，不让后续错误覆盖首因。纯回归应模拟“测量全通过→晚清理失败”以及“性能已失败→再清理失败”，要求最终Pass=false并保留操作错误。**已即时反馈主控，未抢改文件；此项未闭合前不出harness无问题结论。** 这不表示本次实际发生资源泄漏，而是由文件中确定的defer顺序证明报告可能失真。

### 其余合同与旧问题复核

| 检查项 | 当前静态结果 |
| --- | --- |
| 完整CATP分布 | 所有已成功提交的应用批持有值合并后计算nearest-rank p95/p99；复制再排序，不平均各phase分位数。必须complete、非空、无未确认提交和EvidenceError才允许PASS |
| max仅记录 | COMMIT仍立即计样本；max保存transaction/phase/PID、acquired/committed、CommitMS/BeforeCommitMS，不因>1秒调用guard。不把COMMIT墙钟直接解释为fsync原因 |
| 36分钟 | 从worker调度到双方ready验证完成取时间，超过36分钟recordCheck失败但不取消或跳过后续验收；原36分钟timer已不在主循环。总预算可配置，默认90分钟；单HTTP仍45秒，未随总预算变大 |
| M3全样本 | HTTP统一按PATCH corrections分类，9900与周期探针均进入同一原始耗时数组；全部200且整体p95≤1000ms才通过，非子集p95平均。统计保留失败响应，不用重试成功覆盖 |
| 扫描与可用性 | scan_start必须全部202且每个cycle真实succeeded和本轮目录核验，数量对应且非零；播放/搜索全部响应统计503和其他非2xx，零样本不能通过。真实状态/HTTP错误路径仍立即guard失败，不以继续收集性能为由吞掉真实故障 |
| 统一最终判定 | backfill/catp/m3等recordCheck只累积Checks与AcceptanceFailures；已失败项不可被后续PASS覆盖。全部26组建议/三栏结果/计划、深页及两种完整队列完成后finishCollection，缺项不通过；性能超限不再在查询循环提前Fatal |
| 原P1保留 | 成功进度UPDATE且RowsAffected>0识别应用事务，COMMIT时即入统计，日志只唯一同phase确认；markFailed/发布不覆盖未消费提交，backfill_failed立即停止，callback在释放diagnostics锁后调用。提交后LoadState失败不能让该批消失 |
| 原P2保留 | 艺人阶段探针要求扫描前、扫描后修正前、真实PATCH/回读后均满足search ready / artist building；回读revision和label必须真的改变。发布夹在GET与PATCH间不计覆盖，202不当完成 |
| 采样退出 | 成功路径join在线load并等待scan结束后stopOnline，取消并等待sampler、关独立monitor、关在线recording，再AnalyzeSchema/EXPLAIN/预热/正式计时。在线HTTP集合因此不会混入后续正式请求 |
| 错误停止与证据 | 未确认批、phase错配、生产backfill_failed、真实SQL/HTTP/目录验证错误仍走guard或Fatal；首因保留并取清理前现场。唯一统计更新helper、原规模、原查询矩阵和5warm30未被I8改为缩样通过。资源最终报告仍受上述P2影响 |

纯单测代码明确覆盖p95/p99边界、max>1秒不取消、缺证据不通过、整轮9900/M3集合、503与仅202失败、前置/后置building、性能失败后后续项目仍登记及总预算独立。它们主要驱动统计/collector回调，不是实际36分钟超限运行或真实后端在线成功证据，也尚未覆盖上述最终报告/清理顺序。

本轮仍仅文件读取与报告追加，未DB/普通测试/race/vet/编译/benchmark。N+1诊断、真实提交分布、异步提交适用写集及集成测试由各owner/主控后续提供；不能据本报告宣称I8正式验收通过。

## 七、最终清理P2定点复核：静态闭合（2026-11-05）

本次仅刷新三份scale文件中最终报告/资源清理增量及新增三个纯测试，顺带读取NewSchema的Cleanup报错接口；没有重新审查全部SQL/harness，也未运行DB、测试、编译、vet或性能。

**第六节P2已由当前实现闭合**，修复前证据保留但不再代表现状：

- benchmark在创建schema、合成目录及注册资源清理之前，就用`scaleRegisterFinalReport(b.Cleanup, ...)`注册最终报告。函数defer先执行，随后testing.Cleanup逆序执行，所以诊断关闭、连接关闭、worker/scan退出、目录删除及schema删除的已报告错误先进入report，最后才封口summary。最终持久化仅访问内存统计与证据文件，不依赖已关闭数据库。
- 各明确清理错误经过`cleanupError`/`recordRunError`进入独立`Errors`集合，`FirstError`只保留第一个操作/清理错误，`FirstFailure`不覆盖先前性能或操作首因。性能失败已有记录也不抑制后续清理错误；重复读取guard首因不会反复追加同一错误。
- schema使用`scaleReportingTB`包装NewSchema，转发真实testing错误并记录cleanup.schema；自有临时目录通过显式RemoveAll清理登记错误，不再仅依赖testing内部失败位。堆采样goroutine新增取消并join，避免最终报告与尚在运行的采样交错。
- 过程快照保持`Finalized=false`，最终Cleanup设置`Finalized=true`并综合Complete、FirstError及AcceptanceFailures判定Pass。最终写报告回调返回error而不是Fatal，失败记录`report.final_write`并使测试报错，避免在最终清理中Goexit。
- 新三项纯回归分别覆盖：模拟defer+Cleanup的真实顺序（worker→diagnostics→directory→schema→report），同时检查“测量通过后清理失败”和“性能失败再清理失败”；原操作首因与多个清理错误并存且去重；最终报告写入失败不得静默。测试同时断言序列化后的Finalized/Complete/Pass/Errors，而非只检查内存回调已调用。

**取证边界**：Finalized表示最终汇总阶段已到达，不等于所有清理成功；需同时读取Pass/Errors。磁盘写失败时无法保证错误再次落入同一不可写文件，应以测试退出状态/日志为失败证据，不能将此前`Finalized=false`的过程快照当最终PASS。共享NewSchema内部未返回的admin.Close错误并不由包装器新增检测；本次闭合的是明确报告的清理错误在summary封口前汇入，未声称所有外部资源故障都已实际模拟。

主控转述owner无DB race PASS，本检查未核验其完整执行日志，不猜测最终测试数量或耗时。仍待主控真实回归、诊断及I8正式验收，**不宣称性能通过**；本次只追加本报告，不改owner文件。

## 八、maintenance自适应让出切片复核（2026-11-05）

范围仅 `pacing.go`、`pacing_test.go` 和 `worker.go`/`artist_worker.go` 的让出增量；为核对时序，只读取processBatch/processArtistBatch的事务入口与提交出口。未读正在诊断的profiles，不重复旧审查，不运行DB或测试。

**结论：未发现本切片新增的确定性合同、锁序或取消处理错误。** 按本轮最新授权接受基于实测的自适应正时长让出；现存design/spec“至少固定50ms”须由主控同步，本文不擅改规范，也不据旧措辞拒绝已授权改动。

- `batchYield(duration)` 对正耗时严格取 `clamp(duration/4,10ms,50ms)`，零/负等无有效批耗时返回保守50ms。使用time.Duration除法后再夹限，没有乘法溢出或负数变成忙循环；每次仅取当前实测，长批之后不永久锁定最大等待。
- 两worker都先等待process返回成功，再计算计划yield并最终wait；真实应用批的Duration在COMMIT成功后设置，故让出不持CATP。空批事务亦已提交/回滚后才执行publish/repair；成功发布直接退出，无需结束后再等；未发布的修复路径继续正等待。保留维护选主锁只排斥其他worker，不等于睡眠期间持目录写gate。
- Duration沿用从Begin前到COMMIT返回的整批墙钟，含取锁等待和提交耗时。它可作调度依据，**不是CATP精确持有时间**；慢锁/提交尖峰仍让出50ms。没有把等待放到事务内部，也没有新增锁、事务、goroutine或并行批。
- `wait` 沿用timer/context select并defer Stop；取消可以提前退出，错误向worker上传，不需要等计划yield全部完成。timer与取消同时ready时select不承诺取消优先，但后续数据库操作仍使用同一已取消ctx，不会变为无ctx的继续运行。
- 原track/release/profile数量限额、发行轨数/名字预算、adaptLimit阈值和调用条件未因pacing改变。ETA改为计入本批计划yield，仍只是平滑估计，不是完成时间承诺；日志新增浮点`yield_ms`及`yield_status=planned`，没有将取消前未完成的等待伪报为实测等待。原应用批事件/phase及进度SQL识别不变。
- 新4个顶层纯测试的代码覆盖零/负/最小正数、上下边界/分数纳秒/最大Duration、正区间单调和尖峰后恢复；实际timer非忙等；已取消ctx及异步取消退出。异步测试的started信号在进入wait前发出，因此不严格证明取消发生时timer已开始等待；不把它称为数据库持锁/在线公平性回归。

本检查只核验文件中的公式、调用/提交顺序和断言，未执行上述纯测试，未声称其PASS。10–50ms正让出给予其他写者调度机会，但不证明公平性、M3延迟或I8分位数达标；仍需主控原规模在线实测。旧固定50ms规范同步及profiles后续稳定切片留待owner/主控，本次只追加报告。

## 九、profiles稀疏/密集查询选择定点复核（2026-11-05）

范围为稳定候选 `artistgraph/profiles.go` 与 `profiles_batch_integration_test.go` 的新增边界测试；只读取已完成的仓外 `i8-throughput/profile-serial-before`、`profile-serial-materialized-absent`、`profile-serial-variants` 中manifest/计划/对照摘要。**未读取运行中的profile-serial-final内容，不宣称最终诊断完成；未DB、未test、未修改owner文件。** 主控已告知旧固定50ms规范完成同步，本文不重复改动或复查该切片。

### 语义与事务结论

**本次未发现可证实的新语义、版本或锁序bug。** 探测仅选查询策略，不构成产品输入上限：

- 探测限定本批artist_id及非空track_id，`OFFSET 4096 LIMIT 1`仅回答是否存在第4097条符合条件的有效署名。≤4096走旧稀疏连接；>4096在主查询内materialize全部非present轨ID。它数的是署名行而非去重票/名字/艺人数，跨角色重复可影响路径选择，但不改变票数。
- 两路径都按本批artist/name计算全部`count(DISTINCT track_id)`，再扣同样的非present去重轨票。密集分支将 `JOIN tracks ... source_status<>'present'` 换成同一SQL快照内的 `JOIN absent_tracks`；tracks主键保证不会重复放大连接。发行级DISTINCT票及有present轨条件、正票过滤、C排序/name_id打平不变。
- 探测结果不是后续输入集合：正式计票、owned名字递归、搜索名字聚合没有使用4096/4097作LIMIT。全missing仍零票回退，名字仍保留；manual/initial_name/redirect来源别名逻辑不变。未更改Version、代次、完成标记或发布判断，profiles/search names仍用原同一事务写入。
- 新探测和主查询都使用调用者ctx/tx；探测Scan错误直接返回，不降级为稀疏“成功”。主查询的Scan/rows.Err、后续marshal/Exec错误仍向上传播；没有新增goroutine、独立事务、提交、锁或设置。两个查询在调用者CATP覆盖下执行，未借锁外探测旧值回写；即便选路受数据变化影响，两种SQL本身也必须返回相同语义。

### 成本边界不能误述

“最多4097条探测”是最多消费4097条**符合条件的署名**，不是最多扫描4097条物理行/索引项；它不能证明计划耗时有硬上界。密集路径明确在CATP事务内读取全库非present集合，当前证据中实际顺序扫描110000轨、过滤100000 present、materialize10000 missing。因此不能称本优化完全避免全库读取或其成本只取决于本批artist数量；它替换的是热门批逐署名查轨的代价，是否划算由原规模计划和正式运行决定。没有增加全库写操作或反向业务锁。

### 新边界测试与覆盖不足

`TestPostgreSQLArtistGraphProfileDensityProbeDoesNotLimitVotes`建4097条轨署名：前4096票两种写法各2048，最后一票使Beyoncé胜出；将最后一轨改missing后调用RefreshProfiles，要求扣票并回到Beyonce；再清掉最后一轨artist后完整Refresh，显式确认有效署名数4096且主名保持。这覆盖密集端第4097票、missing扣票和回落稀疏边界，非仅检查无错误。

但它没有构造第4098及以后决定胜出的票，也没有在密集分支构造>4097个不同别名；不应宣称单凭此测试已证明任意规模不截断。无截断结论来自SQL中探测与完整计票/名字集合分离，旧260作用域别名测试仍补名字枚举回归。新增测试也未注入探测查询取消/失败，当前只静态核对ctx和错误传播；本人没有运行这些测试。

### 已完成诊断证据抽查

- 三阶段manifest均注明仅三个发行窗口及独立profiles清单12批，仍building，未将其余对象标完成；样本数分别79/84/84，**不能用于整轮p95/p99**。各阶段hot/ordinary/ordinary-to-missing业务对照文件均记录equal=true，不等于最终候选完整验收。
- 抽查独立第6次原始计划：before执行329.781ms，tracks_pkey节点循环110456次、shared hits401792；materialized-absent执行183.627ms，改为absent_tracks一次扫描、10000返回/100000过滤。完整计划总shared hits455797→57279，支持减少热门generic路径重复查轨的解释；不是通过关闭planner/JIT选项换来的结论。
- 仍保留不利证据：窗口CATP平均hot约336.51→236.07ms，但ordinary约91.15→110.81ms，profiles约31.59→47.46ms。窗口批数及written也并非完全相同，不能直接据均值算完整回填加速；这正说明不应把全量materialize套到所有稀疏批。variants还记录ordinary最大约1171.16ms，按I8必须保留归因而非旧max门槛中止。
- 上述是先前候选/variants证据，不是本次4097探测最终候选的完整结果；不能据此认定4096阈值已最优、所有计划稳定或正式性能PASS。待owner结束final诊断、主控回归与正式验收后再追加结论。

本次实际操作仅sed/rg/ls/find/cat及Python读取JSON原始计划/对照，外加追加本报告；未启动测试或数据库任务。

## 十、密集路径按身份LATERAL计票定点复核（2026-11-05）

本次只读当前 `profiles.go` 密集all_track_counts增量、同文件显式ID端口修复及 `profiles_batch_integration_test.go` 增量；读取仓外 `i8-throughput/search-ready-plans-20261105T015551Z` 的候选SQL、六次执行、实际窗口与物理统计。未DB/测试，未改owner文件。

### P2发现、当前修复与剩余测试缺口

首次读取时dense分支直接 `unnest($1::uuid[])`；原 `uniqueIDs` 只按字符串去重，同UUID大小写或无连字符等不同合法文本在PostgreSQL中会变成相同ID。旧ANY只计一次，新LATERAL会产生重复artist/name轨票，后续sum放大轨票，而发行票仍DISTINCT，可改变主名。已即时反馈主控，未自行修复。

随后owner落盘的显式 `RefreshProfiles` 端口已补：标准UUID先转小写再uniqueIDs；非标准文本经 `SELECT DISTINCT id::text FROM unnest($1::uuid[])` 规范化，沿PostgreSQL原有解析/报错语义。另一内部调用点Refresh的old/current来自数据库UUID文本，created来自uuid.New，并统一去重。没有改共用uniqueIDs以免影响原文名字。graph已有platform/uuid依赖许可，无需扩allowlist。**重复ID生产路径静态已闭合。** 空输入仍无操作，空字符串沿原逻辑忽略；非法非空文本由PG报错且整体事务不提交，不静默丢弃。

**剩余回归缺口（已反馈）：** 新测试加入重复/大写/无连字符ID，但目前只检查两个纯轨票名字的主名。若再次删掉规范化，全部轨票同比倍增，原胜出名字仍可能不变，不能敏感地捕获本次问题。需有发行票和轨票竞争的反例，例如同一身份Alpha仅1发行票、Zulu仅1轨票，原平票Alpha胜、错误翻倍后Zulu胜；另一个热门身份可使整批进入dense。不能把现有重复输入调用无报错当作此问题完整回归。本节不声称该补强已交付/执行。

### SQL正确性与候选匹配

- 对规范化去重ID，每个artist的LATERAL独立按name_id分组并COUNT(DISTINCT track_id)，与原按artist_id/name_id分组等价；跨角色同track/name仍一票，不同artist不共用计数。track_id IS NOT NULL仍排除发行级行；name_id为非空外键。无轨身份产生零组，不虚构NULL名字计数，外层artists仍通过fallback/initial_name得到profile。
- 稀疏分支不变，密集分支仍使用完整非present集合扣票；release票、正票过滤、主名C/name_id打平、完整名字及search派生未改。LATERAL内ORDER BY name_id没有LIMIT，不截轨/名字，也不取代最终热门票排序。
- 仍是同一ctx/tx执行，版本/代次/marker和锁序未改；新增非标准ID规范化查询也向上传播Query/Scan/rows.Err错误。不以分身份聚合强行承诺planner一定不排序或不落盘。
- 已用Python从当前Go字符串常量重建密集完整SQL，与 `ordered-lateral_count-executions.json` 的候选SQL按空白规范化比较，**一致**。输入端口规范化是额外正确性保护，原诊断输入为规范化ID；不能把旧source哈希当成新增端口/测试已执行证据。
- 已读新增跨角色断言：给Beyonce同一轨再加composer/performer，仍要求Beyoncé以一票优势胜出；若每角色多计则会错误翻转，断言具有敏感性。另批含Solo Artist并检查其主名，之后missing扣票及删除额外角色回落4096边界仍保留。以上仅静态断言审查。

### 对照混杂与预算可支持范围

核对六次交错执行：全部记录完整结果equal=true，baseline均值 **264.6777ms**，lateral_count均值 **208.3566ms**；可支持“该夹具/参数下、此次交错比较的候选SQL更快”，不是正式全分布结果。

实际process窗口各24批：baseline_replay CATP均值 **234.0970ms**，winner **179.9659ms**，winner局部p95 **196.4539ms**。这两个窗口**不是只改变SQL的洁净对照**：

- baseline前快照时间2026-11-05 02:08:25.992382 UTC，artist_effective_credits relallvisible=0，dead估计12086；
- winner前快照时间02:08:34.514971 UTC，记录该表在 **02:08:29.048908 UTC** 自然autovacuum，relallvisible=9453、dead估计1240；artist_profiles亦在02:08:29.563502发生autovacuum；
- winner后快照仍是该次vacuum后的物理状态。统计是估计值，不能直接用dead数量计算精确因果贡献。

因此234→180ms不能全部归因于LATERAL；六次交错SQL对照与真实CATP窗口必须分开叙述。局部24批p95约196ms不等于整轮CATP p95≤200ms，也不证明p99≤500ms、联合回填≤36分钟或前台M3/扫描/播放/搜索达标。证据支持继续受控回归和正式原规模验证，不支持缩小规模、跳过失败或改变I8门槛。

当前结论：计票改写及输入规范化静态可接受，**重复UUID回归敏感性仍待补强**；未运行任何测试，不认定owner正在完成的工作已经全部通过。

## 十一、最终稳定DISTINCT方案与回归复核：P2闭合（2026-11-05）

本次按最终交付一次刷新profiles密集入口、显式RefreshProfiles端口、新增回归及仓外 `i8-throughput/dense-distinct-final`。**重复UUID计票P2及第十节所列测试敏感性缺口均已闭合。** 第十节描述的Go大小写规范化/额外PG规范化查询只是当时中间态，**已撤回，不是最终实现**；通用uniqueIDs未改。

### 最终生产修复

- dense入口现在是 `FROM (SELECT DISTINCT id FROM unnest($1::uuid[]) ids(id)) a`，在转换为数据库UUID之后去重，再逐身份LATERAL完整计票。大小写/无连字符等相同UUID文本不重复乘轨票，与原ANY集合语义一致；无需修改共用名字去重逻辑或增加Go层UUID解析。
- RefreshProfiles恢复原uniqueIDs→generation→refreshProfiles流程，无新normalize路径或额外依赖。稀疏SQL、NULL轨过滤、跨角色COUNT DISTINCT、发行票、最终排序及事务/版本边界没有因修复扩大修改。

### 新测试确实能抓住原缺陷

`TestPostgreSQLArtistGraphDenseDuplicateUUIDReleaseVoteCompetition`用独立Hot Artist的4097轨触发dense；目标固定UUID带字母，绑定Alpha/Zulu两名字，Alpha仅发行级一票（release_artist/album_artist跨角色去重），Zulu仅轨目一票。先单一目标ID确认Alpha平票胜，再输入目标小写/大写及hot，仍须Alpha。它不再依赖“全部轨票同比放大后主名不变”的无敏感性夹具。

既有密集边界测试另补发行票追平Beyonce/Beyoncé、混合大小写/无连字符ID断言，同时保留跨角色、另一身份、missing扣票及4096回落。最小计划捕获使用实际pgx query tracer读取生产执行SQL，不是测试另复制一份算法。

已读取owner仓外 `no-distinct-overlay.json`、`profiles-no-distinct.go`、`no-distinct-mutation.log`：overlay只替换profiles.go；通过纯文件比对确认变异仅把DISTINCT入口还原为直接unnest。真实日志明确该独立竞争回归 **FAIL：got "Zulu" want Alpha**（包耗时5.966s）。这证明测试对已定位缺陷敏感；这是owner已执行的变异证据，本检查没有运行PG或测试，也不把预期变异FAIL误报为当前生产FAIL。

### 最终SQL证据与早期性能证据区分

- 用Python从当前生产常量重建dense SQL，与 `dense-distinct-minimal-plan.json` 中实际捕获SQL **逐字节相同**，记录的SQL SHA-256也匹配。
- 与早期search-ready-plans候选相比，明确新增UUID DISTINCT入口；替换这一入口后，空白规范化比较一致。不能继续宣称最终SQL与早候选字节完全相同。`sql-delta.json`已显式记录candidate/final不同哈希与差异。
- 最小4097轨计划约6.793ms仅用于确认最终实际SQL及数据库可执行计划，不是原规模SEARCH_READY性能证据。第十节自然autovacuum混杂披露继续有效，局部p95约196ms仍不得冒称正式I8通过。

**验证边界：** 本次只读代码、overlay/日志/JSON，并运行纯Python字符串及SHA比对后追加报告；没有DB、test、race、compile或vet。主控告知两包race尚在运行，未读取其途中输出作PASS，不预报最终数量/耗时。静态正确性和针对性回归设计可闭合，完整回归与正式I8性能验收仍待主控最终结果。

## 十二、最终pacing小增量复核（2026-11-05）

仅读取最终 `pacing.go`、`pacing_test.go`、既有wait实现及仓外 `i8-throughput/pacing-offline-20261105/{report.md,budget.json}`；不重复此前worker/SQL审查，未DB、test、compile或vet。

**静态结论：未发现新增问题。** 第八节的duration/4已由最终分段策略取代：

- `duration<=0` 或 `duration>=200ms` 一律50ms；其余取duration/10且至少10ms。100ms及以下有效短批均10ms，100–200ms之间约10–20ms，恰到200ms跳升50ms是明确的长批保护，不是错误边界。
- 正耗时区间仍单调、等待始终为正；最大Duration在分支直接返回50ms，无除后溢出/负等待。锁等待或COMMIT尖峰使整批达到200ms时不会随/10被削弱让出，下一短批可恢复较短等待。
- 两worker调用位置本次未改，仍在提交释放CATP后让出；原timer/context取消路径不改，计划等待不是必须执行完的不可中断sleep。原批预算、锁序、planned日志及ETA含计划yield的边界沿前述结论，不重新把整批Duration称作精确CATP持有。
- 新断言覆盖100ms前/等/后、200ms前1ns/恰等/后1ns，以及纳秒除法舍入：`(200ms-1ns)/10 = 20ms-1ns`，而200ms返回50ms；同时保留无效耗时、最小正数、最大Duration、提交尖峰、正区间单调与尖峰后恢复。非忙等及ctx取消测试仍保留。本检查只确认断言，没有执行或宣称PASS。

离线报告明确采用带200ms保护的/10候选：warm条件估计 **35.25分钟**，固定工作成本增加5%即 **36.83分钟**，低可见性条件约 **46.02分钟**。它保留原处理工作与timer超额，仅替换计划等待；线性外推、profiles全stale摊销、自然autovac混杂和最终DISTINCT差异均不能视为已经消除。warm剩余约45秒余量只能支持一次正式实测，不能证明36分钟硬线、CATP完整p95/p99或前台指标通过。

主控已告知spec/design同步并将冻结生产后运行最终全影响race及一次正式验收。当前不再提出额外优化、不预判结果；本次只追加报告，未改任何生产或owner测试。

## 十三、唯一正式I8运行与最终race结果核验（2026-11-05）

**最终结论：正式I8验收FAIL，且采集未完成。回填和回填期在线指标通过，不等于搜索性能或整轮通过。** 不再实现、优化或启动验证。本节仅核对仓外汇总、命令、测试日志及race事件统计，未解析巨型activity/SQL日志或EXPLAIN计划树。

### 证据与命令

正式目录：`scale-i8-20261105T025149Z`。读取 `summary.json`、`test.json`、`test.exit`、`command.txt`、`runtime.txt`；命令记录为：

```sh
go test ./internal/app -run ^$ -bench ^BenchmarkPostgreSQLSearchScale$ -benchtime=1x -count=1 -timeout=120m -json
```

这里 `-timeout=120m` 是Go测试进程上限；summary内 `TotalBudgetMS=5400000` 是harness的90分钟总context，二者不能混称。test.json记录2026-11-05 02:51:52 UTC开始、04:21:52在第4个query旧release-groups请求收到503 database_unavailable，最终benchmark/package FAIL，退出码1、包耗时5400.851s，与主控报告的90分钟总context耗尽一致。本次不独立推断具体SQL或锁根因。

最终 `Finalized=true`、`Complete=false`、`Pass=false`。Finalized只表示清理后报告封口，不能解释为全部验收项已测。FirstFailure保留首次搜索性能失败；FirstError为“benchmark未完成”的汇总描述，具体旧路由503仍保存在Errors.operation及test.json，不能漏掉其中任一层。

### 已完整取得的回填/在线结果

| 项目 | 最终记录 | 口径 |
| --- | --- | --- |
| 联合回填 | 1,923,745.0693ms，即 **32分03.745秒** | BackfillComplete/BackfillPass均true；小于36分钟 |
| 已提交应用批CATP | **12,288批，p95 199.361ms，p99 276.037ms** | Evaluated=true、未确认批0、EvidenceError为空；两项门槛通过 |
| 最大持有 | 5,985.595ms | 如实保留，按I8不是独立失败门槛；不在此推断fsync/锁根因 |
| 回填期M3 | **66/66成功，p95 342.840ms** | 全样本非空且≤1秒；不是要求每个请求≤1秒 |
| 扫描 | **65请求、65接受、65 succeeded、65目录核验** | 不是只以202判断完成；艺人building阶段有效周期45轮 |
| 回填期播放 | **1,911请求，503=0，其他失败=0** | 仅回填期在线负载范围 |
| 回填期搜索 | **1,913请求，503=0，其他失败=0** | 不等于发布后正式搜索时延通过，也不覆盖后来的旧路由503 |

fixture检查记录通过：合成110000轨均有两艺人/四credit，10000 missing；加独立探针后总110001轨、10001发行，参与池20000身份、4000身份具有不同key别名、热门present轨50000。额外探针不是缩减原规模。

### 已完成的4/26组新搜索结果

summary声明5次预热/30次测量；四个Cases的Suggestions/Results及三栏均实际Samples=30，test.json也分别输出p95。多字门槛均为300ms：

| 查询 | 建议p95(ms) | 三栏整屏结果p95(ms) | 判断 |
| --- | ---: | ---: | --- |
| 交响曲 第00999号 | 311.171 | 258.047 | 建议FAIL，结果PASS |
| 交响曲 第009 | 172.745 | 230.777 | 两项PASS |
| 周杰倫 | 3729.150 | 21860.786 | 两项FAIL |
| 周杰伦 | 3674.033 | 21648.577 | 两项FAIL |

AcceptanceFailures保留这**5项明确性能失败**，不是因超时才“没有结论”。PlanFiles仅4个EXPLAIN文件，对应Checks记录16/18/19/19份生产SQL计划；这里只确认文件/汇总存在，不重新分析其计划。

### 未完成与不得计PASS的范围

- 剩余 **22/26** 查询没有完整新搜索测量，包含所有单字场景及后续别名、特殊字符、阈值等矩阵；不能以已测的多字结果替代单字1000ms验收。
- 第4组新搜索已经完成，但该组**旧路由对照未完成**：OldReleases/OldGroups的汇总Samples均0，仅保存首次Cold值，不可将零耗时字段理解为很快、通过或实际完全未发请求。前三组旧路由各30样本；其TargetMS=0/Pass=true仅表示无新增SLA，不表示例如24秒耗时满足新搜索300ms目标。
- `Queues=null`，深页检查及两种10000完整队列/随机形状/队列计划与取消验收未到；不声称队列通过。
- Checks当前只有19项（14 true、5 false），中止前未走完整finishCollection；缺失项未全部出现在AcceptanceFailures中。**失败列表长度不是未测缺口数量**，须结合Complete=false、Cases/PlanFiles/Queues和规定矩阵判断。

### 最新全影响race独立核对

选择时间戳最新的 `i8-final-race-20261105T022727Z`，不是较早011338目录。读取race.command/exit/result-summary及流式重计原始race.json：

```sh
go test -race -p 1 ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/... -count=1 -timeout=40m -json
```

原始事件与汇总一致：**2229条测试PASS事件（含子测试）、24包PASS、无FAIL、退出码0**。另有3个真实用户音乐opt-in测试SKIP：TestRealMusicLyricsCoverage、TestRealMusicReadOnlySmoke、TestRealMusicFingerprintCoverage；2个包级skip明确为`[no test files]`。没有其他测试SKIP，因此可说明本轮PG测试无SKIP，但**不能称整个选择集合或全仓0SKIP**，也不能把命令未覆盖包称已验证。

抽查原始PASS事件确认旧RecentAdditionsExplain、CatalogHTTPPerformance、PackageDependencyDirections、密集边界及重复UUID竞争回归均已实际通过。vet-build.log只记录exit=0，本文不擅补其未记录的命令或时长。race通过不推翻本次正式性能FAIL。

主控已通知正常down-v，资源0记录尚待其后续证据；本次不启动Docker/DB检查，也**不预先宣称容器/卷/网络已核验为0**。实际执行仅目录列举、cat及Python读取小型汇总/流式统计race日志、追加本报告；没有测试、编译、DB或实现改动。
