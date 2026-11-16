# Research: 两种模型协议的适配器事实（OpenAI 兼容 Chat Completions / Anthropic Messages）

- **Query**: 自己实现模型调用循环（Go 标准库 net/http + 自写 SSE 解析），需要两种协议在请求、流式响应、工具结果回传、模型列表、错误、探测上的事实，以及本地 OpenAI 兼容服务的差异
- **Scope**: external（官方文档；个别处用官方仓库源码补充，已标注）
- **Date**: 2026-11-16（下文所有链接均于该日抓取核对）

标注约定：

- 「文档」= 官方文档页面原文可见。
- 「源码」= 官方仓库源码可见、文档未写，可能随版本变化。
- 「未核实」= 查过列出的页面但没找到依据，没有补全猜测。

---

## 1. OpenAI 兼容：Chat Completions

官方文档现已迁到 `developers.openai.com`（`platform.openai.com/docs/...` 会跳转；页面 URL 后加 `.md` 可取 Markdown）。

### 1.1 请求

- 路径：`POST {base}/chat/completions`，官方 `base` 为 `https://api.openai.com/v1`。
  [文档：Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
- 认证：`Authorization: Bearer <key>`。多组织/项目时可选 `OpenAI-Organization`、`OpenAI-Project`。**没有必需的版本头**（REST 版本固定为 `v1`，在路径里）。
  [文档：API Overview / Authentication](https://developers.openai.com/api/reference/overview)
- 请求头总大小保持在 64 KiB 以内。（同上）
- 工具定义的 JSON Schema 放在 `tools[].function.parameters`；`tools[].type` 固定 `"function"`。`function.name` 只允许 `a-z A-Z 0-9 _ -`，最长 64。省略 `parameters` 表示无参数。`function.strict` 可选，默认 `false`。
- `tool_choice`：`"none"` / `"auto"` / `"required"`，或 `{"type":"function","function":{"name":"..."}}` 强制某个工具。有工具时默认 `auto`。
- `parallel_tool_calls`：布尔，默认 `true`。
- 输出上限：`max_completion_tokens`（含推理 token）；`max_tokens` 已标记弃用，且与 o 系列模型不兼容。
- 流式：`stream: true`；`stream_options` 只在 `stream: true` 时设置。
- 系统提示是 `messages` 里的一条消息；官方示例里角色用 `developer`（`system` 仍在角色枚举里）。

最小的带工具流式请求体：

```json
{
  "model": "<model-id>",
  "stream": true,
  "stream_options": {"include_usage": true},
  "messages": [
    {"role": "user", "content": "What's the weather like in Paris today?"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get current temperature for a given location.",
        "parameters": {
          "type": "object",
          "properties": {"location": {"type": "string"}},
          "required": ["location"]
        }
      }
    }
  ]
}
```

### 1.2 流式响应

[文档：Chat Completions streaming events](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events)

- 只有 `data:` 行，**没有 `event:` 行**（data-only SSE）。每个 `data:` 是一个 `chat.completion.chunk` JSON；流以字面量 `data: [DONE]` 结束（不是 JSON）。
- 块结构：`{id, object:"chat.completion.chunk", created, model, choices:[{index, delta, finish_reason, logprobs}], usage?, system_fingerprint?, obfuscation?}`。同一次响应的所有块 `id` 相同。
- 文本增量：`choices[0].delta.content`（字符串，可为 `null` 或缺失）。首块通常是 `delta: {"role":"assistant","content":""}`。拒绝文本在 `delta.refusal`。
- 工具调用：`choices[0].delta.tool_calls[]`，元素为 `{index, id?, type?, function?: {name?, arguments?}}`。
  - `id`、`type`、`function.name` **只在该调用的第一个增量里出现**，之后为 `null`/缺失。
  - `function.arguments` 是 **分片的 JSON 字符串**，按 `index` 分组做字符串拼接；首块的 `arguments` 常为 `""`。
  - 一次响应里的并行调用靠 `index` 区分（不是靠 `id`，后续分片没有 `id`）。
  - 官方的累积写法：按 `index` 建表，`id`/`type`/`name` 取首次出现的值，`arguments` 逐块追加。
    [文档：Function calling → Streaming（页面切到 Chat Completions 模式）](https://developers.openai.com/api/docs/guides/function-calling)
  - 文档原文提醒：模型生成的 `arguments` 不保证是合法 JSON，也可能包含 schema 里没有的参数，调用前要校验。
- 结束原因 `choices[0].finish_reason`（中间块为 `null`）：
  - `stop`：自然结束或命中 stop 序列
  - `length`：达到 token 上限
  - `tool_calls`：模型调用了工具
  - `content_filter`：内容被过滤
  - `function_call`：已弃用的旧式函数调用
- 用量：**默认不返回**。请求里加 `stream_options: {"include_usage": true}` 后：
  - 所有块都带 `usage` 字段但值为 `null`；
  - 在 `data: [DONE]` 之前多出一个块，`choices` 为 **空数组**，`usage` 为整次请求的统计（`prompt_tokens`、`completion_tokens`、`total_tokens`，以及 `prompt_tokens_details.cached_tokens`、`completion_tokens_details.reasoning_tokens` 等明细）。
  - 流被中断或取消时可能收不到这个块。
  - 解析时必须允许 `choices` 为空，不能直接取 `choices[0]`。
- `obfuscation`：默认会在增量事件上附带一个随机填充字段，`stream_options.include_obfuscation: false` 可关闭；解析时忽略即可。

### 1.3 把工具结果送回去

[文档：Create chat completion → messages](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)

- 助手消息：`{"role":"assistant","content": <string|null>, "tool_calls":[{"id":"call_...","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}}]}`。有 `tool_calls` 时 `content` 可省略或为 `null`。`arguments` 是 **字符串**，不是对象。
- 每个调用对应一条工具消息：`{"role":"tool","tool_call_id":"call_...","content":"<string>"}`。`content` 是字符串或只含 `text` 类型的内容块数组。
- **没有专门的出错标志字段**。工具执行失败只能把错误信息写进 `content` 字符串。（文档的工具消息 schema 只有 `content`、`role`、`tool_call_id` 三个字段。）

### 1.4 列出模型

[文档：List models](https://developers.openai.com/api/reference/resources/models/methods/list)

- `GET {base}/models`，同样的 Bearer 认证。
- 响应：`{"object":"list","data":[{"id","object":"model","created","owned_by","shutdown_date"}]}`。
- 分页：参考页没有列任何查询参数或游标字段，即 **官方接口不分页**。
- 模型对象里 **没有能力字段**，无法从列表判断是否支持工具调用。

### 1.5 错误

- 错误体：`{"error": {"message": string, "type": string, "param": string|null, "code": string|null}}`。
  [官方 OpenAPI 仓库 `openai/openai-openapi` 的 `Error` / `ErrorResponse` schema](https://github.com/openai/openai-openapi/blob/manual_spec/openapi.yaml)（文档页面只在各节零散提到 `error.code`、`error.type`）
- [文档：Error codes](https://developers.openai.com/api/docs/guides/error-codes) 列出的状态：
  - `401`：Invalid Authentication / Incorrect API key provided / 不属于任何组织 / IP 不在允许名单
  - `403`：所在国家或地区不支持
  - `429`：请求过快（`type: rate_limit_error`，`code: slow_down` 等）；额度或消费上限类也是 429（`code` 为 `credit_balance_exhausted`、`organization_spend_limit_exceeded` 等，`type` 可能仍是 `insufficient_quota`），这类重试无效
  - `500`：服务端错误
  - `503`：模型过载（`type: service_unavailable_error`，`code: server_is_overloaded`）
- 限流：`429` 与 `503` 可能带 `Retry-After`（秒，按最小等待值处理）；没有时用指数退避加抖动。响应头另有 `x-ratelimit-*` 与 `x-request-id`。
  [文档：Rate limits](https://developers.openai.com/api/docs/guides/rate-limits)
- 模型不存在：文档提到错误码 `model_not_found`（Rate limits 页“Model availability”一节）。**对应的 HTTP 状态未核实**（查了 error-codes、rate-limits、chat create 三页，都没写状态码）。
- 模型不支持工具调用：**未核实**（上述三页没有描述这种错误的状态或错误码）。
- 流中途出错：**未核实**。查了 streaming-events 参考页、`streaming-responses` 指南（该指南讲的是 Responses API 的 `error` 事件，不适用于 Chat Completions）和 error-codes 页，Chat Completions 流中错误的线上格式官方没有写。
  - 旁证（非 OpenAI 官方）：OpenRouter 文档规定它在 Chat Completions 流里用一个带顶层 `error: {code, message, metadata?}` 字段的 `chat.completion.chunk` 表示流中错误，且此时 HTTP 状态已是 200。[OpenRouter：Errors and debugging → Streaming error formats](https://openrouter.ai/docs/api/reference/errors-and-debugging)

### 1.6 探测工具调用

文档能支撑的事实：

- `tool_choice: "required"` 让模型必须调用工具；指定函数名则强制调用该工具。配一个无参数或单参数的工具、极短的用户消息和较小的 `max_completion_tokens`，一次请求即可判断：响应里出现 `tool_calls` 且 `finish_reason` 为 `tool_calls`（非流式看 `choices[0].message.tool_calls`）。
- 列表接口不带能力信息，不能代替这次请求。
- 探测用非流式还是流式文档没有倾向；但运行时走的是流式路径，流式工具调用是否可用只有流式请求能确认（本地服务在这一点上各不相同，见第 3 节）。
- `tool_choice: "required"` 在本地服务上不一定被支持（Ollama 明确不支持 `tool_choice`，见 3.1）。

---

## 2. Anthropic：Messages

文档在 `platform.claude.com/docs`（URL 后加 `.md` 取 Markdown）。

### 2.1 请求

- 路径：`POST https://api.anthropic.com/v1/messages`。
  [文档：API overview](https://platform.claude.com/docs/en/api/overview)
- 请求头（同页表格）：
  - `Authorization: Bearer <key>` —— 必需，除非设置了 `x-api-key`
  - `x-api-key: <key>` —— 文档称为“legacy fallback，仍然支持”；两个头任选其一
  - `anthropic-version` —— **必需**
  - `content-type: application/json` —— 必需
  - `anthropic-workspace-id` —— 只有密钥没绑定到单个工作区时才需要
- 版本：当前最新是 **`2026-10-21`**，之前是 `2023-06-01`。[文档：API versions](https://platform.claude.com/docs/en/api/versioning)
  - `2026-10-21` 的 Messages 变更：请求里带 `temperature`、`top_p`、`top_k` 中任意一个都返回 400（即使是默认值）；`assistant` 消息里直接放 `document` 块返回 400；`user` 消息里带非空 `citations` 的 `text` 块返回 400。
    [文档：Migrating to API version 2026-10-21](https://platform.claude.com/docs/en/api/migration-2026-10-21)
  - 迁移文档有一句“依赖非默认采样值的请求可以留在 `2023-06-01`”，说明旧版本目前仍可用；**旧版本的停用日期未核实**（versioning 页没写）。
  - 版本策略允许新增流事件类型、新增枚举值、新增错误类型；解析必须容忍未知值。
- 必需的请求体字段：`model`、`max_tokens`、`messages`。系统提示是顶层 `system` 字段，不是消息。
  [文档：Create a Message](https://platform.claude.com/docs/en/api/messages/create)
- 工具定义在顶层 `tools[]`，每项 `{name, description, input_schema}`，**JSON Schema 放在 `input_schema`**。`name` 须匹配 `^[a-zA-Z0-9_-]{1,128}$`。
  [文档：Define tools](https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools)
- `tool_choice`：`{"type":"auto"}`（默认）、`{"type":"none"}`、`{"type":"any"}`、`{"type":"tool","name":"..."}`。
  - **Claude Fable 5.2 / Mythos 5.2 / Opus 5.5 / Sonnet 5.5 / Fable 5.1 / Mythos 5.1 不支持强制工具调用**：`any` 与 `tool` 返回 400 `invalid_request_error`，消息为 `tool_choice: type "tool" and "any" are not supported for this model.`
    [文档：Errors → Forced tool use not supported](https://platform.claude.com/docs/en/api/errors#common-validation-errors)
  - 关闭并行调用：`tool_choice: {"type":"auto","disable_parallel_tool_use": true}`（在 `tool_choice` 对象里，不是顶层参数）。
    [文档：Parallel tool use](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use)
- 这些模型以及 4.6 之后的模型不接受助手消息预填：对话必须以 `user` 消息结尾，否则 400。（Errors 页“Prefill not supported”）

最小的带工具流式请求：

```http
POST /v1/messages
Authorization: Bearer <key>
anthropic-version: 2026-10-21
content-type: application/json

{
  "model": "<model-id>",
  "max_tokens": 1024,
  "stream": true,
  "tools": [
    {
      "name": "get_weather",
      "description": "Get the current weather in a given location",
      "input_schema": {
        "type": "object",
        "properties": {"location": {"type": "string"}},
        "required": ["location"]
      }
    }
  ],
  "messages": [{"role": "user", "content": "What is the weather like in San Francisco?"}]
}
```

### 2.2 流式响应

[文档：Streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)

- **具名事件**：每条有 `event: <name>` 行和 `data: <json>` 行，`data` 里的 `type` 与事件名一致。**没有 `data: [DONE]`**。
- 事件顺序：
  1. `message_start`：带一个 `content` 为空的 `message` 对象（含 `id`、`model`、`usage`）
  2. 若干内容块，每块依次是 `content_block_start` → 若干 `content_block_delta` → `content_block_stop`；每个事件带 `index`，对应最终 `content` 数组的下标
  3. 一个或多个 `message_delta`（顶层变化：`delta.stop_reason`、`delta.stop_sequence`、`usage`）
  4. `message_stop` —— **流结束标志**
- `ping` 事件可能出现在任意位置，忽略。
- 有些块整块到达、没有 delta（`redacted_thinking`、服务端工具结果等）。
- 文本增量：`content_block_start` 的 `content_block` 为 `{"type":"text","text":""}`，之后 `content_block_delta` 的 `delta` 为 `{"type":"text_delta","text":"..."}`。
- 工具调用：
  - `content_block_start` 的 `content_block` 为 `{"type":"tool_use","id":"toolu_...","name":"get_weather","input":{}}` —— **调用 ID 与名称在这里一次给全**。
  - 参数通过 `content_block_delta` 的 `{"type":"input_json_delta","partial_json":"..."}` 到达，是 **分片的 JSON 字符串**；按同一 `index` 拼接，到 `content_block_stop` 时解析成对象。文档示例里第一片是空字符串 `""`。
  - 默认情况下服务端会把每个键值缓冲完整再发出，所以工具参数的增量之间可能有停顿。逐字符流式要在工具定义上开 `eager_input_streaming`，开启后服务端不再校验输入，累积结果可能不完整或不合法。
  - 一次响应里的并行调用是 **多个 `tool_use` 内容块，各有自己的 `index` 与 `id`**；块是顺序到达的（一块 stop 之后下一块才 start）。
  - 无参数工具时累积的 `partial_json` 为空串应如何处理：**未核实**（文档只说“最终的 `input` 一定是对象”，没写空串的情形）。
- 思考块：`content_block_start` 类型为 `thinking`，增量为 `thinking_delta`（字段 `thinking`），`content_block_stop` 之前有一条 `signature_delta`（字段 `signature`）。新模型默认开启思考且默认 `display: "omitted"`，此时 `thinking` 文本为空串但块和签名照常出现。
  [文档：Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking)
- 用量（不需要任何额外请求选项）：
  - `message_start.message.usage`：`input_tokens`（以及 `cache_creation_input_tokens`、`cache_read_input_tokens`）和一个很小的初始 `output_tokens`。
  - `message_delta.usage`：`output_tokens` 等，**是累计值，不是增量**（文档有 Warning 强调）。有些响应的 `message_delta.usage` 也会带 `input_tokens` 与缓存字段。
  - 输入总量 = `input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens`。（Create a Message 参考页 `usage` 说明）
- 结束原因在 `message_delta.delta.stop_reason`（`message_start` 里为 `null`）。取值：
  [文档：Stop reasons and fallback](https://platform.claude.com/docs/en/build-with-claude/handling-stop-reasons)
  - `end_turn`：自然结束
  - `max_tokens`：达到 `max_tokens`
  - `stop_sequence`：命中 `stop_sequences`
  - `tool_use`：模型在调用工具
  - `pause_turn`：服务端工具循环达到迭代上限（只用客户端工具时不会出现）
  - `refusal`：模型拒绝回答（HTTP 仍是 200，详情在 `stop_details`）
  - `model_context_window_exceeded`：输出填满了上下文窗口，按截断处理

### 2.3 把工具结果送回去

[文档：Handle tool calls](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)

- 没有 `tool` 角色。工具调用在 `assistant` 消息的内容块里，工具结果在 **`user` 消息** 的内容块里。
- 助手消息：把上一次响应的 `content` 数组 **原样** 放回，例如
  `{"role":"assistant","content":[{"type":"text","text":"..."},{"type":"tool_use","id":"toolu_01...","name":"get_weather","input":{"location":"San Francisco, CA"}}]}`。`input` 是 **对象**，不是字符串。
- 响应里若有 `thinking` / `redacted_thinking` 块，回传工具结果时 **必须把它们完整、不改动地一起带回**（含 `signature` / `data`）。文档原文：“when you return tool results, you must pass the thinking blocks from the assistant message back to the API, complete and unmodified”。流式解析因此要保留 `thinking` 文本和 `signature_delta` 的值。
  [文档：Thinking → Preserving thinking blocks](https://platform.claude.com/docs/en/build-with-claude/thinking#preserving-thinking-blocks)
- 工具结果消息：`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01...","content":"15 degrees"}]}`。`content` 可以是字符串或内容块数组，可省略。
- 并行调用的所有结果放在 **同一条 `user` 消息** 里；`tool_result` 块必须排在该消息的最前面，前面放文本会 400，文本只能放在后面。
- 工具执行出错：同样的 `tool_result` 块加 **`"is_error": true`**，`content` 写错误信息。参数不合法时也用这种方式回传，模型会重试。

### 2.4 列出模型

[文档：List Models](https://platform.claude.com/docs/en/api/models/list)

- `GET /v1/models`，头与 Messages 相同（参考页把 `anthropic-version` 标为 optional，但 API overview 的头表写的是必需）。
- 查询参数：`limit`（默认 20，范围 1–1000）、`after_id`、`before_id`、`lifecycle`（`active`/`deprecated`/`retired`，省略时返回 active 与 deprecated）。
- 响应：`{"data":[ModelInfo...],"has_more":bool,"first_id":string,"last_id":string}`。翻页：`has_more` 为真时用 `after_id=<last_id>` 取下一页。新发布的模型排在前面。
- `ModelInfo` 字段：`id`、`type:"model"`、`display_name`、`created_at`、`lifecycle`、`line`、`max_input_tokens`、`max_tokens`、`deprecated_at`、`retires_at`、`capabilities`。
- `capabilities` 的键（示例里可见）：`batch`、`citations`、`code_execution`、`context_management`、`effort`、`image_input`、`pdf_input`、`server_tools`、`structured_outputs`、`thinking`。**没有表示“客户端工具调用”的键。**

### 2.5 错误

[文档：Claude API errors](https://platform.claude.com/docs/en/api/errors)

- 错误体：`{"type":"error","error":{"type":"<error_type>","message":"..."},"request_id":"req_..."}`。响应头里也有 `request-id`。
- 状态与类型：
  - `400 invalid_request_error`：请求格式或内容有问题；文档说其他未列出的 4xx 也可能用这个类型；达到自设消费上限也是 400
  - `401 authentication_error`：密钥有问题（格式错、吊销、过期）
  - `402 billing_error`
  - `403 permission_error`：密钥无权使用该资源
  - `404 not_found_error`：资源不存在（文档原话是“检查端点路径和 URL 里的资源 ID”）
  - `409 conflict_error`
  - `413 request_too_large`（Messages 上限 128 MB，由 Cloudflare 在到达 API 前返回）
  - `429 rate_limit_error`：限流、达到层级月度消费上限
  - `500 api_error`；`504 timeout_error`（`timeout_error` 也可能配其他状态码）
  - `529 overloaded_error`
- 限流：`retry-after` 头（秒）；**不是每个 429 都带**，消费上限的 429 一定不带且重试无效。另有 `anthropic-ratelimit-*` 头。
  [文档：Rate limits](https://platform.claude.com/docs/en/api/rate-limits)
- 模型不存在：文档没有专门条目，**具体状态与消息未核实**（查了 errors、models list、messages create 三页；按上面的通用表最接近的是 `404 not_found_error`，但文档没有明说 `model` 字段写错时返回它）。
- 不支持工具调用：文档没有这类错误；与工具相关、文档明确写出的 400 只有“强制工具调用不被该模型支持”（见 2.1）。
- **流中途出错**：HTTP 已经是 200，之后在流里收到一个具名事件：
  ```
  event: error
  data: {"type": "error", "error": {"type": "overloaded_error", "message": "Overloaded"}}
  ```
  （Streaming → Error events；errors 页也指向这里）
- 流中断后的恢复：`tool_use` 与 `thinking` 块不能部分恢复，只能从最近的文本块续写。（Streaming → Error recovery）

### 2.6 探测工具调用

文档能支撑的事实：

- 模型列表的 `capabilities` 不含客户端工具调用这一项，不能代替实际请求。
- `tool_choice: {"type":"any"}` 在上面列出的新模型上直接 400，**不能用强制调用做通用探测**；通用可行的是 `tool_choice` 省略（`auto`）加一句明确要求调用该工具的用户消息，看响应是否有 `tool_use` 块且 `stop_reason` 为 `tool_use`。
- `max_tokens` 必填，可以给得较小；但新模型思考默认开启，思考也占输出 token，`max_tokens` 太小会以 `stop_reason: "max_tokens"` 结束而没有工具调用。合适的最小值 **未核实**（文档没给数）。
- `POST /v1/messages/count_tokens` 接受 `tools`，但它只计数不生成，不能证明模型会发出工具调用；是否计费 **未核实**（本次没读 token-counting 页）。

---

## 3. 本地的 OpenAI 兼容服务与官方接口的差异

### 3.1 Ollama

来源：[OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)、[Authentication](https://docs.ollama.com/api/authentication)、[Errors](https://docs.ollama.com/api/errors)、[Tool calling](https://docs.ollama.com/capabilities/tool-calling)；源码为官方仓库 `ollama/ollama` main 分支的 [`openai/openai.go`](https://github.com/ollama/ollama/blob/main/openai/openai.go)、[`middleware/openai.go`](https://github.com/ollama/ollama/blob/main/middleware/openai.go)、[`server/routes.go`](https://github.com/ollama/ollama/blob/main/server/routes.go)、[`server/images.go`](https://github.com/ollama/ollama/blob/main/server/images.go)。

| 项 | 事实 | 依据 |
| --- | --- | --- |
| 地址 | 本地 `http://localhost:11434/v1/`；云端直连 `https://ollama.com/v1` | 文档 |
| 密钥 | 本地 **不需要认证**；OpenAI 客户端要求填 key 时随便填，Ollama 忽略。云端要 `Authorization: Bearer` | 文档 |
| `tools` | 支持 | 文档 |
| `tool_choice` | **不支持**（支持字段清单里未勾选） | 文档 |
| `stream_options.include_usage` | 支持 | 文档 |
| `max_tokens` | 支持；清单里 **没有 `max_completion_tokens`** | 文档 |
| `n`、`logit_bias`、`user`、图片 URL | 不支持 | 文档 |
| 流式工具调用 | 支持。每个工具调用 **整个放在一个块里**，`function.arguments` 是一次给全的完整 JSON 字符串（由参数对象序列化得到），**不分片**；带 `id`、`index`、`type:"function"` | 源码（`ToToolCalls`） |
| `finish_reason` | 单独一个空 `delta` 的结束块；本次流里发过工具调用且原因是 `stop` 时改写为 `tool_calls` | 源码（`FinishChunk`） |
| 用量 | 设了 `include_usage` 时，结束块之后再发一个 `choices: []` 且带 `usage` 的块，然后 `data: [DONE]`；没设则不发 | 源码 |
| 模型列表 | `GET /v1/models` 存在，另有 `GET /v1/models/{model}`；`created` 是模型最后修改时间，`owned_by` 默认 `"library"`。响应 `{object, data:[{id, object, created, owned_by}]}`，无分页 | 文档 + 源码 |
| 错误体（`/v1`） | `{"error":{"message","type","param":null,"code":null}}`；`type` 按状态取值：400→`invalid_request_error`，404→`not_found_error`，其余→`api_error` | 源码（`NewError`） |
| 状态码 | 400 请求有误；404 模型不存在；429 限流；500；502 云端模型不可达 | 文档 |
| 模型不存在 | 404，消息 `model "<name>" not found, try pulling it first` | 源码（`scheduleError`） |
| 模型不支持工具 | 400，消息里含 `does not support tools`（由 `does not support` 与缺失的能力名拼成） | 源码（`errCapabilities` + `errCapabilityTools`） |
| 流中途出错 | 文档只写了原生 `/api/*` 的 ndjson 形式（`{"error":"..."}` 一行，状态码不变）。**`/v1` SSE 流里的错误格式未核实** | 文档 |

另外：Ollama 也提供 Anthropic 兼容的 `/v1/messages`（支持消息、流式、函数调用；不支持 `tool_choice`）；云端的该端点只接受 `Authorization: Bearer`，不接受单独的 `x-api-key`。[文档：Anthropic compatibility](https://docs.ollama.com/api/anthropic-compatibility)

### 3.2 LM Studio

来源：[OpenAI Compatibility Endpoints](https://lmstudio.ai/docs/developer/openai-compat)、[Chat Completions](https://lmstudio.ai/docs/developer/openai-compat/chat-completions)、[Tool Use](https://lmstudio.ai/docs/developer/openai-compat/tools)、[Models](https://lmstudio.ai/docs/developer/openai-compat/models)、[Authentication](https://lmstudio.ai/docs/developer/core/authentication)。

| 项 | 事实 | 依据 |
| --- | --- | --- |
| 地址 | `http://localhost:1234/v1`（文档示例假定端口 1234） | 文档 |
| 密钥 | **默认不需要认证**。0.4.0 起可在服务器设置里开启“Require Authentication”，开启后用 `Authorization: Bearer <token>` | 文档 |
| 支持的端点 | `/v1/models`、`/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`、`/v1/completions` 等 | 文档 |
| Chat Completions 支持的参数清单 | `model, top_p, top_k, messages, temperature, max_tokens, stream, stop, presence_penalty, frequency_penalty, logit_bias, repeat_penalty, seed`；`tools` 在 Tool Use 页另述。清单里 **没有 `stream_options`、`tool_choice`、`max_completion_tokens`、`parallel_tool_calls`** | 文档 |
| 工具支持的范围 | “所有模型都至少有某种程度的工具支持”，分两级：Native（模型模板和 LM Studio 解析器都支持）与 Default（LM Studio 注入一段系统提示和默认格式 `[TOOL_REQUEST]...[END_TOOL_REQUEST]`，把 `tool` 角色消息转成 `user`，再从输出里解析）。Default 的效果“因模型而异” | 文档 |
| 流式工具调用 | 支持，且 **是分片的**：函数名和参数“分块发送”。示例里第一片带 `index=0, id='814890118', name='get_current_weather', arguments=''`，之后各片只有 `arguments` 片段。文档的累积代码对 `id`、`name`、`arguments` 三者都按 `index` 做字符串追加 | 文档 |
| 调用 ID 的形态 | 示例是纯数字字符串（`814890118`），不是 `call_` 前缀 | 文档示例 |
| 非流式 `finish_reason` | 有工具调用时为 `"tool_calls"` | 文档 |
| 流式用量 | **未核实**：参数清单没有 `stream_options`，Tool Use 页的流式一节也没提 `usage`（非流式响应示例里有 `usage`） | — |
| 模型列表 | `GET /v1/models` 存在，“返回服务器可见的模型；开启即时加载时可能包含所有已下载的模型”。**响应结构文档没写，未核实** | 文档 |
| 错误体与状态码 | **未核实**（上述页面都没有描述） | — |

另外：LM Studio 也提供 Anthropic 兼容的 `POST /v1/messages`；开启认证后同时接受 `x-api-key` 与 `Authorization: Bearer`。[文档：Anthropic Compatibility](https://lmstudio.ai/docs/developer/anthropic-compat)

### 3.3 vLLM（只查了工具调用一页）

来源：[Tool Calling](https://docs.vllm.ai/en/latest/features/tool_calling/)。

- 默认地址示例 `http://localhost:8000/v1`。
- 工具调用要在 **启动服务时** 打开：`--enable-auto-tool-choice` 加 `--tool-call-parser <name>`（按模型选解析器，可能还要 `--chat-template`）。没有加载解析器时，带具名 `tool_choice` 的请求返回 HTTP 400。
- `tool_choice` 支持 `auto`、`required`（vllm>=0.8.3）、`none` 和具名函数。
- 流式工具调用的分片方式取决于所用解析器；文档对其中一种解析器写的是“流式时每个工具调用在其区段解析完成后整体发出，解析不了的调用被丢弃”。**各解析器的统一行为未核实。**
- 是否需要密钥、模型列表、用量、错误体：**未核实**（没读 OpenAI-Compatible Server 页；抓取到的该页内容为空）。

### 3.4 汇总

| 点 | OpenAI 官方 | Ollama | LM Studio |
| --- | --- | --- | --- |
| 流式工具调用 | 支持 | 支持 | 支持 |
| 参数是否分片 | 分片，按 `index` 拼 | **不分片**，一块给全（源码） | 分片，名称也可能分片 |
| 后续分片带 `id` | 不带 | 不适用 | 不带 |
| 流式用量 | 需 `stream_options.include_usage` | 同官方（支持该选项） | 未核实 |
| `tool_choice` | 支持 | 不支持 | 未列入支持清单 |
| 模型列表 | `GET /v1/models`，无分页 | 有，无分页 | 有，结构未核实 |
| 密钥 | 必需 | 本地不需要 | 默认不需要，可开启 |
| 不支持工具时 | 未核实 | 400 `... does not support tools`（源码） | 不报错，退回 Default 方式，效果因模型而异 |

对适配器的直接含义（都是上表事实的推论，不是文档原话）：累积逻辑必须同时接受“首块就带完整 `arguments`”和“多块拼接”两种形态；`id`/`name` 可能只出现一次；带 `usage` 的块 `choices` 可能为空；收不到用量是正常情况。

---

## 4. 按地址识别服务商的线索

只列在各家文档里实际看到地址的条目。

| 服务商 | 默认接口地址 | 协议 | 依据 |
| --- | --- | --- | --- |
| OpenAI | `https://api.openai.com/v1` | OpenAI Chat Completions | [API Overview](https://developers.openai.com/api/reference/overview) |
| Anthropic | `https://api.anthropic.com`（路径 `/v1/messages`） | Messages | [API overview](https://platform.claude.com/docs/en/api/overview) |
| Ollama 本地 | `http://localhost:11434/v1` | OpenAI 兼容；同一主机另有 Messages 兼容的 `/v1/messages` | [OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility) |
| Ollama 云端 | `https://ollama.com/v1` | 同上，需 Bearer | 同上 |
| LM Studio | `http://localhost:1234/v1` | OpenAI 兼容；另有 Messages 兼容的 `/v1/messages` | [OpenAI Compatibility Endpoints](https://lmstudio.ai/docs/developer/openai-compat) |
| vLLM | `http://localhost:8000/v1` | OpenAI 兼容 | [Tool Calling](https://docs.vllm.ai/en/latest/features/tool_calling/) |
| DeepSeek | `https://api.deepseek.com`（`/chat/completions`） | OpenAI 兼容 | [DeepSeek API Docs](https://api-docs.deepseek.com/) |
| DeepSeek（Messages） | `https://api.deepseek.com/anthropic` | Messages 兼容（`x-api-key`、`anthropic-version`） | [Anthropic API](https://api-docs.deepseek.com/guides/anthropic_api) |
| OpenRouter | `https://openrouter.ai/api/v1` | OpenAI 兼容；SSE 里会夹带注释行 `: OPENROUTER PROCESSING`；`finish_reason` 多一个 `error` | [API overview](https://openrouter.ai/docs/api/reference/overview)、[Streaming](https://openrouter.ai/docs/api/reference/streaming) |
| Google Gemini | `https://generativelanguage.googleapis.com/v1beta/openai/` | OpenAI 兼容，Bearer | [OpenAI compatibility](https://ai.google.dev/gemini-api/docs/openai) |
| Groq | `https://api.groq.com/openai/v1` | OpenAI 兼容 | [OpenAI Compatibility](https://console.groq.com/docs/openai) |
| Mistral | `https://api.mistral.ai/v1` | 路径为 `/v1/chat/completions`；与 OpenAI 的字段级兼容程度未核实 | [API reference](https://docs.mistral.ai/api/) |
| xAI | `https://api.x.ai/v1` | 文档里可见该前缀；Chat Completions 的兼容细节未核实 | [xAI docs](https://docs.x.ai/docs/overview) |
| Moonshot（Kimi） | `https://api.moonshot.cn/v1` | OpenAI 兼容 | [Chat API](https://platform.moonshot.cn/docs/api/chat) |
| 硅基流动 | `https://api.siliconflow.cn/v1` | OpenAI 兼容 | [Chat Completions](https://docs.siliconflow.cn/cn/api-reference/chat-completions/chat-completions) |
| 智谱 | `https://open.bigmodel.cn/api/paas/v4/` | OpenAI 兼容（注意版本段是 `v4`，不是 `v1`） | [OpenAI 兼容介绍](https://docs.bigmodel.cn/cn/guide/develop/openai/introduction) |

识别时要注意的地址形态（从上表可见）：

- 基础地址不一定以 `/v1` 结尾（DeepSeek 无版本段、智谱是 `/v4`、Gemini 是 `/v1beta/openai`、Groq 是 `/openai/v1`、OpenRouter 是 `/api/v1`）。
- 同一主机可能两种协议都有（Ollama、LM Studio、DeepSeek），靠路径区分：`/chat/completions` 对 `/messages`。
- 对表里除 OpenAI、Anthropic、Ollama、LM Studio 之外的服务商，本次只核对了地址和协议类别，**没有逐项核对它们在流式工具调用、用量、错误上的差异**。

---

## 5. SSE 解析器需要覆盖的线上形态

来自上面各节，集中列出：

- 两种协议都是 `text/event-stream`。Anthropic 每条事件有 `event:` + `data:`；OpenAI 只有 `data:`。
- 以 `:` 开头的行是 SSE 注释，要跳过（OpenRouter 文档明确会发，并指出把它交给 JSON 解析会出错）。
- OpenAI 的终止哨兵 `data: [DONE]` 不是 JSON；Anthropic 没有哨兵，以 `message_stop` 事件结束。
- Anthropic 的 `ping`、未知事件类型、未知 delta 类型都要忽略而不是报错（版本策略允许新增）。
- 流中错误：Anthropic 是 `event: error`；OpenAI 官方未核实；OpenRouter 是带顶层 `error` 的块。

---

## Caveats / Not Found

未核实清单（括号里是查过的地方）：

1. **OpenAI Chat Completions 流中途出错的格式**（streaming-events 参考页、streaming-responses 指南、error-codes 页）。
2. **OpenAI `model_not_found` 的 HTTP 状态**，以及 **模型不支持工具调用时的错误**（error-codes、rate-limits、chat create）。
3. OpenAI 错误体的四个字段来自官方 OpenAPI 仓库的 schema，文档页面没有完整示例。
4. **Anthropic 模型名写错时的确切状态与消息**（errors、models list、messages create）。
5. Anthropic `2023-06-01` 版本的停用时间（versioning 页）。
6. Anthropic 无参数工具时 `partial_json` 累积为空串的处理（streaming 页）。
7. Anthropic 探测时合适的最小 `max_tokens`；`count_tokens` 是否计费。
8. **Ollama `/v1` 流里的错误格式**（errors 页只写了原生 ndjson）。
9. Ollama 关于“工具调用不分片”“`does not support tools` 返回 400”“错误体结构”的结论来自 main 分支源码，不是文档，会随版本变化。
10. **LM Studio 的流式用量、错误体与状态码、`/v1/models` 的响应结构**（openai-compat 下的 chat-completions、tools、models 三页与 authentication 页）。
11. vLLM 只读了工具调用页；密钥、模型列表、用量、错误、各解析器的流式分片行为均未核实。
12. 阿里云百炼（DashScope）的兼容地址：页面是脚本渲染的，抓到的内容里没有地址，**未列入第 4 节**。
13. 第 4 节除四家重点对象外，只核对了地址与协议类别。

其他说明：

- 没有用到网络搜索工具（本会话不可用），全部是按已知文档地址直接抓取；因此“某页没写”不等于“官方任何地方都没写”。
- 文档示例里出现的模型名（如 `claude-opus-5-5`、`gpt-6-astra`）只是示例，本文请求体统一写成 `<model-id>`。
- OpenAI 的函数调用指南默认展示 Responses API，Chat Completions 的流式累积代码要把页面切到 Chat Completions 模式才看得到；该页的 `.md` 导出只含 Responses 版本。
