# M2b 歌词执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。约束与派生任务相同（见[派生任务执行计划](../archive/2026-10/10-09-classical-m2b-derived-playback/implement.md)的“约束”）。

## 启动前

- [x] 用户批准规划（Y1–Y5），批准记录写进 PRD；规划与派生任务一起提交。
- [x] 派生任务归档后再 `python3 ./.trellis/scripts/task.py start 10-09-classical-m2b-lyrics`（两者都改正在播放页与真实服务用例，先后进行）。

## 步骤

### 第 1 步：提取与保存（L1、L2）

- [x] 解析器提取三类来源的歌词与来源键；单元测试。
- [x] 迁移 `0015_track_lyrics.sql`；`SourceObservation` 字段；`persist_track.go` 的 INSERT/UPDATE；扫描写入、更新、清空的集成测试。
- [x] 验证：

  ```bash
  cd backend && test -z "$(gofmt -l .)" && go vet ./... && go build ./...
  go test -race ./internal/library/... ./internal/catalog/... ./internal/app/... -count=1
  # 隔离 PostgreSQL（roomusic-test，空闲端口、随机库名，结束 down -v）
  go test -race ./internal/library/... ./internal/catalog/... ./internal/platform/postgres/... -run 'TestPostgreSQL' -count=1
  ```

- [x] 提交：`feat(library): 扫描时提取内嵌歌词`（`d6c9fd4`）。

### 第 2 步：歌词接口与正在播放页（L3–L6）

- [x] `catalog.LoadTrackLyrics`、`catalog.ParseLyrics`；`GET /api/v1/tracks/{id}/lyrics`；接口样本与集成测试。
- [x] 前端：端点与解码器、查询钩子、分组模型、歌词面板、正在播放页布局；Vitest 与 Playwright（路由模拟）；重新构建内嵌产物。
- [x] 验证：后端同第 1 步加 `TestPostgreSQLAPIResponseSamples`；前端 `npm run lint && npm run typecheck && npm test`、`npm run test:e2e`；无环境变量的构建与内嵌产物逐文件一致。
- [x] 提交：`feat(player): 歌词接口与逐行同步显示`（含内嵌产物，`62023a1`）。

### 第 3 步：真实服务自动化（L7）

- [x] 生成带歌词的 FLAC 与 ALAC 素材（更新 `backend/testdata/playback/README.md` 与体积上限检查）；`playback.real.ts` 歌词用例。
- [x] 验证：`./scripts/playback-e2e.sh` 全部通过。
- [x] 提交：`test(player): 真实服务覆盖歌词显示`（`ca17fa3`）。

### 第 4 步：文档与截图（L8）

- [x] 新增 `.trellis/spec/backend/lyrics-contracts.md`（提取、保存、接口、解析规则、测试）；`library-scan-contracts.md` 只加一行指引（保持在 24,000 字节以内）；
  更新 `catalog-rest-contracts.md`、`backend/index.md`、`database-guidelines.md` 的迁移列表（如有）；前端 `player-design-guidelines.md`、`component-guidelines.md`；
  `frontend/DESIGN.md` 的正在播放与歌词描述；路线图与父任务。
- [x] 用合成数据截取宽屏、窄屏的同步、纯文本、空态页面（`ROOMUSIC_LYRICS_SCREENSHOTS=1` 运行路由模拟用例，写到 `/tmp`，不入库）。
- [x] 请用户确认布局（Y5）：2026-10-11 用户看合成数据截图后回复“布局可以，继续收尾（推荐）”，没有修改意见。
- [x] 验证：`git diff --cached --check`、规范文件大小、相对链接、`task.py validate`。
- [x] 提交：`docs(player): 同步歌词合同与规范`（`63099c8`）。

### 第 5 步：收尾

- [x] 真实库只读核对：`TestRealMusicLyricsCoverage`（`c25a70b`）只输出计数：284 个音频文件中 134 个带歌词，同步 110、纯文本 24，无解析失败。
- [x] 补测“回到当前行”的键盘操作（`a42f6f9`）。
- [x] PRD 写实施结果与验收对照；设计文档记录差异；提醒用户重扫真实库后查看歌词。
- [ ] 归档（`--skip-branch-validation`），单独提交归档后的相对链接修正；更新记忆。

## 风险与回退点

- 第 1 步只改扫描与存储，接口不变，可单独回退（两列闲置）。
- 第 2 步若布局需要调整，只改面板与页面样式，不影响接口。
