# ⑪ 定时扫描 · 技术设计

需求见 [prd.md](./prd.md)。S 线第 5 项，以 [④](../11-08-scan-incremental/design.md)（增量扫描，`StartScan(ctx, StartOptions)`）为前提；与 [⑫](../11-08-root-path-change/design.md) 的约定：改路径事务短暂持有扫描锁时，定时触发按“本次跳过”处理。参考设计行号指 [reference-design.md](../11-08-scan-reference-port/research/reference-design.md)。

## 1. 边界与归属

| 部分 | 归属 |
| --- | --- |
| 设置存储（开关、整点） | `library`（表 `scan_schedule`，单行） |
| 下一次触发时间的纯函数 | `library/scan_schedule.go`：`PlanScheduledScan` |
| 调度 goroutine | `library/scan_scheduler.go`，由 `app` 在启动时以应用生命周期 context 启动（与备份调度相同的装配方式） |
| 设置接口 | `library/httpapi`：`GET/PUT /api/v1/scan-schedule` |
| 发起方记录 | `scan_runs.trigger_kind`，`ScanDTO.trigger` |
| 设置界面、记录里的“自动” | 前端 `features/library` |

不改：扫描协调锁、扫描流程、分析 worker 的让路规则（`audio-analysis-contracts.md` 第 38 行：每首之前查扫描是否在运行，在运行就等待）。

## 2. 数据流

```
app 启动 → library.Service.RunScanScheduler(ctx, location)   location = ROOMUSIC_TIMEZONE（默认 Asia/Shanghai）
  循环：
    读 scan_schedule（enabled, hour, updated_at）与 MAX(scan_runs.started_at)
    plan = PlanScheduledScan(now, location, settings, lastStartedAt, lastHandledSlot)
    plan.Due：
       没有 active 目录 → 记为已处理，不发起
       StartScan(ctx, {Mode: incremental, Trigger: scheduled})
         Started → 已处理（日志 library.scan.scheduled_started）
         已有扫描在跑 / 协调不可用 → 已处理，本次跳过（日志 library.scan.scheduled_skipped，reason）
    等待 min(plan.Next - now, 10 分钟)，或被设置保存唤醒
PUT /api/v1/scan-schedule → 写表 → 唤醒本进程调度器（其他进程最迟 10 分钟后读到）
```

## 3. 接口合同

### 3.1 触发规则 `PlanScheduledScan`

输入：`now`、`location`、`enabled`、`hour`（0–23）、`settingsUpdatedAt`、`lastStartedAt`（任何发起方的最近一次扫描开始时间，没有为零值）、`lastHandledSlot`（本进程内存，启动时为零值）。

- `slot` = 在 `location` 里不晚于 `now` 的最近一个 `hour:00`（今天或昨天）。
- **到点**（`Due=true`）当且仅当：`enabled`，且 `slot ≥ settingsUpdatedAt`，且 `lastStartedAt < slot`，且 `slot ≠ lastHandledSlot`。
- `Next` = 严格晚于 `now` 的下一个 `hour:00`。
- 时区换算用 `time.Date(…, location)`，夏令时缺失或重复的钟点由 Go 规范化处理（默认时区没有夏令时）。

由此得到的行为：

| 场景 | 结果 |
| --- | --- |
| 每天 4 点，进程一直在跑 | 4:00 触发一次 |
| 4 点时有手动扫描在跑 | 本次跳过，不排队；当天不再补（`lastHandledSlot` 已记） |
| 4 点前刚手动扫过（开始时间早于 4:00） | 4:00 照常触发（增量扫描，成本低） |
| 进程在 4 点时没运行，9 点启动 | 启动后立即补一次（当天 4:00 之后没有任何扫描开始过） |
| 9 点重启，而 4 点那次已经跑过 | 不补 |
| 15 点把开关打开、时间仍是 4 点 | 当天不触发（`slot` 早于保存时间），明天 4:00 触发 |
| 3:59 把时间改成 4 点 | 4:00 触发 |
| 关闭 | 不触发 |
| 两个进程同时到点 | 一个取得扫描锁发起，另一个得到“已有扫描”而跳过；若前者极快结束、后者随后才检查，`lastStartedAt ≥ slot` 也会让它跳过 |

“进程没运行时启动后补一次”是本设计选的行为（纯技术选择，理由：NAS 夜间休眠或重启时，新放进去的专辑不会因此晚一整天出现；补的是一次增量扫描，代价小）。

### 3.2 设置接口

`GET /api/v1/scan-schedule`：管理员。

```json
{ "enabled": true, "hour": 4 }
```

`PUT /api/v1/scan-schedule`：同源、管理员；整份替换，天然可重复，不要求 `Idempotency-Key`、不带 revision（与备份保留规则 `PUT /api/v1/system/backups/retention` 一致）。

```json
// 请求与 200 响应同形
{ "enabled": false, "hour": 5 }
```

| 条件 | 响应 |
| --- | --- |
| 未登录 / 非管理员 / 来源不符 | 401 / 403 / 来源拒绝 |
| 请求体不是对象、缺字段、多余字段、`enabled` 不是布尔、`hour` 不是 0–23 的整数 | `400 invalid_input`（“定时扫描设置无效”） |
| 数据库失败 | `503 database_unavailable` |

保存时更新 `updated_at=NOW()`（只在值实际变化时更新，避免“只点了保存”把当天的补扫取消掉）。保存后唤醒本进程调度器。

### 3.3 发起方

- `scan_runs.trigger_kind TEXT NOT NULL DEFAULT 'manual' CHECK (trigger_kind IN ('manual','scheduled'))`。列名避开 SQL 关键字 `trigger`。
- `ScanDTO` 增加 `trigger: "manual" | "scheduled"`（所有入口、所有角色）。迁移前的历史扫描为 `manual`。
- `StartOptions` 增加 `Trigger`；HTTP 入口固定 `manual`，调度器传 `scheduled`；定时扫描固定 `Mode: incremental`。
- ⑫ 改路径后前端发起的扫描是 `manual`。

### 3.4 迁移（`0037_scheduled_scan.sql` + `.down.sql`，合入时重新编号）

```sql
CREATE TABLE scan_schedule (
    id         SMALLINT    PRIMARY KEY CHECK (id = 1),
    enabled    BOOLEAN     NOT NULL,
    hour       SMALLINT    NOT NULL CHECK (hour BETWEEN 0 AND 23),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO scan_schedule(id, enabled, hour) VALUES (1, TRUE, 4);
ALTER TABLE scan_runs ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'manual'
    CHECK (trigger_kind IN ('manual','scheduled'));
```

默认开启、每天 4 点（用户裁决），与备份默认 3 点错开。下行迁移删表与列。

存储方式选数据库而不是像备份保留规则那样放文件：备份规则放在备份目录，是因为恢复数据库时规则不能跟着被覆盖；扫描设置属于扫描数据本身，随数据库备份恢复是正确的。

### 3.5 日志

- `library.scan.scheduled_started`（INFO）：`scan_run_id`、`slot`。
- `library.scan.scheduled_skipped`（INFO）：`slot`、`reason`（`scan_running` / `coordination_unavailable` / `no_active_roots`）。
- 读设置或扫描时间失败（WARN `library.scan.schedule_read_failed`）：等 10 分钟再试。

### 3.6 合同同步（R5）

- `scan-coordination-contracts.md`：新增“定时扫描”一节（触发规则表、与手动扫描同一入口与锁、在跑时跳过不排队、发起方字段、设置接口与错误矩阵）；`ScanDTO` 字段清单补 `trigger`。
- `core0-runtime-contracts.md`：`ROOMUSIC_TIMEZONE` 的用途补“定时扫描”。
- `error-handling.md`：无新错误码。

## 4. 前端

- “扫描目录”设置页的扫描区段加一行：开关“每天自动扫描”，开启时旁边一个整点下拉（`0:00`–`23:00`），改动即保存（PUT），失败时恢复原值并显示一句错误与“重试”。不显示时区、下次时间或任何说明。
- 扫描记录与扫描详情：`trigger=scheduled` 时显示标签“自动”。
- `decoders.ts`：`ScanDTO.trigger` 必需、只接受两个值；新增 `ScanScheduleDTO` 严格校验。
- 新 hook `use-scan-schedule.ts`（读取与保存，保存成功后更新缓存）。

## 5. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 实时文件事件：四类关注、上推到目录粒度、防抖 2 秒、同路径合并（4.3，165–199；26.6，5715–5783） | 不采用 | NAS/网络挂载上事件不可靠、监视句柄有上限（16.1，1873–1883 自己也承认会耗尽）；用户裁决改为每天定时扫描 |
| 挂载事件与 5 秒可用性轮询（4.4，201–204；26.6，5745–5765） | 不采用 | 目录不可用由每次扫描的 `root_unavailable` 与 ① 的空根保护处理 |
| 强制重扫不重置监视、避免监视中断窗口（4.2，161–163） | 不适用 | 没有监视 |
| 触发路径彼此独立、不互斥（4，153–155） | 改写 | 手动与定时共用同一入口与协调锁，同一时刻只有一个扫描；定时遇到在跑的扫描直接跳过 |
| 失败重试指数退避（7.2，630–644） | 改写 | 定时触发失败不重试，等下一天（或启动时补扫）；读设置失败 10 分钟后再试 |
| 低频后台定时器（7.3，646–653） | 照搬思路 | 调度器最长 10 分钟醒一次，与备份调度一致（`backup/scheduler.go`） |

## 6. 兼容与已有数据

- 迁移后默认开启：升级后的第一个 4 点（或启动时若当天 4 点已过、且 4 点后没有扫描、且 4 点晚于迁移时间——迁移写入的 `updated_at` 是迁移时刻，所以升级当天不会立刻补扫）开始自动扫描。
- 历史扫描 `trigger=manual`。
- 多实例部署时每个进程都有调度器，靠扫描锁与 `lastStartedAt` 去重。

## 7. 风险与回滚

| 风险 | 应对 |
| --- | --- |
| 每天自动扫描在 ④ 之前合入会很慢 | 本项排在 ④ 之后（PRD 依赖）；合入核对 ④ 已在 main |
| 扫描在 4 点与分析 worker、备份竞争 IO | 分析 worker 已按规则让路；备份在 3 点，扫描在 4 点 |
| 时区配置错误导致在白天扫描 | `ROOMUSIC_TIMEZONE` 启动时已校验（备份共用）；界面可改时间 |
| 启动补扫在频繁重启时重复 | 只在“当天的整点之后没有任何扫描开始过”时补，重启多次也最多补一次 |

回滚：在界面关闭开关即停止；代码回滚后表与列无人读写；`0037` 下行迁移删除。

## 8. 线间关系

- 依赖 ④：`StartScan(ctx, StartOptions)` 已有 `Mode`，本项加 `Trigger`。
- 与 ⑫：改路径事务持锁的瞬间若恰逢触发，`StartScan` 返回协调不可用，本次按跳过处理。
- 共享文件：`library/scans.go`（`StartScan`）、`library/httpapi/{handlers,scans}.go`、`app` 的启动装配、前端扫描区段与扫描记录（与 ④ 的“完整重扫”按钮、标签同一区域，顺序开发无冲突）。
