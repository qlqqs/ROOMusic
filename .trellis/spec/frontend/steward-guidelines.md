# 管家的前端约定

本文记 M6 C4（对话页）、C5（页面上下文、播放与个人库工具）、C6（修改方案、模式、治理工具）、C7（审查结果、Steward 模式、审查模型）、C8（联网搜索、读取网页、MusicBrainz、Responses 接口）与 C10（后台运行与接回、未读、历史摘要的分界、记忆）落地后的前端结构与约定，以代码为准：`frontend/src/features/steward/`、`frontend/src/app/shell/`、
`frontend/src/app/routes/steward-page.tsx`、`frontend/src/app/routes/agent-settings-page.tsx`。后端接口、SSE 事件与错误码见
[管家运行时合同](../backend/steward-runtime-contracts.md) 第 4、5、10 节与“C6 共享写入与确认合同”，审查的提示词、证据摘要与策略见[管家审查合同](../backend/steward-review-contracts.md)；界面与原型的对应见 [DESIGN.md](../../../frontend/DESIGN.md) 第 4 节“管家”。

## 0. 范围

- 已有：入口与名字、右侧停靠面板、整页对话（会话列表、改名、删除）、流式回答与过程条目、结束说明（重试、继续）、模型菜单、
  未配置引导、设置页的 Agent 栏目（名字、模型与检测、访问令牌、管理员的成员开关与内网地址放行）；C5 起有输入框上方的“正在看/正在播放”标签
  与随消息发送的页面上下文，播放、播放历史、收藏与歌单工具的过程条目，以及这些工具结束后的个人数据刷新；
  C6 起有修改方案卡片（逐项确认、结果与撤销、刷新后恢复、作废与过期）、输入框下的模式按钮与菜单（Assistant、Operator）、
  曲库与艺人治理工具的过程条目与查询失效，以及操作记录页的来源标签（见第 11 节）；
  C7 起有审查结果（审查中、按项的状态与理由、撤销、转人工的确认、审查不可用后的重新审查与自己确认）、模式菜单里的 Steward、设置里的审查模型、
  模型菜单里标出审查模型、会话列表的“N 项等你确认”与“审查中”，以及操作记录的“管家（审查通过）”（见第 12 节）；
  C8 起有设置里的“联网搜索”一组（来源、SearXNG 地址、Tavily 密钥、测试搜索）、检测结果里的“自带搜索”、接口类型 Responses，
  联网四个工具与模型自带搜索的过程条目，以及回答下面的来源列表（见第 13 节）；
  C10 起运行不随连接停止：断线自动接回、打开或刷新正在运行的会话时接回、任何标签页都能停止，会话列表的运行中、等待确认与有新回答三种标记，
  入口的提示点，“更早的内容已压缩”一行，设置里的“记忆”一组，以及记忆三个工具的过程条目（见第 14 节）。
- 没有（后端也没有）：审查模型用另一个端点、跨服务重启接回运行、摘要正文的显示、
  展开工具条目看原始输入输出、在对话里显示历史消息带过的上下文、浏览器通知。不做禁用占位。

## 1. 目录与两个公开入口

```text
features/steward/
├── index.ts          # 轻量入口：外壳、路由与运行时直接导入
├── chat.ts           # 对话界面入口：StewardPanel、StewardPage，按需加载
├── api/              # decoders.ts（设置、探测、会话、消息、运行事件、方案、管理员、令牌）、endpoints.ts
├── run/              # reduce.ts（事件与落库消息 → 回合）、run-store.ts（进行中的运行，C10 起含接回）、plan-store.ts（方案的界面状态，C6）、review-store.ts（审查的界面状态，C7）、refresh.ts、viewing.ts（正停在哪些会话里，C10）
├── dock-store.ts     # 面板状态、草稿、草稿里点掉的上下文标签、对话区提示、空白新对话预选的模型与模式
├── context/          # context.ts（上下文的形状、点掉与截断规则）、host.tsx（app 传进来的页面、播放与刷新）、context-chips.tsx（标签行）
├── hooks/            # use-steward-settings、use-conversations、use-run、use-plans（C6；C7 加重新审查与审查的快照）、use-agent-settings、use-access-tokens、use-memories（C10）
├── tool-labels.ts    # 工具的中文名、图标、主要参数、结果说明、失败短语与会改动的数据
├── plan-text.ts      # 方案卡片的字段名、旧值新值与依据来源的文字（C6）
├── components/       # 面板、整页、对话区、回合、过程、回答（Markdown）、来源列表（C8）、结束说明、输入框、模式与模型菜单、方案卡片、审查结果（C7）、会话列表、令牌（C3）
└── settings/         # Agent 栏目：名字、模型与检测结果、联网搜索（C8）、记忆（C10）、成员段、文案
```

- `index.ts` 只导出外壳与路由需要的东西：`useStewardEntry`、`DEFAULT_STEWARD_NAME`、`useDockState`、`setDockOpen`、`toggleDock`、
  `preferBlankConversation`、`resetStewardState`（`resetRuns`、`resetPlans`、`resetReviews`、`resetDock`）、`AgentSettings`，以及 C5 的 `StewardHostProvider` 与上下文类型（见第 5 节）。侧栏、标签栏、外壳与 `app/runtime.ts` 静态导入它；
  `package.json` 的 `sideEffects` 让没用到的导出（`AgentSettings`）不进首屏。
- `chat.ts` 导出 `StewardPanel` 与 `StewardPage`。外壳用 `React.lazy(() => import("../../features/steward/chat"))` 加载面板，
  路由用 `lazyRouteComponent(() => import("./routes/steward-page"))` 加载整页。分成两个入口，是因为同一个模块既被静态导入又被动态导入时
  不会单独成块：对话界面与 Markdown 依赖要等第一次打开面板或整页才下载。
- `features/steward` 只导入 `auth` 的公开入口与 `shared`，不导入其他 feature：页面、播放与别的 feature 的查询键由 app 经 `StewardHostProvider` 传入（第 5 节）。

## 2. 运行状态（`run/run-store.ts`）

- 进行中的运行放在模块级 store，不放在组件里：发消息与继续由 store 发起请求并读流，切页面、面板与整页互换都不打断（R6）。
  组件用 `useSyncExternalStore(subscribeRuns, getRunSnapshot)` 订阅（`hooks/use-run.ts` 的 `useRunSnapshot`、`useRuns`）。
  C10 起 store 由 `createRunStore()` 建出，模块导出其中一个实例的方法（`startRun`、`attachRun`、`stopRun`、`resetRuns`）；测试另建实例模拟两个标签页。
  接回、断线重连、`lastSeq`、`detached` 与停止的新语义见第 14 节；本节其余描述仍适用。
- 快照 `{runs, ends}` 按会话 ID 存，每次变化换新的 `Map`：
  - `runs`：`{userText, baseSequence, view, phase}`。`userText` 为 `null` 是继续（不追加用户消息）；`baseSequence` 是开始时
    已落库的最后一条消息序号；`phase` 是 `running`、`stopping`、`settling`。
  - `ends`：最近一次运行怎么结束的（`done` 事件、`disconnected`、`request_failed`），下一次运行开始时清掉；只在内存里。
- 发起：`startRun({conversationId, text, baseSequence, context, refresh, onToolFinished})`，同一会话已有运行时不再发起。返回的 Promise 在流开起来
  （`{started: true}`）或开流前失败（带 `ApiFailure`）时完成，调用方据此把草稿放回输入框；之后的读流在后台继续。
  `context` 是随消息发的页面上下文（`null` 时请求体里没有这个字段）；继续（`text` 为 `null`）没有请求体、不带上下文——后端的继续接口不接受 `context`
  （带了 `400`），原用户消息的上下文保持发送时的样子，所以调用方继续时传 `null`。每收到一个 `tool_finished`，按 `call_id` 找到工具名调用 `onToolFinished`。
  `useStartRun(conversationId, text, baseSequence, context, decision?)` 包了一层：传入重读会话与列表的 `refresh`、按 `toolDataScopeOf` 刷新数据的 `onToolFinished`、
  把运行里的 `plan` 事件记进方案 store 的 `onPlan`，开流前得到 `403 steward_disabled` 时让设置查询失效。
- 确认方案（C6）：`startRun` 的 `decision: {planId, body, seed}` 走确认接口（`openDecision`，响应是 SSE），`text` 为 `null`，并进最后一个回合；
  运行从 `seed`（方案项对应的等待确认条目）开始，确认后执行方案项的 `tool_started`、`tool_finished` 按 `call_id` 落在这些条目上。
  开流前被拒绝时不记 `ends`（`request_failed`），由方案卡片就地说明；仍会重读会话（摘要不一致时卡片随之换成新的内容）。
- 结束后以落库为准：收到 `done`（或重新接回也没接上）后 `phase` 变成 `settling`、记下结束信息，调用 `refresh`（`run/refresh.ts` 的 `refreshAfterRun`：
  用户还停在这个会话里时先标记已读，再让该会话、列表与设置的查询失效并等它们重读），完成后从 `runs` 移除。运行期间重读会话时，对话区只取 `sequence <= baseSequence`
  的消息，之后的部分由运行自己显示，不会重复。
- 停止：`stopRun` 只调用取消接口，服务端结束运行后每条接着的连接都收到同一个 `done`（会话已释放，重读到的是空闲）；取消接口失败或回答
  `{cancelled: false}`（done 正在路上）时回到 `running`，不断开连接（C10：断开不会停止服务端的运行）。本页没有这次运行时只调用取消接口。
- 登出与换用户：`app/runtime.ts` 在 `session.onEnded` 与 `dispose` 里调用 `resetStewardState()`，即 `resetRuns()`（中止所有请求、
  清空快照）加 `resetDock()`（清掉会话 ID、草稿、提示、预选模型）。`run-store` 与 `dock-store` 各有一个模块级 `epoch`：`resetRuns`、
  `resetDock` 递增它，异步操作开始时记下，返回后不一致就不再写入。发送（`components/conversation-view.tsx` 的 `send`）在建会话、
  写入预选模型、开流前被拒绝之后都先核对 `dockEpoch()`：请求得到 401 时会话边界先清空这里，旧用户的草稿与提示不能留给下一个登录的人。

## 3. 归并与显示（`run/reduce.ts`）

- 流式事件与已落库消息归并成同一种结构：`Turn = {key, userText, items, texts}`，`items` 是所有步骤的工具调用，`texts` 是各步文字。
  `applyRunEvent` 处理事件，`turnsFromMessages` 还原落库消息（助手消息里 `tool_call` 块与 `tool_calls` 按下标对上），两者显示一致。
- `StepItem.status` 多一种 `awaiting_confirmation`（C6）：后端先为方案里的调用发 `tool_started` 再发 `plan`，`plan` 事件把这些条目原地改成等待确认（`executed` 为假，没有的补上），落库的调用记录也可能是这个状态；
  过程里写“等你确认”，回合里什么也没执行、只有等确认的修改时摘要写“N 项修改等你确认”。`StepItem.textsBefore` 是回合里排在这次调用之前的文字段数
  （流式取事件的 `step`，没有 `step` 时取当时的段数；落库取所在助手消息之后的段数），方案卡片据此排在提出它的那段文字之后、确认后的回答之前。
- 继续或确认并进最后一个回合时（`threadOf`），运行里与落库同一 `callId` 的条目用运行里的状态原地替换（保留落库的 `textsBefore`），新的条目排在后面。
- `StepItem.executed`：流式收到的都算执行过；落库的以 `duration_ms` 非空为准（因 `token_limit` 或取消没执行的、中断的都没有耗时）。
  摘要“查了 N 处资料，用时 X”只数执行过的；还没有耗时数据（运行刚结束、重读未回来）时只写“查了 N 处资料”。回合里执行过改动类工具
  （`toolWritesOf`：标了 `changes` 的收藏、歌单、历史写入，或标了 `writes` 的播放控制）时改写成“做了 N 步，用时 X”（`components/steps.tsx`）。
- `RunView.waiting`：刚开始或这一步的工具都执行完时为真，界面显示“正在想…”；收到文字、有工具在执行、`done` 后为假。
- `hooks/use-run.ts` 的 `threadOf` 组合落库内容与运行：发消息是新回合，继续并进最后一个回合；`canContinue` 只在本页没有运行、
  会话空闲、最后一条落库消息是 `user` 或 `tool`（`isIncomplete`），或有已确认但没执行完的方案（`unfinishedPlans`，C6）时为真，
  结束说明的“重试”“继续”只在这时出现（有没执行完的方案时由方案卡片给“继续”），避免 `409 nothing_to_continue`。
- 工具条目（`tool-labels.ts`，后端合同第 3 节与 C6 的全部工具）：每个工具有中文名与 lucide 图标；未登记的工具名原样显示（扳手图标）。
  - 主要参数 `toolArgumentOf(name, input, output)`：先用工具自己的规则——`control_playback`、`edit_queue` 的 `action`，`play_tracks` 的 `placement`，
    `set_play_mode` 的 `mode`，`set_device_volume` 的音量（`volume` 0–1 写成百分比，`muted`），`list_favorites`、`set_favorite` 的 `kind`，
    `create_playlist`、`rename_playlist` 的 `name`；C6 的 `correct_release` 写改了哪些字段（“标题、年份”），`change_release_group` 的 `action`，
    `set_default_release` 的 `release_id` 为 null 时“改回自动选择”，`review_release_issue` 的 `action`，`rename_artist` 的新名字或“改回最常见的写法”，
    `ignore_artist_candidate` 的 `ignored`；取不到时用通用规则：输入的 `q`，或结果里 `title`、`name`、`representative.title`、
    `artist.name`、`playlist.name`、`track.title`。
  - 右侧 `toolResultOf`：改动类工具的结果说明（`set_favorite` 按 `favorited` 写“已收藏/已取消收藏”，`play_tracks`、`add_playlist_tracks` 按 `track_ids`
    写“N 首”，歌单与历史的其他写入写“已新建”“已改名”“已删除”“已移除”“已移动”，治理工具写“已修改”“已调整”“已设定”“已核对”“已认领”“已合并”“已拆开”“已改名”“已忽略”“已撤销”），其余是数量（`pagination.total` 或 `items.length`，按工具的单位）。
  - 失败按 `error_code`：“没找到”“没有权限”“已停止”“没有完成”“没有执行”（`token_limit`）、“分区不在线”（`zone_offline`）、
    “没有可用的输出”（`renderer_unavailable`）、“内容已被改过”（`revision_conflict`）、“你没有选这一项”（`declined_by_user`）、“没有执行”（`not_executed`，与 `token_limit` 同一短语）、“队列已满”（`queue_full`）、“这个设备的音量不能调”（`volume_fixed`）、
    “歌单数量已到上限”（`playlist_limit_reached`）、“歌单曲目已到上限”（`playlist_track_limit_reached`）、“没有可播放的曲目”（`no_playable_tracks`），其余“失败”。
  - 曲库与个人数据里的文字只按文本节点渲染。

## 4. 接口与流

- SSE 的按行解析在 `shared/lib/sse.ts` 的 `SseParser`，播放区事件流（`features/player/zones/event-stream.ts`）与管家共用。
- `shared/api` 的 `apiStream` 接受 `method: "POST"` 与 `body`：`endpoints.ts` 的 `openRun` 用它发消息与继续，`openDecision` 用它确认方案（C6）；开流前的拒绝按错误信封抛出
  `ApiError`，401 照常通知会话边界。运行事件由 `decodeRunEvent(event, data)` 解码，不认识的事件名丢掉，解不了的事件跳过。
  C6 的形状假设集中在 `decoders.ts`：`plan` 事件是 `{type, step?, plan}`；方案是 `{plan_id, digest, expires_at, status?, items}`，方案项
  `{id, call_id, tool, target, changes:[{field, before, after}], revertible, basis?, decision?, operation_id?}`；会话详情多 `pending_plan`（可空）与 `plans`（全部方案）；
  确认后执行方案项的 `tool_started`、`tool_finished` 没有 `step`；已执行项的操作记录取工具输出的 `operation_id`（`operationIdOf`）。
- 会话 `status: "running"` 但本页没有运行时不再轮询（C10）：对话区接回这次运行（第 14 节）；接回之前与接不回来时输入框不能发送，
  接不回来时提示“没能接上这次回答的进度，可以刷新页面查看最新的结果。”。
- `apiStream` 另接受 `lastEventId`（写 `Last-Event-ID` 头），`204` 原样返回给调用方（接回时没有这次运行的事件）。
- `ApiError.body`（`shared/api/errors.ts`，取用 `errorBodyOf`）：http 失败时的整个 JSON 响应体。只给知道这个接口合同的调用方，
  用自己的解码器读错误信封旁边的字段；目前只有检测失败用 `decodeProbeFailure` 取本次的探测报告。网络失败、无效响应时没有响应体。
  显示的文字：检测报告里每一步的 `failure.error.message`（后端已脱敏，最多 500 字）原样列出；`provider_unreachable` 用错误信封的 `message`
  （后端按错误分类给的固定说明）；其他错误码按前端的文案表选择。

## 5. 页面上下文与个人数据刷新（C5）

- 接线：steward 不导入其他 feature，`context/host.tsx` 定义 `StewardHost = {page, playback, refreshData}` 与 `StewardHostProvider`；
  `app/shell/steward-host.tsx` 的 `StewardHostScope` 在 `AppShell` 里挂一次，面板与整页对话都在它下面。`usePageContext`、`usePlaybackContext`
  读这里；没有 provider（单独的组件测试）时两者是 `null`，刷新什么也不做。
- 正在看（`usePageSource`）：`useMatchRoute` 匹配 `/albums/$groupId`（`album`）、`/artists/$artistId`（`artist`）、`/me/playlists/$playlistId`（`playlist`），
  ID 不是 UUID 时不带 ID；标题只从查询缓存取（`useSyncExternalStore` 订阅查询缓存，不发请求）：专辑取 `catalogKeys.group` 的 `representative.title`，
  歌单取 `personalKeys.playlistDetail` 的 `playlist.name`，艺人在 `artistsKeys.details` 下按 ID 找 `artist.name`（键里有目录 epoch）；还没读到时只带路径与 ID。
  其他页面是 `other`，标题是侧栏里的名字（`nav-items.ts` 的 `navLabelOf`），另有“我的”“搜索结果”“设置”；整页对话（`/steward*`）里是 `null`。
- 正在播放（`usePlaybackSource`）：`usePlayerSelector` 分别选出正在控制的分区 ID、设备名（就是分区名）、当前曲目的 ID、标题、艺人与 `status`；
  还没读到分区时是 `null`，分区里没有曲目时只带分区与状态。
- 标签行（`context/context-chips.tsx`，原型的 `.ctx`/`.chip`）：在输入框上方，“正在看：标题”（没有标题时是路径）、“正在播放：曲名”
  （没有曲目时“播放设备：设备名”），每个标签有“移除“正在看””“移除“正在播放””按钮；没有标签时整行不渲染。标题只按文本节点渲染，过长时省略号，`title` 属性给全文。
- 点掉只对当前草稿有效：`dock-store` 按草稿的键（会话 ID 或 `"new"`）记下点掉时那一项的标识（页面是路径，播放是分区 ID 加曲目 ID），
  面板与整页共用；换了页面或曲目后新的标签照常出现，只是标题读到了或状态变了不算换。发送时按当时的标识算出 `context` 并清掉点掉的记录；
  开流前失败、文字放回输入框时，点掉的记录一起放回。继续（“重试”“继续”）不带上下文，也不动点掉的记录。
- 请求体：`requestContextOf` 只放没被点掉的项，路径截到 512 个字符、标题类截到 300 个字符（后端超出时 400）；两项都没有时不发 `context`。
  消息解码器接受用户消息上可选的 `context`（可以是 `null`），对话里不显示。
- 数据刷新：`tool-labels.ts` 给改动类工具标 `changes`（`set_favorite` → `favorites`；歌单的六个写入 → `playlists`；`delete_play_history_item` → `history`；
  C6 的专辑治理与 `revert_operation` → `catalog`，艺人的合并、拆开、改名、忽略候选 → `artists`），`toolDataScopeOf` 取出；工具结束（成功或失败都算）时
  `refreshData` 让 `personalKeys.favorites`、`personalKeys.playlists` 或 `listeningKeys.all` 失效；`catalog`、`artists` 让 `governanceKeys.all` 失效，
  再分别调用 app 的 `useReleaseDataRefresh`、`useArtistDataRefresh`（与管理员在页面上操作之后相同）。方案项的“撤销”成功后按 `catalog` 刷新。
  只读工具与播放类工具不刷新：播放区的变化由分区事件推到浏览器。不通知其他标签页，它们靠窗口重新聚焦时的重新读取对齐。

## 6. Markdown 回答（`components/answer.tsx`）

- `react-markdown` 加 `remark-gfm`，不启用 `rehype-raw`：回答里的 HTML 标签按文字显示。
- 链接只在协议是 `http`/`https` 时渲染成 `<a target="_blank" rel="noreferrer noopener">`（`safeHref`），其他协议与相对地址只显示文字。
- 图片不加载，显示“［图片：替代文字］”。表格套一层可横向滚动的容器，单元格一律左对齐，不用 Markdown 的对齐标记。
- `frontend/vite.config.ts` 的 `codeSplitting.groups` 里有 `markdown` 分组，按包名列出 `react-markdown`、`remark-*`、`mdast-util-*`、
  `micromark*`、`hast-util-*`、`unist-util-*` 等间接依赖。升级或新增 Markdown 相关依赖时同步这个列表，否则新的间接依赖会落进首屏的 `vendor`。

## 7. 入口与名字

- `useStewardEntry()`：设置查询（`GET /me/steward/settings`）读到了才显示入口；读取失败、还没读到、或是 `403 steward_disabled`
  （包括之前读到过、重读时被拒）都不显示。名字取 `display_name`，空时是“管家”。侧栏、标签栏、面板标题与输入框提示都取这里，
  设置页保存后直接写设置缓存，不刷新就变。
- 侧栏“聆听”里管家排在艺人之后（`app/shell/nav-items.ts` 的 `steward` 项，`visibleNavGroups(role, {steward})`）：在别的页面上是
  开合面板的按钮（`aria-expanded`、`aria-controls="steward-dock"`，`id="steward-entry"`），在整页对话里是指向 `/steward` 的当前项链接。
  图标是 `nav-items.ts` 的 `StewardIcon`（lucide `Loader`，对应原型的八道光芒），侧栏与标签栏共用。
- 窄屏标签栏第五项在“艺人”与“我的”之间（`app/shell/tab-bar.tsx`），链接到 `/steward`，名字过长时省略。
- 提示点（C10）：`useStewardEntry().unread` 取设置的 `unread_conversation_count`（缺省为 0），大于 0 时侧栏入口名字右侧、标签栏图标右上角各有一个强调色圆点（`aria-hidden`），
  数量经 `aria-describedby` 指向的隐藏文字说明（`stewardUnreadText`：“N 个对话有新回答”），入口的可访问名称不变。
  不为它轮询：运行结束（`refreshAfterRun`）、打开会话标记已读后（`useMarkRead`）与窗口重新聚焦时随设置查询重读。

## 8. 面板（`app/shell/app-shell.tsx`、`dock-store.ts`、`components/panel.tsx`）

- 面板状态在 `dock-store.ts`：`open` 记在 localStorage（`roomusic.steward.dock-open`）；`conversationId`（只记读取成功过的会话）、
  `newModel`（空白新对话预选的模型）、`blankIntent`、`focusPending` 只在内存。草稿与对话区提示按会话 ID（空白新对话用 `"new"`）存在同一个
  模块里，可订阅（`useDraft`、`useViewNotice`）：新对话创建后对话区按新会话重新挂载，开流前失败写回的文字与提示照样显示。
- 断点（外壳样式表，`--layout-steward-width` 390px）：≥1100px 外壳多一列，面板挤占内容；768–1099px 面板浮在内容上（右侧、带阴影、
  不盖住播放栏），焦点在面板里时按 Esc 收起；焦点在面板外时 Esc 归页面自己（提示、专辑详情等），输入法组合中的 Esc、菜单与对话框已处理的 Esc
  也不算；<768px 不渲染面板。整页对话（`/steward*`）里不渲染面板。
- 焦点：由用户点侧栏入口打开时（`toggleDock` 设 `focusPending`），内容就绪后焦点移进输入框，没有输入框时移到面板本身；就绪前用户已把焦点挪去别处
  （外壳传入的 `canTakeFocus`：焦点不在侧栏入口也不在 `body`）就不移。页面载入时按记住的开关打开的不移焦点。收起（“收起”按钮、Esc）后焦点回到
  侧栏入口 `#steward-entry`。
- 面板标题栏：名字与当前对话标题，新对话、展开到整页（同时收起面板）、收起。记着的会话读到 404 时回到空白新对话。

## 9. 整页与 `/steward` 的去向（`components/steward-page.tsx`）

- 路由 `/steward` 与 `/steward/$conversationId`（`app/router.tsx`），两条路由之间切换时页面重新挂载。宽屏左边是会话列表（今天、本周、更早；
  每项名下的状态文字见第 14 节；每项“更多”里改名、删除），右边是对话；<768px 两者分两屏，标题栏左侧“全部对话”切到列表。
- `/steward` 依次转到：面板记着的会话 → 列表里最近的会话 → 空白新对话。用户在应用里要的是空白新对话时设 `blankIntent`，不再转到最近的会话：
  “新对话”按钮与展开空白面板调用 `startBlankConversation`，标签栏调用 `preferBlankConversation`（面板记着会话时仍转到它），记着的会话 404 时同样。
  `blankIntent` 在打开某个会话后清掉，刷新后不存在，所以刷新或直接打开 `/steward` 会到最近的会话。
- 地址里的会话读取成功才记进面板状态；不存在或非法的 ID 显示“这个对话不存在或已经删除”，不记。删除正在回答的会话时确认框改成“停止回答”。
- 空白新对话发第一条消息时才创建会话（预选了模型就随后 `PATCH`；`PATCH` 失败时仍换到建好的会话，消息不发、文字留着、提示模型没有切换成功）。
- 成员开关关闭（设置查询是 `steward_disabled`，旧数据还在也一样）时整页只有说明。

## 10. 设置页 Agent 栏目（`settings/`、`app/routes/agent-settings-page.tsx`）

- `/settings/agent`，所有登录用户都有；`/settings/tokens` 经根路由的旧地址重定向到它（`settings-sections.ts` 的栏目名是“Agent”）。
- 名字：保存只提交 `{display_name}`，“恢复为‘管家’”提交空字符串，最多 20 个 Unicode 字符。
- 模型：地址与密钥只经“检测”保存（探测接口成功即保存），对话模型另有“保存”（只提交 `{model}`）。审查模型（C7）在对话模型下面，同样可选检测到的模型或直接填写，
  “保存”只提交 `{review_model}`；留空（占位“与对话模型相同”）提交空字符串，说明写明两者相同时审查的独立性有限。检测进行中（`useProbePending`，按 mutation key 判断）
  同一栏的改名保存、模型保存、清除、更换与“修改”都停用。
- 密钥：输入值只在 `AgentModel` 的组件 state 里；提交检测时放进一个 ref，由 `useProbeSettings` 的 `mutationFn` 取用，检测结束（成功或失败）就清掉，
  不进 mutation 的 `variables` 与任何浏览器存储；检测成功后清空输入框。已存密钥只显示末四位，带“更换”“清除”（`{key: null}`）；`key_needs_reentry` 时
  提示重新填写，同样可以清除。已存密钥时把地址改成别的主机或端口（`authorityOf`，与后端 `endpointAuthority` 同一规则），密钥一行换成输入框并说明
  要重新填写，留空表示这个地址不需要密钥（后端会清掉旧密钥，合同 4.2 规则 1）；只改路径时沿用已存密钥。
- 检测结果：“修改”写 `overrides`（在已有覆盖项上整体替换），改过的标“手动修改”，可以“改回检测结果”（去掉这一项）。改接口类型、换地址或清除、更换
  密钥后，后端清掉检测结论（规则 2）；`probe_result` 为空但协议或覆盖项不为空时只渲染“接口类型”一行（仍可修改、改回），并提示之前的检测结论已作废、
  需要重新检测。检测失败时显示本次报告（标注没有保存）与每一步失败的原因，原设置不变；保存模型或清除密钥时这份报告一起清掉。
  `egress_denied` 时说明成员只能用公网地址、需要管理员放行。
- 保存、检测得到 `403 steward_disabled` 时让设置查询失效，栏目随即切到说明。
- 记忆（C10，`settings/agent-memories.tsx`）排在联网搜索之后、访问令牌之前，见第 14 节。
- 访问令牌：C3 的 `AccessTokens` 原样放在这一栏。成员开关关闭时成员看到说明，名字与模型不显示，记忆与令牌两组保留（记忆可以查看与删除、不能添加与修改；令牌按 C3 合同仍能查看与吊销，不能新建）。
- 成员段只对管理员渲染：成员开关（`/admin/steward/settings`）与“成员可以连接的内网地址”（`/admin/steward/egress-allowlist` 的列表、添加、移除）。

## 11. 修改方案与模式（C6）

- 方案的界面状态（`run/plan-store.ts`，模块级 store，按方案 ID 存）：`phase`（`pending`、`submitting`、`decided`、`superseded`、`expired`、`gone`）、
  勾选的项（默认全选）、“不再问我”的工具、提交时接受的项、确认被拒绝的原因、每一项的撤销结果（带幂等键，结果不确定后重试沿用）。面板与整页互换、切页面不丢，
  登出时 `resetPlans` 清掉。要显示的方案由 `plansOf` 算出：本页记下的，加上会话详情里没记过的未处理方案与已处理的历史（`plans`，按服务端状态显示）；
  等待决定的方案内容以会话详情为准（摘要变了换成新内容，原来的勾选保留、新出现的项默认勾选），过了 `expires_at` 是 `expired`（`useConversationPlans` 在最早的有效期到时重绘），
  会话详情已读到、本页没有运行、服务端却不再把它当未处理时，取 `plans` 里的状态，没有时是 `gone`。
- 方案卡片（`components/plan-card.tsx`，原型的 `section.plan`）：标题“修改方案”与一句话（“N 项，都可以撤销”“其中 M 项不能撤销”、已决定后“应用了 N 项”“没有修改”、
  “已作废”“已过期”“已失效”）；每项一个勾选框（可访问名称是工具中文名加目标名）、字段名（`planFieldLabel`）、旧值（`<del>`，删除线）与新值（`<ins>`，金色下划线），
  读屏另有“原来”“改为”；“依据（本地证据/外部资料/本地证据与外部资料）：说明”；不可撤销的项标“不可撤销”。“应用 N 项修改”（没有勾选时停用）、“不修改”（提交空的接受列表）、
  方案里全部项都可以撤销的工具各一个“这次对话里，<工具中文名>不再问我”（`autoApprovableTools`，只在 assistant 模式；Steward 模式的确认不接受免确认工具，
  卡片不显示这个勾选，提交的 `auto_approve_tools` 是空数组，后端对非空值返回 `400 invalid_input`）；不可撤销的工具（艺人合并、拆开、改名、忽略候选）没有这个勾选，
  assistant 模式下每次都要确认。提交的 `auto_approve_tools` 只带勾过、可以免确认、并且至少有一项被接受的工具（`autoApproveToolsOf`；后端对其余情况返回 `400 invalid_input`）。
  目标名、旧值新值与依据都只按文本节点渲染。会话正在回答时两个按钮停用。
- 提交（`useDecidePlan`）：`phase` 先变 `submitting`，流开起来后 `decided`；开流前被拒绝时就地说明（`plan_digest_mismatch` 提醒内容已换成最新的、`conversation_busy`、
  网络失败等），`plan_expired` 变 `expired`，`plan_not_found` 变 `gone`。提交后按钮消失，焦点移到卡片标题。
- 结果列表：每项“已执行”“没有执行：<失败短语>”“未选”（`declined_by_user` 或没有接受）“未执行”（`not_executed` 或作废）、“正在执行”（接受了、还没有结果、会话在回答），
  “还没执行”（接受了、还没有结果、不在回答中）；已执行且可撤销、能取到操作记录的项有“撤销”，
  调用受治理操作的撤销接口（`endpoints.ts` 的 `revertOperation`，带新的幂等键），成功写“已撤销”，被拒绝就地说明原因，结果不确定时给“重试撤销”。
  作废、过期、已失效的方案只列出内容，不能再应用，底部一句说明。
- 位置：卡片放在方案项对应的调用所在的回合（`placePlans`，按 `call_id` 找；找不到时是最后一个回合），排在那一步的文字之后，确认后的回答接在卡片下面。
  运行中卡片在“live”回合里，运行结束重读之后落到已保存的回合（重新挂载，状态在 store 里不丢）。
- 没执行完的方案：已确认、但有接受的项在 `plans` 里还没有 `result`（运行被停止或服务重启之后，`unfinishedPlanIdsOf`）时，`canContinue` 为真，
  卡片底部写“回答停在了这里，有确认过的修改还没执行。点“继续”接着执行；发新消息不会执行它们，会记为未执行。”并给出“继续”（走继续接口，后端先执行剩下的项）；
  结束说明不再重复“继续”按钮，输入框提示行写“发新消息不会执行方案里剩下的项”。只有“继续”恢复执行，新消息让剩下的项记为未执行（或对账出已执行的结果）。
- 发消息：有未处理的方案时输入框照常可用，提示行写“发新消息会作废上面的方案”；流开起来后 `supersedePlans` 把它标为作废（后端同时以 `not_executed` 回给模型）。
  运行以 `awaiting_confirmation` 结束时没有结束说明。
- 模式（`components/mode-menu.tsx`，原型的 `.mode` 与菜单）：输入框下模型按钮左边，主色字，图标与名字（Assistant `CircleAlert`、Steward `ShieldCheck`、Operator `Zap`），可访问名称“模式：<名字>”。
  菜单（`useMenu` 的 `description` 与 `footer`）三项：Assistant“修改曲库前先给你看方案，你确认后执行”，Steward“由另一次模型调用审查，通过的自动执行；拿不准的回来问你”（C7），
  Operator“不询问，直接执行；外部资料里的内容也可能促成修改”，当前项右侧“当前”，底部“Shift + Tab 在输入框里切换”。
  输入框（`Composer` 的 `onShiftTab`）里按 Shift+Tab 按 Assistant → Steward → Operator 轮换，不移动焦点；其他地方的 Shift+Tab 照常。
  空白新对话选的模式记在 `dock-store` 的 `newMode`，创建会话时带上（`POST {mode}`），之后回到默认；已有会话经 `PATCH {mode}` 切换，失败就地提示“没能切换模式”。
  正在回答、会话在运行而本页还没接上、有未处理的方案、正在创建或修改会话时按钮停用，Shift+Tab 不拦截。提示行随模式：“修改曲库前会先给你看方案”
  “修改曲库前由另一次模型调用审查，拿不准的会回来问你”（没有另选审查模型、或审查模型就是这个会话的对话模型时，后面加“；审查与对话是同一个模型，独立性有限”）
  “修改曲库时不询问，直接执行”。
- 操作记录页（`features/governance` 的 `OperationList`）在操作者下面显示来源标签：对话且带 `review_id`、审查结论是 `approved`（或没有结论字段）是“管家（审查通过）”（C7），
  对话且带 `plan_id` 是“管家（确认）”（审查转给用户、由用户确认执行的也是这个），对话不带 `plan_id` 是“管家（直接执行）”，`mcp` 是“MCP”；没有 `origin` 的不显示。

## 12. 审查（C7）

- 线上形状（`api/decoders.ts`）：设置多 `review_model`（可省略，空字符串表示与对话同一个模型）；会话的 `mode` 可以是 `steward`；会话列表每项可带
  `pending_item_count`（未处理方案里等用户决定的项数，后端返回整数、没有未处理方案时为 0；解码时可省略）；方案带 `review`（最近一次审查，可空），方案项的 `decision` 多 `rejected_by_review`；
  运行事件多 `review_started {plan_id, model}` 与 `review_finished {review}`（`step` 可省略），`done.reason` 多 `review_unavailable`。审查是
  `{review_id, plan_id, model, status: completed|unavailable, policy_version, expires_at, items: [{item_id, decision: approved|rejected|needs_human_confirmation, risk, reasons, source: model|policy}]}`，
  不可用时 `items` 为空。重新审查是 `POST …/plans/{plan_id}/review`（没有请求体，响应是 SSE，`endpoints.ts` 的 `openReview`）。
- 流程（后端）：方案落库 → `review_started` → `review_finished` → 批准的项逐个 `tool_started`/`tool_finished`（审查前已为送审的调用发过一次 `tool_started`，
  归并时同一 `call_id` 原地更新，不重复）→ 有转人工的项时发只含这些项的 `plan` 并以 `awaiting_confirmation` 结束；审查不可用、或批准之后执行之前内容变了时
  以 `review_unavailable` 结束，方案保持未处理。两种结束都没有结束说明（`run-ended.tsx`）。被拒绝的项以失败的 `tool_finished`（`rejected_by_review`）落库，失败短语“审查没有通过”。
- 状态（`run/review-store.ts`，模块级，按方案 ID 存，登出时 `resetReviews`）：运行里收到的审查（`reviewStarted` 记“审查中”，`reviewFinished` 记结论）、
  用户选了“我自己确认”（`manual`）、重新审查的请求状态与开流前被拒绝的原因。`useStartRun` 的 `onReview` 写入它；收到 `review_started` 时重读会话
  （审查事件里只有方案 ID，送审的各项要从会话详情取）。流一结束（`run-store` 的 `onEnd`：任何原因的 `done`、没有 `done` 就断开、读流出错，在重读会话之前）
  就 `settleReviews`，把没等到结论的“审查中”去掉：审查中被停止（`done.reason = cancelled`）或保存失败时不会有 `review_finished`，也不落审查记录。
  要显示的审查 `reviewViewOf`：本页运行中以运行里收到的为准，否则以会话详情里方案的 `review` 为准，没有时用运行里收到的。
- 渲染（`conversation-view.tsx`）：方案有审查（或正在审查）时用 `components/review-result.tsx` 代替修改方案卡片，位置规则不变（`placePlans`）；
  各项的内容取会话详情里的完整方案（运行里的 `plan` 事件只有等确认的项）。
- 审查结果（原型的 `section.review`）：标题“审查结果”，右侧“由 <模型> 独立审查，不带这次对话的内容”；审查模型与这个会话的对话模型相同时另起一行
  “审查与对话用的是同一个模型，独立性有限；可以在设置里另选审查模型。”。审查中写“审查中…”（`role="status"`），各项状态“审查中”。
  审查完成后按审查的项逐行：状态（已执行 `Check`、等你确认 `Hand`、没有执行 `Ban`、正在执行/还没执行 `Clock`）、修改摘要（与方案卡片相同的工具名、目标、旧值新值、依据）、
  没有执行的原因（审查没有通过、你没有选这一项、方案已作废/已过期/已失效、批准之后内容变了需要重新审查等）、理由（`reasons` 用“；”连接）、
  策略转人工的项写“按规则交给你确认，不是审查模型的判断。”，风险中等与高另标出来。已执行且可撤销的项有“撤销”（与方案卡片相同的 `RevertControl`）。
  理由、目标名与值都只按文本节点渲染，不走 Markdown。
- 转人工的项：方案还在等待决定时，审查结果里面嵌入修改方案卡片（`PlanCard`，标题“等你确认的修改”，只含审查转给用户、方案里还没处理的项），规则与 C6 相同：
  不可撤销的修改没有“不再问我”，确认走确认接口。提交后卡片收起，各行显示结果，焦点移到“审查结果”标题。确认过但没执行完时审查结果底部给“继续”。
- 需要重新审查（审查 `unavailable`，或审查完成但有批准的项没有执行、本页也不在回答）：写“审查不可用，这份方案什么都没有执行。”或“批准之后内容有变化……”，
  给“重新审查”（只在 Steward 模式的会话；`useRereviewPlan`：走审查接口，运行从方案项对应的条目开始，开流前被拒绝就地说明，
  其中 `409 review_not_applicable` 写“当前模式不使用审查，可以自己确认。”，`plan_expired`、`plan_not_found` 改变方案的状态）与
  “我自己确认”（显示标题为“修改方案”的卡片，包含全部还没处理的项，焦点只在点了这个按钮之后移到卡片标题）。重新审查的 `review_started` 清掉“我自己确认”。
  嵌入的确认卡片只在 assistant 模式有“不再问我”。
- 模型菜单（`model-menu.tsx`）：Steward 模式下标出审查模型：另选了的写“正用作审查模型”，没有另选时当前模型写“当前，也用作审查模型”。其他模式不标。
- 会话列表（`session-list.tsx` 的 `sessionStateOf`）：名字下面写“正在回答”（本页正在审查时是“审查中”），`pending_item_count` 大于 0 时写“N 项等你确认”，两者用“，”连接。

## 13. 联网（C8）

- 线上形状（`api/decoders.ts`，按任务的 `wire-shapes.md`）：设置多 `search_source`（`none`/`searxng`/`tavily`/`native`）、`searxng_url`、`search_key_saved`、
  `search_key_last_four`、`search_key_needs_reentry`（都是必需字段）；`protocol` 多 `responses`；`probe_result` 多可省略的 `native_search`（`supported`/`unsupported`/`unknown`）
  与 `responses_available`（C8 之前保存的检测结论没有，按“没有确认”处理），`overrides` 多 `native_search`。生效的自带搜索是 `effectiveNativeSearch`：覆盖项、检测结果、`unknown` 依次取。
  测试搜索 `POST …/settings/search-test`（没有请求体，`decodeSearchTest`）总是 200：`{ok: true, result_count}` 或 `{ok: false, code}`。
  运行事件多 `search {step?, search_id, query, status: completed|failed, results?: [{title?, url}], error_code?}` 与 `citations {step?, citations: [{url, title?}]}`；
  助手消息多可省略的 `searches`（同 search 事件去掉 type、step）与 `citations`，缺省按空数组。导出的解码器 `decodeSearchTest`、`decodeSearchRecord`、`decodeCitation` 供契约测试登记。
- 设置（`settings/agent-search.tsx`，模型一组之后，原型的 `.radio`）：说明一句（只发搜索词，搜到的内容是外部资料）、四个带说明的单选项（不联网搜索、自己的 SearXNG、Tavily、模型自带的搜索），
  SearXNG 时一行地址，Tavily 时一行密钥；“保存”只提交 `{search_source}`，SearXNG 另带 `searxng_url`，填了 Tavily 密钥另带 `search_key`。
  密钥与模型密钥同样处理：只在组件 state 里，提交时放进 ref（`useSaveSearchSettings`），不进 mutation 的 variables；已存的只显示末四位，带“更换”“清除”（`{search_key: null}`，按钮的可访问名称带“Tavily 密钥”）；
  `search_key_needs_reentry` 时警示并换成输入框，同样可以清除。SearXNG 没填地址、Tavily 没有可用的密钥时不能保存。
  “模型自带的搜索”只在生效的自带搜索是 `supported` 时可选，否则停用并说明原因（检测确认不支持、没能确认、还没检测）与去哪里改；已保存为模型自带、之后结论变了时这一项保持选中，可以换走。
  “测试搜索”只在已保存的来源是 SearXNG 或 Tavily、并且没有未保存的改动时可用（否则写“保存之后才能测试。”）；成功写“这次搜到 N 条结果。”，失败按 `code` 一句中文
  （`searchTestFailureTexts`：`searxng_json_disabled`“这个实例没有开启 JSON 输出……”、密钥不对、额度用完、连不上、没有配置、地址不允许）。
  保存被拒绝按错误信封说明：`egress_denied` 写成员的 SearXNG 地址只能是公网地址、请管理员放行（与模型端点同一种写法），`invalid_input` 提到模型自带要先确认支持。
- 检测结果（`settings/probe-result.tsx`）多“自带搜索”一行（支持、不支持、还没有确认），“修改”写 `overrides.native_search`，可以改回检测结果；检测结论被清掉时这一行与“调用工具”一样不显示。
  接口类型的菜单多“OpenAI Responses 接口”：检测没有确认（`responses_available` 不是 true）时这一项带说明“检测没有确认这个接口支持”，仍然可以选；手动选了它时检测结果下面再写一句。
  检测结论本身是 `responses` 时（OpenAI 兼容端点的 Responses 工具调用探测通过）接口类型直接显示这一项，不带这两处说明。
- 过程条目（`tool-labels.ts`）：`web_search`“联网搜索”（查询词，“N 条结果”）、`fetch_page`“读取网页”（访问的域名，跳到别的域名写“甲 → 乙”；“已读取”或“已读取，过长的部分截掉了”）、
  `search_music_metadata`“查 MusicBrainz”（“专辑：查询词”，“N 条”）、`get_music_metadata`“读取 MusicBrainz 条目”（条目的标题，没有时是种类）。只显示域名（`domainOf`，去掉 `www.`），不显示路径与查询串。
  失败短语加联网的错误码（`fetch_blocked_address`“不能访问这个地址”、`fetch_blocked_port`“只能访问 80 和 443 端口”、`fetch_unsupported_type`“不是文字网页”、`rate_limited`“请求太频繁”等）。
- 模型自带搜索（`run/reduce.ts`）：search 事件与落库的 `searches` 都还原成名字为 `NATIVE_SEARCH`（`$native_web_search`，不会与工具重名）的已结束条目，`input` 是 `{q}`，`output` 是 `{results}`，
  显示为“联网搜索（模型自带）”；落库时排在这条助手消息的工具调用之前。算进“查了 N 处资料”，没有耗时。
  搜索条目（工具与模型自带）成功时下面列出结果（`components/sources.tsx` 的 `SourceList`，可访问名称“<条目名>的结果”）：标题（空时用域名）与灰色的域名。
- 来源（`Turn.citations`、`RunView.citations`，`mergeCitations` 按地址去重）：回合里所有回答之后一块“来源”（列表的可访问名称“来源”）；继续或确认时运行里的来源并进最后一个回合。
  没有可显示的来源时整块不出现。
- 外部内容的显示：标题、地址、查询词只按文本节点渲染；只有 `http`/`https` 的地址是链接（`safeHref`），新标签页打开，`rel="noopener noreferrer"`；其他地址只显示标题，标题也没有时不列。

## 14. 后台运行、未读与记忆（C10）

后端合同见[管家运行时合同](../backend/steward-runtime-contracts.md)“C10 步骤 1”与第 4、5、10 节。

- 运行与连接（`run/run-store.ts`）：
  - 每个数据事件带 `seq`（与 SSE 的 `id` 相同），`ActiveRun.lastSeq` 记下收到的最后一个；`isNewEvent(lastSeq, event)` 丢掉序号不大于它的事件，没有 `seq` 的照常收下。
    回放里同一步连续的 `text` 合成一条，序号是合并范围的末尾；断在中间时服务端按 `Last-Event-ID` 给出准确的后缀，客户端照常追加。
  - 流没有 `done` 就断了（发消息、继续、确认、重新审查、接回的流都一样）：按 `RECONNECT_DELAYS`（第一次马上，之后 1、2、4、8、15 秒，收到新事件后从头算）
    用 `openEvents(id, lastSeq)` 重新接回，期间 `reconnecting` 为真。`204` 表示服务端没有这次运行的事件，不记结束信息，以重读的落库内容为准；
    4xx 不再重试、重试用完时结束信息是 `disconnected`（“连接断了，没能接上这次回答”，说明回答可能还在后台继续、可以刷新页面查看）。
  - 接回（`attachRun`，`hooks/use-run.ts` 的 `useAttachRun`）：对话区在会话详情 `status` 是 `running` 而本页没有这次运行时调用（刷新、另一个标签页、重新打开），
    从 0 回放，`userText` 为 `null`、并进最后一个回合。`baseSequence` 取 `runBaseSequenceOf(detail)`：这次运行的消息都落在 `last_run_finished_at` 之后（按微秒比较，
    `compareTimestamps`）；第一条是用户消息时保留到它，否则保留到它之前一条，之后已经落库的部分由回放重新显示，不重复。
  - `detached`：`204`、被拒绝或重试用完时记下会话详情的 `updated_at`，同一版本不再自动接回（不反复请求）；开始新的运行时清掉。
  - `history_gap`：`RunView.gap` 为真并调用 `onGap`（重读会话详情）；`threadOf` 改为显示全部落库内容、运行只给出“正在想…”，`done` 后照常重读。
  - 登出与换用户：`reset` 中止所有连接（含接回与等待中的重试），沿用 `epoch` 的规则，上一个用户的流内容不会写进下一个用户的界面。
- 已读：对话区挂载时用 `run/viewing.ts` 登记“正停在这个会话里”，读到会话后 `useMarkRead` 调用 `POST …/read` 一次（会话原本未读或入口有提示点时再重读列表与设置）；
  运行结束时 `refreshAfterRun` 在用户还停在这里时先标记已读，再重读，提示点不会一闪而过。
- 会话列表（`components/session-list.tsx` 的 `sessionStateOf`、`isRunningItem`）：名下的状态是文字——“正在回答”（本页审查中时“审查中”，本页的运行、`status` 或
  `run_status` 是 `running` 都算）、“N 项等你确认”（`run_status` 是 `awaiting_confirmation` 而没有项数时“等你确认”）、“有新回答”（`unread` 且不在运行，标题前另有装饰性的圆点）。
  C10 之前的后端没有 `unread`、`run_status` 等字段，解码器按可缺失处理。
- 摘要分界：`summary_through_message_id`（会话详情）或运行里的 `history_compacted` 给出摘要覆盖到的最后一条消息，`compactedTurnKeyOf` 找到它所在的回合，
  `Thread` 在这个回合之后显示一行“更早的内容已压缩”（两侧细线）；完整历史照常显示，摘要正文前端拿不到。
- 发送处的新错误：`409 steward_run_limit`、`steward_run_invalidated` 显示服务端给的原因；`503 steward_shutting_down` 写“服务正在关闭，请稍后再试。”。
- 记忆（`settings/agent-memories.tsx`、`hooks/use-memories.ts`）：
  - 列表按更新时间倒序，每条内容保留换行、只按文字显示，下面是更新时间与“修改…”“删除…”（按钮经 `aria-describedby` 指向这条内容）；
    条数与字数的上限取自接口的 `limit`、`max_length`，写在列表上方，并说明只有自己能看到、每次对话都会发给配置的模型。空状态说明记忆的来源（管家在对话里记下的或手动添加的）。
  - 添加与修改用对话框（`textarea`，字数按 Unicode 字符计、去掉首尾空白，超出上限时说明并停用“保存”）；删除与全部清除先确认。
    写操作没有幂等键、不自动重试，无论成败都重读列表；`invalid_input`、`memory_limit_reached`、`not_found`、`steward_disabled` 有各自的说明。
  - 成员开关关闭时成员仍能查看与删除，“添加记忆”停用、没有“修改…”，上方说明原因。
  - 工具 `remember`（主要参数是内容，结果“已记下”或带 `id` 时“已改写”）、`forget`（“已删除”）是改动类，`changes: "memories"`：结束后 steward 自己让记忆查询失效
    （`ToolDataScope` 比 app 的 `StewardDataScope` 多这一类）；`list_memories` 只读，单位“条”。

## 15. 测试

| 位置 | 内容 |
| --- | --- |
| `src/test/fake-steward.ts` | Vitest 的管家替身（C8 起设置带联网搜索的字段（`searchSettingsDefaults`，样本有这些字段时以样本为准），`PUT` 处理 `search_source`、`searxng_url`、`search_key`，提交来源 `native` 而生效的自带搜索不是 `supported` 时 `400 invalid_input`；测试搜索按已保存的来源回答，`searchTestResult` 预置结果；C6 起另有方案：会话详情带 `pending_plan` 与 `plans`（调用已有结果的项带 `result`），读取时判定过期；发新消息作废未处理的方案；确认接口按设计的校验顺序——会话与方案归属且 pending → 未过期 → 摘要 → 会话空闲 → 选择的项与工具（免确认的工具须全部可撤销且至少有一项被接受）——返回 `plan_not_found`、`plan_expired`、`plan_digest_mismatch`、`conversation_busy`、`invalid_input`，通过后记下 `decisions[i].stream`；新建与 `PATCH` 接受 `assistant`、`steward`、`operator`，有运行或未处理方案时改模式 409；撤销接口记下操作 ID 与幂等键，`nextRevertResponse` 预置拒绝；
C7 起方案可带 `review`（`reviewOf` 合成）与 `reviewed`（审查已处理的项：批准执行的 `accepted` 带操作记录，拒绝的 `rejected_by_review`，这些项不能再确认），
会话列表带 `pending_item_count`；Steward 模式会话的确认带非空 `auto_approve_tools` 是 `400 invalid_input`；
重新审查接口的校验顺序是会话与方案归属且 pending → 未过期 → 会话是 Steward 模式（否则 `409 review_not_applicable`）→ 会话空闲，`nextReviewResponse` 预置开流前的拒绝，
带请求体是 400，通过后记下 `reviews[i].stream`；C10 起会话带 `unread`、`run_status`、`last_run_finished_at`、`summary_through_message_id`，设置的未读数按会话的 `unread` 数出来；
`eventLogs` 里的会话接回时打开可控的流（`attaches[i]`，记下 `Last-Event-ID`），其余 204，`nextAttachResponse` 预置拒绝；`fakeSse` 的 `send` 遇到 `seq` 同时写 `id` 行；
取消让这个会话每条还开着的流收到 `done`（cancelled）并把会话置回空闲；`POST …/read` 记进 `reads`；记忆接口按上限与成员开关回答）：`installStewardApi` 实现设置的读写（`PUT` 按后端 `mergeSettings` 的规则 1–3 清密钥、清检测结论、降级工具能力）与检测、会话增删改查、可控的运行流（`fakeSse` 逐个推送事件、`fail` 模拟断开，请求被中止时流随之出错）、取消、管理员接口；`nextRunResponse`、`probeResponse` 预置开流前的拒绝与检测失败。运行请求按后端规则检查请求体（发消息只认 `text`、`context`，`kind` 枚举与长度上限；继续带 `context` 也算不合格；不合格时 `400 invalid_request`），`runs[i].context` 记下请求体里的上下文（没有时 `null`） |
| `src/features/steward/**/*.test.ts(x)` | 解码器（含消息的 `context`、方案、模式、没有 `step` 的工具事件）、方案 store（默认值、过期、摘要变化、gone、作废、历史）、方案文字、归并（含等待确认的条目与确认后的替换）、工具标签（参数、结果说明、失败短语、只读与改动、改动的数据）、过程摘要的措辞、上下文的点掉与截断、Markdown 安全、run-store（发起、发消息请求体里的 `context`、继续没有请求体、`onToolFinished`、`onPlan`、确认的请求体与开流前拒绝、停止、登出中止） |
| `src/features/steward/**` 的 C10 用例 | 解码器（会话与设置的新字段、`seq`、`history_gap`、`history_compacted`、已读与记忆的边界用合成数据；C10 的后端样本在 `src/test/api-contract.test.ts` 登记）；归并（按序号去重、合并文字的后缀、gap 与分界、微秒比较、`runBaseSequenceOf`、`compactedTurnKeyOf`）；`threadOf`（接回不重复、gap 显示落库内容、分界的回合）；run-store（带 `Last-Event-ID` 重连与去重、204 回退、重试用完、4xx 不重试、`history_gap`、接回与 `detached`、两个 store 实例一处停止两处都结束、登出中止接回与等待中的重试、接回的流断开再接）；会话列表的三种标记；记忆工具的标签；`apiStream` 的 `Last-Event-ID` 与 204 |
| `src/app/steward.test.tsx`、`src/app/agent-settings.test.tsx` 的 C10 用例 | 入口提示点与读屏说明、打开会话后消失、后台结束时出现而停在会话里时结束即标记已读；打开正在运行的会话接回并停止、接不回来的说明与只请求一次、`steward_run_limit` 的原因、“更早的内容已压缩”的位置、列表标记与已读；记忆的空状态与上限说明、添加（去空白、超长停用）、修改、删除与全部清除的确认、到上限、后端说到上限、成员开关关闭时只读可删 |
| `src/app/steward.test.tsx` 的“修改方案”“模式” | 运行里出现方案与卡片的位置、旧值新值与依据、勾选与提交的请求体、结果与撤销、撤销被拒绝、刷新后恢复（未处理与已处理）、“不修改”、发消息作废、过期、摘要不一致；空白新对话与已有会话的模式切换、Shift+Tab 只在输入框里、运行中与有方案时停用 |
| `src/app/steward.test.tsx` 的“审查” | 运行中的审查中、结论与批准项的执行、转人工的确认卡片；刷新后的撤销、拒绝理由按文字显示、确认转人工的项；审查不可用后重新审查与“我自己确认”（Steward 模式没有“不再问我”）；不是 Steward 模式时没有“重新审查”与 `review_not_applicable` 的说明；
审查中被停止后不再显示“审查中”；批准之后内容变了；重新审查开流前被拒绝；同一个模型的说明与模型菜单里的审查模型；会话列表的“N 项等你确认” |
| `src/features/steward/run/review-store.test.ts`、`components/session-list.test.ts` | 审查的界面状态（审查中、结论、运行中与刷新后的取舍、运行结束去掉审查中）、会话名下的状态 |
| `src/app/operations.test.tsx` 的来源标签 | 管家（确认）、管家（直接执行）、MCP、管家（审查通过），没有来源的不显示 |
| `src/app/steward.test.tsx` 的“页面上下文”“工具结束后刷新个人数据” | 用 `vi.mock` 换掉 `features/player/zones/api` 的 `browserZoneApi`：设了 `fakeZoneServer` 的用例有分区与曲目，其余用例照旧（没有播放区）；专辑页的标签与请求体、点掉后发送、点掉后中断再继续（继续没有请求体）、整页里没有“正在看”、`invalidateQueries` 的键 |
| `src/app/agent-settings.test.tsx` 的“联网搜索（C8）”、`src/app/steward.test.tsx` 的“联网（C8）” | SearXNG 保存的请求体与测试搜索、测试失败的原因、Tavily 密钥（请求体、末四位、不进 mutation 缓存、清除、需要重新填写）、模型自带的可选条件与“修改自带搜索”、成员的 `egress_denied`、手动选 Responses；运行里的 search 事件、读取网页的域名与失败原因、citations 的来源列表，刷新后从落库的 `searches`、`citations` 显示一致，模型自带搜索失败 |
| `src/features/steward/**` 的 C8 用例 | 解码器（设置的搜索字段、测试搜索、search 与 citations 事件、消息上的 `searches`、`citations` 可省略）、归并（搜索条目与来源去重、落库还原）、工具标签（域名、结果条数、读取网页、MusicBrainz、错误码）、过程条目的结果列表与来源列表（只有 http/https 是链接、标题按文字显示） |
| `src/app/steward.test.tsx`、`src/app/agent-settings.test.tsx`、`src/app/tokens.test.tsx` | 页面级：入口与名字、面板与整页、流式与结束说明、会话列表、`/steward` 去向、焦点与 Esc、Agent 栏目、令牌与旧地址 |
| `e2e/support/steward.ts`、`e2e/steward.spec.ts`、`e2e/steward-plan.spec.ts`、`e2e/steward-review.spec.ts`、`e2e/steward-web.spec.ts` | 浏览器冒烟（C8 另有设置的 PUT 与测试搜索、`seedWebConversation`（模型自带搜索、读取网页、回答引用两处来源）；联网搜索一组的保存与测试、对话里的搜索条目与来源、390px 无横向溢出；C7 另有 `seedReviewedConversation`（批准已执行、转人工、拒绝各一项）与 `seedUnavailableReviewConversation`，重新审查接口一次性返回整段 SSE；审查结果、撤销与确认、390px 重新审查、三种模式与模型菜单里的审查模型；C6 另有 `seedPlanConversation` 预置停在方案上的会话，确认接口一次性返回整段 SSE，撤销接口只认方案里的那条记录；方案的勾选、应用、结果与撤销，模式菜单与 Shift+Tab）：`installStewardMock(page)` 用 `page.route` 接管管家接口（优先于 `api-mock.ts` 的上下文拦截），发消息一次性返回整段 SSE；桌面面板到整页、390px 标签栏对话与切换会话、旧地址重定向；
C10 的 `background: true` 让发消息只返回前半段事件就断开，带 `Last-Event-ID` 的接回挂着（`heldEvents`），刷新后从头接回时回放整次运行并结束：运行中刷新后回答接着显示、问题与第一步各出现一次 |

其他页面的测试没有装管家替身时，设置接口得到 404，入口不显示，不影响它们原有的断言。
