# 设计：外部接口 mock 服务器

## 组成

```
backend/cmd/roomusic-upstream-mock/main.go   参数解析、启动、优雅退出
backend/internal/upstreammock/
  server.go        路由、情景中间件、请求记录、控制接口
  scenario.go      情景定义与按服务的状态（并发安全）
  data.go          数据模型、内置合成数据（embed）、-data 目录加载
  musicbrainz.go   /musicbrainz/ws/2/...
  coverart.go      /coverartarchive/...（清单与图片，直接返回，不重定向到其他主机）
  lrclib.go        /lrclib/api/get、/lrclib/api/search
  acoustid.go      /acoustid/v2/lookup
  model.go         /openai/v1/{responses,chat/completions,models}、/anthropic/v1/{messages,models}；SSE 流
  script.go        模型脚本：匹配规则 → 文字或工具调用
  search.go        /searxng/search、/tavily/search
  testdata/        内置合成数据（JSON、生成的小图片）
```

路径前缀之后的部分与真实服务一致，后端客户端只换基础地址即可：
MusicBrainz 基础地址 `<mock>/musicbrainz/ws/2`，Cover Art Archive `<mock>/coverartarchive`，LRCLIB `<mock>/lrclib`，
AcoustID `<mock>/acoustid`，Tavily `<mock>/tavily/search`。用户在设置里填的端点：OpenAI 兼容 `<mock>/openai/v1`，
Messages `<mock>/anthropic`，SearXNG `<mock>/searxng`。具体拼接规则以 `provider/endpoint.go` 与各客户端为准，mock 的路由按它们实际发出的路径写。

## 情景

每个服务（`musicbrainz`、`coverartarchive`、`lrclib`、`acoustid`、`openai`、`anthropic`、`searxng`、`tavily`）一个当前情景，默认 `ok`。
情景在中间件里处理，正常处理器只管 `ok`、`slow`、`not_found`、`malformed` 的数据部分：

| 情景 | 行为 |
| --- | --- |
| `ok` | 按数据返回；查不到时按该服务的实际做法返回空结果或 404 |
| `not_found` | 一律按“查不到”返回 |
| `unavailable` | 503，带 `Retry-After: <秒>`（参数可设，默认 2） |
| `rate_limited` | 按该服务的实际限流响应：MusicBrainz 503、其他 429；带 `Retry-After` |
| `timeout` | 不写响应，直到请求的上下文结束 |
| `slow` | 延迟（参数可设，默认 3 秒）后正常返回 |
| `malformed` | JSON 服务返回截断的 JSON；图片返回非图片字节；SSE 返回不合格式的事件 |
| `drop` | 写出响应头与一部分正文后劫持连接并关闭；SSE 在第一个内容事件后断开 |

控制接口：
- `GET /_mock/scenarios` → `{"musicbrainz":{"scenario":"ok","retry_after":2,"delay_ms":3000}, ...}`
- `PUT /_mock/scenarios/{service}` 请求体 `{"scenario":"unavailable","retry_after":5}`；未知服务或情景返回 400。
- `GET /_mock/requests?service=&limit=` → 最近的请求（环形缓冲，默认保留 500 条）：时间、服务、方法、路径、查询、状态码。查询里的密钥参数（AcoustID 的 `client`、`user`，以及 `key`、`api_key`、`token`）的值替换为 `***`；请求头与请求体不记录。
- `POST /_mock/reset` → 情景全部回到 `ok`，清空请求记录。

启动参数：`-addr`（默认 `127.0.0.1:18090`）、`-data`（可选数据目录，覆盖内置数据）、`-script`（可选模型脚本文件）、
`-scenario service=name`（可重复）。

## 数据

内置数据是虚构的：若干艺人（含中文名与英文名、一个同名不同人的例子）、发行（单碟、多碟、单曲、同一发行组的两个版本）、
录音（带 ISRC、时长）、封面（程序生成的小 PNG/JPEG）、歌词（同步与纯文本各有）、AcoustID 指纹对应（指纹字符串 → 录音 ID，
另有“对上多个录音”“没有结果”的例子）。所有 ID 是固定的 UUID，测试与文档可以引用。

`-data <目录>` 读同结构的 JSON 文件替换内置数据，便于写自己的情景。

MusicBrainz 只实现后端实际调用的查询（查后端客户端与 `finders.go` 的调用面）：按 MBID 读取发行、发行组、录音、艺人，
按条码、编号、ISRC、文字的搜索；`inc` 参数按后端实际传的组合返回相应字段。

## 模型端点

- 三种协议的流式事件格式按后端解码器（`provider/openai.go`、`provider/messages.go`）实际解析的事件实现，包括用量事件。
- 模型列表返回脚本里声明的模型名（默认 `mock-model`、`mock-reviewer`）。
- 端点检测（`provider/probe.go`）需要的工具调用与原生搜索检测：工具调用检测按脚本内置规则返回工具调用；原生搜索不支持（按“不支持”的响应返回），后端据此判定。
- 脚本（JSON）：规则列表，按顺序匹配，每条有 `when`（`last_user_contains`、`tool_result_contains`、`tool_available`、`model`）和
  `reply`（`text` 或 `tool_calls: [{name, arguments}]`）。没有规则命中时回复固定文字。内置脚本覆盖：普通问答、
  自动整理模型层的匹配判定（调用后端提供的判定工具）、独立审查（调用 `submit_review` 批准）。工具名与参数以后端的工具定义为准。

## 后端改动

- `platform/config`：读 `ROOMUSIC_UPSTREAM_MOCK_URL`，必须是带主机的绝对 http/https 地址，否则启动失败；存进 `Server.UpstreamMockURL`。
- `app.go` 组装：设了时给四个客户端设 `BaseURL`，给 Tavily 设地址（`search.go` 的 `tavilyURL` 改为服务的可配置字段），并启动时 `logger.Warn`。
- 出站策略：外部源的 `Requester` 与 Tavily 客户端在设了 mock 时用 `PublicOrAllowlist`，放行名单只匹配 mock 的主机与有效端口；
  Cover Art Archive 的重定向判断同时接受 mock 主机。没设时代码路径与现在一致。
- 不改数据库、接口样本与前端。

## 兼容与回退

- 没设变量时逐项不变（A2）；回退即删掉这个变量或回滚提交。
- mock 程序不进生产镜像（`deploy/` 的 Dockerfile 只构建 `cmd/roomusic`，确认不受影响）。

## 取舍

- 不做成“录制回放真实响应”：会复制外部的真实数据，也要联网录制；手写的合成数据更可控。
- 不把 mock 放进 `compose.dev.yaml`：开发后端本身在宿主机运行，同在宿主机最简单；试用实例需要时在其 Compose 里另加服务即可（文档给出写法）。
