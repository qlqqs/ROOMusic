# 管家 C2 设计：运行时与只读工具

总体结构、包划分、工具合同字段和 Provider 接口见父任务 `../11-15-music-steward-m6/design.md`，这里只写 C2 自己的内容。
标 “按 ADR” 的地方以 C1 批准后的 ADR 0005 为准；ADR 与本文不一致时改本文。

## 包与依赖

```text
backend/internal/steward/
├── tool/       Definition、Registry、Invoke
├── provider/   Provider 接口、openai 与 messages 两个适配器、探测
├── egress/     出站 HTTP 客户端（地址检查）
├── secret/     实例密钥文件与加解密
├── runtime/    循环、会话与消息存储、设置存储
└── httpapi/    设置、探测、会话、消息流、成员开关
```

- `steward/*` 只依赖 `identity`、`platform/*` 和彼此；不依赖 `catalog`、`library`、`operations` 等业务包。
- 只读工具由业务包各自实现并导出一个返回 `[]tool.Definition` 的函数（例如 `catalog` 的 `StewardTools(store)`），
  `internal/app` 把它们注册进 `tool.Registry`。做法与 `operations.Kind` 相同（`backend/internal/operations/kind.go`）。
- `backend/internal/app/dependency_rules_test.go` 增加上述包的条目；`egress` 是 `steward` 下唯一允许使用 HTTP 客户端的包。

## 工具

```go
type Scope string // "read" | "personal" | "shared"

type Definition struct {
    Name, Description string
    Input, Output     json.RawMessage // JSON Schema
    MinRole           Role            // tool.RoleMember（成员与管理员）或 tool.RoleAdmin
    Scope             Scope
    Revertible        bool
    Untrusted         bool            // 输出含标签、歌词、文件名等不可信文本
    Handler           func(ctx context.Context, caller Caller, input json.RawMessage) (json.RawMessage, error)
}

type Caller struct{ User identity.User; Source string } // Source: "chat"；C3 加 "mcp"
```

- `Registry.List(caller)` 只返回 caller 有权调用的工具；`Registry.Invoke(ctx, caller, name, input)` 再鉴权一次、
  按 schema 校验输入、执行、对输出按大小上限截断。C2 只注册 `read` 工具，`Invoke` 遇到其他范围直接拒绝，
  确认路径由 C6 接入。
- 处理器读取的对象不存在时返回 `*tool.Error`（`not_found`），其他调用错误码是 `tool_not_found`、`permission_denied`、
  `tool_scope_unsupported`、`validation_failed`。
- C2 的工具共 16 个（清单见合同规范第 3 节）：搜索、专辑列表与详情、版本详情、曲目详情、艺人列表与详情、
  艺人的专辑与曲目、证据、待关注问题、扫描列表、单次扫描报告与当前扫描状态、操作记录列表。每个都调用现有服务层方法
  （如 `catalog.Store.Search`、`ListReleaseGroups`、`ListArtists`，`library.Service.ListScans`、`GetScanReport`），
  输出复用现有接口的 DTO，去掉绝对路径（按 ADR：路径只给音乐目录以下的相对部分；歌词不在曲目详情里返回）。

## 出站与密钥

- `egress.NewClient` 构造出站客户端：拨号前按解析出的 IP 判断；管理员的请求不限，成员的请求只允许公网地址和放行名单里的地址
  （按 ADR D1）。保存设置、探测、发消息时各先预检一次端点地址。模型端点的请求不跟随重定向，端点返回重定向即失败。
  放行名单是一张全局表，由管理员在设置里维护。
- `secret`：第一次加密时在 `ROOMUSIC_DATA_DIR/steward/instance.key` 生成实例密钥文件（权限 0600），用标准库的 AES-GCM 加解密；
  数据库只存密文与密钥末四位（按 ADR D2）。解密不创建密钥文件；密钥文件缺失而库里已有密文时，设置接口把该用户的密钥标为需要重新填写。
- 模型密钥可选：没有密钥的端点可以保存、探测和对话，请求不带认证头。

## 数据（迁移 0051 起，各带 down）

| 表 | 主要字段 |
| --- | --- |
| `steward_user_settings` | `user_id` 主键、端点地址、密钥密文与末四位、协议、对话模型、显示名字、探测结果（JSON）与时间、手动覆盖项 |
| `steward_instance_settings` | 单行：是否允许成员使用 |
| `steward_egress_allowlist` | 主机与端口、添加者、时间 |
| `steward_conversations` | `id`、`user_id`、标题、模式、模型、状态、创建与更新时间 |
| `steward_messages` | `id`、`conversation_id`、序号、角色、内容（JSON）、用量 |
| `steward_tool_calls` | `id`、`message_id`、消息内的调用位置（`position`）、工具名、输入、输出、是否截断、是否不可信、状态、错误码、耗时 |

会话、消息、工具调用按 `user_id` 隔离，删除会话级联删除消息与工具调用。模式字段 C2 只接受 `assistant`。
同一条消息的工具调用按 `position` 排序。设置的保存与探测在一个事务里锁行完成。
审查模型、搜索来源、令牌、方案等字段和表由各自的子任务加。

## 循环

- 每次发消息启动一次运行：读出历史 → 调 `Provider.Stream` → 文本增量直接转发；模型请求工具时
  `Registry.Invoke`，结果作为工具消息追加并落库 → 再调模型，直到没有工具请求。
- 上限：步数、单次运行的输入输出 token 总数、单个工具输出的字节数；数值是包内常量，实施时给出并写进合同文档。
- 运行绑定请求的 context：客户端断开或调用取消接口就停止；每完成一步落库一次，所以中断后会话可读，
  停在最后一个完整的步骤上。同一会话同时只允许一个运行，第二个请求返回冲突。
- 中断时仍在执行的工具调用，在释放会话和进程启动恢复时标为失败（错误码 `interrupted`）；下次重放历史时为缺结果的调用补一条
  `interrupted` 结果再发给模型。
- 运行内的 panic 被捕获：记日志 `steward.run.panicked`，释放会话，以 `internal_error` 结束。
- 模型因输出上限停止（包括截断在工具参数中间）按 `token_limit` 结束，这一步请求的工具不执行。
- 发给模型的工具结果里，`Untrusted` 的输出按 ADR 规定的方式加来源标记；包装前只转义 `&`、`<`、`>`。

## Provider 与探测

- 两个适配器把运行时的消息、工具定义、工具调用转换成各自协议的请求，并把 SSE 流解析成统一的事件：
  文本增量、工具调用（名称与完整参数）、用量、结束原因。请求字段与事件格式实施时按两家的当前文档写，
  文档链接记在 `research/` 下。
- 调研结论对设计的约束（详见 `research/provider-protocols.md`）：
  - 运行时的助手消息要能原样保存适配器给的不透明块（Messages 接口的思考块及其签名必须在回传工具结果时带回），
    这些块落库但不显示、不发给另一种协议。
  - OpenAI 兼容流里的工具调用按 `index` 累积；既要接受分片的参数，也要接受整块给出的参数（本地服务）。
  - 用量要显式请求才返回，且可能缺失；缺失时记为未知，不估算。
  - 不发送采样参数；探测不依赖强制工具调用，用一个明确要求调用工具的提示来确认，模型没有调用时结论记为“未确认”，由用户手动改。
  - 调研里标为未核实的错误格式，适配器按“HTTP 状态非 2xx 或流中出现错误对象即失败”处理，原始错误信息脱敏、截断后返回给用户。
  - 输出上限字段：OpenAI 兼容端点用 `max_tokens`，只有 OpenAI 官方端点用 `max_completion_tokens`。
  - 错误类别：超时（`timeout`）、出站被拒、连不上、认证失败、限流、模型不存在、不支持工具、响应无效、流中断、其他。
- 探测 `Probe(ctx, client, endpoint, key, model)`：先按地址匹配预设表得到猜测的协议；再依次请求模型列表、给了 `model` 时用一个最小的
  带工具请求确认工具调用。返回协议、模型列表、工具能力（支持、不支持、未确认）、每一步的失败原因。预设表是包内的一个 Go 切片。
- 探测接口只在成功时保存设置与结论：协议识别成功且工具步骤没有出错。模型正常结束但没有调用工具的“未确认”、
  端点明确报告的“不支持”都是有效结论，会保存。超时或其他失败时返回错误和本次报告，不改已存设置。
- 端点地址、协议或密钥变化时清除已存的探测结论，并清除已存的手动覆盖项（本次请求提交的覆盖项照常保存）；
  主机或端口变化而没有同时给密钥时清除已存密钥；
  只换模型时把已存结论的工具能力降为“未确认”。只有工具能力为“不支持”才拦截对话。

## 接口（都在 `/me/steward` 下，需登录；管理员关闭后成员返回 403）

| 方法与路径 | 用途 |
| --- | --- |
| `GET/PUT /me/steward/settings` | 读写自己的设置；响应里密钥只有末四位；`key: null` 清除密钥；保存时检查出站地址 |
| `POST /me/steward/settings/probe` | 探测，成功时保存结果；同样接受 `key: null` |
| `GET/POST /me/steward/conversations`、`GET/DELETE /me/steward/conversations/{id}` | 会话列表、新建、读取、删除 |
| `PATCH /me/steward/conversations/{id}` | 改标题、模型（`{title?, model?}`，响应 `{title, model}`；模型为空表示用默认模型） |
| `POST /me/steward/conversations/{id}/messages` | 发消息，响应是 SSE |
| `POST /me/steward/conversations/{id}/cancel` | 取消正在进行的运行 |
| `GET/PUT /admin/steward/settings` | 成员开关 |
| `GET/POST/DELETE /admin/steward/egress-allowlist` | 放行名单 |

SSE 事件：`text`（增量）、`tool_started`、`tool_finished`、`usage`、`done`（结束原因：`completed`、`step_limit`、
`token_limit`、`cancelled`、`provider_error`、`internal_error`；模型错误时带错误类别、端点的 HTTP 状态与脱敏后的说明）。
写法参照 `backend/internal/zones/httpapi/events.go`。长流每 60 秒复查一次凭据，失效或成员开关被关闭时取消运行并发
`done`（`cancelled`）；复查查询出错时跳过这一轮。

错误码：`steward_not_configured`、`steward_disabled`、`provider_unreachable`、`provider_timeout`、`provider_no_tool_support`、
`egress_denied`、`conversation_busy`；HTTP 状态与响应结构按 `.trellis/spec/backend/error-handling.md`。

## 日志

事件名用 `steward.` 前缀；记录会话 ID、工具名、耗时、token 用量和结束原因，不记录消息正文、工具输入输出和密钥。
工具执行失败只记错误码，不记处理器返回的错误文本；panic 时记调用栈。

## 取舍

- 运行绑定请求而不是后台任务：C2 没有需要挂起的确认，简单可靠；后台继续与挂起恢复由 C6、C10 加，
  届时沿用每步落库的状态。
- 工具输出复用现有 DTO：不另造一套给模型看的结构，代价是个别字段对模型多余；路径与歌词按 ADR 单独处理。

## 回退

迁移带 down；没有配置端点的用户不受影响；`app` 里去掉注册即可停用全部接口。
