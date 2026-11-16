# 联网来源调研（2026-11-16，调研子代理）

以下是规划时查到的情况，实现前按地址重新核对。标“凭记忆”的没有在官方页面上核实。

## SearXNG

- `GET`/`POST /search`，参数 `q`、`format=json`、`pageno`、`categories`、`language`、`time_range`、`safesearch`。
  https://docs.searxng.org/dev/search_api.html
- `settings.yml` 的 `search.formats` 默认只有 `html`；没开 `json` 时返回 403。
  https://docs.searxng.org/admin/settings/settings_search.html
- 响应字段文档没有列全（凭记忆）：`results[]`（`title`、`url`、`content`、`engine`、`score`、`publishedDate`）、
  `answers`、`suggestions`、`infoboxes`、`unresponsive_engines`。只有 `pageno` 翻页。

## Tavily

- `POST https://api.tavily.com/search`，`Authorization: Bearer tvly-…`；`query`、`search_depth`、`max_results`(≤20)、域名过滤；
  响应 `results[]{title,url,content,score,raw_content}`。每月 1000 额度，不用绑卡；432、433 表示套餐限制。
  https://docs.tavily.com/documentation/api-reference/endpoint/search · https://docs.tavily.com/documentation/api-credits

## Messages 协议的服务端搜索

- 工具类型 `web_search_20250305`（基础）、`web_search_20260209`、`web_search_20260318`；`name` 为 `web_search`；
  参数 `max_uses`、`allowed_domains` 或 `blocked_domains`、`user_location`、`allowed_callers`（2026 版默认经代码执行，
  设 `["direct"]` 才是普通搜索块）。
- 流：`server_tool_use`（查询词以 `input_json_delta` 到达）→ `web_search_tool_result`（`web_search_result{url,title,page_age,encrypted_content}`）
  → 带 `citations[]` 的文本块（`web_search_result_location{url,title,cited_text,encrypted_index}`）。
- 回传历史时助手块必须原样带回（含 `encrypted_content`），否则 400。错误以 HTTP 200 的
  `web_search_tool_result_error` 返回。停止原因 `pause_turn`：原样回传助手消息后继续。
- 每 1000 次搜索 10 美元；`usage.server_tool_use.web_search_requests`；模型接口的
  `capabilities.server_tools.web_search.supported`。
- https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool

## OpenAI

- `web_search` 工具只在 Responses 接口；Chat Completions 只有固定会搜索的专用模型，引用在 `annotations[]`（`url_citation`）。
  https://developers.openai.com/api/docs/guides/tools-web-search
- 其他：OpenRouter 可经 Chat Completions（`:online` 或 `plugins`）；Perplexity、xAI 的 Chat Completions 搜索已停用或弃用；
  Gemini 的兼容端点不支持对话搜索。本任务都不做。

## MusicBrainz 与 Cover Art Archive

- 根地址 `https://musicbrainz.org/ws/2/`；查找 `/<entity>/<MBID>?inc=…&fmt=json`；搜索 `/<entity>?query=<Lucene>&limit=&offset=`；
  浏览 `/<entity>?<关联实体>=<MBID>`。https://musicbrainz.org/doc/MusicBrainz_API
- 限速：平均每秒 1 次，超过时全部请求返回 503；User-Agent 必须足以联系到应用维护者，格式
  `Application name/<version> ( contact-url )`。https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting
- 许可：核心数据 CC0，补充数据 CC BY-NC-SA 3.0。https://musicbrainz.org/doc/About/Data_License
- 封面：`https://coverartarchive.org/release/{mbid}/` 返回清单；`/front[-250|-500|-1200]` 以 307 转到 archive.org。
  https://musicbrainz.org/doc/Cover_Art_Archive/API

## 抓取

- `github.com/go-shiori/go-readability` 已归档；`codeberg.org/readeck/go-readability/v2` 依赖较多。
  规划选择 `golang.org/x/net/html` 自己提取。
- 仓库里出站客户端已有：解析后校验全部地址再连接、每跳重定向复核、禁用环境代理。缺：端口限制、userinfo、
  按解压后大小限制、内容类型检查。
