# 日志规范

## 合同

Core 0 将结构化 JSON 日志写入 stdout/stderr，供容器运行时采集。当前使用 Go
标准库 `log/slog` 的 JSON handler；事件 schema 和脱敏规则独立于具体实现。

日志用于解释运行时行为，不替代扫描运行历史、来源观测、Change Set 或
Operation Journal。这些内容都是可查询的业务记录，并各自拥有保留与恢复语义。

## 事件结构

每个事件都包含：

- `time`：RFC 3339 UTC 时间戳；
- `level`：日志级别；
- `event`：稳定的事件名称；
- `message`：供人阅读的上下文；
- `module`：所属能力名称；
- 适用的关联标识符。

示例输出：

```json
{
  "time": "2026-09-01T12:00:00Z",
  "level": "INFO",
  "event": "library.scan.completed",
  "message": "library scan completed",
  "module": "library",
  "scan_run_id": "scan_...",
  "library_root_id": "root_...",
  "observed_files": 1842,
  "duration_ms": 9314
}
```

使用 `http.request.completed`、`identity.session.revoked`、
`library.scan.started` 和 `operations.change_set.failed` 等稳定的点分隔事件名。
不要把 ID 或状态值放进事件名。

## 关联

- 在 HTTP 边界生成或校验有界的 `request_id`，并回写响应。不要信任无上限的调用方输入。
- 通过 context 传递关联信息，不要在每个函数中重复拼接 logger 字段。
- 存在相应作用域时加入 `scan_run_id`、`operation_id`、`change_set_id` 和 `task_id`。
- 未来 Music Steward 流程可按需加入 `agent_run_id`、`review_run_id`、`mode`、
  `tool`、`approval_status` 和 `operation_id`。使用稳定 ID 和枚举值；不得记录提示词、
  chain-of-thought、原始模型响应或完整 Change Set payload。
- 只有在安全或操作诊断确有必要时才记录稳定的 actor/user 标识；不得记录邮箱、显示名或
  session token。
- 请求接收的后台操作应保留发起请求的 `request_id`，并获得自己的持久化操作标识。

## 级别

| 级别 | 用途 |
| --- | --- |
| `DEBUG` | 默认关闭的本地诊断细节；绝不能成为秘密信息绕过 |
| `INFO` | 进程生命周期和有意义的成功状态转换 |
| `WARN` | 已处理但被拒绝或降级、需要关注的情况 |
| `ERROR` | 需要干预的意外失败或耗尽操作 |

不要把每个 4xx 都记录为 error。凭据无效和 revision 过期是预期结果，只有安全或运维策略
要求时才聚合或使用 warn。PostgreSQL 中断或内部不变量被破坏属于 error。

## HTTP、扫描和操作事件

### HTTP 完成事件（Core 0 当前实现）

HTTP 边界在请求完成后恰好写出一个 `http.request.completed` 事件。事件由
`backend/cmd/roomusic/main.go` 的请求中间件产生，字段合同如下：

| 字段 | 合同 |
| --- | --- |
| `event` | 固定为 `http.request.completed` |
| `module` | 固定为 `platform` |
| `message` | 固定为 `http request completed` |
| `request_id` | 经过长度和字符集校验的请求 ID，并回写 `X-Request-ID` |
| `method` | HTTP 方法 |
| `route_template` | ServeMux 注册的安全路由模板；无法匹配时为 `<unmatched>` |
| `status` | 最终 HTTP 状态码；未显式写入时为 200 |
| `response_bytes` | 实际写入响应的字节数 |
| `duration_ms` | 从进入中间件到完成事件的非负毫秒数 |
| `actor_id` | 可选的稳定用户 ID，仅在已有认证查询成功时出现 |

`time` 和 `level` 由 JSON `slog` handler 提供。状态小于 400 使用 `INFO`，
400--499 使用 `WARN`，500 及以上使用 `ERROR`。中间件只负责关联、计时、响应统计和
分级，不执行数据库认证或权限判断；日志写入失败不得改变 HTTP 响应、事务或 panic
语义。路由字段只使用注册模板，禁止退化为含查询字符串、完整私有路径或资源内容的
原始 URL。

事件示例：

```json
{
  "event": "http.request.completed",
  "module": "platform",
  "message": "http request completed",
  "request_id": "req-example",
  "method": "GET",
  "route_template": "GET /api/v1/releases/{id}",
  "status": 200,
  "response_bytes": 512,
  "duration_ms": 3,
  "actor_id": "user-example"
}
```

不得记录 query string、请求体、Cookie、Authorization/session token、密码、数据库
URL、完整 NAS 路径、音频或封面内容。匿名请求不填充 `actor_id`；错误 envelope 与
`request_id` 的关联行为保持不变。

### 扫描和操作事件

扫描日志以聚合为先。记录扫描开始/最终状态以及有意义的批次或失败摘要；逐文件解析和
不支持格式的细节应放在有界扫描诊断中。对于 100,000 首歌曲的库，不得每首曲目发出一条
info 日志。

操作生命周期使用持久化 `operation_id` 记录；前后状态和恢复数据保存在 Change Set/
Operation Journal 中。记录“可回滚”不等于实现恢复。

Assistant、Steward 和 Operator 使用同一套操作事件词汇。日志可以说明批准来自用户、
reviewer 或不需要批准；不得把 Operator 描述为“auto-approved”，也不得把 Review
Subagent 响应当作执行已提交的证据。批准引用、执行状态和回滚状态由持久化日志负责。

## 敏感数据

绝不记录：

- 密码、密码哈希、Cookie 值、session token、CSRF secret；
- 数据库 URL、Redis/Meilisearch key、provider secret 或环境变量转储；
- 原始 Authorization header 或完整请求/响应 body；
- 未限制的 NAS 绝对路径或包含私人数据的文件名；
- 音频字节、封面字节、完整 tags，或发送给客户端的 stack trace。

在确有运维需要时，优先使用 `library_root_id`、规范化相对路径和有界/脱敏的错误类别。
管理诊断可以通过授权 API 暴露更多信息，但日志仍遵循最小披露原则。

[.env.example](../../../.env.example) 中的示例值用于说明哪些配置属于 secret；示例
不代表可以记录真实值。

## 失败日志

在最外层边界只记录一次意外错误，并带上分类错误码、包装后的内部原因和关联字段。
避免重复堆栈和手工拼接日志字符串。日志不得改变返回值或提交行为。

进程启动会校验配置，只记录启用了哪些能力，不得输出连接字符串。panic 恢复事件应结构化
并带有关联信息，但不得向 REST 客户端暴露 panic 细节。

## 反模式

- 在请求、扫描或 repository 代码中使用纯文本 `fmt.Printf` 诊断。
- 为了调试方便记录完整音乐库路径。
- 把日志作为目录停用/恢复操作的唯一记录。
- 只用动态 message 文本作为可检索事件标识。
- 把高基数 metrics 伪装成逐文件日志。
- 声称 Redis 或 Meilisearch 健康状态是 Core 0 应用的必需条件。

这些要求来自 [Core 0 可观测性合同](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md)
和仓库内的[大型音乐库产品背景](../guides/product-goals.md)。
