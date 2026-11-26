# 目录监听与定时扫描技术设计

依据为本任务 PRD、父任务 A1.1 和 A3，以及 ADR 0009 第 3.1 节。这里的「首次全量」指首次成功遍历整个登记根。现有默认 incremental 也会遍历全树，首次没有缓存时即建立基线。full 仍表示强制重读。

| PRD 范围 | 实现位置 |
| --- | --- |
| 只扫一个目录 | 第 1 节的范围、身份与对账 |
| 目录监听 | 第 2 节的注册、目录变动与安静计时 |
| 监听不到的目录 | 第 3 节的识别与单根定时扫描 |
| 设置页 | 第 5 节的管理员接口 |
| 确认扫描 | 第 4 节的逐发行窗口与成员目录 |
| 手动扫描、每天定时扫描 | 第 3 节的扫描协调与每日时段 |

## 1. 一次扫描实际覆盖什么

在 library 内为 StartOptions 增加私有范围，随 StartScan → runScan → countScanRoots → scanRootWithOptions → finalizeScan 传递。内部请求有三种范围：全部 active 根、一个登记根的整树、按 root ID 分组的一组根内目录。目录使用相对于原登记根的路径，空串在第三种范围里只代表根直属文件。保持原 root.ID、root.Path、排除规则与来源身份。对子目录作为起点的遍历，先按登记根逐段检查排除项和目录符号链接，扫描开始时再核对路径，避免排队期间被替换的目录突破原遍历边界。手动 HTTP 仍只接收 mode。

- 普通文件事件枚举所属目录的直属文件。碟目录事件归到其父目录，一轮收齐父目录直属文件和同级碟目录的直属文件，共用 ingest 的碟目录判定（由 scope.go 提供窄入口，跟随子任务 3 的规则）。父目录本身发生变化也收齐这些碟。根下其他专辑不参加预计数、不遍历、不解析。扫描使用同一份目录集合做预计数和正式遍历，复用原文件过滤、读标签、排除、暂存、解析缓存和按候选写入。范围的展开只检查目录项，不读取兄弟专辑的音乐。
- 目录树的移入、移出和改名涉及整个发生变化的子树。新端注册时枚举出实际目录，旧端用撤下的监听映射及库里旧路径取得曾有成员的目录，把两端加入同一批。普通目录下删除最后一首、或可访问根内子目录已经消失，可以作为该目录为空的证据。区分子目录确定不存在、权限错误和登记根不可用。失联的根走原 root_unavailable；整根为零且原有 present 曲目的保护只用于完整根遍历。根本身失去挂载或监听失效时不把它当成已删除子树。
- 同一根的所有目标先暂存完，再一次规划身份、保存及清暂存。移动两端同批时才能按现有唯一指纹/CUE 区间对照规则沿用原曲目 ID。ingest 接收范围作为参数；借指纹、vanishedTracksCTE、疑似移动和负向对账的旧侧仅取已覆盖的来源。保持既有严格过半的发行分配。根外、未扫的目录及目录名相似的兄弟项均不能作为消失证据。路径比较使用等值或含分隔符的前缀，目录名里的百分号和下划线按字面量处理。
- CUE 的成员有 sheet 与父音频两种路径。沿已有 tracks 的引用补入受影响 sheet 的目录；由 sheet 显式引用的根内父音频仍按现有解析规则处理，暂存对齐后再归组。对跨目录 sheet 与父来源，在计算旧侧和 missing 时按来源归属目录及这一轮实际覆盖的引用关系判断，不能仅因同一父文件被读到就删除未检查的 sheet。封面附属目录的事件映射回消费该封面的候选目录，沿用现有封面目录规则。多碟的独占证据不能从局部暂存中「没看到其它音频」直接推出；结合覆盖范围外已登记的 present 来源与本轮 held 证据判断，范围内以本轮观察为准。
- 终态事务沿用投影锁、取消复查和成功才允许 missing 的条件。MarkMissingSourcesWithFeedback 的报告快照与核心分批更新必须共用同一范围谓词，affected tracks 同步刷新搜索可见性。候选照原流程保存与 touch；目录扫描跳过按整根的 parseCache.cleanup 与 CleanupCandidateStates，下次整根成功遍历清理遗留缓存。仅遗漏清理会多占缓存，误清兄弟目录会使增量失效。临时观察仍按 scan ID 与 root ID 清理。

## 2. 监听生命周期与 2 秒安静窗口

library.Service 随应用生命周期管理根快照、监听描述符和到期队列，独立于自动整理是否开启。首次成功整根遍历在终态事务里记录基线，核对该根仍 active 且登记路径及配置版本与扫描快照一致。提交后才启动监听。重启只为已有基线的 active 根重建；未完成、取消、零根的扫描均不能给新根建立基线。

1. AddRoot 后为待首次扫描。DisableRoot（现有移除操作）提交后撤销该根监听、待扫目录和定时器。RestoreRoot、RelocateRoot 清除旧基线，下一次手动或每日整根成功扫描衔接新位置。排除设置提交后刷新过滤与监听集合；移除排除时把新开放的子树加入待处理集合。操作回放只复用已提交结果。启动、扫描结束及配置变更后重读 active 根，沿用调度器定期读库的机制同步其他进程的配置变更。出队取得扫描锁后再核对根和配置版本，旧路径的事件不能扫新位置。
2. 每个实时根使用独立的 inotify fd（现有 golang.org/x/sys/unix）。InotifyInit1 使用非阻塞与 close-on-exec。注册根后逐层先 AddWatch 再枚举子目录；跳过隐藏项、内置/用户排除子树及目录符号链接。建立整树监听期间接收的事件先积累，挂全才报告实时。读循环用可取消的等待（如非阻塞 fd 配合 Go 可唤醒的 os.File），停止时唤醒、等待退出并关闭 fd。
3. IN_CREATE、IN_DELETE、IN_MODIFY、IN_ATTRIB、IN_CLOSE_WRITE、IN_MOVED_FROM、IN_MOVED_TO 按 wd 还原所在目录。新建或移入目录立即递归注册，同时登记已经到达的子目录和文件所在目录，覆盖文件先于 watch 到达的情况。删除或移出撤销整个旧子树的注册。用 move cookie 关联同根的新旧端，统一更新后续 wd→路径映射，单端事件仍能处理。自移动、自删除、忽略通知与主动撤表对应，避免失效描述符继续使用。
4. 以（根 ID、归并后的专辑目录）为键，最后一次相关事件将截止时刻改为事件时间加 2 秒。关联移动两端取较晚截止，一次出队。新建目录树的父事件不能绕过子目录的写入计时；正在写的碟会推迟整张专辑。一个目录不停写不妨碍无关目录到期。定时器只投递已安静的集合，排队期间仍接收事件，取执行权时再检查截止时刻。扫描运行中同键的新事件记入下一代，结束时只移除本次取得的代次。
5. 应用取消 lifecycle 后先停止接收、撤销监听、停止 timer，等待读循环和目录调度器退出，并入 App.WaitForScanScheduler，最后沿用 WaitForScans 等待执行器及数据库锁释放。事件队列只在内存，停机期间的变动由手动和每日扫描覆盖。

## 3. 监听降级、单根定时与协调

非 Linux 平台的活动根在建立扫描基线后使用定时扫描，接口返回 `scan_watch_mode=scheduled`，每个根按自己保存的间隔轮询。

注册时用 unix.Statfs 识别本机视角的文件系统类型，如 NFS（含 NFSv4）、SMB/CIFS 和 SMB2。对子树中的独立挂载也作此判定。NAS 上的本地文件系统按实际类型处理。InotifyInit1 或 InotifyAddWatch 无法建立完整覆盖（如 ENOSPC 达到 watches 上限、EMFILE 达到实例或 fd 上限），或收到 IN_UNMOUNT 时，释放该根的全部 watch，改为单根定时。其他根继续工作。IN_Q_OVERFLOW 表示事件丢失，保留实时模式，重新核对目录注册集合，最后一次事件后安静 2 秒整根增量补扫一次。因卸载、上限或注册失败降级时，每次该根整树扫描成功后重新探测，挂全即恢复实时；网络共享持续按间隔扫描。只记录 root ID 与分类原因；通过实际系统调用结果识别上限，程序不调整系统设置。

每个已降级根各有一个到期时间。按该根最近启动时间计算到期日；首次进入定时模式时以进入时刻作基准。间隔有 15、60、240、720、1440 分钟可选，默认 60。按持续时长计算；1440 表示 24 小时。间隔改变时按同一基准加新间隔重排，已经到期则立即投递；相同值重复保存不推迟。轮到该根便发起整根 incremental。记录此根已启动的时刻，多进程在扫描协调锁内复查间隔，已经消费的同一时段合并。停机过多个间隔只需补当前一次。计时与每日整点的开关和时区分别处理。

所有入口共用现有 schema 级 advisory lock、scan_runs 活动行、取消与异常恢复。手动 StartScan 遇忙仍返回已有扫描。局部请求收到 Started=false 或因改路径暂占锁时保留事件/到期根；本进程在完成释放锁后唤醒，外进程占用时短时检查直到可执行，不使用紧循环。排队时同根同目录合并。单根定时和局部扫描均写 incremental/scheduled，与现有扫描记录、进度、报告和角色投影兼容。每日计划的 MAX(started_at) 和持锁后的 scheduledSlot 查重都只统计覆盖全库的扫描，避免局部扫描消费每日时段。每日整点恰有活动扫描仍遵守其既有跳过策略。

## 4. 60 秒确认扫描

保留 program 的 ConfirmScan 回调与启用条件。StartCurationConfirmation 按 release 读取到期的 curation_changed_at 和实际 present 成员，复用 curation_scan_files 的 root_id、path（含 CUE 两种路径）。同一展示发行经归并后可能跨目录、跨根；把全部活动成员目录去重，按第 1 节归并碟目录，在同一扫描内确认。成员有停用根、读取失败或覆盖不完整时不能授予整张确认。

去掉现有「任一扫描开始即消费窗口」的全库 NOT EXISTS。用（release ID、curation_changed_at）标识窗口；在取得扫描锁后复查未确认、已满 60 秒且窗口尚未领取，与新建扫描行在一个事务内记录消费时间戳。忙、未取得执行权或建行回滚均不消费。消费后即便同一坏文件仍存在也不反复扫，下一次成员变化开启新窗口。多个到期发行同目录只读一次。若对应目录仍在 2 秒写入窗口中，留到安静后领取。

finishCurationObservation 仍在 missing 之后运行。局部成功只给全部成员都被本轮覆盖、签名一致且无 held/moving 的发行确认，部分覆盖只可依据已观察到的变化撤销确认、重新开始窗口。保存签名需包括实际展示发行的全部成员，保留失败、取消、CUE 历史基线处理。对受影响发行的选择涵盖本轮写到、touch、转走或标缺失的成员；仅用一个新 observed_at 判定所有成员已读会误认合并发行。无关专辑的签名、窗口与确认状态保持原值。

## 5. 存储、接口、升级与回滚

新建一对迁移，基线最高 0097，文件名用最终合入序号 NNNN。一次迁移完成以下字段。目录集合、watch 模式及 wd 均为执行期状态。

| 表 | 新字段 | 用途 |
| --- | --- | --- |
| library_roots | poll_interval_minutes SMALLINT NOT NULL DEFAULT 60，CHECK IN (15,60,240,720,1440) | 已有和新根均有可持久化的间隔 |
| library_roots | watch_baseline_at、last_poll_started_at TIMESTAMPTZ NULL | 首次成功整根终态后记基线；最近轮询计时起点用于跨进程查重，首次降级初始化，之后与单根定时扫描建行同事务更新 |
| scan_runs | whole_library BOOLEAN NOT NULL DEFAULT TRUE | 区分全库与局部时段；升级前的记录都覆盖全库 |
| releases | curation_attempted_change_at TIMESTAMPTZ NULL | 保存最后一次已消费的成员变化窗口，与 curation_changed_at 等值判重 |

升级后已有根通过下一次成功的手动或每日全树扫描建立基线。旧扫描记录没有完整的逐根路径版本，不能证明当前路径已经扫过。首次降级时在库内将空的 last_poll_started_at 初始化为进入时刻，作为第一次计时起点；之后只在实际启动该根定时扫描时写入。这使尚未轮询过的根也能跨重启沿用首次计时。重新启用或改址同时清空最近轮询时间。解析 CommonVersion（该基线为 5）、格式版本、GroupingVersion（7）、整理 LocalRuleVersion（3）及 MatchRuleVersion（104）均无本任务版本增量；子任务 2、3 自行管理其规则升级。

| 管理员接口 | 请求与响应 |
| --- | --- |
| GET /api/v1/library-roots | items 每项追加 scan_watch_mode 和 poll_interval_minutes。模式 realtime 对应「实时监听」，scheduled 对应「定时扫描」；尚无基线或注册中用 null，停用时也用 null 并沿用 status 区分。间隔始终返回整数 |
| PUT /api/v1/library-roots/{id}/scan-interval | 同源、管理员，严格单字段对象，例如 {"poll_interval_minutes":240}。200 返回已保存的 {"poll_interval_minutes":240}。按根幂等覆盖该设置，UI 仅在 scheduled 时显示选择器；保存偏好不切换模式、不改变目录操作 revision。保存后唤醒调度器 |

沿用鉴权错误 401/403，错误 UUID 返回 400 invalid_id，未知根 404 not_found，非法、缺少、重复字段或尾随 JSON 返回 400 invalid_input，数据库错误 503 database_unavailable。GET 投影从配置与当前监听快照合并，不能因存在基线就声称实时监听；Admin 仍只看到原有安全根名。现有根操作收据按原样回放，前端通过列表刷新运行状态。接口路径是 REST 路由，非宿主机路径。

回滚按项目迁移执行器使用该序号的 .down.sql，先停止新版调度并等在途扫描退出，降级删除上述新增列与 CHECK。间隔偏好、监听基线和逐发行领取记录随之删除；曲目、扫描报告、既有成员签名及每日计划保留。旧版会将历史局部 scheduled 记录也计入其最近时段判定，下一每日时段恢复原节奏。再升级按默认间隔重建基线。

## 6. 实施文件归属

- backend/internal/library/ 下新增 scan_scope.go（枚举与范围）、scan_watch.go（事件、根模式与定时）和 scan_watch_linux.go（系统调用）。修改 scans.go、scanner.go、scan_progress.go、identity_plan.go、curation_observations.go、service.go、scan_schedule.go、scan_scheduler.go、root_operations.go、exclusion_operations.go、root_info.go。沿用 parse_cache.go 和 scan_pipeline.go 的文件读取实现，只调整调用方。
- backend/internal/catalog/ingest/ 下修改 scope.go、identity_plan.go、scan_feedback_missing.go、staging.go，发布碟目录/范围判定并约束旧侧身份与写入。backend/internal/library/httpapi/ 下修改 handlers.go，新增 root_scan_interval.go。backend/internal/app/app.go 负责启动和等待。迁移为 backend/migrations/NNNN_root_scan_watch.sql 与同名 .down.sql。
- 实现时同步 .trellis/spec/backend/ 下的 scan-coordination-contracts.md、scan-feedback-contracts.md、library-scan-incremental.md、library-scan-contracts.md、library-folder-contracts.md 与 curation-contracts.md 中对应条目。设置页由主代理实现，文件读取与归组规则分别归子任务 2、3。

## 拷贝验收口径

连续拷贝使用相邻写入间隙小于 2 秒的合成专辑，安静后整张入库。暂停超过 2 秒视为两次变化，中间可以看到已写完的曲目；整理仍须满足成员稳定满 60 秒并通过确认扫描。
