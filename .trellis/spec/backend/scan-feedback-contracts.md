# 扫描反馈合同

## 1. 范围与归属

M5 扫描进度、管理员报告、最近新增；需求与对外形状以任务 `11-04-classical-m5-scan-feedback/design.md` 为准。
迁移为 `0023_scan_feedback.sql`、`0024_catalog_recent_additions.sql`（合入时按主线顺序改号）。历史扫描不回填反馈版本。
library 拥有进度、报告收口与权限呈现；catalog/ingest 拥有扫描变化和新增发行关联；catalog 读取历史页/当前摘要，governance 只在报告读取时提供实时提示。

## 2. 接口与数据

- `ScanDTO` 原七字段不变，新增 `progress`、`root_progress`、`report_status` 和 `skipped_files`。历史前三者为 `null/null/legacy`，跳过数为 `null`；新扫描跳过数从 0 起，为非负 JS 安全整数，所有成员可见。
- `progress`：`version=1`，四阶段 `counting/reading_tags/organizing/saving`；`percent_bp`、`completed/total/unit`、`files_counted/files_total/files_processed`、`count_complete/files_changed`、`updated_at`。
- `root_progress`：所有成员可见 `index/total`，管理员另有 `id/name`；无当前根时 index 为空，管理员两个身份字段也为空，普通成员省略这两个键。不返回绝对路径。
- `GET /api/v1/scans/{id}/report?page=1&page_size=20`：管理员；页大小最多100。返回冻结的文件/专辑/曲目/诊断计数、`missing_reconciled` 和分页 `added_albums`。
- 新增列表冻结 `group_id_at_scan/added_at/title_at_scan/artist_at_scan/release_id`；动态补 `current_group_id/album/claim_hints`。数据库快照列是 `title_at_creation/artist_at_creation`，不要误用 wire 名写 SQL。
- `catalog.NewStore(tx).ListScanAddedAlbums` 与 `governance.NewStore(tx).LoadClaimHintsBatch` 共用 library 开启的短只读 REPEATABLE READ 事务。不可见新发行保留历史项，当前组与摘要为空；最多3条提示，不能逐项读取详情。
- 延迟敏感的 recent 与 `added_at_desc` 请求使用短只读事务，开始后执行 `SET LOCAL jit = off`，失败按查询错误处理；配置仅当前事务生效，遵循 [数据库规范](./database-guidelines.md)，不修改会话/全局配置或其它 planner 开关。
- 最近新增最多8项；组 `added_at` 为所有当前可见成员 `MIN(created_at)`，不是代表版本时间；详情、列表和recent共用投影。详见 [Catalog REST](./catalog-rest-contracts.md)。

## 3. 进度、记账与锁合同

### 全根计数、逐根流水

先固定 active roots，按 `path,id` 全根预遍历目录项（不读文件、不逐项 Stat）；候选为 CUE、支持音频与不支持音频候选扩展名的并集，经与正式遍历同一个 `scanFilter` 路径过滤。附件不计；候选文件符号链接计入，正式处理仍做安全校验；CUE 虚拟轨不重复推进文件数。
之后逐根读取、整理、保存并清理暂存；根进入各阶段都复核路径安全。计数阶段子目录列举失败只令 `count_complete=false`，正式遍历按目录保住，不单独禁止成功或 missing；根不可用、根本身读不了或核心数据库错误仍阻断完整成功。根列表读取失败不得伪装已确认空库。

计数后总数固定，真实完成尝试数可以超过总数；少遇到文件只在该根遍历结束判断，取消不补数。`files_changed` 一旦为真不回退。
计数阶段占500基点；后续按根预计数加权分配9400基点，根内读取/整理/保存比例60/20/20。仅最终有理数取整，中间使用溢出安全运算。
总数零时采用等根权重；没有根不做除法。阶段切换可重置局部分子，根切换可由saving回reading_tags，但全局百分比不减。
成功提交才10000；其他终态不超过9900并冻结。不因前端轮询或时间增加百分比。

同一个holder串行刷新，256单位或1秒的下一安全边界触发；阶段/根/终态前强制刷新。SQL同时检查running、version及更大的seq，拒绝迟到覆盖。
`scanFeedback.skipped` 对每个登记保住的文件、目录或失败 CUE 加一，不受诊断 100 条上限影响；过滤、确定缺失的 CUE 和封面源问题不计。随进度刷新写 `skipped_files`，终态核心与反馈降级分支都保留此计数；大于零仍可 succeeded 并做 missing。
进度写使用独立短事务的服务端 `statement_timeout=500ms`，避免为了反馈短超时主动断开holder。失败按批次节流，不逐文件重试。
scope分母仅属反馈，查询失败冻结进度但仍保存目录，不破坏报告健康；成功presenter按权威终态补saving/100及finished_at。

### 扫描变化

`ingest.NewScanFeedback(scanID)` 的对象由worker显式串行传给候选、封面、终态missing。无全局回调。
按Track UUID及ReleaseGroup互斥分类：新增优先于最终消失，再更新。mtime/size、观察时间、未知指纹回填、Medium UUID不算元数据更新；位置、归属、恢复、歌词、音频参数、CUE、规范化署名及扫描基础字段算更新。
同路径换内容为新轨新增+旧轨消失；旧来源腾位和曲目改挂必须记录原发行/组。专辑只有整组不再可见才missing；另一版本仍可见则updated。
历史记录不根据当前组重新归桶；后续治理导致的可见性改变不能由一次纯封面/元数据更新冒充扫描missing。

### 统一锁顺序

1. 治理仍先锁操作记录，涉及搜索投影的路径随后取得 CATP，再按UUID升序锁既有发行，再按UUID升序锁组；扫描不取得任何操作记录或人工覆盖表锁。
2. 候选扫描先取得 CATP，再在核心写入前收集目标发行、旧曲目归属及来源腾位占有者的全部既有发行，升序 `FOR UPDATE`。**下一条语句**重新读取归属/可见性，再升序 `FOR SHARE` 锁组，不能用同一JOIN加锁查询充当锁后快照。
3. 取得组锁后禁止补既有发行锁。集合失效采用**零次重试、立即放弃本次扫描报告**：回滚反馈保存点并释放新增锁，按原核心路径继续；不无限重试、不逆序补锁。新建私有发行/组的外键锁由并发测试覆盖。
4. 反馈前态/新增锁与后置记账分别有savepoint；锁预算500ms仅作用于反馈，恢复原预算后才运行核心SQL。反馈失败永久标记Broken，不能回滚已经提交或处于核心区域的变化。
5. 候选、封面只持数据库短事务锁；文件读取/准备在事务外。终态先取得 CATP 再锁scan_runs，然后一次全根missing、冻结报告和终态一起提交；missing按256行键集批处理，每批只返回至多256个ID以同事务刷新搜索可见性完成标记，不在Go拉全库，核心刷新不放进反馈保存点。
6. 报告聚合/反馈终态失败不改变核心结果，回滚反馈部分并置unavailable；若反馈列写入也失败，核心终态照常写，终态pending投影为unavailable。进度写失败与报告健康相互独立。
7. 正常完整成功为ready，正常收口的取消/失败/不完整为partial；崩溃恢复为unavailable。holder失效时不能另借连接继续扫描写入；worker退出、原事务锁释放后，持协调锁的恢复/下次启动把遗留running改incomplete并清理对应暂存，进度冻结。

## 4. 校验与错误矩阵

| 情形 | 行为 |
| --- | --- |
| 未登录/非管理员报告 | 401 unauthorized / 403 forbidden，鉴权先于存在性 |
| 非法ID/分页 | 400 invalid_id / invalid_pagination |
| 未知扫描/历史报告 | 404 not_found / scan_report_not_available |
| running报告 | 409 scan_report_not_ready |
| 记账失败、恢复或ready却缺报告行 | 503 scan_report_unavailable |
| 数据库报告/动态摘要/提示读取失败 | 503 database_unavailable，不伪造空报告 |
| 超JS安全整数或反馈锁/记录失败 | 报告降级，不让核心扫描仅因反馈而失败 |
| 非法、空或重复组sort | 400 invalid_sort |

## 5. 正确、基准与错误案例

- 基准：两根100/300文件，第一根结束2850，第二根读取开始仍2850，读一半4965，全根结束9900，成功提交10000。
- 正确：新扫描2轨新增；重扫无变化为零；删除一轨为tracks missing=1、albums updated=1；删除最后一轨专辑才missing。
- 正确：管理员扫描期间归组不计入扫描变化；历史新增专辑仍保留原组/标题，动态链接按当前组展示。
- 错误：用SQL UPDATE行数算updated，用created_at时间窗反推新增，反馈失败阻止候选保存，或强制取消的客户端SQL断连后继续使用失效holder写终态。

## 6. 必需验证

- library进度/边界集成：候选计数、分批与时间阈值、零根/零文件/不可用根、文件漂移、取消冻结、迟到seq、SQL服务端超时及holder存活、成功补偿、根读取/scope计数失败、RR快照。
- ingest：新增/重扫/恢复/替换/改挂/部分与整组missing、封面、600行分批原子、歌词/音频/CUE/规范化署名、故障降级、锁后重读、集合失效锁释放。
- app：`TestPostgreSQLScanFeedbackGovernance...` 用独立连接和数据库锁屏障覆盖候选/封面/missing与归组、认领、默认、修正、核对及撤销；校验锁顺序、最终目录图、人工revision和报告归桶，而非只看没有死锁。
- 样本由真实handler生成；`testdata/api-samples/scan-feedback/` 独立命名空间。既有样本重生但不得删除另一组样本。
- 隔离库执行相关race/vet/依赖规则，0 SKIP才算数据库验证；禁止复用用户数据库。查询计划和具体命令记录于任务verification目录。

## 7. 错误与正确实现

错误：持组锁后发现新发行，再补 `FOR UPDATE`；或把核心SQL包进反馈保存点，反馈失败回滚音乐变化。

正确：预先收集完整发行集合 → 发行升序锁 → 独立锁后读取 → 组升序共享锁 → 核心变化 → 独立反馈保存点。集合失效或预算超时释放反馈锁并降级，核心按原合同执行。
