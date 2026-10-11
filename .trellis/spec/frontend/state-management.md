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
- 专辑（M1b，`features/catalog/query-keys.ts` 的 `catalogKeys`）：列表一页一个条目（搜索词、筛选与页码都在键里），翻页与改筛选时用 `keepPreviousData` 保留上一份结果，
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
解析成数字，`?q=1984` 按原文本处理）、`attention`（只有 `required` 保留）、`page`（≥2 才写）。搜索显式提交（回车或“搜索”按钮），不做输入即搜，每次提交一条历史；
提交搜索、清除、切换筛选时把页码置为 `undefined`，翻页只改页码；搜索框里输入过程中的草稿是局部状态，地址变化（前进、后退、清除）时由地址重新派生。
越界页码的规范化抽成 `shared/lib/use-clamped-page.ts`（扫描记录同样使用）。点页码链接时把结果区标题滚到视野顶部并聚焦（页码链接在结果下方，不挪的话新的一页停在滚到底的位置，
键盘用户要倒着穿过整页卡片），改搜索词或筛选不挪焦点。

M1a 的 URL 形状已定：`/`、`/setup`、`/login?redirect=`、`/library`（可带 `scan_page`）、`/library/scans/$scanId`、`/members`；旧 hash 链接与
`?release=` 不做兼容（开发阶段不做兼容）。M1b 增加 `/albums`（`q`、`attention`、`page`）与 `/albums/$groupId`（`release`）。
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

### M2a 播放生命周期与恢复合同

`features/player` 的公开入口导出 `PlayerProvider({ userId, children })`、`usePlayer()`、
`usePlayerProgress()`、`usePlayerProgressSelector()`、`usePlayerEngine()`、`usePlayerSelector()`、`useCurrentTrackMarker()`、`usePlaybackCapabilities()`。
provider 放在已登录会话代次子树内，只装配一个分离的 `Audio()` 与不依赖 React 的 `PlayerEngine`；切页不重建。
离散快照通过 `useSyncExternalStore` 订阅，进度独立订阅，避免每次 `timeupdate` 重绘整队列。
大量重复的组件（专辑页每行动作）只用 `usePlayerEngine()` 发命令；只关心某个值的组件用 `usePlayerSelector()` 选出基本值
（外壳只看队列是否为空，专辑页只看当前曲目与状态），音量、队列变化不重绘整张曲目表；曲目行 `memo`，插槽函数保持引用稳定。
`useReleaseLoader` 经 catalog 公开入口复用会话范围的 Query 缓存，格式表 Query 在会话内保持有效。
歌词是服务器状态：`useTrackLyrics(trackId)`（键 `viewerQueryKey(generation, "player", "lyrics", trackId)`，`staleTime: Infinity`，会话结束随代次清空）；
面板的跟随与暂停跟随是组件内局部状态，不进入引擎快照，也不持久化。

普通卸载执行 `detach()`：中断媒体与异步工作，但保留刷新恢复记录；
会话控制器 `onEnded` 同步执行 `endSession()`：停止音频、清空 `src` 并 `load()` 中断网络，
清空媒体键、队列与本账号保存记录，不等待 React 卸载。`401`、跨标签核对发现退出、换查看者
均走这一边界。旧请求代次与引擎异步令牌阻止迟到响应覆盖新曲目/新账号。
不能把“普通卸载”和“结束会话”混为一谈，否则刷新恢复记录会被删除。

队列条目为 `{ entryId, track }`，同曲重复项按 `entryId` 操作。快照包含
`status/queue/current/hasNext/failures/fault/event/volume/muted/restoring`；`status` 含 M2b 的 `preparing`（预检收到 202，服务端正在无损转换），
`fault` 含 `busy`（503 `derivation_busy`，与 `transient` 共用重试）；预检结果是 `ready | preparing`，状态码与响应体不符按无效响应处理。
预取下一首派生曲目的预检由引擎持有独立的取消控制器，不进入快照；进度快照包含
`positionSeconds/durationSeconds/seekable`。只有实际媒体事件才能更新正在播放、进度与时长，
未知时长为 `null`。失败条目与可重试暂停分别表达，不靠模拟时钟推断结束。

恢复键 `sessionStorage["roomusic:player:v1:<userId>"]`，只存 version、entryId/trackId/releaseId/groupId、
currentEntryId、mode、positionSeconds 与有限的随机“上一首”历史（条目 ID），不存标题、路径或凭据。
边界解码版本、UUID、条目唯一性、数量与当前项归属；损坏/未知版本删除，不可用存储安全降级。
按发行 ID 去重、最多 4 个并发重新加载详情；只有明确 404 或详情中缺少的曲目才丢弃并提示数量，
网络/503 等暂时失败保留原恢复记录并提供重试，不能把临时故障当作不存在删除。恢复后保持暂停，首播重新预检后定位。
普通队列动作、暂停、切歌与 pagehide 保存，播放中每 5 秒保存；写失败不声称已保存。
开始位置在元数据与可定位范围到达后才生效，此前保存或出错重试用待定位的位置，不写成 0；在队首按“上一首”从头重播时作废该位置；队列未变时复用已序列化的条目。
音量/静音是设备偏好，存 `localStorage["roomusic:player-volume:v1"]`，不随账号清除。

收藏与歌单仍未实现（M5）；播放历史由服务端按用户保存（阶段 P），队列恢复不构成浏览器个人库权威。以后新增的缓存或偏好持久化同样要带版本与边界校验，
含账号数据的按账号命名（不含私有数据的设备偏好如音量除外）；损坏、未知版本、禁用存储都应可恢复，写失败不能声称“已保存”。
Cookie、凭据、服务器路径和治理操作结果不得写进浏览器持久层。
具体交互与错误矩阵见[播放器规范](./player-design-guidelines.md)；API 见[播放合同](../backend/playback-contracts.md)。

### 阶段 P 播放上报与最近播放

引擎经依赖 `playRecorder: { record, discard }` 交出达到计次规则的播放，不碰网络；`PlayerProvider` 与引擎一起创建 `createViewerPlayReporter(queryClient, generation)`，
把 `report` 接到 `record`、`dispose` 接到 `discard`（`endSession` 调用），所以上报器的生命周期等于引擎的：普通卸载与 StrictMode 重挂载沿用它，会话结束作废它。
上报器是模块内的局部状态（待发送与等待重试的上报，最多 50 个），不进入 Query 缓存，也不持久化；成功后让 `listeningKeys.recentReleases(generation)` 失效。
“最近播放”是服务器状态（键 `viewerQueryKey(generation, "listening", "recent-releases")`）；清除是写操作：成功后 `setQueryData` 置空并失效，不等重新读取，
失败时保留旧数据、对话框里显示错误，DELETE 幂等所以可以直接重试。

See [Hook Guidelines](./hook-guidelines.md),
[Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md), and the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
