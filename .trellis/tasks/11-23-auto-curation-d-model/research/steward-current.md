# Research: 子任务 D 接入管家运行时的现状

- **Query**: 为“模型后台判断”调研管家会话与运行、模式与方案、审查、端点与密钥、用量、写工具与自动层、操作来源、待定项、事件与前端、测试夹具、合同条文，以及与无人值守运行冲突的约束
- **Scope**: internal（代码、迁移、规范、ADR、归档任务）
- **Date**: 2026-11-23
- **基线**: `main`，提交 `05463a57`（B3 已合入）。路径相对仓库根；`steward/…`、`curation/…`、`operations/…` 是 `backend/internal/…` 的简写；迁移在 `backend/migrations/`。
- 写法：每节先列**事实**（带 文件:行号），再列**推断**（明确标出）。未运行任何测试。
- 相关的旧调研：父任务 `.trellis/tasks/11-22-auto-curation-sources/research/steward-runtime.md`（基线 `3e8714b4`，A 之前）。本文以当前代码为准重新核对，那份里的行号多数仍对，但没有 A、B、C、B3 带来的内容。

## 结论速览

1. 运行只有 HTTP 入口：`Supervisor.Start` 唯一调用点是 `steward/httpapi/events.go:157`，入口要登录会话；`RunParams.Check` 由会话 Cookie 构造。后台入口、调度器、运行表都不存在。
2. 会话没有“类型”列；列表、计数、未读三处查询都只按 `user_id` 过滤（`steward/runtime/conversations.go:75-112,322-338`）。
3. 每用户 3 个并发在 `Supervisor.Start` 里按 `userID` 计（`steward/runtime/supervisor.go:91-100`），后台运行挂在开启者名下会占他的名额；`CancelUser` 也会连带取消（登出、改设置都会调用）。
4. 方案项上限 20 与“不可撤销转人工”在 `reviewPolicy`（`steward/runtime/review.go:155-166`）；高风险降级在 `steward/runtime/run_review.go:93-97`；策略版本常量 `ReviewPolicyVersion = 1`（`review.go:20`），执行前核对版本（`run_review.go:200,221`）。
5. 审查不可用不重试，运行以 `review_unavailable` 结束，项留在 `pending`（`run_review.go:100-103,123-125`）；方案 24 小时过期（`steward/runtime/plan_store.go:21`）。
6. 字段写工具 `correct_release` 走 `governance.CorrectionExecutor` 写人工修正，来源由 `Caller` 拼（`catalog/governance/stewardtools/tools.go:89-100`、`commands.go:28-34`）；管家装配（`app/steward.go:66`）拿不到 `curation.Writer`。
7. `curation.Writer.Apply` 已支持 `Source.Kind="model"`、`OnlyEmpty`、批次，操作者取 `curation_settings.enabled_by`，来源固定 `source:"curation"`（`curation/writer.go:42-118`）；`operations.Origin` 没有 `run_id`、待定项字段（`operations/operations.go:135-146`）。
8. 用量只落在 `steward_messages.input_tokens/output_tokens` 与 `steward_reviews.usage`；摘要调用的用量不落库；没有按天、按来源的汇总，也没有每日上限。
9. 待定项表与 `UpsertPendingItem`、`ResolvePendingItems` 已有（`curation/work_states.go:80-103`），C 只写 `waiting_model` 与 `resolved`；表上没有记录“提出的修改、审查理由、所属会话”的列。
10. 受控模型服务的现成做法：运行时单测的 `fakeProvider`（`steward/runtime/run_test.go:96-138`）与 HTTP 层的 `httptest` 假端点 `c7API.serveModel`（`steward/httpapi/review_c7_integration_test.go:45-200`）。

---

## 1. 会话与运行

### 1.1 数据模型（事实）

| 表 | 关键列与约束 | 位置 |
| --- | --- | --- |
| `steward_conversations` | `id`、`user_id NOT NULL → users ON DELETE CASCADE`、`title`、`mode CHECK IN ('assistant','steward','operator')` 默认 `assistant`、`model NOT NULL`、`status CHECK IN ('idle','running')`、`created_at`、`updated_at`；索引 `(user_id, updated_at DESC, id)` | `0051_steward_runtime.sql:32-43` |
| 同上，后加列 | `auto_approved_tools text[]`（`0054_steward_plans.sql:2`）；`last_read_at`、`last_run_finished_at`（0057）；`summary`、`summary_through_message_id`、`summary_updated_at`（0057） | `0054:2`；0057 |
| `steward_messages` | `conversation_id → 会话级联`、`sequence`、`role IN (user,assistant,tool)`、`content json`、`input_tokens`、`output_tokens`（可空，≥0）、`created_at`；后加 `context jsonb`（0053）、`provider_blocks jsonb`（0056） | `0051:45-56` |
| `steward_tool_calls` | `message_id → 消息级联`、`position`、`tool_name`、`input json`、`output json`、`truncated`、`untrusted`、`status`（后加 `awaiting_confirmation`）、`error_code`、`duration_ms`；后加 `idempotency_key uuid NOT NULL DEFAULT gen_random_uuid()` | `0051:58-71`；`0054:3-5` |

- 会话**没有类型/来源列**；`mode` 是唯一区分字段。
- **没有运行表**：一次运行没有持久标识，只体现为 `steward_conversations.status='running'`（`steward/runtime/conversations.go:192-212`）。
- 运行中的协程、取消函数、事件日志、订阅只在进程内存（`steward/runtime/supervisor.go:32-56`）。

### 1.2 运行如何启动（事实）

- HTTP 入口（全部 `/api/v1/me/steward/conversations/{id}/…`，`steward/httpapi/handlers.go:67-70`）：`messages`、`plans/{plan_id}/decision`、`plans/{plan_id}/review`、`continue`。都经 `route`：写方法同源校验、`guard.Authenticate`、成员开关（`handlers.go:86-113`）。
- 公共入口 `Handlers.startRun`（`steward/httpapi/events.go:81-169`）的顺序：
  1. 读会话（`:90`）；decision/review 的预检（`:98-124`）；会话忙拒绝（`:125-128`）。
  2. `h.configured(w,r,user)` 读本人设置、套用覆盖、解密密钥、检查工具能力（`:129`；实现 `steward/httpapi/settings.go:281-327`）。
  3. `h.outboundClient(ctx,user)` 构造出站客户端（`:133`；`steward/httpapi/outbound.go:15-47`）；`preflight` 10 秒预检（`:144`；`outbound.go:49-53`）；`modelProvider` 按协议构造（`:148`；`outbound.go:55-63`）。
  4. 模型名：会话的 `model`，空时用设置的 `Model`（`:153-156`）。
  5. `h.supervisor.Start(r.Context(), RunParams{…})`（`:157-162`）：`Check: h.runCredentialCheck(r,user,settings)`、`Caller: tool.Caller{User:user, Source:"chat", RequestID}`、`ReviewModel: settings.ReviewModel`、`DisplayName`、`Provider`、`Model`、`Protocol`、`Endpoint`、`NativeSearch`。
  6. `h.streamSubscription(w, r, run.Subscribe(0))` 写 SSE（`:168`）。
- `Supervisor.Start` 全仓库只有这一个调用点（`grep` 确认）。

### 1.3 依赖登录会话的地方（事实）

| 位置 | 依赖 |
| --- | --- |
| `steward/httpapi/events.go:172-207` `runCredentialCheck` | 捕获请求里的会话 Cookie；每次检查重建最小请求调用 `h.checkCredentials`；Cookie 无效 → `ErrRunInvalidated`。另查成员开关（非管理员）、端点与密钥密文是否等于启动时的快照（`:198-204`、`sameModelKey` `:209-214`） |
| `app/steward.go:166-178` `WithCredentialCheck` | 实际实现：查 `sessions`+`users`，要求会话未撤销、未过期、用户未停用、角色不变 |
| `steward/runtime/supervisor.go:119-158` | `Check` 启动前执行一次（`:154-158`），之后每 60 秒（`:125`）；`ErrRunInvalidated` → `s.CancelUser(run.userID)`（`:137-139`）。`Check` 为 nil 时不起监视协程（`:144-146`） |
| `app/app.go:255-259` | `identityapi.WithSessionRevoked` → `supervisor.CancelUser(userID)`（登出、停用、撤销会话、改密都会触发，见合同 C10 步骤 1 的“取消钩子”表） |
| `steward/httpapi/settings.go:158-160`、`probe.go:122` | 本人改端点或密钥 → `CancelUser(user.ID)` |
| `steward/httpapi/admin.go:61` | 关成员开关 → `CancelMembers()`（只取消非管理员） |
| `app/steward.go:113` | C9 `set_member_disabled`/`revoke_member_sessions` 工具 → `CancelUser` |

### 1.4 运行循环（事实）

- `Runner.Run`（`steward/runtime/run.go:46-234`）：
  - 有 `Decision` 时 `DecidePlan`，否则 `AcquireConversation`（`:52-62`）。
  - 读会话、摘要、记忆（`:114-127`；记忆按 `userID` 读本人的 `steward_memories`）。
  - 用会话的 `mode` 覆盖 `Caller.Mode`，并清空 `SharedExecution/PlanID/ReviewID/ReviewDecision`，设 `Caller.ConversationID`（`:128-136`）。
  - `ReviewPlanID` 分支：仅 steward（`:137-175`）。`Continue` 分支：有 pending 方案直接发 `plan` 并 `awaiting_confirmation`（`:176-188`）；执行已取舍未落结果的项（`:189-207`）。
- `state.run`（`run.go:267-365`）：`prepare` 追加用户消息并在标题空时取前 32 字作标题（`:236-265`）；工具列表来自 `registry.ListContext(ctx, Caller)`（`:276-282`）；循环到 `maxSteps=24`（`:18,283`）；每步前检查 `maxRunTokens=400000`（`:19,284-286`）；`readModel`（`:291-292`，系统提示 `systemPromptWithMode`，`run_content.go:233-241`）；`recordUsage`（`:293`，实现 `:447-460`）；落库助手消息（`:323-332`）；`executeCalls`（`:338`）；steward 模式每步后从库重读历史（`:342-350`）；无工具请求则 `completed`（`:360-362`）。
- 结束原因：`completed`、`step_limit`、`token_limit`、`cancelled`、`provider_error`、`awaiting_confirmation`、`review_unavailable`、`internal_error`（`steward/runtime/run_types.go:84-99`）。
- 收尾（`run.go:76-113`）：`FinishConversation` 或 `ReleaseConversation`；日志 `steward.run.finished` 带 `steps`、`input_tokens`、`output_tokens`（`:103-109`）；发 `done`（`:110-112`）。
- 步与步之间没有“外部停止条件”钩子：只有 `ctx.Err()` 与 token 上限（`:283-290,354-359`）。

### 1.5 并发限制（事实）

- `MaxUserRuns = 3`（`steward/runtime/supervisor.go:14`）；`Start` 里按 `run.userID == params.Caller.User.ID` 计数未结束的运行（`:91-100`），超出 `ErrRunLimit`（HTTP 映射 `steward/httpapi/errors.go:42`，`409 steward_run_limit`）。
- 同会话不并发：`s.runs[ConversationID]` 未结束即 `ErrConversationBusy`（`:86-90`），数据库层再由 `AcquireConversation` 原子占用（`conversations.go:193-212`）。
- 没有全局上限，也没有“后台类”运行的单独计数。

### 1.6 取消与恢复（事实）

- 运行上下文：`context.WithCancel(context.WithoutCancel(request))`，再挂 Supervisor 生命周期（`supervisor.go:101-102`）。断开 SSE 只退订（`:58-66,279-286`）。
- `Cancel(conversationID)`（`:295-304`）、`CancelUser(userID)`（`:305-313`）、`CancelMembers()`（`:314-322`）、`Shutdown`（`:325-344`，最多等 10 秒）。
- 重启不恢复：`ResetRunningConversations` 把 `running` 会话复位并把遗留工具调用标 `interrupted`（`conversations.go:247-265`），在 `registerSteward` 启动时调用（`app/steward.go:74-91`）。
- 事件日志结束后保留 60 秒（`supervisor.go:16,195,205-214`）。

### 1.7 推断

- 后台入口可以直接调用 `Supervisor.Start`：`RunParams` 不含 HTTP 类型；`request` 参数只用于派生上下文（`:101`），传应用生命周期上下文即可；`Check` 可换成“总开关、开启者仍是启用管理员、端点与密钥快照未变”的闭包，其返回 `ErrRunInvalidated` 时现有代码会 `CancelUser(开启者)`——这会连带取消开启者自己的对话运行（见第 12 节）。
- 构造 Provider 的逻辑（`configured`、`effectiveSettings`、`preflight`、`modelProvider`、`modelKeyPurpose`）都在 `steward/httpapi` 且多为未导出、`configured` 还需要 `http.ResponseWriter`；后台入口要复用就得在 `httpapi` 内新增函数或导出。`OutboundClient` 已导出（`outbound.go:20`）。

## 2. 模式与方案

### 2.1 模式（事实）

| 判定点 | assistant | steward | operator | 位置 |
| --- | --- | --- | --- | --- |
| shared 工具直接执行 | 可撤销且在会话免确认名单 | 从不 | 总是 | `steward/runtime/run.go:473-475` |
| 未直接执行的 shared 调用 | 进方案等用户确认 | 进方案交审查 | — | `steward/runtime/run_tools.go:33-39,105-125` |
| 免确认名单 | 可登记 | 拒绝（`CheckPlanDecision`、`DecidePlan`） | — | `steward/runtime/plans.go:111-113`；`plan_store.go:143-151` |
| 重审接口 | 409 | 可用 | 409 | `run.go:137-140`；`httpapi/events.go:104-108` |
| 每步后重读历史 | 仅摘要条件 | 每步 | 同 assistant | `run.go:342` |
| 提示词附加 | 等用户确认 | 自动审查、批准自动执行 | 直接执行 | `run_content.go:233-241` |

- 模式校验：新建 `CreateConversation`（`conversations.go:60-72`）、修改 `UpdateConversation`（`:268-305`，改模式要求空闲且无 pending 方案 `:285-299`）。
- 注册表最后一道门：shared 工具在 `Caller.SharedExecution=false` 时 `approval_required`（`steward/tool/registry.go:96-101`）。

### 2.2 方案数据模型（事实）

- `steward_plans`（`0054_steward_plans.sql:7-18`）：`conversation_id NOT NULL`、`user_id NOT NULL`、`status IN (pending,applied,declined,expired,superseded)`、`digest`、`expires_at`；部分唯一索引“每会话至多一份 pending”（`:17`）。
- `steward_plan_items`（`0054:19-` ；decision 约束 0055 改为含 `rejected_by_review`，`0055_steward_reviews.sql:2-3`）：`tool_call_id NOT NULL UNIQUE → steward_tool_calls`、`position`、`tool_name`、`input jsonb`、`preview jsonb`、`revertible`、`idempotency_key`、`decision`、`result`、`operation_id → operations`。
- Go 类型：`Plan`（`steward/runtime/plans.go:22-29`）、`PlanItem`（`:32-53`，`Input/Preview/IdempotencyKey/ReviewID` 等只在后端）、`PlanDecision`（`:56-61`，注释“只能由已登录用户的确认接口创建”）。
- 摘要：`planDigest` = `{tool_name, 规范化 input, position}` 数组的 SHA-256（`plans.go:71-97`）。

### 2.3 有效期与执行流程（事实）

- 产生：一步里所有未直接执行的 shared 调用先 `PreviewCall`，合成一份方案（`run_tools.go:33-39`），`CreatePlan`（`run_tools.go:113`；`plan_store.go:16-53`）；有效期 `time.Now().Add(24*time.Hour)`（`plan_store.go:21`）。
- steward 模式直接进 `reviewPlan`（`run_tools.go:120-122`）；其他模式发 `plan` 事件并 `awaiting_confirmation`（`:123-124`）。
- 用户确认：`DecidePlan`（`plan_store.go:130-240`）原子记录取舍并占用会话；随后 `executePlanItems`（`run_tools.go:195-265`）逐项 `Invoke`（`SharedExecution=true`、`PlanID`、`ReviewID`、原幂等键，`:205-212`），拒绝/未选项先只读对账（`:224-243`）。
- 过期收尾是惰性的：读会话、确认、改模式时调用 `resolvePendingPlans(…,"expired",true)`（`conversations.go:125`、`plan_store.go:167-175`、`conversations.go:289`；实现 `plan_store.go:305-354`）。没有后台清理。
- 新的用户消息把 pending 方案作废为 `superseded`（`steward/runtime/messages.go:117-123`）。

### 2.4 方案项上限 20 的位置（事实）

- 唯一位置：`reviewPolicy` 中 `len(plan.Items) > 20`（`steward/runtime/review.go:157`），产生 policy 来源的 `needs_human_confirmation`。执行前复核时同一函数再用（`run_review.go:91,243`）。
- 方案按“一步里的 shared 调用”组成，所以 20 的计数单位是**模型一步的写调用数**，不是一次运行或一批待定项。

### 2.5 推断

- “一批的方案项数由调度控制”在现有结构里只能间接实现：调度能控制送进会话的待定项数量，控制不了模型一步发几个写调用；若要保证不再“整份转人工”，需要在 `reviewPolicy` 的 20 项规则上按运行来源分支，或改变方案的组成方式。
- 例外不能沿用方案：方案 24 小时过期、新消息作废、每会话一份 pending；ADR 已决定例外记在待定项上（ADR 0006 第 9 节“例外”）。

## 3. 审查

### 3.1 审查模型的选择（事实）

- `model := s.params.ReviewModel`，空则用 `s.params.Model`（`steward/runtime/run_review.go:33-36`）；`ReviewModel` 来自本人设置 `settings.ReviewModel`（`httpapi/events.go:160`）。
- 与主对话共用同一个 `Provider`（同端点、同密钥），只有模型名可不同（`run_review.go:77`）。

### 3.2 证据组装（事实）

- `reviewEvidence`（`steward/runtime/review.go:167-185`）：对每项调用装配进来的 `ReviewEvidenceReader`，再调 `registry.PreviewCall` 取字段前后值，组成 `{item_id, facts, preview:{source:"backend_preview_untrusted_values", data: changes}}`。
- 证据读取器在 `app/steward.go:140-162`：`catalogtools.SupportsReviewEvidence(item.Tool)` 只登记 `correct_release`、`change_release_group`、`set_default_release`、`review_release_issue`、`claim_release`（`catalog/stewardtools/review.go:41-48`）；`ReadReviewTargets` 读发行有效值、字段标签概况、未处理问题、专辑组（`review.go:51-139`）；再加 `operations.List(Page:1,PageSize:20)` 的条数。
- `ReviewEvidenceFacts{Targets json.RawMessage, RecentOperationCount int}`（`review.go:111-114`）。
- 送给审查模型的 JSON 是 `{items:[{item_id,tool,revertible}], evidence}`（`review.go:188-196`），系统提示 `ReviewSystemPrompt`（`:21-25`），只提供并强制 `submit_review`，60 秒超时（`:200-202`），不重试。
- 证据里**没有外部对应、候选、待定项**：`ReadReviewTargets` 只读本地目录（已核对 `catalog/stewardtools/review.go`）；管家工具里也没有读取外部对应或待定项的工具（`grep curation|external_link` 于各 `stewardtools`、`steward/` 无结果）。
- 可复用的读取：`curation.LinksOf(ctx,q,objectType,objectIDs)`（`curation/links.go:54`）、`curation.ObjectsLinkedTo`（`links.go:71`）；强证据判定 `checkGroupEvidence`（`curation/join_group.go:70-84`，未导出）。

### 3.3 结论种类与后续（事实）

| 结论 | 来源 | 后续 | 位置 |
| --- | --- | --- | --- |
| `approved` | model | 执行前核对快照（方案摘要、证据摘要、策略版本、15 分钟有效期），`accepted` 并 `executePlanItems` | `run_review.go:37,150-174,199-273` |
| `rejected` | model | 记 `rejected_by_review`，对账后把理由作为工具结果回给主模型 | `run_review.go:168-174`；`run_tools.go:236-238` |
| `needs_human_confirmation` | model 或 policy | 保持 `pending`，发 `plan`，以 `awaiting_confirmation` 结束 | `run_review.go:136-139,188-193` |
| 不可用（`status="unavailable"`，`items=[]`） | — | 保存审查记录，发 `review_finished`，以 `review_unavailable` 结束，项保持 pending | `run_review.go:100-103,123-125` |

- 不可用的触发：模型错误、超时、非唯一 `submit_review`、schema 不合、项集合不一致、截断（`review.go:188-295`），证据读取业务失败（`run_review.go:61-68`）。
- 审查中取消：`EndCancelled`，不保存审查（`run_review.go:82-84,107-109`）。
- 审查用量写入 `steward_reviews.usage`（`review_store.go:10-33`），**不计入**运行的 `inputTokens/outputTokens`（`run_review.go:77` 只赋给 `review.Usage`）。

### 3.4 policy 判定位置（事实）

| 规则 | 位置 |
| --- | --- |
| 方案超过 20 项 → 转人工，不送审 | `review.go:157-158` |
| 工具不可撤销（`item.Revertible=false`）→ 转人工，不送审 | `review.go:159-160` |
| 工具未登记证据读取方式 → 转人工 | `run_review.go:57-59`（由 `app/steward.go:141-143` 返回 `ErrReviewEvidenceUnsupported`） |
| `approved` 且 `risk=high` → 转人工 | `run_review.go:93-97` |
| 模型返回后再判不可撤销与项数 | `run_review.go:91-92` |
| 规则文本常量 | `review.go:26-33`（`ReviewPolicyRules`）、`:35-46`（`ReviewEvidenceFields`），注释要求与规范逐字同步 |

- `reviewPolicy(plan, item)` 不接收 `ctx`、`Caller` 或数据库，只看方案与项本身（`review.go:155`）。
- `revertible` 来自工具定义（`run_tools.go:37` 取 `definition.Revertible`）。

### 3.5 推断

- 强证据类操作的转人工需要数据库读取（外部对应），放在 `reviewPolicy` 里要改签名；放在证据读取器（`app/steward.go:140`）里可以返回一个“policy 转人工”的信号，但现有返回值只有 `ErrReviewEvidenceUnsupported` 会被当作 policy（`run_review.go:57-59`），理由文案固定为“工具未登记后端证据读取方式”。
- 证据里加外部对应与候选会改变证据摘要（`digestJSON(evidence)`，`run_review.go:73`）；`ReviewPolicyVersion` 升到 2 会使执行前核对拒绝所有 v1 审查（`run_review.go:200,221`），影响范围只是 15 分钟内的在途批准。
- 测试固定了 `ReviewSystemPrompt` 全文（`steward/httpapi/review_c7_integration_test.go:256,260`、`steward/runtime/review_c7_test.go:124`）；`ReviewPolicyRules` 与 `ReviewEvidenceFields` 没有测试对照规范文本（检索无结果），只有注释约定。

## 4. 模型端点与密钥

### 4.1 每用户设置（事实）

- 表 `steward_user_settings`（`0051:1-16`，主键 `user_id`）：`endpoint`、`key_ciphertext`、`key_last_four`、`protocol`（后加 `responses`，0056）、`model`、`display_name`、`probe_result`、`probed_at`、`overrides`；`review_model`（`0055:1`）；`search_source`、`searxng_url`、`search_key_*`（0056）。
- Go：`UserSettings`（`steward/runtime/settings.go:28-51`）、`IsConfigured` 只要求端点、协议、模型，**密钥可选**（`:54-56`）；读写 `GetUserSettings`（`:90-97`）、`UpdateUserSettings`（`:106-129`）。
- 实例级只有 `steward_instance_settings.members_enabled`（`0051:18-22`）与出站放行名单；没有实例级端点或密钥，也没有“整理用的模型”字段（全仓检索 `curation_model`、`整理用的模型` 只在 ADR 与规范里出现）。

### 4.2 加密与 `key_needs_reentry`（事实）

- 加密：`h.secrets.Seal(userID, purpose, key)`（`steward/httpapi/settings.go:241-247`）；模型密钥用途常量 `modelKeyPurpose = "steward.model"`（`settings.go:19`）；附加数据绑定用户 ID 与用途（规范 `steward-runtime-contracts.md` 第 8 节“密钥”表）。
- `keyView` 解不开即 `needs_reentry=true`（`settings.go:481-487`）；`settingsView` 输出 `key_needs_reentry`、`search_key_needs_reentry`（`:91-92`）。
- 运行前解密：`configured()` 里 `h.secrets.Open(user.ID, modelKeyPurpose, …)`，失败即 `409 steward_not_configured`（`settings.go:297-303`）。
- `configured()` 另做：`effectiveSettings` 套用覆盖（协议、模型、工具能力，`:254-279`）；自带搜索时改用 Responses 协议（`:306-321`）；工具能力 `unsupported` 拒绝（`:322-325`）。

### 4.3 审查模型设置（事实）

- `review_model` 随设置读写（`settings.go:43,69,83`），空表示与对话同一个模型；前端提示同模型时独立性有限（`frontend/src/features/steward/components/review-result.tsx:195`）。

### 4.4 后台运行读开启者端点与密钥（事实 + 推断）

- 事实：开启者 ID 在 `curation_settings.enabled_by`（`0058_auto_curation.sql:103-110`）；`curation.Writer.Settings` 读出 `EnabledBy`、`Active`（`curation/settings.go:45-74,90-101`）。端点与密钥只能经 `runtime.Store.GetUserSettings(enabledBy)` 读，解密要 `secret.Store.Open(enabledBy, "steward.model", ciphertext)`。
- 事实：`withCurrentStewardUser` 每次工具调用前用 `guard.EnabledUser(ctx, id)` 重读身份（`app/steward.go:184-232`；`identity/access.go:10-15`），可用来构造后台运行的 `identity.User`。
- 推断：后台入口需要一个不依赖 `http.ResponseWriter` 的“按用户构造 Provider”的函数；现有 `configured` 的检查顺序（配置、解密、工具能力）可原样复用为停止条件之一。“端点与密钥仍在”在密钥可选的前提下，含义需要 D 自己定（例如只要求端点与协议、或密钥存在且能解开）。

## 5. 用量统计

### 5.1 现状（事实）

| 用量 | 落点 | 位置 |
| --- | --- | --- |
| 主模型每步 | `steward_messages.input_tokens/output_tokens`（助手消息） | `0051:52-53`；写入 `run_content.go:78`、`messages.go:145-150` |
| 审查调用 | `steward_reviews.usage jsonb`（`{input_tokens?, output_tokens?}`） | `0055:13`；`review_store.go:10-33`；`provider/types.go:128-133` |
| 摘要调用 | 只进运行内计数与日志，**不落库** | `summary.go:101-105`；`SaveSummary` 不存用量（`summary_store.go:20-38`） |
| 日志 | `steward.model.usage`（每步）、`steward.run.finished`（每次运行合计）、`steward.review.finished` | `run.go:457-458,103-109`；`run_review.go:121` |

- 单次运行上限：`maxSteps=24`、`maxRunTokens=400000`、`maxOutputTokens=8192`（`run.go:17-23`），常量，不经 `RunParams` 配置；只累计输入输出都已知的步（`run.go:452-456`）。
- `steward_reviews` 没有 `user_id`/`conversation_id` 列，只经 `plan_id → steward_plans(conversation_id,user_id)` 关联（`0055:4-18`；`0054:7-16`）。
- `curation_batches` 只有 `id, tier, opened_at, closed_at, actor_id`（`0058:80-86`），**没有用量列**；批次统计只有操作数与撤销数（`curation/batches.go:87-91`）。ADR 0006 第 5 节写“批次记录…用量”（`docs/architecture/adr/0006-auto-curation-and-sources.md:122-123`），未实现。
- 按用户、按天、按来源的汇总与上限：检索 `daily`、`budget`、`quota` 无相关代码。

### 5.2 推断

- 现有列可以按“来源=自动整理”汇总：若会话有类型标记，`SUM(steward_messages.input_tokens+output_tokens)` 按 `created_at` 取当天，加上 `steward_reviews.usage` 经方案关联到会话；摘要调用的用量会漏算（第 5.1 表第三行）。
- “达到上限后正在跑的运行跑完当前这一步后结束”在循环里没有现成钩子：只能在 `run.go:283-286` 的步前检查处扩展，或经取消（取消会走 `EndCancelled` 并中止当前模型流，`run.go:302-316`），两者语义不同。

## 6. 工具与写入

### 6.1 字段类写工具（事实）

- 全部在 `catalog/governance/stewardtools`：`StewardTools(db, resolution)`（`tools.go:50-74`）。
  - `correct_release`（`tools.go:60`）：参数 `release_id`、`expected_revision`、`changes`（八个字段，`mode ∈ set|null|clear`，`tools.go:55-58`）、`basis{source∈local|external|mixed, note≤4096}`（`:20`）。
  - 其余：`change_release_group`、`set_default_release`、`review_release_issue`、`claim_release`（可撤销）；`merge_artists`、`split_artist`、`rename_artist`、`ignore_artist_candidate`（不可撤销）（`:61-68`）。
  - 曲目级字段的写工具不存在（I 未做）。
- 执行（`tools.go:89-100`）：`a.command(name, raw)` 得到 `{kind, fingerprint, execute}`；`operations.Run(ctx, a.db, Spec{ActorID: caller.User.ID, Type, Key: caller.IdempotencyKey, Fingerprint, RequestID, Origin: &Origin{Source: caller.Source, ConversationID, PlanID, Mode, TokenID}}, execute)`。
- `correct_release` 的命令：`governance.ParseCorrectionChanges` → `CorrectionRequest{ReleaseID, ExpectedRevision, Changes}` → `governance.CorrectionExecutor`（`commands.go:28-34`），写 `release_corrections` 与 `edit_revision`（规范 `curation-contracts.md` 第 3 节）。
- `change_release_group` 直接用 `governance.GroupChangeExecutor`（`commands.go:35-40`），**不做强证据核对**；强证据只在 `curation.Writer.JoinGroup`（`curation/join_group.go:20-68`）。
- 预览：`correct_release` 读 `LoadCorrectionState` 的有效值作 `before`（`preview.go:24-56`）。
- 装配：`app/steward.go:98-100` 注册；所有工具外包 `withCurrentStewardUser`（`:125`）；shared 工具再包一层，`caller.ReviewID != ""` 时 `operations.WithReviewOrigin`（`:126-134`）。`registerSteward` 的参数里没有 `curation.Writer`（`app/steward.go:66`；调用处 `app/app.go:231`，而 `curationWriter` 在 `app.go:202` 已构造）。
- 依赖白名单：`catalog/governance/stewardtools` 只允许导入 `catalog`、`catalog/governance`、`operations`、`platform/uuid`、`steward/tool`（`app/dependency_rules_test.go:37`）；`curation` 不在内。

### 6.2 `curation.Writer.Apply`（事实）

- 类型：`Source{Kind, Provider, Basis}`（`curation/writer.go:18-22`）；`Request{OnlyEmpty, ReleaseID, Fields map[string]catalog.CorrectionIntent, Source, BatchID, Key, RequestID}`（`:24-32`）；`Applied{OperationID, Written, Skipped}`（`:34-38`）。
- 校验：`Kind ∈ {source, model}`、`Provider` 匹配 `^[a-z0-9._:-]{1,64}$`、`Basis ≤ 500` 字节且不含 NUL（`:40,47-51`）；`NormalizeAutoFields` 拒绝 `clear`（`catalog/governance/auto_values.go:19-44`）。
- 操作者：事务外读 `curation_settings.enabled_by`（`writer.go:57-63`），事务内 `activeEnabler` 核对仍相同（`:79-85`；`curation/settings.go:76-88`，含 `identity.IsEnabledAdmin`）。**不用 `Caller.User.ID`**。
- 批次：`BatchID` 非空时要求未结束（`writer.go:86-90`；`batches.go:78-85`）。
- `OnlyEmpty`：有人工修正行或已有自动值的字段记 `occupied` 跳过（`writer.go:137-144`）；它不看扫描值是否为空——程序层在调用前用 `missingFields` 自行挑字段（`curation/program/enrich.go:75-77,236`）。
- 其余过滤：命中不再提出 `suppressed`、与现有自动值相同 `unchanged`（`writer.go:145-166`）；全部被过滤 `409 nothing_to_apply`（`:96-98`）。
- 指纹含 `type、release_id、fields、provider`，`OnlyEmpty` 时加 `only_empty`（`:64-67`）；不含 `basis`。
- 模型层可用的其他写入口及限制：`sourceSpec` 要求 `Kind=="source"`（`curation/external_artwork.go:49`），所以 `ApplyLinks`、`ApplyArtwork`、`ApplyLyrics`、`JoinGroup` 目前拒绝 `model`；`JoinGroup` 另要求 `Provider=="musicbrainz"`（`join_group.go:22`）。
- 批次 API：`OpenBatch(ctx,"model")`（`batches.go:22-42`，操作者取开启者）、`CloseBatch`（`:44-57`）、`CloseOpenBatches(ctx,"model")`（`:59-65`）。

### 6.3 工具执行时能拿到的运行上下文（事实）

`tool.Caller`（`steward/tool/definition.go:53-69`）：

| 字段 | 谁设置 | 位置 |
| --- | --- | --- |
| `User`（`identity.User{ID,Username,Role}`） | 入口；`withCurrentStewardUser` 每次重读 | `httpapi/events.go:159`；`app/steward.go:186-196` |
| `Source`（`chat`/`mcp`） | 入口 | `events.go:159`；`steward/mcp/server.go:35`；常量 `tool/definition.go:46,49` |
| `Mode`、`ConversationID` | `Runner.Run` 按会话覆盖 | `run.go:131-132` |
| `SharedExecution`、`IdempotencyKey` | 运行时每次调用 | `run_tools.go:30-32,206-210` |
| `PlanID`、`ReviewID`、`ReviewDecision` | 执行方案项时 | `run_tools.go:208-209` |
| `TokenID` | MCP | `mcp/server.go:35` |
| `RequestID` | HTTP 头 | `events.go:159` |

- 没有“会话类型”“运行来源=自动整理”“批次 ID”“待定项 ID”的字段。
- 上下文值：运行上下文用 `WithoutCancel` 派生，保留请求上下文里的值（`supervisor.go:101`）；审查来源经 `operations.WithReviewOrigin` 放进 `ctx`（`app/steward.go:129-131`；`operations/review_origin.go:9-11`）。

### 6.4 推断：让同一工具在自动整理运行里写自动层的最小改动点

- 判定依据：`Caller.Source`（或新增字段）由后台入口设为自动整理专用的值；`Runner.Run` 不覆盖 `Source`（`run.go:131-136` 只覆盖 Mode 等），可以一路传到工具处理器。
- 切换位置的候选：
  1. `app/steward.go:126-134` 的 shared 包装层：按 `caller.Source` 把 `correct_release` 转给 `curation.Writer.Apply`。`app` 可同时导入两边，不触碰依赖白名单；需要把 `curationWriter` 传进 `registerSteward`。
  2. 在 `catalog/governance/stewardtools` 里加分支：要改 `dependency_rules_test.go:37` 或注入一个接口。
- 参数映射要处理的差异（事实）：`correct_release` 的 `mode=clear` 在自动层非法（`auto_values.go:25-27`）；`expected_revision` 是人工层版本，自动层不用它（`writer.go` 无此参数）；`basis.note` 上限 4096 而 `Source.Basis` 上限 500 字节；`Provider` 的正则不允许大写、空格、斜杠（模型名如 `Claude Sonnet 4`、`anthropic/claude` 会被拒绝，`writer.go:40,48`）；幂等由 `operations.Run` 按（操作者, 键）判断，`Writer.Apply` 的操作者是开启者，所以运行的 `Caller.User.ID` 必须等于开启者，否则 `LookupOperationResult(userID,key)`（`run_tools.go:229`、`operations/lookup.go:11-21`）对不上账。
- 预览差异（事实）：`correct_release` 的预览 `before` 是三层合并后的有效值（`preview.go:24-56`）；人工修正存在时自动值写了也不生效（`curation-contracts.md` 第 4 节末条），审查看到的前后值与实际显示可能不一致。

## 7. 操作记录的 origin

### 7.1 现状（事实）

- `operations.Origin`（`operations/operations.go:135-146`）：`provider_kind`、`provider`、`basis`、`review_id`、`review_decision`、`source`、`conversation_id`、`plan_id`、`mode`、`token_id`。**没有 `run_id`、`batch`、`pending_item_id`**。
- 批次不在 origin 里，是 `operations.batch_id` 列（`0058:87-89`；`Spec.BatchID` `operations.go:151`；写入 `:271-275`）。
- `Run` 写入时若 `ctx` 带审查来源，就合并进 `origin`，不论 `source` 是什么（`operations.go:255-266`）。
- 各写入方填法：

| 写入方 | origin | 位置 |
| --- | --- | --- |
| 管家对话/MCP 的治理工具 | `{source: chat\|mcp, conversation_id, plan_id, mode, token_id}` + 审查来源 | `catalog/governance/stewardtools/tools.go:94-95` |
| `Writer.Apply` | `{source:"curation", provider_kind: source\|model, provider, basis}`；`batch_id` 写列 | `curation/writer.go:68-72` |
| `ApplyLinks/ApplyArtwork/ApplyLyrics/JoinGroup` | 同上，但 `Kind` 只能 `source` | `curation/external_artwork.go:44-62` |

- 程序层的实际值：`{Kind:"source", Provider:"musicbrainz", Basis:"MusicBrainz 发行 <ID>，依据<步名>"}`（C 设计 `.trellis/tasks/archive/2026-11/11-22-auto-curation-c-program/design.md:212`）。
- 列表筛选：`ListParams{Source, Provider, BatchID}`；`Source=="curation"` 或给了 `Provider` 时加 `origin->>'source'='curation'`（`operations/records.go:117-125,154-164`），与 `operations_provider_idx` 表达式一致（`0058:89`）。
- 前端解码：`source` 只接受 `chat|mcp|curation`，`provider_kind` 只接受 `source|model`；`conversation_id` 不解码（`frontend/src/features/governance/api/decoders.ts:371-380`）。新增 `source` 取值会让前端解码失败。

### 7.2 推断

- 自动整理运行经 `Writer.Apply` 写入时，origin 现成能带的是 `source:curation`、`provider_kind:model`、`provider`、`basis`、`batch_id`，以及 `ctx` 里的 `review_id/review_decision`；会话、方案、待定项的关联要么扩 `Origin`（JSON 字段，前端会忽略未知键），要么通过批次或待定项反查。

## 8. C 建的待定项

### 8.1 表与状态（事实）

`curation_pending_items`（`0062_curation_program.sql:18-35`）：

| 列 | 说明 |
| --- | --- |
| `id` | UUID，默认生成 |
| `target_type` | `CHECK = 'release'` |
| `target_id` | `→ releases ON DELETE CASCADE` |
| `kind` | `release_match` \| `recording_match` |
| `status` | `waiting_model` \| `in_progress` \| `resolved` \| `waiting_human` \| `shelved` |
| `rule_version`、`input_digest` | 与工作状态相同的版本与摘要 |
| `evidence` | JSONB NOT NULL |
| `candidates`、`conflicts` | JSONB，默认 `[]` |
| `resolution` | `^[a-z_]{1,32}$`，可空 |
| `created_at`、`updated_at`、`resolved_at` | — |

- 唯一索引：每个（目标、种类）至多一条未 `resolved` 的（`:34`）；`(status, created_at)` 索引（`:35`）。
- **没有**存放“提出的修改、前后值、审查理由、会话/方案/审查 ID、尝试次数、下次重试时间”的列。
- C 只写 `waiting_model` 与 `resolved`（`curation-contracts.md` 15.1；C 设计 `:65`“D 用到后三种”）。

### 8.2 函数（事实）

- `PendingItem` 结构（`curation/work_states.go:80-88`）。
- `UpsertPendingItem`（`:90-97`）：插入固定 `status='waiting_model'`；冲突时仅当现有行是 `waiting_model` 才更新内容（`:95`），D 处理中或已交人的不动。
- `ResolvePendingItems(ctx,q,targetID,kind,resolution)`（`:99-103`）：只结束 `waiting_model`、`shelved` 的行。
- 调用方：`savePending`（`curation/program/pass.go:444-457`）——匹配成功结束为 `program_matched`；`insufficient_evidence` 不建；否则 upsert，`kind` 由 `m.Mode=="single"` 决定。
- 进度只统计 `waiting_model`（`curation/progress.go:24-26,48`）。
- 没有列出或读取单条待定项的函数与 HTTP 接口（C 设计 `:380`“列表接口 E2 与 D 一起定”）。

### 8.3 JSON 形状（事实）

- `evidence` = `PendingEvidence(e)`（`curation/program/match.go:193-216`）：

```json
{"title":"…","artist":"…","barcodes":["…"],"catalog_number":"…","label":"…","disc_count":1,
 "track_counts":{"1":12},
 "tracks":[{"id":"UUID","title":"…","artist":"…","disc":1,"position":1,"position_inferred":false,
            "duration_seconds":240,"identifiers":{"mb_recording":["…"]},"isrcs":["…"]}]}
```

- `candidates` = `[]Candidate`（`match.go:29-43`）：`{score?, source, external_id, method, title, artist, date, country, status, track_count, barcode, catalog_numbers, release_group_id}`；C 设计写“至多 10 个”（`design.md:252`）。B3 的 `acoustid` 方法带 `score`（父任务 `implement.md` B3 交付记录）。
- `conflicts` = 冲突代码字符串数组。代码全集见 `curation-contracts.md` 15.2（`:394-416`）及 B3 新增的 `acoustid_mismatch`、`acoustid_spread`、`acoustid_sparse`、`title_mismatch`；另有 `search_only`、`too_many`、`ambiguous`、`track_count`、`mixed_release_tags` 等（`match.go`、`match_album.go`、`match_single.go` 的 `uncertain(...)` 调用）。
- 外部文本原样存放，读取方按不可信处理（C 设计 `:254`）。

### 8.4 推断

- D 的“例外”需要在待定项上加列或新表来存提出的修改与审查理由；“采纳时按当时状态重新校验后执行”意味着需要保存可重放的工具输入（`steward_plan_items.input` 有，但随方案与会话存在，删除会话会级联删掉）。
- 待定项与方案项的对应目前只能靠工具输入里的 `release_id` 与待定项的 `target_id` 匹配；一个发行可能有 `release_match` 与 `recording_match` 两条。

## 9. 事件与前端

### 9.1 会话列表与筛选（事实）

- 后端：`GET /api/v1/me/steward/conversations`（`handlers.go:62`；`httpapi/conversations.go:45-70`），只接受 `page`、`page_size`，无筛选。存储 `ListConversations` 只按 `user_id`（`runtime/conversations.go:75-112`）；`CountConversations`（`:322-326`）、`CountUnreadConversations`（`:334-338`）同样。未读计数进入设置响应 `unread_conversation_count`（`httpapi/settings.go:515-524`），前端入口提示点用它（`frontend/src/features/steward/hooks/use-steward-settings.ts:55`）。
- 响应形状 `conversationResponse`（`httpapi/conversations.go:15-34`），列表项另带 `pending_item_count`（`:61-68`）。
- 新建只接受三种模式（`httpapi/conversations.go:72-`；`runtime/conversations.go:65-67`）。

### 9.2 运行事件流（事实）

- SSE：`text/event-stream`，`: ping` 起始与 15 秒心跳，每事件 `id`/`event`/`data`（`httpapi/events.go:241-278`；规范 `steward-runtime-contracts.md` 第 5 节）。
- 接回：`GET …/conversations/{id}/events`，先 `GetConversation(user.ID,id)` 核对归属，再 `Supervisor.Subscribe(id, after)`（`events.go:216-239`）；没有日志返回 204。
- 事件类型：`text`、`tool_started`、`tool_finished`、`usage`、`plan`、`review_started`、`review_finished`、`search`、`citations`、`history_compacted`、`history_gap`、`done`（`runtime/run_types.go:61-79`）。
- 发布不依赖订阅者存在（`supervisor.go:234-247`）。

### 9.3 前端（事实）

- 会话列表：`useConversations` 取最近 100 个（`frontend/src/features/steward/hooks/use-conversations.ts:13-20`）→ `fetchConversations`（`api/endpoints.ts:85-87`）→ `SessionList`（`components/session-list.tsx:68,81`，按今天/本周/更早分组 `:22-34`），挂在整页 `steward-page.tsx:96`。
- 解码：`mode: d.oneOf(["assistant","steward","operator"])`（`api/decoders.ts:103,114`）；`d.object` 忽略未知字段（`frontend/src/shared/api/decode.ts:42-59`）。
- 对话区可切换模式（`components/conversation-view.tsx:118-119`，`ModeMenu`）。
- 整理记录与设置在 `frontend/src/features/curation`（批次 `tier: program|model` 已在解码里，`api/decoders.ts:73`），路由 `/settings/curation`（`frontend/src/app/curation-settings.test.tsx:57`）。

### 9.4 推断：“自动整理会话不混在用户对话里”要动的地方

- 后端：会话加类型标记；`ListConversations`、`CountConversations`、`CountUnreadConversations` 排除它（否则入口提示点会被后台运行点亮）；另给整理记录一个按类型或按批次列会话的读取。
- 前端：`SessionList` 不需要改（数据源已过滤）；自动整理会话的入口在整理记录页（E2 范围）；对话区的模式菜单若允许切换，会把自动整理会话改成 `operator`（见第 12 节）。

## 10. 测试里的受控模型服务

### 10.1 夹具（事实）

| 层 | 夹具 | 位置 |
| --- | --- | --- |
| 运行时单测 | `memoryRunStore`（内存 RunStore）、`fakeProvider{next func(ctx,i,req)([]provider.Event,error)}`、`fakeStream`、`modelDone/modelCall/modelUsage`、`runtimeRegistry` | `steward/runtime/run_test.go:22-159` |
| HTTP 集成（C6/C7） | `newC6API`（合成库与接口）→ `newC7API(t, protocol, role)`：替换注册表与 Runner、装配审查证据读取器、`httptest.NewServer(f.serveModel)`，按请求体有无 `tool_choice` 区分主模型与审查调用；`failure` 字段模拟 500、超时、文本、截断、缺项、多项、外来项、重复、schema、未知键、空理由、多次调用、错工具 | `steward/httpapi/review_c7_integration_test.go:34-200` |
| app 集成 | `newC3AppFixture(t)` + 内联 `httptest` OpenAI 兼容流（主模型发 `correct_release`，审查回 `submit_review`） | `app/steward_review_c7_integration_test.go:20-80` |
| app 层程序循环 | 共用夹具默认 `disableProgram = true`（避免向真实外部服务发请求） | `app/auth_root_operations_integration_test.go:78`；规范 `curation-contracts.md` 15.6 |

### 10.2 审查四条路径的现有测试（事实）

| 路径 | 测试 |
| --- | --- |
| 通过/没通过/转人工（含混合、两种协议、隔离） | `TestPostgreSQLStewardHTTPC7DecisionsProtocolsAndIsolation`（`steward/httpapi/review_c7_integration_test.go:282`） |
| 不可用 | `TestPostgreSQLStewardHTTPC7FailureClosed`（`:399`）、`TestPostgreSQLStewardHTTPC7TimeoutFailsClosed`（`:630`）、`TestPostgreSQLStewardHTTPC7Round2UnavailableLogCategories`（`review_c7_round2_integration_test.go:282`） |
| policy（20/21 项、不可撤销、高风险） | `TestPostgreSQLStewardHTTPC7Policy`（`review_c7_integration_test.go:427`） |
| 重审与人工回退 | `TestPostgreSQLStewardHTTPC7RetryAndManualFallback`（`:564`） |
| 快照绑定 | `TestPostgreSQLStewardHTTPC7SnapshotBinding`（`:689`） |
| 主模型伪造批准 | `TestPostgreSQLStewardHTTPC7MainCannotForgeApproval`（`:782`） |
| 未登记证据工具 | `TestPostgreSQLStewardAppC7Round2UnsupportedEvidence`（`app/steward_review_c7_round2_integration_test.go:20`） |
| 审查来源进 origin | `TestPostgreSQLStewardAppC7ReviewOriginAndEvidence`（`app/steward_review_c7_integration_test.go:20`） |
| 审查调用的严格流与超时 | `TestC7ReviewRejectsInvalidResults`、`TestC7ReviewCallStrictStreamAndDeadline`、`TestC7ReviewCallHonorsShortParentDeadline`（`steward/runtime/review_c7_test.go:15,79,144`） |
| 并发上限 | `steward/runtime/supervisor_c10_test.go:58`（`ErrRunLimit`） |

## 11. 合同条文（与 D 相关）

### `.trellis/spec/backend/agent-and-operation-guidelines.md`

- Mode contract 表（`:32-45`）：steward = “Independent Review Subagent returns structured approval and policy allows it / Execute bounded autonomous operations after review”。
- Review Subagent Contract 稳定规则（`:67-77`）：主代理不能自批；批准绑定提案、证据、策略版本、范围与期限；审查失败对 steward 关闭，不降级。
- Validation And Error Matrix（`:128-143`）：24 小时过期 `plan_expired`；新消息 `superseded`。
- “C7 已实现的审查权威”（`:209-217`）：不可撤销与超过 20 项先转人工；高风险降级；60 秒、不重试；审查来源从后端 Caller 注入 origin。
- **“已决定、尚未实现：后台自动整理”（`:219-243`）**：本节是 D 的合同起点，实现时要改写为已实现：后台运行是 `steward` 运行、不用免确认名单（`:228-229`）；操作者是开启者、用他的端点与密钥、来源由后端注入（`:230-231`）；鉴权先于审批，停止条件，不依赖登录会话（`:232-233`）；字段写工具写自动层、名字与参数不变，对话与 MCP 仍写人工修正（`:234-235`）；策略第 2 版（`:236-238`）；例外持久、采纳按当时状态重校验、不采纳写不再提出（`:239-240`）；每日上限默认 1000 万、可改可不限、审查计入、全实例一次一个、对话不受限（`:241-242`）。

### `.trellis/spec/backend/steward-runtime-contracts.md`

- C10 步骤 1（`:21-85`）：签名（`:27-35`）；“单用户最多 3 个活动运行，同会话不可并发；等待确认已结束，不占活动额度”（`:41`）；取消钩子表（`:47-58`，含登出、停用、改设置、每 60 秒复查 → `CancelUser`）。
- 4.4 会话（`:645-667`）：列表只有分页参数；新建只接受三种模式。
- 4.5 发消息检查顺序（`:671-682`）；4.7 接回与未读（`:694-706`，未读计数进设置响应）。
- 5 SSE（`:723-773`）：`done.reason` 表（`:751-761`）；“每次启动前及后台每 60 秒复查发起会话…凭据失效调用 CancelUser”（`:771-772`）。
- 7 循环（`:862-963`）：常量表（`:866-874`）、结束判定（`:895-904`）。
- 8 出站与密钥（`:965-1002`）：密钥绑定用户与用途 `steward.model`（`:1000`）；解不开 → `key_needs_reentry`、`409 steward_not_configured`（`:1001`）。
- 9 数据表（`:1004-1025`）：“接口只创建 assistant 会话”（`:1025`，与代码已不符，代码接受三种模式）。

### `.trellis/spec/backend/steward-review-contracts.md`

- 2 签名与存储（`:13-35`）：`RunParams.ReviewModel`、`WithReviewEvidence` 端口、`SupportsReviewEvidence` 名单、`SaveReview` 限定方案 pending、`operations.Origin` 的 `review_id/review_decision`。
- 3“调用、校验、执行绑定”（`:76-96`）：第 2 条“与主模型共用用户自己的端点、密钥…60 秒…不重试”；第 5 条“跨请求必须重新审查，不能凭保存的 approved 恢复执行”；第 6 条“不先保存一个能被普通 continue 当作人工确认恢复的 accepted”。
- 系统提示词、证据字段、策略全文（`:98-142`），与代码常量逐字同步的约定。
- 日志与用量（`:166-171`）：usage 存审查记录。
- 4 错误矩阵（`:173-189`）。

### `.trellis/spec/backend/steward-config-contracts.md`

- “身份、确认与来源”（`:139-155`）：`withCurrentStewardUser` 每次调用前重读角色与停用状态（`:143-145`）；配置类 shared 全部 `Revertible=false`（`:146`），在 steward 下被 policy 转人工（`:150-151`）。D 的后台运行同样经过这层包装。

### `.trellis/spec/backend/curation-contracts.md`

- 4 写入 `Writer.Apply`（`:53-93`）；9 开启者（`:165-168`，“开启者失效目前到不了”）；10 错误码（`:170-186`）；15.1 待定项（`:385-386`）；15.3 写入表（`:421-436`，`OnlyEmpty` 的 `occupied`）；15.5 强证据（`:446-452`）；15.6 后台循环（`:454-470`）。
- 第 4 节的 `Applied.Skipped` 说明只列 `suppressed | unchanged`（`:74`），`occupied` 在 15.3 补充。

### ADR

- `docs/architecture/adr/0006-auto-curation-and-sources.md` 第 9 节（`:242-285`）、第 10 节（`:287-299`）、第 15 节验收（`:382-384`）。

## 12. 与“后台无人值守运行”冲突的现有约束

| # | 约束（事实） | 位置 | 冲突点（推断） |
| --- | --- | --- | --- |
| 1 | 运行只能经登录用户的 HTTP 接口启动 | `httpapi/events.go:157`；`handlers.go:67-70,86-113` | 需要新入口 |
| 2 | `Check` 依赖会话 Cookie，失效即取消 | `httpapi/events.go:172-207`；`app/steward.go:166-178` | 后台运行不能用它；ADR 要求不依赖登录会话 |
| 3 | `CancelUser(开启者)` 由登出、撤销会话、改密、改端点或密钥、每 60 秒复查触发 | `app/app.go:255-259`；`httpapi/settings.go:158-160`；`probe.go:122`；`supervisor.go:137-139,305-313` | 开启者登出会取消后台运行（与“不依赖登录会话”相反）；改密钥取消是期望的；后台 `Check` 失效也会连带取消开启者自己的对话 |
| 4 | 每用户 3 个并发按 `userID` 计 | `supervisor.go:91-100` | 后台运行占开启者名额；ADR 要求不占 |
| 5 | 无全局并发、无运行表 | `supervisor.go:32-41` | “全实例一次一个”需要新机制；重启不恢复（C10 D4） |
| 6 | 方案项人工确认只来自登录用户的 decision 接口 | `plans.go:55-61`；`agent-and-operation-guidelines.md:197-203` | 例外的采纳是新的人工确认路径，需另定 |
| 7 | 方案 24 小时过期、新消息作废、每会话一份 pending | `plan_store.go:21`；`messages.go:117-123`；`0054:17` | 例外不能留在方案里（ADR 已定） |
| 8 | 转人工时运行以 `awaiting_confirmation` 结束，会话留着 pending 方案 | `run_review.go:188-193` | 后台批次结束的判定与“方案仍 pending”并存 |
| 9 | 审查不可用不重试，以 `review_unavailable` 结束 | `run_review.go:100-103,123-125`；规范审查合同第 3 节第 2 条 | 第 2 版要求后台有限重试；“跨请求必须重新审查”意味着重试要重新调用 `reviewPlan`（现有重审入口只在 HTTP） |
| 10 | 方案超过 20 项整份转人工 | `review.go:157` | 第 2 版对后台改为按批控制 |
| 11 | `reviewPolicy` 无数据库访问 | `review.go:155` | 强证据转人工需要读外部对应 |
| 12 | 审查证据不含外部资料 | `catalog/stewardtools/review.go:51-139`；`app/steward.go:140-162` | 第 2 版要加外部对应与候选 |
| 13 | 写工具写人工修正，origin 由 `Caller` 拼 | `governance/stewardtools/tools.go:89-100` | 自动整理运行要落自动层 |
| 14 | `Writer.Apply` 的 `Provider` 正则、`Basis ≤ 500`、拒绝 `clear` | `curation/writer.go:40,48-51`；`auto_values.go:25-27` | 模型名与 `basis.note`（≤4096）直接映射会失败 |
| 15 | `sourceSpec` 只接受 `Kind=="source"` | `curation/external_artwork.go:49` | 模型层写外部对应、归组时需放宽 |
| 16 | `operations.actor_id NOT NULL REFERENCES users`；幂等按（操作者, 键） | `0017_*.sql:9,29`；`operations/lookup.go:11-21` | 运行的 `Caller.User.ID` 必须是开启者，否则对账失效 |
| 17 | 会话、方案的 `user_id NOT NULL`，存储层所有读写带 `userID` | `0051:34`；`0054:10`；`runtime/store.go:61-66` | 后台运行只能挂在开启者名下（与 ADR 一致，无冲突） |
| 18 | 会话列表、计数、未读不分类型 | `runtime/conversations.go:75-112,322-338` | 自动整理会话会混进列表并点亮入口提示点 |
| 19 | 会话模式可被 PATCH 改为 `operator` | `runtime/conversations.go:268-305`；前端 `conversation-view.tsx:118-119` | 自动整理会话被改成 operator 后，管理员接着对话时写入直接执行；ADR 要求“模式固定为 steward” |
| 20 | 运行读取开启者本人的记忆并拼进系统提示 | `run.go:120-127` | 后台运行的提示词会带开启者的个人记忆（是否需要，D 自己定） |
| 21 | 每用户外部请求限额（音乐元数据每分钟 20 次）按 `caller.User.ID` | `steward/web/tools.go:92-108`；`limiter.go:9-14` | 后台运行与开启者自己的对话共用名额 |
| 22 | 步与步之间没有外部停止钩子；单次运行上限是常量 | `run.go:17-23,283-290` | “跑完当前这一步后结束”需要新钩子 |
| 23 | 摘要调用用量不落库；审查用量不计入运行上限 | `summary.go:101-105`；`run_review.go:77` | 每日上限统计口径 |
| 24 | `curation_batches` 没有用量列 | `0058:80-86` | ADR 写批次记用量 |
| 25 | 前端 origin 解码 `source` 只接受三值 | `frontend/src/features/governance/api/decoders.ts:372` | 不能新造 `source` 值，除非同步前端 |
| 26 | 规范记“接口只创建 assistant 会话” | `steward-runtime-contracts.md:1025` | 文字过时，非代码约束 |

## D 的最小接入点建议（推断，均未验证）

1. **会话类型**：迁移给 `steward_conversations` 加一列区分“自动整理”（例如 `kind`），`ListConversations`/`CountConversations`/`CountUnreadConversations` 默认排除；`UpdateConversation` 对该类型拒绝改模式。不确定：管理员“接着对话纠正”时这条消息算对话（写人工修正、不计每日上限）还是后台（写自动层、计上限）——ADR 只说“可以接着对话纠正”，没有定。
2. **后台入口**：在 `steward/httpapi`（已持有 `store`、`secrets`、`supervisor`、`runner`）新增一个不依赖 `http.Request` 的启动函数，复用 `configured` 的检查与 `modelProvider`；`Caller{User: guard.EnabledUser(enabledBy), Source: <自动整理值>}`；`Check` 换成读 `curation.Writer.Settings` 的 `Active` 与设置快照比较。调度器照 `curation/program/worker.go` 的形状（单协程、`Options` 注入时钟），由 `app.go` 在 `curationWriter` 与 `supervisor` 都就绪后启动。不确定：调度器放 `curation/...` 还是 `steward/...`，取决于依赖白名单怎么调。
3. **并发**：`Supervisor.Start` 按 `Caller.Source`（或新字段）把后台运行排除出每用户计数，另加“后台至多 1 个”；`CancelUser` 是否跳过后台运行要单独决定（登出不应取消，改端点或密钥应取消）。
4. **写入分层**：在 `app/steward.go:126-134` 的 shared 包装层按来源把 `correct_release` 改走 `curation.Writer.Apply{Source:{Kind:"model", Provider:<规范化后的标识>, Basis:<截断的 note>}, BatchID:<本次运行的 model 批次>, Key: caller.IdempotencyKey}`；`clear` 与 `expected_revision` 的处理要定。`registerSteward` 需要接收 `curationWriter`。
5. **审查第 2 版**：`ReviewPolicyVersion` 升 2；`ReviewEvidenceFacts` 加外部对应与候选（读取放在 `app/steward.go` 的证据读取器，用 `curation.LinksOf` 与待定项的 `candidates`）；强证据规则可让证据读取器返回一个新的“policy 转人工”错误，由 `run_review.go:57-59` 同样处理；20 项规则按来源分支。
6. **例外**：待定项表加列（提出的工具与输入、前后值、审查 ID 与理由、会话 ID）或新表；审查结束后（`run_review.go:136-193` 之后）由后台入口读方案与审查结果写回待定项，状态转 `waiting_human`/`resolved`。
7. **用量**：每日统计可以从 `steward_messages` 与 `steward_reviews` 按会话类型汇总（漏算摘要），或在运行收尾与审查保存时另记一张用量表/批次列；上限检查放在调度器启动前与 `run.go:283-286` 的步前检查处。
8. **审查不可用重试**：调度器在 `done.reason=review_unavailable` 时按退避再次以 `ReviewPlanID` 启动运行（`run.go:137-175` 已支持，只是入口在 HTTP）；达到次数后待定项回 `waiting_model`。

## Caveats / Not Found

- 查不到：后台或定时发起管家运行的入口；运行表；会话类型列；“整理用的模型”设置字段；每日用量统计或上限；读取外部对应、候选或待定项的管家工具；待定项的列表或读取接口；`operations.Origin` 的 `run_id`/待定项字段；批次用量列。
- 迁移 0017 的行号来自 `grep`（`actor_id` 在第 9 行、唯一约束在第 29 行），未通读该文件。
- 冲突代码全集以规范 15.2 与 B3 交付记录为准，未逐一核对每个 `uncertain(...)` 调用的上下文。
- `steward/web` 的音乐元数据工具与程序层共用同一个 `musicbrainz.Client`（`app/steward.go:107-109`、`app/app.go:180,197`），全进程每秒 1 次的限速是否覆盖后台运行的工具调用，未细查 `sources/musicbrainz` 的限速实现。
