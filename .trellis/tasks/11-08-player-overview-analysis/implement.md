# 执行计划：全屏播放器、后台音频分析与概览改版

需求见 [prd.md](./prd.md)，技术设计与接口合同见 [design.md](./design.md)。本文只写顺序、分工、验证与回滚点。

## 工作环境

- 分支 `player-overview-analysis`，worktree `/home/qlqq/.paseo/worktrees/2i6geckb/player-overview-analysis`（Paseo 工作区 `wks_08845e048b355366`）。`main` 上另有“歌单与专辑页面”任务在进行，本任务不碰 `main` 的检出目录。
- 前端依赖：worktree 里先 `cd frontend && npm ci`（worktree 不共享 `node_modules`）。
- 测试数据库：只用 Compose 项目 `roomusic-test-poa`（`--env-file /dev/null`、绝对路径的 `compose.test.yaml`、空闲端口、随机库名，用完 `down -v`）；集成测试必须真的跑了（PASS 不是 SKIP，需要 `ROOMUSIC_TEST_DATABASE_URL`）。不碰用户自己的 `roomusic-postgres-1`。
- ffmpeg：`ROOMUSIC_TEST_FFMPEG=$HOME/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg`（以目录实际内容为准）。
- 浏览器：Playwright 前 `source /home/qlqq/.local/share/playwright-deps/env.sh`；mock 套件固定端口 4173，与 `main` 上的任务可能同时使用，所有 Playwright 与 `scripts/playback-e2e.sh` 运行都包在 `flock /tmp/roomusic-e2e.lock` 里；`playback-e2e.sh` 用 `ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-poa`。截图要中文字体时用 `FONTCONFIG_FILE=/tmp/roomusic-shot-fc/fonts.conf`（临时文件，可能需重建）。
- mock 套件的 `npm run build` 会改写 `backend/cmd/roomusic/web`；每步提交前确认产物与源码一致。

## 分工（Paseo，均在上面的工作区）

| 角色 | 模型 | 范围 |
| --- | --- | --- |
| 后端实现 | `codex/gpt-6.1-sol`（high，`modeId: full-access`） | `backend/**`（不含 `backend/cmd/roomusic/web/**`）、`compose*`、`deploy/**` |
| 前端实现 | `claude/claude-opus-5-5`（high，`bypassPermissions`） | `frontend/**`、`backend/cmd/roomusic/web/**` |
| 测试 | `codex/gpt-6.1-sol`（high，`full-access`），与后端实现是不同的 agent | 为每步新增或修改的行为补测试、更新受影响的 E2E，跑该步的最小测试集；发现实现 bug 回报主会话，不自行改实现 |

- 实现 agent 只写代码，跑类型检查 / `go vet` / 与改动直接相关的已有测试；不写新测试、不提交。
- 每步：实现回报 → 测试 agent 补测并运行 → 主会话审 diff 与测试输出 → 主会话按文件路径提交（中文提交信息，前端改动连同重建的 `backend/cmd/roomusic/web` 一起提交）。不通过就写明问题打回原 agent。
- 所有 prompt 写明：只测改动直接影响的部分，不跑全量，不为假想场景写测试，不过度兜底；界面不写防御性说明和多余说明。

## 步骤

后端线与前端线并行；合同（design.md 第 1.2、3 节）在开工前冻结，改动须经主会话同意。

### 后端线

- [x] **B1 资料库统计接口**（design 3.3）：`artistListCount` 抽取、`GET /api/v1/catalog/stats`、样本 `catalog/stats.json`、`catalog/stats-artists-preparing.json`、注册。
  - 测试：四个数与对应列表总数相等，艺人未就绪时为 null；样本测试。`go test -race ./internal/app/ -run 'TestPostgreSQL(CatalogStats|APIResponseSamples)' -count=1`
- [x] **B2 后台音频分析**（design 1、2、3.1、3.2）：迁移 0033（含 down）、`catalog/track_analysis.go`、`playback/analysis`（ffmpeg 与 PCM 计算）、`playback/analysis_worker.go`、`app.OpenAnalysis` 与装配、`ROOMUSIC_AUDIO_ANALYSIS` 开关、两个接口与样本、依赖规则、`deploy/production/README.md` 补一行开关说明。
  - 测试：单元（窗口峰值→波形/首尾点、ebur128 摘要解析、专辑增益公式、工作者循环：扫描让路、失败不阻塞、取消退出）；固定 ffmpeg 的合成素材测试（响度 ±0.5 LU、开头静音、CUE 区间）；PostgreSQL（待分析判定四种情况、两个接口与样本、迁移上下行）；依赖规则。命令见 design 8.2。
  - 回滚点：迁移只新增表；不合格时整步回退。

### 前端线

- [x] **F2 全屏播放器、播放栏、队列抽屉、信号路径弹层**（design 4.1–4.5，信号路径浏览器层先只写“解码”）：同时完成 R1（`change-summary.ts:134` → “已完成”）与 R4 删句；`ModalDialog.closeOnBackdrop`；`/now-playing` 跳转；侧栏删“正在播放”；专辑页 `playerFrom` 修正；故障提示与重试按钮；删除正在播放页。
  - 测试：`player-ui.test.tsx`、`nav-items.test.ts`、`change-summary.test.ts`、`player-personal-actions.test.tsx`、`artist-player-metadata-step5.test.tsx` 中受影响的断言；E2E `player.spec.ts`、`auth.spec.ts`、`search-step4.spec.ts`（1440 与 375，含 axe）。
- [ ] **F1 概览改版**（design 5）：依赖 B1/B2 的样本文件已提交（先用 design 3 的 JSON 写解码器，样本到位后对齐）。`LibraryStats`、`AnalysisSummary`、`AttentionSummary`、版面；删除 `LibrarySummary`。
  - 测试：`decodeCatalogStats`、`decodeAnalysisProgress` 读样本；概览组件（四卡、空库、侧栏管理员项、分析各状态文案）；`api-mock.ts` 增加新接口桩；E2E 中概览相关断言（如有）。
- [ ] **F3 波形、播放设置、均衡与交叉渐变**（design 4.6–4.8）：依赖 F2 与 B2 样本。`WaveformSeek`、`playback-settings.ts`、`/settings/playback`（均衡四种模式）、引擎按需接入 Web Audio（接入/退出/中断退回规则）、两路渐变增益、信号路径浏览器层取值。
  - 测试：设置解码；假 AudioContext 下的接入与退出规则（两项都关不创建上下文、非 running 本首不接入、中断退回）；增益选择表（四种模式、正增益、未知 −5 dB）、`setVolume` 不再依赖回读、渐变计划与切换（跨专辑渐变、同专辑/单曲循环/手动切歌不渐变、备用元素被拒退回、切换后聆听只算新曲目）；`WaveformSeek` 有/无分析与键盘；设置页。命令见 design 8.2。
  - 回滚点：引擎改动单独一个提交，出问题可只回退这一提交（两项设置都关时不走新代码）。

### 联调与收尾

- [ ] **J1 真实后端联调**（一次）：B2、F3 都合入分支后，`scripts/playback-e2e.sh`（`ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-poa`，flock）跑更新后的 `frontend/e2e/real/playback.real.ts`：全屏打开、信号路径 `ALAC → FLAC`、全屏歌词点行定位；另确认后台分析在测试资料库上跑完、波形出现、均衡增益写进信号路径。
- [ ] **J2 截图审阅**（A10）：临时 Playwright spec（不提交）用 mock 后端截 1440 与 375：全屏播放器（播放 / 暂停、歌词）、队列抽屉、信号路径弹层、播放设置、概览（管理员、成员、空库）。主会话先自审（文字行数、元素位置、横向溢出、多余说明），有问题打回修复并补回归断言；截图附在结果里给用户看。
- [ ] **J3 规范更新**（Phase 3.3）：`player-design-guidelines.md`、`playback-and-personal-state.md`、`frontend/DESIGN.md` 第 4 节、`frontend/DESIGN-components.md` 第 6、7 节，新增 `.trellis/spec/backend/audio-analysis-contracts.md` 并登记到后端索引。
- [ ] **J4 合并回 `main`**：等 `main` 上的并行任务状态明确后合并（`--no-ff`）。冲突处理：`backend/cmd/roomusic/web` 不手工解，合并后 `npm run build` 重建再提交；迁移号若被占用，本分支重编号（文件名、embed 列表与迁移版本测试一起改）。合并后在 `main` 上只跑受冲突影响部分的测试。
- [ ] **J5 归档**：PRD 写实施结果与 A1–A10 证据 → `task.py archive 11-08-player-overview-analysis --skip-branch-validation` → 链接修正与 journal 同一提交。

## 不做

- 全量 Go / Vitest / Playwright（只在 J1 跑一次真实播放）；为 iOS 行为写模拟测试；重复演练或预演。
