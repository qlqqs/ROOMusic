# 技术设计：界面按钮布局与设置页整理

视觉依据：[research/mockup-v5.html](research/mockup-v5.html)（用户 2026-11-14 认可）。只改前端，不改后端协议与接口。

## 1. 边界与归属

| 区域 | 归属模块 | 主要文件 |
| --- | --- | --- |
| 共用控件 | `shared/ui` | `menu-button.tsx`（菜单分组）、`page.tsx`（自定义页头已支持 `header`）、新增 `settings-group.tsx`（设置分组卡片与行） |
| 专辑头部 | `features/catalog` 出结构，`app/routes` 组合动作 | `album-header.tsx`、`album-detail-view.tsx`、`app/routes/album-detail-page.tsx`、`features/player/components/album-playback-actions.tsx`、`features/governance/components/release-admin-actions.tsx`、`version-nav.tsx` |
| 曲目行 | `features/catalog` 行结构，`app/routes/track-actions.tsx` 动作 | `release-media.tsx`、`track-actions.tsx`，以及共用曲目行的历史、收藏、歌单、搜索、艺人页 |
| 播放栏 | `features/player` | `player-bar.tsx`、`player-controls.tsx`、`fullscreen-player.tsx`、对应 CSS |
| 设置 | `app/routes` 布局，各 feature 的区块 | `settings-layout.*`、`settings-sections.ts`、`playback-settings-form.tsx`、`features/library/components/*-section.tsx`、`root-table.tsx`、`add-root-form.tsx`、`member-management.tsx`、`features/system/components/backup-*`、`account-page.tsx` |
| 侧栏 | `app/shell` | `primary-nav.tsx`、`app-shell.tsx` |

依赖方向不变：player 与 personal 互不导入，仍由 app 组合（见 `.trellis/spec/frontend/directory-structure.md`）。

## 2. 共用控件

### 2.1 MenuButton 分组

`MenuAction` 增加可选的 `group?: string`。相邻且 group 相同的项为一组，组与组之间画分隔线，组名不为空时显示为组标题（`role="presentation"` 的文字，菜单项仍是 `menuitem`，方向键跳过标题）。没有 group 的旧调用方不受影响。

### 2.2 设置分组卡片

新增 `SettingsGroup`（`<section aria-labelledby>`，标题 h2＋卡片）与 `SettingRow`（左：名称、可选说明；右：控件区）。`SettingRow` 有 `nested` 外观（缩进、较小字号），用于从属项。名称用 `<label htmlFor>` 或 `aria-labelledby` 关联控件，说明用 `aria-describedby`。窄于 640px（容器查询）时控件区换到名称下方，分段控件（`Segmented`）在窄容器中改用原生 `<select>` 渲染同一组选项。

## 3. 专辑详情

- 页头：专辑详情改用 `Page` 的 `header` 插槽，由 `AlbumHeader` 渲染唯一的 h1（文字与 `title` 相同，文档标题不变）。封面在左，右侧依次是专辑名、专辑艺人、概况（一行，有人工修正标记时保留标记）、操作行；整体底对齐封面。“返回专辑列表”改为标题上方的“‹ 专辑”链接，读屏名保持“返回专辑列表”，恢复列表参数的逻辑不变。
- 操作行（从左到右）：
  1. 播放整张：墨色圆形图标按钮，读屏名与提示“播放整张专辑”（与现名一致）。
  2. 随机播放（新增）：次要文字按钮。行为：先 `engine.setMode("shuffle")`，再 `engine.playTracks(playable, 随机起始下标)`（`playTracks` 默认从第 0 首开始，随机起点避免每次都从第一首放起）；跳过不可播放曲目的提示与“播放整张”相同。
  3. 收藏（心形，已有 `FavoriteButton`）。
  4. 添加到歌单（只有图标，读屏名沿用“添加到歌单…”）。
  5. 更多（`MenuButton iconOnly`，读屏名“更多操作”）：
     - 无分组：下一首播放（新增，`engine.playNext(playable)`）、加入队列（原“加入队列”按钮移入）。
     - “管理”分组（仅管理员）：编辑发行信息…、设为默认版本 / 改回自动选择、归入其他专辑…、移出为独立专辑（多版本时）、查看问题（跳转 `/issues?release=`）。
  - 管理员动作的命令状态、对话框和结果提示仍由 `ReleaseAdminActions` 持有；它改为导出“菜单项列表＋对话框与提示节点”两部分，由页面把菜单项并入“更多”。结果提示显示在操作行下方。
  - 能力检查失败、全部不可播放等原因仍显示在操作行下方，空的状态段落不占位。
  - “可能接替了已不可见的版本”提示保留在操作行下方。
- 版本导航：改为横向标签（chip），当前版本高亮，“默认版本”标签保留；“对比版本”留在版本导航行尾（只在多版本时出现，不放进“更多”）。

## 4. 曲目行

- 结构：序号列 | 标题与署名 | 动作 | 时长。
- 指针设备（`@media (hover: hover) and (pointer: fine)`）：
  - 平时只显示序号、标题、署名、时长；已收藏的心形常驻（实心金色）。
  - 行悬停或行内任一控件获得焦点（`:hover`、`:focus-within`）时：序号位置显示播放按钮（就是原来的行内播放按钮，读屏名不变，只是换位置），心形与“更多”出现。
  - 双击行（新增）：等同于点行内播放按钮；双击发生在链接、按钮或可选中文本拖选时不触发；不可播放时无动作。
- 触屏或无悬停：播放按钮在序号位置常驻，心形与“更多”常驻（窄屏只留“更多”，心形进“更多”菜单的“收藏 / 取消收藏”项）。
- 当前播放曲目：序号换成三根音柱（`prefers-reduced-motion` 时静止），标题用 `--text-accent`，现有的读屏标记（`currentTrackLabel`）不变。
- 隐藏方式用 `opacity` 与 `pointer-events`，不用 `display:none`，保证键盘 Tab 仍能到达并在聚焦时显现。
- 表格式列表（收藏、历史、歌单、搜索、艺人页）复用同一套 `TrackActions` 显隐规则。

## 5. 播放栏

- 三段：左（封面、标题、艺人 — 专辑、心形），中（随机、上一首、播放、下一首、循环；下方进度：位置｜波形或进度条｜时长），右（格式与信号路径胶囊、设备、队列、分隔线、静音与音量条）。
- 播放模式拆成两个按钮，底层仍是同一个四选一模式（`playModes`，服务端协议不变）：
  - 随机按钮：`aria-pressed = mode === "shuffle"`；按下在 `shuffle` 与 `sequential` 之间切换。
  - 循环按钮：读屏名“循环：关 / 列表循环 / 单曲循环”，`aria-pressed` 在非“关”时为真；按下依次 `sequential → repeat_all → repeat_one → sequential`；当前是 `shuffle` 时按下进入 `repeat_all`。
  - 两者互斥（用户认可）。启用时图标用强调色，下方 4px 金色圆点。
  - 全屏播放器同样换成两个按钮。
- 状态文字（“正在播放”“已暂停”）不再常显，只在加载、缓冲、出错时出现在格式胶囊里。
- 信号路径（用户认可，见对照稿“信号路径”视图）：
  - 入口：`SignalPathButton` 与格式文字合并为一个按钮，可见文字为 `sourceFormat(track)`，前面是质量色圆点；可访问名“FLAC 24/96，信号路径：无损”（包含可见文字，满足名称含可见标签），削波时加“，削波”。没有 `signal` 时只显示格式文字（不是按钮）。圆点动画用 CSS：`data-state="breathing"`（`snapshot.status` 为播放中）4s 无限循环，分段缓动：0%→38% 吸气（`cubic-bezier(.37,0,.63,1)`，scale 0.78→1、opacity 0.55→1），38%→88% 呼气（`cubic-bezier(.45,.05,.3,1)`，回到 0.78 / 0.55），88%→100% 停顿；柔光是圆点的 `::after` 独立层（同色，8%→42% 由 scale 1→1.9、opacity 0→0.26，42%→80% 扩到 2.6 并淡出），只动 `transform` 与 `opacity`；`data-clipping` 时 0.9s 警示色脉动；其余状态静止；`@media (prefers-reduced-motion: reduce)` 下取消全部动画。动画只作用于 8px 圆点，不影响布局与文字。
  - 弹层：头部质量词与图标（颜色沿用四档质量色），副标题“来源格式 → 设备名”（`snapshot.device.name`）；`clipping` 时一条“正在削波”。
  - 步骤链：`stepText` 拆为步骤名（来源、采样率转换、DSD 转 PCM、降混/声道选择、参数均衡、卷积、电平化、Headroom、抖动、编码）与取值两部分；节点颜色按该步 `quality`；与 `signal.quality` 相同且是链上第一个最差的那一步加粗。末尾是输出设备节点（只写设备名，协议没有输出格式字段）。
  - 测量格：响度、范围、峰值（`useTrackAnalysis`）与处理速度（`signal.processingSpeed`），缺项不渲染，全缺时整行不渲染。
  - 宽 340px；全屏播放器内用深色面变量；窄屏底部弹出不变。仍是 `ModalDialog`，标题 `h2`、关闭按钮与焦点归还不变。
- 窄屏（<768px）：浮起的迷你栏（圆角、阴影，左右下各留 8px，底部安全区照旧由实际高度计算），内容为封面、标题＋设备名、播放、下一首、队列，底边 2px 进度线；心形、模式、音量在全屏播放器里。

## 5a. 全屏播放器（R6）

- 结构：`FullscreenPlayer` 的 `dock` 拆掉，宽屏 `stage` 改为两栏：左栏 `VinylDeck`＋`NowPlayingInfo`（标题 h2、心形插槽、更多菜单〔转到专辑、转到艺人，沿用已有链接〕、专辑 · 艺人、`SignalPathButton` 胶囊、`DeviceButton` 胶囊样式）＋`WaveformSeek layout="ends"`＋`TransportControls size="large"`（含拆分后的随机与循环）＋`VolumeControl`；右栏 `FullscreenPanel`（分段切换“歌词 / 接下来”，切换状态是组件内局部状态，每次打开时有歌词默认歌词，否则默认接下来）。
- 顶栏：左上收起（`collapseRef` 初始焦点不变），右上 `QueueButton`。
- 歌词：`LyricsPanel` 增加 `size="large"` 外观（当前行 32px、其余 24px，已唱行更暗，`mask-image` 渐隐，滚动锚点约 35% 高度）；点行跳转、键盘方向键与 `aria` 行为不变。
- 接下来：新组件 `UpNextList`，数据 `upNext(snapshot.queue, 20)`，每项是按钮（读屏名“播放《标题》”），点按 `engine.playEntry(entryId)`；随机模式下列表上方一行“随机播放中，顺序以实际播放为准”。为空时写“队列里没有接下来的曲目”。
- 窄屏：保持单栏；顶部三项；底部行 `DeviceButton`（显示设备名）左、歌词切换按钮（`aria-pressed`）与 `QueueButton` 右。歌词视图用小封面行替代唱片，控制区 DOM 不随视图变化，位置不动。点唱片切歌词的旧交互保留。
- 颜色：沿用全屏现有深色面变量；新加的强调色用于已开启模式与当前歌词行以外不新增。

## 6. 设置

- 布局：保留设置分栏（176px），栏首“设置”标题加大；管理员栏目归入“管理”小标题下（`settings-sections.ts` 增加分组字段）。内容区最大宽度 720px（扫描目录 820px，含表格）。窄屏横向标签改为可横向滑动的胶囊样式（仍是链接加 `aria-current`）。
- 播放：三组——播放（串流音质、无缝播放、交叉渐变＞渐变时长、音量均衡），输出（采样率转换＞目标采样率、转换滤波器、多声道降为双声道），音效处理（参数均衡＋“编辑…”、卷积＋IR 选择＋“管理文件…”、处理顺序＋“对调”、Headroom＞增益、削波指示）。从属项在开关关闭时不渲染（现在是禁用）。页首写明作用于哪台设备（控制目标名，取自现有 `ControlTargetLine` 的数据）。
- 扫描目录：扫描卡（上次扫描状态与摘要、“查看详情”、“立即扫描”主按钮；定时扫描行）→ 音乐目录卡（卡头“添加目录…”打开对话框，原 `add-root-form` 移入对话框；每个目录一行：名称、路径、状态、行尾“更多”菜单收纳原图标动作）→ 扫描记录 → 目录操作记录。
- 成员、备份、账号、操作记录：按同样的分组卡片与行布局整理，表格保留，表格内纯图标动作改为文字按钮或行尾“更多”。
- 文案：说明文字按功能实际含义写，不写对照稿里的示意句。

## 7. 侧栏与列表页

- 搜索入口改为浅底，右侧显示快捷键 `/`（快捷键已有，见 `global-search-shortcut`）。
- 待办类入口（元数据、待认领）在有数量时显示数字，数据来自现有查询；没有现成计数接口的不显示。
- 用户菜单、歌单展开保持现有行为，统一行高与悬停样式。
- 歌单封面：`features/catalog` 的 `Cover` 增加 `variant="thumb"`（无裱边、`--radius-sm` 圆角、1px 分隔线描边、`loading="lazy"`、装饰性 `aria-hidden`；没有封面或加载失败时显示 `ListMusic` 图标，不显示标题文字版面，也没有重试按钮）。`PlaylistCover` 增加 `size="thumb"`，方格间隙 1px，从 `features/personal` 的公开入口导出；`PlaylistLinks` 在名称前渲染它（26px）。歌单项不缩进：与一级导航同一左内边距（封面因比 18px 图标宽，左内边距相应减小，使封面与图标列居中对齐），名称起点与一级文字基本对齐；“全部歌单”的文字与歌单名对齐。数据仍是 `usePlaylists(1)` 的同一份结果，不新增请求。链接的可访问名仍是歌单名。
- 专辑列表、艺人、收藏、历史、歌单列表页：工具条（搜索、筛选、排序）同一行、同一高度，主要按钮每页最多一个。

## 8. 兼容与测试影响

- 读屏名不变的控件：播放整张专辑、从这首开始播放、更多操作、收藏《…》、添加到歌单…、加入队列（变为菜单项）、编辑发行信息、设为默认版本、归入其他专辑…、移出为独立专辑、查看问题。
- 角色变化：加入队列和管理员动作从 `button` 变为 `menuitem`；“播放模式：…”拆成“随机播放”与“循环：…”。受影响的测试（单元：`app/album-detail`、`default-version`、`release-corrections`、`release-grouping`、`issues`、`claim-flow`、`library-roots`、`add-to-playlist` 等；e2e：`albums`、`governance`、`default-version`、`player`、`keyboard`、`library`、`a11y`、`personal`、`dsp-pipeline`、`zones-devices`）；信号路径按钮的可访问名由“信号路径：…”变为“格式，信号路径：…”，按名称查找它的测试改为部分匹配同步改为新角色与新入口，断言的行为不变。
- 设计文档 `frontend/DESIGN-components.md`（按钮与曲目行、播放条、新建成员等条目）与 `.trellis/spec/frontend/component-catalog.md`、`player-design-guidelines.md` 同步更新。

## 9. 取舍

- 随机与循环互斥：保持服务端四选一模式，不改协议；代价是不能“随机＋循环”同时生效。
- 悬停才显现动作：仅限精确指针设备；触屏常驻，键盘聚焦显现。
- 不新增依赖，不引入网络字体。

## 10. 回滚

按区域分阶段提交（见 implement.md），每个阶段可单独回退；构建产物 `backend/cmd/roomusic/web` 随源码提交，回退时一并回退。
