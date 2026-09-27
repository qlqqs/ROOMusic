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

这是恢复客户端的目标组合模式，不是当前源码现状。`frontend/package.json` 已声明
React 与 lucide-react，但 `src/` 尚不存在。按 [Classical 设计依据](../../../frontend/DESIGN.md)
组织组件与状态；新组件库或样式依赖仍须在对应子任务及 ADR 中批准，不能因原型包含某个
控件而假定其业务能力已实现。

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
- Dialogs trap focus, have an accessible name, and support escape/cancel when
  cancellation is safe.
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
