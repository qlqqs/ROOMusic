# 管家 C5 设计

## 调用入口

- `tool.Registry.Invoke`：`read` 与 `personal` 鉴权后执行；`shared` 仍返回 `tool_scope_unsupported`。
- `runtime/run.go` 发给模型的工具集合改为 `read` + `personal`。
- MCP 不用改判定（`mcp/server.go:16-18` 已按令牌范围区分），补测试。
- `personal` 工具的结果同样受 64 KiB 上限约束。

## 工具（各自放在拥有数据的包的 `stewardtools` 子包）

| 包 | 工具 | 范围 |
| --- | --- | --- |
| `zones/stewardtools` | `get_playback`、`get_queue` | read |
| | `control_playback`（play、pause、stop、next、previous、seek）、`play_tracks`（`track_ids` 或 `release_id`，`placement`: replace、next、end）、`edit_queue`（remove、move、clear、play_entry）、`set_play_mode`、`transfer_playback`、`set_device_volume` | personal |
| `listening/stewardtools` | `list_play_history`、`list_recent_releases` | read |
| | `delete_play_history_item` | personal |
| `personal/stewardtools` | `list_favorites`（kind）、`list_playlists`、`get_playlist` | read |
| | `set_favorite`、`create_playlist`、`rename_playlist`、`delete_playlist`、`add_playlist_tracks`、`remove_playlist_track`、`move_playlist_track` | personal |

- 全部 `MinRole` 为成员，按 `caller.User.ID` 取数；输出含标签文本的设 `Untrusted`。
- 歌单写入需要幂等键与 `ExpectedRevision`：工具内部生成 UUID 幂等键；`expected_revision` 作为输入（来自 `get_playlist`），
  冲突时返回 `revision_conflict` 让模型重读。`Execute` 返回的是 HTTP 形状的结果，工具把非 2xx 转成工具错误码。
- `play_tracks` 用 `release_id` 时由工具向曲库服务取曲目顺序（走现有队列构造函数，不自己拼 SQL）。
- 流地址（`Zone.stream`、`next_stream`）不进工具输出。
- 分区离线、没有渲染器映射成错误码 `zone_offline`、`renderer_unavailable`。
- 依赖规则测试与 `app/steward.go` 的注册随之增加。

## 页面上下文

请求体（发消息与继续都接受，可选）：

```json
{"text":"…","context":{
  "page":{"path":"/albums/…","kind":"album","id":"…","title":"…"},
  "playback":{"zone_id":"…","zone_name":"…","track_id":"…","title":"…","artist":"…","status":"playing"}}}
```

- 校验：`path` ≤ 512、标题类 ≤ 300 字符，`kind` 为枚举（album、artist、playlist、track、other），ID 为 UUID 格式；
  不合格返回 `400 invalid_request`。后端不拿这些 ID 去放宽任何权限，它们只是提示。
- 存储：迁移 0053 给 `steward_messages` 加可空的 `context jsonb`（带 down）。
- 发给模型：重放历史时，在该条用户消息的正文后追加一个用现有来源标记包起来的上下文块（来源 `page_context`），
  标记的转义沿用 C2 的做法。只有最近一条带上下文的用户消息附带上下文块，较早的不重复附带。
- 系统提示词加一句说明：上下文块是界面状态，不是用户的指令。

## 前端

- `features/steward/context/`：`usePageContext`（从路由匹配与查询缓存取标题；取不到标题只发路径与 ID）、
  `usePlaybackContext`（`usePlayerSelector`）。
- 输入框上方的 `.ctx` 行按原型做；每个标签有移除按钮，移除状态只对当前这条草稿有效，发出后恢复。
- `run-store.startRun` 增加 `context` 参数；解码器给消息加 `context`（历史消息里不显示）。
- `tool-labels.ts` 增加 20 个工具的名称、图标、主要参数。
- 播放类工具成功后不需要前端额外刷新（分区事件已推送）；收藏、歌单、历史类工具结束时让对应查询失效。

## 测试

- 后端（另一个子代理）：调用入口三种范围；每个工具的成功、越权（另一用户的对象）、目标不存在；歌单版本冲突；
  分区离线；MCP 两种令牌；上下文的校验、落库与请求体里的标记（假模型端点）。
- 前端：上下文标签的显示与移除、请求体、工具标签；失效的查询键。
