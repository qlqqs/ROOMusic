# 管家 C10 设计

## 记忆

- 迁移 0057：`steward_memories`（`id`、`user_id`、`content`、`created_at`、`updated_at`），按用户级联删除。
- 工具放在 `steward/runtime` 旁的 `steward/memory` 包（管家自己的数据）：`remember {"content","id"?}`、
  `forget {"id"}`、`list_memories`；personal，成员；超过条数或长度返回明确的错误码。
- 接口：`GET/POST /api/v1/me/steward/memories`、`PATCH/DELETE …/{id}`、`DELETE …`（全部清除）。
- 提示词：系统提示词末尾加一个带来源标记的“用户让你记住的内容”块，按更新时间倒序，总长 ≤ 8000 字符；
  内容里的标记字符按现有规则转义。每次运行开始时读取一次。
- 记忆不进审查调用（C7）的输入。

## 历史摘要

- 会话加 `summary text`、`summary_through_message_id`、`summary_updated_at`；消息表记录每步的输入用量（已有 `usage` 则沿用）。
- `prepare` 时：若上一步模型请求的输入 token 超过 12 万，且待摘要范围（上次摘要之后、最近 6 个回合之前）不为空，
  先发一次摘要调用：系统提示词要求保留用户目标、已确认的事实、查到的对象 ID、未完成的事项与用户的取舍；
  输入是旧摘要加待摘要的回合（工具结果截短）。不带工具，非流式展示，输出上限 2000 token，超时 60 秒。
  成功则更新会话的摘要与分界；失败记一条日志事件，继续用完整历史。
- 重放：有摘要时，系统提示词之后先放一条带来源标记的“之前对话的摘要”，再放分界之后的消息。
  分界总落在回合边界上，不会拆开工具调用与结果。
- 模型自带搜索的不透明块（C8）在被摘要的范围内直接丢弃。
- SSE 事件 `history_compacted`（分界消息 ID），前端在该位置显示一行标记。

## 运行与请求解耦

- `steward/runtime` 新增 `Supervisor`（应用级，随生命周期 context）：
  - `Start(params) (*Run, error)`：检查并发上限，占用会话，在自己的 goroutine 里跑循环；context 来自 Supervisor，不来自请求。
  - 每个运行有一个事件日志（内存，按序号追加，运行结束后保留 60 秒再释放）和订阅者集合。
  - `Subscribe(conversationID, afterSeq)`：先回放 `afterSeq` 之后的事件，再实时推送；慢订阅者被断开（它可以带着序号重连），不阻塞运行。
  - `Cancel(conversationID)`、`CancelUser(userID)`；`Shutdown(ctx)` 取消全部并等待收尾（上限 10 秒）。
- 事件带序号：SSE 的 `id:` 字段。发消息、继续、确认方案的处理函数改为 `Start` 后 `Subscribe(…, 0)`；
  请求断开只是退订。
- 接回：`GET /api/v1/me/steward/conversations/{id}/events`，支持 `Last-Event-ID`；没有进行中的运行且事件日志已释放时返回
  `204`，前端改读会话详情。
- 现有的凭据复查（成员开关、密钥变化）改为调用 `CancelUser` 或 `Cancel`；登出与成员被停用时由 `identity` 的现有钩子
  （或 `app` 装配的回调）调用 `CancelUser`。
- 启动时的复位逻辑保留（D4）。
- 文本增量事件在事件日志里按步合并存储（同一步的连续 `text` 合成一条），控制内存；单个运行的事件日志上限 4 MiB，
  超过后最早的文本事件被折叠成“已省略”，接回的客户端改读会话详情。

## 未读标记

会话加 `last_read_at`；运行结束时间晚于它即为未读。打开会话时 `POST …/read`。列表接口返回 `unread`，设置接口返回未读会话数供入口显示提示点。

## 前端

- `run-store`：发起运行后用事件流读取；断线（非用户停止）时带 `Last-Event-ID` 自动接回，退避重试；
  打开会话时若 `status` 为运行中则直接接回。去掉“正在别处回答”的轮询分支。
- 归并函数按序号去重。
- 会话列表的三种标记；入口的提示点；`history_compacted` 的分隔行。
- 设置页“记忆”一组。

## 测试

- 后端：Supervisor 的并发上限、订阅回放与实时衔接、慢订阅者、取消、关闭收尾、事件日志上限（单元测试，不需要数据库）；
  请求断开后运行继续并落库；接回接口；登出与开关关闭时取消；记忆的隔离、上限、提示词注入；
  摘要的触发、分界、失败回退、重放内容（假模型端点）。
- 前端：接回与去重、断线重连、两个 store 实例模拟两个标签页、未读标记、记忆设置。
