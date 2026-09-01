# Core 0 实现偏差修复执行计划

## 前置检查

- [x] 复核任务 PRD、设计和当前工作树；保留用户已有 `.env.dev` 改动。
- [x] 读取 shared、backend、frontend 相关规范及播放器设计规范。
- [x] 确认 `ROOMUSIC_TEST_DATABASE_URL` 是否可用，并记录集成测试是否跳过。
- [x] 仅在本计划获用户批准后运行 `task.py start`。

## 实施顺序

1. **启动脚本修复**
   - 修改 `scripts/prod.sh` 的 Go module 工作目录。
   - 增加可重复的 shell smoke 检查，覆盖根目录调用、缺配置和安全 Cookie
     前置条件；不启动或修改生产数据。

2. **前端 API 合同收紧**
   - 在 `frontend/src/api.ts` 增加角色、用户创建/状态和 root label decoder，
     删除 `main.tsx` 中 raw cast/identity decoder。
   - 让 root DTO、后端 root presenter 和 UI 显示字段一致；补充未知枚举、缺失
     字段和错误 envelope 测试。

3. **用户启停事务**
   - 在 `backend/cmd/roomusic/application.go` 按设计的锁顺序实现事务、错误映射、
     `not_found` 和最后管理员保护。
   - 对 rows/Exec/Commit 错误显式处理；必要时提取仅服务该用例的窄辅助函数。
   - 增加 PostgreSQL 集成断言：禁用同时撤销 session、回滚不留半状态、并发/最后
     管理员拒绝和未知用户响应。

4. **扫描结果与多碟**
   - 修改 scanner 的诊断/complete 标记，使 unsupported、CUE 和遍历问题阻止
     `missing` 对账但不阻塞其他合法文件。
   - 按 disc number 查找或创建 Medium，增加小型 fixture/集成测试验证多个 Medium
     与同路径 Track ID 稳定性。
   - 不添加取消 API；在规范和测试中明确当前状态能力边界。

5. **目录 presenter 与界面细节**
   - 使重复注册停用 root 返回实际 status/revision，并同步 decoder/UI。
   - 补播放器 focus-visible、aria 状态、长文本名称、断点和字距的最小 CSS/JS
     修复；不引入库或真实音频能力。

6. **长期规范与根文档**
   - 更新 frontend/backend 各 index、目录、质量、hook、状态、类型、数据库、
     错误、日志和 core tooling 文档的“当前状态”段。
   - 新增或合并环境/构建/API 运行合同，区分当前扁平过渡结构与目标架构。
   - 修复所有失效 Core 0 任务链接，更新 README 与脚本实际行为；清理重复的
     `.cursor/rules/trellis-testing.mdc` 段落和不存在的 `.gitattributes` spec 路径。
   - 将迁移执行器追踪/锁、持久取消、per-root scan 状态、完整 HTTP 日志、用户
     Operation Journal、前端 feature 拆分等列入明确 deferred 技术债。

7. **回归与审查**
   - 先运行局部测试，失败时在任务内修复并重跑；不以未配置 PostgreSQL 的跳过
     结果替代集成证据。
   - 执行 `trellis validate`、`git diff --check`、相关 Go/前端/shell 检查。
   - 由 `trellis-check` 做跨层审查，确认规范与实现没有再次漂移。

## 验证命令

最小验证集：

```bash
bash -n scripts/prod.sh scripts/dev.sh scripts/dev-reset.sh
cd frontend && npm run lint && npm run typecheck && npm run test
cd ../backend && gofmt -l . && go test ./... -count=1
```

按改动需要追加：

```bash
cd backend && go vet ./... && go build ./...
cd .. && docker compose config --quiet
python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-01-core0-correctness-and-spec-refresh
git diff --check
```

若设置了 `ROOMUSIC_TEST_DATABASE_URL`，另运行 PostgreSQL 集成测试并记录实际
连接；否则在最终报告中明确说明测试被跳过的原因。生产脚本 smoke 检查使用
临时环境文件和 `ROOMUSIC_SKIP_BUILD=1`，超时退出只用于确认 module 解析，不
保留后台进程。

## 风险与回退点

- **用户锁顺序**：若 PostgreSQL contention 测试出现死锁，先保留事务错误处理，
  再调整锁定 SQL；不要退回忽略错误的旧实现。
- **扫描 complete 标记**：unsupported 是否导致 `incomplete` 是数据安全门槛；
  不能为了让状态显示 succeeded 而重新允许 missing 对账。
- **多碟归属**：仅在已有 release 内按 disc number 建 Medium，不扩大目录分组
  规则；若 fixture 要求跨目录归并，记录后续产品决策。
- **wire 兼容**：不改变顶层错误 envelope、`canceled` 拼写或已有端点路径。
- **规范漂移**：文档修改必须与代码/测试同一提交，不能单独声明未来目录已经
  存在。

## 提交与发布门槛

- [x] `task.py validate` 通过，PRD 已完成收敛且无未决问题。
- [x] 代码、测试、规范和任务记录经过 `trellis-check` 审查。
- [x] 仅提交本任务相关文件；用户已有 `.env.dev` 改动不纳入提交。
- [ ] 使用清晰的中文文档和 conventional commit message 提交。
- [ ] 提交验证通过后执行用户明确要求的 `git push`，报告远端分支和结果。

## 验证记录

- `cd frontend && npm run lint && npm run typecheck && npm run test && npm run build`：通过，6 项测试通过。
- `cd backend && gofmt -l . && go test ./... -count=1 && go vet ./... && go build ./...`：通过；未设置 `ROOMUSIC_TEST_DATABASE_URL`，PostgreSQL 集成测试按约定跳过。
- `bash -n scripts/prod.sh scripts/dev.sh scripts/dev-reset.sh`：通过。
- `docker compose config --quiet`：加载 `.env.dev` 后通过；无环境变量时按约定拒绝缺失配置。
- `ROOMUSIC_SKIP_BUILD=1 timeout 2 ./scripts/prod.sh`：从仓库根目录可解析 Go module 并启动监听；测试使用无效临时数据库，超时退出且无残留进程。
- `python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-01-core0-correctness-and-spec-refresh`、`git diff --check`：通过。
