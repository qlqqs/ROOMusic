# 歌单与专辑页面布局统一、图标化按钮与管理员改密

## Goal

让单管理员日常操作更顺手、歌单类页面更好看更一致：管理员改密不再输入当前密码；“我喜欢的音乐”和歌单详情的顶部仿网易云歌单页、曲目列表统一成同一套列；专辑页管理按钮只留图标并配快速悬停提示；专辑概况改成一行灰色小字。

## 用户原始需求（2026-11-08）

1. 管理员修改自己或者成员密码，不需要输入当前密码。
2. 我喜欢的音乐、歌单的歌曲列表统一为：序号、长度、专辑艺人、专辑、发行日期、播放/喜欢/更多图标。专辑页面的曲目列表不变。
3. 专辑页面“编辑发行信息”“归入其他专辑…”等按钮全部只用图标，悬停时弹出文字，鼠标移开后不再显示。
4. 歌单和我喜欢的音乐页面顶部直接模仿网易云歌单页（附件图 1：左侧大封面；右侧标题+编辑图标、头像+创建者+创建日期、“播放全部”与“···”；下方“歌曲 N”标签）的布局和样式。
5. 专辑页面的年份、流派、介质、曲目、总时长做成附件图 2 那样（灰色单行，细竖线分隔，只写值）。

## 现状（代码证据）

- 改密接口 `POST /api/v1/auth/password`（`backend/internal/identity/httpapi/passwords.go` 的 `changePassword`）调用 `identity.ChangeOwnPassword`（`backend/internal/identity/passwords.go`），总是核对当前密码并经 `LoginThrottle` 限速；成功后保留当前会话、撤销其他会话。`Authenticate` 返回的 `identity.User` 带 `Role`。
- 管理员重置成员密码 `POST /api/v1/users/{id}/password` 本来就不要当前密码；对自己调用返回 403 `self_password_reset`。成员表自己那一行没有“重置密码”（`frontend/src/features/members/components/member-table.tsx`），重置对话框只有新密码与确认（`reset-password-dialog.tsx`）。
- 前端改密对话框 `frontend/src/features/auth/components/change-password-dialog.tsx` 有“当前密码”字段，校验在 `account-rules.ts`；合同记录在 `.trellis/spec/backend/identity-contracts.md`。
- 我喜欢的音乐：`frontend/src/features/personal/components/liked-tracks-view.tsx`（DataTable：曲目/专辑/收藏时间/操作），页面 `frontend/src/app/routes/favorites-page.tsx`（Page 标题“我喜欢的音乐”+ Panel“收藏的曲目”）；收藏接口只给总数，不给总时长。
- 歌单详情：`frontend/src/features/personal/components/playlist-detail-view.tsx`（`<ol>` 行：拖动把手、序号、标题+“艺人 — 专辑”、操作、时长；头部 Panel=歌单名、“N 首 · 时长”、播放全部、改名、删除歌单；“全部歌单”链接），页面 `playlist-detail-page.tsx`（Page 标题“我的歌单”）。歌单摘要有 `created_at`、`cover_groups`（拼图封面 `PlaylistCover`）、`duration_seconds`。
- 曲目摘要 `trackSummaryDecoder`（`frontend/src/features/catalog/api/track-summary.ts`）含 `duration_seconds`、`release.title/album_artist/artist/year/artwork`；后端只存年份，没有完整发行日期。
- 专辑页管理操作区 `frontend/src/features/governance/components/release-admin-actions.tsx`：编辑发行信息、设为默认版本/改回自动选择（多版本时）、归入其他专辑…、移出为独立专辑（多版本时）、查看问题（链接，需要关注时），都是图标+文字。
- 图标按钮 `Button variant="icon"`（`frontend/src/shared/ui/button.tsx`）用 `aria-label` + 原生 `title` 做提示；`MenuButton iconOnly` 复用它；`favorite-button.tsx`、`primary-nav.tsx` 也各自用 `title`。共约 15 处 `variant="icon"`。
- 专辑概况：`frontend/src/features/catalog/components/album-header.tsx` 的 `factsOf`，标签在上、数值在下，人工修正的字段跟 `ManualCorrectionTag`。
- 应用是浅色纸面主题、金色主色（`frontend/src/styles/tokens.css`）。

## Requirements

### R1 管理员改密免当前密码
- 管理员在 设置 › 账号 的“修改密码”对话框里只填新密码和确认，没有“当前密码”字段；后端对管理员角色不核对当前密码、不走当前密码限速，其他行为（保留当前会话、撤销其他会话、新密码规则）不变。
- 管理员重置成员密码维持现状（本来就不要当前密码）。
- 普通成员改自己的密码仍需当前密码。
- 取舍：被盗用的管理员会话可以直接改掉管理员密码；万一被锁在门外，用命令行 `roomusic reset-password` 恢复。

### R2 曲目列表统一（我喜欢的音乐、歌单详情）
- 两页用同一套列，带表头行：`#`、标题、专辑艺人、专辑、发行、播放/喜欢/更多三个图标、时长（时长在最右，与专辑页一致）。
- 标题下方只在曲目艺人与专辑艺人不同时，小字显示曲目艺人（与专辑页规则相同）；“文件缺失”标签照旧。
- 专辑艺人用 `album_artist`，没有时退回发行艺人；专辑名可点进专辑（文件缺失时是纯文字）；“发行”只显示年份，未知留空占位。
- 我喜欢的音乐去掉“收藏时间”列；序号按当前页偏移连续编号。歌单序号是歌单位置；拖动把手保留（悬停或聚焦行时出现，触屏上一直显示），“更多”菜单里的上移/下移/移到最前/最后/从歌单移除不变。
- 行悬停有整行浅色高亮；窄屏下列折叠为标题+次要信息+操作，不出现横向滚动。
- 专辑页曲目列表不变。

### R3 专辑页管理按钮图标化 + 快速悬停提示
- 编辑发行信息、设为默认版本/改回自动选择、归入其他专辑、移出为独立专辑、查看问题，全部只显示图标、无文字、无边框；读屏名不变。
- 新做应用自己的悬停提示：鼠标停约 0.3 秒弹出，移开立即消失，点击后不残留；只响应鼠标悬停（键盘焦点不弹出，读屏靠 aria-label）。
- 所有 `Button variant="icon"`（含 `MenuButton iconOnly`）、心形按钮、侧栏展开按钮统一换成这个提示，去掉原生 `title`，避免两个提示叠在一起。
- 提示不被表格、播放器等容器裁掉，靠近视口边缘时自动换方向。

### R4 歌单 / 我喜欢的音乐顶部仿网易云
- 布局：左侧大封面（圆角）；右侧标题为页面 h1（歌单名 / “我喜欢的音乐”），歌单标题后有铅笔图标（改名）；下一行是圆形头像（用户名首字）+ 用户名，歌单再加“YYYY-MM-DD 创建”；再下是“播放全部”（主按钮）与“···”（歌单：删除歌单；我喜欢的音乐没有可放的操作就不显示）。
- 封面：歌单用现有拼图封面；我喜欢的音乐用金色心形封面。
- 背景：头部从封面主色的淡色渐变到页面底色（无封面时用中性淡色，我喜欢的音乐用金色淡色）；文字对比度不下降。
- 头部下方一个“歌曲 N”标签（带下划线强调），歌单在旁边灰色小字写总时长（沿用现有“以上/未知”规则）。不做下载、评论、收藏者、歌单内搜索。
- 原来的 Page 标题“我的歌单”/Panel“收藏的曲目”、“全部歌单”链接去掉（侧栏有入口）；超限提示、移除撤销、“列表有更新”、分页等功能照旧。
- 窄屏下封面缩小，信息与按钮不挤成多行乱排。

### R5 专辑概况单行
- 年份、流派、介质、曲目、总时长改成一行灰色小字，细竖线分隔，只写值：`2021 │ 国语流行乐 · 台语流行乐 │ 18 首 │ 1 小时 14 分钟`。
- 介质只在多于 1 张时写“N 张碟”；年份未知不写；有曲目时长未知时不写总时长（沿用现规则）；总时长用口语写法；全应用口语时长统一为“N 小时 M 分钟”，歌单现有的“1 小时 20 分”随之改为“分钟”。
- 人工修正过的年份/流派值后面照旧跟小标记。窄屏下自动换行，分隔线不出现在行首。

### R6 扫描目录的文件夹浏览框（2026-11-08 追加）
- 点“目录路径”输入框弹出的文件夹列表浮在页面上层，不把下面的内容往下推；浮层高度固定，列表在框内上下滚动，顶部当前位置行不随列表滚动。
- 点文件夹进入下一层；点文件夹右边的“选择”选中该文件夹（填入路径框并收起）。
- 浏览从文件系统根 `/` 开始（用户选择“从容器根目录开始”）；只列出允许的音乐目录本身、它们的上层路径和其中的子目录（例如容器里 `/` 下只看到 `music`），不列出选不了的系统目录。
- 只有允许的音乐目录及其子目录可以选择；上层路径只用于进入。新增扫描目录的路径校验（`ValidatePath`）与媒体缓存隔离检查不变。
- 现状证据：浏览框在文档流里（`frontend/src/features/library/components/add-root-form.tsx`、`folder-browser.tsx`）；浏览起点是 `ROOMUSIC_ALLOWED_LIBRARY_ROOTS`（生产为 `/music`，`backend/internal/library/folders.go`，合同 `.trellis/spec/backend/library-folder-contracts.md`）；把允许根配成 `/` 会让 `internal/app/derivation.go` 的缓存隔离检查失败，所以不改配置。

## Acceptance Criteria

- [x] A1 管理员打开“修改密码”看不到“当前密码”，只填新密码即可改成功，其他设备会话被撤销、当前会话保留；普通成员仍看到并必须填对当前密码。后端对管理员请求不带或带错 `current_password` 都成功；成员不带或带错仍是 403 `current_password_incorrect`。
- [x] A2 我喜欢的音乐与歌单详情的表头依次是 `#`、标题、专辑艺人、专辑、发行、（操作）、时长；行内有播放、心形、更多三个图标；没有“收藏时间”列；曲目艺人与专辑艺人相同时标题下不重复显示。
- [x] A3 专辑页管理操作区只有图标，鼠标悬停约 0.3 秒出现文字提示、移开即消失、点击后不残留；全应用图标按钮不再出现原生 title 提示。
- [x] A4 歌单页与我喜欢的音乐页顶部是左封面右信息的网易云布局，有淡色封面取色渐变背景、头像+用户名（歌单有创建日期）、播放全部与“···”、“歌曲 N”标签；歌单的铅笔图标能改名、“···”能删除歌单。
- [x] A5 专辑页概况是一行灰色小字、竖线分隔，单碟时不出现介质项。
- [x] A6 1440 与 375 宽度截图里以上页面无溢出、无错位、无单独一个图标掉到下一行；界面文案没有多余说明。
- [x] A8 扫描目录浏览框浮在上层、高度固定可滚动；从 `/` 起只看到通往音乐目录的路径；上层路径没有“选择”，音乐目录及子目录可以选择并填入路径框。
- [x] A7 相关单元测试与受影响的 E2E 断言更新并通过；专辑页曲目列表行为不变。

## Out of Scope

- 下载、评论、收藏者、歌单内搜索、用户头像上传、完整发行日期（库里只有年份）。
- 普通成员改密流程、管理员重置成员密码流程（已满足需求）。
- 专辑页曲目列表、专辑页其他区块。
- 曲目行缩略封面（网易云的行内小封面不做）。

## 决策记录

- 规划路线：人工选择（human selection），选择人 qlqq，2026-11-08；技术设计由 Opus 子 agent 起草，主会话裁决见 design.md §8，用户 2026-11-08 确认最终规划摘要（含安全取舍、时长统一为“分钟”、歌单标签页标题）并批准实现。
- 顶部配色：浅色封面取色渐变 + 应用金色主按钮（不照搬深色红色）。
- 列顺序：时长放最右（与专辑页、主流播放器一致）。
- 专辑概况：只写值、省掉无用项（单碟不写介质、口语时长）。
- 悬停提示：应用自己的快速提示，全应用图标按钮统一替换。
- 歌单详情的浏览器标题随 h1 变为“歌单名 · ROOMusic”。

## 实施结果（2026-11-08）

| 提交 | 内容 |
| --- | --- |
| `bbc2f34` | R1 后端：`ChangeAdminOwnPassword`，handler 按角色分派，两个限速用例改用成员，新增管理员免当前密码集成用例，`identity-contracts.md` |
| `66056de` | R1 前端：管理员的改密对话框不显示“当前密码”，请求不带该字段；替身与 e2e 同步 |
| `aaacc2a` | R3：`useTooltip` 快速悬停提示，全应用图标按钮去掉原生 `title`；专辑页管理操作只留图标 |
| `dc161b4` | R6：文件夹浏览从 `/` 开始，只列允许目录及其上层路径，`selectable` 区分可选与只用于进入；浮层固定高度、不推开页面 |
| `19c990b` | R5：专辑概况一行灰色小字、竖线分隔；`formatSpokenDuration` 移到 shared，全应用口语时长统一为“分钟” |
| `83d6500` | R2 + R4：`TrackTable` 统一曲目表、`CollectionHero` 网易云式页头、`Page.header`、`Avatar`、`useArtworkTint` |

与执行计划的偏差：

- R2 与 R4 合成一个提交：两步改的是同一组视图与测试文件（`favorites.test.tsx`、`playlists.test.tsx`、`personal.spec.ts`），分开暂存的中间状态无法单独通过测试。
- `playlist-text.ts` 里歌单时长文案的拆分（`playlistDurationText`）随 R5 的 `19c990b` 提交，行为不变。
- R6 是实现中途追加的需求，设计见 design.md §9。

验收中打回并修正：

- 测试辅助 `openAccountScene` 在管理员场景仍填写当前密码，已改。
- 歌单详情 axe `aria-required-children`：dnd-kit 的读屏说明与播报区渲染在表体里，`DndContext` 移到 `TrackTable` 外面。
- 375 宽度下曲目表的次要信息被拆成三行，改为单行省略。
- 键盘用例里“歌曲 N”的断言写成了 2（表头行被算进去了），改为 1。

验证（均由测试 agent 执行；Playwright 用空闲端口的临时配置，未改 `playwright.config.ts`）：

- 后端：`go vet ./internal/identity/...`；`go test ./internal/identity/...`；`ROOMUSIC_TEST_RUN=... scripts/test-integration.sh` 跑管理员改密、成员改密与两个限速用例（PASS，非 SKIP）；`go test ./internal/library -run TestListFolders`；`ROOMUSIC_TEST_RUN='TestPostgreSQLAPIResponseSamples' scripts/test-integration.sh`（重录 `library-folders/root`、`child` 样本，PASS）。
- 前端 Vitest：R1 4 个文件（549 通过，1 跳过）；R3/R6 5 个文件（600 通过，1 跳过）；R5 3 个文件（80 通过）；R2/R4 `favorites`、`playlists`、`user-menu` 3 个文件（48 通过）；`npx tsc --noEmit` 通过。
- Playwright：`auth.spec.ts --grep 密码` 12 通过；R3/R6 与 R1 的响应式、axe 场景 29 通过；`albums.spec.ts` 19 通过；`personal.spec.ts` 与 responsive/a11y 中两个新页面场景共 71 项，第一次 63 项通过，修正后复跑失败的 8 项加截图共 10 项全部通过。
- 真实服务：`scripts/playback-e2e.sh e2e/real/personal.real.ts` 2 项通过。
- 截图：1440 与 375 宽度下的改密对话框、专辑管理区与悬停提示、我喜欢的音乐、歌单详情、文件夹浮层，均无溢出、无错位。
- 没有跑全量测试：各步改动都能用对应的测试文件和场景局部覆盖。
