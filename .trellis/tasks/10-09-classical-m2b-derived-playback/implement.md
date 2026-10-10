# M2b 派生播放执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。

## 约束

- 只使用 `roomusic-test` Compose 项目；不碰用户的 `roomusic` 项目与其他容器；停进程按确切 PID，不用 `pkill`。
- 真实 `music/` 只读；自动化只用合成素材。真实库核对只输出汇总计数，不保存任何派生文件。
- 每步单独提交到 `main`，不推送；提交信息用中文，结尾加协作者行。改动前端源码的提交要重新构建并一起提交 `backend/cmd/roomusic/web`。
- 提交前把暂存区导出到临时目录（`git checkout-index -a --prefix=<临时目录>/`），在完整文件树上构建与测试，通过后再提交；不用 `--amend`。
- 公开仓库：提交前确认不含 `backend/data/`、`.env*`（`.env.example` 除外）、真实音乐或其派生物。

## 启动前

- [x] 用户批准规划（PRD 的 K1–K8 与歌词子任务的规划），把批准写进两个 PRD 的“规划决策记录”。
- [x] 提交两份规划与父任务 PRD 的任务表（`docs(m2b): …`）。
- [x] `python3 ./.trellis/scripts/task.py start 10-09-classical-m2b-derived-playback`。

## 步骤

### 第 1 步：派生核心（B2、B5、B6、B7 的核心部分）

- [x] `scripts/ffmpeg-pinned.sh`（取出并校验固定的静态 ffmpeg，打印路径）；`scripts/gen-playback-fixtures.sh` 增加自检素材的生成。
- [x] `backend/internal/playback/derivation`：缓存（布局、索引、淘汰、清理）、键、任务管理（合并、等待者、宽限期、上限、失败记忆、关停）、
  ffmpeg 适配（定位、版本、自检、两遍转换、进程组）、`STREAMINFO` 核对；`selftest.m4a`。
- [x] `library.ContentFingerprintOf`；`catalog.TrackSource` 增加位深、采样率、声道。
- [x] 依赖规则加入 `playback/derivation`。
- [x] 验证：

  ```bash
  cd backend && test -z "$(gofmt -l .)" && go vet ./... && go build ./...
  go test -race ./internal/playback/... ./internal/library/... ./internal/catalog/... ./internal/app/... -count=1   # 未设置数据库时集成测试跳过
  ROOMUSIC_TEST_FFMPEG="$(../scripts/ffmpeg-pinned.sh)" go test -race ./internal/playback/derivation/... -run TestFFmpeg -count=1
  ```

- [x] 提交：`feat(playback): 无损派生缓存与 ffmpeg 适配`。

### 第 2 步：派生接口与前端（B1、B3、B4、B8）

- [x] `platform/config`：`ROOMUSIC_FFMPEG_PATH`、`ROOMUSIC_MEDIA_CACHE_MAX_MIB`；`app`：缓存目录校验（位于音乐根内则启动失败）、自检、装配与生命周期。
- [x] `playback`：格式表 `Delivery`、派生编排；`playback/httpapi`：预检长轮询与 202、音频的派生分支、错误码。
- [x] 接口样本与 `TestPostgreSQLAPIResponseSamples`；`TestPostgreSQLPlayback…` 派生用例；`scripts/test-integration.sh` 设置 `ROOMUSIC_TEST_FFMPEG` 并把模式扩为 `TestPostgreSQL|TestFFmpeg`。
- [x] 前端：解码器与端点、能力判定、引擎 `preparing` 与预取、信号路径、文案；Vitest 与 Playwright（路由模拟）；重新构建内嵌产物。
- [x] 验证（后端）：同第 1 步的单元测试；隔离 PostgreSQL（`docker compose --env-file /dev/null -f <绝对路径>/compose.test.yaml -p roomusic-test`，空闲端口、随机库名，
  结束 `down -v`）上运行 `go test -race ./internal/playback/... ./internal/app/... -run 'TestPostgreSQL(Playback|APIResponseSamples)|TestFFmpeg' -count=1`。
- [x] 验证（前端）：`npm run lint && npm run typecheck && npm test`；`npm run test:e2e`（先确认 4173 端口空闲）；无环境变量的构建与内嵌产物逐文件一致。
- [x] 验证（现有真实服务自动化）：`./scripts/playback-e2e.sh`（本机没有 ffmpeg，ALAC 仍按“不能直接播放”通过）。
- [x] 提交：`feat(playback): ALAC 经无损派生播放`（含内嵌产物）。

### 第 3 步：真实服务自动化与 CI（B9）

- [x] `scripts/playback-e2e.sh` 用 `scripts/ffmpeg-pinned.sh` 设置 `ROOMUSIC_FFMPEG_PATH`；`frontend/e2e/real/playback.real.ts` 的 ALAC 两组参数改为派生播放、拖动、自然结束，
  断言 `audio/flac` 与信号路径。
- [x] `.github/workflows/ci.yml`：确认集成与真实播放作业都经脚本取得同一个 ffmpeg（不新增作业）。
- [x] 验证：`bash -n scripts/*.sh`；CI 文件可解析；`./scripts/playback-e2e.sh` 全部通过。
- [x] 提交：`test(playback): 真实服务覆盖 ALAC 派生播放`。

### 第 4 步：文档与规范（B10）

- [x] 新增 `.trellis/spec/backend/playback-derivation-contracts.md`（配置、启动、接口、流程、上限、错误、测试）；更新 `playback-contracts.md`（`delivery`、202、链接）、
  `backend/index.md`、`directory-structure.md`、`core0-runtime-contracts.md` 的环境变量与专题表。
- [x] 前端规范 `player-design-guidelines.md`、`state-management.md`；`frontend/DESIGN.md` 中 ALAC 与信号路径的描述。
- [x] ADR 0004：状态、决策记录（K1–K6）、§11 第 2、4、6 项与第 5、7 项派生部分；路线图 M2b 行（拆分、CUE 延后）与 ADR 索引；父任务 PRD；
  后续验收任务 F2（K8）；README 与 `.env.example` 的 ffmpeg 安装和变量说明。
- [x] 验证：`git diff --cached --check`；规范文件都小于 24,000 字节；改动文档的相对链接检查（解码 `%20`，失败即中止）；`task.py validate`。
- [x] 提交：`docs(playback): 同步 M2b 派生合同、决策与部署说明`。

### 第 5 步：收尾

- [x] 真实库只读核对（不保存派生文件）：对 84 个 ALAC 逐个管道转换为 FLAC 并比对解码后 PCM，只输出成功/失败计数与耗时汇总，写入 `research/`。
- [x] PRD 写实施结果与验收对照；提醒用户安装 ffmpeg（`scripts/ffmpeg-pinned.sh` 或发行版包）后重启并播放 ALAC 确认。
- [x] `task.py archive 10-09-classical-m2b-derived-playback --skip-branch-validation`，再单独提交归档后的相对链接修正；更新记忆。

## 风险与回退点

- 第 1 步只新增包与脚本，不改接口，可单独回退。
- 第 2 步是接口与前端的同一提交，回退它即回到 M2a 行为（ALAC 不能播放）。
- 缓存只在 `<数据目录>/media-cache`，回退后可直接删除该目录。
- 若固定镜像不可拉取（网络或仓库问题），测试会因缺少 ffmpeg 跳过派生用例：本地与 CI 的脚本把“取不到 ffmpeg”当作失败，不允许静默跳过。
