# Research: 扫描跨进程单任务协调方案

- Query: 在现有 Go `net/http` + `database/sql` + PostgreSQL 扫描器中，比较专用连接持有的 PostgreSQL session advisory lock 与 `scan_runs` 的 owner/lease/heartbeat/fencing 方案；覆盖持久化取消、崩溃后标记 `incomplete`（不自动接管）、活动任务保护、边遍历边写入兼容性、迁移和测试复杂度，并给出本任务推荐。
- Scope: mixed（仓库代码/规范 + PostgreSQL 18、Go 标准库官方文档）
- Date: 2026-09-02

## Findings

### 1. 当前实现和不可改变的约束

| 证据 | 结论 |
| --- | --- |
| `backend/cmd/roomusic/application.go:17-23`、`backend/cmd/roomusic/scans.go:17-30` | 执行权目前只由进程内 `scanMutex`/`runningScan` 表示；两个进程可以各自创建 `running` 行并遍历同一批目录。 |
| `backend/cmd/roomusic/scanner.go:56-90` | 扫描使用不可取消的 `context.Background()`；终态写入在遍历后单独执行。需要改成由持久状态驱动的派生 context，并保证终态更新只由当前执行者完成。 |
| `backend/cmd/roomusic/scanner.go:119-223` | `filepath.WalkDir` 在每个条目开头检查 `context.Err()`；这是适合有界取消检查点的位置。解析、CUE、诊断和封面处理都可能继续做 I/O，取消延迟必须按检查点和 I/O 超时定义。 |
| `backend/cmd/roomusic/scanner.go:257-311` | 每个观察通过独立 `BeginTx`/`Commit` 写入；遍历期间没有长事务。任何协调方案都不应把整个目录遍历包在一个事务里。 |
| `backend/cmd/roomusic/scanner.go:395-401` | `markMissing` 在终态行更新前运行，并按 root 分别执行更新；若进程在其中途崩溃，可能留下部分 `missing`，随后把 run 标为 `incomplete`。这与“只有完整成功才能负向对账”冲突，必须在本任务中作为终态/对账事务问题单独修正，不能指望锁本身解决。 |
| `backend/cmd/roomusic/database.go:68-82` | 启动时当前 `recoverInterruptedScans` 无条件把所有 `running` 改成 `incomplete`。多实例启动时会误伤仍由另一进程执行的活动任务；恢复必须先取得协调资格，或按可验证的租约过期条件筛选。 |
| `backend/migrations/0002_core_slice.sql:26-32` | `scan_runs` 已有 `running/succeeded/failed/canceled/incomplete` 状态、开始/结束时间和错误消息，但没有取消意图、执行者或心跳字段。 |
| `.trellis/spec/backend/database-guidelines.md:67-74,182-186` | 事务不能跨越文件遍历；进度应以有界批次保存；只有完整成功扫描可做 `missing`；扫描集成测试必须证明不完整扫描不会对账。 |
| `.trellis/spec/backend/core0-runtime-contracts.md:58-64,124-147` | PostgreSQL 是唯一业务权威；`canceled` 已是 wire 枚举但尚无取消端点；取消、失败、不完整都不能改变既有来源的缺失状态。 |
| `.trellis/tasks/09-02-persistent-scan-cancellation-coordination/prd.md:17-50` | 本任务不排队、不自动接管；异常退出或协调资格失效收敛到 `incomplete`，由管理员显式重新发起。 |

### 2. 两种方案的准确语义

#### 方案 A：专用连接 + session advisory lock

1. 启动请求先通过 `db.Conn(ctx)` 取得一个专用 `*sql.Conn`，在该连接上执行
   `SELECT pg_try_advisory_lock(<scan-key>)`。成功后才在短事务中查找/创建唯一
   `running` run；失败则读取并返回现有活动 run。
2. 专用连接必须贯穿整个扫描生命周期。扫描的 SQL 可以继续用 `*sql.DB` 池，
   但更安全的实现是把所有扫描写入和协调查询都绑定到该 `*sql.Conn`，这样连接
   丢失会让后续 I/O 失败，而不会悄悄改用池中另一条 session。结束时必须在同一
   连接执行一次 `pg_advisory_unlock`，确认结果后再 `Conn.Close`；不能只把连接
   放回池中。
3. 这个锁是数据库后端 session 的属性，不是 Go goroutine 或 HTTP 请求的属性。
   PostgreSQL 会在 session 结束（包括非正常断开最终被服务器检测到）时释放它；
   事务回滚不会释放 session lock。`pg_try_advisory_lock` 不等待，适合“已有活动
   run 就返回其 ID”的 API。
4. session lock 对同一 session 是可重入的。若把锁调用散落在 `*sql.DB` 查询中，
   同一池连接可能再次成功取得同一 key，破坏“一个执行者”判断；锁获取必须集中
   在一个 holder 对象中，且不能让两个 run 共享 holder。

#### 方案 B：`scan_runs` owner/lease/heartbeat/fencing

建议的字段语义应类似：

```text
cancel_requested_at  timestamptz NULL
owner_id             uuid/text NULL       -- 不含秘密的进程实例标识
lease_expires_at     timestamptz NULL
heartbeat_at         timestamptz NULL
fencing_token        bigint NOT NULL      -- 每次新 claim 单调递增
```

字段本身不提供互斥。创建/认领必须另外具备一个原子门闩，例如：

- 一个永久的 `scan_coordinator` 单例行，用短事务 `SELECT ... FOR UPDATE`；或
- `scan_runs` 上针对 `status='running'` 的唯一部分索引/常量键；或
- 短时 advisory lock 只保护 claim 事务。

否则两个“没有看到 running 行”的事务仍可同时插入。

执行者按固定间隔用服务端时间续租。所有会写目录、观察、诊断、封面、对账或
终态的事务都要把 `id + owner_id + fencing_token + status='running'`（以及适用的
未过期条件）放进条件更新/断言，并在同一事务内先锁定或更新 run 行。旧执行者
即使在租约过期后恢复，也不能通过过期 token 写入新扫描的数据；这就是 fencing
字段的必要性。只在遍历循环外检查一次 owner，不能达到 fencing 语义。

### 3. 持久化取消的共同设计

两种协调方式都必须额外持久化取消意图；锁或租约不会自动让 Go `context` 取消。
推荐使用 `cancel_requested_at`（可选 `cancel_requested_by`）而不是把“请求已到达”
只放在内存或 HTTP context 中：

```sql
UPDATE scan_runs
SET cancel_requested_at = COALESCE(cancel_requested_at, CURRENT_TIMESTAMP)
WHERE id = $1 AND status = 'running'
RETURNING status, cancel_requested_at;
```

- 受保护的管理员取消端点执行同源、session 和角色检查；重复取消返回同一个
  持久状态。已是终态时返回该终态（幂等 no-op），不能把 `succeeded` 改回
  `canceled`。
- 扫描 goroutine 以 `context.WithCancel`/`WithCancelCause` 为本地停止信号，后台
  轮询 PostgreSQL 的取消字段，或在每个有界批次开始时查询。`WalkDir` 回调、解析
  循环和数据库批次都要检查该 context；取消函数本身不等待 goroutine 结束，HTTP
  响应只能表示“取消请求已持久化/正在收敛”。
- 最终化必须在短事务中 `SELECT ... FOR UPDATE` run 行，再按状态决定结果：取消
  优先得到 `canceled`；只有仍为 `running`、无取消请求、所有 root 完整且协调资格
  有效时才允许 `succeeded`。
- `missing` 更新必须与“成功”判定处于同一可回滚终态事务，或采用等价的成功标记
  闸门。不能先按 root 提交 `missing`，再单独写 `succeeded`；否则进程在两步之间
  崩溃会把不完整扫描留下的来源误标为 `missing`。

### 4. 崩溃、失去资格和恢复

#### 方案 A 的恢复

- 正常完成：worker 写终态后释放 session lock。
- 进程崩溃：PostgreSQL 最终检测到 session 断开并释放 lock，但 `scan_runs.status`
  不会自动改变。必须在下一次应用启动或管理员显式发起扫描时，先尝试取得同一
  scan lock，再把遗留的 `running` 行（通常可限制为“没有当前 holder”的活动行）
  标为 `incomplete`，并写 `error_message='process_restarted'`/诊断；不启动替代
  worker。这正是“人工恢复、不自动接管”。
- 若现有 `recoverInterruptedScans` 仍在未取得 scan lock 时执行无条件 UPDATE，
  第二实例启动会把第一实例的健康 run 错标为 `incomplete`。因此恢复不能继续放在
  `openDatabase` 的无条件批量更新路径中；至少应改成 lock-aware 的 try-lock +
  短事务，或在每次显式 start 前完成。
- session lock 没有 TTL。若网络分区使 PostgreSQL 尚未察觉 socket 断开，锁和
  `running` 行可能比期望时间更久；启动/显式 start 之外没有 watcher 时，状态不会
  立即收敛。这是方案 A 的主要 liveness 限制，必须在运维合同中写清楚。

#### 方案 B 的恢复

- 心跳续租把“最后一次确认仍在工作”的证据存入数据库；一个只负责回收的周期
  检查可执行：

  ```sql
  UPDATE scan_runs
  SET status = 'incomplete', finished_at = CURRENT_TIMESTAMP,
      error_message = 'lease_expired'
  WHERE status = 'running'
    AND lease_expires_at < CURRENT_TIMESTAMP;
  ```

- 回收器只标记过期行，不创建新 worker；管理员下一次明确 POST 才能申请新 run。
  这比方案 A 能更快收敛，也不依赖服务器先发现 TCP 断开。
- 租约超时不能证明进程已经死亡：GC 停顿、磁盘阻塞、数据库短暂不可用或调度
  延迟都可能让健康 worker 错过心跳。回收器若直接把行改成 `incomplete`，旧 worker
  可能随后恢复。fencing token 和每个写事务的条件断言能阻止旧 worker 写入，但
  不能消除“健康任务被提前结束”的用户体验风险；应有宽裕 TTL、至少一次 grace
  周期和可观测的 `heartbeat_at`。
- 回收与 worker 的竞态必须由数据库条件更新决定。worker 续租和回收器不能靠
  应用内时钟或先查后写；服务端 `CURRENT_TIMESTAMP`/`clock_timestamp()` 与
  `UPDATE ... WHERE fencing_token=...` 才是权威。

### 5. 活动任务保护和启动竞态比较

| 场景 | 方案 A：session lock | 方案 B：owner/lease/fencing |
| --- | --- | --- |
| 两实例同时 POST start | 一个专用 session 获得 `pg_try_advisory_lock`；另一个立即失败并返回当前 run。需在锁内完成“查/建 run”。 | 需要单例行、唯一部分索引或短 advisory claim 锁；字段本身不够。冲突通过唯一约束/行锁映射为复用现有 run，而不是 500。 |
| 第二实例启动时第一实例仍扫描 | try-lock 失败则不得执行恢复 UPDATE；直接保持第一实例的 `running`。 | 读取未过期 lease 的 owner 后不得回收；只有过期且满足 grace 条件才可标记。 |
| 第一实例在恢复检查瞬间崩溃 | 新实例可能先跳过恢复，稍后显式 start 再清理；不会误杀健康任务。 | 回收器可能在边界时刻将其标为 incomplete；fence 保证迟到写入被拒绝。 |
| 旧 worker 恢复后继续写 | 若所有扫描 SQL 都绑定专用 holder Conn，连接断开后 I/O 失败；若仍使用任意 `*sql.DB`，需额外 fencing，否则有迟到写风险。 | fencing 是方案核心；每个观察/诊断/封面/终态事务都必须检查 token，遗漏一个写点就会破坏保证。 |
| 数据库重启 | session lock 消失，run 行需下一次 lock-aware 恢复；无 TTL。 | lease 行保留，过期回收可在服务恢复后进行；新 claim 仍需原子门闩。 |

### 6. 与当前“边遍历边写入”代码的兼容性

#### 方案 A

- 只增加一个扫描生命周期资源（专用 `*sql.Conn`）和取消轮询，现有每文件短
  事务模型可保留；不需要给 `tracks`、`track_observations` 或 `scan_diagnostics`
  增加 owner 列。
- 为了防止 lock holder 丢失后旧 worker 使用池中另一连接继续写，推荐把 scanner
  的数据库 executor 抽象为窄接口，并在一次 run 中使用 holder Conn 的
  `BeginTx/ExecContext/QueryContext`。HTTP 取消/状态查询仍可使用 `*sql.DB`。
- `database/sql.DB.Conn` 会保留一个池连接；若连接池被配置为只有一个最大连接，
  holder 会占满池而使 worker 的其他 DB 操作死锁/超时。必须保证至少两个连接，或
  让 holder Conn 承担全部扫描 SQL，并在测试中覆盖池容量。
- artwork 文件写入发生在数据库写入之外；丢锁后已写入 ROOMusic data 目录的内容
  可按内容 hash 重用，但关联 SQL 必须失败关闭，不能宣称扫描成功。

#### 方案 B

- 心跳 goroutine 可独立于文件遍历运行，但每个现有写点都要加入 owner/fence
  断言：`saveObservation` 的 catalog 事务、`saveArtwork` 的关联写入、诊断写入、
  `markMissing` 和终态写入。只在 `WalkDir` 开头检查 lease 会留下 stale-write
  窗口。
- 最安全的批次形状是在同一短事务内先锁/更新 `scan_runs` 的 heartbeat/fence，
  再写该批 observations；回收器若等待该行锁，会看到批次已续租或在提交后拒绝。
  这会改动当前 `saveObservation` 的事务边界和调用签名，代码/测试侵入明显高于
  方案 A。
- 现有 `recordDiagnostic` 忽略了部分 `Scan`/`Exec` 错误（例如
  `scanner.go:153-165,181-214` 的若干调用）；引入 fencing 时必须先统一处理这些
  错误，否则 lease 丢失可能被吞掉并继续遍历。
- `markMissing` 不能按现状逐 root 独立提交；无论选哪种协调方案，都应把成功门槛、
  fence 检查和负向对账放在一个终态事务/等价闸门内。

### 7. 迁移、运行和测试成本

| 维度 | 方案 A | 方案 B |
| --- | --- | --- |
| 必需 schema | 仅新增 `cancel_requested_at`（以及可选 actor/reason）；session lock key 不落表。 | 至少新增 owner、lease expiry、heartbeat、fencing、取消字段；还要有 claim 的单例行/唯一部分索引和索引维护。 |
| 旧数据 | 所有已有 `running` 行可在 lock-aware 启动/显式 start 时统一标 `incomplete`。 | 旧 `running` 行没有 owner/lease，迁移需明确全部标 incomplete 或安全回填；若存在多个 running 行，唯一部分索引会失败，必须先清理/拒绝 ready。 |
| 回滚 | 应用回退只需保留新列的向前兼容；锁 key 不改变已有迁移锁 key。 | 需要 expand/backfill/switch/contract 计划；删除租约列不能作为假 down migration。 |
| 可观测性 | owner/heartbeat 不在业务行；可查 `pg_locks`，日志需记录 run ID 和协调失败。 | 状态接口可显示（或仅管理端显示）heartbeat/lease age，诊断更直接，但要避免暴露主机/进程秘密。 |
| 集成测试 | 两个独立 `*sql.DB`/`*sql.Conn` 的 try-lock 竞争、holder 释放/重启恢复、连接池生命周期、取消竞态、终态无误标 missing。 | 除上述外，还需心跳、TTL/grace、回收竞态、stale owner fencing、token 单调递增、唯一 claim 和时钟边界；需要稳定的时间注入或 PostgreSQL 测试控制。 |
| 复杂度 | 低至中；主要风险是 `database/sql` 连接绑定和无 TTL 的 liveness。 | 高；状态机、租约回收和每个写点 fence 都是新的持久合同。 |

最小测试矩阵（两方案共同）：

1. 两个应用实例并发 start：恰有一个执行者，另一个复用相同 `scan_run_id`，不新增
   第二遍历。
2. 管理员取消、重复取消、终态后取消、普通用户/匿名取消；断言持久字段和稳定
   HTTP 错误/响应，不依赖客户端断开。
3. 取消发生在文件之间、解析期间、最后一个 root 完成后和 missing 闸门前；最终
   `canceled` 不执行 missing，已提交合法 observation 可以保留。
4. 进程/连接异常：run 最终可解释为 `incomplete`，没有自动启动替代 worker；旧
   worker 的迟到写入不能改变新 run 的结果。
5. 成功终态事务崩溃注入：missing 与 `succeeded` 要么一起提交，要么一起回滚；
   不能留下部分 missing。
6. 数据库不可用、锁等待 context 取消、连接池 `MaxOpenConns` 边界、`go test -race`
   和 PostgreSQL 18 集成路径。

### 8. 推荐

**本任务第一版推荐方案 A：专用 `*sql.Conn` 持有 PostgreSQL session advisory
lock，加上 `scan_runs.cancel_requested_at` 和 lock-aware 恢复；暂不引入完整
owner/lease/fencing 状态机。** 理由如下：

- Core 0 明确不做自动接管，目标是“一个全局扫描 + 管理员手动重启”；session lock
  直接表达这个互斥边界，且 PostgreSQL 在 session 结束时负责释放锁。
- 当前扫描已经按文件短事务写入；专用连接 holder 和取消轮询能以较小的代码面接入，
  不必为每个 catalog/diagnostic 写点传播 lease token。
- 迁移和回归测试成本明显较低，符合 Core 0 保持 PostgreSQL-only、避免通用任务
  队列的边界。
- 可通过“启动或显式 start 先 try-lock，再把遗留 running 标 incomplete”修复当前
  无条件恢复误伤活动任务的问题；无锁时绝不执行恢复 UPDATE。

采用该推荐时必须接受并记录两个限制：

1. session lock 没有 TTL；网络分区期间 PostgreSQL 未检测到连接死亡时，状态可能
   暂时保持 `running`。若产品验收要求在不重启/不显式 start 的情况下也有严格的
   秒级收敛，方案 A 单独不够，应改选方案 B。
2. 为防止 holder 丢失后的 stale write，扫描 SQL 应绑定 holder Conn，或在后续
   专门任务中补充 fencing。不能一边声称 session lock 是执行权，一边让 worker
   在锁连接断开后无条件使用 `*sql.DB` 继续写。

**何时选方案 B：** 若部署环境必须在应用仍存活时自动发现并标记断联任务、需要
   heartbeat/lease 可观测性，或未来要支持可验证的接管/多 worker，直接设计
   owner/lease/fencing；但即使“不自动接管”，也要承担租约误过期、每个写点 fencing、
   claim 单例门闩和更高测试成本。不要只添加四个字段而省略原子 claim 或 fence。

### 9. 实现顺序建议（供主任务 design/implement 使用）

1. 先定义 `scan_runs` 状态机和 SQL 终态闸门：`running -> cancel_requested（可用
   字段表示） -> canceled|succeeded|failed|incomplete`；明确终态不可逆，只有
   `succeeded` 能在同一事务中做 `missing`。
2. 增加取消意图迁移和管理员端点；为重复请求、终态请求及权限失败固定响应。
3. 抽取 scan coordination holder：`db.Conn` + 独立 key + try-lock + 释放/异常
   处理；将恢复路径改为持锁后执行，禁止全表无条件把活动 run 标 incomplete。
4. 让 scanner 使用可取消的 run context，并在有界遍历/解析/批次检查取消；将所有
   scan 写入绑定到 holder 或明确的窄 executor。
5. 重构 finalization/missing 为可回滚事务，增加进程异常和连接丢失测试。
6. 最后接入管理界面状态和结构化事件（`library.scan.started`、
   `library.scan.cancel_requested`、`library.scan.completed`/`incomplete`），不把
   日志当作持久状态。

## External References

- PostgreSQL 18 Documentation, “13.3.5 Advisory Locks”: <https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS>。说明 session-level lock 在显式 unlock 或 session 结束前保持、事务回滚不释放；transaction-level lock 在事务结束自动释放；session-level 请求可重入且必须配对 unlock。
- PostgreSQL 18 Documentation, “9.28.10 Advisory Lock Functions”: <https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADVISORY-LOCKS>。说明 `pg_advisory_lock`、`pg_try_advisory_lock`、`pg_advisory_unlock` 及其等待/非等待返回语义。
- Go 1.25 `database/sql` package documentation: <https://pkg.go.dev/database/sql>。`DB.Conn(ctx)` 保留单条连接并保证该 `Conn` 上的查询属于同一数据库 session；`Conn.Close` 将其归还连接池，因此 session lock 必须在同一 Conn 上显式释放后再 Close；`DB.BeginTx` 的 context 取消会回滚事务。
- Go `context` package documentation: <https://pkg.go.dev/context>。`Context` 传递取消信号；`CancelFunc` 不等待工作结束，后台工作必须在检查点观察 `Done`/`Err`。

## Related Specs

- `.trellis/spec/backend/database-guidelines.md`：PostgreSQL 权威、短事务、批次保存、完整成功才 `missing`、迁移 expand/forward-fix 和集成测试要求。
- `.trellis/spec/backend/core0-runtime-contracts.md`：当前 scan 状态枚举、启动恢复、REST/错误合同和后续技术债。
- `.trellis/spec/backend/quality-guidelines.md`：扫描取消/部分失败/完整成功对账、并发 contention 和 race detector 的最低证据。
- `.trellis/spec/backend/error-handling.md`：取消是业务 scan 状态，不应当作为内部 500；后台操作不因 HTTP 客户端断开而自动放弃。
- `.trellis/spec/backend/logging-guidelines.md`：扫描生命周期事件、`scan_run_id` 关联和日志脱敏边界。
- `.trellis/tasks/09-02-persistent-scan-cancellation-coordination/prd.md`：本任务不排队、不自动接管，异常 run 人工恢复为 `incomplete`。

## Caveats / Not Found

- 仓库没有现成的 scanner coordination 抽象、heartbeat/reaper、跨进程测试夹具或
  `cancel_requested` 字段；上述接口和列名是研究建议，不是已批准的代码合同。
- PostgreSQL session lock 的“服务器最终检测到非正常断开”不等于有严格的应用级
  TTL；TCP/网络分区检测时间受部署参数影响。若验收要求确定上界，必须采用 lease
  （或另加 watchdog），并接受其时钟/误过期复杂度。
- `database/sql` 文档不保证某个 `*sql.DB` 后续查询复用同一 session；因此不能
  用 `db.ExecContext` 代替专用 `db.Conn` 来持有 session lock。
- 本研究未修改产品代码、迁移、任务主 artifacts 或规范文件；只新增本目录下的
  research 文档。
