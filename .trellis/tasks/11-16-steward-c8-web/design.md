# 管家 C8 设计

实现前，子代理必须按 `research/web-sources.md` 里的地址核对当前文档（Messages 搜索工具的版本字符串与流事件、
Responses 的请求与流事件、Tavily、SearXNG、MusicBrainz），以文档为准，出入记进执行记录。

## 包

```text
backend/internal/steward/
├── web/           search（SearXNG、Tavily 两个实现）、fetch（抓取与正文提取）、musicbrainz（含限速）
│   └── stewardtools 的角色由本包直接承担：web 是管家自己的能力，没有别的业务包拥有它
└── provider/      responses.go（第三个适配器）；messages.go 增加服务端工具块
```

## 搜索

- 设置表加 `search_source`（`none`、`searxng`、`tavily`、`native`）、`searxng_url`、`search_key_ciphertext` 与末四位
  （迁移 0056）。`PUT` 的合并与密钥处理沿用模型密钥的规则；SearXNG 地址按 ADR D1 走与模型端点相同的出站策略
  （管理员不限，成员公网或放行名单）。
- `web_search` 输入 `{"q","max_results"(≤10)}`，输出 `{"results":[{"title","url","snippet"}]}`，`Untrusted`。
  是否登记给模型按调用者的设置在每次运行时决定（工具定义增加可选的 `Available func(ctx, caller) bool`）；MCP 列表同样过滤。
- 设置页“测试搜索”按钮：用固定的查询词请求一次，显示成功或失败原因。

## 模型自带搜索

- 运行时内部消息表示增加“服务端工具块”：适配器原样保存与回传的不透明内容（Messages 的 `server_tool_use`、
  `web_search_tool_result`、带 `citations` 的文本块；Responses 的对应输出项）。落库在助手消息的新列 `provider_blocks jsonb`，
  重放时只回给产生它的同一种协议；会话中途换了协议或模型端点时丢弃这些块，只保留文字。
- Messages：请求的 `tools` 里加搜索工具，`max_uses` 固定为 5；处理 `pause_turn`（原样回传后继续，计入步数）；
  错误块（HTTP 200 里的 `web_search_tool_result_error`）显示为条目失败。
- 新 SSE 事件 `search`（查询词、结果的标题与地址）与回答的 `citations`（地址、标题）；前端显示为过程条目和回答下的来源列表。
- 探测：Messages 读模型能力字段；Responses 用一次带搜索工具的最小请求。结果存 `probe_result.native_search`，可手动改。

## Responses 协议

`provider/responses.go`：`POST {base}/responses`，流式；消息、函数工具调用、工具结果、用量与现有内部表示互转；
错误分类沿用现有的。探测顺序：预设匹配 → OpenAI 兼容 → Messages；在 OpenAI 兼容成立的端点上再试 Responses，
成立则记为可选协议，默认仍是 Chat Completions，用户选了“模型自带搜索”或手动切换时用 Responses。

## 抓取

- 用 `egress` 的 `PublicOnly` 客户端；另加：只允许 80、443，拒绝带 userinfo 的地址，地址长度 ≤ 2048；
  `Accept` 限定文本类型，响应类型不在允许范围时拒绝；`io.LimitReader` 2 MiB（按解压后计）；总时长 20 秒；
  重定向 ≤ 5 且每跳复核。
- 提取：`golang.org/x/net/html` 分词，去掉 `script`、`style`、`nav`、`header`、`footer`、`aside`、`form`、`noscript`，
  保留标题、段落、列表、表格文字与链接文字，压缩空白；输出 `{"url","final_url","title","text","truncated"}`，文字 ≤ 100 KiB。
  JSON、XML、纯文本原样截断。按 `charset` 转码只支持 UTF-8 与响应头声明的常见编码（`golang.org/x/text`，已在依赖范围内则用，
  否则只收 UTF-8 并说明）。

## MusicBrainz

- 固定主机 `musicbrainz.org`、`coverartarchive.org`；User-Agent `ROOMusic/<版本> ( <项目地址> )`，取自 `buildinfo`。
- 进程内全局限速：每秒 1 次，排队等待，等待超过 10 秒返回 `rate_limited`。503 时不重试，返回说明。
- `search_music_metadata`：`{"entity":"artist"|"release"|"release-group"|"recording","query","limit"(≤10)}`；
  `get_music_metadata`：`{"entity","mbid","include":[…]}`（允许的 `inc` 值是白名单）；release 可带封面清单（地址列表，不下载）。
- 输出裁剪成整理专辑需要的字段（标题、艺人、日期、国家、厂牌、编号、条码、曲目表、时长、MBID）。

## 前端

设置页 Agent 栏目加“联网搜索”一组（来源、地址或密钥、测试、检测到的自带搜索）；模型一组的接口类型多一个 Responses；
工具标签与搜索条目、回答下的来源列表。

## 测试

全部用本地假服务：SearXNG（含 403）、Tavily（含 401、432）、MusicBrainz（含 503、限速计时）、网页
（重定向到内网、超大、错误类型、各种 HTML）、Messages 搜索流（含 `pause_turn`、错误块、多轮回传）、Responses 流。
`egress` 的假服务在回环地址上，沿用 C2 测试里替换拨号校验的做法。不请求任何真实外部服务。
