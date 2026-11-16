# 管家 C6 设计

## 工具

放在 `catalog/governance/stewardtools`（撤销放在 `operations/stewardtools`），输入与现有 HTTP 写接口的请求体一致，
调用同一批 Executor：

`correct_release`、`change_release_group`、`set_default_release`、`review_release_issue`、`preview_release_claim`（read）、
`claim_release`、`merge_artists`、`preview_artist_split`（read）、`split_artist`、`rename_artist`、`ignore_artist_candidate`、
`revert_operation`。

- `MinRole` 管理员，`Scope` shared；前五类 `Revertible` 为真，艺人四个为假，`revert_operation` 为假。
- `expected_revision`、问题核对与认领的令牌都是输入，来自对应的只读工具；模型不需要也不能伪造幂等键，
  幂等键由后端在方案项（或直接执行的调用）上生成并保存，重试同一项时复用。
- `tool.Definition` 增加 `Preview func(ctx, caller, input) (Preview, error)`：返回目标的显示名、逐字段的旧值与新值、
  摘要一句话。方案落库时调用，结果存进方案项；它只读，不做最终校验（执行时仍由 Executor 校验）。
- 每个 `shared` 工具的输入里有可选的 `basis`：`{"source":"local"|"external"|"mixed","note":"…"}`，只用于展示，
  不参与执行与指纹。

## 来源记录

迁移 0054：`operations` 表加可空列 `origin jsonb`（`{"source":"chat"|"mcp","conversation_id","plan_id","mode","token_id"}`），
`operations.Spec` 加可选的 `Origin`，不参与指纹。现有调用方不传，行为不变。操作列表接口与 `list_operations` 输出该字段；
前端操作记录页显示来源标签。

## 方案与挂起

迁移 0054 同时新增：

- `steward_plans`：`id`、`conversation_id`、`user_id`、`status`（`pending`、`applied`、`declined`、`expired`、`superseded`）、
  `digest`、`expires_at`、时间戳。
- `steward_plan_items`：`id`、`plan_id`、`tool_call_id`、`position`、`tool_name`、`input`、`preview`、`revertible`、
  `idempotency_key`、`decision`（`pending`、`accepted`、`declined`）、`result`、`operation_id`。
- 工具调用状态增加 `awaiting_confirmation`；会话加 `auto_approved_tools text[]`。

流程（`assistant` 模式）：

1. 一步里模型要求的调用按顺序处理：`read`、`personal` 照常执行；`shared` 且不在 `auto_approved_tools` 里的，
   先做鉴权与输入校验、调用 `Preview`，然后记为 `awaiting_confirmation`，不执行。
2. 这一步里只要有等待确认的调用，就把它们合成一份方案落库，发 SSE 事件 `plan`，运行以
   `done.reason = awaiting_confirmation` 结束。`ReleaseConversation` 与重放都不再把 `awaiting_confirmation` 当成中断；
   会话有未处理方案时，重放到这一步为止，不向模型发送缺结果的调用。
3. 摘要：对方案项的 `(tool_name, input 的规范化 JSON, position)` 序列做 SHA-256。
4. `POST /api/v1/me/steward/conversations/{id}/plans/{plan_id}/decision`，请求体
   `{"digest":"…","accepted_item_ids":[…],"auto_approve_tools":[…]}`，响应是 SSE（与继续接口相同）。
   校验顺序：会话归属、方案属于该会话且 `pending`、未过期、摘要一致、会话空闲；任何一项不满足返回对应错误
   （`plan_not_found`、`plan_expired`、`plan_digest_mismatch`、`conversation_busy`），不执行。
   通过后在一个事务里记下每项的取舍，然后逐项执行已接受的项（每项各自的事务，沿用它保存的幂等键），
   把结果写成工具结果；未接受的项结果是 `declined_by_user`。接着按继续接口的方式让循环往下走。
   `accepted_item_ids` 为空等于“不修改”。
5. 用户在有未处理方案时发新消息：方案标为 `superseded`，各项以 `not_executed` 写成工具结果，再追加用户消息。
   过期在读取与确认时判定。
6. `GET` 会话详情带上未处理的方案（供刷新后显示）。

`operator` 模式与 `auto_approved_tools` 命中时：第 1 步直接执行，幂等键存在工具调用上。

确认接口不接受模型输出里的任何字段；能让方案执行的只有这个接口上已登录用户的请求。

## 模式

- 新建会话接受 `mode`；`PATCH` 可以改 `mode`（会话空闲且没有未处理方案时）。`steward` 在 C7 之前返回
  `400 invalid_request`。
- 系统提示词按模式加一句，说明修改会先给用户确认或会直接执行。

## MCP

`shared` 工具对读写令牌直接执行，来源记 `mcp` 与令牌 ID。MCP 工具描述里注明会直接修改曲库。

## SSE 与错误码

- 新事件 `plan`：`{plan_id, digest, expires_at, items:[{id, tool, target, changes:[{field, before, after}], revertible, basis}]}`。
- `done.reason` 增加 `awaiting_confirmation`。
- 新错误码：`plan_not_found`（404）、`plan_expired`（409）、`plan_digest_mismatch`（409）。

## 前端

- `components/plan-card.tsx`：按原型；每项一个勾选框、字段名、旧值（删除线）、新值、依据说明、不可撤销的标注；
  “应用 N 项修改”“不修改”；每种工具一个“这次对话里，<工具的中文名>不再问我”的复选框。旧值新值按文本节点渲染。
- 应用后卡片变成结果列表：每项已执行、失败原因、未选；已执行的可撤销项有“撤销”（调用现有撤销接口）。
- 模式按钮与菜单按原型（本任务两项）；Shift+Tab 只在输入框聚焦时轮换；Operator 的说明写明
  “不询问，直接执行；外部资料里的内容也可能促成修改”。
- `run-store` 处理 `plan` 事件与 `awaiting_confirmation`；确认走新的流式请求。
- 操作记录页显示来源。

## 测试

- 后端：每个工具的成功、越权、revision 冲突；方案的生成、摘要、过期、作废、逐项取舍、部分失败；
  确认接口的各项拒绝；模式切换；`auto_approved_tools`；来源记录；MCP 两种令牌；重放时不出现缺结果的调用；
  注入场景（A10）。
- 前端：卡片的勾选与提交、结果显示、刷新后恢复、模式切换与快捷键。
