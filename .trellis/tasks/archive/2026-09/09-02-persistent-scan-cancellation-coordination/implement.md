# 实施计划：持久化扫描取消与跨进程协调

## 实施前门槛

- [ ] 项目负责人明确批准最新规划摘要；批准前不运行 `task.py start`，不修改产品代码。
- [ ] 运行 `task.py validate`，确认 `prd.md`、`design.md`、本文件及两个 JSONL
      manifest 均存在且引用路径有效。
- [ ] 进入实现阶段后再次读取 `trellis-before-dev` 要求和本任务三份规划文档，
      明确最小行为差距、所有权边界和不做事项。

## 有序实施步骤

### 1. 迁移与领域合同

- [ ] 新增 `backend/migrations/0009_scan_cancellation.sql`，添加
      `scan_runs.cancel_requested_at` 和活动查询索引；不修改已发布迁移。
- [ ] 在后端定义共享扫描状态/响应映射，保留既有五个状态和终态不可逆规则。
- [ ] 为迁移执行器补充版本 9 的发现、哈希、重跑和回滚验证（沿用现有迁移测试
      夹具，不新增外部迁移工具）。

### 2. 协调 holder 与恢复

- [ ] 在 `backend/cmd/roomusic` 增加窄的 scan coordination 实现：专用
      `*sql.Conn`、固定 scan advisory lock key、try-lock、显式 unlock/close 和
      生命周期清理。
- [ ] 将本进程 mutex 限定为 worker/holder 映射保护；跨进程判断只依赖 PostgreSQL。
- [ ] 在 `main.go`/应用生命周期中建立可取消的 worker 父 context，处理正常停机时的
      cancel、有限等待和数据库关闭顺序；HTTP 请求 context 不得成为后台扫描父级。
- [ ] 改造 `database.go` 的 `recoverInterruptedScans`：取得 lock 后才在短事务中
      收敛遗留 `running`；未取得 lock 时跳过更新并记录事件；移除启动时无条件全表更新。
- [ ] 在 `POST /api/v1/scans` 的锁内完成恢复、活动行判定和新 run 创建；锁竞争时
      有界重试读取活动行，找不到时返回稳定的协调不可用错误，不创建第二行。
- [ ] 检查/设置连接池边界，确保 holder 不会让状态与取消请求死锁。

### 3. 持久化取消 REST

- [ ] 在 `scans.go` 注册 `POST /api/v1/scans/{id}/cancel` 和
      `GET /api/v1/scans/active`，沿用同源、session、管理员权限和 request ID 合同。
- [ ] 取消事务使用 `SELECT ... FOR UPDATE` 与 `COALESCE(cancel_requested_at, NOW())`；
      明确首次、重复、终态、未知 ID、普通用户和匿名请求的状态码/错误码。
- [ ] 修正扫描状态查询，使 `sql.ErrNoRows` 才映射 404，其他数据库错误返回 503；
      所有响应使用安全 envelope，不泄露 SQL、绝对路径或内部错误。
- [ ] 为启动、取消、活动查询增加 PostgreSQL/HTTP 集成测试和幂等断言。

### 4. 可取消执行器与终态事务

- [ ] 将 `runScan` 的 `context.Background()` 替换为由应用生命周期和取消轮询驱动
      的 context；轮询间隔可注入，数据库/holder 故障按协调失败收敛为 incomplete。
- [ ] 在 `WalkDir`、CUE/解析循环、观察/诊断批次和封面关联边界加入取消检查；保证
      取消后不开始新的文件，已提交合法观察可以保留。
- [ ] 让扫描读取、观察、诊断、封面关联和终态 SQL 使用 holder executor，避免锁连接
      丢失后通过池中另一 session 继续 stale write；普通 HTTP 查询仍使用 `*sql.DB`。
- [ ] 新增统一 `finalizeScan`：锁定 run 行，按取消/失败/不完整/成功顺序判定；成功
      时在同一短事务执行所有 root 的 `missing` 对账和 `succeeded` 更新，任一步失败
      整体回滚。
- [ ] 统一处理诊断和扫描 SQL 错误，区分可记录的单文件诊断与必须终止的协调/数据库
      故障；记录终态事件但不把日志当作业务状态。

### 5. 前端状态与管理入口

- [ ] 在 `frontend/src/api.ts` 增加严格的扫描 DTO 字段、活动响应和取消响应 decoder；
      未知状态、无效时间和 malformed error 继续安全失败。
- [ ] 在 `frontend/src/main.tsx` 管理员加载活动扫描、轮询服务端状态、调用取消端点，
      展示 `cancel_requested_at` 驱动的“取消请求中”和 `canceled/incomplete/failed`
      终态；失败保留重试入口，不做乐观终态更新。
- [ ] 使用现有 CSS 和语义按钮/live region 完成键盘、窄屏和权限可见性检查，不引入
      router、query/cache 或新的 UI 库；普通用户不渲染管理操作。
- [ ] 扩展 `frontend/src/api.test.ts`，必要时增加不依赖新库的状态投影测试。

### 6. 集成回归与文档

- [ ] 增加后端并发/恢复测试：双实例 try-lock、活动 ID 复用、锁释放、恢复跳过/收敛、
      holder 断开、取消检查点、终态竞争、missing 原子性和连接池边界。
- [ ] 更新 `.trellis/spec/backend/core0-runtime-contracts.md`（及确有新约束时的
      相关规范）记录取消字段、lock-aware 恢复、session lock 无 TTL 和旧版本发布限制。
- [ ] 完成跨层搜索：后端 presenter、前端 decoder、状态文案、测试和文档都覆盖新增
      endpoint/字段/枚举语义；确认没有残留旧的无条件恢复或逐 root 提交路径。

## 验证命令

按改动取最小集合，至少执行：

```bash
gofmt -w backend/cmd/roomusic/*.go
go test ./backend/cmd/roomusic -count=1
go vet ./backend/...
go build ./backend/...

npm --prefix frontend run lint
npm --prefix frontend run typecheck
npm --prefix frontend run test -- --run
npm --prefix frontend run build

git diff --check
```

配置 `ROOMUSIC_TEST_DATABASE_URL` 后再执行 PostgreSQL 证据：

```bash
./scripts/test-integration.sh
go test ./backend/cmd/roomusic -run 'Scan|Migration' -count=1
go test -race ./backend/cmd/roomusic -count=1
```

若扫描协调改动触及全局 Go 包，再补 `go test ./... -count=1`、`go vet ./...` 和
`go build ./...`。未配置测试数据库时，必须在结果中明确记录集成测试被跳过，不能把
跳过当作 PostgreSQL 事务证据。

## 风险文件与回滚点

| 文件/区域 | 风险 | 回滚或保护点 |
| --- | --- | --- |
| `backend/migrations/0009_scan_cancellation.sql` | 版本/校验和漂移、旧库升级失败 | 只做 additive migration；备份后用 forward-fix，不删除列 |
| `database.go` | 恢复误伤健康 worker、启动不 ready | 先用双连接恢复测试；锁竞争路径必须只读并可观测 |
| 新协调模块、`application.go`、`scans.go` | holder 泄漏、重复 run、连接池死锁 | `defer` unlock/close；双实例和 `MaxOpenConns` 测试；失败不插入新行 |
| `scanner.go` | 取消遗漏、stale write、部分 missing | 所有写点走 holder；终态与对账单事务；失败保持 running 等恢复 |
| `frontend/src/api.ts`、`main.tsx` | decoder/状态漂移、乐观误报成功 | 先扩展 decoder 测试；状态只由服务端轮询驱动 |
| 规范/发布文档 | 新旧二进制混跑破坏锁合同 | 发布前停止旧执行器；不要在滚动升级中让两套 scanner 同时运行 |

## 完成前检查

- [ ] `prd.md` 的每条 R/AC 都能在代码或测试中找到对应证据。
- [ ] 只有 `succeeded` 能触发 `missing`，并有崩溃注入或事务回滚证据。
- [ ] 取消端点不依赖客户端连接存活，终态竞争结果可解释且幂等。
- [ ] 日志包含 `scan_run_id`/request 关联但不含 token、SQL、完整路径或音频内容。
- [ ] Redis、队列、播放、Agent 和源文件写入没有成为隐式依赖。
- [ ] 通过 `trellis-check` 后再进行规范更新和提交；未获实现批准前保持任务
      `planning` 状态。
