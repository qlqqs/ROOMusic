# 技术设计：歌单与专辑页面布局统一、图标化按钮与管理员改密

> 需求与验收见同目录 [prd.md](./prd.md)（范围已由用户拍板，本文不改动范围）。本文只写边界、合同、文件落点与取舍，不写大段代码。
> 依据的规范：`.trellis/spec/frontend/{component-guidelines,personal-guidelines,directory-structure,quality-guidelines}.md`、
> `.trellis/spec/backend/identity-contracts.md`、`frontend/DESIGN.md`、`frontend/DESIGN-components.md`。

## 0. 通用约束（每个实现子任务都适用）

- **界面文案**：不写防御性说明（权限、隔离、实现细节），去掉标题、按钮和数据本身已经说清楚的说明；确实需要的说明用一句日常的话。
  本任务新增的可见文字只有：“歌曲 N”、“YYYY-MM-DD 创建”、悬停提示（即原有的按钮名）、概况值。不新增任何说明段落。
- **视觉偏好**：无衬线、紧凑字号（正文 13–14px，标题沿用 `--fs-24`/`--fs-20`）、图标按钮无边框（`Button variant="icon"` 现有外观）。
  只用 `styles/tokens.css` 已有变量，不新增设计变量（渐变色用 CSS 自定义属性在组件内局部设置）。
- **测试**：只测改动直接影响的部分，复用已有结果，不为假想场景写测试，不堆防御代码与回退分支（见 §6）。
- **依赖方向**：`shared` 不导入 feature；`personal` 只导入 `shared`、`auth` 的会话范围与 `catalog` 的公开合同；`catalog` 不导入 `personal`/`governance`；
  player 与 personal 互不导入，跨 feature 的组合放在 `app`。

---

## 1. R1 管理员改密免当前密码（前后端接口合同）

### 1.1 接口合同（`POST /api/v1/auth/password`）

| 项 | 合同 |
| --- | --- |
| 请求 | `{ "current_password"?: string, "new_password": string }`。调用者是**管理员**时 `current_password` 可省略，带了也被忽略（不核对、不计入限速）；**成员**仍必填，省略按空串处理，结果是 `403 current_password_incorrect`（与现状一致） |
| 成功 | `200 { "password_changed": true }`，不重设 Cookie；当前会话保留（令牌与到期时间不变），本人其他会话全部撤销（两种角色相同） |
| 新密码不合规 | `400 invalid_input`，文案不变；先于其他检查，不调用 bcrypt、不写库（两种角色相同） |
| 当前密码错误 / 并发中被别处改过 | `403 current_password_incorrect`——**只有成员**会得到 |
| 当前密码核对限速 | `429 too_many_attempts` + `Retry-After`——**只有成员**会得到 |
| 无会话 / 会话失效 / 用户停用 / 来源不合法 | `401 unauthorized` / `403 origin_required` / `403 origin_forbidden`，不变 |
| bcrypt 失败 / 数据库错误 | `500 internal_error` / `503 database_unavailable`，不变 |

错误码集合不新增、不改名。`POST /users/{id}/password`（管理员重置成员）不变，对自己仍是 `403 self_password_reset`。

### 1.2 后端改法（`backend/internal/identity`）

**选择：新增函数 `ChangeAdminOwnPassword`，不给 `ChangeOwnPassword` 加参数。** 理由：
- 成员路径的“读哈希 → 限速计数 → 核对 → 事务内复核哈希”是一个整体，加一个 `verify bool` 会让 `throttle` 变成可空、复核分支变成条件分支，读代码时要同时推演两条路径；
- 新函数让“不核对当前密码”在调用点显式可见，`ChangeOwnPassword` 及其全部现有测试原样不动。

`identity/passwords.go`：
- 新增 `ChangeAdminOwnPassword(ctx, db, userID string, sessionTokenHash []byte, next string) error`：
  `ValidPassword(next)` 不合规 → `ErrInvalidPassword`；事务外 `HashPassword`（失败 → `*PasswordHashError`）；事务内锁用户行
  （`SELECT 1 FROM users WHERE id=$1::uuid AND disabled_at IS NULL FOR UPDATE`，无行 → `ErrSessionEnded`）、锁当前会话行（与 `ChangeOwnPassword` 同一条 SQL，无行 → `ErrSessionEnded`），
  然后 `setPasswordTx(ctx, tx, userID, hash, sessionTokenHash)`、提交。不读旧哈希、不调用 `LoginThrottle`、不比较哈希。
- 锁会话行的那段 SQL 抽成包内小函数（如 `lockOwnSessionTx`），两个函数共用；其余不动。
- 并发：同一会话两次请求串行成功（后写为准）；两个不同会话并发时，先提交的撤销了另一个会话，后者锁会话时得到 `ErrSessionEnded` → 401。这是自然结果，不另加处理。
- 不在事务里复核角色：与 `RequireAdmin` 的所有管理接口一致，角色以 `Authenticate` 读到的为准（当前也没有改角色的接口）。

`identity/httpapi/passwords.go` 的 `changePassword`：`input` 结构不变；按 `user.Role == "admin"`（与 `guard.go` 的判断同一个字面量）分派：
管理员调用 `ChangeAdminOwnPassword(..., input.NewPassword)`，成员调用原 `ChangeOwnPassword`。`writePasswordError` 不变。

### 1.3 `identity-contracts.md` 要改的条目

- §2 接口签名“自助改密码”：写明 `current_password` 对管理员可省略、被忽略。
- §3“改密码与重置密码”：业务函数列表加 `ChangeAdminOwnPassword`（不核对、不限速、仍保留当前会话撤销其他会话）；限速一条注明“只用于成员”；
  “对自己调用返回 `403 self_password_reset`”一条的理由改为“管理员改自己的密码走 `/auth/password`（不需要当前密码），重置端点只管其他成员”，
  原理由“被盗用的管理员会话不能把管理员锁在门外”删除（用户已接受这个取舍；被锁在门外时用 `roomusic reset-password` 恢复）。
- §4 错误矩阵：“自助改密码：当前密码错误……”与“登录/当前密码核对限速”两行加“（成员）”；新增一行“自助改密码（管理员）：不核对当前密码，带或不带 `current_password` 都按新密码规则处理”。
- §6 必需测试：加“管理员不带 / 带错 `current_password` 都 200，当前会话保留、其他会话撤销”。

### 1.4 前端改法（`frontend/src/features/auth`）

- `api/endpoints.ts`：`PasswordChange = { currentPassword: string | null; newPassword: string }`；`null` 时请求体只有 `{ new_password }`（不发 `current_password` 字段）。
- `account-rules.ts`：`PasswordChangeInput.current` 改为 `string | null`；`validatePasswordChange` 只在 `current !== null && current === ""` 时报“请输入当前密码。”。
- `components/change-password-dialog.tsx`：`ChangePasswordButton` 已经取了 `viewer`，把 `requiresCurrent = viewer.role !== "admin"` 传给对话框；
  `requiresCurrent` 为假时不渲染“当前密码”字段（连同 `currentRef` 的聚焦分支），提交 `currentPassword: null`。其余（新密码、确认、成功态、失败态、隐藏 username 字段）不变。
  `403 current_password_incorrect` 与 429 文案的分支保留（只有成员会走到）。对话框导语“改好后，其他设备需要重新登录。”保留（是必要的一句）。
- 不改 `members`：重置成员密码流程不变。

---

## 2. R2 统一曲目列表（我喜欢的音乐、歌单详情）

### 2.1 组件归属与依赖

- 新建 `features/personal/components/track-table.tsx`（+ `track-table.module.css`），导出 `TrackTable`（表格外壳与表头）和 `TrackTableRow`（一行）。
  **放在 personal 而不是 catalog/shared**：两个真实消费者都在 personal；专辑页曲目列表不变、不复用它；规范要求“至少两处使用且无策略”才进 shared，
  而它依赖 catalog 的曲目摘要显示规则（`trackTitleOf`、`albumArtistOf`、`albumArtistRefsOf`、`ArtistNames`），放 shared 会反向依赖 feature。
- 行内动作仍由 app 经插槽给（`renderTrackActions` 返回的 `TrackActions`：播放、心形、“更多”），表格只负责放置；app 层不变。
- `albumArtistOf` 已由 catalog 公开入口导出；`albumArtistRefsOf` 在 `catalog/index.ts` 已导出。无需新增 catalog 导出。

### 2.2 结构：带 ARIA 表格角色的 CSS Grid（不用 `DataTable`，也不用 `<ol>`）

- 理由：`DataTable` 窄屏堆叠成“标签：值”，与 PRD 的窄屏折叠方式（标题 + 次要信息 + 操作）不符；`<table>` 行加 `display:grid` 会在 Safari 丢语义，`<tr>` 上的 dnd-kit 位移也不可靠；
  `<ol>` 没有列头语义。用 `div` + 显式 `role="table" | "rowgroup" | "row" | "columnheader" | "rowheader" | "cell"`（与 `DataTable` 显式写角色的做法一致），
  既有列头可读，又能用 grid/subgrid 对齐、容器查询折叠、dnd-kit 位移。
- 现有测试按 `getByRole("table", { name: "收藏的曲目" })`、`row`、`rowheader` 查询的写法因此保持可用。
- 对齐：外壳 `display:grid` 定义列轨道，表头行与每一行 `grid-column: 1 / -1; display:grid; grid-template-columns: subgrid`，
  所以操作列（偶尔多出“重新读取”）等 `auto` 列按全表最宽者对齐。

### 2.3 列定义

| 列 | 表头（可见 / 读屏） | 内容 | 宽度（宽容器） |
| --- | --- | --- | --- |
| 把手（仅歌单） | 无 / “调整顺序”（`VisuallyHidden`） | 现有拖动把手按钮 | `28px` |
| 序号 | `#` / “序号”（`#` 设 `aria-hidden`，旁边 `VisuallyHidden`） | 收藏：`(page - 1) × page_size + 下标 + 1`（显示行的下标，保留行也连续编号）；歌单：`item.position`；`formatCount`，右对齐 tabular | `3em` |
| 标题（rowheader） | 标题 | 标题 `trackTitleOf`；曲目艺人与专辑艺人不同时下方小字 `ArtistNames`（规则见 2.4）；文件缺失时标题后 `StatusTag tone="outline"`“文件缺失” | `minmax(0, 2fr)` |
| 专辑艺人 | 专辑艺人 | `ArtistNames text={albumArtistOf(release)} refs={albumArtistRefsOf(release)}`（无 `album_artist` 时退回发行艺人） | `minmax(0, 1fr)` |
| 专辑 | 专辑 | `present` 时 `Link` 到 `/albums/$groupId?release=`，否则纯文字 | `minmax(0, 1.2fr)` |
| 发行 | 发行 | `release.year`，未知时空单元格（不写“未知”或“—”） | `4em` |
| 操作 | 无 / “操作” | `renderTrackActions(...)` 的三个图标 | `auto` |
| 时长 | 时长 | `formatDuration`，未知写占位并带 `VisuallyHidden`“时长未知”（沿用歌单现有写法），右对齐 tabular | `4.5em` |

- 表的可访问名称：收藏 `aria-label="收藏的曲目"`（保持现有测试与真实用例的查询），歌单 `aria-label="歌单曲目"`。
- 用户内容沿用 `overflow-wrap: anywhere` + `min-width: 0`，宽容器下长文字换行，不截断（截断会让我们去掉的 `title` 失去替代）。

### 2.4 标题下的曲目艺人

与专辑页 `release-media.tsx` 的规则相同：`artist = track.artist.trim()`；`artist !== "" && artist !== albumArtistOf(track.release)` 时显示
`ArtistNames text={trackArtistOf(track)} refs={trackArtistRefsOf(track)}`，否则不显示。规则写成 `track-table.tsx` 内的小函数，不改 catalog。

### 2.5 行状态与样式

- 行：上下 `6px` 内边距、`1px` 分隔线（`--color-divider`）；`.row:hover` 整行 `background: var(--color-hover)` + `--radius-md`。
- 文件缺失：`data-missing`，标题与次要文字 `--text-secondary`，专辑是纯文字；不能播放由 `TrackActions` 已有逻辑处理。
- 拖动中：沿用现有 `data-dragging` 的表面色与阴影。

### 2.6 歌单拖动把手

- 放在独立的首列（不覆盖序号：触屏上把手常显时序号仍要看得见，且两页其余列完全一致）。收藏页没有这一列。
- 显示规则：`@media (hover: hover)` 下把手 `opacity: 0`，`.row:hover`、`.row:focus-within`、`[data-dragging]` 时 `opacity: 1`；`@media (hover: none)` 常显。
  用 `opacity` 而不是 `visibility/display`：键盘 Tab 仍能到达，聚焦时随 `focus-within` 出现。
- dnd-kit 与表格共存：`PlaylistRow` 对 `TrackTableRow` 传 `ref={setNodeRef}`、`style`（`CSS.Translate`）、`dragging`，把手经 `handle` 插槽传入（`setActivatorNodeRef` + `attributes` + `listeners`，
  `touch-action: none`、`aria-label` 不变）。`DndContext`/`SortableContext` 包住表体 rowgroup；传感器、播报、`before` 锚点计算、`busy` 时不设 disabled 等逻辑原样保留。
- `TrackTableRow` 的 props：`{ track, number, actions, handle?, rowRef?, style?, dragging? }`（React 19 直接收 `ref` 也可）。表格外壳 props：`{ label, sortable: boolean, children }`，
  `sortable` 决定是否有把手列与它的表头。

### 2.7 窄屏折叠（容器宽度 < 640px，与 `DataTable` 同一阈值）

- 表头行 `display: none`。
- 列轨道改为 `[把手 28px] 2.5em minmax(0, 1fr) auto auto`：序号、标题区、操作、时长；把手 / 序号 / 操作 / 时长 `grid-row: span 2` 垂直居中。
- 专辑艺人、专辑、发行三个单元格包在一个无角色的 `div.meta` 里：宽容器下 `display: contents`（单元格直接参与表格网格），窄容器下变成标题下第二行的
  `display: flex; flex-wrap: wrap`，单元格之间用 `::before` 加“ · ”，空的发行单元格 `:empty { display: none }`。DOM 只有一份，读屏不重复。
- 不出现横向滚动；操作三个图标与时长始终和标题同一行组。

### 2.8 两个视图的接入

- `liked-tracks-view.tsx`：`DataTable` 与 `columns` 换成 `TrackTable` + `TrackTableRow`，去掉“收藏时间”列与 `formatDateTime` 引用；
  序号用 `list.data.pagination.page_size`。保留行、`UpdatedHint`、分页、`FavoriteStatusScope` 不变。
- `playlist-detail-view.tsx`：`PlaylistItems` 的 `<ol>` 换成 `TrackTable sortable`，`PlaylistRow` 改为包装 `TrackTableRow`；“艺人 — 专辑”次要行删除（由列取代）。
  `playlist-detail-view.module.css` 中行、把手、标题、时长的样式移入 `track-table.module.css`，原文件只留页面级样式。

---

## 3. R3 悬停提示与专辑页管理按钮图标化

### 3.1 共享提示 `shared/ui/tooltip.tsx`

- 形式：hook `useTooltip(text: string | undefined)` 返回 `{ triggerProps, tooltip }`：`triggerProps` 是 `onPointerEnter/onPointerLeave/onPointerDown` 三个处理器，
  `tooltip` 是要和触发器一起渲染的节点（portal）。`Button` 与图标链接都用它；不做 `cloneElement` 包装组件。
- 行为：
  - 只响应 `event.pointerType === "mouse"` 的 `pointerenter`；触屏、笔、键盘聚焦都不弹（读屏靠 `aria-label`）。
  - 进入后 `300ms` 计时显示；`pointerleave`、`pointerdown`、`Escape`（`keydown`，满足可关闭）、窗口 `scroll`（捕获阶段，任何滚动容器）、`resize`、触发器卸载时立即隐藏并清计时器。
  - 点击后不残留：`pointerdown` 隐藏，且在下一次 `pointerenter` 之前不再显示。
  - 不做“相邻图标免延迟”的预热逻辑（不需要）。
- 渲染：`createPortal` 到 `trigger.closest("dialog") ?? document.body`（与 `useMenu` 相同：模态 `<dialog>` 打开时 body 下的内容在顶层之下）；
  `position: fixed`，按 `getBoundingClientRect()` 默认放在触发器上方居中、间距 `6px`；`useLayoutEffect` 量出提示尺寸后：上方放不下（距视口顶 < 8px）翻到下方，水平方向夹在视口 `8px` 边距内。
  因为是 fixed + portal，不会被表格、播放器栏、`overflow` 容器裁掉。
- 语义：提示元素 `aria-hidden="true"`、`pointer-events: none`，不加 `aria-describedby`（文字与 `aria-label` 相同，避免读两遍）。
- 样式（`tooltip.module.css`）：`--color-neutral-900` 底、白字、`--fs-12`、`--radius-md`、`4px 8px` 内边距、`max-width: 240px`、`z-index` 高于菜单浮层；
  `prefers-reduced-motion: no-preference` 时 100ms 淡入，否则无动画。
- jsdom：测试用 `fireEvent.pointerEnter(el, { pointerType: "mouse" })` + 假计时器；若 jsdom 的事件对象不带 `pointerType`，在测试里用 `Object.defineProperty` 补上，不改实现。

### 3.2 接入点（去掉原生 `title`）

| 位置 | 改法 |
| --- | --- |
| `shared/ui/button.tsx` | 删除 `title={label}`；`variant="icon"` 时调用 `useTooltip(tooltip ?? label)` 并合并调用方传入的同名 pointer 处理器。新增可选 prop `tooltip?: string`（仅 icon 变体），用于提示文字与读屏名不同的场合 |
| `shared/ui/menu-button.tsx` | `iconOnly` 复用 `Button variant="icon"`，自动获得提示；打开菜单的 `pointerdown` 会隐藏提示，无需改动（只核对注释“label 作为读屏名和悬停提示”） |
| `features/personal/components/favorite-button.tsx` | `title={hint}` 改为 `tooltip={hint}`（“收藏 / 取消收藏 / 正在读取收藏状态 / 暂时读不到收藏状态”）；读屏名 `收藏《X》` 不变 |
| `app/shell/primary-nav.tsx` | `title={expanded ? "收起" : "展开"}` 改为 `tooltip=`；读屏名“歌单列表”不变 |
| 其余 `variant="icon"`（`track-actions.tsx`、`player-controls.tsx`、`player-queue.tsx`、`genre-tags-input.tsx`、`folder-browser.tsx`） | 无需改动，经 `Button` 自动换成新提示 |

- 不在范围：播放器里截断文字的 `title`（`player-queue.tsx`、`now-playing-page.tsx` 的标题/艺人/专辑文字）不是图标按钮，保留；
  对应断言 `src/features/player/components/player-ui.test.tsx:265–284`、`e2e/player.spec.ts:161–163` **不受影响**。
- 禁用的图标按钮（如收藏状态读取中的心形）在部分浏览器不派发指针事件，提示可能不出现；读屏名与 `aria-pressed` 仍在，不另做兜底。

### 3.3 专辑页管理操作区（`features/governance/components/release-admin-actions.tsx`）

| 操作 | 图标（lucide，沿用现有） | 读屏名 = 提示 |
| --- | --- | --- |
| 编辑发行信息 | `PencilLine` | “编辑发行信息” |
| 设为默认版本 / 改回自动选择（多版本时） | `Bookmark` / `RotateCcw` | “设为默认版本” / “改回自动选择” |
| 归入其他专辑… | `Combine` | “归入其他专辑…”（保留省略号：现有测试与 e2e 按这个名字查找） |
| 移出为独立专辑（多版本时） | `Split` | “移出为独立专辑” |
| 查看问题（需要关注时） | `ClipboardCheck` | “查看问题” |

- 前四个改为 `Button variant="icon" icon=… label=…`，无文字、无边框。
- **busy**：“正在保存…”文字没了之后，默认版本按钮在提交中把图标换成 `LoaderCircle`（`@media (prefers-reduced-motion: no-preference)` 下旋转，减少动态时静止），
  `label` 改为“正在保存…”（提示与读屏名一致，延续“文案由调用方改成正在…”的约定）；`aria-busy` 由 `Button` 已有逻辑设置。结果仍由 `ReleaseCommandNotice` 播报。
- **查看问题**：`Link` 只放图标，`aria-label="查看问题"`，用 `useTooltip("查看问题")` 的 `triggerProps` 与 `tooltip`；样式类 `.iconLink` 与图标按钮同尺寸（`--control-height` 方形、无边框、悬停 `--color-hover`、可见焦点环）。
  只有这一处图标链接，不抽 shared 组件。
- 认领提示（“可能接替了……”与“查看并认领”）不是按钮，保持文字。

---

## 4. R4 歌单 / 我喜欢的音乐的顶部（仿网易云）

### 4.1 Page 的配合

- `shared/ui/page.tsx` 新增可选 prop `header?: ReactNode`：给出时替换默认的 `<header>`（h1 + 说明 + 操作），`usePageTitle(title)` 照常设置文档标题；
  约定由 `header` 渲染唯一的 `h1`，文字与 `title` 相同。默认用法不变，其他页面不受影响。
- 页面结构调整：`PlaylistDetailView` 与 `LikedTracksView` **自己渲染 `Page`**（与 `AlbumDetailView` 同理：h1 取决于读到的数据），
  `app/routes/playlist-detail-page.tsx`、`favorites-page.tsx` 的 `LikedPage` 不再包 `Page`，只组合插槽。
  - 歌单：`title` = 歌单名（摘要未到时 `"歌单"`，此时用默认头部 + 骨架）；文档标题因此从“我的歌单 · ROOMusic”变成“歌单名 · ROOMusic”。
  - 歌单不存在：`<Page title="歌单不存在">`，内容是“它可能已被删除。”+“回到歌单”链接（原 Panel 内容上移）。
  - 我喜欢的音乐：`title="我喜欢的音乐"`。
- 删除：Page 标题“我的歌单”、Panel“收藏的曲目”、“全部歌单”链接、歌单头部的“改名”“删除歌单”文字按钮。

### 4.2 头部组件 `features/personal/components/collection-hero.tsx`

Props：`{ title, cover: ReactNode, tint: string | null, onRename?, renameBusy?, owner: string, createdAt?: string, actions: ReactNode }`。结构：

```
<section class=hero style="--hero-tint: …">           ← 渐变背景
  <div class=cover>{cover}</div>                     ← 圆角 --radius-lg
  <div class=info>
    <div class=titleRow><h1>{title}</h1>[铅笔 icon 按钮“改名”]</div>
    <p class=owner><Avatar name={owner}/> <span>{owner}</span> [<time dateTime>YYYY-MM-DD 创建</time>]</p>
    <div class=actions>{actions}</div>                ← “播放全部”与“···”
  </div>
</section>
```

- 铅笔：`Button variant="icon" icon={<PencilLine/>} label="改名"`，紧跟 h1 同一行（标题换行时铅笔跟在末行，用 `inline-flex` 放进标题行的流内）；摘要未到时不渲染。
- 头像：新建 `shared/ui/avatar.tsx`（从 `app/shell/user-menu.tsx` 抽出 `initialOf` 与 `.avatar` 样式，用户菜单与头部两处使用），28px 圆形、金色描边、首字（按字素取、拉丁字母大写），`aria-hidden`。
- 用户名：`useViewerScope().viewer.username`（personal 已允许依赖 auth 的会话范围；歌单只属于查看者本人）。
- 创建日期：`shared/lib/time.ts` 新增 `formatDate(value, timeZone?)` → `YYYY-MM-DD`（与 `formatDateTime` 共用 `Intl` 拆分），文字“2025-03-30 创建”，`<time dateTime={created_at}>`。只有歌单有。
- 操作：
  - “播放全部”仍是 app 的 `PersonalPlayAll`（主按钮、金色）。为了让“···”与它同一行，`PersonalPlayAll` 增加 `trailing?: ReactNode`，渲染在它的按钮行里；
    歌单的 `renderPlayAll` 插槽改为 `(playlist, trailing) => ReactNode`，收藏的 `renderPlayAll` 不传 `trailing`。播放结果与失败提示仍在按钮行下方。
  - 歌单的“···”：`MenuButton iconOnly icon={<MoreHorizontal/>} label="歌单操作" actions={[{ id: "delete", label: "删除歌单", onSelect: 打开确认 }]}`；
    读屏名不用“更多操作”，避免与每行的“更多操作”同名。删除确认对话框与文案不变。
  - 我喜欢的音乐没有可放的操作，不显示“···”；收藏为 0 首时不显示“播放全部”（与现状一致）。
- 封面：歌单用现有 `PlaylistCover`（拼图）；我喜欢的音乐新建 `LikedCover`（金色淡底 `--color-accent-100`→`--color-accent-200` 渐变 + 居中 `Heart` 实心 `--color-accent-600`，`aria-hidden`）。
  尺寸：容器 ≥ 560px 时 `200px`，否则 `112px`；始终左封面右信息并排。

### 4.3 “歌曲 N”标签行

- 头部下方一行：`<h2 tabIndex={-1} ref={headingRef}>歌曲 <span class=count>N</span></h2>`，`h2` 下方 `2px` 金色下划线（`--color-accent`，宽度随文字）；
  歌单在旁边灰色小字写总时长（`--fs-13`，`--text-secondary`），我喜欢的音乐不写（接口没有总时长）。N 用 `formatCount`：收藏为 `pagination.total`，歌单为 `state.track_count`。
- 这个 `h2` 取代原 Panel 标题成为程序聚焦目标：翻页 `showTop`、移除后焦点回到它（紧接着 Tab 就是“撤销”）。
- 超限提示、移除撤销、命令失败、“歌单有变化，正在重新读取。”、“列表有更新。”放在 `h2` 与表格之间，顺序同现状；分页在表格下方。
- 时长文字：把 `personal/playlist-text.ts` 的 `playlistMetaText` 拆出 `playlistDurationText(summary)`（返回“1 小时 20 分钟”“…以上”或空串，规则不变），
  歌单卡片仍用 `playlistMetaText`（内部改为调用它）。口语时长函数移到 shared，见 §5.2。

### 4.4 封面取色与渐变

- 新增 catalog 公开 hook `useArtworkTint(artwork: ArtworkDTO | null): string | null`（`features/catalog/hooks/use-artwork-tint.ts`，从 `catalog/index.ts` 导出；catalog 拥有封面地址 `artworkPath`）。
  - 用 `new Image()` 加载 `artworkPath(resource_id)`（同源，与 `Cover` 同一地址，命中浏览器缓存，canvas 不会被污染），`onload` 后画到 `24×24` 的 canvas，
    跳过透明、近白（L > 92%）、近黑（L < 8%）像素，按饱和度加权求平均色，转 HSL。
  - 结果固定成浅色：`hsl(H, min(S, 45%), 90%)`。灰度图（S < 10%）返回 `null`。按 `resource_id` 记在模块内 `Map` 里，同一封面只算一次。
  - 加载失败、`getContext` 为 null、无封面：返回 `null`。不重试、不报错。
- 取哪张：歌单用 `cover_groups` 第一项的 `artwork`（拼图左上那张，与单封面时显示的是同一张）；我喜欢的音乐不取色。
- CSS：`.hero { background: linear-gradient(180deg, var(--hero-tint) 0%, var(--color-bg) 100%); border-radius: var(--radius-lg); padding: var(--space-6); }`；
  `--hero-tint` 缺省为 `var(--color-neutral-200)`（无封面 / 取色失败），我喜欢的音乐传 `var(--color-accent-100)`。
- 对比度：底色亮度固定在 L=90%、S≤45%，最暗处约 `rgb(240,214,214)`，正文 `#201f1d` 远超 7:1，次要文字 `--color-neutral-700`（`#605d5d`）约 4.9:1，满足 4.5:1；
  渐变越往下越接近页面底色。Playwright 的 axe `color-contrast` 在两页的截图场景里覆盖。

### 4.5 窄屏

- 容器 < 560px：封面 112px，信息列 `minmax(0,1fr)`；h1 降到 `--fs-20` 并允许换行；头像行与按钮行各占一行，按钮行 `flex-wrap: nowrap`（“播放全部”+“···”在 375px 下约 150px，放得下），
  头部内边距 `--space-4`。不出现一个图标单独掉到下一行。

---

## 5. R5 专辑概况单行（`features/catalog/components/album-header.tsx`）

### 5.1 `factsOf` 新规则

返回 `{ key, label, value, field? }[]`（`label` 只给读屏）：
1. 年份：`release.year !== null` 时 `String(year)`，`field: "year"`；未知不写。
2. 流派：`formatGenres(detail.genres)` 非空时写（PRD 例子里的“ · ”分隔沿用 `formatGenres` 的现有输出），`field: "genre"`。
3. 介质：`release.medium_count > 1` 时 `${formatCount(n)} 张碟`；单碟或 0 不写。
4. 曲目：`${formatCount(track_count)} 首`。
5. 总时长：`totalDuration(detail.media)` 非 null 且 > 0 时用口语写法；有一首未知就不写（沿用现规则）。

### 5.2 口语时长复用

- 把 `personal/playlist-text.ts` 私有的 `spokenDuration(seconds, floor)` 移到 `shared/lib/duration.ts`，导出为 `formatSpokenDuration(seconds, { floor })`；
  personal 与 catalog 都从 shared 引用（catalog 不能导入 personal）。
- 单位写法按 PRD 例子统一为“分钟”：“1 小时 14 分钟”“45 分钟”“1 小时”，不足 1 分钟写“N 秒”。这会把歌单卡片的“1 小时 20 分”改为“1 小时 20 分钟”（见 §8 待裁决 1）。

### 5.3 竖线分隔的 CSS

- 结构：`<div class=factsClip><ul class=facts aria-label="概况">{facts.map(li)}</ul></div>`，每个 `li` 里 `VisuallyHidden` 的“年份：”等标签 + 值 + 可选 `ManualCorrectionTag`。
- 行首不出现分隔线的做法（负边距 + 裁切）：
  - `.facts { display:flex; flex-wrap:wrap; row-gap: 2px; margin-inline-start: calc(-1 * var(--sep-gap)); }`
  - `.facts li { position:relative; padding-inline: var(--sep-gap) 0; margin-inline-end: calc(var(--sep-gap) - 1px); }`，
    `li::before` 是 `1px` 宽、上下各缩 `0.3em` 的竖线（`--color-neutral-300`），贴在 `li` 左缘；
  - `.factsClip { overflow: clip; }`：每一行第一个 `li` 的竖线落在被负边距推出去的那 `--sep-gap` 宽度里，被裁掉；其余 `li` 的竖线在相邻两项之间。
  - `--sep-gap: 12px`；整行 `--fs-13`、`--text-secondary`、`tabular-nums`、无衬线。内部没有可聚焦元素，裁切不影响焦点环。
- 删除原 `dl/dt/dd` 的大号数值样式（`.fact dd` 的 `--fs-16` 标题字体）。

### 5.4 人工修正标记

`ManualCorrectionTag` 放在对应 `li` 内值的后面（`inline-flex`，`margin-inline-start: 4px`），保持文字加图标；不单独占一项，不参与分隔。

---

## 6. 测试影响与最小测试集

原则：只改被改动行为直接影响的断言、为新行为补最少的用例；不跑全量，不为假想场景加测试，复用已有结果。

### 6.1 后端（R1）

- `backend/internal/app/identity_api_integration_test.go`：新增一个用例——管理员不带 `current_password`、再带错的 `current_password` 各改一次都 200，
  当前会话保留、管理员的第二个会话被撤销、新密码能登录。现有改密用例都用成员（`e.member`），不动。
- `backend/internal/app/login_throttle_integration_test.go`：`TestPostgreSQLPasswordThrottleAcrossSourcesKeepsSession`、`TestPostgreSQLPasswordThrottleSuccessClearsFailures`
  现在用管理员 `curator` 测“当前密码”限速，改为先由管理员新建一个成员再以成员登录（断言不变）。
- 接口样本（`api_samples_integration_test.go`）用的是成员 `archivist`，不受影响，样本不重写。
- 命令：`go test ./internal/identity/... ` 与上述两个文件里的相关用例（按项目现有集成测试脚本带隔离测试库运行，`-run 'ChangeOwnPassword|AdminChangeOwnPassword|PasswordThrottle'`）。

### 6.2 前端 Vitest

| 文件 | 改动 |
| --- | --- |
| `src/features/auth/account-rules.test.ts` | `validatePasswordChange` 的 `current: null` 不报错（一条） |
| `src/app/account-password.test.tsx` | `openPasswordChange` 默认改为成员 `listener`（现有用例都在测当前密码路径）；新增一条：管理员没有“当前密码”字段，请求体只有 `new_password`，成功 |
| `src/test/fake-api.ts` | `/auth/password` 替身：账号是 admin 时跳过当前密码核对 |
| `src/test/account-password-mocks.test.ts` | 仅在类型变化导致编译失败时调整调用参数 |
| `src/shared/ui/tooltip.test.tsx`（新） | 鼠标进入 300ms 后出现、离开即消失、`pointerdown` 后不残留、触屏不出现（4 条，假计时器） |
| `src/features/personal/favorite-interactions.test.tsx:113` | 原断言 `getAttribute("title")`，改为不再断言 `title`（或按新提示断言一次） |
| `src/app/favorites.test.tsx` | 表名“收藏的曲目”不变；聚焦目标从“收藏的曲目”标题改为“歌曲 N”；删除“收藏时间”相关断言；加一条列头顺序断言（A2） |
| `src/app/playlists.test.tsx` | `closest("li")` → `closest('[role="row"]')`；“删除歌单”改为先开“歌单操作”菜单；移除后焦点断言改为“歌曲 N”；改名入口改为铅笔按钮“改名”；加一条列头顺序断言 |
| `src/features/personal/playlist-text.test.ts` | 单位“分”→“分钟”的期望值（若 §8-1 裁决为统一） |
| `src/shared/lib/duration.test.ts` | 为 `formatSpokenDuration` 加几条用例（取自原 `playlist-text.test.ts` 的时长期望）；不新增边界 |
| `src/app/album-detail.test.tsx` | `headerFact`（dt/dd）助手改为读概况列表项；期望改为“2 张碟”、口语总时长、年份未知时不出现 |
| `src/test/setup.ts` | 若 `useArtworkTint` 在 jsdom 触发 “Not implemented: getContext”，在 setup 里把 `HTMLCanvasElement.prototype.getContext` 桩为返回 `null`（jsdom 不加载图片，正常不会走到） |

`src/features/player/components/player-ui.test.tsx` 的 `title` 断言针对截断文字，不受影响。

命令：`npx vitest run <上表文件>`，再加 `npx tsc --noEmit` 与 `npx eslint <改动文件>`。

### 6.3 Playwright（受控后端，`frontend/e2e/`）

| 文件 | 改动 |
| --- | --- |
| `e2e/support/api-mock.ts` | `/auth/password`：admin 跳过当前密码核对 |
| `e2e/auth.spec.ts`（“账户密码：自助修改”一组，约 146–304 行） | 当前密码相关用例改为 `signedInAs: "listener"`；“缺少当前密码”本地校验用例同样用成员；管理员路径不另加 e2e（Vitest 已覆盖） |
| `e2e/support/account-password.ts` | `fillPasswordChange` 的 `current` 允许为 `null`（不填）；`openAccountScene` 中“修改密码对话框/对话框内成功/成功关闭后账号栏/新密码字段错误/改密码结果未确认”保持管理员（无当前密码字段），“当前密码错误”改用成员登录 |
| `e2e/personal.spec.ts` | `playlistRow` 的 `getByRole("listitem")` → `getByRole("row")`；第 225 行“删除歌单”按钮可见行数检查改为检查头部按钮行不换行；第 143/147 行按 `listitem` 找的是“我的收藏”专辑卡片（不在范围），不动 |
| `e2e/albums.spec.ts:175` | “总时长”文字断言改为口语时长值 |
| `e2e/governance.spec.ts` | 按名字找按钮的写法不变；加一条：专辑页悬停“编辑发行信息”约 0.3 秒出现提示、移开消失，页面内 `button[title]` 数为 0（A3） |
| `e2e/responsive.spec.ts`、`e2e/a11y.spec.ts` | 只跑涉及账号场景、专辑详情、我喜欢的音乐、歌单详情的用例（`--grep`），确认 375/1440 宽无溢出、axe 通过；如场景里没有这两页，在已有个人页场景列表里各加一项 |

A6 的 1440 与 375 截图由测试 agent 用上述用例或一次性截图脚本产出，不另写新规格。

### 6.4 真实服务（`frontend/e2e/real/`、`scripts/playback-e2e.sh`）

- `e2e/real/account.real.ts`：用成员改密，断言请求体含 `current_password`，**不受影响**，不跑。
- `e2e/real/personal.real.ts`：按表名“收藏的曲目”和空态文案查询，表名不变；只在 R2/R4 落地后跑这一个文件确认。
- `e2e/real/governance.real.ts`、`default-version.real.ts`、`issues.real.ts`：按按钮/链接的可访问名称查找，名称不变，预计不受影响；不主动跑，除非 mock 规格出现意外。
- `scripts/playback-e2e.sh`：不涉及本任务的文案，不跑。

---

## 7. 风险与回滚（提交切分）

按下面顺序提交，每个提交独立可回滚（`git revert` 单个提交即可），后一项只依赖前面已合入的共享件：

1. **R1 后端**：`ChangeAdminOwnPassword` + handler 分派 + 两个限速测试改成员 + 新集成用例 + `identity-contracts.md`。
   风险：被盗用的管理员会话可以直接改掉管理员密码（用户已接受）；恢复路径是命令行 `roomusic reset-password`。旧前端仍发 `current_password`，后端忽略，兼容。
2. **R1 前端**：对话框按角色隐藏字段、请求体、替身与 e2e 调整。依赖 1（先上前端会让管理员改密 403）。
3. **R3 提示与图标化**：`tooltip.tsx`、`Button` 去 `title`、三个接入点、`release-admin-actions` 图标化与 busy、governance e2e 一条。全应用图标按钮都受影响，回滚只需还原这一个提交。
4. **R5 专辑概况**：`formatSpokenDuration` 移到 shared（personal 改引用）、`factsOf` 与样式、album-detail 测试。体量小，先于 R4 合入以便 R4 复用口语时长。
5. **R2 曲目表格**：`track-table`、两个视图换表、playlists/favorites 测试与 personal e2e 的行查询。
6. **R4 顶部**：`Page.header`、`collection-hero`、`Avatar`、`LikedCover`、`useArtworkTint`、`formatDate`、`PersonalPlayAll.trailing`、“歌曲 N”与焦点目标、相关测试。依赖 3（铅笔与“···”的提示）与 5（表格位于标签行之下）。

规范同步（Phase 3.3，随对应提交或最后一次性）：`component-guidelines.md`（`Button` 的提示改为 `useTooltip`、`tooltip` prop、`Page.header`、`Avatar`）、
`personal-guidelines.md`（“我的音乐的页面与侧栏”里“歌单详情的页面标题也是‘我的歌单’”、详情头部、曲目表格、收藏页列）、`identity-contracts.md`（§1.3）、
`frontend/DESIGN-components.md` 中如有“图标按钮用 title 提示”的条目一并改。

---

## 8. 主会话裁决（2026-11-08）

1. 口语时长统一为“分钟”：一个函数一种写法，歌单卡片与详情的“1 小时 20 分”随之改为“1 小时 20 分钟”，`playlist-text.test.ts` 期望值跟着改。
2. `self_password_reset` 的 message 不改（错误码不变、前端碰不到，不重录样本）。
3. 歌单详情的文档标题改为“歌单名 · ROOMusic”，接受（h1 是歌单名的自然结果）。
4. 程序聚焦目标改为“歌曲 N”（`h2`），接受。
5. 提示 `aria-hidden`，读屏只读 `aria-label`，接受。
6. 中等宽度（640–900px 容器）先不加断点；收尾截图若拥挤，再决定是否隐藏“发行”列。
7. 安全取舍（被盗用的管理员会话可直接改管理员密码，恢复靠 `roomusic reset-password`）随最终规划摘要请用户确认。
