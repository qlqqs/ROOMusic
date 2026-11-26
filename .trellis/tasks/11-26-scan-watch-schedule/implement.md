# 目录监听与定时扫描实施步骤

下列命令从仓库根运行。Go 使用项目 mise 配置。集成测试只用脚本创建的一次性 PostgreSQL、唯一 Compose 项目，以及合成音乐和各用例的独立临时目录。已有步骤的结果可复用；实现代理只改实现与合同，新增用例及相应样本由测试代理负责。

## 1. 先落可回退的数据结构

- 文件：backend/migrations/NNNN_root_scan_watch.sql、backend/migrations/NNNN_root_scan_watch.down.sql。NNNN 为合入序号，在当前最高 0097 后与并行迁移排号。字段、默认值、五种合法间隔见设计第 5 节。使用项目现有嵌入迁移加载器。
- 核对新建根及既有根默认 60 分钟、既有运行默认 whole_library、确认领取初始 NULL。风险是给历史根错误回填基线，以及回滚误删原有扫描历史。
- 验证：`(cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')`；`ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/^[0-9]+_root_scan_watch[.]sql$' mise exec -- ./scripts/test-integration.sh`，核对新迁移子用例确实执行。

## 2. 先让扫描器认识范围，再接入任何自动触发

- 文件：backend/internal/library/ 下新增 scan_scope.go，修改 scans.go、scanner.go、scan_progress.go、identity_plan.go；backend/internal/catalog/ingest/ 下修改 scope.go、identity_plan.go、scan_feedback_missing.go、staging.go。
- 私有范围一路传至预计数、读取、规划与终态事务。抽走两处从 root.Path 无条件 WalkDir 的遍历入口，复用各自现有的计数和读取回调。全库/整根与直属目录分别迭代；同根一轮只清一次暂存，先收齐关联目录、再规划。接入碟目录、CUE 归属、可读根内删除子目录和局部空目录的语义。
- 按设计第 1 节收紧旧侧 SQL、missing 反馈批处理及缓存清理调用点。逐字节校验含相似前缀、百分号的目录。风险是只收紧 WalkDir，导致兄弟目录在终态被记作 missing、误借曲目 ID，或全根缓存被清空。
- 验证：`(cd backend && mise exec -- go test ./internal/library ./internal/catalog/ingest -run '^(TestScanFilterCountingSkipsSubtreesButNotRegisteredRoot|TestScanProgressCandidateDefinition|TestParseDiscMarker|TestParseDiscMarkerRejectsNonDiscNames)$')`。数据库回归：`ROOMUSIC_TEST_RUN='^(TestPostgreSQLBorrowFingerprintsAfterDirectoryRename|TestPostgreSQLScanRootAlignsCrossDirectoryCueParent|TestPostgreSQLEmptyRootProtection|TestPostgreSQLScanFeedbackMoveRestoreAndMissing|TestPostgreSQLIncrementalParseCacheCompleteTraversalCleansDeletedFile)$' mise exec -- ./scripts/test-integration.sh`。

## 3. 将整理确认切到成员目录

- 文件：backend/internal/library/curation_observations.go、backend/internal/library/scans.go、backend/internal/library/scanner.go。复用 app 已装配的 ConfirmScan。
- 取出到期 release 与成员路径，去重并合并碟父目录。锁内复查窗口、领取与建扫描行同事务；活动扫描返回时保留资格。确认和 missing 使用同一覆盖集合，覆盖不全不能确认；出错/取消沿用只撤销确认的约束。旧全局时间谓词两处都要拆开：确认的查重看本发行的变化时间，每日时段只看全库。
- 风险：一个局部扫描吃掉另一张专辑的 60 秒窗口；只读取合并发行的一部分就进入识别；持续读不了的文件被每个调度周期重复确认。
- 验证：`ROOMUSIC_TEST_RUN='^(TestPostgreSQL整理安静窗口只发一次确认扫描|TestPostgreSQL整理扫描中增长的文件不确认稳定)$' mise exec -- ./scripts/test-integration.sh`。测试代理补上文件读取范围后再断言读数。

## 4. 接入根生命周期、Linux 事件与定时器

- 文件：backend/internal/library/ 下新增 scan_watch.go、scan_watch_linux.go，修改 service.go、scanner.go、scans.go、scan_scheduler.go、scan_schedule.go、root_operations.go、exclusion_operations.go；backend/internal/app/app.go 负责与每日调度一起启动、取消和等待。
- 成功整根终态提交基线，根变动事务提交后唤醒。Linux 用现有 x/sys/unix 建立整根递归监听；先判挂载类型再 AddWatch，任何一处无法监听就释放本根整套 fd。实现目录新增、整棵移入、撤销与 cookie 对应的旧端/新端合并。采用已有 ScanSchedulerClock 注入时间，同一目录每次写入延后 2 秒。按根到期只提交该根；执行器忙时保存待扫代次。每日调度的两处全库时段查询按 whole_library 限定。
- 保留可替换的文件系统类型查询和挂监听调用，测试可注入 NFS/SMB、ENOSPC，不必真实挂载或修改内核。停机测试同时检查 fd、timer、worker 的退出顺序。风险是新子树文件早于监听、同一 move 分两轮导致身份变化，以及扫描结束误删运行中到达的事件。
- 验证：`(cd backend && mise exec -- go test ./internal/library -run '^(TestPlanScheduledScan|TestRegisteredRootMustRemainARealDirectory|TestScanFilterUserExclusionsUseExactRelativeDirectory)$')`；`ROOMUSIC_TEST_RUN='^(TestPostgreSQLScheduledScan|TestPostgreSQLScanCoordinationReusesActiveScanAndReleasesHolder|TestPostgreSQLRelocateRootScanLockRejects|TestPostgreSQLRelocateDisabledRootThenRestoreKeepsNewPath)$' mise exec -- ./scripts/test-integration.sh`。

## 5. 发布目录状态与间隔接口

- 文件：backend/internal/library/root_info.go、backend/internal/library/scan_watch.go、backend/internal/library/httpapi/handlers.go、新增 backend/internal/library/httpapi/root_scan_interval.go。GET 列表把当前进程的实时状态与持久化间隔合并，PUT 路由沿用设置接口的同源、管理员、严格 JSON 和错误信封。数据库保存成功后唤醒本根计时器。
- 状态与操作回执分开：已存的目录操作回放仍返回当时的收据，列表反映当前运行方式。重复 PUT 不改变计时起点。风险是先提交内存间隔再写库、把 pending 根展示成实时、写设置泄露主机路径。
- 验证：`(cd backend && mise exec -- go test ./internal/library/httpapi -run '^TestScanFeedbackPresenterRedactsRootIdentity$')` 用于编译路由及检查现有角色投影；`ROOMUSIC_TEST_RUN='^(TestPostgreSQLScanScheduleSettings|TestPostgreSQLExclusionAPIIdempotentReplay)$' mise exec -- ./scripts/test-integration.sh` 用于既有设置及目录收据。新增接口的功能验证见下表。

## 6. 同步合同并完成针对性验收

- 文件：.trellis/spec/backend/ 下的 library-scan-incremental.md（范围证据）、scan-coordination-contracts.md（锁、首次扫描、两类定时）、scan-feedback-contracts.md（局部计数和 missing）、library-scan-contracts.md（登记根列表字段）、library-folder-contracts.md（设置页接口与目录浏览的分工）、curation-contracts.md（仅修改确认扫描条款，与子任务 11 交接）。扫描版本号沿用子任务 2、3 合入结果。
- 验证：`git diff --check`；`(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')`。只在相关实现发生变化或缺少验证证据时重跑前面对应的小组。

### 测试代理清单

下列名称是拟新增的顶层测试。分别在相应实现合入后执行，不能把「未匹配到用例」报告为通过。

| 用例、位置 | 合成输入与关键断言 |
| --- | --- |
| TestDirectoryWatchDebounce，backend/internal/library | 可控时钟给两个独立目录连续发事件；1.999 秒不扫，2 秒只扫到期目录，新写入重置。两张碟共用时钟，繁忙或正在执行时收到的最后一代仍会执行 |
| TestDirectoryWatchFallback，backend/internal/library | 注入 NFS/CIFS/SMB2、局部 AddWatch ENOSPC、正常本地挂载；确认释放失败根此前注册的全部 watch、其他根继续，五个间隔换算正确 |
| TestPostgreSQLDirectoryWatchLifecycle，backend/internal/library 或 app | 独立临时根使用真实 inotify，卡住首次整根扫描，检查尚无监听运行；成功提交后复制合成专辑，连续文件写间隔小于 2 秒，记录曲库可见成员数只从零变到整张。对照在两个文件间暂停超过 2 秒。检查新增/停用/改址/恢复、排除变更、重启恢复和退出释放 |
| TestPostgreSQLDirectoryScanScope，backend/internal/library | 建兄弟专辑、同名前缀目录、根直属散曲、CD1/CD2、跨目录 CUE、排除子树。对移动、删除最后一首、整树移入/移出/改名检查 missing、反馈、ID 与旧缓存。旧目录外故意留同指纹副本，核对没有误配。对比空子目录、根失联、读取失败、取消时的负向对账 |
| TestPostgreSQLDirectoryConfirmationReads，backend/internal/library 或 app | 仅一张到期，另一目录的解析缓存也预热；在枚举与实际读入口用测试计数观察路径（不能以解析缓存命中数代替）。相关目录有枚举，兄弟目录枚举与文件读取均为零。再测两张不同目录分时到期、合并发行跨目录、CUE 和多碟、同窗口重复调用、失败与 busy、读取后窗口变化 |
| TestPostgreSQLRootPollAndSettings，backend/internal/app | 一个降级根及一个实时根。覆盖 GET 各模式、PUT 五档与非法值、未知根、角色与同源检查、重复保存。推进假时钟验单根到期、跨进程持锁、局部扫描不消费每日补扫、关闭每日计划仍按根轮询。迁移往返使用第 1 步的独立 schema |

单元用例命令：`(cd backend && mise exec -- go test ./internal/library -run '^(TestDirectoryWatchDebounce|TestDirectoryWatchFallback)$')`。集成用例命令：`ROOMUSIC_TEST_RUN='^(TestPostgreSQLDirectoryWatchLifecycle|TestPostgreSQLDirectoryScanScope|TestPostgreSQLDirectoryConfirmationReads|TestPostgreSQLRootPollAndSettings)$' mise exec -- ./scripts/test-integration.sh`。测试目录、时钟、挂载模拟和读数只属于本次运行；任何新状态应由合成文件或测试库生成。
