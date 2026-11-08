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
| `Page`、`Panel`、`StatList` | `Page` 渲染唯一的 `h1`（并设置“页面名 · ROOMusic”标题），标题很长时 `compactTitle` 降一级字号（M1b：专辑名超过 80 个字符），完整显示并换行；给 `header` 时它替换默认页头（标题、说明与操作），由它渲染唯一的 `h1`，文字与 `title` 相同（歌单与我喜欢的音乐的封面页头，2026-11-08）；`Panel` 是带标题的区段，给 `headingRef` 时标题可以被程序聚焦（`tabIndex=-1`，不进 Tab 顺序）；`StatList` 显示数值，数据值用 `tabular-nums` |
| `QueryContent` | 数据区块的首次加载（保留结构的 `Skeleton`，不画成空库）、失败（带请求 ID 的 `FailureNotice` 与重试）、成功，以及“刷新失败但还有旧数据”（旧数据继续显示，错误在其上方）；它只依赖最小的查询形状，不依赖查询库 |
| `Notice`、`FailureNotice` | 行内状态或错误，由图标与文字表达，不靠颜色；错误 `role="alert"`，其余 `role="status"`；可附请求 ID 与下一步操作；**不使用全局 toast**，结果就近显示在操作区 |
| `Button`、`TextField` | `Button` 有主要 / 次要 / 只有图标三种外观（`primary | secondary | icon`），只有图标时必须给 `label`，用作 `aria-label`，也是悬停提示的文字（`tooltip` 可另给提示文字，如心形状态未读到时的说明）；不用原生 `title`（2026-11-08 起）；忙碌态不设 `disabled`（焦点留在按钮上），点击被忽略并设置 `aria-busy`，文案由调用方改成“正在…”；`TextField` 的 label、说明与错误经 `aria-describedby` 关联，错误同时设置 `aria-invalid` |
| `StatusTag` | 文字加图标，不只靠颜色 |
| `useTooltip`（2026-11-08） | 图标按钮与图标链接的悬停提示：返回 `triggerProps`（放到触发元素上）与 `tooltip`（渲染在触发元素旁）。只响应鼠标，停留 300ms 显示，移开、按下、Esc、滚动或窗口缩放立即消失，按下后要移出再移入才会再出现；键盘焦点不弹，提示 `aria-hidden`（读屏靠 `aria-label`）。浮层 fixed 定位，渲染到 `trigger.closest("dialog") ?? document.body`，不被表格、播放器栏裁掉，默认在上方、放不下翻到下方。`Button variant="icon"`（含 `MenuButton iconOnly`）、心形、侧栏展开按钮都用它；新加的图标控件也用它，不写 `title` |
| `Avatar`（2026-11-08） | 圆形头像，内容是用户名的第一个字素（拉丁字母大写）；装饰性（`aria-hidden`），旁边总要写出用户名。用户菜单与歌单页头共用 |
| `ModalDialog`、`ConfirmDialog` | 原生 `<dialog>` 的模态：有可访问名称，打开时焦点进入（确认对话框初始焦点在“取消”，避免回车直接确认），Esc 与取消可关闭，关闭后焦点回到触发按钮；提交中确认忙碌、取消与 Esc 暂时无效；阻塞性错误留在对话框内。子内容只在打开时渲染，关闭即丢弃其中的私有状态 |
| `DataTable` | 真正的 `<table>`（有 caption、`rowHeader`）；以**容器宽度**（CSS 容器查询，小于 640px）而不是视口宽度决定是否堆叠成“标签：值”，因为带侧栏时 768px 视口里内容区只有约 500px；不产生任何横向滚动（局部滚动区需要可聚焦，会引入额外的无障碍负担） |
| `Disclosure`（M3） | 页面底部折叠的区块（“来源与证据”“修改记录”）：原生 `<details>` 的展开状态由 React 持有，**折叠时不渲染内容**（没有可聚焦的隐藏控件，内容里的请求也在展开后才发出），展开时挂载；标题行是原生 `summary`（Enter 与空格展开），可经 `summaryRef` 取到，区块外的操作结束后可把焦点带回；换对象时用 `key` 回到折叠 |
| `Pagination`（F3） | `<nav>` 带可访问名称（如“扫描记录分页”，同一页面可能不止一个分页），内容是“上一页”“第 x / y 页”“下一页”；当前页 `aria-current="page"`；到头的方向显示为 `aria-disabled` 的文字而不是消失，布局不跳动；只有一页或没有数据时不渲染；链接由调用方经 `renderLink` 生成（通常是路由 `Link`，组件不依赖路由），上一页与下一页带 `rel`。页码的 URL 约定见[状态规范](./state-management.md) |

页面级约定：

- 外壳有“跳到主要内容”链接、`nav` 与 `main` 地标；导航用链接，当前页有 `aria-current`；窄屏的“菜单”按钮打开原生 `<dialog>` 模态导航，
  打开时焦点进入，Esc 关闭并把焦点还给“菜单”按钮，选择链接后关闭。
- 设置页布局与旧地址重定向见[状态规范](./state-management.md)“URL State”。
- 外壳底部是用户菜单（`app/shell/user-menu.tsx`）：一整行按钮（`Avatar` 首字头像、用户名、角色），可访问名称显式写 `aria-label="用户名 角色"`（`.who` 是 grid，空白文本节点不可靠）；
  菜单向上弹出“设置 / 退出登录”；在 `/settings/*` 时这一行有当前样式（`data-current`，不加 `aria-current`）；退出失败的提示在按钮上方同一 section 内。
  窄屏对话框里用户区贴底（内容区纵向 flex + `margin-top: auto`），选“设置”先关菜单再关对话框。
- 菜单用 `useMenu`（`shared/ui/menu-button.tsx`）：`placement: "above" | "below"`、`matchTriggerWidth`、菜单项 `icon`（不进可访问名称）；
  浮层渲染到 `trigger.closest("dialog") ?? document.body`——模态 `<dialog>` 打开时 body 之外的内容是惰性的，渲染到 body 的菜单在对话框里点不了。
  `MenuButton` 是它的薄包装。菜单项是按钮，导航类菜单项（“设置”）在 `onSelect` 里 `navigate`，这是“导航用链接”的例外。
- 管理页由 `AdminGate` 把关：非管理员显示“需要管理员权限”，不渲染页面内容，也就不调用管理接口；后端仍独立鉴权。
- 停用与撤销类操作先确认并说明影响范围，恢复与重新启用直接执行；目标是自己时对话框额外提示会被退出登录。
- 失败文案按**错误码**选择（各 feature 的 `*-messages.ts`，`as const` 映射），不比较后端 message；未知错误码显示后端的安全
  message 与请求 ID（`describeFailure`）。
- 扫描阶段只在变化时经礼貌型 live region 播报一次，轮询得到相同阶段不重复播报。M5 ⑥ 起只有服务端给出的确定进度（`progress.percent_bp`）才显示百分比，
  前端不推算、不补动画；剩余时间（ETA）仍不做。
- 扫描进度（M5 ⑥，`library/components/scan-progress.tsx`，概览与扫描详情同源）：只读的 `role="progressbar"`，名称“扫描进度”，`aria-valuemin=0`、
  `aria-valuemax=100`、`aria-valuenow=floor(percent_bp/100)`（没有成功结束的扫描最多 99），`aria-valuetext` 写出当前目录与阶段、全局文件累计与整体百分比
  （如“第 2 / 共 3 个目录，读取标签，已处理 1,200 / 2,400 个文件，整体进度 42%”；计数时“正在数文件，已检查 1 / 3 个目录”，不把目录单位读成文件）。
  没有进度数据时进度条不给数值：运行中说“正在准备”，旧扫描不显示这一块。文件比计数时多时写“已处理 A 个文件（计数时 T 个）”，不写成 A / T。
  阶段说的是当前目录（允许“第 1 个目录保存”之后是“第 2 个目录读取标签”，整体进度不倒退）；全部目录处理完时是“保存扫描结果”。
  目录序号所有成员可见，目录名称只在管理员的扫描详情显示。播报用 `aria-live="polite"`，只读阶段/目录那句话（变化时读一次），扫描详情的终态仍由状态标签的
  `role="status"` 播报。失败、取消、不完整停在停下的位置，换中性色并写“停在……”，不补满；取消请求中仍显示进度。不做滑块，不加键盘调节，不抢焦点。
- 扫描报告（M5 ⑥，管理员的扫描详情，`scan-report.tsx`）：`ready`/`partial` 才请求；`partial` 用一句话说明“只有停下之前已经保存的变化”；`unavailable`
  （或读取时 503 `scan_report_unavailable`）只说“这次的报告没能生成，扫描结果已经保存。”；`legacy`/`pending` 不显示。问题数到上限写“至少 100 条”。
  新增专辑列出扫描时的标题与艺人，现在还看得到就链接到当前所在的专辑，看不到时标“现在看不到了”、不给入口；“可能接替了《…》”的链接沿用
  `/claims?release=<旧版本>&target=<新发行>`，不在报告里创建认领命令。
- 概览“最近新增”（M5 ⑥，catalog 的 `RecentlyAddedPanel`）：所有成员可见，接口给最多 8 张，卡片复用 `AlbumGrid`/`AlbumCard`，“查看全部”到 `/albums?sort=added_at_desc`；
  只显示能排满的行：一行能放 3 张及以上时只显示一行（张数等于列数，最多 8），每行 1–2 张时最多两行。列数由 `useGridColumns` 读浏览器解析出的
  `grid-template-columns`（auto-fill 展开后的列数）并随 ResizeObserver 更新，列宽规则只写在 CSS 里；多出的卡片不渲染（不可 Tab、读屏读不到）；
  量不到列数（jsdom 没有布局）时全部显示，测试要验证行数时替换 `getComputedStyle`/ResizeObserver；
  加载与失败照常显示，成功且为空时整块不渲染（概览外层的 `.wide:empty` 不留空行）；接口 404（旧服务）按没有新增处理。
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
| `AlbumHeader`、`VersionNav` | 头部是封面加概况：一行灰色小字、细竖线分隔、只写值（`ul[aria-label="概况"]`，每项带只给读屏的“年份：”等标签；年份未知不写，介质多于 1 张才写“N 张碟”，流派与总时长有才写，有一首曲目时长未知就不写总时长，总时长用 `formatSpokenDuration` 口语写法；窄屏换行时分隔线不出现在行首，2026-11-08 起）；版本导航是 `<nav aria-label="版本">` 里的链接，不是 ARIA 标签页，各版本标题不同时才附标题；生效中的默认版本带 `DefaultVersionTag`（M4，所有成员可见，自动选择时没有），标题旁的“对比版本”打开对比对话框 |
| `VersionComparisonDialog`、`ComparisonTable`（M4） | `ModalDialog`，`min(980px, 100vw - 边距)`、最高 90vh，标题行 sticky、内容在对话框内部滚动，初始焦点在标题上；内容只在打开时挂载（打开后才请求）。“字段 × 版本”的真表格（显式 ARIA 角色），以第一列为基准：不同的格加金色下划线并带读屏文字“（与版本 1 不同）”，没有值写“—”加读屏的“没有记录”，有人工修正带 `ManualCorrectionTag`；行的取舍与文字在纯函数 `comparison.ts`（全空的行不出现，标题与专辑艺人只在不同时出现，音频格式写参数不写“最高音质”）；容器窄于 640px 时每个字段堆叠成“版本 N：值”；列头的“查看这个版本”关闭对话框并切换版本 |
| `ReleaseMedia` | 一张介质时直接是 `<ol role="list">`，多于一张时每张一个 `<section aria-labelledby>` 加“跳到第 N 张”链接（点击把焦点放到分段标题上并滚到视野，不改地址）；分段用 `content-visibility: auto`，占位高度只写块方向的 `contain-intrinsic-block-size`（简写 `contain-intrinsic-size` 带一个值会同时给宽度，窄屏上被跳过的分段有 480px 的固有宽度，整页横向溢出） |
| `ReleaseFacts`、`EvidenceSection` | 发行信息里没有值的条目不列出；“来源与证据”是 `Disclosure`（折叠时不渲染内容，换版本用 `key` 回到折叠），有人工修正的字段在证据表里标 `ManualCorrectionTag`（“人工修正”，文字加图标）；管理员的“查看完整证据”按钮在展开后才出现，点击后才请求，按钮留在原处（加载中忙碌、之后可重新加载）使焦点不丢 |
| `ReleaseMedia` 的 `CreditsLine` | 曲目行署名写成“组名：名字、名字；组名：名字”，不加解释；有 `detail`（乐器或工种）的按细分成组，组名用 `creditDetailLabel`（已知键中文名，歌词或标签原写法原样），否则按角色、组名用 `creditRoleLabel`；组与名字都按接口顺序（后端按作词→作曲→编曲→指挥→演奏→制作人排好） |
| `labels.ts` | 署名角色、署名细分、证据字段、来源、可信度、处理方式、原因码与归组方式的中文名；清单按后端当时的取值核对，**未知取值原样显示**，后端新增取值不会让页面出错（样本里出现过的取值都有中文名，由测试保证） |

## 已实现的受治理操作组件（M3，`features/governance`）

管理员的受治理写操作、元数据页与待认领页（M4 第三项）的组件约定已移到 [governance-components.md](./governance-components.md)（本文件接近 24 KB 的上限；内容原样搬移，之后的增补写在那里）。

## 已实现的收藏与歌单组件（M5 ② ③，`features/personal`）

心形、批量收藏状态范围、收藏页、歌单页、“添加到歌单…”对话框与各页共用的曲目行动作（`app/routes/track-actions.tsx`）的约定写在
[personal-guidelines.md](./personal-guidelines.md)。

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
`pending` 如需展示，只是本地请求状态，不是新增远端扫描枚举。诊断计数不是百分比进度，也不能用来推算进度。
不把不完整扫描标为成功，也不暗示失败扫描做了全部目录的缺失对账；停下之前已经保存的变化（包括确认被新文件替换的旧曲目）会保留，
所以非成功终态说“没扫到的曲目保持原样”，不说“已有的曲目不受影响”。

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
  do not announce every file.（M5 ⑥：只播报计数/正式处理、目录序号与阶段的变化及终态，换目录即使阶段相同也播报。）
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
