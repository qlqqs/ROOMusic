# 技术设计（二）：实时通道、输出端协议、数据模型与扩展点

[design.md](./design.md) 的续篇，因 Trellis 单文件 32 KB 注入上限拆出；章节编号与 design.md 共用一套。
接口合同见 [design-contract.md](./design-contract.md)。参考稿正本在本机 `~/.local/share/roomusic-reference/reference-design.md`（不进仓库），本文只引用其章节号与概括。

## 2. 实时通道：SSE + REST 命令

| 考量 | SSE（fetch 流读取） | WebSocket |
| --- | --- | --- |
| 同源 Cookie 鉴权 | 普通 GET，`Guard.Authenticate` 照用；跨站页面读不到响应（没有 CORS 头） | 握手带 Cookie，但要自己校验 Origin 防跨站劫持；Go 标准库没有实现，要引第三方依赖 |
| 写操作的同源校验 | 命令都是普通 POST，沿用 `RequireSameOrigin` | 消息在一条连接里，同源校验只发生在握手时 |
| 反向代理 | 普通 HTTP 长响应：只要求不缓冲、读超时大于心跳；服务端发 `X-Accel-Buffering: no`，nginx 默认配置即可工作 | 需要代理显式转发 `Upgrade/Connection`，家用 NAS 的反代界面常常没有这个开关 |
| 手机锁屏/后台被挂起 | 两者都会被挂起或断开，**都不能依赖**；本设计的输出端靠 REST 回报对账（§3），通道只用来加快远端命令到达 | 同左 |
| 断线重连与补发 | 重连后服务端先发一份完整快照（设备 + 播放区，不含队列），客户端按 `queue_revision` 判断要不要重读队列。快照很小，不需要补发环 | 同样需要自己做 |
| 单进程内存状态 | hub 在进程内按用户扇出，命令写库后再推送；断开即退订，没有跨连接状态 | 同左 |
| 401 处理 | 用 fetch 读流（不用 EventSource）：能拿到状态码，401 经现有会话边界处理；重连节奏、页面隐藏时断开都由前端控制 | — |

**重连**：1、2、5、10 秒退避，之后每 10 秒；页面重新可见时立即重连。服务端每 15 秒写一行注释（`: ping`）作心跳，每 60 秒顺带重新核对一次会话，失效就结束流（这条流会持续推送私有数据，与音频流“建立后不复核”的取舍不同）。

**偏离参考稿 §2.5**：参考稿用自定义二进制 RPC、变更集差量和 16 MiB 发送历史环。ROOMusic 的状态是“一个用户的几个播放区”，快照只有几 KB，
重连直接发全量快照更简单、不会出错；队列用 `queue_revision` 加增量事件，序号对不上就整段重读。参考稿的历史环是为大对象图准备的，这里不需要。

**HTTP/1.1 每个主机 6 条连接的限制**：局域网直连通常是明文 HTTP/1.1，每个标签页的事件流各占一条。
控制端标签页在页面隐藏超过 30 秒时断开事件流，重新可见时重连并以快照对齐；输出端标签页始终保持。经 HTTPS 反代走 HTTP/2 时没有这个问题。

**部署要求**（写进 `deploy/production/README.md` 第 5 节）：事件流路径 `/api/v1/me/playback/events` 不缓冲（nginx 认 `X-Accel-Buffering: no`，其他代理要关掉缓冲）、
读超时不小于 60 秒、不对 `text/event-stream` 压缩。代理缓冲时的表现是“别的设备上的变化要等很久才出现”。

## 3. 输出端协议

只有一种交付：**服务端渲染流**。一个播放区一条跨曲目的连续流；输出端只把流放出来、回报流内位置，换曲、电平化、无缝、淡化都在服务端完成。

### 3.1 期望状态，而不是一串命令

服务端给每个播放区维护一份状态（字段见合同 §2.2），其中与输出端有关的是：

- `stream`：当前要放的流 `{seq, url, mime_type, quality, format, segments}`。`seq` 是播放区的流序号（取开流时的 `revision`，跨重启也递增）；`segments` 是已渲染、尚未播完的曲目分段
  `[{entry_id, track, stream_start_seconds, track_start_seconds}]`，输出端用它在本地算曲目内位置、在边界换锁屏信息。没有活动的渲染会话时为 null（停止、清空、暂停超过 5 秒后）。
- `next_stream`：**接续流**。下一首与当前流格式不同、不能放进同一条流时（无缝的格式条件，算法线 gapless-crossfade G1），或下一首超时没准备好时（G3），服务端提前开好下一条流；输出端在当前流 `ended` 时换上它。
  会话再次宣布边界时以最新一条替换（seq 换新，输出端按对齐函数改预载）。其余时候为 null。
- `intent`：`play` 或 `pause`，用户想要的状态。
- 设备的 `volume/muted`。

协议层**没有曲目前瞻**：下一首由渲染会话在服务端内部经 `Cursor` 取得，按参考稿 §7.3 预缓冲、提前准备，以便无缝。

输出端的对齐函数（每次拿到状态时调用，来源可以是事件流、命令响应或回报响应；`revision` 比已处理的旧就丢弃）：

```text
if zone.stream == null:                     卸载两个元素（保留锁屏上的曲目信息与暂停态）
elif zone.stream.seq == 正在放的 seq:          无事
elif zone.stream.seq == 已预载的接续流 seq:     无事（当前流 ended 时自然换上）
else:                                       按 §3.5 在备用元素上打开 zone.stream 并换上
if zone.next_stream 且 seq 不是已预载的:      在备用元素上预载（preload，不播放；替换旧的预载）
if intent == play  且元素没在放:  element.play()
if intent == pause 且元素在放:    element.pause()
setVolume(device.volume, device.muted)
```

这是“对齐到目标状态”，漏掉、重复、乱序的消息都不会让输出端做错事：重连后拿到快照再对齐一次即可。

### 3.2 渲染流

- **两档**：无损档按能力协商 FLAC 或 WAV、位深与采样率；有损档由服务端编成 Opus 或 AAC（按输出端能解的选），给在外省流量用。档位是播放区设置 `quality`，在“设置 › 播放”里选。
- **流的格式**：`Renderer.Negotiate(capabilities, quality)` 选编码与档位（§3.9），采样率、位深、声道在 `Open` 确定首曲后由 `session.Format()` 给出，填进 `Stream.format`。同一条流内格式不变，格式变化只发生在流与流之间（接续流）。
- **流从哪里开始**：每条流对应一次开流，带“起始条目 + 曲目内起始秒数”。开流的时机：起播、切曲、定位、改设置、切模式或改队列使已准备的下一首失效且必须重建、暂停超过 5 秒后恢复、断流后续流、转移、重启后接着放、接手。
- **流在哪里结束**：队列自然放完（`stream_ended`）；下一首格式不同或超时没准备好（`format_boundary`，此时已开好接续流）；被新流取代（会话关闭，输出端已换走）。
- **地址**：`GET /zones/{zone_id}/streams/{seq}`（合同 §4.4），只有该播放区当前输出会话的用户能取；`seq` 不是当前流或接续流时 `410 stream_gone`；一条流只能被取一次，第二个连接同样 `410`，输出端按 `stream_lost` 续流。流的编码、分块、iOS 上用长 FLAC/WAV 响应还是 ManagedMediaSource，由 dsp-pipeline 设计（§14）。

### 3.3 输出端回报

`POST /outputs/{session}/reports`（合同 §4.2），同一个输出端**串行**发送（一次只有一个在途，状态类回报合并成最新一条）：

| 时机 | `kind` | 服务端处理 |
| --- | --- | --- |
| 状态变化（loading/playing/paused/buffering）立即报；播放中按 `timeupdate` 节流每 10 秒报一次；换上新流时报一次 | `status` | 带 `stream_seq` 与流内秒数；交给渲染会话 `Observe` 作为播放头（§3.4）；写在线时间 |
| 当前流放完且没有接续流 | `ended` | 正常情况下会话已经派发了 `stream_ended`；这条回报只用于收尾（意图暂停、关闭会话） |
| 浏览器拒绝自动播放 | `fault: autoplay_blocked` | 意图改为暂停并记下故障，用户在那台设备上点播放时清除 |
| 流断开或取不到（网络、`410`、5xx） | `fault: stream_lost`，带流内秒数 | 换算出断开处的曲目与位置，**不暂停**，立即从那里开新流（visit 延续）；连续三次续流都失败时意图暂停、记 `transient` 故障 |

- `stream_seq` 不是当前流（也不是接续流）的回报只刷新在线时间、不改状态；响应里的新状态会让输出端对齐。
- 回报的响应总是 `{zone, device}`：**输出端不需要事件流也能拿到远端命令**，最迟在下一次回报（≤10 秒）时生效。
- 事件类回报（`ended`、`fault`）失败时保留，随下一次回报重发；重复的是无操作。状态类回报失败直接丢弃。
- 曲目级的失败（文件变化、目录停用、解码失败）发生在服务端打开来源时，由会话的 `track_failed` 事件带回，不经输出端回报。

### 3.4 位置换算与会话事件

渲染跑在播放头前面（浏览器会缓冲），所以“此刻在放哪首、第几秒”只能按参考稿 §7.4 的未来事件队列得到：会话边渲染边登记带**流帧号**的事件，**播放头到达才派发**。
未来事件队列由 dsp-pipeline 在会话里实现；`zones` 负责喂播放头、应用会话发出的事件。事件分两类，`zones` 不能假定所有事件都按播放头到达：

- **到达播放头才派发**：`time`、`track_started`、`track_failed`、`stream_ended`、带分段的 `signal`。
- **立即发出**：`format_boundary`（要让 `zones` 提前开接续流）、`rebuild_needed`、削波与处理速度的 `signal` 更新。

`zones` 在自己的协程里持续读 `Events()` 直到通道关闭，每条事件在播放区锁内应用：


- **播放头**：每条 `status` 回报换成 `PlayheadSample{stream_seconds, at, playing}` 交给 `session.Observe`；两次回报之间由会话按墙钟外推，下一次回报再校正。
- `time`（每 200 ms）：更新 `position`（曲目内秒数）、喂计次（§3.8）、连续失败计数清零。不逐条广播：播放区事件只在回报、换曲与状态变化时发出，控制端自己外推。
- `track_started {entry_id, transition}`：按 `AdvanceTo` 接受自然前进，当前条目换成它，开始新的访问，产生 `track_started` 公告，更新 `segments`。`transition: gapless` 即参考稿 §4.4 的 OnToNext，流不停；
  `crossfade` 是交叉渐变；`gap` 是关了无缝时曲间插入 0.5 秒静音。起始条目不会收到这条：`zones` 开流时自己产生（合同 §3.3 的“开流”）。
- `track_failed {entry_id, reason}`：标记失败、连续失败计数加一；满 5 次关闭会话、意图暂停、公告 `failures_stopped`（参考稿 §4.4）；否则会话自己经 `Cursor` 跳到下一首，`Cursor` 跳过已失败的条目。
  起始条目打不开也走这条（`Open` 本身不因曲目报错）。
- `format_boundary {next_entry_id, reason}`：`reason` 为 `format`（下一首格式不同）或 `not_ready`（下一首超时没准备好）。为这一条开接续流（visit 新），放进 `next_stream`；
  可以重复发送（宣布边界后下一首又变了），`zones` 关闭旧的接续流会话、以最新一条替换 `next_stream`。
- `stream_ended`：队列放完。意图暂停、当前条目留在最后一首、位置回 0（等于现在播完最后一首）；全部失败时公告 `all_failed`。
- `rebuild_needed`：已准备的下一首失效且已进入发出的字节，按 §3.5 重建。
- `signal`：信号路径（结构归 dsp-pipeline P11），原样放进状态的 `signal`；两类来源都按到达顺序覆盖。

**开流失败**：`Renderer.Open` 只在渲染器不可用或忙（并发上限）时返回错误，`zones` 记 `fault: transient`、意图暂停，`retry` 可重试；曲目级问题都经事件。
**调用约束**：`Observe`、`NextChanged`、`Close` 不阻塞，在播放区锁内调用；`Cursor.Next` 由会话在自己的协程里调用、会取播放区锁，不回调会话；`Close` 可重复调用。

### 3.5 重建与换流

- **重建**：关闭旧会话，按“当前条目、目标位置”开新会话，`stream.seq` 换新。定位、改处理设置、切模式**300 ms 去抖**（参考稿 §4.5、§6.1）：300 ms 内连续的改动只重建一次，去抖期间状态已显示目标位置与 `loading`。
  不要求样本级衔接，短暂换流可以接受（同 Roon）；定位本来就是跳转。
- **改队列**：`zones` 调 `session.NextChanged()`；会话判断已准备的下一首还没进入发出的字节就悄悄换掉，否则发 `rebuild_needed`。
- **暂停**：5 秒内恢复只是 `element.play()`（会话因 TCP 背压停在原处）；超过 5 秒服务端关闭会话、`stream` 置 null，恢复时从保存的位置开新流（参考稿 §4.5）。
- **输出端换上新流**：活动元素继续放旧流，备用元素打开新流并 `play()`；备用元素出 `playing` 时卸载旧元素、两者对调，回报 `status`。意图是暂停时只预载、不播放，换上后保持暂停。
- **接续流**：预载在备用元素上；当前流 `ended` 时立即 `play()` 备用元素并对调，回报 `status`（新 `stream_seq`、流内位置从 0 起）。
- 备用元素失败按 `stream_lost` 回报；旧流继续放到服务端给出新流为止。

### 3.6 锁屏后台不中断

- 换曲在流内完成，媒体元素自己持续拉流，不需要页面脚本参与；回报由 `timeupdate` 驱动（后台的 `setInterval` 会被节流，播放中的媒体事件不会），服务端据此推进队列与计次。
- 锁屏信息：输出端按 `segments` 与本地流内位置在曲目边界换 Media Session 的标题与时长，不等服务端的回报响应。
- 流断开（换网络、服务端重启）：浏览器先放完已缓冲的部分，随后出 `error` 或长时间 `waiting`；输出端回报 `fault: stream_lost`，服务端从换算出的位置开新流，输出端在**同一个已解锁的元素**上换源 `play()`。
- 回报发不出去（网络一直不通）：缓冲放完后停下；服务端 45 秒收不到回报判离线、暂停在最后的位置（R6）。网络恢复、页面被唤醒后重新认领。
- 锁屏期间别的设备发的命令，最迟在下一次回报（≤10 秒）时以新流的形式生效。
- **iOS 风险（须先做小验证，归 dsp-pipeline）**：Safari 能否在 `<audio>` 里播放没有长度的长 FLAC/WAV 响应、锁屏后能否连续播放跨过多首、后台换源能否不经手势 `play()`（含锁屏暂停超过 5 秒后从锁屏控件恢复、需要换新流的情形）、
  流内 `currentTime` 是否单调且准确。任何一项不成立都会改变交付方式（例如改用 ManagedMediaSource，或按片段交付），协议形状不变。

### 3.7 在线、离线与“锁屏仍在播”

一个输出端会话**在线**，当且仅当：它的事件流连着（流的查询参数带 `output_session`），或者距上次回报不超过 **45 秒**。

- 播放中每 10 秒必有回报，所以锁屏后事件流被断开也一直在线；45 秒容得下四次回报丢失。
- 暂停中的手机锁屏、事件流被断开 45 秒后算离线：此时本来就不在出声，转为离线没有副作用；解锁后重新连上即恢复在线。
- **关页面**：浏览器在 `pagehide` 时发 `release`，服务端立即判离线、关闭渲染会话，不等 45 秒。
- 服务端每 5 秒扫一次；判离线时若意图是播放，就改为暂停、关闭会话、保留位置（R6），并记 `offline_paused`。
- `offline_paused` 的播放区，原来的输出端会话回报 `status: playing`（锁屏时网络断过、其实一直在放）时恢复意图播放；会话已关闭则按 `stream_lost` 续流。任何控制命令或转移都会清掉这个标记。
- 服务重启后会话全部失效：输出端的回报得到 `404 output_unknown`，随即重新认领，带上自己正在放的条目与曲目内位置（由 `segments` 算出），服务端从那里开新流（§5.8）。

### 3.8 服务端计次

把 `frontend/.../engine/listen-tracker.ts` 的规则移植到 `zones`，规则不变：门槛 min(D/2, 240 秒)，D 用校正后的显示时长；只在播放中累计曲目内位置的前进量；
相邻两条 `time` 之间前进量超过墙钟流逝 + 1 秒视为跳变，不累计；一次访问最多记一次。

- **访问**：`track_started` 派发、起播类命令开的新流（立即播放、播放此项、下一首、上一首、转移）开始新访问；定位、改设置重建、`stream_lost` 续流、暂停后继续、接手、重启后接着放延续原访问（换流时重置基准，跨过的距离不算）。
- 达到门槛时经端口 `PlayRecorder.Record(ctx, userID, idempotencyKey, trackID, playedSeconds)` 记一次（`app` 装配到 `listening.Service.Record`，幂等键是该访问的 UUID；写库失败在下一条 `time` 时重试，同一访问不会记两次），然后推送 `play_recorded`。
- 播放区主人即记录归属，与输出端是哪台设备无关。输出端离线期间的出声收不到，不计（已接受）。

### 3.9 能力声明与交付协商（R14，参考稿 §1.2）

输出端每次认领都声明能力（合同 §2.6）：能解的流格式（编码、容器、MIME、每种的最高采样率与位深）、最多声道、DSD 方式、是否独占输出、音量是否可调。
服务端据此：经 `Renderer.Negotiate` 为每条流选编码与档位，具体采样率、位深、声道由会话在首曲确定后给出（不超过声明的上限；声道不够由处理链降混、不支持 DSD 就转 PCM，具体归算法线）；`volume: fixed` 时拒绝设音量。
没有任何可用格式时开流失败，播放区记 `fault: no_stream_format`、意图暂停。浏览器、以后的原生客户端、服务端驱动的输出端都走同一个协商点。

## 4. 数据模型与迁移

一个迁移文件 `00NN_playback_zones.sql` 加 `.down.sql`。编号：`main` 当前到 0036，扫描对标的 O、S、T 线各自占用了暂定编号，
本任务在分支上先用 0037，**合入 main 时按当时的最大编号顺延**（与 M5、扫描对标线的做法相同，`migration_version_integration_test` 里的版本号随之更新）。

```sql
CREATE TABLE playback_devices (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL DEFAULT 'browser' CHECK (kind IN ('browser','native')),
  capabilities jsonb NOT NULL DEFAULT '{}',     -- 最近一次认领声明的能力（合同 §2.6），服务端按它协商
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
  volume real NOT NULL DEFAULT 1 CHECK (volume >= 0 AND volume <= 1),
  muted boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, user_id)
);
CREATE INDEX playback_devices_user_idx ON playback_devices (user_id);

CREATE TABLE playback_zones (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL,
  device_id uuid NOT NULL UNIQUE,
  mode text NOT NULL DEFAULT 'sequential' CHECK (mode IN ('sequential','repeat_all','repeat_one','shuffle')),
  current_entry_id uuid,                        -- 由服务代码保证指向本区条目；转移会搬动条目，不设外键
  position_seconds double precision NOT NULL DEFAULT 0 CHECK (position_seconds >= 0 AND position_seconds <= 10000000),
  position_saved_at timestamptz NOT NULL DEFAULT now(),
  history uuid[] NOT NULL DEFAULT '{}',         -- 最多 500
  shuffle_bag uuid[] NOT NULL DEFAULT '{}',
  leveling text NOT NULL DEFAULT 'auto' CHECK (leveling IN ('off','auto','track','album')),
  crossfade boolean NOT NULL DEFAULT false,
  crossfade_seconds smallint NOT NULL DEFAULT 5 CHECK (crossfade_seconds IN (3,5,8,12)),
  gapless boolean NOT NULL DEFAULT true,       -- 无缝（第一批，语义归算法线 gapless-crossfade）
  quality text NOT NULL DEFAULT 'lossless' CHECK (quality IN ('lossless','lossy')),  -- 流的档位（语义归 dsp-pipeline）
  revision bigint NOT NULL DEFAULT 0,
  queue_revision bigint NOT NULL DEFAULT 0,
  imported_at timestamptz,
  FOREIGN KEY (device_id, user_id) REFERENCES playback_devices (id, user_id) ON DELETE CASCADE
);
CREATE INDEX playback_zones_user_idx ON playback_zones (user_id);

CREATE TABLE playback_queue_entries (
  zone_id uuid NOT NULL REFERENCES playback_zones(id) ON DELETE CASCADE,
  entry_id uuid NOT NULL,
  position integer NOT NULL CHECK (position >= 0),
  track_id uuid NOT NULL REFERENCES tracks(id),  -- 不级联：曲目从不物理删除（与 play_history 相同）
  PRIMARY KEY (zone_id, entry_id),
  UNIQUE (zone_id, position) DEFERRABLE INITIALLY DEFERRED
);
```

- **设备与播放区分两张表**，现在是一对一（`device_id UNIQUE`）：以后多输出编组时，播放区与设备的关系改成关联表，不必拆表。复合外键保证播放区与设备同属一个用户。
- **队列行**：`position` 只表示顺序。插入时 `UPDATE … SET position = position + n WHERE position > $at` 一条语句腾位置；移除只删行不重排；移动是交换两行的位置；
  替换与清空是整段删除再用 `unnest` 批量插入。上限 10,000 条在服务代码里检查（参考稿是 8,000，沿用现有前端的 10,000，见 §9）。
- 历史与随机袋用数组存在播放区行里：随机模式每次前进要改袋子，整列重写 10,000 个 UUID（约 160 KB）在人操作的频率下可以接受，比给条目加一列再维护简单。
- 条目 ID 由服务端生成 UUID；导入旧队列（R11）时一律换新 ID，并按映射改写当前条目与历史。
- **处理设置的位置**：第一批要用的电平化、交叉渐变、无缝、档位都是 `playback_zones` 的列，本线迁移建好；第二批的 PEQ、卷积、降混、Headroom 等按 Roon 同样归播放区，由算法线的迁移加在 `playback_zones` 上（结构复杂的可用一列 `jsonb` 或一对一的 `playback_zone_dsp` 表，由那条线定）。本线不为它们预建空列。转移不搬播放设置。
- **IR 文件与 EQ 预设按成员归属**（2026-11-08 用户裁决）：由算法线建带 `user_id` 的表，播放区设置按 ID 引用；`set_settings` 收到引用时经算法线提供的端口核对“属于播放区主人”，不属于按不存在处理。播放区与引用的预设同属一个成员，转移（只在本人设备之间）不会让引用失效。
- **回退 SQL** 删除三张表（数据库指南要求 0031 起每个迁移带 `.down.sql`）。降级会丢掉服务端的队列，数据在迁移前备份里。
- **长期不用的设备**：服务每小时清理一次 `last_seen_at` 早于 30 天、且此刻不在线的设备，级联删除播放区与队列，并向本人推送 `device_removed`。
  `last_seen_at` 在认领、事件流连接、释放时写，回报时最多 5 分钟写一次。
- 播放位置随会话的 `time` 事件更新内存，最多每 10 秒写一次 `position_seconds`。

## 6. 界面与文案（F3 的边界）

- 播放栏加一个“播放设备”图标按钮，打开设备列表：每行设备名（本机标“本机”），第二行是正在放的曲名、“已暂停”或“离线”。点一行即把它设为控制目标。
- 行内图标按钮：“转到这台设备”（把当前控制目标的播放转过来）、离线行的“转到本机”、“改名”、离线行的“删除”。删除确认只写一句：“它的队列会一起删除。”
- 控制目标不是本机时，播放栏在控制按钮旁显示目标设备名（图标 + 名字，不加说明）。
- 远端设备不能调音量时音量控件禁用，悬停提示“这台设备不能调音量”。远端被拒绝自动播放时提示“请在那台设备上点一下播放”。
- “设置 › 播放”：电平化（关 / 自动 / 按曲目 / 按专辑）、无缝（开关）、交叉渐变与时长、音质（无损 / 省流量），作用于正在控制的那台设备，标题旁显示设备名（§12）；不写说明句。信号路径的展示归 dsp-pipeline。
- 连续失败停止的公告：“连续几首都放不了，已停下。”没有可用的流格式时：“这台设备放不了服务器给的音频格式。”
- 不写解释权限、同步、离线判定、流与重建的说明。

## 7. 其他输出端（R12 / R14，只定接口形状）

控制面（命令 → 播放区状态 → 渲染会话）与输出端的种类无关，三类输出端走同一套状态、回报、能力协商与计次：

| 输出端 | 怎么接入 | 首版 |
| --- | --- | --- |
| 浏览器 | 合同 §4 的 HTTP 端点 + §5 事件流 + 渲染流 | 实现 |
| 原生客户端（R14） | 同一套 HTTP 端点、事件流与渲染流，按协议 v1 自己实现；能力里声明更高的采样率、DSD、独占输出等 | 不做；鉴权方式以后定（加在 Guard 里），协议不变 |
| 服务端驱动（UPnP、ALSA 直连 DAC、AirPlay…） | 进程内实现下面的接口，直接消费渲染会话，调用 `Service` 的回报入口 | 不做 |

```go
// Output 是进程内的输出端（服务端驱动的设备）。它收到的状态与浏览器从事件流里收到的相同，
// 自己读渲染会话的 Body 送给设备，并像浏览器一样把播放头回报给 Service.Report。
type Output interface {
	Kind() string                // "upnp"、"alsa"、"airplay"…
	Capabilities() Capabilities  // 与合同 §2.6 同一个结构
	Deliver(state Zone)          // 不阻塞；同一输出端按顺序投递
}
```

计次（§3.8）、协商（§3.9）、会话事件的应用（§3.4）都在 `zones` 里，接入新输出端不需要再实现一次。
接入时要补的（首版不做）：`playback_devices.kind` 放宽取值；发现与登记由各协议的集成负责；原生客户端的鉴权与它的来源校验规则；公用设备的共享规则（R8 已裁决留到那时）。

## 9. 偏离参考稿的记录

| 参考稿 | 本设计 | 理由 |
| --- | --- | --- |
| 自定义二进制 RPC、变更集差量、大容量发送历史环（§2.5） | SSE + REST，重连发全量快照，队列增量带序号 | 状态很小；浏览器原生支持；不需要补发环 |
| 位置事件每 200 ms 一条推给界面（§7.4） | 会话内照样每 200 ms 派发，但只在回报、换曲与状态变化时广播，控制端按时间戳外推 | 跨网络推 200 ms 事件浪费；外推同样平滑 |
| 未来事件队列，到达播放头才派发（§7.4） | **照搬**；播放头来自输出端回报加墙钟外推 | 渲染跑在浏览器缓冲前面，只有这样才知道此刻在放哪首 |
| 定位与改设置重建处理链（§4.5、§6.1） | 照搬为“重建流”，300 ms 去抖 | 用户定的去抖时长 |
| 暂停后释放设备、恢复时从保存位置重建（§4.5） | 照搬：暂停超过 5 秒关闭渲染会话 | 释放解码器与处理链 |
| 队列上限 8,000（§4.2） | 10,000 | 沿用现有前端与发行详情的上限 |
| 队列项可以是整张专辑（§4.2） | 一项一首 | R2 要求与现有前端语义一致 |
| 播放记录阈值 30 秒或 1/3（§11.3） | min(时长/2, 240 秒)，服务端判定 | 沿用阶段 P 的已定规则（R10） |
| 自动续播、撤销重做、睡眠定时器（§4.3、§11.3、§11.4） | 不做 | 不在 PRD 范围 |
| 连续 5 次失败停止（§4.4） | **照搬** | 现有前端没有这条 |
| 端点掉线区域暂停、回归后可恢复（§12.2） | 照搬，另加“回报证明仍在播就恢复” | 浏览器的“掉线”可能只是锁屏时事件流断开 |

## 10. 测试范围（只测改动直接影响的行为）

- `zones/queue`：把 `queue.test.ts` 的用例表移植成 Go 表驱动测试，补“下一条”（随机预选与自然前进一致、预选条目被移除后重选）几条。
- 服务端计次：把 `listen-tracker.test.ts` 的规则用例移植成 Go 表驱动测试（恰好一次、跳变不算、暂停继续、换曲与循环重新计、换流延续访问）。
- `zones` + `zones/httpapi`（隔离 PostgreSQL，**假渲染器**按脚本发事件、输出静音 WAV）：每条命令的代表性成功路径；转移；导入；会话事件的应用（换曲、失败与 5 次停止、接续流及重复宣布时替换、放完）；`Open` 出错记 `transient`；
  离线转暂停与回报恢复；`takeover` 接手；`output_superseded` / `output_unknown`；`stream_lost` 续流；`410 stream_gone`；隔离；写接口 403→401；重启（新建 Service 读同一库）后认领接着放；清理 30 天设备；事件流首条快照与一条增量。
- 自动电平化的“发行上下文”（`Entry.AlbumContext` 由 `zones` 计算，规则同 `leveling.ts` 的 `inAlbumContext`）：移植 `leveling.test.ts` 的三条用例（相邻同一发行为真、不同发行为假、随机模式为假）。
- 无缝开关：`gapless: false` 时会话发出的 `track_started.transition` 为 `gap`（曲间 0.5 秒静音），用假渲染器按设置产生，核对 `zones` 把设置传进 `RenderSpec`、按事件推进。
- 前端单元：对齐函数（新流换上、接续流在 ended 换上、意图与暂停、旧 revision 丢弃）；store 的进度外推；控制目标回退（播放中、暂停 14/16 分钟、离线）；SSE 解析与 401；R11 导入后删键；按 `segments` 换锁屏信息。
- 真实服务（`scripts/playback-e2e.sh`，真实渲染器、两个浏览器上下文模拟两台设备）：PRD 验收各条一个用例。
- 不测：渲染会话内部（归算法线）、浏览器 Web Locks 与 fetch 流本身；不做长时间锁屏演练（真机手工核对一次锁屏连播）。
