# 界面布局与交互约定（2026-11-14）

按钮布局、设置页、侧栏、专辑详情、播放栏、信号路径与全屏播放器的整理结果。设计依据是任务 `11-14-ui-layout-polish` 的定稿稿子（参考 Apple Music、Spotify、Roon 与系统设置），
设计层面的说明在 [DESIGN-components.md](../../../frontend/DESIGN-components.md)“界面整理（2026-11-14）”。本文件只写已实现的合同；与旧规范冲突时以本文件为准，
旧条目已在[播放器规范](./player-design-guidelines.md)与[组件目录](./component-catalog.md)里标注。

## 共用控件（`shared/ui`）

| 组件 | 约定 |
| --- | --- |
| `MenuAction.group` | 相邻且 `group` 相同的项为一组，组间一条 `role="separator"`；有名字的组包在 `role="group"` 里，组标题（`aria-hidden` 的小号灰字，经 `aria-labelledby` 作为组名，避免读两遍）是它的名称，不可聚焦，方向键跳过。不给 `group` 的项结构与以前相同 |
| `MenuAction.hint`、`untitledGroup` | `hint` 是右侧说明，也是菜单项名称的一部分，与标签之间保留空格；`untitledGroup` 保留组间分隔线但不显示组标题 |
| `FilterField`、`FilterChip`、`SortMenu` | 列表筛选共用控件：250ms 后提交，首次 push、后续 replace；清空与失焦结束输入过程，前进后退由地址同步草稿。校验失败就近提示且不写地址。胶囊用 `aria-pressed`，排序菜单显示当前项；均不发请求、不判断角色 |
| `SettingsGroup` | `section` + `h2` 标题 + 卡片；可给标题旁的操作、标题 ref 与卡片下方的脚注。卡片宽度用 ResizeObserver 量出，经 context 告诉里面的 `Segmented` 是否窄（< 640px，与 CSS 里的 639px 成对修改）；没有 ResizeObserver（jsdom）时按宽处理 |
| `SettingRow` | 左名称（可带一行说明）、右控件右对齐；名称是指向 `controlId` 的 `<label>`，子内容可以是 `(ids) => ReactNode` 拿到 `controlId`/`labelId`/`descriptionId`。控件是按钮或只读值时用 `plainLabel`（`<label>` 指向按钮会改掉按钮的名称）。`nested` 是从属项：缩进、字号小一级；所属开关关闭时**由调用方不渲染**这一行，不做禁用态。容器查询：卡片窄时控件放不下才换到下一行 |
| `Segmented` | 宽卡片里是 `role="radiogroup"` 的相连分段，窄卡片里是同名同值的原生 `<select>`（`aria-labelledby` 指向行名称）。用量宽切换而不是 CSS 同时渲染两份，避免读屏与测试看到重复控件 |
| `Switch` | 开关（`role="switch"`），名称来自 `SettingRow` 的 label |
| `TextField labelHidden` | label 视觉隐藏但仍是输入框的名称；用于单行工具条，可见提示改为 placeholder |
| `useMediaQuery`（`shared/lib`） | 同一个查询共用一个 `MediaQueryList`，列表每行订阅不产生多个监听 |

## 曲目行（专辑页与各列表页）

- 动作显隐只在 `@media (hover: hover) and (pointer: fine)` 下生效：行内动作按钮 `opacity: 0` + `pointer-events: none`，行悬停或 `:focus-within` 时显现；
  行是最近的 `[role="row"]`（表格式列表）或 `[data-track-row]`（专辑曲目）。已收藏的心形（`aria-pressed="true"`）、展开中的“更多”（`aria-expanded="true"`）、
  等待确认的心形（`aria-busy`）常驻。触屏不隐藏任何动作。不用 `:has()`（目标浏览器含 Firefox 113）。淡入 0.12s，`prefers-reduced-motion` 下无过渡。
- 专辑页：带 `data-track-play` 的播放按钮放在序号位（`TrackActions playPlacement="lead"`），悬停或聚焦时盖住序号；触屏常驻。当前曲目的序号位是音柱（减少动态效果时静止），
  标题用强调色，“正在播放 / 已暂停”只给读屏。双击行等于点它的播放按钮；双击发生在链接或按钮里时不触发，双击不选中文字。
- 窄屏（< 767px）心形收进行的“更多”，菜单项是“收藏 / 取消收藏”。结果不确定且重读失败时，菜单项不可用，菜单旁出现与心形相同的“重新读取”（`useFavoriteMenuAction` 返回 `{ action, retry }`，`data-favorite-retry` 常驻）。其他列表页（历史、搜索、艺人、收藏、歌单）播放按钮仍在心形旁，显隐规则相同。

## 专辑详情

- `AlbumHeader` 经 `Page header` 渲染整个页头：“‹ 专辑”返回链接（读屏名“返回专辑列表”）、封面、`h1`、专辑艺人、概况行、操作行、需要关注行；文字与封面底边对齐。
- 当前曲目与详情同一 `releaseId` 或 `groupId` 时，大按钮按播放区 `intent` 显示“暂停播放 / 继续播放”，调用 `engine.pause()` / `engine.play()`，保留队列、当前曲目与位置；其他专辑仍是从头播放整张。
- 手机封面宽度约 250px 并居中，标题、概况、共同署名与操作行一起居中。共同署名的提取规则见[组件目录](./component-catalog.md)。
- 操作行顺序固定：圆形播放（状态名称见上）、随机播放、心形、添加到歌单（只有图标）、更多。键盘顺序与视觉顺序一致，窄屏不重排。
- 随机播放（`useAlbumPlayback().shuffle`）：先检查队列上限，再 `setMode("shuffle")`，再 `playTracks(可播放曲目, 随机起始下标)`——`playTracks` 默认从 0 开始，必须传随机下标。
- “更多”菜单：下一首播放（`playNext(整张)`）、加入队列；管理员另有“管理”组（编辑发行信息、设为默认版本 / 改回自动选择、归入其他专辑…、移出为独立专辑、查看问题）。
  `useReleaseAdminActions` 返回 `{ actions, panel }`：`actions` 进菜单，`panel`（认领提示、结果提示、对话框）显示在操作行下方，结果提示位置不变。
  组合在 app（`album-detail-page.tsx`）：player、personal、governance 各出钩子，互不导入。
- 版本导航是标签（链接加 `aria-current`），“对比版本”在行尾；“版本”`h2` 只给读屏。标题不同时标签里只显示一行，完整标题在链接名称里。

## 播放栏

- 三段：左封面（打开全屏）、标题、心形；中传输键与两端时间的进度；右侧固定顺序：信号路径胶囊（加载、缓冲状态紧跟其后）、设备、队列、分隔线、音量。列宽 `1fr / minmax(240px, 2fr) / minmax(max-content, 1fr)`，中段最宽 560px。
- 播放模式拆成两个按钮，四种模式仍互斥：`ShuffleButton`（“随机播放”，`aria-pressed`，shuffle ↔ sequential）、`RepeatButton`（名称随状态“循环：关 / 列表循环 / 单曲循环”，
  sequential → repeat_all → repeat_one → sequential；当前是 shuffle 时按下进入 repeat_all）。开启时强调色加 4px 圆点。`TransportControls modes` 把两者放在传输键两侧。
- 状态文字只在加载、缓冲时出现（在格式胶囊旁）；错误仍在标题下（`PlayerFaultInline`）。
- 窄屏（< 768px）是浮起的迷你栏，底边一条进度线；外层透明并让点击穿过，底部留白仍按实际高度计算，不遮最后一行。设备按钮在迷你栏里隐藏，切换设备在全屏底部行。

## 信号路径

- 入口 `SignalPathButton` 是“圆点 + 格式文字”的胶囊，名称“FLAC 24/96，信号路径：增强”（含可见文字），削波时加“，削波”；没有 `signal` 时只是格式文字，不是按钮。
  弹层的可访问名称仍是“信号路径：…”。
- `StatusTag` 的中性色字用 `--tag-neutral-text`（默认 `--color-neutral-800`），全屏深色面改写为 `--color-neutral-200`，从全屏打开的设备列表里的“本机”标签才有足够对比度。
- 圆点颜色按整体评级（无损 `--text-accent`、增强 `--color-signal-enhanced`、高质量 `--color-signal-high`、有损 `--text-secondary`），削波 `--color-warning`。
- 呼吸：只在 `playing` 时动，暂停、缓冲、出错静止。4s 一轮：0→38% 吸气（`cubic-bezier(.37,0,.63,1)`，scale .78→1、opacity .55→1），38→88% 呼气（`cubic-bezier(.45,.05,.3,1)`），88→100% 停顿；
  `::after` 光晕独立一层（8→42% scale 1→1.9、opacity 0→.26，42→80% 扩到 2.6 并淡出）。削波是红色约 0.9s 的脉冲（同样只用 transform 与 opacity）。只动画 transform 与 opacity；
  `prefers-reduced-motion` 下没有动画。
- 弹层宽 340px：头部是评级图标、评级名与“来源 → 设备”摘要；步骤链每步拆成步骤名与取值（`stepParts`），最差的一步加粗（整体无损时不加粗），最后一个节点是“输出：设备名”；
  底部测量格两列：响度、范围、峰值（`useTrackAnalysis`）与处理速度，缺的项不出现。全屏里用深色面。

## 全屏播放器

- 宽屏两栏：左唱机，下面是控制台（标题与心形、“更多”、“专辑 · 艺人”、格式胶囊与设备胶囊、两端时间的波形进度、随机 / 传输 / 循环、音量）；右栏 `歌词 / 接下来` 切换，
  有歌词时默认歌词，没有歌词时默认“接下来”。收起按钮在左上，队列在右上。“更多”有“转到专辑”和每位艺人一项“转到艺人：名字”（艺人链接可用时）。
- `LyricsPanel size="large"`：当前行 32px、其余 24px，已唱行更暗，渐隐更长，当前行停在约 35% 高度。字号变化不做动画，只有颜色过渡（取代旧规则“高亮不改字号”）。
- `UpNextList`：基于 `upNext(queue, 20)`，每项是按钮“播放《标题》”，点了 `playEntry`；随机模式下显示“随机播放中…”提示；空时“队列里没有接下来的曲目”。
- 窄屏：顶部行（收起、“正在播放”与专辑名、更多），唱机本身是“显示歌词”按钮，底部行（设备胶囊、“歌词”开关 `aria-pressed`、队列）。不改唱机动画本身。

## 设置

- 分栏保持 176px，顶部“设置”标题；管理员的扫描目录、成员、操作记录、备份在“管理”小标题下的另一个列表。< 1024px 时变成一行可横向滚动的胶囊标签，不显示小标题。
  内容最宽 720px（扫描目录与扫描详情 820px）。
- 播放设置分“播放 / 输出 / 音效处理”三组；页首写“这些设置作用于正在控制的设备：设备名”。渐变时长、目标采样率、增益是从属行，所属开关或选项关闭时不渲染。
  削波指示不是 Headroom 的从属项（后端不依赖 Headroom 使用它）。“音质”改名“串流音质”。处理顺序是一个“对调”按钮（名称“对调处理顺序”）加“卷积 → 参数均衡”链。
- 扫描目录：扫描卡（当前状态、完整重扫、主要按钮“开始扫描”，每天自动扫描一行）、音乐目录卡（“添加目录…”打开对话框；每行图标、名称、状态与“目录 {名} 的更多操作”菜单：
  停用目录 / 恢复目录 / 改路径… / 不扫描的文件夹…；列表是 `<ul aria-label="音乐目录列表">`）、扫描记录与目录操作记录卡。
- 账号、成员、备份、操作记录按同样的卡片与行排；“新建成员”“立即备份”在卡片标题旁。

## 侧栏与列表页

- 搜索框浅底无边框，右侧 `/` 提示（真实快捷键，`aria-keyshortcuts="/"`）；只在精确指针、输入框为空且未聚焦时显示。
- “元数据”“待认领”只在本会话已经读过对应列表时显示数字（`useCachedTodoCounts` 只读查询缓存，不发请求）；0 不显示；数字经描述给读屏（“N 个待处理的问题”），链接名称不变。
- 歌单行在名称前有 26px 封面（`PlaylistCover size="thumb"`，`Cover variant="thumb"`：无裱边、懒加载、`aria-hidden`）：4 组封面拼 2×2，1–3 组用第一张，没有时是歌单图标。
  行与主导航左对齐不缩进，当前歌单高亮；数据就是 `usePlaylists(1)` 的结果。导航列用 `minmax(0, 1fr)`，长名称省略号截断。
- 专辑与艺人列表用 `FilterField` 即时筛选，去掉提交按钮；关注与角色用胶囊、排序用菜单，控件同高，窄屏按组换行；Tab 顺序不变。
- 曲目“更多”中的播放与加入歌单项带图标；有可用引用时追加“前往艺人”（第一位），其他专辑的曲目有“前往专辑”（携带 track 定位）；当前专辑页不重复提供自己的专辑入口。
- 验证重点：`filter-field.test.tsx` 与列表页面测试覆盖一次输入一条历史、清空、超长与后退同步；`player-ui.test.tsx` 覆盖同组其他版本、暂停继续保持第二首；`track-credits.test.ts` 覆盖单首不上提、跨介质共同署名与不同署名保留。
