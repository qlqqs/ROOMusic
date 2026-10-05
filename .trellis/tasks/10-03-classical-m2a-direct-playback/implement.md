# M2a 执行计划

按 [design.md](design.md) 实施，验收编号见 [prd.md](prd.md)。每步单独提交到 `main`，不推送；提交信息用中文，
结尾加协作者行。每个改动前端源码的提交都要重新构建并一起提交 `backend/cmd/roomusic/web`。

## 启动前

- 用户批准最终规划摘要后才运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/10-03-classical-m2a-direct-playback`。
- 按 `trellis-before-dev` 读取 `implement.jsonl` 列出的规范。
- 本机 Playwright 需要 `export LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu`（`/tmp` 重启会丢，重做方法见前端质量规范）。
- 只使用 `roomusic-test` Compose 项目；不碰用户的 `roomusic` 项目与其他容器；停进程按确切 PID，不用 `pkill -x roomusic`。
- 真实 `music/` 只读；自动化只用合成素材。

## 步骤

### 第 1 步：后端来源核对与安全打开（A3、A4、A5 后端部分、A13 依赖规则）

- [x] `catalog.Store.LoadTrackSource` 与 `ErrTrackNotFound`；`library.LoadRootLocation`。
- [x] `backend/internal/playback`：格式表、容器识别、版本比对、`os.Root` 加 `O_NONBLOCK` 打开、ETag、Range 解析、`recheckingReader`、`StillPlayable`。
- [x] 单元测试与 `TestPostgreSQL…` 集成测试（design §7 后端第 1、2 条）；依赖规则加入 `playback`。
- 验证：`cd backend && test -z "$(gofmt -l .)" && go vet ./... && go build ./...`；
  `go test -race ./internal/playback/... ./internal/catalog/... ./internal/library/... ./internal/app/... -count=1`；`./scripts/test-integration.sh`。
- 提交：`feat(playback): 曲目音频直出的来源核对与安全打开`。

### 第 2 步：后端接口（A1、A2、A6、A13）

- [x] `backend/internal/playback/httpapi`：`GET /api/v1/playback/formats`、`GET /api/v1/tracks/{id}/playback`、`GET|HEAD /api/v1/tracks/{id}/audio`；错误映射；
  复核接线（`Guard.Authenticate` 加 `StillPlayable`，应用生命周期上下文）；`playback.stream.aborted` 日志。
- [x] `app.New` 装配（传入允许根、生命周期上下文与日志）。
- [x] 接口样本 8 个与样本测试；HTTP 集成测试（含传输中撤销会话、停用目录、关停）。
- 验证：同第 1 步，加 `./scripts/test-integration.sh` 确认样本无漂移。
- 提交：`feat(playback): 新增格式表、播放预检与音频直出接口`。

### 第 3 步：测试素材（A14 的前置）

- [x] `scripts/gen-playback-fixtures.sh`：用固定摘要的 FFmpeg 容器（`docker run --rm --network none`，只挂载输出目录，不挂载 `music/`）生成 design §7 列出的素材到 `backend/testdata/playback/`（合计约 0.7 MB，带 `README.md`）。
- [x] 解析器测试逐个解析素材，核对编码、位深、采样率与声道；`playback` 测试核对素材与格式表、容器识别一致。发现 24 bit WAV 被 FFmpeg 写成 `wav_extensible`，不在格式表中（见 prd 风险）。
- 验证：`bash -n scripts/gen-playback-fixtures.sh`；`go test ./internal/library/parser/ ./internal/playback/ -run Fixture -count=1`；`du -sb backend/testdata/playback`；脚本重复运行字节相同。
- 提交：`test(playback): 合成音频测试素材与生成脚本`。

### 第 4 步：前端播放引擎（A7–A10、A12、A13 前端部分）

- [ ] `frontend/src/features/player/api`：端点与 decoder（读取接口样本）。
- [ ] `engine/queue.ts`（纯函数）、`capability.ts`、`persistence.ts`、`media-session.ts`、`player-engine.ts`；`PlayerProvider` 与 `usePlayer`。
- [ ] Vitest：design §7 前端第 1 条中与引擎相关的全部用例。
- 验证：`cd frontend && npm run lint && npm run typecheck && npx vitest run src/features/player`。
- 提交：`feat(player): 播放引擎、队列与能力判定`（只含引擎，界面尚未接入，不需要重建内嵌产物）。

### 第 5 步：前端界面（A5 界面部分、A9、A11）

- [ ] `shared/ui/menu-button.tsx`；外壳播放栏区域与窄屏留白；导航分组与 `/now-playing` 路由。
- [ ] 播放栏、正在播放页（信号路径、队列）、专辑页插槽与动作、不可播放原因；停用目录的确认框与结果提示文案。
- [ ] Vitest 与 Playwright（路由模拟）：五种宽度、axe、键盘路径、长标题。
- 验证（外壳变化影响所有页面，所以跑前端全量）：`npm run lint && npm run typecheck && npm test && npm run build`；
  内嵌产物漂移检查 `git diff --exit-code -- backend/cmd/roomusic/web` 在提交后为空；`npm run test:e2e`。
- 提交：`feat(player): 播放栏、正在播放页与专辑页播放入口`（含重建的内嵌产物）。

### 第 6 步：真实服务自动化与 CI（A14）

- [ ] `scripts/playback-e2e.sh` 与 `frontend/playwright.real.config.ts`、对应用例（design §7“真实服务自动化”）。
- [ ] `.github/workflows/ci.yml` 新增作业：Go、Node、Playwright Chromium、Docker PostgreSQL，运行该脚本。
- 验证：本机运行 `./scripts/playback-e2e.sh` 通过并确认 `roomusic-test` 容器已清理；`bash -n scripts/*.sh`；`docker compose -f compose.test.yaml config --quiet`。
- 提交：`test(playback): 真实服务播放自动化与 CI 作业`。

### 第 7 步：文档与规范（A16）

- [ ] 新增 `.trellis/spec/backend/playback-contracts.md`（接口、检查顺序、错误码、复核、测试）；更新 `backend/index.md`、`directory-structure.md`、
  `core0-runtime-contracts.md` 的专题表与接口列表；`library-scan-contracts.md` 只加一行指向停用目录的播放语义（该文件余量约 900 字节）。
- [ ] 前端规范：`player-design-guidelines.md`、`state-management.md`、`quality-guidelines.md`、`index.md`；`frontend/DESIGN.md` §4、§6、§7。
- [ ] ADR 0004 决策记录（第 1 项按 K2、第 5 项中撤销与停用部分按 K3）；路线图与父任务 PRD 的 M2a 状态。
- 验证：`find .trellis/spec -name '*.md' -size +24000c` 为空；Markdown 相对链接检查；`git diff --check`。
- 提交：`docs(playback): 同步 M2a 合同、规范与路线`。

### 第 8 步：手工实播与收尾（A15）

- [ ] 写 `research/manual-acceptance.md`：三组组合的检查清单（FLAC 两种参数、ALAC、MP3、拖动、切歌、模式、切页、刷新恢复、
  另一标签页退出、媒体键、不可播放标注）与结果表；请用户执行并回填操作系统与浏览器版本。
- [ ] 按结果修正；ALAC 在 Safari 未通过时从格式表移除并同步文档。
- [ ] PRD 写“实施结果与验收对照”；`task.py archive … --skip-branch-validation`，再单独提交归档后的相对链接修正；更新记忆。

## 风险与回退点

- **Safari 行为**（Range 请求方式、类型严格、自动播放）只能靠手工实播发现；问题在第 8 步修正。
- **复核上界**不含浏览器缓冲，合同与界面都写明“已缓存的音频无法收回”。
- **严格版本核对**会让改过标签但未重扫的文件不能播放；文案提示重扫，必要时再议放宽。
- **素材生成**需要一次性拉取 FFmpeg 镜像；拉取失败时改为只用代码生成 FLAC/WAV，并把其余格式移出格式表。
- **规范余量**：`library-scan-contracts.md` 接近 24 KB，只允许加一行链接。
- 回退：每步独立提交；前端回退必须连同内嵌产物。
