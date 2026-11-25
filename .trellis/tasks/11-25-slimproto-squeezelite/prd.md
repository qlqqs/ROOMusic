# SlimProto 服务端接入 Squeezelite 设备（单台）

状态：规划中。协议事实见 `research/slimproto-protocol.md`（读 Squeezelite 与 Lyrion Music Server 源码整理，未复制代码）。父任务 `11-25-lan-hires-multiroom`；多台同步在 `11-25-slimproto-sync-groups`，本任务只做单台。

## 目标

ROOMusic 内置一个 SlimProto 服务端。局域网里的 Squeezelite 类播放器（piCorePlayer、moOde、带 Squeezelite 的播放器与解码器）能自动找到 ROOMusic 并连上来，作为托管局域网输出出现在设备列表里。成员选中后，播放区音频以无损高码率交付，经过 ROOMusic 的音效处理链，跨曲无缝。

## 背景与现状

- 托管局域网输出已有三种（UPnP、Cast、AirPlay），共用设置、可见性、分配、占用与托管输出规则，见 `.trellis/spec/backend/upnp-device-contracts.md`。播放区与输出端合同见 `playback-zone-contracts.md` §1、§5、§6、§10，以及 `playback-zone-service-contracts.md` §9。
- 设备的采样率上限接口 `PUT /devices/{id}/sample-rate` 目前只接受 44100/48000/96000/192000（`backend/internal/zones/managed_devices.go:230`）。播放区的 SRC 设置已支持到 768000（`settings_sample_rate.go:41`）。
- DSD 来源在渲染时先转成 PCM（`dsd_to_pcm`，最高 DXD 352.8 kHz，见 `dsd-contracts.md`）。能力字段 `dsd: none|dop|native` 目前只保存、不生效。
- 生产 Compose 用 host 网络（M8 W8），SlimProto 的 UDP/TCP 3483 不需要另加端口映射。

## 决定（主代理按“优先使用和操作舒适”取默认，随交付请 qlqq 审阅）

| 编号 | 决定 | 理由 |
| --- | --- | --- |
| S1 | 局域网设备设置增加 `slimproto_enabled`，默认 true，只在总开关 `enabled` 为 true 时起作用；关闭时停止监听，释放 SlimProto 托管输出 | 与 Cast、AirPlay 的子开关一致；总开关默认关，未启用的实例行为不变 |
| S2 | 设备由播放器主动连上来，不提供手动添加。播放器被配置成连别的服务器时，由用户在播放器上改服务器地址，文档写明 | SlimProto 的连接方向是播放器连服务器，服务端无从“添加” |
| S3 | 设备键 `slim:<MAC>`，与其他局域网设备共用分配表与占用规则 | MAC 是 HELO 里稳定的设备身份 |
| S4 | 交付格式只用 FLAC 长流（格式码 `f`，采样率由流自描述）。采样率上限默认取播放器声明的 `MaxSampleRate`，管理员可在设备音质设置里调低。位深按现有协商规则，最高 24 位。能力串没有 `flc` 的播放器显示为不支持 | Squeezelite 的常见构建都带 FLAC；PCM 必须是不带头的裸流，ROOMusic 现有长流没有这种格式，为极少数构建再做一种不值得 |
| S5 | 音质设置走管理员接口 `PUT /api/v1/admin/upnp/devices/{udn}/format`（与 Cast、AirPlay 一致）：`{"codec":"flac","max_sample_rate","volume":"number"\|"fixed"}`。`max_sample_rate` 取 44100/48000/88200/96000/176400/192000/352800/384000/705600/768000 中不超过播放器声明的值（封顶 768000；FLAC 块大小固定 4096 帧，见 `research/ffmpeg-flac-rates.md`）。成员侧的 `PUT /devices/{id}/sample-rate` 仍只对 UPnP 生效 | 设置按物理设备保存，对所有可用成员生效，与现有两种协议相同 |
| S6 | DSD 来源仍转成 PCM 交付（最高 DXD，受设备上限约束），本任务不做 DoP/原生 DSD 直通 | 直通会绕开音效处理链，需要另一条渲染路径，单列为后续项 |
| S7 | 跨曲：同一格式的曲目放在一条长流里连续交付（服务端已做无缝与交叉渐变）；格式变化时用接续流，在播放器报告“可接下一首”后下发 | 与现有长流和接续流合同一致 |
| S8 | 音量：默认 `number`，用 `audg` 调播放器音量（播放器用 `-V` 接了硬件混音器时，调的是硬件音量）。管理员把设备设为 `fixed` 时，增益命令的 adjust 置 0，播放器按原电平输出，成员侧显示固定音量 | 能力串没有“固定音量”键，只能由服务器决定（调研 §8）；接功放、用解码器音量的用户需要固定输出 |
| S9 | 只接受来自局域网 IPv4（私网、链路本地、回环）的 SlimProto 连接与发现请求；取流令牌与现有托管取流相同（免 Cookie、按 TCP 对端校验） | 协议本身没有认证，沿用 `internal/devices/lan` 的地址规则 |
| S10 | 3483 已被占用（例如同机运行 Lyrion Music Server）时，SlimProto 标记为不可用，设置读取里给出原因码 `port_in_use`，其他协议照常。不提供备用端口 | 发现应答无法告知别的端口，备用端口只能靠用户在每台播放器上配 `-s`，不值得为此加配置 |
| S11 | 不做设备端暂停与继续的映射 | Squeezelite 没有这类上行报文（只有 lirc 构建发红外码），调研 §11 |

## 要求

- R1 发现：应答播放器在局域网上发出的服务器发现请求。
- R2 连接：接受 HELO 登记设备，记录型号、名称、MAC、能力（编解码、最高采样率、是否固定音量）；完成服务器侧必需的初始化命令。
- R3 设备行：设备出现在局域网设备列表（管理员）与成员可见设备中，`kind:"slimproto"`。离线判定、可见性、分配、占用、接管与其他种类一致。
- R4 播放：认领后按 S4/S7 交付长流，支持播放、暂停、继续、停止、跳转（重新开流）、下一首、上一首；位置来自播放器回报的已出声时长（已扣除声卡缓冲），进度与实际出声一致。
- R4a 取流：播放器按 `strm s` 里的请求串拉流，服务器回 HTTP/1.0 响应、不分块、关连接表示结束；暂停期间写操作被反压阻塞时不断开。
- R5 音量与静音按 S8 生效，成员的设备音量持久保存。
- R6 断线：播放器断开或超时无心跳时，设备离线，释放输出并暂停播放区（与其他托管设备一致）；播放器重连后可重新认领。
- R7 前端：局域网设备设置里有 SlimProto 开关与 `port_in_use` 提示；设备列表显示 SlimProto 设备（型号、固件、最高采样率）；设备音质设置可选采样率上限与固定音量。按图标优先的界面约定。
- R8a 插在 NAS 上的 USB 解码器：文档给出 Squeezelite 边车容器的 Compose 示例（映射 `/dev/snd`、host 网络、`-s 127.0.0.1`），让没有网络功能的解码器也作为 SlimProto 设备使用；测试脚本验证示例能启动并被发现（用 `-o -` 代替声卡）。
- R8 文档：README 功能列表、`docs/install-nas.md` 或部署说明写明端口 3483 与播放器的服务器地址设置；spec 增加 SlimProto 一节。

## 验收

- A1 在 Docker 测试网络里运行一台真实 Squeezelite（`-o -` 接按名义采样率读取的计时程序，`-r` 声明采样率），能被发现、出现在设备列表、被成员认领并播放；播放器回报的位置与计时程序读出的时长相差不超过 1 秒。
- A2 96 kHz/24 bit 与 192 kHz/24 bit 的测试曲目以原采样率 FLAC 交付到声明支持的播放器；设备上限调低到 48 kHz 后，下一条流按 48 kHz 交付。
- A3 同格式的连续两首无缝（一条长流）；采样率不同的两首按接续流切换，播放器不断开。
- A4 暂停、继续、跳转、下一首、音量、静音在播放器侧生效（以播放器回报的状态与增益为准）。
- A5 杀掉播放器进程后，设备在规定时间内离线、播放区暂停；重启播放器后可再次播放。
- A6 3483 被占用时设置接口给出 `port_in_use`，其他协议不受影响。
- A7 SlimProto 开关关闭或局域网设备总开关关闭时，不监听 3483，实例行为不变。
- A8 受影响包的单元与集成测试通过；前端类型检查、单测与相关截图核对通过。

## 不做

- 多台同步编组（`11-25-slimproto-sync-groups`）。
- DoP/原生 DSD 直通（S6），列入后续候选。
- Lyrion Music Server 的 CLI、JSON-RPC 与遥控应用接口；带屏 Squeezebox 的显示命令。
- 手动添加设备、跨网段发现、备用端口。
- 设备端暂停与继续的映射（S11）。
- PCM 裸流交付（S4）。

## 规划决定记录

路线：按 qlqq 的长期要求“规划完直接实现，能自己定的自己定”（记忆 `plan-then-implement-autonomously`），S1–S11 由主代理取推荐值，随交付请 qlqq 审阅。
