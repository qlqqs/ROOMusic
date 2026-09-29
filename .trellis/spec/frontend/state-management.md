# State Management

## Principle

Keep each fact at its natural owner and derive everything else. Server resources
remain server state, shareable filters remain URL state, transient interaction
remains local component state, and authentication authority remains on the
backend.

当前 `frontend/src/` 已删除，不存在运行中的页面 effect 或全局 store。下文保留状态
归属合同；路由和 query/cache 方案已于 2026-09-28 选定 TanStack Router + Query（[前端基础 ADR](../../../docs/architecture/adr/0001-frontend-foundations.md)
决策记录），具体接入与清理策略由 M1a 子任务决定。不能把历史实现或候选库当成已经落地的当前状态。

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
- 扫描状态来自持久 scan-run 资源；当前没有已扫描文件数或百分比进度，诊断计数不能
  替代总进度。本地提交状态不能新增远端枚举或改写成功、失败、取消和不完整终态。
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

现有 Release API 的 `q`、`attention=required` 和 `page` 仍应映射为可恢复 URL state。
提交新搜索、清空搜索或切换 attention 时把 `page` 重置为 1；翻页限制在有效页内。
总数为零时保留第一页空态；非零总数对应的 URL 页码超过末页时，用替换历史项而非新增
历史项的方式规范化并请求末页。前进/后退必须重新派生筛选，不维护隐藏分页副本。

旧客户端以 `release` 查询参数选择详情；新路径形式与旧链接兼容策略尚待 M1 确定，
不要求继续 hash 路由。无论采用路径参数还是查询参数，都只在边界读取/trim/校验一次
opaque ID，拒绝空值及超过 255 个 JavaScript 字符单元的值，避免任意长值进入请求。
打开/关闭详情必须能恢复列表筛选与页码；精确 URL 语法获批后同步路由测试与本规范。

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
- 在 Core 0 或 M1 中引入未经批准的真实播放基础设施、Agent 会话或 PWA 离线存储。
- 用模拟计时器、演示数据或浏览器本地个人库掩盖生产 API/能力缺失。

## 播放与个人状态的阶段边界

旧 `music-client-experience` 的模拟播放和本地收藏/歌单持久化只是历史任务方案，
不再作为 Classical 客户端的例外合同。M1 没有真实播放时隐藏或明确禁用播放动作，
不生成播放历史；受控响应只用于测试，不作为生产 fallback。

M2 候选由路由外的唯一播放生命周期持有队列、当前队列条目和模式，服务端目录仍是媒体
事实权威。是否使用 `PlayerEngine` 与 `useSyncExternalStore` 由子任务/ADR 决定，
不是本规范预先安装库或创建全局 store 的授权。恢复队列后暂停，不自动发声。

服务端按用户隔离的历史、收藏和歌单是后续方案，不再把本地快照当作持久权威。若未来
批准缓存或偏好持久化，需带版本、边界校验和账号命名空间；损坏、未知版本、禁用存储
都应可恢复，写失败不能声称“已保存”。

任何受认证接口返回 `unauthorized` 都必须结束当前会话并清理该用户的查询、队列及媒体
生命周期；账号切换时取消旧请求并阻止迟到响应污染新会话。`forbidden` 只表示当前操作
无权执行，不等同于注销。Cookie、凭据、服务器路径和治理操作结果不得存入浏览器持久层。

See [Hook Guidelines](./hook-guidelines.md),
[Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md), and the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
