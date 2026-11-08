# 扫描协调合同

本合同来自原运行合同的“场景：持久化扫描取消与跨进程协调”，并并入扫描状态枚举、扫描记录接口的测试与 `canceled` 终态条目。
扫描内容本身（遍历、暂存、身份、CUE、封面）见 [目录与扫描流程合同](./library-scan-contracts.md)。

## 1. 范围与触发

当代码创建、恢复、查询、取消或结束全库扫描，或改路径事务需要与扫描互斥时，必须遵守本场景。其目标是在同一 schema 的多个
ROOMusic 进程之间只允许一个扫描执行器，并保证取消、停机、数据库故障或执行权丢失
不会触发错误的 `missing` 对账。PostgreSQL 同时是扫描状态和执行资格的权威；进程内
mutex 只保护本进程的 worker 引用。

## 2. 接口与数据库签名

- 扫描状态枚举：`running | succeeded | failed | canceled | incomplete`。
- `POST /api/v1/scans`：管理员启动扫描；新建时返回 `202`，已有本进程或其他进程的
  活动扫描时返回该活动 DTO 和 `200`，不改变原扫描模式。
  空请求体（无请求体或 `Content-Length: 0`）等价于 `{"mode":"incremental"}`；有体严格解码单字段对象 `{"mode":"incremental"}` 或 `{"mode":"full"}`，未知字段、缺少 mode、其他值或无效 JSON 返回 `400 invalid_input`。
- `Service.StartScan(ctx, options ...StartOptions)` 接受零个或一个 `StartOptions{Mode string}`，省略时默认增量；HTTP 总是传显式模式，再传至 worker、root 与 `ingest.PlanOptions`。`scanRoot` 保留默认增量入口，转调 `scanRootWithOptions`，避免其他线调用处的无谓变更；full 与 incremental 共用协调锁，不另开 HTTP 入口。
- `0038` 增加 `scan_runs.scan_mode TEXT NOT NULL DEFAULT 'incremental' CHECK (scan_mode IN ('incremental','full'))`；历史记录默认增量，下行删除列。
- `GET /api/v1/scans`：管理员查询扫描记录（F3），`page`（默认 1）、`page_size`（默认 20，最大 100），按
  `started_at DESC, id DESC` 排序；响应 `{ "items": [ScanDTO], "pagination": { "page", "page_size", "total" } }`。
- `GET /api/v1/scans/active`：已登录用户查询活动扫描，返回
  `{ "scan": ScanDTO | null }`。
- `GET /api/v1/scans/{id}`：已登录用户查询指定扫描。
- `POST /api/v1/scans/{id}/cancel`：管理员持久化取消意图；请求不等待 worker 结束。
- `scan_runs.cancel_requested_at TIMESTAMPTZ NULL`：`NULL` 表示未请求取消，首次取消写入
  数据库时间，重复取消用 `COALESCE(cancel_requested_at, NOW())` 保留原时间。
- 扫描锁使用两个 `int4` 键：固定类别 `0x5343414e`（ASCII `SCAN`，十进制
  `1396916558`，可放入 `int4`）和 `hashtext(current_schema())`。专用
  `database/sql.Conn` 调用 `pg_try_advisory_lock(0x5343414e::int, hashtext(current_schema()))`
  获取 PostgreSQL session advisory lock；作用域为当前 schema，与受保护的 `scan_runs`
  一致，不同 schema 的扫描互不阻塞。生产只有一个 schema 时，扫描互斥行为不变。
- `library.TryLockScanCoordination(ctx context.Context, tx *sql.Tx) (bool, error)` 位于 `library/scans.go`，供改路径事务调用：
  `SELECT pg_try_advisory_xact_lock($1::int, hashtext(current_schema()))`，参数复用该文件的 `scanAdvisoryLockClass`。
  与扫描执行器使用完全相同的两键和 schema 作用域，handler 不得复制常量或另建键；session 锁与事务锁在此互斥。
  返回 false 时改路径入口返回 `409 scan_running`（“正在扫描，稍后再试”），不写操作记录；SQL 失败按 `503 database_unavailable` 回滚。
  返回 true 时由同一改路径事务持有到提交或回滚，不在更新根路径之前释放；这是短写事务用途，不能据此把扫描执行器的 session holder 改成事务锁。
- 取得锁时将实际的两个键值保存在 `scanExecution` 中；worker 或恢复路径结束时，按保存的
  键调用 `pg_advisory_unlock(int, int)`，再关闭专用连接。解锁不得重新计算
  `current_schema()`，即使 holder 的 `search_path` 改变，也必须释放原先取得的锁。

## 3. 请求、响应与生命周期合同

`ScanDTO` 的基础 JSON 字段如下（M5 进度/报告字段见下文）：

```json
{
  "id": "uuid",
  "scan_run_id": "uuid",
  "status": "running | succeeded | failed | canceled | incomplete",
  "mode": "incremental | full",
  "started_at": "RFC 3339 timestamp",
  "finished_at": "RFC 3339 timestamp | null",
  "cancel_requested_at": "RFC 3339 timestamp | null",
  "diagnostics": { "total": 0, "counts": [] },
  "skipped_files": 0
}
```

- `mode` 是所有入口、所有角色的必需字段，只接受 `incremental` 或 `full`；列表、详情、活动、启动、取消响应使用同一 presenter，历史记录为 `incremental`。完整重扫绕过解析缓存读取和指纹借用，仍写缓存；其余语义见[目录与扫描流程合同](./library-scan-contracts.md)。
- `skipped_files` 是所有角色共用 presenter 的必需字段：非负 JS 安全整数或 null；历史扫描为 null，新扫描从 0 起，按文件/目录/CUE 保住登记计数，不受诊断上限影响。随进度及终态写入，大于零不改变 succeeded 与 missing 收口规则。
- `diagnostics` 是必需摘要，`total` 为诊断条目数，`counts` 为 `{ "kind": string, "count": integer }`
  数组；示例为零诊断状态。它不表示已扫描文件数或完成百分比。
- `id` 与 `scan_run_id` 当前必须相同；新增字段和状态必须同时更新后端 presenter、前端
  decoder、状态文案和测试，前端不得将未知状态强制转换为已知状态。
- `GET /api/v1/scans` 的条目与 `GET /scans/{id}` 的 `ScanDTO` 完全相同（含诊断汇总）。诊断汇总对整页只用一次
  `scan_run_id=ANY($1::uuid[]) GROUP BY scan_run_id,kind` 查询读取，列表与详情共用同一个读取函数，所以计数一致；
  单次扫描的诊断种类超过 100 视为数据异常，读取失败而不是截断。总数与当前页是两次查询，不保证同一快照；
  超出末页返回空 `items` 而 `total` 照常。`scan_runs` 每次扫描一行，表很小，不为列表另建索引。
- 启动和取消是同源、session、管理员授权的状态变更；活动/指定状态查询要求有效
  session；扫描记录列表要求管理员（唯一的使用方是管理员的扫描目录页）。前端权限只控制按钮可见性，不能代替后端鉴权。
- worker 的父 context 属于应用生命周期，不得使用 HTTP 请求 context。取消端点只提交
  意图；worker 每 500 ms 轮询一次，并在 root、目录遍历和文件处理边界协作式停止。
- session advisory lock 必须由贯穿扫描生命周期的专用连接持有；扫描读取、观察、诊断
  和终态写入使用该 holder。holder 上的 SQL 使用不随扫描取消而取消的调用上下文，避免
  driver 因查询中取消而关闭持锁连接；文件遍历与标签读取仍响应扫描取消，短 SQL 随后收口。
  连接池 `MaxOpenConns=1` 无法同时容纳 holder 与控制查询，必须拒绝启动而不是进入死锁。
- 只有持有同一协调锁的恢复路径可以把遗留 `running` 改为 `incomplete`；锁竞争时必须
  保持活动行不变。正常停机先取消 worker、有限等待，再关闭数据库。
- `finalizeScan` 先 `SELECT ... FOR UPDATE` 锁定扫描行。持久化取消优先收敛为
  `canceled`；只有完整 `succeeded` 才能在同一事务执行所有 root 的 `missing` 对账和
  终态更新。`canceled`、`failed`、`incomplete` 与协调故障均禁止负向对账。
- 改路径在幂等回放检查之后、锁目录行之前探测协调锁；禁止用 `scan_runs.status='running'` 查询或进程内 mutex 代替，避免检查与更新之间新扫描读到旧路径。
  改路径事务持锁期间 `StartScan` 仍沿用未获锁分支：可确认活动扫描则 `200` 返回该 DTO，否则 `503 scan_coordination_unavailable`，不得创建扫描。
  前端在改路径提交后才发起扫描；定时扫描若撞上这个短窗口，按本次跳过、下一次检查重试处理，不把锁竞争视为已启动。
- session advisory lock 没有 TTL，不能据本地时间推断锁已失效；发布或回滚时不得让
  不认识该锁的旧扫描器与新扫描器同时运行。
- `canceled` 是已落地的 wire 终态；取消意图持久化在
  `scan_runs.cancel_requested_at`，扫描 worker 由应用生命周期 context 管理，
  不继承启动或取消请求的 HTTP context。

## 4. 校验与错误矩阵

| 条件 | HTTP/状态行为 |
| --- | --- |
| 未登录访问扫描 API | `401` 安全错误 envelope |
| 普通用户启动或取消 | `403`，不创建扫描、不写 `cancel_requested_at` |
| 普通用户读取扫描记录列表 | `403 forbidden`；未登录为 `401` |
| 扫描记录的 `page`/`page_size` 非法（非整数、小于 1、`page_size` 超过 100、偏移溢出） | `400 invalid_pagination` |
| 扫描记录读取失败（计数、分页、诊断汇总任一步） | `503 database_unavailable`，不回显 SQL |
| 有请求体但 mode 缺失/非法、未知字段或 JSON 非法 | `400 invalid_input`，不启动扫描 |
| 启动获得锁且无活动任务 | 插入一个 `running`，返回 `202` 和新 DTO |
| 本进程已有活动任务 | 返回同一 DTO 和 `200`，不创建第二行 |
| 未获锁但可读取其他实例的活动任务 | 返回该 DTO 和 `200`，不等待、不接管 |
| 未获锁且无法确认活动任务 | `503 scan_coordination_unavailable`，不创建扫描 |
| 改路径探测扫描锁失败（任意进程持锁） | `409 scan_running`，不改路径、不递增版本、不写操作记录 |
| 改路径取得事务级扫描锁 | 持有至提交/回滚；期间扫描启动未获锁且无活动记录时 `503 scan_coordination_unavailable` |
| 改路径锁 SQL 失败 | `503 database_unavailable`，回滚目录操作事务 |
| holder 会耗尽唯一连接池槽位 | `503 scan_coordination_unavailable`，不启动 worker |
| 查询未知扫描 | `404 not_found`；其他数据库错误为 `503 database_unavailable` |
| 首次或重复取消 `running` | `202`；时间戳非空且重复请求保持不变 |
| 取消已是终态的扫描 | `200` 和原 DTO；不得改写终态或取消时间 |
| 恢复时协调锁正被持有 | 跳过恢复，其他实例的 `running` 保持不变 |
| 恢复取得锁并发现遗留 `running` | 收敛为 `incomplete`，不自动接管或续扫 |
| 取消、停机、协调/数据库故障 | 不执行 `missing` 对账；终态按已持久化证据收敛 |

## 5. 正确、基准与错误案例

- 正确：管理员连续两次取消同一 `running` 扫描，两次均返回 `202`，DTO 中的
  `cancel_requested_at` 完全相同；worker 随后写入 `canceled` 和 `finished_at`。
- 基准：刷新管理页面后调用活动查询，从 PostgreSQL 恢复“正在扫描”或“取消请求中”，
  不依赖旧页面内存；没有活动任务时返回 `{ "scan": null }`。
- 正确：第二实例未取得 advisory lock，读取并返回第一实例的 `scan_run_id`，不插入
  排队行，也不启动替代 worker。
- 正确：扫描专用连接持锁时改路径返回 `409 scan_running`；无扫描时改路径短事务持同键锁，提交后新扫描读取新路径。
- 错误：启动时无条件把所有 `running` 改为 `incomplete`；用事务级 advisory lock、
  本地 mutex 或 HTTP context 代替 session holder；先逐 root 提交 `missing` 再更新终态。

## 6. 必需测试与断言点

- PostgreSQL/HTTP：首次与重复取消均返回 `202`，时间戳持久且幂等；终态取消返回
  `200` 且终态不可逆；未知 ID、匿名和普通用户分别验证错误 envelope 与无副作用。
- PostgreSQL 同 schema 并发：两条独立物理连接竞争
  `(0x5343414e::int, hashtext(current_schema()))` 时只有一个成功；失败实例的 `StartScan`
  复用活动扫描 ID，不创建替代扫描；unlock/close 后另一独立连接可重新获取；
  `MaxOpenConns=1` 快速失败而非等待超时或阻塞，且不启动 worker 或创建扫描行。
- PostgreSQL 改路径互斥：独立连接持有扫描 session 锁时改路径返回 `409 scan_running` 且无收据/状态变更；反向由改路径事务取得同键锁时，扫描不能取得执行权，无活动行则 `503 scan_coordination_unavailable`。
  提交或回滚释放事务锁后可重新启动扫描；已成功操作的有效同键回放先于锁探测，不重复改路径。
- PostgreSQL schema 隔离：用两次 `postgrestest.NewSchema` 创建不同 schema，两边可以
  同时取得并持有各自的扫描锁，作用域与各自的 `scan_runs` 一致。
- PostgreSQL 解锁键：验证 holder 保存实际的两个 `int4` 键；改变 holder 的 `search_path`
  后，close 仍释放原键，原 schema 的独立连接可以重新取得锁。
- PostgreSQL 恢复：活 holder 存在时遗留行保持 `running`；释放后恢复为
  `incomplete`，且不创建替代扫描。
- PostgreSQL 终态：取消请求覆盖成功候选并得到 `canceled`；取消、失败和不完整均保持
  旧来源 `present`；成功扫描的所有 root 对账与 `succeeded` 更新一起提交或回滚。
- 后端并发与生命周期：取消轮询、停机等待、holder 释放和本进程 worker 引用通过
  适用的 `go test -race`；数据库/holder 故障不得继续 stale write。
- 完整重扫 HTTP：空请求体与显式 incremental 等价；full 写入并在全部 DTO 入口返回；非法请求 400；已有活动扫描不因所请求模式不同而创建新行或改模式。
- 前端 decoder/UI：上述 DTO 字段必需（含 `mode`），严格校验时间、状态与诊断摘要；活动空值、取消请求中、
  `canceled`/`failed`/`incomplete` 文案以及取消失败后的重试入口均有覆盖。
- 扫描记录（F3，`internal/app/scan_history_integration_test.go`、`internal/platform/httpserver/pagination_test.go`）：401/403、
  分页参数的各种非法值、排序与翻页覆盖全部扫描、条目与 `GET /scans/{id}` 逐项相同、超出末页、读取失败（改名表）返回 503；
  `catalog/httpapi` 的分页合同测试证明共用解析器后行为不变。

## 7. 错误与正确示例

### Wrong

```go
go runScan(request.Context(), scanID) // 客户端断开会成为业务取消
_, _ = db.Exec("UPDATE scan_runs SET status='incomplete' WHERE status='running'")
```

这既没有跨进程执行资格，也会在另一实例仍健康时破坏其活动状态。

### Correct

```go
execution, acquired, err := acquireScanExecution(app.scanContext, app.database.connection)
if err != nil || !acquired {
    // 返回活动任务，或以 scan_coordination_unavailable 安全失败。
    return
}
go app.runScan(scanID, execution) // worker 由应用生命周期和持久化取消共同驱动
```

终态仍必须在 holder 连接上的单个事务中锁定 `scan_runs`，并仅在判定为
`succeeded` 后执行 `missing` 对账。

## 8. M5 进度、报告与恢复补充

`ScanDTO` 增加 `progress/root_progress/report_status`，所有入口统一角色presenter。普通成员可看阶段、基点与根序号，省略根id/name；管理员有身份快照但无绝对路径。历史扫描为null/null/legacy；报告仅管理员，完整字段/错误矩阵见 [扫描反馈合同](./scan-feedback-contracts.md)。

全根先计数，之后逐根读取/整理/保存，阶段可随换根回到reading_tags而全局百分比不减。256单位或1秒安全边界刷新；服务端短语句超时不主动中断holder；running+version+seq拒绝迟到写。成功终态才10000，报告可能unavailable，非成功冻结。

终态事务先锁scan_runs，取消意图优先；仅succeeded做一次全根missing。missing的catalog锁序为全部既有发行升序FOR UPDATE→独立锁后重读→组升序FOR SHARE，不能持组再加发行锁；扫描不取治理操作锁。报告savepoint失败不得回滚核心missing。

RecoverInterruptedScans与StartScan遗留恢复在取得协调锁后同一事务分批读取至多256个running ID、改incomplete/unavailable并经ingest.ClearScansStaging清对应暂存。不得清理其他活扫描，也不重算进度或missing。若取消正在执行的SQL使holder断连，worker退出且连接/锁释放后由此恢复路径收口，而不是借另一个连接继续stale write。
