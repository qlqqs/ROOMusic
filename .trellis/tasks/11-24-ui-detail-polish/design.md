# 设计

只改前端，不动接口。按归属分到各 feature，跨 feature 的组合仍在 app 层。

## 专辑详情（features/catalog）

- `ReleaseMedia` 计算两件事并传给每个 `TrackList`：
  - `withArtist`：任一曲目的艺人非空且不等于专辑艺人，或艺人字段有修正/补全标记时为真；为假时不渲染艺人单元格，表头与网格少一栏。
  - 曲目行的作曲：`ownTrackCredits(credits, sharedTrackCredits(media).keys)` 里的作曲；`withComposer` 按去掉共同署名后的结果判断。
- 网格栏目由 `data-artist`、`data-composer`、`data-has-actions` 三个属性组合，CSS 写出全部组合；窄版（容器 < 640px）沿用“标题下一行”的写法。
- 窄版没有第二行时，标题跨两行网格居中（`:has` 判断第二行是否有可见内容）。
- 多碟：`TrackList` 增加 `showColumns`，只有第一张为真；“跳到”导航在 `media.length >= 3` 时出现。
- CUE：`ReleaseMedia` 判断全部曲目都是 CUE 分轨时行内不加标签；`AlbumHeader` 的概况在同样条件下加一项“CUE 分轨”。
- `AlbumHeader` 去掉 `playing` 属性与标记；调用方不再计算。
- `releaseFacts` 去掉流派；`ReleaseFacts` 没有条目时不渲染“发行信息”面板；`album-detail-view` 的两栏在侧栏没有内容时退成一栏（`:has`）。

## 卡片与封面

- `albumMeta` 写“N 张碟”“N 首”；卡片 `.meta` 一行省略；标题 clamp 2 行。
- `Cover` 没有封面时（card 与 detail）显示浅底与 `Disc3` 图标，不排标题；失败时图标加“封面无法加载”。

## 概览（app/routes 与各 feature 的摘要行）

- `ResumeCard` 在有当前曲目时返回 null。
- `RecentPlaysPanel` 没有记录时不渲染。
- 摘要行：`AttentionSummary` 有数字时不再写说明；`RootSummary` 没有目录时只写说明；`AnalysisSummary` 在 total 为 0 时不渲染。
- 新增 `OverviewEmptyState`（catalog）：资料库专辑数为 0 时在主栏说明，管理员带“添加音乐目录”链接。

## 音乐目录（features/library 与设置分栏）

- 设置分栏 label 与 `LibraryPage` 标题改为“音乐目录”；`RootSection` 移到 `ScanSection` 前面。
- `ScanSection` 读取目录列表，没有启用的目录时两个扫描按钮不可用，并说明原因。
- `RootStatusTag` 只在停用时渲染。
- 添加目录成功的提示（`RootCommandNotice`）带“开始扫描”按钮，复用扫描命令。
- `RootOperationsSection` 去掉版本号列；`scanPhaseNotes.succeeded` 改为“扫描完成。”。

## 搜索、表格与链接

- `ResultSections`：已到达的结果里有非空栏时隐藏空栏；全部为空时一句话。
- `TrackResults` 的动作列表头只给读屏。
- `matchNote`：命中原文与已显示文字按“/”“、”拆开后的任一部分相同也算已显示。
- `DataTable`：`.secondary` 单元格里的链接继承颜色、悬停才有下划线；有 secondary 列的表（曲目表）行标题用常规字重。
- 播放栏 `.subtitle a` 同样处理。

## 播放器

- `PlayerBar`：`PlayModeButton` 作为 `TransportControls` 的 `trailing`。
- 全屏 `TrackMeta`：专辑链接与间隔号加类名，窄屏隐藏（顶部已有专辑名）。

## 艺人

- 手机：`ArtistFilters` 与首字母导航外层在有字母索引时右侧留出索引宽度。
- 列表行的专辑数可见文字带“张”。
