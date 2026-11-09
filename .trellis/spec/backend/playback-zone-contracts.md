# 播放区协议 v1（客户端协议文档）

任何客户端（浏览器、以后的原生客户端）按本文实现控制端与输出端。服务端内部见[播放区服务端实现合同](./playback-zone-service-contracts.md)、
[播放区数据合同](./zones-data-contracts.md)与[服务端渲染合同](./playback-render-contracts.md)。本文以已实现代码为准（`zones`、`zones/httpapi`、`playback/render`）。

## 0. 约定

- 路径前缀 `/api/v1/me/playback`，下文省略。路径与请求体里没有用户 ID，一律作用于已鉴权的用户；管理员也只能访问自己的设备。
- **版本**：v1 内只做增加（字段、事件类型、命令、枚举值），客户端必须忽略不认识的字段与事件类型；不兼容的改动换前缀 `/api/v2/…`。
- **鉴权**：每个请求经 `Guard.Authenticate`，协议不规定凭据形式（现在只有同源 HttpOnly Cookie）。写请求先来源校验（403）再鉴权（401）。流地址由服务端给出，不含凭据。
- **JSON**：请求体是单个对象、≤ 1 MiB，拒绝重复键与未知键；必填键不能缺也不能为 `null`，标 `?` 的键必须出现、可为 `null`。
  响应 `Cache-Control: no-store`；错误信封 `{error: {code, message}, request_id}`（[平台运行合同](./core0-runtime-contracts.md)）。
- UUID 小写；时间为 UTC RFC 3339、带毫秒。改变状态的响应带 `server_time`，客户端取最近一次算时钟偏移（`server_time` − 收到时的本地时间）。
- **样本**：`backend/internal/app/testdata/api-samples/playback-zones/` 下由真实 handler 生成：`requests/<名>.json`（`{method, path, body}`）、
  `responses/<名>.json`（`{status, body}`）、`events/<名>.json`（`{event, data}`），错误在 `errors/` 子目录；ID 与时间已归一化。HLS 没有样本（见 `zones/hls_http_integration_test.go`）。
  下文“样本 x”指同名的请求与响应。

## 1. 设备与播放区

一台设备对应一个播放区。设备由客户端登记、服务端生成 ID；播放区持有队列、模式、播放设置与传输状态。

```text
Device = {device_id, zone_id, name, kind: "browser"|"native", online, last_seen_at, volume: 0..1, muted,
          capabilities: Capabilities, output_session_id: UUID?}
Snapshot = {server_time, protocol: 1, devices: Device[], zones: Zone[]}          // 按设备创建时间升序
QueueEntry = {entry_id: UUID, track: TrackSummary}   // 与 POST /tracks/summaries 同一投影，按曲目当前的发行与组给出
Zone = {zone_id, device_id, revision, queue_revision, queue_length,
        mode: "sequential"|"repeat_all"|"repeat_one"|"shuffle", settings: Settings,
        current: QueueEntry?, has_next, intent: "play"|"pause",
        status: "idle"|"loading"|"playing"|"paused"|"buffering",
        fault: {kind: "transient"|"autoplay_blocked"|"no_stream_format"}?, failures: [{entry_id, reason}],
        position: {seconds, at, duration_seconds?, seekable}, stream: Stream?, next_stream: Stream?,
        paused_at: time?, signal: Signal?, event: ZoneEvent?}
Settings = {leveling: "off"|"auto"|"track"|"album", gapless, crossfade, crossfade_seconds: 3|5|8|12,
            quality: "lossless"|"lossy", headroom: {enabled, db, clip_indicator},
            sample_rate: {strategy, custom_rate: number|null, filter}, downmix: {enabled: boolean}}
```

- `revision` 每次状态变化加一，客户端按它丢弃旧状态；`queue_revision` 只在条目增删移动时加一。
- `status` 来自输出端回报；开新流时服务端先置 `loading`，设备离线时为 `paused` 或 `idle`。`paused_at` 是意图变为暂停的时刻。
- `position` 是曲目内位置；`status == "playing"` 时控制端按 `seconds + (服务端现在 − at)` 外推，不超过时长。输出端用流内位置自己算（§3.4）。
- `failures` 只在内存，替换队列、清空、重启时清除；`reason` ∈ `not_found/root_disabled/unsupported_format/source_unavailable/source_changed/decode_failed`。
- 默认设置：`auto`、无缝开、交叉渐变关、5 秒、`lossless`、Headroom `{enabled: false, db: -3, clip_indicator: false}`。
- `downmix` 默认 `{enabled:true}`，完整文档合同见[设置合同 §9](./zones-data-contracts.md#9-降混设置文档0046)。
- `sample_rate` 默认 `{strategy:"none",custom_rate:null,filter:"linear_precise"}`；策略、滤波器枚举、标准采样率与完整文档校验见[设置合同 §8](./zones-data-contracts.md#8-src-设置文档0045)。
- `ZoneEvent = {seq, kind, …}` 是一次性公告，`seq` 取当时的 `revision`，每个 `seq` 只公告一次；只带与 `kind` 有关的字段：
  `track_started {entry_id}`、`track_failed {entry_id, reason}`（服务端已跳到下一首）、`all_failed`、`failures_stopped`（连续 5 首失败，已暂停）、
  `transient_failure`（可 `retry`）、`autoplay_blocked`、`no_stream_format`、`queue_restored {restored, dropped}`、`play_recorded {track_id}`（据此刷新播放历史）。

队列：`GET /zones/{zone_id}/queue` → `200 {zone_id, queue_revision, entries}`，按列表顺序、≤ 10,000 条（样本 queue、queue-empty）。增量：

```text
QueueChange = {queue_revision, kind, …}: replace {entries}（超过 500 条改发 reload）| reload | insert {index, entries}（插在第 index 位之前）
              | remove {entry_id} | move {entry_id, delta: -1|1} | clear
```

手里的 `queue_revision` 等于 `change.queue_revision − 1` 才应用，否则重新 GET。只有显示该队列的客户端需要保存队列。

| 方法与路径 | 请求 | 成功 |
| --- | --- | --- |
| `GET` 空路径 | — | `200 Snapshot`（样本 snapshot、snapshot-empty） |
| `POST /devices` | `{name, kind, volume, muted, settings?}`，`settings` 是 `Settings` 的任意子集、可省略 | `201 {server_time, device, zone}`（register-device） |
| `PATCH /devices/{id}` | `{name}` | `200 {server_time, device}`（rename-device） |
| `DELETE /devices/{id}` | — | `204`；在线时 `409 device_online`（delete-device） |
| `PUT /devices/{id}/volume` | `{volume, muted}` | `200 {server_time, device}`；`volume: fixed` 的设备 `409 volume_fixed`（volume） |

名称去掉首尾空白后 1–64 字符，同名时服务端追加“ 2”“ 3”。新登记的设备离线。30 天没上线且离线的设备每小时自动清理（推送 `device_removed`），
认领得 `404 not_found` 时客户端重新登记。音量属于设备、只作用于输出端，不进渲染流。

## 2. 命令

`POST /zones/{zone_id}/commands`，请求 `{command, …参数}`，成功 `200 {server_time, zone, queue_change: QueueChange?}`；`transfer` 另带 `target_zone`，`import` 另带 `imported`。
命令在播放区内串行执行，不带期望版本号，不幂等，客户端不自动重试；条目已不存在时是无操作。样本与命令同名（下划线换连字符）。

“起播”：意图置 `play`，清故障与离线暂停标记；目标离线时 `409 zone_offline` 且不改状态。“开流”：关旧会话、从某条目某秒开新流，`next_stream` 置空、状态 `loading`。
服务端没有渲染器时起播类命令 `503 renderer_unavailable`；渲染器忙或打不开时接受命令、置 `fault: transient` 并暂停。

| `command` | 参数 | 语义 |
| --- | --- | --- |
| `play_tracks` | `track_ids: UUID[1..10000]`、`start_index`（越界取 0） | 替换队列并起播；有不存在的曲目整批 `404 track_not_found` |
| `play_next` / `enqueue` | `track_ids: UUID[1..]` | 插到当前项后 / 追加到队尾，不打断；超过 10,000 条 `409 queue_full` |
| `play_entry` / `remove_entry` | `entry_id` | 播放这一项 / 移除（移除正在放的当前项时接着放后继） |
| `move_entry` | `entry_id`、`delta: -1\|1` | 上移或下移一位 |
| `clear_queue`、`set_mode {mode}` | — | 清空并暂停；切换模式 |
| `play`、`pause`、`stop` | — | 播放（没有当前项取第一项）；暂停；暂停并回到 0 |
| `next`、`previous` | — | 手动下一首（跳过失败项）；上一首（结果是当前项时从头放） |
| `seek` | `position_seconds ≥ 0` | 钳到 `[0, 时长]`，300 ms 去抖后从新位置开流 |
| `set_settings` | `settings`：任意子集，给出的键非 `null`，`headroom`、`sample_rate`、`downmix` 须是完整对象 | 部分更新；正在放时 300 ms 去抖后重建流 |
| `retry` | — | 只在 `fault: transient` 时起播并续流 |
| `transfer` | `target_zone_id` | 队列、当前项、位置、模式与历史搬到目标并直接播放，源清空；给自己 `400 invalid_input` |
| `import` | `entries: [{entry_id: string(1..64), track_id}]`（≤ 10,000，ID 不重复）、`current_entry_id: string?`、`mode`、`position_seconds`、`history: string[≤500]` | 只在播放区为空且从没导入过时生效（`imported: true`，公告 `queue_restored`），否则 `imported: false` |

暂停 5 秒后服务端关闭渲染会话（`stream` 置空），之后 `play` 从保存的位置开新流。

## 3. 输出端

### 3.1 认领 `POST /devices/{device_id}/output`

请求 `{protocol: 1, capabilities, takeover: bool, playing: {entry_id, position_seconds}?}`，响应 `200 {server_time, protocol: 1, session_id, device, zone}`。

- 生成新会话，同设备旧会话立即失效（之后回报 `409 output_superseded`）；保存能力，设备在线。不支持的版本 `400 unsupported_protocol`。
- `takeover: true`：原会话释放不超过 10 秒且播放区因释放而暂停时，从原位置恢复（浏览器多标签页接替；其他客户端发 `false`）。
- `playing` 非空、条目仍在队列且播放区离线暂停时，从该位置恢复（重启或掉线后重新认领）。
- 样本 claim、claim-takeover、claim-playing、claim-replacement、claim-fixed、claim-target。

### 3.2 期望状态与对齐

输出端只关心 `stream`、`next_stream`（接续流）、`intent` 与设备音量。协议没有曲目前瞻：换曲、电平化、无缝、交叉渐变都在流里完成；
`next_stream` 只在下一首格式不同或超时没准备好时出现，可被替换。每次拿到状态（事件、命令响应、回报响应）都对齐，`revision` 旧的丢弃：

```text
stream == null                             → 停止并卸载（保留锁屏信息与暂停态）
stream.seq 是正在放的或已预载的接续流      → 不动
否则                                       → 在备用播放器打开 stream，出声后换上、卸载旧的
next_stream 未预载                         → 在备用播放器预载（不播放）
按 intent play/pause；按 volume/muted 设音量
```

当前流 `ended` 且接续流已预载时立即换上，并以接续流的 `stream_seq` 回报 `status: playing`，服务端据此把它晋升为当前流（样本 next-stream-playing）。
漏掉、重复、乱序的消息不会让输出端做错事；重连后拿快照再对齐一次即可。

### 3.3 回报 `POST /outputs/{session_id}/reports`

请求 `{kind: "status"|"ended"|"fault", stream_seq: int?, stream_seconds: 0..1e7, status: "loading"|"playing"|"paused"|"buffering", fault: "autoplay_blocked"|"stream_lost"?}`，
五个键都须出现，`fault` 只在 `kind == "fault"` 时非空；响应 `200 {server_time, device, zone}`。同一输出端串行发送：

| 时机 | 回报 | 服务端 |
| --- | --- | --- |
| 状态变化立即；播放中每 10 秒；换上新流时 | `status` | 作为播放头推进曲目、位置与计次；续租 |
| 当前流放完且没有接续流 | `ended` | 意图暂停 |
| 系统拒绝自动播放 | `fault: autoplay_blocked` | 暂停并记故障，用户在那台设备上点播放时清除 |
| 流断开或取不到（网络、410、5xx、长时间缓冲） | `fault: stream_lost` | 按分段换算断开处的曲目与位置、立即续流；连续 3 次失败才暂停为 `transient` |

- `stream_seq` 不是当前流或接续流时只续租；响应里的新状态让输出端对齐。所以输出端**不连事件流也能收到远端命令**，最迟在下一次回报时生效。
- 事件类回报失败时保留、随下一次重发（重复是无操作）；状态类失败直接丢弃，只保留最新一条。
- `409 output_superseded`：停声。`404 output_unknown`（服务重启、已释放）：带 `playing` 重新认领。曲目级失败由服务端经 `track_failed` 带回，不经回报。
- 样本 status-playing/paused/loading/buffering、ended、fault-stream-lost、fault-autoplay-blocked。

### 3.4 位置与分段

`Stream.segments = [{entry_id, track, stream_start_seconds, track_start_seconds}]` 是已渲染、尚未播完的分段。输出端取 `stream_start_seconds ≤ 流内位置` 的最后一段，
曲目内位置 = `track_start_seconds + 流内位置 − stream_start_seconds`，在分段边界本地切换锁屏信息。服务端只以回报的播放头判断“正在放哪首”（渲染跑在缓冲前面）。

### 3.5 释放、在线与计次

- `POST /outputs/{session_id}/release`，请求 `{stream_seq: int?, stream_seconds}`，`204`（未知会话也 `204`）：立即离线、关闭渲染会话，意图为播放时改为离线暂停（样本 release*）。
- 输出会话**在线**当且仅当带 `output_session` 的事件流连着，或距上次回报 ≤ 45 秒（每 5 秒扫描）。播放中每 10 秒回报，所以锁屏断开事件流也保持在线。
- 判离线时意图为播放则暂停、保留位置、记离线暂停；原会话之后回报 `status: playing` 即恢复。任何控制命令清掉该标记。重启后全部离线，输出端按 `output_unknown` 重新认领接着放。
- 计次在服务端：门槛 min(时长 / 2, 240 秒)（时长未知取 240 秒），只累计播放中的正向前进，超过墙钟流逝 + 1 秒视为跳变；一次访问最多一次，记下后公告 `play_recorded`。
  起播类命令与自然换曲开始新访问；定位、改设置、续流、暂停后继续、接手、重启后续播延续原访问。离线期间的出声不计；访问状态不持久化，跨重启的同一次访问可能重计（已知缺口）。

## 4. 事件流 `GET /events[?output_session=UUID]`

- 标准 SSE：`text/event-stream`、`no-store`、`X-Accel-Buffering: no`；每条 `event: <类型>\ndata: <一行 JSON>\n\n`，不带 `id:`，写完即 flush。HEAD 得 `405 method_not_allowed`。
- 首条总是 `snapshot`，重连不补发。每 15 秒 `: ping`；每 60 秒复核凭据，失效即结束流。订阅缓冲 64 条，满了结束流，客户端重连拿快照。
- `output_session` 是本人有效的输出会话时，流连着即视为该输出端在线；无效或他人的会话按普通控制端处理，不报错。
- 非 200（如 401）不立即重连；其余断开按 1、2、5、10 秒退避，之后每 10 秒。

| `event` | `data` | 何时 |
| --- | --- | --- |
| `snapshot` | `Snapshot` | 连接建立 |
| `zone` | `{zone}` | 命令、换曲、开流、回报（约每 10 秒）、在线变化、信号路径变化 |
| `queue` | `{zone_id, change: QueueChange}` | 条目增删移动 |
| `device` | `{device}` | 登记、改名、音量、在线变化、认领 |
| `device_removed` | `{device_id, zone_id}` | 删除或自动清理 |

样本在 `events/`（`zone-*` 覆盖各种公告与 `zone-signal`、`zone-next-stream`）。一次命令可能产生多条事件，同一播放区内队列事件先于状态事件。
反代须对事件流与长流不缓冲、读超时 ≥ 60 秒、不压缩 `text/event-stream`（`deploy/production/README.md`）。

## 5. 流

`Stream = {seq, url, mime_type, quality: "lossless"|"lossy", format: {codec, sample_rate, bit_depth?, channels}?, segments}`。`seq` 单调递增（跨重启也是），`url` 由服务端给出。
`format` 在首曲确定后给出；首曲就打不开的空流为 `null`（之后照常收到 `track_failed` 与接续流）；有损档 `bit_depth` 为 `null`。一条流内格式不变。
只有该播放区当前输出会话的用户能取；`seq` 不是当前流或接续流 `410 stream_gone`；`seq` 不是非负整数 `400 invalid_input`。

**长流** `GET /zones/{zone_id}/streams/{seq}`（样本 stream、stream-headers）：

- 从开流位置起跨曲目连续的音频；头为 `Content-Type: <mime_type>`、`Accept-Ranges: none`、`Cache-Control: no-store`、`X-Content-Type-Options: nosniff`、`X-Accel-Buffering: no`，无长度。
- 忽略 Range，总是 `200`；**只能取一次**，第二个连接 `410 stream_gone`（输出端按 `stream_lost` 续流）；HEAD `405`。客户端断开不关闭服务端会话。
- 编码：`flac`（`audio/flac`，STREAMINFO 长度与 MD5 为 0）、`wav`（`audio/wav`，长度字段写 `0xFFFFFFFF`）、`opus`（`audio/ogg; codecs="opus"`，128 kbps、48 kHz）、`aac`（`audio/aac`，ADTS AAC-LC 256 kbps）。

**HLS** `GET /zones/{zone_id}/streams/{seq}/{file}`（`mime_type` 为 `application/vnd.apple.mpegurl`，`url` 指向 `…/index.m3u8`）：

- `file` 只认 `index.m3u8`、`init.mp4`、`<十进制片号>.m4s`，列表用相对地址。取用条件同长流但**可重复取**；片号未产出、已删除或文件名不认识得 `404 not_found`。
- 增长型 EVENT 列表（`VERSION:7`、`TARGETDURATION:4`、`START:TIME-OFFSET=0,PRECISE=YES`），条目只增不删，流自然结束（放完或格式边界）时写 `ENDLIST`；
  `stream_seconds` 即列表时间线上的位置。不足 3 片且未结束时列表请求最多等 10 秒；带 `Accept-Encoding: gzip` 时列表 gzip。
- 初始化段与分片 `audio/mp4`，带长度、支持 Range。片长约 4 秒；结束时刻早于“播放头 − 30 秒”的分片被删除；会话关闭后之后的请求 410。
- 分片里无损是 `alac`，有损是 AAC-LC 256 kbps（`aac`）。格式边界仍用接续流（新的播放列表）。

## 6. 能力声明与协商

```text
Capabilities = {stream_formats: [{codec, mime_type, lossless, max_sample_rate: int?, max_bit_depth: int?}],   // 按偏好排序
                max_channels: 1..64, dsd: "none"|"dop"|"native", exclusive: bool, volume: "number"|"fixed"}
从没认领过 = {stream_formats: [], max_channels: 2, dsd: "none", exclusive: false, volume: "number"}
```

- 五个键必填；每项 `codec`、`mime_type` 非空，两个上限必须出现、非空时为正。`mime_type` 为 `application/vnd.apple.mpegurl` 表示 HLS，否则是长流。
- `volume: fixed` 表示软件不能调音量（iPhone/iPad 网页）。`dsd`、`exclusive` 第一批只保存，不影响交付。
- 服务端能产出的 `(codec, mime_type)`：长流 `flac`、`wav`、`opus`、`aac`（MIME 同 §5），HLS `alac`、`aac`；只发布 ffmpeg 有对应编码器的组合（WAV 总可用）。

协商在每次开流时进行：按声明顺序过滤出能产出的项；`quality: lossless` 取第一个无损项，没有就取第一个有损项，`lossy` 反之；一个都没有则 `fault: no_stream_format`、暂停。
具体格式按首曲定：`max_channels` 为 1–32，非 WAV 限 8。超限时按[降混合同](./playback-render-contracts.md#13-降混后端接入2026-11-09)混合或选择声道。采样率按 SRC 策略与设备上限规划，Opus 固定 48 kHz，AAC 使用 44.1/48 kHz；
无损位深在处理链透明（整数源、电平化与 Headroom 都不施加）时取源位深（≤ 16 → 16，其余 24），否则 24，不超过 `max_bit_depth`。下一首源格式不同时以接续流交付。
浏览器的声明见[播放与个人状态](../frontend/playback-and-personal-state.md)“播放区客户端”。

## 7. 信号路径与 Headroom

`Zone.signal` 是正在放的那首的信号路径（播放头到达分段起点时更新）；没有流时为 `null`。样本 events/zone-signal。

```text
Signal = {quality, steps: Step[], clipping?: bool, processing_speed?: number}   // 两个可选键不适用时省略（不是 null）
Step = {kind, quality, lossy: bool, compat: bool, params}
quality ∈ "lossless" > "enhanced" > "high" > "low"（从好到差；Signal.quality 取最差的一步）
```

| `kind` | `params` | 评级 |
| --- | --- | --- |
| `source` | `{codec, sample_rate, channels, bit_depth?}` | 无损源 `lossless`；`mp3/aac/vorbis/opus/wavpack_hybrid` 为 `low`、有损 |
| `sample_rate` | `{from_rate:int, to_rate:int, filter:string}` | 用户转换 `enhanced`；设备/有损约束 `high`、兼容转换；仅链 ② 产生 |
| `downmix` | `{from_channels, to_channels, from_layout, to_layout, by:"roomusic", mode:"matrix"\|"select"}` | `high`、兼容转换；`select` 为纯选择；兼容旧 `by:"ffmpeg"` |
| `leveling` | `{gain_db, basis}` | `enhanced`；电平化不为“关”时出现 |
| `headroom` | `{gain_db}` | `enhanced`；开启时出现 |
| `dither` | `{bits, kind: "tpdf"}` | `enhanced`；降位深时 `high`、兼容转换；逐位直通时不出现 |
| `encode` | 无损 `{codec, sample_rate, bit_depth}`；有损 `{codec, sample_rate, bitrate_kbps}` | 无损 `lossless`；有损 `low`、有损 |

- 交叉渐变是瞬时的，不进信号路径；输出设备一步由客户端按设备名补上。
- **Headroom**：`db` ∈ [−30, 0]、步进 0.1，否则 `400 invalid_input`。开启时在处理链开头乘 `10^(db/20)`，整条流不再逐位、量化时加抖动，无损档按 24 位输出。改动经 `set_settings`（样本 set-settings-headroom）。
- **削波指示**：只在 `clip_indicator` 开时统计（无损档在量化钳位处，有损档在交给编码器前统计 |x| > 1）。有削波立即 `clipping: true`，最后一次削波约 450 ms 后变回 `false`。
- **处理速度**：处理的音频时长 / 解码、处理链与量化花的墙钟时长，一位小数，变化且距上次 ≥ 1 秒才更新。

## 8. 错误码

检查顺序：来源 → 鉴权 → 路径 ID → JSON → 字段 → 归属（404）→ 状态冲突（409/410）→ 写入。样本在 `errors/`。

| 状态与 `code` | 条件 |
| --- | --- |
| `400 invalid_json` | 不是 JSON、类型不符或超过 1 MiB |
| `400 invalid_id` | 路径里的设备、播放区、会话 ID 不是 UUID |
| `400 invalid_input` | 取值不合法、缺必填键、未知键/命令/设置键、重复键、转移给自己、流 `seq` 不是非负整数 |
| `400 unsupported_protocol` | 认领时的协议版本不受支持 |
| `401 unauthorized` | 未鉴权、凭据失效、成员停用 |
| `403 origin_required` / `origin_forbidden` | Cookie 会话写请求的来源校验 |
| `404 not_found` | 设备、播放区、流不存在或不属于本人；HLS 文件不存在 |
| `404 track_not_found` / `output_unknown` | 命令里有不存在的曲目 / 输出会话不存在 |
| `405 method_not_allowed` | 对事件流或长流发 HEAD |
| `409 queue_full` / `zone_offline` | 超过 10,000 条 / 离线播放区上起播、转移目标离线 |
| `409 device_online` / `volume_fixed` / `output_superseded` | 删除在线设备 / 设备不能调音量 / 会话已被新认领取代 |
| `410 stream_gone` | 不是当前流或接续流、长流已被取过、HLS 会话已关闭 |
| `503 renderer_unavailable` / `database_unavailable` | 没有可用的渲染器（起播类命令不改状态）/ 数据库失败 |

## 9. 常量

| 项 | 值 |
| --- | --- |
| 队列 / 历史 / replace 内联上限 | 10,000 / 500 / 500 条 |
| 回报间隔 / 在线租约 / 离线扫描 / 接手窗口 | 播放中 10 秒 / 45 秒 / 5 秒 / 10 秒 |
| 重建去抖 / 暂停后关闭会话 | 300 ms / 5 秒 |
| 连续失败停止 / 续流失败暂停 | 5 首 / 3 次 |
| 事件流心跳 / 凭据复核 / 订阅缓冲 | 15 秒 / 60 秒 / 64 条 |
| HLS 片长 / 首次等待 / 分片保留 | 约 4 秒 / ≤ 10 秒 / 播放头前 30 秒 |
| 设备自动清理 | 30 天没上线，每小时扫一次 |
| 控制目标回退（客户端） | 暂停超过 15 分钟或离线 |
