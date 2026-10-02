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
| `Page`、`Panel`、`StatList` | `Page` 渲染唯一的 `h1`（并设置“页面名 · ROOMusic”标题）；`Panel` 是带标题的区段；`StatList` 显示数值，数据值用 `lining-nums tabular-nums` |
| `QueryContent` | 数据区块的首次加载（保留结构的 `Skeleton`，不画成空库）、失败（带请求 ID 的 `FailureNotice` 与重试）、成功，以及“刷新失败但还有旧数据”（旧数据继续显示，错误在其上方）；它只依赖最小的查询形状，不依赖查询库 |
| `Notice`、`FailureNotice` | 行内状态或错误，由图标与文字表达，不靠颜色；错误 `role="alert"`，其余 `role="status"`；可附请求 ID 与下一步操作；**不使用全局 toast**，结果就近显示在操作区 |
| `Button`、`TextField` | `Button` 有主要 / 次要 / 文字三种形态；忙碌态不设 `disabled`（焦点留在按钮上），点击被忽略并设置 `aria-busy`，文案由调用方改成“正在…”；`TextField` 的 label、说明与错误经 `aria-describedby` 关联，错误同时设置 `aria-invalid` |
| `StatusTag` | 文字加图标，不只靠颜色 |
| `ModalDialog`、`ConfirmDialog` | 原生 `<dialog>` 的模态：有可访问名称，打开时焦点进入（确认对话框初始焦点在“取消”，避免回车直接确认），Esc 与取消可关闭，关闭后焦点回到触发按钮；提交中确认忙碌、取消与 Esc 暂时无效；阻塞性错误留在对话框内。子内容只在打开时渲染，关闭即丢弃其中的私有状态 |
| `DataTable` | 真正的 `<table>`（有 caption、`rowHeader`）；以**容器宽度**（CSS 容器查询，小于 640px）而不是视口宽度决定是否堆叠成“标签：值”，因为带侧栏时 768px 视口里内容区只有约 500px；不产生任何横向滚动（局部滚动区需要可聚焦，会引入额外的无障碍负担） |
| `Pagination`（F3） | `<nav>` 带可访问名称（如“扫描记录分页”，同一页面可能不止一个分页），内容是“上一页”“第 x / y 页”“下一页”；当前页 `aria-current="page"`；到头的方向显示为 `aria-disabled` 的文字而不是消失，布局不跳动；只有一页或没有数据时不渲染；链接由调用方经 `renderLink` 生成（通常是路由 `Link`，组件不依赖路由），上一页与下一页带 `rel`。页码的 URL 约定见[状态规范](./state-management.md) |

页面级约定：

- 外壳有“跳到主要内容”链接、`nav` 与 `main` 地标；导航用链接，当前页有 `aria-current`；窄屏的“菜单”按钮打开原生 `<dialog>` 模态导航，
  打开时焦点进入，Esc 关闭并把焦点还给“菜单”按钮，选择链接后关闭。
- 管理页由 `AdminGate` 把关：非管理员显示“需要管理员权限”，不渲染页面内容，也就不调用管理接口；后端仍独立鉴权。
- 停用与撤销类操作先确认并说明影响范围，恢复与重新启用直接执行；目标是自己时对话框额外提示会被退出登录。
- 失败文案按**错误码**选择（各 feature 的 `*-messages.ts`，`as const` 映射），不比较后端 message；未知错误码显示后端的安全
  message 与请求 ID（`describeFailure`）。
- 扫描阶段只在变化时经礼貌型 live region 播报一次，轮询得到相同阶段不重复播报；不显示百分比或剩余时间。
- 用户内容（用户名、目录名等）允许任意位置换行（`overflow-wrap: anywhere`），超长值不撑破布局。
- 表格里每一行都重复的链接文字（“查看详情”）用 `VisuallyHidden` 补上对象（如开始时间），读屏的链接列表里能分辨是哪一行。
- 原生 `<dialog>` 模态在最后一个可聚焦元素之后会把焦点交给浏览器界面或 `body`，不是严格的循环；测试断言“焦点始终在对话框内或
  在 `body` 上”，而不是要求严格循环。

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
