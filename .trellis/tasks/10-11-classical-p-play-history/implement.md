# P 服务端播放历史执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。

## 约束

- 只使用 `roomusic-test` Compose 项目（`--env-file /dev/null`、Compose 文件用绝对路径、空闲端口、随机库名，结束 `down -v`）；
  不碰用户的 `roomusic` 项目与其他容器；停进程按确切 PID，不用 `pkill`。
- 不读取真实 `music/`：本任务不改扫描与解析，自动化只用合成素材。
- 每步单独提交到 `main`，不推送；提交信息用中文，结尾加协作者行。改动前端源码的提交要重新构建并一起提交 `backend/cmd/roomusic/web`。
- 提交前把暂存区导出到临时目录（`git checkout-index -a --prefix=<临时目录>/`），在完整文件树上构建与测试，通过后再提交；不用 `--amend`；
  `git diff --cached --check` 失败时不接着提交。
- 公开仓库：提交前确认不含 `backend/data/`、`.env*`（`.env.example` 除外）、真实音乐或其派生物。
- 规范文件不超过 24,000 字节。

## 启动前

- [x] 用户批准规划（P2–P6 与本计划），批准记录写进 PRD；提交规划文档。
- [x] `python3 ./.trellis/scripts/task.py start 10-11-classical-p-play-history`。

## 步骤

### 第 1 步：数据表与记录规则（R1、R2 的服务端部分、R4）

- [x] 迁移 `0016_play_history.sql`（design §2）。
- [x] catalog：`LoadTrackDuration`、`LoadTrackPlacements`（design §5），集成测试覆盖 present-only、批量、空输入、封面校验。
- [x] `internal/listening`：门槛计算、`Record`（含幂等与冲突）、`RecentReleases`（分批去重）、`Clear`；在 `dependency_rules_test.go` 声明依赖。
  单元测试覆盖门槛边界；集成测试覆盖重放、并发同键只写一行、门槛、缺失曲目、用户隔离、最近播放的倒序/去重/上限/分批/隐藏缺失、
  `has_history`、曲目换发行后的显示、表约束。改名与同 root 移动后重扫仍指向同一曲目的测试需要扫描管线，放在第 2 步的 `internal/app` 集成测试里。
- [x] 验证：

  ```bash
  cd backend && test -z "$(gofmt -l .)" && go vet ./... && go build ./...
  go test -race ./internal/listening/... ./internal/catalog/... ./internal/app/... -count=1
  # 隔离 PostgreSQL 起来后（ROOMUSIC_TEST_DATABASE_URL 指向它）
  go test -race ./internal/listening/... ./internal/catalog/... ./internal/platform/postgres/... -run 'TestPostgreSQL' -count=1
  ```

- [x] 提交：`feat(listening): 播放历史的数据表与记录规则`。

### 第 2 步：接口、样本与前端解码（R1、R3）

- [x] `internal/listening/httpapi`：三个路由（design §4）；`app` 装配；依赖规则。
- [x] 集成测试 `internal/app/listening_api_integration_test.go`：201 与重放、409、400（缺键、非 UUID、数值、`play_too_short`）、404、401、跨源 403、
  用户 A/B 与管理员互相读不到、清不掉；停用成员的记录保留、重新启用后本人可见；合成 FLAC 经扫描入库后改名、同 root 移动并重扫，
  记录仍指向同一曲目、最近播放仍显示同一专辑，文件消失（完整扫描标记缺失）后记录不显示但仍在库中。
- [x] 接口样本：记录、最近播放（有数据与空）、清除、`play_too_short`；`ROOMUSIC_UPDATE_API_SAMPLES=1` 重写后逐个核对差异只有新增文件。
- [x] 前端：`shared/api/request.ts` 支持 `DELETE`；`features/listening/api` 的端点与解码器（发行摘要复用 catalog 公开的解码器）；
  契约测试登记新样本；解码器的样本与反例测试。重新构建内嵌产物（无变化也要核对）。
- [x] 验证：第 1 步的命令，另加

  ```bash
  ROOMUSIC_TEST_FFMPEG=<固定 ffmpeg 路径> go test -race ./internal/app/... -run 'TestPostgreSQL' -count=1   # 含 TestPostgreSQLAPIResponseSamples
  cd frontend && npm run lint && npm run typecheck
  npx vitest run src/test/api-contract.test.ts src/features/listening src/shared/api
  npm run build   # 内嵌产物与暂存区一致
  ```

- [x] 提交：`feat(listening): 播放历史接口`（含前端解码与内嵌产物）。

### 第 3 步：播放器计次与上报（R1、R2、R5）

- [x] `features/player/engine/listen-tracker.ts` 与单元测试（design §3 的每条规则）。
- [x] `PlayerEngine`：`playRecorder` 依赖（`record`、`discard`）、`seeking` 监听、访问的开始与继续、`endSession` 作废；引擎测试覆盖只调用一次、拖动不算、
  暂停后继续、重试继续、换曲与单曲循环重新计、“上一首”回到曲首重新计、会话结束作废。
- [x] `features/listening` 的上报器与测试（重试节奏、4xx 放弃、`dispose` 之后不再发送、成功后失效“最近播放”查询）；
  `PlayerProvider` 接线，`endSession` 经 `playRecorder.discard` 调用 `dispose`；默认装配的 provider 测试覆盖上报与会话结束后不再重试。
- [x] 验证：`npm run lint && npm run typecheck`，`npx vitest run src/features/player src/features/listening`，
  再跑一次 `npm test`（引擎依赖变化会影响所有构造引擎的测试与替身，局部集合覆盖不全）；重新构建内嵌产物。
- [x] 提交：`feat(player): 达到规则的播放上报到服务端`（含内嵌产物）。

### 第 4 步：概览“最近播放”（P1）

- [x] `catalog/index.ts` 导出 `Cover`、`albumArtistOf`、`releaseSummaryDecoder` 与 `ReleaseSummaryDTO`。
- [x] `RecentPlaysPanel`（加载、空态、错误、卡片、清除确认与失败）；概览页组合并更新页面说明。
- [x] 测试替身：Vitest 的 `fake-api`、Playwright 的 `api-mock` 增加三个路由（响应取自样本）；概览相关页面测试与 Playwright 用例
  （五种宽度不溢出、axe、键盘完成清除与确认）。
- [x] 验证：`npm run lint && npm run typecheck && npm test`；
  `LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu npm run test:e2e`（概览出现在多数页面级用例的起点，需要全量）；
  重新构建内嵌产物。
- [ ] 提交：`feat(listening): 概览显示最近播放`（含内嵌产物）。

### 第 5 步：真实服务覆盖

- [x] `frontend/e2e/real/playback.real.ts`：合成 FLAC 真实播放过半后接口有记录、概览显示该专辑、清除后为空；只拖动到结尾附近不产生记录。
- [x] 验证：`./scripts/playback-e2e.sh` 全部通过（运行前确认没有残留的 `roomusic-test` 容器；我自己的隔离数据库先 `db.sh down`）：23 个用例通过，含新增 2 个。
- [x] 提交：`test(listening): 真实服务覆盖播放历史`。

### 第 6 步：文档与布局确认（P6）

- [ ] 新增 `.trellis/spec/backend/listening-contracts.md`；更新运行合同（路由与专题文件表）、`catalog-rest-contracts.md`（两个新公开函数的一行指引）、
  `directory-structure.md`（后端与前端各加 `listening` 的归属）、`player-design-guidelines.md`（计一次播放）、`state-management.md`（上报器与查询键，
  保持在 24,000 字节以内）、`frontend/DESIGN.md` §7 概览行、`product-goals.md`、路线图与父任务；
  在 `10-06-m2a-acceptance-followup/research/manual-acceptance.md` 的交互检查里加“最近播放”一行。
- [ ] 用合成数据截取概览的宽屏与窄屏（有记录、空态、清除确认），写到 `/tmp`，不入库；请用户确认布局（P6）。
- [ ] 验证：`git diff --cached --check`、规范文件大小、相对链接、`python3 ./.trellis/scripts/task.py validate`。
- [ ] 提交：`docs(listening): 同步播放历史合同与规范`。

### 第 7 步：收尾

- [ ] PRD 写实施结果与验收对照；设计与实现有差异时记在 design.md。
- [ ] 归档（`--skip-branch-validation`），单独提交归档后的相对链接修正；写 journal；更新记忆。

## 风险与回退点

- 第 1 步只加表和未被使用的读取函数，可单独回退。
- 第 2 步加接口，前端还没有调用者；第 3 步开始上报，界面不变；第 4 步才有界面。任何一步都可以按提交回退，表留着不影响其他功能。
- 布局（P6）需要调整时只改区块样式与组件，不影响接口与上报。
