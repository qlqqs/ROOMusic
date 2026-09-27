# State Management

## Principle

Keep each fact at its natural owner and derive everything else. Server resources
remain server state, shareable filters remain URL state, transient interaction
remains local component state, and authentication authority remains on the
backend.

当前 Core 0 未引入状态库或 query/cache 层；server state 由页面 effect 加载，
URL 搜索参数由 History API 管理，短生命周期交互使用本地 `useState`。不得把
该过渡实现误认为目标全局 store，也不得默认新增状态库。

## State Categories

| Category | Examples | Owner |
| --- | --- | --- |
| Server state | current user, library roots, scan runs, Releases, search results, Change Sets | selected query/cache mechanism through typed feature API |
| URL state | search query, filters, sort, page, selected tab when shareable | router/search parameters |
| Local UI state | dialog open, focused row, draft input, disclosure state | nearest component/feature |
| Form state | login, setup, directory command inputs and field errors | form/component until submit |
| Durable operation state | revision, operation status, recovery availability | backend; frontend displays/refetches |
| Session credential | opaque cookie token | browser cookie jar + backend; never JavaScript state |

A view model derived from server data may be memoized for rendering, but it is
not a second editable source of truth.

## Server State

- Feature API boundaries decode responses before cache insertion.
- Query identity includes every input that changes the result.
- Preserve paginated/bounded access for large libraries; do not fetch the full
  catalog into a client store.
- Refetch or invalidate the smallest affected resource after a mutation.
- Treat 401, 403, 409, and 503 as distinct states.
- Scan progress derives from the durable scan-run resource. A local percentage
  cannot redefine succeeded/failed/canceled/incomplete.
- PostgreSQL remains the Core 0 authority. The frontend must not assume
  Meilisearch lag, Redis task semantics, or subscriptions.

Current-user data is server state, not proof of authorization. It may guide the
shell and controls, but the client still handles permission denial on every
request.

## URL State

Search text, stable filters, sorting, and pagination belong in the URL when users
should be able to reload, bookmark, or use browser navigation. Normalize and
validate URL values at the search feature boundary; invalid values fall back to
documented defaults without sending arbitrary backend fields.

当前发行列表的 `q`、`attention=required` 和 `page` 都是 URL state。提交新搜索、清空
搜索或切换 attention 时把 `page` 重置为 1；上一页/下一页只写入 `[1,lastPage]`。
服务端返回总数后，若 URL page 超出真实末页，使用 `replaceState` 规范化 URL 并请求
末页，避免把可恢复的过期书签永久显示为空结果。`popstate` 必须重新读取三项状态，
不得维护一份与 URL 分叉的隐藏分页真相。

当前详情选择同样由 URL 的 `release` 参数持有：读取和写入时都先 trim，只接受非空且
不超过 255 个 JavaScript 字符单元的 opaque ID；超长值按未选择处理并从规范化 URL
删除，禁止带着任意长的深链接值发起详情请求。打开或关闭详情只增删 `release`，必须
保留 `q`、`attention` 和 `page`。

Do not put session tokens, absolute paths, secrets, raw errors, or uncommitted
operation payloads in the URL.

## Local And Global State

Keep state local until two distant, active consumers truly need the same
client-owned lifecycle. Before adding global state, prove that the value is:

1. not server-owned;
2. not representable in the URL;
3. needed across route boundaries or unrelated component branches; and
4. stable enough to justify a public mutation contract.

A toast queue or confirmed display preference may eventually qualify. A release
response, scan run, dialog boolean, or form draft does not.

## Mutations And Concurrency

Administrative commands include expected revision and idempotency input when
required by REST. Preserve the draft on a revision conflict, show that the
resource changed, and offer a safe refresh/reconcile action.

Use pessimistic UI by default for directory disable/restore and other persistent
management actions. Optimistic state is allowed only when rollback is
deterministic and the server result/revision replaces the optimistic value.

Do not mark an operation complete merely because its request was accepted.
Follow the returned operation or resource status contract. Change Set and
Operation Journal are backend records; the UI does not synthesize recovery
availability from button history.

Approval and execution are separate server-owned state machines. Future Agent
UI must not collapse them into one `isRunning` boolean. Preserve at least:

```ts
type ApprovalState =
  | { status: "not_required" }
  | { status: "awaiting_user" }
  | { status: "reviewing" }
  | { status: "approved"; approvalId: string }
  | { status: "rejected"; reasons: string[] }
  | { status: "needs_human_confirmation"; reasons: string[] }
  | { status: "review_unavailable" };

type ExecutionState =
  | { status: "planned" }
  | { status: "running"; operationId: string }
  | { status: "succeeded"; operationId: string }
  | { status: "partially_failed"; operationId: string }
  | { status: "failed"; operationId: string }
  | { status: "rolling_back"; operationId: string }
  | { status: "rolled_back"; operationId: string }
  | { status: "rollback_failed"; operationId: string };
```

This is a semantic example, not a finalized wire DTO. The stable requirement is
that Operator uses `not_required`, not a fake automatic approval; Steward review
failure never becomes approval; and execution/recovery state always comes from
the backend journal.

## Session Lifecycle

The app bootstraps a safe current-session endpoint. Logout, expiry, revocation,
or user disable clears viewer-scoped server state and returns the UI to the
appropriate unauthenticated flow. No token is copied to localStorage,
sessionStorage, IndexedDB, URL state, or a global store.

Production requests are same-origin with the Go server. Development uses the
documented proxy/credentials setup, not a second auth scheme.

## Anti-Patterns

- Copying query results into a global store through effects.
- Storing the same search filters in URL, component state, and cache state.
- A boolean `isAdmin` used to assume a mutation will succeed.
- Client-generated scan completion or missing-source decisions.
- Clearing all cached data after every mutation.
- Optimistically disabling a root without revision-conflict recovery.
- 在 Core 0 中引入未获任务授权的真实播放基础设施、Agent 会话或 PWA 离线存储；本地模拟播放、收藏和歌单遵循下述例外合同。

## 本地音乐客户端状态

经 music-client-experience 任务批准，模拟队列、收藏、歌单和最近播放属于跨页面的客户端状态，由各 feature 的 hook 统一管理，不引入全局状态库。仅保存有限的曲目标识和展示快照，服务端目录仍是权威来源。

持久化结构带版本，读取先校验未知数据，再交给内部类型。存储键必须包含真实用户身份或独立演示身份；退出、失效及账号切换时不能将前一身份的内存写入后一身份。恢复队列必须暂停，不能在刷新后启动模拟时钟。

任何受认证接口返回 `unauthorized` 都必须结束当前会话，包括目录、详情、证据、扫描和管理命令；不能仅显示错误后留下旧播放器。`forbidden` 只表示当前操作无权执行，不等同于注销。

损坏 JSON、未知版本、非法数值和浏览器禁用存储均应有可恢复行为。写入失败时本轮操作可保留在内存，但不能显示“已保存”的成功状态。不得持久化 Cookie、凭据、服务端路径或管理操作结果。

See [Hook Guidelines](./hook-guidelines.md),
[Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md), and the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
