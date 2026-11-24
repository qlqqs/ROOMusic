# Research: Chromecast 与 AirPlay 作为服务端托管输出的可行性

- **Query**：ROOMusic Go 服务（AGPL-3.0-only，CGO_ENABLED=0 静态二进制，Alpine 容器，`network_mode: host`）能否像现有 UPnP/DLNA 渲染器那样，把 Google Cast 设备和 AirPlay 接收端作为服务端托管的输出设备（发现、每设备流地址、播放控制、音量、占用）。
- **Scope**：mixed（仓库现状 + 外部文档、开源实现、问题追踪）
- **Date**：2026-11-24

## 0. 仓库现状（对照基线）

| 文件 | 说明 |
|---|---|
| `.trellis/spec/backend/upnp-device-contracts.md` | UPnP 合同：SSDP 发现、出站校验、AVTransport/RenderingControl、`/upnp/streams/{token}` 免 Cookie 拉流、占用与接管、每秒轮询 |
| `backend/internal/devices/upnp/*.go` | 设备层，非测试代码约 2767 行（含 `httpapi/`） |
| `backend/internal/zones/managed_devices.go:12` | `ManagedOutputs` 接口（`VisibleDevices`/`Acquire`/`ReleaseIdle`） |
| `backend/internal/zones/managed_devices.go:51` | `visible()` 里设备种类写死为 `"upnp"`；`zones/*.go` 非测试代码中 `"upnp"` 字面量出现 14 处 |
| `backend/internal/zones/upnp_stream.go` | `UPnPStream(userID, sessionID, seq)` 产出拉流路径 |
| `backend/internal/playback/render/encode.go:24-63` | 渲染编码器：`wav`（内置头）、`flac`、`alac`、`opus`（Ogg）、`aac`（`stream` 交付为 ADTS，HLS 为 fMP4），经 ffmpeg 子进程；**没有 MP3 编码分支** |
| `backend/internal/playback/render/session.go:500-535` | `waitToRender`：长流最多领先播放头 20 秒（按实时节奏供流，不是一次性灌满） |
| `deploy/production/Dockerfile:90`、`compose.prod.yaml:15,46-47` | 容器 `USER 1000:1000`，`cap_drop: [ALL]`，`no-new-privileges`，`network_mode: host` |
| `CONTRIBUTING.md:12-30` | 贡献者许可协议：维护者保留以 AGPL 以外条款（含商业许可）分发的权利 |
| `.trellis/tasks/archive/2026-11/11-24-m8-devices-artists-plugins/prd.md:19` | 上一轮决定 U3 只做 UPnP，“Chromecast、AirPlay 没有官方开放接口” |
| `.trellis/tasks/archive/2026-11/11-08-scan-reference-port/research/reference-design.md:5180-5320` | 早先整理的 AirPlay TXT 键、Cast 能力字段、四协议能力矩阵，可直接复用 |

UPnP 那次合入（`73012804`）共 117 个文件、+7272/−104 行（含测试、前端、迁移、样本），作为下文“工作量倍数”的基准。

仓库里没有任何 mDNS/DNS-SD 代码（`grep -i mdns|zeroconf|5353` 在 `backend/` 无命中），两种协议都要新增 mDNS 发现。

---

## 1. Google Cast / Chromecast

### 1.1 发现

- 服务类型 `_googlecast._tcp.local`（Music Assistant 的 Cast 提供器清单写的就是 `"mdns_discovery": ["_googlecast._tcp.local."]`，见 [manifest.json](https://github.com/music-assistant/server/blob/HEAD/music_assistant/providers/chromecast/manifest.json)）。TXT 记录里有 `id`（设备 UUID）、`fn`（名称）、`md`（型号）、`ca`（能力位）等，可以拿来当注册表主键和显示名。
- 控制端口通常是 TCP 8009。Cast 音箱组（多房间组）是单独的 mDNS 实例，型号为 “Google Cast Group”，端口不是 8009，由组长设备代答。
- Go 侧纯 Go 的 mDNS 库：`hashicorp/mdns`（MIT，2026-11 仍有提交）、`grandcat/zeroconf`（GitHub 许可识别为 NOASSERTION，需人工核对 LICENSE；最后推送 2023-12）。用临时端口发查询（RFC 6762 §6.7 的传统单播应答）就不用绑定 5353，也不会跟宿主机的 avahi 抢端口。

### 1.2 CASTV2 协议

来源：[thibauts/node-castv2 协议说明](https://github.com/thibauts/node-castv2#protocol-description)，以及 [OwnTone `src/outputs/cast.c`](https://github.com/owntone/owntone-server/blob/master/src/outputs/cast.c)。

- 连 TCP 8009 走 TLS。设备证书是自签名的，发送端不校验（`vishen/go-chromecast` 用的是 `tls.Config{InsecureSkipVerify: true}`，见 [cast/connection.go](https://github.com/vishen/go-chromecast/blob/master/cast/connection.go)）。Go 标准库 `crypto/tls` 就够，用不到 CGO。
- 帧格式：4 字节大端长度 + protobuf `CastMessage`（7 个字段：protocol_version、source_id、destination_id、namespace、payload_type、payload_utf8、payload_binary）。可以用仓库已有的间接依赖 `google.golang.org/protobuf`（`backend/go.mod`）生成代码，也可以用 `protowire` 手写编解码。
- 命名空间（payload 都是 JSON）：
  - `urn:x-cast:com.google.cast.tp.connection`：`CONNECT`/`CLOSE` 建立和关闭虚拟连接
  - `urn:x-cast:com.google.cast.tp.heartbeat`：`PING`/`PONG`，约每 5 秒一次，不发会被断开
  - `urn:x-cast:com.google.cast.receiver`：`LAUNCH`（appId）、`GET_STATUS`、`SET_VOLUME`（`level` 0–1、`muted`）、`STOP`；`RECEIVER_STATUS` 里带 `volume.controlType`（`attenuation`/`fixed`/`master`）和 `stepInterval`
  - `urn:x-cast:com.google.cast.media`：`LOAD`、`PLAY`、`PAUSE`、`STOP`、`SEEK`、`GET_STATUS`、`QUEUE_INSERT` 等；设备主动广播 `MEDIA_STATUS`，里面有 `playerState`（IDLE/PLAYING/BUFFERING/PAUSED）、`idleReason`（CANCELLED/INTERRUPTED/FINISHED/ERROR）和 `currentTime`（见 [Media Messages](https://developers.google.com/cast/docs/media/messages)）
- **设备认证**：`urn:x-cast:com.google.cast.tp.deviceauth` 是发送端验证接收端是不是正版 Cast 设备，“对发送端完全可选”。接收端不认证发送端，所以第三方发送端不需要任何 Google 凭据（来源同 node-castv2 README 的 Device authentication 一节）。

### 1.3 默认媒体接收端能否播长时间 HTTP 音频流

- **不用注册**：[Registration](https://developers.google.com/cast/docs/registration) 写明 “If you use the Default Media Receiver, you do not have to register your application… uses a provided constant as the application ID”。这个常量是 `CC1AD845`（MA 源码 `APP_MEDIA_RECEIVER = "CC1AD845"`，见 [constants.py](https://github.com/music-assistant/server/blob/HEAD/music_assistant/providers/chromecast/constants.py)）。Styled 或 Custom Receiver 才需要注册，开发者账号一次性 5 美元，未发布前只能在登记过的设备上测试。
- `LOAD` 的 `MediaInformation`：`contentId`（URL，最长 1k）、`contentType`（MIME）、`streamType`（`BUFFERED`/`LIVE`）、`metadata`（`metadataType: 3` 即 MusicTrackMediaMetadata，带 title/artist/albumName/images）、`duration`。
- **实际项目的做法**：Music Assistant 的 Cast 播放器对所有服务端实时编码的流一律用 `streamType: LIVE`，注释原话是 “MA streams are real-time encoded by FFmpeg, so they are not seekable and don't have a known content length”（[player.py `_create_cc_media_item`](https://github.com/music-assistant/server/blob/HEAD/music_assistant/providers/chromecast/player.py)）。它的流地址是局域网 `http://`，说明默认接收端可以拉局域网 HTTP 地址的无长度分块音频。
- CORS：官方文档说 CORS 主要影响自适应流（HLS/DASH 等）（[Supported Media](https://developers.google.com/cast/docs/media)）。直接用媒体元素播放的单文件渐进流，从 MA 的实践看不需要 CORS 头。这一点只有实践依据，官方没有逐条写明。
- **已知坑：快速灌数据会被截断**。MA issue [#6748](https://github.com/music-assistant/support/issues/6748)：所有 Cast 设备在一个流收到约 24 MiB 后就认为播完（FLAC 约 3–4 分钟，MP3 320k 约 10.5 分钟）。[#7065](https://github.com/music-assistant/support/issues/7065)：Nest Audio 播长 FLAC 流会周期性中断。MA 在 2.10.13 把多数 Cast 音箱的默认编码改成 MP3，常量注释写明 “several receivers (Samsung's Cast Lite speakers, Google Nest Mini and Nest Audio) stop playing a long lossless stream after a while”，只有 Chromecast Audio 和 Nest Hub 在“长时间无损无中断”白名单里（`CAST_LOSSLESS_MODELS`）。同帖有人指出按实时节奏供流、用 `LIVE` 类型的 WAV 桥接一直稳定。ROOMusic 的渲染本来就最多领先 20 秒（`session.go:500`），节奏上更接近后一种情况，但这个问题没有在 ROOMusic 上验证过。

### 1.4 编解码与采样率

- 官方 [Supported Media](https://developers.google.com/cast/docs/media) 的“Audio codecs”一节：Chromecast Audio、Google Home、Home Mini 支持 **FLAC（最高 96 kHz/24-bit）**、HE-AAC、LC-AAC、MP3、Opus、Vorbis、WAV（LPCM）、WebM。容器：MP2T、MP3、MP4、OGG、WAV、WebM。WAV 和其他编码没有写采样率上限。
- MA 的采样率配置（`constants.py`）：单设备上限 192k/24、组 96k/24，但**安全默认 48 kHz/16-bit**。注释原话 “originally/officially cast supports 96k sample rate… but it seems a (recent?) update broke this”。
- Cast 协议本身不声明采样率、位深和声道，只在 `GET_DEVICE_CAPABILITIES` 里有一个 `hi_res_audio_supported` 布尔（见仓库 `reference-design.md` 25.6.4 节）。
- 和 ROOMusic 编码器的对应：FLAC、WAV、Opus/Ogg、AAC ADTS 现成就有；MP3 还没有编码分支，生产镜像的 ffmpeg 是 `mwader/static-ffmpeg`，有没有 libmp3lame 没有核对。

### 1.5 无缝（gapless）

- 媒体命名空间支持 `QUEUE_INSERT`，`QueueItem.preloadTime` 只是“提示接收端提前预载，让条目之间过渡平滑”（[QueueItem 参考](https://developers.google.com/cast/docs/reference/web_receiver/cast.framework.messages.QueueItem)），官方没有承诺无缝。
- MA 默认对 Cast 开启 flow mode（整个队列一条连续流），注释原话 “cast devices handle a continuous flow stream more reliably than enqueueing individual tracks”。flow 模式下想中途更新曲目信息，MA 是用自有接收端应用 `C35B0678` 从 `customData` 读元数据。推断：默认接收端不处理这个 customData，所以用默认接收端加连续流时，屏幕上的元数据会停在第一首。
- ROOMusic 的 UPnP 路径是“每个 seq 一条流，设备 STOPPED 后推下一条 URI”，搬到 Cast 上就是 `idleReason=FINISHED` 后再 `LOAD`，或者提前 `QUEUE_INSERT`。两种做法曲间都可能有短暂空白。

### 1.6 另一条路：镜像应用（RTP）

OwnTone 不走默认媒体接收端，而是启动 Chrome 的音频镜像应用 `85CDB22F`（旧版 `0F5096E8`），在 `urn:x-cast:com.google.cast.webrtc` 命名空间里发 OFFER，再以 Cast RTP 推 Opus 48k/16/2。作者说优点是起播快，并且和 AirPlay 一样是推流，但 “I have not found much documentation for it, so the reference is Chromium itself”（`cast.c:58-63`）。OwnTone 文档还写了 “Playback to Google Nest Hub doesn't work” 和无法与 AirPlay 精确同步（[docs/audio-outputs/chromecast.md](https://github.com/owntone/owntone-server/blob/master/docs/audio-outputs/chromecast.md)）。这条路没有官方文档。

### 1.7 Go 库

| 库 | 许可 | 状态 | 说明 |
|---|---|---|---|
| [vishen/go-chromecast](https://github.com/vishen/go-chromecast) | Apache-2.0 | 983★，2026-11 仍有提交 | 主体是 CLI；`cast/` 子包约 12 KB（connection.go + payload.go + proto），依赖 `gogo/protobuf`、`logrus`、`buger/jsonparser`、`pkg/errors`；模块的 go.mod 还带着 Google TTS、gRPC 等大量依赖 |
| [barnybug/go-cast](https://github.com/barnybug/go-cast) | MIT | 169★，2026-07 合过一个 PR，此前停在 2024 | 依赖 2016 年的 `gogo/protobuf` 和 `hashicorp/mdns` |
| [ninjasphere/go-castv2](https://github.com/ninjasphere/go-castv2) | MIT | 2016 年起停更 | 只能参考 |
| [tristanpenman/go-cast](https://github.com/tristanpenman/go-cast) | MIT | 26★，2026-08 | 发送端和接收端都有 |
| [alexballas/go2tv](https://github.com/alexballas/go2tv) | MIT | 1156★，活跃 | 同时投 DLNA 和 Chromecast 的应用，可以参考它的媒体加载流程 |
| [pychromecast](https://github.com/home-assistant-libs/pychromecast) | MIT | 2710★，活跃 | MA 和 Home Assistant 用的 Python 实现，适合当行为参考 |

以上全部是纯 Go（或用作参考的 Python），许可都能并入 AGPL-3.0。Apache-2.0 和 MIT 不妨碍 CLA 里“另行商业授权”的选项。CASTV2 本身很小（一个 protobuf 消息加四个 JSON 命名空间），自己写一个最小客户端的代码量和引入 `cast/` 子包差不多。

### 1.8 网络与部署

- Cast 是拉流：设备从 ROOMusic 拉 HTTP，这一点和 UPnP 一样，现有的 `/upnp/streams/{token}`（私网对端校验、令牌、限流）可以原样沿用或换个前缀。
- 发送端只需要出站 TLS 到 8009 加 mDNS 查询，不用绑定特权端口，现有的 UID 1000 加 `cap_drop: ALL` 就能跑。

---

## 2. AirPlay

### 2.1 AirPlay 1（RAOP）和 AirPlay 2 的区别

| 方面 | AirPlay 1 / RAOP | AirPlay 2 |
|---|---|---|
| 发现 | `_raop._tcp`（实例名 `MAC@名称`），TXT 有 `cn`/`et`/`sr`/`ss`/`ch`/`pw`/`sf` 等 | `_airplay._tcp`，TXT 带 `features` 64 位特性位、`pk`、`gid`、`tsid`（立体声组）等；见 [Features 表](https://openairplay.github.io/airplay-spec/features.html) |
| 控制 | 明文 RTSP（ANNOUNCE 携带 SDP，SETUP，RECORD，SET_PARAMETER，FLUSH，TEARDOWN） | 配对后整条 RTSP 用 ChaCha20-Poly1305 加密；SETUP 改用二进制 plist（会话级 + 流级两次） |
| 认证 | 可选 RSA 加密 AES 密钥（`rsaaeskey`，固定的 AirPort 公钥）；密码接收端用 Digest；Apple TV（tvOS 10.2 起）需要 “legacy pairing” PIN 验证（[pyatv supported_features](https://github.com/postlund/pyatv/blob/master/docs/documentation/supported_features.md)） | HAP（HomeKit）配对：pair-setup（SRP-6a）与 pair-verify（X25519 + Ed25519）。**transient 配对**用固定 PIN `3939`，停在 M4、不落盘凭据；Apple TV 要屏幕 PIN 加长期凭据 |
| 音频 | ALAC，`a=fmtp:96 352 0 16 40 10 14 2 255 0 0 44100`，即每包 352 帧，**44.1 kHz/16-bit/2ch**（[ANNOUNCE](https://openairplay.github.io/airplay-spec/audio/rtsp_requests/announce.html)）；UDP RTP，另有控制和计时端口 | realtime（RTP 类型 96，UDP，ALAC，PTP 或 NTP 计时）；buffered（类型 103，RTP over TCP，`SETRATEANCHORTIME`）；音频用 ChaCha20-Poly1305 |
| 计时 | NTP 风格计时端口 | **PTP**（IEEE 1588 / gPTP 方言，UDP 319/320）或 NTP；多房间要求同一个 grandmaster，`SETPEERS` 下发组成员 |
| 多房间 | 发送端自己对齐 | 接收端按 PTP 时钟渲染，`groupUUID` 把成员拉进同一个组 |
| 规格来源 | 都没有官方公开规范，只有逆向得来的 [Unofficial AirPlay Specification](https://openairplay.github.io/airplay-spec/) | 同左 |

### 2.2 MFi 与 FairPlay：发送端要不要苹果授权

- 第三方开源发送端（OwnTone、Music Assistant 的 cliairplay、saundo、airplay2-sender-cpp）都**没有 MFi 授权**，靠的是 transient 配对或 PIN 配对。cliairplay README 原话：“**Sonos and most third-party AirPlay 2 receivers need no pairing step: the native flow uses transient pairing automatically**”；Apple TV 和 HomePod 要 PIN 时走完整的 HomeKit pair-setup（[music-assistant/airplay-cli](https://github.com/music-assistant/airplay-cli)）。
- cliairplay 另有 “AirPlay 2 RAOP-compat”：先 `auth-setup` 再走 RAOP 流程，用于“既不能 pair-verify 也不能 transient 配对”的 AirPlay 2 接收端。
- **FairPlay 是真正的硬墙**：特性位 14（Authentication4，FairPlay）、12（FPSAPv2pt5_AES_GCM）、51（MFi 认证）。saundo 原话：“Not for you… if your receiver requires Apple FairPlay — some TVs do. saundo detects that class of receiver and fails fast”（[4202217/saundo](https://github.com/4202217/saundo)）。有人逆向了发送端的 FairPlay SAP 握手（[objevovat/…-airplay2-sender-authentication-handshake](https://github.com/objevovat/fairplay-sap-core-airplay2-sender-authentication-handshake)，LGPL-3.0，2026-08 创建，3★），法律风险和成熟度都不适合采用。
- MFi 计划管的是接收端硬件厂商（音箱要过 MFi 才能打 AirPlay 2 标），不涉及发送端授权。这一条来自行业常识和上面这些项目的做法，没有找到苹果官方的明文出处（developer.apple.com 页面要 JS 渲染，没能抓取）。

### 2.3 设备兼容性：哪些只收 AirPlay 2，哪些还收 RAOP

| 设备 | 现状 | 来源 |
|---|---|---|
| Mac（macOS 的 AirPlay 接收器） | **不再接受 RAOP**，只能走 AirPlay 2 | MA AirPlay 文档：“Playback to Macbooks is not possible due to removal of RAOP support”（[music-assistant.io/player-support/airplay](https://www.music-assistant.io/player-support/airplay/)）；saundo 用 AirPlay 2 加密码配对验证通过 |
| HomePod / HomePod mini | 仍可用 RAOP 或 AirPlay 2 兼容模式；原生 AirPlay 2 **必须 PTP**（接受 NTP 会话但会静默忽略流 SETUP）；家庭 App 里设成“仅限本家庭成员”会挡住第三方发送端 | saundo [docs/devices.md](https://github.com/4202217/saundo/blob/HEAD/docs/devices.md)；OwnTone [#2061](https://github.com/owntone/owntone-server/issues/2061)（HomePod mini 2 / 27.2 只有 `airplay2_disable=true` 即 RAOP 才出声）、[#2063](https://github.com/owntone/owntone-server/issues/2063)（PTP 下无声）；MA [#6935](https://github.com/music-assistant/support/issues/6935)（PTP 无声，兼容模式和 RAOP 正常）；pyatv 验证过 HomePod mini 14.5 的 RAOP |
| HomePod OS 27（2026-09） | **按 User-Agent 版本拒绝**旧发送端：`GET /info` 返回 403，改发 `AirPlay/999.0.0` 才恢复 | OwnTone [#2042](https://github.com/owntone/owntone-server/issues/2042) |
| Apple TV | 要 PIN 配对；tvOS 15 起 MRP 并进了 AirPlay 2 | pyatv supported_features；MA 文档 |
| Sonos（Era 100 等新款） | 公告 `_airplay._tcp`，`features=0x445F8A00,0x1C340`；**RAOP 无效**，原生 AirPlay 2 **必须 PTP**。OwnTone 2026-02 合入 PTP 后才能用 | OwnTone [#1853](https://github.com/owntone/owntone-server/issues/1853) |
| WiiM | 仍支持 RAOP，也能走 AirPlay 2 + PTP | 同上 #1853 评论 |
| Samsung 电视/音箱 | MA 文档：“Samsung devices seem to have implemented AirPlay 1 in a way that isn't fully backwards compatible”，症状是一切正常但无声；部分电视要 FairPlay | MA 文档；saundo README |
| Yamaha | pyatv 验证过 Yamaha RX-V773（RAOP） | pyatv supported_features |
| Denon/Marantz（HEOS） | **没有找到**可靠的兼容性记录 | — |
| Bose 老款 AirPlay 1 设备、AirPort Express | RAOP 正常 | #1853；pyatv |

总的趋势：苹果和新一代第三方设备正在收紧到“AirPlay 2 + PTP”，RAOP 覆盖面逐年缩小。另外，不少第三方 AirPlay 音箱（Sonos、WiiM、Yamaha MusicCast、HEOS 等）同时提供 UPnP/DLNA 渲染器，已经能被现有 UPnP 输出覆盖；这一点**在本次调研里没有逐款核实**。

### 2.4 延迟、同步与采样率

- RAOP：固定 44.1 kHz/16-bit；cliairplay 起播最小提前量 200 ms。
- AirPlay 2 realtime：cliairplay 默认队列深度 600 ms（上限 3000 ms），并支持 **24-bit ALAC 44.1/48 kHz**；MA 文档写的是 “up to 48 kHz/24-bit on AirPlay 2 devices and 44.1 kHz/16-bit on AirPlay 1”，HomePod 默认关闭 24-bit，因为 Wi-Fi 下会噼啪。saundo 只发 44.1 kHz，实测 HomePod 渲染管线在 200 ms 以下会断续，生产固定 200 ms。
- HomePod 实时渲染端：流一停就清空抖动缓冲，长时间暂停后恢复会持续杂音，所以 saundo 暂停期间**一直发静音包**（每 8 ms 一包）。
- 别的发送端接入会直接把当前会话顶掉：控制连接被干净地关闭，不给任何接管通知（saundo devices.md “Session takeover”）。这对应 ROOMusic 的“设备端停止”语义。
- AirPlay 是**推流**：发送端按时间戳持续推 RTP，所以只要给一条连续 PCM，曲间天然无缝。cliairplay 的做法是整个进程生命周期只有一条持续 PCM 输入，跳曲和 seek 时用 `FLUSH` 再 `START` 重新定锚。

### 2.5 PTP 对 ROOMusic 部署的直接影响

- cliairplay README：“Only one process per host can bind the privileged PTP ports (UDP 319/320)… the usual cause [of silence] is UDP 319/320 not reaching this host from the speaker”。它需要 root 或 `CAP_NET_BIND_SERVICE`，并且提供共享时钟守护进程模式。
- ROOMusic 生产容器是 `USER 1000:1000` 加 `cap_drop: [ALL]` 加 `no-new-privileges`，又是 host 网络，**现状下绑不了 319/320**。要么给容器加 `NET_BIND_SERVICE`（非 root 用户还要配环境能力，与 `no-new-privileges` 有冲突），要么在宿主机调 `net.ipv4.ip_unprivileged_port_start`（host 网络下这是整机设置，属于改系统配置）。
- 同一台宿主机上已经在跑 shairport-sync/nqptp 或 MA 的 PTP 守护进程时会抢端口（MA 文档专门提醒 Shairport Sync 同机要切兼容模式）。
- RAOP 和 AirPlay 2 的 NTP 模式不需要特权端口，但需要一组 UDP 入站端口（控制、计时、事件），host 网络下可以直接用。

### 2.6 开源发送端实现

| 实现 | 语言 | 许可 | 成熟度 | 覆盖 |
|---|---|---|---|---|
| [OwnTone](https://github.com/owntone/owntone-server) | C | GPL-2.0-or-later（文件头 “version 2… or any later version”） | 2596★，十余年，2026-11 活跃 | RAOP（`raop.c` 约 124 KB）+ AirPlay 2（`airplay.c` 约 151 KB，4954 行）+ 配对库 `pair_ap/`（约 170 KB）+ PTP（2026-02 合入）+ Cast（`cast.c` 约 70 KB） |
| [music-assistant/airplay-cli](https://github.com/music-assistant/airplay-cli)（cliairplay） | C | GPL-3.0 | 2026-07 新建，21★；替代了 cliraop/cliap2，MA 正在用 | RAOP、AP2 兼容、AP2 原生（transient / pair-verify，realtime 加 buffered，PTP/NTP）、MediaRemote 元数据、共享 PTP 守护进程 |
| [pyatv](https://github.com/postlund/pyatv) | Python | MIT | 1216★，v0.18.0（2026-06） | **只有 RAOP 推流**；文档原话 “Only legacy pairing is supported for RAOP… encryption has not been implemented for HAP based authentication” |
| [philippe44/libraop](https://github.com/philippe44/libraop) | C | MIT | 124★，2026-10 | RAOP（AirConnect 一系） |
| [4202217/saundo](https://github.com/4202217/saundo) | **纯 Go** | **GPL-3.0-only** | 2026-11-01 新建，8★；作者有详细的实机验证记录 | AirPlay 2 realtime、PTP（follower 和 master）、transient/密码/PIN 配对、HomePod 立体声对；协议代码在 `internal/airplay/*`（约 500 KB 含测试），是 `internal` 包不能直接 import；依赖 `hashicorp/mdns`、`miekg/dns`、`x/crypto`、`purego`（仅桌面端用）；只发 44.1 kHz |
| [omarroth/doubletake](https://github.com/omarroth/doubletake) | Go + CGO（GStreamer、FDK-AAC） | LGPL-3.0-or-later | 172★，2026-04 新建 | 屏幕镜像和音频发送端；依赖 CGO，不符合静态二进制要求 |
| [akustikrausch/airplay2-sender-cpp](https://github.com/akustikrausch/airplay2-sender-cpp) | C++ | Apache-2.0 | 77★，2026-06 新建，用在商业产品 FXChainPlayer 里 | AP2 realtime ALAC，对 Apple TV / HomePod / macOS 验证过；README 列了一批“少一个头就无声”的细节 |
| [openairplay/ap2-sender](https://github.com/openairplay/ap2-sender) | — | 无许可 | 2020 年后停更 | 只能参考 |
| [brutella/hap](https://github.com/brutella/hap) | Go | Apache-2.0 | 427★ | HomeKit **配件端**的 SRP、pair-setup/verify、ChaCha20 实现，可以借鉴配对的密码学部分（角色相反） |

许可方面：GPL-2.0-or-later、GPL-3.0、LGPL-3.0 都能与 AGPL-3.0 合并（GPLv3/AGPLv3 第 13 条）。但这些代码一旦并入，`CONTRIBUTING.md` 里“维护者可以按 AGPL 以外条款（含商业许可）分发”的选项就**不再覆盖这部分代码**。MIT/Apache 来源（pyatv、libraop、airplay2-sender-cpp、brutella/hap）不受这个限制。

Go 生态里**没有成熟可直接 import 的 AirPlay 发送端库**。唯一的纯 Go AirPlay 2 发送端 saundo 才出现三周多，GPL-3.0-only，而且是 `internal` 包。

---

## 3. 首版范围、主要风险、相对 UPnP 的工作量

工作量以 UPnP 那次合入（+7272 行，含测试、前端、迁移、样本；设备层非测试代码约 2.8k 行）为 1×。下面是按协议面和参考实现体量做的粗估，不是承诺。

### 3.1 Chromecast

**首版范围（建议）**
- mDNS 发现 `_googlecast._tcp`，外加手动 IP（与 UPnP 的手动登记对称）；注册表主键用 TXT `id`；Cast 音箱组可以先当普通设备出现，也可以先不做。
- 自写最小 CASTV2 客户端：TLS 8009、CONNECT、心跳、`LAUNCH CC1AD845`，不注册应用。
- `LOAD`：URL 沿用令牌拉流路径，`streamType: LIVE`，带 MusicTrackMediaMetadata 和封面 URL。下一首在 `IDLE/FINISHED` 后 `LOAD`，与 UPnP 的“设备 STOPPED 后推下一条”同构。
- 格式：默认用有损（AAC ADTS 或 MP3；MP3 要新增编码分支）；FLAC 只对白名单型号开或让管理员手动开；采样率上限默认 48 kHz/16-bit。
- 状态：以 `MEDIA_STATUS` 推送为主，`GET_STATUS` 兜底；音量用接收端 `SET_VOLUME`，`controlType=fixed` 时按固定音量处理。
- 占用：`RECEIVER_STATUS` 里 appId 或 sessionId 变了，就当设备端停止或被接管。

**主要风险**
- 长无损流在 Nest Audio/Mini、三星 Cast 音箱上会中断，以及约 24 MiB 截断问题（MA #6748、#7065）。
- 默认接收端曲间不保证无缝；连续流模式下屏幕元数据不更新。
- 96 kHz 在新固件上不可靠（MA 注释）。
- 默认接收端行为由 Google 控制，可能随固件变化。
- zones 层把设备种类写死为 `"upnp"`，需要泛化。

**相对工作量**：约 0.6–0.9×。协议本身比 SOAP/SCPD 简单，状态是推送不用轮询，但 mDNS、TLS 长连接管理、事件驱动状态机都是新写的；授权、占用、前端设备列表这些可以沿用 UPnP 的模式。

### 3.2 AirPlay

**两档范围（可选）**
- **A 档（RAOP + AP2 兼容模式，纯 NTP）**：mDNS 发现 `_raop._tcp`/`_airplay._tcp` 并解析 TXT（已有 `reference-design.md` 25.6.3 可用）；RTSP 会话、ALAC 打包（可以发“未压缩 ALAC”帧，省掉编码器）、44.1/16 重采样、RTP 和重传、NTP 计时、RSA/AES、音量（−30…0 dB，静音 −144）、DMAP 元数据与封面、Apple TV legacy PIN 配对。覆盖老 AirPlay 设备、WiiM、Yamaha、AirPort Express，以及目前仍然收 RAOP 的 HomePod。**覆盖不到** Mac、Sonos Era 这类新 Sonos、要 FairPlay 的电视。
- **B 档（原生 AirPlay 2）**：在 A 档基础上加 HAP transient/PIN 配对（SRP-6a 3072、X25519、Ed25519、HKDF、ChaCha20-Poly1305）、加密 RTSP、二进制 plist（Go 标准库没有，要引库或自写）、事件通道、**PTP**（Sonos Era、HomePod 原生模式必需），可选 24-bit/48k。

**主要风险**
- 没有官方规范，接收端固件一更新就可能失效，例如 HomePod OS 27 的 User-Agent 拒绝、HomePod mini 2 的 PTP 无声，都是 2026 年 9–11 月才出现并在修的问题，维护是持续性的。
- PTP 要绑定特权 UDP 319/320，和现有容器的安全配置（非 root、`cap_drop: ALL`、`no-new-privileges`）冲突，还可能和同机 shairport-sync/nqptp 抢端口。
- 推流模型和 UPnP/Cast 的拉流模型不同：ROOMusic 要从渲染会话直接拿 PCM，自己做 RTP 打包和发送节奏，暂停时还要持续发静音（HomePod），不能复用 `/upnp/streams/{token}`。zones 层的“取流路径 + Delivered”合同要另加一种输出形态。
- FairPlay 设备做不了，要在发现阶段识别出来并标为不可用。
- 许可：可参考的成熟实现多是 GPL（OwnTone、cliairplay、saundo），直接移植会限制 CLA 的商业授权选项；MIT/Apache 来源只覆盖 RAOP（pyatv、libraop）或是 C++（airplay2-sender-cpp）。

**相对工作量**
- A 档：约 1.2–1.8×（新的推流管线、RTSP、RTP 重传、计时、ALAC 帧）。
- B 档：约 3–5×，另加长期维护。参照：OwnTone 的 AirPlay 2 加配对加 RAOP 约 445 KB C，外加独立的 PTP；saundo 的 `internal/airplay/*` 约 500 KB Go（含测试）。

---

## Caveats / Not Found

- Denon/Marantz（HEOS）、Yamaha 新款、Bose 新款对 RAOP 和 AirPlay 2 的现状没有找到可靠记录。
- “很多 AirPlay 第三方音箱同时支持 UPnP/DLNA”没有逐款核实。
- Cast 默认接收端对 WAV 流的采样率上限、对无 Content-Length 的 FLAC/WAV 的官方说明都没找到，只有 MA 的实践和 issue。
- 默认接收端是否忽略 `customData` 里的元数据是根据 MA 代码做的推断。
- 苹果 MFi 对发送端的政策没能从苹果官网直接取到原文（页面要 JS 渲染）。
- 生产镜像的 static-ffmpeg 有没有 libmp3lame 没有核对。
- 工作量倍数是粗估，没有拆到任务级别。
- 本次调研没有在任何真实设备上验证。
