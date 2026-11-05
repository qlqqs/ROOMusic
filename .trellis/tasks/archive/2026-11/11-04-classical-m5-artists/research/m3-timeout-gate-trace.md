# Research: M3 修正超时、503 映射与 CATP 持锁工作量

- Query: 追踪 M3 correction 超时来源与 503 链路；核对设计 §3.3、§6、§15 D2 的在线写入/短批要求，定位扫描、后台及图刷新中的扩大工作量路径，并给出原规模采样与新增回归上限建议。
- Scope: internal（只读代码与合同；未启动/连接数据库、未运行服务或测试）
- Date: 2026-11-04

## Findings

### 1. 结论与证据等级

1. **已证实：基准中的单请求 45 秒来自测试请求 context，不是生产 middleware、service 或 CATP 的默认超时。** 当前 M3 写接口直接调用 `operations.Run`，没有独立 correction service 追加 deadline。
2. **已证实：只改 label 也会调用完整 `artistgraph.Refresh`。** 其 `affected` 无条件并入全库缺少 profile 的艺人，随后逐艺人 upsert profile。因此“单发行输入”不等于“只处理该发行涉及的艺人”。
3. **未证实：该扩大路径就是本轮 45 秒/503 的实际主因。** 扫描候选、maintenance、M3 自己都能调用同一图刷新；必须区分请求在等 CATP，还是已经拿到 CATP 后自己执行很慢。静态 SQL 也不能证明实际发生了全表扫描、磁盘溢出或哪个节点最慢。
4. **已证实：maintenance 有自适应批量与提交后 50ms 让出，但没有每批 200ms 硬 deadline。** 批量缩小发生在当前批完成之后，不能中断一个已经过大的 Refresh，也不能限制全库孤立身份补齐。
5. **合同没有 M3 的独立数值 SLA。** 100–200ms 是后台持 gate 的调优目标；300ms/1s 是搜索首屏目标，不能冒称是既有 M3 时限。建议新增的小发行修正回归使用 **5 秒单请求硬上限、p95≤2 秒**，明确作为工程回归预算而非已批准产品 SLA；不得通过放宽原 45 秒或缩小夹具规避问题。

### 2. 文件地图

| 文件 | 作用 |
| --- | --- |
| `backend/internal/app/app.go` | 组装真实路由、middleware、扫描 service 与后台 maintenance |
| `backend/internal/platform/httpserver/middleware.go` | 请求 ID、状态与耗时日志；不设执行 deadline |
| `backend/internal/catalog/httpapi/corrections.go` | 管理员/同源校验、PATCH correction、错误映射 |
| `backend/internal/operations/operations.go` | READ COMMITTED 事务、OPER 幂等锁、业务执行、原子记录、503 分类 |
| `backend/internal/catalog/governance/corrections.go` | CATP→发行行锁→修正→revision→派生刷新 |
| `backend/internal/catalog/governance/artist_state.go` | M3 通用发行刷新组合：图后搜索 |
| `backend/internal/catalog/internal/writegate/gate.go` | CATP 事务 advisory lock（`:14-18`） |
| `backend/internal/catalog/internal/artistgraph/{refresh,facts,profiles,names,candidates,state}.go` | 受影响艺人集合、署名事实、有效关联、主名/名字检索及候选 |
| `backend/internal/catalog/internal/searchindex/write.go` | 单 subject 搜索投影刷新与输入 revision |
| `backend/internal/catalog/maintenance/{worker,batch,artist_worker,artist_batch,artist_publish,publish}.go` | 选主、短批、自适应、发布与补缺 |
| `backend/internal/catalog/ingest/{persist,artist_projection,scan_feedback_missing}.go` | 扫描 candidate、旧发行收集、missing 两个不同入口 |
| `backend/internal/library/scanner.go` | 扫描终态事务、CATP 与 30 秒终态预算 |
| `backend/internal/app/catalog_scale_benchmark_test.go` | 原规模 HTTP 基准、45 秒请求、CATP acquire tracer |
| `backend/internal/app/testdata/search-scale.sql` | 预置两万身份而不预造 profiles 的原规模夹具 |

### 3. M3 请求链及错误映射

```text
app.New → httpserver.Middleware → catalog HTTP correction
  同源 + RequireAdmin + ID/key/body
  operations.Run(request.Context())
    BEGIN READ COMMITTED
    OPER(actor:key) → 查幂等记录（已有成功结果直接重放）
    CorrectionExecutor
      CATP → release FOR UPDATE → revision 校验
      写/删 correction → edit_revision+1
      refreshGovernedRelease
        artistgraph.Refresh([releaseID])
          generation → affected(old) → facts → effective
          affected(current) → profiles/search names → candidates → marker
        searchindex.RefreshRelease
      loadCorrectionState
    INSERT operations → COMMIT
  WriteJSON(200) 或 writeOperationError
```

- 装配：`app/app.go:72-84,94-104`；middleware 仅在请求 context 上加 observation（`platform/httpserver/middleware.go:23-31`），调用 handler 并记录状态/总耗时（`:39-65,81-99`），没有 `WithTimeout`/`TimeoutHandler`。
- PATCH：`catalog/httpapi/corrections.go:60-102`，请求 context 原样传入 `Run`（`:94`）。不是先调用一个另设超时的 correction service。
- `Run`：`operations/operations.go:182-211` 开事务/OPER/读取幂等记录/执行；`:229-247` 写记录与提交；`:189` defer rollback。幂等命中在 CATP 之前返回（`:199-203`）。
- `lockVisibleRelease`：`catalog/governance/corrections.go:285-296`，CATP 在 `FOR UPDATE` 前；执行器 `:307-324,347-360`。任何真正改变状态的字段都走 `:357` 图刷新，不只 `album_artist`。
- 组合刷新：`catalog/governance/artist_state.go:226-230`，图刷新失败直接返回，不执行后面的搜索更新。
- 错误链：`operations/operations.go:209-211,254-289`，executor 返回 `context.Canceled` / `context.DeadlineExceeded`、网络错误或 PG `08/53/57/58` 类错误 → `ErrUnavailable`；`Problem` 在 `:113-116` 映射为 `503 database_unavailable`，其它非业务错误通常为 `500 operation_failed`。BEGIN、OPER 加锁、幂等记录读取失败有各自直接 `unavailable` 分支（`:185-205`）。
- HTTP：`catalog/httpapi/corrections.go:25-31` 记录 `governed operation failed`、request_id、status、包装 error 后输出安全错误信封。**503 不等于数据库宕机**，可能仅是这个请求的 deadline 到了；也不能只凭状态码反推出取消发生在哪条 SQL。
- 特别注意 PG SQLSTATE 差异：当前分类器将 `57014` 纳入 57 类；`55P03` 不在该集合。不要把任意 lock_timeout 都直接描述成同一种 503；以实际包装 error/SQLSTATE 为准。
- 超时后必须用同一 Idempotency-Key 对账；不能随便生成新 key 重放不确定提交。事务中断时 correction/revision/profile/search/operation 应一起回滚，需实测确认，而非看见 503 就认定没有提交。

### 4. 现有时间预算与来源

| 数值 | 代码来源 | 实际意义 |
| --- | --- | --- |
| 45s | `app/catalog_scale_benchmark_test.go:282-300` | 每个 httptest 请求的 context，包含鉴权、连接等待、OPER、CATP、业务 SQL、返回处理；不是生产配置 |
| 45min | 同文件 `:220-221` | 整个规模基准的父 context；与单次 45s 不同 |
| 12 连接 | 同文件 `:235-238` | 该基准的 max open/idle pool；不能假设生产也为 12 |
| 5s | `cmd/roomusic/main.go:65-69` | 仅 `ReadHeaderTimeout`，不是 PATCH 执行时限；此处没有配置 ReadTimeout/WriteTimeout |
| 10s | 同文件 `:82-84` | HTTP 停机等待，非在线 M3 预算 |
| 30s | `library/scanner.go:147-158` | 扫描终态 context，覆盖 flush/finalize，不是普通扫描 candidate 或 M3 的请求超时 |
| 500ms | `catalog/ingest/scan_feedback.go:83-91` | 反馈保存点的局部 lock_timeout，恢复原值；不是 CATP 的默认等待时限 |
| 500ms | `library/scan_report.go:132-140`、`library/scan_progress.go:176` | 扫描报告/进度局部 statement_timeout，不是 M3 通用预算 |
| 100–200ms | `catalog/maintenance/worker.go:162-169` | 已完成批次的自适应判断区间，不是超时取消 |
| ≥50ms | `maintenance/worker.go:155-157`、`artist_worker.go:75-77` | 每批提交后让出；持锁阶段不让出 |
| 1s | `maintenance/worker.go:63,72-75` | 失败/未选主退避，及独立的选主锁释放清理 context |

`catalog/internal/writegate/gate.go:14-18` 的 CATP 实现只有 `pg_advisory_xact_lock(..., 0x43415450, 1)`，接受调用方 context，未自行设置 deadline/lock_timeout。`platform/postgres/migrate.go:52-66` 的生产连接入口也未在此设置通用 statement_timeout；**外部 DSN、数据库/角色配置仍须主控采样 SHOW，静态读取不能声称实际配置为零**。

### 5. 设计合同的精确期待

共同设计为 `.trellis/tasks/11-04-classical-m5-search/design.md`（artists 的 design.md 只是指向它）。

| 行号 / 章节 | 明确期待及边界 |
| --- | --- |
| `:109-111` / §3.3 | HTTP/ready 立即服务；目录、播放、历史、已有个人功能、扫描与 M3 不等待回填 |
| `:118` / §3.3.1 | maintenance 选主锁只排斥维护者，不能阻挡扫描/M3；专用连接及单 worker |
| `:126-128` / §3.3.2 | 扫描/M3 在 CATP 内同事务双写；写请求可等待**短批**，不能异步丢失一致性 |
| `:130` / §3.3.2 | 起步 50 轨/10 发行、上限 500 轨/100 发行；持 gate 约100–200ms 调优，批间至少50ms；超大发行需可恢复子批及 revision 防旧值覆盖 |
| `:134-138` / §3.3.3 | 短 CATP 发布，只核对完成/缺口标记；不在发布事务重新规范化全库；失败不把全站拖成503 |
| `:142-144` / §3.3.4 | 每批日志；十万轨同时跑播放/搜索/扫描/M3，记录 gate 等待和前台 p95；联合16–36分钟只是未实测后台墙钟预算，绝非停机或单请求容忍值 |
| `:329-331` / §6.1 | 扫描 candidate 同事务解析与刷新；不能扫描结束再第二遍补图 |
| `:349-360` / §6.3 | CATP 仅目录相关写事务；不包整个扫描/文件读取；记录扫描每 candidate 耗时与治理等待；先 OPER/必要旧记录，再 CATP，再业务锁 |
| `:958,962-967` / §15 D2 | 再次明确在线升级、旧路径继续服务；艺人未发布的专门503/409并不授权让 M3 修正不可用 |
| `:984` / §15.3 | 时延和后台耗时尚待实测，要求联测争用与重启；不是静态设计已达标 |

**不存在“允许 M3 在回填时等45秒”条款，也没有独立 M3 数值承诺。** 搜索300ms/1s阈值不能移植为既有修正合同。应把可用性、短 gate 与局部刷新作为新回归预算的理由，并单独记录工程选择。

### 6. 短批现状与全库工作可能性

#### 6.1 maintenance：数量受限不等于持锁时间受限

- `maintenance/worker.go:39-46,81-85` 在专用连接拿 CATM，先搜索后艺人。`runSearchLocked :116-153` 从10发行/50轨起步，自适应上限100/500；`:162-169` 超200ms减半、低于100ms增加，下限1。
- `maintenance/batch.go:12-21,55-63,83-86` 开事务→CATP→逐 subject Apply→提交，Duration 从开始事务之前计到提交后。**batch_ms 含取锁等待，不等于纯持锁时间。** `searchindex/write.go:50-52` 明确发行 Apply 不展开整张发行的曲目，曲目独立任务。
- `maintenance/artist_worker.go:39-75` 艺人按10发行起步、上限100；`artist_batch.go:63-73,88-90,115-126,144-147` 只有发行数 LIMIT，整批调用一次图 Refresh；未看到发行内按轨的可恢复 cursor/revision 子批实现。
- `artist_batch.go:77-82` 清单耗尽后额外 `Refresh(nil)` 补孤立身份；它在 CATP 内，无数量上限，且该分支没填写 Duration、BatchSize=0，不产生普通批次日志。不能只用已记录的 batch_ms 分布代表所有维护持锁区间。
- `maintenance/artist_publish.go:18,38,50-66` 发布 gate 内检查所有发行缺口、读完整艺人映射并调用收藏刷新端口；`artistgraph/state.go:39-54` Mappings 查询全体 artists。是否造成长暂停要测，不能因为叫 publish 就当常数时间。

#### 6.2 图 Refresh：可证实的扩大范围

- `artistgraph/refresh.go:19-38,49-52` 的 `affected` 调用两遍；第一部分限定传入发行，**UNION 第二部分是全库 `artists WHERE NOT EXISTS artist_profiles`，无发行/数量约束**。
- 原规模夹具 `app/testdata/search-scale.sql:1-17` 预置20,000个身份及4,000不同 key 别名，明确不预造 profiles。若采样时这些 profile 仍缺失，则扫描探针或一次 label correction 的 affected 集合会包含它们。**这是条件化的数据流结论；谁先执行、是否已被其它事务填完，必须看当时采样。**
- `artistgraph/profiles.go:21-31,53-67` 聚合 votes/fallback 后逐 profile 执行一条 INSERT…ON CONFLICT；当返回两万个 profile 时会有相应规模的客户端/数据库往返。即便查询计划高效，也不能被“批里只有一个发行”限制。
- 同文件 `:12-15` 的 ownedNames 是名字路由和全体 effective credits 的 UNION；`:25-31,71-75` 再使用它。**其文本具有全库关系来源，不等于执行计划必定全库 materialize**：应捕获生产 SQL 的真实参数、计划、循环次数、buffers、temp I/O 后再判断谓词下推/重复计算。
- votes 的 `c.artist_id=ANY(...)` 按受影响艺人限定，但某位热门艺人可关联跨库大量轨/发行，故即使修复了孤立身份扩张，也仍需在五万轨热门身份倾斜下测主名计票。
- `artistgraph/facts.go:98-119` 读取传入发行的全部曲目，无轨数 LIMIT；`:126-159` 遍历，原文展示变化还会触发逐轨 `searchindex.RefreshTrack`。`:166-175` 事实集合替换；`refresh.go:67-84` 有效关联替换；`candidates.go:23-29,95-112` 候选重算。它们的 SQL 语义均针对传入发行，不应概括为“所有步骤都在刷新全库”。
- `artistgraph/names.go:19-58` 每次 Refresh 使用本地 resolver cache，同一不同拼写至少走 key/name 查询，新名字还会插入；需观测调用次数，不能仅统计最慢一条 SQL。
- `searchindex/write.go:17-47,144-147` 正常 M3 发行搜索刷新针对单个 release；不应把艺人图的全库风险错误归给这一步。

#### 6.3 扫描与 M3 互相阻塞的具体持锁区间

- candidate：`ingest/persist.go:49-65` 拿 CATP 后开始目录事实工作；`:156-165` 图→搜索→反馈→COMMIT。**扫描探针也可能先补全库缺失 profiles，让 M3 只是在等待扫描的 gate。**
- M3：`governance/corrections.go:286-291` 获取 CATP 后一直到 `operations/operations.go:244` 提交，包含完整图和搜索、返回状态读取与 operation 插入。维护者也可能反过来被该 M3 阻塞。
- 实际终态入口：`library/scanner.go:198-238` 从 CATP、scan_run 行锁，到 `MarkMissingSourcesWithFeedback`、反馈终态及提交，全在一个事务。`ingest/scan_feedback_missing.go:10-13,35-69` 虽然每次256轨，但所有批次共用同一 tx，**没有每256轨释放 CATP**。
- 不要误追旧入口：`ingest/persist.go:278-310` 的 `MarkMissingSources` 调 `refreshMissingArtists`（`artist_projection.go:51-73`）；但 scanner 当前调用的是上述 WithFeedback 入口，当前读取版本的后者只刷新搜索可见性，未见图刷新。此为额外正确性核查点，不能把旧入口的全图成本直接记到当前扫描终态上。基准合成大根 disabled（夹具`:24`），只扫探针，不应先假设发生了十万轨 missing 对账。

### 7. 主控原规模采样应观察什么

1. **保留原夹具和并发顺序。** 基准 `app/catalog_scale_benchmark_test.go:351-369` 启 maintenance/真实探针扫描；`:406-412` 在回填期间发一次 label PATCH；它可能碰到搜索阶段而不是艺人阶段。记录两能力 phase、profile 缺口数、当前 candidate 与执行顺序。
2. **按 PID/事务建立 CATP 持有者与等待者链。** 周期读取当前隔离库的 `pg_stat_activity`（state、query_start、xact_start、wait_event_type、wait_event、query）、`pg_locks` 的 advisory granted/键、`pg_blocking_pids(pid)`；带 application_name/request_id 的对应关系。注意 OPER 先于 CATP；不能把所有 advisory 等待都命名为 CATP。
3. **拆解请求时间**：鉴权/取连接、OPER 等待、CATP acquire、CATP 持有（拿锁成功到 COMMIT/ROLLBACK）、图各步骤、搜索刷新、写 operation、提交。另记录 DB pool WaitCount/WaitDuration/InUse，排除先在连接池里等待而看不到 SQL 的情形。
4. **重点 SQL**：`affected` 两次返回数；`WITH votes … owned …` 返回 profiles 数；profile upsert 调用数/累计时长；ownedNames搜索名字 SELECT；事实读取/替换、effective DELETE/INSERT、候选 SQL；两类发布 gap 检查与完整 Mappings。计划必须对实际 SQL/参数，不能手抄简化查询替代；写 SQL 的 EXPLAIN ANALYZE 只能由主控在授权隔离事务中执行并回滚。
5. **记录错误本体**：deadline 时间戳、错误链、SQLSTATE、末条 SQL 和 PID，503 request_id；采样结束核对 correction、edit_revision、operations 是否原子提交/回滚，并测试原 key 重放。
6. **当前 tracer 不足以定根因**：`catalog_scale_benchmark_test.go:58-74,104-125` 的 gate 指标只是 advisory 获取语句耗时（含往返），不含后续持锁；`:74-87,128-136` 的详细 SQL 采集要 `enabled=true`，而在线阶段 `:342-344` 只设置 monitorGates。后台/扫描/M3 的 acquire 样本混在一起，不能据汇总直接知道谁持锁。M3 的 dbMS 返回值在 `:407` 被丢弃。
7. **p95 样本不足**：`corrected` 只允许一次 correction（`:368,406-412`）；成功一次只能说明那次耗时，不能声称稳定 p95。失败会在写入 foreground 样本之前 Fatal（`:408-411`），所以还应保留失败请求的耗时/阶段，不只成功汇总。
8. SHOW `statement_timeout`、`lock_timeout`、`idle_in_transaction_session_timeout` 及配置来源，记录版本与池容量；外部配置可能早于45秒触发，不能凭源码排除。

### 8. 推荐新回归预算与覆盖方式（建议，非现有合同）

- **新增小发行 M3 PATCH 单次硬上限5秒**，使用真实 Handler 和请求 context；同时测成功耗时，多样本目标 **p95≤2秒**。5秒是为100–200ms后台短批+局部事务保留较大环境余量的工程护栏，不是把后台调优目标直接当 HTTP deadline。由主控确认后实施，不能自动改生产超时。
- 原45秒诊断采样可保留到定位结束；新的5秒回归应与诊断目的区分，不能仅把45改5后宣称根因已修复。预算超限应失败并保存 holder/SQL证据，不能通过重试到成功掩盖第一请求停顿。
- 对 cold preparing（大量无profile身份）、搜索回填中、艺人回填中、ready 热状态分别测同一规模；至少覆盖普通 label 和 album_artist set/clear/revert。第一次冷启动不得被预热悄悄消除；后续p95需足够的独立写操作/样本及新revision，避免同键成功重放变成假快路径。
- 独立记录后台**真正 gate hold**分布，100–200ms作为合同调优目标；发现大批/单subject无法满足则应评估有界子批、受影响集合隔离与 SQL，而不是把 batch_ms 改名为 hold 或无限降批后仍让孤立身份全库刷新。
- 功能断言要与时延一起保留：ready/播放正常；M3提交立即可搜，清除/撤销恢复；回填不得覆盖新值；无关艺人不应由小发行事务无界补齐，但真正受影响热门艺人的正确计票不能为提速省略；失败无部分写入。
- 锁竞争测试用 barrier/持锁连接明确制造一次短批，再确认 M3 在该批提交后继续；另测 M3 在持锁期取消的回滚。避免仅靠 sleep 偶然交错。

### 9. 相关 specs 与版本

- `.trellis/spec/backend/governance-contracts.md:44-64,201-207`：Run/503/原子事务及 M5 派生同事务要求。
- `.trellis/spec/backend/artist-contracts.md:13,22,30-32,57-64,75-77`：图刷新入口、修正层、短批与锁顺序、整理错误分流、原规模不可缩小。
- `.trellis/spec/backend/search-contracts.md:29-39,81,85`：同事务输入revision、后台短批、锁等待测试和 F 线实际 missing 入口。
- 已读取后端 index、database-guidelines、error-handling、scan-coordination-contracts；本次没有修改 specs。
- 外部依赖只核对仓库 `backend/go.mod:3-13`：Go 1.25.0、pgx/v5 v5.10.0；图版本 `artistgraph/state.go:11` 为1。未联网研究库/数据库最新行为，未将任何外部资料当作本次实测证据。

## Caveats / Not Found

- 本报告是静态调用链与合同研究，**没有实际 SQL/锁等待采样、执行计划、运行日志或耗时数据，未认定根因**。数据库由主控准备并采样，本 agent 未启动/连接数据库，未改生产/测试。
- 遵循 researcher 角色隔离，没有加载 implement.jsonl/check.jsonl；直接读取已提供任务的 prd/design/implement 及其指向的共享设计和相关 specs。
- 未运行单测/全量测试：此次只读研究且无代码修改，用户明确将动态验证交给主控。实际使用的核查命令为 `cat`、`nl -ba ... | sed -n ...`、`rg -n ...`、`find`、`ls`、`wc -l`；只创建 research 目录及本文。没有 Git 操作。
- 行号基于本次读取的工作树；并行实现可能改变文件内容，主控引用前应重新定位。5秒/2秒仅为新增回归建议，不是既有数值合同或达标声明。
