# Music Steward And Operation Guidelines

## Status And Scope

This document records the long-term product contract for ROOMusic's Music
Steward and its durable operation model. Core 0 defines the contracts and the
directory-management example.
当前 C6 已实现模型调用、用户确认与 operator 直接执行；C7 已实现独立审查与 steward 模式（见[审查合同](./steward-review-contracts.md)）；C8 联网、C9 应用配置与诊断、C10 记忆与后台运行已实现。物理文件整理、部署层操作与应用容器内 shell 不做（父任务 S5–S7）。

The system has one product Agent, **Music Steward**, with three execution modes:
`assistant`, `steward`, and `operator`. These are approval modes, not separate
business authorities. The deterministic scanner and parser are ordinary program
logic, not Agents.

## Authority And Execution Flow

All modes eventually submit a typed operation command to the backend authority:

```text
后端身份、工具查找与鉴权
  -> 按模式取得审批（assistant 的模型不能批准自身）
  -> 类型化方案与命令
  -> target, scope, revision, and physical-safety validation
  -> transactional or staged executor
  -> Operation Journal + structured log
```

The Agent, model, Review Subagent, browser, and CLI are callers. None of them
may write private tables, open a database connection, access an arbitrary host
path, or execute an unregistered tool.

### Mode contract

| Mode | Approval requirement | Allowed authority |
| --- | --- | --- |
| `assistant` | Current user explicitly approves a dangerous Change Set | Prepare plans, explain evidence, and execute approved operations |
| `steward` | Independent Review Subagent returns structured approval and policy allows it | Execute bounded autonomous operations after review |
| `operator` | No user confirmation or AI review step | 所有用户可选的直接执行；权限仍由各工具决定 |

operator 对所有用户开放，模式只决定审批路径；鉴权必须先于审批，各模式共用相同工具权限。
Operator direct execution skips approval only. It does not skip backend
authorization, allowlists, argument validation, target checks, revision or
idempotency checks, journaling, logging, or an explicit irreversible marker.
There is no unrestricted-shell or arbitrary-SQL capability in the product
contract.

## Review Subagent Contract

The Review Subagent is an independent reviewer role. It receives a bounded
proposal and AI-safe evidence projection, not raw database rows, secrets, audio
bytes, or unrestricted paths. It returns a strict result such as:

```json
{
  "decision": "approved",
  "risk": "low",
  "scope_digest": "sha256:...",
  "reasons": ["target is unambiguous", "inverse action is available"],
  "requires_human_confirmation": false
}
```

C7 的实际线上 schema、完整提示词、证据摘要和策略版本 1 见[独立审查合同](./steward-review-contracts.md)。
上面的单项示例只表达概念，实际调用是 `submit_review({items:[{item_id,decision,risk,reasons}]})`，
来源由后端生成；审查模型使用用户自己的端点与密钥，且请求不含主对话历史。

稳定规则如下：

- The main Agent cannot approve its own proposal by setting an approval field.
- Approval is bound to the proposal, target, evidence summary, policy version,
  scope, and expiry; changes require a new review.
- `approved`, `rejected`, and `needs_human_confirmation` are distinct outcomes.
- The backend re-checks current authorization and physical constraints after
  review and before execution.
- Review approval never grants a capability that the backend policy denies.
- A reviewer failure or unavailable reviewer fails closed for Steward mode; it
  does not silently downgrade to Operator mode.

## Change Set And Recovery Contract

Every persistent management operation has a durable Change Set and Operation
Journal. At minimum, the operation records:

- `operation_id` and `change_set_id`;
- actor, session/request, mode, and optional Agent/reviewer run identifiers;
- registered tool and canonical target scope;
- planned, running, succeeded, failed, or partially-failed status;
- expected resource revision and idempotency key/fingerprint;
- before/after state or a checkpoint reference;
- rollback availability, inverse action, and irreversible status.

Use a typed inverse operation. Do not store only natural-language instructions
such as "undo the previous change". A supported operation is reversible only if
the system has enough recorded state to execute its inverse and can detect a
later conflicting change.

Core 0's first supported Change Set is library-root add, disable, and restore.
Scanner-derived catalog state uses scan runs and source observations rather than
one Change Set per discovered field. Full file checkpoints, quarantine, tag
write-back recovery, and physical file operations require a later reviewed
capability.

## Tool Registration Rules

Tools are named, typed capabilities owned by the backend. A tool definition
must specify:

- input and output schema;
- minimum role and allowed mode;
- target scope and resource limits;
- whether it changes state;
- whether it is reversible or permanently irreversible;
- transaction/staging behavior;
- idempotency and revision requirements;
- error classes and correlation fields.

Examples of future capability names are `library_search`,
`release_evidence_query`, `metadata_overlay_apply`, `file_quarantine`, and
`tag_write_back`. Names are not permission: each call is authorized again using
the current principal and resource state.

Never expose a generic `run_shell`, `run_sql`, or `operate_path` escape hatch.
If a future Operator feature needs a system action, add a narrow named tool with
bounded arguments and an explicit recovery contract.

## Validation And Error Matrix

| Condition | Required behavior |
| --- | --- |
| assistant 未批准的 shared | 生成 pending 方案并挂起 awaiting_confirmation；不调用 Executor |
| Steward without independent review approval | Reject with `review_required` or `review_unavailable`; no downgrade |
| 非管理员选择 operator | 允许；shared 工具仍不可见，直接调用 permission_denied |
| Unknown tool or unsupported mode/tool pair | Reject with `capability_denied` |
| 目标版本过期 / 方案摘要改变 | revision_conflict / 409 plan_digest_mismatch |
| 方案不存在、他人方案或已处理 | 404 plan_not_found |
| 方案已过 24 小时 | 409 plan_expired，项记 not_executed |
| 用户发新消息 | pending 方案 superseded，项记 not_executed |
| 用户未选择某项 | declined_by_user，其他已选项独立执行 |
| 新建或切换到 steward | C7 已支持；切换仍要求会话空闲且无 pending 方案 |
| Same idempotency key and same canonical request | Return the recorded result without repeating side effects |
| Same idempotency key and different request | Reject with `idempotency_conflict` |
| Executor fails before commit/stage completion | Record failure and recover according to tool contract |
| Permanent purge or equivalent irreversible action | Require explicit Operator-only tool policy and record `rollback_available=false` |

## Good, Base, And Bad Cases

- **Good:** Steward proposes a bounded metadata operation, the independent
  reviewer approves the exact digest, the backend re-checks the target revision,
  executes the registered inverse-capable tool, and records the result.
- **Base:** Operator directly runs an administrator-approved tool, the tool
  validates its arguments and scope, and the journal records that no AI review
  was required.
- **Bad:** The model emits `approved: true` together with a shell command and
  the worker executes it without a backend capability check.

## Required Tests

- Unit-test mode/approval matrix and exhaustive tool policy decisions.
- Test that Assistant cannot execute a dangerous proposal before user approval.
- Test that Steward cannot self-approve and fails closed when the reviewer is
  unavailable or returns `needs_human_confirmation`.
- Test that Operator bypasses approval but still enforces admin, tool, scope,
  revision, idempotency, and irreversible-action rules.
- Integration-test one Change Set through success, failure, repeated request,
  idempotency conflict, stale revision, and inverse-action paths.
- Assert that journal records and logs contain correlation IDs but no secrets,
  prompt bodies, raw provider responses, SQL, or unrestricted paths.
- When file tools are introduced, add filesystem integration tests for path
  containment, content hash verification, interruption, quarantine, restore,
  and permanent purge markers.

## Wrong Versus Correct

### Wrong

```text
worker-ai -> shell("mv " + model_output.source + " " + model_output.target)
```

This gives the model physical authority, has no bounded scope, and cannot
reliably recover from partial execution.

### Correct

```text
Steward -> Review Subagent -> typed Change Set
        -> backend tool authorization
        -> bounded move tool(source resource ID, target relative name)
        -> checkpoint/hash verification
        -> journaled result and typed inverse action
```

The implementation may use a sidecar or another model adapter later, but the
authority and recovery contract does not move out of the Go backend.


## C6 的用户确认边界

确认只来自已登录用户的 `POST /api/v1/me/steward/conversations/{id}/plans/{plan_id}/decision`：
`{digest,accepted_item_ids,auto_approve_tools}`。工具输入中的展示依据、模型文本或 `approved` 字段均无执行权。
后端绑定规范化内容摘要与 24 小时有效期，在确认事务中记录每项取舍；随后逐项经 `operations.Run` 校验 revision、幂等并记录来源。
会话免确认工具名也只在该事务保存。每项业务失败不影响其他项，中断恢复复用保存的幂等键。
MCP 读写令牌直接执行 shared，仍受管理员权限限制；只读令牌不可见且不可调用 personal/shared。

正确：成员切到 operator 后继续操作本人播放，但仍不能修改共享曲库。基线：管理员 assistant 提案等待本人勾选。
错误：因 operator 模式给成员额外权限，或将工具参数中的批准字段传入执行许可。测试须覆盖这两类越权、逐项失败、摘要篡改与过期。
具体表、事件与字段见[运行时合同](./steward-runtime-contracts.md#c6-共享写入与确认合同)。

## C7 已实现的审查权威

- steward 模式不使用 assistant 的 auto_approved_tools。不可撤销项和超过 20 项的整份方案先转人工，不送审；
  high 风险批准在返回后降为人工确认。`ReviewPolicyVersion = 1`。
- 独立调用只提供 submit_review 并强制调用，60 秒超时，不重试；任何错误或结论集合不一致均失败关闭。
- 审查落库后，执行前重读方案和证据并校验摘要、当前审查与期限；审批不豁免工具鉴权、revision 或业务约束。
- 审查来源从后端 Caller 注入 operations origin，不从模型参数读取，不改变操作幂等指纹；同键重放保留原来源。
- 转人工只展示 pending 项；拒绝、已执行项不被人工确认或失效处理覆盖；审查批准不作为普通 continue 的恢复授权。
- 日志只记录标识、模型、耗时、用量、状态和结论计数，不记录消息、工具输入输出或理由正文。

## 已实现的后台自动整理

自动整理调度器以开启者的管理员身份启动独立的 `steward` 运行，使用其端点、密钥和审查配置。后台白名单逐次生效，匹配与字段写入受本批待定项和独立审查约束。批准的自动操作记录模型来源及批次。需要人工决定的提案成为持久例外，管理员采纳时重新预览，并以本人身份执行。个人对话与 MCP 保留各自的权限和落层规则。

后台运行使用全实例唯一名额，主模型、摘要和审查共同计入按服务器日期计算的额度。会话、停止条件、工具、落层、例外和接口见[自动整理合同第 17 节](./curation-contracts.md#17-d模型后台判断)。
