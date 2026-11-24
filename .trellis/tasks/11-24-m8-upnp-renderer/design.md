# 设计：UPnP/DLNA 设备发现与单设备串流

## 组成

```
backend/internal/devices/upnp/        （新包；不依赖 catalog、curation）
  discovery.go     SSDP 搜索（每 30 秒，MX=2，发 3 次）与 NOTIFY 监听；按 max-age 过期；byebye 立即下线
  description.go   取描述文件（来源主机校验）、找 AVTransport/RenderingControl/ConnectionManager、读 SCPD 得到可选动作
  control.go       SOAP：SetAVTransportURI、Play、Pause（没有时 Stop）、Stop、GetTransportInfo、GetPositionInfo、Get/SetVolume、GetProtocolInfo
  didl.go          DIDL-Lite 元数据（当前曲目标题、艺人、专辑、封面地址），非 ASCII 转字符引用
  registry.go      实例级设备表：UDN、名称、型号、地址、能力、在线、占用者
  output.go        服务端托管的输出端：对每个被选中的设备，以占用者的播放区身份 ClaimOutput，
                   订阅输出事件，把 stream/intent/volume 翻译成 SOAP；轮询状态并 ReportOutput
  egress.go        只允许局域网私有地址（RFC 1918、链路本地、ULA 不做）的 HTTP 客户端，拒绝回环与公网
backend/internal/zones/
  新的设备种类 upnp；能力由适配器声明（stream_formats、max_sample_rate、max_bit_depth、volume）
  设备流令牌：开流时为 upnp 输出生成随机令牌，存内存（流结束、换流、输出释放即失效）
  免 Cookie 的流路由：GET/HEAD /upnp/streams/{token}
backend/cmd/roomusic-fake-renderer/    假渲染器
```

装配在 `app.go`；开关存实例设置（与自动整理开关同类的设置表或已有的全局设置机制，以现有做法为准）。

## 设备与播放区的映射

- 实例级注册表保存发现到的设备（UDN 为键）。可见性：共享开关开时所有成员可见；关时只有 `upnp_device_grants`（设备 UDN × 用户）里分配到的成员与管理员可见。
  每个成员的快照里，每台对他可见的在线 UPnP 设备对应一行该成员自己的 `playback_devices`（`kind='upnp'`，`external_id=UDN`），
  首次在快照里出现时创建；对应的播放区归该成员，队列与设置各自独立。
- 占用：注册表记录每台设备当前服务的播放区。成员选择设备为输出（现有“控制目标/转移”流程最终落到这台设备的播放区开始播放）时：
  - 空闲 → 适配器以该播放区认领输出；
  - 被别人占用 → 返回 `409 device_busy`（带占用者显示名）；确认接管时带 `takeover=true`，原播放区收到输出离线（按现有离线处理暂停），新播放区认领。
- 成员停用、会话全部吊销（`WithSessionRevoked`）时释放其占用。

## 播放

1. 输出事件 `zone.stream`（新 seq）→ 生成流令牌 → `Stop`（设备在播时）→ `SetAVTransportURI(<LAN 基础地址>/upnp/streams/<token>, DIDL)` → `Play(Speed=1)`。
2. `intent=paused` → `Pause`（没有时 `Stop`，继续时重新推流）；`intent=playing` → `Play`。
3. `volume` → `SetVolume(Master, 0–100 映射到设备范围)`；设备没有音量动作时能力声明 `volume: fixed`。
4. 轮询：播放中每 1 秒 `GetPositionInfo` + `GetTransportInfo`，其余每 5 秒 `GetTransportInfo`；连续 3 次失败视为离线（释放输出，播放区按离线处理）。
5. 进度：`RelTime` 单调前进时用它；对连续流不可靠（停在 0 或回跳）时改用“进入 PLAYING 后的墙钟时间，暂停时冻结”。换算成 `stream_seconds` 后在进程内调用 `ReportOutput`（status，状态变化时立即，否则每秒一次）。
6. 设备报告 `PAUSED_PLAYBACK` 而播放区意图是播放 → 回报 status（playing=false），并对播放区下发暂停命令（来源记为设备）；`STOPPED` 且不是我们发起的 → 同样处理为暂停；
   设备从暂停变为 PLAYING → 播放区继续。
7. 流结束（播放区队列放完）→ 回报 `ended`。

## 流路由

- `GET /upnp/streams/{token}`：令牌对应（用户、播放区、seq）；不带 Cookie；响应头 `Content-Type`（audio/flac 或 audio/wav）、`transferMode.dlna.org: Streaming`、
  `contentFeatures.dlna.org: DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000`、`Accept-Ranges: none`、chunked。
- `HEAD`：同样的头，不取流。
- 同一令牌第二次 GET：视为重连，按当前播放头对该播放区开续流（复用现有 `stream_lost` 续流路径），令牌转到新 seq；旧连接关闭。
- 未知或失效令牌：404；同一来源地址的 404 用 `identity.SourceThrottle` 限速。
- 这条路由不做来源校验（GET），只接受局域网私有地址或回环的对端（`RemoteAddr`）。

## 格式与能力

- 启动时 `GetProtocolInfo`：Sink 含 `audio/flac`（或 `audio/x-flac`）→ `stream_formats=[flac]`；否则含 `audio/wav`/`audio/x-wav`/`audio/L16` → `[wav]`；都没有 → 仍试 FLAC 并在设备行标“格式未声明”。
- 上限：默认 48000 / 24，设备菜单可改（存设备行的设置，进入能力声明）。

## 地址

- `ROOMUSIC_LAN_URL`：带主机与端口的 http 地址（不能是 https），设了时所有设备都用它；
- 没设时：用 goupnp 记录的、收到该设备响应的本机地址 + HTTP 监听端口；
- 手动添加的设备在没设 `ROOMUSIC_LAN_URL` 时，用连接该设备时的本机地址（`net.Dial` 的 LocalAddr）；bridge 下这是容器地址，文档要求设变量。

## 出站限制

- 描述文件：主机必须等于 SSDP 响应的来源 IP（或管理员手动添加的主机）；控制、SCPD 地址按描述文件的 URLBase 或描述地址解析，主机必须相同。
- 只允许 10/8、172.16/12、192.168/16、169.254/16；拒绝回环、公网、组播；端口不限。重定向不跟随。
- 响应体大小上限（描述 256 KiB、SOAP 64 KiB）与超时（5 秒）。

## 部署

- `compose.prod.yaml` 默认改为 host 网络：应用 `network_mode: host`，`ROOMUSIC_HTTP_ADDR` 默认 `0.0.0.0:${ROOMUSIC_HTTP_PORT:-8090}`；
  PostgreSQL 保留自己的网络，端口发布为 `127.0.0.1:${ROOMUSIC_DB_PORT:-<选一个不常用的默认值>}:5432`，应用的数据库地址改为 `127.0.0.1:<该端口>`。
  其余加固（`read_only`、`cap_drop`）保留；确认 host 网络下 SSDP 收发不需要额外 capability。
- 新增 `deploy/production/compose.bridge.yaml` 覆盖文件恢复原来的 bridge 网络（手动添加设备，需设 `ROOMUSIC_LAN_URL`）。
- `scripts/check-production-compose.sh` 覆盖两种组合；`deploy/production/README.md` 写升级说明（网络变化、端口冲突检查、同机多实例端口）；`.env.prod.example` 增加端口与 `ROOMUSIC_LAN_URL`。
- 登录限速依赖 TCP 对端地址：host 网络下看到的是真实来源，与原来一致，验收时核对。

## 前端

- 设备列表：`kind=upnp` 用音箱图标与“局域网设备”副标题；占用中显示“<成员> 正在使用”，点选时确认接管。
- 设备菜单：采样率上限（44.1/48/96/192 kHz）；音量固定时隐藏音量滑块并显示“设备音量”。
- 设置 › 播放（或现有的设备相关设置页）：管理员开关、共享开关与按设备分配账号（关闭共享时显示）、手动添加（地址输入、添加结果）、已发现设备列表（名称、型号、地址、在线）。

## 假渲染器

`cmd/roomusic-fake-renderer -name 客厅 -addr 0.0.0.0:0`：SSDP 公告与响应；描述文件与三个服务的 SCPD；SOAP 动作维护状态机；
`Play` 后用 HTTP 拉流、按 FLAC/WAV 头里的采样率估算实时速度读取并丢弃（只解析到能算时长为止，否则按字节速率估算），`RelTime` 可设为“正常”或“停在 0”两种行为；
暴露 `/_fake/state` 查看收到的 URI、状态、音量。测试里以进程内方式复用。

## 兼容与回退

- 开关关闭时没有新 goroutine 与端口；迁移只放宽 `kind` 约束与新增设备设置列，down 迁移删除 `upnp` 行后恢复约束。
- 回退：关开关即停止；代码可整体回退。
