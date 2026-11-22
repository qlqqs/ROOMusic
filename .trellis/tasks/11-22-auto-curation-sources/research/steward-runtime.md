# Research: 管家运行时离“无人值守的后台整理”差多少

- **Query**: 现有 Music Steward（M6）运行时的结构、模式、方案、独立审查、触发方式、凭据归属、用量控制、只读工具与 MCP 入口；父任务里与“自动、后台、批量整理、外部元数据源”有关的既有决定
- **Scope**: internal（代码、规范、ADR、归档任务）
- **Date**: 2026-11-22
- **基线**: 分支 `main`，提交 `3e8714b4`。`docs/architecture/adr/0005-steward-data-and-egress.md` 与 `docs/architecture/core-1-roadmap.md` 有未提交改动（只把“尚未实施”改成“已随 M6 实施”），本文按工作区内容引用行号。
- 路径均相对仓库根；`steward/...` 是 `backend/internal/steward/...` 的简写。未运行任何测试。

## 结论（10 行以内）

1. 运行已与 HTTP 请求解耦（C10），但**启动入口只有四个已登录用户的 HTTP 接口**；代码里没有定时、事件或系统发起的管家运行入口。
2. 一次运行必须属于“某个用户的某个会话”，并且**依赖该用户仍然有效的登录会话**：每 60 秒复查，会话过期或登出即取消运行。
3. 模型端点与密钥按用户存放、按用户加密绑定；没有实例级或共享密钥。父任务明确排除了“管理员提供的共享模型密钥与用量上限”。
4. `steward` 模式已实现“提方案 → 独立审查 → 批准的自动执行”，审查失败关闭；但方案只能由会话里的模型工具调用产生。
5. 审查策略第 1 版把不可撤销项、超过 20 项的整份方案、未登记证据读取方式的工具、`approved+high` 一律转人工；能自动执行的只有 5 个发行级可撤销工具。
6. 审查员看到的只有本地目录摘要和字段前后值，**看不到外部资料（MusicBrainz 等）**和主代理的依据说明。
7. 用量控制只有每次运行 24 步、40 万 token、每用户 3 个并发运行；没有按用户或全局的统计汇总与预算上限，审查调用的用量不计入运行上限。
8. MusicBrainz 只有“按查询词搜索”和“按 MBID 读取”两种请求，全进程每秒 1 次，无缓存，无重试。
9. 没有读取艺人候选列表的管家工具，也没有读歌词的工具（均查不到）。
10. C10 的 PRD 把“定时任务、无人值守的周期性整理”列为不做；C6 把“一次对一批专辑的批量操作类型”列为不做。

---

## 1. 整体结构

### 1.1 子包职责

`backend/internal/steward/` 下实际有 10 个子包（父任务 `design.md:22-29` 只列了 5 个）：

| 子包 | 职责 | 依据 |
| --- | --- | --- |
| `tool/` | 工具合同（名称、schema、最低身份、影响范围、可撤销、输出可信度）、启动时注册表、逐次鉴权的调用入口、64 KiB 输出上限 | `steward/tool/definition.go:95-126`、`steward/tool/registry.go:13-14,87-127` |
| `provider/` | 模型协议适配（OpenAI 兼容、Messages、Responses）、流解析、探测、错误分类 | `steward/provider/openai.go`、`messages.go`、`responses.go`、`probe.go`、`errors.go` |
| `runtime/` | 多步循环、方案与确认、独立审查、会话/消息/工具调用持久化、历史摘要、进程内运行协调器（Supervisor） | `steward/runtime/run.go`、`run_tools.go`、`run_review.go`、`review.go`、`plan_store.go`、`supervisor.go`、`summary.go` |
| `httpapi/` | 设置、探测、会话、发消息/继续/确认/重审、SSE 接回、令牌与记忆管理、管理员开关与放行名单 | `steward/httpapi/handlers.go:51-79` |
| `mcp/` | 无状态 MCP HTTP 入口，按访问令牌暴露同一注册表 | `steward/mcp/handler.go:43-52`、`steward/mcp/server.go:25-68` |
| `egress/` | 唯一出站客户端与地址策略（PublicOnly / PublicOrAllowlist / Unrestricted） | `steward/egress/client.go:15-25,44` |
| `secret/` | 实例密钥文件与按用户、用途绑定的 AES-256-GCM | `steward/secret/secret.go:29-60,88-93`、`steward/secret/key.go:10-53` |
| `token/` | MCP 个人访问令牌（只读/读写） | `steward/token/token.go:15-21`、`steward/token/store.go:41,114` |
| `memory/` | 按用户的记忆存储与三个记忆工具（每人 50 条、每条 500 字） | `steward/memory/store.go:15-16`、`steward/memory/tools.go:28-34` |
| `web/` | 搜索（SearXNG、Tavily）、网页抓取、MusicBrainz 与封面清单、限速 | `steward/web/service.go`、`musicbrainz.go`、`limiter.go`、`tools.go` |

业务工具不在 `steward/` 里，由各业务包的 `stewardtools` 子包提供，在 `backend/internal/app/steward.go:91-126` 统一注册。

### 1.2 一次运行的流程

1. HTTP 入口 `sendMessage`（`steward/httpapi/events.go:27-50`）校验文本与页面上下文后调用 `startRun`。
2. `startRun`（`steward/httpapi/events.go:81-169`）：读会话 → 会话忙则拒绝（125-128）→ `configured()` 读本人设置并解密密钥（129，实现在 `steward/httpapi/settings.go:281-327`）→ 按身份构造出站客户端（133）→ 端点预检（144）→ 构造 Provider（148）→ `supervisor.Start(...)`（157-162）→ 把本次运行的订阅写成 SSE（168）。
3. `Supervisor.Start`（`steward/runtime/supervisor.go:80-232`）：检查协调器未关闭、同会话无运行中、该用户运行中数量 < 3（86-100）；用 `context.WithCancel(context.WithoutCancel(request))` 派生运行上下文（101）；另起协程先跑一次 `Check`，再调用 `runner.Run`（154-165）。
4. `Runner.Run`（`steward/runtime/run.go:46-234`）：`AcquireConversation` 把会话从 `idle` 原子置为 `running`（60）；读会话、摘要、记忆（114-127）；按会话取模式并写入 `Caller.Mode`（128-136）；之后进入 `state.run`。
5. `state.run`（`steward/runtime/run.go:267-365`）：追加用户消息（`prepare`，236-265）→ 取当前用户可见的工具定义（275-282）→ 循环：调用模型（291）→ 助手消息与工具调用落库（323-332）→ `executeCalls` 串行执行工具（338）→ 没有工具请求则 `completed`（360-362）。
6. `executeCalls`（`steward/runtime/run_tools.go:13-127`）：只读与个人工具直接 `Invoke`；`shared` 工具在未获直接执行权时只做 `PreviewCall` 并收集为方案项（33-39）；本步结束后 `CreatePlan`（113），`steward` 模式转 `reviewPlan`（120-122），其余模式发 `plan` 事件并以 `awaiting_confirmation` 结束（123-124）。
7. 收尾（`steward/runtime/run.go:76-113`）：释放会话、记 `steward.run.finished` 日志（步数、耗时、输入输出 token）、发 `done` 事件。

结束原因的全集：`completed`、`step_limit`、`token_limit`、`cancelled`、`provider_error`、`awaiting_confirmation`、`review_unavailable`、`internal_error`（`steward/runtime/run_types.go:84-99`）。

### 1.3 运行与 HTTP 请求的解耦（C10）

- 运行上下文脱离请求取消：`steward/runtime/supervisor.go:101`；只随应用生命周期取消（102）。
- 断开连接只退订，不取消运行：`Subscription.Close`（`steward/runtime/supervisor.go:58,279-286`）；SSE 写失败只返回（`steward/httpapi/events.go:254-276`）。
- 接回：`GET /api/v1/me/steward/conversations/{id}/events`，游标取 `Last-Event-ID` 或 `after`（`steward/httpapi/handlers.go:72`、`steward/httpapi/events.go:216-239`）。
- 停止：`POST .../cancel` → `Supervisor.Cancel`（`steward/httpapi/conversations.go:277-288`、`steward/runtime/supervisor.go:295-304`）。
- 决定来源：C10 PRD D3“所有运行都改为不依赖请求…不区分前台后台两种运行”（`.trellis/tasks/archive/2026-11/11-16-steward-c10-memory/prd.md:26`）。

### 1.4 运行状态存在哪

| 状态 | 位置 | 依据 |
| --- | --- | --- |
| 会话是否在运行 | `steward_conversations.status`（`idle`/`running`） | `backend/migrations/0051_steward_runtime.sql`（`steward_conversations` 表）、`steward/runtime/conversations.go:192-212` |
| 消息、工具调用及结果、每条消息的 token | `steward_messages`、`steward_tool_calls` | `backend/migrations/0051_steward_runtime.sql` |
| 方案与方案项 | `steward_plans`、`steward_plan_items` | `backend/migrations/0054_steward_plans.sql` |
| 审查记录 | `steward_reviews` | `backend/migrations/0055_steward_reviews.sql` |
| 运行结束与未读 | `last_run_finished_at`、`last_read_at` | `steward/runtime/conversations.go:56-57,220-244` |
| 运行中的协程、取消函数、事件日志、订阅 | **仅进程内存**：`Supervisor.runs`；事件日志上限 4 MiB，结束后保留 60 秒 | `steward/runtime/supervisor.go:13-18,32-56,195,205-214` |

- 没有运行（run）表：一次运行没有自己的持久化标识，只体现为会话的 `running` 状态。
- 服务重启不恢复运行：启动时把遗留的 `running` 会话复位、未完成的工具调用标为 `interrupted`（`steward/runtime/conversations.go:246-265`、`backend/internal/app/steward.go:66-83`）；C10 D4“不做跨重启恢复”（C10 `prd.md:27`）。
- 关闭时取消全部运行并最多等 10 秒收尾（`steward/runtime/supervisor.go:17,324-344`）。

## 2. 三种模式的行为差异

模式记在会话上（`steward_conversations.mode`，默认 `assistant`，见迁移 0051），新建与修改时校验取值（`steward/runtime/conversations.go:60-72,268-305`）；切换模式要求会话空闲且没有待处理方案（285-299）。

| 判定点 | assistant | steward | operator | 代码位置 |
| --- | --- | --- | --- | --- |
| `shared` 工具是否直接执行 | 仅当工具可撤销且在本会话免确认名单里 | 从不 | 总是 | `steward/runtime/run.go:473-475` |
| 未直接执行的 `shared` 调用 | 进方案，等用户确认 | 进方案，交独立审查 | 不适用 | `steward/runtime/run_tools.go:32-39,105-125` |
| 免确认名单（`auto_approve_tools`） | 可在确认时登记，仅限可撤销工具 | 提交即拒绝 | 不适用 | `steward/runtime/plans.go:111-113`、`steward/runtime/plan_store.go:143-151,199-211` |
| 重新审查接口 | 409 `review_not_applicable` | 可用 | 409 | `steward/httpapi/events.go:104-108`、`steward/runtime/run.go:137-140` |
| 每步后是否从库里重读历史 | 仅在触发摘要条件时 | 每步都重读 | 同 assistant | `steward/runtime/run.go:342-353` |
| 系统提示词附加语 | “等用户在界面上确认后才执行” | “系统会自动…交给独立审查…批准的项自动执行” | “鉴权后直接执行，不询问用户” | `steward/runtime/run_content.go:233-241` |
| 操作记录里的来源 | `origin.mode` 取会话模式；steward 另带 `review_id`、`review_decision` | 同左 | 同左 | `backend/internal/catalog/governance/stewardtools/tools.go:94-95`、`backend/internal/app/steward.go:113-121`、`backend/internal/operations/operations.go:131-139` |

三种模式共同的部分：

- 工具列表不随模式变化，只按身份与可用性过滤（`steward/runtime/run.go:275-282`、`steward/tool/registry.go:70-83`）。
- 只读与个人范围的工具在三种模式下都直接执行（`steward/runtime/run_tools.go:40-41`）。
- 注册表的最后一道门：`shared` 工具在 `Caller.SharedExecution` 为假时返回 `approval_required`（`steward/tool/registry.go:96-101`）；`SharedExecution` 只由运行时与 MCP 入口设置（`steward/tool/definition.go:56-57`）。
- 模式不改变权限：所有曲库写工具的最低身份是管理员（`backend/internal/catalog/governance/stewardtools/tools.go:80`），每次预览与执行前重读调用者身份（`backend/internal/app/steward.go:171-219`）。

## 3. 修改方案（plan）

### 3.1 数据结构

- `Plan`：`plan_id`、`digest`、`expires_at`、`items`、`status`、最近一次 `review`（`steward/runtime/plans.go:22-29`）。
- `PlanItem`：对外有 `id`、`tool`、`target`、`changes`（字段前后值）、`revertible`、`basis`（模型给的依据）、`decision`、`result`、`operation_id`、`call_id`；执行输入、幂等键、审查来源只留在后端（`steward/runtime/plans.go:32-53`）。
- `PlanDecision`：`digest`、`accepted_item_ids`、`auto_approve_tools`，只能由登录用户的确认接口创建（`steward/runtime/plans.go:55-61`、`steward/httpapi/plans.go:11-34`）。
- 表结构（`backend/migrations/0054_steward_plans.sql`、`0055_steward_reviews.sql`）：
  - `steward_plans.status`：`pending`、`applied`、`declined`、`expired`、`superseded`。
  - `steward_plan_items.decision`：`pending`、`accepted`、`declined`、`rejected_by_review`。
  - `steward_plans.conversation_id`、`user_id` 均为 NOT NULL；`steward_plan_items.tool_call_id` NOT NULL 且唯一，指向会话消息里的工具调用。
  - 每个会话同一时刻最多一份 `pending` 方案（部分唯一索引 `steward_plans_pending_idx`）。
- 方案只有一个产生途径：同一步模型输出里未直接执行的 `shared` 工具调用合成一份方案（`steward/runtime/run_tools.go:105-113`、`steward/runtime/plan_store.go:16-53`）。

### 3.2 digest

`planDigest`（`steward/runtime/plans.go:71-97`）：对每项的 `{tool_name, 规范化后的 input, position}` 数组取 SHA-256 十六进制。确认时必须带回同一摘要（`steward/runtime/plans.go:127-129`、`steward/runtime/plan_store.go:176-178`）；审查批准绑定同一摘要（`steward/runtime/run_review.go:217-223`）。

### 3.3 过期

- 有效期 24 小时，从创建时算（`steward/runtime/plan_store.go:21`）。
- 过期的收尾是惰性的：读会话详情或确认时才处理（`steward/runtime/conversations.go:125`、`steward/runtime/plan_store.go:167-175`）；没有后台清理任务。
- 收尾内容：未出结果的项写 `not_executed` 工具结果，方案状态改 `expired`（`steward/runtime/plan_store.go:305-354`）。审查过的方案在收尾前先按幂等键对账，已提交的操作恢复为 `accepted`（325-340）。

### 3.4 作废

- 用户在同一会话发新消息时，未处理的 `pending` 方案改为 `superseded`，项记 `not_executed`（`steward/runtime/messages.go:117-119`）。
- 用户不选的项记 `declined_by_user`；审查拒绝的项记 `rejected_by_review` 并把理由回给主模型（`steward/runtime/run_tools.go:236-242`）。

### 3.5 方案项如何映射到受治理操作

- 工具名 → 操作类型、指纹、执行器：`backend/internal/catalog/governance/stewardtools/commands.go:27-118`。
- 执行统一经 `operations.Run`，`ActorID` 取调用者用户 ID，幂等键取工具调用自带的键，来源写 `Origin{Source, ConversationID, PlanID, Mode, TokenID}`（`backend/internal/catalog/governance/stewardtools/tools.go:89-100`）。
- 幂等键在工具调用落库时生成（`steward_tool_calls.idempotency_key`，迁移 0054），确认后执行、中断后继续、审查后执行都沿用同一个键（`steward/runtime/run_tools.go:206-212`）。
- `operations.actor_id` 是 `NOT NULL REFERENCES users(id)`（`backend/migrations/0017_governed_operations.sql:9`）；没有查到“系统操作者”的写法。

### 3.6 写工具清单（`shared` 范围，全部要求管理员）

曲库类（`backend/internal/catalog/governance/stewardtools/tools.go:60-68`；操作类型见 `backend/internal/operations/operations.go:37-46`）：

| 工具名 | 受治理操作 | 可撤销 | 已登记审查证据 |
| --- | --- | --- | --- |
| `correct_release` | `release_correction`（字段限 `title`、`album_artist`、`year`、`genre`、`label`、`catalog_number`、`edition`、`barcode`，`tools.go:55`） | 是 | 是 |
| `change_release_group` | `release_group_change`（join / detach） | 是 | 是 |
| `set_default_release` | `release_group_default` | 是 | 是 |
| `review_release_issue` | `release_issue_review`（review / unreview，需 `get_release_issues` 返回的 basis 令牌） | 是 | 是 |
| `claim_release` | `release_claim`（需 `preview_release_claim` 的 preview 令牌） | 是 | 是 |
| `merge_artists` | `artist_merge` | 否 | 否 |
| `split_artist` | `artist_split` | 否 | 否 |
| `rename_artist` | `artist_rename` | 否 | 否 |
| `ignore_artist_candidate` | `artist_candidate_ignore` | 否 | 否 |
| `revert_operation` | `revert`（撤销一条已登记类型的操作；定义里未设 `Revertible`，即否） | 否 | 否 |

- “已登记审查证据”的名单：`backend/internal/catalog/stewardtools/review.go:41-48`。
- `revert_operation` 定义：`backend/internal/operations/stewardtools/revert.go:41`。
- 清单里没有曲目级字段、署名、封面或外部标识（MBID）的写工具（已核对含 `tool.Definition` 定义的全部 17 个文件）。

配置类（全部 `Revertible: false`）：

| 工具名 | 位置 |
| --- | --- |
| `add_library_root`、`set_library_root_enabled`、`add_root_exclusion`、`remove_root_exclusion`、`start_scan`、`cancel_scan`、`set_scan_schedule` | `backend/internal/library/stewardtools/config.go:51-57,77` |
| `start_backup`、`set_backup_retention` | `backend/internal/backup/stewardtools/tools.go:39,50,63` |
| `set_member_disabled`、`revoke_member_sessions` | `backend/internal/identity/stewardtools/tools.go:49-58` |

验收记录写明其中多数配置写入只记一条带来源的日志、不进操作记录页（`.trellis/tasks/archive/2026-11/11-15-music-steward-m6/acceptance.md:338`）。

个人范围的写工具（`personal`，三种模式下都直接执行，不进方案）：播放与队列（`backend/internal/zones/stewardtools/tools.go:145-229`）、分区设置（`backend/internal/zones/stewardtools/settings.go:34`）、PEQ 预设套用（`backend/internal/dspsettings/stewardtools/tools.go:27`）、收藏与歌单（`backend/internal/personal/stewardtools/tools.go:128-137`）、删除播放历史（`backend/internal/listening/stewardtools/tools.go:49`）、记忆（`steward/memory/tools.go:28-31`）。

## 4. 独立审查（C7）

规范全文：`.trellis/spec/backend/steward-review-contracts.md`。

### 4.1 发给审查模型的内容

- 一次独立调用：系统提示词是常量 `ReviewSystemPrompt`（`steward/runtime/review.go:21-25`），只有一条 user 消息，正文是 JSON `{items, evidence}`（`steward/runtime/review.go:188-202`）。
- `items`：每项只有 `item_id`、`tool`、`revertible`（`steward/runtime/review.go:131-135,189-192`）。
- `evidence`：每项 `item_id`、`facts`、`preview`（`steward/runtime/review.go:126-130`）。
  - `facts.targets`：后端只读接口生成的目标摘要——发行的当前有效字段值、所在组、各字段的本地标签概况（来源、置信度、去重候选数、是否一致）、未处理问题数量；专辑组的默认版本、成员、关注数（`backend/internal/catalog/stewardtools/review.go:51-139`）。
  - `facts.recent_operation_count`：目标最近一页操作记录条数，最多 20（`backend/internal/app/steward.go:142-148`）。
  - `preview`：重新调用工具 `Preview` 得到的字段前后值（`steward/runtime/review.go:177-182`）。
- 明确不发：原始工具参数、模型的 `basis`、预览说明、主对话历史、原始候选文本、歌词、路径、页面上下文、记忆（`steward/runtime/review.go:36-46,181`；ADR 0005 第 4 节第 4 条，`docs/architecture/adr/0005-steward-data-and-egress.md:80`；第 6 节 `:152`）。
- 证据里没有任何外部资料字段：`ReadReviewTargets` 只读本地目录、证据与问题（`backend/internal/catalog/stewardtools/review.go:51-139`）。C7 的 PRD 把“审查时让审查模型自己调用工具查资料”列为不做（`.trellis/tasks/archive/2026-11/11-16-steward-c7-review/prd.md:54`）。
- 调用参数：只提供并强制调用 `submit_review`，60 秒超时，输出上限 8192 token，不重试（`steward/runtime/review.go:200-202`）。

### 4.2 结论取值

- `decision`：`approved`、`rejected`、`needs_human_confirmation`；`risk`：`low`、`medium`、`high`；每项必须有非空 `reasons`（schema 在 `steward/runtime/review.go:48`，校验在 255-295）。
- `source` 由后端填：`model` 或 `policy`（`steward/runtime/review.go:86-92,292`）。
- 审查整体状态：`completed` 或 `unavailable`（`steward/runtime/review.go:93-104`）。

### 4.3 policy 规则

`ReviewPolicyVersion = 1`（`steward/runtime/review.go:20`），规则文本是常量 `ReviewPolicyRules`（26-33）。不经模型判断、直接转人工的情况：

| 规则 | 实现位置 |
| --- | --- |
| 方案总项数超过 20：全部未处理项转人工，不调用审查模型 | `steward/runtime/review.go:155-166` |
| 工具登记为不可撤销：转人工，不送审 | `steward/runtime/review.go:159-161` |
| 工具没有登记后端证据读取方式：转人工，不送审 | `steward/runtime/run_review.go:57-59` |
| 审查返回 `approved` 且 `risk=high`：降为转人工 | `steward/runtime/run_review.go:93-97` |
| 不可撤销与项数规则在模型返回后再判一次 | `steward/runtime/run_review.go:91-92` |

policy 产生的项固定为 `decision=needs_human_confirmation`、`risk=high`、`source=policy`（`steward/runtime/review.go:165`）。

`policy_version` 是什么：一个整数常量，随每条审查记录存入 `steward_reviews.policy_version`（迁移 0055）；执行前核对记录里的版本等于当前常量，不等则按审查不可用处理（`steward/runtime/run_review.go:200,221`）。C7 D3 写明用户要改规则时作为“策略第 2 版”（C7 `prd.md:26`）。

批准的绑定与执行（`steward/runtime/run_review.go:37,126-180,199-273`）：

- 批准绑定方案摘要、证据摘要、策略版本和 15 分钟有效期。
- 批准项逐项执行，每项执行前重读方案、审查记录和该项证据，任何一处变化即停止并以 `review_unavailable` 结束。
- 批准只在当前这次运行内有效；跨请求必须重新审查或人工确认（规范 `steward-review-contracts.md` 第 3 节“调用、校验、执行绑定”第 5、6 条）。
- 转人工的项保持 `pending`，随后发 `plan` 事件，以 `awaiting_confirmation` 结束（`steward/runtime/run_review.go:136-139,188-194`）。

### 4.4 审查模型与对话模型相同时

- 后端不做区分：`ReviewModel` 为空时直接用对话模型（`steward/runtime/run_review.go:33-36`）。
- 审查调用与对话共用同一个 Provider，也就是同一用户的同一端点和密钥，只有模型名可以不同（`steward/runtime/run_review.go:77`、`steward/httpapi/events.go:160`）。C7 的 PRD 把“审查模型用另一个端点”列为不做（C7 `prd.md:54`）。
- 仅有界面提示：“审查与对话用的是同一个模型，独立性有限；可以在设置里另选审查模型”（`frontend/src/features/steward/components/review-result.tsx:195`）。
- ADR 0005 第 7 节把它登记为已接受的剩余风险（`docs/architecture/adr/0005-steward-data-and-egress.md:162`）。

### 4.5 审查不可用时

- 触发条件：模型错误、60 秒超时、非唯一 `submit_review`、schema 不合、项集合不一致、输出截断、证据读取的业务失败（`steward/runtime/review.go:223-241,255-295`、`steward/runtime/run_review.go:61-68`）。
- 行为：保存一条 `status=unavailable`、`items=[]` 的审查记录，发 `review_finished`，运行以 `review_unavailable` 结束；未处理项保持 `pending`，不执行，不降级为直接执行（`steward/runtime/run_review.go:100-103,123-125`）。
- 之后只有两条路：用户调用重审接口 `POST .../plans/{plan_id}/review`（`steward/httpapi/handlers.go:69`、`steward/runtime/run.go:137-175`），或用户人工确认；都不做则方案 24 小时后过期。没有自动重试。
- 审查期间取消：以 `cancelled` 结束，不保存审查（`steward/runtime/run_review.go:82-84,107-109`）。

## 5. 触发方式

- `Supervisor.Start` 在全仓库只有一个调用点：`steward/httpapi/events.go:157`。
- 到达它的入口只有四个，全部在 `/api/v1/me/steward/conversations/{id}/...` 下（`steward/httpapi/handlers.go:67-70`）：
  - `POST .../messages`（发消息）
  - `POST .../plans/{plan_id}/decision`（确认方案）
  - `POST .../plans/{plan_id}/review`（重新审查）
  - `POST .../continue`（继续）
- 四个入口都经 `route`：写方法要求同源，必须通过登录会话认证，成员还要过成员开关（`steward/httpapi/handlers.go:86-113`）。
- 定时、事件或系统发起的管家运行入口：**查不到**。`steward/` 下没有调度器；应用里现有的定时机制是每日定时扫描（`backend/internal/library/scan_scheduler.go:42-92`，由 `backend/internal/app/app.go:131-139` 启动）和备份调度（`backend/internal/backup/scheduler.go`），两者都不调用管家。扫描结束后也没有通知管家的钩子。
- 运行必须属于某个用户的某个会话：
  - `RunParams` 必带 `Caller`（含 `identity.User`）与 `ConversationID`（`steward/runtime/run_types.go:38-41`）。
  - Supervisor 以会话 ID 为键登记运行、按用户 ID 计并发（`steward/runtime/supervisor.go:86-104`）。
  - 存储层所有读写都带 `userID` 与 `conversationID`；会话、方案的 `user_id` 为 NOT NULL（迁移 0051、0054）。
- 运行还依赖该用户有效的登录会话：
  - `RunParams.Check` 在运行开始前执行一次，之后每 60 秒执行一次（`steward/runtime/supervisor.go:119-158`）。
  - `Check` 由 `runCredentialCheck` 构造，携带发起请求时的会话 Cookie；没有 Cookie、会话已撤销或已过期都判为失效（`steward/httpapi/events.go:171-207`、`backend/internal/app/steward.go:153-164`）。
  - 失效即取消该用户的全部运行（`steward/runtime/supervisor.go:137-139`）。规范写明“会话自然过期…每 60 秒复查 → CancelUser”（`.trellis/spec/backend/steward-runtime-contracts.md` 的“取消钩子”表）。
  - 同一检查还覆盖：成员开关被关闭、端点或密钥被改（`steward/httpapi/events.go:189-204`）。
- 另一个不经运行时的入口是 MCP（见第 9 节）：模型在客户端一侧，服务端只执行单次工具调用。

## 6. 模型凭据的归属

### 6.1 按用户，不是全局

- 设置表 `steward_user_settings` 以 `user_id` 为主键，存端点、密钥密文、协议、模型、审查模型、搜索来源与搜索密钥（迁移 0051、0055、0056；`steward/runtime/settings.go:86-97`）。
- 实例级设置只有成员开关 `steward_instance_settings.members_enabled` 和出站放行名单 `steward_egress_allowlist`（迁移 0051）。没有实例级的端点或密钥字段。
- 密钥用实例密钥文件加密，并把用户 ID 与用途作为关联数据绑定；密文挪到别的用户名下解不开（`steward/secret/secret.go:29-60,88-93`）。运行前按 `user.ID` 解密（`steward/httpapi/settings.go:297-303`）。
- 搜索来源与搜索密钥同样按用户（`steward/web/service.go:25-28`、`steward/web/tools.go:26`）。
- 父任务决策 S15：“模型端点与密钥由每个用户自己配置，只供本人使用，没有管理员提供的共享密钥；不做用量上限”（`.trellis/tasks/archive/2026-11/11-15-music-steward-m6/prd.md:51`）；“不在范围内”再次列出“管理员提供的共享模型密钥与用量上限”（`prd.md:127`）。

### 6.2 系统发起的后台运行可以用谁的凭据

- 代码层面：没有任何不带具体用户就能取得模型凭据的路径；`configured()` 与出站客户端都以 `identity.User` 为参数（`steward/httpapi/settings.go:281`、`steward/httpapi/outbound.go:20`）。
- ADR 0005 的相关条文：
  - “数据只发往当前用户自己配置的模型端点，以及该用户选择的搜索来源”（`docs/architecture/adr/0005-steward-data-and-egress.md:48`）。
  - “请求只带该用户自己的密钥。一个用户的请求不会带上另一个用户的密钥”（`:122`）。
  - “每个用户的密钥只供本人的请求使用，管理员没有读取他人密钥的接口”（`:135`）。
- ADR 与父任务文档都没有出现“系统发起的运行”这一情形，也没有规定这种运行算谁的“本人的请求”——**文档里查不到答案**。

### 6.3 ADR 0005 中会约束后台整理的硬性规则

发给模型的数据（第 3 节，`:46-63`）：

- 可以发：专辑、版本、曲目、艺人、证据、待关注问题、扫描与操作记录，范围等于该用户在界面上能看到的内容（`:52`）。
- 文件路径只发音乐目录以下的相对部分（`:54`）；歌词只在专门读歌词的工具里发（`:55`）；音频字节与封面图片不发（`:56`）。
- 其他成员的歌单、收藏、历史、对话、记忆不发（`:57`）；密钥、令牌等不发（`:58`）。
- 发给搜索服务和音乐数据源的只有查询词（`:61`）。
- 审查子代理拿到的是方案和后端生成的证据摘要，受同一张表约束（`:62`）。
- 规则在工具输出层实现，不靠提示词（`:63`）。

不可信文本（第 4 节，`:65-85`）：

- 标签、歌词、文件名、扫描出的字符串、搜索结果、网页正文、**音乐数据源的返回值**都是不可信文本（`:67`）。
- 含不可信文本的工具结果由运行时包固定来源标记（`:71-73`；实现 `steward/runtime/run_content.go:182-199`）。
- 来源标记不是安全边界；边界是后端鉴权、“`assistant` 须用户确认 / `steward` 须审查子代理批准且后端只认审查调用的返回值”、改动经 `operations.Run`（`:74-78`）。
- 方案里每一项标明依据来自本地证据还是外部资料（`:79`；工具参数 `basis.source` 取 `local`/`external`/`mixed`，`backend/internal/catalog/governance/stewardtools/tools.go:20`）。
- 审查输入不含主对话历史和原始不可信文本，摘要里引用的外部文字同样带来源标记（`:80`）。
- `operator` 不询问、不审查，读到外部内容后的写操作直接执行，作为已接受风险（`:82-83`，D4）。
- MCP 读写令牌的写操作没有服务端确认（`:84`）。

出站地址（第 5 节，`:87-123`）：

- 所有出站请求经同一个出站客户端（`:89`）。
- 用户自己填的模型端点与 SearXNG：管理员不限，成员只能公网或放行名单（`:95`）。
- 程序固定地址的商业搜索与音乐数据源：管理员、成员都允许（`:96`）。
- 网页抓取对所有人只允许公网地址，不适用放行名单（`:97,106`）；只发 GET、不带实例标识、只 80/443、正文最多读 2 MiB、给模型最多 100 KiB、最多 5 次重定向、总超时 20 秒（`:111-116`）。
- 模型端点请求不跟随重定向（`:103`）。
- MusicBrainz 的 User-Agent 写明应用名、版本、项目地址，不含实例信息（`:120`，D7）。

对话与日志（第 6 节，`:148-154`）：对话与记忆按用户隔离、明文存库；日志不记消息正文和工具输入输出，只记会话标识、工具名、耗时、用量和结束原因。

## 7. 用量控制

| 项目 | 现状 | 依据 |
| --- | --- | --- |
| 每次运行的模型请求数上限 | 24 | `steward/runtime/run.go:18,283,364` |
| 每次运行的 token 上限 | 输入加输出累计 400,000；只累计输入、输出都已知的步骤，缺失不估算 | `steward/runtime/run.go:19,284-286,335-337,451-456` |
| 每次模型请求的输出上限 | 8,192 token | `steward/runtime/run.go:20,292` |
| 单次模型请求总时限 | 5 分钟 | `steward/httpapi/outbound.go:21` |
| 每步或每次运行的工具调用数上限 | 查不到；一步里的调用全部串行执行 | `steward/runtime/run_tools.go:16` |
| 单个工具结果大小 | 64 KiB，超出截断 | `steward/tool/registry.go:13-14,141-163` |
| 并发 | 每用户最多 3 个运行中的会话；同一会话不并发；没有跨用户的全局上限 | `steward/runtime/supervisor.go:14,86-100` |
| 审查 | 每次 60 秒超时；方案超过 20 项不送审 | `steward/runtime/review.go:157,200` |
| 历史摘要 | 上一次请求输入超过 120,000 token 时触发，保留最近 6 个回合 | `steward/runtime/summary.go:15-20` |
| 外部请求（每用户每分钟） | 网页搜索 30、网页抓取 30、音乐元数据 20；计数在进程内存，重启清零 | `steward/web/limiter.go:9-14,29`、`steward/web/tools.go:92-108` |
| MusicBrainz 全进程 | 相邻请求间隔至少 1 秒，排队超过 10 秒返回 `rate_limited` | `steward/web/limiter.go:61-109` |

token 统计的落点：

- 每条助手消息的 `input_tokens`、`output_tokens` 存在 `steward_messages`（迁移 0051）。
- 每次审查的用量存在 `steward_reviews.usage`（迁移 0055）。
- 日志事件 `steward.model.usage`（每步）、`steward.run.finished`（每次运行的步数与 token 合计）、`steward.review.finished`（`steward/runtime/run.go:103-109,457-458`、`steward/runtime/run_review.go:121`）。
- 审查调用的用量不计入 40 万的运行上限：`reviewPlan` 只把用量写进审查记录，没有累加到运行计数（`steward/runtime/run_review.go:77`；`recordUsage` 的调用点只有 `steward/runtime/run.go:293` 与 `steward/runtime/summary.go:105`）。
- 按用户、按天或全局的用量汇总：查不到（在 `steward/` 下检索 `SUM(input_tokens`、`budget`、`quota` 无相关结果）。
- 预算上限：没有。父任务 S15 写明“不做用量上限”（`prd.md:51`）。
- 模型端点返回限流时归类为 `rate_limit` 错误并结束运行，不重试（`steward/provider/errors.go:29-30,103-104`、`steward/runtime/run.go:316,462-471`）。

## 8. 只读工具清单

以下是全部登记为 `read` 的工具（外加 `personal` 范围但标为只读的 `list_memories`）。输出都标为不可信，除非另注。

曲库与元数据核对（`backend/internal/catalog/stewardtools/tools.go:79-129`）：

| 工具 | 最低身份 | 用途 |
| --- | --- | --- |
| `search_library` | 成员 | 按关键词搜专辑、曲目或艺人 |
| `list_albums` | 成员 | 浏览专辑组；`attention=required` 只列待关注项 |
| `get_album` | 成员 | 专辑组详情与各版本（最多前 100 个） |
| `get_release` | 成员 | 版本详情、署名、证据摘要、曲目；带 `revision`；不含歌词 |
| `get_track` | 成员 | 单曲详情（曲序、音频事实、作品、署名、文件名） |
| `list_artists` | 成员 | 浏览艺人，可按关键词、角色、首字母筛选 |
| `get_artist` | 成员 | 艺人身份、名字（首批 20 条）、各角色参与数、`revision` |
| `list_artist_albums` | 成员 | 艺人的专辑 |
| `list_artist_tracks` | 成员 | 按署名角色列艺人的曲目 |
| `get_release_evidence` | 管理员 | 版本各字段的选中值、扫描候选值、来源类别、置信度、规则、归组相对来源路径 |
| `list_release_issues` | 管理员 | 问题列表，按 `status`（open/reviewed）与 `category`（fields/grouping/missing_tracks）筛选 |
| `get_release_issues` | 管理员 | 单个版本的字段、归组、缺失曲目问题，含核对所需的 basis 令牌（`backend/internal/catalog/stewardtools/issues.go:10-42`） |

治理预览（`backend/internal/catalog/governance/stewardtools/tools.go:71-72`，管理员）：

| 工具 | 用途 |
| --- | --- |
| `preview_release_claim` | 预览旧版本认领到新版本，返回 preview 令牌与双方 revision |
| `preview_artist_split` | 预览艺人拆分范围，返回 basis 令牌 |

艺人候选：

- 读取艺人候选列表的管家工具**查不到**。HTTP 接口 `GET /api/v1/artist-candidates` 存在（`backend/internal/catalog/httpapi/artist_governance.go:39,236`），但没有对应的工具定义。
- 写工具 `ignore_artist_candidate` 存在（见 3.6）。

读歌词的工具：**查不到**（ADR 0005 第 3 节提到“专门读歌词的工具”，注册表里没有）。

操作、扫描与系统（均为管理员，另注除外）：

| 工具 | 用途 | 位置 |
| --- | --- | --- |
| `list_operations` | 按版本、专辑组、艺人筛选操作记录，含是否可撤销 | `backend/internal/operations/stewardtools/tools.go:44-46` |
| `list_scans` | 历史扫描记录 | `backend/internal/library/stewardtools/tools.go:53` |
| `get_scan_report` | 一次扫描的统计与新增专辑 | 同上 `:66` |
| `get_scan_status` | 当前扫描状态（成员可用） | 同上 `:71` |
| `list_library_roots` | 音乐目录与排除规则 | `backend/internal/library/stewardtools/config.go:40` |
| `get_scan_schedule` | 每日定时扫描的开关与整点 | 同上 `:44` |
| `get_system_status` | 版本、运行时长、数据库与曲库统计 | `backend/internal/platform/diagnostics/stewardtools/tools.go:20` |
| `get_disk_usage` | 磁盘总量与可用量 | 同上 `:27` |
| `list_recent_logs` | 已脱敏的最近日志 | 同上 `:34` |
| `get_backup_overview` | 备份状态与保留规则 | `backend/internal/backup/stewardtools/tools.go:43` |
| `list_members` | 成员列表 | `backend/internal/identity/stewardtools/tools.go:41` |
| `get_analysis_progress` | 音频分析进度 | `backend/internal/playback/stewardtools/tools.go:71` |

播放与个人库（成员）：

| 工具 | 用途 | 位置 |
| --- | --- | --- |
| `get_track_audio` | 曲目音频格式与响度结果 | `backend/internal/playback/stewardtools/tools.go:19` |
| `get_playback`、`get_queue` | 本人播放状态、设备、队列 | `backend/internal/zones/stewardtools/tools.go:112,135` |
| `get_zone_settings` | 本人分区的播放与音效设置 | `backend/internal/zones/stewardtools/settings.go:16` |
| `list_peq_presets` | 本人的 PEQ 预设 | `backend/internal/dspsettings/stewardtools/tools.go:19` |
| `list_play_history`、`list_recent_releases` | 本人播放历史 | `backend/internal/listening/stewardtools/tools.go:43,47` |
| `list_favorites`、`list_playlists`、`get_playlist` | 本人收藏与歌单 | `backend/internal/personal/stewardtools/tools.go:108,120,124` |
| `list_memories` | 本人记忆（`personal` 范围、`ReadOnly`） | `steward/memory/tools.go:13,34` |

联网（成员，`steward/web/tools.go:11-115`）：

| 工具 | 用途 |
| --- | --- |
| `web_search` | 搜索外部网页；仅当该用户配置了 SearXNG 或 Tavily 时可见（`Available`，`:16`） |
| `fetch_page` | 读取公网网页正文 |
| `search_music_metadata` | 在 MusicBrainz 按查询词找艺人、发行、发行组或录音，`limit` 默认 5、最多 10 |
| `get_music_metadata` | 按 MBID 读 MusicBrainz 实体，可带 `include`；`cover-art` 只返回封面清单 |

### 8.1 `steward/web/musicbrainz.go` 的细节

提供的查询只有两种请求形态：

- 搜索：`GET https://musicbrainz.org/ws/2/{entity}?query=…&limit=…&fmt=json`，`entity` 限 `artist`、`release`、`release-group`、`recording`；查询词原样放进 `query` 参数（`steward/web/musicbrainz.go:17,63-101`）。
- 按 MBID 读取：`GET …/ws/2/{entity}/{mbid}?fmt=json&inc=…`，`inc` 按实体取白名单（`:20-25,110-167`）：
  - `artist`：`releases`、`release-groups`、`recordings`
  - `release`：`artist-credits`、`recordings`、`labels`、`release-groups`，另可加 `cover-art`
  - `release-group`、`recording`：`artist-credits`、`releases`
- 封面清单：`GET https://coverartarchive.org/release/{mbid}/`，只跟随到 `archive.org` 或 `coverartarchive.org` 下的 `index.json`，只返回图片地址、正背面标记和缩略图地址，不下载图片（`:18,177-231`）。
- 按条码、DiscID、ISRC 的专用查找接口、browse 接口、URL 关系：代码里查不到。
- 返回值是裁剪后的投影：MBID、标题或名称、消歧、艺人署名（含连接词）、日期、首发日期、国家、条码、时长、厂牌与发行编号、介质与曲目表、所属发行组、关联的发行/发行组/录音（`steward/web/metadata.go:51-100`）。

限速：

- 全进程一个许可串行所有 MusicBrainz 请求，相邻请求至少间隔 1 秒，排队超过 10 秒返回 `rate_limited`（`steward/web/limiter.go:61-109`；在 `musicJSON` 里调用，`steward/web/musicbrainz.go:47`）。
- 封面清单请求不经过这个限速器（`coverArt` 里没有 `musicLimiter.wait`，`steward/web/musicbrainz.go:177-231`）。
- 每用户每分钟 20 次（`search_music_metadata` 与 `get_music_metadata` 合计），只加在工具包装层（`steward/web/limiter.go:11`、`steward/web/tools.go:92-108`）。`Service.SearchMusic` 与 `Service.GetMusic` 本身不带用户参数（`steward/web/musicbrainz.go:63,110`）。
- 非 200（含 503）一律返回 `source_unavailable`，不重试，不读错误正文（`steward/web/musicbrainz.go:55-58`）。
- 单次请求超时 20 秒，响应最多读 2 MiB（`steward/web/service.go:21-22,105-114`）；出站策略 `PublicOnly`（`steward/web/musicbrainz.go:38`）。

缓存：**没有**。`steward/web/` 下检索 `cache` 无结果，每次调用都发网络请求。

User-Agent：`ROOMusic/<版本> ( https://github.com/qlqqs/ROOMusic )`，版本为空时写 `dev`（`steward/web/musicbrainz.go:27-33`、`backend/internal/platform/buildinfo/buildinfo.go:23-24`）；网页抓取用通用值 `Mozilla/5.0 (compatible; WebReader/1.0)`（`steward/web/service.go:23`）。

日志：`steward.web.request.finished`，记来源、域名、耗时、结果数、错误码，不记查询词（`steward/web/service.go:79-93`）。

## 9. MCP 入口

- 挂载路径是 `/api/v1/mcp`（`backend/internal/app/steward.go:152`）；路线图里简写为 `/mcp`。
- 与对话用的是**同一个注册表实例**：`registry` 同时传给 `runtime.NewRunner` 与 `stewardmcp.New`（`backend/internal/app/steward.go:127,152`）。
- 可见工具按令牌所属用户的身份过滤，再按令牌范围过滤（`steward/mcp/server.go:17-23,36-41`）：
  - 只读令牌：`read` 工具，加上标为 `ReadOnly` 的 `personal` 工具（即 `list_memories`）。
  - 读写令牌：再加 `personal` 与 `shared` 工具。
- 写入**不走确认，也不走审查**：读写令牌的调用者 `SharedExecution` 直接为真（`steward/mcp/server.go:35`），`Invoke` 直接执行；工具说明里追加“此工具会直接修改曲库，不经过服务端确认”（`:44-46`）。ADR 0005 第 4 节第 6 条与第 7 节把这一点登记为已接受风险（`docs/architecture/adr/0005-steward-data-and-egress.md:84,161`）；父任务 S10（`prd.md:46`）。
- MCP 调用者没有模式、会话 ID 和方案 ID；来源记 `mcp` 并带令牌 ID（`steward/mcp/server.go:35`）；每次调用新生成幂等键（`:98`）。
- 无状态传输，每个请求重新校验令牌、用户与成员开关；Cookie 不参与认证（`steward/mcp/handler.go:48-51,55-91`）。
- MCP 不经过运行时：没有步数与 token 上限，没有方案；工具处理器里自带的每用户外部请求限额仍然生效（限额加在 `Handler` 包装上，`steward/web/tools.go:102-108`）。
- 令牌属于签发它的用户，可设只读或读写、过期时间，可吊销（`steward/token/token.go:15-21`、`steward/token/store.go:41,96,114`）。

## 10. 父任务（M6）里与“自动、后台、批量整理、外部元数据源”有关的既有决定

目录：`.trellis/tasks/archive/2026-11/11-15-music-steward-m6/`（下称“父”）与同月各子任务目录。

### 已做出的决定

| 内容 | 出处 |
| --- | --- |
| S1：形态是自主多步工具循环，过程流式可见 | 父 `prd.md:37` |
| S7：不做物理文件整理，整理只在目录层面，走现有受治理操作 | 父 `prd.md:43` |
| S8：支持联网——结构化音乐数据源、通用搜索、网页抓取 | 父 `prd.md:44` |
| S10：MCP 读写令牌的写操作直接执行，由客户端把关 | 父 `prd.md:46` |
| S12：三种模式对所有用户开放，模式只决定审批路径，不改变权限 | 父 `prd.md:48` |
| S14：`steward` 模式的方案交独立审查，批准后自动执行；三种结论；审查不可用时拒绝执行，不降级 | 父 `prd.md:50` |
| S15：端点与密钥按用户配置、只供本人使用，没有共享密钥；不做用量上限 | 父 `prd.md:51` |
| 默认值：审查子代理默认用同一个端点和模型，可另选模型 | 父 `prd.md:60` |
| S20：未走 `operations.Run` 的配置写入标为不可撤销，照常确认 | 父 `prd.md:68` |
| S21：历史长度用自动摘要控制 | 父 `prd.md:69` |
| R3：目录层面的整理全部经 `operations.Run` | 父 `prd.md:78-79` |
| R4：联网——结构化音乐数据源查询、通用搜索、网页抓取 | 父 `prd.md:80` |
| R11：耗时长的任务可以在后台继续，完成后可查看结果 | 父 `prd.md:93` |
| 设计：循环有步数上限、单次对话 token 上限和用户取消；遇到待确认方案时循环挂起并落库 | 父 `design.md:79-81` |
| 设计：审查默认与主代理同端点同模型，同源时独立性有限 | 父 `design.md:74` |
| C10 D3：所有运行都不依赖请求，不区分前台与后台 | `11-16-steward-c10-memory/prd.md:26` |
| C10 D4：服务重启时运行中断，可用“继续”接口接着跑；不做跨重启恢复 | 同上 `:27` |
| C10 D5：每个用户同时最多 3 个运行中的会话 | 同上 `:28` |
| C7 D1：审查按项给结论，一份方案可部分批准、部分拒绝、部分转人工 | `11-16-steward-c7-review/prd.md:24` |
| C7 D2：策略第 1 版——不可撤销项与一次超过 20 项的方案一律转人工 | 同上 `:25` |
| C7 D3：提示词、证据字段、策略写进规范；用户要改时作为策略第 2 版 | 同上 `:26` |
| C7 D4：审查模型在设置里选，空值表示与对话同一个模型；用用户自己的端点和密钥 | 同上 `:27` |
| C6 D2：方案有效期 24 小时；过期或用户发新消息时作废 | `11-16-steward-c6-writes/prd.md:33` |
| C6 D5：艺人合并、拆分、改名、忽略候选标为不可撤销 | 同上 `:36` |
| C8 D4：音乐数据源只做 MusicBrainz 与 Cover Art Archive 的封面清单 | `11-16-steward-c8-web/prd.md:31` |
| C8 D5：MusicBrainz 的 User-Agent 写明应用名、版本、项目地址 | 同上 `:32` |
| C8 R5：MusicBrainz 全实例共用每秒 1 次的限速 | 同上 `:43` |

### 明确排除项

| 内容 | 出处 |
| --- | --- |
| 部署层操作、应用容器内 shell、物理文件整理 | 父 `prd.md:125` |
| 订阅类模型接入；管理员提供的共享模型密钥与用量上限 | 父 `prd.md:127` |
| **定时任务、无人值守的周期性整理** | `11-16-steward-c10-memory/prd.md:67` |
| 跨服务重启恢复运行；多实例部署下的运行分发 | 同上 `:66` |
| 浏览器通知、邮件通知 | 同上 `:68` |
| 自动从对话里提炼记忆 | 同上 `:69` |
| **一次对一批专辑的批量操作类型**；艺人别名；给不可撤销的艺人操作补撤销 | `11-16-steward-c6-writes/prd.md:74-75` |
| 审查模型用另一个端点；审查时让审查模型自己调用工具查资料 | `11-16-steward-c7-review/prd.md:54` |
| 配置类工具的审查规则（默认按不可撤销处理） | 同上 `:55` |
| Discogs；外部歌词库；**下载封面图片入库**；Brave、Exa、Serper 等其他商业搜索 | `11-16-steward-c8-web/prd.md:64` |
| 抓取需要执行脚本的页面、PDF | 同上 `:66` |
| 只读 SQL；给配置写入补操作记录与撤销 | `11-16-steward-c9-config/prd.md:61-62` |

### 验收记录里的相关事实

- A1 判为“部分”：没有一条用例把“查证据→查外部→出方案→确认→逐项撤销”串起来（父 `acceptance.md:18,59`）。
- “24 步与 40 万 token 的上限对大专辑是否够用”列为只能真实环境验证（父 `acceptance.md:358`）。
- 真实 MusicBrainz 返回的资料质量与错误形态列为未验证（父 `acceptance.md:63,359,393`）。路线图记 2026-11-20 的真实试用里 SearXNG 与 Tavily 未试用（`docs/architecture/core-1-roadmap.md:24`）。

### 路线图与产品合同

- “外部元数据源不作为 Core 1 前置”（`docs/architecture/core-1-roadmap.md:51-52`）。
- 产品合同对 `steward` 模式的定位：“Autonomous evidence-backed organization and bounded maintenance”（`.trellis/spec/guides/product-goals.md:33`）；“Execute bounded autonomous operations after review”（`.trellis/spec/backend/agent-and-operation-guidelines.md` 的 Mode contract 表）。
- 产品合同：确定性的扫描与解析是普通程序逻辑，不是 Agent（`.trellis/spec/backend/agent-and-operation-guidelines.md` 的 Status And Scope；`.trellis/spec/guides/product-goals.md:51`）。

## 离“无人值守的后台整理”还缺什么

只列从代码和文档能确认的缺口。

触发与归属：

1. 没有系统发起运行的入口：`Supervisor.Start` 只被登录用户的 HTTP 处理器调用（`steward/httpapi/events.go:157`），`steward/` 下没有调度器，扫描结束也没有通知管家的钩子。
2. 运行必须挂在某个用户的某个会话上；方案项必须挂在会话消息里的工具调用上（迁移 0054 的外键）。没有不属于会话的方案或运行。
3. 方案只能由模型的工具调用产生；程序侧（扫描或外部源匹配）直接提交方案或待定项给审查的入口不存在。
4. 运行依赖发起者有效的登录会话，60 秒复查一次，会话过期或登出即取消（`steward/httpapi/events.go:171-207`、`backend/internal/app/steward.go:153-164`）。
5. 受治理操作的操作者必须是真实用户（`operations.actor_id NOT NULL REFERENCES users`）；管家工具写入的来源 `origin.source` 只有 `chat` 与 `mcp` 两个取值（`steward/tool/definition.go:45-49`、`steward/httpapi/events.go:159`、`steward/mcp/server.go:35`）。
6. 没有运行表：运行不落库、没有运行标识，重启即丢失，不自动恢复（C10 D4）。

凭据：

7. 没有实例级或共享的模型凭据；密钥按用户加密绑定。系统运行该用谁的端点与密钥，ADR 0005 与父任务文档都没有规定（父 `prd.md:127` 明确排除了共享密钥）。

审查与自动执行的覆盖面：

8. 能被审查自动放行的只有 5 个发行级可撤销工具；艺人合并、拆分、改名、忽略候选和 `revert_operation` 一律转人工（策略第 1 版）。
9. 一份方案超过 20 项即整份转人工；没有批量操作类型（C6 排除项）。
10. 审查证据只有本地目录摘要与字段前后值，不含外部资料，也不含主代理的依据；审查模型不能自己查资料（C7 排除项）。
11. 审查模型与对话模型只能共用同一用户的同一端点；不能用另一个端点（C7 排除项）。
12. 审查不可用时没有自动重试；未处理项停在 `pending`，要用户调重审接口或人工确认，否则 24 小时后过期。
13. 批准只在当次运行内有效，执行前逐项重读核对；跨请求不能凭已保存的批准恢复执行。

例外的呈现：

14. 转人工的项以会话里的方案卡片呈现，按会话统计 `pending_item_count`（`steward/runtime/conversations.go:83-93`）；没有跨会话的待处理例外队列。
15. 没有站外通知（C10 排除了浏览器通知与邮件通知）；只有会话未读标记与未读会话计数（`steward/httpapi/settings.go:51,516-522`）。
16. 每个会话同一时刻只能有一份 `pending` 方案；用户在该会话发新消息会把它作废。

用量：

17. 没有按用户或全局的 token、调用次数汇总，也没有预算上限（父 S15 明确不做）；审查调用的用量不计入单次运行的 40 万上限。
18. 并发上限只有每用户 3 个运行；没有全局并发上限或后台任务排队。
19. 每步与每次运行的工具调用数没有上限（只有 24 步与 token 上限间接约束）。

外部数据源：

20. MusicBrainz 只有按查询词搜索和按 MBID 读取；没有按条码、DiscID、ISRC 的专用查找，没有缓存，503 不重试；封面清单请求不受每秒 1 次的限速器约束。
21. MusicBrainz 的每用户限额（每分钟 20 次）只加在工具包装层，面向对话与 MCP 调用；`Service.SearchMusic`/`GetMusic` 本身只受全进程每秒 1 次的限制。
22. 没有保存外部标识或外部匹配结果的写工具：`correct_release` 只能改 8 个发行级字段；没有曲目级字段、署名、封面入库的写工具（C8 排除了下载封面入库）。
23. 没有读取艺人候选列表的管家工具（HTTP 接口存在，工具查不到）；没有读歌词的工具。
24. 外部数据源只有 MusicBrainz 与 Cover Art Archive；Discogs 与外部歌词库在 C8 被排除。

既有决定上的冲突点：

25. C10 的 PRD 把“定时任务、无人值守的周期性整理”列为不做（`11-16-steward-c10-memory/prd.md:67`）；本任务的目标与这条排除项直接相对，需要新的决定来取代。

## Caveats / Not Found

- 未运行任何测试；全部结论来自阅读代码、迁移、规范与归档文档。
- 查不到的项：系统或定时发起的管家运行入口；艺人候选的读工具；读歌词的工具；按用户或全局的用量汇总与预算；MusicBrainz 缓存；ADR 对“系统发起的运行用谁的凭据”的规定；“系统操作者”形式的操作记录。
- `steward/provider/` 的三种协议适配细节、`steward/web/search.go` 与 `fetch.go` 的内部实现、前端 `frontend/src/features/steward/` 只做了针对性检索，没有通读。
- `.trellis/spec/backend/steward-runtime-contracts.md` 共 1213 行，只读了 C10 步骤 1（21-85 行）与第 7 节循环（862-900 行）并核对了章节目录；第 3 节工具清单未与代码逐条比对，本文的工具清单以代码为准。
- 配置类写工具“是否进操作记录”取自验收记录（父 `acceptance.md:338`），未逐个工具核对实现。
- 本文行号对应提交 `3e8714b4` 的工作区；ADR 0005 与路线图各有一处未提交的文字改动，不影响所引条文。
