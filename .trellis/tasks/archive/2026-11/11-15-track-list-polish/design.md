# 设计

## 右键菜单

- `shared/ui/menu-button.tsx` 的 `useMenu` 增加按坐标打开：`openAt({ x, y }, actions)`，菜单定位沿用现有 `Position` 计算（贴边翻转），焦点进入第一项、Esc 关闭后焦点回到触发行。
- 新增 `shared/ui/context-menu.tsx`：`useRowContextMenu(getActions)` 返回 `onContextMenu`、`onKeyDown`（ContextMenu 键、Shift+F10）与 `menu` 节点；事件目标在输入框或有文字选区时不拦截。
- `app/routes/track-actions.tsx`：把动作列表抽成 `useTrackMenuActions(track, …)`，行尾“⋯”和右键共用；右键版本最前加“播放”。专辑页额外加“曲目信息”。

## 分栏与一行

- `shared/ui/data-table.tsx`（搜索、历史、艺人页）与 `features/personal/components/track-table.tsx`（收藏、歌单）各加“艺人”列；标题单元格不再渲染艺人。窄容器折叠沿用两者现有的 subgrid / stacked 机制，第二行拼“艺人 · 专辑”。
- 专辑页 `features/catalog/components/release-media.tsx` 改为与 `TrackTable` 同样的 `role="table"` + subgrid 结构：列 # / 标题 / 艺人 / 作曲（`hasComposer`）/ 操作 / 时长；作品组是跨全列的分组行。删除 `CreditsLine` 在行内的使用（函数移给曲目信息块）。
- 双击播放、定位高亮、正在播放律动标记、CUE 标识保持。

## 选中与曲目信息

- `album-detail-view.tsx` 持有 `selectedTrackId`（组件状态，不进 URL）；`ReleaseMedia` 行 `onClick`（非交互目标、无文字选区）切换选中，`aria-selected` 写在行上，表格 `aria-multiselectable="false"`。
- `release-facts.tsx`：发行信息 → 制作人员（过滤 `album_artist`）→ `TrackInfo`（新组件，`features/catalog/components/track-info.tsx`）。署名用 `ownTrackCredits` 再去掉名字已在曲目艺人 `artist_refs` 中的演奏者；格式与响度取曲目已有字段与 `useTrackAnalysis`（来自 player 的不可直接导入：由 app 经插槽注入响度，catalog 不依赖 player）。
- Esc：选中存在时在页面级监听，焦点在菜单或对话框内时不处理。
- 窄版面：`useMediaQuery` 判断右栏是否在下方，是则 `TrackInfo` 放进底部 `ModalDialog`。

## 风险

- 专辑页表格结构改动最大，现有组件测试与 e2e（album、responsive、a11y、personal）选择器需更新。
- 右键拦截只挂在曲目行，避免影响其他区域。
