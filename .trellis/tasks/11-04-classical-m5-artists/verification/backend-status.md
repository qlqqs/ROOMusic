# M5 ⑤后端验证记录

## 状态

**本轮结论（2026-11-05，I8）**：完整回填及回填期在线指标PASS；多字符搜索明确FAIL。唯一正式运行在90分钟总上下文到期时结束，4/26组新搜索完成，单字符/完整计划矩阵/10000首队列未全部采集；任务仍未完成。已停止，不重跑或降低门槛，隔离资源已正常清理。

**上一轮结果（2026-11-04 23:58 UTC；按当时主会话口径）**：A四接口/35样本已交付。B修后完整2191项race PASS、PG集成0SKIP、vet/依赖门禁PASS；正式基准在16分24.957秒因CATP max **1462.520ms>1000ms**停止。失败批COMMIT占1281.200ms，采样为WALWrite/WalSync；未完成联合回填、未进入正式搜索/队列测量，不重跑或降低门槛。隔离资源已清理；完整数字与证据见本文最后一节。

2026-11-04，步骤5实现已落盘，但完整规模验收失败，已按用户要求停止；未提交、未推送，任务不能标记完成。前端及嵌入产物由并行代理拥有，本会话不修改。

**历史结果（21:48 UTC）**：原M3 503已据锁采样修复，原规模M3返回200（2490.969ms）；影响范围race/vet通过。但本轮正式基准45分钟总context到期，艺人发行回填仅2288/10001，完整验收仍FAIL，未进入正式搜索p95/EXPLAIN/队列。详见文末；前述旧失败记录保留作历史。

改动清单见同目录 `backend-files.txt`。大体积原始证据在仓外：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/

## 迁移与实现

- 0026：艺人/名字键/写法、署名事实及有效关系、人工绑定/作用域/主名意图、搜索/主名派生、标签证据、候选/忽略、回填发行标记、艺人操作目标及类型约束；原周杰伦/周杰倫/Jay Chou硬编码改种子。
- 0027：personal拥有的typed `artist_favorites`，保留原锚点及resolved列，共用本人收藏revision。
- 0028：profile完成代次与独立artist工作清单类型；历史孤立身份移入后台有界阶段，不回写既有0026/0027。
- 0029：`artist_effective_credits(artist_id,name_id,track_id,release_id)`覆盖索引；同SQL完整规模对照确认收益，不改Version/ready或旧迁移。
- ingest同事务解析署名，与扫描反馈和搜索投影共同提交；治理经R4 operations.Run及app注入收藏端口，扫描不写人工意图。
- maintenance短批续跑/补漏与原子发布；读503 preparing、写409 not_ready；共享摘要未ready时refs为空数组。

## 接口与样本

所有路径以 `/api/v1` 为前缀：

- GET `/artists`、`/artists/{id}`、`/artists/{id}/names`、`/albums`、`/tracks`、`/queue`（后四项位于 `/artists/{id}` 下）。
- POST `/artists/{id}/merge`、`/split-preview`、`/split`、`/candidate-ignore`；PATCH `/artists/{id}/name`。
- GET `/artist-candidates`。
- GET `/me/favorites/artists`；PUT/DELETE `/me/favorites/artists/{id}`；POST `/me/favorites/lookup`增加artist_ids。
- GET `/search`、`/search/suggestions`增加艺人/别名；`/catalog/status`增加艺人状态；`/operations`增加艺人筛选/目标描述。

真实app handler样本位于 `backend/internal/app/testdata/api-samples/` 的 artists、governance、operations、favorites、search、catalog 分区。旧history/playlist/catalog公共摘要样本同步refs。最终测试结果待补。

## 已同步规范

backend的library-scan、governance、catalog-rest、search、personal合同；新增artist-contracts及catalog-summary-contracts，更新index/directory-structure/database-guidelines。ADR0002/0003仅追加decisions行，不改正文。

## 验证边界

- 用户命令中的 `./internal/governance/...` 不存在，实际包为 `./internal/catalog/governance/...`，已包含在catalog范围。
- 不读取用户真实音乐目录；三个TestRealMusic opt-in用例须如实单列，不用合成目录冒充真实音乐覆盖。
- 正式性能必须完整100000 present+10000 missing/10000发行/约8000组/20000艺人/20%别名/50000轨倾斜；5次预热30次测量；首个门槛失败即停止并报告。不引用已取消的旧二栏benchmark作为⑤证据。
- 只使用自行创建的roomusic-test-m5s及随机库；第一轮已down -v并让出前端窗口，前端19:04:54Z释放后重建第二轮。第二轮清理结果待补。

## 已完成的局部验证

命令均在backend目录；PG测试加载本轮随机隔离库环境，不把连接串写入仓内。

| 实际命令/检查 | 结果 | 证据与说明 |
| --- | --- | --- |
| `go vet ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/...` | PASS | 主控及独立check各执行；最终新增测试后仍需复验 |
| `go test ./internal/app -run '^TestPackageDependencyDirections$\|^TestRoutesResolveToRegisteredPatterns$' -count=1` | PASS | 0.032s |
| `go test -race ./internal/personal/... -count=1` | PASS | personal-tests.json |
| `go test -race ./internal/catalog/maintenance -count=1` | PASS | maintenance-tests.json；发布、续跑、缺回调fail-closed |
| `go test -race ./internal/catalog/internal/artistgraph -run '^TestPostgreSQLArtistGraphReview' -count=1 -v` | PASS | 独立check三项、0 SKIP，4.624s；普通构建3.183s |
| `go test ./internal/catalog/governance -run '^TestPostgreSQLArtist(ParticipationReleaseAndTrackUnion\|ReleaseOnlyParticipationBudgetBoundary)$' -count=1 -v` | PASS | 发行级+轨级去重，100000/100001边界；11.114s |
| `go test ./internal/catalog/governance -run Artist -count=1` | PASS | 25.602s；含治理范围修复 |
| `go test -race ./internal/catalog/ingest -run '^TestPostgreSQLSearchFeedback' -count=1 -json` | PASS | scan-search-feedback-epoch-fix.json；13.018s；此前旧epoch断言失败保留于scan-final-recheck.json，修复为完整公开catalog代次，不改事务/数量语义 |
| 五组真实API样本无UPDATE race复验＋recorder ownership | PASS | all-five-samples-tests.json；134.748s，0 FAIL/0 SKIP；⑤84个新增样本 |
| 89个旧样本结构/匹配语义审计 | PASS | legacy-sample-semantic-audit.json、legacy-seven-match-review.json；仅有意refs、准备态/epoch、artist_id补充；不扩展credit到兄弟轨 |
| `git diff --check`（仅后端、后端spec与ADR） | PASS | 未检查/改写并行前端工作区 |

独立check发现的真实重扫与M3 claim/revert新增图断言缺口，已派owner补最小跨层回归；不能仅用graph直接Refresh冒充扫描验证。


## 完整race首轮及补充回归

实际完整命令（启用CATALOG_EXPLAIN、两旧规模诊断输出路径和固定ffmpeg；未设置真实音乐ROOT）：

```sh
go test -race -p 1 ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/... -count=1 -timeout=40m -json
```

首轮 **FAIL**，不是整体通过。23个包PASS，app失败；2051个测试/子测试PASS、15个失败项（含父测试汇总），3个真实音乐opt-in SKIP；PostgreSQL集成测试 **0 SKIP**。两个无测试文件的helper/httpapi包不记作集成用例SKIP。完整结果见仓外full-race.json、full-race-summary.json。

失败均定位到app旧合同断言：艺人lookup/types已合法、Match.artist_id已补身份、建议变三栏及公共摘要增加refs。正在修复这些旧断言，保留原文、角色、数量、排序、代表版本及分页严格验证；不能将首轮FAIL覆盖成PASS。

追加实际执行：

```sh
go test -race -p 1 ./internal/catalog/governance ./internal/library -run 'TestPostgreSQLArtist(CorrectionRefreshesGraphAndSearch|ClaimRefreshesBothGraphsAndRevert)$|^TestPostgreSQLScannerArtistGovernanceSurvivesRealRescans$' -count=1 -json
```

- 两项M3图新增回归PASS：claim/revert2.18s，correction set/clear/revert1.87s。
- 真实scanner治理重扫首次FAIL于测试错误期望：两个候选失败按root只汇总一条诊断。补非回滚序列证明两个候选确实失败，断言一条诊断、零本轮观察，并保留四意图表/revision/operations、有效图、人工主名、失败/取消不missing全部断言。
- `go test -race ./internal/library -run '^TestPostgreSQLScannerArtistGovernanceSurvivesRealRescans$' -count=1 -json`复验PASS，3.975s、0 SKIP；证据scan-artist-governance-recheck.json。


## 修复后验证

- `go test -race ./internal/app -count=1 -timeout=40m -json`：**PASS**，502.623s，512个测试/子测试通过、0 FAIL/0 SKIP；两旧规模诊断/固定ffmpeg已启用。证据app-race-recheck.json。
- 首轮其余23包PASS，加app整包复验及新增跨层回归，覆盖用户要求的全部实际包；完整命令首轮FAIL证据保留，不伪称同一命令重跑通过。
- 最终 `go vet ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/...` PASS。
- 依赖规则/路由测试再次PASS（0.030s），Go依赖文件无改动。
- 96个艺人增量仓内样本及1个完整仓外队列，UPDATE及无UPDATE两轮race均PASS、0 SKIP；新增搜索ready/艺人preparing与M3即时命中已覆盖。实际命令：

```sh
ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR=/home/qlqq/.local/share/roomusic-evidence/m5-artists/api-samples ROOMUSIC_UPDATE_API_SAMPLES=1 go test -race ./internal/app -run '^TestPostgreSQLArtist(APIResponseSamples|QueueTruncatedResponseSample)$' -count=1 -timeout=15m -json
ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR=/home/qlqq/.local/share/roomusic-evidence/m5-artists/api-samples go test -race ./internal/app -run '^TestPostgreSQLArtist(APIResponseSamples|QueueTruncatedResponseSample)$' -count=1 -timeout=15m -json
```

## 性能验收

普通验证结束后独占隔离库执行（正常构建，不加race）：

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchScale$' -benchtime=1x -count=1 -timeout=60m -json
```

固定5次预热/30次测量，不允许复用已回填schema、覆盖查询清单或缩减测量数。实际镜像digest：postgres@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2。正式进程结果为FAIL，详见下节。


## 正式规模失败（停止点）

唯一正式运行目录：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/scale-full-20261104T200228Z/

- 运行73.972s后FAIL；回填期间的真实M3修正请求返回HTTP503、code=database_unavailable（catalog_scale_benchmark_test.go:409）。
- BackfillMS=47078.95086，是开始维护到失败的耗时，**不是完成回填耗时**。尚未发布完整投影。
- CPUCount=12，RAM=33653415936字节，采样PeakHeap=7116576字节，连接池上限12；测量配置固定预热5/测量30/三栏结果并发3。
- 夹具按合同生成100000 present+10000 missing、10000发行/8000组、20000共享艺人/4000别名、2艺人+4credits、50000轨倾斜；但失败发生在最终统计核验前，Counts与Sizes尚为空，**不得把生成脚本规模当作已完成的统计验收**。
- PostgreSQL取消日志记录到advisory lock等待及artist_profiles主名查询取消，仅作定位线索；未取得足以确定根因的证据，不声称已定位或修复。
- 第一处失败后未重跑、未缩小规模、未降低阈值、未继续优化。现需后续授权再调查完整规模下在线治理503。

| 指标 | 门槛 | 本轮结果 |
| --- | --- | --- |
| 回填期间M3在线治理 | 继续可用 | **FAIL：503 database_unavailable** |
| ≥2字符建议/三栏首屏p95 | ≤300ms | 未到测量阶段；无有效p95 |
| 单字符建议/三栏首屏p95 | ≤1000ms | 未到测量阶段；无有效p95 |
| 完整规模生产SQL EXPLAIN | 含计数/分页/补全计划 | 未到采集阶段，PlanFiles为空 |
| 完整规模10000队列 | 独立测大小、延迟、取消 | 未执行；不能引用小规模样本作通过证明 |

样本功能测试另测得10000项HTTP正文9139046字节、完整脱敏golden15069139字节；这只证明响应结构/上限/全候选随机正确，不替代上述正式规模延迟与取消验收。普通race中的旧规模EXPLAIN证据保留于race-plans、race-recent-explain.md，也不冒充⑤正式性能通过。

## 清理与交付边界

正式失败后已停止继续实现/测试，仅整理证据并正常down -v。第二轮隔离roomusic-test-m5s容器、卷、网络均已清理，环境凭据文件已删除；没有访问用户roomusic-postgres-1或roomusic库。前端代理创建的其他资源未接管/清理。

本轮未提交、未推送、未归档任务。后端spec已同步，性能门禁未通过是明确剩余项。三个真实用户音乐opt-in按安全约束未运行；完整race首轮失败和修复后app整包PASS分开保留。巨型queue样本按用户仓外证据约束外置，准确路径及映射见artist-large-samples.md和artist-sample-manifest.md。

## 继续授权：M3 503原规模取证（2026-11-04）

用户授权查清并修复，再执行完整验收。生产修改前已用同一完整规模夹具复现；sha256校验确认graph、maintenance和M3 handler与失败版本未变，仅增加测试诊断。诊断启用 `ROOMUSIC_SEARCH_SCALE_DIAGNOSTICS=1`，原45秒请求context、5次预热/30次测量、完整夹具均未改变。

原始证据目录（仓外）：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/repro-online-20261104T201219Z/

每100ms采样pg_stat_activity（wait_event/query/xact_start/query_start/blocking_pids）及pg_locks，包括advisory持有/等待；逐SQL tracer关联PID、request_id、事务、参数数组数量、CATP请求/获取/事务结束。SQL参数值、凭据不写日志。470次采样、0采样错误；33MB原始activity、SQL字典、事务/请求JSONL和取消前现场均在上述目录。`blocking-evidence-summary.json`保存代表快照，`pre-fix-production.sha256`与`production-unchanged-check.txt`证明先取证。

### 已确认根因与时间线

根因不是maintenance选主锁排斥M3，也不是M3拿发行行后再等gate；是**在线扫描把全库未回填艺人的主名工作捎进了自己的CATP事务**：`artistgraph.Refresh → affected`无条件UNION所有无profile身份，使单曲探针刷新扩成20002个艺人；`refreshProfiles`对整个集合执行主名/owned名字查询。现场证据：

| 角色 | PostgreSQL PID / 事务 | 实测 |
| --- | --- | --- |
| 扫描候选 | 2286 / tx10 | 20:20:21.861580Z取得CATP，取消前已持有46999.455ms；其主名SQL从20:20:23.162094Z运行45698.941ms仍未返回，参数数组20002项；activity为active、wait_event=null、无blocking_pids，说明当时正在执行而非等待另一个锁 |
| M3 PATCH | 2287 / tx12 | 已持OPER；20:20:23.863192Z请求CATP，pg_locks granted=false，blocking_pids=[2286]；等待44997.207ms，以`timeout: context deadline exceeded`结束；从未拿到CATP或发行行锁 |
| 回填worker | 2193 / tx13 | 持CATM（不同命名空间），等待CATP42633.208ms；pg_locks granted=false，blocking_pids=[2286,2287]（包含队列前方等待者）；不是CATP持有者 |

CATP键为classid=1128354896（0x43415450）、objid=1、objsubid=2；扫描granted=true，M3和worker=false。扫描SQL指纹22bc49701ae5faa7315cc9e16e26a09adb56271f3e52931e862afa6a9d7fc904，完整SQL见diagnostic-sql.jsonl。

503链已经用同request_id核对：基准单请求 `context.WithTimeout(...,45*time.Second)`到期 → CATP QueryRow错误 `timeout: context deadline exceeded` → `operations.Run`分类`ErrUnavailable` → correction HTTP返回503/database_unavailable。HTTP总时长45001.207ms；应用日志明确`operations: database unavailable: execute: timeout: context deadline exceeded`。45秒来自测试请求预算，不是生产M3配置，不能通过拉长它修复。

本次总运行74.751s。失败前回填只有发现清单事务提交：4458.906ms（含commit3262.502ms），**未持CATP**；第一个应用批正在等gate，尚无已提交应用批，不能捏造“批均值”。逐事务日志和取消前未完成事务的时长均已保存。后续修复验收必须继续报每批事务/CATP时长。

### 据证据确定的修复边界

- 在线graph只刷新本次发行变更前后实际涉及身份及本次facts新建身份（含被M3遮盖、无有效署名者），保留失去最后一条署名的身份刷新；不再顺带补全库孤立profile。
- 全库profile/搜索名字缺口由maintenance owner独立有界清单阶段补齐；小批CATP内重读当前事实、更新代次marker、提交释放、批间至少让出50ms；不删除内容或提前发布。
- 收窄主名/owned查询到显式身份集合，保持票数、manual、seed、redirect、scope语义。
- 增加0028作为增量修正，保持0026/0027不变；仍遵循OPER→CATP→发行/组→艺人→个人state锁序。
- 新回归对小发行M3修正和扫描写入各设**5秒工程上限**：合同未给独立M3数字SLA，明确目标是后台持gate约100–200ms且前台不等待整个回填；5秒为200ms目标的25倍，容纳race/CI调度而严格捕获原45秒阻塞。不是新的产品SLA，不改生产或正式基准请求超时。回归同时断言请求成功时仍有后台未完成工作，以及后续续跑完整发布。

### 修复落盘与增量正确性检查

- `artistgraph` 的主名/owned-name 聚合改为显式身份集合，一跳路由按索引找名字，保持present subject/name跨角色去重票、人工主名与种子/拆分规则。`Refresh(nil)` 不触发全库刷新。
- `0028_artist_profile_backfill.sql` 增加 `artist_profiles.applied_generation` 和清单 `subject_kind=artist`。后台独立 `artist_profiles` 阶段起步10身份、上限100，自适应小批；先处理发行，每批提交后让出50ms。旧sealed build补阶段；既有ready不因新增marker默认0被撤销；取消、重启、迟到UUID、零搜索行均保留完整覆盖检查。
- 独立check发现并补齐一个范围收窄遗漏：被M3覆盖的新scan原名仍新建真实身份，但不进入有效关联。现仅将本次成功INSERT的身份加入old/current显式集合；真实提交后按ID读返回 `available=false` 安全摘要，profile/搜索名字同代，无关孤立身份不被捎带。该最小graph测试直接构造扫描事实，不冒称完整scanner；实际扫描在线回归另在app覆盖。
- `online-backfill-check.md` 第五节已静态关闭该P2，保留旧问题和性能失败历史。
- maintenance最终独占全包 `go test -race ./internal/catalog/maintenance -count=1 -timeout=5m -json`：PASS，30测试/子测试，0 SKIP；原始 `maintenance-profiles-isolated-final.json`。先前并发测试中的deadline失败日志保留，不将其删除或隐藏。
- 新app回归 `TestPostgreSQLArtistOnlineWritesDuringPendingBackfill`：20,000艺人/24,000名字、真实worker批提交后barrier暂停；扫描从POST到succeeded与图落盘586ms、真实M3修正80.740ms，均小于5秒工程上限，成功时仍有38项待处理、至少19,997身份profile缺口；释放后完整发布。该局部回归不代替正式规模竞争实测；证据 `app-online-backfill-regression.json`。

### 调试复盘

根因类别为隐含假设＋跨层职责混入：原“缺profile就补齐”的兜底把后台全库完整性误放入每次在线扫描，少量数据的功能测试不暴露CATP放大。首次范围修复又只考虑有效关联，遗漏M3遮盖的新事实身份；独立review与提交后按ID读取测试补齐此边界。预防采用显式身份参数、后台独立清单/代次/发布缺口检测、20k缺口在线回归及正式规模锁采样；不靠提高超时、减少数据或修改ready错误语义。相关约束已写入artist-contracts/database-guidelines及ADR0002 decisions，未改ADR正文。

### 修复后完整影响范围验证

2026-11-04，独占 `roomusic-test-m5s` 随机隔离库，串行包调度避免数据库级CATM互相阻塞；不是以串行规避统计断言，旧双阶段统计/EXPLAIN测试仍启用并保持assert。命令（backend目录）：

```sh
go test -race -p 1 ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/... -count=1 -timeout=40m -json
go vet ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/...
go test ./internal/app -run '^TestPackageDependencyDirections$|^TestRoutesResolveToRegisteredPatterns$' -count=1
```

- 三条命令均 **PASS / exit 0**。治理实际包为 `internal/catalog/governance`，已被catalog/...覆盖；仓库不存在 `internal/governance`，未虚构该包。
- race：24个有测试包通过、2个包无测试文件；2092测试/子测试PASS、0 FAIL。**PostgreSQL集成0 SKIP**；3个用户真实音乐opt-in SKIP：`TestRealMusicLyricsCoverage`、`TestRealMusicReadOnlySmoke`、`TestRealMusicFingerprintCoverage`。未设置用户音乐ROOT，不宣称全命令0 SKIP。
- app整包657.687s；新在线回归61.36s，其中扫描含真实写入/succeeded **394.040ms**、M3 PATCH **75.246ms**，各≤5秒；请求完成后仍有38项待处理与19997个预置身份profile缺口，随后完整发布。
- 显式启用固定ffmpeg、`ROOMUSIC_CATALOG_EXPLAIN=1`、`ROOMUSIC_CATALOG_EXPLAIN_DIR/OUTPUT`、`ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT`和外置完整queue样本目录；没有UPDATE样本，没有缩减opt-in PG覆盖。
- 原始输出、逐包耗时、跳过分类、旧规模EXPLAIN与环境命令见：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/online-fix-final/

普通验证结束后正常 `down -v`，再以新随机隔离库进行正式规模benchmark；两阶段不并发。前端/浏览器资源未触碰。

## 修复后正式规模：M3在线成功，回填吞吐失败（再次停止）

唯一修复后正式运行：2026-11-04 21:03:24–21:48:25 UTC，未重跑。命令仍为：

```sh
ROOMUSIC_SEARCH_SCALE_DIAGNOSTICS=1 go test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchScale$' -benchtime=1x -count=1 -timeout=60m -json
```

原始证据目录（约1.8GB，全部仓外）：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/scale-online-fixed-20261104T210323Z/

`diagnostic-activity.jsonl`（26,662次activity/locks采样、0错误）、`diagnostic-transactions.jsonl`、`diagnostic-http.jsonl`、`diagnostic-queries.jsonl`、`diagnostic-sql.jsonl`、`backfill.jsonl`保留全部在线窗口；`online-evidence-summary.json`、`slow-statements-summary.json`是离线汇总。失败现场/最终报告及postgres日志均保留，没有新运行EXPLAIN或变更数据库来追查下一轮性能问题。

### 正式结果与门槛对照

| 项目 | 合同/测量条件 | 本轮实测与状态 |
| --- | --- | --- |
| 回填期间M3 | 可在线成功，不等整个回填 | **PASS（单次可用性探针）**：HTTP200，2490.969ms；CATP等待679.736ms，持有1806.014ms；不是延长45秒请求预算后才成功 |
| 回填期间扫描 | 真实写入且succeeded | **PASS（单次可用性探针）**：从POST开始至scan.completed为6935.425ms；候选事务CATP持有2715.082ms；非仅202。5秒是另一个小发行工程回归上限，不是本规模扫描SLA，不将6.935s写成“满足5秒” |
| 回填期间播放/ready | 在线继续可用 | 2665次音频206，p95=2.823ms；2668次ready200，p95=0.113ms；诊断性统计，不虚构新性能门槛 |
| 回填完成耗时 | 完整处理、最后原子发布，不跳过内容 | **FAIL/未完成**：完整命令2700.869s；内部原有45分钟总context到期。开始worker到失败2672.432s（44分32.432秒），**不是完成耗时**。搜索清单10000发行/110000轨完成且已发布；艺人发行清单仅2288/10001（22.878%）；独立profile阶段尚未开始，艺人未发布 |
| ≥2字符建议/三栏首屏 | 5预热/30测量、p95≤300ms | **未执行**；未进入正式测量，不能用回填期legacy请求充数 |
| 单字符 | 5预热/30测量、p95≤1000ms | **未执行** |
| 完整规模生产SQL EXPLAIN | 计数/分页/补全 | **未执行**；PlanFiles为空。已记录原始运行SQL与耗时，但它们不是EXPLAIN计划 |
| 10000首队列 | 正式规模大小/延迟/取消 | **未执行**；不拿旧样本或普通集成结果代替 |

45分钟是现有benchmark总context预算（`catalog_scale_benchmark_test.go:ctx`），**不是设计规定的回填维护SLA**；本轮没有把它改成新的产品门槛。失败出自在线循环 `ctx.Done()`，并非M3再次503，也不是worker报治理冲突。没有延长预算、换小夹具或继续执行后续测量。完整最终Counts/Sizes/PG统计在发布之后，尚未执行；因此报告保留为空，不能把生成脚本参数冒充最终表统计验收。夹具未改，manifest已证实10000发行/110000轨，包含在线扫描新增发行后艺人清单10001；CPU12、RAM33653415936字节、池上限12、采样PeakHeap22533104字节。

### 每批事务与CATP实际时长

下面是已提交批逐条关联同一maintenance PID的事务结束时间（全部匹配，无漏项）；原始每批仍在JSONL中。单位ms：

| 阶段 | 已提交批 | batch_ms中位/p95/最大 | CATP持有中位/p95/最大 |
| --- | ---: | --- | --- |
| 搜索发行 | 272 | 114 / 141 / 1810 | 114.016 / 140.363 / 251.614 |
| 搜索曲目 | 4557 | 124 / 163 / 3669 | 123.032 / 161.988 / 3667.387 |
| 艺人发行（未完成） | 2274 | 948 / 1143 / 4827 | 947.594 / 1141.906 / 4826.369 |

每批确实提交释放CATP并至少让出50ms，锁顺序未反转；但艺人批已自适应降至1发行，持gate p95仍1.142秒，**没有达到100–200ms调优目标**。将工作从全库缺profile集合移开修复了原单扫描47秒堵塞，不等于回填查询成本已经合格。

离线逐SQL汇总进一步显示：

- `refreshEffective` 的 `INSERT INTO artist_effective_credits ... WHERE (c.release_id=ANY(...) OR m.release_id=ANY(...))`，maintenance执行2275次，累计965227.886ms；最慢1074.479ms，参数仅1发行，插入62条。SQL指纹 `fd8bf4d361d50052936b70c930afa94d25ddb0e26ef9f0f2096dc31c0fb2f7ad`。
- 显式身份主名votes/owned查询2274次，累计275782.586ms，最慢343.405ms（18身份）。指纹 `c00c8aba4614ac9fb2056e2a14489aa1ed1d6cb8ece0ba17c922c4739e36923e`。
- 这些是下一步取证方向，不是未经EXPLAIN就断言“缺某索引/JIT是根因”。本轮已停止，未追加生产优化。若继续，应先对完整规模回填实际SQL做计划证据，再收窄发行/轨选择范围与重复聚合成本，保持相同内容、锁序和全规模门槛；不是延长M3超时或关写事务JIT。

### 最终边界与清理

- 原503原因已确认并修复；新回归、完整影响race、vet、依赖规则均PASS，PG集成0 SKIP；三个用户音乐opt-in例外如上明确列出。
- 正式规模仍FAIL，不能标记步骤5完成、不能宣称正式搜索性能通过。任务保持in_progress；本轮没有降低门槛、减少测量数/夹具或自动重跑。
- 本轮同步artist-contracts、database-guidelines、ADR0002 decisions；此前已同步的scan/governance/catalog/search/personal合同保留。本轮无新API形状，旧96+1样本在完整race中无UPDATE复验通过。
- 隔离库已正常 `down -v`。2026-11-04 21:49:47 UTC核验 `roomusic-test-m5s` 容器/卷/网络均为空，临时凭据env已删除，详见本轮 `resource-cleanup.json`。未连接用户实例；未触碰浏览器锁或他人资源。
- 不提交、不推送、不归档。前端及嵌入web文件未修改。

## 新授权A：四处主会话裁决已交付（2026-11-04 22:07 UTC）

- I1 `GET /api/v1/artists/{id}/merged-sources`管理员只读，真实合并来源/当前旧身份revision/当前仍归本人的名字键；支持链式合并、restore后重新merge与空键。经operations.Load读类型化快照，不解析changes文本；未知/坏证据按安全错误，不新增未经裁决的数量或JSON读取字节cap。
- I2 `/artists/{id}/albums?participation=any&q=...`提供任意角色present参与组及非空roles；默认响应不新增roles，默认筛选/排序不变。q只匹配可见成员有效专辑名，%/_/~为字面值，代表/credited版本不随q改变。
- I4 `POST /api/v1/tracks/summaries`成员可读，同源校验；UUID小写去重后1–1000，按首次输入顺序返回安全摘要，unknown省略、missing保留、含artist_refs。
- I6 `role=album_artist`匹配release_artist或album_artist，release_artist仍精确。DTO七项词表不变。
- 新增35个真实handler样本：`backend/internal/app/testdata/api-samples/artists/adjudication/*.json`，精确清单/场景/命令已追加`artist-sample-manifest.md`。UPDATE生成及无UPDATE逐字race复验均PASS/0 SKIP；旧样本所有权不变。

最终A综合验证（backend目录、显式随机隔离库）：

```sh
go test -race -p 1 ./internal/catalog ./internal/catalog/governance ./internal/catalog/httpapi ./internal/app -run '^(TestArtistMergedSources|TestPostgreSQLArtistMergedSources|TestPostgreSQLArtistI2|TestPostgreSQLArtistI6|TestArtistI2|TestPostgreSQLTrackSummaries|TestDecodeTrackSummaryIDs|TestTrackSummariesSecurityBeforeDecode|TestPostgreSQLArtistAdjudicationResponseSamples|TestArtistAdjudicationSampleOwnership|TestPackageDependencyDirections|TestRoutesResolveToRegisteredPatterns|TestAPISampleRecorderOwnership|TestPostgreSQLArtistRead|TestArtistRead)' -count=1 -timeout=10m -json
go vet ./internal/catalog/... ./internal/app
```

**PASS：75测试/子测试、0 FAIL、0 SKIP；vet exit0。** 包括真实权限/400/404/503、I4 1000/1001与首次顺序、merged-sources真实merge/split链路和JIT同PID恢复；依赖和路由门禁已包含。此为A的明确局部集合，不冒称B修改后的最终全影响race。旧handler/shared sampler已有断言未放松；并行新增ownership测试导致的一次重复函数编译失败已修复、日志保留，最终75项通过。

独立审查`adjudication-check.md`最终无未关闭确定问题；移除了审查发现的合法JSONB读回大于原紧凑写预算的新拒绝问题，空白等价>8MiB回归通过。同步catalog-rest-contracts、catalog-summary-contracts、artist-contracts、governance-contracts及ADR0003 decisions，未改前端。A不需要迁移。

原始日志：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/adjudication-a/

已按用户要求先回报A，随后继续B；当前隔离项目由主控继续持有用于B，最终统一清理，不声称此时已清理。不提交、不推送。

## 新授权B执行边界

A于22:07 UTC完成并先行回报后开始B。当时主会话按设计提出联合回填≤36分钟、应用批CATP持有p95约≤200ms/max≤1s；其中max并非用户要求，2026-11-05已由I8修订；总benchmark预算可延长，但M3请求timeout和搜索矩阵/5warm30/规模不变。正式负载必须覆盖艺人回填阶段的实际扫描和M3，不只初期受理一次。此授权已补入S设计§15.2b及艺人spec。

先做诊断计划再改生产：可在隔离诊断夹具中集合构造完整已知事实/派生以避免等待旧算法三小时，但必须校验规模和与真实graph的等价性，清楚标识诊断用途；正式验收仍从空投影走全部生产maintenance，不把预填夹具当作完成回填证据。原始EXPLAIN放仓外，任何正式门槛失败仍停止报告。

### B harness证据完整性复核

独立静态复核已关闭提交后LoadState失败可能漏计的P1，以及发布夹在预快照与M3之间可能假计在线覆盖的P2。应用事务成功COMMIT立即记入CATP分布，生产日志只确认；未确认提交及backfill_failed使验收失败，不能被自动重试掩盖。M3真实回读后仍需building快照才算艺人阶段负载。详见`verification/backfill-b-check.md`第五节；这是harness正确性结论，尚非B正式性能通过。相应证据合同已同步search-contracts。

### B全规模SQL诊断与优化交付（正式验收前）

原始旧计划确认有效署名INSERT跨表OR先连接全680000事实/110000轨，再过滤本批；改为发行级/轨级互斥局部入口，单10轨发行由约470–586ms降至9–13ms。profile合并主名/归属读取与批量写入；本次原文按256项预取仍完整校验哈希命中，不截断后续项。主名仍每subject/name一票，全部去重轨票减非present票并剔除零票；作用域名字沿索引严格递增枚举到耗尽。

仅加候选索引的初轮无可靠收益，未据此落迁移；最新SQL再做同SQL有/无索引对照：热门主名418.990→118.441ms，cost208256→44757.39，JIT135.038→0ms；credit_names79.189→0.321ms。据此新增**0029_artist_backfill_indexes.sql**，`artist_effective_credits(artist_id,name_id,track_id,release_id)`，51986432字节。没有改JIT/enable/synchronous_commit或在业务事务ANALYZE，旧迁移与Version/ready不变。

| 完整Refresh诊断样本 | B旧版CATP ms | 最新SQL无索引 | 最新SQL+0029 |
| --- | ---: | ---: | ---: |
| 热门单发行 | 2052.83 | 471.56 | 172.22 |
| 另一热门单发行 | 2011.79 | 468.16 | 173.43 |
| 冷门单发行 | 861.58 | 102.56 | 102.40 |
| 100missing单发行 | 1283.21 | 255.80 | 268.70 |
| 热门10发行 | 2184.11 | 1052.37 | 857.35 |
| 冷门10发行 | 2098.66 | 692.30 | 728.85 |
| 100missing×10发行 | 4953.59 | 2487.35 | 2377.36 |

以上含COMMIT；有索引样本COMMIT约3–9ms。诊断预置完整图并校验真实Refresh事实/effective等价，**不是从空投影的正式验收**。10missing批的INSERT仍950.25ms（FK触发器679.93ms），不能只看单SQL就声称max1s通过。maintenance据此改为CATP内按轨数/原始署名工作量限连续前缀，首个超大subject完整处理；单100missing仍269ms如实保留，不截断或跳过。旧正式搜索阶段曾出现5轨批COMMIT3607.347ms（tx4777），最新诊断快提交不能消除线上尖峰风险。

Graph owner最终`go test -race ./internal/catalog/internal/artistgraph -count=1 -v` PASS 14.191s，17顶层Test＋1Fuzz入口、10顶层PG Test、0 SKIP；vet/build/编译PASS。新增300名字预取/未命中/哈希全文/回滚、260作用域别名完整与全missing零票回退；原masked/manual/seed/redirect/计票回归通过。主控完整影响race与正式bench尚待执行。原始计划、旧源码SHA/overlay及各轮对照：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/backfill-sql-b/

独立check已关闭harness及maintenance静态问题，最终graph/0029正在复核。同步artist-contracts、database-guidelines、search-contracts和ADR0002 decisions，未改前端。DB已由graph于22:48:38 UTC释放给maintenance定点PG验证，项目仍保留，不宣称此时资源已清理。

### B完整影响race首轮：依赖门禁失败（保留历史）

2026-11-04 22:51–23:14 UTC执行原完整范围`go test -race -p 1`（catalog/...、library/...、app、platform/...、personal/...、operations/...；governance在catalog/governance内）。2190个测试/子测试PASS，唯一失败为app的`TestPackageDependencyDirections`：maintenance新增直接导入metadata，违反原allowlist。全部PostgreSQL集成无SKIP；3个用户真实音乐opt-in未设置ROOT，明确SKIP；另2个无测试文件包的package skip不是集成跳过。真实样本、旧性能opt-in、在线回填及新增0029/graph/workload回归均已实际执行。

原日志与失败汇总保留在`backfill-b-final/race.json`及`first-race-summary.json`。已安排把输入名字成本估计移交graph窄端口，maintenance不直接解释metadata；不修改依赖门禁、不复制拆分算法。修复后再次跑完整影响集，未启动正式基准，因此此次普通质量门禁失败不冒充正式性能结果。主控首轮全影响vet PASS，修后仍将复验。

### B修后完整质量门禁：PASS

2026-11-04 23:16–23:40 UTC再次执行完整影响集，**2191测试/子测试PASS、0FAIL、PostgreSQL集成0SKIP**。24个有测试包全部PASS；3项用户真实音乐opt-in（LyricsCoverage、ReadOnlySmoke、FingerprintCoverage）因不使用用户音乐ROOT单列SKIP，2个无测试文件包不计为集成跳过。没有缩小测试选择；旧EXPLAIN/HTTP性能opt-in、全部真实样本及外置大队列样本均启用，未UPDATE。

```sh
# backend目录，source随机隔离库env；ffmpeg及旧性能/外置样本配置见仓外race.command和执行脚本记录
go test -race -p 1 ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/... -count=1 -timeout=40m -json
go vet ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/...
```

上述两条exit0；依赖/路由门禁包含在app整包中PASS。governance实际路径为catalog/governance，已被catalog/...覆盖，没有伪造不存在的internal/governance包。最小依赖修复由独立check第八节复核，未放宽allowlist；最终graph/maintenance源码含input_cost两新增文件另存SHA。

/home/qlqq/.local/share/roomusic-evidence/m5-artists/backfill-b-final-rerun/

23:41:09 UTC已正常down-v并确认容器/卷/网络均0，作为正式基准前清洁边界；随后新建同项目、空闲端口/新随机库，从空投影开始正式验收。初次依赖失败日志仍保留，不覆盖成PASS。

## B正式全规模验收：首次max门槛失败，按要求停止（2026-11-04 23:58 UTC）

**任务仍未完成。** 仅运行一次本轮正式基准，空随机隔离库、原100000 present＋10000 missing/10000发行/20000艺人夹具；不复用诊断预填图、不缩减queries/iterations。命令：

```sh
ROOMUSIC_SEARCH_SCALE_DIAGNOSTICS=1 ROOMUSIC_SEARCH_SCALE_OUTPUT=<本轮仓外目录> \
go test ./internal/app -run '^$' -bench '^BenchmarkPostgreSQLSearchScale$' -benchtime=1x -count=1 -timeout=120m -json
```

结果**FAIL**（Go总运行1013.812秒，含建夹具与清理）。worker从23:42:00.992 UTC开始，23:58:25首因触发，已运行984.957秒（16分24.957秒），不是36分钟预算到期。首因：`artist_releases`已提交应用事务6967的CATP持有**1462.520ms > 1000ms**。COMMIT回调立即留证/停止，未继续或重跑正式验收，后续搜索与队列项目未执行。

### 门槛逐项对照

| 项目 | 实测 | 结论 |
| --- | --- | --- |
| 回填期M3在线 | 固定9900发行PATCH HTTP200，2574.471ms；33轮探针PATCH均200，p95 486.148ms、max490.688ms；其中8轮严格在artist building期间完成，p95/max172.076ms | 已观测窗口可用；未拉长45秒单请求预算 |
| 真扫描写入 | 33轮均succeeded且核验本轮观察/目录；全窗口p95 6623.983ms、max6812.799ms；8轮artist building窗口max1020.813ms | 不是仅202受理；正式大库数字不冒充5秒小回归门槛 |
| 播放与ready | 978次Range音频206，p95 1.751ms；980次ready成功，p95 0.185ms | 已观测窗口可用 |
| 联合回填≤36min | 16分24.957秒即因max失败停止；搜索10000发行/110000轨已完成并发布；艺人最后日志1230/10001，另有失败触发批1个真实提交，合计1231；profile阶段未到 | 未完成，不能判36分钟达标 |
| 总体应用批CATP p95≤200ms | 已提交5520批的未完成分布p95 **163.313ms** | 只报部分分布，不宣称全程PASS |
| 应用批CATP max≤1000ms | **1462.520ms** | **FAIL，正式停止点** |
| ≥2字符建议/三栏首屏p95≤300ms | 未进入5warm30测量，Cases为空 | 未执行 |
| 单字符p95≤1000ms | 未进入正式测量 | 未执行 |
| 完整规模生产搜索计数/分页/补全EXPLAIN | 本轮正式PlanFiles为空；此前graph SQL诊断另存，不能替代 | 未执行 |
| 10000首队列大小/延迟/取消 | 本轮正式Queues为空；普通回归/样本通过不能替代规模项目 | 未执行 |

后台读探针的建议p95 454.595ms是未发布/并发负载窗口的运行观测，不是ready后的5warm30三栏搜索验收，不能用它宣称正式搜索PASS或冒称已执行下一项。

分阶段已提交CATP持有（ms，均含COMMIT返回）：

| phase | 批数 | 中位 | p95 | max |
| --- | ---: | ---: | ---: | ---: |
| releases | 273 | 116.148 | 138.484 | 224.523 |
| tracks | 4016 | 115.865 | 141.981 | 262.449 |
| artist_releases（未完成） | 1231 | 137.631 | 171.697 | 1462.520 |

### 本次失败窗口：不是旧的全库INSERT，也不是等CATP

同一maintenance PID243、事务6967：CATP等待0.454ms，取得于23:58:23.887406776，COMMIT成功返回于23:58:25.349926295。持有1462.519519ms，其中**COMMIT本身1281.200128ms**，提交前持有约181.319ms。主名SQL75.808ms、effective INSERT12.352ms，均不再是此前全表反连接的数百毫秒瓶颈。

100ms采样看到同一PID的`query=commit`：23:58:24.093–24.293为`LWLock/WALWrite`，24.393–25.293为`IO/WalSync`；同窗口CATP仍在持有。因而本次超限主要发生在WAL写/持久化提交等待，**不是M3重新503，也不是等待CATP**。这些观测不能继续推断底层磁盘、宿主竞争或checkpoint的唯一根因；没有为通过而修改synchronous_commit/规划器/JIT、排除COMMIT或放宽max。进一步定位WAL/存储尖峰应在后续授权中取证，本轮遵守首次失败停止。

该批在COMMIT即进入分布，尚未取得之后的LoadState/批日志确认时已触发取消，所以报告有1个UnconfirmedBatch；它就是6967，**没有漏统计，也没有把日志缺失当成功**。取消前快照active_queries与active_transactions均为空；sampler/worker随后退出，清理成功。

证据（纯文本仓外路径）：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/scale-backfill-b-20261104T234130Z/

重点文件：`summary.json`、`acceptance-summary.json`、`failed-batch-transactions.jsonl`、`failed-batch-queries.jsonl`、`failed-batch-slow-statements.json`、`failed-batch-activity.jsonl`、`failed-batch-wait-summary.json`、`diagnostics-before-cleanup.json`、`source-sha256.txt`、`runtime.txt`、`postgres.log`。完整原始采样/锁/SQL/HTTP仍保留，没有覆盖历史失败。

### 最终交付与资源

A四合同和35真实样本已先交付，路径及75项A验证见前文/artist-sample-manifest。B新增0029及局部化SQL、完整名字遍历/批量预取、工作量限批、正式失败链回归；修后完整2191项race及vet/依赖/路由PASS，PG集成0SKIP（3用户音乐opt-in明确单列）。指定旧spec保留，本轮增量同步artist/database/search/directory与ADR0002 decisions；独立审查报告保留所有发现及闭合时间。

2026-11-04 **23:59:53 UTC**核验正式隔离项目已正常`down -v`：容器/卷/网络均0，临时凭据env已删除；`resource-cleanup.json`保存核验。未操作用户容器/roomusic库，未触碰其他代理浏览器锁。**未提交、未推送、未归档，未改前端；任务保持in_progress。**

## I8继续实施：先分解吞吐（2026-11-05）

### 口径更正

此前把主会话制定的max≤1s写成“用户明确要求”不准确，现已更正本报告、design首段及相关spec/ADR decisions。保留此前1.462秒批和停止记录作为**当时口径下的历史**；当前I8按整轮完整分布p95≤200ms/p99≤500ms，max只记录归因，M3全成功且p95≤1s/扫描全成功/播放搜索无503，联合≤36分钟。正式运行收齐各项性能数据再统一判定；真实错误仍安全停止，不伪造成功。

### 上一轮完整原始trace的离线分解

先澄清时钟：1231批艺人发行阶段的实际区间是**2026-11-04 23:54:25.305至23:58:25.350 UTC，共240.045秒**；984.957秒是从搜索worker启动算起的联合时长。不能用984.957/1231推算艺人速度。该艺人窗口全部为1发行/批，含最后已提交但未打印批日志的事务6967。

| artist发行阶段分解 | 1231批累计 | 每批均值 |
| --- | ---: | ---: |
| CATP前等待 | 3.617s | 2.939ms |
| 清单/输入选择查询 | 6.121s | 4.972ms |
| graph读取/计算SQL | 69.268s | 56.270ms |
| 写入SQL（含派生及进度） | 85.975s | 69.842ms |
| COMMIT | 5.574s | 4.528ms |
| CATP总持有（含Go/驱动间隔） | 172.916s | 140.468ms |
| 已记录1230批固定让出下限 | 61.500s | 50ms/批 |
| 艺人阶段墙钟 | 240.045s | 195.000ms/批 |

以上为客户端query trace耗时，读取包含结果消费/网络，不声称纯数据库CPU；SQL分类脚本与全表保留仓外。CATP总持有减已分类锁内SQL约5.978秒是Go/驱动/调度残差，不归为纯计算。固定让出至少占艺人墙钟25.6%。选取不是主要瓶颈；单次COMMIT尖峰显著但累计提交只占约2.3%，不能只关同步提交就声称解决吞吐。

搜索发行已完成51.214秒，搜索曲目687.438秒，其中4016次让出至少200.8秒。以已观察到的热门艺人段线性外推10001发行约32.5分钟，加前面搜索约12.4分钟约45分钟；**这不是完整库预测**（后半冷门/missing/profile成本不同），但足以证明现状尚无36分钟把握，不应直接消耗一次正式运行。

艺人段最高累计SQL：主名50.696s/1231次、effective INSERT16.408s/1231次；逐轨搜索值INSERT16.038s/12310次，同时有12310次tracks.artist UPDATE与重复state/credit读取/marker写入。恢复可信原文时每个实际变更轨调用RefreshTrack，产生本批可合并的N+1，已授权graph/searchindex owner设计有界集合端口，复用原造值/校验，保留每轨revision与同事务目标marker；不跳变更轨、不Touch未变更轨。

新maintenance独立诊断将保持完整规模，测少量热门/普通/100missing真实processArtistBatch的选择、计算、写入、提交及预算/让出，以旧源码SHA固定overlay作前后对照。正式harness另按I8修改，不把诊断预填阶段当正式回填成功。发行批写集含稳定身份/名字键、scan署名及tracks.artist，**不符合异步提交许可**；暂保持默认，纯profiles/search批是否采用待写集与成本证据决定。database-guidelines已补I8条件及同PID恢复测试要求。

原始分解与基线源码：

/home/qlqq/.local/share/roomusic-evidence/m5-artists/i8-throughput/

### I8第一轮新诊断与N+1实施（尚未正式验收）

maintenance独立诊断保持原100k/10k全库及680k事实/effective，真实process批、默认同步提交、固定50ms让出；baseline overlay与N+1 overlay代码SHA固定。热门/普通/普通转missing各24发行窗口及12个profile批，全部业务facts/effective前后等价。它预置完整艺人图，但没有完整search背景，偏差已明确记录，不能据此预测36分钟。

| 代表窗口 | 旧CATP平均ms | N+1后平均ms | 说明 |
| --- | ---: | ---: | --- |
| 热门 | 409.684 | 359.042 | 新graph读取仍307ms/批，需继续串行计划取证 |
| 普通 | 116.303 | 95.840 | 原24批2184 SQL→24批950 SQL |
| 普通→missing | 229.533 | 231.439 | 新22批覆盖24发行，不能忽略批大小差异 |
| profiles | 34.267 | 49.627 | 新样本有提交尖峰；不是整轮分位数 |

原热门24批3146 SQL→938 SQL，证明往返确有减少，但全图热门读取仍超过200ms。与前轮单次118ms主名计划不同，正在捕获真实串行参数、prepared计数与同连接计划；尚未确认是否通用计划变化，不先下结论或用开关回避。

N+1生产仅graph实际变化轨128项UPDATE，以及searchindex有界RefreshTracks，共用原造值/校验；未变化轨不Touch。新PG/race已真实通过`go test -race ./internal/catalog/internal/searchindex ./internal/catalog/internal/artistgraph -count=1 -v`，0SKIP，含260项跨chunk与单轨等价/后段回滚/取消、graph130轨恢复且不变轨不Touch。无DB依赖门禁PASS；最初新增uuid依赖已在进入完整测试前移除，未放宽allowlist。

I8 harness已实现完整p95/p99、max归因、全部M3/扫描/播放搜索检查、36分钟只判硬结果不取消后续测量，所有性能失败累积后统一判定。独立审查发现最终报告早于清理的P2已修复：最早注册Cleanup最后封口，FirstError/Errors与性能失败并存、Finalized区分过程快照。纯测试通过，静态复核关闭；尚未运行本轮正式基准。

### I8批间让出修订与串行主名计划取证（继续实施）

原固定50ms让出已按本轮用户“按实测自适应”授权调整：两worker提交释放CATP后，共用clamp(整批Duration/4,10ms,50ms)，无有效耗时保守50ms；context可取消，不忙循环。Duration含取锁等待与COMMIT，不能作为CATP持有；yield_ms/yield_status=planned只表示计划值。原选批、工作量预算、锁序和发布条件未改。独立静态复核未见新问题；此调整不证明调度公平性或正式门槛通过。search/artist spec、design §3.3及ADR decisions已同步；上文旧50ms记录仍保留为历史。

串行取证确认另一主名瓶颈来自prepared generic计划，**不是JIT**：真实24批不插入EXPLAIN，第1–5次custom，第6次开始generic，末次generic19/custom5；主名336.054ms、shared hits455825、JIT0，其中非present减票分支对110456条署名逐条tracks主键探测。独立连接第1custom139.675ms、第6generic329.781ms作对照。原始参数、SQL SHA、prepared计数及计划在仓外i8-throughput/profile-serial-before/。

首个固定非present集合的MATERIALIZED候选，将真实hot24批平均CATP336.51→236.07ms、末次generic主名336.054→178.745ms、shared hits455825→57307；但cold24批总2096→2659ms、profiles12批379→570ms，有明确固定成本回退，不能称达标。证据i8-throughput/profile-serial-materialized-absent/。owner继续等价SQL候选取证；最终SQL、全影响race及I8正式运行尚未完成。

最终局部方案依据完整夹具默认串行计划：最多消费4097条本批符合条件署名仅用于选路，≤4096保持原稀疏SQL，>4096固定非present集合；不截断主名计票或名字。NOT EXISTS/去重anti join对照303.951/303.114ms；NOT IN108.343ms但大missing集合风险未采纳。新增4097末票决胜、missing扣票、降至4096切路回归。最终真实process窗口（仍预置全图、无完整搜索背景）：hot24批平均CATP182.745ms、最大211.747ms；普通24发行/22批总2227.382ms；missing24发行/19批平均258.623ms、最大363.012ms；profiles12批总373.001ms。末次真正generic主名148.866ms/cost8240.45/JIT0/shared hits46843，generic19/custom5。并非正式完整分布，不据此宣布p95达标；探测成本已包含在CATP。

owner执行 `go test -race ./internal/catalog/internal/searchindex ./internal/catalog/internal/artistgraph -count=1 -v`：29顶层测试PASS、0 SKIP，日志仓外i8-throughput/graph-search-final-race.log。00:54:46Z释放数据库给maintenance进行完整真实search背景诊断。pacing纯race日志maintenance-pacing-unit-race.json包含29测试事件PASS、无FAIL/SKIP；完整影响race尚未执行。

本轮排障复盘：根因类别为隐含假设与测试覆盖缺口——单次新连接计划不代表长期worker的generic计划，且把联合墙钟当artist阶段导致吞吐判断偏差。首个MATERIALIZED候选改善热点却增加稀疏固定成本；因此用默认串行实际参数、热门/普通/missing/profile多窗口及阈值语义测试共同验证。该经验已写入database-guidelines；正式指标仍待整轮，不能以局部优化替代验收。

最终局部hot24批nearest-rank p95=203.005ms（仍略高于200），missing边界p95=363.012ms；不得只报均值。hot策略探测24次累计30.464ms/均1.269ms/最大1.807ms，占CATP0.695%，已逐条确认包含在持gate窗口；详细分解和五个生产SHA见仓外i8-throughput/i8-profile-batch-report.md、final-production-sha256.txt及profile-serial-final/final-cost-summary.json。

### 完整SEARCH_READY背景诊断：暂不启动正式验收

maintenance真实跑完search：557.505s，120000 subjects、820000 values、零缺口；诊断仍预置680000艺人事实/有效署名后只执行代表窗口，不等于正式艺人回填。13顶层maintenance最小race PASS/0 SKIP（8.097s），完整背景诊断PASS（762.146s包含装载和预置）。完整证据i8-throughput/maintenance-search-ready-final/report.md。

相同生产SHA下热门24批CATP均337.675ms/p95=363.878ms/max376.192ms，主名SQL累计2951.455→6535.588ms；普通24批均102.181ms，missing边界24批均230.123ms，profiles12批均32.469ms。84批计划让出2898.644ms/实测2928.397ms，相比旧固定4200ms确有改善，但不足抵消SQL回退。选择、graph读/写、进度、COMMIT、残差及实际批大小全部在上述报告分开记录；本轮热门COMMIT累计72.143ms，不是主要瓶颈。

按完整夹具分布与代表批成本的条件性外推53.56分钟（p95单对象成本敏感情景57.63分钟），不是正式实测或严格上下界。预置全图可能高估早期热门票数成本，无在线负载/热缓存又可能低估争用，因此不能断言本机36分钟不可能；但不足以支持正式启动。保持原规模与门槛，下一步在真实完整search背景取得实际prepared计划，定位主名读取增量；不先猜JIT/硬件根因、不采用开关、不进行正式重跑。全影响race已在独占DB执行，计划诊断排在其后。

### I8当前源码完整影响验证（2026-11-05 01:36:58 UTC）

`go test -race -p 1 ./internal/catalog/... ./internal/library/... ./internal/app ./internal/platform/... ./internal/personal/... ./internal/operations/... -count=1 -timeout=40m -json`：exit0，2224测试PASS、24包PASS，PostgreSQL集成0 SKIP。governance在catalog/governance内已覆盖。3个用户真实音乐opt-in（LyricsCoverage、ReadOnlySmoke、FingerprintCoverage）未设置ROOT且明确SKIP；另2无测试包的包级skip不作为集成测试跳过。旧EXPLAIN/HTTP性能opt-in已开启，并非筛掉后宣称全包通过。证据仓外i8-final-race-20261105T011338Z/race.json、race.command及result-summary.json。依赖/路由门禁随app实际执行PASS；全影响go vet/build也exit0，见i8-throughput/final-vet-build.log。

此验证覆盖当时稳定的N+1、profile分支与pacing实现，不代表尚未启动的正式性能验收，也不自动覆盖之后改动。DB现移交下一轮完整search背景计划取证，资源尚未清理。

### 完整背景主名计划增量已定位（第二次诊断，非正式验收）

仓外i8-throughput/search-ready-plans-20261105T013725Z/保留完整真实search后的主名/probe实际参数、SQL SHA、prepared计数、EXPLAIN及本schema只读统计。诊断PASS/768.893s，hot CATP均345.193ms/max381.011ms；主名第6次generic，第24次generic19/custom5，probe保持custom24。真实末次主名306.268ms/cost14304.26/JIT0/hits52536/reads0；独立custom300.119ms、generic304.623ms，不能把这轮回退仅归因generic切换。

主要增量在all_track_counts：旧有序Index Only Scan后聚合24.596ms，新Bitmap Heap Scan后排序100448行再聚合169.319ms；排序节点总157.067ms（含heap scan25.537ms），temp读714/写716 blocks。新effective relallvisible=0/9297，发行票分支index-only也有10012次heap fetch（旧267）。missing全集扫描19.666/18.864ms近似，发行票约36ms近似；tracks约26.7MB，只观察20→260条更新，没有“全表更新膨胀”的依据。当前证据定位计划/排序增量，不猜测不可证的存储、JIT或机器极限。

本轮schema已自动清理、remaining=0；继续授权局部计票聚合SQL候选，在新的完整背景同一fixture内对照结果等价与真实批耗时。不改票规则、迁移、work_mem、planner开关或同步提交；尚未启动正式验收。

同完整fixture的局部聚合候选（i8-throughput/search-ready-plans-20261105T015551Z/）：按身份分别聚合的lateral_count六次完整结果等价，在自然autovacuum前交错基线主名平均264.68ms→208.36ms（约21%改善）；另外两个去重/排序候选未采纳。真实process的baseline_replay均234.097ms/p95 353.247ms，winner均179.966ms/p95 196.454ms/max210.055ms；**其间02:08:29自然autovacuum将effective可见页0/9297变为9453/9461，不能把全部改善归因SQL**。不手工VACUUM、不调整设置；这些是候选连接包装替换SQL下的诊断，不是正式生产源码整轮验收。最终生产仅修改dense计票，保留sparse；独立审查另发现混合大小写UUID在unnest中重复计票的P2，owner按uuid类型DISTINCT修正并补回归后才能关闭。

最终dense SQL采用按身份聚合及uuid类型DISTINCT；sparse和通用uniqueIDs不变。新Alpha发行1票/Zulu轨1票、独立hot4097触发密集、混合大小写UUID回归，在移除DISTINCT的仓外overlay上真实FAIL（Zulu错胜），最终生产两包race30顶层测试PASS/0 SKIP；依赖/vet PASS，独立静态复核已闭合P2。最终捕获SQL SHA为48ce11cc3c34bf483a114eef1892ae8a1754152216131ed384badcf7980b1f09，较早候选仅新增类型级去重，已明确记录差异；最小PG计划只验证实际SQL，不冒充完整性能。报告i8-throughput/dense-distinct-final/report.md。此前完整影响2224项PASS不包含这次后续生产改动，最终还需重跑。

本次完整search541.595s；warm winner 24批process4339.120ms+yield1097.398ms。当前四分之一让出的两种条件预算39.58/48.18分钟（后者按未vacuum交错收益估算），不是上下界或保证，仍不足以据此承诺36分钟。下一步只对已记录等待做离线预算，检验更小正时长让出的吞吐空间；不以自然vacuum必然及时发生作为保证，不重开产品门槛。

### 最后调度调整与正式启动依据

离线逐批重算132条艺人样本及两次完整search日志，不改实测工作成本/批大小：当前/4在warm winner条件39.69min；纯/10为35.09min；保留长批保护（至少200ms仍50ms）为35.25min，仅比纯/10多约9.4s。采用长批保护方案：正Duration<200ms取Duration/10且至少10ms；Duration<=0或>=200ms取50ms。仍每批提交释放后可取消正wait，不忙循环、不改选批/预算。

35.25min是条件性估算而非保证；固定工作增加5%即36.83min，低可见性敏感性46.02min，自然autovacuum改善不能全归SQL也不能假定必然按时。全预置热门成本可能高估正式从空早期批，无争用/热缓存也可能低估；证据足以支持一次正式原规模验证，但不预判通过。原始逐阶段预算与算法在i8-throughput/pacing-offline-20261105/report.md、budget.json。先最小在线/取消回归与最终全影响race，再运行一次正式基准，性能失败保留并收齐后续各项统一判定。

长批保护方案最小race已完成：maintenance/app共11顶层测试（含子测试26项）PASS、0 SKIP；maintenance9.306s、app37.980s。真实在线回归扫描552.269ms至succeeded、M3 76.902ms成功，之后回填续跑ready且无profile缺口；这是工程功能回归，不代替原规模前台p95。命令与日志i8-throughput/pacing-guard-final/verification.md、minimal-race.json；两包vet/build exit0。02:25:47Z释放DB，主控开始最终完整影响race，生产/测试源码冻结。

### 最终冻结源码验证与正式启动（2026-11-05 02:51 UTC）

最终完整影响同命令race exit0：2229测试PASS、24包PASS、PG集成0 SKIP。3真实音乐opt-in未运行、2无测试包单列；未缩减旧性能opt-in。全影响go vet/build exit0，依赖及路由门禁随app通过。日志仓外i8-final-race-20261105T022727Z/race.json、result-summary.json、vet-build.log。该轮已包含最终dense DISTINCT与长批保护/10让出。

全部诊断进程结束后旧roomusic-test-m5s正常down -v，容器/卷/网络各0；再以同限定compose、空闲端口、新随机库重新创建正式隔离环境。正式源码SHA由scale脚本固化；一次完整I8基准现已启动，原规模与5warm/30测量不变，性能失败统一收齐再判定。

## I8唯一正式运行最终结果：回填通过，搜索失败，整套验收未完成（2026-11-05）

证据根目录：/home/qlqq/.local/share/roomusic-evidence/m5-artists/scale-i8-20261105T025149Z。`test.exit=1`；summary为`Finalized=true / Complete=false / Pass=false`，已停止，不再实现或重跑。源码SHA逐项复核全部一致（source-unchanged-check.txt）。

### 回填与在线指标（完整分布）

| 项目 | 实测 | 门槛/结论 |
|---|---:|---|
| 联合回填 | 1923745.069ms＝32分03.745秒 | ≤36分钟，PASS |
| 全部应用批CATP | 12288批；p95 199.361ms；p99 276.037ms | ≤200/500ms，PASS；0未确认批 |
| CATP最大值 | 5985.595ms | 仅记录归因，不作失败门槛 |
| M3 | 66/66成功；p95 342.840ms | 全成功且≤1000ms，PASS |
| 扫描 | 65/65受理、succeeded、真实落盘核验 | PASS |
| 播放 | 1911请求，503=0、其他失败=0 | PASS |
| 回填期搜索 | 1913请求，503=0、其他失败=0 | PASS |

45轮扫描+M3严格在artist building期间完成。M3首次2493.043ms如实保留，I8验收的是完整p95而非最大值。

| CATP阶段（仅归因，不替代整轮分布） | 批数 | p95 ms | p99 ms | max ms |
|---|---:|---:|---:|---:|
| artist_profiles | 209 | 15.408 | 18.114 | 34.388 |
| artist_releases | 7704 | 208.426 | 318.061 | 5985.595 |
| releases | 263 | 140.786 | 169.504 | 225.748 |
| tracks | 4112 | 144.021 | 206.124 | 539.301 |

最大批为artist_releases，tx14240/PID294，03:22:27.714919–03:22:33.700514 UTC。提交前143.471ms、COMMIT5842.125ms；窗口内8份activity采样中7份为commit/LWLock/WALWrite。原始activity/锁与事务仍在，抽取见max-catp-attribution.json。只能确认提交/WAL写入等待，未进一步归因底层设备，也没有将COMMIT剔除或使用异步提交。

### 搜索：已完成的新接口测量

每组保持5次预热、30次测量。下列四组均为多字符，建议/三栏首屏各要求p95≤300ms。
| 查询 | 建议p95 ms | 三栏首屏p95 ms | 专辑/曲目/艺人各栏p95 ms | 结果 |
|---|---:|---:|---|---|
| 交响曲 第00999号 | 311.171 | 258.047 | 117.983 / 257.909 / 161.925 | FAIL |
| 交响曲 第009 | 172.745 | 230.777 | 142.475 / 230.612 / 8.380 | PASS |
| 周杰倫 | 3729.150 | 21860.786 | 21860.708 / 1131.730 / 943.558 | FAIL |
| 周杰伦 | 3674.033 | 21648.577 | 21648.090 / 1119.310 / 957.599 | FAIL |

首个失败是建议311.171ms；随后热点查询出现建议约3.7秒、专辑主导的三栏首屏约21.6–21.9秒，不能仅以首个轻微超标描述本轮。当前保留完整计划但未实施搜索修复或认定搜索根因。

### EXPLAIN与未完成项

- 原规模4组已落盘explain-01..04.json，共72份生产SQL计划（包括计数、分页、补全，不表示72种互异SQL）；索引文件final-plan-index.json。第三组中含group_id实体映射、limit51的分页/描述SQL执行21494.420ms。未收齐其余22组计划，不能声称完整矩阵EXPLAIN通过。
- 仅4/26组新搜索完成；所有单字符组尚未执行，1000ms门槛不能判定。深页、10000首两种队列的大小/时延/取消未执行，Queues=null，不能填0或PASS。
- 旧路由对照前三组完成5warm/30；第三组旧releases/group p95分别14021.285/24829.648ms。第四组旧对照尚在执行时，总context到期；其Samples=0只表示没有完整汇总，不表示0ms或已通过。

### 未完成原因与责任边界

本轮02:51:52 UTC启动，04:21:52 UTC预设90分钟总上下文耗尽，第四组旧release-groups正在执行的请求在19.490秒时返回503；整个benchmark 5400.851s后exit1。该503发生在回填发布后的性能对照，不属于前述回填期1913次搜索无503的窗口。没有增加M3/单请求45秒预算。

**总预算设置不足是本轮验收执行缺口，不是产品SLA或“36分钟回填失败”。** 用户允许按需调整总预算；本轮运行前选择90分钟，未预见热点与旧路由对照的数十秒请求，最终未完成用户要求的全套测量。不能用已达到的回填指标宣布任务完成，也不能把未测项从清单删除。按失败停止并保留原数据；没有重跑挑样、减少次数或降低门槛。

### 验证、文档与资源

- 最终完整影响race：2229条测试PASS事件（含子测试）/24包PASS/exit0；PG集成0 SKIP。3真实音乐opt-in SKIP及2无测试包单列；不是整体零SKIP。命令/日志：/home/qlqq/.local/share/roomusic-evidence/m5-artists/i8-final-race-20261105T022727Z/race.command、race.json、result-summary.json。
- 完整影响go vet/build PASS；依赖/路由门禁随app实际通过。正式benchmark FAIL且未完成，不并入功能race通过数量。
- I8代码主要为graph显示署名/搜索批量写、主名密集/稀疏等价路径与uuid级去重、maintenance有界正让出，以及正式harness完整分布/前台指标/最终封口。此次I8无新增迁移，既有0026–0029不变；A接口与样本不变。
- search/artist/database后端spec、S设计实施修订与ADR0002 decisions已同步；旧max≤1秒已更正为主会话旧口径。最终静态审查见i8-check.md末节，逐条区分检查者与owner实际执行证据。
- 正式结束后正常down -v；04:24:43 UTC确认项目容器/卷/网络均0，凭据env已删除，证据resource-cleanup.txt。未连接用户库、未触碰前端、未提交、未推送。
