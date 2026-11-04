# M5 P 线步骤 3 最终测试记录

日期：2026-11-04。仅在 `/home/qlqq/.paseo/worktrees/2i6geckb/m5-personal`（`m5-personal`）接手测试收尾。
本版取代交接时的未全绿结论；保留累计范围及历史结果摘要，最终证据使用 `resume-final` 命名，不覆盖旧轮日志。

## 1. 最终结论与本次改动

**用户指定的步骤 3 收尾验证全部通过，剩余失败 0。** 不代表步骤 4、跨线集成或整个 M5 已完成。

| 验证 | 最终结果 |
| --- | --- |
| Vitest，完整指定 20 文件 | **971 PASS / 0 FAIL / 0 SKIP**，30.20 秒 |
| 个人 decoder（包含在上一行） | **309 PASS**，D1–D3 原样转绿 |
| 全局 API 登记与往返（包含在第一行） | **220 PASS**，完整登记 219 份样本（personal 70 份） |
| Playwright，指定 4 个 spec | **78 PASS / 0 FAIL / 0 SKIP**，3.3 分钟；personal 29，其他 49 |
| 真实服务 2 条 | **2 PASS / 0 FAIL / 0 SKIP**，浏览器部分 4.5 秒 |
| 隔离启动器拒绝路径（假 Docker） | **7 PASS / 0 FAIL**，不连接 Docker 守护进程 |
| `npm run lint` / `npm run typecheck` / `npm run build` | 全部退出 0 |
| 截图 | 保留原 14 张，新增失败态 375/1440 各 1 张，共 16 张及 SHA-256 清单 |

本次只修改以下测试和测试基础设施，以及本记录：

- `frontend/src/features/personal/favorite-interactions.test.tsx`：F2 等稳定失败态，保留无第二次写、旧心形和无读取中的断言；新增参数化的双位置恢复用例（服务端 true/false 各一次），确认失败期间跨位置禁止切换、手动重读期间仍忙碌、读成功后两处显示服务端值、提示清掉、随后可正常切换。
- `frontend/src/app/favorites.test.tsx`：仅取消无新增不显示多余刷新按钮；重新进入后保留行消失；原换页、换栏、换页签覆盖保留。
- `frontend/src/features/personal/playlist-text.test.ts`、`frontend/src/app/add-to-playlist.test.tsx`：按裁决省略零计数；全已存在用简短文案；混合计数断言保留。
- `frontend/e2e/real/personal.real.ts`：全部新增两首的反馈不含“0 首已在歌单里”；原收藏/换序/刷新后 DOM 与 API 持久化断言保持。
- `frontend/e2e/personal.spec.ts`：新增 375/1440 心形重读失败测试、截图、行四边界与下一行不重叠断言，仍执行页面/main 溢出、字号、桌面滚动与 axe 检查。
- `scripts/playback-e2e.sh`：测试项目名支持安全前缀参数，默认行为不变；本轮显式传 `roomusic-test-m5p`。容器/卷/网络预检均检查查询退出码，拒绝接管占用或在查询失败后继续。
- `scripts/playback-e2e-isolation.test.sh`：假 Docker 验证三类资源查询失败/已占用，以及生产项目名拒绝，共 7 例。

没有手改产品、后端样本或用户数据；build 按要求自动重建内嵌资源（`index-rc07TiWJ.js` / `index-CtHtTuBF.css`）。前端实现、规范、设计文档等已有未提交差异不是本次改动。没有 commit / stash / reset / checkout，也没有归档任务或运行会自动提交的 journal 命令。

## 2. 步骤 3 累计测试范围（含上一位交付）

以下路径相对 `frontend/`。

### 新增

- `src/features/personal/api/decoders.test.ts`：70 份 golden 的状态码、JSON 往返、覆盖完整性；必填/nullable、非法枚举、安全整数、计数关系、分页、位置、撤销回执、批内唯一性、数组上限。上限夹具使用独立 ID，包含 UUID 大小写重复反例。
- `src/features/personal/playlist-name.test.ts`：trim、必填、100/101 Unicode 码点、控制字符、默认日期与跨日。
- `src/features/personal/playlist-text.test.ts`：全部已知、部分未知向下取整、全部未知时长；批量两组计数、超限与冲突文案。
- `src/features/personal/favorite-interactions.test.tsx`：乐观更新/回滚、两目标并发隔离、pending 忽略、跨位置同步、不确定提交的重读门禁。
- `src/features/personal/sync.test.tsx`：真实 QueryClient/会话与互通 BroadcastChannel 传输替身；无个人载荷、无回传、聚焦/可见性主动重读、scope/代次隔离、非法消息、卸载清理。
- `src/app/favorites.test.tsx`：保留槽不污染缓存；聚焦/后台重读保留、刷新/换页/换栏/换页签清除、新行提示、409 丢 revision、缺失与不可用状态。
- `src/app/playlists.test.tsx`：同幂等键同请求重试、409 后重读、四项移动边界、移除无确认且焦点回标题、撤销只带 token 并按服务端位置跨页、超限提示、删除文案、创建名字验证。
- `src/app/add-to-playlist.test.tsx`：单曲 membership 禁用、批量计数、原子新建并加入、列表加载更多、结果不确定同请求重试、冲突、pending/失败态。
- `src/app/routes/track-actions.test.tsx`：迁入旧曲目动作的播放/队列/不可播行为，并检查心形及添加歌单入口。
- `src/app/routes/personal-playback.test.tsx`：真实 PlayerEngine 下分别统计 missing/unsupported/omitted、10,000 上限、不补位、满旧队列仍替换、无可播/失败/迟到会话不改变队列。
- `src/app/player-personal-actions.test.tsx`：队列首次出现去重、同名异轨、打开时快照、本地今天日期/跨日、一次请求与同键重试。
- `src/test/personal-data.ts`、`src/test/fake-personal.ts`、`src/test/personal-data.test.ts`：有状态个人接口共用行为、两种传输接线与 54 项协议/隔离回归。
- `e2e/personal.spec.ts`：五宽几何/文字行数/灰态/axe；纯键盘完整流程；鼠标与 CDP 真实触屏输入拖动，刷新验证；生产构建截图。
- `e2e/real/personal.real.ts`：隔离目录合成两首 WAV，真实 UI + Go + PostgreSQL；收藏与歌单排序持久化。

### 修改已有测试/fixture

- `src/app/history.test.tsx`：`/me` 默认收藏并可转历史。
- `src/app/routes/history-playback.test.tsx`：真实 `FavoriteStatusScope`。
- `src/features/player/components/player-ui.test.tsx`：迁走弃用组件的三项测试；信号路径按已有 `player-design-guidelines.md:22` 的裁决更新（不显示 PCM 实现细节）。
- `src/shared/api/request.test.ts`：新增无 body PUT 的 Cookie/cache/header/body 回归，不发送 JSON null。
- `src/test/api-contract.test.ts`：56 个个人成功样本与 14 个错误样本加入既有全局 registry。
- `src/test/fake-api.ts`、`e2e/support/api-mock.ts`：安装共享 personal 全路由；未知路由 404，不用默认成功兜底。
- `e2e/history.spec.ts`、`e2e/keyboard.spec.ts`、`e2e/player.spec.ts`：新默认页、心形和菜单、不可播行的更多仍可用；卡片定位排除“全部历史”；逐次 Tab 预算适配新增动作；ALAC 旧文案更新。

### 共享 fake 的公开入口

`installPersonalApi(api,{session,catalog})`；`mock.personal` 按当前 account.id 隔离；`personalTracksOf`、`personalTrack`、`personalAlbumOf`、`seedPersonalTracks/Albums`、`seedFavoriteTracks/Albums`、`seedPlaylist`。
所有收藏、歌单读取/lookup/queue、CRUD、加入、移除、撤销、移动均有状态；版本、配额、幂等收据、去重和跨用户隔离有独立测试。静态错方法 405，未登记路径 404。

## 3. 缺陷与裁决关闭

| 编号 | 本次处理与证据 | 状态 |
| --- | --- | --- |
| D1–D3 | 前端已修；decoder 非法页位置、页后锚点、UUID 大小写重复原测试全部通过，309 项无删减 | 已关闭 |
| F1 | 前端已修；不确定写入后同目标两位置 busy、阻止第二次写，原用例通过 | 已关闭 |
| F2 | 等待稳定失败态而非立即被替换的中间文案；补双位置手动重读成功后的解锁与继续切换（两种服务端值） | 已关闭 |
| F3 | 用户裁决不是缺陷；只有新增时提示刷新，仅取消不增加按钮；离开后清保留层已验证 | 非缺陷，预期已修正 |
| T1 | 用户裁决不是缺陷；零计数省略，全已存在简写，混合两组计数保留；真实服务也通过 | 非缺陷，预期已修正 |

没有发现新的产品缺陷。隔离脚本有两处既有防护缺口（孤立网络未检查、查询失败被空输出掩盖），在获准的测试基础设施范围内修复并加拒绝路径回归。

## 4. 实际命令与最终结果

`R=/home/qlqq/.paseo/worktrees/2i6geckb/m5-personal`。除注明外，npm/npx 在 `$R/frontend` 执行。浏览器与真实服务均在同一个持锁 shell 中 source 环境；`CI=1` 不复用旧预览服务，禁重试。

### 4.1 Vitest：全部 20 文件

```bash
cd "$R/frontend"
set -o pipefail
npx vitest run \
  src/features/personal \
  src/app/favorites.test.tsx src/app/playlists.test.tsx src/app/add-to-playlist.test.tsx \
  src/app/player-personal-actions.test.tsx src/app/routes/personal-playback.test.tsx \
  src/app/routes/track-actions.test.tsx src/app/routes/history-playback.test.tsx \
  src/app/history.test.tsx src/features/player/components/player-ui.test.tsx \
  src/test/personal-data.test.ts src/test/api-contract.test.ts \
  src/app/album-detail.test.tsx src/app/app.test.tsx src/app/shell/nav-items.test.ts \
  src/shared/api/request.test.ts \
  --reporter=default --reporter=json --outputFile=test-results/m5-personal-vitest-resume-final.json \
  2>&1 | tee test-results/m5-personal-vitest-resume-final.log
```

退出 0，971/971 通过，无跳过。JSON 报告的总数与各文件一致。日志存在 jsdom 的 `HTMLMediaElement.pause/load Not implemented` 提示，不是失败；真实媒体行为另由浏览器层验证。

| 文件 | PASS | FAIL |
| --- | ---: | ---: |
| `src/app/add-to-playlist.test.tsx` | 14 | 0 |
| `src/app/album-detail.test.tsx` | 47 | 0 |
| `src/app/app.test.tsx` | 27 | 0 |
| `src/app/favorites.test.tsx` | 10 | 0 |
| `src/app/history.test.tsx` | 17 | 0 |
| `src/app/player-personal-actions.test.tsx` | 10 | 0 |
| `src/app/playlists.test.tsx` | 22 | 0 |
| `src/test/api-contract.test.ts` | 220 | 0 |
| `src/test/personal-data.test.ts` | 54 | 0 |
| `src/app/routes/history-playback.test.tsx` | 13 | 0 |
| `src/app/routes/personal-playback.test.tsx` | 38 | 0 |
| `src/app/routes/track-actions.test.tsx` | 4 | 0 |
| `src/app/shell/nav-items.test.ts` | 2 | 0 |
| `src/features/personal/favorite-interactions.test.tsx` | 20 | 0 |
| `src/features/personal/playlist-name.test.ts` | 81 | 0 |
| `src/features/personal/playlist-text.test.ts` | 27 | 0 |
| `src/features/personal/sync.test.tsx` | 8 | 0 |
| `src/shared/api/request.test.ts` | 26 | 0 |
| `src/features/personal/api/decoders.test.ts` | 309 | 0 |
| `src/features/player/components/player-ui.test.tsx` | 22 | 0 |

### 4.2 Playwright：四个 spec

```bash
flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-personal/frontend
  set -o pipefail
  CI=1 npx playwright test e2e/personal.spec.ts e2e/history.spec.ts \
    e2e/keyboard.spec.ts e2e/player.spec.ts \
    --workers=1 --retries=0 --reporter=list \
    --output=test-results/m5-personal-browser-resume-final \
    2>&1 | tee test-results/m5-personal-browser-resume-final.log
'
```

退出 0，**78 PASS / 0 FAIL / 0 SKIP**。覆盖既有五宽 375/768/1024/1280/1440、纯键盘、鼠标与 CDP 触屏拖动、axe；新增两宽稳定失败态的位置断言均过。personal 的 WCAG 2.1 A/AA 检查断言全部 violations 为空，不只过滤 serious。

### 4.3 真实服务：两条持久化

```bash
flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-personal
  set -o pipefail
  CI=1 ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-m5p \
    scripts/playback-e2e.sh e2e/real/personal.real.ts \
    --reporter=list --output=test-results/m5-personal-real-resume-final \
    2>&1 | tee frontend/test-results/m5-personal-real-resume-final.log
'
```

退出 0，**2 PASS / 0 FAIL / 0 SKIP**：

1. 收藏一首 → 浏览器刷新仍存在 → 取消时保留原行 → 刷新后消失；UI 与真实 API 核验。
2. 新建歌单 → 整张加入两首 → 菜单下移交换 → DOM/API 确认顺序 → 刷新后顺序仍保持；零计数省略文案也通过。

本轮仅用 `docker compose --env-file /dev/null -f "$R/compose.test.yaml" -p roomusic-test-m5p`，随机库 `playback_14400_26662`、空闲端口 `59599`、专用测试凭据。合成音频与临时编译树位于 `/tmp/roomusic-playback-e2e.jptPrkWm`，不读项目 `.env` 或真实音乐。
结束日志显示容器、卷、网络全部移除；再次只读查询该项目 label 三类资源均为空，临时目录已不存在。没有连接用户 `roomusic-postgres-1` 或 `roomusic` 库，也没有清理其他线资源。使用 list reporter 和独立 test-results 输出，不生成会污染 lint 的 HTML 报告。

### 4.4 门禁与基础设施

```bash
cd "$R/frontend"
set -o pipefail
npm run lint 2>&1 | tee test-results/m5-personal-lint-resume-final.log
npm run typecheck 2>&1 | tee test-results/m5-personal-typecheck-resume-final.log
```

两条均退出 0。

```bash
flock /tmp/roomusic-m5-e2e.lock bash -lc '
  source /home/qlqq/.local/share/playwright-deps/env.sh
  cd /home/qlqq/.paseo/worktrees/2i6geckb/m5-personal/frontend
  set -o pipefail
  npm run build 2>&1 | tee test-results/m5-personal-build-resume-final.log
'
```

退出 0，TypeScript 与生产构建完成，2330 模块转换。

```bash
cd "$R"
bash -n scripts/playback-e2e.sh scripts/playback-e2e-isolation.test.sh
set -o pipefail
bash scripts/playback-e2e-isolation.test.sh 2>&1 | tee frontend/test-results/m5-personal-compose-guard-final.log
git diff --check
```

语法与空白检查通过；假 Docker 回归 **7 PASS**。容器/卷/网络各自的查询失败与已有资源均拒绝，生产项目名在调用 Docker 前拒绝。

### 4.5 本次局部验证与审查

以下先于完整集合执行，不替代最终验收：

```bash
cd "$R/frontend"
npx vitest run src/features/personal/favorite-interactions.test.tsx src/app/favorites.test.tsx src/features/personal/playlist-text.test.ts src/app/add-to-playlist.test.tsx
npx eslint src/features/personal/favorite-interactions.test.tsx src/app/favorites.test.tsx src/features/personal/playlist-text.test.ts src/app/add-to-playlist.test.tsx
npx eslint e2e/personal.spec.ts e2e/real/personal.real.ts
npm run typecheck
```

四文件 **71 PASS / 0 FAIL / 0 SKIP**；两个 ESLint 命令与 typecheck 均退出 0。独立检查 agent 还执行同范围 `npx --no-install eslint`、typecheck、脚本 `bash -n` 和定向 `git diff --check -- scripts/playback-e2e.sh`，均过。
最初单独运行 `ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic scripts/playback-e2e.sh` 预检退出 1（预期拒绝，未访问 Docker）；后由 7 项脚本回归覆盖。

### 4.6 历史轮次摘要（不是最终状态）

- 交接前最终 Vitest：969 项，957 PASS / 12 FAIL；本次 12 个旧失败全关闭，新增恢复参数用例 2 项，总数为 971。
- 交接前浏览器最终：76 PASS；此前定位轮 26/1、63/13、22/5，主要是 fake 无 body 请求区分、灰态 locator 和旧键盘/ALAC 断言，上一位已修。
- 交接前真实服务：1 PASS / 1 FAIL，仅零计数反馈失败；排序与刷新使用 soft 断言继续验证。本次两条完整通过。
- 交接前 lint 曾因生成的 HTML 报告压缩 JS 被扫描而失败，移到忽略目录后通过；本次 list reporter 不再生成该报告。
- 旧证据仍保留在 `frontend/test-results/m5-personal-*-final.*`、历史轮目录和 `m5-personal-lint-generated-report.log.gz`；本次以 `*-resume-final.*` 为最终依据。没有重复安装依赖。

## 5. 截图与视觉复核

根目录：`frontend/test-results/m5-personal-screenshots/`。
保留原 14 张并验证原 SHA-256 全部一致，新增两张截图；`manifest.json` 已更新为 16 张的 SHA-256。

| 页面 | 1440 | 375 |
| --- | --- | --- |
| 收藏专辑 | `1440/favorites-albums.png` | `375/favorites-albums.png` |
| 收藏曲目 | `1440/favorites-tracks.png` | `375/favorites-tracks.png` |
| 歌单列表 | `1440/playlists.png` | `375/playlists.png` |
| 歌单详情 | `1440/playlist-detail.png` | `375/playlist-detail.png` |
| 拖动中 | `1440/playlist-dragging.png` | `375/playlist-dragging.png` |
| 添加到歌单 | `1440/add-to-playlist.png` | `375/add-to-playlist.png` |
| 播放器心形 | `1440/player-heart.png` | `375/player-heart.png` |
| **新增：心形重读失败** | **`1440/favorite-reread-failed.png`** | **`375/favorite-reread-failed.png`** |

新增两张均已人工图像复核：375 下“播放 / 心形 / 重新读取 / 更多”在行内，失败文案在下方，不越界、不覆盖下一行；1440 下操作与提示在操作列中，行高自然扩展，无重叠。几何断言还检查页面/main 溢出与下一行位置。
本轮四 spec 也重新生成正常状态截图，原始文件见 `frontend/test-results/m5-personal-browser-resume-final/`；稳定目录的旧14张不冒充本轮生成。
此前正常态截图已由上一位复核：千字标题会完整换行，375 fullPage 较长；1440 main 独立滚动，截图不是所有行完整展开的证据，其余由几何断言覆盖。

## 6. 验收边界与后续

- 本次用户指定的步骤 3 收尾已完成；D1–D3、F1/F2 回归全绿，F3/T1 按明确裁决调整，无剩余失败。
- 两 PRD A6 的本次接口样本、前端相关集、五宽、axe、键盘/拖动和两条真实业务持久化证据已取得。
- 未重跑后端 Go race/SQL 鉴权与并发矩阵：后端步骤 1 已交付，本次用户指定的是前端测试收尾。共享 fake 不能替代真实后端鉴权和 SQL 锁证据。
- BroadcastChannel 仍以互通传输替身验证合同，不冒充两张真实浏览器标签页专项验收；未做人类读屏设备、Firefox/Safari 手工验证。
- 未运行全量前后端测试：遵守相关最小集合约束，已完整运行用户指定的20文件与4 spec；未做步骤 4 导出树验证、提交、合入、任务归档或更新两 PRD 验收状态，留主会话处理。
