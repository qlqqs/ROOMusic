# 管家 C4 设计

## 后端改动（两处，交 Codex 子代理）

### 名字进系统提示词

`runtime.RunParams` 加 `DisplayName`；`httpapi` 发消息时从当前设置取。非空时在系统提示词末尾加一句
“用户称呼你为「<名字>」。”。名字已有 20 字上限并拒绝 NUL；拼接前再去掉换行与「」两个字符。空值时提示词不变。

### 继续运行

`POST /api/v1/me/steward/conversations/{id}/continue`，无请求体，响应与发消息相同（SSE）。

- 开始流之前的检查与发消息相同（4.5 节的 1–5），另加一条：会话的最后一条消息必须是 `user` 或 `tool`
  （即上一次运行没有给出最终回答），否则 `409 nothing_to_continue`。
- 运行流程与发消息相同，只是不追加用户消息、不改标题。实现上 `RunParams.Text` 为空且 `Continue` 为真时跳过追加。
- 路由、错误码、样本（`continue-nothing.json`）与规范第 4、10 节同步。

## 前端结构

```text
frontend/src/features/steward/
├── api/            decoders.ts、endpoints.ts（设置、探测、会话、管理员接口）、stream.ts（SSE 解析）
├── run/            run-store.ts（进行中的运行，应用级）、reduce.ts（事件 → 回合视图）
├── hooks/          use-steward-settings、use-conversations、use-conversation、use-run、use-steward-name
├── components/     thread、turn、steps、step、answer（Markdown）、run-ended、composer、model-menu、
│                   session-list、setup-guide、panel、access-tokens…（C3 已有）
├── settings/       agent-name、agent-model、probe-result、members-section、allowlist
└── tool-labels.ts  16 个工具的中文名称、图标、主要参数与数量的取法
frontend/src/app/routes/steward-page.tsx、agent-settings-page.tsx
frontend/src/app/shell/steward-dock.tsx
```

`features/steward` 按路由和面板按需加载（`React.lazy`），Markdown 依赖随它进入独立分包，不进首屏。

## 运行状态

- `run-store` 是模块级的外部 store（`useSyncExternalStore`），按会话 ID 存：已收到的事件归并出的“进行中回合”、
  `AbortController`、结束信息。发消息或继续时由 store 发起 `fetch` 并读流，与组件生命周期无关，所以切页面、
  面板与整页切换都不打断（R6）。
- 流解析沿用 `event-stream.ts` 的写法（`fetch` + `ReadableStream` 按行解析）；能共用的行解析提到 `shared/lib`。
  `401` 交给现有的会话边界处理。
- 归并规则（`reduce.ts`，纯函数）：`text` 追加到当前步的文字；`tool_started` 加一条进行中的条目；`tool_finished` 按 `call_id` 更新；
  `done` 记结束原因。同一个函数也用于把已落库的 `messages` 还原成回合，保证流式显示与刷新后的显示一致。
- 运行结束（`done`）后让该会话的查询失效并重新读取，以落库内容为准；列表查询同时失效（标题、状态、排序）。
- 停止：调用取消接口并中止 `fetch`。离开应用或刷新时不做额外处理（服务端在断开时自行取消）。
- 打开一个 `status: "running"` 但本页没有对应运行的会话（另一个标签页正在跑，或刚刷新）：显示“正在别处回答”，输入框不可发送，
  每 3 秒重读会话直到变回 `idle`。

## 显示

- 回合 = 一条用户消息 + 之后到下一条用户消息之前的全部助手与工具消息。回合内：过程条目（所有步骤的工具调用，按顺序）、
  各步的文字。中间步骤的文字（工具调用之前说的话）与最终回答都按顺序显示在过程下面；运行结束后过程收成摘要
  “查了 N 处资料，用时 X”，用时取条目 `duration_ms` 之和。
- 工具条目：`tool-labels.ts` 给出名称与图标；参数取输入里的 `q`，或结果里对象的标题（没有就不显示）；
  数量取输出的 `pagination.total`，没有分页时取 `items.length`，都没有则不显示。失败按 `error_code` 显示短语
  （`not_found` “没找到”、`permission_denied` “没有权限”、`cancelled` “已停止”、`interrupted` “没有完成”、其余“失败”）。
  未登记的工具名直接显示工具名。参数与标题来自曲库，按文本节点渲染。
- 结束说明（`run-ended`）：`provider_error` 显示分类后的中文说明、`status_code` 与 `message`，带“重试”“检查模型设置”；
  `step_limit`、`token_limit` 显示说明和“继续”（同一个继续接口）；`cancelled` 显示“已停止”；`internal_error` 与存储错误显示说明和“重试”。
  刷新后从落库内容判断：最后一条消息是 `user` 或 `tool` 时显示“这次回答没有完成”和“继续”。
- Markdown：`react-markdown` + `remark-gfm`，不启用 `rehype-raw`；`a` 只在 `http`/`https` 时渲染为链接并带
  `rel="noreferrer noopener" target="_blank"`；图片不加载，显示为替代文字。流式过程中每次增量整段重新解析；
  表格左对齐，样式用现有 token。实现前核对两个包的当前版本与新增的间接依赖数量，记进执行记录。
- 输入框：Enter 发送，Shift+Enter 换行，输入法组合期间不发送；运行中禁用发送、显示停止。草稿按会话存在内存里。

## 外壳与路由

- 路由 `/steward`（最近的会话或空白新对话）、`/steward/$conversationId`；`/settings/agent` 取代 `/settings/tokens`（后者重定向）。
- 面板状态（开关、当前会话 ID）放应用级 store；开关记在 `localStorage`。`app-shell` 在 ≥ 1100px 时给主内容右侧留出
  `--layout-steward-width`，768–1099px 时面板浮在内容上，< 768px 不渲染面板。整页路由下不显示面板，侧栏入口显示为当前项。
- 入口可见性：`GET /me/steward/settings` 返回 `403 steward_disabled` 时隐藏入口；这个查询同时提供名字与是否已配置。
  `nav-items.ts`、`tab-bar.tsx` 的入口标签取自名字。
- 新建会话要求已配置：未配置时面板与整页显示引导，不调用新建接口。空白新对话在发第一条消息时才创建会话。

## 设置页

- 名字：保存走 `PUT`（只带 `display_name`）；成功后更新设置查询，侧栏等处随之变化。
- 模型：地址与密钥输入后点“检测”调用探测接口（成功即保存）；失败时在检测结果区逐条显示 `probe_result.failures`，
  原设置不变。检测结果里“接口类型”“调用工具”的“修改”写 `overrides`。对话模型下拉取 `probe_result.models`，
  也可以手动输入。已存密钥显示末四位与“更换”“清除”；`key_needs_reentry` 时提示重新填写。
  `egress_denied` 时说明成员只能用公网地址，需要管理员放行。
- 成员段只对管理员渲染；后端接口本身要求管理员。

## 测试

- 归并函数、工具标签、Markdown 的链接与 HTML 处理：单元测试。
- 组件测试（`src/app/steward*.test.tsx`，沿用现有的假后端写法）：A1、A3–A10、A12；流用可控的假 SSE 响应。
- 契约测试：设置、探测、会话、管理员接口的样本改由解码器消费（A13）。
- 端到端：`e2e/` 加一条用假模型端点的冒烟用例（发消息、看到回答）；A2、A11 在这里或用浏览器手动核对，结果如实记录。
- 后端两处改动由另一个子代理补测试：系统提示词含名字与过滤字符；继续接口的前置检查、不追加用户消息、样本。

## 取舍

- 运行放在模块级 store 而不是路由组件里：换来切页面不中断；代价是多一处组件之外的状态，登出时要清掉并中止。
- 不做断线接回：需要服务端把运行与请求解耦，属于 C10 后台任务的范围。
- 工具条目不展开原始 JSON：界面干净；调试工具设计时用 MCP 客户端或数据库。
