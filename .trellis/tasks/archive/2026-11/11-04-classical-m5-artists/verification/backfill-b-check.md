# B 阶段回填验收 harness：独立只读检查

- 日期：2026-11-04；检查角色：trellis-check，未创建子代理。
- 依据：联合设计 §15.2b 及本轮明确授权。完整原规模生产联合回填不超过 36 分钟；已提交应用批 CATP 持有总体 p95≤200ms、max≤1000ms；搜索保持 5 次预热/30 次测量及 300/1000ms，队列合同不变。
- 当前范围：`app/catalog_scale_benchmark_test.go`、`catalog_scale_acceptance{,_unit}_test.go`、`catalog_scale_load_test.go`、`catalog_scale_diagnostics_test.go`，并只读对照生产批次日志时序与夹具脚本。
- 本次未启动 DB、测试、编译、vet 或性能任务；未改 owner 文件/生产/前端。graph owner 的未完成优化及 SQL EXPLAIN 不在本次检查范围。

## 一、阶段性结论

harness 的预算、原规模夹具、真实写入验证及成功后的测量隔离总体符合方向，但**存在两项验收证据缺口，已即时反馈主控**。owner 仍在收尾；下述问题按当前文件快照记录，不能把测试代码存在或 A 的 75 项 race 通过视为 B 通过。

### P1：提交后日志缺失会漏计真正应用批，维护失败可能被重试掩盖

证据链：

1. `scaleDiagnostics.endQuery` 在 maintenance 的 CATP 事务成功 COMMIT 后只设置一个 `pendingBatch`；真正加入 `batchHolds` 依赖稍后的 `catalog_projection_backfill_batch` 日志。
2. 生产 `maintenance/worker.go` 及 `artist_worker.go` 均在应用批提交后，先执行一次 `LoadState/loadArtistState`，再输出该日志。状态查询失败时，本批已真实提交，但日志不发生。
3. 后续维护事务结束会清空/覆盖 `pendingBatch`。`maintenance.Run` 会记录 `catalog_projection_backfill_failed` 并重试，但当前 `scaleBatchHandler` 只处理成功批日志，未对这个失败事件触发 guard。
4. 因此一次已提交应用批（即使持有超过 1 秒）可能从总体分布消失，维护重试恢复后 harness 仍有路径判为成功；不符合“不漏失败/commit”和首次失败停止的要求。

建议：至少对生产回填失败事件立即留证并停止；应用批事务最好以真实清单进度写入/提交关联，或者显式检测已提交应用事务未被日志确认，不能仅把日志有无当成批存在与否。应补“COMMIT成功→后续状态读取失败→没有batch日志”的针对性断言，并确保该批持有仍留在失败报告中。当前未代替 owner 改代码。

### P2：艺人阶段覆盖只检查写入前状态，可能把发布后的 M3 算成回填期间写入

`scaleOnlineLoad.cycle` 在扫描前记录状态；扫描完成后、GET revision 之前再记录 `CorrectionArtistStatus`。随后才 GET revision、PATCH、GET 回读。`scaleArtistPhaseCycle` 只要求这两个前置快照为 building 及写入验证成功。

若 worker 在第二个快照和实际 PATCH 之间发布，真正 M3 可以完全在 ready 后执行，仍计入 `ArtistPhaseCycles`。现有单测把前置 `CorrectionArtistStatus` 改成 ready，只能证明已观察到 ready 时不计入，不能排除此竞态。

建议添加写入完成后的状态/阶段证据，要求扫描与 M3 都在艺人 building 期间实际完成（或提供等价严格关联），并覆盖“预快照 building、回读时 ready”不计入有效回填负载的情况。现有 scan succeeded/本轮观察和 M3 revision/label 回读充分证明写入真实性；问题仅在阶段归类，不能退化成只断言 202。

## 二、已经确认的合同和实现边界

| 检查项 | 静态结果 |
| --- | --- |
| 总预算与回填门槛 | 总 context=90分钟；独立36分钟 timer 从 worker 调度开始，到维护返回且双方 ready 复核后停止；时间超标调用 guard，未用90分钟替代36分钟 |
| M3 单请求 | 所有 handler 请求仍以 life 为父并单独设45秒 context，没有拉长请求预算 |
| CATP 持有口径 | 从第一次 CATP 获取成功的 query-end 到事务 COMMIT query-end；等待另记，重复获取不重置起点；COMMIT耗时包含在内。不是 transaction lifetime，也没有把OPER等待或清单发现计入持有 |
| 应用批筛选 | 只有 maintenance actor、取得CATP、成功COMMIT且tag为COMMIT才产生候选；回滚、失败COMMIT、COMMIT返回ROLLBACK、扫描/M3不作为成功应用批。发布/空批不打应用批日志，不应纳入；但仍有第一节漏计问题 |
| 统计 | 所有phase的持有样本合并后计算总体p95/max，不是phase p95再平均；phase分布仅拆解报告；空证据或未完成分布不判PASS；统计使用复制后的数组，不排序改写原样本 |
| 停止规则 | 已记录应用批max>1000ms在该批日志处立即guard.fail；总体p95在完整分布形成后判定；36分钟独立timer失败立即取消；首个失败原因不被后续context取消覆盖。尚需补第一节维护错误路径 |
| 周期真扫描 | 每30秒锚定worker开始调度，慢轮不补发堆积；真实POST扫描后轮询succeeded，核验该scan ID的track_observations和observed_at、真实present目录身份/标题/艺人，不以202作完成 |
| 周期真M3 | 每轮真实GET revision，PATCH交替label并使用新幂等key，随后GET确认revision恰加1及label正确；不把no_changes当负载成功 |
| 原9900用例 | 独立保留9900发行修正及“在线修正厂牌”“厂牌 9900”新旧词搜索矩阵；周期探针改其自己的发行，不覆盖这组证据 |
| 并行读负载 | 主循环1秒调度ready/建议、探针轨出现后真实Range播放；实际耗时分别记录，不能称请求阻塞时仍有固定吞吐 |
| 原规模与全回填 | 拒绝复用schema、缩减queries或iterations；脚本为10000发行/约8000组、110000轨、20000身份/4000别名，轨角色/倾斜检查保留；只预置原始目录、初始身份/名字路由，不插入profile/effective/search派生来替代维护 |
| 正式测量 | 5warm30、三栏并发、300/1000ms未改；首个搜索门槛失败后不继续下个查询/队列。原10000完整队列及随机/顺序、去重present、计划和取消检查没有用缩小响应替换 |

CATP 数字的观测边界是客户端 pgx 获取响应结束至 COMMIT 响应结束，含提交返回链路开销，不是服务端时钟精确锁释放时间；不得把它描述成数据库内部毫无误差的时间戳测量。它明确排除获取等待，且不拿生产 `batch_ms`（含事务前等待）冒充持有时间。

## 三、采样退出与取消清理

- 成功路径等待 worker 完成，并 join 周期 load；之后执行 `diagnostics.stopOnline`，先取消并等待 sampler goroutine、关闭独立 monitor pool，再关闭在线 trace，随后才执行唯一 `postgrestest.AnalyzeSchema`、计划、预热和正式 p95。
- 无详细诊断时仍开启最小事务/CATP追踪，足以收集已提交批；关闭后 beginQuery 直接返回，不继续在线 SQL/事务记录。正式查询自身原有SQL计时收集仍保留，不能误称“所有tracer均删除”。
- guard 在首次失败先 checkpoint 再取消 life；在线请求、扫描生命周期和维护worker共用该父context。在线load另有join，外层对 WaitForScans 和 workerDone 做有界等待，超时以测试错误显式报告，非fire-and-forget。
- 清理有界等待不是“资源已清理”的证据。本检查未实际取消、未检查连接/容器状态；后续真实运行仍需保留清理结果，不能仅凭defer代码宣称无残留。

## 四、单测断言与待验证范围

已读纯测试：预算/边界/空证据、完整与未完成分布；候选必须被日志消费、重复日志拒绝、排除清单发现/回滚/失败COMMIT/COMMIT退回ROLLBACK/其他actor；max超限仍纳入失败报告及首次错误保留；周期锚点/label交替/真实观察及revision检查；非详细诊断停止后不追踪正式查询。

这些断言尚不能覆盖第一节的提交后日志缺失，也不能覆盖第二节的发布夹在预快照与PATCH之间。应由 owner 补齐后再定点复核。本检查未运行任何测试，不声称这些测试通过。

后续仍需：owner交付后的纯回归与主控允许的DB验证；原规模生产联合回填墙钟、完整应用批CATP分布、艺人阶段真写入证据；达标后原5warm30搜索/队列计划；失败时首因与残留资源收证。graph SQL优化、EXPLAIN前后对比及业务语义回归留待后续授权。A已由主控报告75项race/0SKIP与vet通过，不替代上述B证据。

## 五、Wegener 交付后的定点复核：P1/P2 已关闭

**当前 harness 静态结论：第一节两项问题均已闭合，本次定点范围未发现新的确定性问题。** 上文保留修复前证据，不再表示当前实现仍漏计或误归类。

### P1：以实际应用事务提交计数，日志只确认

- `scaleApplicationPhase` 从事务中实际成功且 RowsAffected>0 的 work/phase 完成进度 UPDATE 识别应用批；已对照当前 `maintenance/batch.go`、`artist_batch.go` 的四条真实 SQL 及参数顺序，均匹配。清单发现 INSERT、状态失败标记、发布和空批不被当作应用批。
- 成功 COMMIT 且持有 CATP 的维护应用事务，在 `endQuery` 内立即进入总体/phase统计和 `pendingBatches[TransactionID]`。max 超标在 COMMIT 回调即触发失败，**无需等后续 LoadState 或批次日志**。
- 发布、markFailed、其他事务不能删除未确认应用提交；若又出现新的应用提交，两个提交都保留并产生验收错误。成功日志必须对应唯一、同 phase 的未确认提交；重复/缺失/错 phase 不能静默通过。最终 PASS 额外要求无未确认批、无 EvidenceError。
- `catalog_projection_backfill_failed` 现在立即调用 guard，阻止生产维护自动重试把错误隐藏。首次失败快照及清理前快照先于取消，后者由 `sync.Once` 保留，不被取消后的现场覆盖。
- COMMIT验收失败回调在释放 diagnostics mutex 后才调用 guard，避免 guard 保存快照时反向取得同一 mutex 死锁。原 CATP 起止边界未改变，仍排除获取等待、包含 COMMIT 返回。
- `TestScaleAcceptancePostCommitFailureCannotHideRetry` 明确模拟应用 COMMIT→LoadState 失败→markFailed COMMIT，断言原应用提交仍在；随后失败日志取消，错误继续提交时两个批均留存且不能通过。`BatchLogAndFirstFailure` 另覆盖没有任何批日志的 max 超标立即失败。

### P2：增加真实写入之后的 building 证据

- 周期探针完成 M3 PATCH、成功 GET 回读并核验 label/revision 后，再读取 `AfterCorrectionSearchStatus/AfterCorrectionArtistStatus/AfterCorrectionArtistPhase`。
- `scaleArtistPhaseCycle` 同时要求扫描前、扫描完成后的修正前、以及修正完成后三次快照满足 search ready / artist building，且真实目录与修正验证成功。原“GET revision 与 PATCH 之间发布”路径不再计为 artist 回填期覆盖。
- 纯单测明确保持前置 building、只把写后状态改为 ready，断言不得计入；写前已 ready、未完成修正也仍拒绝。没有把 202 或仅请求发出当作完成。

### 验证边界与最终判断

本次我只执行文件读取和文本检索，未运行 DB、普通测试、race、编译、vet 或 benchmark，未改 owner 文件。主控报告 Wegener 的 **11 个顶层纯测试 race PASS**；代码中对应7个 acceptance 测试及4个 diagnostics 测试，本报告不冒称亲自执行或复核其完整日志。

上述闭合仅针对 harness 的计数、失败停止和阶段证据完整性。正式原规模生产回填≤36分钟、应用批 CATP 总体 p95≤200ms/max≤1秒、5warm30 搜索及完整队列仍需主控实际测量；graph 未完成优化及其前后EXPLAIN不在本次结论中。**目前不宣称 B 性能通过。**

## 六、maintenance 工作量限批稳定切片复核

本次仅重新读取 `maintenance/artist_batch.go`、`artist_worker.go`、`artist_workload_test.go` 三个指定文件，**没有检查正在变动的 graph/0029，没有运行 DB、测试、vet、编译或性能任务**。

**结论：该三文件切片未发现新的确定性合同或续跑错误。** 新预算是调度选择依据，不是产品输入拒绝条件，也没有以估计值替代事实或完成标记。

### 锁序与事务完整性

- 仍在维护专用连接开 READ COMMITTED 事务，先 `lockArtistBuild` 取得 CATP、复核构建，再读 pending 清单及当前代次 marker。工作量估计也在同一事务内，不带锁外旧值回来覆盖事实。
- 仅对选中的 refresh ID 调用原 graph 端口，随后验证目标代次 marker，再一起更新 work completed、phase completed 和能力 building，最后 COMMIT。预算不会提前写 marker；`workload.Selected` 也仅在成功提交后回传。
- QueryRow/QueryContext、Scan、完整读取时的 rows.Err、估计回调、graph、缺口复核、更新和 COMMIT 的错误均向上传播；估计第二项失败时不会把第一项包装成成功小批。提前判定超预算只返回显式不完整估计，仍关闭 rows，不把未读取的其余输入推断为空。
- 没有增加 OPER、发行/组或个人 state 的反向锁，也没有把对象发现移入新的全库 CATP 临界区。提交后仍让出 50ms；批次日志原事件名及进度 SQL 保留，与已复核的 harness 提交证据识别兼容。

### 连续前缀、超额项与续跑

- `selectArtistReleaseWork` 只选 UUID 排序 pending 清单的连续前缀，不跳过昂贵项去处理后面的便宜项。
- 已接纳至少一个待刷新项时，下一个超额项留给下一批，**不把它标成完成**；它下次成为第一个待刷新项时，即使单项超过整批预算或估计不完整，仍完整传给 graph，随后停止扩大该批。因此大 subject 不被饿死、截断或永久拒绝。
- 前面的在线已完成/已不存在 subject 可以一起标清单完成，不消耗 refresh 预算，也不调用估计。此处“已不存在”与 source_status=missing 不同：missing 轨仍在发行轨数和各输入查询中，没有被过滤掉。
- 零轨/零输入、恰好达到预算、重复的已完成项前缀都能前进。持久化续跑依据仍是原 work/phase/代次，而非本次内存预算；重启重置估计预算不会重置已提交进度。
- profile 阶段仍使用独立 profileLimit，不被发行轨数预算误限；本切片没有缩减 profile 完整性要求。

### 估计边界及诚实日志

- 输入估计只读当前发行的轨、原展示、观察、原始重复标签、四角色 credit 和 album_artist correction；同一修正计两次，对应两个发行级角色。包含可能被实际解析规则替代或丢弃的输入，是有意保守的输入工作量估计，不是有效署名数/去重艺人数。
- 每条 raw 使用已有 `CanonicalArtistNames` 拆分并至少计1，避免把一条多值 credit 当一个名字。`LIMIT remaining.Names+1` 加逐raw成本至少1，足以发现超额；若轨数已经超额则不继续读取全部名字，明确设置 `EstimateComplete=false`。
- 早停时 Names 只是已读输入成本，不是完整总数；日志使用 `estimated_input_names`、`input_estimate_complete`、`subject_over_budget` 区分，没有把零个已读名字解释成零事实。
- 起步10轨/64输入名字，上限50轨/256名字，和发行数量上限并存。仅在实际刷新过发行时适应工作量预算，避免纯在线已完成项把刷新预算虚抬；下限1使调度保持前进。
- **这些数值不证明每批 CATP≤200ms 或≤1秒。** 单个超大 subject 仍完整处理，可能超过性能目标；它必须由 harness 据真实提交持有时间失败并报告，不允许通过裁掉内容换取达标。`result.Duration` 含等锁等开销，只用于调度与警告，不能当作精确CATP持有时间。
- 逐发行/逐轨 LATERAL 参数化查询及输入行 LIMIT 只是当前查询形状；轨数 count 仍须读取该发行全部轨，MATERIALIZED scoped_tracks 等也有成本。不能把50/256预算、OFFSET 0或字符串单测当作实际索引计划/查询耗时证明，后续仍须独占原规模计划与实测。

### 测试覆盖与未验证边界

已读7个顶层纯测试：一般选择和双预算边界；小→大→小跨三批无饥饿；在线已完成/删除项不估计；剩余预算传递及估计错误整体失败；多值/重复/AC/DC拆分；SQL来源/含missing/局部入口检查；自适应上下界。

其中 `TestArtistReleaseWorkloadSQLScopesInputsAndIncludesMissing` 仅检查SQL字符串，其他工作量选择测试使用可控估计回调；它们不执行真实 SQL，也不证明数据库计划、取消时的驱动行为或完整回填性能。owner已有纯race报告与本次只读审查严格分开：**本文没有亲自运行或核验该报告的命令/日志，不新增PASS数量或耗时声明。** 实际估计查询、完整 subject 刷新、取消/故障回滚和断点续跑的集成结果由主控后续汇总。

该稳定切片审查到此结束；等待主控通知后再审最终 graph/0029/B 集成改动，不提前读取正在变动的实现。

## 七、最终 graph / 0029 与 maintenance 集成增量复核（2026-11-04）

**结论：本次指定稳定切片未发现新的确定性语义、原子性或锁序问题，可以进入主控的完整影响回归与正式 B 验收；这不是性能通过结论。** 本节仅追加，不撤销此前正式失败记录，也不把诊断夹具视作正式生产回填。

### 生产语义覆盖矩阵

| 检查点 | 文件证据与结论 |
| --- | --- |
| old / current / created | `refresh.go` 仍先读旧有效身份，再刷新事实和有效署名，合并旧身份、当前身份及本次新建身份后刷新 profiles。`names.go` 只在实际 INSERT 新身份时记录 created；`facts.go` 显式返回。被 M3 遮盖的新 scan 身份仍有派生，移出作用域的旧身份也不会漏刷；没有回到全库 profiles 刷新 |
| seed、原文和首次归属 | 事实解析保留原文及 scan/correction 层。预取虽排序去重，但随后的 ensure 仍按原事实顺序调用，不改变首次创建身份的 initial_name/origin；已有名字不会被重写 origin。事实仍按原键 upsert 保留 ID，不以重扫创建新治理意图 |
| scope / 路由 | release 与 track 两分支取代跨表 OR；0026 的 subject 互斥约束使两分支不重复。track 分支保留所属发行供 release override 使用；优先级仍为 track override → release override → 全局 binding → initial，再取一跳 redirect；发行 correction 层选择不变 |
| subject-name 去重及 missing | 轨票按 artist/name 的 DISTINCT track 计数减非 present 的 DISTINCT track，且仅保留正票；在现有轨 FK/source_status 约束下等价于只计 present 的原票集合。发行票先 DISTINCT artist/release/name，再要求发行有 present 轨，跨角色不增票。missing 仍保留事实、有效署名和可搜索名字，不参与主名票数 |
| 主名与搜索别名 | 热门票排序仍为票数降序、原文 C 排序、name ID；零票回退完整 owned 名字，再回退 initial_name，人工名优先。owned 包含全局/一跳路由和实际有效作用域名字；redirect 来源人工名及当前显示名仍进入搜索值 |
| 名字集合无截断 | `ownedNames` 递归每次取严格更大的下一个 name_id，LIMIT 1 是逐步枚举，不是集合上限。预取每批256项且遍历全部输入，没有总数 cap；未命中继续 ensure，不把预取缺失当作事实不存在 |
| 哈希与全文 | 命中 key hash 后比较完整 normalized_key，命中 spelling hash 后比较完整 spelling；ensure 保留同样校验。既有单名字/规范化键预算没有被批大小替换；碰撞报错而非复用错误名字 |
| 原子性 / 代次 | 范围 DELETE、facts upsert、effective、profiles、search names、候选与 release marker 仍使用调用者同一事务。profiles 批量写入仍保留 version/applied_generation；搜索仍写同一 generation。任何错误向上传播，未自行提交；原文展示先更新也依赖整事务回滚，不是允许部分成功 |
| 锁和 0029 | graph 仍要求调用方已取 CATP，没有新增 OPER/对象锁或独立全库维护事务。0029 仅加 `(artist_id,name_id,track_id,release_id)` 索引，未改0026–28、Version、状态或业务数据，没有 planner 开关或业务 ANALYZE。索引构建部署成本、磁盘/WAL和写放大不能据此忽略 |

这里只确认请求集合驱动查询及语义保持，**不以 artist/profile 数量限额、256预取或索引存在来证明查询成本有界到200ms**。热门身份计票仍需处理其有效署名；单个巨大 subject 必须完整处理，仍可能触发正式 max 门槛。

### 新增测试的实际断言与边界

- `names_batch_integration_test.go`：300个已有名字跨预取批，增加同规范化键变体和真实新身份；检查303条有效 track_artist、301份目标代次 profile、seed origin及身份复用，并在回滚后检查新身份不存在。另分别篡改完整规范化键/原文而保留哈希，要求 collision 错误及事实不新增。这不是只检验函数返回 nil。
- `profiles_batch_integration_test.go`：260个仅通过作用域归属到新目标的名字，不能靠全局名字查询掩盖递归截断；断言完整260搜索名字、发行票与轨票共同选中 Alias 259，并以旧票定义对照主名。全部轨 missing 后回退 Alias 000，260别名仍存在。该测试不是对所有路由/人工名组合的穷举，新旧票数对照也只验证本夹具胜出主名；相关旧 scope/review/masked 测试仍须一起回归。
- `backfill_sql_diagnostic_test.go`：显式 opt-in benchmark，完整规模诊断 fixture；真实 Refresh 前后对比 facts/effective 业务字段，重放原 SQL/参数取 EXPLAIN，另外以真实 Refresh + COMMIT 记录提交与CATP持有。没有以 rollback 耗时替代 COMMIT。其集合预置680000条派生仅用于诊断，**不证明从空派生的生产回填耗时**，业务快照相等也不等于所有 profiles/search/治理语义已穷举。
- `maintenance/artist_workload_integration_test.go` 三项补齐第六节原先仅字符串/回调的缺口：真实SQL估计覆盖missing与各原始输入、不串发行，超预算显式 incomplete；小→完整大subject→后续小/空发行的实际批次与Initialize续跑；破坏输入表使估计报错，确认完成进度/marker不前进，再证明当前代次在线已完成项跳过坏输入且不改credit ID，恢复输入后续跑。大subject实际保留100轨/200署名，非仅调度回调模拟。

主控最新报告 maintenance 新3项 PG race PASS 4.529s、全包 race PASS 32.221s／47项含子测试／0 SKIP，及全影响 vet PASS；这些是**主控/owner已执行结果，本检查没有重跑**。完整影响 race（主控 session39636）当时仍在运行，本报告不先行声明其通过。

### 诊断证据核验与性能剩余风险

读取仓外 `backfill-sql-b/report.md`、`comparison.json`、`final-source-sha256.json` 及热门单发行有/无索引的原始 plans：

1. 用 Python hashlib 核验 SHA 清单全部 **16个当前文件均匹配**，包括四生产文件、0029及三个新增测试；不是只核验报告中展示的 SQL 字符串。
2. 原始计划与 comparison 对应：相同最新 SQL，热门单发行无索引主名 cost208256、执行418.990ms、JIT135.038ms；有索引 cost44757.39、执行118.441ms、JIT0。credit_names CTE 79.189→0.321ms、named 86.974→7.750ms 与报告一致。证据支持算法与索引组合的解释，不证明单独加索引普遍有效。
3. 相同样本真实提交 CATP 471.56→172.22ms；另一个热门单发行468.16→173.43ms。明确保留反例：100missing单发行268.70ms；10missing发行批2377.36ms，仍超过1秒；冷门和missing并非稳定受益。索引约49.6MiB。没有用改善比例覆盖失败样本。
4. EXPLAIN 重放的计划/JIT耗时与另一次真实提交墙钟分开，不相加或互相替代。SQL诊断7组样本不能构成正式所有已提交应用批总体p95；profile scope和maintenance工作量预算也不能替代该分布。
5. 仓外报告记录 graph 全包 race 14.191s／17个顶层Test+1个Fuzz种子入口／0 SKIP及vet/build；这是owner既有结果，本检查仅核对文件/证据，没有执行该命令，也不把诊断 benchmark 算作普通Test通过。

**尚待主控：** 完整影响 race最终结果；原规模从空生产联合回填≤36分钟、全部已提交应用批CATP总体p95≤200ms/max≤1秒、艺人building期间真scan/M3完成证据、成功采样退出后原5warm30搜索300/1000ms与完整10000队列，以及取消清理收证。任一门槛失败仍须停止并报告。此前失败不被本节抹去，三个真实用户音乐opt-in的安全约束未运行边界也未改变。

本次实际操作仅 `cat/sed/rg/git status` 文件读取及 Python JSON/原始计划读取、SHA-256核验，并追加本报告；**未运行 DB、测试、race、benchmark、编译或vet，未修改生产/测试/spec/前端文件**。不重复验证是为遵守主控独占DB及本轮只读授权。

## 八、首轮完整影响 race 依赖失败后的最小修复复核（2026-11-04）

**结论：maintenance→metadata 直接依赖的违规路径已从当前源码移除，静态闭合；测试层面的闭合待主控修后重跑。本次未发现估计语义变化或通过放宽 allowlist 绕过失败。** 不重复审查上一节SQL，也不将首轮2190项通过称为整轮通过。

- 已读取仓外 `backfill-b-final/race.json` 和 `first-race-summary.json`：2190条测试PASS、唯一测试FAIL为 `app.TestPackageDependencyDirections`，原始输出明确为 `internal/catalog/maintenance must not import internal/catalog/metadata`。同时保留3个真实用户音乐测试SKIP；整轮不是0SKIP/PASS。
- `maintenance/artist_batch.go` 已去除 metadata import，逐raw成本现在调用 `artistgraph.ArtistInputNameCost(raw)`。新增 `artistgraph/input_cost.go` 的唯一算法仍为 `max(1, len(metadata.CanonicalArtistNames(raw)))`，复用权威拆分，不复制算法、不改变去重/AC/DC/多值规则，空有效名字仍至少计1。该纯端口无DB、锁、缓存或写入副作用。
- 调用仍位于原输入rows循环中，`cost.Names +=`、超预算比较、`EstimateComplete=false`、轨数早停、输入SQL及剩余预算均保持；没有把成本变成有效事实数，也没有改变完整首个超额subject的调度规则。
- 当前 `dependency_rules_test.go` 中 maintenance allowlist 仍仅允许 artistgraph/textnorm/searchindex/writegate，没有 metadata；artistgraph 原本已允许 metadata，facts/candidates也使用它。依赖测试仍遍历包并逐项拒绝未允许import，没有新增跳过、泛匹配或专门豁免。本任务相对HEAD已有graph等规则增量，不把它们误称此次修复新增或“整个规则文件从未改动”。
- `input_cost_test.go` 将实际成本测试放在graph：100名字、多值2名字、重复名字去重、AC/DC不拆及无有效名字至少1的断言均存在；maintenance继续保留选择/预算/续跑测试。这里确认的是断言内容，**没有亲自执行测试或声称新增测试PASS**。

本次实际仅 `rg/sed/git diff` 与 Python读取首轮日志，并追加报告；没有DB、测试、编译、vet或性能命令。修复新增两个graph文件不在上一节旧16文件SHA清单内；上一节SHA核验描述的是当时清单，不能当作本次新增文件的owner执行证据。等待主控再次完整race与正式B验收，不覆盖历史失败。
