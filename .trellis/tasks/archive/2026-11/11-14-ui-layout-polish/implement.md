# 执行计划：界面按钮布局与设置页整理

纯前端任务，按全局分工由主代理实现；测试改动与新增测试由独立的检查轮次完成（`trellis-check`），实现轮次不写新测试。
工作目录：独立分支 `feat/ui-layout-polish`（工作区已有其他任务的未提交改动，用独立 worktree，不覆盖）。工具链：`mise exec --`。

## 阶段与检查点

每个阶段结束：`npm run lint && npm run typecheck && npm run test`（frontend 目录）通过，截图自查（桌面 1440、手机 390），单独提交，便于回退。

### 阶段 1：共用控件

- [x] `MenuButton` 支持 `group`（分隔线、组标题、方向键跳过标题）。
- [x] 新增 `SettingsGroup`、`SettingRow`（含 `nested`、容器查询换行）；`Segmented` 在窄容器里改用原生 select。
- [x] 曲目行显隐样式（`hover: hover` 与 `pointer: fine`、`:focus-within`、触屏常驻）放进 `track-actions.module.css` 与 `release-media.module.css`。

### 阶段 2：专辑详情与曲目行

- [x] `AlbumHeader` 接管页头（h1、“‹ 专辑”、概况、操作行），`album-detail-view` 用 `Page header`。
- [x] 操作行：圆形播放、随机播放（新增）、收藏、添加到歌单、更多（下一首播放〔新增〕、加入队列、管理分组）。
- [x] `ReleaseAdminActions` 拆为菜单项＋对话框与提示节点；结果提示位置不变（操作行下方）。
- [x] 版本导航改为标签，“对比版本”在行尾。
- [x] 曲目行：序号位播放、悬停与聚焦显现、已收藏常驻、当前曲目音柱、双击播放（新增）；窄屏心形进“更多”。
- [x] 其他使用 `TrackActions` 的列表页一并核对显示。

### 阶段 3：播放栏与全屏播放器

- [x] `ModeControl` 拆为 `ShuffleButton`、`RepeatButton`（互斥，见 design.md 第 5 节）。
- [x] 播放栏三段布局、进度两端时间、右侧固定顺序、状态文字只在加载/缓冲/出错时显示。
- [x] 窄屏浮起迷你栏与底边进度线；底部留白仍按实际高度计算，不遮最后一行。
- [x] 全屏播放器换用两个模式按钮。
- [x] 全屏播放器：两栏结构（唱机＋控制台｜歌词/接下来）、收起移到左上、`UpNextList`、`LyricsPanel` 大字号外观、窄屏顶部与底部行、歌词按钮。
- [x] 信号路径：入口胶囊、弹层头部与削波条、步骤链（`stepText` 拆为步骤名与取值、最差一步加粗）、底部测量格、全屏深色面。

### 阶段 4：设置

- [x] 设置分栏：标题、“管理”分组、窄屏胶囊标签。
- [x] 播放设置分三组，从属项随开关隐藏，页首显示控制目标。
- [x] 扫描目录：扫描卡、音乐目录卡（添加目录对话框、行尾“更多”）、记录。
- [x] 成员、备份、账号、操作记录按卡片与行布局整理。

### 阶段 5：侧栏与列表页

- [x] 搜索入口样式与快捷键提示；有现成数据时显示待办数量。
- [x] `Cover` 增加 `thumb`，`PlaylistCover` 增加 `size="thumb"` 并导出；侧栏歌单名前显示封面，当前歌单高亮。
- [x] 列表页工具条统一。

### 阶段 6：测试、规范与构建产物（检查轮次）

- [x] 更新受影响的单元测试与 e2e（角色从 button 变为 menuitem、模式按钮拆分、页头结构），断言行为不变。
- [x] 新增测试：菜单分组、随机播放与下一首播放整张、双击播放（含链接与按钮内不触发）、随机/循环互斥、从属设置项随开关隐藏、触屏下动作常驻、信号路径链与测量格的缺项、侧栏歌单封面三种情形（4 张、1–3 张、空）、全屏“接下来”点播与随机提示、全屏无歌词时默认“接下来”、窄屏歌词按钮。
- [x] 更新 `frontend/DESIGN-components.md`、`frontend/DESIGN.md` 第 4 节、`.trellis/spec/frontend/component-catalog.md`、`player-design-guidelines.md`。
- [x] `npm run build` 更新 `backend/cmd/roomusic/web` 产物并提交。

## 验证命令

```bash
cd frontend
mise exec -- npm run lint
mise exec -- npm run typecheck
mise exec -- npm run test
mise exec -- npm run build
mise exec -- npx playwright test   # 全量 e2e，最后一轮跑一次
```

本机 headless Chromium 缺系统库：不装系统包，使用 `temp/ui-review/libs` 下解出的库（`LD_LIBRARY_PATH`）与私有 fontconfig（中文字体）运行 Playwright；该目录不提交。

## 风险与回滚点

- 测试面广（约 17 个单元测试文件、11 个 e2e 文件引用受影响的控件）：阶段 6 集中修改，前面各阶段只保证 lint 与类型检查通过、单元测试失败项都能归因到角色变化。
- 曲目行悬停显现可能影响 axe 与键盘用例：靠 `:focus-within` 保证聚焦可见，e2e 的焦点环检查覆盖。
- 回滚：按阶段提交，单个阶段 `git revert`；构建产物随最后一次提交生成。
