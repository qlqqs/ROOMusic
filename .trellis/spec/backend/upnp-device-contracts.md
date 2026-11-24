# 局域网 UPnP 渲染器合同

## 设置与持久化

`app.New` 为每个实例装配一个 `upnp.Service` 与一个 `Registry`。迁移 `0069_upnp_devices` 采用实例设置的单行表模式。

| 表 | 内容 |
|---|---|
| `upnp_settings` | `id=1`，`enabled` 默认 false，`shared` 默认 true |
| `upnp_manual_devices` | UDN 主键、描述地址、最后登记的名称、型号和能力 |
| `upnp_device_grants` | 主键为 `(udn,user_id)`，包括自动发现设备的分配，账号外键指向 `users(id)` |

运行中开启发现或重启已开启的实例时读取手动设备，创建 UDP 套接字和发现工作协程。关闭时取消探测，关闭套接字并等待发现工作退出。初始关闭不启动设备工作协程，不发 SSDP 或访问设备。关闭及重启保留数据库里的设置、手动设备和分配。

`ROOMUSIC_LAN_URL` 可留空。配置时须为含主机与显式端口的 HTTP 来源，例如 `http://192.168.1.10:8090`。端口为 1–65535，可带单个末尾斜杠，不接受路径、用户信息、查询串或片段。读取值在 `config.Server.LANURL`。设备对象的 `LocalAddress` 保存单播搜索响应所在网卡地址，手动探测与组播公告取描述 HTTP 连接的本机地址。

## 发现与注册表

- 在有局域网 IPv4 的活动组播网卡上，启动时及每 30 秒向 `239.255.255.250:1900` 发送三次 MediaRenderer M-SEARCH，MX 为 2。另向该网卡自己的私网地址发送一次单播搜索，用于组播无法回送时的同机渲染器。
- 单播响应与 `ssdp:alive` / `ssdp:update` 带来的 LOCATION 通过出站校验后取描述，核对描述内 MediaRenderer 的 UDN 与 USN。有效公告按 `Cache-Control: max-age` 续期；到期后读取快照即为离线。同一来源的 `ssdp:byebye` 立即下线，旧探测结果不得使设备重新上线。
- 注册表以 UDN 合并行，保存名称、型号、地址、能力和在线状态。已登记的手动地址每 30 秒重新探测，探测失败撤销手动在线状态；同时被 SSDP 发现的设备仍按公告有效期判断。删除手动登记后，仍有有效公告的设备可以继续出现在发现列表。
- `Registry.List()`、`Registry.Lookup(udn)` 返回实例快照。`Service.Visible(ctx, actor)` 返回在线且对该账号可见的行。共享开启时所有成员可见，关闭时管理员可见全部，其余成员只见分配给自己的 UDN。

## 出站地址与控制

`DiscoverDescription(ctx, location, source)` 只接受主机为 UDP 来源 IPv4 字面地址的 HTTP URL。手动输入裸 IP 时向该 IP 的 UDP 1900 端口单播搜索。手动输入 HTTP 描述地址时，`ManualDescription` 可解析管理员给定的主机名。每次探测仅选用 10/8、172.16/12、192.168/16 或 169.254/16 的 IPv4，固定 TCP 目的 IP，并保留原 HTTP Host。

按 URLBase 或描述 URL 解析 SCPD 和控制地址。URLBase 及解析出的地址都必须与描述 URL 同主机，端口可以不同。HTTP 客户端直连，不使用环境代理，不跟随重定向；单请求限时 5 秒，DNS 解析限时 5 秒，描述与 SCPD 各限 256 KiB，SOAP 限 64 KiB。

| 服务 | 动作及语义 |
|---|---|
| AVTransport | SetAVTransportURI、Play（Speed=1）、Pause、Stop、GetTransportInfo、GetPositionInfo，InstanceID=0。SCPD 缺少 Pause 时调用 Stop。GetPositionInfo 返回 RelTime 时长，设备报告 NOT_IMPLEMENTED 时返回零 |
| RenderingControl | GetVolume、SetVolume 使用 Master 通道，在 0–100 与 SCPD 声明的设备范围、步长之间换算。SetVolume 能力决定音量类型 `number` 或 `fixed` |
| ConnectionManager | GetProtocolInfo 读取 Sink。可交付 FLAC 时优先 FLAC，其次 WAV，未声明可用格式时尝试 FLAC 并设 `format_declared=false`。默认上限 48000 Hz、24 位 |

设置 URI 时传入 `BuildDIDL(TrackMetadata{Title, Artist, Album, ArtURI, URI, MIMEType})` 生成的 DIDL-Lite。资源的 DLNA 标志对应连续转码流；非 ASCII 元数据以 XML 数字字符引用编码。

## 管理员接口

统一前缀 `/api/v1/admin/upnp`。读取要求管理员会话，写入还需通过实例来源校验。成功响应及错误信封见 `backend/internal/app/testdata/api-samples/upnp/`。

| 方法与路径 | 请求 | 成功响应 |
|---|---|---|
| GET /settings | 无 | 200，`{enabled,shared}` |
| PUT /settings | 至少一个布尔字段 `enabled`、`shared` | 200，完整设置 |
| GET /devices | 无 | 200，`{items:[设备行]}`，按 UDN 排序 |
| POST /devices | `{"address":"http://192.168.1.25:8080/description.xml"}` 或裸 IP | 200，手动设备行。相同 UDN 更新登记 |
| DELETE /devices/{udn} | 无 | 204，同时清除该 UDN 的分配 |
| PUT /devices/{udn}/grants | `{"user_ids":["账号 UUID"]}` | 200，`{user_ids:[...]}`。事务内全量替换、去重排序，空数组清空 |

设备行含 `udn,name,model,address,capabilities,online,manual,user_ids`。其中 `address` 为描述 URL，`capabilities` 含 `pause,volume,stream_formats,format_declared,max_sample_rate,max_bit_depth`。

## 错误与边界样例

| 情形 | 结果 |
|---|---|
| 初始设置、发现列表为空 | `{"enabled":false,"shared":true}`、`{"items":[]}` |
| 发现来源为 192.168.1.25，LOCATION 的主机也是 192.168.1.25 | 接受描述，按同主机规则检查后续地址 |
| 来源为 192.168.1.25，LOCATION 指向 192.168.1.26 | 丢弃公告，即使两端都属于私网 |
| 描述 URL 合法，SCPD、URLBase 或控制指向另一主机 | 拒绝设备；手动添加返回 400 `invalid_address` |
| 关闭状态添加设备 | 409 `upnp_disabled` |
| 非法设备地址或被拒的地址范围 | 400 `invalid_address` |
| 手动设备未响应或响应无法解析 | 422 `device_unreachable` |
| 未知设备授权或移除未登记的设备 | 404 `not_found` |
| 非法 JSON、未知字段 | 400 `invalid_json` |
| 设置缺少布尔字段、分配缺少数组或引用不存在的账号 | 400 `invalid_input` |
| 发现监听初始化失败 | 503 `discovery_unavailable` |

## 验证接点

`fakerenderer.New(Options)` 提供可挂载的 HTTP handler 与完整的 `Serve`（SSDP + HTTP）。使用合成 WAV 或 FLAC，经发现、SetAVTransportURI 和 Play 后，核对 GetPositionInfo 与 `/_fake/state` 的 URI、状态和拉流字节。`-rel-time zero` 模拟拉流正常但位置停在零。权限和迁移检查需要独立 PostgreSQL 数据；可见性断言覆盖共享开启、关闭、分配、撤销及管理员。

## 渲染器取流

托管输出调用 `zones.Service.UPnPStream(userID, sessionID, seq)` 取得 `Path` 与 `Delivered`。路径只由当前或接续渲染句柄持有，渲染器通过 `GET /upnp/streams/{token}` 免 Cookie 拉流。回复成功时为 200，使用实际音频 MIME（`audio/flac` 或 `audio/wav`），带以下响应头。

```http
Cache-Control: no-store
Accept-Ranges: none
transferMode.dlna.org: Streaming
contentFeatures.dlna.org: DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000
```

- GET 忽略 Range，使用 HTTP/1.1 分块传输。HEAD 返回相同的媒体头，保留首个 GET 的取流资格。
- 同一地址再 GET 时，服务在播放区锁内记录当前播放位置，复用 `stream_lost` 的恢复流程并将令牌转至新 seq，结束旧 HTTP 连接。换曲重建、接续流被替换、释放输出或结束播放会销毁相应句柄的令牌。带会话 Cookie 的普通播放区流地址不能获取托管输出的音频。
- `Delivered` 表示该 seq 的所有音频字节已写入 HTTP 响应并读到 EOF。设备可在播放完自身缓冲后才变为 STOPPED。渲染完成后，`DurationSeconds` 给出实际输出帧数对应的流长，含剪静音和过渡效果。
- DIDL-Lite 有封面时使用 `GET/HEAD /upnp/streams/{token}/artwork`。封面绑定到签发令牌时的曲目发行封面，两种方法都保留音频取流资格。封面与音频使用相同的令牌有效期、TCP 对端校验和令牌失败配额。
- 按 TCP 对端接受 IPv4 私网、IPv4 链路本地及回环地址。缺少令牌、令牌失效、来自其他地址均返回 404。使用独立的 `identity.SourceThrottle` 按来源计数，一分钟内第 30 次失败返回 429 和 `Retry-After`，暂停该来源一分钟。日志记录路由模板。

## 托管输出与占用

- `upnp.Service.Acquire` 在实例内串行检查设备上线、成员启用、有效会话及当前共享／分配。以 UDN 记录物理占用。相同成员重复激活同一设备共用输出会话，占用者显示名取自账号的 `username`。占用冲突使用统一的 409 错误信封，`error.code` 为 `device_busy`，`error.message` 包含占用者显示名，顶层携带 `request_id`。其他成员需要确认接管；接管等旧输出停止并完成 `ReleaseOutput` 后才认领新播放区。
- 关闭发现时先停止全部托管输出。关闭共享、变更分配、删除手动设备时重新核对占用者权限。成员停用或失去最后一条有效会话时，`WithSessionRevoked` 释放该成员的所有占用。队列转移后释放仍处于暂停且队列为空的原设备。
- 每个占用者通过进程内的 `ClaimOutput`、`SubscribeOutput` 接收所属设备的音量和播放区事件。流地址优先取 `ROOMUSIC_LAN_URL`，否则取发现设备时的本机 IPv4 和实际 HTTP 监听端口。推送新流前查询播放状态，在播放中先 Stop，再设置 URI 和 DIDL、Play。协议选取音频、位深、采样率的规则见上文。新登记的成员设备沿用探测到的 Master 音量。可调设备未提供有效读数时登记 0.5 供显示，首次由成员设置音量或静音后才向设备发送 SetVolume。成员的设备行持久保存该状态，重新认领或重启后继续生效。后续使用成员保存的音量，从 0–1 映射至 0–100，静音取 0。
- 播放中每秒读取 TransportInfo 与 PositionInfo，其余状态每五秒读取 TransportInfo。RelTime 可用时采用设备位置，停滞或回跳后，从既有播放头改按墙钟累计，暂停时冻结。通过 `ReportOutput` 回报状态与流内位置。连续三次控制或轮询失败导致设备下线、释放输出并暂停播放区。
- 设备端暂停、继续、提前停止分别产生播放区 pause、play、pause 命令，事件带 `source:"device"`。缺少 Pause 动作时用 Stop，继续时从暂停位置重新推送。正常暂停保留托管流，设备端长时间暂停后仍可通过原 URI 恢复。设备端跳转与上下首未映射。
- 播放头到达已交付流的末尾且设备停止时，有接续流就推送其 URI 和 DIDL，再以 playing 回报晋升为当前流；队列放完则回报 ended。设备重连复用 URI 时只更新 seq 和流内时间起点。

## Google Cast

与 UPnP 共用设置、可见性、分配、占用与托管输出规则，以下只列不同之处。

- 设置：`upnp_settings` 增加 `upnp_enabled`、`cast_enabled`（默认 true），都只在 `enabled` 为 true 时起作用；关闭某个协议时停止该协议的发现并释放其托管输出。
  `GET/PUT /api/v1/admin/upnp/settings` 返回 `{enabled, shared, upnp_enabled, cast_enabled}`。迁移 `0075_cast_devices`：手动设备表加 `kind`，新表 `cast_device_formats(udn, codec, max_sample_rate)`。
- 设备键 `cast:<TXT id>`，与 UDN 共用分配表。设备行有 `kind`（`upnp`/`cast`）与 `format`（Cast 为 `{codec, max_sample_rate}`，UPnP 为 `null`）。
- 发现：`internal/devices/mdns` 每 30 秒在局域网 IPv4 网卡上查询 `_googlecast._tcp`（PTR→SRV/TXT/A，按记录 TTL 过期），不绑定特权端口；地址只接受局域网 IPv4（`internal/devices/lan` 与 UPnP 共用）。手动添加 `POST …/devices {"kind":"cast","address":"IP[:端口]"}`，默认端口 8009，注册表每 30 秒重探。
- 协议：`internal/devices/cast/castv2` 自写最小 CASTV2（TLS 8009、长度前缀的 CastMessage，connection/heartbeat/receiver/media 命名空间），启动默认媒体接收端 `CC1AD845`；每 5 秒心跳与 `GET_STATUS`，连续无响应视为离线并释放。
  接收端应用或会话被换掉视为被其他发送端接管，释放输出并暂停播放区（`source:"device"`）。`controlType=fixed` 按固定音量处理。
- 取流：沿用托管输出的令牌拉流与封面路径；每首一个流（渲染 `SingleTrack`），收到 `IDLE/FINISHED` 且有接续流时 LOAD 下一首，曲间不保证无缝。
- 格式：`PUT /api/v1/admin/upnp/devices/{udn}/format {codec, max_sample_rate}`，`codec` 为 `mp3`（默认，libmp3lame CBR 320 kb/s）、`aac`、`flac`；采样率上限 44100 或 48000（默认），FLAC 另可 96000；其他组合或非 Cast 设备 `400 invalid_input`。设置按物理设备保存，对所有可用成员生效，下一首起生效。
- 开发与测试：`cmd/roomusic-fake-cast` 假接收端（自签 TLS、mDNS 公告、CASTV2 子集、拉流计字节、`/_fake/state`）。
