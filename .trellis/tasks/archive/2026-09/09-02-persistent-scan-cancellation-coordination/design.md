# 技术设计：持久化扫描取消与跨进程协调

## 1. 设计边界与所有权

本任务仍属于 `library/scanner` 能力，但暂时遵循仓库现有过渡单体布局，不提前
拆分 `internal/*`。职责边界如下：

| 边界 | 所有者 | 责任 |
| --- | --- | --- |
| REST | `backend/cmd/roomusic/scans.go`、`application.go` | 解码请求、同源检查、会话/管理员鉴权、映射稳定响应和错误 |
| 扫描应用服务 | `scanner.go` 与新增的协调小模块 | 生命周期、可取消上下文、终态决策、负向对账闸门 |
| PostgreSQL 适配 | `database.go` 及扫描 SQL | 迁移、session advisory lock、短事务、状态和取消字段 |
| 文件系统 | 现有 scanner/parser | 只读遍历、解析和封面读取；不写音乐根目录 |
| 前端 | `frontend/src/api.ts`、`main.tsx`、必要的样式 | 解码 DTO、轮询服务端状态、展示管理员操作和恢复状态 |

进程内互斥只用于保护本进程的 worker/holder 生命周期；它不能作为跨进程执行权，
也不能让 UI 或 HTTP handler 自己决定扫描终态。

## 2. 状态机与不变量

数据库仍使用既有五个状态，不新增一个持久化的 `cancel_requested` 状态；取消意图
由新列表示：

```text
running + cancel_requested_at IS NULL  -- 正常执行
running + cancel_requested_at IS NOT NULL -- 取消请求已持久化，等待 worker 收敛
running -> succeeded                     -- 仅完整、无取消、协调资格仍有效
running -> canceled                      -- worker 在检查点观察到取消
running -> failed                        -- 明确的全局基础设施/不可恢复失败
running -> incomplete                    -- 根目录不完整、协调资格丢失或人工恢复
```

- 终态不可逆；取消端点不能把任何终态改回 `canceled`。
- `canceled` 只表示执行器实际观察到取消并完成终态写入。进程在观察前崩溃时，
  恢复路径标记 `incomplete`，即使行上已经有取消请求，也不声称取消被干净执行。
- `failed` 保留现有“明确失败”语义；连接/执行权丢失统一归入 `incomplete`，避免
  把部分遍历误报为完整失败。
- `missing` 对账和 `succeeded` 写入必须在同一终态事务中完成。任一 SQL 或提交
  失败，事务整体回滚，后续恢复只能将仍为 `running` 的行标记 `incomplete`。

## 3. REST 合同

### 3.1 共享扫描 DTO

成功响应使用以下字段；时间为 RFC 3339 UTC，新增字段均可为空以兼容旧客户端：

```json
{
  "id": "<opaque-scan-id>",
  "scan_run_id": "<same-id>",
  "status": "running",
  "started_at": "2026-09-02T10:00:00Z",
  "finished_at": null,
  "cancel_requested_at": "2026-09-02T10:03:00Z"
}
```

`GET` 状态响应不返回 SQL、内部错误、绝对路径或进程标识；管理员可通过已有诊断
端点查看有界的安全诊断。

### 3.2 启动与活动查询

- `POST /api/v1/scans`：要求同源、有效 session 和 `admin`。保持现有空 JSON 请求
  兼容性。
  - 新建并启动 worker：`202 Accepted`，返回共享 DTO，`status=running`。
  - 已有活动 worker：`200 OK`，返回同一个 `scan_run_id`，不新增行、不启动第二次
    遍历。锁竞争时不得先把该行标成 `incomplete`。
  - 获得锁但发现遗留 `running` 行：在同一恢复事务中标为 `incomplete` 后再创建新行。
  - 未获得锁且有活动行：短暂重试读取活动行；仍无法读到时返回
    `503 scan_coordination_unavailable`，绝不自行插入第二行。
- `GET /api/v1/scans/{id}`：要求有效 session，返回共享 DTO；只有 `sql.ErrNoRows`
  映射为 `404 not_found`，其他数据库错误映射为 `503 database_unavailable`。
- `GET /api/v1/scans/active`：要求有效 session，返回
  `{ "scan": <共享 DTO 或 null> }`。该端点让管理员刷新页面后仍能发现持久化活动
  扫描；没有活动任务不是错误。

### 3.3 取消

- `POST /api/v1/scans/{id}/cancel`：要求同源、有效 session 和 `admin`；请求体可为
  `{}`。handler 不等待 worker 完成，只等待取消意图事务提交。
- 事务先 `SELECT ... FOR UPDATE` 锁定目标行：
  - 当前为 `running`：执行
    `cancel_requested_at = COALESCE(cancel_requested_at, CURRENT_TIMESTAMP)`，
    提交后返回 `202 Accepted` 和共享 DTO。首次与重复请求返回相同时间和 ID。
  - 当前为任一终态：不改行，返回 `200 OK` 和真实终态；已成功的扫描绝不回退。
  - 不存在：`404 not_found`。
- 未认证、无管理员权限、数据库故障和协调故障分别沿用现有
  `401 unauthorized`、`403 forbidden`、`503 database_unavailable` /
  `503 scan_coordination_unavailable` envelope；所有响应保留 `request_id`。
- 取消字段的自然幂等性足够，本任务不另建 Change Set 或等待队列；日志记录 actor
  和 request ID，但不记录 session token。

## 4. PostgreSQL 迁移

新增 `backend/migrations/0009_scan_cancellation.sql`，仅做向前兼容的加法：

```sql
ALTER TABLE scan_runs
    ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS scan_runs_running_index
    ON scan_runs(started_at, id)
    WHERE status = 'running';
```

不修改既有迁移、不提供虚假的 down migration。旧行的 `cancel_requested_at` 为
`NULL`。迁移仍由现有 `embed.FS` 执行器在版本 1--9 连续校验、同一事务记录元数据；
迁移失败或记录漂移时应用不 ready。

## 5. 协调 holder 与生命周期

### 5.1 执行权

新增窄的 `scanCoordinator`/`scanExecution` 结构（可放在新文件中），每个执行中的
扫描拥有一条专用 `*sql.Conn`：

1. `db.Conn(ctx)` 取得连接。
2. 在该连接上执行
   `SELECT pg_try_advisory_lock($1::bigint)`，其中 `$1` 是代码中固定、公开记录且
   与迁移锁 `0x524f4f4d55534943` 不同的 scan key（建议 `0x5343414e`）。
3. 返回 `false` 时立即关闭连接，再查询活动行；不得在未持锁时执行恢复 UPDATE。
4. 返回 `true` 后，所有“恢复遗留行、创建 run、扫描写入、终态写入和释放锁”都由
   holder 的生命周期管理。正常结束先在同一连接执行并检查
   `pg_advisory_unlock($1::bigint)`，再 `Conn.Close()`。

session lock 不是事务锁：事务回滚不会释放它，连接 session 结束才会释放。因此不能
用随意的 `db.ExecContext` 代替 holder，也不能把 holder 放回池后继续用普通 `*sql.DB`
写扫描数据。

### 5.2 连接与本进程状态

- scanner 的数据库 executor 使用 holder 的 `QueryContext`、`QueryRowContext`、
  `ExecContext` 和 `BeginTx`；HTTP 状态/取消请求仍可使用池连接。
- 打开数据库时显式保证 holder 与 HTTP 查询至少有两个可用连接，或让所有 worker
  SQL 都在 holder 上执行并为状态请求保留一个槽位；测试覆盖 `MaxOpenConns=1` 的
  拒绝/超时行为，不能静默死锁。
- 本进程的 mutex 只保护“当前 run -> holder/cancel/完成信号”映射。重复启动即使
  穿过本地锁，也必须由 PostgreSQL lock 和活动行结果决定。
- 连接错误、holder 丢失或 unlock 失败都记录结构化事件；无法在失效连接上写终态时，
  不伪造成功，等待下一次持锁恢复将行置为 `incomplete`。

## 6. Lock-aware 恢复与启动竞态

`openDatabase` 不再无条件更新 `scan_runs`。恢复 helper 改为：

1. 取得临时专用 holder 并 `pg_try_advisory_lock`。
2. 失败则记录 `library.scan.recovery_skipped`，关闭连接并返回成功；另一实例的
   活动 `running` 行保持不变。
3. 成功则在短事务中锁定/更新所有遗留 `running` 行：
   `status='incomplete'`、`finished_at=CURRENT_TIMESTAMP`、
   `error_message='process_restarted'`，并写一条有界 recovery 诊断（若诊断写入
   失败，事务失败并让启动 fail closed）。不创建替代 worker。
4. 提交后释放 lock、关闭 holder。

显式 `POST /scans` 在成功取得同一 lock 后再次执行同样的遗留收敛，再原子地创建
新 run；这样可以覆盖启动恢复被另一健康实例跳过、随后管理员手动重新发起的场景。
旧版本遗留的多个 `running` 行都先收敛，不尝试猜测哪个应继续。

## 7. 可取消扫描执行器

- 每个 run 建立 `context.WithCancel`（必要时带取消原因），其父级由应用生命周期
  持有；HTTP 请求 context 不作为后台 worker 的父级。`main.go` 应使用进程生命周期
  context（例如信号触发的 `signal.NotifyContext`）创建应用，并在关闭 HTTP server
  时先取消 worker、等待其有界退出，再关闭数据库连接；这样正常停机也不会留下
  无主 goroutine 或把数据库连接提前归还给连接池。
- 后台 ticker 以固定、可注入的间隔（默认约 500ms，实施时记录常量）查询该 run
  的 `cancel_requested_at`。轮询使用独立的池连接执行只读查询，不与持有 session lock
  的 holder 并发复用同一 `*sql.Conn`。查询返回 true 时调用本地 cancel；查询发生数据库/holder
  错误时按协调失败停止本次扫描并归类为 `incomplete`，不继续无限遍历。
- `WalkDir` 回调入口、CUE track 循环、解析前后、每个观察/诊断批次边界和封面关联
  写入前后都检查 `context.Err()`。已经提交的观察保留；取消后不开始新的文件。
- 无法被 context 中断的单次操作仍受现有文件/数据库 I/O 边界约束；合同承诺的是
  “轮询间隔 + 下一个安全检查点”的可观察上界，不声称操作系统阻塞调用的严格毫秒
  上界。
- 取消观察后跳过 `markMissing`，将 `scanOutcome.Canceled` 交给统一终态函数。
  取消请求事务本身不因浏览器断开而回滚或撤销。

## 8. 终态与 missing 闸门

新增统一 `finalizeScan`（或等价窄函数），使用 holder 开启短事务：

```text
BEGIN
  SELECT status, cancel_requested_at FROM scan_runs WHERE id = $1 FOR UPDATE
  if status != running: return existing terminal state
  if cancel_requested_at IS NOT NULL or outcome.Canceled:
      UPDATE scan_runs SET status='canceled', finished_at=NOW()
  else if outcome.Failed:
      UPDATE scan_runs SET status='failed', finished_at=NOW(), error_message=<safe code>
  else if !outcome.Complete:
      UPDATE scan_runs SET status='incomplete', finished_at=NOW(), error_message=<safe code>
  else:
      UPDATE tracks ... WHERE source_root_id IN (all roots)
      UPDATE scan_runs SET status='succeeded', finished_at=NOW(), error_message=NULL
COMMIT
```

实际 SQL 必须绑定参数；多个 root 可以在同一事务中循环执行，但禁止每个 root 单独
提交。`SELECT ... FOR UPDATE` 使取消与成功线性化：

- 取消先提交，终态事务看到取消并得到 `canceled`；
- 成功终态先提交，随后取消只返回 `succeeded`，不回退；
- 取消请求在 `markMissing` 期间等待行锁，不会留下“部分 missing + incomplete”。

终态 SQL/提交失败时保持行 `running`（事务回滚），记录一次错误；下一次持锁恢复
将其置为 `incomplete`。诊断写入必须检查错误，不能因为忽略 `Exec` 错误而继续报告
成功。

## 9. 日志与诊断

使用现有 `log/slog` JSON 合同，事件名称固定且不把状态拼进名称：

- `library.scan.started`
- `library.scan.cancel_requested`
- `library.scan.completed`（字段 `outcome` 为枚举）
- `library.scan.recovery_skipped`
- `library.scan.coordination_failed`

事件包含 `scan_run_id`、必要时 `actor_id`、`request_id`、`outcome`/`reason` 和有界
计数或耗时；不包含完整 NAS 路径、文件名集合、token、SQL 或音频内容。恢复原因
写入有界 `scan_diagnostics`，与日志和扫描历史分离。

## 10. 前端状态与交互

不引入 router、query/cache 或 UI 库。`frontend/src/api.ts` 增加
`cancel_requested_at` 的严格解码、活动查询 decoder 和取消请求 decoder；未知状态
仍安全拒绝。可以将现有 `ScanStartDTO`/`ScanStatusDTO` 收敛为共享扫描 view model，
但保留旧字段兼容。

`main.tsx` 的扫描生命周期：

1. 管理员 session 建立后请求 `/api/v1/scans/active`，以服务端结果恢复页面状态；
   无活动任务显示空状态。
2. `running` 时每秒轮询 `/api/v1/scans/{id}`；状态为终态后停止并清理 timer。
3. `cancel_requested_at != null` 时显示“取消请求中”，按钮进入 pending/禁用；不在
   本地把状态直接改成 `canceled`。请求失败保留活动状态并显示可重试错误。
4. 终态分别显示成功、失败、已取消和不完整；不完整不得使用“已同步”文案。
5. 只有管理员渲染取消/启动管理按钮；普通用户仍可按既有权限查看状态，后端每次
   mutation 重新检查 401/403。

状态文本放入现有工作区的语义按钮和 live region，保证键盘可达、无颜色单独编码、
窄屏不重叠；不把错误消息字符串作为逻辑分支。

## 11. 兼容、发布与回滚

- 0009 是 additive migration；旧客户端忽略新增字段仍可查询状态。新客户端遇到旧
  服务端缺少字段时按 `null` 处理，但取消端点必须由新服务端提供。
- 新旧扫描执行器不能在同一数据库上滚动并存：旧二进制不持有 scan lock，也不会
  读取取消字段。发布前先完成迁移，再停止旧 worker/实例并启动新版本；必要时使用
  短暂维护窗口。
- session lock 无 TTL。网络分区期间 PostgreSQL 尚未侦测断开时，`running` 可能
  暂时存在；运维文档不得承诺秒级自动收敛。
- 不写 down migration。若实现需回退，先停止新扫描并备份，回到兼容旧二进制后用
  forward-fix 处理状态；不要删除 `cancel_requested_at` 或强制清理扫描数据。

## 12. 验证策略

后端 PostgreSQL 集成测试使用两个独立连接/应用和隔离 schema，覆盖锁、取消、恢复、
终态事务和连接池边界；单元测试覆盖状态映射与检查点。前端使用现有 Vitest 验证
decoder/状态投影，`npm run build` 和人工窄/宽屏检查验证界面；不把 UI 本地状态当
作后端安全证据。
