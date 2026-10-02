# State Management

## Principle

Keep each fact at its natural owner and derive everything else. Server resources
remain server state, shareable filters remain URL state, transient interaction
remains local component state, and authentication authority remains on the
backend.

2026-10-01 的 M1a 已落地下面的归属合同：服务器状态由 TanStack Query 持有，URL 状态由 TanStack Router 持有（路由不使用
loader，页面直接调用 feature 查询 hook，不让 Router 缓存与 Query 缓存并存），会话由 `features/auth/session` 的会话控制器
持有（普通 TypeScript 对象，组件经 `useSyncExternalStore` 读取）。没有全局 store。选型依据见
[前端基础 ADR](../../../docs/architecture/adr/0001-frontend-foundations.md) 决策记录；尚未实现的部分（搜索 URL、受治理操作、
播放）保留为目标合同。

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

已实现的约定（M1a）：

- 查询键以 `["viewer", 代次, feature, …资源与输入]` 开头，由各 feature 的 `query-keys.ts` 经 `viewerQueryKey` 构造，组件与
  hook 不手写查询键。代次变化后旧身份的缓存不会被新身份读到。
- 默认选项集中在 `app/runtime.ts`：`staleTime` 30 秒，窗口重新聚焦时刷新已过期的数据；读取只对网络失败与 503 自动重试，
  最多 2 次（`shouldRetryQuery`），4xx 与无效响应不重试；写操作从不自动重试。
- 轮询只发生在有意义的时候：扫描详情运行中（含取消请求中）每 2 秒一次，到终态即停止并让活动扫描与扫描记录查询失效；活动扫描只在
  确有扫描时每 5 秒刷新，没有活动扫描时不轮询。页面不可见时暂停（沿用 Query 默认的 `refetchIntervalInBackground: false`），
  重新可见时立即刷新。诊断在运行中只手动刷新，到终态时自动刷新一次。
- 扫描记录（F3，`GET /scans`，仅管理员）自己不轮询，由事件让它失效：启动扫描（`onSettled`）、活动扫描消失或换成另一个、
  扫描详情到达终态；失效按 `libraryKeys.scanHistory(generation)` 前缀覆盖所有页。翻页用 `keepPreviousData` 保留上一页，
  新页到达前表格不闪空。
- 响应不是 JSON、信封不合法或 decoder 失败时按 `invalid_response` 失败，结果不进入缓存。

## URL State

Search text, stable filters, sorting, and pagination belong in the URL when users
should be able to reload, bookmark, or use browser navigation. Normalize and
validate URL values at the search feature boundary; invalid values fall back to
documented defaults without sending arbitrary backend fields.

现有 Release API 的 `q`、`attention=required` 和 `page` 仍应映射为可恢复 URL state。
提交新搜索、清空搜索或切换 attention 时把 `page` 重置为 1；翻页限制在有效页内。
总数为零时保留第一页空态；非零总数对应的 URL 页码超过末页时，用替换历史项而非新增
历史项的方式规范化并请求末页。前进/后退必须重新派生筛选，不维护隐藏分页副本。

已实现的分页（F3，`/library` 的扫描记录）：查询参数 `scan_page`，只在 2 及以上时写进 URL（第 1 页是默认值，不带参数）。
`validateSearch` 经 `shared/lib/pagination.ts` 的 `parsePageParam` 解析，缺失、0、负数、小数、非数字与过大的值都显式返回 `undefined`
并回到第 1 页。页码超出末页（总数缩小、手改 URL）时用**替换**历史项的方式改到末页，没有数据时回到第 1 页；翻页时保留的上一页
（占位数据）不能用来判断当前页是否越界。`shared/ui` 的 `Pagination` 只负责结构与可访问语义，链接由调用方用路由 `Link` 生成。
M1b 的列表页沿用同一约定（参数名按页面区分，写入、解析、越界处理都一样）。

M1a 的 URL 形状已定：`/`、`/setup`、`/login?redirect=`、`/library`（可带 `scan_page`）、`/library/scans/$scanId`、`/members`；旧 hash 链接与
`?release=` 不做兼容（开发阶段不做兼容）。专辑详情的 URL 形状在 M1b 确定。无论采用路径参数还是查询参数，都只在边界读取/trim/
校验一次 opaque ID，拒绝空值及超过 255 个 JavaScript 字符单元的值，避免任意长值进入请求。
打开/关闭详情必须能恢复列表筛选与页码；精确 URL 语法在 M1b 获批后同步路由测试与本规范。

路由实现约定（M1a）：TanStack Router 把 `validateSearch` 的返回值合并到原始查询串上，所以不合法的值必须**显式返回
`undefined`**，只省略键时原值仍留在 `search` 里并被 `beforeLoad` 读到（`/login` 的 `redirect` 曾因此让 `beforeLoad`
读到未清理的 `//evil.example` 并抛出“Redirect blocked”异常，已有回归测试）。路径参数在边界校验：扫描 ID 不是 UUID 时不发请求，直接显示“扫描不存在”。`redirect` 只接受以单个 `/` 开头、
不超过 2048 字符的站内路径，拒绝 `//`、`/\`、带协议的值以及 `/login`、`/setup`。

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

已实现的写操作约定（M1a）：

- 写操作按“意图”建模，命令状态为 `idle | pending | succeeded | failed`，一个 hook 同一时刻只有一个命令；结束时失效相关查询并
  等待刷新完成，界面不依据本地猜测更新列表。
- 目录写操作（新增、停用、恢复）每个新意图生成新的幂等键。结果不确定（网络失败、无效响应、5xx，见 `isOutcomeUncertain`）时，
  “重试”沿用同一个键，后端按键重放已记录的结果；4xx 是明确的拒绝，不重试。409 `revision_conflict` 刷新列表并说明目录已变化，
  409 `idempotency_conflict` 提示重新操作。停用与恢复携带读取时的 `expected_revision`。
- 成员写操作后端没有幂等保证：界面没有“重试”，结果不确定时刷新列表并说明“请核对成员的当前状态，再决定是否重新操作”。
  对自己停用或撤销会话成功后，后端已撤销当前会话，随后的列表刷新得到 401，由会话边界回到登录页。
- 写操作 hook 在提交时记录会话代次，成功或失败回调先检查代次再更新界面或导航：旧身份的迟到响应不渲染、不导航。

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

会话控制器（`features/auth/session/session-controller.ts`）是普通 TypeScript 对象，状态为
`checking | unavailable | setup_required | anonymous(notice) | authenticated(viewer, generation)`，所有转换串行执行，登录与
退出不会交错。浏览器端没有 token：凭据只在 HttpOnly Cookie 与后端会话里，不进入 localStorage、sessionStorage、IndexedDB、
URL、查询键或全局 store。

- **启动**：先查 `GET /setup/status`，需要初始化则进入 `/setup`，否则用 `GET /auth/me` 确认会话；启动失败（如 503）显示可重试的
  整页错误。不从本地角色快照恢复私有页面。
- **代次**：进入 `authenticated` 与离开它都使代次递增。查看者是 `{ id, username, role }`：`/auth/me`、登录与初始化管理员的响应都带用户 `id`
  （F3，2026-10-02），“同一查看者”按 `id` 与 `role` 判断（`sameViewer`），用户名只用于显示；成员表标出“你”、停用与撤销时判断目标是否为自己，
  同样按 `id`。
- **清理顺序**：离开已登录状态或查看者变化时，`cancelQueries()` → `clear()`（查询与写操作缓存）→ `router.invalidate()`
  重新执行守卫 → 导航。三重防护避免旧身份的数据回流：查询键带代次；已登录布局以代次为 `key` 重新挂载（丢弃表单、对话框等私有
  状态）；写操作回调检查代次。
- **401 与 403**：`session: "required"`（默认）的请求发出时读取代次，401 `unauthorized` 到达时带回，控制器忽略属于旧会话的 401，
  旧会话的请求不能结束新登录的会话。初始化状态、初始化与登录接口标为 `"none"`，登录的 401 `invalid_credentials` 不是会话失效。
  403 只拒绝当前操作或页面，不结束会话。
- **跨标签页**：`BroadcastChannel("roomusic:session")` 在登录、退出、会话失效后只广播 `{ type: "changed" }`（不含凭据与用户
  数据）；其他标签页收到广播，以及页面重新可见且距上次核对不少于 30 秒时，用 `/auth/me` 核对：同一查看者（`id` 与 `role` 都相同）不处理，用户不同、角色变了或 401
  时按上文清理。浏览器不支持 BroadcastChannel 时只依赖重新可见时的核对。广播不构成授权，后端继续鉴权每个请求。
- **退出**：`POST /auth/logout` 由后端确认后才清理；失败时留在当前页并提示重试，不假装已退出。
- **回跳**：登录成功后进入 `redirect` 指定的站内路径，否则进入概览。在页面里主动退出时登录页**不带**回跳（下一个登录的人
  不应被送到上一个人停留的页面）；重新载入后（没有“刚退出”的记忆）直接访问受保护页，或会话被撤销后刷新，都会带回跳。
- **初始化**：成功后用同一凭据自动登录；自动登录失败则回到登录页并预填用户名，提示“管理员已创建，请登录”。

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
