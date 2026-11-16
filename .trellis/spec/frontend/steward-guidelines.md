# 管家的前端约定

本文记 M6 C4（对话页）落地后的前端结构与约定，以代码为准：`frontend/src/features/steward/`、`frontend/src/app/shell/`、
`frontend/src/app/routes/steward-page.tsx`、`frontend/src/app/routes/agent-settings-page.tsx`。后端接口、SSE 事件与错误码见
[管家运行时合同](../backend/steward-runtime-contracts.md) 第 4、5、10 节；界面与原型的对应见 [DESIGN.md](../../../frontend/DESIGN.md) 第 4 节“管家”。

## 0. 范围

- 已有：入口与名字、右侧停靠面板、整页对话（会话列表、改名、删除）、流式回答与过程条目、结束说明（重试、继续）、模型菜单、
  未配置引导、设置页的 Agent 栏目（名字、模型与检测、访问令牌、管理员的成员开关与内网地址放行）。
- 没有（C4 的决定 D1，后端也没有）：模式按钮、“正在看/正在播放”上下文标签、方案卡片、审查结果、联网搜索设置、审查模型、
  断线或刷新后接回正在进行的回答、展开工具条目看原始输入输出。不做禁用占位。

## 1. 目录与两个公开入口

```text
features/steward/
├── index.ts          # 轻量入口：外壳、路由与运行时直接导入
├── chat.ts           # 对话界面入口：StewardPanel、StewardPage，按需加载
├── api/              # decoders.ts（设置、探测、会话、消息、运行事件、管理员、令牌）、endpoints.ts
├── run/              # reduce.ts（事件与落库消息 → 回合）、run-store.ts（进行中的运行）、refresh.ts
├── dock-store.ts     # 面板状态、草稿、对话区提示
├── hooks/            # use-steward-settings、use-conversations、use-run、use-agent-settings、use-access-tokens
├── tool-labels.ts    # 16 个只读工具的中文名、图标、主要参数与数量的取法
├── components/       # 面板、整页、对话区、回合、过程、回答（Markdown）、结束说明、输入框、模型菜单、会话列表、令牌（C3）
└── settings/         # Agent 栏目：名字、模型与检测结果、成员段、文案
```

- `index.ts` 只导出外壳与路由需要的东西：`useStewardEntry`、`DEFAULT_STEWARD_NAME`、`useDockState`、`setDockOpen`、`toggleDock`、
  `preferBlankConversation`、`resetStewardState`、`AgentSettings`。侧栏、标签栏、外壳与 `app/runtime.ts` 静态导入它；
  `package.json` 的 `sideEffects` 让没用到的导出（`AgentSettings`）不进首屏。
- `chat.ts` 导出 `StewardPanel` 与 `StewardPage`。外壳用 `React.lazy(() => import("../../features/steward/chat"))` 加载面板，
  路由用 `lazyRouteComponent(() => import("./routes/steward-page"))` 加载整页。分成两个入口，是因为同一个模块既被静态导入又被动态导入时
  不会单独成块：对话界面与 Markdown 依赖要等第一次打开面板或整页才下载。
- `features/steward` 只导入 `auth` 的公开入口与 `shared`，不导入其他 feature。

## 2. 运行状态（`run/run-store.ts`）

- 进行中的运行放在模块级 store，不放在组件里：发消息与继续由 store 发起请求并读流，切页面、面板与整页互换都不打断（R6）。
  组件用 `useSyncExternalStore(subscribeRuns, getRunSnapshot)` 订阅（`hooks/use-run.ts` 的 `useRunSnapshot`、`useRuns`）。
- 快照 `{runs, ends}` 按会话 ID 存，每次变化换新的 `Map`：
  - `runs`：`{userText, baseSequence, view, phase}`。`userText` 为 `null` 是继续（不追加用户消息）；`baseSequence` 是开始时
    已落库的最后一条消息序号；`phase` 是 `running`、`stopping`、`settling`。
  - `ends`：最近一次运行怎么结束的（`done` 事件、`disconnected`、`request_failed`），下一次运行开始时清掉；只在内存里。
- 发起：`startRun({conversationId, text, baseSequence, refresh})`，同一会话已有运行时不再发起。返回的 Promise 在流开起来
  （`{started: true}`）或开流前失败（带 `ApiFailure`）时完成，调用方据此把草稿放回输入框；之后的读流在后台继续。
  `useStartRun` 包了一层：传入重读会话与列表的 `refresh`，开流前得到 `403 steward_disabled` 时让设置查询失效。
- 结束后以落库为准：收到 `done`（或流断开）后 `phase` 变成 `settling`、记下结束信息，调用 `refresh`（`run/refresh.ts`：
  让该会话与列表的查询失效并等它们重读），完成后从 `runs` 移除。运行期间重读会话时，对话区只取 `sequence <= baseSequence`
  的消息，之后的部分由运行自己显示，不会重复。
- 停止：`stopRun` 先调用取消接口，服务端结束运行后照常发 `done`（会话已释放，重读到的是空闲）；取消接口失败或回答
  `{cancelled: false}` 时中止请求。本页没有这次运行（别的标签页在跑）时只调用取消接口。
- 登出与换用户：`app/runtime.ts` 在 `session.onEnded` 与 `dispose` 里调用 `resetStewardState()`，即 `resetRuns()`（中止所有请求、
  清空快照）加 `resetDock()`（清掉会话 ID、草稿、提示、预选模型）。`run-store` 与 `dock-store` 各有一个模块级 `epoch`：`resetRuns`、
  `resetDock` 递增它，异步操作开始时记下，返回后不一致就不再写入。发送（`components/conversation-view.tsx` 的 `send`）在建会话、
  写入预选模型、开流前被拒绝之后都先核对 `dockEpoch()`：请求得到 401 时会话边界先清空这里，旧用户的草稿与提示不能留给下一个登录的人。

## 3. 归并与显示（`run/reduce.ts`）

- 流式事件与已落库消息归并成同一种结构：`Turn = {key, userText, items, texts}`，`items` 是所有步骤的工具调用，`texts` 是各步文字。
  `applyRunEvent` 处理事件，`turnsFromMessages` 还原落库消息（助手消息里 `tool_call` 块与 `tool_calls` 按下标对上），两者显示一致。
- `StepItem.executed`：流式收到的都算执行过；落库的以 `duration_ms` 非空为准（因 `token_limit` 或取消没执行的、中断的都没有耗时）。
  摘要“查了 N 处资料，用时 X”只数执行过的；还没有耗时数据（运行刚结束、重读未回来）时只写“查了 N 处资料”。
- `RunView.waiting`：刚开始或这一步的工具都执行完时为真，界面显示“正在想…”；收到文字、有工具在执行、`done` 后为假。
- `hooks/use-run.ts` 的 `threadOf` 组合落库内容与运行：发消息是新回合，继续并进最后一个回合；`canContinue` 只在本页没有运行、
  会话空闲、最后一条落库消息是 `user` 或 `tool`（`isIncomplete`）时为真，结束说明的“重试”“继续”只在这时出现，避免 `409 nothing_to_continue`。
- 工具条目（`tool-labels.ts`）：参数取输入的 `q` 或结果对象的标题，数量取 `pagination.total` 或 `items.length`；失败按 `error_code`
  显示“没找到”“没有权限”“已停止”“没有完成”“没有执行”（`token_limit`），其余“失败”；未登记的工具名原样显示。曲库文字只按文本节点渲染。

## 4. 接口与流

- SSE 的按行解析在 `shared/lib/sse.ts` 的 `SseParser`，播放区事件流（`features/player/zones/event-stream.ts`）与管家共用。
- `shared/api` 的 `apiStream` 接受 `method: "POST"` 与 `body`：`endpoints.ts` 的 `openRun` 用它发消息与继续；开流前的拒绝按错误信封抛出
  `ApiError`，401 照常通知会话边界。运行事件由 `decodeRunEvent(event, data)` 解码，不认识的事件名丢掉，解不了的事件跳过。
- “正在别处回答”（会话 `status: "running"` 但本页没有运行）：输入框不能发送，`useConversation` 与 `useConversations` 每 3 秒重读；
  上一次读取出错时停止轮询（`busyPollInterval`）。
- `ApiError.body`（`shared/api/errors.ts`，取用 `errorBodyOf`）：http 失败时的整个 JSON 响应体。只给知道这个接口合同的调用方，
  用自己的解码器读错误信封旁边的字段；目前只有检测失败用 `decodeProbeFailure` 取本次的探测报告。网络失败、无效响应时没有响应体。
  显示的文字：检测报告里每一步的 `failure.error.message`（后端已脱敏，最多 500 字）原样列出；`provider_unreachable` 用错误信封的 `message`
  （后端按错误分类给的固定说明）；其他错误码按前端的文案表选择。

## 5. Markdown 回答（`components/answer.tsx`）

- `react-markdown` 加 `remark-gfm`，不启用 `rehype-raw`：回答里的 HTML 标签按文字显示。
- 链接只在协议是 `http`/`https` 时渲染成 `<a target="_blank" rel="noreferrer noopener">`（`safeHref`），其他协议与相对地址只显示文字。
- 图片不加载，显示“［图片：替代文字］”。表格套一层可横向滚动的容器，单元格一律左对齐，不用 Markdown 的对齐标记。
- `frontend/vite.config.ts` 的 `codeSplitting.groups` 里有 `markdown` 分组，按包名列出 `react-markdown`、`remark-*`、`mdast-util-*`、
  `micromark*`、`hast-util-*`、`unist-util-*` 等间接依赖。升级或新增 Markdown 相关依赖时同步这个列表，否则新的间接依赖会落进首屏的 `vendor`。

## 6. 入口与名字

- `useStewardEntry()`：设置查询（`GET /me/steward/settings`）读到了才显示入口；读取失败、还没读到、或是 `403 steward_disabled`
  （包括之前读到过、重读时被拒）都不显示。名字取 `display_name`，空时是“管家”。侧栏、标签栏、面板标题与输入框提示都取这里，
  设置页保存后直接写设置缓存，不刷新就变。
- 侧栏“聆听”里管家排在艺人之后（`app/shell/nav-items.ts` 的 `steward` 项，`visibleNavGroups(role, {steward})`）：在别的页面上是
  开合面板的按钮（`aria-expanded`、`aria-controls="steward-dock"`，`id="steward-entry"`），在整页对话里是指向 `/steward` 的当前项链接。
  图标是 `nav-items.ts` 的 `StewardIcon`（lucide `Loader`，对应原型的八道光芒），侧栏与标签栏共用。
- 窄屏标签栏第五项在“艺人”与“我的”之间（`app/shell/tab-bar.tsx`），链接到 `/steward`，名字过长时省略。

## 7. 面板（`app/shell/app-shell.tsx`、`dock-store.ts`、`components/panel.tsx`）

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

## 8. 整页与 `/steward` 的去向（`components/steward-page.tsx`）

- 路由 `/steward` 与 `/steward/$conversationId`（`app/router.tsx`），两条路由之间切换时页面重新挂载。宽屏左边是会话列表（今天、本周、更早；
  正在回答的有标记；每项“更多”里改名、删除），右边是对话；<768px 两者分两屏，标题栏左侧“全部对话”切到列表。
- `/steward` 依次转到：面板记着的会话 → 列表里最近的会话 → 空白新对话。用户在应用里要的是空白新对话时设 `blankIntent`，不再转到最近的会话：
  “新对话”按钮与展开空白面板调用 `startBlankConversation`，标签栏调用 `preferBlankConversation`（面板记着会话时仍转到它），记着的会话 404 时同样。
  `blankIntent` 在打开某个会话后清掉，刷新后不存在，所以刷新或直接打开 `/steward` 会到最近的会话。
- 地址里的会话读取成功才记进面板状态；不存在或非法的 ID 显示“这个对话不存在或已经删除”，不记。删除正在回答的会话时确认框改成“停止回答”。
- 空白新对话发第一条消息时才创建会话（预选了模型就随后 `PATCH`；`PATCH` 失败时仍换到建好的会话，消息不发、文字留着、提示模型没有切换成功）。
- 成员开关关闭（设置查询是 `steward_disabled`，旧数据还在也一样）时整页只有说明。

## 9. 设置页 Agent 栏目（`settings/`、`app/routes/agent-settings-page.tsx`）

- `/settings/agent`，所有登录用户都有；`/settings/tokens` 经根路由的旧地址重定向到它（`settings-sections.ts` 的栏目名是“Agent”）。
- 名字：保存只提交 `{display_name}`，“恢复为‘管家’”提交空字符串，最多 20 个 Unicode 字符。
- 模型：地址与密钥只经“检测”保存（探测接口成功即保存），对话模型另有“保存”（只提交 `{model}`）。检测进行中（`useProbePending`，按 mutation key 判断）
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
- 访问令牌：C3 的 `AccessTokens` 原样放在这一栏。成员开关关闭时成员看到说明，名字与模型不显示，令牌一组保留（C3 合同：仍能查看与吊销，不能新建）。
- 成员段只对管理员渲染：成员开关（`/admin/steward/settings`）与“成员可以连接的内网地址”（`/admin/steward/egress-allowlist` 的列表、添加、移除）。

## 10. 测试

| 位置 | 内容 |
| --- | --- |
| `src/test/fake-steward.ts` | Vitest 的管家替身：`installStewardApi` 实现设置的读写（`PUT` 按后端 `mergeSettings` 的规则 1–3 清密钥、清检测结论、降级工具能力）与检测、会话增删改查、可控的运行流（`fakeSse` 逐个推送事件、`fail` 模拟断开，请求被中止时流随之出错）、取消、管理员接口；`nextRunResponse`、`probeResponse` 预置开流前的拒绝与检测失败 |
| `src/features/steward/**/*.test.ts(x)` | 解码器、归并、工具标签、Markdown 安全、run-store（发起、停止、断开、登出中止）、轮询间隔 |
| `src/app/steward.test.tsx`、`src/app/agent-settings.test.tsx`、`src/app/tokens.test.tsx` | 页面级：入口与名字、面板与整页、流式与结束说明、会话列表、`/steward` 去向、焦点与 Esc、Agent 栏目、令牌与旧地址 |
| `e2e/support/steward.ts`、`e2e/steward.spec.ts` | 浏览器冒烟：`installStewardMock(page)` 用 `page.route` 接管管家接口（优先于 `api-mock.ts` 的上下文拦截），发消息一次性返回整段 SSE；桌面面板到整页、390px 标签栏对话与切换会话、旧地址重定向 |

其他页面的测试没有装管家替身时，设置接口得到 404，入口不显示，不影响它们原有的断言。
