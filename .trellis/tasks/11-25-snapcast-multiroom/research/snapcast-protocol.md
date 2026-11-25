# 调研：Snapcast 服务端协议（为 ROOMusic 自研服务端）

- **问题**：snapclient 的发现、连接、消息格式、握手、时钟同步、音频块、播放校正、控制、多台、断线、高码率、版本兼容、无声卡测试
- **范围**：内部（本机克隆源码）为主，外部只查了发行版的软件包版本
- **日期**：2026-11-25
- **一手资料**：`temp/research-slimproto/snapcast/`，浅克隆，提交 `ea981ec`（2026-11-03），`changelog.md` 最新一节是 0.36.0，`CMakeLists.txt:15` 写 0.35.0。GPL-3，本文只用自己的话描述协议行为，不含代码。下文路径省略前缀 `temp/research-slimproto/snapcast/`。

---

## 1. 发现

**结论**：snapserver 用 mDNS 发布服务类型 `_snapcast._tcp`（音频流，端口 1704），实例名 `Snapcast`，**不带 TXT 记录**。snapclient 没有给服务器地址时，默认 URI 是 `tcp://_snapcast._tcp`：它用 Avahi 只找 IPv4，**取第一个解析出来的结果**，每次找 1 秒，没找到就隔 500 ms 再找。找到以后把 IP 和端口写死在设置里，之后重连也不再重新查找。

| 项目 | 内容 | 来源 |
|---|---|---|
| 流服务 | `_snapcast._tcp`，端口取 tcp-streaming 的端口（默认 1704） | `server/snapserver.cpp:455-458`，`server/server_settings.hpp:206` |
| 控制服务 | `_snapcast-ctrl._tcp`，JSON-RPC over TCP，默认 1705 | `server/snapserver.cpp:460-463`，`server/server_settings.hpp:193` |
| HTTP | `_snapcast-http._tcp`，默认 1780：Snapweb、`/jsonrpc`（HTTP 与 WebSocket）、`/stream`（WebSocket 音频流） | `server/snapserver.cpp:464-467`，`server/server_settings.hpp:168`，`server/control_session_http.cpp:402-450` |
| HTTPS | `_snapcast-https._tcp`，默认 1788 | `server/snapserver.cpp:468-471`，`server/server_settings.hpp:170` |
| 实例名 | `"Snapcast"`；名字冲突时用 Avahi 生成的替代名 | `server/snapserver.cpp:453`，`server/publishZeroConf/publish_avahi.cpp:112,194` |
| TXT | 无（`avahi_entry_group_add_service` 的 TXT 参数为空） | `server/publishZeroConf/publish_avahi.cpp:161-162` |
| 客户端默认 URI | `tcp://_snapcast._tcp` | `client/snapclient.cpp:149,432-439` |
| 判断是不是服务名 | host 为空或以 `_` 开头就按 mDNS 服务名查找 | `client/controller.cpp:433-450` |
| 查找方式 | Avahi，`AVAHI_PROTO_INET`（只要 IPv4），每次超时 1000 ms，找到第一个就返回 | `client/browseZeroConf/browse_avahi.cpp:188-206`，`client/controller.cpp:386` |
| 重试 | 没找到时隔 500 ms 再查 | `client/controller.cpp:401-412` |
| 找到之后 | IP 和端口写进 `settings_.server.uri`，重连时直接用（`reconnect()` 走 `worker()`，不再查 mDNS） | `client/controller.cpp:444-447,464-480` |
| URI 默认端口 | tcp→1704，ws→1780，wss→1788；`-h/-p` 从 0.32 起不推荐使用，但仍可用（强制 tcp） | `client/snapclient.cpp:358-394`，`changelog.md:189` |

影响：同一局域网里同时有 ROOMusic 和真正的 snapserver 时，不带地址启动的 snapclient 连到哪一台取决于谁先被解析出来。服务端只能靠发布 `_snapcast._tcp` 来被发现，不能用 TXT 区分。

## 2. 连接与消息

**结论**：TCP 上每条消息由 26 字节的公共头和类型相关的正文组成，全部**小端**。公共头里的两个时间戳是“发送方填的发送时刻”和“接收方填的接收时刻”，都是各自的**单调时钟**（秒 + 微秒）。目前用到的消息类型有 0–5、7、8；6（StreamTags）已经注释掉。单条消息正文上限 1 000 000 字节，双方超过都会断开。

### 公共头（Base，26 字节）

| 偏移 | 长度 | 类型 | 字段 | 含义 |
|---|---|---|---|---|
| 0 | 2 | uint16 | type | 消息类型 |
| 2 | 2 | uint16 | id | 请求编号（客户端请求从 2 开始递增，到 10000 回到 1） |
| 4 | 2 | uint16 | refersTo | 回应时填所回应请求的 id |
| 6 | 4 | int32 | sent.sec | 发送方的发送时刻，秒 |
| 10 | 4 | int32 | sent.usec | 发送方的发送时刻，微秒（0–999999） |
| 14 | 4 | int32 | received.sec | 接收方的接收时刻，秒（接收方读到后自己填，线上的值没人用） |
| 18 | 4 | int32 | received.usec | 同上，微秒 |
| 22 | 4 | uint32 | size | 后面正文的字节数 |

来源：`common/message/message.hpp:188-241`（读写顺序是 sent 在 received 前，与结构体成员顺序无关）；小端：`doc/binary_protocol.md:8`，`common/endian.hpp:24-32`（只有大端主机才交换字节序）；请求 id：`client/client_connection.cpp:246-249`；1 MB 上限：`common/message/message.hpp:164-165`，`server/stream_session_tcp.cpp:103-114`，`client/client_connection.cpp:366-379`。

时间戳格式 `tv` 是一对 int32（秒、微秒），默认构造就取当前单调时间（Linux 上是 `std::chrono::steady_clock`，即 `CLOCK_MONOTONIC`；Windows 上是系统时间）：`common/message/message.hpp:110-159`，`common/time_defs.hpp:41-47,85-91`。

### 各消息正文

| id | 名称 | 方向 | 正文布局 | 来源 |
|---|---|---|---|---|
| 1 | CodecHeader | S→C | `uint32 codec名长度` + codec名（不带 NUL，如 `flac`/`pcm`/`ogg`/`opus`/`null`）+ `uint32 头长度` + 编码器头 | `common/message/codec_header.hpp:48-71` |
| 2 | WireChunk | S→C | `int32 ts.sec` + `int32 ts.usec` + `uint32 长度` + 编码数据（每条消息是一段可独立解码的数据） | `common/message/wire_chunk.hpp:62-97` |
| 3 | ServerSettings | S→C | `uint32 长度` + JSON：`bufferMs`、`latency`、`volume`(0–100)、`muted` | `common/message/server_settings.hpp:33-92`，`common/message/json_message.hpp:45-65` |
| 4 | Time | 双向 | `int32 latency.sec` + `int32 latency.usec`（8 字节） | `common/message/time.hpp:39-58` |
| 5 | Hello | C→S | `uint32 长度` + JSON | `common/message/hello.hpp:80-93` |
| 6 | StreamTags | — | 已废弃，枚举里是注释 | `common/message/message.hpp:67` |
| 7 | ClientInfo | C→S | `uint32 长度` + JSON：`volume`、`muted` | `common/message/client_info.hpp:38-70` |
| 8 | Error | S→C | `uint32 code` + `uint32 长度`+错误串 + `uint32 长度`+详细信息 | `common/message/error.hpp:45-74` |

接收方对未知类型的处理：类型号大于 8 直接报错断开（客户端会重连）；类型号是 6 这类在范围内但工厂不认识的，客户端只记一条警告，然后跳过：`client/client_connection.cpp:366-372,395-399`，`client/controller.cpp:195-198`，`common/message/factory.hpp:65-89`。

### Hello 里客户端报告的内容

| 键 | 值 | 来源 |
|---|---|---|
| `MAC` | 第一块非回环网卡的 MAC，取不到时是 `00:00:00:00:00:00` | `common/message/hello.hpp:82`，`client/client_connection.cpp:334-346`，`common/utils.hpp:283+` |
| `HostName` | 主机名 | `common/message/hello.hpp:83` |
| `Version` | snapclient 版本串，如 `0.32.0` | `common/message/hello.hpp:84` |
| `ClientName` | `"Snapclient"`（第三方客户端可能不同） | `common/message/hello.hpp:85` |
| `OS` / `Arch` | 发行版名 / CPU 架构 | `common/message/hello.hpp:86-87` |
| `Instance` | 同一台机器上的实例号，默认 1（`-i`） | `common/message/hello.hpp:88`，`client/snapclient.cpp:179` |
| `ID` | `--hostID`，默认等于 MAC；MAC 无效（全 0、Android 的 02:00…、Mac 的 iBridge）时退回主机名 | `common/message/hello.hpp:89`，`client/controller.cpp:489-491`，`common/utils.hpp:428-468` |
| `Auth` | 可选，`{scheme, param}`，URI 里带用户名密码时是 Basic | `common/message/hello.hpp:90-91`，`client/snapclient.cpp:396-402` |
| `SnapStreamProtocolVersion` | 固定 `2` | `common/message/hello.hpp:92` |

snapserver 的客户端唯一标识：`ID`（没有就用 `MAC`），`Instance` 不等于 1 时再加 `#<instance>`：`common/message/hello.hpp:140-162`，`server/server.cpp:311`。缺键时的默认值：Instance=1，协议版本=1，ID=MAC（`common/message/hello.hpp:135-150`）。

## 3. 握手顺序

**结论**：服务器收到 Hello 后，必须**先**发一条 `refersTo = Hello.id` 的 ServerSettings，**再**发 CodecHeader，之后才能发 WireChunk。客户端在收到 ServerSettings 回应后才开始对时，在收到 CodecHeader 后才建解码器和播放器。之后任何时候都要回应客户端的 Time 请求。

客户端一侧（`client/controller.cpp:482-541`，`client/client_connection.cpp:262-281`）：

1. TCP 连上后先开始读消息，再用 `sendRequest` 发 Hello，**超时 2 s**。
2. 回应按 `refersTo` 和请求 id 配对。配上的回应必须是 ServerSettings（或 Error），否则重连。如果服务器发的 ServerSettings 没填 `refersTo`，这条消息会走普通消息分支，Hello 请求 2 s 后超时，客户端重连。
3. 收到 Error 回应只记日志，不主动重连（`client/controller.cpp:511-517`）；snapserver 发完 Error 会关掉连接（`server/server.cpp:341-349`），客户端读出错以后 1 s 重连。
4. 收到 ServerSettings 后，连续快速对时 50 次（间隔 100 µs），之后每 1 s 一次（`client/controller.cpp:88,343-377,531`）。
5. 收到 CodecHeader 后：销毁旧的解码器、缓冲（Stream）和播放器，按 codec 名建新解码器，从头里解析采样格式，再建 Stream 和播放器（`client/controller.cpp:229-327`）。这里直接用了 `serverSettings_`，所以 **CodecHeader 必须排在 ServerSettings 之后**，否则空指针。
6. 收到 WireChunk 时，只有解码器和 Stream 都已经存在才解码入队，否则丢弃（`client/controller.cpp:200-217`）。

snapserver 的做法（`server/server.cpp:307-426`）：鉴权（可选）→ 建或找到组与客户端记录 → 发 ServerSettings（音量、静音=客户端或组静音、客户端 latency、全局 bufferMs，refersTo=hello.id）→ 给会话挂上组的流 → 发该流编码器的 CodecHeader → 之后这个会话就收到该流的 WireChunk 广播。

## 4. 时钟

**结论**：服务器时间就是 snapserver 进程看到的 `steady_clock`（Linux 上为 `CLOCK_MONOTONIC`，从开机算起），协议里所有时间戳都在这个时间轴上。客户端用 NTP 式的一次往返估计偏移，把 200 个样本放进窗口取**中位数**。连接后先连续快速对时 50 次，然后每 1 s 一次。客户端的偏移缓冲是进程级单例，**断线重连不清空，只有距上次对时超过 60 s 才清空**。

### 一次往返

| 步骤 | 谁 | 内容 | 来源 |
|---|---|---|---|
| 1 | 客户端 | 发 Time 请求，公共头 `sent` = 发送前一刻的客户端单调时间（`latency` 字段无意义） | `client/client_connection.cpp:196-202` |
| 2 | 服务器 | 读完正文后记 `received` = 服务器单调时间 | `server/stream_session_tcp.cpp:128-129` |
| 3 | 服务器 | 回 Time：`refersTo = 请求 id`，`latency = received − sent`（服务器收到时刻减去客户端发送时刻，等于“单程时延 + 时钟偏移”），公共头 `sent` = 服务器序列化回应时的时间 | `server/server.cpp:272-280`，`server/stream_session.hpp:72-88` |
| 4 | 客户端 | 读完公共头立刻记 `received` = 客户端单调时间 | `client/client_connection.cpp:361-363` |
| 5 | 客户端 | `c2s = 回应.latency`，`s2c = 回应.received − 回应.sent`；偏移 `diff = (c2s − s2c) / 2`（毫秒，double），即服务器时间减客户端时间 | `client/controller.cpp:357`，`client/time_provider.cpp:36-43` |
| 6 | 客户端 | 把 diff（换算成 µs）放进容量 200 的窗口，当前偏移取中位数；`serverNow() = 本地 steady_clock + 偏移` | `client/time_provider.cpp:30-33,46-66`，`client/time_provider.hpp:87-90`，`client/double_buffer.hpp:60-81` |

| 参数 | 值 | 来源 |
|---|---|---|
| 稳态对时间隔 | 1 s | `client/controller.cpp:88,360` |
| 连接后快速对时 | 50 次，间隔 100 µs | `client/controller.cpp:361-367,531` |
| Time 请求超时 | 2 s，超时就重连 | `client/controller.cpp:346-354` |
| 偏移窗口 | 200 个样本，取中位数 | `client/time_provider.cpp:32,62-63` |
| 窗口清空 | 距上次对时超过 60 s 才清空 | `client/time_provider.cpp:49-60` |

对服务端实现的影响：

- snapserver 的回应 `sent` 在**进入发送队列时**就填好了（源码自己写了 TODO：应该在真正发送时再填，`server/stream_session.cpp:119`）。前面排着大的 WireChunk 时，这会让回程看起来变长，偏移估计有偏差；客户端的中位数能压掉一部分。服务端 TCP 设置了 `TCP_NODELAY`（`server/stream_server.cpp:208-209`）；客户端没设置。
- **服务器时钟必须在 ROOMusic 重启后保持连续**：客户端重连时（1 s 后）偏移窗口还留着 150 个旧样本，新的 50 次快速对时压不过中位数，要再过约 51 s 新样本才能过半。如果服务器时间轴以进程启动为起点，重启后这段时间里客户端会按错误的偏移播放。用主机的 `CLOCK_MONOTONIC` 就没有这个问题，同一主机上的所有容器共用它（不用时间命名空间时）。
- 秒字段是 int32，单调时钟从开机计，不会溢出。

## 5. 音频

**结论**：WireChunk 的时间戳是**这一块第一个采样点的服务器时刻**（snapserver 里是“采集时刻”）。客户端在 `时间戳 + 有效缓冲` 这个服务器时刻把它送到 DAC。snapserver 按实时节奏读源、编码、发送，块的时间戳由**累计的采样数**推出来，不是每块取一次当前时间。客户端对“将来的块”没有上限，只丢弃“太旧的块”（早于 `5 s + 缓冲`）。

### CodecHeader 负载

| codec | 负载内容 | 来源 |
|---|---|---|
| `flac` | libFLAC 编码器在第一帧音频之前输出的全部字节：`fLaC` 标记 + STREAMINFO + VORBIS_COMMENT（TITLE=SnapStream、VERSION）+ 1234 字节 PADDING；客户端只从 STREAMINFO 取采样率、位深、声道数 | `server/encoder/flac_encoder.cpp:46,152-161,223-236`，`client/decoder/flac_decoder.cpp:112-131,210-219` |
| `pcm` | 44 字节 RIFF/WAVE 头（fmt 块：PCM=1、声道、采样率、字节率、块对齐、位深；data 长度 0）；客户端按块遍历，只要 fmt | `server/encoder/pcm_encoder.cpp:65-84`，`client/decoder/pcm_decoder.cpp:78-131` |
| `opus` | 12 字节假头：`0x4F505553`、uint32 采样率、uint16 位深、uint16 声道（小端）；只支持 48 kHz | `doc/binary_protocol.md:86`，`doc/configuration.md:25` |
| `ogg` | Vorbis 流头 | `doc/binary_protocol.md:84` |
| `null` | 空；客户端按 48000:16:2 建播放器，不出声 | `server/encoder/null_encoder.cpp:34-49`，`client/decoder/null_decoder.cpp:37-47` |

PCM 的样本布局：24 bit 每个样本占 4 字节（低 3 字节有效），8/16/32 bit 按 1/2/4 字节，全部小端交错（`common/sample_format.cpp:76-84`）。

### WireChunk 的时间戳与节奏（snapserver）

| 项目 | 行为 | 来源 |
|---|---|---|
| 读源节奏 | 每次读 `chunk_ms`（默认 20 ms）的 PCM，读完用定时器等到下一个 tick，即按实时节奏 | `server/streamreader/asio_stream.hpp:186-271`，`server/server_settings.hpp:231` |
| 起点 | 连接源后第一次读完时，起点 = 当前时间 − 一块的时长 | `server/streamreader/asio_stream.hpp:244-249` |
| 递推 | 每输出一块编码数据，时间戳 = 当前起点，然后起点加上**这块实际编码的采样数对应的时长**（纳秒） | `server/streamreader/pcm_stream.cpp:335-355` |
| 源断流 | 读得比预期慢就记一次 resync，把起点重设为“现在”（时间轴向前跳） | `server/streamreader/asio_stream.hpp:272-278` |
| 空块 | 编码器这次没有输出完整帧（duration ≤ 0）就不发 | `server/streamreader/pcm_stream.cpp:340-341` |
| FLAC 块大小 | 压缩级别 0–2 是 1152 帧（48 kHz 下约 26 ms），3–8 是 4096 帧；默认级别 2。每条 WireChunk 装上一次输入后编码器吐出的整数个 FLAC 帧，时长不固定 | `server/encoder/flac_encoder.cpp:73-76,85-148,210-215` |
| 服务器发送队列 | 尚未开始发送、且时间戳早于 `bufferMs + 100 ms` 的块会被丢掉 | `server/stream_session.cpp:85-111` |

客户端解码 FLAC 时，如果 libFLAC 用到了上一块留下的数据，会按这部分采样数把时间戳往前挪（`client/decoder/flac_decoder.cpp:100-107`），所以 FLAC 帧跨块也能对上时间，不过整帧装块最简单。

### 缓冲与延迟参数

| 参数 | 含义 | 默认 | 来源 |
|---|---|---|---|
| `bufferMs`（ServerSettings） | 端到端延迟：服务器时间戳到出声的总时长 | snapserver 1000；消息类缺省 0 | `server/server_settings.hpp:224-225`，`common/message/server_settings.hpp:35,44-48`，`doc/configuration.md:28` |
| `latency`（ServerSettings） | 服务器给这台客户端配的额外延迟（ms，JSON-RPC `Client.SetLatency`） | 0 | `common/message/server_settings.hpp:50-54` |
| `--latency`（snapclient 命令行） | 本机 PCM 设备的额外延迟（ms） | 0 | `client/snapclient.cpp:193`，`client/client_settings.hpp:99-100` |
| 客户端有效缓冲 | `max(0, bufferMs − latency − --latency)` | — | `client/controller.cpp:226,259` |
| Stream 初始缓冲 | 收到设置之前用 500 ms | — | `client/stream.cpp:49` |

所以采样点在服务器时刻 `ts + bufferMs − latency − --latency` 到达 DAC。两个 latency 让这台设备**提前**出声，用来补偿 DAC 之后的延迟（功放、蓝牙等）。

### 提前发送

- 客户端入队时只丢弃“年龄 > 5 s + 缓冲”的块（`client/stream.cpp:109-134`），队列不限长度（`client/stream.hpp:113`）。
- 硬同步时，队首块还没到播放时刻就输出静音等待（`client/stream.cpp:302-315`）。
- 因此服务器可以按实时节奏发送（snapserver 的做法，客户端队列里大约积着 bufferMs 的数据），也可以提前发送；只要时间戳在同一条服务器时间轴上，客户端都会在正确时刻出声。

## 6. 客户端播放与校正

**结论**：每次声卡要数据时，客户端算 `年龄 = serverNow − 当前块位置的时间戳 − 有效缓冲 + 声卡缓冲里待播的时长`。年龄为 0 表示刚好。偏差超过阈值就**硬同步**：太早补静音，太晚丢块或在块内跳过。偏差较小时**软同步**：调整“每多少帧删或复制一个采样点”，最大 ±500 ppm；死区 100 µs。README 写的典型偏差低于 0.2 ms。

### 声卡延迟的计入

| 播放器 | 送给 Stream 的“DAC 时间” | 来源 |
|---|---|---|
| ALSA | `snd_pcm_avail_delay`（失败时退回 `snd_pcm_avail` + `snd_pcm_delay`）的 delay 帧数换算成 µs；默认 buffer_time 80 ms、4 个周期 | `client/player/alsa_player.cpp:42-44,539-563,612-642` |
| Pulse | `pa_stream_get_latency` | `client/player/pulse_player.cpp:344-350` |
| file | 固定 10 ms | `client/player/file_player.cpp:106` |

DAC 时间大于有效缓冲时，这一轮直接放静音（`client/stream.cpp:264-269`）。

### 硬同步（`hard_sync_` 为真时，`client/stream.cpp:302-367`）

- 年龄 < −本次请求时长：整段输出静音。
- 年龄 > 0：丢掉过期块，落在某块中间就在块内 seek 跳过，让年龄回到 0。
- −请求时长 ≤ 年龄 ≤ 0：先补 `−年龄` 对应的静音帧，再接上音频，按帧对齐。完成后退出硬同步并清空统计窗口。
- 首次播放、新 CodecHeader 之后（Stream 是新建的）、取数抛异常（如块读完而队列空）之后都会进入硬同步（`client/stream.cpp:50,458-463`）。

### 进入硬同步的条件（`client/stream.cpp:387-407`）

窗口按“每次声卡回调”记一个年龄样本：`buffer_` 500 个、`shortBuffer_` 100 个、`miniBuffer_` 20 个（`client/stream.cpp:52-54`）。ALSA 默认周期约 20 ms，大约对应 10 s、2 s、0.4 s。`median_` 和 `shortMedian_` 每秒更新一次（`client/stream.cpp:436-441`）。

| 条件（另外都要求当前 \|年龄\| > 500 µs） | 阈值 |
|---|---|
| 长窗口满且 \|中位数\| | > 2 ms |
| 短窗口满且 \|中位数\| | > 5 ms |
| 迷你窗口满且 \|中位数\| | > 50 ms |
| 单次 \|年龄\|（不另加条件） | > 500 ms |

### 软同步（`client/stream.cpp:44,370-431,166-243,76-87`）

- 条件：短窗口满，短窗口中位数 > 100 µs、迷你窗口中位数 > 50 µs、当前年龄 > 50 µs（太晚）；负方向对称（太早）。
- 速率偏移 = `短中位数(µs)/100 × 50 ppm`，上限 500 ppm。实现上换算成“每 X 帧修正 1 帧”：太晚就少读（删点），太早就重复读（复制点）。每次回调都先把速率复位为标称值，满足条件时再设。
- 一次回调里要修正 n 帧时，把输出分成 n+1 段，每段边界删除或复制一帧，修正点分散开。
- 精度量级（推断）：稳态误差主要来自 100 µs 死区、对时中位数的偏差（局域网 RTT 不对称，通常是几十 µs）和驱动报告 delay 的精度；48 kHz 下一个采样点约 20.8 µs（`README.md:44-47`）。

### 音量与静音

在 Stream 输出之后、写声卡之前按缓冲区整体处理（软件混音），立即生效，不按时间戳对齐。默认音量曲线是指数，底数 10（`client/player/player.cpp:160-235`）。混音器有 `software`（默认）、`hardware`、`script`、`none` 四种（`client/snapclient.cpp:209-222`）。硬件混音器的音量在本机被改动时，客户端发 ClientInfo（`client/controller.cpp:300-320`）。

## 7. 控制

**结论**：音量、静音、bufferMs、latency 都靠**重发一条完整的 ServerSettings** 下发，客户端每次都会重设音量和有效缓冲。协议里**没有**暂停、跳转、清空缓冲的消息。snapserver 自己也不处理暂停：源停了就不发块，客户端把缓冲里约 bufferMs 的音频放完，然后放静音；5 s 收不到块就关掉 ALSA。**唯一能立刻清空客户端缓冲的办法是再发一条 CodecHeader**：客户端会销毁并重建解码器、Stream 和播放器，不断开连接。格式变化也用这个办法。

| 场景 | 客户端行为 | 来源 |
|---|---|---|
| 收到 ServerSettings（任意时刻） | 设置音量和静音；有效缓冲改为 `max(0, bufferMs − latency − --latency)` | `client/controller.cpp:218-228` |
| snapserver 何时重发 ServerSettings | 客户端音量、静音、延迟改了，或组静音改了 | `server/control_requests.cpp:171-186,410-431` |
| 收到新 CodecHeader | 立即销毁解码器、Stream（连同队列）和播放器（ALSA 会关闭再打开），按新头重建，然后进入硬同步；连接保持 | `client/controller.cpp:229-327` |
| snapserver 何时重发 CodecHeader | 组换流（`Group.SetStream`）或客户端被移到另一组（`Group.SetClients`）时，先发新流的头再切换会话的流 | `server/control_requests.cpp:448-480,499-557` |
| 不认识的 codec，或头里解析不出采样格式 | 抛异常，异常离开 io_context，**snapclient 进程退出** | `client/controller.cpp:252-253`，`client/decoder/flac_decoder.cpp:127-128`，`client/snapclient.cpp:592-603` |
| 断流（不再收到块） | 读完队列后抛异常进入硬同步，放静音；ALSA 5 s 收不到块就关闭设备并清空队列，来块时重新打开 | `client/stream.cpp:149-163,458-463`，`client/player/alsa_player.cpp:657-672,575-593` |
| 断流后时间戳向前跳 | 硬同步：新块在将来就补静音等待，块已过期就丢 | `client/stream.cpp:302-367` |
| 时间戳回退（与已入队的块重叠） | 队列先进先出，后到的块会因“过期”被丢掉，不会覆盖已入队的块 | `client/stream.cpp:109-134,318-340`（推断） |
| 静音的客户端 | snapserver 默认不给静音的客户端或组发音频块（`sendAudioToMutedClients=false`） | `server/stream_server.cpp:86-111`，`server/server_settings.hpp:233` |

对 ROOMusic 的含义（由上面推出）：暂停和跳转要么接受约 bufferMs 的延迟（停发，或者跳转后发新时间轴的块），要么发新 CodecHeader 立刻清空缓冲。后一种会重开 ALSA，也就是一次设备打开的开销。暂停时也可以先用 ServerSettings 静音立刻止声，再让缓冲放完。换曲时采样率或位深变了，必须发新 CodecHeader；格式不变时可以无缝接着发。

## 8. 多台

**结论**：snapserver 把每个编码好的块**只序列化一次**，把同一份字节（包括公共头里的 `sent`）发给订阅这个流的所有会话；同一组的客户端收到完全相同的 CodecHeader 和块。每台客户端只有 ServerSettings（音量、静音、latency）不同。协议本身不要求字节相同，客户端只看时间戳和解码出来的 PCM，同步靠“同一条服务器时间轴 + 各自对时”。

| 项目 | 行为 | 来源 |
|---|---|---|
| 广播 | 一块序列化一次，遍历会话；只发给流匹配的会话（没挂流的会话收默认流） | `server/stream_server.cpp:72-112` |
| 共享缓冲 | `shared_const_buffer` 包着同一份 `vector<char>`；`sent` 在构造时填一次 | `server/stream_session.hpp:59-132` |
| 每客户端差异 | ServerSettings（音量、静音、latency；bufferMs 全局相同）；静音的客户端不收块 | `server/server.cpp:367-374`，`server/stream_server.cpp:88-105` |
| 中途加入 | 新会话拿到流编码器初始化时生成的同一份 CodecHeader，然后从下一块开始收；FLAC 帧各自独立，可以从任意一帧开始解码 | `server/server.cpp:401-404`，`server/streamreader/pcm_stream.cpp:106-109` |
| 每会话发送队列 | 各会话各自排队，各自丢弃过期未发的块 | `server/stream_session.cpp:85-111` |

推断：同组设备用不同的编码或采样率在协议上也行，只要时间戳描述的是同一时刻的内容。但 snapserver 的模型是一组对应一个流、一个编码器。

## 9. 断线与重连

**结论**：协议没有专门的心跳。客户端每秒一次的 Time 请求起心跳作用：2 s 收不到回应就断开，1 s 后重连。snapserver 只在读写出错时才发现断线，没有应用层超时，只用 Time 消息刷新 `lastSeen`。

| 项目 | 行为 | 来源 |
|---|---|---|
| 客户端 Time 超时 | 2 s，超时就 `reconnect()` | `client/controller.cpp:346-354` |
| Hello 超时 | 2 s，超时就重连 | `client/controller.cpp:501-508` |
| 读写出错 | 重连 | `client/controller.cpp:188-193,310-317` |
| 重连过程 | 取消定时器、断开、销毁播放器、Stream 和解码器，1 s 后重新连接（地址不变，不重新查 mDNS），再走一遍 Hello | `client/controller.cpp:464-480` |
| 重连后的对时 | 偏移窗口保留（不到 60 s 不清空），见第 4 节 | `client/time_provider.cpp:49-60` |
| 服务器端 | 设了 5 s 的 `SO_RCVTIMEO/SO_SNDTIMEO`（对 asio 异步读写一般不起作用），开了 `TCP_NODELAY`；读写出错时移除会话 | `server/stream_server.cpp:198-220`，`server/stream_session.cpp:72-76`，`server/stream_session_tcp.cpp:93-97` |
| 服务器端在线状态 | Hello 和每次 Time 刷新 `lastSeen`/`connected` | `server/server.cpp:282-288,385-386` |
| 同一客户端重复连接 | 按 clientId 查会话时返回第一个匹配的；断开时按会话指针移除 | `server/stream_server.cpp:131-180` |

## 10. 高码率

**结论**：协议和 snapclient 都没有写死采样率上限。实际上限来自：单条消息不超过 1 000 000 字节、libFLAC 的能力（32 bit FLAC 需要 libFLAC ≥ 1.4，属于推断）、ALSA 格式（只有 S8/S16_LE/S24_LE（4 字节容器）/S32_LE，S24_LE 不支持时退回 S32_LE）、声卡能否设到这个采样率（`set_rate_near` 设不上只打一条警告，然后按实际采样率继续播，结果会变调）。**Hello 不报告设备能力**，服务器没法通过协议得知设备支持什么采样率或位深。

| 项目 | 内容 | 来源 |
|---|---|---|
| 消息大小 | 正文 ≤ 1 000 000 字节（例：384 kHz/32 bit/2 声道 PCM 的 20 ms 约 61 KB，在限制内） | `common/message/message.hpp:164-165` |
| 样本容器 | 24 bit 用 4 字节 | `common/sample_format.cpp:76-84` |
| ALSA 格式 | 8/16/24(4 字节)/32；S24_LE 设不上时退回 S32_LE，并把音量乘以 256 补偿 | `client/player/alsa_player.cpp:331-368` |
| ALSA 采样率 | `snd_pcm_hw_params_set_rate_near`，与请求值不同时只打警告 | `client/player/alsa_player.cpp:374-377` |
| 客户端重采样 | `--sampleformat <rate>:<bits>:<ch>` 由客户端自己配置，位深只能是 16/24/32 | `client/snapclient.cpp:195,476-482`，`client/stream.cpp:57-72` |
| FLAC 块大小 | 级别 0–2 为 1152 帧，3–8 为 4096 帧（96 kHz 下分别约 12 ms、43 ms）；服务器可以自己选块大小 | `server/encoder/flac_encoder.cpp:210-218` |
| FLAC 输出 | 客户端把解码结果按 1/2/4 字节容器写出 | `client/decoder/flac_decoder.cpp:163-207` |
| Opus | 只支持 48 kHz | `doc/configuration.md:25` |

## 11. 版本兼容

**结论**：Hello 里有 `SnapStreamProtocolVersion`，当前固定为 2，没有协商机制，snapserver 只记录不判断。手头只有最新源码的浅克隆，没法比较旧版源码。从 changelog 和默认值来看，核心消息（Base、CodecHeader、WireChunk、ServerSettings、Time、Hello）长期没有变化。服务端按缺省值处理 Hello 的可选键（ID、Instance、Auth、协议版本），不主动发 Error，就能兼容各发行版的版本。

| 发行版 | snapclient 版本（2026-11-25 查询） |
|---|---|
| Debian bookworm / bookworm-backports | 0.26.0 / 0.28.0、0.30.0 |
| Debian trixie | 0.31.0 |
| Debian forky / sid | 0.34.0 |
| Debian bullseye / buster | 0.23.0 / 0.15.0 |
| Ubuntu jammy / noble / questing / resolute | 0.25.0 / 0.27.0 / 0.31.0 / 0.34.0 |
| Alpine 3.20 / 3.21 / 3.22 / edge | 0.28.0 / 0.29.0 / 0.31.0 / 0.35.0 |

来源：`https://sources.debian.org/api/src/snapcast/`、Launchpad API `getPublishedSources`、`https://pkgs.alpinelinux.org/packages?name=snapcast-client`。Home Assistant 插件的版本**没有查证**，各插件做法不同，常见的是基于 Alpine 包或自己编译。

从源码与 changelog 能看出的差异：

| 项目 | 说明 | 来源 |
|---|---|---|
| Error（类型 8）与 Auth | 新版才有。旧客户端收到类型号大于自己 kLast 的消息会断开，所以服务端只在鉴权失败时才发 Error | `client/client_connection.cpp:366-372`，`server/server.cpp:319-351` |
| WebSocket 客户端 | 0.32 起客户端支持 `ws://` / `wss://`（走 `/stream`）；服务器端 WebSocket 流从 0.21 开始 | `changelog.md:175,463` |
| `-h/-p` | 0.32 起不推荐使用，改用 URI；旧版只有 `-h/-p` | `changelog.md:189` |
| 时钟 | 0.19 起“系统时间改了也不影响播放”（客户端改用单调时钟）；服务端照样只回 Time，不受影响 | `changelog.md:543` |
| 快速初始对时 | 0.19 起有“更快的初始同步” | `changelog.md:541` |
| 默认采样率 | 0.5.0-beta-1（2016）起服务器默认改为 48 kHz（与客户端无关）；同一版本精简了协议消息（去掉 SampleFormat、Command、Ack、String），此后消息集合基本稳定 | `changelog.md:940,963-966` |
| 缺键默认值 | Instance=1、ID=MAC、协议版本=1 | `common/message/hello.hpp:135-150` |

## 12. 测试：Docker 里没有声卡时

**结论**：snapclient 一定带 `file` 播放器（不依赖编译开关），用 `--player file:filename=<stdout|stderr|null|路径>[,mode=w|a]` 选用；必须显式指定，否则默认用 ALSA。file 播放器用 `steady_clock` 定时器，每 50 ms 写一次 50 ms 的帧数，算是**按实时节奏消耗**；它声称的 DAC 延迟固定为 10 ms，没有静音时也照样写静音。它的“声卡时钟”就是本机单调时钟，适合测服务端时间戳和对时是否正确（客户端决定的对齐位置），测不了真实 DAC 和驱动 delay 的误差。

| 方式 | 节奏 | 说明 | 来源 |
|---|---|---|---|
| `--player file:filename=stdout` | 实时（50 ms 一块，fflush） | 日志默认也写 stdout，要配 `--logsink stderr` 或写文件 | `client/player/file_player.cpp:55-139`，`client/snapclient.cpp:232,323-345,489-495` |
| `--player file:filename=/path` | 实时 | 写原始交错 PCM（不带 WAV 头），格式等于流格式或 `--sampleformat` | 同上 |
| `--player file:filename=null` | 实时 | 不输出，只跑同步逻辑和日志 | `client/player/file_player.cpp:71` |
| ALSA + `snd-aloop` | 声卡时钟（虚拟） | 宿主机要加载内核模块，属于系统改动，需要授权；容器要挂 `/dev/snd` | 推断 |
| Pulse / PipeWire null sink | 声音服务器时钟 | 要看发行版的 snapclient 是否带 pulse 或 pipewire（用 `snapclient --player ?` 或 `-l` 查看） | `client/controller.cpp:151-181` |

file 播放器的细节（`client/player/file_player.cpp:99-139`）：每次回调请求 `50 ms × 采样率` 帧，按“本次回调时刻 + 10 ms”对齐内容；下一次回调时间是固定的 `next_request_ += 50 ms`，落后时重设为“现在 + 1 ms”（这时样本数和时间的对应关系就断了，会触发硬同步）。所以文件里第 i 个采样点大致对应“第一次回调时刻 + 10 ms + i/采样率”的服务器时间，定时器抖动会被软同步的中位数平滑掉。

日志里每秒有一行 `Stats`（DEBUG 级）：当前年龄、迷你/短/长窗口中位数（单位 0.1 ms）、窗口大小、DAC 时间、这一秒的修正帧数（`client/stream.cpp:436-444`），可以直接拿来看客户端自己估计的偏差。

---

## 服务端实现要点清单（按开发顺序）

1. **帧编解码**：26 字节小端公共头加 8 种正文（第 2 节表）；收到正文超过 1 MB 或类型号大于 8 就断开；JSON 正文前有 uint32 长度、不带 NUL。时间字段统一用 int32 秒 + int32 微秒（微秒归一到 0–999999）。
2. **服务器时钟**：用主机 `CLOCK_MONOTONIC`（Go 可以用 `golang.org/x/sys/unix.ClockGettime`，`backend/go.mod:14` 已经依赖 x/sys），保证 ROOMusic 重启后时间轴连续（第 4 节的 60 s 窗口问题）。
3. **TCP 监听 1704**：只接受局域网 IPv4（N6），设置 `TCP_NODELAY`（Go 默认开启）。
4. **Hello 处理**：解析 JSON，缺键时用默认值（Instance=1、ID=MAC、协议版本=1）。设备标识 = `ID`（+`#instance`）。回 ServerSettings 时 `refersTo = Hello.id`，然后发 CodecHeader，顺序不能反。需要拒绝时可以发 Error（类型 8）后关闭，但旧客户端不认识类型 8。
5. **Time 回应**：在读完正文时记接收时间；回应 `refersTo = 请求 id`、`latency = 接收时间 − 请求.sent`；回应的 `sent` 在**真正写到 socket 之前**才填，Time 回应排在待发音频块前面，减少回程偏差。
6. **mDNS 公告**：发布 `_snapcast._tcp`，端口 1704，不需要 TXT。客户端取第一个结果，要考虑和真正的 snapserver 共存（第 1 节）。
7. **FLAC 编码与 CodecHeader**：头负载 = `fLaC` + STREAMINFO（可以只有这一个元数据块，客户端只读 STREAMINFO），之后每条 WireChunk 放整数个 FLAC 帧。块大小服务器自定（1152 或 4096 都行，或按采样率换算成固定毫秒数）。不要发 codec 名不对或解析不出 STREAMINFO 的头，否则 snapclient 进程会退出。
8. **时间戳**：每个播放会话固定一个起点 `T0`（服务器时钟），块时间戳 = `T0 + 已发采样数 / 采样率`，按采样数累加，不要每块取当前时间。出声时刻 = 时间戳 + 有效缓冲。
9. **发送节奏与 bufferMs**：可以照 snapserver 按实时节奏发送（时间戳≈现在，bufferMs 决定端到端延迟），也可以提前发（客户端不限将来的块，只丢早于 `5 s + 缓冲` 的旧块）。bufferMs 要大于声卡缓冲（ALSA 默认 80 ms），再留出网络和解码的余量。
10. **多台**：同一组用同一个编码器输出，把同一份块字节发给组内所有会话；每台的音量、静音、latency 通过各自的 ServerSettings 下发。中途加入的设备发当前格式的 CodecHeader，然后从下一块开始。
11. **暂停、跳转、换曲**：协议里没有这几个命令。要立即生效就发新 CodecHeader 清空客户端缓冲（客户端会重开声卡），然后按新的 `T0` 发块；采样率或位深变了也必须发新 CodecHeader。只是暂停的话，也可以用 ServerSettings 静音立刻止声，再停发。
12. **音量和静音**：重发完整的 ServerSettings（四个键都带上，客户端每次都会用 bufferMs 重算缓冲）。收 ClientInfo（硬件混音器在本机被改动）以后回写音量状态。
13. **断线**：每秒的 Time 就是心跳；服务端自己加一个应用层超时（例如几秒收不到 Time 就关会话），因为 snapserver 没有，单靠 TCP 读错误发现断线可能很慢。客户端 1 s 后自动重连并重新 Hello；一台掉线不影响组里其他会话的发送。
14. **采样率和位深上限**：Hello 不报告设备能力，只能由 ROOMusic 按设备配置（默认值加用户可改的上限）；ALSA 设不上采样率时客户端会变调播放，不会报错。

## 同步测试方法建议

1. **基本连通**：Docker 测试网络里跑官方 snapclient，参数用 `tcp://<ROOMusic IP>:1704 --player file:filename=/out/a.raw --logsink stderr --logfilter *:debug`。根据日志检查 Hello、ServerSettings、Codec、`diff to server [ms]`，以及每秒 `Stats` 里的年龄和中位数。mDNS 发现单独测：容器里要有 avahi-daemon，网络也要能传组播。
2. **对齐位置测量（主要测服务端）**：两台 snapclient 跑在同一宿主机的两个容器里（共用 `CLOCK_MONOTONIC`），都用 `filename=stdout`，输出接到测量程序。测量程序在读到每个 50 ms 块时记下单调时间，在合成脉冲（如每秒一个单采样脉冲）的块内找到偏移 j，估计出声时刻 = 块到达时刻 + 10 ms + j/采样率，再比较两台的差。它能验证时间戳、对时和缓冲参数，误差下限是 file 播放器定时器和管道的抖动，需要实测。
3. **更接近真实声卡**：宿主机加载 `snd-aloop` 以后，容器用 ALSA 回环设备输出，在回环的另一端用同一时钟采集，测的是包含 ALSA delay 报告在内的偏差。要改系统，需要授权。
4. **100 ppm 漂移**：同一宿主机上的客户端没有真实的时钟漂移。有两种模拟方式：其一，ROOMusic 测试模式给一台会话用按 1.0001 缩放的“服务器时钟”，Time 回应和块时间戳都用这个时钟，相当于服务器时钟相对这台客户端漂移 100 ppm，会触发客户端的软同步；其二，客户端容器用 libfaketime 调速，这种方式和 asio 定时器配合时行为不确定，要先验证。
5. **控制场景**：暂停、继续、跳转、换曲（包括采样率变化）各做一次，检查两台在 `Stats` 回到稳态的时间和重新对齐后的偏差；断开一台的网络，确认另一台的输出不中断，被断开的那台 1 s 后重连，并在重新硬同步后回到同一时间轴。

## 相关规范与文档

- `.trellis/tasks/11-25-snapcast-multiroom/prd.md`：本任务 PRD（N1–N6）
- `.trellis/tasks/11-25-slimproto-sync-groups/research/sync-algorithm.md`：SlimProto 编组同步算法，计时程序计划共用

## 注意事项 / 未找到

- 源码是浅克隆，只有一个提交，没有 git 历史，无法逐版本比较协议；第 11 节的版本差异只来自 changelog 和缺省值处理。
- Home Assistant 插件里的 snapclient 版本没有查证。
- 32 bit FLAC 要求的 libFLAC 版本、ALSA `null` 插件是否按实时节奏消耗数据，都不是从本源码得出的，标为推断，需要验证。
- 精度量级（几十到一百多 µs）是根据死区、对时方法和 README 推断的，不是实测结果。
- 本文没有读 WebSocket 流会话（`server/stream_session_ws.cpp`、`client/client_connection.cpp` 的 WS 部分）和 Ogg/Opus 编码器的细节；PRD 只要求 TCP + FLAC。
