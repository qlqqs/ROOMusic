# C8 线上形状

前后端共同遵守；确有必要偏离时在汇报里逐条写明。字段命名与现有设置接口保持同一风格，已有字段不改。

## 设置 `GET/PUT /api/v1/me/steward/settings`

- 响应增加：`search_source`（`none`｜`searxng`｜`tavily`｜`native`，默认 `none`）、`searxng_url`（字符串，未填为空串）、
  `search_key_saved`（布尔）、`search_key_last_four`（字符串）、`search_key_needs_reentry`（布尔）。
- 请求增加：`search_source`、`searxng_url`、`search_key`。`search_key` 的省略、`null`、空串、非空的含义与模型密钥 `key` 相同。
- `protocol` 在现有两个取值之外增加 `responses`。
- `probe_result` 增加：`native_search`（`supported`｜`unsupported`｜`unknown`）、`responses_available`（布尔）。
  `overrides` 增加可选的 `native_search`（取值同上，沿用现有覆盖项的写法）。
- `search_source=native` 而生效的 `native_search` 不是 `supported` 时，PUT 返回 400 `invalid_input`。
- 成员的 `searxng_url` 不是公网地址且不在放行名单里时，返回与模型端点相同的 `egress_denied` 错误。

## 测试搜索 `POST /api/v1/me/steward/settings/search-test`

无请求体，按已保存的设置请求一次。响应 200：`{"ok":true,"result_count":3}` 或 `{"ok":false,"code":"…"}`，
`code` 取 `search_not_configured`、`searxng_json_disabled`、`search_key_invalid`、`search_quota_exceeded`、
`search_unreachable`、`egress_denied`。来源是 `native` 或 `none` 时返回 `search_not_configured`。

## 工具

`web_search`、`fetch_page`、`search_music_metadata`、`get_music_metadata`，输入输出见 design.md。
工具失败时结果里的错误码：上面测试搜索的各个 `code`，以及 `fetch_blocked_address`、`fetch_blocked_port`、
`fetch_unsupported_type`、`fetch_too_many_redirects`、`fetch_timeout`、`fetch_failed`、`rate_limited`、`source_unavailable`。

## 模型自带搜索（后端乙）

- SSE `search`：`{"type":"search","step":n,"search_id":"…","query":"…","status":"completed"|"failed","results":[{"title","url"}],"error_code":"…"?}`。
- SSE `citations`：`{"type":"citations","step":n,"citations":[{"url","title"}]}`，在该步文字结束后发一次，去重。
- 会话详情里的助手消息增加 `searches`（同 `search` 事件去掉 `type`、`step` 的形状，数组）与 `citations`（数组）；没有时为空数组。

## 补充（前端实现后确定）

- `search_source=native` 的 400 校验只在请求里提交了 `search_source=native` 时做；已保存为 `native` 的用户改其他字段不被拒绝。
  运行时生效的 `native_search` 不是 `supported` 时，不向模型请求里加搜索工具。
- `search_music_metadata` 的输出是 `{"results":[…]}`，每条用 `title`（艺人用名字填 `title`）。
- 模型自带搜索失败时 `error_code` 固定为 `search_failed`。
- `citations` 事件带 `step`。
- `probe_result.failures[].step` 增加 `responses`、`native_search` 两个取值（Responses 可用性与自带搜索的探测失败）。
