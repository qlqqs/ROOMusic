# Hook Guidelines

## Role Of Hooks

Hooks own reusable stateful React coordination for one feature concern. They may
connect a typed feature API client to components, synchronize URL/local state,
or manage a lifecycle such as scan polling. They do not become service locators
or alternative business-policy engines.

2026-10-01 的 M1a 已按下述生命周期与边界实现 hook（`features/*/hooks/`）。TanStack Query
（[ADR 0001](../../../docs/architecture/adr/0001-frontend-foundations.md) 决策记录）是服务器状态的唯一缓存边界，统一请求、
取消和缓存，不能与无归属请求混用；`queryFn` 必须消费 Query 传入的 `signal` 并交给 `apiRequest`。

## 已实现的 hook 形态

- **查询 hook**（`useRoots`、`useScan`、`useActiveScan`、`useMembers`、`useLibraryTotals`……）：从 `useViewerScope()` 取会话
  代次，查询键由 feature 的 `query-keys.ts` 构造，用 `select` 取出需要的数据，返回 Query 的结果交给 `QueryContent`
  渲染加载、错误重试与就绪。轮询用 `refetchInterval` 的函数形式，只在非终态返回间隔。
- **命令 hook**：把写操作建模为“意图”。目录与成员的命令 hook（`useRootCommands`、`useMemberCommands`）的 `state` 是
  `idle | pending | succeeded | failed` 的可辨识联合（失败态带 `failure` 与 `uncertain`）；`submit(intent, callbacks)` 发起新意图，
  `dismiss()` 清除结果。目录命令在发起时生成幂等键，`retry(callbacks)` 沿用同一命令与同一个键；成员命令没有幂等保证，所以
  没有 `retry`。`onSettled` 返回刷新相关查询的 Promise，命令在刷新完成后才结束；回调先经 `scope.isCurrent()` 检查发起时的会话
  代次，旧会话的迟到结果不更新界面、不导航。开始与取消扫描（`useStartScan`、`useCancelScan`）是更简单的 mutation：开始扫描
  重复发送只会得到同一个扫描，不需要幂等键；取消被接受不等于已取消，结束前先刷新扫描状态，按钮保持忙碌。
  `useStartScan` 的变量是扫描模式（S ④）：`incremental` 不发请求体，`full`（“完整重扫”）发 `{"mode":"full"}`；`useBeginScan().begin(mode?)` 缺省为增量，
  `StartScanFeedback` 的“重试”用 `start.variables` 沿用失败那次的模式。
- **受治理命令 hook**（M3，`useReleaseCommands`）：同一个命令状态机承载 M3 的四种意图（修正、归入、移出、撤销；M4 增加第五、第六、第七种，见本条末尾），每个新意图在发起时生成幂等键，
  结果不确定时 `retry` 沿用同一个命令与键；`refresh` 与 `onMoved` 由 app 传入（governance 不知道别的 feature 的查询键，也不依赖路由）。
  版本换了专辑组之后**先导航再刷新**：`onMoved` 在 mutation 的 `onSuccess` 里同步调用，刷新放在 `onSettled`——否则刷新读到“原来的专辑已没有这个版本”，
  详情页的地址规范化与这次导航争抢、旧专辑闪一下“专辑不存在”。返回的对象只在状态变化时换新（`useMemo`），页面才能把它放进 `useCallback` 的依赖。
  命令状态由页面持有，因为写操作之后路由可能换到另一个专辑组、详情组件重新挂载，结果提示要留在页面上；撤销对话框的开合与焦点兜底在 `useRevertFlow`。
  M4 增加第五种意图 `default`（`{release, group: {id, revision}, target}`，`target` 为 null 是改回自动选择）：专辑不变，但代表版本可能变了，
  所以成功后同样先 `onMoved(group_id, release.id)` 让页面停在刚才的版本（它不再是代表版本时地址带上 `release`），再刷新。
  撤销专辑级记录（默认版本）时，命令里的版本取页面当前显示的版本、`returnGroupId` 是这张专辑；操作记录页没有当前版本，不跟随导航。
  M4 第二项增加第六种意图 `review`（`{release, revision, issue, action, basis?}`，核对带页面读到的依据令牌，取消核对不带）：不移动版本，不调用 `onMoved`；
  撤销它的命令 `subject` 是 `review`（成功提示说“已撤销这次核对”），撤销对话框按记录的改动类型 `release_issue_review` 选它。`409 issue_changed` 与修正的
  `revision_conflict` 一样经 `onSettled` 刷新，页面说明“刚刚变了”。
  M4 第三项增加第七种意图 `claim`（`{release, revision, source: {id, title}, sourceRevision, preview}`，`release` 是新版本，`preview` 是预览令牌）：认领发生在待认领页，那里不跟随版本的移动，不调用 `onMoved`；遇到 `claim_changed` 或 `revision_conflict` 后经 `onSettled` 刷新，页面重新读取详情与预览；结果不确定时用同一个键重试。撤销沿用 `revert`，`subject` 增加 `claim`；从操作记录撤销认领之后页面跟着新版本回到认领之前所在的组（记录改动摘要里的 `previous_group_id`，与撤销归组相同；撤销记录不带）。
- **待认领页的查询**（M4 第三项，`useClaims`、`useClaim`、`useClaimPreview`、`useClaimHints`）：都是 `staleTime: 0`，每次打开重新读取（旧版本的集合、推荐和预览令牌都随扫描与认领变化）；列表翻页时 `keepPreviousData`，详情与预览换版本不保留上一个的数据；预览与提示 `retry: false`（失败就是没有，不为线索重试三次）；键都在 `governanceKeys.all` 之下，命令结束时整体失效。
- **问题页的查询**（M4，`useIssues`、`useReleaseIssues`）：问题列表一页一个条目（状态、类别、页码与页大小都在键里），翻页与换筛选时 `keepPreviousData`；
  版本的问题按版本 ID 一个条目，换版本不保留上一个版本的数据。两者 `staleTime` 都是 0：问题随扫描变化，依据令牌要与服务器当前的情况一致。
  键都在 `governanceKeys.all` 之下，命令结束时整体失效，再由 app 的 `refresh` 更新目录的“需要关注”。
- **容器宽度**（M4，`useContainerWide`）：问题页按自己的宽度在两栏与顺序导航之间切换（看容器不看视口，带侧栏时 1024px 视口里内容区不到 900px）。两种布局渲染的元素不同，
  只靠 CSS 容器查询做不到，所以用 `ResizeObserver` 量宽度；没有 `ResizeObserver`（jsdom）时按宽屏处理，测试要窄屏时自己提供。
- **核对之后的焦点**（M4，`ReviewAction`）：核对或取消之后被点的按钮换成另一个，焦点会掉回页面顶部；这时把焦点带到这一节的标题（`tabIndex=-1`），
  下一次 Tab 就到新的按钮。由 Playwright 的纯键盘用例发现，Vitest 有回归用例。
- **地址规范化暂缓**（M4，`AlbumDetailView`）：组详情正在刷新（`isFetching`）时不做地址规范化，刷新结束再按新数据规范化。命令先导航、后刷新，
  刷新完成之前缓存里的旧成员列表可能还不含这个版本，或还把旧的代表版本当作代表版本，按旧数据规范化会去掉 `release`，页面停在别的版本。
  判据必须是视图自己读到的刷新状态，不能是“页面传入、命令进行中为真”的开关：归入一张已有缓存的专辑时，命令在新专辑的视图挂载之前就结束了
  （刷新只覆盖当时挂载着的查询），新视图挂载后才开始刷新那张专辑的缓存。M3 的归组在这种情况下会落在目标专辑原来的代表版本上，M4 一起修好。
- **版本对比**（`useReleaseComparison`，M4）：只在对比对话框打开后挂载，没有人打开时不发请求；键在 catalog 的 `all` 前缀下，写操作之后随目录一起失效。
- **操作记录查询**（`useOperations`）：键里带发行与专辑筛选（M4，两者都给时后端取并集）、页码与页大小；翻页用 `keepPreviousData` 保留上一页；`staleTime` 为 0（审计视图每次打开都重读，
  别的管理员的修改也能看到，先显示缓存再刷新）；展开才请求的区块把请求放在只在展开后挂载的子组件里，不靠 `enabled` 开关。
- **派生值留在来源处**：扫描的阶段、文案与是否终态由 `scan-status.ts` 的纯函数从后端状态推出，不用 effect 同步。
- **到终态的副作用**：`useScan` 在读到终态时用 effect 使活动扫描查询失效（依赖 `finished` 布尔值而不是整份数据，只触发一次）。

## Planned Layers

```text
component -> feature hook -> feature API function -> shared HTTP transport
                                   |
                                   -> feature runtime decoder
```

- The shared transport owns credentials, common headers, safe body handling, and
  common error-envelope decoding.
- The feature API function owns endpoint, request/response DTO, and runtime
  decoding.
- The hook owns cache/query lifecycle and maps DTOs into feature state.
- The component owns rendering and local interaction.

## Hook Design

- Name hooks `use...` and make the name describe one concern:
  `useReleaseDetail`, `useScanRun`, `useDisableLibraryRoot`.
- Return a narrow typed object or discriminated state. Do not expose the entire
  cache client or a generic dispatch function.
- Keep derived values computed from their source instead of synchronized through
  another effect/state pair.
- Keep dependencies complete and stable. Fix unstable object/function creation
  at the owner; do not suppress hook dependency rules.
- Clean up timers, subscriptions, observers, and abort signals.
- Do not start a mutation during render.
- Avoid a hook that merely renames `useState`; extraction should centralize a
  real lifecycle or contract.

## REST And Session Behavior

All Core 0 requests target versioned REST. Production is same-origin, and the
HTTP client includes cookie credentials according to the selected transport
mechanism. No hook reads an auth cookie, stores JWT/access tokens, or attaches a
browser-readable bearer token.

A current-session hook consumes a safe `/api/v1` session/user response. Its role
projection controls presentation only; 401/403/409 behavior still comes from
the backend and must be handled for every query/mutation.

Runtime decoding happens once in the feature API boundary. Hooks receive typed
DTOs and classified `ApiError` values; they do not use `as` casts to recover
unknown response fields.

## Queries, Mutations, And Polling

- Query identity contains every server-visible input: resource ID, search text,
  filters, pagination, and viewer-specific scope where applicable.
- Search/filter state that should survive reload or be shareable belongs in the
  URL, then feeds the query key.
- Mutations send expected revision and idempotency fields when the REST contract
  requires them. On 409, preserve user context and expose refresh/reconcile
  behavior.
- Invalidate or update only the affected feature queries after success. Do not
  clear the entire cache because ownership is unclear.
- Prefer pessimistic updates for destructive-looking administrative state
  changes. Optimistic updates require a deterministic rollback and revision-safe
  response.
- Poll scan status only while it is non-terminal, back off according to the
  selected mechanism, pause when appropriate, and stop/clean up on terminal
  state or unmount.
- Abort obsolete searches and ignore stale completions through the chosen query
  mechanism; do not let a slower previous query replace a newer result.

Planned consumption shape:

```ts
const scan = useScanRun(scanRunId);

switch (scan.status) {
  case "loading":
  case "running":
  case "succeeded":
  case "failed":
  case "canceled":
  case "incomplete":
    return renderScanState(scan);
}
```

The exact API depends on the selected library; exhaustive visible states do not.

## Tests

Test hooks through observable behavior with a controlled API boundary. Cover
success, classified error, cancellation/stale response, mutation conflict, and
polling termination when relevant. Do not assert internal hook call ordering as
a substitute for user-visible outcomes.

实际做法：hook 通过整页测试（`src/app/*.test.tsx`）覆盖——经 `renderApp` 装配真实的应用运行时，后端是有状态的替身
（`src/test/fake-library.ts`、`fake-members.ts`，实现幂等重放、`expected_revision` 与 409，响应取自真实样本并经真实 decoder，
可注入“请求丢失 / 响应丢失”），轮询用假定时器推进，断言用户可见的结果（提示文案、列表、焦点、发出的请求）。

## Anti-Patterns

- Fetching directly in every component.
- One `useApi` hook with arbitrary paths and untyped generic return casts.
- A global `useAppState` hook containing auth, search, scan, and dialog state.
- Polling that continues after success or when the route is gone.
- Mutating cache data in place.
- Treating a frontend admin flag as permission to skip handling 403.
- Adding GraphQL/subscription hooks for Core 0 scan progress.

See [State Management](./state-management.md),
[Type Safety](./type-safety.md), and
[Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md).
