# ⑪ 定时扫描 · 实施计划

依据：[prd.md](./prd.md)、[design.md](./design.md)。S 线第 5 项，在 ④ 合入 S 线分支之后开始（⑤ 不是前置，可与 ⑤ 并行开发，合入顺序按线内顺序）；子 agent 不提交 git。

## 通用约束（派活时原样写进 prompt）

- 测试只做必要的：只补改动直接影响的测试（触发规则纯函数、接口、发起方字段），跑下面的最小相关集；不跑全量，不做演练；不为不可能的时间边界写测试；调度失败等下一次，不加重试与回退。
- 前端文案：不写防御性说明，不显示时区、下次时间或解释；开关与下拉本身说清楚；错误提示一句日常话。
- 数据库测试只用隔离的 `roomusic-test` 项目；集成测试必须 PASS 而不是 SKIP。

## 后端（GPT-6-Astra，high）

1. 迁移 `backend/migrations/0042_scheduled_scan.sql` 与 `.down.sql`（design 3.4）。
2. `library/scan_schedule.go`：`PlanScheduledScan` 纯函数（design 3.1）；设置读写（`LoadScanSchedule`、`SaveScanSchedule`，值不变时不更新 `updated_at`）。
3. `library/scans.go`：`StartOptions.Trigger`，写 `scan_runs.trigger_kind`；`Scan`、`scanRow`、presenter 增加 `trigger`。
4. `library/scan_scheduler.go`：调度循环（最长 10 分钟醒一次、设置保存唤醒、`lastHandledSlot`、无 active 目录跳过、日志）；`app` 启动时以应用生命周期 context 启动，传入 `ROOMUSIC_TIMEZONE` 的 location，停机时随 context 退出。
5. `library/httpapi`：`GET/PUT /api/v1/scan-schedule`（design 3.2），注册路由。
6. 合同同步：`scan-coordination-contracts.md`、`core0-runtime-contracts.md`（design 3.6）。

2、3 可并行；4 依赖 2、3；5 依赖 2。

## 前端（Claude Opus 5.5，新开一个子 agent，做完即结束）

后端接口形状已定，可与后端并行。

1. `decoders.ts`：`ScanDTO.trigger`、`ScanScheduleDTO`；`endpoints.ts`：读取与保存。
2. `hooks/use-scan-schedule.ts`。
3. 扫描区段加“每天自动扫描”开关与整点下拉，改动即保存，失败恢复原值并可重试。
4. 扫描记录与详情：`trigger=scheduled` 显示“自动”标签。

## 测试（GPT-6.1-Sol，与实现分开的 agent）

- 单测 `PlanScheduledScan`：按 design 3.1 表格逐行（到点、在跑时跳过后当天不再补、启动补扫、已跑过不补、保存时间晚于整点不触发、关闭不触发、跨日、`Asia/Shanghai` 与 UTC 换算）。
- 集成（PostgreSQL + `httptest`）：GET 默认值 `{true,4}`；PUT 成功与各类 400；非管理员 403；值不变时 `updated_at` 不变；调度器以注入的时钟触发一次扫描，记录 `trigger=scheduled`、`mode=incremental`；已有扫描在跑时不新建。
- 前端（Vitest）：decoder；开关与下拉保存、失败回滚。
- API 样本：`scan-schedule` 读取样本；`ScanDTO.trigger` 引起的样本重生。

### 验证命令（最小相关集）

```bash
cd backend
go test ./internal/library/ -run 'PlanScheduledScan' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/library/... ./internal/app/ -run 'ScanSchedule|ScheduledScan' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/platform/postgres/ -run 'TestPostgreSQLMigrationRollbackRoundTrip' -count=1
go vet ./internal/library/...
cd ../frontend
npx vitest run src/features/library
npm run typecheck
```

## 风险文件与回滚点

- 风险文件：`app` 启动装配（新增 goroutine 的生命周期）、`library/scans.go`。
- 回滚点：迁移单独；后端 2–5 一组；前端独立。上线后可在界面关闭开关停用。

## 合入前核对

- [ ] ④ 已在 main（每天自动扫描依赖增量扫描）。
- [ ] 调度 goroutine 由应用生命周期 context 驱动，停机时退出，不使用 HTTP 请求 context。
- [ ] 定时扫描与手动扫描走同一 `StartScan` 与协调锁，在跑时跳过不排队。
- [ ] 设置界面截图（1440/375）由主会话复核；无多余说明。
