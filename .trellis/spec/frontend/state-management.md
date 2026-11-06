# State Management

## Principle

Keep each fact at its natural owner and derive everything else. Server resources
remain server state, shareable filters remain URL state, transient interaction
remains local component state, and authentication authority remains on the
backend.

2026-10-01 的 M1a 已落地下面的归属合同：服务器状态由 TanStack Query 持有，URL 状态由 TanStack Router 持有（路由不使用
loader，页面直接调用 feature 查询 hook，不让 Router 缓存与 Query 缓存并存），会话由 `features/auth/session` 的会话控制器
持有（普通 TypeScript 对象，组件经 `useSyncExternalStore` 读取）。没有全局 store。选型依据见
[前端基础 ADR](../../../docs/architecture/adr/0001-frontend-foundations.md) 决策记录；M1b（2026-10-03）又落地了专辑的搜索与详情 URL；
尚未实现的部分（受治理操作、播放）保留为目标合同。

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
- 扫描状态来自持久 scan-run 资源。M5 ⑥ 起新扫描带服务端持久化的进度（`progress`：四个工作阶段、整体基点 `percent_bp`、
  全局文件计数；`root_progress`：第几个 / 共几个目录）与报告状态（`report_status`）；本功能上线前的扫描与旧服务没有这些字段
  （`progress: null` 或缺失、`report_status: legacy`），前端不补算、不伪造进度。诊断计数仍不能替代总进度。本地提交状态不能新增远端枚举或改写成功、
  失败、取消和不完整终态。
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
  重新可见时立即刷新。诊断在运行中只手动刷新，到终态时自动刷新一次。进度随这两个轮询一起到达，不另加轮询器、SSE 或 WebSocket。
- 扫描与专辑数据（M5 ⑥，`library/hooks/use-scan-catalog-refresh.ts`，活动扫描与扫描详情两个 hook 都用）：观察到的运行中扫描结束（任一终态，
  非成功也可能已经提交了变化）、消失或换成另一个，以及同一个扫描在正式处理阶段从第 i 个目录换到更后的目录时，让 `catalogKeys.all` 失效
  （专辑列表、组与版本详情、概览的数字与“最近新增”）；计数阶段换目录不算，轮询间隔里跳过几个目录也只失效一次，第一次读到不失效。
- 扫描报告（M5 ⑥，仅管理员）：`libraryKeys.scanReportPage(generation, scanId, page, pageSize)`，前缀 `libraryKeys.scanReports(generation)`
  （经 library 公开入口的 `scanReportsQueryKey` 给 app）。只在扫描已结束且 `report_status` 为 `ready`/`partial` 时 `enabled`，不轮询；
  翻页是组件内状态并用 `keepPreviousData`。`503 scan_report_unavailable` 是确定结果，不按普通 503 重试，显示“报告没能生成”的一句话。
  认领、撤销、归组之后 app 的 `useReleaseDataRefresh` 让报告前缀失效：新增专辑现在所在的专辑与“可能接替了……”随之更新，历史计数不变。
- 扫描记录（F3，`GET /scans`，仅管理员）自己不轮询，由事件让它失效：启动扫描（`onSettled`）、活动扫描消失或换成另一个、
  扫描详情到达终态；失效按 `libraryKeys.scanHistory(generation)` 前缀覆盖所有页。翻页用 `keepPreviousData` 保留上一页，
  新页到达前表格不闪空。
- 响应不是 JSON、信封不合法或 decoder 失败时按 `invalid_response` 失败，结果不进入缓存。
- 专辑（M1b，`features/catalog/query-keys.ts` 的 `catalogKeys`）：列表一页一个条目（搜索词、筛选、排序与页码都在键里；M5 ⑥ 起排序规范化为
  `title | added_at_desc`，省略与按标题是同一条目，“最近新增”不复用标题排序的缓存），概览的“最近新增”是 `catalogKeys.recentlyAdded`（在 `all` 前缀下），翻页与改筛选时用 `keepPreviousData` 保留上一份结果，
  页面标明“正在更新”，保留的结果是占位数据，不能用来判断页码是否越界；组详情、版本详情各按 ID 一个条目；完整证据只有管理员且点了“查看完整证据”才 `enabled`，
  普通用户从不创建这个查询。选中的版本在组详情里还是成员、读详情却是 404（两次请求之间被重新扫描）时就地提示，并让组详情与该版本的查询失效，
  重新读到的成员列表决定还有哪些版本。

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
已实现的专辑列表（M1b，`/albums`）沿用同一约定：`q`（去掉首尾空白后非空且不超过 200 个 UTF-8 字节，与后端上限一致，超限的值丢弃；路由会把形如数字的值
解析成数字，`?q=1984` 按原文本处理）、`attention`（只有 `required` 保留）、`sort`（M5 ⑥：只有 `added_at_desc` 写进地址，按标题是默认值不写；
其他值丢弃）、`page`（≥2 才写）。换排序与提交搜索、切换筛选一样回到第 1 页并保留其他条件，翻页、清除搜索与“清除筛选”保留排序。搜索显式提交（回车或“搜索”按钮），不做输入即搜，每次提交一条历史；
提交搜索、清除、切换筛选时把页码置为 `undefined`，翻页只改页码；搜索框里输入过程中的草稿是局部状态，地址变化（前进、后退、清除）时由地址重新派生。
越界页码的规范化抽成 `shared/lib/use-clamped-page.ts`（扫描记录同样使用）。点页码链接时把结果区标题滚到视野顶部并聚焦（页码链接在结果下方，不挪的话新的一页停在滚到底的位置，
键盘用户要倒着穿过整页卡片），改搜索词或筛选不挪焦点。

M1a 的 URL 形状已定：`/`、`/setup`、`/login?redirect=`、`/library`（可带 `scan_page`）、`/library/scans/$scanId`、`/members`；旧 hash 链接与
`?release=` 不做兼容（开发阶段不做兼容）。M1b 增加 `/albums`（`q`、`attention`、`page`；M5 ⑥ 增加 `sort`）与 `/albums/$groupId`（`release`）。
无论采用路径参数还是查询参数，都只在边界读取/trim/校验一次 opaque ID，拒绝空值及超过 255 个 JavaScript 字符单元的值，避免任意长值进入请求。
打开/关闭详情必须能恢复列表筛选与页码。

已实现的专辑详情（M1b）：
- `/albums/$groupId?release=<UUID>`：`release` 只在选中非代表版本时出现。`validateSearch` 只保留 UUID 形状的值并统一成小写；组详情返回后，`release` 不是本组成员或就是代表版本时
  用**替换**历史项的方式去掉（返回键不会回到被规范掉的地址）。`groupId` 不是 UUID 时不发请求，直接显示“专辑不存在”（与扫描详情一致）。
- 返回列表：卡片链接把列表参数放进路由的 history state（`state={{ albumList: search }}`，类型由 `router.tsx` 里对 `HistoryState` 的声明合并给出）；
  详情页读 `location.state`，经 `albumListSearchFromState`（内部再走列表自己的 `parseAlbumSearch`，因为 state 会跨刷新保留、也可能被改写）生成“返回专辑列表”的目标，
  切换版本与地址规范化都把它带在新的历史项里；没有时（直接打开详情）回到 `/albums`。浏览器后退不依赖它。
- 版本导航的链接用路由给当前版本的链接自动加的 `aria-current="page"`（`activeOptions={{ exact: true }}`：`release` 为 `undefined` 与缺省等价，其余必须相等）。
  只改查询参数的导航不重置滚动、不把焦点抢回主内容区；只有路径变化（进入详情、返回列表）才回到页面顶部并聚焦主内容区。

路由实现约定（M1a）：TanStack Router 把 `validateSearch` 的返回值合并到原始查询串上，所以不合法的值必须**显式返回
`undefined`**，只省略键时原值仍留在 `search` 里并被 `beforeLoad` 读到（`/login` 的 `redirect` 曾因此让 `beforeLoad`
读到未清理的 `//evil.example` 并抛出“Redirect blocked”异常，已有回归测试）。路径参数在边界校验：扫描 ID 不是 UUID 时不发请求，直接显示“扫描不存在”。`redirect` 只接受以单个 `/` 开头、
不超过 2048 字符的站内路径，拒绝 `//`、`/\`、带协议的值以及 `/login`、`/setup`。

Do not put session tokens, absolute paths, secrets, raw errors, or uncommitted
operation payloads in the URL.

### 设置页与旧地址

- 设置页（`/settings/*`）：`settings-layout.tsx` 只放分栏与 `<Outlet />`，不另加 h1（各栏内容组件自带 `Page`）；视口 ≥1024px 左侧分栏，更窄时内容上方一行横向链接。
  管理员栏照旧由路由层 `AdminGate` 把关，分栏只决定显示哪些项。旧地址（`/library`、`/members`、`/operations`、`/system/backups`）在根路由下、登录守卫之前重定向，
  `path` 是 `string`，不进类型化路由树，所以旧 `<Link to>` 会被 tsc 拦下；查询参数值等价保留（`%20` 可能变 `+`，重复键不保证）。

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
- 成员写操作后端没有幂等保证：界面没有“重试”，结果不确定时刷新列表并说明“列表已刷新，请看一下成员现在的状态再操作。”。
  对自己停用或撤销会话成功后，后端已撤销当前会话，随后的列表刷新得到 401，由会话边界回到登录页。
- 写操作 hook 在提交时记录会话代次，成功或失败回调先检查代次再更新界面或导航：旧身份的迟到响应不渲染、不导航。
- 受治理写操作（M3，管理员）沿用同一模型：修改带读取时的 `revision` 作 `expected_revision`，撤销不带版本号（后端判断）；409 `revision_conflict` 说明“刚被修改过”
  并刷新，保留草稿；撤销资格 `revertible` 与记录只来自服务器当前状态，修改后让 governance 的全部查询失效。命令状态由页面持有，细节见[Hook 规范](./hook-guidelines.md)。
  M4 的默认版本带专辑的 `revision`；组详情刷新期间暂缓地址规范化，见同一规范。
  认领（M4 第三项）带两个版本的 `revision` 与预览令牌：令牌与服务器当前的情况不一致就 409 `claim_changed`，页面重新读取详情与预览，不保留草稿以外的旧预览。

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

会话控制器、代次、清理顺序、401 与 403、跨标签页核对、退出、回跳与初始化见[会话生命周期](./session-lifecycle.md)。

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

M2a 播放生命周期与恢复、阶段 P 播放上报与最近播放、M5 ① 历史页签与 M5 ④ 全局搜索的查询键、URL 与目录状态见[播放与个人状态](./playback-and-personal-state.md)。

See [Hook Guidelines](./hook-guidelines.md),
[Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md), and the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
