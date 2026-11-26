# 管家联网合同

## 1. 范围与触发

C8 的外部搜索、网页读取、MusicBrainz 与 Cover Art Archive 清单由 `steward/web` 拥有，
`internal/app/steward.go` 装配工具与设置测试共用的 `web.Service`。修改这些能力时读取本合同、
[运行时合同](./steward-runtime-contracts.md)、[日志规范](./logging-guidelines.md)与
[ADR 0005](../../../docs/architecture/adr/0005-steward-data-and-egress.md)。

C8 后端乙补齐 Responses、模型自带搜索请求、原始内容块持久化与 search/citations 投影。
模型协议处理由 provider 拥有；runtime 负责原始块的端点归属、重放与步骤限制，httpapi 负责用户设置与线上形状。

## 2. 接口与存储签名

- `web.New(SettingsReader, *secret.Store, EndpointClient, *slog.Logger) *Service`。
- `Service.StewardTools() []tool.Definition`：四个工具都是成员可见、`read`、`Untrusted=true`。
- `Service.Search(ctx, user, query, limit)`、`FetchPage(ctx, address)`、`SearchMusic(ctx, entity, query, limit)`、`GetMusic(ctx, entity, mbid, include)`。
- `httpapi.OutboundClient(ctx, user, store)` 同时服务模型端点与 SearXNG；不跟随重定向。
- `Definition.Available func(context.Context, Caller) bool` 为可选的运行时可用性谓词。
  `Registry.ListContext(ctx, caller)` 按身份和 Available 过滤；`List(caller)` 使用后台上下文保留兼容入口。
  `Invoke` 与 `PreviewCall` 同样复查，失败返回 `permission_denied`。列表副本不含 Handler、Preview、Available。
- 聊天每轮运行开始时用 `ListContext` 生成模型工具；MCP 每次请求用同一路径构建工具列表，调用时再次检查。
- 迁移 `0056_steward_web.sql` 增加 `steward_user_settings.search_source`（默认 none，枚举 CHECK）、
  `searxng_url`（默认空）、`search_key_ciphertext bytea`、`search_key_last_four`（最多四字符，无密文时必须为空）。
  给已有非空探测报告补 `native_search=unknown`、`responses_available=false`；down 删除新列、约束和两个探测字段及 native_search 覆盖项。
  同一个 0056 将 protocol CHECK 扩展为空串/openai/messages/responses；steward_messages 增加可空的
  `provider_blocks jsonb`，仅 assistant 允许保存 JSON 对象。down 删除该列和约束，把设置/覆盖/探测中的 responses
  改回 openai，并移除 Responses 探测失败条目，再恢复原协议 CHECK。

## 3. 请求、响应与边界

手动艺人队列使用发起管理员的模型和搜索配置，整理开关单独控制整理任务。艺人身份由 MusicBrainz 发行结构核对，未匹配的候选供人工选择。每位艺人最多执行 10 次工具调用。每次模型请求和重试前检查共享日账与本项剩余的 50000 token，按完整协议请求估算输入并限制输出。已发送请求按上游报告结算，最后一次实耗可越过余量，触线后停止发送。上游完全缺报 usage 时，本项扣除输入估算加此次输出上限，共享日账仅记录已报告用量。 发行整理的联网档使用整理开启者的搜索设置。启用网络搜索来源且依赖就绪时，SearXNG 或 Tavily 提供 `web_search`，同时可用 `fetch_page`。选择 native 且能力判定为 supported 时，由模型自带搜索并提供 `fetch_page`。来源关闭后停止提供后台联网工具，已启用原生搜索的运行在下一步重新检查。模型整理图片逐跳使用 `egress.PublicOnly`，引用 URL 只按相同策略预检解析结果。两者均限 HTTP(S) 的 80 或 443 端口，管家出站放行名单对两者均无效。配置 `ROOMUSIC_UPSTREAM_MOCK_URL` 后，仅该 mock 的主机与有效端口获额外放行。

### 设置与测试搜索

`GET/PUT /api/v1/me/steward/settings` 响应新增：

```json
{"search_source":"none","searxng_url":"","search_key_saved":false,"search_key_last_four":"","search_key_needs_reentry":false}
```

PUT 增加 `search_source`（none/searxng/tavily/native）、`searxng_url`、只写 `search_key`。
密钥与模型共用 `parseKey`、`mergeKey`、`encryptKey`、`keyView` 与 `secret.Store`，仅用途分别是
`steward.search` 与 `steward.model`：省略保留，null 清除，空串/纯空白/控制字符拒绝，非空加密并保存末四个 Unicode 字符。
不能解密时仍保留密文与末四位，needs_reentry 为 true；请求、错误、日志与响应不回显密钥。

SearXNG 地址使用模型端点的 URL 校验规则；仅本次 PUT 提交了 searxng_url，或合并后 search_source=searxng 时，对非空 SearXNG 地址预检。
管理员地址不限，成员必须为公网或命中实例放行名单。仅修改搜索字段不清除模型探测。

`probe_result` 增加 `native_search`（supported/unsupported/unknown）、`responses_available`（布尔）；
模型名为空时写 unknown/false；有模型时的探测规则见下文，不根据域名或模型名称猜测。
`overrides.native_search` 使用同样三态，覆盖优先于探测；仅本次显式提交 `search_source=native` 且生效值不为 supported 时返回 400 invalid_input；已存 native 的用户改其他字段不被此校验拒绝。
模型改变时探测中的 native_search 失效为 unknown，既有 native_search 覆盖项清除；本次显式提交的新覆盖值保留。
端点、密钥与协议变化沿用原探测失效规则。
协议取值增加 responses；OpenAI 兼容端点在 Responses 工具调用探测通过时探测结论就是 responses，否则为 openai。
配置为 native 且当前协议 openai、生效 native_search=supported（含手动覆盖）时，
对话入口使用 Responses，保留已存默认协议；探测结论或手动选择为 responses 则直接使用 Responses。
自动切换时不沿用 Chat Completions 的工具结论，显式 tool_support 覆盖仍优先。

`POST /api/v1/me/steward/settings/search-test` 无请求体，使用已保存设置和固定查询 `MusicBrainz`，最多 3 项。
响应始终是业务结果 200：`{"ok":true,"result_count":0}`（零也要输出）或 `{"ok":false,"code":"search_not_configured"}`。
身份、同源、成员开关与数据库边界使用现有 HTTP 路由合同。

### 原生搜索与 Responses（后端乙）

- `provider.NewResponses(client, endpoint, key)` 与既有适配器使用同一出站客户端、超时、取消、SSE 大小上限与错误分类。
- `provider.Request.NativeSearch bool` 由已认证入口根据 `search_source=native` 和 `effectiveNativeSearch=supported` 同时决定。
  `ForceTool` 非空时两种协议都不附加搜索工具。C7 继续只发独立的 submit_review 请求，不读取主会话或 provider_blocks。
- Responses 请求为 `POST {base}/responses`，`stream:true`、`store:false`、`input`、可选 instructions/max_output_tokens；
  include 含 reasoning.encrypted_content，搜索时另含 web_search_call.action.sources。
  不发送 max_tool_calls：[OpenAI 官方请求文档](https://developers.openai.com/api/reference/resources/responses/methods/create)有这个参数，
  但 2026-11-20 实测 Codex 类上游以 "Unsupported parameter: max_tool_calls" 拒绝整个请求；搜索次数只受单次运行的步数与 token 上限约束。
  函数定义是平铺的 type/function、name、description、parameters、strict:false；保留现有 schema 的可选字段。
  强制具名工具使用 `{"type":"function","name":"submit_review"}` 与 parallel_tool_calls:false。
  输出 function_call 用 call_id 关联本地结果，回传 `{"type":"function_call_output","call_id":"…","output":"…"}`。
- Responses 文字消费 response.output_text.delta（拒答消费 response.refusal.delta）；
  response.output_item.done 是完整参数、输出项及加密内容的权威版本，参数增量不重复执行工具。
  message 的 output_text.annotations 中 url_citation 投影引用；web_search_call 投影搜索。
  response.completed 归为正常或工具调用；response.incomplete 的 max_output_tokens 归为输出上限，其他原因保留 other；
  response.failed 与顶层 error 按现有错误类别返回，rate_limit_exceeded 归为 rate_limit。
  未结束的输出项不能被 completed 冒充成功；达到 token 上限的非法参数调用不执行、不重放。
  usage.input_tokens/output_tokens 是最终累计值，不另加 cached/reasoning 明细。
- Messages 请求附加 `{"type":"web_search_20250305","name":"web_search","max_uses":5}`，不启用代码执行或动态过滤。
  server_tool_use.input 从 input_json_delta 还原；web_search_tool_result 用 tool_use_id 关联查询词，
  citations_delta.citation 追加到原文本块 citations；encrypted_content/encrypted_index 及未知原字段保留。
  HTTP 200 的 web_search_tool_result_error 转为 search.status=failed、error_code=search_failed，不将整次回答判为 HTTP 错误。
- 两个适配器的 EventDone.ProviderBlocks 按原顺序提供完整输出块；runtime 将
  `{protocol,endpoint,model,blocks:[{protocol,data}]}` 存入 provider_blocks。可见正文、本地工具历史及 searches/citations 存 content。
  同协议、同端点、同模型时优先使用完整原块，不能再追加同一份可见文字或调用；不匹配时只用普通文字和本地工具历史，
  不转成 user/system 指令，不把搜索投影重新拼入提示词。旧 content 中没有端点归属的 opaque 块不再重放；旧 provider_blocks 缺少模型归属时不向已选模型回放。
  单条消息的 provider_blocks 连同归属封装 JSON 序列化后最多 256 KiB；超限不落库原始块，保留文字、展示投影和本地工具历史。
  OpenAI Chat Completions 的原 refusal 字段也绑定端点保存，重放为原 assistant 扩展字段。
- pause_turn 原样回传助手块后继续，每次调用计入 24 步和 token 限制；达到限制仍保留已完成块。
  continue 也接受最后一条助手消息的 finish_reason=pause_turn。确认模式下 shared 工具仍经过已有方案确认入口。
- 探测先尝试地址预设，再按 openai/messages 去重尝试模型列表。
  Messages 从选中模型 capabilities.server_tools.web_search.supported 读取 supported/unsupported，缺失为 unknown。
  OpenAI 端点先在 Responses 下做工具调用探测：调用了工具则协议定为 responses；请求完整结束就置 responses_available=true，
  没调用工具或请求出错时协议回退为 openai 并在 Chat Completions 下再测工具（步骤见运行时合同第 6 节）。
  responses_available=true 时随后发一次带 web_search 的最小请求。
  出现 completed 搜索才置 native_search=supported；明确错误码 tool_unsupported（包括搜索工具拒绝）为 unsupported；没搜索、搜索失败、其他错误为 unknown。
  Responses 可用性失败和原生搜索失败分别记录 failures 的 responses/native_search 步骤，不抹去成功的基础探测。

SSE 严格采用任务 wire-shapes 的顶层字段：

```json
{"type":"search","step":1,"search_id":"搜索标识","query":"查询词","status":"completed","results":[{"title":"标题","url":"https://example.org"}]}
{"type":"citations","step":1,"citations":[{"url":"https://example.org","title":"标题"}]}
```

failed 的 search 带 error_code=search_failed，results 仍为数组。citations 在该步文字结束后发一次，没引用则不发。
消息详情新增 searches（去掉 type/step 的 search 数组）、citations，缺省均为 []；绝不暴露 provider_blocks。
引用及搜索来源只保留 HTTP(S) URL，按 URL 去重并保持顺序；同一 URL 后续有标题时补齐空标题。
每次搜索最多 10 条结果，每步最多 20 条引用；query 最多 400 个 rune、title 最多 200 个 rune，超长按 rune 截断；
URL 最多 2048 个 rune，超长丢弃整条来源。适配器与运行时共用 provider 的投影规则，不改变线上字段形状。
Responses 的 action.query/queries 可以缺失，sources 官方字段可能只有 URL：缺失 query/title 留空串，不捏造来源。
多个 queries 用换行连接；open_page/find_in_page 也用同一个 search_id 投影访问过程，结果列出其 URL。

原生搜索只记录 `steward.search.finished` 的 search_count、failed_count、duration_ms（本模型步骤耗时）、error_code
及固定事件元字段（message=管家模型搜索结束、module=steward）。查询词、结果、引用正文、原始块不进日志；解析失败的公开错误也不拼接原始 SSE 帧。

### 搜索与抓取

| 工具 | 输入 | 输出 |
| --- | --- | --- |
| web_search | q 必填且非空；max_results 默认 5，1–10 | results 数组，每项 title/url/snippet |
| fetch_page | url 必填，最多 2048 字节 | url/final_url/title/text/truncated |
| search_music_metadata | entity 为 artist/release/release-group/recording；query 非空；limit 默认 5，1–10 | results 数组 |
| get_music_metadata | entity、合法 UUID mbid；可选 include 白名单 | metadata 与可选 covers |

web_search 仅在来源为 searxng 或 tavily 时可用，未填地址或密钥的错误在调用时报告。
四个工具在已认证调用入口按用户共用进程内滚动一分钟额度（跨聊天、MCP 与 Service）：
fetch_page 每用户每分钟 30 次（fetchPagePerMinute），web_search 每用户每分钟 30 次（webSearchPerMinute），
search_music_metadata 与 get_music_metadata 合计每用户每分钟 20 次（musicMetadataPerMinute）。
这些上限是 web/limiter.go 包内常量；超过立即返回 rate_limited，不发出请求；已放行的失败调用也占额度。
用户记录惰性创建，每次访问裁掉过期时间戳，访问时至多每分钟扫描删除不活跃条目，不累计历史用户记录。
SearXNG 用 `GET {base}/search?q=…&format=json`，支持基础路径；Tavily 固定
`POST https://api.tavily.com/search`，Bearer 密钥、basic 搜索、不请求 answer/raw_content。
两者只传查询词与程序固定参数，不传用户、实例或曲库标识；只保留 results 中的 title/url/content。
Tavily 使用 egress.PublicOnly，固定域名解析到非公网地址也拒绝；测试通过替换 newClient 注入本地服务。
读取总时限 20 秒，JSON 响应最多 2 MiB；不重试。

fetch_page 使用 egress.PublicOnly，管理员与放行名单均不能放宽；每次请求和每跳重定向检查
HTTP(S)、无 userinfo、80/443 端口、URL 字节数，拨号由 egress 复核全部解析地址。
只发 GET，不读 robots.txt，最多 5 次重定向，总时限 20 秒。固定通用 User-Agent；
无 Cookie、认证、实例信息；重定向重建请求头，移除 Go 自动生成的 Referer。
只收 HTML、纯文本、JSON、XML（含 application/*+json 与 application/*+xml）；错误 Content-Type 不读正文。
Go Transport 解 gzip 后最多读取 2 MiB，并用一个额外字节判断截断；文本上限 100 KiB，按 UTF-8 边界截断。
未声明字符集只接受 UTF-8；声明时使用 x/text/htmlindex 支持的常见编码转码，转码输出也受 2 MiB 上限约束。

HTML 使用 x/net/html 分词，移除 script/style/nav/header/footer/aside/form/noscript；这些非 void 标签即使写成自闭合也按起始标签跳过其内容。提取 title，
保留段落、列表、表格及链接文字，压缩空白；纯文本、JSON、XML 不解析内容，只转码和截断。
所有工具仍受注册表已有的 **64 KiB JSON 编码上限**：超过时变为 JSON 文本前缀字符串、Result.Truncated=true；
所以 fetch_page 的 100 KiB 是提取上限，不能视为模型最终可得到的文字量。

### 音乐资料

M7 B1 起，两个工具经 `sources/musicbrainz` 的适配器请求 MusicBrainz（实体级的 `Lookup` 与 `Search`，对话身份），
与后台自动整理共用同一个适配器实例、同一个每秒一次的限速器与同一份缓存；规则见[外部源合同](./sources-contracts.md)。
工具的名字、参数、输出字段与每用户额度没有变化：

- 固定 `https://musicbrainz.org/ws/2`，请求带 fmt=json，PublicOnly；User-Agent 为 `ROOMusic/<构建版本或 dev> ( <buildinfo.ProjectURL> )`，不含实例信息。
- 对话调用排队超过 10 秒返回 rate_limited，取消立即退出；503 不重试；响应读取上限 2 MiB。
- 错误码的对应：适配器的 `ErrRateLimited` → `rate_limited`；`ErrNotFound`、`ErrUnavailable`、`ErrTryLater` → `source_unavailable`。
- 适配器多解出的字段（ISRC、别名、disc ID 等）不进入工具输出。
- 有结果的响应缓存 30 天、空结果 1 天：有效期内同一查询读到的是缓存，不反映 MusicBrainz 上更新的编辑。
- **自动整理的总开关与各源开关不影响这两个工具**：对话里的查询是用户本人发起的请求。
- `web.New` 自己构造一个不缓存的默认适配器；`app` 用 `WithMusic` 换成带缓存的共用实例。测试经适配器的构造参数替换基础地址、
  客户端工厂与限速器；不添加用户配置项。
- Cover Art Archive 的清单读取走 `sources/coverartarchive` 适配器的 `Covers`（M7 子任务 B2）：与 MusicBrainz 共用每秒 1 次的限速器（对话优先，
  排队超过 10 秒 `rate_limited`），重定向只放行官方主机的 `index.json`；应用装配的共用实例有缓存（200 存 30 天，404 存 1 天）。
  输出不变：`covers[]` 的 `url`、`front`、`back`、`thumbnails`（清单里是 null 时仍是 null），不下载图片。重定向被拒绝是 `source_unavailable`。
  `web.New` 自己构造一个不缓存的默认实例（客户端工厂跟随 `newClient` 的测试注入）；`app` 用 `WithCovers` 换成与后台共用的实例。

include 白名单：

| entity | include |
| --- | --- |
| artist | releases、release-groups、recordings |
| release | artist-credits、recordings、labels、release-groups、cover-art |
| release-group、recording | artist-credits、releases |

cover-art 是本工具的本地选项，不传给 MusicBrainz；只对 release 读取
`https://coverartarchive.org/release/{mbid}/`，404 表示无封面。返回 URL、front/back、thumbnails，不下载图片。
官方清单接口可能 307 到 Archive.org 的 index.json：只允许 coverartarchive.org、archive.org 或其子域的
`/index.json`，逐跳通过 PublicOnly、URL/端口与五次重定向限制；不向其他主机或图片地址跟随。

元数据显式投影为 MBID、标题/艺人名、消歧说明、署名、日期、国家、条码、厂牌与编号、介质、曲目表、录音与时长（毫秒）。
未列出的响应扩展、账户、注释与关系不进入工具输出。

### 日志

`steward.web.request.finished` 只记录来源类型、域名、duration_ms、result_count、error_code 和固定事件元字段。
不记录查询词、URL 路径/查询串、请求/响应体、网页内容、密钥或底层错误字符串。
工具运行日志沿用现有合同；工具处理器只产生固定说明的 `*tool.Error`。

## 4. 校验与错误矩阵

| 条件 | 错误码 |
| --- | --- |
| 搜索来源 none/native，或 SearXNG 地址未填 | search_not_configured |
| SearXNG 403 | searxng_json_disabled（“这个实例没有开启 JSON 输出”） |
| Tavily 密钥缺失/无法解密/401 | search_key_invalid |
| Tavily 432/433 | search_quota_exceeded |
| 搜索地址被 egress 拒绝 | egress_denied |
| 搜索连接、响应或其他状态错误（含 Tavily 429/434） | search_unreachable |
| 网页私网、userinfo、协议或 URL 长度违规 | fetch_blocked_address |
| 网页端口不符 | fetch_blocked_port |
| 网页响应类型或字符集不支持 | fetch_unsupported_type |
| 第六次重定向 | fetch_too_many_redirects |
| 网页超时 | fetch_timeout |
| 网页最终响应 404、410 | fetch_not_found |
| 网页最终响应 401、403 | fetch_forbidden |
| 网页其他 HTTP/读取错误 | fetch_failed |
| 用户工具频率超限或 MusicBrainz 排队超限 | rate_limited |
| MusicBrainz/封面源 503、连接、解析或其他失败 | source_unavailable |
| 原生搜索 HTTP 200 错误块或 Responses 搜索 failed/incomplete | search.status=failed、search_failed；不改变本地写操作审批 |
| 原始块协议、端点或模型不匹配，或序列化后超过 256 KiB | 丢弃原始块，仅重放普通消息历史 |
| 工具 schema、音乐 entity/mbid/include 或查询输入错误 | validation_failed（沿用注册表） |

## 5. 正确、基准与错误案例

- 正确：成员保存公网 SearXNG 地址，web_search 出现在模型和 MCP 列表；调用得到不可信结果；来源改 none 后直接调用也被拒绝。
- 基准：未保存设置时搜索来源 none，GET 没有密钥；测试搜索返回 search_not_configured，不发外部请求。
- 错误：管理员要求 fetch_page 读取回环地址，仍被 egress 拒绝；公网跳转到内网也不能通过。
- 正确：网页超过读取或文本上限，结果标记截断；不能将超大结果报告为完整读取。

## 6. 必需测试与当前验证

测试代理应使用本地假服务和合成设置覆盖：

1. 0056 up/down 结构往返、默认值、密钥约束、旧探测补字段与回退；不修改旧迁移。
2. 搜索密钥省略/null/空串/重填、恢复后解密失败、用户与用途绑定、并发合并、响应及日志不含明文。
3. native 三态与 overrides 优先级；探测/模型变化后能力失效；搜索设置不清除模型设置。
4. 管理员/成员/放行名单的 SearXNG 保存与调用；403、Tavily 401/432/433/429/434、空 results、超大和非法 JSON。
5. Available 的聊天/MCP 列表与调用一致；设置变更后不能调用已失效工具；ctx 取消传递。
6. fetch_page 的私网和混合 DNS、DNS 重绑定、重定向到私网、userinfo、端口/URL 长度、五/六跳、20 秒、类型、gzip 解压上限、字符集、UTF-8 截断、HTML 排除块与空白。
7. MusicBrainz 全 Service 共用每秒许可、排队取消与十秒超限（注入时钟）、503 一次即失败；MBID/include 校验与字段裁剪。
8. Cover Art Archive 的 404 得空列表、清单 307 到官方 index.json、禁止跳图片/其他域名、不下载图片、四个字段的映射、清单请求经过限速器；User-Agent 与头部无用户/实例信息。
9. 外部内容的 Untrusted 与既有来源标记，日志不得包含查询词、查询串、正文、密钥；64 KiB 注册表截断。

本阶段按授权不新增或修改测试/testdata、不连接数据库或容器、不请求真实外部服务。
执行了指定 build、vet、steward 单元测试命令；因 x/net 源码与 go.sum 未在本机缓存且禁止联网下载，
build/vet 与 web/httpapi 测试加载受阻。egress、provider、secret、token、tool 的无数据库测试通过；mcp 现有测试均需数据库，本次均跳过。
runtime 的 `TestRunC5ModelToolsContainReadAndPersonalOnly` 失败：断言不允许 shared，而 C6 既有代码已把 shared 发给模型；未修改该测试。
数据库测试在未设置 ROOMUSIC_TEST_DATABASE_URL 时跳过，不视为验证通过。

后端乙测试代理另需覆盖：

1. Responses 文本、多个函数调用、arguments 分片与 output_item.done、函数结果的 call_id、强制 submit_review、用量未知/缓存/推理明细不重复计数。
2. Messages 搜索初始输入/分片输入、跨 pause_turn 关联查询、错误块、citations_delta、多轮加密内容及思考签名原样回传。
3. 原始块落库/读取/同协议同端点同模型重放没有重复文字与调用；协议、端点或模型改变不泄漏块；详情不泄漏加密字段。
4. search/citations 的顶层字段、step、空数组、HTTP(S) 过滤和去重；缺失 Responses 查询/标题、多 queries、open_page/find_in_page。
5. native 来源与 supported 的双重门禁；none/unknown/unsupported 不加工具；已存 native 修改其他字段；覆盖优先、模型和端点失效。
6. 预设失败后的候选顺序、Responses 工具探测通过选 responses、出错或没调用工具回退 openai、Responses 可用但搜索失败、Messages 能力缺失/真假；不因普通 400 推断 unsupported。
7. pause_turn 连续 24 步、token 限制、取消和 continue；无未确认写入；审查请求无搜索和主历史，意外 search/citations 事件拒绝。
8. 0056 up/down 往返及恢复旧协议 CHECK、清理 Responses 探测失败条目；日志与公开解析错误没有搜索或加密内容。
9. Responses 顶层 error/response.failed、流中断、未完成输出项、非法参数、max_output_tokens；现有三种协议回归。

后端乙本轮验证：指定 build/vet 已通过；provider 单元测试通过，runtime 仅既有
TestRunC5ModelToolsContainReadAndPersonalOnly 失败。未新增/修改测试或 testdata，未请求真实服务；
未设置 ROOMUSIC_TEST_DATABASE_URL，数据库测试跳过，迁移与持久化往返尚未验证。

## 7. 错误与正确实现

错误：把 search/citations 的展示字段拼成 user/system 指令，或同时回传原始文本块与同一份普通文字。
正确：原始块绑定协议与端点，匹配时优先完整回传；不匹配只用普通消息历史，写操作仍通过已有确认/审查。


错误：只在模型工具列表隐藏 web_search，MCP 或手工调用仍绕过设置；直接用默认 http.Client 抓网页。

正确：列表与 Invoke 都检查同一个 Available；fetch_page 从 egress.PublicOnly 构建客户端，保留拨号策略，
叠加逐跳 URL 限制，并在返回模型前标记 Untrusted。

## 文档核对（2026-11-16）

- [SearXNG API](https://docs.searxng.org/dev/search_api.html) 与
  [搜索设置](https://docs.searxng.org/admin/settings/settings_search.html)：确认 GET/search、q、format=json、未启用格式返回 403。
  另核对[主结果类型](https://docs.searxng.org/dev/result_types/main/mainresult.html)，确认 title/url/content 字段。
- [Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search)：确认 Bearer、POST、results 与 432/433；
  另外列出 434（Flex tier capacity exceeded），归为暂不可访问；本工具不启用 flex 请求参数。
  [额度文档](https://docs.tavily.com/documentation/api-credits)已读取，但未从页面正文确认“每月 1000”数字；实现不依赖免费额度。
- [MusicBrainz API](https://musicbrainz.org/doc/MusicBrainz_API) 与
  [限速](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting)：确认 JSON、inc、MBID、每秒请求规则、503 与有联系地址的 User-Agent。
- [Cover Art Archive API](https://musicbrainz.org/doc/Cover_Art_Archive/API)：清单本身也可 307 到 index.json，
  与调研只提图片跳转有差异；因此增加限定官方目标的清单跳转处理。

### 后端乙文档核对（2026-11-16）

- Anthropic [搜索指南](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool)在本环境返回 403，
  改核对[官方 SDK 类型目录](https://github.com/anthropics/anthropic-sdk-python/tree/main/src/anthropic/types)：
  web_search_tool_20250305_param.py、20260209/20260318 版本、server_tool_use_block.py、web_search_tool_result_block.py、
  web_search_result_block.py、web_search_tool_result_error.py、citations_delta.py、citations_web_search_result_location.py、
  message.py、usage.py、model_info.py、model_capabilities.py、server_tools_capability.py。
  三个版本字符串均存在；本实现选基础版 20250305。message.py 明确 pause_turn 应原样回传继续。
  能力字段仅表示至少支持一个工具版本，不能证明指定版本或组织权限已获准；这些仍需真实端点验收。
- OpenAI [Responses 创建接口](https://developers.openai.com/api/reference/resources/responses/methods/create)、
  [流式指南](https://developers.openai.com/api/docs/guides/streaming-responses)、
  [搜索指南](https://developers.openai.com/api/docs/guides/tools-web-search)、
  [函数调用指南](https://developers.openai.com/api/docs/guides/function-calling)可读取；并核对
  [官方 SDK Responses 类型](https://github.com/openai/openai-python/tree/main/src/openai/types/responses)：
  response_create_params、response_function_tool_call、response_function_web_search、response_output_item_done_event、
  response_output_text、response_usage、response_reasoning_item、tool_choice_function_param、response_input_item_param、response_error_event。
  确认具名 tool_choice 与 Chat Completions 的嵌套 function 形状不同；完成输出项携带最终加密内容。
- 对设计的细化：Responses 使用 store:false 与加密推理 include 实现本地历史重放；先测基础 Responses，再测搜索，
  区分“不支持接口”与“接口可用但搜索失败”；来源 title/query 缺失时保留线上字段但值为空。
  设计的 HTTP 200 搜索错误继续作为失败条目，线上 error_code 严格使用 wire-shapes 补充的 search_failed。
  与 wire-shapes.md 的字段、枚举和空数组约定无偏差。

### C8 审查修复验证（2026-11-16）

本轮修复 F1–F10，未改测试、迁移、前端或 wire-shapes.md，未启动容器。
在 backend/ 执行以下命令，均额外设置 GOPROXY=off、GOSUMDB=off 禁止依赖联网下载：

- `mise exec -- go build ./...`：通过。
- `mise exec -- go vet ./internal/steward/...`：通过。
- `env -u ROOMUSIC_TEST_DATABASE_URL mise exec -- go test ./internal/steward/provider/... ./internal/steward/web/... ./internal/steward/egress/... ./internal/steward/runtime/... ./internal/steward/httpapi/... -run 'C8|Egress|Public' -count=1`：失败。
  provider、web、egress 的筛选用例通过；runtime 只有 TestC8PauseTurnStepLimitAndReplay 失败：
  测试构造的回放归属没有 Model，却期待恢复已绑定模型的原始块，测试代理需补全同模型归属并覆盖模型变化。
  httpapi 命令成功退出，但数据库集成用例因未配置 ROOMUSIC_TEST_DATABASE_URL 跳过，不算通过。
  PublicOnly 收紧没有导致现有 web 用例失败，假服务仍通过替换 newClient 注入。

新增边界的专门测试由测试代理补充，本轮只执行授权的既有筛选测试。
