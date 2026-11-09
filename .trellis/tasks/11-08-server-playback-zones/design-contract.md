# 接口合同：服务端播放区（协议 v1）

[design.md](./design.md) 与 [design-protocol.md](./design-protocol.md) 的接口部分。前后端据本文并行实现；改动本文要先经主会话裁决。
`?` 表示可为 null，所有字段都必须出现（与 `HistoryItem` 的写法一致）。§1–§7 是**协议**，任何客户端（浏览器、以后的原生客户端）都按它实现；
§8 是浏览器客户端自己的实现约定，不属于协议。

## 1. 通则

- 路径前缀 `/api/v1/me/playback`。路径与请求体里没有用户 ID，一律作用于已鉴权的用户，管理员也一样。
- **协议版本**：本文是 v1。输出端认领时声明 `protocol: 1`，服务端在快照与认领响应里给出 `protocol: 1`。v1 内只做**增加**：新字段、新事件类型、新命令、新枚举值。
  客户端必须忽略不认识的字段与事件类型；服务端只按输出端声明的能力给流格式。不兼容的改动用新的路径前缀（`/api/v2/…`）。
- **鉴权与客户端无关**：每个请求都经 `Guard.Authenticate` 得到用户，协议本身不规定凭据形式。现在只有浏览器的同源 HttpOnly Cookie；以后原生客户端的鉴权（例如 `Authorization` 头）加在 Guard 里，协议不变。
  写接口的来源校验 `RequireSameOrigin` 是 Cookie 会话的跨站防护，只对 Cookie 会话生效（现在全部请求都是）。协议里不出现 Cookie、`keepalive`、Web Locks 等浏览器特有的东西；
  流地址由服务端给出，客户端用自己的凭据去取，地址里不放凭据。
- 读接口只鉴权（401）；写接口（POST/PUT/PATCH/DELETE）先来源校验（403）再鉴权（401）。响应经 `httpserver.WriteJSON`（`no-store`）与统一错误信封；
  请求体经 `httpserver.DecodeJSON`（1 MiB 上限，解码失败 `400 invalid_json`）。
- UUID 输出为小写；时间为 UTC RFC 3339，**带毫秒**（`2026-11-08T12:00:00.123Z`）。
- 路径里的设备、播放区、输出会话、流不属于该用户时与不存在相同：`404 not_found`。
- 每个会改变状态的响应都带 `server_time`；客户端用它更新时钟偏移（偏移 = server_time − 收到响应时的本地时间，取最近一次）。
- **消息格式的文档化**：实现时整理进 `.trellis/spec/backend/playback-zone-contracts.md`；每种请求、响应与事件各有一份由真实 handler 生成的样本
  （`backend/internal/app/testdata/api-samples/playback-zones/`），原生客户端据此实现与核对。

## 2. DTO

### 2.1 队列条目

```text
QueueTrack = catalog.TrackSummary 的 JSON（与 POST /tracks/summaries、HistoryItem 同一投影）：
  {track_id, title, artist, artist_refs, group_id, present, duration_seconds?, release: ReleaseSummary,
   source: {source_kind, codec?, bit_depth?, sample_rate?, channels?, bitrate?}}
QueueEntry = {entry_id: UUID, track: QueueTrack}
```

服务端每次读取都按曲目当前的发行与组给出，不存快照。浏览器用现有的 `playerTrackOfSummary` 映射成 `PlayerTrack`。

### 2.2 播放区

```text
Zone = {
  zone_id: UUID, device_id: UUID,
  revision: integer,            // 每次状态变化加一；客户端按它丢弃旧状态
  queue_revision: integer,      // 条目的增删移动加一；当前条目、模式、历史、随机袋的变化不算
  queue_length: integer,
  mode: "sequential" | "repeat_all" | "repeat_one" | "shuffle",
  settings: {leveling: "off"|"auto"|"track"|"album", gapless: boolean, crossfade: boolean, crossfade_seconds: 3|5|8|12,
             quality: "lossless" | "lossy",
             headroom: {enabled: boolean, db: number, clip_indicator: boolean}},   // headroom 的取值范围与列归 dsp-pipeline（P7）；
                                                                                  // 算法线第二批在此增加键（PEQ、卷积、降混）
  current: QueueEntry?,
  has_next: boolean,            // “下一首”按钮是否可用（同现有 hasNext）
  intent: "play" | "pause",
  status: "idle" | "loading" | "playing" | "paused" | "buffering",
  fault: {kind: "transient" | "autoplay_blocked" | "no_stream_format"}?,
  failures: [{entry_id: UUID, reason: PlaybackFailureReason}],
  position: {seconds: number, at: time, duration_seconds: number?, seekable: boolean},   // 曲目内位置，由服务端按会话事件换算
  stream: Stream?,              // 当前流；没有活动的渲染会话时为 null
  next_stream: Stream?,         // 接续流：下一首格式不同时提前开好，当前流放完时换上
  paused_at: time?,             // 意图变为暂停（或变为空闲）的时刻；播放中为 null
  signal: object?,              // 信号路径，结构归算法线 dsp-pipeline（P11），客户端按其文档解析
  event: ZoneEvent?
}
Stream = {
  seq: integer,                 // 播放区的流序号，单调递增（跨重启也是）
  url: string,                  // 服务端给出，客户端不拼接
  mime_type: string, quality: "lossless" | "lossy",
  format: {codec: string, sample_rate: integer, bit_depth: integer?, channels: integer},   // 渲染会话确定首曲后给出；有损档 bit_depth 为 null
  segments: [{entry_id: UUID, track: QueueTrack, stream_start_seconds: number, track_start_seconds: number}]
                                // 已渲染、尚未播完的曲目分段：从流内 stream_start_seconds 起是这首的 track_start_seconds 秒
}
PlaybackFailureReason = "not_found" | "root_disabled" | "unsupported_format" | "source_unavailable" | "source_changed" | "decode_failed"
ZoneEvent = {seq: integer} & (            // seq 取产生事件时的 revision；客户端每个 seq 只公告一次
  {kind: "track_started", entry_id} | {kind: "track_failed", entry_id, reason} | {kind: "all_failed"} | {kind: "failures_stopped"} |
  {kind: "transient_failure"} | {kind: "autoplay_blocked"} | {kind: "no_stream_format"} |
  {kind: "queue_restored", restored: integer, dropped: integer} | {kind: "play_recorded", track_id: UUID})
```

- `status` 是输出端回报的状态；服务端开新流时先置为 `loading`，离线时为 `paused`（有当前项）或 `idle`。
- `position.seconds` 在 `status == "playing"` 时按 `seconds + (服务端现在 − at)` 外推，不超过 `duration_seconds`。输出端用 `segments` 与本地流内位置自己算。
- `failures` 只在内存里，替换队列、清空、重启时清除。`play_recorded`：服务端记下一次播放，浏览器据此让本人的播放历史查询失效。
- 失败原因去掉了直出时代的 `not_directly_playable`、`derivation_failed`；`unsupported_format` 是服务端也解不了的格式（实际取值以 dsp-pipeline 的打开逻辑为准，只增不减）。

### 2.3 设备与快照

```text
Device = {device_id: UUID, zone_id: UUID, name: string, kind: "browser" | "native", online: boolean, last_seen_at: time,
          volume: number, muted: boolean, capabilities: Capabilities, output_session_id: UUID?}
Snapshot = {server_time: time, protocol: 1, devices: Device[], zones: Zone[]}   // 按设备创建时间升序，zones 与 devices 一一对应
```

`output_session_id` 让输出端发现自己被接管（与自己的会话不同即停声）。`capabilities` 是最近一次认领时声明的；从没认领过为 §2.6 的默认值。

### 2.4 队列变化

```text
QueueChange = {queue_revision: integer} & (
  {kind: "replace", entries: QueueEntry[]} |      // 条目多于 500 条时改发 {kind: "reload"}
  {kind: "reload"} |                              // 客户端重新 GET 队列
  {kind: "insert", index: integer, entries: QueueEntry[]} |   // 插在第 index 位（0 起）之前
  {kind: "remove", entry_id: UUID} |
  {kind: "move", entry_id: UUID, delta: -1 | 1} |
  {kind: "clear"})
```

客户端手里的 `queue_revision` 等于 `change.queue_revision − 1` 才应用，否则重新 GET。只有显示该播放区队列的客户端需要保存队列。

### 2.5 播放头

```text
PlayheadSample = {stream_seq: integer, stream_seconds: number, playing: boolean}   // 回报里带；服务端以收到的时刻为采样时刻
```

### 2.6 能力（参考稿 §1.2）

```text
Capabilities = {
  stream_formats: [{codec: "flac" | "wav" | "opus" | "aac" | string, mime_type: string, lossless: boolean,
                    max_sample_rate: integer?, max_bit_depth: integer?}],   // 能解的流格式，按偏好排序；具体取值由 dsp-pipeline 的 iOS 验证定
  max_channels: integer,        // 能放的最多声道
  dsd: "none" | "dop" | "native",
  exclusive: boolean,           // 能否独占输出（原生客户端用；浏览器为 false）
  volume: "number" | "fixed"    // fixed：不能由软件调音量（iPhone/iPad 的网页）
}
默认值 = {stream_formats: [], max_channels: 2, dsd: "none", exclusive: false, volume: "number"}
```

**协商点**：`Renderer.Negotiate(capabilities, quality)` 为每条流选编码与档位，采样率、位深、声道由渲染会话确定首曲后给出（design-protocol.md §3.9）。没有可用格式时 `fault: no_stream_format`。

## 3. 控制端点

### 3.1 读取

| 方法与路径 | 响应 |
| --- | --- |
| `GET /api/v1/me/playback` | `200 Snapshot` |
| `GET /api/v1/me/playback/zones/{zone_id}/queue` | `200 {zone_id, queue_revision, entries: QueueEntry[]}`，按列表顺序，最多 10,000 条（服务端按 1,000 条一批读摘要） |
| `GET /api/v1/me/playback/events` | 事件流，见 §5 |

### 3.2 设备

| 方法与路径 | 请求 | 成功 |
| --- | --- | --- |
| `POST /devices` | `{name: string, kind: "browser" \| "native", volume: number, muted: boolean, settings: Zone.settings 的子集}` | `201 {server_time, device, zone}`；name 去掉首尾空白后 1–64 字符，同一用户已有同名时追加“ 2”“ 3”；volume ∈ [0,1]；settings 缺省的键用默认值 |
| `PATCH /devices/{device_id}` | `{name: string}` | `200 {server_time, device}`；推送 `device` |
| `DELETE /devices/{device_id}` | — | `204`；在线时 `409 device_online`；推送 `device_removed` |
| `PUT /devices/{device_id}/volume` | `{volume: number, muted: boolean}` | `200 {server_time, device}`；`capabilities.volume == fixed` 时 `409 volume_fixed`；推送 `device` |

（表中路径都省略前缀 `/api/v1/me/playback`。）

### 3.3 命令

`POST /zones/{zone_id}/commands`，请求体 `{command: string, ...参数}`，成功 `200 {server_time, zone: Zone, queue_change: QueueChange?}`；
`transfer` 另带 `target_zone: Zone`。每条命令在该播放区的锁内执行，不带期望版本号；命令不幂等，客户端不自动重试。

“起播”指：意图置为 `play`、清除故障与 `offline_paused`、连续失败计数清零；离线播放区上起播返回 `409 zone_offline` 且不改任何状态。
“开流(E, p, v)”指：关闭旧会话，从条目 E 的第 p 秒开新渲染会话，`stream` 换新、`next_stream` 置 null，状态置 `loading`；`v = new` 时开始新的访问并产生 `track_started`（起始条目的这条公告由服务端开流时产生，不等渲染会话）。
渲染器不可用或忙而开不了流时：记 `fault: transient`、意图暂停（`retry` 可重试）；E 本身打不开时照常开流，由渲染会话报 `track_failed` 并跳到下一首。

| `command` | 参数 | 语义（与现有引擎方法对应） |
| --- | --- | --- |
| `play_tracks` | `track_ids: UUID[1..10000]`、`start_index: integer`（越界取 0） | `replaceQueue`，清除失败标记；起播并开流(当前, 0, new)。曲目不存在 `404 track_not_found`（整批拒绝） |
| `play_next` | `track_ids: UUID[1..]` | `insertAfterCurrent`；原来空闲则停在新当前项的暂停；会话在放时 `NextChanged()`。超限 `409 queue_full` |
| `enqueue` | `track_ids: UUID[1..]` | `appendEntries`；同上 |
| `play_entry` | `entry_id` | 是当前项且没失败：同 `play`；否则 `jumpTo` 并起播、开流(E, 0, new)。条目不存在：无操作 |
| `remove_entry` | `entry_id` | `removeEntry`；移除的是当前项时：意图为播放且有后继就开流(后继, 0, new)，否则停在后继的暂停或空闲；否则 `NextChanged()`。不存在：无操作 |
| `move_entry` | `entry_id`、`delta: -1 \| 1` | `moveEntry`；`NextChanged()` |
| `clear_queue` | — | `clearQueue`，意图暂停，关闭会话 |
| `set_mode` | `mode` | `setMode`；`NextChanged()` |
| `play` | — | 没有当前项时取第一项；起播。会话还在就只改意图；会话已关闭（暂停超过 5 秒、停止、重启后）就开流(当前, 位置, continue)；当前项已失败时清掉标记并开流 |
| `pause` | — | 意图暂停；5 秒后仍暂停则关闭会话 |
| `stop` | — | 意图暂停、位置 0、关闭会话、结束这次访问 |
| `next` | — | `advance(manual, 跳过失败)`；有结果就起播、开流(E, 0, new)；没有：无操作 |
| `previous` | — | `retreat`；结果是当前项本身时开流(当前, 0, new)（从头再播）；起播 |
| `seek` | `position_seconds: number ≥ 0` | 钳到 `[0, duration]`（时长未知只钳下限）；300 ms 去抖后开流(当前, 位置, continue)；意图不变 |
| `set_settings` | `settings` 的任意子集 | 部分更新；会话在放时 300 ms 去抖后在当前位置重建（continue）；推送 |
| `retry` | — | 只在故障为 `transient` 时：起播、开流(当前, 位置, continue)；否则无操作 |
| `transfer` | `target_zone_id` | design.md §5.4；目标与源相同 `400 invalid_input`，目标离线 `409 zone_offline` |
| `import` | `entries: [{entry_id: string(1..64), track_id: UUID}] (≤10000)`、`current_entry_id: string?`、`mode`、`position_seconds: number`、`history: string[] (≤500)` | design.md §5.9；不满足“播放区为空且没导入过”时 `200`、`queue_change: null`、另带 `imported: false`；导入成功 `imported: true` 并产生 `queue_restored` |

未知 `command` 或参数不合法：`400 invalid_input`。设置里有不认识的键：`400 invalid_input`（v1 新增的键由服务端先支持）。

## 4. 输出端端点

### 4.1 认领 `POST /devices/{device_id}/output`

```text
请求 {protocol: 1, capabilities: Capabilities,
      takeover: boolean,                                        // 同一设备上接替刚释放的会话（浏览器多标签页用；其他客户端发 false）
      playing: {entry_id: UUID, position_seconds: number}?}    // 重新认领时仍在出声的曲目与曲目内位置；首次认领为 null
响应 200 {server_time, protocol: 1, session_id: UUID, device: Device, zone: Zone}
```

- `protocol` 不是服务端支持的版本：`400 unsupported_protocol`。
- 生成新会话，旧会话立即失效（之后的回报得 `409 output_superseded`）；关闭旧会话持有的渲染会话；保存能力；设备在线；写 `last_seen_at`；推送 `device`。
- `takeover` 且原会话释放不超过 10 秒且播放区 `offline_paused`：起播、开流(当前, 位置, continue)。
- `playing` 不为空、条目在队列里且播放区 `offline_paused`：当前条目设为它，起播、开流(它, 位置, continue)。
- 设备不存在：`404 not_found`（客户端重新登记）。

### 4.2 回报 `POST /outputs/{session_id}/reports`

```text
请求 {
  kind: "status" | "ended" | "fault",
  stream_seq: integer?,             // 正在放的流；没有流时为 null
  stream_seconds: number,           // 流内位置（从流开头算）
  status: "loading" | "playing" | "paused" | "buffering",
  fault: "autoplay_blocked" | "stream_lost" | null    // kind == fault 时必填
}
响应 200 {server_time, device: Device, zone: Zone}
```

| 结果 | 条件 |
| --- | --- |
| `200` | 正常；`stream_seq` 不是当前流或接续流时只刷新在线时间，不改状态（输出端会按响应对齐） |
| `409 output_superseded` | 会话已被同一设备的新认领取代：输出端停声 |
| `404 output_unknown` | 会话不存在（服务重启、已释放）：输出端重新认领（带 `playing`） |

各 `kind` 的处理见 design-protocol.md §3.3，播放头的使用见 §3.4，计次见 §3.8。

### 4.3 释放 `POST /outputs/{session_id}/release`

请求 `{stream_seq: integer?, stream_seconds: number}`，`204`。输出端停止出声或退出时发出（浏览器在 `pagehide` 与会话结束时发）。
会话失效、设备离线、关闭渲染会话；意图为播放时改为暂停并记 `offline_paused` 与释放时刻；推送。会话不存在时同样 `204`。

### 4.4 渲染流 `GET /zones/{zone_id}/streams/{seq}`

- 只有该播放区当前输出会话的用户能取；`seq` 不是当前流或接续流：`410 stream_gone`。
- 一条流只能被取一次（流头只在开头，无法从中途接上）：同一条流的第二个连接得 `410 stream_gone`，输出端按 `fault: stream_lost` 回报续流。
  若 dsp-pipeline 的 iOS 验证发现 Safari 先发探测请求再正式请求，改用 dsp-pipeline design-delivery.md §1.4 的头部重放，协议形状不变。
- 响应体是从开流时的起始位置开始、跨曲目连续的音频，`Content-Type` 等于 `Stream.mime_type`；`Cache-Control: no-store`。是否支持 Range、是否带长度，由 dsp-pipeline 的 iOS 验证决定。
- 曲目边界、失败与流结束只经服务端的未来事件队列处理，不写进响应体；客户端只需要 `Stream.segments`。

## 5. 事件流 `GET /api/v1/me/playback/events[?output_session=<UUID>]`

- 格式是标准 SSE（`text/event-stream`），任何客户端可读。响应头：`Content-Type: text/event-stream`、`Cache-Control: no-store`、`X-Accel-Buffering: no`；写完每条事件立即 `Flush`。
- 第一条总是 `snapshot`。之后的事件按发生顺序；每条形如 `event: <类型>\ndata: <一行 JSON>\n\n`，不带 `id:`（重连不补发，靠快照）。
- 每 15 秒写 `: ping\n\n`；每 60 秒顺带重新核对凭据，失效即结束流。应用关停、请求取消时结束。
- 每个订阅有 64 条的缓冲，满了就结束这条流（客户端重连拿快照），不阻塞命令。
- `output_session` 是该用户当前有效的输出会话时，这条流连着即视为该输出端在线；无效时当普通控制端流处理，不报错。
- 非 200（如 401）时客户端不应立即重连；其余断开按 1、2、5、10 秒退避重连。

| `event` | `data` | 何时 |
| --- | --- | --- |
| `snapshot` | `Snapshot` | 连接建立 |
| `zone` | `{zone: Zone}` | 播放区状态变化：命令、换曲、开流、回报（约每 10 秒）、在线变化 |
| `queue` | `{zone_id: UUID, change: QueueChange}` | 条目增删移动 |
| `device` | `{device: Device}` | 登记、改名、音量、在线变化、认领 |
| `device_removed` | `{device_id: UUID, zone_id: UUID}` | 删除或自动清理 |

一次命令可能产生多条事件（如转移：两个 `queue`、两个 `zone`），按“队列先于状态”的顺序发出。

## 6. 错误码

| 状态与 `code` | 条件 |
| --- | --- |
| `400 invalid_json` | 请求体不是 JSON 或类型不符、超过 1 MiB |
| `400 invalid_id` | 路径里的 ID 不是 UUID |
| `400 invalid_input` | 字段取值不合法、未知命令或设置键、转移给自己 |
| `400 unsupported_protocol` | 认领时声明的协议版本不受支持 |
| `401 unauthorized` | 未鉴权、凭据失效、成员停用 |
| `403 origin_required` / `origin_forbidden` | Cookie 会话写请求的来源校验 |
| `404 not_found` | 设备、播放区或流不存在或不属于该用户 |
| `404 track_not_found` | 命令里有不存在的曲目 |
| `404 output_unknown` | 输出会话不存在 |
| `409 queue_full` | 加入后超过 10,000 条 |
| `409 zone_offline` | 离线播放区上起播、转移目标离线 |
| `409 device_online` | 删除在线设备 |
| `409 volume_fixed` | 给不能调音量的设备设音量 |
| `409 output_superseded` | 输出会话已被取代 |
| `410 stream_gone` | 流的 seq 已不是当前流或接续流，或这条流已经被取过 |
| `503 database_unavailable` | 数据库失败 |

检查顺序：来源 → 鉴权 → 路径 ID → JSON → 字段 → 归属（404）→ 状态冲突（409/410）→ 写入。

## 7. 常量

| 常量 | 值 | 位置 |
| --- | --- | --- |
| 队列上限 / 历史上限 | 10,000 / 500 | `zones/queue` |
| 输出端播放中回报间隔 | 10 秒，状态变化与换流立即 | 输出端 |
| 在线租约 / 离线扫描间隔 | 45 秒 / 5 秒 | `zones` |
| 接手窗口 | 10 秒 | `zones` |
| 连续失败停止 | 5 次 | `zones` |
| 重建去抖 / 暂停后关闭会话 | 300 ms / 5 秒 | `zones` |
| 续流连续失败后暂停 | 3 次 | `zones` |
| 计一次播放门槛 | min(时长 / 2, 240 秒)，前进量超过墙钟流逝 + 1 秒视为跳变 | `zones`（与 `listening.Threshold` 同一规则） |
| 位置写库间隔 | 最多每 10 秒 | `zones` |
| 事件流心跳 / 凭据复核 | 15 秒 / 60 秒 | `zones/httpapi` |
| 订阅缓冲 / replace 内联上限 | 64 条 / 500 条 | `zones` |
| 控制目标回退 | 暂停超过 15 分钟或离线 | 客户端 |
| 设备自动清理 | 30 天没上线，每小时扫一次 | `zones` |
| 音量下发节流 / 乐观显示 | 125 ms / 1 秒 | 客户端 |

## 8. 浏览器客户端的实现约定（不属于协议）

| 项 | 约定 |
| --- | --- |
| 设备 ID | `localStorage["roomusic:playback-device:v1:<userId>"]` = `{version: 1, deviceId}`，边界解码，坏了就当没有 |
| 控制目标 | `localStorage["roomusic:control-target:v1:<userId>"]` = `{version: 1, zoneId}`，`storage` 事件同步标签页 |
| 旧队列 | `sessionStorage["roomusic:player:v1:<userId>"]`，只读一次用于 `import`，之后删除 |
| 旧偏好 | `roomusic:player-volume:v1`、`roomusic:playback-settings:v1`，只在登记时读作初值（音量、电平化、交叉渐变），不再写入，不删除 |
| 多标签页 | Web Lock `roomusic:output:<deviceId>` 选出唯一的输出端标签页，`roomusic:device-registration:<userId>` 串行首次登记；接替时认领带 `takeover: true` |
| 释放 | `pagehide` 与会话结束时 `fetch(…, {method: "POST", keepalive: true})` 发 `release` |
| 事件流 | `fetch` + `ReadableStream` 按行解析（不用 `EventSource`）；401 经 `SessionBoundary.onUnauthorized(请求时的代次)`；控制端标签页隐藏 30 秒后断开 |
| 手势解锁 | 本机第一次点击时，在同一个点击里对两个 `<audio>` 元素各同步 `play()` 一段静音再暂停，之后远程起播、换流、续流不需要新的手势 |
| 能力 | `stream_formats` 用 `canPlayType` / `MediaSource.isTypeSupported` 探测 dsp-pipeline 给出的候选；`volume` 按“写 0.5 读回”判定；`max_channels: 2`、`dsd: "none"`、`exclusive: false` |
| 音量 | 直接写元素的 `volume` / `muted`；没有 Web Audio |
