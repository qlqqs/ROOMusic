# 全屏播放器、后台音频分析与概览改版

## 目标

用户在 2026-11-08 提出五项改动，在临时分支 `player-overview-analysis`（Paseo worktree `~/.paseo/worktrees/2i6geckb/player-overview-analysis`，从 `main` 的 `71c4fa5` 分出；`main` 上另有“歌单与专辑页面”任务在进行）上规划和实施：

1. 修改记录里，撤销记录本身显示的“撤销不能再撤销”改成“已完成”。
2. 像 Roon 一样做后台音频分析，得到音量电平（响度）、交叉渐变过渡和波形显示所需的信息；不求快，在后台慢慢跑。
3. 取消“正在播放”页，改成网易云那样点播放栏封面放大到全屏；全屏参考附件图 1（Roon 底栏：收起箭头、波形进度、传输控制、信号路径圆点、队列图标）、图 2（网易云：黑胶唱片 + 唱臂、标题、专辑/歌手、歌词，深色背景）、图 3（Roon 信号路径弹层：标题写出整体质量，各层一个图标、名称和一行取值）。
4. 信号路径里“ALAC 转成 FLAC，位深、采样率与声道不变。”“由浏览器解码，可能按输出设备重新采样。”“网页看不到声卡的实际格式。”全部删掉。
5. 概览页把资料库数字放到顶部（参考附件图 4：图标 + 数字 + 名称的卡片），直接显示专辑、曲目、艺术家、作曲家；右边做一个小的局部侧栏，放扫描状态、音乐目录、成员。

用户价值：播放体验接近 Roon / 网易云（全屏沉浸、看得到波形、曲目之间音量一致、可选渐变），概览一眼看清资料库规模。

## 现状（代码证据）

- 撤销原因：`frontend/src/features/governance/change-summary.ts:134` 对 `type === "revert"` 返回“撤销不能再撤销”，测试在 `change-summary.test.ts:90` 一带。
- 信号路径：`frontend/src/features/player/components/signal-path.tsx`，四段（源文件、服务端、浏览器、系统输出），说明句在第 10–11、29–30 行。
- 正在播放页：路由 `/now-playing`（`frontend/src/app/router.tsx` 的 `nowPlayingRoute`）→ `app/routes/now-playing-route.tsx` → `features/player/pages/now-playing-page.tsx`；内容为封面、歌词（`LyricsPanel`，逐行同步、点行定位）、标题/艺人/专辑、当前曲目心形（插槽 `CurrentTrackFavorite`）、完整控制、`PlayerFeedback`、信号路径、队列（插槽 `SaveQueueAsPlaylist`、清空队列）。
- 播放栏 `features/player/components/player-bar.tsx` 有三处链到 `/now-playing`（标题、格式 `#signal-path`、“队列（N）”），故障提示写着“进入正在播放页重试”；侧栏导航 `app/shell/nav-items.ts` 另有“正在播放”入口。
- 播放引擎基于单个 `HTMLAudioElement`（`features/player/engine/player-engine.ts`，约 1170 行），没有 Web Audio，也没有无缝播放；`prefetcher.ts` 只为下一首需转换的曲目提前预检。iPhone / iPad 上网页音量只读。
- 概览 `app/routes/overview-page.tsx`：最近播放、最近新增各占一行，下面是资料库（专辑数与需要关注数，取自专辑列表分页总数）、扫描、音乐目录（管理员）、成员（管理员）四个面板。没有艺术家、曲目、作曲家计数接口；艺人有 `composer` 角色，艺人列表支持 `/artists?role=composer` 筛选（`features/artists/artist-search.ts:27`）。
- 后端已用 ffmpeg 做 ALAC→FLAC 派生（`backend/internal/playback/derivation/`），生产镜像自带静态 ffmpeg（`deploy/production/Dockerfile:17`），路径由 `ROOMUSIC_FFMPEG_PATH` 配置；自检失败时派生关闭，应用照常启动。
- 迁移目前到 `0032`。设置分栏（`app/routes/settings-sections.ts`）有账号、扫描目录、成员、操作记录、备份，没有“播放”栏目。

## 已定决策

- D1（用户 2026-11-08 选定）分析结果三项都用：波形进度条、音量均衡、交叉渐变。均衡与渐变放进设置里新的“播放”栏目可开关；交叉渐变默认关，同一张专辑连续播放时不渐变；播放引擎改成两路音频交替。
- D2（用户 2026-11-08 选定）队列做成右侧抽屉：播放栏和全屏底栏都有队列图标，点开从右侧滑出，含调整顺序、移除、清空、存为歌单。
- D3（用户 2026-11-08 选定，D6 按 Roon 修订）音量均衡默认开，自动模式：连续播放同一张专辑（同一发行）时整张专辑用同一增益，否则按曲目；均衡不得导致削波。
- D4（主会话按现有约定定）播放设置（均衡开关、渐变开关与时长）存在本机浏览器里，跟随设备，和现在的音量、静音一样；分析没有速度设置，始终单线程低优先级慢慢跑。
- D5（主会话按用户原话定）概览四个统计按用户写的顺序：专辑、曲目、艺术家、作曲家。
- D6（用户 2026-11-08 定：“按照 Roon 的来”）音量均衡照 Roon：EBU R128，目标 −14 LUFS（固定、不暴露）；可以提升安静曲目，但按真峰值减到不削波为止，不用压限器；模式有关 / 自动 / 按曲目 / 按专辑（设置里可选，默认自动）；还没分析（响度未知）的曲目固定 −5 dB；实际增益显示在信号路径里。提升需要 Web Audio：均衡与渐变都关时不接入，iPhone / iPad 上不接入（见 D7）。
- D7（主会话定）iPhone / iPad 上网页音量只读，均衡与渐变不生效：设置页两个开关禁用，只加一句“这台设备不能调节网页音量，这两项不会生效。”
- D8（主会话定）“曲目”数不跨版本去重，等于资料库里可播放的曲目文件数（CUE 虚拟轨也算）。
- D9（主会话定）CUE 虚拟轨照 R2 分析，排在最后，计入分析进度总数。
- D10（主会话定）专辑还没分析完时先用曲目增益，整张分析完后自动改用专辑增益。
- D11（主会话定）全屏里点专辑或艺人后按返回，回到全屏播放器；全屏开合记在浏览器历史里。
- D12（主会话定）信号路径圆点：无损用强调色（金色）实心、有损灰色实心、编码未知空心；标题文字同时写明，不只靠颜色。概览里分析不可用时只写“未运行”。
- D13（主会话定）侧栏导航的“正在播放”入口随页面一起删除。

## 需求

### R1 撤销记录显示“已完成”
- 操作记录（设置 → 操作记录、专辑详情底部“修改记录”）里，类型为撤销的记录在操作列显示“已完成”，不再是“撤销不能再撤销”；其他原因文案不变。

### R2 后台音频分析
- 后端有一个后台工作者，逐首分析资料库中可播放的曲目：用 ffmpeg 解码，得到整体响度（LUFS）、真峰值、用于波形的峰值序列，以及交叉渐变需要的首尾信息（开头静音结束点、结尾静音开始点）。
- 慢速：同一时间只分析一首，低 CPU 优先级，两首之间留间隔；扫描进行中让路；应用启动后自动开始，新扫描进来的曲目、文件内容变了的曲目会重新排进来。
- ffmpeg 不可用时分析不运行、记录日志，应用其他功能不受影响；单首分析失败记为失败，不反复重试卡住队列。
- 结果入库（新迁移），可以通过接口按曲目读取；概览能读到分析进度（已分析 / 总数）。
- CUE 虚拟轨按其在源文件中的起止区间分析。

### R3 全屏播放器（取代正在播放页）
- 点播放栏封面放大成全屏播放器（有过渡动画，尊重“减少动态效果”），向下箭头、Esc、浏览器返回都能收起。
- 全屏版面：深色背景（取自封面的模糊色调）；左边黑胶唱片，中间是封面，唱臂在播放时落到唱片上、暂停时抬起，唱片播放时转、暂停时停；右边是标题、专辑与歌手（可点进对应页面，点了收起全屏）、当前曲目心形、歌词（沿用现有逐行同步与点行定位）。窄屏（375）时唱片在上，点唱片与歌词切换。
- 全屏底栏参考图 1：收起箭头、信号路径圆点、上一首/播放暂停/下一首、播放模式、队列图标、波形进度（左右两端时间）、音量。
- 播放栏（非全屏）保留现有布局，进度条同样换成波形；标题与格式不再链到 `/now-playing`；故障提示不再提“正在播放页”，在播放栏或全屏里能重试。
- `/now-playing` 不再是页面，旧地址跳到概览（替换历史项）；侧栏导航删去“正在播放”。
- 队列抽屉（D2）：播放栏与全屏底栏的队列图标打开，右侧滑出，内容与现在的队列一致（播放此项、上移、下移、移除、清空并确认、存为歌单、随机时的提示），Esc 或点外面关闭，焦点进出正确。

### R4 信号路径弹层
- 信号路径从页面区块改成弹层：点播放栏或全屏底栏的彩色圆点打开（参考图 3）。
- 标题写出整体质量（如“信号路径：无损”/“有损”），各层一个图标 + 名称 + 一行取值：数据源（如 “FLAC 44.1kHz 16bit 2ch”）、服务端（“原文件直出”或“ALAC → FLAC”）、浏览器（解码；音量均衡生效时写出增益，如“音量均衡 −3.2 dB”；交叉渐变开着时也写出来）、系统输出。
- 删掉“目标”第 4 条列出的三句说明，弹层里不再有任何解释句。
- 圆点颜色反映整体质量：无损源一种颜色，有损源另一种。

### R5 波形进度条
- 曲目已分析时，进度条显示波形（已播放与未播放部分颜色不同），可点击、拖动、键盘调整位置，保留现有可访问名称与取值文字；未分析的曲目显示普通进度条。

### R6 音量均衡与交叉渐变（播放设置）
- 设置新增“播放”栏目（所有成员可见）：音量均衡（关 / 自动 / 按曲目 / 按专辑，默认自动）、交叉渐变（开/关，默认关；时长 3/5/8/12 秒，默认 5 秒）。
- 音量均衡按 D3、D6、D10；目标响度固定，不在界面上暴露；均衡增益与用户音量叠加，不改动音量滑块的位置。
- 交叉渐变开启时，前一首结尾与后一首开头重叠渐变；同一张专辑（同一发行）的相邻曲目、单曲循环、用户手动切歌时不渐变。
- 关闭渐变与均衡时，现有播放行为（需转换曲目的提前预检、失败处理、媒体会话、聆听记录）保持不变。

### R7 概览改版
- 顶部一行四个统计卡片（图标 + 数字 + 名称，参考图 4）：专辑、曲目、艺术家、作曲家；专辑链到专辑页，艺术家链到艺人页，作曲家链到 `/artists?role=composer`，曲目不链接。资料库为空时照现在一样给一句话。
- 下面主栏放最近播放与最近新增；右侧局部窄栏放扫描状态、音频分析进度、需要关注的专辑数（链到筛选后的专辑页）、音乐目录（管理员）、成员（管理员）。
- 窄屏时统计卡片两列两行，侧栏排到主栏下面。
- 新增一个只读的资料库统计接口提供这四个数（口径与各列表页一致：专辑按专辑组，艺术家与作曲家按艺人列表的可见规则）。

## 验收标准

- [x] A1 操作记录中撤销类记录的操作列显示“已完成”（R1）。
- [x] A2 对测试资料库启动应用后，后台分析逐首完成，结果可从接口读到；ffmpeg 不可用时应用正常、分析不运行；分析失败的曲目不阻塞后续曲目（R2）。
- [x] A3 点播放栏封面打开全屏播放器，黑胶随播放/暂停转动与停止、唱臂落下与抬起；向下箭头、Esc、浏览器返回都能收起；访问 `/now-playing` 跳到概览（R3）。
- [x] A4 全屏显示标题、专辑、歌手、心形与歌词，歌词逐行同步与点行定位照常可用；375 宽时唱片与歌词可切换，没有横向溢出（R3）。
- [x] A5 队列图标在播放栏和全屏底栏都能打开右侧队列抽屉，队列操作与“存为歌单”可用，关闭后焦点回到队列图标（R3/D2）。
- [x] A6 点信号路径圆点出现弹层，各层一行取值；页面与弹层中找不到“目标”第 4 条的三句话；均衡生效时浏览器层写出增益（R4）。
- [x] A7 已分析曲目的进度条是波形，可点击/拖动/键盘定位；未分析曲目是普通进度条（R5）。
- [x] A8 设置 → 播放可选音量均衡模式、开关交叉渐变；均衡开启时响的曲目被压低、安静且有峰值余量的曲目被提升，都不削波，未分析曲目 −5 dB，信号路径写出实际增益；自动模式下同一专辑连续播放时用专辑增益；渐变开启时跨专辑的相邻曲目重叠渐变、同专辑相邻曲目不渐变（R6）。
- [x] A9 概览顶部显示专辑、曲目、艺术家、作曲家四个数字，与各列表页口径一致；右侧栏有扫描、分析进度、需要关注、目录与成员（后两者仅管理员）；375 宽时侧栏在主栏下方，无横向溢出（R7）。
- [x] A10 收尾时 1440 / 375 截图（全屏播放器、队列抽屉、信号路径弹层、概览）经主会话审阅无布局问题，界面没有多余说明句。

## 不做

- 输出设备选择（图 1 的 “System Output” 区域）：浏览器只有部分支持，不做。
- 分析速度设置、按需（播放时立即）分析：用户只要求后台慢慢跑。
- 歌词以外的全屏标签（网易云的“百科”“相似推荐”）。
- 波形缩略图出现在曲目列表等其他位置。

## 规划决策记录

- 路线：(a) 用户审阅最终规划摘要后批准。D1–D3 由用户逐项选定；技术设计由 Opus 子 agent 起草（design.md），主会话审阅后定 D4、D5、D7–D13，D6 原为“只衰减”的推荐，用户审阅时改为“按照 Roon 的来”，design 4.6 随之改为 Web Audio。
- 批准：用户 2026-11-08 审阅修订后的规划摘要（含 D6 按 Roon 的均衡与 Web Audio 按需接入、安卓锁屏不做专门演练），选“按这份规划开工”。

## 实施结果（2026-11-08）

在临时分支 `player-overview-analysis`（Paseo worktree）上实施，`main` 上的“歌单与专辑页面”任务归档后以 `--no-ff` 合并回 `main`。

| 提交 | 内容 |
| --- | --- |
| `37a871b` | B1：`GET /api/v1/catalog/stats`（专辑、曲目、艺术家、作曲家；艺人整理中后两项为 null），抽出 `artistListCount` 与艺人列表共用 |
| `e90beec` | F2 + R1 + R4 删句：全屏播放器（黑胶、唱臂、歌词、心形）、队列抽屉、信号路径弹层；`/now-playing` 跳到概览，侧栏去掉“正在播放”；撤销记录显示“已完成” |
| `0d4d3bc` | B2：迁移 0033 `track_analyses`、ffmpeg 慢速后台分析工作者、`GET /api/v1/tracks/{id}/analysis` 与 `GET /api/v1/audio-analysis/progress`、`ROOMUSIC_AUDIO_ANALYSIS` 开关 |
| `aab1e09` | F1：概览顶部四张统计卡，右侧窄栏放扫描、音频分析、需要关注、目录与成员 |
| `c44ba31` | 专辑增益只统计分析工作集内的曲目（停用目录或缺大小、修改时间的曲目不再让专辑增益一直为空） |
| `ff83dda` | J3：播放器、播放状态、DESIGN 与后台音频分析合同写入规范 |
| `890cf53` | F3：波形进度条、设置 → 播放、按 Roon 的音量均衡（−14 LUFS，可提升到真峰值 −0.5 dB，未分析 −5 dB）、跨专辑交叉渐变 |
| `3ea63b4` | J1：分析空闲等待由 5 分钟改为 1 分钟；`playback.real.ts` 覆盖分析完成后的波形与均衡增益 |
| `3eece9c` | J4：合入 `main`（合并提交）；分支的图标控件改用 `main` 新的 `useTooltip` 悬停提示（队列按钮、播放栏封面、信号路径圆点、窄屏唱片），全屏里的提示反成浅底；重建嵌入产物；交汇处测试 `auth.spec` 补“播放”栏目、`personal.spec` 播放栏曲名按文字定位 |
| `a4ef27b` | J4 发现 `main` 上已失败的三个旧用例（`19c990b` 去掉概况 dt 标签后未同步）：`default-version.test.tsx`、`release-corrections.test.tsx`、`governance.spec.ts` 改从“概况”列表读取 |

与执行计划的偏差：

- F2 中播放栏在队列为空但有可重试故障时仍显示；`PlayerFeedback` 与 `PlayerControls` 删除，故障提示与重试按钮放进播放栏和全屏；信号路径圆点在 900px 以下的播放栏里隐藏（全屏底栏仍有）。
- F1 中“需要关注”为 0 时不加链接；分析进度在“已分析 + 失败 ≥ 总数”时写“已完成”；空资料库仍显示“需要关注”。
- F3 中播放栏波形高 22px；随机播放在自动模式下用曲目增益；负增益用 U+2212 减号。
- J1 发现分析工作者空闲等待 5 分钟太长（新资料库扫描后要等满 5 分钟才开始），改为 1 分钟，design.md 与 `audio-analysis-contracts.md` 同步。
- J4 发现 `.trellis/spec/frontend/personal-guidelines.md` 仍写着已删除的 `NowPlayingPage` 插槽，随合并改为 `FullscreenPlayer`。

验收中打回并修正：

- B1：`ListArtists` 先算 `textnorm.Text` 再取总数，顺序错误，改为先取总数。
- B2：失败曲目阻塞专辑增益；LUFS / 真峰值带 float32 噪声（改为一位小数）；`catalog/httpapi` 依赖了 `playback/analysis`（改为由 `app` 注入版本）。
- F2：测试 agent 删掉了 `player-ui.test.tsx` 中仍然有效的用例并去掉了 `expectLastItemUncovered`，已恢复。
- F3：引擎测试过薄，补了同专辑 / 单曲循环 / 手动切歌不渐变、备用元素被拒退回普通切歌、切换后聆听只算新曲目、上下文非 running 走直连、音量只读不建 AudioContext；安静曲目波形几乎看不见，改为按本曲最大峰值归一化。

验收对照：

| 验收 | 证据 |
| --- | --- |
| A1 | `change-summary.ts` 撤销类返回“已完成”；`change-summary.test.ts` 与 F2 Vitest 通过 |
| A2 | B2 单元与固定 ffmpeg 合成素材测试（响度、开头静音、CUE 区间）、`TestPostgreSQLAnalysis*`（待分析判定、失败不阻塞、专辑增益）均 PASS 非 SKIP；失败不阻塞后续曲目（`TestAnalysisWorkerPersistsFailureAndContinuesToNextCandidate`）；ffmpeg 不可用或 `ROOMUSIC_AUDIO_ANALYSIS=off` 时 `OpenAnalysis` 只记日志、不装配工作者，应用照常启动，进度接口报 `unavailable`（`TestPostgreSQLAnalysisEndpointsExposeProtectedReadiness`）；J1 真实服务上 13/13 首分析完成 |
| A3 | `player-ui.test.tsx`、`player.spec.ts`：封面打开全屏，收起按钮、Esc、浏览器返回关闭，`/now-playing` 跳到概览；截图中播放时唱臂落下、暂停时抬起 |
| A4 | `player.spec.ts` 1440 / 375（含 axe）；J1 真实服务全屏歌词点行定位通过；375 截图唱片与歌词可切换、无横向溢出 |
| A5 | `player-ui.test.tsx` 抽屉打开、关闭后焦点回到队列图标；`player.spec.ts` 播放栏与全屏底栏都能打开抽屉；截图 |
| A6 | `playback.real.ts` 断言四层取值且三句说明不存在；均衡生效时浏览器层写“解码 · 音量均衡 +7.8 dB”（截图）；J1 真实服务增益与分析接口一致 |
| A7 | `WaveformSeek` 有 / 无分析与键盘用例；J1 真实服务分析完成后播放栏出现波形 canvas |
| A8 | `leveling.test.ts` 四种模式、正增益受真峰值限制、未知 −5 dB；`player-engine.test.ts` 跨专辑渐变、同专辑 / 单曲循环 / 手动切歌不渐变；`playback-settings` 解码与设置页用例；J1 真实服务信号路径增益与接口一致 |
| A9 | `TestPostgreSQLCatalogStats` 四个数与各列表总数相等、艺人未就绪为 null（PASS 非 SKIP）；`overview-page.test.tsx`；1440 / 375 截图 |
| A10 | 1440 / 375 截图 20 张（全屏播放 / 暂停 / 歌词、队列抽屉、信号路径、播放栏波形、设置 → 播放、概览管理员 / 成员 / 空库），主会话审阅无溢出、错位与多余说明 |

验证（均由测试 agent 执行）：

- 后端：`go vet ./...`、`go build ./...`；`TestPackageDependencyDirections`；隔离库（`roomusic-test-poa` / `roomusic-test-poa-b2`）上 `TestPostgreSQL(APIResponseSamples|ArtistAPIResponseSamples|CatalogStats|ArtistListCountsAndSearchDeepPages)`、`TestPostgreSQLAnalysis*`、`TestPostgreSQLMigrationRollbackRoundTrip`（含 0033 上下行），均 PASS 非 SKIP；`ROOMUSIC_TEST_FFMPEG=… go test -race ./internal/playback/... -run 'Test(Analysis|Waveform|Ebur128|FFmpegAnalysis)'` PASS。
- 前端 Vitest：F2 7 个文件 89 通过、`player-ui.test.tsx` 20 通过；F1 8 个文件 313 通过；F3 8 个文件 282 通过、`player-engine.test.ts` 168 通过；`npx tsc --noEmit`、`npx eslint src e2e`、`npm run build` 通过。
- Playwright（mock）：F2 `player.spec.ts`、`auth.spec.ts`、`search-step4.spec.ts`、`claims.spec.ts` 87 通过；F1 `auth.spec.ts` 22 通过；F3 `player.spec.ts` 38 通过。
- 真实服务：`ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-poa scripts/playback-e2e.sh` 第一次 52 通过、4 失败（分析空闲等待 5 分钟，另三项是测试写法），修正后 56 通过，分析 `{"total":13,"analyzed":13,"failed":0}`。
- J4 合并后（`main`）：eslint 5 个整合文件；`npx vitest run src/features/player src/app src/shared/ui/tooltip.test.tsx src/features/library/api/decoders.test.ts` 66 个文件，1342 通过、12 失败（全是 `main` 上已有的旧断言，`a4ef27b` 修正后两个文件 34 通过）；Playwright `player/auth/a11y/responsive/personal/albums/governance/library` 382 通过、8 失败（设置分栏缺“播放”、播放栏曲名不再是链接、概况 dt 标签），修正后定向重跑 8 项通过；`go build ./...`、`go vet ./...`、`go test ./internal/library/... ./internal/playback/...`、`TestPackageDependencyDirections` 通过；`TestPostgreSQLCatalogStats` 与（经 `scripts/test-integration.sh`）`TestPostgreSQLAPIResponseSamples` PASS 非 SKIP。
- 没有跑全量测试：各步改动都能用对应的测试文件和场景局部覆盖。

未验证：

- 安卓锁屏下的后台播放与交叉渐变没有真机演练（规划时用户同意不做专门演练）。
- iPhone / iPad 上均衡与渐变不接入 Web Audio，按计划没有写模拟测试。
- 交叉渐变没有在浏览器里实际听过：mock 曲目只有 3 秒，达不到“曲目 ≥ 3 × 渐变时长”，由假 AudioContext 的引擎测试覆盖。
