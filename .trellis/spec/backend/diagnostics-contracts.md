# 问题反馈诊断合同

## 管理员导出诊断信息

`GET /api/v1/admin/diagnostics` 通过 `RequireAdmin` 校验会话和角色。未登录返回 `401 unauthorized`，普通成员返回 `403 forbidden`。响应头含 `Cache-Control: no-store`。

成功返回 200，JSON 字段如下。HTTP 接口与管家工具共用一个 `diagnostics.Service` 和日志环。

| 字段 | 内容 |
| --- | --- |
| `generated_at` | 生成报告的时间，RFC 3339 UTC |
| `status` | 管家 `get_system_status` 使用的 `SystemStatus`。含构建信息、启动时刻、运行秒数、数据库连通性、连接池、迁移版本、曲库统计和分类错误列表 |
| `disks` | 管家 `get_disk_usage` 的 `items` 数组。每项含用途、目录末级名称、总字节数、可用字节数，末级名称命中凭据规则时替换成 `[redacted]`，音乐根另有 `root_id`。单项探测失败时保留该项，两项容量为 null，`error_code` 为 `disk_unavailable` |
| `logs` | 日志环的 `Recent` 快照，与管家 `list_recent_logs` 的 `items` 相同。最新在前，最多 200 条，每条含 `time`、`level`、`fields`。空环返回 `[]` |

磁盘列表整体读取失败时仍返回 200，`disks` 为 `[]`，并在 `status.errors` 追加 `disk_unavailable`。
若 `SystemStatus` 返回请求上下文错误，返回 `503 diagnostics_unavailable`，消息为“暂时无法读取诊断信息”。
导出复用管家的脱敏结果，仅含诊断字段、固定消息和分类错误。字段白名单及凭据、路径、自由日志正文的处理见[日志规范](./logging-guidelines.md)。

## 已登录用户上报页面报错

`POST /api/v1/diagnostics/client-errors` 先通过 `RequireSameOrigin` 校验来源，再验证已登录用户。
缺少 Origin 返回 `403 origin_required`，来源不受信任返回 `403 origin_forbidden`，会话无效返回 `401 unauthorized`。

请求为单个 UTF-8 JSON 对象，整个请求体上限 4096 字节。以下四项均为必填字符串。

| 字段 | 约束 |
| --- | --- |
| `kind` | `error`、`unhandledrejection`、`render` 之一 |
| `name` | 错误类名，匹配 `^[A-Za-z][A-Za-z0-9_$]{0,63}$` |
| `message` | 最多 500 个 Unicode 码点，允许空串 |
| `route` | 前端路由模板，匹配 `^/[A-Za-z0-9_$./-]{0,199}$`，例如 `/albums/$groupId` |

成功返回 204，响应体为空。请求超限、编码或 JSON 无效、未知键、重复键及尾随 JSON 返回 `400 invalid_json`；缺少字段或字段值不符合约束返回 `400 invalid_client_error`。
每个用户在滚动的 10 分钟内最多成功上报 20 条。超额返回 `429 client_errors_throttled`。额度按用户 ID 在单个应用进程内计数，重启后清空。
所有错误采用统一信封 `{error: {code, message}, request_id}`。

每次成功上报产生一条 `client.error.reported` WARN，记录固定消息、用户和请求 ID、错误类别、类名、模板和正文。
只有 stdout 的 JSON 日志包含用户提供的 `message`。日志环和导出仅保留白名单字段。`client_route` 经通用凭据脱敏后，整体匹配 `^/$|^(/(\$[A-Za-z][A-Za-z0-9]*|\$|[a-z]+(-[a-z]+)*))+/?$` 时原样保留，否则记为 `[redacted]`。
例如 `/albums/$groupId` 原样保留，含数字资源编号的 `/albums/123` 会脱敏。限流拒绝不产生这条 WARN。

接口样本位于 `backend/internal/app/testdata/api-samples/diagnostics/`，由现有 API 样本录制器比对真实 handler 响应。
