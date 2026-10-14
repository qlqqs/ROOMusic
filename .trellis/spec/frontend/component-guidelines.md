# Component Guidelines

## Component Responsibilities

Components render typed state and emit user intent. Pages/routes compose feature
components and hooks. Components do not decode raw REST payloads, issue arbitrary
requests, enforce backend authority, or contain database/filesystem vocabulary.

Prefer small composition by responsibility:

```text
ReleaseRoute
  -> useReleaseDetail
  -> ReleaseHeader
  -> MediumList
       -> TrackRow
  -> EvidenceSummary
```

这是目标组合模式；M1a 的页面（扫描目录、扫描详情、成员、概览）按它实现：路由组件只组合 feature 内容，数据与命令在 feature
hook 里，展示在组件里。按 [Classical 设计依据](../../../frontend/DESIGN.md)
组织组件与状态；新组件库或样式依赖仍须在对应子任务及 ADR 中批准，不能因原型包含某个
控件而假定其业务能力已实现。

## 已实现的共用组件与约定（M1a，`shared/ui`）

`shared/ui` 只放至少两处使用、与策略无关的表现组件，样式用 CSS Modules 与 `styles/tokens.css` 的变量：

| 组件 | 约定 |
| --- | --- |
| `Page`、`Panel`、`StatList` | `Page` 渲染唯一的 `h1`（并设置“页面名 · ROOMusic”标题），标题很长时 `compactTitle` 降一级字号（M1b：专辑名超过 80 个字符），完整显示并换行；`Panel` 是带标题的区段，给 `headingRef` 时标题可以被程序聚焦（`tabIndex=-1`，不进 Tab 顺序）；`StatList` 显示数值，数据值用 `lining-nums tabular-nums` |
| `QueryContent` | 数据区块的首次加载（保留结构的 `Skeleton`，不画成空库）、失败（带请求 ID 的 `FailureNotice` 与重试）、成功，以及“刷新失败但还有旧数据”（旧数据继续显示，错误在其上方）；它只依赖最小的查询形状，不依赖查询库 |
| `Notice`、`FailureNotice` | 行内状态或错误，由图标与文字表达，不靠颜色；错误 `role="alert"`，其余 `role="status"`；可附请求 ID 与下一步操作；**不使用全局 toast**，结果就近显示在操作区 |
| `Button`、`TextField` | `Button` 有主要 / 次要 / 文字三种形态；忙碌态不设 `disabled`（焦点留在按钮上），点击被忽略并设置 `aria-busy`，文案由调用方改成“正在…”；`TextField` 的 label、说明与错误经 `aria-describedby` 关联，错误同时设置 `aria-invalid` |
| `StatusTag` | 文字加图标，不只靠颜色 |
| `ModalDialog`、`ConfirmDialog` | 原生 `<dialog>` 的模态：有可访问名称，打开时焦点进入（确认对话框初始焦点在“取消”，避免回车直接确认），Esc 与取消可关闭，关闭后焦点回到触发按钮；提交中确认忙碌、取消与 Esc 暂时无效；阻塞性错误留在对话框内。子内容只在打开时渲染，关闭即丢弃其中的私有状态 |
| `DataTable` | 真正的 `<table>`（有 caption、`rowHeader`）；以**容器宽度**（CSS 容器查询，小于 640px）而不是视口宽度决定是否堆叠成“标签：值”，因为带侧栏时 768px 视口里内容区只有约 500px；不产生任何横向滚动（局部滚动区需要可聚焦，会引入额外的无障碍负担） |
| `Disclosure`（M3） | 页面底部折叠的区块（“来源与证据”“修改记录”）：原生 `<details>` 的展开状态由 React 持有，**折叠时不渲染内容**（没有可聚焦的隐藏控件，内容里的请求也在展开后才发出），展开时挂载；标题行是原生 `summary`（Enter 与空格展开），可经 `summaryRef` 取到，区块外的操作结束后可把焦点带回；换对象时用 `key` 回到折叠 |
| `Pagination`（F3） | `<nav>` 带可访问名称（如“扫描记录分页”，同一页面可能不止一个分页），内容是“上一页”“第 x / y 页”“下一页”；当前页 `aria-current="page"`；到头的方向显示为 `aria-disabled` 的文字而不是消失，布局不跳动；只有一页或没有数据时不渲染；链接由调用方经 `renderLink` 生成（通常是路由 `Link`，组件不依赖路由），上一页与下一页带 `rel`。页码的 URL 约定见[状态规范](./state-management.md) |

页面级约定：

- 外壳有“跳到主要内容”链接、`nav` 与 `main` 地标；导航用链接，当前页有 `aria-current`；窄屏的“菜单”按钮打开原生 `<dialog>` 模态导航，
  打开时焦点进入，Esc 关闭并把焦点还给“菜单”按钮，选择链接后关闭。
- 管理页由 `AdminGate` 把关：非管理员显示“需要管理员权限”，不渲染页面内容，也就不调用管理接口；后端仍独立鉴权。
- 停用与撤销类操作先确认并说明影响范围，恢复与重新启用直接执行；目标是自己时对话框额外提示会被退出登录。
- 失败文案按**错误码**选择（各 feature 的 `*-messages.ts`，`as const` 映射），不比较后端 message；未知错误码显示后端的安全
  message 与请求 ID（`describeFailure`）。
- 扫描阶段只在变化时经礼貌型 live region 播报一次，轮询得到相同阶段不重复播报；不显示百分比或剩余时间。
- 用户内容（用户名、目录名、专辑与曲目标题等）允许任意位置换行（`overflow-wrap: anywhere`），超长值不撑破布局。**网格项与弹性项还要 `min-width: 0`**：
  `overflow-wrap: anywhere` 只让文字能折行，网格轨道与弹性项的“自动最小宽度”仍可能取内容的最小宽度（尤其是被 `content-visibility` 跳过、保留着上一次渲染宽度的分段），
  窗口先宽后窄时整页横向溢出；M1b 的 Playwright 有“先宽后窄”的回归用例。
- **“仅供读屏”的绝对定位元素需要定位祖先**：`VisuallyHidden` 与 `DataTable` 的 caption 是绝对定位的，没有定位祖先时它们的包含块是整个文档，不受主内容区裁剪，
  会把文档撑得比窗口还高，窗口本身能滚出一片空白。桌面外壳的两个滚动区（`.main`、`.sidebar`）因此是 `position: relative`；新增的滚动容器同样要这样做。
  Playwright 的 `expectNoDocumentScroll` 检查它（≥768px 时窗口本身不能再滚动）。
- 界面标签里的数字（“版本 2”“第 2 张”）用 `lining-nums`；作品标题里的数字保持标题字体默认的旧式数字。
- 表格里每一行都重复的链接文字（“查看详情”）用 `VisuallyHidden` 补上对象（如开始时间），读屏的链接列表里能分辨是哪一行。
- 原生 `<dialog>` 模态在最后一个可聚焦元素之后会把焦点交给浏览器界面或 `body`，不是严格的循环；测试断言“焦点始终在对话框内或
  在 `body` 上”，而不是要求严格循环。

## 已实现的专辑组件（M1b，`features/catalog`）

| 组件 | 约定 |
| --- | --- |
| `Cover` | 带裱边的正方形封面，不加滤镜，真实封面 `object-fit: contain` 不裁切；四态（加载中的同尺寸底色、已加载、没有封面的标题文字版面、加载失败加“封面无法加载”）由 `data-state` 表达；`variant="card"` 是装饰（`alt=""`、懒加载、对读屏隐藏），`variant="detail"` 有替代文字（“《标题》封面”“《标题》没有封面”）并在失败后提供“重新加载封面”（换 `key` 重新挂载图片，浏览器重新发请求）。只引用同源的封面接口，不拉外部图片 |
| `AlbumCard`、`AlbumGrid` | 整张卡片是一个 `Link`（里面没有按钮），可访问名称含标题与艺人，标题最多 3 行（`line-clamp`，完整文字仍在 DOM 里）；网格 `repeat(auto-fill, minmax(160px, 1fr))`；“N 个版本”只在 `release_count > 1` 时出现，单版本组没有版本字样 |
| `AlbumFilters`、`AlbumListView` | `<form role="search">`：搜索词显式提交，清除后焦点回到输入框，字节超限在字段旁说明现状；“只看需要关注”立即生效。结果数用 `aria-live="polite"`，“正在更新…”只给看得见的人；翻页时旧结果保持可见并标 `aria-busy`；点页码链接时滚到结果标题并聚焦 |
| `AlbumDetailView` | 页面标题与 h1 取决于选中的版本，所以详情自己渲染 `Page`（路由组件不包 `Page`）；头部用组详情里的摘要先渲染，曲目区单独显示加载态；选中的版本读详情得到 404 时就地提示并给出办法，不影响页面其余部分 |
| `AlbumHeader`、`VersionNav` | 头部是封面加概况（年份未知写“未知”，流派与总时长有才写，有一首曲目时长未知就不写总时长）；版本导航是 `<nav aria-label="版本">` 里的链接，不是 ARIA 标签页，各版本标题不同时才附标题 |
| `ReleaseMedia` | 一张介质时直接是 `<ol role="list">`，多于一张时每张一个 `<section aria-labelledby>` 加“跳到第 N 张”链接（点击把焦点放到分段标题上并滚到视野，不改地址）；分段用 `content-visibility: auto`，占位高度只写块方向的 `contain-intrinsic-block-size`（简写 `contain-intrinsic-size` 带一个值会同时给宽度，窄屏上被跳过的分段有 480px 的固有宽度，整页横向溢出） |
| `ReleaseFacts`、`EvidenceSection` | 发行信息里没有值的条目不列出；“来源与证据”是 `Disclosure`（折叠时不渲染内容，换版本用 `key` 回到折叠），有人工修正的字段在证据表里标 `ManualCorrectionTag`（“人工修正”，文字加图标）；管理员的“查看完整证据”按钮在展开后才出现，点击后才请求，按钮留在原处（加载中忙碌、之后可重新加载）使焦点不丢 |
| `labels.ts` | 署名角色、证据字段、来源、可信度、处理方式、原因码与归组方式的中文名；清单按后端当时的取值核对，**未知取值原样显示**，后端新增取值不会让页面出错（样本里出现过的取值都有中文名，由测试保证） |

## 已实现的受治理操作组件（M3，`features/governance`）

管理员专用；后端仍独立鉴权，界面只决定显示。结果就近显示在操作区（`ReleaseCommandNotice`：成功、明确的拒绝、结果不确定各有文案与下一步），不用全局 toast。

| 组件 | 约定 |
| --- | --- |
| `ReleaseAdminActions` | 专辑详情头部的操作区（由 app 经插槽 `renderAdminActions` 传给 catalog 的详情视图）：编辑发行信息、归入其他专辑…、移出为独立专辑（组内多于一个版本才有）与上一次操作的结果提示；成功提示里有“撤销”（撤销本身的成功提示没有）。命令状态由页面持有 |
| `EditReleaseDialog` | 八个字段各一个 `fieldset`：三选一（使用扫描值 / 自定义 / 留空，标题与专辑艺人没有“留空”）、自定义输入、写明扫描值；扫描结果不确定的字段多“确认扫描值”。**草稿只记动过的字段**：没动的字段始终显示最新基线，只提交动过且确有变化的字段，别人刚改过的字段不会被静默改回去；409 时对话框保持打开、保留草稿并重新载入最新状态；结果不确定时“保存”变成同一个键的“重试保存”。字段的 `legend` 在已有修正时带“人工修正”，测试按前缀匹配 |
| `JoinAlbumDialog`、`DetachAlbumDialog` | 归入：按标题搜索专辑（复用 `/release-groups?q=`，结果不含当前组，只显示最相关的前几张并提示输入更精确的搜索词），单选列表的可访问名称含标题与艺人（标题与元信息之间要有空格），选中后预览结果（`role="status"`），确认才提交；移出：确认对话框写明结果。成功后页面跟到新的专辑组并选中这个版本 |
| `OperationList` | `DataTable`：时间（行标题）、操作者、专辑（可见的链接到它现在所在的组并选中这个版本，已不可见的只写标题）、改动（类型名加逐行描述）、操作；只有 `revertible` 的行有“撤销”，其余在同一位置写出原因（已撤销、之后又有修改、专辑已不可见、撤销不能再撤销）；每行按钮文字相同，时间与类型放进可访问名称（`VisuallyHidden`） |
| `RevertOperationDialog` | 确认对话框写明会改回什么（前后互换的逐行预览）与“撤销会留下一条自己的记录，不能再被撤销”；结果不确定时保持打开、“撤销”变成“重试撤销”；成功或明确的拒绝关闭对话框，由页面提示说明。撤销成功后触发它的按钮所在行已变，`useRevertFlow` 把焦点带到区块标题或折叠面板的标题行 |
| `OperationsView`、`ReleaseHistory` | “操作记录”页内容（分页、按版本筛选并写明在看谁的记录、页码越界规范化）与专辑详情折叠的“修改记录”（展开才读取最近 5 条、带“全部记录”链接）；两处共用 `OperationList` 与撤销流程，撤销的结果提示放在折叠区块外，收起或换专辑组之后仍看得到 |

对话框的实现经验：加载态换成字段时焦点会丢，所以对话框打开后初始焦点放在标题上（确认对话框仍在“取消”上）；jsdom 不实现 `<dialog>` 的 Esc 取消，Esc 由 Playwright 覆盖；
长标题与无空格长串要在表格与对话框里换行（`overflow-wrap: anywhere`），Playwright 检查对话框自己没有横向滚动。

## 已实现的歌词组件（M2b，`features/player`）

| 组件 | 约定 |
| --- | --- |
| `LyricsPanel({ trackId })` | 自己取数（`useTrackLyrics`），经 `QueryContent` 呈现加载（普通文字）、失败（`FailureNotice` 加“重试”）、没有歌词（空态）、纯文本与同步五种状态；同步与纯文本子组件按曲目 `key` 重建，关闭跟随等局部状态不带到下一首；面板 `position: relative`，内部框架绝对铺满所在格，歌词再长也不撑高“封面 \| 歌词”那一行（窄于 760px 的单栏改为随内容增高、最高 60vh） |
| 同步歌词 | `<ol role="list">`（`list-style: none` 会让部分读屏丢掉列表语义，所以显式写 role），每组一个 `<li>`，当前组 `aria-current="true"`，`data-state`（`past`、`active`、`upcoming`）驱动样式；媒体可拖动时每组是 `<button>`，用 roving tabindex（只有当前组可 Tab 到，方向键、Home、End 移动焦点），不可拖动时是普通文本，可滚动区域本身可聚焦（axe 的 `scrollable-region-focusable`）；可滚动区域用 `role="group"` 加名称，不再用第二个名为“歌词”的 `region`（axe 的 `landmark-unique`） |
| 跟随滚动 | 目标位置由 `offsetTop` 相对滚动容器（容器必须 `position: relative`）计算，列表本身不能定位；只有用户输入事件（滚轮、触摸、滚动键、点滚动条）暂停跟随，不监听 `scroll`；`scrollTo`、`matchMedia` 在 jsdom 里没有实现，由 `test/setup.ts` 提供默认桩，需要断言的测试自己替换 |

## Props And Contracts

- Define the smallest props at the component owner. Pass stable IDs and display
  models, not whole cache records or global stores.
- Use discriminated unions for mutually exclusive states instead of unrelated
  booleans such as `isLoading`, `hasError`, and `isEmpty` that can conflict.
- Prefer callbacks that express intent: `onRetryScan`, `onDisableRoot`, not
  `setData` or a raw dispatch function.
- Keep server mutation and invalidation in feature hooks; keep input focus,
  disclosure, and selection state local to the component.
- Do not mirror props into local state unless editing requires a deliberate
  draft lifecycle.

Planned state example:

```ts
type ReleasePanelState =
  | { status: "loading" }
  | { status: "error"; error: ApiError }
  | { status: "empty" }
  | { status: "ready"; release: ReleaseDetailView };
```

## Required Workflow States

Every data surface explicitly handles loading, empty, error, and ready states.
Add stale/retrying where the selected server-state mechanism can distinguish
them. Administrative mutations also show pending, success, revision conflict,
permission loss, and failure without losing the user's context.

扫描视图区分后端的 `running | succeeded | failed | canceled | incomplete`；提交中的
`pending` 如需展示，只是本地请求状态，不是新增远端扫描枚举。诊断计数不是百分比进度。
不把不完整扫描标为成功，也不暗示失败扫描移除了缺失音乐。

Use stable entity IDs as React keys. Track numbers, titles, array indexes, and
file names are not stable identities.

## Operation And Agent Surfaces

Future operation screens must render the backend's durable Change Set and
Operation Journal state; they must not infer completion from a button click or
from a log message. Display the operation mode and approval state separately:

- `assistant`: show the pending user-approval step and keep the exact proposal
  available for review before submitting approval.
- `steward`: show Review Subagent status and distinguish approved, rejected,
  reviewer unavailable, and `needs_human_confirmation`.
- `operator`: show that no approval/review step was required, while still
  displaying validation, execution, failure, recovery availability, and any
  irreversible marker.

The UI may hide controls for usability, but it never turns a local role flag
into permission. A dangerous action dialog must show target scope, impact,
before/after summary where available, current revision, operation status, and
whether an inverse action or checkpoint exists. Natural-language Agent output
is explanatory content, not an executable command or proof of approval.

## Security And Authority

A hidden or disabled control is user guidance only. The backend rechecks role and
resource authority. Components must handle 401 by returning to session recovery,
403 as denied, and 409 revision/idempotency conflicts as refresh/reconcile flows.

The browser never constructs a host file URL. Cover components consume an
authorized resource ID/URL returned by the API and show deterministic fallback,
loading, and broken-image states. Ordinary screens do not display raw NAS paths
or sensitive scan diagnostics.

## Accessibility

- Use semantic HTML before custom roles: buttons for commands, links for
  navigation, headings in order, lists/tables for structured collections.
- Every form control has a programmatic label and associated error/help text.
- Keyboard focus remains visible and returns predictably when a dialog closes.
- Dialogs keep focus inside while open, have an accessible name, and support escape/cancel when
  cancellation is safe（原生 `<dialog>` 的行为见上文）。
- Status changes and scan progress use appropriately restrained live regions;
  do not announce every file.
- Icon-only controls have accessible names and tooltips where meaning is not
  obvious.
- Do not rely on color alone for scan, evidence, error, or permission status.
- Test narrow and wide viewports; text and controls must not overlap or rely on
  hover.

## Styling And Composition

Use the design/styling system selected by the initial frontend task and record
that decision. Do not introduce a second component or styling framework in a
feature patch. Keep compact operational panels readable; reserve large display
type and decorative layout for contexts that warrant it.

Extract a shared component only after multiple real consumers need the same
semantics. Prefer feature-local composition over a mega-component controlled by
many flags. Do not nest decorative cards simply to create spacing.

Large libraries require pagination or bounded rendering from the API. Add
virtualization only after the list size and interaction need are demonstrated;
never render 100,000 tracks in one component.

## Tests

Component tests assert visible behavior, keyboard interaction, accessible names,
and emitted intent across important states. Avoid snapshot-only tests. A catalog
test should prove Medium/Track hierarchy and empty/error states rather than the
exact incidental DOM tree.

`shared/ui` 的组件有各自的组件测试（Testing Library，按角色与可访问名称查询）；页面行为由 `src/app/*.test.tsx` 的整页测试
覆盖；浏览器里的键盘路径、焦点、五种宽度与 axe 由 Playwright 覆盖（见[质量规范](./quality-guidelines.md)）。

## Anti-Patterns

- `useEffect(() => fetch(...))` inside presentational components.
- Casting `response.json()` in a component.
- Checking `user.role === "admin"` and assuming a mutation is authorized.
- Using absolute source paths as image URLs or element keys.
- A single Release component that fetches, mutates, handles scan polling, maps
  errors, and renders every child.
- Restoring V0's visual dependencies without a current design decision.

See [Hook Guidelines](./hook-guidelines.md),
[Type Safety](./type-safety.md), and the
[Core 0 UI requirements](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
