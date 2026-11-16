# Research: 官方 MCP Go SDK 与自实现 Streamable HTTP（仅 tools）

- **Query**: 官方 Go SDK 的版本/依赖/用法；MCP 当前协议版本与 Streamable HTTP、授权要求；Claude Code 接入固定 Bearer 头的远程 HTTP MCP 服务；不用 SDK 自实现的清单、规模与风险
- **Scope**: mixed（以外部为主）
- **Date**: 2026-11-16（下文所有链接的核对日期均为 2026-11-16）

核对方式说明：

- 「文档」= 当天用 curl 取回的官方页面原文。
- 「源码」= 当天从 `proxy.golang.org` 下载的 `github.com/modelcontextprotocol/go-sdk@v1.8.0` 模块包内的文件。
- 「实测」= 当天在会话临时目录用 Go 1.25.10 + SDK v1.8.0 编了一个一次性探测服务（监听 127.0.0.1，不在仓库内，未改动 `backend/go.mod`），用 curl 打请求看到的真实响应。探测用的模块缓存也放在临时目录，没有写入本机 GOMODCACHE。

## 0. 先看这一条：任务前提与现行规范有出入

任务背景里写「只需要 initialize、tools/list、tools/call、ping」。这是 `2025-11-25` 及更早版本的方法集合。**现行规范是 `2026-07-28`，它删除了 `initialize`/`notifications/initialized` 握手、删除了 `ping`、删除了 `Mcp-Session-Id` 和 GET 流，并新增了必须实现的 `server/discover`。**

- 现行版本声明：<https://modelcontextprotocol.io/specification/versioning>（「The current protocol version is 2026-07-28」）
- 变更清单：<https://modelcontextprotocol.io/specification/2026-07-28/changelog>

所以现在有两套线上形态（规范用语）：

| 名称 | 协议版本 | 特征 |
|---|---|---|
| modern（现行） | `2026-07-28` | 无握手；每个请求在 `params._meta` 里带协议版本和客户端能力；HTTP 头镜像 `Mcp-Method`/`Mcp-Name`；必须有 `server/discover` |
| legacy（旧） | `2025-11-25` 及更早 | `initialize` 握手、可选 `Mcp-Session-Id`、`ping`、GET 流 |

服务端可以只做其中一种，也可以两种都做（dual-era）。来源：<https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning>（Terminology、Compatibility Matrix）。

Claude Code 两种都会说（见第 4 节）：新运行时先探测服务端是否支持 `2026-07-28`，支持就用，不支持就退回旧握手。因此「只做 legacy」「只做 modern」「两者都做」三种服务端它都能连，但只做 modern 时，旧版 Claude Code 或被固定在 v1 运行时的会话连不上。

## 1. 官方 Go SDK：版本、Go 要求、许可证、依赖

### 版本

- 最新稳定版：**v1.8.0**。`proxy.golang.org/.../@latest` 返回 `v1.8.0`（提交时间 2026-09-04）；GitHub Releases 上 v1.8.0 的发布时间是 2026-09-14，非预发布。版本列表中没有比它更高的版本（含预发布）。
  - <https://proxy.golang.org/github.com/modelcontextprotocol/go-sdk/@latest>
  - <https://proxy.golang.org/github.com/modelcontextprotocol/go-sdk/@v/list>
  - <https://github.com/modelcontextprotocol/go-sdk/releases>
- 该版本支持的协议版本（源码 `mcp/shared.go:50-63`）：`2026-07-28`、`2025-11-25`、`2025-06-18`、`2025-03-26`、`2024-11-05`。

### 最低 Go 版本

- `go 1.25.0`（v1.8.0 的 `go.mod`）。与本项目 `backend/go.mod` 的 `go 1.25.0` 相同，不需要升级工具链。
  - <https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/go.mod>

### 许可证

- `LICENSE` 文件开头说明项目正在从 MIT 迁移到 Apache-2.0：新贡献按 Apache-2.0；已取得同意的旧贡献按 Apache-2.0；尚未取得同意的旧贡献仍按 MIT；文档（不含规范）按 CC-BY-4.0。也就是说代码是 **Apache-2.0 与 MIT 混合**，两者都是宽松许可证。
  - <https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/LICENSE>

### 依赖清单

SDK 自己的 `go.mod`（v1.8.0）：

| 模块 | 版本 | 类型 |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | 直接 |
| `github.com/google/go-cmp` | v0.7.0 | 直接 |
| `github.com/google/jsonschema-go` | v0.4.3 | 直接 |
| `github.com/segmentio/encoding` | v0.5.4 | 直接 |
| `github.com/yosida95/uritemplate/v3` | v3.0.2 | 直接 |
| `golang.org/x/oauth2` | v0.35.0 | 直接 |
| `golang.org/x/time` | v0.15.0 | 直接 |
| `golang.org/x/tools` | v0.42.0 | 直接 |
| `github.com/segmentio/asm` | v1.1.3 | 间接 |
| `golang.org/x/sync` | v0.20.0 | 间接 |
| `golang.org/x/sys` | v0.41.0 | 间接 |

`go.sum` 里出现的模块就是以上 11 个，没有更多。

**实际进入使用方的部分（实测）**：在一个只 `import ".../go-sdk/mcp"` 的模块里执行 `go mod tidy`，使用方 `go.mod` 得到 1 个直接依赖 + 8 个间接依赖；`jwt`、`go-cmp`、`x/tools` 被剪掉（只被 SDK 的测试、示例或未引用的包使用）：

```
require github.com/modelcontextprotocol/go-sdk v1.8.0
require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)
```

`go list -deps` 确认这 9 个模块全部被编进二进制（`mcp` 包引用 `auth` 包取 `TokenInfo` 类型，`auth` 引用 `x/oauth2`，所以即使不用 SDK 的 OAuth 功能，`x/oauth2` 也会编进来）。

对照本项目 `backend/go.mod`：`x/sync`（已有 v0.22.0）和 `x/sys`（已有 v0.47.0）已经存在且版本更高；**净新增 7 个模块**：`go-sdk`、`jsonschema-go`、`segmentio/encoding`、`segmentio/asm`、`uritemplate/v3`、`x/oauth2`、`x/time`。

其他量级数据（实测/源码）：SDK 非测试 Go 源码约 2 万行（`mcp`、`jsonrpc`、`auth`、`oauthex`、`internal` 合计 20378 行）；探测程序的二进制约 11.9 MB（未去符号，仅含 net/http + SDK）。

## 2. 用 SDK 做 Streamable HTTP 服务端

### 2.1 得到 `http.Handler` 并挂到已有 mux

`mcp.NewStreamableHTTPHandler(getServer func(*http.Request) *mcp.Server, opts *mcp.StreamableHTTPOptions) *mcp.StreamableHTTPHandler`，返回值实现 `http.Handler`，直接 `mux.Handle("/某路径", handler)`。

- 源码：`mcp/streamable.go:226-247`
- 文档示例（`docs/troubleshooting.md` 的 `ExampleStreamableHTTPHandler_middleware`）：<https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/troubleshooting.md>

### 2.2 每个请求按用户给不同工具列表：SDK 支持哪种

SDK 实际提供的机制是 **`getServer` 回调**：它拿到 `*http.Request`，返回这个请求要用的 `*mcp.Server`。文档注释原文：「The getServer function is used to create or look up servers for new sessions. It is OK for getServer to return the same server multiple times. If getServer returns nil, a 400 Bad Request will be served.」

- 无状态模式下，每个 POST 都会调用 `getServer` 并建立一个临时会话，请求结束就关闭（源码 `mcp/streamable.go:367-443`）。因此「按请求构造 Server」是可行的，SDK 文档也承认这种用法：`docs/server.md` 的「Stateless server deployments: Some deployments create a new Server for each incoming request, re-registering tools every time」，并为此提供 `SchemaCache`（只对 Go 结构体推导 schema 有意义，用原始 JSON Schema 时用不到）。
  - <https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/server.md>
- 认证放在外层 HTTP 中间件，把用户放进 `request.Context()`，`getServer` 里从 context 取用户、按用户过滤后登记工具。实测：`Bearer member` 得到 1 个工具，`Bearer admin` 得到 2 个；member 调用 admin 专属工具得到 `-32602 unknown tool`。
- **实测到的一个细节**：无状态模式下每个 POST 会调用 `getServer` **两次**（一次在 `ServeHTTP` 里取支持的协议版本，`mcp/streamable.go:349`；一次在 `serveStateless` 里真正使用，`:400`）。按请求构造 Server 时，构造逻辑会跑两遍。
- 另外两种思路的现状：
  - MCP 层中间件 `Server.AddReceivingMiddleware`（示例 `examples/server/middleware`、`examples/auth/server/main.go`）可以拦截 `tools/list`/`tools/call`，处理器也能从 `req.Extra.TokenInfo`、`req.Extra.Header` 读到令牌信息和 HTTP 头（源码 `mcp/shared.go:631-647`；实测 `req.Extra.Header.Get("Authorization")` 可读到）。用它过滤列表属于自己写过滤逻辑，SDK 没有现成的「按调用者过滤工具」选项。
  - per-session 过滤：SDK 没有这样的 API。并且现行规范明确 `tools/list` 「MUST NOT vary per-connection」，但「MAY vary by the authorization presented on the request」——按令牌过滤是规范允许的。来源：<https://modelcontextprotocol.io/specification/2026-07-28/server/tools>（Capabilities 一节）。
- SDK 自带 `auth.RequireBearerToken(verifier, opts)` 中间件（`auth/auth.go:97-125`）：校验 `Authorization: Bearer`，失败返回 401/403；`verifier` 签名是 `func(ctx, token string, req *http.Request) (*auth.TokenInfo, error)`。两点事实：
  - 只有 `opts.ResourceMetadataURL` 或 `opts.Scopes` 非空时才会写 `WWW-Authenticate` 头；都为空时 401 不带该头。
  - 默认要求 `TokenInfo.Expiration` 非零，否则返回 401「token missing expiration」；不过期的令牌需要设 `AllowMissingExpiration`（`auth/auth.go:163-172`）。
  - 文档：<https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/protocol.md>（Authorization → Server）

### 2.3 用原始 JSON Schema 登记工具并拿到原始参数 JSON

用低层方法 `(*mcp.Server).AddTool(t *mcp.Tool, h mcp.ToolHandler)`（不是泛型的顶层 `mcp.AddTool`）。

- `Tool.InputSchema`、`Tool.OutputSchema` 的类型是 `any`，可以直接放 `json.RawMessage`；`AddTool` 的文档注释原文：「set Tool.InputSchema to `{"type": "object"}`, using your preferred library or `json.RawMessage`」。输入 schema 必须非 nil 且 `type` 为 `"object"`，否则 **panic**（源码 `mcp/server.go:292-345`）。
- 处理器签名 `func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error)`；`req.Params.Arguments` 是 `json.RawMessage`（`CallToolParamsRaw`，`mcp/protocol.go:241-259`）。
- 低层 API **不做**输入校验、不做输出校验、不自动填 `Content`/`StructuredContent`/`IsError`，全部由调用方负责（同一段文档注释）。实测：传入 schema 之外的多余字段，SDK 原样交给处理器。
- 处理器返回 `error` 会被当作协议错误（JSON-RPC error）；要表达「工具执行失败」得自己返回 `IsError: true` 的结果或调 `result.SetError(err)`（`mcp/tool.go:17-30`、`mcp/protocol.go:332-343`）。
- `Tool.Annotations` 是 `*mcp.ToolAnnotations`，其中 `ReadOnlyHint`、`IdempotentHint` 是 `bool` 且 v1.7.0 起总是序列化；`DestructiveHint`、`OpenWorldHint` 是 `*bool`（`mcp/protocol.go:1964-1991`，`docs/mcpgodebug.md` 的 `hintomitempty` 条目）。
- 工具名不合规时 `AddTool` 只记日志不拒绝；默认分页大小 1000（`mcp.DefaultPageSize`）。

### 2.4 无状态模式

支持。`&mcp.StreamableHTTPOptions{Stateless: true}`；再加 `JSONResponse: true` 让响应用 `application/json` 而不是 SSE。

- `Stateless` 的文档注释（`mcp/streamable.go:129-146`）：不读不写 `Mcp-Session-Id`；每个请求用带默认初始化参数的临时会话；服务端发往客户端的请求立即被拒；GET 和 DELETE 返回 405。
- **`2026-07-28` 的请求只有在 `Stateless = true` 时才会被接受**；有状态的 handler 会拒绝这个版本（`docs/protocol.md` 的 Stateless Mode 一节）。
  - <https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/protocol.md>
- SDK 文档在同一节仍保留一段警告：「Stateless mode is not directly discussed in the spec, and is still being defined」（这段文字早于 `2026-07-28` 定稿，文档尚未更新）。
- v1.7.0/v1.8.0 的行为变更通过 `MCPGODEBUG` 临时开关回退，开关在 v1.9.0 移除（`docs/mcpgodebug.md`）。

### 2.5 最小代码片段

下面的片段是本次探测程序的精简版，**已实测可编译并按预期响应**；API 出处见上文各小节（`docs/troubleshooting.md` 的 handler 示例、`mcp/server.go` 的 `AddTool` 注释、`examples/auth/server/main.go` 的中间件挂法）。

```go
func serverFor(caller tool.Caller, registry *tool.Registry) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "roomusic", Version: "x"}, &mcp.ServerOptions{
		// 非 nil 的 Capabilities 会去掉默认的 {"logging":{}}；&ToolCapabilities{} 表示不发 listChanged。
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		// 列表随用户变化时，规范要求 cacheScope 为 "private"。
		SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) { c.CacheScope = "private" },
	})
	for _, d := range registry.List(caller) {
		s.AddTool(&mcp.Tool{
			Name:         d.Name,
			Description:  d.Description,
			InputSchema:  d.Input,  // json.RawMessage
			OutputSchema: d.Output, // json.RawMessage
			Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: d.Scope == tool.ScopeRead},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := registry.Invoke(ctx, caller, req.Params.Name, req.Params.Arguments)
			// 这里由调用方决定：哪些错误返回 IsError 结果，哪些返回 error（协议错误）。
			_ = err
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: string(res.Output)}},
				StructuredContent: res.Output,
			}, nil
		})
	}
	return s
}

handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
	return serverFor(callerFromContext(r.Context()), registry) // 每个 POST 会被调用两次
}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

mux.Handle("/api/mcp", bearerAuth(handler)) // bearerAuth 是自己的中间件：验令牌、把用户放进 context、失败回 401
```

### 2.6 这个配置在线上的实际表现（实测）

| 请求 | 响应 |
|---|---|
| 无令牌（被外层中间件拦截） | 401，头和正文完全由自己的中间件决定 |
| `GET`（带令牌） | 405，`Allow: POST` |
| legacy `initialize`（`protocolVersion: 2025-11-25`） | 200 JSON：`{"capabilities":{"tools":{}},"protocolVersion":"2025-11-25","serverInfo":{...}}`，**没有** `Mcp-Session-Id` 头 |
| legacy `notifications/initialized` | 202，无正文 |
| legacy `tools/list`（另起连接、之前没 initialize） | 200，正常返回；结果里也带 `ttlMs`、`cacheScope` |
| legacy `tools/call` | 200，`content` + `structuredContent` |
| legacy `ping` | 200，`{"result":{}}` |
| modern `server/discover` | 200，`resultType: "complete"`、`supportedVersions`（5 个版本）、`capabilities`、`_meta` 里的 `serverInfo`、`ttlMs`、`cacheScope` |
| modern `tools/list` / `tools/call` | 200，带 `resultType` 和 `_meta.serverInfo` |
| modern `tools/call` 缺 `Mcp-Name` 头 | 400，`-32020` |
| modern 不支持的版本 | 400，`-32022`，`data.supported`/`data.requested` |
| modern `prompts/list`（没登记任何 prompt） | 200，空列表（不是 404/`-32601`） |
| 带 `Origin: http://evil.example` 的 POST | 200，照常处理 |

要点：

- 同一个 handler 同时服务 legacy 和 modern 两种客户端（dual-era），不需要额外配置。
- **默认不校验 `Origin`**：`StreamableHTTPOptions.CrossOriginProtection` 为 nil 即不保护，且该字段已标记 Deprecated，文档指示在外层包 `http.NewCrossOriginProtection()`（`mcp/streamable.go:184-195`）。SDK 默认只做一件事：请求从回环地址进来但 `Host` 头不是回环地址时回 403（DNS rebinding 保护，`mcp/streamable.go:317-326`）。本项目已有 `backend/internal/platform/weborigin/weborigin.go`。
- 请求体默认上限 4 MiB（`DefaultMaxRequestBodyBytes`）。
- `Content-Type` 必须是 `application/json`（否则 415），`Accept` 必须同时含 `application/json` 和 `text/event-stream`（否则 400）。

## 3. MCP 规范

### 3.1 当前协议版本

**`2026-07-28`**（状态 Current）。上一个版本 `2025-11-25` 现在是 Final。

- <https://modelcontextprotocol.io/specification/versioning>
- 相对上一版的主要变更（<https://modelcontextprotocol.io/specification/2026-07-28/changelog>）：
  1. 删除协议级会话和 `Mcp-Session-Id`；
  2. 删除 `initialize`/`notifications/initialized`，每个请求在 `_meta` 带 `io.modelcontextprotocol/protocolVersion` 和 `io.modelcontextprotocol/clientCapabilities`；
  3. 新增必须实现的 `server/discover`；
  4. GET 端点和 `resources/subscribe` 由 `subscriptions/listen` 取代；
  5. 删除 `ping`、`logging/setLevel`；
  6. 所有结果必须带 `resultType`（`"complete"` 或 `"input_required"`）；
  7. `tools/list` 等结果必须带 `ttlMs`、`cacheScope`；
  8. POST 必须带 `Mcp-Method`、`Mcp-Name` 头；
  9. `structuredContent` 放宽为任意 JSON 值，`inputSchema`/`outputSchema` 放宽为任意 JSON Schema 2020-12；
  10. 错误码区间划分：`-32020` 到 `-32099` 归规范所有。

### 3.2 Streamable HTTP 对服务端的最低要求（`2026-07-28`）

来源：<https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http>

- **单端点 POST**：「The server MUST provide a single HTTP endpoint path … that supports POST」。每条 JSON-RPC 消息是一次独立的 POST，正文是单个请求或通知（不再有批量，客户端也不再发 response）。
- **响应形式**：对请求，服务端返回 `application/json`（单个 JSON 对象）或 `text/event-stream` 二选一，由服务端决定；只回 JSON 是合规的。对通知，接受则 `202 Accepted` 无正文。
- **`Mcp-Session-Id`**：本版本没有。只支持本版本的服务端收到该头应忽略，不生成也不回显。
- **GET**：本版本没有 GET 流。只支持本版本的服务端对 GET 或 DELETE 「SHOULD respond with 405 Method Not Allowed」。
- **`MCP-Protocol-Version` 头**：每个 POST 必带；必须与正文 `_meta` 里的版本一致，不一致回 `400` + `-32020 HeaderMismatch`；版本不支持回 `400` + `-32022 UnsupportedProtocolVersionError`（`data.supported`、`data.requested`）。
- **`Mcp-Method`、`Mcp-Name` 头**：前者所有请求必带，后者 `tools/call` 必带（值为 `params.name`）；缺失或与正文不符回 `400` + `-32020`。`Mcp-Name` 可能是 `=?base64?...?=` 形式，比较前要解码。
- **未实现的方法**：`404` + JSON-RPC `-32601`。
- **缺少必需的 `_meta` 字段**：`-32602`，HTTP `400`（<https://modelcontextprotocol.io/specification/2026-07-28/basic>，`_meta` 一节）。
- **Origin 校验**：「Servers MUST validate the Origin header on all incoming connections to prevent DNS rebinding attacks. If the Origin header is present and invalid, servers MUST respond with HTTP 403 Forbidden」；正文可以是没有 `id` 的 JSON-RPC 错误。另有两条 SHOULD：本地运行时只绑定 127.0.0.1；对所有连接做认证。
- **取消**：关闭 SSE 响应流即取消；HTTP 上不再有 `notifications/cancelled`。
- **向后兼容**：两代都支持的客户端先发 modern 请求，收到 `400` 时看正文——是可识别的 modern JSON-RPC 错误就留在 modern，否则退回 `initialize`。

### 3.3 旧版（`2025-11-25`）的对应要求

来源：<https://modelcontextprotocol.io/specification/2025-11-25/basic/transports>、<https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle>

- 端点「supports both POST and GET」，但对 GET 服务端可以返回 SSE 流「or else return HTTP 405 Method Not Allowed」——**GET 回 405 合规**。
- `MCP-Session-Id`：服务端「MAY assign a session ID at initialization time」——**可选**，不发就是无会话；DELETE 也可回 405。
- `MCP-Protocol-Version` 头：客户端在 initialize 之后的请求上必带；服务端收到无效或不支持的值「MUST respond with 400 Bad Request」；没收到时应按 `2025-03-26` 处理。
- Origin 校验要求与现行版本相同（MUST，403）。
- 必须先 `initialize` 再发 `notifications/initialized`；`ping` 是可选机制，收到必须回空结果 `{}`。

### 3.4 授权：对只用静态 Bearer 令牌（不实现 OAuth）的服务端

来源：<https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization>、<https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/authorization-server-discovery>、<https://modelcontextprotocol.io/specification/2026-07-28/basic>（Auth 一节）

- 授权整体是可选的：「Authorization is OPTIONAL for MCP implementations. When supported: Implementations using an HTTP-based transport SHOULD conform to this specification」。基础协议页另有一句：「clients and servers MAY negotiate their own custom authentication and authorization strategies」。
- 授权规范描述的唯一方案是 OAuth 2.1。**在这个方案之内**：MCP 服务端「MUST implement OAuth 2.0 Protected Resource Metadata (RFC 9728)」，元数据里必须有至少一个 `authorization_servers`；发现机制二选一——401 响应的 `WWW-Authenticate` 头带 `resource_metadata`，或者在 well-known URI 提供元数据。无效或过期令牌「MUST receive a HTTP 401 response」；权限/范围不足用 403。
- 规范**没有专门写**「静态 Bearer 令牌、不做 OAuth」这种服务端的 401 要求。按上面两句的字面：
  - 不实现 OAuth 授权方案的服务端属于「自定义认证策略」，不在授权规范的 MUST 范围内，Protected Resource Metadata **不是必需**（也无从提供，因为没有授权服务器可填）。
  - 401 仍是无效令牌的通用语义。`WWW-Authenticate` 的 `resource_metadata` 参数只在走 OAuth 发现时有意义。
  - 通用 HTTP 层面，RFC 9110 §11.6.1 规定 401 响应必须带至少一个 `WWW-Authenticate` challenge，例如 `WWW-Authenticate: Bearer`（RFC 6750 §3）。这是 HTTP 的规定，不是 MCP 的。
    - <https://www.rfc-editor.org/rfc/rfc9110.html#section-11.6.1>、<https://datatracker.ietf.org/doc/html/rfc6750#section-3>
- 这一段是对规范文字的解读，规范没有直接给出结论句；`2025-11-25` 版的授权页在这些点上措辞相同（<https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization>）。

### 3.5 与「按用户过滤工具列表」直接相关的规范条文

- `tools/list` 的集合「MAY vary by the authorization presented on the request」（<https://modelcontextprotocol.io/specification/2026-07-28/server/tools>）。
- 列表随用户变化时 `cacheScope` 用 `"private"`：「"private" is appropriate for … filtered list results that vary per user」；`ttlMs` 必须 ≥ 0（<https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/caching>）。
- 工具应按确定的顺序返回（SHOULD）。本项目 `Registry.List` 已按名称排序（`backend/internal/steward/tool/registry.go:63-76`）。

## 4. Claude Code 接入带固定 Bearer 头的远程 HTTP MCP 服务

来源：<https://code.claude.com/docs/en/mcp>（当天取回的 Markdown 版）、<https://code.claude.com/docs/en/env-vars>

### 命令与配置

```bash
claude mcp add --transport http <名称> <URL> --header "Authorization: Bearer <令牌>"
# -t / -H 是短写；--scope local(默认) | project | user
```

`.mcp.json`（项目级）或 `claude mcp add-json` 的写法：

```json
{
  "mcpServers": {
    "roomusic": {
      "type": "http",
      "url": "https://example.com/api/mcp",
      "headers": { "Authorization": "Bearer ${ROOMUSIC_TOKEN}" }
    }
  }
}
```

- `type` 必须写（`http`，或别名 `streamable-http`）；有 `url` 没 `type` 会被当成 stdio 并跳过。
- `headers` 的值支持 `${VAR}` 和 `${VAR:-默认}` 展开。
- 需要运行时生成头可以用 `headersHelper`（一条输出 JSON 的命令）。

### 对不实现 OAuth 的服务端是否正常工作

文档明确支持，GitHub 远程 MCP 服务用个人访问令牌的例子就是这种形态（「authenticates with a GitHub personal access token passed as a header」）。配置了 `Authorization` 头之后的行为，文档原文：

- 「For a server whose `Authorization` header you configured … a 401 or 403 while connecting doesn't flag the server, because the credential to fix is the one you configured. Claude Code reports the connection as failed instead.」
- 「If you configured `headers.Authorization` for the server and the server rejects that header, Claude Code reports the connection as failed instead of falling back to OAuth.」

也就是说：**配了 `Authorization` 头就不会走 OAuth 发现**，令牌无效时在 `/mcp` 里显示连接失败并附 HTTP 状态码。

### 已知坑（均来自官方文档）

1. **没配 `Authorization` 头时**，服务端回 401/403 会被标记为「需要认证」，Claude Code 会尝试 OAuth：先查 RFC 9728 的 `/.well-known/oauth-protected-resource`，再查 RFC 8414 的 `/.well-known/oauth-authorization-server`。不实现 OAuth 的服务端在这条路径上会让用户看到一个走不通的登录入口。
2. **部分环境变量名在远程服务的 `url`/`headers` 里被读成空**：`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、云厂商凭据、`HTTPS_PROXY`、`NPM_TOKEN` 等；写 `Bearer ${ANTHROPIC_AUTH_TOKEN}` 时服务端收到的是 `Bearer `（无凭据）。自定义名称（如 `API_KEY`）正常展开。
3. **令牌首尾空白**：粘贴时带进来的换行不会被修剪，原样发送；`claude mcp list` 和 `/mcp` 会给出警告。
4. `claude mcp add` 保存时不校验凭据，占位符也能存；连接结果要到 `/mcp` 里看。
5. 认证错误和 4xx 不重试；首次连接遇到 5xx、连接被拒或超时会重试最多三次。
6. `.mcp.json` 里的项目级服务在交互式会话首次使用前要用户批准。
7. 工具输出：超过 10,000 token 有警告，默认上限 25,000 token（`MAX_MCP_OUTPUT_TOKENS`）；超过 50,000 字符的文本结果会被存成文件。本项目注册表的单次输出上限是 64 KiB（`backend/internal/steward/tool/registry.go:14`）。
8. `claude -p` 和 Agent SDK 没有 `/mcp` 面板，跑不了 OAuth；固定头的方式不受影响。

### Claude Code 说哪一代协议

- 有两套客户端运行时：v1（MCP TypeScript SDK 1.x）和 v2（TypeScript SDK 2.0，增加 `2026-07-28`）。v2.1.232 起在拉取功能开关的会话里用 v2，v2.1.274 起其余会话也默认 v2。本机装的是 Claude Code 2.1.325（见本机环境说明），属于默认 v2 的范围。
- v2 上：「Asks HTTP, stdio, and claude.ai connector servers whether they support the newer revision and uses it with those that do」，其余按 v1 的方式连（旧握手）。
- v2 上对新协议的服务端，会保持一条流来接收 `list_changed` 通知，流断了会重开（有次数限制）。
- 环境变量：`MCP_SDK_GENERATION=v1|v2` 固定运行时；`MCP_PROTOCOL_NEGOTIATION=legacy|auto` 控制是否探测新协议。
- Anthropic 可以用功能开关把某个服务端固定在旧协议上。

## 5. 不用 SDK，自己用标准库实现（仅 tools、无状态）

下面按两代协议分别列。只做一代时取对应一列；两代都做时两列都要，并按「请求是否带 modern `_meta`/是否是 `initialize`」分流。

### 5.1 HTTP 层（两代共用）

- 只接受 POST；GET、DELETE 回 `405`（带 `Allow: POST`）。
- 校验 `Origin`：存在且不合法回 `403`。
- 认证失败回 `401`（见 3.4）。
- `Content-Type: application/json`；`Accept` 含 `application/json` 和 `text/event-stream`。
- 限制请求体大小。
- 正文解析失败：`-32700`；不是合法请求对象：`-32600`。
- 响应一律 `Content-Type: application/json`，单个 JSON-RPC 响应对象；通知回 `202` 无正文。
- JSON-RPC `id` 可能是字符串或数字，原样回传。

### 5.2 legacy（`2025-11-25`）需要的方法与字段

来源：<https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle>、<https://modelcontextprotocol.io/specification/2025-11-25/server/tools>、<https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/ping>、<https://modelcontextprotocol.io/specification/2025-11-25/schema>

| 方法 | 请求 `params` | 响应 `result` |
|---|---|---|
| `initialize` | `protocolVersion`（字符串）、`capabilities`（对象）、`clientInfo`（`name`、`version`，可选 `title` 等） | `protocolVersion`（支持则回同一个，否则回自己支持的最新版）、`capabilities`（`{"tools":{}}`，可带 `listChanged`）、`serverInfo`（`name`、`version`，可选 `title`）、可选 `instructions` |
| `notifications/initialized` | 无 | 通知，无响应；HTTP `202` |
| `ping` | 无 | `{}` |
| `tools/list` | 可选 `cursor` | `tools`（数组）、可选 `nextCursor` |
| `tools/call` | `name`、可选 `arguments`（对象）、可选 `_meta`（如 `progressToken`） | `content`（数组，必有）、可选 `structuredContent`（**必须是 JSON 对象**）、可选 `isError` |
| `notifications/cancelled` | `requestId`、可选 `reason` | 通知；可只回 `202` 不处理 |

HTTP 头：initialize 之后的请求带 `MCP-Protocol-Version`；值不支持回 `400`。不发 `MCP-Session-Id` 即无会话。

### 5.3 modern（`2026-07-28`）需要的方法与字段

来源：<https://modelcontextprotocol.io/specification/2026-07-28/server/discover>、<https://modelcontextprotocol.io/specification/2026-07-28/server/tools>、<https://modelcontextprotocol.io/specification/2026-07-28/basic>、<https://modelcontextprotocol.io/specification/2026-07-28/schema>

每个请求的 `params._meta`：

| 键 | 必需 | 说明 |
|---|---|---|
| `io.modelcontextprotocol/protocolVersion` | 是 | 缺失回 `-32602` + HTTP 400 |
| `io.modelcontextprotocol/clientCapabilities` | 是 | 对象，可为空 |
| `io.modelcontextprotocol/clientInfo` | 否 | `name`、`version` |
| `progressToken`、`io.modelcontextprotocol/logLevel`、`traceparent` 等 | 否 | 不用这些功能时忽略 |

HTTP 头：`MCP-Protocol-Version`（必带，须与 `_meta` 一致）、`Mcp-Method`（必带）、`Mcp-Name`（`tools/call` 必带，可能是 Base64 哨兵格式）。

| 方法 | 请求 `params` | 响应 `result` |
|---|---|---|
| `server/discover`（MUST 实现） | 只有 `_meta` | `resultType: "complete"`、`supportedVersions`（数组）、`capabilities`、可选 `instructions`、`ttlMs`、`cacheScope`、`_meta["io.modelcontextprotocol/serverInfo"]`（SHOULD） |
| `tools/list` | 可选 `cursor` | `resultType`、`tools`、可选 `nextCursor`、`ttlMs`（≥0）、`cacheScope`（`"public"`/`"private"`）、`_meta` 里的 `serverInfo`（SHOULD） |
| `tools/call` | `name`、可选 `arguments`；（MRTR 的 `inputResponses`、`requestState` 不用可忽略） | `resultType: "complete"`、`content`（必有）、可选 `structuredContent`（任意 JSON 值）、可选 `isError` |

本版本没有 `initialize`、`ping`。只支持 modern 的服务端收到 `initialize` 时，规范要求在返回的错误里写明自己支持的协议版本（SHOULD）。`subscriptions/listen` 只在声明了 `listChanged` 时才有通知可发；规范没有把它列为必须实现的方法，未实现的方法按 `404` + `-32601` 处理。

### 5.4 工具定义（两代基本相同）

- `name`（建议 1–128 字符，仅 `A-Za-z0-9_-.`）
- 可选 `title`、`description`、`icons`、`_meta`
- `inputSchema`：必须是合法 JSON Schema 对象；无 `$schema` 时按 2020-12；无参数工具推荐 `{"type":"object","additionalProperties":false}`
- 可选 `outputSchema`：提供了的话，服务端的 `structuredContent` 必须符合它
- 可选 `annotations`：`title`、`readOnlyHint`（默认 false）、`destructiveHint`（默认 true）、`idempotentHint`（默认 false）、`openWorldHint`（默认 true）；全部只是提示
- `content` 的文本块：`{"type":"text","text":"..."}`；返回 `structuredContent` 时应同时在一个文本块里放序列化后的 JSON（SHOULD，向后兼容）

### 5.5 错误

| 情况 | 表达方式 |
|---|---|
| 解析失败 / 非法请求 / 方法不存在 / 参数非法 / 内部错误 | JSON-RPC `-32700` / `-32600` / `-32601` / `-32602` / `-32603` |
| 未知工具、不满足 CallToolRequest 结构 | 协议错误 `-32602` |
| 输入校验失败、业务错误、下游失败 | 工具执行错误：正常 `result`，`isError: true`，`content` 里写可供模型自我纠正的说明 |
| （modern）头与正文不符或缺必需头 | `-32020` + HTTP 400 |
| （modern）缺少必需的客户端能力 | `-32021` + HTTP 400，`data.requiredCapabilities` |
| （modern）协议版本不支持 | `-32022` + HTTP 400，`data.supported`、`data.requested` |
| （modern）方法未实现 | `-32601` + HTTP 404 |

`-32000` 到 `-32019` 是遗留区间，新实现不应使用；`-32020` 到 `-32099` 只能用规范定义的码。应用自定义错误码应在 `-32768` 到 `-32000` 之外。

本项目注册表的错误分类（`backend/internal/steward/tool/errors.go`）有 `tool_not_found`、`permission_denied`、`tool_scope_unsupported`、`validation_failed`、`not_found` 五种，以及处理器原始错误。按上表，「未知工具」对应协议错误 `-32602`，「输入校验失败」对应 `isError: true`；其余几种规范没有逐一规定。

### 5.6 规模估计

以下是估计，不是测量：

- 只做一代、只回 JSON、无会话：HTTP 入口 + JSON-RPC 编解码 + 三四个方法 + 错误映射，约 300–450 行 Go，测试另计。
- 两代都做：再加版本分流、modern 的头校验（含 Base64 哨兵解码）、`_meta` 校验、`resultType`/缓存字段，合计约 500–700 行，测试另计。
- 参照：SDK 的 `mcp/streamable.go` 单文件就有 2700 多行，但其中大部分是会话、SSE、断点续传、客户端等本需求用不到的部分。

### 5.7 主要风险与规范升级时要跟进的内容

- **规范变动幅度**：`2025-11-25` → `2026-07-28` 是一次不向后兼容的改动（握手、会话、ping、GET 流全部移除，新增必需头和必需方法）。规范采用日期版本，「不兼容变更才换版本号」；自实现需要自己跟每一版的 changelog。
- **客户端推进节奏**：Claude Code 在 v2.1.232–v2.1.274 之间把默认运行时切到了支持 `2026-07-28` 的 v2。旧握手目前仍作为回退保留；文档没有说明保留到什么时候。
- **容易漏的细节**：`Mcp-Name` 的 Base64 哨兵解码；头与正文的一致性校验；`MCP-Protocol-Version` 不支持时必须回可识别的 JSON-RPC 错误（否则 dual-era 客户端会误判为 legacy 服务端并退回 `initialize`）；`id` 的字符串/数字两种类型；通知不能回正文。
- **规范新增的 SHOULD/MUST**：例如每个结果的 `serverInfo`、`ttlMs`/`cacheScope`、错误码区间政策，都是这一版才加的。
- **弃用登记**：<https://modelcontextprotocol.io/specification/2026-07-28/deprecated> 列出进入弃用期的特性（Roots、Sampling、Logging、HTTP+SSE、DCR），最短十二个月后可移除；本需求用不到这些。
- **没有官方一致性测试可直接套用到自实现上**这一点未核实：SDK 仓库里有 `conformance/` 目录，但本次没有查它能否独立用于第三方服务端。

## 6. 对比小结

- **用 SDK**：净增 7 个模块（含 `x/oauth2`、`segmentio/encoding` 等本需求用不到但会编进来的）；约 60 行接入代码；两代协议、头校验、错误码、`server/discover` 现成，规范升级时主要是升 SDK 版本。
- **用 SDK 的代价**：按请求构造 Server 且每个 POST 构造两次；Origin 校验要自己在外层加；低层 `AddTool` 不做校验、schema 不合规会 panic；行为变更靠 `MCPGODEBUG` 过渡两个小版本后强制生效；自带的 Bearer 中间件默认要求令牌有过期时间。
- **自己实现**：零新依赖，符合项目习惯；估计 300–700 行加测试；线上行为完全自己掌握。
- **自己实现的代价**：要自己跟规范（上一次升级是不兼容改动）；要自己决定做一代还是两代，并承担对应的客户端兼容面；头校验和版本协商的细节容易出错，出错的表现是客户端静默退回旧协议或直接连不上。
- **两者共同**：认证中间件、按用户过滤、401 响应头、`Registry.Invoke` 的错误到 MCP 错误的映射，无论哪种都要自己写。

## Findings 补充

### Files Found（项目内）

| File Path | Description |
|---|---|
| `backend/go.mod` | 当前 5 个直接依赖、4 个间接依赖；`go 1.25.0` |
| `backend/internal/steward/tool/definition.go` | `Definition`（`Input`/`Output` 为 `json.RawMessage`）、`Caller`、`Role`、`Scope`、`Result`；`SourceChat` 是目前唯一的来源常量 |
| `backend/internal/steward/tool/registry.go` | `Registry.List(caller)` 按身份过滤并按名称排序；`Registry.Invoke` 逐次鉴权、只放行 `ScopeRead`、校验输入、输出上限 64 KiB |
| `backend/internal/steward/tool/errors.go` | `*tool.Error` 与五个错误分类码 |
| `backend/internal/steward/httpapi/events.go:77` | 现有聊天入口构造 `tool.Caller{User: user, Source: "chat"}` 的位置 |
| `backend/internal/steward/runtime/run.go:117`、`run_tools.go:29` | 现有对 `registry.List` / `registry.Invoke` 的调用 |
| `backend/cmd/roomusic/main.go:182-186` | 主 HTTP mux 的创建与路由挂载处 |
| `backend/internal/platform/weborigin/weborigin.go` | 项目已有的 Origin 相关实现 |

### Related Specs

- `.trellis/spec/backend/steward-runtime-contracts.md` — 管家运行时合同
- `.trellis/spec/backend/identity-contracts.md`、`.trellis/spec/backend/identity-origin-contracts.md` — 身份与 Origin 合同
- `.trellis/spec/backend/error-handling.md` — 错误处理约定

（以上三类规范本次只确认了存在，没有逐条对照。）

### External References

- [MCP 规范 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28) — 现行版本
- [MCP 规范 2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25) — 旧握手版本，Claude Code 的回退路径
- [go-sdk v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0) — `docs/protocol.md`、`docs/server.md`、`docs/mcpgodebug.md`、`examples/auth/server/main.go`
- [pkg.go.dev: mcp 包](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.8.0/mcp)
- [Claude Code MCP 文档](https://code.claude.com/docs/en/mcp)
- [Claude Code 环境变量](https://code.claude.com/docs/en/env-vars)
- [RFC 9110 §11.6.1](https://www.rfc-editor.org/rfc/rfc9110.html#section-11.6.1)、[RFC 6750 §3](https://datatracker.ietf.org/doc/html/rfc6750#section-3)、[RFC 9728](https://datatracker.ietf.org/doc/html/rfc9728)

## Caveats / Not Found

- **没有用真实的 Claude Code 连过探测服务**。Claude Code 的行为全部来自官方文档；它探测新协议时具体发什么请求（`server/discover` 还是直接发业务请求）、对未声明 `listChanged` 的服务端是否仍会发 `subscriptions/listen`，文档没写，未核实。
- **Claude Code 与 go-sdk 的 issue 列表没有查成**：GitHub 搜索接口当天被限流，`gh search` 没返回相关结果。第 4 节的「已知坑」只覆盖官方文档写明的内容，不含社区报告的问题。
- 3.4 节关于「静态 Bearer、不做 OAuth」的结论是对规范文字的解读；规范没有针对这种服务端的直接条文。
- SDK 探测中 `prompts/list` 在未登记任何 prompt 时返回 200 空列表，而规范对未实现方法的要求是 404 + `-32601`。只记录现象，没有查 SDK 是否把它视为已实现的方法。
- 与本项目数据形态相关的两点事实，供设计时对照：
  - `Registry.Invoke` 在输出超过 64 KiB 时把 `Result.Output` 换成一个 JSON 字符串（原始 JSON 的前缀），此时它不再符合工具声明的输出 schema（`definition.go` 的 `Result` 注释）。`2025-11-25` 要求 `structuredContent` 是对象，`2026-07-28` 允许任意 JSON 值但要求符合 `outputSchema`。
  - `Registry.Invoke` 目前只放行 `ScopeRead` 的工具，而 `Registry.List` 按身份过滤、不按 scope 过滤，所以列表里可能出现当前入口调不了的工具（`registry.go:61-62` 的注释）。
- 5.6 节的行数是估计。
- SDK 的 `conformance/` 目录能否用于验证第三方服务端，没有查。
- 探测程序和下载的 SDK 源码、规范页面都在会话临时目录，没有留在仓库里；仓库内只新增了本文件。
